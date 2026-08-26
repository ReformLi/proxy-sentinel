package storage

import (
	"context"
	"fmt"
	"time"
)

// RealtimeStats 实时统计指标
type RealtimeStats struct {
	TodayTotal    int64   `json:"today_total"`
	ErrorCount    int64   `json:"error_count"`     // 5xx 数量
	ErrorRate     float64 `json:"error_rate"`       // 5xx 占比
	AvgDuration   float64 `json:"avg_duration"`    // 平均耗时（ms）
	LastMinuteQPS float64 `json:"last_minute_qps"` // 最近 1 分钟 QPS
}

// GetRealtimeStats 计算实时统计指标
func (db *DB) GetRealtimeStats(ctx context.Context) (*RealtimeStats, error) {
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	oneMinAgo := now.Add(-60 * time.Second)

	s := &RealtimeStats{}

	// 今日总请求数
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM proxy_logs WHERE created_at >= ?`, startOfDay).Scan(&s.TodayTotal); err != nil {
		return nil, err
	}
	// 今日 5xx 数量
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM proxy_logs WHERE created_at >= ? AND status >= 500`, startOfDay).Scan(&s.ErrorCount); err != nil {
		return nil, err
	}
	// 今日平均耗时
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(AVG(duration), 0) FROM proxy_logs WHERE created_at >= ?`, startOfDay).Scan(&s.AvgDuration); err != nil {
		return nil, err
	}
	// 最近 1 分钟请求数 -> QPS
	var lastMinCount int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM proxy_logs WHERE created_at >= ?`, oneMinAgo).Scan(&lastMinCount); err != nil {
		return nil, err
	}
	if s.TodayTotal > 0 {
		s.ErrorRate = float64(s.ErrorCount) / float64(s.TodayTotal) * 100
	}
	s.LastMinuteQPS = float64(lastMinCount) / 60.0
	return s, nil
}

// GetWindowCounts 统计指定时间以来的总请求数与 5xx 数量（告警引擎错误率评估用）
func (db *DB) GetWindowCounts(ctx context.Context, from time.Time) (total, errCount int64, err error) {
	q := `SELECT COUNT(*), COALESCE(SUM(CASE WHEN status >= 500 THEN 1 ELSE 0 END), 0)
		FROM proxy_logs WHERE created_at >= ?`
	if err := db.QueryRowContext(ctx, q, from).Scan(&total, &errCount); err != nil {
		return 0, 0, err
	}
	return total, errCount, nil
}

// TrendPoint 趋势数据点
type TrendPoint struct {
	TS          time.Time `json:"ts"`
	Count       int64     `json:"count"`
	ErrorCount  int64     `json:"error_count"`
	AvgDuration float64   `json:"avg_duration"`
}

// sqliteEpochExpr 生成将 DATETIME 列转为真实 UTC epoch 秒的 SQL 表达式。
// modernc.org/sqlite 把 time.Time 存成 Go String() 格式：
//
//	"2026-08-24 12:06:05.1882565 +0800 CST m=+120.57"
//
// 前 19 位是本地墙上时间，其后依次为小数秒、空格、±HHMM 时区偏移、" CST"、m=+ 单调钟后缀。
// 解析：墙上时间按 UTC 取 epoch，再按 ±HHMM 修正；无时区后缀的旧格式（UTC 存储）直接使用。
func sqliteEpochExpr(col string) string {
	// 小数秒之后第一个空格的位置（相对第 20 字符），时区偏移紧跟其后
	sp := fmt.Sprintf("instr(substr(%s, 20), ' ')", col)
	off := fmt.Sprintf("substr(%s, 20 + %s, 5)", col, sp)
	return fmt.Sprintf(
		`CASE WHEN %s > 0 THEN CAST(strftime('%%s', substr(%s, 1, 19)) AS INTEGER)`+
			` - (CASE WHEN substr(%s, 1, 1) = '-' THEN -1 ELSE 1 END)`+
			` * (CAST(substr(%s, 2, 2) AS INTEGER) * 3600 + CAST(substr(%s, 4, 2) AS INTEGER) * 60)`+
			` ELSE CAST(strftime('%%s', substr(%s, 1, 19)) AS INTEGER) END`,
		sp, col, off, off, off, col)
}

