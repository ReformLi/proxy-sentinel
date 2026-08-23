package api

import (
	"encoding/csv"
	"encoding/json"
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
		Backend:   q.Get("backend_url"),
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
			// 用 json.Marshal 序列化，避免 path 等字段含引号/换行时破坏 JSON 结构
			payload, err := json.Marshal(map[string]any{
				"id":          rec.ID,
				"method":      rec.Method,
				"path":        rec.Path,
				"status":      rec.Status,
				"duration":    rec.Duration,
				"backend_url": rec.BackendURL,
				"client_ip":   rec.ClientIP,
				"created_at":  rec.CreatedAt.Format(time.RFC3339),
			})
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", payload)
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
			sanitizeCSV(l.Method),
			sanitizeCSV(l.Path),
			sanitizeCSV(l.Query),
			strconv.Itoa(l.Status),
			strconv.FormatInt(l.Duration, 10),
			sanitizeCSV(l.ClientIP),
			sanitizeCSV(l.UserAgent),
			sanitizeCSV(l.BackendURL),
			l.CreatedAt.Format(time.RFC3339),
		})
	}
	cw.Flush()
}

// sanitizeCSV 防止 CSV 公式注入：以 = + - @ 开头的单元格在 Excel 打开时会被当公式执行，加前缀使其成为纯文本
func sanitizeCSV(s string) string {
	if len(s) == 0 {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@':
		return "'" + s
	}
	return s
}
