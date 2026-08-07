// Package store — Management Plane v2 持久化方法。
//
// v2 表与 v1 表共存于同一 SQLite 数据库；v2 表前缀/命名独立(robots/devices_v2/runtimes/
// runtime_sessions/runtime_heartbeats)。v1 表只读 tasks(用于 RunV2 引用检查)。
// ExternalRef 使用独立的 external_id_mappings 关系表，保持 namespace/value 唯一约束。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/inayina/robot-platform-service/internal/domain"
)

// ──── 常量 ────

const (
	RobotIDPrefix  = "rob-"
	DeviceIDPrefix = "dev-"
	RuntimeIDPrefix = "rt-"
	RunIDPrefix    = "run-"
)

// ──── 错误 ────

var (
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrSessionNotCurrent = errors.New("session is not current")
	ErrRobotMismatch     = errors.New("runtime session belongs to a different robot")
)

// ──── ID 校验 ────

func ValidateCanonicalID(id, prefix string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("%w: id must not be empty", ErrInvalidArgument)
	}
	return id, nil
}

// NewCanonicalID 生成带前缀的 canonical ID 并校验。
func NewCanonicalID(prefix string) (string, error) {
	return NewID(prefix), nil
}

// ──── 辅助 ────

func marshalExternalRefs(refs domain.ExternalRefs) (string, error) {
	if len(refs) == 0 { return "[]", nil }
	b, err := json.Marshal(refs)
	return string(b), err
}

// trimAndCheckExternalRefs 校验 ExternalRef 无空白,且同一 objectKind 内 namespace+value 唯一。
// 返回去重后的拷贝和手动 trim 校验结果。
func sanitizeExternalRefs(kind string, refs domain.ExternalRefs) (domain.ExternalRefs, error) {
	seen := map[string]bool{}
	var clean domain.ExternalRefs
	for _, r := range refs {
		ns := strings.TrimSpace(r.Namespace)
		val := strings.TrimSpace(r.Value)
		if ns != r.Namespace || val != r.Value {
			return nil, fmt.Errorf("%w: external_ref namespace/value must be trimmed", ErrInvalidArgument)
		}
		key := ns + "\x00" + val
		if seen[key] { continue }
		seen[key] = true
		clean = append(clean, domain.ExternalRef{Namespace: ns, Value: val})
	}
	return clean, nil
}

// writeExternalRefs 写入 ExternalRef 映射(事务内调用)
func writeExternalRefs(tx *sql.Tx, objectKind, objectID string, refs domain.ExternalRefs) error {
	if len(refs) == 0 { return nil }
	stmt, err := tx.Prepare(
		`INSERT OR IGNORE INTO external_id_mappings (object_kind, object_id, namespace, value)
		 VALUES (?, ?, ?, ?)`)
	if err != nil { return err }
	defer stmt.Close()
	seen := map[string]bool{}
	for _, r := range refs {
		key := r.Namespace + "\x00" + r.Value
		if seen[key] { continue }
		seen[key] = true
		if _, err := stmt.Exec(objectKind, objectID, r.Namespace, r.Value); err != nil {
			return fmt.Errorf("external ref: %w", err)
		}
	}
	return nil
}

