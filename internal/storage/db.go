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
  request_id TEXT,
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
  last_used_at DATETIME,
  rate_limit_rpm INTEGER DEFAULT 0,
  expires_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_token ON proxy_tokens(token);

CREATE TABLE IF NOT EXISTS audit_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT,
  action TEXT,
  ip TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS backend_health_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  backend_url TEXT NOT NULL,
  healthy INTEGER NOT NULL,          -- 1=健康 0=不健康
  latency_ms INTEGER,               -- 探测耗时（失败时为实际等待时间）
  status_code INTEGER,              -- 探测响应状态码（网络失败为 NULL）
  error TEXT,                       -- 失败原因（超时/连接拒绝等）
  checked_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_bhl_backend_time ON backend_health_logs(backend_url, checked_at);
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
	if _, err := db.Exec(schemaSQL); err != nil {
		return err
	}
	// 增量列迁移：CREATE TABLE IF NOT EXISTS 不会给已存在的表补列
	if err := db.addColumnIfMissing("proxy_tokens", "rate_limit_rpm", "INTEGER DEFAULT 0"); err != nil {
		return err
	}
	if err := db.addColumnIfMissing("proxy_tokens", "expires_at", "DATETIME"); err != nil {
		return err
	}
	if err := db.addColumnIfMissing("proxy_logs", "request_id", "TEXT"); err != nil {
		return err
	}
	// request_id 索引须在补列之后创建（旧表建列前无法建索引，故不放在 schemaSQL）
	_, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_request_id ON proxy_logs(request_id)")
	return err
}

// addColumnIfMissing 若表中不存在指定列则执行 ALTER TABLE ADD COLUMN
func (db *DB) addColumnIfMissing(table, column, def string) error {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dfltValue any
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil // 已存在
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, def))
	return err
}
