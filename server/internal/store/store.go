// Package store 是 cilihub 的全部持久化状态：作品/剧集/分类/同步/管理员/审计。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// ErrNotFound 表示调用方查询的记录不存在，由上层映射为 404。
var ErrNotFound = errors.New("not found")

// Store 包装 PostgreSQL 连接池。
type Store struct {
	db *sql.DB
}

// Open 连接 CILIHUB_DATABASE_URL 指向的库、建表并写入默认分类。
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭底层连接池。
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// inTx 在单个事务内执行 fn，任何错误都整体回滚。
func (s *Store) inTx(fn func(*sql.Tx) error) (err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

// uniqueViolation 判断错误是否为 PostgreSQL 唯一约束冲突（SQLSTATE 23505）。
func uniqueViolation(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "23505"
	}
	return false
}

// bind 追加一个参数并返回其占位符（$1..$n）。
func bind(args *[]any, v any) string {
	*args = append(*args, v)
	return fmt.Sprintf("$%d", len(*args))
}

// placeholders 返回从 start 开始的 n 个连续占位符。
func placeholders(start, n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("$%d", start+i)
	}
	return strings.Join(parts, ",")
}

func pageArgs(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	return page, size
}

func pageCount(total, size int) int {
	if total <= 0 {
		return 0
	}
	return (total + size - 1) / size
}

// likePattern 把用户关键词转成字面量 LIKE 模式，配合 ESCAPE '\' 使用。
func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}