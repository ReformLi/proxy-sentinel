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
	Role         string
	CreatedAt    time.Time
}

// ProxyToken 对应 proxy_tokens 表
type ProxyToken struct {
	ID         int64
	Token      string
	Name       string
	CreatedAt  time.Time
	LastUsedAt sql.NullTime
	ExpiresAt  sql.NullTime
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
	row := db.QueryRowContext(ctx, `SELECT id, username, password_hash, role, created_at FROM users WHERE username=?`, username)
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UserInfo 用户列表项（不含密码哈希）
type UserInfo struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// ListUsers 列出全部用户（按 ID 正序，不含密码哈希）
func (db *DB) ListUsers(ctx context.Context) ([]UserInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, username, role, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserInfo
	for rows.Next() {
		var u UserInfo
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetUserByID 按 ID 查询用户（不含密码哈希）
func (db *DB) GetUserByID(ctx context.Context, id int64) (*UserInfo, error) {
	var u UserInfo
	err := db.QueryRowContext(ctx, `SELECT id, username, role, created_at FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// DeleteUser 删除用户，返回是否确实删除了记录
func (db *DB) DeleteUser(ctx context.Context, id int64) (bool, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// CreateUser 创建用户账号，role 为 "admin" 或 "viewer"
func (db *DB) CreateUser(ctx context.Context, username, passwordHash, role string) error {
	if role == "" {
		role = "admin"
	}
	_, err := db.ExecContext(ctx, `INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`, username, passwordHash, role)
	return err
}

// UpdateUserRole 更新用户角色
func (db *DB) UpdateUserRole(ctx context.Context, id int64, role string) error {
	_, err := db.ExecContext(ctx, `UPDATE users SET role=? WHERE id=?`, role, id)
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
// 过期 Token 视同无效（SQL 层面排除）；返回 (tokenID, rateLimitRPM, 是否有效, 错误)
func (db *DB) ValidToken(ctx context.Context, token string) (int64, int, bool, error) {
	var id int64
	var rpm int
	err := db.QueryRowContext(ctx, `
		SELECT id, rate_limit_rpm FROM proxy_tokens
		WHERE token=? AND (expires_at IS NULL OR expires_at > datetime('now'))`,
		hashToken(token)).Scan(&id, &rpm)
	if err == sql.ErrNoRows {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	_, _ = db.ExecContext(ctx, `UPDATE proxy_tokens SET last_used_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return id, rpm, true, nil
}

// TokenExists 判断 Token 是否已存在（按哈希比对）
func (db *DB) TokenExists(ctx context.Context, token string) (bool, error) {
	var n int64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM proxy_tokens WHERE token=?`, hashToken(token)).Scan(&n)
	return n > 0, err
}

// AddToken 新增代理 Token（存哈希），rateLimitRPM=0 表示不限流（跟随全局默认）
// expiresAt 为零值时永不过期（存 NULL），否则按 UTC 存入
func (db *DB) AddToken(ctx context.Context, token, name string, rateLimitRPM int, expiresAt time.Time) error {
	var expArg interface{}
	if !expiresAt.IsZero() {
		expArg = expiresAt.UTC()
	}
	_, err := db.ExecContext(ctx, `INSERT INTO proxy_tokens (token, name, rate_limit_rpm, expires_at) VALUES (?, ?, ?, ?)`,
		hashToken(token), name, rateLimitRPM, expArg)
	return err
}

// TokenInfo Token 管理列表项（不含 token 值本身，只返回元数据）
type TokenInfo struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	RateLimitRPM int        `json:"rate_limit_rpm"`
	CreatedAt    time.Time  `json:"created_at"`
	LastUsedAt   *time.Time `json:"last_used_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
}

// ListTokens 列出全部代理 Token 元数据（按创建时间正序）
func (db *DB) ListTokens(ctx context.Context) ([]TokenInfo, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, rate_limit_rpm, created_at, last_used_at, expires_at FROM proxy_tokens ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TokenInfo
	for rows.Next() {
		var t TokenInfo
		var lastUsed sql.NullTime
		var expires sql.NullTime
		if err := rows.Scan(&t.ID, &t.Name, &t.RateLimitRPM, &t.CreatedAt, &lastUsed, &expires); err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			u := lastUsed.Time
			t.LastUsedAt = &u
		}
		if expires.Valid {
			e := expires.Time
			t.ExpiresAt = &e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetToken 按 ID 查询单个 Token 元数据（不存在返回 nil）
func (db *DB) GetToken(ctx context.Context, id int64) (*TokenInfo, error) {
	var t TokenInfo
	var lastUsed sql.NullTime
	var expires sql.NullTime
	err := db.QueryRowContext(ctx,
		`SELECT id, name, rate_limit_rpm, created_at, last_used_at, expires_at FROM proxy_tokens WHERE id=?`, id).
		Scan(&t.ID, &t.Name, &t.RateLimitRPM, &t.CreatedAt, &lastUsed, &expires)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastUsed.Valid {
		u := lastUsed.Time
		t.LastUsedAt = &u
	}
	if expires.Valid {
		e := expires.Time
		t.ExpiresAt = &e
	}
	return &t, nil
}

// UpdateTokenMeta 更新 Token 名称与限流设置（空值表示不修改对应字段）
func (db *DB) UpdateTokenMeta(ctx context.Context, id int64, name string, rateLimitRPM *int) error {
	if name != "" && rateLimitRPM != nil {
		_, err := db.ExecContext(ctx, `UPDATE proxy_tokens SET name=?, rate_limit_rpm=? WHERE id=?`, name, *rateLimitRPM, id)
		return err
	}
	if name != "" {
		_, err := db.ExecContext(ctx, `UPDATE proxy_tokens SET name=? WHERE id=?`, name, id)
		return err
	}
	if rateLimitRPM != nil {
		_, err := db.ExecContext(ctx, `UPDATE proxy_tokens SET rate_limit_rpm=? WHERE id=?`, *rateLimitRPM, id)
		return err
	}
	return nil
}

// DeleteToken 删除（吊销）Token，返回是否确实删除了记录
func (db *DB) DeleteToken(ctx context.Context, id int64) (bool, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM proxy_tokens WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
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
