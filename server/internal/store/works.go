package store

import (
	"database/sql"
	"errors"
	"strings"
)

// WorksFilter 是后台影视库的筛选条件；Status 取 ""|"visible"|"hidden"。
type WorksFilter struct {
	Q, CategorySlug, Status string
	Page, Size              int
}

type WorkRow struct {
	ID            int64
	Title         string
	CategoryID    int64
	CategoryName  string
	Visible       bool
	Pinned        bool
	SortIndex     int
	EpisodeCount  int
	MissingCount  int
	UpdatedAt     string
}

type WorksPage struct {
	Items        []WorkRow
	Page, Size   int
	Pages, Total int
}

type FileRow struct {
	FileID       int64
	WorkID       int64
	FileName     string
	SourcePath   string
	SizeBytes    int64
	EpisodeNo    int
	Present      bool
	MissingSince string
}

type WorkDetail struct {
	Work  WorkRow
	Files []FileRow
}

type WorkPatch struct {
	Title      *string
	CategoryID *int64
	Visible    *bool
	Pinned     *bool
}

type rowScanner interface {
	Scan(...any) error
}

const workColumns = `SELECT w.id, w.title, w.category_id, COALESCE(c.name, ''), w.visible, w.pinned,
	w.sort_index,
	(SELECT COUNT(*) FROM work_files f WHERE f.work_id = w.id AND f.present = TRUE),
	(SELECT COUNT(*) FROM work_files f WHERE f.work_id = w.id AND f.present = FALSE),
	w.updated_at
	FROM works w LEFT JOIN categories c ON c.id = w.category_id`

func scanWork(sc rowScanner) (WorkRow, error) {
	var w WorkRow
	err := sc.Scan(&w.ID, &w.Title, &w.CategoryID, &w.CategoryName, &w.Visible, &w.Pinned,
		&w.SortIndex, &w.EpisodeCount, &w.MissingCount, &w.UpdatedAt)
	return w, err
}

// ListWorks 分页返回后台作品列表（含隐藏作品）。
func (s *Store) ListWorks(f WorksFilter) (WorksPage, error) {
	page, size := pageArgs(f.Page, f.Size)
	where := []string{"TRUE"}
	var args []any
	if q := strings.TrimSpace(f.Q); q != "" {
		where = append(where, `w.title LIKE `+bind(&args, likePattern(q))+` ESCAPE '\'`)
	}
	if slug := strings.TrimSpace(f.CategorySlug); slug != "" {
		where = append(where, "c.slug = "+bind(&args, slug))
	}
	switch f.Status {
	case "visible":
		where = append(where, "w.visible = TRUE")
	case "hidden":
		where = append(where, "w.visible = FALSE")
	}
	clause := strings.Join(where, " AND ")

	var total int
	countSQL := `SELECT COUNT(*) FROM works w LEFT JOIN categories c ON c.id = w.category_id WHERE ` + clause
	if err := s.db.QueryRow(countSQL, args...).Scan(&total); err != nil {
		return WorksPage{}, err
	}

	query := workColumns + ` WHERE ` + clause +
		` ORDER BY w.updated_at DESC, w.id DESC LIMIT ` + bind(&args, size) +
		` OFFSET ` + bind(&args, (page-1)*size)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return WorksPage{}, err
	}
	defer rows.Close()

	items := []WorkRow{}
	for rows.Next() {
		w, err := scanWork(rows)
		if err != nil {
			return WorksPage{}, err
		}
		items = append(items, w)
	}
	if err := rows.Err(); err != nil {
		return WorksPage{}, err
	}
	return WorksPage{Items: items, Page: page, Size: size, Pages: pageCount(total, size), Total: total}, nil
}

