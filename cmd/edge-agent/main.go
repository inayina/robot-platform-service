// Command edge-agent 是 Orange Pi Edge Agent 的入口。
//
// 用法(环境变量注入):
//
//	export PLATFORM_BASE_URL=http://10.0.0.5:9100
//	export ROBOT_ID=opi-edge-001
//	go run ./cmd/edge-agent
//
// 详细配置见 .env.example。
package main

import (
	"log"

	"github.com/inayina/robot-platform-service/internal/edgeagent"
)

func main() {
	cfg := edgeagent.DefaultConfig()
	cfg.Init()
	agent := edgeagent.New(cfg)

	log.Printf("[edge-agent] starting robot=%s platform=%s", cfg.RobotID, cfg.PlatformBaseURL)
	if err := agent.Run(); err != nil {
		log.Fatalf("[edge-agent] fatal: %v", err)
	}
	log.Println("[edge-agent] shutdown complete")
}
