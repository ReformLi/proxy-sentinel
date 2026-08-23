package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// schema 与根目录 migrations/001_initial_schema.sql 保持一致；
// 此处直接内联以便单二进制自包含部署。
const schemaSQL = `
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

CREATE TABLE IF NOT EXISTS proxy_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  method TEXT NOT NULL,
  path TEXT NOT NULL,
  query TEXT,
  request_headers TEXT,
  request_body TEXT,
  status INTEGER NOT NULL,
  response_headers TEXT,
  response_body TEXT,
  duration INTEGER NOT NULL,
  client_ip TEXT,
  user_agent TEXT,
  referer TEXT,
  backend_url TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_path ON proxy_logs(path);
CREATE INDEX IF NOT EXISTS idx_status ON proxy_logs(status);
CREATE INDEX IF NOT EXISTS idx_created_at ON proxy_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_method ON proxy_logs(method);

CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS proxy_tokens (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  token TEXT UNIQUE NOT NULL,
  name TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  last_used_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_token ON proxy_tokens(token);

CREATE TABLE IF NOT EXISTS audit_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT,
  action TEXT,
  ip TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
`

// DB 封装数据库连接与查询
type DB struct {
	*sql.DB
}

// Open 打开（或创建）SQLite 数据库并执行迁移
func Open(dbPath string) (*DB, error) {
	// 确保数据目录存在
	if dir := filepath.Dir(dbPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据库目录失败: %w", err)
		}
	}

	// modernc.org/sqlite 注册的驱动名为 "sqlite"
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", dbPath)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// 连接池配置（SQLite 单写，但读可并发）
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("数据库 ping 失败: %w", err)
	}

	db := &DB{sqlDB}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}
	return db, nil
}

func (db *DB) migrate() error {
	_, err := db.Exec(schemaSQL)
	return err
}
