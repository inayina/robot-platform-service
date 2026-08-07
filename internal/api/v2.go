// Package api — Management Plane v2 本地草案 HTTP API。
//
// v2 端点以 docs/ROBOT_DEVICE_RUNTIME_CONTRACT.md 定义的 Robot/Device/Runtime
// 拆分模型。v1 端点(api.go)全部保留不变；v2 注册在 /v2/ 前缀下。
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/inayina/robot-platform-service/internal/domain"
	"github.com/inayina/robot-platform-service/internal/store"
)

// ServerV2 聚合存储与 v2 判定器。
type ServerV2 struct {
	store *store.Store
	eval  *domain.RuntimeLivenessEvaluator
}

// NewHandlerV2 构建 v2 路由(不带 /v2 前缀，由调用方用 http.StripPrefix 挂载)。
func NewHandlerV2(st *store.Store, eval *domain.RuntimeLivenessEvaluator) http.Handler {
	s := &ServerV2{store: st, eval: eval}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealthV2)

	// Robot
	mux.HandleFunc("POST /robots", s.handleCreateRobot)
	mux.HandleFunc("GET /robots", s.handleListRobots)
	mux.HandleFunc("GET /robots/{id}", s.handleGetRobot)

	// Device(v2)
	mux.HandleFunc("POST /robots/{robot_id}/devices", s.handleCreateDeviceV2)
	mux.HandleFunc("GET /robots/{robot_id}/devices", s.handleListDevicesByRobot)
	mux.HandleFunc("GET /devices/{id}", s.handleGetDeviceV2)

	// Runtime
	mux.HandleFunc("POST /robots/{robot_id}/runtimes", s.handleCreateRuntime)
	mux.HandleFunc("GET /robots/{robot_id}/runtimes", s.handleListRuntimesByRobot)
	mux.HandleFunc("GET /runtimes/{id}", s.handleGetRuntime)
	mux.HandleFunc("GET /runtimes/{id}/liveness", s.handleGetRuntimeLiveness)

	// RuntimeSession
	mux.HandleFunc("POST /runtimes/{id}/sessions", s.handleCreateSession)
	mux.HandleFunc("GET /runtimes/{id}/sessions", s.handleListSessions)
	mux.HandleFunc("POST /runtimes/{id}/sessions/{sid}/heartbeats", s.handleRuntimeHeartbeat)
	mux.HandleFunc("POST /runtimes/{id}/sessions/{sid}/end", s.handleEndSession)

	// Run(v2)
	mux.HandleFunc("POST /runs", s.handleCreateRunV2)
	mux.HandleFunc("GET /runs", s.handleListRunsV2)
	mux.HandleFunc("GET /runs/{id}", s.handleGetRunV2)

	return mux
}

// ---------------------------------------------------------------------------
// Health
// ---------------------------------------------------------------------------

func (s *ServerV2) handleHealthV2(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": "v2",
		"time_ms": s.eval.Now(),
	})
}

// ---------------------------------------------------------------------------
// Robot
// ---------------------------------------------------------------------------

func (s *ServerV2) handleCreateRobot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DisplayName  string              `json:"display_name"`
		Domain       string              `json:"domain"`
		Embodiment   string              `json:"embodiment"`
		ExternalRefs domain.ExternalRefs `json:"external_refs,omitempty"`
	}
	if !decodeJSONStrict(w, r, &req) {
		return
	}
	if req.DisplayName == "" || req.Domain == "" || req.Embodiment == "" {
		writeErr(w, http.StatusBadRequest, "display_name, domain, and embodiment are required")
		return
	}
	if req.Embodiment != "physical" && req.Embodiment != "simulation" {
		writeErr(w, http.StatusBadRequest, "embodiment must be 'physical' or 'simulation'")
		return
	}
	robotID, ok := newCanonicalID(w, store.RobotIDPrefix)
	if !ok {
		return
	}
	now := s.eval.Now()
	robot := &domain.Robot{
		ID:             robotID,
		DisplayName:    req.DisplayName,
		Domain:         req.Domain,
		Embodiment:     req.Embodiment,
		LifecycleState: domain.RobotActive,
		ExternalRefs:   req.ExternalRefs,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.store.CreateRobot(r.Context(), robot); err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			writeErr(w, http.StatusConflict, "robot already exists")
		case errors.Is(err, store.ErrInvalidArgument):
			writeErr(w, http.StatusBadRequest, err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, robot)
}

func (s *ServerV2) handleListRobots(w http.ResponseWriter, r *http.Request) {
	robots, err := s.store.ListRobots(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"robots": robots, "count": len(robots)})
}

func (s *ServerV2) handleGetRobot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	robot, err := s.store.GetRobot(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "robot not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, robot)
}

// ---------------------------------------------------------------------------
// Device v2
// ---------------------------------------------------------------------------

