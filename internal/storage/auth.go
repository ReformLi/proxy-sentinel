package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log"
	"time"
)

// User 对应 users 表
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

// ProxyToken 对应 proxy_tokens 表
type ProxyToken struct {
	ID         int64
	Token      string
	Name       string
	CreatedAt  time.Time
	LastUsedAt sql.NullTime
}

// hashToken 代理 Token 采用 SHA-256 哈希后入库，避免数据库文件泄露导致 Token 明文外泄
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// MigrateLegacyTokens 将历史明文 Token 记录转换为哈希存储（一次性兼容迁移）
func (db *DB) MigrateLegacyTokens(ctx context.Context) error {
	rows, err := db.QueryContext(ctx, `SELECT id, token FROM proxy_tokens`)
	if err != nil {
		return err
	}
	type pair struct {
		id    int64
		token string
	}
	var legacy []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.id, &p.token); err != nil {
			rows.Close()
			return err
		}
		// 哈希值为 64 位十六进制字符；其余视为旧明文
		if len(p.token) != 64 {
			legacy = append(legacy, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range legacy {
		if _, err := db.ExecContext(ctx, `UPDATE proxy_tokens SET token=? WHERE id=?`, hashToken(p.token), p.id); err != nil {
			return err
		}
		log.Printf("已将旧明文代理 Token (id=%d, name 前缀=%s) 转换为哈希存储", p.id, p.token[:min(8, len(p.token))])
	}
	return nil
}

// GetUserByUsername 按用户名查询管理员
func (db *DB) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	row := db.QueryRowContext(ctx, `SELECT id, username, password_hash, created_at FROM users WHERE username=?`, username)
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// CreateUser 创建管理员账号
func (db *DB) CreateUser(ctx context.Context, username, passwordHash string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO users (username, password_hash) VALUES (?, ?)`, username, passwordHash)
	return err
}

// UpdatePasswordHash 更新管理员密码哈希（配置文件中修改密码后重启生效）
func (db *DB) UpdatePasswordHash(ctx context.Context, username, passwordHash string) error {
	_, err := db.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE username=?`, passwordHash, username)
	return err
}

// UserExists 判断用户名是否已存在
func (db *DB) UserExists(ctx context.Context, username string) (bool, error) {
	var n int64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE username=?`, username).Scan(&n)
	return n > 0, err
}

// ValidToken 校验代理 Token 是否有效（按哈希比对），有效时更新最后使用时间
func (db *DB) ValidToken(ctx context.Context, token string) (bool, error) {
	var id int64
	err := db.QueryRowContext(ctx, `SELECT id FROM proxy_tokens WHERE token=?`, hashToken(token)).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, _ = db.ExecContext(ctx, `UPDATE proxy_tokens SET last_used_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return true, nil
}

// TokenExists 判断 Token 是否已存在（按哈希比对）
func (db *DB) TokenExists(ctx context.Context, token string) (bool, error) {
	var n int64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM proxy_tokens WHERE token=?`, hashToken(token)).Scan(&n)
	return n > 0, err
}

// AddToken 新增代理 Token（存哈希）
func (db *DB) AddToken(ctx context.Context, token, name string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO proxy_tokens (token, name) VALUES (?, ?)`, hashToken(token), name)
	return err
}

// InsertAudit 记录审计事件
func (db *DB) InsertAudit(ctx context.Context, username, action, ip string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO audit_logs (username, action, ip) VALUES (?, ?, ?)`, username, action, ip)
	return err
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
