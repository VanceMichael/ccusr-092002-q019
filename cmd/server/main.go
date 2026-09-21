package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/vancemichael/092002-dam-fish-passage/internal/httpapi"
	"github.com/vancemichael/092002-dam-fish-passage/internal/store"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "data/app.sqlite3"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		log.Fatalf("打开数据库 %s 失败: %v", dbPath, err)
	}
	defer db.Close()

	log.Printf("鱼类过坝链路核验服务启动，监听 :%s，数据库 %s", port, dbPath)
	log.Fatal(http.ListenAndServe(":"+port, httpapi.Router(db)))
}
