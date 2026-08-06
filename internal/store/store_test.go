package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/inayina/robot-platform-service/internal/domain"
	"github.com/inayina/robot-platform-service/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustDevice(t *testing.T, s *store.Store, id string) *domain.Device {
	t.Helper()
	d := &domain.Device{ID: id, Name: "test-dev", Kind: "dev", Version: "v0.1.0", FirstSeenMs: 1000}
	if err := s.CreateDevice(context.Background(), d); err != nil {
		t.Fatalf("create device: %v", err)
	}
	return d
}

func TestDeviceCRUD(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	d := mustDevice(t, s, "dev-1")

	got, err := s.GetDevice(ctx, "dev-1")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if got.Name != d.Name || got.Kind != "dev" {
		t.Errorf("unexpected device: %+v", got)
	}

	// 重复注册 → ErrConflict
	if err := s.CreateDevice(ctx, d); !errors.Is(err, store.ErrConflict) {
		t.Errorf("duplicate create: want ErrConflict, got %v", err)
	}

	// 不存在 → ErrNotFound
	if _, err := s.GetDevice(ctx, "dev-nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing device: want ErrNotFound, got %v", err)
	}

	// 列表
	devs, err := s.ListDevices(ctx)
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devs) != 1 {
		t.Errorf("want 1 device, got %d", len(devs))
	}
}

func TestHeartbeatSeqStrictlyIncreasing(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustDevice(t, s, "dev-1")

	if err := s.AddHeartbeat(ctx, domain.Heartbeat{DeviceID: "dev-1", Seq: 1, TsMs: 2000}); err != nil {
		t.Fatalf("first heartbeat: %v", err)
	}
	if err := s.AddHeartbeat(ctx, domain.Heartbeat{DeviceID: "dev-1", Seq: 2, TsMs: 3000}); err != nil {
		t.Fatalf("second heartbeat: %v", err)
	}

	// seq 回退/重复 → ErrSeqRegression
	if err := s.AddHeartbeat(ctx, domain.Heartbeat{DeviceID: "dev-1", Seq: 2, TsMs: 4000}); !errors.Is(err, store.ErrSeqRegression) {
		t.Errorf("duplicate seq: want ErrSeqRegression, got %v", err)
	}
	if err := s.AddHeartbeat(ctx, domain.Heartbeat{DeviceID: "dev-1", Seq: 1, TsMs: 4000}); !errors.Is(err, store.ErrSeqRegression) {
		t.Errorf("regressed seq: want ErrSeqRegression, got %v", err)
	}

	// last_seen 被更新,且 LastHeartbeat 返回最新
	d, err := s.GetDevice(ctx, "dev-1")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if d.LastSeenMs != 3000 {
		t.Errorf("last_seen: want 3000, got %d", d.LastSeenMs)
	}
	hb, err := s.LastHeartbeat(ctx, "dev-1")
	if err != nil {
		t.Fatalf("last heartbeat: %v", err)
	}
	if hb.Seq != 2 || hb.TsMs != 3000 {
		t.Errorf("last heartbeat: %+v", hb)
	}
}

func TestHeartbeatUnknownDevice(t *testing.T) {
	s := newStore(t)
	err := s.AddHeartbeat(context.Background(), domain.Heartbeat{DeviceID: "ghost", Seq: 1, TsMs: 1})
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown device heartbeat: want ErrNotFound, got %v", err)
	}
}

func TestTaskAndRun(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustDevice(t, s, "dev-1")

	// task 创建 + 状态过滤
	t1 := &domain.Task{ID: "task-1", Domain: "amr", Kind: "transport", Target: "station_a", Status: domain.TaskPending, CreatedMs: 100, UpdatedMs: 100}
	t2 := &domain.Task{ID: "task-2", Domain: "panda", Kind: "collect", Target: "red-box", Status: domain.TaskSucceeded, CreatedMs: 200, UpdatedMs: 200}
	if err := s.CreateTask(ctx, t1); err != nil {
		t.Fatalf("create task1: %v", err)
	}
	if err := s.CreateTask(ctx, t2); err != nil {
		t.Fatalf("create task2: %v", err)
	}
	pending, err := s.ListTasks(ctx, string(domain.TaskPending))
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "task-1" {
		t.Errorf("want 1 pending task, got %+v", pending)
	}
	all, err := s.ListTasks(ctx, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("want 2 tasks, got %d", len(all))
	}

	// run:引用不存在的 task/device → ErrBadReference
	bad := &domain.Run{ID: "run-bad", TaskID: "task-nope", DeviceID: "dev-1", StartedMs: 1}
	if err := s.CreateRun(ctx, bad); !errors.Is(err, store.ErrBadReference) {
		t.Errorf("bad task ref: want ErrBadReference, got %v", err)
	}

	// run 创建 + 查询
	run := &domain.Run{ID: "run-1", TaskID: "task-1", DeviceID: "dev-1", StartedMs: 300, Result: "succeeded", ArtifactRef: "release/e2_scene_preflight_20260718_v1"}
	if err := s.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	got, err := s.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Result != "succeeded" || got.ArtifactRef != run.ArtifactRef {
		t.Errorf("unexpected run: %+v", got)
	}
	if _, err := s.GetRun(ctx, "run-nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing run: want ErrNotFound, got %v", err)
	}
}
