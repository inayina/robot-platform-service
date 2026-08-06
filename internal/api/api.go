// Package api 提供 v1 HTTP API。
//
// 边界(见 ARCHITECTURE_DESIGN.md 3.3):
//
//	负责: 设备注册/心跳/状态查询、任务与运行记录、健康检查;
//	不负责: 控制闭环、数据本体、评测判定、认证(v1 reserved)、多设备调度。
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/inayina/robot-platform-service/internal/domain"
	"github.com/inayina/robot-platform-service/internal/store"
)

// Server 聚合存储与状态判定器,提供路由。
type Server struct {
	store *store.Store
	eval  *domain.StatusEvaluator
}

// NewHandler 构建全部 v1 路由。
func NewHandler(st *store.Store, eval *domain.StatusEvaluator) http.Handler {
	s := &Server{store: st, eval: eval}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("POST /v1/devices", s.handleCreateDevice)
	mux.HandleFunc("GET /v1/devices", s.handleListDevices)
	mux.HandleFunc("GET /v1/devices/{id}", s.handleGetDevice)
	mux.HandleFunc("POST /v1/devices/{id}/heartbeats", s.handleHeartbeat)
	mux.HandleFunc("POST /v1/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /v1/tasks", s.handleListTasks)
	mux.HandleFunc("POST /v1/runs", s.handleCreateRun)
	mux.HandleFunc("GET /v1/runs/{id}", s.handleGetRun)
	return mux
}

// --- handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"time_ms": s.eval.Now(),
	})
}

func (s *Server) handleCreateDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID                  string `json:"id"`
		Name                string `json:"name"`
		Kind                string `json:"kind"`
		Version             string `json:"version"`
		HeartbeatIntervalMs int64  `json:"heartbeat_interval_ms"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	now := s.eval.Now()
	d := &domain.Device{
		ID:                  req.ID,
		Name:                req.Name,
		Kind:                req.Kind,
		Version:             req.Version,
		HeartbeatIntervalMs: req.HeartbeatIntervalMs,
		FirstSeenMs:         now,
	}
	if d.ID == "" {
		d.ID = store.NewID("dev") // 信封原则:平台统辖 id
	}
	if d.HeartbeatIntervalMs <= 0 {
		d.HeartbeatIntervalMs = domain.DefaultHeartbeatIntervalMs
	}
	if err := s.store.CreateDevice(r.Context(), d); err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			writeErr(w, http.StatusConflict, "device already exists")
		default:
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	d.Status = s.eval.Evaluate(d)
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	devs, err := s.store.ListDevices(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range devs {
		devs[i].Status = s.eval.Evaluate(&devs[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devs, "count": len(devs)})
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, err := s.store.GetDevice(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "device not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	hb, err := s.store.LastHeartbeat(r.Context(), id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.Status = s.eval.Evaluate(d)
	resp := map[string]any{"device": d}
	if hb != nil {
		resp["last_heartbeat"] = hb
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Seq int64 `json:"seq"`
		Ts  int64 `json:"ts_ms"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Seq <= 0 {
		writeErr(w, http.StatusBadRequest, "seq must be positive")
		return
	}
	if req.Ts <= 0 {
		req.Ts = s.eval.Now()
	}
	if err := s.store.AddHeartbeat(r.Context(), domain.Heartbeat{
		DeviceID: id, Seq: req.Seq, TsMs: req.Ts,
	}); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, "device not found")
		case errors.Is(err, store.ErrSeqRegression):
			writeErr(w, http.StatusConflict, err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "device_id": id, "seq": req.Seq})
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string `json:"id"`
		Domain string `json:"domain"`
		Kind   string `json:"kind"`
		Target string `json:"target"`
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Domain == "" {
		writeErr(w, http.StatusBadRequest, "domain is required (amr|panda|...)")
		return
	}
	now := s.eval.Now()
	t := &domain.Task{
		ID:        req.ID,
		Domain:    req.Domain,
		Kind:      req.Kind,
		Target:    req.Target,
		Status:    domain.TaskStatus(req.Status),
		CreatedMs: now,
		UpdatedMs: now,
	}
	if t.ID == "" {
		t.ID = store.NewID("task")
	}
	if t.Status == "" {
		t.Status = domain.TaskPending
	}
	if err := s.store.CreateTask(r.Context(), t); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, http.StatusConflict, "task already exists")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	tasks, err := s.store.ListTasks(r.Context(), status)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks, "count": len(tasks)})
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID          string `json:"id"`
		TaskID      string `json:"task_id"`
		DeviceID    string `json:"device_id"`
		StartedMs   int64  `json:"started_ms"`
		EndedMs     int64  `json:"ended_ms"`
		Result      string `json:"result"`
		ArtifactRef string `json:"artifact_ref"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.TaskID == "" || req.DeviceID == "" {
		writeErr(w, http.StatusBadRequest, "task_id and device_id are required")
		return
	}
	now := s.eval.Now()
	run := &domain.Run{
		ID:          req.ID,
		TaskID:      req.TaskID,
		DeviceID:    req.DeviceID,
		StartedMs:   req.StartedMs,
		EndedMs:     req.EndedMs,
		Result:      req.Result,
		ArtifactRef: req.ArtifactRef,
	}
	if run.ID == "" {
		run.ID = store.NewID("run")
	}
	if run.StartedMs <= 0 {
		run.StartedMs = now
	}
	if err := s.store.CreateRun(r.Context(), run); err != nil {
		switch {
		case errors.Is(err, store.ErrBadReference):
			writeErr(w, http.StatusUnprocessableEntity, "task or device does not exist")
		default:
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.store.GetRun(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "run not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// --- helpers ---

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 信封接口,拒绝大报文
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			writeErr(w, http.StatusBadRequest, "empty body")
			return false
		}
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