// checkExternalRefConflict 检查 ExternalRef 是否已被其他同 kind 对象占用
func checkExternalRefConflict(tx *sql.Tx, objectKind, objectID string, refs domain.ExternalRefs) error {
	if len(refs) == 0 { return nil }
	for _, r := range refs {
		var existingID string
		err := tx.QueryRow(
			`SELECT object_id FROM external_id_mappings
			 WHERE object_kind = ? AND namespace = ? AND value = ? AND object_id != ?
			 LIMIT 1`,
			objectKind, r.Namespace, r.Value, objectID,
		).Scan(&existingID)
		if err == nil {
			return fmt.Errorf("%w: external_ref namespace=%s value=%s already mapped by %s %s", 
				ErrConflict, r.Namespace, r.Value, objectKind, existingID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

// ──── Robot ────

func (s *Store) CreateRobot(ctx context.Context, r *domain.Robot) error {
	refs, err := sanitizeExternalRefs("robot", r.ExternalRefs)
	if err != nil { return err }
	refsJSON, err := marshalExternalRefs(refs)
	if err != nil { return err }

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(`INSERT INTO robots (id, display_name, domain, embodiment, lifecycle_state, external_refs_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.DisplayName, r.Domain, r.Embodiment, r.LifecycleState, refsJSON, r.CreatedAt, r.UpdatedAt); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrConflict
		}
		return err
	}
	if err := checkExternalRefConflict(tx, "robot", r.ID, refs); err != nil {
		return fmt.Errorf("external ref: %w", err)
	}
	if err := writeExternalRefs(tx, "robot", r.ID, refs); err != nil {
		return err
	}
	r.ExternalRefs = refs // 回写去重后的 refs,API 层返回时不含重复
	return tx.Commit()
}

func (s *Store) GetRobot(ctx context.Context, id string) (*domain.Robot, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, display_name, domain, embodiment, lifecycle_state, external_refs_json, created_at, updated_at
		 FROM robots WHERE id = ?`, id)
	var r domain.Robot
	var refsJSON string
	if err := row.Scan(&r.ID, &r.DisplayName, &r.Domain, &r.Embodiment, &r.LifecycleState,
		&refsJSON, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return nil, ErrNotFound }
		return nil, err
	}
	if refsJSON != "" && refsJSON != "[]" {
		json.Unmarshal([]byte(refsJSON), &r.ExternalRefs)
	}
	return &r, nil
}

func (s *Store) ListRobots(ctx context.Context) ([]domain.Robot, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, display_name, domain, embodiment, lifecycle_state, external_refs_json, created_at, updated_at FROM robots ORDER BY created_at`)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []domain.Robot
	for rows.Next() {
		var r domain.Robot
		var refsJSON string
		if err := rows.Scan(&r.ID, &r.DisplayName, &r.Domain, &r.Embodiment, &r.LifecycleState,
			&refsJSON, &r.CreatedAt, &r.UpdatedAt); err != nil { return nil, err }
		if refsJSON != "" && refsJSON != "[]" { json.Unmarshal([]byte(refsJSON), &r.ExternalRefs) }
		out = append(out, r)
	}
	return out, rows.Err()
}

// ──── DeviceV2 ────

func (s *Store) CreateDeviceV2(ctx context.Context, d *domain.DeviceV2) error {
	refs, err := sanitizeExternalRefs("device", d.ExternalRefs)
	if err != nil { return err }
	refsJSON, _ := marshalExternalRefs(refs)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback() //nolint:errcheck

	// robot_ref 校验
	var one int
	if err := tx.QueryRow(`SELECT 1 FROM robots WHERE id = ?`, d.RobotID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return ErrNotFound } // robot not found
		return err
	}
	// parent device 同一 robot 校验
	if d.ParentDeviceID != "" {
		var parentRobot string
		if err := tx.QueryRow(`SELECT robot_id FROM devices_v2 WHERE id = ?`, d.ParentDeviceID).Scan(&parentRobot); err != nil {
			if errors.Is(err, sql.ErrNoRows) { return fmt.Errorf("parent device %w", ErrNotFound) }
			return err
		}
		if parentRobot != d.RobotID { return fmt.Errorf("parent device belongs to a different robot: %w", ErrBadReference) }
	}

	if _, err := tx.Exec(`INSERT INTO devices_v2 (id, robot_id, parent_device_id, display_name, device_class, domain_type, manufacturer, model, serial_number, lifecycle_state, external_refs_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.RobotID, d.ParentDeviceID, d.DisplayName, d.DeviceClass, d.DomainType, d.Manufacturer, d.Model, d.SerialNumber, d.LifecycleState, refsJSON, d.CreatedAt, d.UpdatedAt); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") { return ErrConflict }
		return err
	}
	if err := checkExternalRefConflict(tx, "device", d.ID, refs); err != nil { return err }
	if err := writeExternalRefs(tx, "device", d.ID, refs); err != nil { return err }
	return tx.Commit()
}

func (s *Store) GetDeviceV2(ctx context.Context, id string) (*domain.DeviceV2, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, robot_id, parent_device_id, display_name, device_class, domain_type, manufacturer, model, serial_number, lifecycle_state, external_refs_json, created_at, updated_at
		 FROM devices_v2 WHERE id = ?`, id)
	var d domain.DeviceV2
	var refsJSON string
	if err := row.Scan(&d.ID, &d.RobotID, &d.ParentDeviceID, &d.DisplayName, &d.DeviceClass,
		&d.DomainType, &d.Manufacturer, &d.Model, &d.SerialNumber,
		&d.LifecycleState, &refsJSON, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return nil, ErrNotFound }
		return nil, err
	}
	if refsJSON != "" && refsJSON != "[]" { json.Unmarshal([]byte(refsJSON), &d.ExternalRefs) }
	return &d, nil
}

func (s *Store) ListDevicesByRobot(ctx context.Context, robotID string) ([]domain.DeviceV2, error) {
	var one int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM robots WHERE id = ?`, robotID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return nil, ErrNotFound }
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, robot_id, parent_device_id, display_name, device_class, domain_type, manufacturer, model, serial_number, lifecycle_state, external_refs_json, created_at, updated_at
		 FROM devices_v2 WHERE robot_id = ? ORDER BY created_at`, robotID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []domain.DeviceV2
	for rows.Next() {
		var d domain.DeviceV2
		var refsJSON string
		if err := rows.Scan(&d.ID, &d.RobotID, &d.ParentDeviceID, &d.DisplayName, &d.DeviceClass,
			&d.DomainType, &d.Manufacturer, &d.Model, &d.SerialNumber,
			&d.LifecycleState, &refsJSON, &d.CreatedAt, &d.UpdatedAt); err != nil { return nil, err }
		if refsJSON != "" && refsJSON != "[]" { json.Unmarshal([]byte(refsJSON), &d.ExternalRefs) }
		out = append(out, d)
	}
	return out, rows.Err()
}

// ──── Runtime ────

func (s *Store) CreateRuntime(ctx context.Context, rt *domain.Runtime) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback() //nolint:errcheck

	var one int
	if err := tx.QueryRow(`SELECT 1 FROM robots WHERE id = ?`, rt.RobotID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return ErrNotFound }
		return err
	}
	if rt.HostDeviceID != "" {
		var hostRobotID string
		err := tx.QueryRow(`SELECT robot_id FROM devices_v2 WHERE id = ?`, rt.HostDeviceID).Scan(&hostRobotID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("host device: %w", ErrNotFound)
		}
		if err != nil { return err }
		if hostRobotID != rt.RobotID { return fmt.Errorf("%w: host device belongs to a different robot", ErrBadReference) }
	}

	if _, err := tx.Exec(`INSERT INTO runtimes (id, robot_id, host_device_id, display_name, runtime_role, component, heartbeat_interval_ms, lifecycle_state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rt.ID, rt.RobotID, rt.HostDeviceID, rt.DisplayName, rt.RuntimeRole, rt.Component, rt.HeartbeatIntervalMs, rt.LifecycleState, rt.CreatedAt, rt.UpdatedAt); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") { return ErrConflict }
		return err
	}
	return tx.Commit()
}

