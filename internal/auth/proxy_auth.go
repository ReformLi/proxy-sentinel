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

		tokenID, rpm, valid, err := m.db.ValidToken(r.Context(), token)
		if err != nil {
			http.Error(w, `{"error":"token validation failed"}`, http.StatusInternalServerError)
			return
		}
		if !valid {
			http.Error(w, `{"error":"invalid, revoked or expired token"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), tokenKey{}, token)
		ctx = context.WithValue(ctx, tokenIDKey{}, tokenID)
		ctx = context.WithValue(ctx, tokenRPMKey{}, rpm)
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

// TokenIDFromContext 从上下文取出已认证 Token 的数据库 ID（限流计数键）
func TokenIDFromContext(ctx context.Context) int64 {
	if v, ok := ctx.Value(tokenIDKey{}).(int64); ok {
		return v
	}
	return 0
}

// TokenRPMFromContext 从上下文取出该 Token 的独立限流值（0=跟随全局默认）
func TokenRPMFromContext(ctx context.Context) int {
	if v, ok := ctx.Value(tokenRPMKey{}).(int); ok {
		return v
	}
	return 0
}

type tokenKey struct{}
type tokenIDKey struct{}
type tokenRPMKey struct{}
