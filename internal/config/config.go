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
	Server   ServerConfig   `yaml:"server"`
	Backends []string       `yaml:"backends"`
	Balancer BalancerConfig `yaml:"balancer"`
	Database DatabaseConfig `yaml:"database"`
	Proxy    ProxyConfig    `yaml:"proxy"`
	Auth     AuthConfig     `yaml:"auth"`
	Log      LogConfig       `yaml:"log"`
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
	TimeoutSeconds int `yaml:"timeout_seconds"`
	MaxBodyBytes   int `yaml:"max_body_bytes"`
}

type AuthConfig struct {
	AdminUsername string   `yaml:"admin_username"`
	AdminPassword string   `yaml:"admin_password"`
	JWTSecret      string   `yaml:"jwt_secret"`
	ProxyTokens    []string `yaml:"proxy_tokens"`
}

type LogConfig struct {
	Level          string  `yaml:"level"`
	SampleRate     float64 `yaml:"sample_rate"`
	RetentionDays  int     `yaml:"retention_days"`
	MaskSensitive  bool    `yaml:"mask_sensitive"`
}

// LoadResult 描述配置加载过程，便于调用方打印诊断日志
type LoadResult struct {
	ConfigFileUsed string   // 实际读取的配置文件路径（空 = 未找到，用内置默认）
	EnvFilesLoaded []string // 实际加载的 .env 文件列表（按加载顺序：.env.local → .env）
	Backends       []string // 最终生效的后端列表（含环境变量覆盖后）
	ProxyTokens    []string // 最终生效的代理 Token 值列表（原值，调用方自行脱敏后打印）
}

// Load 从配置文件加载，再用环境变量覆盖；同时返回诊断信息
// 环境变量加载顺序（后者优先）：进程已有环境变量 → .env → .env.local
// 注意：godotenv 不会覆盖已存在的 shell 环境变量（Override=false），
// 若要强制让 .env.local 覆盖 shell 变量，请通过 ENV_OVERRIDE=true 设置。
func Load(path string) (*Config, *LoadResult, error) {
	cfg := defaultConfig()
	res := &LoadResult{}

	// 0. 加载 .env / .env.local（仅当文件存在时）
	// 优先级：进程已有的 shell 环境变量 > .env.local > .env
	// Overload 会覆盖先载入的值，所以先 .env，再 .env.local（后者覆盖前者）。
	// 不使用 Override 选项以兼容 godotenv v1.5.x 稳定 API。
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
			// 忽略不存在或解析失败（.env 是可选增强）
		}
	}

	// 1. 读取配置文件（可选）
	// 先按用户传入路径精确读取；若找不到且是相对路径，再向上逐级查找（适配从 cmd/sentinel 启动的场景）
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

	// 3. 校验
	if err := cfg.Validate(); err != nil {
		return nil, res, err
	}

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
// 目的：允许从 cmd/sentinel 子目录启动时仍能读到项目根的 .env / .env.local / config.yaml
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
//
// 返回：文件内容、实际路径、错误（不存在时返回 os.ErrNotExist）
func readConfigFile(path string) ([]byte, string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, path, nil
	}
	if !os.IsNotExist(err) {
		return nil, "", err
	}
	// 绝对路径找不到直接返回不存在
	if filepath.IsAbs(path) {
		return nil, "", err
	}
	// 相对路径：向上找
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
			TimeoutSeconds: 30,
			MaxBodyBytes:   10 * 1024 * 1024, // 10MB
		},
		Auth: AuthConfig{
			AdminUsername: "admin",
			AdminPassword: "change-me-please",
			JWTSecret:      "change-this-to-a-random-secret",
		},
		Log: LogConfig{
			Level:         "info",
			SampleRate:    1.0,
			RetentionDays: 30,
			MaskSensitive: true,
		},
	}
}

// applyEnv 用环境变量覆盖配置
func applyEnv(cfg *Config) {
	if v := os.Getenv("SERVER_PORT"); v != "" {
		cfg.Server.Port = v
	}
	if v := os.Getenv("BACKEND_URLS"); v != "" {
		// 逗号分隔多个后端
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
}

// Validate 校验配置完整性
func (c *Config) Validate() error {
	if len(c.Backends) == 0 {
		return fmt.Errorf("必须配置至少一个后端地址（backends 或 BACKEND_URLS）")
	}
	if len(c.Auth.ProxyTokens) == 0 {
		return fmt.Errorf("必须配置至少一个代理 Token（auth.proxy_tokens 或 PROXY_TOKENS），否则所有 /proxy/* 请求都会 401")
	}
	if c.Auth.JWTSecret == "" {
		return fmt.Errorf("必须配置 JWT Secret（jwt_secret 或 JWT_SECRET）")
	}
	if c.Auth.AdminPassword == "" {
		return fmt.Errorf("必须配置管理员密码（admin_password 或 ADMIN_PASSWORD）")
	}
	if c.Balancer.Strategy != "round_robin" && c.Balancer.Strategy != "random" {
		return fmt.Errorf("负载均衡策略非法：%s（仅支持 round_robin/random）", c.Balancer.Strategy)
	}
	if c.Log.SampleRate < 0 || c.Log.SampleRate > 1 {
		return fmt.Errorf("采样率必须在 0.0 ~ 1.0 之间")
	}
	return nil
}
