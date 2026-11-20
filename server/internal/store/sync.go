package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

type ScannedFile struct {
	Path, Name string
	Size       int64
	Dir        bool
}

type MergeResult struct {
	Added, Restored, Missing int
}

type SyncRun struct {
	ID         int64
	StartedAt  string
	FinishedAt string
	Status     string
	Added      int
	Missing    int
	Failed     int
	Message    string
}

type SyncEvent struct {
	ID       int64
	RunID    int64
	Kind     string
	FilePath string
	Message  string
	At       string
}

type SyncState struct {
	Last   *SyncRun
	Runs   []SyncRun
	Events []SyncEvent
}

// episodeSuffixes 用于把同目录多集文件归入同一作品基础名。
var episodeSuffixes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)第?\d+集$`),
	regexp.MustCompile(`(?i)EP?\d+$`),
	regexp.MustCompile(`(?i)E\d+$`),
	regexp.MustCompile(`(?i)-\d+$`),
	regexp.MustCompile(`(?i)_\d+$`),
	regexp.MustCompile(`(?i)\.\d+$`),
}

// StartSyncRun 新建一次扫描记录，状态为 running。
func (s *Store) StartSyncRun(now string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`INSERT INTO sync_runs(started_at, status) VALUES($1, 'running') RETURNING id`, now).Scan(&id)
	return id, err
}

// ApplyScan 在单事务内按 source_path 合并扫描结果，并标记本轮缺失文件。
func (s *Store) ApplyScan(runID int64, files []ScannedFile, now string) (MergeResult, error) {
	var result MergeResult

	err := s.inTx(func(tx *sql.Tx) error {
		type existingFile struct {
			id        int64
			workID    int64
			episodeNo int
			present   bool
		}

		existing := map[string]*existingFile{}
		workBase := map[string]int64{}
		maxEpisode := map[int64]int{}

		rows, err := tx.Query(`SELECT id, work_id, source_path, episode_no, present FROM work_files ORDER BY id`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var e existingFile
			var sourcePath string
			if err := rows.Scan(&e.id, &e.workID, &sourcePath, &e.episodeNo, &e.present); err != nil {
				rows.Close()
				return err
			}
			existing[sourcePath] = &e
			key := baseKey(sourcePath)
			if _, ok := workBase[key]; !ok {
				workBase[key] = e.workID
			}
			if e.episodeNo > maxEpisode[e.workID] {
				maxEpisode[e.workID] = e.episodeNo
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		var maxSort int
		if err := tx.QueryRow(`SELECT COALESCE(MAX(sort_index), 0) FROM works`).Scan(&maxSort); err != nil {
			return err
		}

		seen := map[string]bool{}
		sorted := append([]ScannedFile(nil), files...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

		for _, f := range sorted {
			if f.Dir || f.Path == "" || seen[f.Path] {
				continue
			}
			seen[f.Path] = true

			if e, ok := existing[f.Path]; ok {
				if e.present {
					if _, err := tx.Exec(`UPDATE work_files SET last_seen_at = $1, size_bytes = $2 WHERE id = $3`,
						now, f.Size, e.id); err != nil {
						return err
					}
				} else {
					if _, err := tx.Exec(`UPDATE work_files SET present = TRUE, missing_since = '', last_seen_at = $1, size_bytes = $2 WHERE id = $3`,
						now, f.Size, e.id); err != nil {
						return err
					}
					if err := addSyncEvent(tx, runID, "restored", f.Path, now); err != nil {
						return err
					}
					e.present = true
					result.Restored++
				}
				continue
			}

			key := baseKey(f.Path)
			workID, ok := workBase[key]
			if !ok {
				workID, err = freeWorkFileID(tx, legacyFileID(f.Path), true)
				if err != nil {
					return err
				}
				maxSort++
				if _, err := tx.Exec(`INSERT INTO works(id, title, category_id, visible, pinned, sort_index, created_at, updated_at)
					VALUES($1, $2, 1, TRUE, FALSE, $3, $4, $5)`,
					workID, workTitle(f), maxSort, now, now); err != nil {
					if uniqueViolation(err) {
						return errors.New("作品标识重复")
					}
					return err
				}
				workBase[key] = workID
			}

			fileID, err := freeWorkFileID(tx, legacyFileID(f.Path), false)
			if err != nil {
				return err
			}
			episode := maxEpisode[workID] + 1
			maxEpisode[workID] = episode
			if _, err := tx.Exec(`INSERT INTO work_files(work_id, file_id, source_path, file_name, size_bytes, episode_no, present, missing_since, first_seen_at, last_seen_at)
				VALUES($1, $2, $3, $4, $5, $6, TRUE, '', $7, $8)`,
				workID, fileID, f.Path, scannedName(f), f.Size, episode, now, now); err != nil {
				if uniqueViolation(err) {
					return errors.New("作品标识重复")
				}
				return err
			}
			if err := addSyncEvent(tx, runID, "new", f.Path, now); err != nil {
				return err
			}
			result.Added++
		}

		var disappeared []string
		for p, e := range existing {
			if e.present && !seen[p] {
				disappeared = append(disappeared, p)
			}
		}
		sort.Strings(disappeared)
		for _, p := range disappeared {
			if _, err := tx.Exec(`UPDATE work_files SET present = FALSE, missing_since = $1 WHERE id = $2`,
				now, existing[p].id); err != nil {
				return err
			}
			if err := addSyncEvent(tx, runID, "missing", p, now); err != nil {
				return err
			}
			result.Missing++
		}
		return nil
	})
	if err != nil {
		return MergeResult{}, err
	}
	return result, nil
}

// FinishSyncRun 回填扫描结果。
func (s *Store) FinishSyncRun(id int64, status string, added, missing, failed int, msg string) error {
	_, err := s.db.Exec(`UPDATE sync_runs SET finished_at = $1, status = $2, added = $3, missing = $4, failed = $5, message = $6 WHERE id = $7`,
		time.Now().UTC().Format(time.RFC3339), status, added, missing, failed, msg, id)
	return err
}

// SyncState 返回最近一次同步、最近 20 次运行与最近 100 条事件。
func (s *Store) SyncState() (SyncState, error) {
	state := SyncState{Runs: []SyncRun{}, Events: []SyncEvent{}}

	rows, err := s.db.Query(`SELECT id, started_at, finished_at, status, added, missing, failed, message
		FROM sync_runs ORDER BY id DESC LIMIT 20`)
	if err != nil {
		return SyncState{}, err
	}
	for rows.Next() {
		var r SyncRun
		if err := rows.Scan(&r.ID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.Added, &r.Missing, &r.Failed, &r.Message); err != nil {
			rows.Close()
			return SyncState{}, err
		}
		state.Runs = append(state.Runs, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return SyncState{}, err
	}
	rows.Close()
	if len(state.Runs) > 0 {
		last := state.Runs[0]
		state.Last = &last
	}

	events, err := s.db.Query(`SELECT id, run_id, kind, file_path, message, at
		FROM sync_events ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return SyncState{}, err
	}
	defer events.Close()
	for events.Next() {
		var e SyncEvent
		if err := events.Scan(&e.ID, &e.RunID, &e.Kind, &e.FilePath, &e.Message, &e.At); err != nil {
			return SyncState{}, err
		}
		state.Events = append(state.Events, e)
	}
	if err := events.Err(); err != nil {
		return SyncState{}, err
	}
	return state, nil
}

