package api

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"proxy-sentinel/internal/auth"
)

// loginLimiter 防暴力破解：失败次数超限后锁定 IP 一段时间
type loginLimiter struct {
	mu          sync.Mutex
	failures    map[string]int
	lockedUntil map[string]time.Time
	maxFail     int
	lockDur     time.Duration
	done        chan struct{}
}

func newLoginLimiter(maxFail int, lockSec int) *loginLimiter {
	l := &loginLimiter{
		failures:    make(map[string]int),
		lockedUntil: make(map[string]time.Time),
		maxFail:     maxFail,
		lockDur:     time.Duration(lockSec) * time.Second,
		done:        make(chan struct{}),
	}
	go l.cleanup()
	return l
}

// Stop 停止后台清理协程（由 Server.Close 调用）
func (l *loginLimiter) Stop() { close(l.done) }

// allowed 判断 IP 是否被锁定
func (l *loginLimiter) allowed(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if until, ok := l.lockedUntil[ip]; ok {
		if time.Now().Before(until) {
			return false
		}
		delete(l.lockedUntil, ip)
		delete(l.failures, ip)
	}
	return true
}

// fail 记录一次失败，返回是否触发锁定
func (l *loginLimiter) fail(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[ip]++
	if l.failures[ip] >= l.maxFail {
		l.lockedUntil[ip] = time.Now().Add(l.lockDur)
		return true
	}
	return false
}

// success 登录成功，清除该 IP 记录
func (l *loginLimiter) success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, ip)
	delete(l.lockedUntil, ip)
}

func (l *loginLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.mu.Lock()
			now := time.Now()
			for ip, until := range l.lockedUntil {
				if now.After(until) {
					delete(l.lockedUntil, ip)
					delete(l.failures, ip)
				}
			}
			l.mu.Unlock()
		case <-l.done:
			return
		}
	}
}

// ipFromRequest 提取客户端 IP
func ipFromRequest(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// dummyBcryptHash 用于用户不存在时的恒时比较，防止通过响应时间差枚举有效用户名
const dummyBcryptHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// auditCtx 审计日志使用独立于请求生命周期的上下文，客户端提前断开也能落库
func auditCtx(r *http.Request) context.Context {
	return context.WithoutCancel(r.Context())
}

// login POST /api/auth/login
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := ipFromRequest(r)
	if !s.loginLimit.allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "登录失败次数过多，IP 已被锁定 15 分钟")
		return
	}

	var req loginRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "用户名和密码不能为空")
		return
	}

	user, err := s.db.GetUserByUsername(r.Context(), req.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "内部错误")
		return
	}
	if user == nil {
		// 用户不存在：仍执行一次 bcrypt 比较，消除与"密码错误"分支的耗时差
		auth.CheckPassword(dummyBcryptHash, req.Password)
		s.loginLimit.fail(ip)
		auth.Audit(auditCtx(r), s.db, req.Username, "login_failed", ip)
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		s.loginLimit.fail(ip)
		auth.Audit(auditCtx(r), s.db, req.Username, "login_failed", ip)
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	// 登录成功
	s.loginLimit.success(ip)
	token, expiresAt, err := s.jwtMgr.Generate(user.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成令牌失败")
		return
	}
	s.webAuth.SetAuthCookie(w, token, expiresAt)
	auth.Audit(auditCtx(r), s.db, user.Username, "login_success", ip)
	writeJSON(w, http.StatusOK, map[string]string{
		"message":  "登录成功",
		"username": user.Username,
	})
}

// logout POST /api/auth/logout
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	username := auth.UsernameFromContext(r.Context())
	if username == "" {
		// 即便未登录也清除 Cookie
		username = "unknown"
	}
	s.webAuth.ClearAuthCookie(w)
	auth.Audit(auditCtx(r), s.db, username, "logout", ipFromRequest(r))
	writeJSON(w, http.StatusOK, map[string]string{"message": "已登出"})
}
