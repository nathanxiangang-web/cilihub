package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// CategoryRow 是分类的读模型，WorkCount 为该分类下的作品总数（含未上架）。
type CategoryRow struct {
	ID        int64
	Slug      string
	Name      string
	SortIndex int
	Visible   bool
	WorkCount int
}

// Categories 返回按 sort_index、id 升序排列的全部分类。
func (s *Store) Categories() ([]CategoryRow, error) {
	rows, err := s.db.Query(`
		SELECT c.id, c.slug, c.name, c.sort_index, c.visible, COUNT(w.id)
		FROM categories c
		LEFT JOIN works w ON w.category_id = c.id
		GROUP BY c.id, c.slug, c.name, c.sort_index, c.visible
		ORDER BY c.sort_index ASC, c.id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]CategoryRow, 0, 8)
	for rows.Next() {
		var row CategoryRow
		if err := rows.Scan(&row.ID, &row.Slug, &row.Name, &row.SortIndex, &row.Visible, &row.WorkCount); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// CreateCategory 追加一个可见分类，排序值取当前最大值 +1。
func (s *Store) CreateCategory(name, slug string) (CategoryRow, error) {
	var row CategoryRow
	err := s.inTx(func(tx *sql.Tx) error {
		var next int
		if err := tx.QueryRow(`SELECT COALESCE(MAX(sort_index), 0) + 1 FROM categories`).Scan(&next); err != nil {
			return err
		}
		err := tx.QueryRow(`
			INSERT INTO categories(slug, name, sort_index, visible)
			VALUES($1, $2, $3, TRUE)
			RETURNING id, slug, name, sort_index, visible`,
			slug, name, next).Scan(&row.ID, &row.Slug, &row.Name, &row.SortIndex, &row.Visible)
		if err != nil && uniqueViolation(err) {
			return errors.New("分类标识重复")
		}
		return err
	})
	if err != nil {
		return CategoryRow{}, err
	}
	row.WorkCount = 0
	return row, nil
}

// UpdateCategory 修改分类名称与可见性；分类不存在时返回 ErrNotFound。
func (s *Store) UpdateCategory(id int64, name string, visible bool) error {
	return s.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE categories SET name = $1, visible = $2 WHERE id = $3`, name, visible, id)
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

// ReorderCategories 按给定顺序把分类的 sort_index 重写为 1..n，忽略不存在的 id。
func (s *Store) ReorderCategories(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return s.inTx(func(tx *sql.Tx) error {
		for i, id := range ids {
			if _, err := tx.Exec(`UPDATE categories SET sort_index = $1 WHERE id = $2`, i+1, id); err != nil {
				return fmt.Errorf("分类排序失败：%w", err)
			}
		}
		return nil
	})
}