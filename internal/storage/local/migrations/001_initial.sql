CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
    event_id TEXT PRIMARY KEY,
    source_deduplication_key TEXT NOT NULL UNIQUE,
    schema_version TEXT NOT NULL,
    installation_id TEXT NOT NULL,
    session_key TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    source_sequence INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    outcome TEXT NOT NULL,
    source_agent TEXT NOT NULL,
    source_kind TEXT NOT NULL,
    source_record_id TEXT NOT NULL,
    source_run_id TEXT NOT NULL,
    historical INTEGER NOT NULL CHECK (historical IN (0, 1)),
    canonical_json BLOB NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS events_session_order_idx
    ON events(session_key, source_sequence, occurred_at, event_id);
CREATE INDEX IF NOT EXISTS events_observed_idx
    ON events(observed_at, event_id);

CREATE TRIGGER IF NOT EXISTS events_no_update
BEFORE UPDATE ON events
BEGIN
    SELECT RAISE(ABORT, 'canonical events are append-only');
END;

CREATE TRIGGER IF NOT EXISTS events_no_delete
BEFORE DELETE ON events
BEGIN
    SELECT RAISE(ABORT, 'canonical events are append-only');
END;

CREATE TABLE IF NOT EXISTS findings (
    finding_id TEXT PRIMARY KEY,
    source_run_id TEXT NOT NULL,
    session_key TEXT,
    detected_at TEXT NOT NULL,
    rule_id TEXT NOT NULL,
    rule_version TEXT NOT NULL,
    severity TEXT NOT NULL,
    source_agent TEXT NOT NULL,
    confidence TEXT NOT NULL,
    cited_event_ids_json BLOB NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS import_runs (
    source_run_id TEXT PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'running',
    complete INTEGER NOT NULL DEFAULT 0 CHECK (complete IN (0, 1)),
    artifacts_scanned INTEGER NOT NULL DEFAULT 0,
    events_emitted INTEGER NOT NULL DEFAULT 0,
    findings_emitted INTEGER NOT NULL DEFAULT 0,
    indicators_emitted INTEGER NOT NULL DEFAULT 0,
    diagnostics INTEGER NOT NULL DEFAULT 0,
    first_seen_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS quarantine (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_run_id TEXT,
    line_number INTEGER NOT NULL,
    category TEXT NOT NULL,
    reason TEXT NOT NULL,
    record_sha256 TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS quarantine_run_idx
    ON quarantine(source_run_id, line_number);

CREATE TABLE IF NOT EXISTS diagnostics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_run_id TEXT,
    line_number INTEGER NOT NULL,
    level TEXT NOT NULL,
    code TEXT NOT NULL,
    created_at TEXT NOT NULL
);
