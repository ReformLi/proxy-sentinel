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
	Backends  []proxy.BackendStat   `json:"backends"`
	Strategy  string                `json:"strategy"`
	Rules     []storage.RouteRule   `json:"rules"`
	Rewrites  []storage.RewriteRule `json:"rewrites"`
	Log       logSettings           `json:"log"`
	Proxy     proxySettings         `json:"proxy"`
	RateLimit rateLimitSettings     `json:"rate_limit"`
	Managed   bool                  `json:"managed"` // 后端列表是否由数据库持久化管理（优先于 config.yaml）
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

type rateLimitSettings struct {
	DefaultRPM int `json:"default_rpm"` // 按 Token 的全局默认限流（0=关闭），在 config.yaml 修改后重启生效
}

type updateBackendsRequest struct {
	Backends []storage.WeightedBackend `json:"backends"`
	Strategy string                    `json:"strategy"`
	Rules    *[]storage.RouteRule      `json:"rules"`     // 指针区分"未传"（保持现状）与"传空数组"（清空规则）
	Rewrites *[]storage.RewriteRule    `json:"rewrites"`  // 同上
}

// getSettings GET /api/settings —— 当前生效配置（含后端健康状态）
func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	_, managedInDB, _ := s.db.GetSettingBackends(r.Context())
	rules, _, _ := s.db.GetSettingRules(r.Context())
	if rules == nil {
		rules = []storage.RouteRule{}
	}
	rewrites, _, _ := s.db.GetSettingRewrites(r.Context())
	if rewrites == nil {
		rewrites = []storage.RewriteRule{}
	}
	writeJSON(w, http.StatusOK, settingsInfo{
		Backends: s.balancer.Backends(),
		Strategy: s.balancer.Strategy(),
		Rules:    rules,
		Rewrites: rewrites,
		Log:      logSettings{
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
		RateLimit: rateLimitSettings{
			DefaultRPM: s.cfg.RateLimit.DefaultRPM,
		},
		Managed: managedInDB,
	})
}

// updateBackends PUT /api/settings/backends —— 运行时增删改后端节点/权重/策略/定向规则并持久化
func (s *Server) updateBackends(w http.ResponseWriter, r *http.Request) {
	var req updateBackendsRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}

	// 校验后端地址与权重
	seen := make(map[string]bool)
	backends := make([]storage.WeightedBackend, 0, len(req.Backends))
	for _, wb := range req.Backends {
		u := strings.TrimSpace(wb.URL)
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
		if wb.Weight < 0 || wb.Weight > 100 {
			writeError(w, http.StatusBadRequest, "权重需在 0~100 之间: "+u)
			return
		}
		seen[u] = true
		backends = append(backends, storage.WeightedBackend{URL: u, Weight: wb.Weight})
	}
	if len(backends) == 0 {
		writeError(w, http.StatusBadRequest, "后端列表不能为空")
		return
	}

	// 策略：不传则保持现状
	strategy := s.balancer.Strategy()
	if req.Strategy != "" {
		if req.Strategy != "round_robin" && req.Strategy != "random" && req.Strategy != "weighted" {
			writeError(w, http.StatusBadRequest, "非法的负载均衡策略（仅支持 round_robin/random/weighted）")
			return
		}
		strategy = req.Strategy
	}
	// weighted 策略下至少一个后端权重 > 0，否则全部流量无后端可去
	if strategy == "weighted" {
		any := false
		for _, wb := range backends {
			if wb.Weight > 0 {
				any = true
				break
			}
		}
		if !any {
			writeError(w, http.StatusBadRequest, "weighted 策略要求至少一个后端权重 > 0")
			return
		}
	}

	// 定向规则：未传保持现状；传入则校验（目标后端必须在新列表中）
	rules := s.proxyH.LoadRules()
	if req.Rules != nil {
		if err := proxy.ValidateRules(*req.Rules, backends); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		rules = *req.Rules
	}

	// 路径重写规则：未传保持现状；传入则校验
	rewrites := s.proxyH.LoadRewrites()
	if req.Rewrites != nil {
		if err := proxy.ValidateRewrites(*req.Rewrites, backends); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		rewrites = *req.Rewrites
	}

	// 1. 持久化到数据库（重启后仍生效，优先于 config.yaml）
	if err := s.db.SetSettingBackends(r.Context(), backends); err != nil {
		writeError(w, http.StatusInternalServerError, "持久化后端列表失败: "+err.Error())
		return
	}
	if err := s.db.SetSetting(r.Context(), storage.SettingStrategy, strategy); err != nil {
		writeError(w, http.StatusInternalServerError, "持久化负载策略失败: "+err.Error())
		return
	}
	if req.Rules != nil {
		if err := s.db.SetSettingRules(r.Context(), rules); err != nil {
			writeError(w, http.StatusInternalServerError, "持久化定向规则失败: "+err.Error())
			return
		}
	}
	if req.Rewrites != nil {
		if err := s.db.SetSettingRewrites(r.Context(), rewrites); err != nil {
			writeError(w, http.StatusInternalServerError, "持久化重写规则失败: "+err.Error())
			return
		}
	}
	// 2. 运行时热更新
	s.balancer.SetBackends(backends)
	if err := s.balancer.SetStrategy(strategy); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Rules != nil {
		s.proxyH.SetRules(rules)
	}
	if req.Rewrites != nil {
		s.proxyH.SetRewrites(rewrites)
	}

	auth.Audit(auditCtx(r), s.db, auth.UsernameFromContext(r.Context()), "settings_backends_updated", ipFromRequest(r))
	writeJSON(w, http.StatusOK, map[string]any{
		"message":  "后端配置已更新（立即生效，重启后保留）",
		"backends": s.balancer.Backends(),
		"strategy": s.balancer.Strategy(),
		"rules":    len(rules),
		"rewrites": len(rewrites),
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
