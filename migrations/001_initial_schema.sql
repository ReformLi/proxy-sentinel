-- 001_initial_schema.sql
-- Proxy Sentinel 初始建表脚本

PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

-- 日志记录表：所有经过代理的请求/响应
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
  duration INTEGER NOT NULL,           -- 耗时（毫秒）
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

-- 可视化页面管理员表
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
-- role / token_version 列由程序启动时增量迁移补齐（见 internal/storage/db.go migrate）
-- token_version：密码/角色变更时 +1，使旧 JWT 立即失效

-- 代理接口 Token 表
CREATE TABLE IF NOT EXISTS proxy_tokens (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  token TEXT UNIQUE NOT NULL,
  name TEXT,                          -- Token 备注名称
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  last_used_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_token ON proxy_tokens(token);

-- 操作审计表
CREATE TABLE IF NOT EXISTS audit_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT,
  action TEXT,
  ip TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
