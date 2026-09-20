package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/vancemichael/092002-dam-fish-passage/internal/domain"
	"github.com/vancemichael/092002-dam-fish-passage/internal/httpapi"
	"github.com/vancemichael/092002-dam-fish-passage/internal/store"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	databasePath := os.Getenv("DATABASE_PATH")
	if databasePath == "" {
		databasePath = "data/app.sqlite3"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := store.Open(ctx, databasePath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	api := httpapi.NewAPI(domain.NewService(db))
	log.Printf("鱼类过坝核验服务启动，监听 :%s，数据库 %s", port, databasePath)
	log.Fatal(http.ListenAndServe(":"+port, api.Router()))
}
