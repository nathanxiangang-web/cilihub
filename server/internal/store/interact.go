package store

import (
	"database/sql"
	"errors"
	"strings"
)

// WorkCounts 是单部作品的互动计数（评论只统计 visible）。
type WorkCounts struct{ Likes, Favorites, Comments int64 }

// CommentRow 是评论读模型，附带作品标题与用户名。
type CommentRow struct {
	ID, WorkID int64
	EpisodeNo  int
	UserID     int64
	WorkTitle  string
	Username   string
	Content    string
	Status     string
	CreatedAt  string
}

type CommentPage struct {
	Items        []CommentRow
	Page, Size   int
	Pages, Total int
}

// WorkCounts 汇总作品维度的点赞、收藏与可见评论数。
func (s *Store) WorkCounts(workID int64) (WorkCounts, error) {
	var c WorkCounts
	err := s.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM likes WHERE work_id = $1),
		(SELECT COUNT(*) FROM favorites WHERE work_id = $1),
		(SELECT COUNT(*) FROM comments WHERE work_id = $1 AND status = 'visible')`, workID).
		Scan(&c.Likes, &c.Favorites, &c.Comments)
	return c, err
}

// SetLike 幂等地设置或取消点赞。
func (s *Store) SetLike(userID, workID int64, on bool, now string) error {
	if on {
		_, err := s.db.Exec(`INSERT INTO likes(user_id, work_id, created_at) VALUES($1, $2, $3)
			ON CONFLICT (user_id, work_id) DO NOTHING`, userID, workID, now)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM likes WHERE user_id = $1 AND work_id = $2`, userID, workID)
	return err
}