// GetTrend 获取请求量与错误趋势（按指定时间桶聚合）
func (db *DB) GetTrend(ctx context.Context, from, to time.Time, bucketSeconds int) ([]TrendPoint, error) {
	if bucketSeconds < 1 {
		bucketSeconds = 60
	}
	epochExpr := db.epochExpr("created_at")
	q := `SELECT ` +
		epochExpr + ` / ? * ? AS bucket, ` +
		`COUNT(*) AS cnt, ` +
		`SUM(CASE WHEN status >= 500 THEN 1 ELSE 0 END) AS err, ` +
		`COALESCE(AVG(duration), 0) AS avg_d ` +
		`FROM proxy_logs WHERE created_at >= ? AND created_at <= ? ` +
		`GROUP BY bucket ORDER BY bucket`

	rows, err := db.QueryContext(ctx, q, bucketSeconds, bucketSeconds, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TrendPoint
	for rows.Next() {
		var b int64
		var cnt, errc int64
		var avgd float64
		if err := rows.Scan(&b, &cnt, &errc, &avgd); err != nil {
			return nil, err
		}
		out = append(out, TrendPoint{
			TS:          time.Unix(b, 0),
			Count:       cnt,
			ErrorCount:  errc,
			AvgDuration: avgd,
		})
	}
	return out, rows.Err()
}

// Percentiles 耗时分位数
type Percentiles struct {
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P99 float64 `json:"p99"`
}

// GetPercentiles 计算指定时间范围内的耗时 P50/P90/P99
// 通过 COUNT + LIMIT/OFFSET 定位分位点，避免全量载入内存（大表下 OOM 风险）
func (db *DB) GetPercentiles(ctx context.Context, from, to time.Time) (*Percentiles, error) {
	var total int64
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM proxy_logs WHERE created_at >= ? AND created_at <= ?`, from, to).Scan(&total); err != nil {
		return nil, err
	}
	if total == 0 {
		return &Percentiles{}, nil
	}
	pick := func(p float64) float64 {
		offset := int64(float64(total-1) * p)
		if offset < 0 {
			offset = 0
		}
		var d int64
		err := db.QueryRowContext(ctx,
			`SELECT duration FROM proxy_logs WHERE created_at >= ? AND created_at <= ? ORDER BY duration ASC LIMIT 1 OFFSET ?`,
			from, to, offset).Scan(&d)
		if err != nil {
			return 0
		}
		return float64(d)
	}
	return &Percentiles{
		P50: pick(0.50),
		P90: pick(0.90),
		P99: pick(0.99),
	}, nil
}

// StatusBucket 状态码分布
type StatusBucket struct {
	Class string `json:"class"` // 2xx / 4xx / 5xx
	Count int64  `json:"count"`
}

// GetStatusDistribution 状态码分布
func (db *DB) GetStatusDistribution(ctx context.Context, from, to time.Time) ([]StatusBucket, error) {
	q := `SELECT
		CASE
			WHEN status < 300 THEN '2xx'
			WHEN status < 400 THEN '3xx'
			WHEN status < 500 THEN '4xx'
			ELSE '5xx'
		END AS cls, COUNT(*) FROM proxy_logs
		WHERE created_at >= ? AND created_at <= ? GROUP BY cls ORDER BY cls`
	rows, err := db.QueryContext(ctx, q, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatusBucket
	for rows.Next() {
		var b StatusBucket
		if err := rows.Scan(&b.Class, &b.Count); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// TopPath 热点路径
type TopPath struct {
	Path  string `json:"path"`
	Count int64  `json:"count"`
}

// GetTopPaths Top N 热点路径
func (db *DB) GetTopPaths(ctx context.Context, from, to time.Time, limit int) ([]TopPath, error) {
	if limit < 1 {
		limit = 10
	}
	q := `SELECT path, COUNT(*) c FROM proxy_logs WHERE created_at >= ? AND created_at <= ? GROUP BY path ORDER BY c DESC LIMIT ?`
	rows, err := db.QueryContext(ctx, q, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TopPath
	for rows.Next() {
		var p TopPath
		if err := rows.Scan(&p.Path, &p.Count); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClientBucket 客户端分布聚合项（按 IP 或 User-Agent）
type ClientBucket struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// GetClientDistribution 客户端分布（by: "ip" | "ua"）
func (db *DB) GetClientDistribution(ctx context.Context, from, to time.Time, by string, limit int) ([]ClientBucket, error) {
	if limit < 1 {
		limit = 10
	}
	col := "client_ip"
	if by == "ua" {
		col = "user_agent"
	}
	q := `SELECT COALESCE(NULLIF(` + col + `, ''), 'unknown') AS k, COUNT(*) c
		FROM proxy_logs WHERE created_at >= ? AND created_at <= ?
		GROUP BY k ORDER BY c DESC LIMIT ?`
	rows, err := db.QueryContext(ctx, q, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClientBucket
	for rows.Next() {
		var b ClientBucket
		if err := rows.Scan(&b.Key, &b.Count); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// FlowNode 数据流向节点聚合
type FlowNode struct {
	BackendURL  string  `json:"backend_url"`
	Count       int64   `json:"count"`
	AvgDuration float64 `json:"avg_duration"`
	ErrorCount  int64   `json:"error_count"`
}

// GetFlowMap 数据流向拓扑数据
func (db *DB) GetFlowMap(ctx context.Context, from, to time.Time) ([]FlowNode, error) {
	q := `SELECT
		COALESCE(backend_url, 'unknown') AS be,
		COUNT(*) AS cnt,
		COALESCE(AVG(duration), 0) AS avg_d,
		SUM(CASE WHEN status >= 500 THEN 1 ELSE 0 END) AS err
		FROM proxy_logs WHERE created_at >= ? AND created_at <= ? GROUP BY be ORDER BY cnt DESC`
	rows, err := db.QueryContext(ctx, q, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FlowNode
	for rows.Next() {
		var n FlowNode
		if err := rows.Scan(&n.BackendURL, &n.Count, &n.AvgDuration, &n.ErrorCount); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
