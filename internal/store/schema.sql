-- robot-platform-service 平台实体建表(信封模型,域对象不入库)
-- 设计依据:ARCHITECTURE_DESIGN.md 第二部分
-- 2026-08-06: heartbeats 表加 session_id / metrics(Edge Agent 集成)

CREATE TABLE IF NOT EXISTS devices (
    id                    TEXT PRIMARY KEY,
    name                  TEXT NOT NULL,
    kind                  TEXT NOT NULL DEFAULT '',
    version               TEXT NOT NULL DEFAULT '',
    hostname              TEXT NOT NULL DEFAULT '',
    arch                  TEXT NOT NULL DEFAULT '',
    os                    TEXT NOT NULL DEFAULT '',
    heartbeat_interval_ms INTEGER NOT NULL DEFAULT 5000,
    first_seen_ms         INTEGER NOT NULL,
    last_seen_ms          INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS heartbeats (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id  TEXT NOT NULL REFERENCES devices(id),
    seq        INTEGER NOT NULL,
    ts_ms      INTEGER NOT NULL,
    session_id TEXT NOT NULL DEFAULT '',
    metrics    TEXT NOT NULL DEFAULT '',
    UNIQUE (device_id, seq)
);

-- 存量数据库迁移(新库自动跳过)
ALTER TABLE heartbeats ADD COLUMN session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE heartbeats ADD COLUMN metrics TEXT NOT NULL DEFAULT '';

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
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    component      TEXT NOT NULL,
    repo           TEXT NOT NULL DEFAULT '',
    git_sha        TEXT NOT NULL DEFAULT '',
    released_at_ms INTEGER NOT NULL
);

-- V2 表(docs/ROBOT_DEVICE_RUNTIME_CONTRACT.md)
CREATE TABLE IF NOT EXISTS robots (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL DEFAULT '',
    domain TEXT NOT NULL DEFAULT '',
    embodiment TEXT NOT NULL DEFAULT 'simulation',
    lifecycle_state TEXT NOT NULL DEFAULT 'active',
    external_refs_json TEXT NOT NULL DEFAULT '[]',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS devices_v2 (
    id TEXT PRIMARY KEY,
    robot_id TEXT NOT NULL REFERENCES robots(id),
    parent_device_id TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL DEFAULT '',
    device_class TEXT NOT NULL DEFAULT '',
    domain_type TEXT NOT NULL DEFAULT '',
    manufacturer TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    serial_number TEXT NOT NULL DEFAULT '',
    lifecycle_state TEXT NOT NULL DEFAULT 'active',
    external_refs_json TEXT NOT NULL DEFAULT '[]',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS runtimes (
    id TEXT PRIMARY KEY,
    robot_id TEXT NOT NULL REFERENCES robots(id),
    host_device_id TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL DEFAULT '',
    runtime_role TEXT NOT NULL DEFAULT '',
    component TEXT NOT NULL DEFAULT '',
    heartbeat_interval_ms INTEGER NOT NULL DEFAULT 5000,
    lifecycle_state TEXT NOT NULL DEFAULT 'active',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS runtime_sessions (
    session_id TEXT NOT NULL,
    runtime_id TEXT NOT NULL REFERENCES runtimes(id),
    software_version_ref TEXT NOT NULL DEFAULT 'unknown',
    session_state TEXT NOT NULL DEFAULT 'current',
    started_at_reported INTEGER NOT NULL DEFAULT 0,
    started_at_received INTEGER NOT NULL,
    last_heartbeat_at_ms INTEGER NOT NULL DEFAULT 0,
    ended_at INTEGER,
    PRIMARY KEY (runtime_id, session_id)
);

CREATE TABLE IF NOT EXISTS runtime_heartbeats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    runtime_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    reported_at INTEGER NOT NULL,
    received_at INTEGER NOT NULL,
    UNIQUE(runtime_id, session_id, seq, reported_at)
);

CREATE TABLE IF NOT EXISTS external_id_mappings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    object_kind TEXT NOT NULL,
    object_id TEXT NOT NULL,
    namespace TEXT NOT NULL,
    value TEXT NOT NULL,
    UNIQUE(object_kind, object_id, namespace, value)
);

CREATE TABLE IF NOT EXISTS runs_v2 (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id),
    robot_id TEXT NOT NULL DEFAULT '',
    device_id TEXT NOT NULL DEFAULT '',
    runtime_id TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    started_at INTEGER NOT NULL,
    ended_at INTEGER,
    result TEXT NOT NULL DEFAULT '',
    artifact_ref_json TEXT NOT NULL DEFAULT '{}'
);

