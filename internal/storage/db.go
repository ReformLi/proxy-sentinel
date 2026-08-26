package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// SQLite 建表语句（与根目录 migrations/001_initial_schema.sql 保持一致；内联以便单二进制自包含部署）
const sqliteSchema = `
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
  healthy INTEGER NOT NULL,
  latency_ms INTEGER,
  status_code INTEGER,
  error TEXT,
  checked_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_bhl_backend_time ON backend_health_logs(backend_url, checked_at);
`

// MySQL 建表语句
const mysqlSchema = `
CREATE TABLE IF NOT EXISTS proxy_logs (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  method VARCHAR(16) NOT NULL,
  path VARCHAR(2048) NOT NULL,
  query TEXT,
  request_headers MEDIUMTEXT,
  request_body MEDIUMTEXT,
  status INT NOT NULL,
  response_headers MEDIUMTEXT,
  response_body MEDIUMTEXT,
  duration BIGINT NOT NULL,
  client_ip VARCHAR(64),
  user_agent VARCHAR(512),
  referer VARCHAR(2048),
  backend_url VARCHAR(512),
  request_id VARCHAR(128),
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_path (path),
  INDEX idx_status (status),
  INDEX idx_created_at (created_at),
  INDEX idx_method (method)
);

CREATE TABLE IF NOT EXISTS users (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  username VARCHAR(64) UNIQUE NOT NULL,
  password_hash VARCHAR(128) NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS proxy_tokens (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  token VARCHAR(128) UNIQUE NOT NULL,
  name VARCHAR(128),
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  last_used_at DATETIME,
  rate_limit_rpm INT DEFAULT 0,
  expires_at DATETIME,
  INDEX idx_token (token)
);

CREATE TABLE IF NOT EXISTS audit_logs (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  username VARCHAR(64),
  action VARCHAR(512),
  ip VARCHAR(64),
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS settings (
  ` + "`key`" + ` VARCHAR(128) PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS backend_health_logs (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  backend_url VARCHAR(512) NOT NULL,
  healthy INT NOT NULL,
  latency_ms BIGINT,
  status_code INT,
  error TEXT,
  checked_at DATETIME,
  INDEX idx_bhl_backend_time (backend_url, checked_at)
);
`

// PostgreSQL 建表语句
const postgresSchema = `
CREATE TABLE IF NOT EXISTS proxy_logs (
  id BIGSERIAL PRIMARY KEY,
  method VARCHAR(16) NOT NULL,
  path VARCHAR(2048) NOT NULL,
  query TEXT,
  request_headers TEXT,
  request_body TEXT,
  status INTEGER NOT NULL,
  response_headers TEXT,
  response_body TEXT,
  duration BIGINT NOT NULL,
  client_ip VARCHAR(64),
  user_agent VARCHAR(512),
  referer VARCHAR(2048),
  backend_url VARCHAR(512),
  request_id VARCHAR(128),
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_path ON proxy_logs(path);
CREATE INDEX IF NOT EXISTS idx_status ON proxy_logs(status);
CREATE INDEX IF NOT EXISTS idx_created_at ON proxy_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_method ON proxy_logs(method);

CREATE TABLE IF NOT EXISTS users (
  id BIGSERIAL PRIMARY KEY,
  username VARCHAR(64) UNIQUE NOT NULL,
  password_hash VARCHAR(128) NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS proxy_tokens (
  id BIGSERIAL PRIMARY KEY,
  token VARCHAR(128) UNIQUE NOT NULL,
  name VARCHAR(128),
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  last_used_at TIMESTAMP,
  rate_limit_rpm INTEGER DEFAULT 0,
  expires_at TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_token ON proxy_tokens(token);

CREATE TABLE IF NOT EXISTS audit_logs (
  id BIGSERIAL PRIMARY KEY,
  username VARCHAR(64),
  action VARCHAR(512),
  ip VARCHAR(64),
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS settings (
  key VARCHAR(128) PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS backend_health_logs (
  id BIGSERIAL PRIMARY KEY,
  backend_url VARCHAR(512) NOT NULL,
  healthy INTEGER NOT NULL,
  latency_ms BIGINT,
  status_code INTEGER,
  error TEXT,
  checked_at TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_bhl_backend_time ON backend_health_logs(backend_url, checked_at);
`

// DB 封装数据库连接与查询
type DB struct {
	*sql.DB
	Dialect Dialect
}

// Open 根据 driver 打开对应类型的数据库并执行迁移。
// driver: "sqlite" | "mysql" | "postgres"
// dsn: SQLite 为文件路径；MySQL/PG 为标准连接字符串。
func Open(driver, dsn string) (*DB, error) {
	switch Dialect(driver) {
	case DialectMySQL:
		return openMySQL(dsn)
	case DialectPostgres:
		return openPostgres(dsn)
	default: // sqlite（默认）
		return openSQLite(dsn)
	}
}

// openSQLite 打开（或创建）SQLite 数据库
func openSQLite(dbPath string) (*DB, error) {
	if dir := filepath.Dir(dbPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据库目录失败: %w", err)
		}
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", dbPath)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(time.Hour)
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("数据库 ping 失败: %w", err)
	}
	db := &DB{DB: sqlDB, Dialect: DialectSQLite}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}
	return db, nil
}

