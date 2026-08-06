// Command platformd 是 robot-platform-service 的守护进程入口。
//
// 定位:管理面汇聚服务(单机/少量设备),不是云平台、不做控制闭环。
// 用法:
//
//	go run ./cmd/platformd -addr :9100 -db data/platform.db
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/inayina/robot-platform-service/internal/api"
	"github.com/inayina/robot-platform-service/internal/domain"
	"github.com/inayina/robot-platform-service/internal/store"
)

func main() {
	addr := flag.String("addr", ":9100", "HTTP listen address")
	dbPath := flag.String("db", "data/platform.db", "SQLite database path")
	flag.Parse()

	if dir := filepath.Dir(*dbPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("create db dir: %v", err)
		}
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// 生产时钟:epoch 毫秒;测试通过注入固定时钟保证 stale 判定可测。
	eval := domain.NewStatusEvaluator(func() int64 { return time.Now().UnixMilli() })

	log.Printf("robot-platform-service listening on %s (db=%s)", *addr, *dbPath)
	if err := http.ListenAndServe(*addr, api.NewHandler(st, eval)); err != nil {
		log.Fatalf("server: %v", err)
	}
}
