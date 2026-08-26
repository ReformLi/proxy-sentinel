package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"proxy-sentinel/internal/alert"
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
	log.Printf("配置加载完成：监听 :%s，后端数=%d，策略=%s，代理Token数=%d，默认限流=%s",
		cfg.Server.Port, len(cfg.Backends), cfg.Balancer.Strategy, len(cfg.Auth.ProxyTokens),
		func() string {
			if cfg.RateLimit.DefaultRPM > 0 {
				return fmt.Sprintf("%d 次/分钟/Token", cfg.RateLimit.DefaultRPM)
			}
			return "关闭"
		}())
	for _, w := range cfgInfo.Warnings {
		log.Printf("⚠ 配置告警: %s", w)
	}

	// 2. 打开数据库
	driver := cfg.Database.Driver
	if driver == "" {
		driver = "sqlite"
	}
	dsn := cfg.Database.DSN
	if dsn == "" {
		// SQLite 默认走文件路径
		absDBPath, err := absPath(cfg.Database.Path)
		if err != nil {
			log.Fatalf("解析数据库路径失败: %v", err)
		}
		log.Printf("数据库: %s (%s)", absDBPath, driver)
		dsn = absDBPath
	} else {
		log.Printf("数据库: %s (DSN 模式)", driver)
	}
	db, err := storage.Open(driver, dsn)
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

	// 3.5 数据库持久化的运行时设置覆盖配置文件（/settings 页面修改过的后端/策略/定向规则优先）
	backends := make([]storage.WeightedBackend, 0, len(cfg.Backends))
	for _, u := range cfg.Backends { // config.yaml 后端默认权重 1
		backends = append(backends, storage.WeightedBackend{URL: u, Weight: 1})
	}
	if wbs, ok, err := db.GetSettingBackends(context.Background()); err == nil && ok && len(wbs) > 0 {
		log.Printf("已加载数据库持久化的后端列表（%d 个，优先于 config.yaml）", len(wbs))
		backends = wbs
	}
	if v, ok, err := db.GetSetting(context.Background(), storage.SettingStrategy); err == nil && ok && v != "" {
		cfg.Balancer.Strategy = v
	}
	rules, _, _ := db.GetSettingRules(context.Background())
	if len(rules) > 0 {
		log.Printf("已加载定向分流规则 %d 条", len(rules))
	}
	rewrites, _, _ := db.GetSettingRewrites(context.Background())
	if len(rewrites) > 0 {
		log.Printf("已加载路径重写规则 %d 条", len(rewrites))
	}

	// 4. 创建认证组件
	secure := os.Getenv("SECURE_COOKIE") == "true"
	jwtMgr := auth.NewJWTManager(cfg.Auth.JWTSecret, 24*time.Hour, secure)
	webAuth := auth.NewWebAuthMiddleware(jwtMgr)
	proxyAuth := auth.NewProxyAuthMiddleware(db)

	// 5. 负载均衡 + 健康检查（探测结果落库） + 代理处理器 + 日志写入器
	balancer := proxy.NewBalancer(cfg.Balancer.Strategy, backends)
	health := proxy.NewHealthChecker(balancer, 30*time.Second, func(res proxy.ProbeResult) {
		if err := db.InsertHealthLog(context.Background(), res.URL, res.Healthy, res.LatencyMs, res.StatusCode, res.Error); err != nil {
			log.Printf("写入健康检查日志失败 [%s]: %v", res.URL, err)
		}
	})
	health.Start()
	defer health.Stop()

	logWriter := logger.NewWriter(db, cfg.Log.SampleRate, cfg.Log.MaskSensitive, cfg.Log.QueueCapacity)
	defer logWriter.Close()

	proxyHandler := proxy.NewHandler(balancer, logWriter, cfg.Proxy.TimeoutSeconds,
		int64(cfg.Proxy.MaxBodyBytes), int64(cfg.Proxy.MaxUploadBytes), int64(cfg.Log.BodyMaxBytes), cfg.Proxy.TrustForwardedHeaders)
	proxyHandler.SetRules(rules)
	proxyHandler.SetRewrites(rewrites)

	// 6. 统计服务 + 告警引擎 + API Server
	realtimeSvc := stats.NewRealtimeService(db)
	trendSvc := stats.NewTrendService(db)
	flowSvc := stats.NewFlowService(db)

	var ding *alert.DingTalk
	if cfg.Alert.DingTalk.WebhookURL != "" {
		ding = alert.NewDingTalk(cfg.Alert.DingTalk.WebhookURL, cfg.Alert.DingTalk.Secret)
		log.Printf("告警通知：钉钉 webhook 已配置（评估周期 %d 秒）", cfg.Alert.CheckIntervalSeconds)
	} else {
		log.Printf("告警通知：未配置钉钉 webhook（config.yaml → alert.dingtalk.webhook_url），告警引擎空转")
	}
	alertEngine := alert.NewEngine(db, balancer, ding, cfg.Alert.CheckIntervalSeconds)
	alertEngine.Start()
	defer alertEngine.Stop()

	server := api.NewServer(
		db, jwtMgr, webAuth, proxyAuth,
		realtimeSvc, trendSvc, flowSvc,
		logWriter, balancer, proxyHandler,
		cfg, alertEngine, cfg.Auth.AdminUsername, secure,
	)
	defer server.Close()

	// 7. 日志保留期清理（每小时检查一次过期数据；三表独立保留期）
	stopRetention := startRetention(db, cfg.Log.RetentionDays, cfg.Log.HealthRetentionDays, cfg.Log.AuditRetentionDays)
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

	// 先绑定端口再打"已启动"日志，避免绑定失败时日志自相矛盾
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("监听 %s 失败（端口被占用？）: %v", addr, err)
	}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()
	log.Printf("Proxy Sentinel 已启动，监听 %s", addr)

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
			if err := db.AddToken(ctx, t, name, 0, time.Time{}); err != nil {
				return fmt.Errorf("创建 Token 失败: %w", err)
			}
			log.Printf("已初始化代理 Token [name=%s] [value=%s... (已截断，完整值请查看 config.yaml 或 PROXY_TOKENS)]",
				name, maskTokenPrefix(t, 8))
		}
	}
	return nil
}

