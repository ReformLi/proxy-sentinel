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
	SettingIPACL    = "ip_acl"            // JSON 对象：代理入口 IP 黑白名单（模式 + 条目）
	SettingRules     = "route_rules"      // JSON 数组：定向分流规则（灰度发布）
	SettingRewrites  = "path_rewrites"    // JSON 数组：路径重写规则
)

// WeightedBackend 带权重的后端（灰度发布：权重 = 流量比例，0 = 不接流量但保留健康检查）
type WeightedBackend struct {
	URL        string `json:"url"`
	Weight     int    `json:"weight"`      // 0~100；round_robin/random 策略下忽略
	HealthPath string `json:"health_path"` // 健康检查探测路径（空 = /）
}

// RouteRule 定向分流规则：命中的请求固定路由到指定后端（优先于负载均衡策略）
type RouteRule struct {
	Type    string `json:"type"`    // header | cookie | path
	Key     string `json:"key"`     // header/cookie 名；path 类型时留空
	Value   string `json:"value"`   // header/cookie 精确匹配；path 前缀匹配
	Backend string `json:"backend"` // 命中后路由到的后端 URL
}

// RewriteRule 路径重写规则：前缀替换（Nginx proxy_pass 风格）
type RewriteRule struct {
	Prefix      string `json:"prefix"`      // 匹配的路径前缀（以 / 开头）
	Replacement string `json:"replacement"` // 替换为（空 = 剥离前缀；非空需以 / 开头）
	Backend     string `json:"backend"`     // 可选：仅对该后端生效（空 = 全部后端）
}

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

// GetSettingBackends 读取运行时后端列表（带权重）。
// 兼容旧格式（纯字符串数组）：旧条目按权重 1 读取。
func (db *DB) GetSettingBackends(ctx context.Context) ([]WeightedBackend, bool, error) {
	v, ok, err := db.GetSetting(ctx, SettingBackends)
	if err != nil || !ok {
		return nil, false, err
	}
	// 先尝试新格式 [{url, weight}]
	var wbs []WeightedBackend
	if err := json.Unmarshal([]byte(v), &wbs); err == nil && wbs != nil {
		return wbs, true, nil
	}
	// 旧格式 ["url1","url2"]：权重默认 1
	var urls []string
	if err := json.Unmarshal([]byte(v), &urls); err != nil {
		return nil, false, nil
	}
	out := make([]WeightedBackend, 0, len(urls))
	for _, u := range urls {
		out = append(out, WeightedBackend{URL: u, Weight: 1})
	}
	return out, true, nil
}

// SetSettingBackends 持久化运行时后端列表（带权重）
func (db *DB) SetSettingBackends(ctx context.Context, backends []WeightedBackend) error {
	b, err := json.Marshal(backends)
	if err != nil {
		return err
	}
	return db.SetSetting(ctx, SettingBackends, string(b))
}

// GetSettingRules 读取定向分流规则
func (db *DB) GetSettingRules(ctx context.Context) ([]RouteRule, bool, error) {
	v, ok, err := db.GetSetting(ctx, SettingRules)
	if err != nil || !ok {
		return nil, false, err
	}
	var rules []RouteRule
	if err := json.Unmarshal([]byte(v), &rules); err != nil {
		return nil, false, nil
	}
	return rules, true, nil
}

// SetSettingRules 持久化定向分流规则（合法性由调用方校验）
func (db *DB) SetSettingRules(ctx context.Context, rules []RouteRule) error {
	b, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	return db.SetSetting(ctx, SettingRules, string(b))
}

// GetSettingRewrites 读取路径重写规则
func (db *DB) GetSettingRewrites(ctx context.Context) ([]RewriteRule, bool, error) {
	v, ok, err := db.GetSetting(ctx, SettingRewrites)
	if err != nil || !ok {
		return nil, false, err
	}
	var rules []RewriteRule
	if err := json.Unmarshal([]byte(v), &rules); err != nil {
		return nil, false, nil
	}
	return rules, true, nil
}

// SetSettingRewrites 持久化路径重写规则（合法性由调用方校验）
func (db *DB) SetSettingRewrites(ctx context.Context, rules []RewriteRule) error {
	b, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	return db.SetSetting(ctx, SettingRewrites, string(b))
}

// GetSettingIPACL 读取 IP 黑白名单配置；不存在时 ok=false（调用方用空配置兜底）
func (db *DB) GetSettingIPACL(ctx context.Context) (raw string, ok bool, err error) {
	return db.GetSetting(ctx, SettingIPACL)
}

// SetSettingIPACL 持久化 IP 黑白名单配置（JSON 原文，合法性由调用方校验）
func (db *DB) SetSettingIPACL(ctx context.Context, raw string) error {
	return db.SetSetting(ctx, SettingIPACL, raw)
}
