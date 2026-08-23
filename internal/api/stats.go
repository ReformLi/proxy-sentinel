package api

import (
	"net/http"
	"time"

	"proxy-sentinel/internal/stats"
	"proxy-sentinel/internal/storage"
)

// realtimeStats GET /api/stats/realtime
func (s *Server) realtimeStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.realtime.Get(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "获取实时统计失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// trendStats GET /api/stats/trend?window=1h|24h|7d
func (s *Server) trendStats(w http.ResponseWriter, r *http.Request) {
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "1h"
	}
	data, err := s.trend.Get(r.Context(), window)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "获取趋势数据失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, data)
}

// flowStats GET /api/stats/flow?window=24h
func (s *Server) flowStats(w http.ResponseWriter, r *http.Request) {
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "24h"
	}
	nodes, err := s.flow.Get(r.Context(), window)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "获取流向数据失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

// clientStats GET /api/stats/clients?window=24h&by=ip|ua 客户端分布
func (s *Server) clientStats(w http.ResponseWriter, r *http.Request) {
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "24h"
	}
	by := r.URL.Query().Get("by")
	if by != "ua" {
		by = "ip"
	}
	from, _ := stats.ParseWindow(window)
	items, err := s.db.GetClientDistribution(r.Context(), from, time.Now(), by, 10)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "获取客户端分布失败: "+err.Error())
		return
	}
	if items == nil {
		items = []storage.ClientBucket{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"by": by, "items": items})
}