func addSyncEvent(tx *sql.Tx, runID int64, kind, filePath, now string) error {
	_, err := tx.Exec(`INSERT INTO sync_events(run_id, kind, file_path, at) VALUES($1, $2, $3, $4)`,
		runID, kind, filePath, now)
	return err
}

// freeWorkFileID 返回一个未被本事务已写入行占用的 id。
func freeWorkFileID(tx *sql.Tx, start int64, checkWork bool) (int64, error) {
	id := start
	for i := 0; i < 100; i++ {
		var files int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM work_files WHERE file_id = $1`, id).Scan(&files); err != nil {
			return 0, err
		}
		if files > 0 {
			id++
			continue
		}
		if checkWork {
			var works int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM works WHERE id = $1`, id).Scan(&works); err != nil {
				return 0, err
			}
			if works > 0 {
				id++
				continue
			}
		}
		return id, nil
	}
	return 0, errors.New("作品标识重复")
}

// baseKey 是“同目录 + 基础名”的分组键。
func baseKey(sourcePath string) string {
	return path.Dir(sourcePath) + "\x00" + baseName(path.Base(sourcePath))
}

// baseName 去掉扩展名与尾部的集号标记。
func baseName(name string) string {
	stem := strings.TrimSuffix(name, path.Ext(name))
	base := stem
	for {
		next := strings.TrimRight(base, " ._-")
		for _, re := range episodeSuffixes {
			if loc := re.FindStringIndex(next); loc != nil && loc[1] == len(next) {
				next = strings.TrimRight(next[:loc[0]], " ._-")
				break
			}
		}
		if next == base || next == "" {
			break
		}
		base = next
	}
	return base
}

func scannedName(f ScannedFile) string {
	name := f.Name
	if name == "" {
		name = path.Base(f.Path)
	}
	return name
}

func workTitle(f ScannedFile) string {
	name := scannedName(f)
	title := strings.TrimSuffix(name, path.Ext(name))
	if title == "" {
		return name
	}
	return title
}

// legacyFileID 与旧版前台算法一致：sha256(source_path) 低 52 位 + 1。
func legacyFileID(sourcePath string) int64 {
	digest := sha256.Sum256([]byte(sourcePath))
	return int64(binary.BigEndian.Uint64(digest[:])&((1<<52)-1)) + 1
}