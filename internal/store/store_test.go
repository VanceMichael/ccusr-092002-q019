package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenAppliesMigrationsAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.sqlite3")
	ctx := context.Background()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("首次打开失败: %v", err)
	}
	var version string
	if err := db.QueryRowContext(ctx,
		"SELECT version FROM schema_migrations ORDER BY version").Scan(&version); err != nil {
		t.Fatalf("迁移版本查询失败: %v", err)
	}
	if version != "001_bootstrap" {
		t.Fatalf("迁移版本应为 001_bootstrap，实际 %q", version)
	}
	if _, err := db.ExecContext(ctx,
		"INSERT INTO lots(lot_code, species_code, origin, initial_count, created_at) VALUES (?,?,?,?,?)",
		"L-PERSIST", "SP", "WILD", 3, "2026-09-20T00:00:00+08:00"); err != nil {
		t.Fatalf("写入批次失败: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	// 重新打开：迁移幂等，数据仍在，外键仍然生效。
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("重新打开失败: %v", err)
	}
	defer reopened.Close()
	var count int
	if err := reopened.QueryRowContext(ctx,
		"SELECT initial_count FROM lots WHERE lot_code = ?", "L-PERSIST").Scan(&count); err != nil {
		t.Fatalf("持久化数据读取失败: %v", err)
	}
	if count != 3 {
		t.Fatalf("持久化数量应为 3，实际 %d", count)
	}
	if _, err := reopened.ExecContext(ctx,
		"INSERT INTO handoffs(lot_code, stage_code, seq, expected_count, created_at) VALUES (?,?,?,?,?)",
		"NO-SUCH-LOT", "SORTING", 1, 1, "2026-09-20T00:00:00+08:00"); err == nil {
		t.Fatal("外键约束未生效：不存在的批次不应能写入交接")
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("空 DATABASE_PATH 应报错")
	}
}
