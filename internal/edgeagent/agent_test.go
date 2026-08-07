package edgeagent_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inayina/robot-platform-service/internal/edgeagent"
)

type fakePlatform struct {
	*httptest.Server
	nextSeq atomic.Int64
}

func newFakePlatform(t *testing.T) *fakePlatform {
	t.Helper()
	fp := &fakePlatform{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/devices", fp.handleRegister)
	mux.HandleFunc("POST /v1/devices/{id}/heartbeats", fp.handleHeartbeat)
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	fp.Server = httptest.NewServer(mux)
	t.Cleanup(fp.Server.Close)
	return fp
}

func (fp *fakePlatform) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct{ ID string }
	json.NewDecoder(r.Body).Decode(&req)
	w.Header().Set("Content-Type", "application/json")
	if req.ID == "dup-device" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "device already exists"})
		return
	}
	writeJSON(w, http.StatusCreated, req)
}

func (fp *fakePlatform) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seq       int64           `json:"seq"`
		SessionID string          `json:"session_id"`
		Metrics   json.RawMessage `json:"metrics"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	w.Header().Set("Content-Type", "application/json")
	if req.Seq <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "seq must be positive"})
		return
	}
	expected := fp.nextSeq.Add(1)
	if req.Seq < expected {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "heartbeat seq regression"})
		return
	}
	fp.nextSeq.Store(req.Seq)
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "seq": req.Seq})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func TestAgentRegistersAndSendsHeartbeat(t *testing.T) {
	fp := newFakePlatform(t)
	cfg := &edgeagent.Config{
		PlatformBaseURL:     fp.URL,
		RobotID:            "test-robot",
		DeviceID:           "test-robot",
		RuntimeType:        "orangepi-edge",
		RuntimeVersion:     "0.1.0-test",
		HeartbeatIntervalMs: 200,
		RequestTimeoutMs:    5000,
	}
	cfg.Init()
	a := edgeagent.New(cfg)

	errCh := make(chan error, 1)
	go func() { errCh <- a.Run() }()

	time.Sleep(500 * time.Millisecond)
	a.Shutdown()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("shutdown error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("agent did not shut down")
	}
}

func TestDuplicateRegistrationIsOk(t *testing.T) {
	fp := newFakePlatform(t)
	cfg := &edgeagent.Config{
		PlatformBaseURL:     fp.URL,
		RobotID:            "dup-device",
		DeviceID:           "dup-device",
		RuntimeType:        "test",
		HeartbeatIntervalMs: 500,
		RequestTimeoutMs:    5000,
	}
	cfg.Init()
	a := edgeagent.New(cfg)

	errCh := make(chan error, 1)
	go func() { errCh <- a.Run() }()

	time.Sleep(800 * time.Millisecond)
	a.Shutdown()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("agent should survive 409: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("agent did not shut down")
	}
}

func TestSessionIDChangesAfterRestart(t *testing.T) {
	fp := newFakePlatform(t)
	makeAgent := func() *edgeagent.Agent {
		cfg := &edgeagent.Config{
			PlatformBaseURL:     fp.URL,
			RobotID:            "session-test",
			DeviceID:           "session-test",
			HeartbeatIntervalMs: 200,
			RequestTimeoutMs:    5000,
		}
		cfg.Init()
		return edgeagent.New(cfg)
	}

	run := func(a *edgeagent.Agent) {
		errCh := make(chan error, 1)
		go func() { errCh <- a.Run() }()
		time.Sleep(400 * time.Millisecond)
		a.Shutdown()
		<-errCh
		fp.nextSeq.Store(0) // 重置 seq,模拟设备新 session(真实 Platform 按设备跟踪 seq)
	}
	run(makeAgent())
	run(makeAgent())
	t.Log("both sessions completed without seq regression")
}

func TestPermanentHTTP4xxShutsDown(t *testing.T) {
	fp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"bad request"}`)
	}))
	defer fp.Close()

	cfg := &edgeagent.Config{
		PlatformBaseURL:     fp.URL,
		RobotID:            "permanent-400",
		DeviceID:           "permanent-400",
		HeartbeatIntervalMs: 100,
		RequestTimeoutMs:    5000,
	}
	cfg.Init()
	a := edgeagent.New(cfg)

	if err := a.Run(); err == nil {
		t.Fatal("expected error on permanent 400")
	}
	t.Logf("agent correctly shut down on permanent 400")
}

func TestContextCancelExits(t *testing.T) {
	fp := newFakePlatform(t)
	cfg := &edgeagent.Config{
		PlatformBaseURL:     fp.URL,
		RobotID:            "ctx-cancel-test",
		DeviceID:           "ctx-cancel-test",
		HeartbeatIntervalMs: 500,
		RequestTimeoutMs:    5000,
	}
	cfg.Init()
	a := edgeagent.New(cfg)

	errCh := make(chan error, 1)
	go func() { errCh <- a.Run() }()

	time.Sleep(800 * time.Millisecond)
	a.Shutdown()

	select {
	case err := <-errCh:
		t.Logf("agent exited with: %v", err)
	case <-time.After(time.Second):
		t.Fatal("agent did not exit on shutdown")
	}
}
