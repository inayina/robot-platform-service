// Package store 提供 SQLite 持久化。
//
// 选型说明:modernc.org/sqlite 是纯 Go 驱动(无 CGO),保持"单静态二进制"部署形态,
// 与 robot-control-runtime 的 systemd 部署叙事一致。
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/inayina/robot-platform-service/internal/domain"
)

//go:embed schema.sql
var schemaFS embed.FS

// 领域错误:API 层据此映射 HTTP 状态码。
var (
	ErrNotFound      = errors.New("not found")
	ErrConflict      = errors.New("already exists")
	ErrSeqRegression = errors.New("heartbeat seq regression")
	ErrBadReference  = errors.New("referenced entity does not exist")
)

// Store 封装所有 SQLite 访问。
type Store struct {
	db *sql.DB
}

// Open 打开(或创建)数据库并执行迁移。
func Open(path string) (*Store, error) {
	dsn := path
	if !strings.HasPrefix(dsn, ":memory:") {
		// WAL:读不阻塞写,与"平台常驻 + 上报频繁"的形态匹配。
		dsn = "file:" + path + "?_pragma=journal_mode(WAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭底层连接。
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	schema, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}
	if _, err := s.db.Exec(string(schema)); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// NewID 生成平台内部 ID(客户端未提供时使用;信封原则:id 由平台统辖)。
func NewID(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%x", prefix, b)
}

// CreateDevice 注册设备;id 已存在返回 ErrConflict。
func (s *Store) CreateDevice(ctx context.Context, d *domain.Device) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO devices (id, name, kind, version, heartbeat_interval_ms, first_seen_ms, last_seen_ms)
		 VALUES (?, ?, ?, ?, ?, ?, 0)`,
		d.ID, d.Name, d.Kind, d.Version, d.HeartbeatIntervalMs, d.FirstSeenMs)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrConflict
	}
	return err
}

// GetDevice 查询单个设备(不含状态计算,状态由调用方用 StatusEvaluator 判定)。
func (s *Store) GetDevice(ctx context.Context, id string) (*domain.Device, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, kind, version, heartbeat_interval_ms, first_seen_ms, last_seen_ms
		 FROM devices WHERE id = ?`, id)
	var d domain.Device
	if err := row.Scan(&d.ID, &d.Name, &d.Kind, &d.Version,
		&d.HeartbeatIntervalMs, &d.FirstSeenMs, &d.LastSeenMs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &d, nil
}

// ListDevices 返回全部设备。
func (s *Store) ListDevices(ctx context.Context) ([]domain.Device, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, kind, version, heartbeat_interval_ms, first_seen_ms, last_seen_ms
		 FROM devices ORDER BY first_seen_ms`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Device
	for rows.Next() {
		var d domain.Device
		if err := rows.Scan(&d.ID, &d.Name, &d.Kind, &d.Version,
			&d.HeartbeatIntervalMs, &d.FirstSeenMs, &d.LastSeenMs); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AddHeartbeat 追加心跳。约束:
//   - seq 必须严格递增(拒绝重放/乱序,与主仓命令合同理念一致);
//   - 设备必须存在;
//   - 成功后同步设备 last_seen_ms。
//
// 三步在同一事务内完成,避免并发上报时读到旧最大 seq。
func (s *Store) AddHeartbeat(ctx context.Context, h domain.Heartbeat) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // 提交成功后 Rollback 是 no-op

	var maxSeq sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MAX(seq) FROM heartbeats WHERE device_id = ?`, h.DeviceID).Scan(&maxSeq); err != nil {
		return err
	}
	if maxSeq.Valid && h.Seq <= maxSeq.Int64 {
		return ErrSeqRegression
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO heartbeats (device_id, seq, ts_ms) VALUES (?, ?, ?)`,
		h.DeviceID, h.Seq, h.TsMs); err != nil {
		return err
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE devices SET last_seen_ms = ? WHERE id = ?`, h.TsMs, h.DeviceID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// LastHeartbeat 返回设备最近一次心跳。
func (s *Store) LastHeartbeat(ctx context.Context, deviceID string) (*domain.Heartbeat, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT device_id, seq, ts_ms FROM heartbeats
		 WHERE device_id = ? ORDER BY seq DESC LIMIT 1`, deviceID)
	var h domain.Heartbeat
	if err := row.Scan(&h.DeviceID, &h.Seq, &h.TsMs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &h, nil
}

// CreateTask 创建任务记录;id 已存在返回 ErrConflict。
func (s *Store) CreateTask(ctx context.Context, t *domain.Task) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tasks (id, domain, kind, target, status, created_ms, updated_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Domain, t.Kind, t.Target, t.Status, t.CreatedMs, t.UpdatedMs)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrConflict
	}
	return err
}

// ListTasks 列出任务,可按状态过滤(空串 = 全部)。
func (s *Store) ListTasks(ctx context.Context, status string) ([]domain.Task, error) {
	q := `SELECT id, domain, kind, target, status, created_ms, updated_ms FROM tasks`
	var args []any
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY created_ms DESC`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Task
	for rows.Next() {
		var t domain.Task
		if err := rows.Scan(&t.ID, &t.Domain, &t.Kind, &t.Target,
			&t.Status, &t.CreatedMs, &t.UpdatedMs); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateRun 记录一次运行;校验 task 与 device 必须存在(引用完整性)。
func (s *Store) CreateRun(ctx context.Context, r *domain.Run) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	var one int
	if err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM tasks WHERE id = ?`, r.TaskID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBadReference
		}
		return err
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM devices WHERE id = ?`, r.DeviceID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBadReference
		}
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO runs (id, task_id, device_id, started_ms, ended_ms, result, artifact_ref)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.TaskID, r.DeviceID, r.StartedMs, r.EndedMs, r.Result, r.ArtifactRef); err != nil {
		return err
	}
	return tx.Commit()
}

// GetRun 查询运行详情。
func (s *Store) GetRun(ctx context.Context, id string) (*domain.Run, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, task_id, device_id, started_ms, ended_ms, result, artifact_ref
		 FROM runs WHERE id = ?`, id)
	var r domain.Run
	var ended sql.NullInt64
	if err := row.Scan(&r.ID, &r.TaskID, &r.DeviceID, &r.StartedMs,
		&ended, &r.Result, &r.ArtifactRef); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if ended.Valid {
		r.EndedMs = ended.Int64
	}
	return &r, nil
}
