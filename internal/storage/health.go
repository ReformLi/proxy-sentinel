package storage

import (
	"context"
	"time"
)

// InsertHealthLog 写入一条健康检查探测结果（健康检查器每次探测后调用）
func (db *DB) InsertHealthLog(ctx context.Context, backendURL string, healthy bool, latencyMs int64, statusCode int, errMsg string) error {
	h := 0
	if healthy {
		h = 1
	}
	var sc any
	if statusCode > 0 {
		sc = statusCode
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO backend_health_logs (backend_url, healthy, latency_ms, status_code, error, checked_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		backendURL, h, latencyMs, sc, errMsg, time.Now())
	return err
}

// HealthPoint 探测序列聚合点（按时间桶）
type HealthPoint struct {
	TS         time.Time `json:"ts"`
	Healthy    bool      `json:"healthy"`     // 桶内全部健康才为 true（任一失败即标记不健康，异常时段可见）
	LatencyAvg float64   `json:"latency_avg"` // 桶内平均探测耗时（ms）
	Probes     int64     `json:"probes"`      // 桶内探测次数
}

// BackendHealthSeries 单个后端的探测序列
type BackendHealthSeries struct {
	Backend string        `json:"backend"`
	Points  []HealthPoint `json:"points"`
}

// GetHealthSeries 按后端 + 时间桶聚合探测序列（后端监控页 RTT 折线/不健康色带数据源）
func (db *DB) GetHealthSeries(ctx context.Context, from, to time.Time, bucketSeconds int) ([]BackendHealthSeries, error) {
	if bucketSeconds < 1 {
		bucketSeconds = 60
	}
	q := `SELECT backend_url, ` + sqliteEpochExpr("checked_at") + ` / ? * ? AS bucket,
			MIN(healthy) AS min_h, AVG(latency_ms) AS avg_l, COUNT(*) AS probes
		FROM backend_health_logs WHERE checked_at >= ? AND checked_at <= ?
		GROUP BY backend_url, bucket ORDER BY backend_url, bucket`
	rows, err := db.QueryContext(ctx, q, bucketSeconds, bucketSeconds, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]BackendHealthSeries, 0)
	var cur *BackendHealthSeries
	for rows.Next() {
		var backend string
		var b int64
		var minH int64
		var avgL float64
		var probes int64
		if err := rows.Scan(&backend, &b, &minH, &avgL, &probes); err != nil {
			return nil, err
		}
		if cur == nil || cur.Backend != backend {
			out = append(out, BackendHealthSeries{Backend: backend, Points: []HealthPoint{}})
			cur = &out[len(out)-1]
		}
		cur.Points = append(cur.Points, HealthPoint{
			TS:         time.Unix(b, 0),
			Healthy:    minH == 1,
			LatencyAvg: avgL,
			Probes:     probes,
		})
	}
	return out, rows.Err()
}

// BackendTrafficPoint 单后端流量聚合点（按时间桶，来自 proxy_logs）
type BackendTrafficPoint struct {
	TS          time.Time `json:"ts"`
	Count       int64     `json:"count"`
	ErrorCount  int64     `json:"error_count"`
	AvgDuration float64   `json:"avg_duration"`
}

// BackendTrafficSeries 单个后端的流量序列
type BackendTrafficSeries struct {
	Backend string               `json:"backend"`
	Points  []BackendTrafficPoint `json:"points"`
}

// GetBackendTraffic 按后端 + 时间桶聚合真实流量（请求量/5xx/平均耗时，来自代理日志）
func (db *DB) GetBackendTraffic(ctx context.Context, from, to time.Time, bucketSeconds int) ([]BackendTrafficSeries, error) {
	if bucketSeconds < 1 {
		bucketSeconds = 60
	}
	q := `SELECT backend_url, ` + sqliteEpochExpr("created_at") + ` / ? * ? AS bucket,
			COUNT(*) AS cnt,
			SUM(CASE WHEN status >= 500 THEN 1 ELSE 0 END) AS err,
			COALESCE(AVG(duration), 0) AS avg_d
		FROM proxy_logs WHERE created_at >= ? AND created_at <= ? AND backend_url IS NOT NULL AND backend_url != ''
		GROUP BY backend_url, bucket ORDER BY backend_url, bucket`
	rows, err := db.QueryContext(ctx, q, bucketSeconds, bucketSeconds, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]BackendTrafficSeries, 0)
	var cur *BackendTrafficSeries
	for rows.Next() {
		var backend string
		var b, cnt, errc int64
		var avgd float64
		if err := rows.Scan(&backend, &b, &cnt, &errc, &avgd); err != nil {
			return nil, err
		}
		if cur == nil || cur.Backend != backend {
			out = append(out, BackendTrafficSeries{Backend: backend, Points: []BackendTrafficPoint{}})
			cur = &out[len(out)-1]
		}
		cur.Points = append(cur.Points, BackendTrafficPoint{
			TS:          time.Unix(b, 0),
			Count:       cnt,
			ErrorCount:  errc,
			AvgDuration: avgd,
		})
	}
	return out, rows.Err()
}

// BackendLatencyStat 告警引擎延迟评估用的后端探测统计
type BackendLatencyStat struct {
	Backend string  `json:"backend"`
	AvgMs   float64 `json:"avg_ms"`
	Probes  int64   `json:"probes"`
}

// GetProbeLatencyStats 窗口内各后端的平均探测延迟（仅统计健康探测，失败探测的延迟语义不同）
func (db *DB) GetProbeLatencyStats(ctx context.Context, from time.Time) ([]BackendLatencyStat, error) {
	q := `SELECT backend_url, COALESCE(AVG(latency_ms), 0), COUNT(*)
		FROM backend_health_logs WHERE checked_at >= ? AND healthy = 1
		GROUP BY backend_url`
	rows, err := db.QueryContext(ctx, q, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]BackendLatencyStat, 0)
	for rows.Next() {
		var s BackendLatencyStat
		if err := rows.Scan(&s.Backend, &s.AvgMs, &s.Probes); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteHealthBefore 清理过期的健康检查日志（随日志保留期一起执行）
func (db *DB) DeleteHealthBefore(ctx context.Context, before time.Time) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM backend_health_logs WHERE checked_at < ?`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountHealthLogs 返回当前健康检查日志总条数
func (db *DB) CountHealthLogs(ctx context.Context) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backend_health_logs`).Scan(&n)
	return n, err
}
