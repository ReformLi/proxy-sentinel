package storage

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// AuditRecord 对应 audit_logs 表的一行
type AuditRecord struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Action    string    `json:"action"`
	IP        string    `json:"ip"`
	CreatedAt time.Time `json:"created_at"`
}

// AuditFilter 审计日志查询过滤
type AuditFilter struct {
	Start    time.Time
	End      time.Time
	Username string // 精确匹配
	Keyword  string // 在 action 中模糊搜索
	IP       string // 精确或前缀匹配
	Page     int
	PageSize int
}

// ListAudits 分页查询审计日志
func (db *DB) ListAudits(ctx context.Context, f AuditFilter) ([]AuditRecord, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 200 {
		f.PageSize = 50
	}
	offset := (f.Page - 1) * f.PageSize
	q := `SELECT id, username, action, ip, created_at FROM audit_logs`
	where, args := buildAuditWhere(f)
	if where != "" {
		q += " " + where
	}
	q += " ORDER BY id DESC LIMIT ? OFFSET ?"

	finalArgs := append(args, f.PageSize, offset)
	rows, err := db.QueryContext(ctx, q, finalArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAudits(rows)
}

// CountAuditsByFilter 统计符合过滤条件的审计日志总数
func (db *DB) CountAuditsByFilter(ctx context.Context, f AuditFilter) (int64, error) {
	q := `SELECT COUNT(*) FROM audit_logs`
	where, args := buildAuditWhere(f)
	if where != "" {
		q += " " + where
	}
	var n int64
	err := db.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

// GetAudit 获取单条审计记录详情
func (db *DB) GetAudit(ctx context.Context, id int64) (*AuditRecord, error) {
	row := db.QueryRowContext(ctx, `SELECT id, username, action, ip, created_at FROM audit_logs WHERE id=?`, id)
	r := &AuditRecord{}
	err := row.Scan(&r.ID, &r.Username, &r.Action, &r.IP, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

func buildAuditWhere(f AuditFilter) (string, []any) {
	var conds []string
	var args []any
	if !f.Start.IsZero() {
		conds = append(conds, "created_at >= ?")
		args = append(args, f.Start)
	}
	if !f.End.IsZero() {
		conds = append(conds, "created_at <= ?")
		args = append(args, f.End)
	}
	if f.Username != "" {
		conds = append(conds, "username = ?")
		args = append(args, f.Username)
	}
	if f.IP != "" {
		conds = append(conds, "(ip = ? OR ip LIKE ?)")
		args = append(args, f.IP, f.IP+"%")
	}
	if f.Keyword != "" {
		conds = append(conds, "action LIKE ?")
		args = append(args, "%"+f.Keyword+"%")
	}
	if len(conds) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

func scanAudits(rows *sql.Rows) ([]AuditRecord, error) {
	var out []AuditRecord
	for rows.Next() {
		var r AuditRecord
		if err := rows.Scan(&r.ID, &r.Username, &r.Action, &r.IP, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
