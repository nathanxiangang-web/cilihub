package store

import (
	"database/sql"
	"errors"
)

type AdminUser struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    string
}

// AdminCount 返回管理员数量，用于首次启动初始化。
func (s *Store) AdminCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM admin_users`).Scan(&n)
	return n, err
}

func (s *Store) CreateAdmin(username, passwordHash, now string) (AdminUser, error) {
	var id int64
	err := s.db.QueryRow(`INSERT INTO admin_users(username, password_hash, created_at) VALUES($1, $2, $3) RETURNING id`,
		username, passwordHash, now).Scan(&id)
	if uniqueViolation(err) {
		return AdminUser{}, errors.New("管理员用户名已存在")
	}
	if err != nil {
		return AdminUser{}, err
	}
	return AdminUser{ID: id, Username: username, PasswordHash: passwordHash, CreatedAt: now}, nil
}

func (s *Store) FindAdmin(username string) (AdminUser, error) {
	var u AdminUser
	err := s.db.QueryRow(`SELECT id, username, password_hash, created_at FROM admin_users WHERE username = $1`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, ErrNotFound
	}
	if err != nil {
		return AdminUser{}, err
	}
	return u, nil
}

func (s *Store) UpdateAdminPassword(id int64, hash string) error {
	res, err := s.db.Exec(`UPDATE admin_users SET password_hash = $1 WHERE id = $2`, hash, id)
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
}

func (s *Store) PutSession(tokenHash string, adminID int64, expiresAt, now string) error {
	_, err := s.db.Exec(`INSERT INTO admin_sessions(token_hash, admin_id, expires_at, created_at)
		VALUES($1, $2, $3, $4)
		ON CONFLICT (token_hash) DO UPDATE SET admin_id = EXCLUDED.admin_id,
			expires_at = EXCLUDED.expires_at, created_at = EXCLUDED.created_at`,
		tokenHash, adminID, expiresAt, now)
	return err
}

// SessionAdmin 校验未过期会话并返回其管理员；失效时 ErrNotFound。
func (s *Store) SessionAdmin(tokenHash, now string) (AdminUser, error) {
	var u AdminUser
	err := s.db.QueryRow(`SELECT u.id, u.username, u.password_hash, u.created_at
		FROM admin_sessions s JOIN admin_users u ON u.id = s.admin_id
		WHERE s.token_hash = $1 AND s.expires_at > $2`, tokenHash, now).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, ErrNotFound
	}
	if err != nil {
		return AdminUser{}, err
	}
	return u, nil
}

func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM admin_sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// DeleteOtherSessions 保留当前会话，删除该管理员的其余会话。
func (s *Store) DeleteOtherSessions(adminID int64, keepTokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM admin_sessions WHERE admin_id = $1 AND token_hash <> $2`, adminID, keepTokenHash)
	return err
}