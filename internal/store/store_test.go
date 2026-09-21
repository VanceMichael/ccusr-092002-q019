package store

import (
	"context"
	"path/filepath"
	"testing"
)

// newTestStore 在临时目录打开一个全新数据库（迁移自动执行）。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func boxes(refs []string, counts ...int) []ContainerCount {
	if len(counts) == 0 {
		counts = make([]int, len(refs))
	}
	out := make([]ContainerCount, len(refs))
	for i, ref := range refs {
		out[i] = ContainerCount{ContainerRef: ref, SentCount: counts[i]}
	}
	return out
}

func rcvBoxes(refs []string, counts ...int) []ContainerCount {
	out := make([]ContainerCount, len(refs))
	for i, ref := range refs {
		out[i] = ContainerCount{ContainerRef: ref, ReceivedCount: counts[i]}
	}
	return out
}

func ptrF(v float64) *float64 { return &v }
