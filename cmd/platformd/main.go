// Command platformd 是 robot-platform-service 的守护进程入口。
// 同时挂载 v1(/v1/)与 v2(/v2/)API。
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

	nowFunc := func() int64 { return time.Now().UnixMilli() }
	evalV1 := domain.NewStatusEvaluator(nowFunc)
	evalV2 := domain.NewRuntimeLivenessEvaluator(nowFunc)

	mux := http.NewServeMux()
	mux.Handle("/v1/", api.NewHandler(st, evalV1))
	mux.Handle("/v2/", http.StripPrefix("/v2", api.NewHandlerV2(st, evalV2)))

	log.Printf("robot-platform-service v1+v2 listening on %s (db=%s)", *addr, *dbPath)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
