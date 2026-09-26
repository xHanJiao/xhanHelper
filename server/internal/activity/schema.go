package activity

import "strings"

// schemaDDL 是 events.db 的全部建表语句，权威定义见
// docs/design/activity-events/附表 B · 表结构与索引.md。
//
// 一次建全，后续派生期不再改表结构；events 表 append-only，永不 ALTER 加派生列。
// 关键字列名（trigger / key）加引号，避免 SQLite 解析歧义。
const schemaDDL = `
CREATE TABLE IF NOT EXISTS events (
  event_id      TEXT PRIMARY KEY,
  type          TEXT NOT NULL,
  occurred_at   TEXT NOT NULL,
  received_at   TEXT NOT NULL,
  actor         TEXT NOT NULL,
  "trigger"     TEXT NOT NULL,
  outcome       TEXT,
  error_type    TEXT,
  error_msg     TEXT,
  parent_event_id TEXT,
  pair_id       TEXT,
  launch_id        TEXT,
  focus_epoch_id   TEXT,
  component_instance_id TEXT,
  wake_epoch_id    TEXT,
  conversation_id  TEXT,
  turn_id          TEXT,
  call_id          TEXT,
  client_instance_id TEXT,
  client_build     TEXT,
  os_version       TEXT,
  attrs         TEXT,
  biz_key       TEXT,
  schema_version INTEGER NOT NULL,
  CHECK (parent_event_id IS NULL OR parent_event_id <> event_id)
);
CREATE INDEX IF NOT EXISTS idx_events_time_type ON events(occurred_at, type);
CREATE INDEX IF NOT EXISTS idx_events_type_time ON events(type, occurred_at);
CREATE UNIQUE INDEX IF NOT EXISTS uq_events_bizkey ON events(type, biz_key) WHERE biz_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS event_refs (
  event_id   TEXT NOT NULL,
  ref_type   TEXT NOT NULL,
  ref_id     TEXT NOT NULL,
  source     TEXT NOT NULL,
  confidence REAL DEFAULT 1.0,
  PRIMARY KEY (event_id, ref_type, ref_id, source)
);
CREATE INDEX IF NOT EXISTS idx_refs_target ON event_refs(ref_type, ref_id, event_id);

CREATE TABLE IF NOT EXISTS event_resources (
  event_id      TEXT NOT NULL,
  resource_kind TEXT NOT NULL,
  resource_id   TEXT NOT NULL,
  raw_value     TEXT,
  PRIMARY KEY (event_id, resource_kind, resource_id)
);
CREATE INDEX IF NOT EXISTS idx_res_event ON event_resources(resource_kind, resource_id, event_id);

CREATE TABLE IF NOT EXISTS resources (
  resource_id   TEXT PRIMARY KEY,
  resource_kind TEXT NOT NULL,
  canonical     TEXT NOT NULL,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS resource_aliases (
  resource_kind TEXT NOT NULL,
  alias         TEXT NOT NULL,
  resource_id   TEXT NOT NULL,
  first_seen    TEXT NOT NULL,
  last_seen     TEXT NOT NULL,
  PRIMARY KEY (resource_kind, alias)
);

CREATE TABLE IF NOT EXISTS interval_closed (
  start_event_id TEXT PRIMARY KEY,
  end_event_id   TEXT,
  end_at         TEXT NOT NULL,
  duration_ms    INTEGER NOT NULL,
  closed_by      TEXT NOT NULL,
  rule_version   INTEGER NOT NULL,
  computed_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  session_id     TEXT PRIMARY KEY,
  level          INTEGER NOT NULL,
  parent_id      TEXT,
  anchor_id      TEXT,
  started_at     TEXT NOT NULL,
  ended_at       TEXT,
  closed_by      TEXT,
  threshold_version INTEGER NOT NULL,
  computed_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_level_time ON sessions(level, started_at);

CREATE TABLE IF NOT EXISTS event_session (
  event_id   TEXT NOT NULL,
  level      INTEGER NOT NULL,
  session_id TEXT NOT NULL,
  PRIMARY KEY (event_id, level)
);
CREATE INDEX IF NOT EXISTS idx_evsess_session ON event_session(session_id, event_id);

CREATE TABLE IF NOT EXISTS day_stats (
  day        TEXT NOT NULL,
  hour       INTEGER NOT NULL,
  source     TEXT NOT NULL,
  type       TEXT NOT NULL,
  actor      TEXT NOT NULL,
  cnt        INTEGER NOT NULL,
  duration_ms INTEGER NOT NULL,
  threshold_version INTEGER NOT NULL,
  PRIMARY KEY (day, hour, source, type, actor)
);

CREATE TABLE IF NOT EXISTS day_health (
  day        TEXT PRIMARY KEY,
  inferred_ratio  REAL,
  fallback_count  INTEGER,
  offline_gaps    TEXT,
  collection_gaps TEXT,
  clock_anomalies INTEGER,
  threshold_version INTEGER NOT NULL,
  computed_at     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS event_edges (
  from_event_id TEXT NOT NULL,
  to_event_id   TEXT NOT NULL,
  relation_type TEXT NOT NULL,
  status        TEXT NOT NULL,
  confidence    REAL,
  source        TEXT,
  threshold_version INTEGER NOT NULL,
  embedding_model_version TEXT,
  created_at    TEXT NOT NULL,
  PRIMARY KEY (from_event_id, to_event_id, relation_type)
);

CREATE TABLE IF NOT EXISTS meta (
  "key"   TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
`

// splitStatements 把 DDL 拆成单条语句（本 schema 无字符串字面量，按分号拆即可）。
func splitStatements(ddl string) []string {
	parts := strings.Split(ddl, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
