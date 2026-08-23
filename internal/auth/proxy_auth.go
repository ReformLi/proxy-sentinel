package auth

import (
	"context"
	"net/http"
	"strings"

	"proxy-sentinel/internal/storage"
)

// ProxyAuthMiddleware 校验 /proxy/* 路由的 Bearer Token
type ProxyAuthMiddleware struct {
	db *storage.DB
}

// NewProxyAuthMiddleware 创建代理认证中间件
func NewProxyAuthMiddleware(db *storage.DB) *ProxyAuthMiddleware {
	return &ProxyAuthMiddleware{db: db}
}

// Middleware 返回一个 HTTP 中间件，验证 Authorization: Bearer <token>
func (m *ProxyAuthMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
			return
		}
		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, `{"error":"invalid authorization scheme, expected Bearer"}`, http.StatusUnauthorized)
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		if token == "" {
			http.Error(w, `{"error":"empty token"}`, http.StatusUnauthorized)
			return
		}

		valid, err := m.db.ValidToken(r.Context(), token)
		if err != nil {
			http.Error(w, `{"error":"token validation failed"}`, http.StatusInternalServerError)
			return
		}
		if !valid {
			http.Error(w, `{"error":"invalid or revoked token"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), tokenKey{}, token)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TokenFromContext 从上下文取出已认证的 token
func TokenFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(tokenKey{}).(string); ok {
		return v
	}
	return ""
}

type tokenKey struct{}