// GetWork 返回单个作品及其全部剧集文件；不存在时 ErrNotFound。
func (s *Store) GetWork(id int64) (WorkDetail, error) {
	w, err := scanWork(s.db.QueryRow(workColumns+` WHERE w.id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkDetail{}, ErrNotFound
	}
	if err != nil {
		return WorkDetail{}, err
	}

	rows, err := s.db.Query(`SELECT file_id, work_id, source_path, file_name, size_bytes, episode_no, present, missing_since
		FROM work_files WHERE work_id = $1 ORDER BY episode_no, id`, id)
	if err != nil {
		return WorkDetail{}, err
	}
	defer rows.Close()

	files := []FileRow{}
	for rows.Next() {
		var f FileRow
		if err := rows.Scan(&f.FileID, &f.WorkID, &f.SourcePath, &f.FileName, &f.SizeBytes,
			&f.EpisodeNo, &f.Present, &f.MissingSince); err != nil {
			return WorkDetail{}, err
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return WorkDetail{}, err
	}
	return WorkDetail{Work: w, Files: files}, nil
}

// UpdateWork 只写入 patch 中非 nil 的运营字段。
func (s *Store) UpdateWork(id int64, p WorkPatch, now string) error {
	return s.inTx(func(tx *sql.Tx) error {
		sets := []string{}
		var args []any
		if p.Title != nil {
			sets = append(sets, "title = "+bind(&args, *p.Title))
		}
		if p.CategoryID != nil {
			sets = append(sets, "category_id = "+bind(&args, *p.CategoryID))
		}
		if p.Visible != nil {
			sets = append(sets, "visible = "+bind(&args, *p.Visible))
		}
		if p.Pinned != nil {
			sets = append(sets, "pinned = "+bind(&args, *p.Pinned))
		}
		sets = append(sets, "updated_at = "+bind(&args, now))
		idRef := bind(&args, id)

		res, err := tx.Exec(`UPDATE works SET `+strings.Join(sets, ", ")+` WHERE id = `+idRef, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// SetVisibility 批量上/下架。
func (s *Store) SetVisibility(ids []int64, visible bool, now string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.inTx(func(tx *sql.Tx) error {
		var args []any
		vis := bind(&args, visible)
		at := bind(&args, now)
		in := placeholders(len(args)+1, len(ids))
		for _, id := range ids {
			args = append(args, id)
		}
		_, err := tx.Exec(`UPDATE works SET visible = `+vis+`, updated_at = `+at+` WHERE id IN (`+in+`)`, args...)
		return err
	})
}

// ReorderEpisodes 按给定顺序把作品集号重写为连续 1..n。
func (s *Store) ReorderEpisodes(workID int64, fileIDs []int64, now string) error {
	return s.inTx(func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM works WHERE id = $1`, workID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrNotFound
		}
		if err := renumberEpisodes(tx, workID, fileIDs); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE works SET updated_at = $1 WHERE id = $2`, now, workID)
		return err
	})
}

// AssignFiles 把文件移入目标作品，并重排目标作品集号为 1..n。
func (s *Store) AssignFiles(workID int64, fileIDs []int64, now string) error {
	return s.inTx(func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM works WHERE id = $1`, workID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrNotFound
		}

		affected := map[int64]bool{}
		for _, id := range fileIDs {
			var oldWork int64
			err := tx.QueryRow(`SELECT work_id FROM work_files WHERE id = $1`, id).Scan(&oldWork)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if oldWork == workID {
				continue
			}
			if _, err := tx.Exec(`UPDATE work_files SET work_id = $1 WHERE id = $2`, workID, id); err != nil {
				return err
			}
			affected[oldWork] = true
		}

		if err := renumberEpisodes(tx, workID, nil); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE works SET updated_at = $1 WHERE id = $2`, now, workID); err != nil {
			return err
		}
		for id := range affected {
			if _, err := tx.Exec(`UPDATE works SET updated_at = $1 WHERE id = $2`, now, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetHomeOrder 覆盖置顶集合，并按 orderedIDs 重写首页顺序（未列出者保持原相对顺序排在后面）。
// 首页编排不改变作品内容更新时间。
func (s *Store) SetHomeOrder(pinnedIDs, orderedIDs []int64, _ string) error {
	return s.inTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id FROM works ORDER BY sort_index, id`)
		if err != nil {
			return err
		}
		var all []int64
		known := map[int64]bool{}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			all = append(all, id)
			known[id] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		if _, err := tx.Exec(`UPDATE works SET pinned = FALSE`); err != nil {
			return err
		}
		if len(pinnedIDs) > 0 {
			var args []any
			in := placeholders(1, len(pinnedIDs))
			for _, id := range pinnedIDs {
				args = append(args, id)
			}
			if _, err := tx.Exec(`UPDATE works SET pinned = TRUE WHERE id IN (`+in+`)`, args...); err != nil {
				return err
			}
		}

		order := make([]int64, 0, len(all))
		used := map[int64]bool{}
		for _, id := range orderedIDs {
			if known[id] && !used[id] {
				used[id] = true
				order = append(order, id)
			}
		}
		for _, id := range all {
			if !used[id] {
				order = append(order, id)
			}
		}
		for i, id := range order {
			if _, err := tx.Exec(`UPDATE works SET sort_index = $1 WHERE id = $2`, i, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// renumberEpisodes 按 preferred 顺序重写连续集号；preferred 中不属于该作品或重复的 ID 忽略，
// 其余文件保持原有相对顺序排在后面。
func renumberEpisodes(tx *sql.Tx, workID int64, preferred []int64) error {
	rows, err := tx.Query(`SELECT id FROM work_files WHERE work_id = $1 ORDER BY episode_no, id`, workID)
	if err != nil {
		return err
	}
	var current []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		current = append(current, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	member := make(map[int64]bool, len(current))
	for _, id := range current {
		member[id] = true
	}
	used := make(map[int64]bool, len(current))
	order := make([]int64, 0, len(current))
	for _, id := range preferred {
		if member[id] && !used[id] {
			used[id] = true
			order = append(order, id)
		}
	}
	for _, id := range current {
		if !used[id] {
			order = append(order, id)
		}
	}
	for i, id := range order {
		if _, err := tx.Exec(`UPDATE work_files SET episode_no = $1 WHERE id = $2`, i+1, id); err != nil {
			return err
		}
	}
	return nil
}