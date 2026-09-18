CREATE TABLE IF NOT EXISTS event_read_order (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE
        REFERENCES events(event_id) ON DELETE CASCADE
);

INSERT OR IGNORE INTO event_read_order (event_id)
SELECT event_id
FROM events
ORDER BY rowid;

CREATE TABLE IF NOT EXISTS finding_read_order (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    finding_id TEXT NOT NULL UNIQUE
        REFERENCES findings(finding_id) ON DELETE CASCADE
);

INSERT OR IGNORE INTO finding_read_order (finding_id)
SELECT finding_id
FROM findings
ORDER BY rowid;

CREATE INDEX IF NOT EXISTS events_session_filter_idx
    ON events(session_key, occurred_at, source_agent, outcome, historical);

CREATE INDEX IF NOT EXISTS events_activity_filter_idx
    ON events(occurred_at, source_sequence, event_id, source_agent, outcome);

CREATE INDEX IF NOT EXISTS findings_read_filter_idx
    ON findings(detected_at, finding_id, severity);
