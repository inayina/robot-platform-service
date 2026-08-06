// Package store — Management Plane v2 本地草案持久化方法。
//
// v2 方法操作本地草案表(robots/devices_v2/runtimes/runtime_sessions/
// runtime_heartbeats/runs_v2)，v1 表只读 tasks(用于 RunV2 引用检查)。
// D2 同时使用 SQLite FK/CHECK/UNIQUE 与事务内语义检查：数据库阻止孤儿和非法枚举，
// store 负责跨行的同 Robot ownership、ExternalRef mapping 和可读领域错误。
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

// v2 领域错误。
var (
	ErrSessionNotCurrent = errors.New("session is not current")
	ErrRobotMismatch     = errors.New("runtime session belongs to a different robot")
)

// ---------------------------------------------------------------------------
// Robot
// ---------------------------------------------------------------------------

// CreateRobot 注册 Robot；canonical ID 必须由上层 Platform identity issuer 预先签发。
func (s *Store) CreateRobot(ctx context.Context, r *domain.Robot) error {
	refs, err := validateRobotRecord(r)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	_, err = tx.ExecContext(ctx,
		`INSERT INTO robots (id, display_name, domain, embodiment, lifecycle_state, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.DisplayName, r.Domain, r.Embodiment, r.LifecycleState, r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return mapWriteConstraint(err)
	}
	if err := insertExternalRefs(ctx, tx, robotExternalRefs, r.ID, refs); err != nil {
		return err
	}
	r.ExternalRefs = refs
	return tx.Commit()
}

// GetRobot 查询单个 Robot。
func (s *Store) GetRobot(ctx context.Context, id string) (*domain.Robot, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, display_name, domain, embodiment, lifecycle_state, created_at, updated_at
		 FROM robots WHERE id = ?`, id)
	var r domain.Robot
	if err := row.Scan(&r.ID, &r.DisplayName, &r.Domain, &r.Embodiment,
		&r.LifecycleState, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	refs, err := loadExternalRefs(ctx, s.db, robotExternalRefs, r.ID)
	if err != nil {
		return nil, err
	}
	r.ExternalRefs = refs
	return &r, nil
}

// ListRobots 返回全部 Robot。
func (s *Store) ListRobots(ctx context.Context) ([]domain.Robot, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, display_name, domain, embodiment, lifecycle_state, created_at, updated_at
		 FROM robots ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Robot
	for rows.Next() {
		var r domain.Robot
		if err := rows.Scan(&r.ID, &r.DisplayName, &r.Domain, &r.Embodiment,
			&r.LifecycleState, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		refs, err := loadExternalRefs(ctx, s.db, robotExternalRefs, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].ExternalRefs = refs
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Device v2
// ---------------------------------------------------------------------------

// CreateDeviceV2 注册 Device。约束(事务内检查):
//   - robot_id 必须存在；
//   - parent_device_id 如果指定，必须与 child 属于同一 Robot，且不能形成环。
func (s *Store) CreateDeviceV2(ctx context.Context, d *domain.DeviceV2) error {
	refs, err := validateDeviceRecord(d)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// 检查 robot 存在
	if err := checkExists(ctx, tx, "robots", d.RobotID); err != nil {
		return err
	}

	// 检查 parent 约束
	if d.ParentDeviceID != "" {
		parent, err := getDeviceV2(ctx, tx, d.ParentDeviceID)
		if err != nil {
			return ErrBadReference
		}
		if parent.RobotID != d.RobotID {
			return fmt.Errorf("parent device belongs to robot %s, not %s: %w",
				parent.RobotID, d.RobotID, ErrBadReference)
		}
		// 无环检查：沿 parent 链向上走，不能回到当前 device
		if err := checkNoCycle(ctx, tx, d.ParentDeviceID, d.ID); err != nil {
			return err
		}
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO devices_v2 (id, robot_id, parent_device_id, display_name, device_class,
		 domain_type, manufacturer, model, serial_number, lifecycle_state, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.RobotID, nullStr(d.ParentDeviceID), d.DisplayName, d.DeviceClass,
		d.DomainType, d.Manufacturer, d.Model, d.SerialNumber, d.LifecycleState, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return mapWriteConstraint(err)
	}
	if err := insertExternalRefs(ctx, tx, deviceExternalRefs, d.ID, refs); err != nil {
		return err
	}
	d.ExternalRefs = refs
	return tx.Commit()
}

// GetDeviceV2 查询单个 Device。
func (s *Store) GetDeviceV2(ctx context.Context, id string) (*domain.DeviceV2, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	return getDeviceV2(ctx, tx, id)
}

// ListDevicesByRobot 返回指定 Robot 下的全部 Device。
func (s *Store) ListDevicesByRobot(ctx context.Context, robotID string) ([]domain.DeviceV2, error) {
	// 先确认 robot 存在
	if _, err := s.GetRobot(ctx, robotID); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, robot_id, parent_device_id, display_name, device_class,
		 domain_type, manufacturer, model, serial_number, lifecycle_state, created_at, updated_at
		 FROM devices_v2 WHERE robot_id = ? ORDER BY created_at`, robotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.DeviceV2
	for rows.Next() {
		var d domain.DeviceV2
		var parentID sql.NullString
		if err := rows.Scan(&d.ID, &d.RobotID, &parentID, &d.DisplayName, &d.DeviceClass,
			&d.DomainType, &d.Manufacturer, &d.Model, &d.SerialNumber,
			&d.LifecycleState, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		if parentID.Valid {
			d.ParentDeviceID = parentID.String
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		refs, err := loadExternalRefs(ctx, s.db, deviceExternalRefs, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].ExternalRefs = refs
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Runtime
// ---------------------------------------------------------------------------

// CreateRuntime 注册 Runtime。约束(事务内):
//   - robot_id 必须存在；
//   - host_device_id 如果指定，必须存在且属于同一 Robot。
func (s *Store) CreateRuntime(ctx context.Context, rt *domain.Runtime) error {
	refs, err := validateRuntimeRecord(rt)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	if err := checkExists(ctx, tx, "robots", rt.RobotID); err != nil {
		return err
	}

	if rt.HostDeviceID != "" {
		dev, err := getDeviceV2(ctx, tx, rt.HostDeviceID)
		if err != nil {
			return ErrBadReference
		}
		if dev.RobotID != rt.RobotID {
			return fmt.Errorf("host device belongs to robot %s, not %s: %w",
				dev.RobotID, rt.RobotID, ErrBadReference)
		}
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO runtimes (id, robot_id, display_name, runtime_role, component,
		 host_device_id, heartbeat_interval_ms, lifecycle_state, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rt.ID, rt.RobotID, rt.DisplayName, rt.RuntimeRole, rt.Component,
		nullStr(rt.HostDeviceID), rt.HeartbeatIntervalMs, rt.LifecycleState, rt.CreatedAt, rt.UpdatedAt)
	if err != nil {
		return mapWriteConstraint(err)
	}
	if err := insertExternalRefs(ctx, tx, runtimeExternalRefs, rt.ID, refs); err != nil {
		return err
	}
	rt.ExternalRefs = refs
	return tx.Commit()
}

// GetRuntime 查询单个 Runtime。
func (s *Store) GetRuntime(ctx context.Context, id string) (*domain.Runtime, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, robot_id, display_name, runtime_role, component, host_device_id,
		 heartbeat_interval_ms, lifecycle_state, created_at, updated_at
		 FROM runtimes WHERE id = ?`, id)
	var rt domain.Runtime
	var hostID sql.NullString
	if err := row.Scan(&rt.ID, &rt.RobotID, &rt.DisplayName, &rt.RuntimeRole, &rt.Component,
		&hostID, &rt.HeartbeatIntervalMs, &rt.LifecycleState, &rt.CreatedAt, &rt.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if hostID.Valid {
		rt.HostDeviceID = hostID.String
	}
	refs, err := loadExternalRefs(ctx, s.db, runtimeExternalRefs, rt.ID)
	if err != nil {
		return nil, err
	}
	rt.ExternalRefs = refs
	return &rt, nil
}

// ListRuntimesByRobot 返回指定 Robot 下的全部 Runtime。
func (s *Store) ListRuntimesByRobot(ctx context.Context, robotID string) ([]domain.Runtime, error) {
	if _, err := s.GetRobot(ctx, robotID); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, robot_id, display_name, runtime_role, component, host_device_id,
		 heartbeat_interval_ms, lifecycle_state, created_at, updated_at
		 FROM runtimes WHERE robot_id = ? ORDER BY created_at`, robotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Runtime
	for rows.Next() {
		var rt domain.Runtime
		var hostID sql.NullString
		if err := rows.Scan(&rt.ID, &rt.RobotID, &rt.DisplayName, &rt.RuntimeRole, &rt.Component,
			&hostID, &rt.HeartbeatIntervalMs, &rt.LifecycleState, &rt.CreatedAt, &rt.UpdatedAt); err != nil {
			return nil, err
		}
		if hostID.Valid {
			rt.HostDeviceID = hostID.String
		}
		out = append(out, rt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		refs, err := loadExternalRefs(ctx, s.db, runtimeExternalRefs, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].ExternalRefs = refs
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// RuntimeSession
// ---------------------------------------------------------------------------

// CreateRuntimeSession 为 Runtime 创建新 session。行为(事务内):
//   - runtime_id 必须存在；
//   - (runtime_id, session_id) 已存在 → 幂等返回已有记录(含 session_state，不重新激活)；
//   - 新 session → 先将该 runtime 的 current session 变为 superseded，再插入 current。
func (s *Store) CreateRuntimeSession(ctx context.Context, sess *domain.RuntimeSession) (*domain.RuntimeSession, error) {
	if err := validateRuntimeSessionRecord(sess); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	// 检查 runtime 存在
	if err := checkExists(ctx, tx, "runtimes", sess.RuntimeID); err != nil {
		return nil, err
	}

	// 幂等：已存在 (runtime_id, session_id) → 返回已有记录
	existing, err := getSession(ctx, tx, sess.RuntimeID, sess.SessionID)
	if err == nil {
		// 已存在，返回已有记录(不重新激活)
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	// 将当前 current session 变为 superseded。错误必须使整个 transaction 回滚，
	// 不能在 supersede 未完成时继续插入第二个 current session。
	if _, err := tx.ExecContext(ctx,
		`UPDATE runtime_sessions SET session_state = 'superseded'
		 WHERE runtime_id = ? AND session_state = 'current'`, sess.RuntimeID); err != nil {
		return nil, err
	}

	// 插入新 session(current)
	if sess.SoftwareVersionRef == "" {
		sess.SoftwareVersionRef = "unknown"
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO runtime_sessions
		 (runtime_id, session_id, software_version_ref, started_at_reported, started_at_received,
		  session_state, last_heartbeat_at_ms)
		 VALUES (?, ?, ?, ?, ?, 'current', 0)`,
		sess.RuntimeID, sess.SessionID, sess.SoftwareVersionRef,
		nullInt(sess.StartedAtReported), sess.StartedAtReceived)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, ErrConflict
		}
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// 返回刚创建的记录
	return getSessionDirect(ctx, s.db, sess.RuntimeID, sess.SessionID)
}

// GetRuntimeSession 查询单个 session。
func (s *Store) GetRuntimeSession(ctx context.Context, runtimeID, sessionID string) (*domain.RuntimeSession, error) {
	return getSessionDirect(ctx, s.db, runtimeID, sessionID)
}

// GetCurrentSession 返回指定 Runtime 的 current session(可能为 nil)。
func (s *Store) GetCurrentSession(ctx context.Context, runtimeID string) (*domain.RuntimeSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT runtime_id, session_id, software_version_ref, started_at_reported, started_at_received,
		 ended_at_reported, ended_at_received, session_state, last_heartbeat_at_ms
		 FROM runtime_sessions WHERE runtime_id = ? AND session_state = 'current'`, runtimeID)
	return scanSession(row)
}

// ListRuntimeSessions 返回指定 Runtime 的全部 session 历史。
func (s *Store) ListRuntimeSessions(ctx context.Context, runtimeID string) ([]domain.RuntimeSession, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT runtime_id, session_id, software_version_ref, started_at_reported, started_at_received,
		 ended_at_reported, ended_at_received, session_state, last_heartbeat_at_ms
		 FROM runtime_sessions WHERE runtime_id = ? ORDER BY started_at_received DESC`, runtimeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.RuntimeSession
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sess)
	}
	return out, rows.Err()
}

// EndRuntimeSession 结束一个 session。已 ended → no-op；不存在 → ErrNotFound。
func (s *Store) EndRuntimeSession(ctx context.Context, runtimeID, sessionID string, endedAtReceived int64) error {
	if err := validateSessionIdentity(runtimeID, sessionID); err != nil {
		return err
	}
	if endedAtReceived <= 0 {
		return fmt.Errorf("ended_at_received must be positive: %w", ErrInvalidArgument)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	sess, err := getSession(ctx, tx, runtimeID, sessionID)
	if err != nil {
		return err
	}
	if sess.SessionState == domain.SessionEnded {
		return tx.Commit() // no-op
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE runtime_sessions SET session_state = 'ended', ended_at_received = ?
		 WHERE runtime_id = ? AND session_id = ?`,
		endedAtReceived, runtimeID, sessionID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------------
// RuntimeHeartbeat
// ---------------------------------------------------------------------------

// AddRuntimeHeartbeat 追加管理面心跳。约束(事务内):
//   - session 必须存在；
//   - 同一 (runtime_id, session_id, seq) 且 payload 相同 → 幂等成功；
//   - 同一 key 但 payload 不同 → ErrConflict；
//   - session 内 seq 必须严格递增；
//   - 只有 current session 才更新 last_heartbeat_at_ms；
//   - 返回 session 的当前状态(调用方可据此判断是否被接受)。
func (s *Store) AddRuntimeHeartbeat(ctx context.Context, hb *domain.RuntimeHeartbeat) (domain.SessionState, error) {
	if err := validateRuntimeHeartbeatRecord(hb); err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback() //nolint:errcheck

	// 检查 session 存在
	sess, err := getSession(ctx, tx, hb.RuntimeID, hb.SessionID)
	if err != nil {
		return "", err
	}

	// 幂等检查：同一 key 是否已存在
	var existingReported sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT reported_at FROM runtime_heartbeats
		 WHERE runtime_id = ? AND session_id = ? AND seq = ?`,
		hb.RuntimeID, hb.SessionID, hb.Seq).Scan(&existingReported)
	if err == nil {
		// key 已存在
		same := (existingReported.Valid && hb.ReportedAt > 0 && existingReported.Int64 == hb.ReportedAt) ||
			(!existingReported.Valid && hb.ReportedAt == 0)
		if same {
			// 幂等成功，不写入
			if err := tx.Commit(); err != nil {
				return "", err
			}
			return sess.SessionState, nil
		}
		return "", fmt.Errorf("heartbeat (runtime=%s, session=%s, seq=%d) already exists with different payload: %w",
			hb.RuntimeID, hb.SessionID, hb.Seq, ErrConflict)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	// seq 严格递增检查(session 内)
	var maxSeq sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MAX(seq) FROM runtime_heartbeats
		 WHERE runtime_id = ? AND session_id = ?`,
		hb.RuntimeID, hb.SessionID).Scan(&maxSeq); err != nil {
		return "", err
	}
	if maxSeq.Valid && hb.Seq <= maxSeq.Int64 {
		return "", ErrSeqRegression
	}

	// 插入心跳记录
	_, err = tx.ExecContext(ctx,
		`INSERT INTO runtime_heartbeats (runtime_id, session_id, seq, reported_at, received_at)
		 VALUES (?, ?, ?, ?, ?)`,
		hb.RuntimeID, hb.SessionID, hb.Seq, nullInt(hb.ReportedAt), hb.ReceivedAt)
	if err != nil {
		return "", err
	}

	// 仅 current session 更新 last_heartbeat_at_ms(合同 7.2：迟到心跳只保留审计)
	if sess.SessionState == domain.SessionCurrent {
		_, err = tx.ExecContext(ctx,
			`UPDATE runtime_sessions SET last_heartbeat_at_ms = ?
			 WHERE runtime_id = ? AND session_id = ?`,
			hb.ReceivedAt, hb.RuntimeID, hb.SessionID)
		if err != nil {
			return "", err
		}
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return sess.SessionState, nil
}

// LastRuntimeHeartbeat 返回指定 session 最近一次心跳。
func (s *Store) LastRuntimeHeartbeat(ctx context.Context, runtimeID, sessionID string) (*domain.RuntimeHeartbeat, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT runtime_id, session_id, seq, reported_at, received_at
		 FROM runtime_heartbeats
		 WHERE runtime_id = ? AND session_id = ?
		 ORDER BY seq DESC LIMIT 1`, runtimeID, sessionID)
	var hb domain.RuntimeHeartbeat
	var reported sql.NullInt64
	if err := row.Scan(&hb.RuntimeID, &hb.SessionID, &hb.Seq, &reported, &hb.ReceivedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if reported.Valid {
		hb.ReportedAt = reported.Int64
	}
	return &hb, nil
}

// ---------------------------------------------------------------------------
// Run v2
// ---------------------------------------------------------------------------

// CreateRunV2 创建 v2 Run。约束(事务内):
//   - task_id 必须存在(v1 tasks 表)；
//   - (runtime_id, session_id) 必须存在；
//   - session 所属 runtime 的 robot_id 必须与 run.robot_id 一致。
func (s *Store) CreateRunV2(ctx context.Context, r *domain.RunV2) error {
	if err := validateRunRecord(r); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// task 存在(v1 tasks 表)
	if err := checkExists(ctx, tx, "tasks", r.TaskID); err != nil {
		return ErrBadReference
	}

	// session 存在
	sess, err := getSession(ctx, tx, r.RuntimeID, r.SessionID)
	if err != nil {
		return ErrBadReference
	}

	// session 所属 runtime 的 robot_id 必须与 run.robot_id 一致
	var sessRobotID string
	if err := tx.QueryRowContext(ctx,
		`SELECT robot_id FROM runtimes WHERE id = ?`, sess.RuntimeID).Scan(&sessRobotID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBadReference
		}
		return err
	}
	if sessRobotID != r.RobotID {
		return fmt.Errorf("run robot_id %s does not match session runtime robot_id %s: %w",
			r.RobotID, sessRobotID, ErrRobotMismatch)
	}

	// 序列化 artifact_ref
	artJSON := "null"
	if r.ArtifactRef != nil {
		b, err := json.Marshal(r.ArtifactRef)
		if err != nil {
			return fmt.Errorf("marshal artifact_ref: %w", err)
		}
		artJSON = string(b)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO runs_v2 (id, task_id, robot_id, runtime_id, session_id, started_ms, ended_ms, result, artifact_ref)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.TaskID, r.RobotID, r.RuntimeID, r.SessionID,
		r.StartedMs, nullInt(r.EndedMs), r.Result, artJSON)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrConflict
		}
		return err
	}
	return tx.Commit()
}

