ALTER TABLE events
    ADD COLUMN canonical_encoding TEXT NOT NULL DEFAULT 'plaintext.v0';

ALTER TABLE findings
    ADD COLUMN cited_event_ids_encoding TEXT NOT NULL DEFAULT 'plaintext.v0';

CREATE TABLE IF NOT EXISTS local_store_metadata (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    store_id TEXT NOT NULL UNIQUE,
    key_check BLOB,
    key_check_encoding TEXT,
    plaintext_cleanup_required INTEGER NOT NULL DEFAULT 0
        CHECK (plaintext_cleanup_required IN (0, 1)),
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS finding_event_citations (
    finding_id TEXT NOT NULL REFERENCES findings(finding_id) ON DELETE CASCADE,
    event_id TEXT NOT NULL REFERENCES events(event_id) ON DELETE CASCADE,
    PRIMARY KEY (finding_id, event_id)
);

DROP TRIGGER IF EXISTS events_no_update;
DROP TRIGGER IF EXISTS events_no_delete;

CREATE TRIGGER events_no_update
BEFORE UPDATE ON events
BEGIN
    SELECT RAISE(ABORT, 'canonical events are append-only');
END;

CREATE TRIGGER events_no_delete
BEFORE DELETE ON events
BEGIN
    SELECT RAISE(ABORT, 'canonical events are append-only');
END;

CREATE TRIGGER findings_no_update
BEFORE UPDATE ON findings
BEGIN
    SELECT RAISE(ABORT, 'findings are append-only');
END;

CREATE TRIGGER findings_no_delete
BEFORE DELETE ON findings
BEGIN
    SELECT RAISE(ABORT, 'findings are append-only');
END;
