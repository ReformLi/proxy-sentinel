package storage

import (
	"context"
	"database/sql"
	"encoding/json"
)

// settings 表内置键
const (
	SettingBackends = "backends"          // JSON 数组：运行时管理的后端列表（优先于 config.yaml）
	SettingStrategy = "balancer_strategy" // 负载均衡策略
)

// GetSetting 读取一项设置；不存在时返回 ("", false, nil)
func (db *DB) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// SetSetting 写入（UPSERT）一项设置
func (db *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// GetSettingBackends 读取运行时后端列表
func (db *DB) GetSettingBackends(ctx context.Context) ([]string, bool, error) {
	v, ok, err := db.GetSetting(ctx, SettingBackends)
	if err != nil || !ok {
		return nil, false, err
	}
	var urls []string
	if err := json.Unmarshal([]byte(v), &urls); err != nil {
		return nil, false, nil
	}
	return urls, true, nil
}

// SetSettingBackends 持久化运行时后端列表
func (db *DB) SetSettingBackends(ctx context.Context, urls []string) error {
	b, err := json.Marshal(urls)
	if err != nil {
		return err
	}
	return db.SetSetting(ctx, SettingBackends, string(b))
}
