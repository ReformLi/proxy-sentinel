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

// backendMonitorItem GET /api/stats/backends 响应中的单后端监控数据
type backendMonitorItem struct {
	Backend    string                        `json:"backend"`
	Healthy    bool                          `json:"healthy"`
	HealthPath string                        `json:"health_path"`
	UptimePct  float64                       `json:"uptime_pct"` // 窗口内探测可用率（%）
	Probes     []storage.HealthPoint         `json:"probes"`     // 探测序列（RTT/健康）
	Traffic    []storage.BackendTrafficPoint `json:"traffic"`    // 真实流量序列（请求/5xx/耗时）
}

// backendsStats GET /api/stats/backends?window=1h|24h|7d —— 后端健康与流量监控数据
func (s *Server) backendsStats(w http.ResponseWriter, r *http.Request) {
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "24h"
	}
	from, bucket := stats.ParseWindow(window)
	// 监控窗口适当放大桶宽：1h→1min，24h→10min，7d→30min（点位 60~336，前端可直接渲染）
	switch window {
	case "1h":
		bucket = 60
	case "24h":
		bucket = 600
	case "7d":
		bucket = 1800
	}
	to := time.Now()

	healthSeries, err := s.db.GetHealthSeries(r.Context(), from, to, bucket)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "获取探测序列失败: "+err.Error())
		return
	}
	healthMap := make(map[string][]storage.HealthPoint, len(healthSeries))
	for _, hs := range healthSeries {
		healthMap[hs.Backend] = hs.Points
	}
	trafficSeries, err := s.db.GetBackendTraffic(r.Context(), from, to, bucket)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "获取流量序列失败: "+err.Error())
		return
	}
	trafficMap := make(map[string][]storage.BackendTrafficPoint, len(trafficSeries))
	for _, ts := range trafficSeries {
		trafficMap[ts.Backend] = ts.Points
	}

	items := make([]backendMonitorItem, 0)
	for _, be := range s.balancer.Backends() {
		item := backendMonitorItem{
			Backend:    be.URL,
			Healthy:    be.Healthy,
			HealthPath: be.HealthPath,
			Probes:     healthMap[be.URL],
			Traffic:    trafficMap[be.URL],
		}
		if item.Probes == nil {
			item.Probes = []storage.HealthPoint{}
		}
		if item.Traffic == nil {
			item.Traffic = []storage.BackendTrafficPoint{}
		}
		// 可用率 = 健康桶占比（按桶而非按次，与图形展示一致）
		if n := len(item.Probes); n > 0 {
			ok := 0
			for _, p := range item.Probes {
				if p.Healthy {
					ok++
				}
			}
			item.UptimePct = float64(ok) / float64(n) * 100
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"window": window, "items": items})
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