func (s *Store) GetRuntime(ctx context.Context, id string) (*domain.Runtime, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, robot_id, host_device_id, display_name, runtime_role, component, heartbeat_interval_ms, lifecycle_state, created_at, updated_at
		 FROM runtimes WHERE id = ?`, id)
	var rt domain.Runtime
	if err := row.Scan(&rt.ID, &rt.RobotID, &rt.HostDeviceID, &rt.DisplayName, &rt.RuntimeRole,
		&rt.Component, &rt.HeartbeatIntervalMs, &rt.LifecycleState, &rt.CreatedAt, &rt.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return nil, ErrNotFound }
		return nil, err
	}
	return &rt, nil
}

func (s *Store) ListRuntimesByRobot(ctx context.Context, robotID string) ([]domain.Runtime, error) {
	var one int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM robots WHERE id = ?`, robotID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return nil, ErrNotFound }
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, robot_id, host_device_id, display_name, runtime_role, component, heartbeat_interval_ms, lifecycle_state, created_at, updated_at
		 FROM runtimes WHERE robot_id = ? ORDER BY created_at`, robotID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []domain.Runtime
	for rows.Next() {
		var rt domain.Runtime
		if err := rows.Scan(&rt.ID, &rt.RobotID, &rt.HostDeviceID, &rt.DisplayName, &rt.RuntimeRole,
			&rt.Component, &rt.HeartbeatIntervalMs, &rt.LifecycleState, &rt.CreatedAt, &rt.UpdatedAt); err != nil { return nil, err }
		out = append(out, rt)
	}
	return out, rows.Err()
}

// ──── Session ────

func (s *Store) CreateRuntimeSession(ctx context.Context, sess *domain.RuntimeSession) (*domain.RuntimeSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return nil, err }
	defer tx.Rollback() //nolint:errcheck

	var one int
	if err := tx.QueryRow(`SELECT 1 FROM runtimes WHERE id = ?`, sess.RuntimeID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return nil, ErrNotFound }
		return nil, err
	}

	// 检查是否已存在(事务内查询避免读写竞争)
	var existingState string
	err = tx.QueryRow(`SELECT session_state FROM runtime_sessions WHERE runtime_id = ? AND session_id = ?`,
		sess.RuntimeID, sess.SessionID).Scan(&existingState)
	if err == nil {
		// 已存在——返回已有记录(幂等)
		sess.SessionState = domain.SessionState(existingState)
		return sess, tx.Rollback()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// 将当前 current 改为 superseded
	if _, err := tx.Exec(`UPDATE runtime_sessions SET session_state = ? WHERE runtime_id = ? AND session_state = ?`,
		domain.SessionSuperseded, sess.RuntimeID, domain.SessionCurrent); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(`INSERT INTO runtime_sessions (session_id, runtime_id, software_version_ref, session_state, started_at_reported, started_at_received, last_heartbeat_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, 0)`,
		sess.SessionID, sess.RuntimeID, sess.SoftwareVersionRef, domain.SessionCurrent, sess.StartedAtReported, sess.StartedAtReceived); err != nil {
		return nil, err
	}

	sess.SessionState = domain.SessionCurrent
	return sess, tx.Commit()
}

func (s *Store) GetCurrentSession(ctx context.Context, runtimeID string) (*domain.RuntimeSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT session_id, runtime_id, software_version_ref, session_state, started_at_reported, started_at_received, last_heartbeat_at_ms
		 FROM runtime_sessions WHERE runtime_id = ? AND session_state = ?`, runtimeID, domain.SessionCurrent)
	var sess domain.RuntimeSession
	if err := row.Scan(&sess.SessionID, &sess.RuntimeID, &sess.SoftwareVersionRef, &sess.SessionState,
		&sess.StartedAtReported, &sess.StartedAtReceived, &sess.LastHeartbeatAtMs); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return nil, ErrNotFound }
		return nil, err
	}
	return &sess, nil
}

