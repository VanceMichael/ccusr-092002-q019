// Command migrate 仅执行数据库迁移后退出，适合部署前的初始化作业。
// 服务启动时也会自动应用迁移，本命令供需要提前建库的运维场景使用。
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/vancemichael/092002-dam-fish-passage/internal/store"
)

func main() {
	databasePath := os.Getenv("DATABASE_PATH")
	if databasePath == "" {
		databasePath = "data/app.sqlite3"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := store.Open(ctx, databasePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "迁移失败: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	fmt.Printf("迁移已应用：%s\n", databasePath)
}
