package store

import (
	"database/sql"
	"errors"
)

// OpenListSettings 是单行 openlist_settings 表对应的配置；密码只以密文存在。
type OpenListSettings struct {
	Origin      string
	Folder      string
	CDNHost     string
	Username    string
	PasswordEnc string
	UpdatedAt   string
}

// OpenListSettings 读取当前配置；没有记录时返回 ErrNotFound。
func (s *Store) OpenListSettings() (OpenListSettings, error) {
	var v OpenListSettings
	err := s.db.QueryRow(`SELECT origin, folder, cdn_host, username, password_enc, updated_at
		FROM openlist_settings WHERE id = 1`).
		Scan(&v.Origin, &v.Folder, &v.CDNHost, &v.Username, &v.PasswordEnc, &v.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return OpenListSettings{}, ErrNotFound
	}
	if err != nil {
		return OpenListSettings{}, err
	}
	return v, nil
}

// SaveOpenListSettings 以 upsert 方式写入 id=1 的唯一配置，now 作为 updated_at。
func (s *Store) SaveOpenListSettings(v OpenListSettings, now string) error {
	_, err := s.db.Exec(`INSERT INTO openlist_settings(id, origin, folder, cdn_host, username, password_enc, updated_at)
		VALUES(1, $1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET origin = EXCLUDED.origin, folder = EXCLUDED.folder,
			cdn_host = EXCLUDED.cdn_host, username = EXCLUDED.username,
			password_enc = EXCLUDED.password_enc, updated_at = EXCLUDED.updated_at`,
		v.Origin, v.Folder, v.CDNHost, v.Username, v.PasswordEnc, now)
	return err
}