// migrate 是一次性数据库初始化命令：打开 DATABASE_PATH 指向的 SQLite 文件并
// 执行内嵌迁移后退出。服务启动时也会自动执行同样的迁移，本命令供部署预热使用。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/vancemichael/092002-dam-fish-passage/internal/store"
)

func main() {
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "data/app.sqlite3"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		log.Fatalf("迁移失败: %v", err)
	}
	if err := s.Close(); err != nil {
		log.Fatalf("关闭数据库失败: %v", err)
	}
	fmt.Println("数据库迁移完成:", dbPath)
}
