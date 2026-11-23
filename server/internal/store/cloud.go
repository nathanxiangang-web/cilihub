package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

const CloudPrivatePrefix = "cilihub-private-user-"
const CloudCollectorPrefix = "cilihub-collected-"

func IsCloudPrivatePath(path string) bool {
	return cloudPathHasPrefix(path, CloudPrivatePrefix)
}

func IsCloudCollectorPath(path string) bool {
	return cloudPathHasPrefix(path, CloudCollectorPrefix)
}

func cloudPathHasPrefix(path, prefix string) bool {
	for _, name := range strings.Split(path, "/") {
		if strings.HasPrefix(strings.ToLower(name), prefix) {
			return true
		}
	}
	return false
}

type CloudFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	PickCode string `json:"pick_code"`
}

type CloudTask struct {
	ID, Owner                                                  int64
	Hash, Magnet, Identity, Title, Key, Folder, State, Message string
	SharedFolder                                               bool
	OutputID                                                   string
	Progress                                                   *float64
	Files                                                      []CloudFile
}

var cloudSchema = []string{
	`CREATE TABLE IF NOT EXISTS cloud_tasks (
 id BIGSERIAL PRIMARY KEY, owner_id BIGINT NOT NULL CHECK (owner_id >= 0),
 info_hash TEXT NOT NULL, magnet TEXT NOT NULL, identity TEXT NOT NULL DEFAULT '',
 title TEXT NOT NULL DEFAULT '', folder_key TEXT NOT NULL UNIQUE, folder_id TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL DEFAULT 'queued', message TEXT NOT NULL DEFAULT '', progress DOUBLE PRECISION,
 files JSONB NOT NULL DEFAULT '[]', updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(owner_id,info_hash))`,
	`ALTER TABLE cloud_tasks ADD COLUMN IF NOT EXISTS shared_folder BOOLEAN NOT NULL DEFAULT FALSE`,
	`ALTER TABLE cloud_tasks ADD COLUMN IF NOT EXISTS output_id TEXT NOT NULL DEFAULT ''`,
	`CREATE UNIQUE INDEX IF NOT EXISTS cloud_tasks_active_identity ON cloud_tasks(owner_id,identity)
 WHERE identity <> '' AND state NOT IN ('failed','no_mp4')`,
	`CREATE INDEX IF NOT EXISTS cloud_tasks_pending ON cloud_tasks(updated_at,id)
 WHERE state IN ('queued','submitting','provider_queued','downloading','indexing','uncertain')`,
}

const cloudColumns = `id,owner_id,info_hash,magnet,identity,title,folder_key,folder_id,state,message,shared_folder,output_id,progress,files`

func scanCloud(row interface{ Scan(...any) error }) (CloudTask, error) {
	var task CloudTask
	var files []byte
	err := row.Scan(&task.ID, &task.Owner, &task.Hash, &task.Magnet, &task.Identity, &task.Title, &task.Key, &task.Folder, &task.State, &task.Message, &task.SharedFolder, &task.OutputID, &task.Progress, &files)
	if errors.Is(err, sql.ErrNoRows) {
		return task, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(files, &task.Files)
	}
	return task, err
}

func (s *Store) QueueCloud(owner int64, hash, magnet, identity, title string) (CloudTask, bool, error) {
	var key [16]byte
	if _, err := rand.Read(key[:]); err != nil {
		return CloudTask{}, false, err
	}
	task, err := scanCloud(s.db.QueryRow(`INSERT INTO cloud_tasks(owner_id,info_hash,magnet,identity,title,folder_key,shared_folder)
 VALUES($1,$2,$3,$4,$5,$6,TRUE) ON CONFLICT DO NOTHING RETURNING `+cloudColumns, owner, hash, magnet, identity, title, hex.EncodeToString(key[:])))
	if !errors.Is(err, ErrNotFound) {
		return task, err == nil, err
	}
	task, err = scanCloud(s.db.QueryRow(`SELECT `+cloudColumns+` FROM cloud_tasks WHERE owner_id=$1 AND
 (info_hash=$2 OR ($3<>'' AND identity=$3 AND state NOT IN ('failed','no_mp4')))
 ORDER BY (info_hash=$2) DESC,id LIMIT 1`, owner, hash, identity))
	return task, false, err
}

func (s *Store) CloudTask(owner, id int64) (CloudTask, error) {
	return scanCloud(s.db.QueryRow(`SELECT `+cloudColumns+` FROM cloud_tasks WHERE owner_id=$1 AND id=$2`, owner, id))
}

func (s *Store) CloudTasks(owner, before int64) ([]CloudTask, error) {
	return s.cloudRows(`SELECT `+cloudColumns+` FROM cloud_tasks WHERE owner_id=$1 AND ($2=0 OR id<$2) ORDER BY id DESC LIMIT 100`, owner, before)
}

func (s *Store) PendingCloud() ([]CloudTask, error) {
	return s.cloudRows(`SELECT ` + cloudColumns + ` FROM cloud_tasks WHERE state IN ('queued','submitting','provider_queued','downloading','indexing','publishing','uncertain') ORDER BY updated_at,id LIMIT 32`)
}

func (s *Store) cloudRows(query string, args ...any) ([]CloudTask, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CloudTask{}
	for rows.Next() {
		task, err := scanCloud(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

func (s *Store) UpdateCloud(task CloudTask, next, message string) (bool, error) {
	if task.Files == nil {
		task.Files = []CloudFile{}
	}
	files, err := json.Marshal(task.Files)
	if err != nil {
		return false, err
	}
	res, err := s.db.Exec(`UPDATE cloud_tasks SET state=$2,folder_id=$3,progress=$4,files=$5::jsonb,message=$6,updated_at=now(),output_id=$8 WHERE id=$1 AND state=$7`, task.ID, next, task.Folder, task.Progress, string(files), message, task.State, task.OutputID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
