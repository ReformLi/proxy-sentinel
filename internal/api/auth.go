package api

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"proxy-sentinel/internal/auth"
)

// loginEntry 单个 IP 的失败记录
type loginEntry struct {
	failures    int
	lastFail    time.Time
	lockedUntil time.Time
}

// loginLimiter 防暴力破解：失败次数超限后锁定 IP 一段时间。
// 条目带时间戳，闲置超过 idleTTL 后由后台协程淘汰，防止 map 随攻击者 IP 数无限增长
type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]*loginEntry
	maxFail int
	lockDur time.Duration
	idleTTL time.Duration
	done    chan struct{}
}

func newLoginLimiter(maxFail int, lockSec int) *loginLimiter {
	l := &loginLimiter{
		entries: make(map[string]*loginEntry),
		maxFail: maxFail,
		lockDur: time.Duration(lockSec) * time.Second,
		idleTTL: 15 * time.Minute,
		done:    make(chan struct{}),
	}
	go l.cleanup()
	return l
}

// Stop 停止后台清理协程（由 Server.Close 调用）
func (l *loginLimiter) Stop() { close(l.done) }

// allowed 判断 IP 是否被锁定。
// 仅当该 IP 曾被锁定且锁已过期时清除记录（重新累计失败次数）；
// 未锁定条目的失败计数必须保留，否则计数永远无法达到锁定阈值
func (l *loginLimiter) allowed(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[ip]
	if !ok {
		return true
	}
	if time.Now().Before(e.lockedUntil) {
		return false
	}
	if !e.lockedUntil.IsZero() {
		delete(l.entries, ip)
	}
	return true
}

// fail 记录一次失败，返回是否触发锁定
func (l *loginLimiter) fail(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[ip]
	if !ok {
		e = &loginEntry{}
		l.entries[ip] = e
	}
	e.failures++
	e.lastFail = time.Now()
	if e.failures >= l.maxFail {
		e.lockedUntil = e.lastFail.Add(l.lockDur)
		return true
	}
	return false
}

// success 登录成功，清除该 IP 记录
func (l *loginLimiter) success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, ip)
}

func (l *loginLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.mu.Lock()
			now := time.Now()
			// 锁已过期 且 超过闲置时长未再失败的条目才淘汰，
			// 保证锁定期间（即使超过 idleTTL）不会被提前清除
			for ip, e := range l.entries {
				if now.After(e.lockedUntil) && now.Sub(e.lastFail) > l.idleTTL {
					delete(l.entries, ip)
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
	token, expiresAt, err := s.jwtMgr.Generate(user.Username, user.Role, user.TokenVersion)
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