func (s *Store) GetRuntimeSession(ctx context.Context, runtimeID, sessionID string) (*domain.RuntimeSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT session_id, runtime_id, software_version_ref, session_state, started_at_reported, started_at_received, last_heartbeat_at_ms
		 FROM runtime_sessions WHERE runtime_id = ? AND session_id = ?`, runtimeID, sessionID)
	var sess domain.RuntimeSession
	if err := row.Scan(&sess.SessionID, &sess.RuntimeID, &sess.SoftwareVersionRef, &sess.SessionState,
		&sess.StartedAtReported, &sess.StartedAtReceived, &sess.LastHeartbeatAtMs); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return nil, ErrNotFound }
		return nil, err
	}
	return &sess, nil
}

func (s *Store) ListRuntimeSessions(ctx context.Context, runtimeID string) ([]domain.RuntimeSession, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT session_id, runtime_id, software_version_ref, session_state, started_at_reported, started_at_received, last_heartbeat_at_ms
		 FROM runtime_sessions WHERE runtime_id = ? ORDER BY started_at_received DESC`, runtimeID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []domain.RuntimeSession
	for rows.Next() {
		var sess domain.RuntimeSession
		if err := rows.Scan(&sess.SessionID, &sess.RuntimeID, &sess.SoftwareVersionRef, &sess.SessionState,
			&sess.StartedAtReported, &sess.StartedAtReceived, &sess.LastHeartbeatAtMs); err != nil { return nil, err }
		out = append(out, sess)
	}
	return out, rows.Err()
}

// ──── RuntimeHeartbeat ────

