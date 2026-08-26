package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"proxy-sentinel/internal/auth"
)

// tableStat 单表状态
type tableStat struct {
	Table         string `json:"table"`          // 表名标识
	Label         string `json:"label"`          // 中文标签
	Count         int64  `json:"count"`          // 当前条数
	SizeBytes     int64  `json:"size_bytes"`     // 估算大小（字节）
	RetentionDays int    `json:"retention_days"` // 自动保留期天数（0=不自动清理）
	TimeColumn    string `json:"time_column"`    // 时间列名
}

// maintenanceStats GET /api/maintenance/stats 响应
type maintenanceStats struct {
	DBSizeBytes int64       `json:"db_size_bytes"` // 数据库文件总大小（字节）
	Tables      []tableStat `json:"tables"`
}

// purgeRequest POST /api/maintenance/purge 请求体
type purgeRequest struct {
	Tables   []string `json:"tables"`    // 要清理的表（log/health/audit）
	KeepDays int      `json:"keep_days"` // 保留最近 N 天（必填，>0）
	Confirm  bool     `json:"confirm"`   // 必须 true（二次确认保护）
}

// purgeResult POST /api/maintenance/purge 响应
type purgeResult struct {
	Deleted map[string]int64 `json:"deleted"` // table -> 删除条数
}

const (
	tableLog    = "log"
	tableHealth = "health"
	tableAudit  = "audit"
)

// getMaintenanceStats GET /api/maintenance/stats —— 返回三表条数/大小与自动保留期
func (s *Server) getMaintenanceStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	dbSize, _ := s.db.DatabaseSize(ctx)

	logCount, _ := s.db.CountAllLogs(ctx)
	healthCount, _ := s.db.CountHealthLogs(ctx)
	auditCount, _ := s.db.CountAudits(ctx)

	logSize, _ := s.db.TableSize(ctx, "proxy_logs")
	healthSize, _ := s.db.TableSize(ctx, "backend_health_logs")
	auditSize, _ := s.db.TableSize(ctx, "audit_logs")

	tables := []tableStat{
		{
			Table:         tableLog,
			Label:         "代理日志 (proxy_logs)",
			Count:         logCount,
			SizeBytes:     logSize,
			RetentionDays: s.cfg.Log.RetentionDays,
			TimeColumn:    "created_at",
		},
		{
			Table:         tableHealth,
			Label:         "健康检查 (backend_health_logs)",
			Count:         healthCount,
			SizeBytes:     healthSize,
			RetentionDays: s.cfg.Log.HealthRetentionDays,
			TimeColumn:    "checked_at",
		},
		{
			Table:         tableAudit,
			Label:         "审计日志 (audit_logs)",
			Count:         auditCount,
			SizeBytes:     auditSize,
			RetentionDays: s.cfg.Log.AuditRetentionDays,
			TimeColumn:    "created_at",
		},
	}

	writeJSON(w, http.StatusOK, maintenanceStats{
		DBSizeBytes: dbSize,
		Tables:      tables,
	})
}

// postMaintenancePurge POST /api/maintenance/purge —— 按保留天数手动清理指定表
func (s *Server) postMaintenancePurge(w http.ResponseWriter, r *http.Request) {
	var req purgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.KeepDays <= 0 {
		writeError(w, http.StatusBadRequest, "keep_days 必须大于 0")
		return
	}
	if !req.Confirm {
		writeError(w, http.StatusBadRequest, "未勾选确认，已阻止删除")
		return
	}
	if len(req.Tables) == 0 {
		writeError(w, http.StatusBadRequest, "至少选择一个要清理的表")
		return
	}

	username := auth.UsernameFromContext(r.Context())
	before := time.Now().AddDate(0, 0, -req.KeepDays)
	deleted := map[string]int64{}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute) // DELETE 可能慢
	defer cancel()

	for _, t := range req.Tables {
		var (
			n   int64
			err error
		)
		switch t {
		case tableLog:
			n, err = s.db.DeleteLogsBefore(ctx, before)
		case tableHealth:
			n, err = s.db.DeleteHealthBefore(ctx, before)
		case tableAudit:
			n, err = s.db.DeleteAuditsBefore(ctx, before)
		default:
			continue // 忽略未知表名
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("清理 %s 失败: %v", t, err))
			return
		}
		deleted[t] = n
		auth.Audit(auditCtx(r), s.db, username,
			fmt.Sprintf("手动清理 %s：删除 %d 条（保留最近 %d 天）", t, n, req.KeepDays),
			ipFromRequest(r))
	}

	// 清理后回收空间（SQLite: VACUUM / MySQL: OPTIMIZE TABLE / PostgreSQL: VACUUM ANALYZE）
	_ = s.db.Vacuum(ctx)

	writeJSON(w, http.StatusOK, purgeResult{Deleted: deleted})
}
