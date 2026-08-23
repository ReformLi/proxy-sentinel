package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims JWT 载荷
type Claims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// JWTManager 负责签发与校验 JWT
type JWTManager struct {
	secret    []byte
	expiry    time.Duration
	secure    bool // 生产环境启用 Secure Cookie
}

// NewJWTManager 创建 JWT 管理器
func NewJWTManager(secret string, expiry time.Duration, secure bool) *JWTManager {
	return &JWTManager{
		secret: []byte(secret),
		expiry: expiry,
		secure: secure,
	}
}

// Secure 是否启用 Secure Cookie
func (m *JWTManager) Secure() bool { return m.secure }

// Generate 签发 JWT
func (m *JWTManager) Generate(username string) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(m.expiry)
	claims := Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			Subject:   username,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expiresAt, nil
}

// Parse 校验并解析 JWT
func (m *JWTManager) Parse(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("非预期的签名算法: %v", t.Header["alg"])
		}
		return m.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("token 无效")
	}
	return claims, nil
}
