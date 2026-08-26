// init_admin 用于手动创建或重置可视化页面管理员账号。
//
// 用法：
//   go run ./scripts/init_admin -username admin -password "new-password"
//
// 若不指定参数，则从 config.yaml + 环境变量读取（与主程序相同的配置加载逻辑）。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"proxy-sentinel/internal/auth"
	"proxy-sentinel/internal/config"
	"proxy-sentinel/internal/storage"
)

func main() {
	var username, password, dbPath, configPath string
	flag.StringVar(&username, "username", "", "管理员用户名")
	flag.StringVar(&password, "password", "", "管理员密码")
	flag.StringVar(&dbPath, "db", "", "SQLite 数据库路径（覆盖配置）")
	flag.StringVar(&configPath, "config", "config.yaml", "配置文件路径")
	flag.Parse()

	cfg, _, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	if username == "" {
		username = cfg.Auth.AdminUsername
	}
	if password == "" {
		password = cfg.Auth.AdminPassword
	}
	if dbPath == "" {
		dbPath = cfg.Database.Path
	}
	if username == "" || password == "" {
		log.Fatal("必须提供用户名与密码（参数或配置）")
	}

	db, err := storage.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	hash, err := auth.HashPassword(password)
	if err != nil {
		log.Fatalf("加密密码失败: %v", err)
	}

	exists, err := db.UserExists(ctx, username)
	if err != nil {
		log.Fatalf("查询用户失败: %v", err)
	}
	if exists {
		// 重置：删除后重建
		if _, err := db.ExecContext(ctx, "DELETE FROM users WHERE username=?", username); err != nil {
			log.Fatalf("重置用户失败: %v", err)
		}
	}
	if err := db.CreateUser(ctx, username, hash); err != nil {
		log.Fatalf("创建用户失败: %v", err)
	}
	fmt.Fprintf(os.Stdout, "管理员 '%s' 创建/重置成功\n", username)
}
