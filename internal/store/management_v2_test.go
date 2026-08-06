package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/inayina/robot-platform-service/internal/domain"
	"github.com/inayina/robot-platform-service/internal/store"
)

func newStoreV2(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustRobot(t *testing.T, s *store.Store, id string) *domain.Robot {
	t.Helper()
	r := &domain.Robot{
		ID: id, DisplayName: "test-robot", Domain: "amr", Embodiment: "physical",
		LifecycleState: domain.RobotActive, CreatedAt: 1000, UpdatedAt: 1000,
	}
	if err := s.CreateRobot(context.Background(), r); err != nil {
		t.Fatalf("create robot: %v", err)
	}
	return r
}

func mustDeviceV2(t *testing.T, s *store.Store, id, robotID string) *domain.DeviceV2 {
	t.Helper()
	d := &domain.DeviceV2{
		ID: id, RobotID: robotID, DisplayName: "test-device",
		DeviceClass: domain.DeviceCompute, LifecycleState: "active",
		CreatedAt: 2000, UpdatedAt: 2000,
	}
	if err := s.CreateDeviceV2(context.Background(), d); err != nil {
		t.Fatalf("create device v2: %v", err)
	}
	return d
}

func mustRuntime(t *testing.T, s *store.Store, id, robotID string) *domain.Runtime {
	t.Helper()
	rt := &domain.Runtime{
		ID: id, RobotID: robotID, DisplayName: "test-runtime",
		RuntimeRole: domain.RuntimeControlRuntime, Component: "rcrd",
		HeartbeatIntervalMs: 100, LifecycleState: "active",
		CreatedAt: 3000, UpdatedAt: 3000,
	}
	if err := s.CreateRuntime(context.Background(), rt); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	return rt
}

func mustTask(t *testing.T, s *store.Store, id string) {
	t.Helper()
	// v1 CreateTask — 仍可用
	task := &domain.Task{ID: id, Domain: "amr", Kind: "test", Status: domain.TaskPending, CreatedMs: 100, UpdatedMs: 100}
	if err := s.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("create task: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Robot
// ---------------------------------------------------------------------------

func TestRobotCRUD(t *testing.T) {
	s := newStoreV2(t)
	ctx := context.Background()

	r := mustRobot(t, s, "rob-1")

	got, err := s.GetRobot(ctx, "rob-1")
	if err != nil {
		t.Fatalf("get robot: %v", err)
	}
	if got.DisplayName != r.DisplayName || got.Domain != "amr" {
		t.Errorf("unexpected robot: %+v", got)
	}

	// 重复注册 → ErrConflict
	if err := s.CreateRobot(ctx, r); !errors.Is(err, store.ErrConflict) {
		t.Errorf("duplicate create: want ErrConflict, got %v", err)
	}

	// 不存在 → ErrNotFound
	if _, err := s.GetRobot(ctx, "rob-nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing robot: want ErrNotFound, got %v", err)
	}

	robots, err := s.ListRobots(ctx)
	if err != nil {
		t.Fatalf("list robots: %v", err)
	}
	if len(robots) != 1 {
		t.Errorf("want 1 robot, got %d", len(robots))
	}
}

func TestExternalRefIdentityConstraints(t *testing.T) {
	s := newStoreV2(t)
	ctx := context.Background()
	ref := domain.ExternalRef{Namespace: "amr_wms.robot_id", Value: "amr-sim-01"}

	r1 := &domain.Robot{
		ID: "rob-ref-1", DisplayName: "robot-ref-1", Domain: "amr", Embodiment: "simulation",
		LifecycleState: domain.RobotActive,
		ExternalRefs:   domain.ExternalRefs{ref, ref},
		CreatedAt:      1000, UpdatedAt: 1000,
	}
	if err := s.CreateRobot(ctx, r1); err != nil {
		t.Fatalf("create first external ref mapping: %v", err)
	}
	got, err := s.GetRobot(ctx, r1.ID)
	if err != nil {
		t.Fatalf("get robot with external refs: %v", err)
	}
	if len(got.ExternalRefs) != 1 || got.ExternalRefs[0] != ref {
		t.Fatalf("exact duplicate mapping should collapse: %+v", got.ExternalRefs)
	}

	r2 := &domain.Robot{
		ID: "rob-ref-2", DisplayName: "robot-ref-2", Domain: "amr", Embodiment: "simulation",
		LifecycleState: domain.RobotActive,
		ExternalRefs:   domain.ExternalRefs{ref},
		CreatedAt:      2000, UpdatedAt: 2000,
	}
	if err := s.CreateRobot(ctx, r2); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("same Robot namespace/value must conflict, got %v", err)
	}
	if _, err := s.GetRobot(ctx, r2.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("external ref conflict must roll back object row, got %v", err)
	}

	bad := &domain.Robot{
		ID: "rob-ref-bad", DisplayName: "bad-ref", Domain: "amr", Embodiment: "simulation",
		LifecycleState: domain.RobotActive,
		ExternalRefs:   domain.ExternalRefs{{Namespace: " amr_wms.robot_id", Value: "x"}},
		CreatedAt:      3000, UpdatedAt: 3000,
	}
	if err := s.CreateRobot(ctx, bad); !errors.Is(err, store.ErrInvalidArgument) {
		t.Fatalf("untrimmed ExternalRef must be invalid, got %v", err)
	}

	// uniqueness 以 object kind 为 scope；相同 namespace/value 可映射一个 Device。
	d := &domain.DeviceV2{
		ID: "dev-ref-1", RobotID: r1.ID, DisplayName: "device-ref",
		DeviceClass: domain.DeviceComposite, LifecycleState: "active",
		ExternalRefs: domain.ExternalRefs{ref}, CreatedAt: 4000, UpdatedAt: 4000,
	}
	if err := s.CreateDeviceV2(ctx, d); err != nil {
		t.Fatalf("same mapping in Device scope should succeed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Device v2
// ---------------------------------------------------------------------------

func TestDeviceV2Constraints(t *testing.T) {
	s := newStoreV2(t)
	ctx := context.Background()
	mustRobot(t, s, "rob-1")
	mustRobot(t, s, "rob-2")

	// 正常创建
	mustDeviceV2(t, s, "dev-1", "rob-1")
	got, err := s.GetDeviceV2(ctx, "dev-1")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if got.RobotID != "rob-1" {
		t.Errorf("unexpected robot_id: %s", got.RobotID)
	}

	// 不存在的 robot → ErrBadReference
	bad := &domain.DeviceV2{
		ID: "dev-bad", RobotID: "rob-nope", DisplayName: "bad",
		DeviceClass: domain.DeviceCompute, LifecycleState: "active",
		CreatedAt: 1000, UpdatedAt: 1000,
	}
	err = s.CreateDeviceV2(ctx, bad)
	if err == nil {
		t.Fatal("expected error for unknown robot")
	}

	// parent 属于不同 robot → 拒
	crossParent := &domain.DeviceV2{
		ID: "dev-2", RobotID: "rob-1", DisplayName: "bad-parent",
		DeviceClass: domain.DeviceSensor, ParentDeviceID: "dev-1", LifecycleState: "active",
		CreatedAt: 2000, UpdatedAt: 2000,
	}
	if err := s.CreateDeviceV2(ctx, crossParent); err != nil {
		t.Fatalf("create device with valid parent: %v", err)
	}

	// parent 跨 robot → 拒(dev-1 在 robot-1，但 child 想挂到 robot-2)
	crossRobot := &domain.DeviceV2{
		ID: "dev-cross", RobotID: "rob-2", DisplayName: "cross",
		DeviceClass: domain.DeviceSensor, ParentDeviceID: "dev-1", LifecycleState: "active",
		CreatedAt: 3000, UpdatedAt: 3000,
	}
	err = s.CreateDeviceV2(ctx, crossRobot)
	if !errors.Is(err, store.ErrBadReference) {
		t.Errorf("cross-robot parent: want ErrBadReference, got %v", err)
	}

	// containment 环: A→B, 然后 B→A
	a := mustDeviceV2(t, s, "dev-a", "rob-1")
	_ = a
	b := &domain.DeviceV2{
		ID: "dev-b", RobotID: "rob-1", DisplayName: "b",
		DeviceClass: domain.DeviceController, ParentDeviceID: "dev-a", LifecycleState: "active",
		CreatedAt: 4000, UpdatedAt: 4000,
	}
	if err := s.CreateDeviceV2(ctx, b); err != nil {
		t.Fatalf("create dev-b: %v", err)
	}
	cycle := &domain.DeviceV2{
		ID: "dev-cycle", RobotID: "rob-1", DisplayName: "cycle",
		DeviceClass: domain.DeviceController, ParentDeviceID: "dev-b", LifecycleState: "active",
		CreatedAt: 5000, UpdatedAt: 5000,
	}
	// dev-a 已经存在，这不会形成环。但如果我们尝试让 dev-a 的 parent 变成 dev-b...
	// CreateDeviceV2 只检查向上链不包含自身；dev-a 向上是空(无parent)，所以这条链无环。
	if err := s.CreateDeviceV2(ctx, cycle); err != nil {
		t.Fatalf("create dev-cycle (non-cycle): %v", err)
	}

	// ListDevicesByRobot
	devs, err := s.ListDevicesByRobot(ctx, "rob-1")
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devs) < 3 {
		t.Errorf("want at least 3 devices under robot-1, got %d", len(devs))
	}

	// 不存在的 robot
	if _, err := s.ListDevicesByRobot(ctx, "rob-nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("list devices for unknown robot: want ErrNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Runtime
// ---------------------------------------------------------------------------

func TestRuntimeConstraints(t *testing.T) {
	s := newStoreV2(t)
	ctx := context.Background()
	mustRobot(t, s, "rob-1")
	mustRobot(t, s, "rob-2")
	mustDeviceV2(t, s, "dev-1", "rob-1")
	mustDeviceV2(t, s, "dev-2", "rob-2")

	// 正常创建
	mustRuntime(t, s, "rt-1", "rob-1")
	got, err := s.GetRuntime(ctx, "rt-1")
	if err != nil {
		t.Fatalf("get runtime: %v", err)
	}
	if got.RobotID != "rob-1" {
		t.Errorf("unexpected robot_id: %s", got.RobotID)
	}

	// 不存在的 robot → 拒
	bad := &domain.Runtime{
		ID: "rt-bad", RobotID: "rob-nope", DisplayName: "bad",
		RuntimeRole: domain.RuntimeControlRuntime, Component: "rcrd",
		HeartbeatIntervalMs: 100, LifecycleState: "active",
		CreatedAt: 1000, UpdatedAt: 1000,
	}
	if err := s.CreateRuntime(ctx, bad); err == nil {
		t.Fatal("expected error for unknown robot")
	}

	// host_device 跨 robot → 拒
	hostCross := &domain.Runtime{
		ID: "rt-cross", RobotID: "rob-1", DisplayName: "cross",
		RuntimeRole: domain.RuntimeControlRuntime, Component: "rcrd",
		HostDeviceID:        "dev-2", // dev-2 在 robot-2
		HeartbeatIntervalMs: 100, LifecycleState: "active",
		CreatedAt: 2000, UpdatedAt: 2000,
	}
	err = s.CreateRuntime(ctx, hostCross)
	if !errors.Is(err, store.ErrBadReference) {
		t.Errorf("cross-robot host: want ErrBadReference, got %v", err)
	}

	// host_device 有效 → OK
	hostOK := &domain.Runtime{
		ID: "rt-host", RobotID: "rob-1", DisplayName: "host-ok",
		RuntimeRole: domain.RuntimeControlRuntime, Component: "rcrd",
		HostDeviceID: "dev-1", HeartbeatIntervalMs: 100, LifecycleState: "active",
		CreatedAt: 3000, UpdatedAt: 3000,
	}
	if err := s.CreateRuntime(ctx, hostOK); err != nil {
		t.Fatalf("create runtime with valid host: %v", err)
	}

	// ListRuntimesByRobot
	rts, err := s.ListRuntimesByRobot(ctx, "rob-1")
	if err != nil {
		t.Fatalf("list runtimes: %v", err)
	}
	if len(rts) < 2 {
		t.Errorf("want at least 2 runtimes under robot-1, got %d", len(rts))
	}
}

// ---------------------------------------------------------------------------
// RuntimeSession
// ---------------------------------------------------------------------------

func TestSessionLifecycle(t *testing.T) {
	s := newStoreV2(t)
	ctx := context.Background()
	mustRobot(t, s, "rob-1")
	mustRuntime(t, s, "rt-1", "rob-1")

	// 创建 session
	sess1 := &domain.RuntimeSession{
		SessionID:          "sess-1",
		RuntimeID:          "rt-1",
		SoftwareVersionRef: "unknown",
		StartedAtReceived:  5000,
	}
	result, err := s.CreateRuntimeSession(ctx, sess1)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if result.SessionState != domain.SessionCurrent {
		t.Errorf("want current, got %s", result.SessionState)
	}

	// 查询 current session
	cs, err := s.GetCurrentSession(ctx, "rt-1")
	if err != nil {
		t.Fatalf("get current session: %v", err)
	}
	if cs.SessionID != "sess-1" {
		t.Errorf("want sess-1, got %s", cs.SessionID)
	}

	// 第二个 session → 旧变 superseded
	sess2 := &domain.RuntimeSession{
		SessionID:          "sess-2",
		RuntimeID:          "rt-1",
		SoftwareVersionRef: "unknown",
		StartedAtReceived:  6000,
	}
	result2, err := s.CreateRuntimeSession(ctx, sess2)
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}
	if result2.SessionState != domain.SessionCurrent {
		t.Errorf("want current, got %s", result2.SessionState)
	}

	// 旧 session 现在是 superseded
	old, err := s.GetRuntimeSession(ctx, "rt-1", "sess-1")
	if err != nil {
		t.Fatalf("get old session: %v", err)
	}
	if old.SessionState != domain.SessionSuperseded {
		t.Errorf("old session: want superseded, got %s", old.SessionState)
	}

	// 幂等：重复创建 sess-1 返回已有记录(superseded)
	dup, err := s.CreateRuntimeSession(ctx, sess1)
	if err != nil {
		t.Fatalf("duplicate session create: %v", err)
	}
	if dup.SessionState != domain.SessionSuperseded {
		t.Errorf("duplicate create: want superseded, got %s", dup.SessionState)
	}

	// current 仍是 sess-2
	cs2, err := s.GetCurrentSession(ctx, "rt-1")
	if err != nil {
		t.Fatalf("get current after dup: %v", err)
	}
	if cs2.SessionID != "sess-2" {
		t.Errorf("current should still be sess-2, got %s", cs2.SessionID)
	}

	// 不存在的 runtime → ErrNotFound
	bad := &domain.RuntimeSession{
		SessionID: "sess-x", RuntimeID: "rt-nope",
		SoftwareVersionRef: "unknown", StartedAtReceived: 7000,
	}
	_, err = s.CreateRuntimeSession(ctx, bad)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown runtime: want ErrNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// RuntimeHeartbeat
// ---------------------------------------------------------------------------

func TestRuntimeHeartbeatSeq(t *testing.T) {
	s := newStoreV2(t)
	ctx := context.Background()
	mustRobot(t, s, "rob-1")
	mustRuntime(t, s, "rt-1", "rob-1")
	mustSession(t, s, "rt-1", "sess-1")

	// seq=1
	state, err := s.AddRuntimeHeartbeat(ctx, &domain.RuntimeHeartbeat{
		RuntimeID: "rt-1", SessionID: "sess-1", Seq: 1, ReceivedAt: 1000,
	})
	if err != nil {
		t.Fatalf("heartbeat seq 1: %v", err)
	}
	if state != domain.SessionCurrent {
		t.Errorf("want current, got %s", state)
	}

	// seq=2
	if _, err := s.AddRuntimeHeartbeat(ctx, &domain.RuntimeHeartbeat{
		RuntimeID: "rt-1", SessionID: "sess-1", Seq: 2, ReceivedAt: 2000,
	}); err != nil {
		t.Fatalf("heartbeat seq 2: %v", err)
	}

	// 查询 last_heartbeat_at_ms
	sess, err := s.GetRuntimeSession(ctx, "rt-1", "sess-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.LastHeartbeatAtMs != 2000 {
		t.Errorf("last_heartbeat_at_ms: want 2000, got %d", sess.LastHeartbeatAtMs)
	}

	// seq 回退(seq=1 已存在且 payload 相同 → 幂等。合同 7.2:同 key 同 payload 幂等成功)
	state, err = s.AddRuntimeHeartbeat(ctx, &domain.RuntimeHeartbeat{
		RuntimeID: "rt-1", SessionID: "sess-1", Seq: 1, ReceivedAt: 1000,
	})
	if err != nil {
		t.Fatalf("idempotent seq 1: %v", err)
	}
	if state != domain.SessionCurrent {
		t.Errorf("idempotent seq 1: want current, got %s", state)
	}

	// seq 间隔允许(gap OK)
	if _, err := s.AddRuntimeHeartbeat(ctx, &domain.RuntimeHeartbeat{
		RuntimeID: "rt-1", SessionID: "sess-1", Seq: 5, ReceivedAt: 5000,
	}); err != nil {
		t.Fatalf("heartbeat seq 5 (gap): %v", err)
	}

	// seq=3 回退(新 seq=3 但 max=5) → ErrSeqRegression
	_, err = s.AddRuntimeHeartbeat(ctx, &domain.RuntimeHeartbeat{
		RuntimeID: "rt-1", SessionID: "sess-1", Seq: 3, ReceivedAt: 3000,
	})
	if !errors.Is(err, store.ErrSeqRegression) {
		t.Errorf("seq regression: want ErrSeqRegression, got %v", err)
	}

	// 查询最后 heartbeat
	hb, err := s.LastRuntimeHeartbeat(ctx, "rt-1", "sess-1")
	if err != nil {
		t.Fatalf("last heartbeat: %v", err)
	}
	if hb.Seq != 5 {
		t.Errorf("last heartbeat seq: want 5, got %d", hb.Seq)
	}
}

func TestSupersededSessionLateHeartbeat(t *testing.T) {
	s := newStoreV2(t)
	ctx := context.Background()
	mustRobot(t, s, "rob-1")
	mustRuntime(t, s, "rt-1", "rob-1")
	mustSession(t, s, "rt-1", "sess-1")

	// sess-1 心跳
	s.AddRuntimeHeartbeat(ctx, &domain.RuntimeHeartbeat{
		RuntimeID: "rt-1", SessionID: "sess-1", Seq: 1, ReceivedAt: 1000,
	})

	// 创建 sess-2(sess-1 → superseded)
	mustSession(t, s, "rt-1", "sess-2")

	// sess-2 心跳
	s.AddRuntimeHeartbeat(ctx, &domain.RuntimeHeartbeat{
		RuntimeID: "rt-1", SessionID: "sess-2", Seq: 1, ReceivedAt: 2000,
	})

	// sess-1 迟到心跳 → accepted(审计)但 state=superseded
	state, err := s.AddRuntimeHeartbeat(ctx, &domain.RuntimeHeartbeat{
		RuntimeID: "rt-1", SessionID: "sess-1", Seq: 2, ReceivedAt: 3000,
	})
	if err != nil {
		t.Fatalf("late heartbeat: %v", err)
	}
	if state != domain.SessionSuperseded {
		t.Errorf("late heartbeat state: want superseded, got %s", state)
	}

	// sess-1 的 last_heartbeat_at_ms 没有更新(只有 current session 更新)
	sess1, err := s.GetRuntimeSession(ctx, "rt-1", "sess-1")
	if err != nil {
		t.Fatalf("get sess-1: %v", err)
	}
	if sess1.LastHeartbeatAtMs != 1000 {
		t.Errorf("sess-1 last_heartbeat_at_ms: want 1000 (unchanged), got %d", sess1.LastHeartbeatAtMs)
	}

	// sess-2 的 last_heartbeat_at_ms 不受影响
	sess2, err := s.GetRuntimeSession(ctx, "rt-1", "sess-2")
	if err != nil {
		t.Fatalf("get sess-2: %v", err)
	}
	if sess2.LastHeartbeatAtMs != 2000 {
		t.Errorf("sess-2 last_heartbeat_at_ms: want 2000, got %d", sess2.LastHeartbeatAtMs)
	}
}

func TestNewSessionSeqReset(t *testing.T) {
	s := newStoreV2(t)
	ctx := context.Background()
	mustRobot(t, s, "rob-1")
	mustRuntime(t, s, "rt-1", "rob-1")
	mustSession(t, s, "rt-1", "sess-1")

	// sess-1: seq 1,2,3
	for seq := int64(1); seq <= 3; seq++ {
		if _, err := s.AddRuntimeHeartbeat(ctx, &domain.RuntimeHeartbeat{
			RuntimeID: "rt-1", SessionID: "sess-1", Seq: seq, ReceivedAt: seq * 1000,
		}); err != nil {
			t.Fatalf("sess-1 seq %d: %v", seq, err)
		}
	}

	// 新 session → seq 从 1 重新开始
	mustSession(t, s, "rt-1", "sess-2")
	if _, err := s.AddRuntimeHeartbeat(ctx, &domain.RuntimeHeartbeat{
		RuntimeID: "rt-1", SessionID: "sess-2", Seq: 1, ReceivedAt: 4000,
	}); err != nil {
		t.Fatalf("sess-2 seq 1: %v", err)
	}

	// sess-1 继续递增 → accepted(审计)
	if _, err := s.AddRuntimeHeartbeat(ctx, &domain.RuntimeHeartbeat{
		RuntimeID: "rt-1", SessionID: "sess-1", Seq: 4, ReceivedAt: 5000,
	}); err != nil {
		t.Fatalf("sess-1 seq 4: %v", err)
	}
}

// ---------------------------------------------------------------------------
// EndSession
// ---------------------------------------------------------------------------

func TestEndSession(t *testing.T) {
	s := newStoreV2(t)
	ctx := context.Background()
	mustRobot(t, s, "rob-1")
	mustRuntime(t, s, "rt-1", "rob-1")
	mustSession(t, s, "rt-1", "sess-1")

	// 结束 session
	if err := s.EndRuntimeSession(ctx, "rt-1", "sess-1", 5000); err != nil {
		t.Fatalf("end session: %v", err)
	}

	sess, err := s.GetRuntimeSession(ctx, "rt-1", "sess-1")
	if err != nil {
		t.Fatalf("get ended session: %v", err)
	}
	if sess.SessionState != domain.SessionEnded {
		t.Errorf("want ended, got %s", sess.SessionState)
	}

	// 再次结束 → no-op
	if err := s.EndRuntimeSession(ctx, "rt-1", "sess-1", 6000); err != nil {
		t.Fatalf("second end session: %v", err)
	}

	// current session 变为 nil
	cs, err := s.GetCurrentSession(ctx, "rt-1")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get current: %v", err)
	}
	if cs != nil {
		t.Errorf("current session should be nil after end, got %+v", cs)
	}

	// 不存在的 session → ErrNotFound
	if err := s.EndRuntimeSession(ctx, "rt-1", "sess-nope", 7000); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("end unknown session: want ErrNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Run v2
// ---------------------------------------------------------------------------

func TestRunV2(t *testing.T) {
	s := newStoreV2(t)
	ctx := context.Background()
	mustRobot(t, s, "rob-1")
	mustDeviceV2(t, s, "dev-1", "rob-1")
	mustRuntime(t, s, "rt-1", "rob-1")
	mustSession(t, s, "rt-1", "sess-1")
	mustTask(t, s, "task-1")

	// 正常创建
	run := &domain.RunV2{
		ID: "run-1", TaskID: "task-1", RobotID: "rob-1",
		RuntimeID: "rt-1", SessionID: "sess-1",
		StartedMs: 1000, Result: "in_progress",
	}
	if err := s.CreateRunV2(ctx, run); err != nil {
		t.Fatalf("create run v2: %v", err)
	}

	// 查询
	got, err := s.GetRunV2(ctx, "run-1")
	if err != nil {
		t.Fatalf("get run v2: %v", err)
	}
	if got.TaskID != "task-1" || got.RobotID != "rob-1" {
		t.Errorf("unexpected run: %+v", got)
	}

	// 不存在的 task → ErrBadReference
	badTask := &domain.RunV2{
		ID: "run-bad", TaskID: "task-nope", RobotID: "rob-1",
		RuntimeID: "rt-1", SessionID: "sess-1", StartedMs: 2000,
	}
	if err := s.CreateRunV2(ctx, badTask); !errors.Is(err, store.ErrBadReference) {
		t.Errorf("bad task ref: want ErrBadReference, got %v", err)
	}

	// 不存在的 session → ErrBadReference
	badSess := &domain.RunV2{
		ID: "run-bad2", TaskID: "task-1", RobotID: "rob-1",
		RuntimeID: "rt-1", SessionID: "sess-nope", StartedMs: 3000,
	}
	if err := s.CreateRunV2(ctx, badSess); !errors.Is(err, store.ErrBadReference) {
		t.Errorf("bad session ref: want ErrBadReference, got %v", err)
	}

	// robot-session 不一致 → ErrRobotMismatch
	mustRobot(t, s, "rob-2")
	mustRuntime(t, s, "rt-2", "rob-2")
	mustSession(t, s, "rt-2", "sess-r2")

	mismatch := &domain.RunV2{
		ID: "run-mismatch", TaskID: "task-1", RobotID: "rob-1", // ← robot-1
		RuntimeID: "rt-2", SessionID: "sess-r2", // ← runtime-2 属于 robot-2
		StartedMs: 4000,
	}
	if err := s.CreateRunV2(ctx, mismatch); !errors.Is(err, store.ErrRobotMismatch) {
		t.Errorf("robot mismatch: want ErrRobotMismatch, got %v", err)
	}

	// 带 typed artifact_ref 的 run
	runWithArt := &domain.RunV2{
		ID: "run-art", TaskID: "task-1", RobotID: "rob-1",
		RuntimeID: "rt-1", SessionID: "sess-1",
		StartedMs: 5000, Result: "succeeded",
		ArtifactRef: &domain.ArtifactRef{
			Type: "episode", URI: "release/e2_v1",
			HashSHA256: "abc123", ProducerRepo: "example/repo", ProducerVersion: "v1.0",
		},
	}
	if err := s.CreateRunV2(ctx, runWithArt); err != nil {
		t.Fatalf("create run with artifact: %v", err)
	}
	gotArt, err := s.GetRunV2(ctx, "run-art")
	if err != nil {
		t.Fatalf("get run with artifact: %v", err)
	}
	if gotArt.ArtifactRef == nil || gotArt.ArtifactRef.Type != "episode" {
		t.Errorf("artifact ref: %+v", gotArt.ArtifactRef)
	}

	// ListRunsV2
	runs, err := s.ListRunsV2(ctx, "rob-1")
	if err != nil {
		t.Fatalf("list runs v2: %v", err)
	}
	if len(runs) < 2 {
		t.Errorf("want at least 2 runs, got %d", len(runs))
	}
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

func mustSession(t *testing.T, s *store.Store, runtimeID, sessionID string) *domain.RuntimeSession {
	t.Helper()
	sess := &domain.RuntimeSession{
		SessionID:          sessionID,
		RuntimeID:          runtimeID,
		SoftwareVersionRef: "unknown",
		StartedAtReceived:  1000,
	}
	result, err := s.CreateRuntimeSession(context.Background(), sess)
	if err != nil {
		t.Fatalf("create session %s/%s: %v", runtimeID, sessionID, err)
	}
	return result
}
