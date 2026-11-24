package localusage

// Partial indexes keep page seeks and bucket scans on start events, including
// existing databases without changing their append-only event schema.
const queryIndexesSQL = `
PRAGMA journal_mode=WAL;
CREATE INDEX IF NOT EXISTS idx_events_start_scope_time
  ON events(scope_id, started_at_ms DESC, call_id DESC, seq) WHERE phase='start';
CREATE INDEX IF NOT EXISTS idx_events_start_scope_model_time
  ON events(scope_id, model, started_at_ms DESC, call_id DESC, seq) WHERE phase='start';
`

// 本地用量 schema：append-only 事件表 + scope 覆盖元数据。
// 设计见 DOC 用量统计/设计说明.md「持久化」节。

const createSchemaSQL = `
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=200;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS meta (
  key TEXT PRIMARY KEY NOT NULL,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS scopes (
  scope_id TEXT PRIMARY KEY NOT NULL,
  account_digest TEXT NOT NULL,
  bridge_digest TEXT NOT NULL,
  tenant_id TEXT NOT NULL,
  collection_started_at_ms INTEGER NOT NULL,
  last_healthy_write_at_ms INTEGER,
  coverage_status TEXT NOT NULL DEFAULT 'ok',
  dirty_run_id TEXT
);

CREATE TABLE IF NOT EXISTS events (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  call_id TEXT NOT NULL,
  phase TEXT NOT NULL CHECK(phase IN ('start','finish')),
  run_id TEXT NOT NULL,
  scope_id TEXT NOT NULL,
  account_digest TEXT NOT NULL,
  bridge_digest TEXT NOT NULL,
  tenant_id TEXT NOT NULL,
  model TEXT NOT NULL,
  call_kind TEXT NOT NULL,
  session_id TEXT NOT NULL DEFAULT '',
  turn_id TEXT NOT NULL DEFAULT '',
  generation_id TEXT NOT NULL DEFAULT '',
  started_at_ms INTEGER NOT NULL,
  finished_at_ms INTEGER,
  duration_ms INTEGER,
  status TEXT,
  input_tokens INTEGER,
  input_tokens_known INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER,
  output_tokens_known INTEGER NOT NULL DEFAULT 0,
  cache_hit_tokens INTEGER,
  cache_hit_tokens_known INTEGER NOT NULL DEFAULT 0,
  cache_miss_tokens INTEGER,
  cache_miss_tokens_known INTEGER NOT NULL DEFAULT 0,
  generated_images INTEGER,
  token_source TEXT NOT NULL DEFAULT 'unavailable',
  error_code TEXT NOT NULL DEFAULT '',
  recorded_at_ms INTEGER NOT NULL,
  UNIQUE(call_id, phase)
);

CREATE INDEX IF NOT EXISTS idx_events_scope_started
  ON events(scope_id, started_at_ms, call_id);
CREATE INDEX IF NOT EXISTS idx_events_scope_model
  ON events(scope_id, model, started_at_ms);
CREATE INDEX IF NOT EXISTS idx_events_call_seq
  ON events(call_id, seq);

-- 有效缓存（供应商上报或本地近似）在 finish 时物化，查询只做普通连接。
CREATE TABLE IF NOT EXISTS cache_estimates (
  call_id TEXT PRIMARY KEY NOT NULL,
  cache_hit_tokens INTEGER,
  cache_miss_tokens INTEGER,
  estimated INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS diagnostics (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  at_ms INTEGER NOT NULL,
  code TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS coverage_gaps (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  scope_id TEXT NOT NULL,
  start_at_ms INTEGER NOT NULL,
  end_at_ms INTEGER NOT NULL,
  reason TEXT NOT NULL
);
`