// GetRunV2 查询 v2 Run 详情。
func (s *Store) GetRunV2(ctx context.Context, id string) (*domain.RunV2, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, task_id, robot_id, runtime_id, session_id, started_ms, ended_ms, result, artifact_ref
		 FROM runs_v2 WHERE id = ?`, id)
	var r domain.RunV2
	var ended sql.NullInt64
	var artJSON string
	if err := row.Scan(&r.ID, &r.TaskID, &r.RobotID, &r.RuntimeID, &r.SessionID,
		&r.StartedMs, &ended, &r.Result, &artJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if ended.Valid {
		r.EndedMs = ended.Int64
	}
	if artJSON != "" && artJSON != "null" {
		var ar domain.ArtifactRef
		if err := json.Unmarshal([]byte(artJSON), &ar); err == nil {
			r.ArtifactRef = &ar
		}
	}
	return &r, nil
}

// ListRunsV2 返回 v2 Run 列表。robotID 为空返回全部。
func (s *Store) ListRunsV2(ctx context.Context, robotID string) ([]domain.RunV2, error) {
	q := `SELECT id, task_id, robot_id, runtime_id, session_id, started_ms, ended_ms, result, artifact_ref FROM runs_v2`
	var args []any
	if robotID != "" {
		q += ` WHERE robot_id = ?`
		args = append(args, robotID)
	}
	q += ` ORDER BY started_ms DESC`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.RunV2
	for rows.Next() {
		var r domain.RunV2
		var ended sql.NullInt64
		var artJSON string
		if err := rows.Scan(&r.ID, &r.TaskID, &r.RobotID, &r.RuntimeID, &r.SessionID,
			&r.StartedMs, &ended, &r.Result, &artJSON); err != nil {
			return nil, err
		}
		if ended.Valid {
			r.EndedMs = ended.Int64
		}
		if artJSON != "" && artJSON != "null" {
			var ar domain.ArtifactRef
			if err := json.Unmarshal([]byte(artJSON), &ar); err == nil {
				r.ArtifactRef = &ar
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// 内部辅助
// ---------------------------------------------------------------------------

// checkExists 检查表中指定 id 的行是否存在。
func checkExists(ctx context.Context, tx *sql.Tx, table, id string) error {
	var one int
	if err := tx.QueryRowContext(ctx,
		fmt.Sprintf("SELECT 1 FROM %s WHERE id = ?", table), id).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// getDeviceV2 在事务内查询 Device。
func getDeviceV2(ctx context.Context, tx *sql.Tx, id string) (*domain.DeviceV2, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT id, robot_id, parent_device_id, display_name, device_class,
		 domain_type, manufacturer, model, serial_number, lifecycle_state, created_at, updated_at
		 FROM devices_v2 WHERE id = ?`, id)
	var d domain.DeviceV2
	var parentID sql.NullString
	if err := row.Scan(&d.ID, &d.RobotID, &parentID, &d.DisplayName, &d.DeviceClass,
		&d.DomainType, &d.Manufacturer, &d.Model, &d.SerialNumber,
		&d.LifecycleState, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if parentID.Valid {
		d.ParentDeviceID = parentID.String
	}
	refs, err := loadExternalRefs(ctx, tx, deviceExternalRefs, d.ID)
	if err != nil {
		return nil, err
	}
	d.ExternalRefs = refs
	return &d, nil
}