func (s *Store) AddRuntimeHeartbeat(ctx context.Context, hb *domain.RuntimeHeartbeat) (domain.SessionState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return "", err }
	defer tx.Rollback() //nolint:errcheck

	// 获取 session 状态
	var state string
	if err := tx.QueryRow(`SELECT session_state FROM runtime_sessions WHERE runtime_id = ? AND session_id = ?`,
		hb.RuntimeID, hb.SessionID).Scan(&state); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return "", ErrNotFound }
		return "", err
	}
	sessionState := domain.SessionState(state)

	// seq 检查: 允许 gap; 同 key 同 payload 幂等; 新 seq < max → regression
	var maxSeq sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(seq) FROM runtime_heartbeats WHERE runtime_id = ? AND session_id = ?`,
		hb.RuntimeID, hb.SessionID).Scan(&maxSeq); err != nil {
		if !errors.Is(err, sql.ErrNoRows) { return "", err }
	}
	if maxSeq.Valid {
		if hb.Seq < maxSeq.Int64 {
			// seq 小于 max——先检查是否为幂等重发(同 seq+同 payload 已记录)
			var existingReceived int64
			err := tx.QueryRow(`SELECT received_at FROM runtime_heartbeats WHERE runtime_id=? AND session_id=? AND seq=?`,
				hb.RuntimeID, hb.SessionID, hb.Seq).Scan(&existingReceived)
			if err == nil && existingReceived == hb.ReceivedAt {
				if sessionState == domain.SessionCurrent {
					tx.Exec(`UPDATE runtime_sessions SET last_heartbeat_at_ms = ? WHERE runtime_id = ? AND session_id = ?`,
						hb.ReceivedAt, hb.RuntimeID, hb.SessionID)
				}
				return sessionState, tx.Commit()
			}
			return "", ErrSeqRegression
		}
		if hb.Seq == maxSeq.Int64 {
			var existingReceived int64
			_ = tx.QueryRow(`SELECT received_at FROM runtime_heartbeats WHERE runtime_id=? AND session_id=? AND seq=?`,
				hb.RuntimeID, hb.SessionID, hb.Seq).Scan(&existingReceived)
			if existingReceived == hb.ReceivedAt {
				// 只有 current session 更新 last_heartbeat_at_ms
				if sessionState == domain.SessionCurrent {
					if _, err := tx.Exec(`UPDATE runtime_sessions SET last_heartbeat_at_ms = ? WHERE runtime_id = ? AND session_id = ?`,
						hb.ReceivedAt, hb.RuntimeID, hb.SessionID); err != nil { return "", err }
				}
				return sessionState, tx.Commit()
			}
			return sessionState, tx.Commit()
		}
	}

	if _, err := tx.Exec(`INSERT INTO runtime_heartbeats (runtime_id, session_id, seq, reported_at, received_at)
		VALUES (?, ?, ?, ?, ?)`, hb.RuntimeID, hb.SessionID, hb.Seq, hb.ReportedAt, hb.ReceivedAt); err != nil {
		return "", err
	}

	// 只有 current session 更新 last_heartbeat_at_ms;superseded/ended 只接收审计
	if sessionState == domain.SessionCurrent {
		if _, err := tx.Exec(`UPDATE runtime_sessions SET last_heartbeat_at_ms = ? WHERE runtime_id = ? AND session_id = ?`,
			hb.ReceivedAt, hb.RuntimeID, hb.SessionID); err != nil { return "", err }
	}
	return sessionState, tx.Commit()
}

func (s *Store) LastRuntimeHeartbeat(ctx context.Context, runtimeID, sessionID string) (*domain.RuntimeHeartbeat, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT runtime_id, session_id, seq, reported_at, received_at
		 FROM runtime_heartbeats WHERE runtime_id = ? AND session_id = ?
		 ORDER BY seq DESC LIMIT 1`, runtimeID, sessionID)
	var hb domain.RuntimeHeartbeat
	if err := row.Scan(&hb.RuntimeID, &hb.SessionID, &hb.Seq, &hb.ReportedAt, &hb.ReceivedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return nil, ErrNotFound }
		return nil, err
	}
	return &hb, nil
}

// ──── Session End ────

