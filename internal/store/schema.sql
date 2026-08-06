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
