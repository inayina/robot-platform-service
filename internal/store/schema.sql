-- robot-platform-service 平台实体建表(信封模型,域对象不入库)
-- 设计依据:ARCHITECTURE_DESIGN.md 第二部分

CREATE TABLE IF NOT EXISTS devices (
    id                    TEXT PRIMARY KEY,
    name                  TEXT NOT NULL,
    kind                  TEXT NOT NULL DEFAULT '',
    version               TEXT NOT NULL DEFAULT '',
    heartbeat_interval_ms INTEGER NOT NULL DEFAULT 5000,
    first_seen_ms         INTEGER NOT NULL,
    last_seen_ms          INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS heartbeats (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id TEXT NOT NULL REFERENCES devices(id),
    seq       INTEGER NOT NULL,
    ts_ms     INTEGER NOT NULL,
    UNIQUE (device_id, seq)
);

CREATE TABLE IF NOT EXISTS tasks (
    id         TEXT PRIMARY KEY,
    domain     TEXT NOT NULL,
    kind       TEXT NOT NULL DEFAULT '',
    target     TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'pending',
    created_ms INTEGER NOT NULL,
    updated_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
    id           TEXT PRIMARY KEY,
    task_id      TEXT NOT NULL REFERENCES tasks(id),
    device_id    TEXT NOT NULL REFERENCES devices(id),
    started_ms   INTEGER NOT NULL,
    ended_ms     INTEGER,
    result       TEXT NOT NULL DEFAULT '',
    artifact_ref TEXT NOT NULL DEFAULT ''
);

-- v1 仅建表,端点 reserved
CREATE TABLE IF NOT EXISTS alerts (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id TEXT NOT NULL REFERENCES devices(id),
    severity  TEXT NOT NULL DEFAULT 'info',
    code      TEXT NOT NULL DEFAULT '',
    message   TEXT NOT NULL DEFAULT '',
    ts_ms     INTEGER NOT NULL,
    acked     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS versions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    component     TEXT NOT NULL,
    repo          TEXT NOT NULL DEFAULT '',
    git_sha       TEXT NOT NULL DEFAULT '',
    released_at_ms INTEGER NOT NULL
);

-- Management Plane v2 D2：Robot/Device/Runtime identity 与持久化不变量。
-- v1 表保持不动；Open() 通过 DSN 对每个 SQLite connection 启用 foreign_keys。
-- ExternalRef 使用独立映射表，使 (object kind, namespace, value) 可由数据库唯一约束。

CREATE TABLE IF NOT EXISTS robots (
    id              TEXT PRIMARY KEY
                        CHECK (substr(id, 1, 4) = 'rob-' AND length(id) > 4),
    display_name    TEXT NOT NULL CHECK (length(trim(display_name)) > 0),
    domain          TEXT NOT NULL CHECK (length(trim(domain)) > 0),
    embodiment      TEXT NOT NULL CHECK (embodiment IN ('physical', 'simulation')),
    lifecycle_state TEXT NOT NULL DEFAULT 'active'
                        CHECK (lifecycle_state IN ('active', 'retired')),
    created_at      INTEGER NOT NULL CHECK (created_at > 0),
    updated_at      INTEGER NOT NULL CHECK (updated_at >= created_at)
);

CREATE TABLE IF NOT EXISTS robot_external_refs (
    robot_id  TEXT NOT NULL REFERENCES robots(id) ON DELETE RESTRICT,
    namespace TEXT NOT NULL CHECK (length(trim(namespace)) > 0 AND namespace = trim(namespace)),
    value     TEXT NOT NULL CHECK (length(trim(value)) > 0 AND value = trim(value)),
    PRIMARY KEY (namespace, value),
    UNIQUE (robot_id, namespace, value)
);
CREATE INDEX IF NOT EXISTS idx_robot_external_refs_robot_id
    ON robot_external_refs(robot_id);

CREATE TRIGGER IF NOT EXISTS trg_robots_identity_immutable
BEFORE UPDATE OF id ON robots
WHEN NEW.id <> OLD.id
BEGIN
    SELECT RAISE(ABORT, 'robot identity is immutable');
END;

CREATE TRIGGER IF NOT EXISTS trg_robots_no_hard_delete
BEFORE DELETE ON robots
BEGIN
    SELECT RAISE(ABORT, 'robot identity must be retired, not deleted');
END;

CREATE TABLE IF NOT EXISTS devices_v2 (
    id               TEXT PRIMARY KEY
                         CHECK (substr(id, 1, 4) = 'dev-' AND length(id) > 4),
    robot_id         TEXT NOT NULL REFERENCES robots(id),
    parent_device_id TEXT REFERENCES devices_v2(id),
    display_name     TEXT NOT NULL CHECK (length(trim(display_name)) > 0),
    device_class     TEXT NOT NULL
                         CHECK (device_class IN ('compute', 'controller', 'sensor', 'actuator', 'bus_node', 'composite')),
    domain_type      TEXT NOT NULL DEFAULT '',
    manufacturer     TEXT NOT NULL DEFAULT '',
    model            TEXT NOT NULL DEFAULT '',
    serial_number    TEXT NOT NULL DEFAULT '',
    lifecycle_state  TEXT NOT NULL DEFAULT 'active'
                         CHECK (lifecycle_state IN ('active', 'retired')),
    created_at       INTEGER NOT NULL CHECK (created_at > 0),
    updated_at       INTEGER NOT NULL CHECK (updated_at >= created_at),
    CHECK (parent_device_id IS NULL OR parent_device_id <> id)
);
CREATE INDEX IF NOT EXISTS idx_devices_v2_robot_id ON devices_v2(robot_id);

CREATE TABLE IF NOT EXISTS device_external_refs_v2 (
    device_id  TEXT NOT NULL REFERENCES devices_v2(id) ON DELETE RESTRICT,
    namespace  TEXT NOT NULL CHECK (length(trim(namespace)) > 0 AND namespace = trim(namespace)),
    value      TEXT NOT NULL CHECK (length(trim(value)) > 0 AND value = trim(value)),
    PRIMARY KEY (namespace, value),
    UNIQUE (device_id, namespace, value)
);
CREATE INDEX IF NOT EXISTS idx_device_external_refs_v2_device_id
    ON device_external_refs_v2(device_id);

CREATE TRIGGER IF NOT EXISTS trg_devices_v2_identity_immutable
BEFORE UPDATE OF id, robot_id ON devices_v2
WHEN NEW.id <> OLD.id OR NEW.robot_id <> OLD.robot_id
BEGIN
    SELECT RAISE(ABORT, 'device identity and robot ownership are immutable');
END;

CREATE TRIGGER IF NOT EXISTS trg_devices_v2_no_hard_delete
BEFORE DELETE ON devices_v2
BEGIN
    SELECT RAISE(ABORT, 'device identity must be retired, not deleted');
END;

-- parent 必须属于同一 Robot。FK 负责 parent existence；trigger 负责 owner equality。
CREATE TRIGGER IF NOT EXISTS trg_devices_v2_parent_same_robot_insert
BEFORE INSERT ON devices_v2
WHEN NEW.parent_device_id IS NOT NULL
 AND NOT EXISTS (
     SELECT 1 FROM devices_v2 parent
      WHERE parent.id = NEW.parent_device_id AND parent.robot_id = NEW.robot_id
 )
BEGIN
    SELECT RAISE(ABORT, 'parent device must belong to the same robot');
END;

CREATE TRIGGER IF NOT EXISTS trg_devices_v2_parent_same_robot_update
BEFORE UPDATE OF parent_device_id, robot_id ON devices_v2
WHEN NEW.parent_device_id IS NOT NULL
 AND NOT EXISTS (
     SELECT 1 FROM devices_v2 parent
      WHERE parent.id = NEW.parent_device_id AND parent.robot_id = NEW.robot_id
 )
BEGIN
    SELECT RAISE(ABORT, 'parent device must belong to the same robot');
END;

CREATE TRIGGER IF NOT EXISTS trg_devices_v2_no_cycle_update
BEFORE UPDATE OF parent_device_id ON devices_v2
WHEN NEW.parent_device_id IS NOT NULL
 AND EXISTS (
     WITH RECURSIVE ancestors(id, parent_device_id) AS (
         SELECT id, parent_device_id FROM devices_v2 WHERE id = NEW.parent_device_id
         UNION
         SELECT parent.id, parent.parent_device_id
           FROM devices_v2 parent
           JOIN ancestors child ON parent.id = child.parent_device_id
          WHERE child.parent_device_id IS NOT NULL
     )
     SELECT 1 FROM ancestors WHERE id = NEW.id
 )
BEGIN
    SELECT RAISE(ABORT, 'device containment cycle');
END;

CREATE TABLE IF NOT EXISTS runtimes (
    id                    TEXT PRIMARY KEY
                              CHECK (substr(id, 1, 3) = 'rt-' AND length(id) > 3),
    robot_id              TEXT NOT NULL REFERENCES robots(id),
    display_name          TEXT NOT NULL CHECK (length(trim(display_name)) > 0),
    runtime_role          TEXT NOT NULL
                              CHECK (runtime_role IN ('control_runtime', 'domain_executor', 'device_bridge', 'replay_executor')),
    component             TEXT NOT NULL CHECK (length(trim(component)) > 0),
    host_device_id        TEXT REFERENCES devices_v2(id),
    heartbeat_interval_ms INTEGER NOT NULL DEFAULT 5000 CHECK (heartbeat_interval_ms > 0),
    lifecycle_state       TEXT NOT NULL DEFAULT 'active'
                              CHECK (lifecycle_state IN ('active', 'retired')),
    created_at            INTEGER NOT NULL CHECK (created_at > 0),
    updated_at            INTEGER NOT NULL CHECK (updated_at >= created_at)
);
CREATE INDEX IF NOT EXISTS idx_runtimes_robot_id ON runtimes(robot_id);

CREATE TABLE IF NOT EXISTS runtime_external_refs (
    runtime_id TEXT NOT NULL REFERENCES runtimes(id) ON DELETE RESTRICT,
    namespace  TEXT NOT NULL CHECK (length(trim(namespace)) > 0 AND namespace = trim(namespace)),
    value      TEXT NOT NULL CHECK (length(trim(value)) > 0 AND value = trim(value)),
    PRIMARY KEY (namespace, value),
    UNIQUE (runtime_id, namespace, value)
);
CREATE INDEX IF NOT EXISTS idx_runtime_external_refs_runtime_id
    ON runtime_external_refs(runtime_id);

CREATE TRIGGER IF NOT EXISTS trg_runtimes_identity_immutable
BEFORE UPDATE OF id, robot_id ON runtimes
WHEN NEW.id <> OLD.id OR NEW.robot_id <> OLD.robot_id
BEGIN
    SELECT RAISE(ABORT, 'runtime identity and robot ownership are immutable');
END;

CREATE TRIGGER IF NOT EXISTS trg_runtimes_no_hard_delete
BEFORE DELETE ON runtimes
BEGIN
    SELECT RAISE(ABORT, 'runtime identity must be retired, not deleted');
END;

CREATE TRIGGER IF NOT EXISTS trg_runtimes_host_same_robot_insert
BEFORE INSERT ON runtimes
WHEN NEW.host_device_id IS NOT NULL
 AND NOT EXISTS (
     SELECT 1 FROM devices_v2 host
      WHERE host.id = NEW.host_device_id AND host.robot_id = NEW.robot_id
 )
BEGIN
    SELECT RAISE(ABORT, 'host device must belong to the same robot');
END;

CREATE TRIGGER IF NOT EXISTS trg_runtimes_host_same_robot_update
BEFORE UPDATE OF host_device_id, robot_id ON runtimes
WHEN NEW.host_device_id IS NOT NULL
 AND NOT EXISTS (
     SELECT 1 FROM devices_v2 host
      WHERE host.id = NEW.host_device_id AND host.robot_id = NEW.robot_id
 )
BEGIN
    SELECT RAISE(ABORT, 'host device must belong to the same robot');
END;

CREATE TABLE IF NOT EXISTS runtime_sessions (
    runtime_id            TEXT NOT NULL REFERENCES runtimes(id),
    session_id            TEXT NOT NULL CHECK (length(trim(session_id)) > 0),
    software_version_ref  TEXT NOT NULL DEFAULT 'unknown'
                              CHECK (length(trim(software_version_ref)) > 0),
    started_at_reported   INTEGER,
    started_at_received   INTEGER NOT NULL CHECK (started_at_received > 0),
    ended_at_reported     INTEGER,
    ended_at_received     INTEGER,
    session_state         TEXT NOT NULL DEFAULT 'current'
                              CHECK (session_state IN ('current', 'ended', 'superseded')),
    last_heartbeat_at_ms  INTEGER NOT NULL DEFAULT 0 CHECK (last_heartbeat_at_ms >= 0),
    PRIMARY KEY (runtime_id, session_id),
    CHECK (started_at_reported IS NULL OR started_at_reported > 0),
    CHECK (ended_at_reported IS NULL OR ended_at_reported > 0),
    CHECK (ended_at_received IS NULL OR ended_at_received >= started_at_received)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_runtime_sessions_one_current
    ON runtime_sessions(runtime_id) WHERE session_state = 'current';

CREATE TRIGGER IF NOT EXISTS trg_runtime_sessions_identity_immutable
BEFORE UPDATE OF runtime_id, session_id, software_version_ref, started_at_received ON runtime_sessions
WHEN NEW.runtime_id <> OLD.runtime_id
  OR NEW.session_id <> OLD.session_id
  OR NEW.software_version_ref <> OLD.software_version_ref
  OR NEW.started_at_received <> OLD.started_at_received
BEGIN
    SELECT RAISE(ABORT, 'runtime session identity and version binding are immutable');
END;

CREATE TRIGGER IF NOT EXISTS trg_runtime_sessions_no_hard_delete
BEFORE DELETE ON runtime_sessions
BEGIN
    SELECT RAISE(ABORT, 'runtime session identity cannot be reused');
END;

CREATE TABLE IF NOT EXISTS runtime_heartbeats (
    runtime_id  TEXT NOT NULL,
    session_id  TEXT NOT NULL,
    seq         INTEGER NOT NULL CHECK (seq > 0),
    reported_at INTEGER,
    received_at INTEGER NOT NULL CHECK (received_at > 0),
    UNIQUE (runtime_id, session_id, seq),
    FOREIGN KEY (runtime_id, session_id)
        REFERENCES runtime_sessions(runtime_id, session_id) ON DELETE RESTRICT,
    CHECK (reported_at IS NULL OR reported_at > 0)
);

CREATE TABLE IF NOT EXISTS runs_v2 (
    id           TEXT PRIMARY KEY
                     CHECK (substr(id, 1, 4) = 'run-' AND length(id) > 4),
    task_id      TEXT NOT NULL REFERENCES tasks(id),
    robot_id     TEXT NOT NULL REFERENCES robots(id),
    runtime_id   TEXT NOT NULL,
    session_id   TEXT NOT NULL,
    started_ms   INTEGER NOT NULL CHECK (started_ms > 0),
    ended_ms     INTEGER,
    result       TEXT NOT NULL DEFAULT '',
    artifact_ref TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (runtime_id, session_id)
        REFERENCES runtime_sessions(runtime_id, session_id) ON DELETE RESTRICT,
    CHECK (ended_ms IS NULL OR ended_ms >= started_ms)
);
CREATE INDEX IF NOT EXISTS idx_runs_v2_robot_id ON runs_v2(robot_id);

CREATE TRIGGER IF NOT EXISTS trg_runs_v2_correlation_immutable
BEFORE UPDATE OF id, task_id, robot_id, runtime_id, session_id, started_ms ON runs_v2
WHEN NEW.id <> OLD.id
  OR NEW.task_id <> OLD.task_id
  OR NEW.robot_id <> OLD.robot_id
  OR NEW.runtime_id <> OLD.runtime_id
  OR NEW.session_id <> OLD.session_id
  OR NEW.started_ms <> OLD.started_ms
BEGIN
    SELECT RAISE(ABORT, 'run identity and execution correlation are immutable');
END;

CREATE TRIGGER IF NOT EXISTS trg_runs_v2_no_hard_delete
BEFORE DELETE ON runs_v2
BEGIN
    SELECT RAISE(ABORT, 'run ledger identity cannot be reused');
END;

CREATE TRIGGER IF NOT EXISTS trg_runs_v2_robot_session_match_insert
BEFORE INSERT ON runs_v2
WHEN NOT EXISTS (
    SELECT 1
      FROM runtime_sessions session
      JOIN runtimes runtime ON runtime.id = session.runtime_id
     WHERE session.runtime_id = NEW.runtime_id
       AND session.session_id = NEW.session_id
       AND runtime.robot_id = NEW.robot_id
)
BEGIN
    SELECT RAISE(ABORT, 'run robot must match runtime session robot');
END;

PRAGMA user_version = 2;
