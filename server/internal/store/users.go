package store

import (
	"database/sql"
	"errors"
	"strings"
)

// UserRow 是站点前台用户（与 admin_users 完全独立）。
type UserRow struct {
	ID          int64
	Username    string
	Status      string
	Note        string
	CreatedAt   string
	LastLoginAt string
}

type UserPage struct {
	Items        []UserRow
	Page, Size   int
	Pages, Total int
}

const userColumns = `SELECT id, username, status, note, created_at, last_login_at FROM users`

func scanUser(sc rowScanner) (UserRow, error) {
	var u UserRow
	err := sc.Scan(&u.ID, &u.Username, &u.Status, &u.Note, &u.CreatedAt, &u.LastLoginAt)
	return u, err
}

// CreateUser 创建状态为 active 的用户；用户名重复时返回「该账号已被注册」。
func (s *Store) CreateUser(username, passwordHash, note, now string) (UserRow, error) {
	row, err := scanUser(s.db.QueryRow(`
		INSERT INTO users(username, password_hash, status, note, created_at, last_login_at)
		VALUES($1, $2, 'active', $3, $4, '')
		RETURNING id, username, status, note, created_at, last_login_at`,
		username, passwordHash, note, now))
	if uniqueViolation(err) {
		return UserRow{}, errors.New("该账号已被注册")
	}
	if err != nil {
		return UserRow{}, err
	}
	return row, nil
}

// FindUserByUsername 按用户名返回用户读模型；不存在时 ErrNotFound。
func (s *Store) FindUserByUsername(username string) (UserRow, error) {
	return scanUserErr(s.db.QueryRow(userColumns+` WHERE username = $1`, username))
}

// FindUserCredential 按用户名返回用户及其密码哈希，供登录校验；不存在时 ErrNotFound。
func (s *Store) FindUserCredential(username string) (UserRow, string, error) {
	var row UserRow
	var hash string
	err := s.db.QueryRow(`SELECT id, username, status, note, created_at, last_login_at, password_hash
		FROM users WHERE username = $1`, username).
		Scan(&row.ID, &row.Username, &row.Status, &row.Note, &row.CreatedAt, &row.LastLoginAt, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return UserRow{}, "", ErrNotFound
	}
	if err != nil {
		return UserRow{}, "", err
	}
	return row, hash, nil
}

// GetUser 按主键返回用户读模型；不存在时 ErrNotFound。
func (s *Store) GetUser(id int64) (UserRow, error) {
	return scanUserErr(s.db.QueryRow(userColumns+` WHERE id = $1`, id))
}

// ListUsers 按 id 倒序分页返回用户；q 匹配用户名或备注，status 为 ""|active|disabled。
func (s *Store) ListUsers(q, status string, page, size int) (UserPage, error) {
	page, size = pageArgs(page, size)
	where := []string{"TRUE"}
	var args []any
	if kw := strings.TrimSpace(q); kw != "" {
		pattern := likePattern(kw)
		where = append(where, `(username LIKE `+bind(&args, pattern)+` ESCAPE '\' OR note LIKE `+
			bind(&args, pattern)+` ESCAPE '\')`)
	}
	switch status {
	case "active", "disabled":
		where = append(where, "status = "+bind(&args, status))
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE `+clause, args...).Scan(&total); err != nil {
		return UserPage{}, err
	}

	query := userColumns + ` WHERE ` + clause + ` ORDER BY id DESC LIMIT ` + bind(&args, size) +
		` OFFSET ` + bind(&args, (page-1)*size)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return UserPage{}, err
	}
	defer rows.Close()

	items := []UserRow{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return UserPage{}, err
		}
		items = append(items, u)
	}
	if err := rows.Err(); err != nil {
		return UserPage{}, err
	}
	return UserPage{Items: items, Page: page, Size: size, Pages: pageCount(total, size), Total: total}, nil
}

// UpdateUser 按非空字段更新用户名、状态与备注；未传字段保持不变。
// now 保留在签名中与其他更新方法一致（users 表无 updated_at 列）。
func (s *Store) UpdateUser(id int64, username, status, note *string, now string) error {
	_ = now
	sets := []string{}
	var args []any
	if username != nil {
		sets = append(sets, "username = "+bind(&args, *username))
	}
	if status != nil {
		sets = append(sets, "status = "+bind(&args, *status))
	}
	if note != nil {
		sets = append(sets, "note = "+bind(&args, *note))
	}
	if len(sets) == 0 {
		_, err := s.GetUser(id)
		return err
	}
	res, err := s.db.Exec(`UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = `+bind(&args, id), args...)
	if uniqueViolation(err) {
		return errors.New("该账号已被注册")
	}
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

// UpdateUserPassword 覆盖用户的密码哈希；不存在时 ErrNotFound。
func (s *Store) UpdateUserPassword(id int64, passwordHash string) error {
	res, err := s.db.Exec(`UPDATE users SET password_hash = $1 WHERE id = $2`, passwordHash, id)
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

// DeleteUser 在同一事务内删除用户及其全部会话；不存在时 ErrNotFound。
func (s *Store) DeleteUser(id int64) error {
	return s.inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM user_sessions WHERE user_id = $1`, id); err != nil {
			return err
		}
		res, err := tx.Exec(`DELETE FROM users WHERE id = $1`, id)
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

// TouchUserLogin 记录最近一次登录时间；不存在时 ErrNotFound。
func (s *Store) TouchUserLogin(id int64, now string) error {
	res, err := s.db.Exec(`UPDATE users SET last_login_at = $1 WHERE id = $2`, now, id)
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

// PutUserSession 写入或覆盖一条用户会话（token 已哈希）。
func (s *Store) PutUserSession(tokenHash string, userID int64, expiresAt, now string) error {
	_, err := s.db.Exec(`INSERT INTO user_sessions(token_hash, user_id, expires_at, created_at)
		VALUES($1, $2, $3, $4)
		ON CONFLICT (token_hash) DO UPDATE SET user_id = EXCLUDED.user_id,
			expires_at = EXCLUDED.expires_at, created_at = EXCLUDED.created_at`,
		tokenHash, userID, expiresAt, now)
	return err
}

// UserBySession 校验未过期且账号为 active 的会话；失效、过期或禁用均返回 ErrNotFound。
func (s *Store) UserBySession(tokenHash, now string) (UserRow, error) {
	return scanUserErr(s.db.QueryRow(`
		SELECT u.id, u.username, u.status, u.note, u.created_at, u.last_login_at
		FROM user_sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > $2 AND u.status = 'active'`, tokenHash, now))
}

func (s *Store) DeleteUserSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM user_sessions WHERE token_hash = $1`, tokenHash)
	return err
}

func (s *Store) DeleteUserSessions(userID int64) error {
	_, err := s.db.Exec(`DELETE FROM user_sessions WHERE user_id = $1`, userID)
	return err
}

func scanUserErr(sc rowScanner) (UserRow, error) {
	u, err := scanUser(sc)
	if errors.Is(err, sql.ErrNoRows) {
		return UserRow{}, ErrNotFound
	}
	if err != nil {
		return UserRow{}, err
	}
	return u, nil
}