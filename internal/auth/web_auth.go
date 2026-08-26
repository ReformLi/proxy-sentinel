package auth

import (
	"context"
	"net/http"
	"sync"
	"time"

	"proxy-sentinel/internal/storage"
)

// CookieName JWT 在浏览器中的 Cookie 名称
const CookieName = "sentinel_token"

// 缓存用户存在性与令牌版本检查结果，避免每请求查 DB
const userCacheTTL = 30 * time.Second

type cacheEntry struct {
	exists    bool
	version   int // 查询时刻的 token_version（用于踢出旧 JWT）
	checkedAt time.Time
}

type userCtxKey struct{}
type roleCtxKey struct{}

// WebAuthMiddleware 校验可视化页面与 API 的 JWT Cookie
type WebAuthMiddleware struct {
	jwt   *JWTManager
	db    *storage.DB
	cache sync.Map // username → cacheEntry
}

// NewWebAuthMiddleware 创建 Web 认证中间件
func NewWebAuthMiddleware(jm *JWTManager, db *storage.DB) *WebAuthMiddleware {
	return &WebAuthMiddleware{jwt: jm, db: db}
}

// SetAuthCookie 将 JWT 写入 HttpOnly Cookie
func (m *WebAuthMiddleware) SetAuthCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.jwt.Secure(),
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})
}

// ClearAuthCookie 清除认证 Cookie
func (m *WebAuthMiddleware) ClearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.jwt.Secure(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// Middleware 保护 /dashboard/* 与 /api/* 路由
// acceptJSON=true 时未认证返回 401 JSON，否则 302 跳转 /login（适配浏览器页面）
func (m *WebAuthMiddleware) Middleware(acceptJSON bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(CookieName)
			if err != nil || c.Value == "" {
				m.unauthorized(w, r, acceptJSON)
				return
			}
			claims, err := m.jwt.Parse(c.Value)
			if err != nil {
				m.unauthorized(w, r, acceptJSON)
				return
			}
			// 老令牌无 role 字段：按最小权限视为 viewer（而非 admin），
			// 防止升级部署后旧版签发的令牌自动获得管理员权限；重新登录即恢复正常角色
			role := claims.Role
			if role == "" {
				role = "viewer"
			}
			// 验证用户是否仍存在且令牌版本未变（内存缓存 + 30s TTL）：
			// 密码重置/角色变更会使 token_version+1，旧 JWT 立即失效，强制重新登录
			exists, version := m.userState(r.Context(), claims.Username)
			if !exists {
				m.ClearAuthCookie(w)
				m.unauthorized(w, r, acceptJSON)
				return
			}
			if version != claims.TokenVersion {
				m.ClearAuthCookie(w)
				m.unauthorized(w, r, acceptJSON)
				return
			}
			ctx := context.WithValue(r.Context(), userCtxKey{}, claims.Username)
			ctx = context.WithValue(ctx, roleCtxKey{}, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// userState 检查用户是否存在并返回其当前令牌版本（30s 内存缓存）
func (m *WebAuthMiddleware) userState(ctx context.Context, username string) (bool, int) {
	if v, ok := m.cache.Load(username); ok {
		entry := v.(cacheEntry)
		if time.Since(entry.checkedAt) < userCacheTTL {
			return entry.exists, entry.version
		}
	}
	exists := false
	version := 0
	if u, err := m.db.GetUserByUsername(ctx, username); err == nil && u != nil {
		exists = true
		version = u.TokenVersion
	}
	m.cache.Store(username, cacheEntry{exists: exists, version: version, checkedAt: time.Now()})
	return exists, version
}

// InvalidateUser 清除指定用户的缓存（删除用户时调用，立即生效）
func (m *WebAuthMiddleware) InvalidateUser(username string) {
	m.cache.Delete(username)
}

func (m *WebAuthMiddleware) unauthorized(w http.ResponseWriter, r *http.Request, acceptJSON bool) {
	if acceptJSON {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}

// UsernameFromContext 从上下文取出已认证用户名
func UsernameFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(userCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// RoleFromContext 从上下文取出用户角色（admin / viewer）
func RoleFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(roleCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// AdminOnly 仅允许 admin 角色访问的中间件；viewer 返回 403
func AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RoleFromContext(r.Context()) != "admin" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"forbidden: admin only"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Audit 记录登录审计事件
func Audit(ctx context.Context, db *storage.DB, username, action, ip string) {
	_ = db.InsertAudit(ctx, username, action, ip)
}
