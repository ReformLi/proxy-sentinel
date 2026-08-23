package api

import (
	"net/http"
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
