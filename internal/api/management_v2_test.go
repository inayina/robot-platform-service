package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inayina/robot-platform-service/internal/api"
	"github.com/inayina/robot-platform-service/internal/domain"
	"github.com/inayina/robot-platform-service/internal/store"
)

// testEnvV2 用可推进的假时钟驱动 Runtime liveness 判定。
type testEnvV2 struct {
	now   int64
	srv   *httptest.Server
	store *store.Store
}

func newTestEnvV2(t *testing.T) *testEnvV2 {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	env := &testEnvV2{now: 1_000_000, store: st}
	eval := domain.NewRuntimeLivenessEvaluator(func() int64 { return env.now })

	mux := http.NewServeMux()
	mux.Handle("/v2/", http.StripPrefix("/v2", api.NewHandlerV2(st, eval)))
	env.srv = httptest.NewServer(mux)
	t.Cleanup(env.srv.Close)
	return env
}

// advance 推进假时钟(毫秒)。
func (e *testEnvV2) advance(ms int64) { e.now += ms }

func (e *testEnvV2) do(method, path, body string) (*http.Response, map[string]any) {
	var rdr *bytes.Reader
	if body == "" {
		rdr = bytes.NewReader(nil)
	} else {
		rdr = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, e.srv.URL+"/v2"+path, rdr)
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

// ---------------------------------------------------------------------------
// Health
// ---------------------------------------------------------------------------

func TestHealthV2(t *testing.T) {
	env := newTestEnvV2(t)
	resp, body := env.do("GET", "/health", "")
	if resp.StatusCode != http.StatusOK || body["version"] != "v2" {
		t.Fatalf("health v2: %d %v", resp.StatusCode, body)
	}
}

// ---------------------------------------------------------------------------
// Robot
// ---------------------------------------------------------------------------

func TestRobotLifecycleV2(t *testing.T) {
	env := newTestEnvV2(t)

	// 创建
	resp, body := env.do("POST", "/robots",
		`{"display_name":"panda-sim","domain":"panda","embodiment":"simulation"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create robot: %d %v", resp.StatusCode, body)
	}
	robotID, _ := body["id"].(string)
	if !strings.HasPrefix(robotID, "rob-") {
		t.Fatalf("robot id must be Platform-issued rob- identity, got %q", robotID)
	}

	// canonical ID 和 lifecycle 都是 server-owned，caller 注入必须拒绝。
	resp, _ = env.do("POST", "/robots",
		fmt.Sprintf(`{"id":%q,"display_name":"dup","domain":"amr","embodiment":"physical"}`, robotID))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("caller-supplied id: want 400, got %d", resp.StatusCode)
	}
	resp, _ = env.do("POST", "/robots",
		`{"display_name":"retired","domain":"amr","embodiment":"physical","lifecycle_state":"retired"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("caller-supplied lifecycle_state: want 400, got %d", resp.StatusCode)
	}

	// 列表
	resp, body = env.do("GET", "/robots", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list robots: %d", resp.StatusCode)
	}
	if n, _ := body["count"].(float64); n != 1 {
		t.Errorf("want 1 robot, got %v", n)
	}

	// 详情
	resp, body = env.do("GET", "/robots/"+robotID, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get robot: %d", resp.StatusCode)
	}
	if body["domain"] != "panda" {
		t.Errorf("unexpected robot: %v", body)
	}

	// 不存在
	resp, _ = env.do("GET", "/robots/nope", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing robot: want 404, got %d", resp.StatusCode)
	}

	// 缺少必填字段
	resp, _ = env.do("POST", "/robots", `{"display_name":"bad"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing fields: want 400, got %d", resp.StatusCode)
	}
}

func TestRegistryD2IdentityAndExternalRefEnforcement(t *testing.T) {
	env := newTestEnvV2(t)

	ref := `{"namespace":"amr_wms.robot_id","value":"amr-sim-01"}`
	resp, body := env.do("POST", "/robots",
		`{"display_name":"amr-sim","domain":"amr","embodiment":"simulation","external_refs":[`+ref+`,`+ref+`]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create robot with external ref: %d %v", resp.StatusCode, body)
	}
	robotID, _ := body["id"].(string)
	refs, _ := body["external_refs"].([]any)
	if len(refs) != 1 {
		t.Fatalf("exact duplicate external refs must be idempotently collapsed, got %v", body["external_refs"])
	}

	// 同 object kind 下，namespace/value 只能映射一个 canonical Robot。
	resp, _ = env.do("POST", "/robots",
		`{"display_name":"amr-duplicate","domain":"amr","embodiment":"simulation","external_refs":[`+ref+`]}`)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate Robot external ref: want 409, got %d", resp.StatusCode)
	}

	resp, _ = env.do("POST", "/robots",
		`{"display_name":"bad-ref","domain":"amr","embodiment":"simulation","external_refs":[{"namespace":" amr_wms.robot_id","value":"x"}]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("untrimmed external ref: want 400, got %d", resp.StatusCode)
	}
	resp, _ = env.do("POST", "/robots",
		`{"display_name":"bad-ref-type","domain":"amr","embodiment":"simulation","external_refs":""}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("string external_refs: want 400, got %d", resp.StatusCode)
	}

	// ExternalRef uniqueness is scoped by object kind，Device 可以有相同的 namespaced value。
	resp, body = env.do("POST", "/robots/"+robotID+"/devices",
		`{"display_name":"sim-base","device_class":"composite","external_refs":[`+ref+`]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("same ref in another object kind should be allowed: %d %v", resp.StatusCode, body)
	}

	resp, _ = env.do("POST", "/robots/"+robotID+"/devices",
		`{"id":"dev-caller","display_name":"injected","device_class":"sensor"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("caller-supplied device id: want 400, got %d", resp.StatusCode)
	}

	resp, _ = env.do("POST", "/robots/"+robotID+"/runtimes",
		`{"display_name":"bad-interval","runtime_role":"domain_executor","component":"executor","heartbeat_interval_ms":0}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("explicit zero heartbeat interval: want 400, got %d", resp.StatusCode)
	}
	resp, _ = env.do("POST", "/robots/"+robotID+"/runtimes",
		`{"id":"rt-caller","display_name":"injected","runtime_role":"domain_executor","component":"executor"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("caller-supplied runtime id: want 400, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Device v2
// ---------------------------------------------------------------------------

func TestDeviceV2Endpoints(t *testing.T) {
	env := newTestEnvV2(t)

	// 先创建 robot
	resp, body := env.do("POST", "/robots",
		`{"display_name":"amr-1","domain":"amr","embodiment":"physical"}`)
	robotID, _ := body["id"].(string)

	// 创建 compute device
	resp, body = env.do("POST", "/robots/"+robotID+"/devices",
		`{"display_name":"jetson","device_class":"compute"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create device: %d %v", resp.StatusCode, body)
	}
	devID, _ := body["id"].(string)
	if !strings.HasPrefix(devID, "dev-") {
		t.Fatalf("device id must be Platform-issued dev- identity, got %q", devID)
	}

	// 创建 sensor device
	resp, body = env.do("POST", "/robots/"+robotID+"/devices",
		`{"display_name":"lidar","device_class":"sensor","parent_device_id":"`+devID+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create child device: %d %v", resp.StatusCode, body)
	}

	// 列表
	resp, body = env.do("GET", "/robots/"+robotID+"/devices", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list devices: %d", resp.StatusCode)
	}
	if n, _ := body["count"].(float64); n != 2 {
		t.Errorf("want 2 devices, got %v", n)
	}

	// 详情
	resp, body = env.do("GET", "/devices/"+devID, "")
	if resp.StatusCode != http.StatusOK || body["device_class"] != "compute" {
		t.Errorf("get device: %d %v", resp.StatusCode, body)
	}

	// 不存在的 robot
	resp, _ = env.do("POST", "/robots/rob-nope/devices",
		`{"display_name":"ghost","device_class":"compute"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown robot: want 404, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Runtime + liveness
// ---------------------------------------------------------------------------

func TestRuntimeLivenessTransition(t *testing.T) {
	env := newTestEnvV2(t)

	// 创建 robot
	resp, body := env.do("POST", "/robots",
		`{"display_name":"panda-1","domain":"panda","embodiment":"physical"}`)
	robotID, _ := body["id"].(string)

	// 创建 runtime(interval 100ms)
	resp, body = env.do("POST", "/robots/"+robotID+"/runtimes",
		`{"display_name":"rcrd-main","runtime_role":"control_runtime","component":"rcrd","heartbeat_interval_ms":100}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create runtime: %d %v", resp.StatusCode, body)
	}
	runtimeID, _ := body["id"].(string)
	if !strings.HasPrefix(runtimeID, "rt-") {
		t.Fatalf("runtime id must be Platform-issued rt- identity, got %q", runtimeID)
	}

	// 初始 liveness → unknown(无 session)
	livenessOf(t, env, runtimeID, string(domain.LivenessUnknown))

	// 创建 session
	resp, body = env.do("POST", "/runtimes/"+runtimeID+"/sessions",
		`{"session_id":"boot-1"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create session: %d %v", resp.StatusCode, body)
	}
	// 有 session 但无心跳 → still unknown
	livenessOf(t, env, runtimeID, string(domain.LivenessUnknown))

	// 心跳 seq=1 → online
	resp, _ = env.do("POST", "/runtimes/"+runtimeID+"/sessions/boot-1/heartbeats",
		`{"seq":1}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("heartbeat: %d", resp.StatusCode)
	}
	livenessOf(t, env, runtimeID, string(domain.LivenessOnline))

	// 推进 400ms(age=4×interval) → stale
	env.advance(400)
	livenessOf(t, env, runtimeID, string(domain.LivenessStale))

	// 再推进 300ms(age=7×interval) → offline
	env.advance(300)
	livenessOf(t, env, runtimeID, string(domain.LivenessOffline))
}

func TestSessionSupersedeV2(t *testing.T) {
	env := newTestEnvV2(t)

	resp, body := env.do("POST", "/robots",
		`{"display_name":"amr-1","domain":"amr","embodiment":"physical"}`)
	robotID, _ := body["id"].(string)

	resp, body = env.do("POST", "/robots/"+robotID+"/runtimes",
		`{"display_name":"rcrd","runtime_role":"control_runtime","component":"rcrd","heartbeat_interval_ms":100}`)
	runtimeID, _ := body["id"].(string)

	// session 1
	env.do("POST", "/runtimes/"+runtimeID+"/sessions", `{"session_id":"s1"}`)
	env.do("POST", "/runtimes/"+runtimeID+"/sessions/s1/heartbeats", `{"seq":1}`)
	livenessOf(t, env, runtimeID, string(domain.LivenessOnline))

	// session 2 → s1 superseded
	env.do("POST", "/runtimes/"+runtimeID+"/sessions", `{"session_id":"s2"}`)
	env.do("POST", "/runtimes/"+runtimeID+"/sessions/s2/heartbeats", `{"seq":1}`)

	// s1 迟到心跳 → 202 但 session_state=superseded
	resp, body = env.do("POST", "/runtimes/"+runtimeID+"/sessions/s1/heartbeats",
		`{"seq":2}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("late heartbeat: %d", resp.StatusCode)
	}
	if ss, _ := body["session_state"].(string); ss != string(domain.SessionSuperseded) {
		t.Errorf("late heartbeat state: want superseded, got %s", ss)
	}

	// s2 session 内 seq 从 1 重新开始(不是续接 s1 的 seq)
	resp, body = env.do("POST", "/runtimes/"+runtimeID+"/sessions/s2/heartbeats",
		`{"seq":2}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("s2 seq 2: %d", resp.StatusCode)
	}

	// session 列表
	resp, body = env.do("GET", "/runtimes/"+runtimeID+"/sessions", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list sessions: %d", resp.StatusCode)
	}
	if n, _ := body["count"].(float64); n != 2 {
		t.Errorf("want 2 sessions, got %v", n)
	}
}

func TestSeqRegressionV2(t *testing.T) {
	env := newTestEnvV2(t)

	resp, body := env.do("POST", "/robots",
		`{"display_name":"amr-1","domain":"amr","embodiment":"physical"}`)
	robotID, _ := body["id"].(string)
	resp, body = env.do("POST", "/robots/"+robotID+"/runtimes",
		`{"display_name":"rcrd","runtime_role":"control_runtime","component":"rcrd"}`)
	runtimeID, _ := body["id"].(string)
	env.do("POST", "/runtimes/"+runtimeID+"/sessions", `{"session_id":"s1"}`)

	// seq 1,2,5
	env.do("POST", "/runtimes/"+runtimeID+"/sessions/s1/heartbeats", `{"seq":1}`)
	env.do("POST", "/runtimes/"+runtimeID+"/sessions/s1/heartbeats", `{"seq":2}`)
	env.do("POST", "/runtimes/"+runtimeID+"/sessions/s1/heartbeats", `{"seq":5}`)

	// seq 3 回退 → 409
	resp, _ = env.do("POST", "/runtimes/"+runtimeID+"/sessions/s1/heartbeats",
		`{"seq":3}`)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("seq regression: want 409, got %d", resp.StatusCode)
	}

	// seq 1 幂等(同 payload) → 202
	env.do("POST", "/runtimes/"+runtimeID+"/sessions/s1/heartbeats", `{"seq":1}`)
}

// ---------------------------------------------------------------------------
// Run v2
// ---------------------------------------------------------------------------

func TestRunV2Endpoints(t *testing.T) {
	env := newTestEnvV2(t)

	// 创建 robot
	resp, body := env.do("POST", "/robots",
		`{"display_name":"amr-1","domain":"amr","embodiment":"physical"}`)
	robotID, _ := body["id"].(string)

	// 创建 runtime + session
	resp, body = env.do("POST", "/robots/"+robotID+"/runtimes",
		`{"display_name":"rcrd","runtime_role":"control_runtime","component":"rcrd"}`)
	runtimeID, _ := body["id"].(string)
	env.do("POST", "/runtimes/"+runtimeID+"/sessions", `{"session_id":"s1"}`)

	// 通过 v1 端点创建 task
	st := env.store
	mustTaskV1(t, st, "task-1")

	// 创建 run
	resp, _ = env.do("POST", "/runs",
		fmt.Sprintf(`{"id":"run-caller","task_id":"task-1","robot_id":%q,"runtime_id":%q,"session_id":"s1"}`,
			robotID, runtimeID))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("caller-supplied run id: want 400, got %d", resp.StatusCode)
	}

	resp, body = env.do("POST", "/runs",
		fmt.Sprintf(`{"task_id":"task-1","robot_id":%q,"runtime_id":%q,"session_id":"s1","result":"succeeded","artifact_ref":{"type":"episode","uri":"release/e2_v1","hash_sha256":"abc123","producer_repo":"example/repo","producer_version":"v1.0"}}`,
			robotID, runtimeID))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create run: %d %v", resp.StatusCode, body)
	}
	runID, _ := body["id"].(string)
	if !strings.HasPrefix(runID, "run-") {
		t.Fatalf("run id must be Platform-issued run- identity, got %q", runID)
	}

	// 查询
	resp, body = env.do("GET", "/runs/"+runID, "")
	if resp.StatusCode != http.StatusOK || body["result"] != "succeeded" {
		t.Errorf("get run: %d %v", resp.StatusCode, body)
	}

	// 不存在
	resp, _ = env.do("GET", "/runs/nope", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing run: want 404, got %d", resp.StatusCode)
	}

	// robot mismatch → 422
	resp, body2 := env.do("POST", "/robots",
		`{"display_name":"amr-2","domain":"amr","embodiment":"physical"}`)
	robot2ID, _ := body2["id"].(string)
	resp, _ = env.do("POST", "/runs",
		fmt.Sprintf(`{"task_id":"task-1","robot_id":%q,"runtime_id":%q,"session_id":"s1"}`, robot2ID, runtimeID))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("robot mismatch: want 422, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

func livenessOf(t *testing.T, env *testEnvV2, runtimeID, want string) {
	t.Helper()
	resp, body := env.do("GET", "/runtimes/"+runtimeID+"/liveness", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("liveness: %d", resp.StatusCode)
	}
	got, _ := body["liveness"].(string)
	if got != want {
		t.Errorf("liveness of %s: want %s, got %s", runtimeID, want, got)
	}
}

func mustTaskV1(t *testing.T, st *store.Store, id string) {
	t.Helper()
	task := &domain.Task{
		ID: id, Domain: "amr", Kind: "test", Target: "station_a",
		Status: domain.TaskPending, CreatedMs: 100, UpdatedMs: 100,
	}
	if err := st.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("create v1 task: %v", err)
	}
}
