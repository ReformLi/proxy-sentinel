package storage

import (
	"context"
	"database/sql"
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

// UserExists 判断用户名是否已存在
func (db *DB) UserExists(ctx context.Context, username string) (bool, error) {
	var n int64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE username=?`, username).Scan(&n)
	return n > 0, err
}

// ValidToken 校验代理 Token 是否有效，有效时更新最后使用时间
func (db *DB) ValidToken(ctx context.Context, token string) (bool, error) {
	var id int64
	err := db.QueryRowContext(ctx, `SELECT id FROM proxy_tokens WHERE token=?`, token).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, _ = db.ExecContext(ctx, `UPDATE proxy_tokens SET last_used_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return true, nil
}

// TokenExists 判断 Token 是否已存在
func (db *DB) TokenExists(ctx context.Context, token string) (bool, error) {
	var n int64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM proxy_tokens WHERE token=?`, token).Scan(&n)
	return n > 0, err
}

// AddToken 新增代理 Token
func (db *DB) AddToken(ctx context.Context, token, name string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO proxy_tokens (token, name) VALUES (?, ?)`, token, name)
	return err
}

// InsertAudit 记录审计事件
func (db *DB) InsertAudit(ctx context.Context, username, action, ip string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO audit_logs (username, action, ip) VALUES (?, ?, ?)`, username, action, ip)
	return err
}
