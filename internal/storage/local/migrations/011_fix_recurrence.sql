ALTER TABLE events ADD COLUMN occurred_at_order_ns INTEGER;
ALTER TABLE issue_occurrences ADD COLUMN fingerprint_scope_id TEXT;
ALTER TABLE session_analysis_revisions
    ADD COLUMN analysis_through_order_ns INTEGER;
ALTER TABLE session_analysis_revisions
    ADD COLUMN analyzed_event_generation INTEGER NOT NULL DEFAULT 0;
ALTER TABLE issue_projection_metadata
    ADD COLUMN retention_generation INTEGER NOT NULL DEFAULT 1;

CREATE TABLE IF NOT EXISTS local_migration_progress (
    migration_version INTEGER NOT NULL,
    phase TEXT NOT NULL,
    after_sequence INTEGER NOT NULL DEFAULT 0 CHECK (after_sequence >= 0),
    complete INTEGER NOT NULL DEFAULT 0 CHECK (complete IN (0, 1)),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (migration_version, phase)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS fix_monitoring_metadata (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    readiness TEXT NOT NULL CHECK (
        readiness IN ('catching_up', 'ready', 'failed')
    ),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    retry_at TEXT,
    failure_code TEXT,
    catchup_started_at TEXT,
    catchup_completed_at TEXT,
    updated_at TEXT NOT NULL
);

INSERT OR IGNORE INTO fix_monitoring_metadata (
    singleton, readiness, updated_at
) VALUES (1, 'catching_up', '1970-01-01T00:00:00.000000000Z');

CREATE TABLE IF NOT EXISTS fix_monitoring_subjects (
    annotation_id TEXT PRIMARY KEY
        REFERENCES fix_annotations(annotation_id),
    fingerprint_scope_id TEXT,
    scope_capture_status TEXT NOT NULL CHECK (
        scope_capture_status IN ('captured', 'unavailable')
    ),
    category TEXT,
    title_code TEXT,
    severity TEXT,
    confidence TEXT,
    anchor_harness TEXT,
    origin TEXT NOT NULL CHECK (origin IN ('belay', 'numbat')),
    detector_id TEXT NOT NULL,
    detector_version TEXT NOT NULL,
    fingerprint_version TEXT NOT NULL,
    negative_comparison_mode TEXT NOT NULL CHECK (
        negative_comparison_mode IN ('supported', 'positive_only')
    ),
    monitor_from_order_ns INTEGER,
    captured_at TEXT NOT NULL,
    CHECK (
        (scope_capture_status = 'captured'
            AND fingerprint_scope_id IS NOT NULL
            AND monitor_from_order_ns IS NOT NULL)
        OR
        (scope_capture_status = 'unavailable'
            AND fingerprint_scope_id IS NULL)
    )
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS session_analysis_capabilities (
    revision_id TEXT NOT NULL
        REFERENCES session_analysis_revisions(revision_id) ON DELETE CASCADE,
    session_id TEXT NOT NULL,
    fingerprint_scope_id TEXT NOT NULL,
    origin TEXT NOT NULL CHECK (origin IN ('belay', 'numbat')),
    detector_id TEXT NOT NULL,
    detector_version TEXT NOT NULL,
    fingerprint_version TEXT NOT NULL,
    negative_comparison_mode TEXT NOT NULL CHECK (
        negative_comparison_mode IN ('supported', 'positive_only')
    ),
    analysis_through_order_ns INTEGER,
    PRIMARY KEY (
        revision_id, fingerprint_scope_id, origin,
        detector_id, fingerprint_version
    )
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS fix_recurrence_jobs (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL UNIQUE,
    session_id TEXT NOT NULL,
    revision_id TEXT NOT NULL
        REFERENCES session_analysis_revisions(revision_id),
    projection_generation INTEGER NOT NULL CHECK (projection_generation > 0),
    annotation_snapshot INTEGER NOT NULL CHECK (annotation_snapshot >= 0),
    state TEXT NOT NULL CHECK (
        state IN ('pending', 'claimed', 'complete', 'failed')
    ),
    attempt_after_sequence INTEGER NOT NULL DEFAULT 0 CHECK (
        attempt_after_sequence >= 0
    ),
    claim_generation INTEGER NOT NULL DEFAULT 0 CHECK (claim_generation >= 0),
    claim_token TEXT,
    lease_expires_at TEXT,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    retry_at TEXT,
    claimed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS fix_recurrence_job_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL REFERENCES fix_recurrence_jobs(job_id)
        ON DELETE CASCADE,
    event_kind TEXT NOT NULL CHECK (
        event_kind IN ('queued', 'failed', 'complete')
    ),
    attempt_number INTEGER NOT NULL CHECK (attempt_number >= 0),
    recorded_at TEXT NOT NULL,
    UNIQUE(job_id, event_kind, attempt_number)
);

CREATE TABLE IF NOT EXISTS fix_recurrence_observations (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    recurrence_id TEXT NOT NULL UNIQUE,
    annotation_id TEXT NOT NULL
        REFERENCES fix_annotations(annotation_id),
    issue_id TEXT NOT NULL,
    revision_id TEXT NOT NULL,
    occurrence_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    fingerprint_id TEXT NOT NULL,
    fingerprint_version TEXT NOT NULL,
    origin TEXT NOT NULL CHECK (origin IN ('belay', 'numbat')),
    detector_id TEXT NOT NULL,
    detector_version TEXT NOT NULL,
    analysis_generation INTEGER NOT NULL CHECK (analysis_generation > 0),
    projection_generation INTEGER NOT NULL CHECK (projection_generation > 0),
    first_qualifying_event_order_ns INTEGER NOT NULL,
    last_qualifying_event_order_ns INTEGER NOT NULL,
    qualifying_citation_count INTEGER NOT NULL CHECK (
        qualifying_citation_count > 0
    ),
    evidence_complete INTEGER NOT NULL CHECK (evidence_complete IN (0, 1)),
    same_session_as_anchor INTEGER NOT NULL CHECK (
        same_session_as_anchor IN (0, 1)
    ),
    observed_at TEXT NOT NULL,
    UNIQUE(annotation_id, occurrence_id)
);

CREATE TABLE IF NOT EXISTS fix_recurrence_observation_events (
    recurrence_id TEXT NOT NULL
        REFERENCES fix_recurrence_observations(recurrence_id)
        ON DELETE CASCADE,
    event_id TEXT NOT NULL
        REFERENCES events(event_id)
        ON DELETE CASCADE,
    PRIMARY KEY (recurrence_id, event_id)
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS fix_monitoring_subject_match_idx
    ON fix_monitoring_subjects(
        fingerprint_scope_id, origin, detector_id, fingerprint_version,
        annotation_id
    );
CREATE INDEX IF NOT EXISTS session_analysis_capability_match_idx
    ON session_analysis_capabilities(
        fingerprint_scope_id, origin, detector_id, fingerprint_version,
        revision_id
    );
CREATE INDEX IF NOT EXISTS fix_recurrence_jobs_work_idx
    ON fix_recurrence_jobs(state, retry_at, lease_expires_at, sequence);
CREATE INDEX IF NOT EXISTS fix_recurrence_job_events_lookup_idx
    ON fix_recurrence_job_events(job_id, sequence);
CREATE INDEX IF NOT EXISTS fix_recurrence_observations_annotation_idx
    ON fix_recurrence_observations(
        annotation_id, first_qualifying_event_order_ns DESC, recurrence_id DESC
    );
CREATE INDEX IF NOT EXISTS fix_recurrence_observations_issue_idx
    ON fix_recurrence_observations(issue_id, observed_at DESC);
CREATE INDEX IF NOT EXISTS fix_recurrence_observations_session_idx
    ON fix_recurrence_observations(
        session_id, fingerprint_id, recurrence_id
    );
CREATE INDEX IF NOT EXISTS fix_recurrence_observations_snapshot_idx
    ON fix_recurrence_observations(annotation_id, sequence, observed_at);
CREATE INDEX IF NOT EXISTS events_monitoring_session_time_idx
    ON events(session_key, occurred_at_order_ns, event_id);
CREATE INDEX IF NOT EXISTS session_analysis_monitoring_snapshot_idx
    ON session_analysis_revisions(
        session_key, visible_from_generation, visible_until_generation,
        status, analyzed_event_generation
    );
CREATE INDEX IF NOT EXISTS fix_recurrence_jobs_revision_snapshot_idx
    ON fix_recurrence_jobs(revision_id, sequence, job_id);
CREATE INDEX IF NOT EXISTS fix_recurrence_job_events_completion_idx
    ON fix_recurrence_job_events(job_id, event_kind, sequence);
CREATE INDEX IF NOT EXISTS fix_annotations_monitor_order_idx
    ON fix_annotations(fingerprint_id, sequence);