func (s *ServerV2) handleCreateDeviceV2(w http.ResponseWriter, r *http.Request) {
	robotID := r.PathValue("robot_id")
	var req struct {
		ParentDeviceID string              `json:"parent_device_id,omitempty"`
		DisplayName    string              `json:"display_name"`
		DeviceClass    domain.DeviceClass  `json:"device_class"`
		DomainType     string              `json:"domain_type,omitempty"`
		Manufacturer   string              `json:"manufacturer,omitempty"`
		Model          string              `json:"model,omitempty"`
		SerialNumber   string              `json:"serial_number,omitempty"`
		ExternalRefs   domain.ExternalRefs `json:"external_refs,omitempty"`
	}
	if !decodeJSONStrict(w, r, &req) {
		return
	}
	if req.DisplayName == "" || req.DeviceClass == "" {
		writeErr(w, http.StatusBadRequest, "display_name and device_class are required")
		return
	}
	if !isValidDeviceClass(req.DeviceClass) {
		writeErr(w, http.StatusBadRequest, "invalid device_class")
		return
	}
	deviceID, ok := newCanonicalID(w, store.DeviceIDPrefix)
	if !ok {
		return
	}
	now := s.eval.Now()
	d := &domain.DeviceV2{
		ID:             deviceID,
		RobotID:        robotID,
		ParentDeviceID: req.ParentDeviceID,
		DisplayName:    req.DisplayName,
		DeviceClass:    req.DeviceClass,
		DomainType:     req.DomainType,
		Manufacturer:   req.Manufacturer,
		Model:          req.Model,
		SerialNumber:   req.SerialNumber,
		LifecycleState: string(domain.RobotActive),
		ExternalRefs:   req.ExternalRefs,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.store.CreateDeviceV2(r.Context(), d); err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			writeErr(w, http.StatusConflict, "device already exists")
		case errors.Is(err, store.ErrBadReference):
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, "robot not found")
		case errors.Is(err, store.ErrInvalidArgument):
			writeErr(w, http.StatusBadRequest, err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *ServerV2) handleListDevicesByRobot(w http.ResponseWriter, r *http.Request) {
	robotID := r.PathValue("robot_id")
	devs, err := s.store.ListDevicesByRobot(r.Context(), robotID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "robot not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devs, "count": len(devs)})
}

func (s *ServerV2) handleGetDeviceV2(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, err := s.store.GetDeviceV2(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "device not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// ---------------------------------------------------------------------------
// Runtime
// ---------------------------------------------------------------------------

func (s *ServerV2) handleCreateRuntime(w http.ResponseWriter, r *http.Request) {
	robotID := r.PathValue("robot_id")
	var req struct {
		DisplayName         string              `json:"display_name"`
		RuntimeRole         domain.RuntimeRole  `json:"runtime_role"`
		Component           string              `json:"component"`
		HostDeviceID        string              `json:"host_device_id,omitempty"`
		HeartbeatIntervalMs *int64              `json:"heartbeat_interval_ms,omitempty"`
		ExternalRefs        domain.ExternalRefs `json:"external_refs,omitempty"`
	}
	if !decodeJSONStrict(w, r, &req) {
		return
	}
	if req.DisplayName == "" || req.RuntimeRole == "" || req.Component == "" {
		writeErr(w, http.StatusBadRequest, "display_name, runtime_role, and component are required")
		return
	}
	if !isValidRuntimeRole(req.RuntimeRole) {
		writeErr(w, http.StatusBadRequest, "invalid runtime_role")
		return
	}
	heartbeatIntervalMs := domain.DefaultRuntimeHeartbeatIntervalMs
	if req.HeartbeatIntervalMs != nil {
		if *req.HeartbeatIntervalMs <= 0 {
			writeErr(w, http.StatusBadRequest, "heartbeat_interval_ms must be positive")
			return
		}
		heartbeatIntervalMs = *req.HeartbeatIntervalMs
	}
	runtimeID, ok := newCanonicalID(w, store.RuntimeIDPrefix)
	if !ok {
		return
	}
	now := s.eval.Now()
	rt := &domain.Runtime{
		ID:                  runtimeID,
		RobotID:             robotID,
		DisplayName:         req.DisplayName,
		RuntimeRole:         req.RuntimeRole,
		Component:           req.Component,
		HostDeviceID:        req.HostDeviceID,
		HeartbeatIntervalMs: heartbeatIntervalMs,
		LifecycleState:      string(domain.RobotActive),
		ExternalRefs:        req.ExternalRefs,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := s.store.CreateRuntime(r.Context(), rt); err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			writeErr(w, http.StatusConflict, "runtime already exists")
		case errors.Is(err, store.ErrBadReference):
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, "robot not found")
		case errors.Is(err, store.ErrInvalidArgument):
			writeErr(w, http.StatusBadRequest, err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, rt)
}

func (s *ServerV2) handleListRuntimesByRobot(w http.ResponseWriter, r *http.Request) {
	robotID := r.PathValue("robot_id")
	runtimes, err := s.store.ListRuntimesByRobot(r.Context(), robotID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "robot not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 为每个 Runtime 附加 current session 和 liveness
	type runtimeWithLiveness struct {
		*domain.Runtime
		Liveness       domain.RuntimeLiveness `json:"liveness"`
		CurrentSession *domain.RuntimeSession `json:"current_session,omitempty"`
	}
	out := make([]runtimeWithLiveness, len(runtimes))
	for i := range runtimes {
		cs, _ := s.store.GetCurrentSession(r.Context(), runtimes[i].ID)
		out[i] = runtimeWithLiveness{
			Runtime:        &runtimes[i],
			Liveness:       s.eval.Evaluate(&runtimes[i], cs),
			CurrentSession: cs,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runtimes": out, "count": len(out)})
}

func (s *ServerV2) handleGetRuntime(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rt, err := s.store.GetRuntime(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "runtime not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	cs, _ := s.store.GetCurrentSession(r.Context(), id)
	resp := map[string]any{
		"runtime":  rt,
		"liveness": s.eval.Evaluate(rt, cs),
	}
	if cs != nil {
		resp["current_session"] = cs
	} else {
		// 无 current session 时回退到最近 session(用于判断 ended 导致的 offline)
		sessions, _ := s.store.ListRuntimeSessions(r.Context(), id)
		if len(sessions) > 0 {
			resp["last_session"] = &sessions[0]
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *ServerV2) handleGetRuntimeLiveness(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rt, err := s.store.GetRuntime(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "runtime not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 首选 current session；无 current 时取最近 session(用于判断是否明确 ended)。
	sess, _ := s.store.GetCurrentSession(r.Context(), id)
	if sess == nil {
		sessions, _ := s.store.ListRuntimeSessions(r.Context(), id)
		if len(sessions) > 0 {
			sess = &sessions[0]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"runtime_id": id,
		"liveness":   s.eval.Evaluate(rt, sess),
	})
}

// ---------------------------------------------------------------------------
// RuntimeSession
// ---------------------------------------------------------------------------

func (s *ServerV2) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	runtimeID := r.PathValue("id")
	var req struct {
		SessionID          string `json:"session_id"`
		SoftwareVersionRef string `json:"software_version_ref"`
		StartedAtReported  int64  `json:"started_at_reported"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.SessionID == "" {
		writeErr(w, http.StatusBadRequest, "session_id is required")
		return
	}
	now := s.eval.Now()
	sess := &domain.RuntimeSession{
		SessionID:          req.SessionID,
		RuntimeID:          runtimeID,
		SoftwareVersionRef: req.SoftwareVersionRef,
		StartedAtReported:  req.StartedAtReported,
		StartedAtReceived:  now,
	}
	if sess.SoftwareVersionRef == "" {
		sess.SoftwareVersionRef = "unknown"
	}

	result, err := s.store.CreateRuntimeSession(r.Context(), sess)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, "runtime not found")
		case errors.Is(err, store.ErrBadReference):
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
		case errors.Is(err, store.ErrInvalidArgument):
			writeErr(w, http.StatusBadRequest, err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	// 幂等返回(可能是 superseded 的旧记录)
	code := http.StatusCreated
	if result.SessionState != domain.SessionCurrent {
		code = http.StatusOK
	}
	writeJSON(w, code, result)
}

func (s *ServerV2) handleListSessions(w http.ResponseWriter, r *http.Request) {
	runtimeID := r.PathValue("id")
	sessions, err := s.store.ListRuntimeSessions(r.Context(), runtimeID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "runtime not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions, "count": len(sessions)})
}

func (s *ServerV2) handleRuntimeHeartbeat(w http.ResponseWriter, r *http.Request) {
	runtimeID := r.PathValue("id")
	sessionID := r.PathValue("sid")
	var req struct {
		Seq        int64 `json:"seq"`
		ReportedAt int64 `json:"reported_at"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Seq <= 0 {
		writeErr(w, http.StatusBadRequest, "seq must be positive")
		return
	}
	now := s.eval.Now()
	hb := &domain.RuntimeHeartbeat{
		RuntimeID:  runtimeID,
		SessionID:  sessionID,
		Seq:        req.Seq,
		ReportedAt: req.ReportedAt,
		ReceivedAt: now,
	}

	sessState, err := s.store.AddRuntimeHeartbeat(r.Context(), hb)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, "session not found")
		case errors.Is(err, store.ErrSeqRegression):
			writeErr(w, http.StatusConflict, err.Error())
		case errors.Is(err, store.ErrConflict):
			writeErr(w, http.StatusConflict, err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":      true,
		"runtime_id":    runtimeID,
		"session_id":    sessionID,
		"seq":           req.Seq,
		"session_state": sessState,
	})
}

// handleEndSession 结束一个 session。
// 已 ended → 200 no-op；不存在 → 404。
func (s *ServerV2) handleEndSession(w http.ResponseWriter, r *http.Request) {
	runtimeID := r.PathValue("id")
	sessionID := r.PathValue("sid")
	now := s.eval.Now()

	if _, err := s.store.EndRuntimeSession(r.Context(), runtimeID, sessionID, now); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "session not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ended":      true,
		"runtime_id": runtimeID,
		"session_id": sessionID,
	})
}

// ---------------------------------------------------------------------------
// Run v2
// ---------------------------------------------------------------------------

func (s *ServerV2) handleCreateRunV2(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID      string              `json:"task_id"`
		RobotID     string              `json:"robot_id"`
		RuntimeID   string              `json:"runtime_id"`
		SessionID   string              `json:"session_id"`
		StartedMs   int64               `json:"started_ms"`
		EndedMs     int64               `json:"ended_ms"`
		Result      string              `json:"result"`
		ArtifactRef *domain.ArtifactRef `json:"artifact_ref,omitempty"`
	}
	if !decodeJSONStrict(w, r, &req) {
		return
	}
	if req.TaskID == "" || req.RobotID == "" || req.RuntimeID == "" || req.SessionID == "" {
		writeErr(w, http.StatusBadRequest, "task_id, robot_id, runtime_id, and session_id are required")
		return
	}
	// artifact_ref 如果存在，type 和 uri 必填
	if req.ArtifactRef != nil && (req.ArtifactRef.Type == "" || req.ArtifactRef.URI == "") {
		writeErr(w, http.StatusBadRequest, "artifact_ref.type and artifact_ref.uri are required when artifact_ref is present")
		return
	}
	now := s.eval.Now()
	runID, ok := newCanonicalID(w, store.RunIDPrefix)
	if !ok {
		return
	}
	run := &domain.RunV2{
		ID:          runID,
		TaskID:      req.TaskID,
		RobotID:     req.RobotID,
		RuntimeID:   req.RuntimeID,
		SessionID:   req.SessionID,
		StartedMs:   req.StartedMs,
		EndedMs:     req.EndedMs,
		Result:      req.Result,
		ArtifactRef: req.ArtifactRef,
	}
	if run.StartedMs <= 0 {
		run.StartedMs = now
	}
	if err := s.store.CreateRunV2(r.Context(), run); err != nil {
		switch {
		case errors.Is(err, store.ErrBadReference):
			writeErr(w, http.StatusUnprocessableEntity, "task or session does not exist")
		case errors.Is(err, store.ErrRobotMismatch):
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
		case errors.Is(err, store.ErrConflict):
			writeErr(w, http.StatusConflict, "run already exists")
		case errors.Is(err, store.ErrInvalidArgument):
			writeErr(w, http.StatusBadRequest, err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

func (s *ServerV2) handleListRunsV2(w http.ResponseWriter, r *http.Request) {
	robotID := r.URL.Query().Get("robot_id")
	runs, err := s.store.ListRunsV2(r.Context(), robotID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "count": len(runs)})
}

func (s *ServerV2) handleGetRunV2(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.store.GetRunV2(r.Context(), id)
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

// ---------------------------------------------------------------------------
// 枚举校验
// ---------------------------------------------------------------------------

var validDeviceClasses = map[domain.DeviceClass]bool{
	domain.DeviceCompute:    true,
	domain.DeviceController: true,
	domain.DeviceSensor:     true,
	domain.DeviceActuator:   true,
	domain.DeviceBusNode:    true,
	domain.DeviceComposite:  true,
}

// ──── helpers ────

func isValidDeviceClass(c domain.DeviceClass) bool { return validDeviceClasses[c] }

var validRuntimeRoles = map[domain.RuntimeRole]bool{
	domain.RuntimeControlRuntime: true, domain.RuntimeDomainExecutor: true,
	domain.RuntimeDeviceBridge: true, domain.RuntimeReplayExecutor: true,
}
func isValidRuntimeRole(r domain.RuntimeRole) bool { return validRuntimeRoles[r] }

func decodeJSONStrict(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) { writeErr(w, http.StatusBadRequest, "empty body"); return false }
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return false
	}
	return true
}

func newCanonicalID(w http.ResponseWriter, prefix string) (string, bool) {
	id := store.NewID(prefix)
	if _, err := store.ValidateCanonicalID(id, prefix); err != nil {
		writeErr(w, http.StatusInternalServerError, "id generation failed")
		return "", false
	}
	return id, true
}