// openMySQL 打开 MySQL 数据库
func openMySQL(dsn string) (*DB, error) {
	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 MySQL 失败: %w", err)
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("MySQL ping 失败: %w", err)
	}
	db := &DB{DB: sqlDB, Dialect: DialectMySQL}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}
	return db, nil
}

// openPostgres 打开 PostgreSQL 数据库
func openPostgres(dsn string) (*DB, error) {
	sqlDB, err := sql.Open("pgx", dsn) // github.com/jackc/pgx/v5/stdlib
	if err != nil {
		return nil, fmt.Errorf("打开 PostgreSQL 失败: %w", err)
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("PostgreSQL ping 失败: %w", err)
	}
	db := &DB{DB: sqlDB, Dialect: DialectPostgres}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}
	return db, nil
}

// ---- shadow database/sql 方法：PostgreSQL 自动把 ? 转为 $N ----

func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.DB.QueryContext(ctx, db.rebind(query), args...)
}

func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.DB.ExecContext(ctx, db.rebind(query), args...)
}

func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.DB.QueryRowContext(ctx, db.rebind(query), args...)
}

// ---- 迁移 ----

func (db *DB) migrate() error {
	var schema string
	switch db.Dialect {
	case DialectMySQL:
		schema = mysqlSchema
	case DialectPostgres:
		schema = postgresSchema
	default:
		schema = sqliteSchema
	}
	if _, err := db.DB.Exec(schema); err != nil {
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
	// request_id 索引须在补列之后创建
	_, err := db.DB.Exec(db.rebind("CREATE INDEX IF NOT EXISTS idx_request_id ON proxy_logs(request_id)"))
	return err
}

// addColumnIfMissing 若表中不存在指定列则执行 ALTER TABLE ADD COLUMN
func (db *DB) addColumnIfMissing(table, column, def string) error {
	exists, err := db.columnExists(table, column)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = db.DB.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, def))
	return err
}

// columnExists 检查表中是否已有指定列（方言相关）
func (db *DB) columnExists(table, column string) (bool, error) {
	switch db.Dialect {
	case DialectSQLite:
		rows, err := db.DB.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
		if err != nil {
			return false, err
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, colType string
			var notNull int
			var dfltValue any
			var pk int
			if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
				return false, err
			}
			if name == column {
				return true, nil
			}
		}
		return false, rows.Err()
	case DialectMySQL:
		var n int
		err := db.DB.QueryRow(
			`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`,
			table, column,
		).Scan(&n)
		return n > 0, err
	case DialectPostgres:
		var n int
		err := db.DB.QueryRow(
			`SELECT COUNT(*) FROM information_schema.columns WHERE table_name = $1 AND column_name = $2`,
			table, column,
		).Scan(&n)
		return n > 0, err
	default:
		return false, fmt.Errorf("unknown dialect: %s", db.Dialect)
	}
}

// ---- 数据库大小估算（方言相关） ----

// DatabaseSize 返回数据库总大小（字节）
func (db *DB) DatabaseSize(ctx context.Context) (int64, error) {
	switch db.Dialect {
	case DialectSQLite:
		var pageCount, pageSize int64
		if err := db.DB.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
			return 0, err
		}
		if err := db.DB.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
			return 0, err
		}
		return pageCount * pageSize, nil
	case DialectMySQL:
		var sz sql.NullInt64
		err := db.DB.QueryRowContext(ctx,
			`SELECT SUM(data_length + index_length) FROM information_schema.tables WHERE table_schema = DATABASE()`,
		).Scan(&sz)
		if !sz.Valid {
			return 0, nil
		}
		return sz.Int64, err
	case DialectPostgres:
		var sz int64
		err := db.DB.QueryRowContext(ctx, `SELECT pg_database_size(current_database())`).Scan(&sz)
		return sz, err
	default:
		return 0, fmt.Errorf("unknown dialect: %s", db.Dialect)
	}
}

// TableSize 单表估算大小（字节）
func (db *DB) TableSize(ctx context.Context, table string) (int64, error) {
	switch db.Dialect {
	case DialectSQLite:
		row := db.DB.QueryRowContext(ctx, "SELECT SUM(pgsize) FROM dbstat WHERE name = ?", table)
		var sz sql.NullInt64
		if err := row.Scan(&sz); err != nil {
			return 0, nil // 老 SQLite 没 dbstat 时静默降级
		}
		if !sz.Valid {
			return 0, nil
		}
		return sz.Int64, nil
	case DialectMySQL:
		var sz sql.NullInt64
		err := db.DB.QueryRowContext(ctx,
			`SELECT data_length + index_length FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`,
			table,
		).Scan(&sz)
		if !sz.Valid {
			return 0, nil
		}
		return sz.Int64, err
	case DialectPostgres:
		var sz int64
		err := db.DB.QueryRowContext(ctx,
			`SELECT pg_total_relation_size($1)`, table,
		).Scan(&sz)
		return sz, err
	default:
		return 0, fmt.Errorf("unknown dialect: %s", db.Dialect)
	}
}

// Vacuum 回收空间（方言相关）。在手动清理后调用。
func (db *DB) Vacuum(ctx context.Context) error {
	_, err := db.DB.ExecContext(ctx, db.vacuumSQL())
	return err
}
