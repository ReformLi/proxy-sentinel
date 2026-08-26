package storage

import (
	"fmt"
	"strings"
)

// Dialect 标识当前数据库类型
type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectMySQL    Dialect = "mysql"
	DialectPostgres Dialect = "postgres"
)

// rebind 将 SQL 中的 ? 占位符转换为指定方言的占位符。
// SQLite/MySQL 原生支持 ?，PostgreSQL 需要 $1, $2, ... 编号占位符。
func (db *DB) rebind(query string) string {
	if db.Dialect != DialectPostgres {
		return query // SQLite & MySQL 原生支持 ?
	}
	var b strings.Builder
	n := 1
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			b.WriteString(fmt.Sprintf("$%d", n))
			n++
		} else {
			b.WriteByte(query[i])
		}
	}
	return b.String()
}

// epochExpr 返回将 DATETIME 列转为 epoch 秒的 SQL 表达式（方言相关）。
// SQLite：modernc.org/sqlite 把 time.Time 存为 Go String() 格式，需要复杂字符串解析。
// MySQL：UNIX_TIMESTAMP(col) 直接取 epoch。
// PostgreSQL：EXTRACT(EPOCH FROM col) 直接取 epoch。
func (db *DB) epochExpr(col string) string {
	switch db.Dialect {
	case DialectMySQL:
		return fmt.Sprintf("UNIX_TIMESTAMP(%s)", col)
	case DialectPostgres:
		return fmt.Sprintf("EXTRACT(EPOCH FROM %s)::bigint", col)
	default: // SQLite
		return sqliteEpochExpr(col)
	}
}

// upsertSettingsSQL 返回 settings 表的 UPSERT 语句（方言相关）。
// SQLite/PostgreSQL 支持 ON CONFLICT ... DO UPDATE；
// MySQL 使用 ON DUPLICATE KEY UPDATE。
func (db *DB) upsertSettingsSQL() string {
	switch db.Dialect {
	case DialectMySQL:
		return `INSERT INTO settings (key, value) VALUES (?, ?) ON DUPLICATE KEY UPDATE value=VALUES(value)`
	default: // SQLite, PostgreSQL
		return `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`
	}
}

// vacuumSQL 返回空间回收语句（方言相关）。
// SQLite：VACUUM 重建整个数据库文件。
// MySQL：OPTIMIZE TABLE 重建表（InnoDB file_per_table 时回收空间）。
// PostgreSQL：VACUUM ANALYZE 标记空间可重用（不锁表，不缩小文件，但允许重用）。
func (db *DB) vacuumSQL() string {
	switch db.Dialect {
	case DialectMySQL:
		return `OPTIMIZE TABLE proxy_logs, backend_health_logs, audit_logs`
	case DialectPostgres:
		return `VACUUM ANALYZE proxy_logs, backend_health_logs, audit_logs`
	default: // SQLite
		return `VACUUM`
	}
}
