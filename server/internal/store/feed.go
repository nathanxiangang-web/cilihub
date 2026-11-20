package store

import (
	"database/sql"
	"errors"
	"strings"
)

type FeedItem struct {
	ID            int64
	Title         string
	CategorySlug  string
	EpisodeCount  int
	TotalEpisodes int
	Pinned        bool
}

type FeedPage struct {
	Items        []FeedItem
	Page, Size   int
	Pages, Total int
}

type FileRef struct {
	FileID  int64
	WorkID  int64
	Path    string
	Visible bool
	Present bool
}

const feedColumns = `SELECT w.id, w.title, COALESCE(c.slug, ''),
	(SELECT COUNT(*) FROM work_files f WHERE f.work_id = w.id AND f.present = TRUE),
	(SELECT COUNT(*) FROM work_files f WHERE f.work_id = w.id),
	w.pinned
	FROM works w LEFT JOIN categories c ON c.id = w.category_id`

// Feed 返回前台可见作品列表（只含 visible=1）。
func (s *Store) Feed(categorySlug, q string, page, size int) (FeedPage, error) {
	return s.feed(categorySlug, q, "", page, size)
}

// FeedWithSeed 与 Feed 过滤、计数、分页一致；seed 在 2~32 字符内时按
// md5(seed || id) 排序，保证同一 seed 的整轮分页确定且不重不漏。
func (s *Store) FeedWithSeed(categorySlug, q, seed string, page, size int) (FeedPage, error) {
	return s.feed(categorySlug, q, seed, page, size)
}

func (s *Store) feed(categorySlug, q, seed string, page, size int) (FeedPage, error) {
	page, size = pageArgs(page, size)
	where := []string{"w.visible = TRUE"}
	var args []any
	if slug := strings.TrimSpace(categorySlug); slug != "" {
		where = append(where, "c.slug = "+bind(&args, slug))
	}
	if kw := strings.TrimSpace(q); kw != "" {
		where = append(where, `w.title LIKE `+bind(&args, likePattern(kw))+` ESCAPE '\'`)
	}
	clause := strings.Join(where, " AND ")

	var total int
	countSQL := `SELECT COUNT(*) FROM works w LEFT JOIN categories c ON c.id = w.category_id WHERE ` + clause
	if err := s.db.QueryRow(countSQL, args...).Scan(&total); err != nil {
		return FeedPage{}, err
	}

	order := ` ORDER BY w.pinned DESC, w.sort_index ASC, w.id ASC`
	if len(seed) >= 2 && len(seed) <= 32 {
		order = ` ORDER BY w.pinned DESC, md5(` + bind(&args, seed) + ` || w.id::text) ASC, w.id ASC`
	}
	query := feedColumns + ` WHERE ` + clause + order + ` LIMIT ` + bind(&args, size) +
		` OFFSET ` + bind(&args, (page-1)*size)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return FeedPage{}, err
	}
	defer rows.Close()

	items := []FeedItem{}
	for rows.Next() {
		var it FeedItem
		if err := rows.Scan(&it.ID, &it.Title, &it.CategorySlug, &it.EpisodeCount, &it.TotalEpisodes, &it.Pinned); err != nil {
			return FeedPage{}, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return FeedPage{}, err
	}
	return FeedPage{Items: items, Page: page, Size: size, Pages: pageCount(total, size), Total: total}, nil
}

// VisibleFile 按 file_id 返回播放所需的可见性与存在性；不存在时 ErrNotFound。
func (s *Store) VisibleFile(fileID int64) (FileRef, error) {
	var ref FileRef
	err := s.db.QueryRow(`SELECT f.work_id, f.source_path, w.visible, f.present
		FROM work_files f JOIN works w ON w.id = f.work_id
		WHERE f.file_id = $1`, fileID).
		Scan(&ref.WorkID, &ref.Path, &ref.Visible, &ref.Present)
	if errors.Is(err, sql.ErrNoRows) {
		return FileRef{}, ErrNotFound
	}
	if err != nil {
		return FileRef{}, err
	}
	ref.FileID = fileID
	return ref, nil
}