func (s *Store) EndSession(ctx context.Context, runtimeID, sessionID string, endedAt int64) (domain.SessionState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return "", err }
	defer tx.Rollback() //nolint:errcheck
	var state string
	if err := tx.QueryRow(`SELECT session_state FROM runtime_sessions WHERE runtime_id = ? AND session_id = ?`,
		runtimeID, sessionID).Scan(&state); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return "", ErrNotFound }
		return "", err
	}
	newState := domain.SessionEnded
	if _, err := tx.Exec(`UPDATE runtime_sessions SET session_state = ?, ended_at = ? WHERE runtime_id = ? AND session_id = ?`,
		newState, endedAt, runtimeID, sessionID); err != nil { return "", err }
	return domain.SessionState(newState), tx.Commit()
}

// EndRuntimeSession 是 EndSession 的别名(API v2 命名约定)。
func (s *Store) EndRuntimeSession(ctx context.Context, runtimeID, sessionID string, endedAt int64) (domain.SessionState, error) {
	return s.EndSession(ctx, runtimeID, sessionID, endedAt)
}

// ──── RunV2 ────

func (s *Store) ListRunsV2(ctx context.Context, robotID string) ([]domain.RunV2, error) {
	q := `SELECT id, task_id, robot_id, runtime_id, session_id, started_at, ended_at, result, artifact_ref_json
		 FROM runs_v2 ORDER BY started_at DESC`
	var args []any
	if robotID != "" {
		q = `SELECT id, task_id, robot_id, runtime_id, session_id, started_at, ended_at, result, artifact_ref_json
		 FROM runs_v2 WHERE robot_id = ? ORDER BY started_at DESC`
		args = append(args, robotID)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []domain.RunV2
	for rows.Next() {
		var r domain.RunV2
		var artJSON string
		if err := rows.Scan(&r.ID, &r.TaskID, &r.RobotID, &r.RuntimeID, &r.SessionID,
			&r.StartedMs, &r.EndedMs, &r.Result, &artJSON); err != nil { return nil, err }
		if artJSON != "" && artJSON != "{}" {
			var a domain.ArtifactRef
			if json.Unmarshal([]byte(artJSON), &a) == nil { r.ArtifactRef = &a }
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CreateRunV2(ctx context.Context, run *domain.RunV2) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback() //nolint:errcheck

	// 校验 task 存在
	var one int
	if err := tx.QueryRow(`SELECT 1 FROM tasks WHERE id = ?`, run.TaskID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return ErrBadReference }
		return err
	}

	// 校验 session 存在
	var sessRuntimeID string
	if err := tx.QueryRow(`SELECT runtime_id FROM runtime_sessions WHERE runtime_id = ? AND session_id = ?`,
		run.RuntimeID, run.SessionID).Scan(&sessRuntimeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return ErrBadReference }
		return err
	}

	// 校验 robot-runtime 一致性
	var rtRobotID string
	if err := tx.QueryRow(`SELECT robot_id FROM runtimes WHERE id = ?`, run.RuntimeID).Scan(&rtRobotID); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return ErrBadReference }
		return err
	}
	if rtRobotID != run.RobotID {
		return ErrRobotMismatch
	}

	artifactJSON := "{}"
	if run.ArtifactRef != nil {
		if b, err := json.Marshal(run.ArtifactRef); err == nil { artifactJSON = string(b) }
	}
	if _, err := tx.Exec(
		`INSERT INTO runs_v2 (id, task_id, robot_id, runtime_id, session_id, started_at, ended_at, result, artifact_ref_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.TaskID, run.RobotID, run.RuntimeID, run.SessionID,
		run.StartedMs, run.EndedMs, run.Result, artifactJSON); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetRunV2(ctx context.Context, id string) (*domain.RunV2, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, task_id, robot_id, runtime_id, session_id, started_at, ended_at, result, artifact_ref_json
		 FROM runs_v2 WHERE id = ?`, id)
	var r domain.RunV2
	var artJSON string
	if err := row.Scan(&r.ID, &r.TaskID, &r.RobotID, &r.RuntimeID, &r.SessionID,
		&r.StartedMs, &r.EndedMs, &r.Result, &artJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return nil, ErrNotFound }
		return nil, err
	}
	if artJSON != "" && artJSON != "{}" {
		var a domain.ArtifactRef
		if json.Unmarshal([]byte(artJSON), &a) == nil { r.ArtifactRef = &a }
	}
	return &r, nil
}
