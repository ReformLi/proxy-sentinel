package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"proxy-sentinel/internal/api"
	"proxy-sentinel/internal/auth"
	"proxy-sentinel/internal/config"
	"proxy-sentinel/internal/logger"
	"proxy-sentinel/internal/proxy"
	"proxy-sentinel/internal/stats"
	"proxy-sentinel/internal/storage"
)

func main() {
	// 1. 加载配置
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.yaml"
	}
	cfg, cfgInfo, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	if cfgInfo.ConfigFileUsed == "" {
		log.Printf("⚠ 未找到配置文件（%s），当前使用内置默认值 + 环境变量；请确认工作目录是否正确", configPath)
	} else {
		log.Printf("配置文件路径: %s", cfgInfo.ConfigFileUsed)
	}
	if len(cfgInfo.EnvFilesLoaded) > 0 {
		log.Printf("环境变量来源 (dotenv): %s（优先级：shell 已设置的变量 > .env.local > .env）",
			strings.Join(cfgInfo.EnvFilesLoaded, " , "))
	} else {
		log.Printf("⚠ 未加载 .env / .env.local（若已在项目根创建了这些文件，检查当前工作目录或文件名大小写）")
	}
	log.Printf("配置加载完成：监听 :%s，后端数=%d，策略=%s，代理Token数=%d",
		cfg.Server.Port, len(cfg.Backends), cfg.Balancer.Strategy, len(cfg.Auth.ProxyTokens))
	for _, w := range cfgInfo.Warnings {
		log.Printf("⚠ 配置告警: %s", w)
	}

	// 2. 打开数据库
	absDBPath, err := absPath(cfg.Database.Path)
	if err != nil {
		log.Fatalf("解析数据库路径失败: %v", err)
	}
	log.Printf("数据库文件路径: %s", absDBPath)
	db, err := storage.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}
	defer db.Close()

	// 3. 引导：创建/更新管理员账号 + 初始化代理 Token（含旧明文 Token 哈希迁移）
	if err := db.MigrateLegacyTokens(context.Background()); err != nil {
		log.Fatalf("迁移旧明文 Token 失败: %v", err)
	}
	if err := bootstrap(db, cfg); err != nil {
		log.Fatalf("初始化账号/Token 失败: %v", err)
	}

	// 3.5 数据库持久化的运行时设置覆盖配置文件（/settings 页面修改过的后端/策略优先）
	if urls, ok, err := db.GetSettingBackends(context.Background()); err == nil && ok && len(urls) > 0 {
		log.Printf("已加载数据库持久化的后端列表（%d 个，优先于 config.yaml）", len(urls))
		cfg.Backends = urls
	}
	if v, ok, err := db.GetSetting(context.Background(), storage.SettingStrategy); err == nil && ok && v != "" {
		cfg.Balancer.Strategy = v
	}

	// 4. 创建认证组件
	secure := os.Getenv("SECURE_COOKIE") == "true"
	jwtMgr := auth.NewJWTManager(cfg.Auth.JWTSecret, 24*time.Hour, secure)
	webAuth := auth.NewWebAuthMiddleware(jwtMgr)
	proxyAuth := auth.NewProxyAuthMiddleware(db)

	// 5. 负载均衡 + 健康检查 + 代理处理器 + 日志写入器
	balancer := proxy.NewBalancer(cfg.Balancer.Strategy, cfg.Backends)
	health := proxy.NewHealthChecker(balancer, 30*time.Second)
	health.Start()
	defer health.Stop()

	logWriter := logger.NewWriter(db, cfg.Log.SampleRate, cfg.Log.MaskSensitive)
	defer logWriter.Close()

	proxyHandler := proxy.NewHandler(balancer, logWriter, cfg.Proxy.TimeoutSeconds,
		int64(cfg.Proxy.MaxBodyBytes), int64(cfg.Log.BodyMaxBytes), cfg.Proxy.TrustForwardedHeaders)

	// 6. 统计服务 + API Server
	realtimeSvc := stats.NewRealtimeService(db)
	trendSvc := stats.NewTrendService(db)
	flowSvc := stats.NewFlowService(db)

	server := api.NewServer(
		db, jwtMgr, webAuth, proxyAuth,
		realtimeSvc, trendSvc, flowSvc,
		logWriter, balancer, proxyHandler,
		cfg, cfg.Auth.AdminUsername, secure,
	)
	defer server.Close()

	// 7. 日志保留期清理（每小时检查一次过期数据）
	stopRetention := startRetention(db, cfg.Log.RetentionDays)
	defer stopRetention()

	// 8. 启动 HTTP 服务
	addr := ":" + cfg.Server.Port
	srv := &http.Server{
		Addr:         addr,
		Handler:      server.Router(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("Proxy Sentinel 已启动，监听 %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	// 9. 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("正在关闭服务...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("强制关闭: %v", err)
	}
	log.Println("服务已退出")
}

// bootstrap 初始化管理员账号与代理 Token：
// - 管理员不存在则创建；已存在但密码与配置不一致则更新（改 config.yaml 后重启即生效）
// - Token 不存在则创建
func bootstrap(db *storage.DB, cfg *config.Config) error {
	ctx := context.Background()
	// 管理员
	user, err := db.GetUserByUsername(ctx, cfg.Auth.AdminUsername)
	if err != nil {
		return fmt.Errorf("查询管理员失败: %w", err)
	}
	if user == nil {
		hash, err := auth.HashPassword(cfg.Auth.AdminPassword)
		if err != nil {
			return fmt.Errorf("加密密码失败: %w", err)
		}
		if err := db.CreateUser(ctx, cfg.Auth.AdminUsername, hash); err != nil {
			return fmt.Errorf("创建管理员失败: %w", err)
		}
		log.Printf("已创建管理员账号: %s", cfg.Auth.AdminUsername)
	} else if !auth.CheckPassword(user.PasswordHash, cfg.Auth.AdminPassword) {
		// 配置中的密码已变更：同步更新库中哈希
		hash, err := auth.HashPassword(cfg.Auth.AdminPassword)
		if err != nil {
			return fmt.Errorf("加密密码失败: %w", err)
		}
		if err := db.UpdatePasswordHash(ctx, cfg.Auth.AdminUsername, hash); err != nil {
			return fmt.Errorf("更新管理员密码失败: %w", err)
		}
		log.Printf("已根据配置更新管理员密码: %s", cfg.Auth.AdminUsername)
	}
	// 代理 Token
	for i, t := range cfg.Auth.ProxyTokens {
		if t == "" {
			continue
		}
		exists, err := db.TokenExists(ctx, t)
		if err != nil {
			return fmt.Errorf("查询 Token 失败: %w", err)
		}
		if !exists {
			name := fmt.Sprintf("token-%d", i+1)
			if err := db.AddToken(ctx, t, name); err != nil {
				return fmt.Errorf("创建 Token 失败: %w", err)
			}
			log.Printf("已初始化代理 Token [name=%s] [value=%s... (已截断，完整值请查看 config.yaml 或 PROXY_TOKENS)]",
				name, maskTokenPrefix(t, 8))
		}
	}
	return nil
}

// startRetention 启动日志保留期清理协程，返回停止函数
func startRetention(db *storage.DB, retentionDays int) func() {
	if retentionDays <= 0 {
		return func() {}
	}
	ticker := time.NewTicker(1 * time.Hour)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				before := time.Now().AddDate(0, 0, -retentionDays)
				n, err := db.DeleteLogsBefore(context.Background(), before)
				if err != nil {
					log.Printf("清理过期日志失败: %v", err)
					continue
				}
				if n > 0 {
					log.Printf("已清理 %d 条过期日志（>%d 天）", n, retentionDays)
				}
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()
	return func() { close(done) }
}

// absPath 返回路径的绝对形式（用于日志打印，方便用户确认 db 文件实际位置）
func absPath(p string) (string, error) {
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, p), nil
}

// maskTokenPrefix 只打印 Token 的前 n 位用于核对，避免完整泄露
func maskTokenPrefix(t string, n int) string {
	if len(t) <= n {
		return t
	}
	return t[:n]
}
