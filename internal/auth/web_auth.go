package auth

import (
	"context"
	"net/http"
	"time"

	"proxy-sentinel/internal/storage"
)

// CookieName JWT 在浏览器中的 Cookie 名称
const CookieName = "sentinel_token"

type userCtxKey struct{}

// WebAuthMiddleware 校验可视化页面与 API 的 JWT Cookie
type WebAuthMiddleware struct {
	jwt *JWTManager
}

// NewWebAuthMiddleware 创建 Web 认证中间件
func NewWebAuthMiddleware(jm *JWTManager) *WebAuthMiddleware {
	return &WebAuthMiddleware{jwt: jm}
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
			ctx := context.WithValue(r.Context(), userCtxKey{}, claims.Username)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
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

// Audit 记录登录审计事件
func Audit(ctx context.Context, db *storage.DB, username, action, ip string) {
	_ = db.InsertAudit(ctx, username, action, ip)
}
