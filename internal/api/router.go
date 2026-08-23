package api

import (
	"log"
	"net/http"
	"time"

	"proxy-sentinel/internal/auth"
	"proxy-sentinel/internal/logger"
	"proxy-sentinel/internal/proxy"
	"proxy-sentinel/internal/stats"
	"proxy-sentinel/internal/storage"
)

// Server 装配所有依赖，提供 HTTP 处理器
type Server struct {
	db        *storage.DB
	jwtMgr    *auth.JWTManager
	webAuth   *auth.WebAuthMiddleware
	proxyAuth *auth.ProxyAuthMiddleware
	realtime  *stats.RealtimeService
	trend     *stats.TrendService
	flow      *stats.FlowService
	logWriter *logger.Writer
	balancer  proxy.Balancer
	proxyH    *proxy.Handler
	loginLimit *loginLimiter
	adminUser string
	secure    bool
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
	balancer proxy.Balancer,
	proxyH *proxy.Handler,
	adminUser string,
	secure bool,
) *Server {
	return &Server{
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
		loginLimit: newLoginLimiter(5, 15*60),
		adminUser:  adminUser,
		secure:     secure,
	}
}

// Router 构建带认证中间件的路由
func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	// 健康检查（无认证）
	mux.HandleFunc("GET /health", s.health)

	// 登录/登出（无认证）
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)

	// 静态页面（无认证）
	mux.HandleFunc("GET /login", servePage("login.html"))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	})

	// 受 WebAuth 保护的浏览器页面（未登录跳转 /login）
	webPage := s.webAuth.Middleware(false)
	mux.Handle("GET /dashboard", webPage(http.HandlerFunc(servePage("index.html"))))
	mux.Handle("GET /logs", webPage(http.HandlerFunc(servePage("logs.html"))))
	mux.Handle("GET /flow", webPage(http.HandlerFunc(servePage("flow.html"))))

	// 受 WebAuth 保护的 API（返回 JSON 401）
	webJSON := s.webAuth.Middleware(true)
	mux.Handle("GET /api/stats/realtime", webJSON(http.HandlerFunc(s.realtimeStats)))
	mux.Handle("GET /api/stats/trend", webJSON(http.HandlerFunc(s.trendStats)))
	mux.Handle("GET /api/stats/flow", webJSON(http.HandlerFunc(s.flowStats)))
	mux.Handle("GET /api/logs", webJSON(http.HandlerFunc(s.listLogs)))
	mux.Handle("GET /api/logs/stream", webJSON(http.HandlerFunc(s.streamLogs)))
	mux.Handle("GET /api/logs/export", webJSON(http.HandlerFunc(s.exportCSV)))
	mux.Handle("GET /api/logs/{id}", webJSON(http.HandlerFunc(s.getLog)))

	// 反向代理路由（Bearer Token 认证）
	mux.Handle("/proxy/", s.proxyAuth.Middleware(s.proxyH))

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
