package api

import (
	"bufio"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"proxy-sentinel/internal/alert"
	"proxy-sentinel/internal/auth"
	"proxy-sentinel/internal/config"
	"proxy-sentinel/internal/ipacl"
	"proxy-sentinel/internal/logger"
	"proxy-sentinel/internal/proxy"
	"proxy-sentinel/internal/ratelimit"
	"proxy-sentinel/internal/stats"
	"proxy-sentinel/internal/storage"
	"proxy-sentinel/web"
)

// Server 装配所有依赖，提供 HTTP 处理器
type Server struct {
	db         *storage.DB
	jwtMgr     *auth.JWTManager
	webAuth    *auth.WebAuthMiddleware
	proxyAuth  *auth.ProxyAuthMiddleware
	realtime   *stats.RealtimeService
	trend      *stats.TrendService
	flow       *stats.FlowService
	logWriter  *logger.Writer
	balancer   proxy.DynamicManager
	proxyH     *proxy.Handler
	cfg        *config.Config
	loginLimit *loginLimiter
	limiter    *ratelimit.Limiter
	stopLimiterSweep func()
	alertEngine *alert.Engine
	adminUser  string
	secure     bool
	ipACL      atomic.Pointer[ipacl.List] // 代理入口 IP 黑白名单（编译后只读，原子替换热更新）
}

// NewServer 创建 API Server
func NewServer(
	db *storage.DB,
	jwtMgr *auth.JWTManager,
	webAuth *auth.WebAuthMiddleware,
	proxyAuth *auth.ProxyAuthMiddleware,
	realtime *stats.RealtimeService,
	trend *stats.TrendService,
	flow *stats.FlowService,
	logWriter *logger.Writer,
	balancer proxy.DynamicManager,
	proxyH *proxy.Handler,
	cfg *config.Config,
	alertEngine *alert.Engine,
	adminUser string,
	secure bool,
) *Server {
	limiter := ratelimit.NewLimiter(time.Minute)
	s := &Server{
		db:         db,
		jwtMgr:     jwtMgr,
		webAuth:    webAuth,
		proxyAuth:  proxyAuth,
		realtime:   realtime,
		trend:      trend,
		flow:       flow,
		logWriter:  logWriter,
		balancer:   balancer,
		proxyH:     proxyH,
		cfg:        cfg,
		loginLimit: newLoginLimiter(5, 15*60),
		limiter:    limiter,
		alertEngine: alertEngine,
		adminUser:  adminUser,
		secure:     secure,
	}
	s.stopLimiterSweep = limiter.StartSweeper(10 * time.Minute)
	s.loadIPACL()
	return s
}

