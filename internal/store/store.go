// Package store 管理 SQLite 连接与迁移。驱动采用纯 Go 的 modernc.org/sqlite，
// 以便以 CGO_ENABLED=0 静态构建。
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// DB 包装 *sql.DB，附带本服务使用的少量连接配置。
type DB struct {
	*sql.DB
}

// Open 打开（必要时创建）path 指向的数据库文件并应用全部迁移。
// path 为 ":memory:" 时使用进程内内存库，供测试使用。
func Open(ctx context.Context, path string) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("DATABASE_PATH 不能为空")
	}
	if path != ":memory:" {
		// 自动创建数据目录，部署时无需额外 mkdir。
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("创建数据目录失败: %w", err)
		}
	}
	dsn := path
	if path != ":memory:" {
		// 忙等待，避免偶发锁错误；外键由下方 PRAGMA 显式开启。
		dsn += "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	}
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	if path == ":memory:" {
		// 内存库只在单连接内可见，限制连接池并在该连接上开启外键。
		sqlDB.SetMaxOpenConns(1)
	} else {
		sqlDB.SetMaxIdleConns(2)
		sqlDB.SetConnMaxLifetime(time.Hour)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	if path == ":memory:" {
		if _, err := sqlDB.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
	}
	db := &DB{sqlDB}
	if err := db.migrate(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) migrate(ctx context.Context) error {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("读取嵌入迁移失败: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	applied := map[string]bool{}
	rows, err := db.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		// bootstrap 之前表可能尚不存在，忽略首次查询错误，交由首个迁移建表。
		rows = nil
	} else {
		defer rows.Close()
		for rows.Next() {
			var version string
			if err := rows.Scan(&version); err != nil {
				return err
			}
			applied[version] = true
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}

	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		if applied[version] {
			continue
		}
		script, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, statement := range splitStatements(string(script)) {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("迁移 %s 执行失败: %w", version, err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)", version); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// splitStatements 按行拆分 SQL 脚本：以分号结尾的行视为一条语句结束。
// 本服务迁移不包含触发器或字符串内分号，足以满足需要。
func splitStatements(script string) []string {
	var statements []string
	var current strings.Builder
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		current.WriteString(line)
		current.WriteByte('\n')
		if strings.HasSuffix(trimmed, ";") {
			statement := strings.TrimSpace(current.String())
			if statement != "" {
				statements = append(statements, statement)
			}
			current.Reset()
		}
	}
	if tail := strings.TrimSpace(current.String()); tail != "" {
		statements = append(statements, tail)
	}
	return statements
}
