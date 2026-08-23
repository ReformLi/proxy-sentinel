package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"proxy-sentinel/internal/storage"
)

type pagedLogs struct {
	Total    int64                `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
	Data     []storage.LogRecord `json:"data"`
}

// parseLogFilter 从查询参数构建日志过滤条件
func parseLogFilter(r *http.Request) storage.LogFilter {
	q := r.URL.Query()
	f := storage.LogFilter{
		Method:    q.Get("method"),
		Path:      q.Get("path"),
		Keyword:   q.Get("keyword"),
		Page:      atoiDefault(q.Get("page"), 1),
		PageSize:  atoiDefault(q.Get("page_size"), 50),
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
	if v := q.Get("status_min"); v != "" {
		f.StatusMin = atoiDefault(v, 0)
	}
	if v := q.Get("status_max"); v != "" {
		f.StatusMax = atoiDefault(v, 0)
	}
	if v := q.Get("min_duration"); v != "" {
		f.MinDuration = int64(atoiDefault(v, 0))
	}
	return f
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// listLogs GET /api/logs
func (s *Server) listLogs(w http.ResponseWriter, r *http.Request) {
	f := parseLogFilter(r)
	total, err := s.db.CountLogs(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询日志总数失败: "+err.Error())
		return
	}
	logs, err := s.db.ListLogs(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询日志失败: "+err.Error())
		return
	}
	if logs == nil {
		logs = []storage.LogRecord{}
	}
	writeJSON(w, http.StatusOK, pagedLogs{
		Total:    total,
		Page:     f.Page,
		PageSize: f.PageSize,
		Data:     logs,
	})
}

// getLog GET /api/logs/{id}
func (s *Server) getLog(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的日志 ID")
		return
	}
	rec, err := s.db.GetLog(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询日志失败: "+err.Error())
		return
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "日志不存在")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// streamLogs GET /api/logs/stream （SSE 实时日志流）
func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "不支持流式响应")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, cancel := s.logWriter.Subscribe()
	defer cancel()

	// 推送初始心跳
	fmt.Fprintf(w, "event: ping\ndata: {\"ts\":\"%s\"}\n\n", time.Now().Format(time.RFC3339))
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case rec, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: {\"id\":%d,\"method\":\"%s\",\"path\":\"%s\",\"status\":%d,\"duration\":%d,\"backend_url\":\"%s\"}\n\n",
				rec.ID, rec.Method, rec.Path, rec.Status, rec.Duration, rec.BackendURL)
			flusher.Flush()
		}
	}
}

// exportCSV GET /api/logs/export
func (s *Server) exportCSV(w http.ResponseWriter, r *http.Request) {
	f := parseLogFilter(r)
	f.Page = 1
	f.PageSize = 10000 // 单次导出上限
	logs, err := s.db.ListLogs(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询日志失败: "+err.Error())
		return
	}

	filename := fmt.Sprintf("proxy-logs-%s.csv", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "method", "path", "query", "status", "duration_ms", "client_ip", "user_agent", "backend_url", "created_at"})
	for _, l := range logs {
		_ = cw.Write([]string{
			strconv.FormatInt(l.ID, 10),
			l.Method,
			l.Path,
			l.Query,
			strconv.Itoa(l.Status),
			strconv.FormatInt(l.Duration, 10),
			l.ClientIP,
			l.UserAgent,
			l.BackendURL,
			l.CreatedAt.Format(time.RFC3339),
		})
	}
	cw.Flush()
}
