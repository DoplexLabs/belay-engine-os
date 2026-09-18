CREATE TABLE IF NOT EXISTS issue_summary_metadata (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    cursor_epoch TEXT,
    readiness TEXT NOT NULL DEFAULT 'building'
        CHECK (readiness IN ('building', 'ready', 'failed')),
    build_generation INTEGER NOT NULL DEFAULT 0 CHECK (build_generation >= 0),
    materialized_generation INTEGER NOT NULL DEFAULT 0
        CHECK (materialized_generation >= 0),
    oldest_materialized_generation INTEGER NOT NULL DEFAULT 0
        CHECK (oldest_materialized_generation >= 0),
    updated_at TEXT NOT NULL
);

INSERT OR IGNORE INTO issue_summary_metadata (
    singleton, readiness, build_generation, materialized_generation,
    oldest_materialized_generation, updated_at
) VALUES (1, 'building', 0, 0, 0, '1970-01-01T00:00:00.000000000Z');

CREATE TABLE IF NOT EXISTS issue_projection_generation_times (
    generation INTEGER PRIMARY KEY CHECK (generation >= 0),
    generated_at TEXT NOT NULL
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS issue_summary_revisions (
    summary_revision_id TEXT PRIMARY KEY,
    issue_id TEXT NOT NULL,
    fingerprint_id TEXT NOT NULL,
    fingerprint_version TEXT NOT NULL,
    origin TEXT NOT NULL CHECK (origin IN ('belay', 'numbat')),
    detector_id TEXT NOT NULL,
    detector_version TEXT NOT NULL,
    category TEXT NOT NULL,
    title_code TEXT NOT NULL,
    severity TEXT NOT NULL
        CHECK (severity IN ('info', 'low', 'medium', 'high', 'critical')),
    severity_rank INTEGER NOT NULL CHECK (severity_rank BETWEEN 1 AND 5),
    confidence TEXT NOT NULL CHECK (confidence IN ('low', 'medium', 'high')),
    scope_quality TEXT NOT NULL
        CHECK (scope_quality IN ('resolved', 'lexical', 'unscoped', 'conflict')),
    first_observed_at TEXT NOT NULL,
    last_observed_at TEXT NOT NULL,
    occurrence_count INTEGER NOT NULL CHECK (occurrence_count > 0),
    session_count INTEGER NOT NULL CHECK (session_count > 0),
    repeated INTEGER NOT NULL CHECK (repeated IN (0, 1)),
    analysis_status TEXT NOT NULL
        CHECK (analysis_status IN ('current', 'pending', 'failed', 'truncated')),
    evidence_complete INTEGER NOT NULL CHECK (evidence_complete IN (0, 1)),
    retained_history_only INTEGER NOT NULL
        CHECK (retained_history_only IN (0, 1)),
    experimental INTEGER NOT NULL CHECK (experimental IN (0, 1)),
    visible_from_generation INTEGER NOT NULL CHECK (visible_from_generation >= 0),
    visible_until_generation INTEGER,
    created_at TEXT NOT NULL,
    CHECK (
        visible_until_generation IS NULL OR
        visible_until_generation > visible_from_generation
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS issue_summary_active_idx
    ON issue_summary_revisions(issue_id)
    WHERE visible_until_generation IS NULL;
CREATE INDEX IF NOT EXISTS issue_summary_snapshot_order_idx
    ON issue_summary_revisions(
        visible_from_generation, visible_until_generation,
        severity_rank DESC, repeated DESC, last_observed_at DESC, issue_id
    );
CREATE INDEX IF NOT EXISTS issue_summary_order_snapshot_idx
    ON issue_summary_revisions(
        severity_rank DESC, repeated DESC, last_observed_at DESC, issue_id,
        visible_from_generation, visible_until_generation
    );
CREATE INDEX IF NOT EXISTS issue_summary_filter_idx
    ON issue_summary_revisions(
        category, origin, analysis_status, experimental,
        severity_rank DESC, repeated DESC, last_observed_at DESC, issue_id
    );

CREATE TABLE IF NOT EXISTS issue_summary_harnesses (
    summary_revision_id TEXT NOT NULL
        REFERENCES issue_summary_revisions(summary_revision_id) ON DELETE CASCADE,
    harness TEXT NOT NULL,
    PRIMARY KEY (summary_revision_id, harness)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS issue_summary_harness_lookup_idx
    ON issue_summary_harnesses(harness, summary_revision_id);

CREATE TABLE IF NOT EXISTS issue_summary_sessions (
    summary_revision_id TEXT NOT NULL
        REFERENCES issue_summary_revisions(summary_revision_id) ON DELETE CASCADE,
    session_key TEXT NOT NULL,
    PRIMARY KEY (summary_revision_id, session_key)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS issue_summary_session_lookup_idx
    ON issue_summary_sessions(session_key, summary_revision_id);

CREATE TABLE IF NOT EXISTS issue_analysis_coverage_revisions (
    coverage_revision_id TEXT PRIMARY KEY,
    current_sessions INTEGER NOT NULL CHECK (current_sessions >= 0),
    pending_sessions INTEGER NOT NULL CHECK (pending_sessions >= 0),
    failed_sessions INTEGER NOT NULL CHECK (failed_sessions >= 0),
    truncated_sessions INTEGER NOT NULL CHECK (truncated_sessions >= 0),
    unscoped_sessions INTEGER NOT NULL CHECK (unscoped_sessions >= 0),
    analysis_through TEXT,
    complete INTEGER NOT NULL CHECK (complete IN (0, 1)),
    visible_from_generation INTEGER NOT NULL CHECK (visible_from_generation >= 0),
    visible_until_generation INTEGER,
    created_at TEXT NOT NULL,
    CHECK (
        visible_until_generation IS NULL OR
        visible_until_generation > visible_from_generation
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS issue_analysis_coverage_active_idx
    ON issue_analysis_coverage_revisions((1))
    WHERE visible_until_generation IS NULL;
CREATE INDEX IF NOT EXISTS issue_analysis_coverage_snapshot_idx
    ON issue_analysis_coverage_revisions(
        visible_from_generation, visible_until_generation
    );
