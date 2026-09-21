// Package store 提供过坝链路核验的 SQLite 持久化与逐段守恒规则。
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
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

// ErrNotFound 表示引用的记录不存在。
var ErrNotFound = errors.New("记录不存在")

// RuleError 是违反业务守恒规则（HTTP 422）。
type RuleError string

func (e RuleError) Error() string { return string(e) }

// ConflictError 是记录当前状态不允许该操作（HTTP 409）。
type ConflictError string

func (e ConflictError) Error() string { return string(e) }

func rulef(format string, args ...any) error {
	return RuleError(fmt.Sprintf(format, args...))
}

func conflictf(format string, args ...any) error {
	return ConflictError(fmt.Sprintf(format, args...))
}

// Store 封装数据库访问。
type Store struct {
	db *sql.DB
}

// Open 打开（必要时创建）数据库文件并执行未应用的迁移。
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		path = "data/app.sqlite3"
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file::memory:") {
		if dir := filepath.Dir(path); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("创建数据库目录 %s 失败: %w", dir, err)
			}
		}
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite 写事务互相排斥，单连接也让 foreign_keys 等 PRAGMA 对所有操作确定生效。
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭数据库。
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	if _, err := s.db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		return err
	}
	applied := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()

	for _, name := range names {
		if applied[name] {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, stmt := range splitStatements(string(body)) {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("迁移 %s 失败: %w", name, err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)`, name); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// splitStatements 按分号拆分 SQL 脚本（脚本内没有触发器等含分号的定义）。
func splitStatements(body string) []string {
	var out []string
	for _, raw := range strings.Split(body, ";") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		out = append(out, raw)
	}
	return out
}

// newID 生成带前缀的随机标识，如 HO-3F9A12C0D4E1。
func newID(prefix string) string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 随机源不可用时退化为时间戳，保证进程不崩溃。
		return fmt.Sprintf("%s-%X", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + strings.ToUpper(hex.EncodeToString(b[:]))
}

func nowText() string { return time.Now().Format(time.RFC3339) }

// parseTime 校验带时区偏移的 ISO 8601 时间；为空时取当前时间。
func parseTime(v string) (string, error) {
	if strings.TrimSpace(v) == "" {
		return nowText(), nil
	}
	if _, err := time.Parse(time.RFC3339, v); err != nil {
		return "", RuleError("时间字段必须是带时区偏移的 ISO 8601 字符串，例如 2026-09-20T08:30:00+08:00")
	}
	return v, nil
}
