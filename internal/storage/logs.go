package storage

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// LogRecord 对应 proxy_logs 表的一行
type LogRecord struct {
	ID              int64     `json:"id"`
	Method          string    `json:"method"`
	Path            string    `json:"path"`
	Query           string    `json:"query"`
	RequestHeaders  string    `json:"request_headers"`
	RequestBody     string    `json:"request_body"`
	Status          int       `json:"status"`
	ResponseHeaders string    `json:"response_headers"`
	ResponseBody    string    `json:"response_body"`
	Duration        int64     `json:"duration"`
	ClientIP        string    `json:"client_ip"`
	UserAgent       string    `json:"user_agent"`
	Referer         string    `json:"referer"`
	BackendURL      string    `json:"backend_url"`
	RequestID       string    `json:"request_id"` // 请求链路标记（X-Request-ID），同 ID = 同一次请求
	CreatedAt       time.Time `json:"created_at"`
}

// LogFilter 日志查询过滤条件
type LogFilter struct {
	Start       time.Time
	End         time.Time
	Method      string
	Path        string
	StatusMin   int
	StatusMax   int
	MinDuration int64
	Keyword     string // 在请求体/响应体中搜索
	Backend     string // 精确匹配后端地址（流向图下钻）
	RequestID   string // 精确匹配链路标记（日志详情下钻）
	Page        int
	PageSize    int
}

// InsertLog 插入一条日志记录（供 logger 异步写入调用）
func (db *DB) InsertLog(ctx context.Context, r *LogRecord) error {
	_, err := db.ExecContext(ctx, `INSERT INTO proxy_logs
		(method, path, query, request_headers, request_body, status, response_headers, response_body, duration, client_ip, user_agent, referer, backend_url, request_id, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?);`,
		r.Method, r.Path, r.Query, r.RequestHeaders, r.RequestBody,
		r.Status, r.ResponseHeaders, r.ResponseBody, r.Duration,
		r.ClientIP, r.UserAgent, r.Referer, r.BackendURL, r.RequestID, r.CreatedAt,
	)
	return err
}

// InsertLogs 批量插入日志（单事务提交，避免逐条 fsync 拖垮写入吞吐）
func (db *DB) InsertLogs(ctx context.Context, recs []*LogRecord) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO proxy_logs
		(method, path, query, request_headers, request_body, status, response_headers, response_body, duration, client_ip, user_agent, referer, backend_url, request_id, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?);`)
	if err != nil {
		tx.Rollback()
		return err
	}
	for _, r := range recs {
		if _, err := stmt.ExecContext(ctx,
			r.Method, r.Path, r.Query, r.RequestHeaders, r.RequestBody,
			r.Status, r.ResponseHeaders, r.ResponseBody, r.Duration,
			r.ClientIP, r.UserAgent, r.Referer, r.BackendURL, r.RequestID, r.CreatedAt,
		); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// CountLogs 统计符合过滤条件的日志总数
func (db *DB) CountLogs(ctx context.Context, f LogFilter) (int64, error) {
	q, args := buildLogQuery("SELECT COUNT(*) FROM proxy_logs", f)
	var n int64
	err := db.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

// ListLogs 分页查询日志列表（不含请求/响应体，详情用 GetLog 获取）
func (db *DB) ListLogs(ctx context.Context, f LogFilter) ([]LogRecord, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 200 {
		f.PageSize = 50
	}
	offset := (f.Page - 1) * f.PageSize
	q := `SELECT id, method, path, query, request_headers, request_body, status, response_headers, response_body, duration, client_ip, user_agent, referer, backend_url, request_id, created_at FROM proxy_logs`
	where, whereArgs := buildWhere(f)
	if where != "" {
		q += " " + where
	}
	q += " ORDER BY id DESC LIMIT ? OFFSET ?"

	finalArgs := append(whereArgs, f.PageSize, offset)
	rows, err := db.QueryContext(ctx, q, finalArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLogs(rows)
}

// GetLog 获取单条日志详情
func (db *DB) GetLog(ctx context.Context, id int64) (*LogRecord, error) {
	row := db.QueryRowContext(ctx, `SELECT id, method, path, query, request_headers, request_body, status, response_headers, response_body, duration, client_ip, user_agent, referer, backend_url, request_id, created_at FROM proxy_logs WHERE id=?`, id)
	r := &LogRecord{}
	err := row.Scan(&r.ID, &r.Method, &r.Path, &r.Query, &r.RequestHeaders, &r.RequestBody,
		&r.Status, &r.ResponseHeaders, &r.ResponseBody, &r.Duration,
		&r.ClientIP, &r.UserAgent, &r.Referer, &r.BackendURL, &r.RequestID, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

func buildLogQuery(selectSQL string, f LogFilter) (string, []any) {
	where, args := buildWhere(f)
	q := selectSQL
	if where != "" {
		q += " " + where
	}
	q += " ORDER BY id DESC"
	return q, args
}

func buildWhere(f LogFilter) (string, []any) {
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
	if f.Method != "" {
		conds = append(conds, "method = ?")
		args = append(args, f.Method)
	}
	if f.Path != "" {
		conds = append(conds, "path LIKE ?")
		args = append(args, "%"+f.Path+"%")
	}
	if f.StatusMin > 0 {
		conds = append(conds, "status >= ?")
		args = append(args, f.StatusMin)
	}
	if f.StatusMax > 0 {
		conds = append(conds, "status <= ?")
		args = append(args, f.StatusMax)
	}
	if f.MinDuration > 0 {
		conds = append(conds, "duration >= ?")
		args = append(args, f.MinDuration)
	}
	if f.Keyword != "" {
		conds = append(conds, "(request_body LIKE ? OR response_body LIKE ?)")
		args = append(args, "%"+f.Keyword+"%", "%"+f.Keyword+"%")
	}
	if f.Backend != "" {
		conds = append(conds, "backend_url = ?")
		args = append(args, f.Backend)
	}
	if f.RequestID != "" {
		conds = append(conds, "request_id = ?")
		args = append(args, f.RequestID)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

func scanLogs(rows *sql.Rows) ([]LogRecord, error) {
	var out []LogRecord
	for rows.Next() {
		var r LogRecord
		if err := rows.Scan(&r.ID, &r.Method, &r.Path, &r.Query, &r.RequestHeaders, &r.RequestBody,
			&r.Status, &r.ResponseHeaders, &r.ResponseBody, &r.Duration,
			&r.ClientIP, &r.UserAgent, &r.Referer, &r.BackendURL, &r.RequestID, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteLogsBefore 删除早于指定时间的日志（归档/清理）
func (db *DB) DeleteLogsBefore(ctx context.Context, before time.Time) (int64, error) {
	res, err := db.ExecContext(ctx, "DELETE FROM proxy_logs WHERE created_at < ?", before)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