// SetFavorite 幂等地设置或取消收藏。
func (s *Store) SetFavorite(userID, workID int64, on bool, now string) error {
	if on {
		_, err := s.db.Exec(`INSERT INTO favorites(user_id, work_id, created_at) VALUES($1, $2, $3)
			ON CONFLICT (user_id, work_id) DO NOTHING`, userID, workID, now)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM favorites WHERE user_id = $1 AND work_id = $2`, userID, workID)
	return err
}

// UserLikes 返回用户全部点赞的作品 ID，最近点赞在前。
func (s *Store) UserLikes(userID int64) ([]int64, error) {
	return s.userWorkIDs(`SELECT work_id FROM likes WHERE user_id = $1
		ORDER BY created_at DESC, work_id DESC`, userID)
}

// UserFavorites 返回用户全部收藏的作品 ID，最近收藏在前。
func (s *Store) UserFavorites(userID int64) ([]int64, error) {
	return s.userWorkIDs(`SELECT work_id FROM favorites WHERE user_id = $1
		ORDER BY created_at DESC, work_id DESC`, userID)
}

func (s *Store) userWorkIDs(query string, userID int64) ([]int64, error) {
	rows, err := s.db.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

// AddComment 新建一条可见评论并返回联表后的读模型。
func (s *Store) AddComment(workID int64, episodeNo int, userID int64, content, now string) (CommentRow, error) {
	if episodeNo < 1 {
		episodeNo = 1
	}
	var c CommentRow
	err := s.db.QueryRow(`
		INSERT INTO comments(work_id, episode_no, user_id, content, status, created_at)
		VALUES($1, $2, $3, $4, 'visible', $5)
		RETURNING id, work_id, episode_no, user_id, content, status, created_at`,
		workID, episodeNo, userID, content, now).
		Scan(&c.ID, &c.WorkID, &c.EpisodeNo, &c.UserID, &c.Content, &c.Status, &c.CreatedAt)
	if err != nil {
		return CommentRow{}, err
	}
	if err := s.db.QueryRow(`SELECT COALESCE(w.title, ''), COALESCE(u.username, '')
		FROM users u LEFT JOIN works w ON w.id = $2 WHERE u.id = $1`, userID, workID).
		Scan(&c.WorkTitle, &c.Username); err != nil {
		return CommentRow{}, err
	}
	return c, nil
}

const commentColumns = `SELECT c.id, c.work_id, c.episode_no, c.user_id,
	COALESCE(w.title, ''), COALESCE(u.username, ''), c.content, c.status, c.created_at
	FROM comments c LEFT JOIN works w ON w.id = c.work_id
	LEFT JOIN users u ON u.id = c.user_id`

func scanComment(sc rowScanner) (CommentRow, error) {
	var c CommentRow
	err := sc.Scan(&c.ID, &c.WorkID, &c.EpisodeNo, &c.UserID, &c.WorkTitle, &c.Username,
		&c.Content, &c.Status, &c.CreatedAt)
	return c, err
}

// ListComments 分页返回作品的评论；status 为空时只取 visible。
func (s *Store) ListComments(workID int64, status string, page, size int) (CommentPage, error) {
	page, size = pageArgs(page, size)
	if strings.TrimSpace(status) == "" {
		status = "visible"
	}

	var total int
	countSQL := `SELECT COUNT(*) FROM comments c WHERE c.work_id = $1 AND c.status = $2`
	if err := s.db.QueryRow(countSQL, workID, status).Scan(&total); err != nil {
		return CommentPage{}, err
	}

	rows, err := s.db.Query(commentColumns+` WHERE c.work_id = $1 AND c.status = $2
		ORDER BY c.created_at DESC, c.id DESC LIMIT $3 OFFSET $4`,
		workID, status, size, (page-1)*size)
	if err != nil {
		return CommentPage{}, err
	}
	return collectComments(rows, page, size, total)
}

// AdminListComments 分页返回后台评论列表；q 匹配作品标题、用户名或评论内容。
func (s *Store) AdminListComments(q, status string, page, size int) (CommentPage, error) {
	page, size = pageArgs(page, size)
	where := []string{"TRUE"}
	var args []any
	if kw := strings.TrimSpace(q); kw != "" {
		pattern := likePattern(kw)
		where = append(where, `(w.title LIKE `+bind(&args, pattern)+` ESCAPE '\' OR u.username LIKE `+
			bind(&args, pattern)+` ESCAPE '\' OR c.content LIKE `+bind(&args, pattern)+` ESCAPE '\')`)
	}
	switch status {
	case "visible", "hidden":
		where = append(where, "c.status = "+bind(&args, status))
	}
	clause := strings.Join(where, " AND ")

	var total int
	countSQL := `SELECT COUNT(*) FROM comments c
		LEFT JOIN works w ON w.id = c.work_id LEFT JOIN users u ON u.id = c.user_id WHERE ` + clause
	if err := s.db.QueryRow(countSQL, args...).Scan(&total); err != nil {
		return CommentPage{}, err
	}

	query := commentColumns + ` WHERE ` + clause + ` ORDER BY c.created_at DESC, c.id DESC LIMIT ` +
		bind(&args, size) + ` OFFSET ` + bind(&args, (page-1)*size)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return CommentPage{}, err
	}
	return collectComments(rows, page, size, total)
}

func collectComments(rows *sql.Rows, page, size, total int) (CommentPage, error) {
	defer rows.Close()
	items := []CommentRow{}
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return CommentPage{}, err
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return CommentPage{}, err
	}
	return CommentPage{Items: items, Page: page, Size: size, Pages: pageCount(total, size), Total: total}, nil
}

// SetCommentStatus 切换评论为 visible 或 hidden；不存在时 ErrNotFound。
func (s *Store) SetCommentStatus(id int64, status string) error {
	if status != "visible" && status != "hidden" {
		return errors.New("无效的评论状态")
	}
	res, err := s.db.Exec(`UPDATE comments SET status = $1 WHERE id = $2`, status, id)
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

// DeleteComment 删除一条评论；不存在时 ErrNotFound。
func (s *Store) DeleteComment(id int64) error {
	res, err := s.db.Exec(`DELETE FROM comments WHERE id = $1`, id)
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