// startRetention 启动保留期清理协程（三表独立保留期），返回停止函数
// 各表 days=0 表示不自动清理
func startRetention(db *storage.DB, logDays, healthDays, auditDays int) func() {
	if logDays <= 0 && healthDays <= 0 && auditDays <= 0 {
		return func() {}
	}
	ticker := time.NewTicker(1 * time.Hour)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				// proxy_logs
				if logDays > 0 {
					before := time.Now().AddDate(0, 0, -logDays)
					n, err := db.DeleteLogsBefore(context.Background(), before)
					if err != nil {
						log.Printf("清理过期 proxy_logs 失败: %v", err)
					} else if n > 0 {
						log.Printf("已清理 %d 条过期 proxy_logs（>%d 天）", n, logDays)
					}
				}
				// backend_health_logs
				if healthDays > 0 {
					before := time.Now().AddDate(0, 0, -healthDays)
					hn, err := db.DeleteHealthBefore(context.Background(), before)
					if err != nil {
						log.Printf("清理过期 backend_health_logs 失败: %v", err)
					} else if hn > 0 {
						log.Printf("已清理 %d 条过期 backend_health_logs（>%d 天）", hn, healthDays)
					}
				}
				// audit_logs
				if auditDays > 0 {
					before := time.Now().AddDate(0, 0, -auditDays)
					an, err := db.DeleteAuditsBefore(context.Background(), before)
					if err != nil {
						log.Printf("清理过期 audit_logs 失败: %v", err)
					} else if an > 0 {
						log.Printf("已清理 %d 条过期 audit_logs（>%d 天）", an, auditDays)
					}
				}
				// 清理后回收空间
				if err := db.Vacuum(context.Background()); err != nil {
					log.Printf("VACUUM 失败: %v", err)
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
