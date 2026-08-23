package api

import (
	"net/http"
	"net/url"
	"strings"

	"proxy-sentinel/internal/auth"
	"proxy-sentinel/internal/proxy"
	"proxy-sentinel/internal/storage"
)

// settingsInfo GET /api/settings 响应结构
type settingsInfo struct {
	Backends []proxy.BackendStat `json:"backends"`
	Strategy string              `json:"strategy"`
	Log      logSettings         `json:"log"`
	Proxy    proxySettings       `json:"proxy"`
	Managed  bool                `json:"managed"` // 后端列表是否由数据库持久化管理（优先于 config.yaml）
}

type logSettings struct {
	Level         string  `json:"level"`
	SampleRate    float64 `json:"sample_rate"`
	RetentionDays int     `json:"retention_days"`
	MaskSensitive bool    `json:"mask_sensitive"`
	BodyMaxBytes  int     `json:"body_max_bytes"`
}

type proxySettings struct {
	TimeoutSeconds        int  `json:"timeout_seconds"`
	MaxBodyBytes          int  `json:"max_body_bytes"`
	TrustForwardedHeaders bool `json:"trust_forwarded_headers"`
}

type updateBackendsRequest struct {
	Backends []string `json:"backends"`
	Strategy string   `json:"strategy"`
}

// getSettings GET /api/settings —— 当前生效配置（含后端健康状态）
func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	_, managedInDB, _ := s.db.GetSettingBackends(r.Context())
	writeJSON(w, http.StatusOK, settingsInfo{
		Backends: s.balancer.Backends(),
		Strategy: s.balancer.Strategy(),
		Log: logSettings{
			Level:         s.cfg.Log.Level,
			SampleRate:    s.cfg.Log.SampleRate,
			RetentionDays: s.cfg.Log.RetentionDays,
			MaskSensitive: s.cfg.Log.MaskSensitive,
			BodyMaxBytes:  s.cfg.Log.BodyMaxBytes,
		},
		Proxy: proxySettings{
			TimeoutSeconds:        s.cfg.Proxy.TimeoutSeconds,
			MaxBodyBytes:          s.cfg.Proxy.MaxBodyBytes,
			TrustForwardedHeaders: s.cfg.Proxy.TrustForwardedHeaders,
		},
		Managed: managedInDB,
	})
}

// updateBackends PUT /api/settings/backends —— 运行时增删改后端节点并持久化
func (s *Server) updateBackends(w http.ResponseWriter, r *http.Request) {
	var req updateBackendsRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}

	// 校验后端地址
	seen := make(map[string]bool)
	urls := make([]string, 0, len(req.Backends))
	for _, raw := range req.Backends {
		u := strings.TrimSpace(raw)
		if u == "" {
			continue
		}
		if seen[u] {
			writeError(w, http.StatusBadRequest, "存在重复的后端地址: "+u)
			return
		}
		parsed, err := url.Parse(u)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			writeError(w, http.StatusBadRequest, "非法的后端地址（需含 scheme 与 host）: "+u)
			return
		}
		seen[u] = true
		urls = append(urls, u)
	}
	if len(urls) == 0 {
		writeError(w, http.StatusBadRequest, "后端列表不能为空")
		return
	}

	// 策略：不传则保持现状
	strategy := s.balancer.Strategy()
	if req.Strategy != "" {
		if req.Strategy != "round_robin" && req.Strategy != "random" {
			writeError(w, http.StatusBadRequest, "非法的负载均衡策略（仅支持 round_robin/random）")
			return
		}
		strategy = req.Strategy
	}

	// 1. 持久化到数据库（重启后仍生效，优先于 config.yaml）
	if err := s.db.SetSettingBackends(r.Context(), urls); err != nil {
		writeError(w, http.StatusInternalServerError, "持久化后端列表失败: "+err.Error())
		return
	}
	if err := s.db.SetSetting(r.Context(), storage.SettingStrategy, strategy); err != nil {
		writeError(w, http.StatusInternalServerError, "持久化负载策略失败: "+err.Error())
		return
	}
	// 2. 运行时热更新
	s.balancer.SetBackends(urls)
	if err := s.balancer.SetStrategy(strategy); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	auth.Audit(auditCtx(r), s.db, auth.UsernameFromContext(r.Context()), "settings_backends_updated", ipFromRequest(r))
	writeJSON(w, http.StatusOK, map[string]any{
		"message":  "后端配置已更新（立即生效，重启后保留）",
		"backends": s.balancer.Backends(),
		"strategy": s.balancer.Strategy(),
	})
}

// me GET /api/auth/me —— 当前登录用户（前端会话探测）
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	username := auth.UsernameFromContext(r.Context())
	if username == "" {
		username = s.adminUser
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": username})
}
