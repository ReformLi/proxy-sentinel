package api

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"

	"proxy-sentinel/internal/auth"
	"proxy-sentinel/internal/ipacl"
)

// loadIPACL 启动时从数据库加载名单（失败不阻断启动，仅空名单兜底 = 不拦截）；
// 自动兼容旧单名单格式（blacklist/whitelist 三态 → 双名单语义）
func (s *Server) loadIPACL() {
	raw, ok, err := s.db.GetSettingIPACL(context.Background())
	if err != nil || !ok {
		if err != nil {
			log.Printf("⚠ 加载 IP 名单失败：%v（暂不拦截，可在管理页重新保存）", err)
		}
		return
	}
	cfg, err := ipacl.ParseConfig([]byte(raw))
	if err != nil {
		log.Printf("⚠ IP 名单配置损坏：%v（暂不拦截，可在管理页重新保存）", err)
		return
	}
	l, err := ipacl.Compile(cfg)
	if err != nil {
		log.Printf("⚠ IP 名单配置非法：%v（暂不拦截，可在管理页重新保存）", err)
		return
	}
	s.ipACL.Store(l)
	log.Printf("已加载 IP 名单：模式=%s，默认动作=%s，黑名单=%d 条，白名单=%d 条",
		l.Mode(), l.Default(), len(cfg.Blacklist), len(cfg.Whitelist))
}

// getIPACL GET /api/ip-acl —— 读取名单配置（旧格式自动转换后返回）
func (s *Server) getIPACL(w http.ResponseWriter, r *http.Request) {
	raw, ok, err := s.db.GetSettingIPACL(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取 IP 名单失败: "+err.Error())
		return
	}
	cfg := ipacl.Config{Mode: ipacl.ModeOff, Default: ipacl.DefaultAllow, Blacklist: []ipacl.Entry{}, Whitelist: []ipacl.Entry{}}
	if ok {
		parsed, err := ipacl.ParseConfig([]byte(raw))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "IP 名单配置损坏，请重新保存")
			return
		}
		cfg = parsed
	}
	writeJSON(w, http.StatusOK, cfg)
}

// updateIPACL PUT /api/ip-acl —— 校验、持久化并热更新名单
func (s *Server) updateIPACL(w http.ResponseWriter, r *http.Request) {
	var req ipacl.Config
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if req.Blacklist == nil {
		req.Blacklist = []ipacl.Entry{}
	}
	if req.Whitelist == nil {
		req.Whitelist = []ipacl.Entry{}
	}
	// 先编译校验：任何非法条目拒绝整份提交，避免"半生效"
	l, err := ipacl.Compile(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	b, err := json.Marshal(req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "序列化失败")
		return
	}
	if err := s.db.SetSettingIPACL(r.Context(), string(b)); err != nil {
		writeError(w, http.StatusInternalServerError, "保存 IP 名单失败: "+err.Error())
		return
	}
	s.ipACL.Store(l) // 原子替换，请求路径无锁热生效

	auth.Audit(auditCtx(r), s.db, auth.UsernameFromContext(r.Context()), "ip_acl_updated", ipFromRequest(r))
	writeJSON(w, http.StatusOK, map[string]any{
		"message":   "IP 名单已保存，立即生效",
		"mode":      req.Mode,
		"default":   req.Default,
		"blacklist": len(req.Blacklist),
		"whitelist": len(req.Whitelist),
	})
}

// ipACLMiddleware 代理入口 IP 黑白名单拦截：挂在认证之前，拒绝的请求不消耗任何下游资源。
// 判定使用 TCP 直连 IP（不读 XFF 头），防止客户端伪造 X-Forwarded-For 绕过名单。
func (s *Server) ipACLMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := s.ipACL.Load()
		if l == nil {
			next.ServeHTTP(w, r) // 未配置名单
			return
		}
		ip := directIP(r.RemoteAddr)
		if !l.Allowed(ip) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"ip not allowed"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// directIP 从 RemoteAddr 提取主机部分（ACL 专用：绝不信任请求头）
func directIP(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return strings.TrimSpace(host)
	}
	return strings.TrimSpace(remoteAddr)
}
