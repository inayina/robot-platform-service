package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inayina/robot-platform-service/internal/domain"
)

func openD2TestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open D2 store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestD2CanonicalIDIssuer(t *testing.T) {
	for _, prefix := range []string{RobotIDPrefix, DeviceIDPrefix, RuntimeIDPrefix, RunIDPrefix} {
		id, err := NewCanonicalID(prefix)
		if err != nil {
			t.Fatalf("generate %s ID: %v", prefix, err)
		}
		if !strings.HasPrefix(id, prefix+"-") {
			t.Fatalf("ID %q is outside %s namespace", id, prefix)
		}
		suffix := strings.TrimPrefix(id, prefix+"-")
		if len(suffix) != 32 {
			t.Fatalf("ID %q must use the current 128-bit issuer encoding", id)
		}
		if _, err := hex.DecodeString(suffix); err != nil {
			t.Fatalf("ID %q has a non-hex issuer suffix: %v", id, err)
		}
		if err := ValidateCanonicalID(id, prefix); err != nil {
			t.Fatalf("generated ID failed validation: %v", err)
		}
	}
	if _, err := NewCanonicalID("runtime"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("unsupported prefix must fail closed, got %v", err)
	}
}

func TestD2SchemaEnforcesIdentityAndReferences(t *testing.T) {
	s := openD2TestStore(t)
	ctx := context.Background()

	var foreignKeys int
	if err := s.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys must be enabled on the active connection, got %d", foreignKeys)
	}
	version, err := s.userVersion()
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != managementSchemaVersion {
		t.Fatalf("schema version: want %d, got %d", managementSchemaVersion, version)
	}

	// CHECK protects the canonical namespace and enum even if a caller bypasses Store validation.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO robots
		(id, display_name, domain, embodiment, lifecycle_state, created_at, updated_at)
		VALUES ('caller-id', 'bad', 'amr', 'simulation', 'active', 1, 1)`); err == nil {
		t.Fatal("database accepted a non-canonical Robot ID")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO robots
		(id, display_name, domain, embodiment, lifecycle_state, created_at, updated_at)
		VALUES ('rob-bad-state', 'bad', 'amr', 'simulation', 'unknown', 1, 1)`); err == nil {
		t.Fatal("database accepted an invalid lifecycle_state")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO robots
		(id, display_name, domain, embodiment, lifecycle_state, created_at, updated_at)
		VALUES ('rob-immutable', 'stable identity', 'amr', 'simulation', 'active', 1, 1)`); err != nil {
		t.Fatalf("insert valid Robot: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE robots SET id = 'rob-rewritten' WHERE id = 'rob-immutable'`); err == nil {
		t.Fatal("database allowed a Robot canonical identity rewrite")
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM robots WHERE id = 'rob-immutable'`); err == nil {
		t.Fatal("database allowed a Robot canonical identity hard delete")
	}

	// FK rejects an orphan even when bypassing the transaction-level existence check.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO devices_v2
		(id, robot_id, parent_device_id, display_name, device_class, domain_type,
		 manufacturer, model, serial_number, lifecycle_state, created_at, updated_at)
		VALUES ('dev-orphan', 'rob-missing', NULL, 'orphan', 'sensor', '', '', '', '', 'active', 1, 1)`); err == nil {
		t.Fatal("database accepted an orphan Device")
	}
}

func TestD2SchemaAllowsOnlyOneCurrentSession(t *testing.T) {
	s := openD2TestStore(t)
	ctx := context.Background()

	r := &domain.Robot{
		ID: "rob-session", DisplayName: "session-robot", Domain: "amr", Embodiment: "simulation",
		LifecycleState: domain.RobotActive, CreatedAt: 1, UpdatedAt: 1,
	}
	if err := s.CreateRobot(ctx, r); err != nil {
		t.Fatalf("create robot: %v", err)
	}
	rt := &domain.Runtime{
		ID: "rt-session", RobotID: r.ID, DisplayName: "executor",
		RuntimeRole: domain.RuntimeDomainExecutor, Component: "executor",
		HeartbeatIntervalMs: 100, LifecycleState: "active", CreatedAt: 2, UpdatedAt: 2,
	}
	if err := s.CreateRuntime(ctx, rt); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if _, err := s.CreateRuntimeSession(ctx, &domain.RuntimeSession{
		RuntimeID: rt.ID, SessionID: "boot-1", SoftwareVersionRef: "unknown", StartedAtReceived: 3,
	}); err != nil {
		t.Fatalf("create first session: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, `INSERT INTO runtime_sessions
		(runtime_id, session_id, software_version_ref, started_at_received, session_state, last_heartbeat_at_ms)
		VALUES (?, 'boot-2', 'unknown', 4, 'current', 0)`, rt.ID); err == nil {
		t.Fatal("database accepted two current sessions for one Runtime")
	}
}

func TestD2SchemaRejectsContainmentCycleOnUpdate(t *testing.T) {
	s := openD2TestStore(t)
	ctx := context.Background()

	r := &domain.Robot{
		ID: "rob-cycle", DisplayName: "cycle-robot", Domain: "amr", Embodiment: "simulation",
		LifecycleState: domain.RobotActive, CreatedAt: 1, UpdatedAt: 1,
	}
	if err := s.CreateRobot(ctx, r); err != nil {
		t.Fatalf("create robot: %v", err)
	}
	a := &domain.DeviceV2{
		ID: "dev-cycle-a", RobotID: r.ID, DisplayName: "a", DeviceClass: domain.DeviceComposite,
		LifecycleState: "active", CreatedAt: 2, UpdatedAt: 2,
	}
	if err := s.CreateDeviceV2(ctx, a); err != nil {
		t.Fatalf("create device a: %v", err)
	}
	b := &domain.DeviceV2{
		ID: "dev-cycle-b", RobotID: r.ID, ParentDeviceID: a.ID, DisplayName: "b",
		DeviceClass: domain.DeviceSensor, LifecycleState: "active", CreatedAt: 3, UpdatedAt: 3,
	}
	if err := s.CreateDeviceV2(ctx, b); err != nil {
		t.Fatalf("create device b: %v", err)
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE devices_v2 SET parent_device_id = ? WHERE id = ?`, b.ID, a.ID); err == nil {
		t.Fatal("database accepted a Device containment cycle")
	}
}

func TestD2RefusesImplicitMigrationOfPreD2Tables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-d2.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE robots (id TEXT PRIMARY KEY)`); err != nil {
		_ = db.Close()
		t.Fatalf("create pre-D2 marker table: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy sqlite: %v", err)
	}

	s, err := Open(path)
	if s != nil {
		_ = s.Close()
	}
	if !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("pre-D2 database must require explicit migration, got %v", err)
	}
}
