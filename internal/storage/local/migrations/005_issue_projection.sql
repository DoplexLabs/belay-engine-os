CREATE TABLE IF NOT EXISTS session_scopes (
    session_key TEXT PRIMARY KEY,
    project_scope_id TEXT,
    normalization_version TEXT NOT NULL,
    scope_quality TEXT NOT NULL
        CHECK (scope_quality IN ('resolved', 'lexical', 'unscoped', 'conflict')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS event_enrichments (
    event_id TEXT PRIMARY KEY REFERENCES events(event_id) ON DELETE CASCADE,
    command_signature_id TEXT,
    command_class TEXT NOT NULL,
    permission_class TEXT NOT NULL,
    enrichment_version TEXT NOT NULL,
    enrichment_payload BLOB NOT NULL,
    enrichment_encoding TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS dirty_sessions (
    session_key TEXT PRIMARY KEY,
    target_generation INTEGER NOT NULL CHECK (target_generation > 0),
    dirty_reason TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'claimed', 'failed', 'current', 'truncated')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    retry_at TEXT,
    claimed_at TEXT,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS dirty_sessions_work_idx
    ON dirty_sessions(state, retry_at, updated_at, session_key);

CREATE TABLE IF NOT EXISTS issue_projection_metadata (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    current_generation INTEGER NOT NULL DEFAULT 0 CHECK (current_generation >= 0),
    oldest_retained_generation INTEGER NOT NULL DEFAULT 0
        CHECK (oldest_retained_generation >= 0),
    last_successful_analysis_at TEXT
);

INSERT OR IGNORE INTO issue_projection_metadata (
    singleton, current_generation, oldest_retained_generation
) VALUES (1, 0, 0);

CREATE TABLE IF NOT EXISTS session_analysis_revisions (
    revision_id TEXT PRIMARY KEY,
    session_key TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('current', 'pending', 'failed', 'truncated')),
    scope_quality TEXT NOT NULL
        CHECK (scope_quality IN ('resolved', 'lexical', 'unscoped', 'conflict')),
    target_generation INTEGER NOT NULL CHECK (target_generation > 0),
    analyzed_generation INTEGER NOT NULL CHECK (analyzed_generation >= 0),
    error_code TEXT,
    visible_from_generation INTEGER NOT NULL CHECK (visible_from_generation > 0),
    visible_until_generation INTEGER,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (
        visible_until_generation IS NULL OR
        visible_until_generation > visible_from_generation
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS session_analysis_active_idx
    ON session_analysis_revisions(session_key)
    WHERE visible_until_generation IS NULL;
CREATE INDEX IF NOT EXISTS session_analysis_snapshot_idx
    ON session_analysis_revisions(
        visible_from_generation, visible_until_generation, status, session_key
    );

CREATE TABLE IF NOT EXISTS issue_occurrences (
    revision_id TEXT PRIMARY KEY,
    occurrence_id TEXT NOT NULL,
    issue_id TEXT NOT NULL,
    fingerprint_id TEXT NOT NULL,
    fingerprint_version TEXT NOT NULL,
    origin TEXT NOT NULL CHECK (origin IN ('belay', 'numbat')),
    origin_record_id TEXT,
    session_key TEXT NOT NULL,
    harness TEXT NOT NULL,
    detector_id TEXT NOT NULL,
    detector_version TEXT NOT NULL,
    projection_version TEXT NOT NULL,
    category TEXT NOT NULL,
    title_code TEXT NOT NULL,
    severity TEXT NOT NULL CHECK (severity IN ('info', 'low', 'medium', 'high', 'critical')),
    confidence TEXT NOT NULL CHECK (confidence IN ('low', 'medium', 'high')),
    scope_quality TEXT NOT NULL
        CHECK (scope_quality IN ('resolved', 'lexical', 'unscoped', 'conflict')),
    first_observed_at TEXT NOT NULL,
    last_observed_at TEXT NOT NULL,
    evidence_complete INTEGER NOT NULL CHECK (evidence_complete IN (0, 1)),
    retained_history_only INTEGER NOT NULL CHECK (retained_history_only IN (0, 1)),
    analysis_status TEXT NOT NULL
        CHECK (analysis_status IN ('current', 'pending', 'failed', 'truncated')),
    analysis_generation INTEGER NOT NULL CHECK (analysis_generation > 0),
    evidence_payload BLOB NOT NULL,
    evidence_encoding TEXT NOT NULL,
    visible_from_generation INTEGER NOT NULL CHECK (visible_from_generation > 0),
    visible_until_generation INTEGER,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (
        visible_until_generation IS NULL OR
        visible_until_generation > visible_from_generation
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS issue_occurrences_active_idx
    ON issue_occurrences(session_key, fingerprint_id, origin)
    WHERE visible_until_generation IS NULL;
CREATE INDEX IF NOT EXISTS issue_occurrences_snapshot_idx
    ON issue_occurrences(
        visible_from_generation, visible_until_generation, issue_id,
        last_observed_at, occurrence_id
    );
CREATE INDEX IF NOT EXISTS issue_occurrences_group_idx
    ON issue_occurrences(fingerprint_id, origin, session_key);

CREATE TABLE IF NOT EXISTS issue_occurrence_events (
    revision_id TEXT NOT NULL
        REFERENCES issue_occurrences(revision_id) ON DELETE CASCADE,
    event_id TEXT NOT NULL REFERENCES events(event_id) ON DELETE CASCADE,
    PRIMARY KEY (revision_id, event_id)
);

CREATE INDEX IF NOT EXISTS issue_occurrence_events_event_idx
    ON issue_occurrence_events(event_id, revision_id);

CREATE TABLE IF NOT EXISTS analysis_diagnostics (
    session_key TEXT NOT NULL,
    detector_id TEXT NOT NULL,
    error_code TEXT NOT NULL,
    occurrence_count INTEGER NOT NULL DEFAULT 1 CHECK (occurrence_count > 0),
    first_observed_at TEXT NOT NULL,
    last_observed_at TEXT NOT NULL,
    PRIMARY KEY (session_key, detector_id, error_code)
);
