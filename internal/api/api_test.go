package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inayina/robot-platform-service/internal/api"
	"github.com/inayina/robot-platform-service/internal/domain"
	"github.com/inayina/robot-platform-service/internal/store"
)

// testEnv 用可推进的假时钟驱动 ok/stale/missing 判定。
type testEnv struct {
	now   int64
	srv   *httptest.Server
	store *store.Store
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	env := &testEnv{now: 1_000_000, store: st}
	eval := domain.NewStatusEvaluator(func() int64 { return env.now })
	env.srv = httptest.NewServer(api.NewHandler(st, eval))
	t.Cleanup(env.srv.Close)
	return env
}

// advance 推进假时钟(毫秒)。
func (e *testEnv) advance(ms int64) { e.now += ms }

func (e *testEnv) do(method, path, body string) (*http.Response, map[string]any) {
	var rdr *bytes.Reader
	if body == "" {
		rdr = bytes.NewReader(nil)
	} else {
		rdr = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rdr)
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

func statusOf(t *testing.T, resp map[string]any) string {
	t.Helper()
	devs, ok := resp["devices"].([]any)
	if !ok || len(devs) == 0 {
		t.Fatalf("no devices in response: %v", resp)
	}
	d := devs[0].(map[string]any)
	st, _ := d["status"].(string)
	return st
}

func TestHealth(t *testing.T) {
	env := newTestEnv(t)
	resp, body := env.do("GET", "/v1/health", "")
	if resp.StatusCode != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("health: %d %v", resp.StatusCode, body)
	}
}

func TestDeviceLifecycleWithClock(t *testing.T) {
	env := newTestEnv(t)

	// 注册(interval 100ms,便于测试)
	resp, body := env.do("POST", "/v1/devices",
		`{"name":"panda-sim","kind":"panda","version":"v0.1.0","heartbeat_interval_ms":100}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create device: %d %v", resp.StatusCode, body)
	}
	devID, _ := body["id"].(string)
	if devID == "" {
		t.Fatal("device id empty")
	}

	// 注册重复 → 409
	resp, _ = env.do("POST", "/v1/devices",
		fmt.Sprintf(`{"id":%q,"name":"dup"}`, devID))
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate register: want 409, got %d", resp.StatusCode)
	}

	// 未收到心跳 → missing
	resp, body = env.do("GET", "/v1/devices", "")
	if resp.StatusCode != http.StatusOK || statusOf(t, body) != string(domain.StatusMissing) {
		t.Errorf("never-heartbeat: want missing, got %v", body)
	}

	// 心跳 seq=1 → ok
	resp, _ = env.do("POST", "/v1/devices/"+devID+"/heartbeats", `{"seq":1}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("heartbeat: %d", resp.StatusCode)
	}
	resp, body = env.do("GET", "/v1/devices", "")
	if statusOf(t, body) != string(domain.StatusOK) {
		t.Errorf("after heartbeat: want ok, got %v", body)
	}

	// seq 回退 → 409
	resp, _ = env.do("POST", "/v1/devices/"+devID+"/heartbeats", `{"seq":1}`)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("seq regression: want 409, got %d", resp.StatusCode)
	}

	// 时间推进:age 4×interval → stale(100ms interval,推进 400ms)
	env.advance(400)
	resp, body = env.do("GET", "/v1/devices", "")
	if statusOf(t, body) != string(domain.StatusStale) {
		t.Errorf("advance 400ms: want stale, got %v", body)
	}

	// 时间推进:age 7×interval → missing(再推进 300ms)
	env.advance(300)
	resp, body = env.do("GET", "/v1/devices", "")
	if statusOf(t, body) != string(domain.StatusMissing) {
		t.Errorf("advance 700ms total: want missing, got %v", body)
	}
}

func TestUnknownDeviceHeartbeat(t *testing.T) {
	env := newTestEnv(t)
	resp, _ := env.do("POST", "/v1/devices/ghost/heartbeats", `{"seq":1}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown device heartbeat: want 404, got %d", resp.StatusCode)
	}
}

func TestTaskAndRunEndpoints(t *testing.T) {
	env := newTestEnv(t)

	// 注册设备
	resp, body := env.do("POST", "/v1/devices", `{"name":"amr-sim","kind":"amr"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create device: %d", resp.StatusCode)
	}
	devID, _ := body["id"].(string)

	// 创建任务
	resp, body = env.do("POST", "/v1/tasks", `{"domain":"amr","kind":"transport","target":"station_a"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create task: %d %v", resp.StatusCode, body)
	}
	taskID, _ := body["id"].(string)

	// 任务状态过滤
	resp, body = env.do("GET", "/v1/tasks?status=pending", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list tasks: %d", resp.StatusCode)
	}
	if n, _ := body["count"].(float64); n != 1 {
		t.Errorf("want 1 pending task, got %v", body["count"])
	}

	// run 引用不存在的设备 → 422
	resp, _ = env.do("POST", "/v1/runs",
		fmt.Sprintf(`{"task_id":%q,"device_id":"ghost"}`, taskID))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("bad run ref: want 422, got %d", resp.StatusCode)
	}

	// 合法 run
	resp, body = env.do("POST", "/v1/runs",
		fmt.Sprintf(`{"task_id":%q,"device_id":%q,"result":"succeeded","artifact_ref":"release/e2_v1"}`, taskID, devID))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create run: %d %v", resp.StatusCode, body)
	}
	runID, _ := body["id"].(string)

	resp, body = env.do("GET", "/v1/runs/"+runID, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get run: %d", resp.StatusCode)
	}
	if body["result"] != "succeeded" {
		t.Errorf("run result: %v", body["result"])
	}

	// 不存在的 run → 404
	resp, _ = env.do("GET", "/v1/runs/nope", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing run: want 404, got %d", resp.StatusCode)
	}
}
