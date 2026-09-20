package httpapi

import (
	"context"
	"testing"

	"github.com/vancemichael/092002-dam-fish-passage/internal/store"
)

// openTestDB 为每个测试建立独立的内存数据库（迁移自动应用）。
func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
