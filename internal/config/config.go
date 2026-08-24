package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// Config 全局配置
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Backends  []string        `yaml:"backends"`
	Balancer  BalancerConfig  `yaml:"balancer"`
	Database  DatabaseConfig  `yaml:"database"`
	Proxy     ProxyConfig     `yaml:"proxy"`
	Auth      AuthConfig      `yaml:"auth"`
	Log       LogConfig       `yaml:"log"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
	Alert     AlertConfig     `yaml:"alert"`
}

type ServerConfig struct {
	Port string `yaml:"port"`
}

type BalancerConfig struct {
	Strategy string `yaml:"strategy"` // round_robin | random
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type ProxyConfig struct {
	TimeoutSeconds      int  `yaml:"timeout_seconds"`
	MaxBodyBytes        int  `yaml:"max_body_bytes"`        // 请求体大小上限（拒绝超限）
	TrustForwardedHeaders bool `yaml:"trust_forwarded_headers"` // 是否信任入站 X-Forwarded-For（多级代理时开启，默认 false 防伪造）
}

type AuthConfig struct {
	AdminUsername string   `yaml:"admin_username"`
	AdminPassword string   `yaml:"admin_password"`
	JWTSecret     string   `yaml:"jwt_secret"`
	ProxyTokens   []string `yaml:"proxy_tokens"`
}

type LogConfig struct {
	Level         string  `yaml:"level"`
	SampleRate    float64 `yaml:"sample_rate"`
	RetentionDays int     `yaml:"retention_days"`
	MaskSensitive bool    `yaml:"mask_sensitive"`
	BodyMaxBytes  int     `yaml:"body_max_bytes"` // 日志记录的请求/响应体截断上限
}

// RateLimitConfig 限流配置：按 Token 维度限制每分钟请求数
type RateLimitConfig struct {
	DefaultRPM int `yaml:"default_rpm"` // 全局默认值（次/分钟/Token），0 = 不限流
}

// AlertConfig 告警配置：通知渠道凭据（规则阈值存数据库，页面可改）
type AlertConfig struct {
	CheckIntervalSeconds int             `yaml:"check_interval_seconds"` // 告警评估周期（秒），默认 30
	DingTalk             DingTalkConfig  `yaml:"dingtalk"`
}

// DingTalkConfig 钉钉群机器人凭据（群设置 → 智能群助手 → 添加机器人 → 自定义）
type DingTalkConfig struct {
	WebhookURL string `yaml:"webhook_url"` // https://oapi.dingtalk.com/robot/send?access_token=xxx
	Secret     string `yaml:"secret"`      // 加签密钥（机器人安全设置选"加签"时填，未开启则留空）
}

// LoadResult 描述配置加载过程，便于调用方打印诊断日志
type LoadResult struct {
	ConfigFileUsed string   // 实际读取的配置文件路径（空 = 未找到，用内置默认）
	EnvFilesLoaded []string // 实际加载的 .env 文件列表（按加载顺序：.env.local → .env）
	Backends       []string // 最终生效的后端列表（含环境变量覆盖后）
	ProxyTokens    []string // 最终生效的代理 Token 值列表（原值，调用方自行脱敏后打印）
	Warnings       []string // 非致命告警（弱密钥等）
}

// Load 从配置文件加载，再用环境变量覆盖；同时返回诊断信息
// 环境变量加载顺序（后者优先）：进程已有环境变量 → .env → .env.local
func Load(path string) (*Config, *LoadResult, error) {
	cfg := defaultConfig()
	res := &LoadResult{}

	// 0. 加载 .env / .env.local（仅当文件存在时）
	for i, envFile := range []string{".env", ".env.local"} {
		if p, ok := findFileUpwards(envFile); ok {
			var err error
			if i == 0 {
				err = godotenv.Load(p)
			} else {
				err = godotenv.Overload(p) // 后加载的 .env.local 覆盖前加载的 .env
			}
			if err == nil {
				res.EnvFilesLoaded = append(res.EnvFilesLoaded, p)
			}
		}
	}

	// 1. 读取配置文件（可选）
	if path != "" {
		data, resolvedPath, err := readConfigFile(path)
		if err == nil {
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, res, fmt.Errorf("解析配置文件失败: %w", err)
			}
			res.ConfigFileUsed = absOrDefault(resolvedPath)
		} else if !os.IsNotExist(err) {
			return nil, res, fmt.Errorf("读取配置文件失败: %w", err)
		}
	}

	// 2. 环境变量覆盖
	applyEnv(cfg)

	// 3. 校验（收集告警但不中断）
	warnings, err := cfg.validate()
	if err != nil {
		return nil, res, err
	}
	res.Warnings = warnings

	// 4. 导出诊断信息
	res.Backends = append([]string{}, cfg.Backends...)
	res.ProxyTokens = append([]string{}, cfg.Auth.ProxyTokens...)
	return cfg, res, nil
}

// absOrDefault 将路径转为绝对路径（失败时保留原值），仅用于日志
func absOrDefault(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	cwd, err := os.Getwd()
	if err != nil {
		return p
	}
	return filepath.Join(cwd, p)
}

// findFileUpwards 从当前工作目录开始向上查找 name 文件，找到返回绝对路径 + true
func findFileUpwards(name string) (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		candidate := filepath.Join(dir, name)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir { // 到达根
			return "", false
		}
		dir = parent
	}
}

// readConfigFile 按以下顺序读取配置文件：
//  1. 按传入 path 直接读（绝对/相对cwd）
//  2. 若是相对路径且不存在，向上逐级查找同名文件
func readConfigFile(path string) ([]byte, string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, path, nil
	}
	if !os.IsNotExist(err) {
		return nil, "", err
	}
	if filepath.IsAbs(path) {
		return nil, "", err
	}
	base := filepath.Base(path)
	if resolved, ok := findFileUpwards(base); ok {
		data, err := os.ReadFile(resolved)
		if err == nil {
			return data, resolved, nil
		}
		return nil, "", err
	}
	return nil, "", err
}

func defaultConfig() *Config {
	return &Config{
		Server:   ServerConfig{Port: "8080"},
		Balancer: BalancerConfig{Strategy: "round_robin"},
		Database: DatabaseConfig{Path: "./data/sentinel.db"},
		Proxy: ProxyConfig{
			TimeoutSeconds:        30,
			MaxBodyBytes:          10 * 1024 * 1024, // 10MB
			TrustForwardedHeaders: false,
		},
		Auth: AuthConfig{
			AdminUsername: "admin",
		},
		Log: LogConfig{
			Level:         "info",
			SampleRate:    1.0,
			RetentionDays: 30,
			MaskSensitive: true,
			BodyMaxBytes:  64 * 1024, // 64KB：日志缓冲独立上限，防止大响应撑爆内存
		},
	}
}

// applyEnv 用环境变量覆盖配置
func applyEnv(cfg *Config) {
	if v := os.Getenv("SERVER_PORT"); v != "" {
		cfg.Server.Port = v
	}
	if v := os.Getenv("BACKEND_URLS"); v != "" {
		parts := strings.Split(v, ",")
		cfg.Backends = nil
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				cfg.Backends = append(cfg.Backends, t)
			}
		}
	}
	if v := os.Getenv("BALANCER_STRATEGY"); v != "" {
		cfg.Balancer.Strategy = v
	}
	if v := os.Getenv("DATABASE_PATH"); v != "" {
		cfg.Database.Path = v
	}
	if v := os.Getenv("PROXY_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Proxy.TimeoutSeconds = n
		}
	}
	if v := os.Getenv("PROXY_MAX_BODY_BYTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Proxy.MaxBodyBytes = n
		}
	}
	if v := os.Getenv("PROXY_TRUST_FORWARDED_HEADERS"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Proxy.TrustForwardedHeaders = b
		}
	}
	if v := os.Getenv("ADMIN_USERNAME"); v != "" {
		cfg.Auth.AdminUsername = v
	}
	if v := os.Getenv("ADMIN_PASSWORD"); v != "" {
		cfg.Auth.AdminPassword = v
	}
	if v := os.Getenv("JWT_SECRET"); v != "" {
		cfg.Auth.JWTSecret = v
	}
	if v := os.Getenv("PROXY_TOKENS"); v != "" {
		parts := strings.Split(v, ",")
		cfg.Auth.ProxyTokens = nil
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				cfg.Auth.ProxyTokens = append(cfg.Auth.ProxyTokens, t)
			}
		}
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("LOG_SAMPLE_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Log.SampleRate = f
		}
	}
	if v := os.Getenv("LOG_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Log.RetentionDays = n
		}
	}
	if v := os.Getenv("LOG_MASK_SENSITIVE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Log.MaskSensitive = b
		}
	}
	if v := os.Getenv("LOG_BODY_MAX_BYTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Log.BodyMaxBytes = n
		}
	}
	if v := os.Getenv("RATE_LIMIT_DEFAULT_RPM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.RateLimit.DefaultRPM = n
		}
	}
	if v := os.Getenv("ALERT_CHECK_INTERVAL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Alert.CheckIntervalSeconds = n
		}
	}
	if v := os.Getenv("ALERT_DINGTALK_WEBHOOK_URL"); v != "" {
		cfg.Alert.DingTalk.WebhookURL = v
	}
	if v := os.Getenv("ALERT_DINGTALK_SECRET"); v != "" {
		cfg.Alert.DingTalk.Secret = v
	}
}

// 已知占位/弱默认值黑名单：出现即视为致命错误（防止带默认密钥上线）
var weakSecrets = map[string]bool{
	"change-me-please":              true,
	"change-this-to-a-random-secret": true,
	"secret":                        true,
	"password":                      true,
}

// validate 校验配置：致命问题返回 error（阻止启动），弱配置仅收集为告警
func (c *Config) validate() ([]string, error) {
	var warnings []string

	if len(c.Backends) == 0 {
		return nil, fmt.Errorf("必须配置至少一个后端地址（backends 或 BACKEND_URLS）")
	}
	if len(c.Auth.ProxyTokens) == 0 {
		return nil, fmt.Errorf("必须配置至少一个代理 Token（auth.proxy_tokens 或 PROXY_TOKENS），否则所有 /proxy/* 请求都会 401")
	}
	if c.Auth.JWTSecret == "" {
		return nil, fmt.Errorf("必须配置 JWT Secret（jwt_secret 或 JWT_SECRET）")
	}
	if weakSecrets[strings.ToLower(c.Auth.JWTSecret)] {
		return nil, fmt.Errorf("JWT Secret 使用了已知的弱默认值，请更换为随机强密钥（建议 ≥32 字符）")
	}
	if len(c.Auth.JWTSecret) < 16 {
		warnings = append(warnings, fmt.Sprintf("JWT Secret 长度仅 %d 字符（建议 ≥32），存在被暴力破解风险", len(c.Auth.JWTSecret)))
	}
	if c.Auth.AdminPassword == "" {
		return nil, fmt.Errorf("必须配置管理员密码（admin_password 或 ADMIN_PASSWORD）")
	}
	if weakSecrets[strings.ToLower(c.Auth.AdminPassword)] {
		return nil, fmt.Errorf("管理员密码使用了已知的弱默认值，请更换强密码（建议 ≥12 字符，含大小写数字符号）")
	}
	if len(c.Auth.AdminPassword) < 8 {
		warnings = append(warnings, fmt.Sprintf("管理员密码长度仅 %d 字符（建议 ≥12）", len(c.Auth.AdminPassword)))
	}
	if c.Balancer.Strategy != "round_robin" && c.Balancer.Strategy != "random" {
		return nil, fmt.Errorf("负载均衡策略非法：%s（仅支持 round_robin/random）", c.Balancer.Strategy)
	}
	if c.Log.SampleRate < 0 || c.Log.SampleRate > 1 {
		return nil, fmt.Errorf("采样率必须在 0.0 ~ 1.0 之间")
	}
	if c.RateLimit.DefaultRPM < 0 {
		return nil, fmt.Errorf("限流默认值 rate_limit.default_rpm 不能为负数")
	}
	if c.Alert.CheckIntervalSeconds < 0 {
		return nil, fmt.Errorf("告警评估周期 alert.check_interval_seconds 不能为负数")
	}
	if c.Alert.CheckIntervalSeconds > 0 && c.Alert.CheckIntervalSeconds < 5 {
		return nil, fmt.Errorf("告警评估周期 alert.check_interval_seconds 不能小于 5 秒")
	}
	if c.Log.BodyMaxBytes <= 0 {
		c.Log.BodyMaxBytes = 64 * 1024
	}
	if c.Proxy.MaxBodyBytes <= 0 {
		c.Proxy.MaxBodyBytes = 10 * 1024 * 1024
	}
	return warnings, nil
}