// checkNoCycle 沿 parent 链向上走，如果遇到 childID 则返回 ErrBadReference(无环)。
// 此处用简单循环(最大链长有限)，不实现完整图算法。
func checkNoCycle(ctx context.Context, tx *sql.Tx, parentID, childID string) error {
	current := parentID
	visited := map[string]bool{}
	for current != "" {
		if current == childID {
			return fmt.Errorf("containment cycle detected: %s: %w", childID, ErrBadReference)
		}
		if visited[current] {
			return fmt.Errorf("existing containment cycle encountered at %s: %w", current, ErrBadReference)
		}
		visited[current] = true

		var next sql.NullString
		if err := tx.QueryRowContext(ctx,
			`SELECT parent_device_id FROM devices_v2 WHERE id = ?`, current).Scan(&next); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("containment parent chain is broken at %s: %w", current, ErrBadReference)
			}
			return err
		}
		if next.Valid {
			current = next.String
		} else {
			current = ""
		}
	}
	return nil
}

// getSession 在事务内查询 session。
func getSession(ctx context.Context, tx *sql.Tx, runtimeID, sessionID string) (*domain.RuntimeSession, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT runtime_id, session_id, software_version_ref, started_at_reported, started_at_received,
		 ended_at_reported, ended_at_received, session_state, last_heartbeat_at_ms
		 FROM runtime_sessions WHERE runtime_id = ? AND session_id = ?`, runtimeID, sessionID)
	return scanSession(row)
}

// getSessionDirect 在 db 上查询 session(非事务内)。
func getSessionDirect(ctx context.Context, db *sql.DB, runtimeID, sessionID string) (*domain.RuntimeSession, error) {
	row := db.QueryRowContext(ctx,
		`SELECT runtime_id, session_id, software_version_ref, started_at_reported, started_at_received,
		 ended_at_reported, ended_at_received, session_state, last_heartbeat_at_ms
		 FROM runtime_sessions WHERE runtime_id = ? AND session_id = ?`, runtimeID, sessionID)
	return scanSession(row)
}

// scanSession 从 scanner 读取 RuntimeSession 行。
func scanSession(row interface{ Scan(dest ...any) error }) (*domain.RuntimeSession, error) {
	var s domain.RuntimeSession
	var startedReported, endedReported, endedReceived sql.NullInt64
	if err := row.Scan(&s.RuntimeID, &s.SessionID, &s.SoftwareVersionRef,
		&startedReported, &s.StartedAtReceived,
		&endedReported, &endedReceived, &s.SessionState, &s.LastHeartbeatAtMs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if startedReported.Valid {
		s.StartedAtReported = startedReported.Int64
	}
	if endedReported.Valid {
		s.EndedAtReported = endedReported.Int64
	}
	if endedReceived.Valid {
		s.EndedAtReceived = endedReceived.Int64
	}
	return &s, nil
}

// ---------------------------------------------------------------------------
// D2 identity / ExternalRef validation
// ---------------------------------------------------------------------------

type externalRefTarget struct {
	table       string
	ownerColumn string
}

var (
	robotExternalRefs   = externalRefTarget{table: "robot_external_refs", ownerColumn: "robot_id"}
	deviceExternalRefs  = externalRefTarget{table: "device_external_refs_v2", ownerColumn: "device_id"}
	runtimeExternalRefs = externalRefTarget{table: "runtime_external_refs", ownerColumn: "runtime_id"}
)

type rowsQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func validateRobotRecord(r *domain.Robot) (domain.ExternalRefs, error) {
	if r == nil {
		return nil, fmt.Errorf("robot is required: %w", ErrInvalidArgument)
	}
	if err := ValidateCanonicalID(r.ID, RobotIDPrefix); err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.DisplayName) == "" || strings.TrimSpace(r.Domain) == "" {
		return nil, fmt.Errorf("robot display_name and domain are required: %w", ErrInvalidArgument)
	}
	if r.Embodiment != "physical" && r.Embodiment != "simulation" {
		return nil, fmt.Errorf("invalid robot embodiment %q: %w", r.Embodiment, ErrInvalidArgument)
	}
	if r.LifecycleState != domain.RobotActive && r.LifecycleState != domain.RobotRetired {
		return nil, fmt.Errorf("invalid robot lifecycle_state %q: %w", r.LifecycleState, ErrInvalidArgument)
	}
	if r.CreatedAt <= 0 || r.UpdatedAt < r.CreatedAt {
		return nil, fmt.Errorf("invalid robot timestamps: %w", ErrInvalidArgument)
	}
	return normalizeExternalRefs(r.ExternalRefs)
}

func validateDeviceRecord(d *domain.DeviceV2) (domain.ExternalRefs, error) {
	if d == nil {
		return nil, fmt.Errorf("device is required: %w", ErrInvalidArgument)
	}
	if err := ValidateCanonicalID(d.ID, DeviceIDPrefix); err != nil {
		return nil, err
	}
	if err := ValidateCanonicalID(d.RobotID, RobotIDPrefix); err != nil {
		return nil, err
	}
	if d.ParentDeviceID != "" {
		if err := ValidateCanonicalID(d.ParentDeviceID, DeviceIDPrefix); err != nil {
			return nil, err
		}
		if d.ParentDeviceID == d.ID {
			return nil, fmt.Errorf("device cannot contain itself: %w", ErrInvalidArgument)
		}
	}
	if strings.TrimSpace(d.DisplayName) == "" || !validDeviceClass(d.DeviceClass) {
		return nil, fmt.Errorf("invalid device display_name or class %q: %w", d.DeviceClass, ErrInvalidArgument)
	}
	if d.LifecycleState != string(domain.RobotActive) && d.LifecycleState != string(domain.RobotRetired) {
		return nil, fmt.Errorf("invalid device lifecycle_state %q: %w", d.LifecycleState, ErrInvalidArgument)
	}
	if d.CreatedAt <= 0 || d.UpdatedAt < d.CreatedAt {
		return nil, fmt.Errorf("invalid device timestamps: %w", ErrInvalidArgument)
	}
	return normalizeExternalRefs(d.ExternalRefs)
}

func validateRuntimeRecord(rt *domain.Runtime) (domain.ExternalRefs, error) {
	if rt == nil {
		return nil, fmt.Errorf("runtime is required: %w", ErrInvalidArgument)
	}
	if err := ValidateCanonicalID(rt.ID, RuntimeIDPrefix); err != nil {
		return nil, err
	}
	if err := ValidateCanonicalID(rt.RobotID, RobotIDPrefix); err != nil {
		return nil, err
	}
	if rt.HostDeviceID != "" {
		if err := ValidateCanonicalID(rt.HostDeviceID, DeviceIDPrefix); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(rt.DisplayName) == "" || strings.TrimSpace(rt.Component) == "" || !validRuntimeRole(rt.RuntimeRole) {
		return nil, fmt.Errorf("invalid runtime display_name, component, or role %q: %w", rt.RuntimeRole, ErrInvalidArgument)
	}
	if rt.HeartbeatIntervalMs <= 0 {
		return nil, fmt.Errorf("heartbeat_interval_ms must be positive: %w", ErrInvalidArgument)
	}
	if rt.LifecycleState != string(domain.RobotActive) && rt.LifecycleState != string(domain.RobotRetired) {
		return nil, fmt.Errorf("invalid runtime lifecycle_state %q: %w", rt.LifecycleState, ErrInvalidArgument)
	}
	if rt.CreatedAt <= 0 || rt.UpdatedAt < rt.CreatedAt {
		return nil, fmt.Errorf("invalid runtime timestamps: %w", ErrInvalidArgument)
	}
	return normalizeExternalRefs(rt.ExternalRefs)
}

func validateRuntimeSessionRecord(sess *domain.RuntimeSession) error {
	if sess == nil {
		return fmt.Errorf("runtime session is required: %w", ErrInvalidArgument)
	}
	if err := validateSessionIdentity(sess.RuntimeID, sess.SessionID); err != nil {
		return err
	}
	if strings.TrimSpace(sess.SoftwareVersionRef) == "" || sess.StartedAtReceived <= 0 || sess.StartedAtReported < 0 {
		return fmt.Errorf("invalid runtime session version or timestamps: %w", ErrInvalidArgument)
	}
	return nil
}

func validateRuntimeHeartbeatRecord(hb *domain.RuntimeHeartbeat) error {
	if hb == nil {
		return fmt.Errorf("runtime heartbeat is required: %w", ErrInvalidArgument)
	}
	if err := validateSessionIdentity(hb.RuntimeID, hb.SessionID); err != nil {
		return err
	}
	if hb.Seq <= 0 || hb.ReceivedAt <= 0 || hb.ReportedAt < 0 {
		return fmt.Errorf("invalid heartbeat seq or timestamps: %w", ErrInvalidArgument)
	}
	return nil
}

func validateSessionIdentity(runtimeID, sessionID string) error {
	if err := ValidateCanonicalID(runtimeID, RuntimeIDPrefix); err != nil {
		return err
	}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(sessionID) != sessionID {
		return fmt.Errorf("invalid session_id: %w", ErrInvalidArgument)
	}
	return nil
}

func validateRunRecord(r *domain.RunV2) error {
	if r == nil {
		return fmt.Errorf("run is required: %w", ErrInvalidArgument)
	}
	if err := ValidateCanonicalID(r.ID, RunIDPrefix); err != nil {
		return err
	}
	if err := ValidateCanonicalID(r.RobotID, RobotIDPrefix); err != nil {
		return err
	}
	if err := validateSessionIdentity(r.RuntimeID, r.SessionID); err != nil {
		return err
	}
	if strings.TrimSpace(r.TaskID) == "" || r.StartedMs <= 0 || (r.EndedMs > 0 && r.EndedMs < r.StartedMs) {
		return fmt.Errorf("invalid run task or timestamps: %w", ErrInvalidArgument)
	}
	return nil
}

func validDeviceClass(class domain.DeviceClass) bool {
	switch class {
	case domain.DeviceCompute, domain.DeviceController, domain.DeviceSensor,
		domain.DeviceActuator, domain.DeviceBusNode, domain.DeviceComposite:
		return true
	default:
		return false
	}
}

func validRuntimeRole(role domain.RuntimeRole) bool {
	switch role {
	case domain.RuntimeControlRuntime, domain.RuntimeDomainExecutor,
		domain.RuntimeDeviceBridge, domain.RuntimeReplayExecutor:
		return true
	default:
		return false
	}
}

func normalizeExternalRefs(refs domain.ExternalRefs) (domain.ExternalRefs, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(refs))
	out := make(domain.ExternalRefs, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.Namespace) == "" || strings.TrimSpace(ref.Value) == "" ||
			strings.TrimSpace(ref.Namespace) != ref.Namespace || strings.TrimSpace(ref.Value) != ref.Value {
			return nil, fmt.Errorf("external ref namespace and value must be non-empty and trimmed: %w", ErrInvalidArgument)
		}
		key := ref.Namespace + "\x00" + ref.Value
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	return out, nil
}

func insertExternalRefs(ctx context.Context, tx *sql.Tx, target externalRefTarget, ownerID string, refs domain.ExternalRefs) error {
	query := fmt.Sprintf(`INSERT INTO %s (%s, namespace, value) VALUES (?, ?, ?)`, target.table, target.ownerColumn)
	for _, ref := range refs {
		if _, err := tx.ExecContext(ctx, query, ownerID, ref.Namespace, ref.Value); err != nil {
			return mapWriteConstraint(err)
		}
	}
	return nil
}

func loadExternalRefs(ctx context.Context, queryer rowsQueryer, target externalRefTarget, ownerID string) (domain.ExternalRefs, error) {
	query := fmt.Sprintf(`SELECT namespace, value FROM %s WHERE %s = ? ORDER BY namespace, value`, target.table, target.ownerColumn)
	rows, err := queryer.QueryContext(ctx, query, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var refs domain.ExternalRefs
	for rows.Next() {
		var ref domain.ExternalRef
		if err := rows.Scan(&ref.Namespace, &ref.Value); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return refs, nil
}

func mapWriteConstraint(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "UNIQUE constraint failed"):
		return fmt.Errorf("%v: %w", err, ErrConflict)
	case strings.Contains(message, "FOREIGN KEY constraint failed"):
		return fmt.Errorf("%v: %w", err, ErrBadReference)
	case strings.Contains(message, "CHECK constraint failed"), strings.Contains(message, "NOT NULL constraint failed"):
		return fmt.Errorf("%v: %w", err, ErrInvalidArgument)
	default:
		return err
	}
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
