package store

import "fmt"

// schemaStatements 与 design.md §2.2 C-STORE@1 的 PostgreSQL DDL 逐字一致。
var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS works (
    id BIGINT PRIMARY KEY, title TEXT NOT NULL, category_id BIGINT NOT NULL DEFAULT 1,
    visible BOOLEAN NOT NULL DEFAULT TRUE, pinned BOOLEAN NOT NULL DEFAULT FALSE,
    sort_index INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);`,

	`CREATE TABLE IF NOT EXISTS work_files (
    id BIGSERIAL PRIMARY KEY, work_id BIGINT NOT NULL REFERENCES works(id) ON DELETE CASCADE,
    file_id BIGINT NOT NULL UNIQUE, source_path TEXT NOT NULL UNIQUE, file_name TEXT NOT NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0, episode_no INTEGER NOT NULL DEFAULT 1,
    present BOOLEAN NOT NULL DEFAULT TRUE, missing_since TEXT NOT NULL DEFAULT '',
    first_seen_at TEXT NOT NULL, last_seen_at TEXT NOT NULL);`,

	`CREATE INDEX IF NOT EXISTS idx_work_files_work ON work_files(work_id, episode_no);`,

	`CREATE TABLE IF NOT EXISTS categories (
    id BIGSERIAL PRIMARY KEY, slug TEXT NOT NULL UNIQUE, name TEXT NOT NULL,
    sort_index INTEGER NOT NULL DEFAULT 0, visible BOOLEAN NOT NULL DEFAULT TRUE);`,

	`CREATE TABLE IF NOT EXISTS sync_runs (
    id BIGSERIAL PRIMARY KEY, started_at TEXT NOT NULL, finished_at TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL, added INTEGER NOT NULL DEFAULT 0, missing INTEGER NOT NULL DEFAULT 0,
    failed INTEGER NOT NULL DEFAULT 0, message TEXT NOT NULL DEFAULT '');`,

	`CREATE TABLE IF NOT EXISTS sync_events (
    id BIGSERIAL PRIMARY KEY, run_id BIGINT NOT NULL, kind TEXT NOT NULL, file_path TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '', at TEXT NOT NULL);`,

	`CREATE TABLE IF NOT EXISTS admin_users (
    id BIGSERIAL PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL,
    created_at TEXT NOT NULL);`,

	`CREATE TABLE IF NOT EXISTS admin_sessions (
    token_hash TEXT PRIMARY KEY, admin_id BIGINT NOT NULL,
    expires_at TEXT NOT NULL, created_at TEXT NOT NULL);`,

	`CREATE TABLE IF NOT EXISTS audit_log (
    id BIGSERIAL PRIMARY KEY, admin_id BIGINT NOT NULL, action TEXT NOT NULL,
    target_type TEXT NOT NULL, target_id TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '', at TEXT NOT NULL);`,

	`CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active', note TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL, last_login_at TEXT NOT NULL DEFAULT '');`,

	`CREATE TABLE IF NOT EXISTS user_sessions (
    token_hash TEXT PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL, created_at TEXT NOT NULL);`,

	`CREATE INDEX IF NOT EXISTS idx_user_sessions_user ON user_sessions(user_id);`,

	`CREATE TABLE IF NOT EXISTS openlist_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    origin TEXT NOT NULL, folder TEXT NOT NULL, cdn_host TEXT NOT NULL,
    username TEXT NOT NULL, password_enc TEXT NOT NULL, updated_at TEXT NOT NULL);`,

	`CREATE TABLE IF NOT EXISTS shares (
    id BIGSERIAL PRIMARY KEY, code TEXT NOT NULL UNIQUE,
    work_id BIGINT NOT NULL REFERENCES works(id) ON DELETE CASCADE,
    password_hash TEXT NOT NULL DEFAULT '', expires_at TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active', views BIGINT NOT NULL DEFAULT 0,
    created_by BIGINT NOT NULL DEFAULT 0, created_at TEXT NOT NULL);`,

	`CREATE INDEX IF NOT EXISTS idx_shares_work ON shares(work_id);`,

	`CREATE TABLE IF NOT EXISTS likes (
    user_id BIGINT NOT NULL, work_id BIGINT NOT NULL, created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, work_id));`,

	`CREATE TABLE IF NOT EXISTS favorites (
    user_id BIGINT NOT NULL, work_id BIGINT NOT NULL, created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, work_id));`,

	`CREATE TABLE IF NOT EXISTS comments (
    id BIGSERIAL PRIMARY KEY, work_id BIGINT NOT NULL, episode_no INTEGER NOT NULL DEFAULT 1,
    user_id BIGINT NOT NULL, content TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'visible',
    created_at TEXT NOT NULL);`,

	`CREATE INDEX IF NOT EXISTS idx_comments_work ON comments(work_id, status, created_at DESC);`,
}

const defaultCategorySQL = `INSERT INTO categories(slug,name,sort_index,visible) VALUES('openlist-films','影视',0,TRUE) ON CONFLICT (slug) DO NOTHING`

func (s *Store) initSchema() error {
	for _, stmt := range append(append([]string(nil), schemaStatements...), cloudSchema...) {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("创建数据表失败: %w", err)
		}
	}
	if _, err := s.db.Exec(defaultCategorySQL); err != nil {
		return fmt.Errorf("写入默认分类失败: %w", err)
	}
	return nil
}
