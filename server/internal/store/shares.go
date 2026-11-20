package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ShareRow 是分享链接的读模型；WorkTitle、Creator 由联表得出。
type ShareRow struct {
	ID           int64
	Code         string
	WorkID       int64
	WorkTitle    string
	PasswordHash string
	ExpiresAt    string
	Status       string
	CreatedAt    string
	Views        int64
	CreatedBy    int64
	Creator      string
}

type SharePage struct {
	Items        []ShareRow
	Page, Size   int
	Pages, Total int
}

const shareColumns = `SELECT s.id, s.code, s.work_id, COALESCE(w.title, ''), s.password_hash,
	s.expires_at, s.status, s.created_at, s.views, s.created_by, COALESCE(u.username, '管理员')
	FROM shares s
	LEFT JOIN works w ON w.id = s.work_id
	LEFT JOIN users u ON u.id = s.created_by`

func scanShare(sc rowScanner) (ShareRow, error) {
	var r ShareRow
	err := sc.Scan(&r.ID, &r.Code, &r.WorkID, &r.WorkTitle, &r.PasswordHash, &r.ExpiresAt,
		&r.Status, &r.CreatedAt, &r.Views, &r.CreatedBy, &r.Creator)
	return r, err
}

// CreateShare 在单个事务内插入分享并回读联表后的读模型。
func (s *Store) CreateShare(r ShareRow, now string) (ShareRow, error) {
	status := r.Status
	if status == "" {
		status = "active"
	}
	var row ShareRow
	err := s.inTx(func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRow(`
			INSERT INTO shares(code, work_id, password_hash, expires_at, status, created_by, created_at)
			VALUES($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			r.Code, r.WorkID, r.PasswordHash, r.ExpiresAt, status, r.CreatedBy, now).Scan(&id); err != nil {
			if uniqueViolation(err) {
				return errors.New("分享码重复，请重试")
			}
			return err
		}
		got, err := scanShare(tx.QueryRow(shareColumns+` WHERE s.id = $1`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		row = got
		return nil
	})
	if err != nil {
		return ShareRow{}, err
	}
	return row, nil
}

// ShareByCode 按分享码返回读模型；不存在时 ErrNotFound。
func (s *Store) ShareByCode(code string) (ShareRow, error) {
	r, err := scanShare(s.db.QueryRow(shareColumns+` WHERE s.code = $1`, code))
	if errors.Is(err, sql.ErrNoRows) {
		return ShareRow{}, ErrNotFound
	}
	if err != nil {
		return ShareRow{}, err
	}
	return r, nil
}

// ListShares 按创建时间倒序分页；q 匹配分享码、作品标题或创建人，
// status 为 ""|active|revoked|expired（expired 指仍为 active 但已过有效期）。
func (s *Store) ListShares(q, status string, page, size int) (SharePage, error) {
	page, size = pageArgs(page, size)
	where := []string{"TRUE"}
	var args []any
	if kw := strings.TrimSpace(q); kw != "" {
		pattern := likePattern(kw)
		where = append(where, `(s.code LIKE `+bind(&args, pattern)+` ESCAPE '\' OR w.title LIKE `+
			bind(&args, pattern)+` ESCAPE '\' OR u.username LIKE `+bind(&args, pattern)+` ESCAPE '\')`)
	}
	switch status {
	case "active":
		where = append(where, "s.status = "+bind(&args, "active"))
	case "revoked":
		where = append(where, "s.status = "+bind(&args, "revoked"))
	case "expired":
		where = append(where, "s.status = 'active' AND s.expires_at <> '' AND s.expires_at <= "+
			bind(&args, time.Now().UTC().Format(time.RFC3339)))
	}
	clause := strings.Join(where, " AND ")
	from := ` FROM shares s LEFT JOIN works w ON w.id = s.work_id LEFT JOIN users u ON u.id = s.created_by`

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*)`+from+` WHERE `+clause, args...).Scan(&total); err != nil {
		return SharePage{}, err
	}

	query := shareColumns + ` WHERE ` + clause + ` ORDER BY s.created_at DESC, s.id DESC LIMIT ` +
		bind(&args, size) + ` OFFSET ` + bind(&args, (page-1)*size)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return SharePage{}, err
	}
	defer rows.Close()

	items := []ShareRow{}
	for rows.Next() {
		r, err := scanShare(rows)
		if err != nil {
			return SharePage{}, err
		}
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		return SharePage{}, err
	}
	return SharePage{Items: items, Page: page, Size: size, Pages: pageCount(total, size), Total: total}, nil
}

// RevokeShare 取消分享；不存在时 ErrNotFound。
func (s *Store) RevokeShare(id int64) error {
	return s.setShareStatus(id, "revoked")
}

// RestoreShare 恢复分享为 active；不存在时 ErrNotFound。
func (s *Store) RestoreShare(id int64) error {
	return s.setShareStatus(id, "active")
}

func (s *Store) setShareStatus(id int64, status string) error {
	res, err := s.db.Exec(`UPDATE shares SET status = $1 WHERE id = $2`, status, id)
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

// DeleteShare 删除分享记录；不存在时 ErrNotFound。
func (s *Store) DeleteShare(id int64) error {
	res, err := s.db.Exec(`DELETE FROM shares WHERE id = $1`, id)
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

// IncShareViews 累加分享访问次数；分享不存在时 ErrNotFound。
func (s *Store) IncShareViews(code string) error {
	res, err := s.db.Exec(`UPDATE shares SET views = views + 1 WHERE code = $1`, code)
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