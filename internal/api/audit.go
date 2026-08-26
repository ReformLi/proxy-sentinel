package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"proxy-sentinel/internal/storage"
)

type pagedAudits struct {
	Total    int64                `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
	Data     []storage.AuditRecord `json:"data"`
}

// parseAuditFilter 从查询参数构建审计过滤条件
func parseAuditFilter(r *http.Request) storage.AuditFilter {
	q := r.URL.Query()
	f := storage.AuditFilter{
		Username: q.Get("username"),
		Keyword:  q.Get("keyword"),
		IP:       q.Get("ip"),
		Page:     atoiDefault(q.Get("page"), 1),
		PageSize: atoiDefault(q.Get("page_size"), 50),
	}
	if v := q.Get("start"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.Start = t
		}
	}
	if v := q.Get("end"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.End = t
		}
	}
	return f
}

// listAudits GET /api/audit-logs
func (s *Server) listAudits(w http.ResponseWriter, r *http.Request) {
	f := parseAuditFilter(r)
	total, err := s.db.CountAuditsByFilter(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询审计日志总数失败: "+err.Error())
		return
	}
	logs, err := s.db.ListAudits(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询审计日志失败: "+err.Error())
		return
	}
	if logs == nil {
		logs = []storage.AuditRecord{}
	}
	writeJSON(w, http.StatusOK, pagedAudits{
		Total:    total,
		Page:     f.Page,
		PageSize: f.PageSize,
		Data:     logs,
	})
}

// getAudit GET /api/audit-logs/{id}
func (s *Server) getAudit(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的审计日志 ID")
		return
	}
	rec, err := s.db.GetAudit(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询审计日志失败: "+err.Error())
		return
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "审计日志不存在")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// exportAuditsCSV GET /api/audit-logs/export
func (s *Server) exportAuditsCSV(w http.ResponseWriter, r *http.Request) {
	f := parseAuditFilter(r)
	f.Page = 1
	f.PageSize = 20000 // 单次导出上限
	logs, err := s.db.ListAudits(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询审计日志失败: "+err.Error())
		return
	}

	filename := fmt.Sprintf("audit-logs-%s.csv", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "username", "action", "ip", "created_at"})
	for _, a := range logs {
		_ = cw.Write([]string{
			strconv.FormatInt(a.ID, 10),
			sanitizeCSV(a.Username),
			sanitizeCSV(a.Action),
			sanitizeCSV(a.IP),
			a.CreatedAt.Format(time.RFC3339),
		})
	}
	cw.Flush()
}
