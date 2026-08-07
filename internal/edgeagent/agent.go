// Package edgeagent 提供 Edge Agent 的实现(Orange Pi / 通用 Linux 主机管理面上报)。
//
// 边界:
//   - 只做管理面(注册 + 心跳 + 主机指标上报)
//   - 不进控制闭环、不做实时控制、不接 CAN
//   - Agent 崩溃/重启 → 新的 session_id → 平台不自动重放过期命令(本 Agent 不发命令)
package edgeagent

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/inayina/robot-platform-service/internal/edgeagent/hostmetrics"
	"github.com/inayina/robot-platform-service/internal/edgeagent/platformclient"
)

// Config 从环境变量或命令行注入。
type Config struct {
	PlatformBaseURL     string
	RobotID             string
	DeviceID            string
	RuntimeID           string
	RuntimeType         string
	RuntimeVersion      string
	HeartbeatIntervalMs int64
	RequestTimeoutMs    int64
}

// DefaultConfig 返回从环境变量 + 内置默认值构造的配置。
// 调用后需执行 Init() 补全缺省字段。
func DefaultConfig() *Config {
	return &Config{
		PlatformBaseURL:     envOr("PLATFORM_BASE_URL", "http://127.0.0.1:9100"),
		RobotID:             envOr("ROBOT_ID", hostnameOr("opi-edge-unknown")),
		DeviceID:            envOr("DEVICE_ID", ""),
		RuntimeID:           envOr("RUNTIME_ID", ""),
		RuntimeType:         envOr("RUNTIME_TYPE", "orangepi-edge"),
		RuntimeVersion:      envOr("RUNTIME_VERSION", "0.1.0-dev"),
		HeartbeatIntervalMs: envInt64Or("HEARTBEAT_INTERVAL_MS", 5000),
		RequestTimeoutMs:    envInt64Or("REQUEST_TIMEOUT_MS", 10000),
	}
}

// Init 规范化配置(必须在 DefaultConfig 后调用)。
func (c *Config) Init() {
	if c.DeviceID == "" {
		c.DeviceID = c.RobotID
	}
	if c.RuntimeID == "" {
		c.RuntimeID = c.RobotID
	}
}

// Agent 管理面 Edge Agent。
type Agent struct {
	cfg       *Config
	client    *platformclient.Client
	metrics   *hostmetrics.Collector
	sessionID string
	seq       int64
	mu        sync.Mutex
	cancel    context.CancelFunc
	ctx       context.Context // Agent 生命周期,Shutdown 触发
}

// New 构建 Agent。
func New(cfg *Config) *Agent {
	cfg.Init()
	ctx, cancel := context.WithCancel(context.Background())
	return &Agent{
		cfg:       cfg,
		client:    platformclient.New(cfg.PlatformBaseURL, time.Duration(cfg.RequestTimeoutMs)*time.Millisecond),
		metrics:   hostmetrics.New(),
		sessionID: newSessionID(),
		cancel:    cancel,
		ctx:       ctx,
	}
}

// Run 启动 Agent 主循环:注册→心跳循环→信号退出。
func (a *Agent) Run() error {
	// 使用 Agent 已创建的 ctx 作为生命周期(Shutdown 触发 cancel)
	ctx := a.ctx

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	if err := a.register(ctx); err != nil {
		return fmt.Errorf("register: %w", err)
	}
	log.Printf("[edge-agent] device=%s session=%s seq=0", a.cfg.DeviceID, a.sessionID)

	ticker := time.NewTicker(time.Duration(a.cfg.HeartbeatIntervalMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := a.sendHeartbeat(ctx); err != nil {
				if cerr, ok := err.(*platformclient.Error); ok && cerr.StatusCode >= 400 && cerr.StatusCode < 500 {
					log.Printf("[edge-agent] permanent HTTP %d, shutting down: %s", cerr.StatusCode, cerr.Body)
					return err
				}
				log.Printf("[edge-agent] heartbeat error: %v", err)
			}
		case sig := <-sigCh:
			log.Printf("[edge-agent] signal %v, shutdown heartbeat", sig)
			_ = a.sendShutdownHeartbeat(ctx)
			return nil
		case <-a.ctx.Done():
			// Shutdown 触发:优雅退出
			return nil
		}
	}
}

// Shutdown 优雅退出。
func (a *Agent) Shutdown() {
	if a.cancel != nil {
		a.cancel()
	}
}

// --- 内部方法 ---

func (a *Agent) register(ctx context.Context) error {
	host, _ := a.metrics.Collect(ctx)
	if host == nil {
		host = &hostmetrics.Snapshot{}
	}
	req := platformclient.RegisterDeviceRequest{
		ID:                  a.cfg.DeviceID,
		Name:                a.cfg.RobotID,
		Kind:                a.cfg.RuntimeType,
		Version:             a.cfg.RuntimeVersion,
		Hostname:            host.Hostname,
		Arch:                host.Arch,
		OS:                  host.OS,
		HeartbeatIntervalMs: a.cfg.HeartbeatIntervalMs,
	}
	err := a.client.RegisterDevice(ctx, req)
	if err != nil {
		if cerr, ok := err.(*platformclient.Error); ok && cerr.StatusCode == 409 {
			log.Printf("[edge-agent] device %s already registered (previous run), ok", a.cfg.DeviceID)
			return nil
		}
		return err
	}
	return nil
}

func (a *Agent) sendHeartbeat(ctx context.Context) error {
	a.mu.Lock()
	a.seq++
	seq := a.seq
	sid := a.sessionID
	a.mu.Unlock()

	host, _ := a.metrics.Collect(ctx)
	if host == nil {
		host = &hostmetrics.Snapshot{}
	}

	req := platformclient.HeartbeatRequest{
		Seq:       seq,
		SessionID: sid,
		Metrics: platformclient.HostMetricsPayload{
			CPUPercent:         host.CPUPercent,
			MemoryPercent:      host.MemoryPercent,
			TemperatureCelsius: host.TemperatureCelsius,
			CanAvailable:       host.CanAvailable,
			RuntimeState:       "idle",
		},
	}
	return a.client.SendHeartbeat(ctx, a.cfg.DeviceID, req)
}

func (a *Agent) sendShutdownHeartbeat(ctx context.Context) error {
	a.mu.Lock()
	a.seq++
	seq := a.seq
	sid := a.sessionID
	a.mu.Unlock()

	req := platformclient.HeartbeatRequest{
		Seq:       seq,
		SessionID: sid,
		Metrics: platformclient.HostMetricsPayload{
			RuntimeState: "shutdown",
		},
	}
	return a.client.SendHeartbeat(ctx, a.cfg.DeviceID, req)
}

// --- helpers ---

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt64Or(key string, def int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	if n, err := time.ParseDuration(v); err == nil {
		return int64(n / time.Millisecond)
	}
	return def
}

func hostnameOr(fallback string) string {
	h, err := os.Hostname()
	if err != nil {
		return fallback
	}
	return h
}

func newSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("sess-%d-%x", time.Now().UnixMilli(), b)
}
