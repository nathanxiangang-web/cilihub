package store

type AuditRow struct {
	ID         int64
	At         string
	AdminName  string
	Action     string
	TargetType string
	TargetID   string
	Detail     string
}

type AuditPage struct {
	Items        []AuditRow
	Page, Size   int
	Pages, Total int
}

// Audit 追加一条操作审计记录。
func (s *Store) Audit(adminID int64, action, targetType, targetID, detail, at string) error {
	_, err := s.db.Exec(`INSERT INTO audit_log(admin_id, action, target_type, target_id, detail, at)
		VALUES($1, $2, $3, $4, $5, $6)`,
		adminID, action, targetType, targetID, detail, at)
	return err
}

// AuditList 按时间倒序分页返回审计记录。
func (s *Store) AuditList(page, size int) (AuditPage, error) {
	page, size = pageArgs(page, size)

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&total); err != nil {
		return AuditPage{}, err
	}

	rows, err := s.db.Query(`SELECT a.id, a.at, COALESCE(u.username, ''), a.action, a.target_type, a.target_id, a.detail
		FROM audit_log a LEFT JOIN admin_users u ON u.id = a.admin_id
		ORDER BY a.id DESC LIMIT $1 OFFSET $2`, size, (page-1)*size)
	if err != nil {
		return AuditPage{}, err
	}
	defer rows.Close()

	items := []AuditRow{}
	for rows.Next() {
		var r AuditRow
		if err := rows.Scan(&r.ID, &r.At, &r.AdminName, &r.Action, &r.TargetType, &r.TargetID, &r.Detail); err != nil {
			return AuditPage{}, err
		}
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		return AuditPage{}, err
	}
	return AuditPage{Items: items, Page: page, Size: size, Pages: pageCount(total, size), Total: total}, nil
}