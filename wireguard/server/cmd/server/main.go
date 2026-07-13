package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"sogame/wireguard/server/internal/api"
	"sogame/wireguard/server/internal/db"
	"sogame/wireguard/server/internal/ipam"
	"sogame/wireguard/server/internal/room"
	"sogame/wireguard/server/internal/ws"
)

func main() {
	dbPath := envOrDefault("SOGAME_DB_PATH", "/data/sogame.db")
	listenAddr := envOrDefault("SOGAME_LISTEN", ":8080")
	webDir := envOrDefault("SOGAME_WEB_DIR", "/web")

	// 确保数据目录存在
	dataDir := filepathDir(dbPath)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Printf("warning: create data dir: %v", err)
	}

	// 初始化数据库
	database, err := db.New(dbPath)
	if err != nil {
		log.Fatalf("failed to init database: %v", err)
	}
	defer database.Close()

	// 初始化组件
	hub := ws.New()
	ipamMgr := ipam.New(database)
	roomMgr := room.New(database, ipamMgr, hub)
	apiHandler := api.New(roomMgr, database)

	// 注册路由
	mux := http.NewServeMux()
	apiHandler.RegisterRoutes(mux)

	// 健康检查端点
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// WebSocket 路由
	mux.HandleFunc("/ws/room/", func(w http.ResponseWriter, r *http.Request) {
		roomID := strings.TrimPrefix(r.URL.Path, "/ws/room/")
		if roomID == "" {
			http.Error(w, "room_id is required", http.StatusBadRequest)
			return
		}
		ws.HandleWS(hub, w, r, roomID)
	})

	// 静态文件服务（Web UI）
	if _, err := os.Stat(webDir); err == nil {
		mux.Handle("/", http.FileServer(http.Dir(webDir)))
	}

	// CORS 中间件
	handler := corsMiddleware(mux)

	// 使用 http.Server 支持优雅关闭
	server := &http.Server{
		Addr:         listenAddr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 监听信号实现优雅关闭
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("SoGame control server listening on %s", listenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-stop
	log.Println("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("warning: forced shutdown: %v", err)
	}

	log.Println("server stopped")
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func envOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// filepathDir 返回路径的目录部分（避免引入 path/filepath 仅为此一处）
func filepathDir(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return "."
	}
	if idx == 0 {
		return "/"
	}
	return path[:idx]
}