// spaIndex 返回前端 SPA 入口页（带基础缓存头）
func spaIndex(w http.ResponseWriter, r *http.Request) {
	html, err := web.Index()
	if err != nil {
		http.Error(w, "前端资源缺失（请先构建 web/frontend）", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(html)
}

// Router 构建带认证中间件的路由
func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	// 健康检查（无认证）
	mux.HandleFunc("GET /health", s.health)

	// 登录/登出（无认证）
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)

	// 前端静态资源（Vite 构建产物：/assets/*、favicon 等）
	static := http.StripPrefix("/", web.Static())
	mux.Handle("GET /assets/", static)
	mux.Handle("GET /favicon.ico", static)
	mux.Handle("GET /favicon.svg", static)

	// 登录页（SPA 无认证路由）
	mux.HandleFunc("GET /login", spaIndex)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	})

	// 受 WebAuth 保护的浏览器页面（未登录 302 跳转 /login）
	webPage := s.webAuth.Middleware(false)
	spa := http.HandlerFunc(spaIndex)
	mux.Handle("GET /dashboard", webPage(spa))
	mux.Handle("GET /logs", webPage(spa))
	mux.Handle("GET /audit-logs", webPage(spa))
	mux.Handle("GET /flow", webPage(spa))
	mux.Handle("GET /backends", webPage(spa))
	mux.Handle("GET /tokens", webPage(spa))
	mux.Handle("GET /users", webPage(spa))
	mux.Handle("GET /settings", webPage(spa))

	// 受 WebAuth 保护的 API（返回 JSON 401）
	webJSON := s.webAuth.Middleware(true)
	mux.Handle("GET /api/auth/me", webJSON(http.HandlerFunc(s.me)))
	mux.Handle("GET /api/stats/realtime", webJSON(http.HandlerFunc(s.realtimeStats)))
	mux.Handle("GET /api/stats/trend", webJSON(http.HandlerFunc(s.trendStats)))
	mux.Handle("GET /api/stats/flow", webJSON(http.HandlerFunc(s.flowStats)))
	mux.Handle("GET /api/stats/clients", webJSON(http.HandlerFunc(s.clientStats)))
	mux.Handle("GET /api/stats/backends", webJSON(http.HandlerFunc(s.backendsStats)))
	mux.Handle("GET /api/logs", webJSON(http.HandlerFunc(s.listLogs)))
	mux.Handle("GET /api/logs/stream", webJSON(http.HandlerFunc(s.streamLogs)))
	mux.Handle("GET /api/logs/export", webJSON(http.HandlerFunc(s.exportCSV)))
	mux.Handle("GET /api/logs/{id}", webJSON(http.HandlerFunc(s.getLog)))

	// 审计日志（只读列表，管理员可读）
	mux.Handle("GET /api/audit-logs", webJSON(http.HandlerFunc(s.listAudits)))
	mux.Handle("GET /api/audit-logs/export", webJSON(http.HandlerFunc(s.exportAuditsCSV)))
	mux.Handle("GET /api/audit-logs/{id}", webJSON(http.HandlerFunc(s.getAudit)))

	// 配置管理（写操作审计）
	mux.Handle("GET /api/settings", webJSON(http.HandlerFunc(s.getSettings)))
	mux.Handle("PUT /api/settings/backends", webJSON(auth.AdminOnly(http.HandlerFunc(s.updateBackends))))

	// Token 管理（写操作审计）
	mux.Handle("GET /api/tokens", webJSON(http.HandlerFunc(s.listTokens)))
	mux.Handle("POST /api/tokens", webJSON(auth.AdminOnly(http.HandlerFunc(s.createToken))))
	mux.Handle("PUT /api/tokens/{id}", webJSON(auth.AdminOnly(http.HandlerFunc(s.updateToken))))
	mux.Handle("DELETE /api/tokens/{id}", webJSON(auth.AdminOnly(http.HandlerFunc(s.deleteToken))))

	// 用户管理（写操作审计）
	mux.Handle("GET /api/users", webJSON(http.HandlerFunc(s.listUsers)))
	mux.Handle("POST /api/users", webJSON(auth.AdminOnly(http.HandlerFunc(s.createUser))))
	mux.Handle("DELETE /api/users/{id}", webJSON(auth.AdminOnly(http.HandlerFunc(s.deleteUser))))
	mux.Handle("PUT /api/users/{id}/password", webJSON(auth.AdminOnly(http.HandlerFunc(s.resetPassword))))
	mux.Handle("PUT /api/users/{id}/role", webJSON(auth.AdminOnly(http.HandlerFunc(s.updateRole))))

	// 告警通知（规则热更新 + 连通性测试）
	mux.Handle("GET /api/alert/config", webJSON(http.HandlerFunc(s.getAlertConfig)))
	mux.Handle("PUT /api/alert/config", webJSON(auth.AdminOnly(http.HandlerFunc(s.updateAlertConfig))))
	mux.Handle("POST /api/alert/test", webJSON(auth.AdminOnly(http.HandlerFunc(s.testAlert))))

	// IP 黑白名单（保存即热生效）
	mux.Handle("GET /api/ip-acl", webJSON(http.HandlerFunc(s.getIPACL)))
	mux.Handle("PUT /api/ip-acl", webJSON(auth.AdminOnly(http.HandlerFunc(s.updateIPACL))))

	// 数据维护（统计 + 手动清理）
	mux.Handle("GET /api/maintenance/stats", webJSON(http.HandlerFunc(s.getMaintenanceStats)))
	mux.Handle("POST /api/maintenance/purge", webJSON(auth.AdminOnly(http.HandlerFunc(s.postMaintenancePurge))))

	// 反向代理路由（IP 黑白名单 → Bearer Token 认证 → 按 Token 限流）
	mux.Handle("/proxy/", s.ipACLMiddleware(s.proxyAuth.Middleware(s.rateLimitMiddleware(s.proxyH))))

	return accessLogger(mux)
}

// health 健康检查端点
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// accessLogger 基础访问日志（stdout）
func accessLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(ww, r)
		log.Printf("%s %s %d %s %s", r.Method, r.URL.Path, ww.status, time.Since(start), r.RemoteAddr)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status     int
	wroteHead bool
}

func (s *statusWriter) WriteHeader(code int) {
	if s.wroteHead {
		return
	}
	s.wroteHead = true
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if !s.wroteHead {
		s.wroteHead = true
	}
	return s.ResponseWriter.Write(b)
}

// Flush 透传 Flush，保证 SSE/流式响应不被 accessLogger 包装后失效
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack 透传连接劫持，支持 WebSocket 升级等场景
func (s *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := s.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Close 释放 Server 持有的后台资源
func (s *Server) Close() {
	s.loginLimit.Stop()
	if s.stopLimiterSweep != nil {
		s.stopLimiterSweep()
	}
}
