CREATE TABLE IF NOT EXISTS fix_annotations (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    annotation_id TEXT NOT NULL UNIQUE,
    request_fingerprint TEXT NOT NULL,
    issue_id TEXT NOT NULL,
    anchor_revision_id TEXT NOT NULL,
    anchor_occurrence_id TEXT NOT NULL,
    anchor_session_id TEXT NOT NULL,
    fingerprint_id TEXT NOT NULL,
    fingerprint_version TEXT NOT NULL,
    origin TEXT NOT NULL CHECK (origin IN ('belay', 'numbat')),
    detector_id TEXT NOT NULL,
    detector_version TEXT NOT NULL,
    scope_quality TEXT NOT NULL CHECK (
        scope_quality IN ('resolved', 'lexical', 'unscoped', 'conflict')
    ),
    issue_snapshot_generation INTEGER NOT NULL CHECK (
        issue_snapshot_generation > 0
    ),
    anchor_analysis_generation INTEGER NOT NULL CHECK (
        anchor_analysis_generation > 0
    ),
    anchor_first_observed_at TEXT NOT NULL,
    anchor_last_observed_at TEXT NOT NULL,
    baseline_citation_count INTEGER NOT NULL CHECK (
        baseline_citation_count >= 0
    ),
    change_kind TEXT NOT NULL CHECK (
        change_kind IN (
            'code_change',
            'configuration_change',
            'dependency_change',
            'permission_change',
            'environment_change',
            'agent_instruction',
            'project_rule',
            'monitor_hook',
            'other'
        )
    ),
    change_catalog_version TEXT NOT NULL CHECK (
        change_catalog_version = 'fix-change.v1'
    ),
    recorded_via TEXT NOT NULL CHECK (recorded_via = 'local_ui'),
    recorded_at TEXT NOT NULL,
    monitor_from TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS fix_annotation_events (
    annotation_id TEXT NOT NULL
        REFERENCES fix_annotations(annotation_id) ON DELETE CASCADE,
    event_id TEXT NOT NULL
        REFERENCES events(event_id) ON DELETE CASCADE,
    PRIMARY KEY (annotation_id, event_id)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS fix_annotation_retractions (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    retraction_id TEXT NOT NULL UNIQUE,
    request_fingerprint TEXT NOT NULL,
    annotation_id TEXT NOT NULL UNIQUE
        REFERENCES fix_annotations(annotation_id),
    reason TEXT NOT NULL CHECK (
        reason IN ('recorded_by_mistake', 'superseded', 'other')
    ),
    recorded_via TEXT NOT NULL CHECK (recorded_via = 'local_ui'),
    retracted_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS fix_annotations_issue_order_idx
    ON fix_annotations(issue_id, recorded_at DESC, annotation_id DESC);

CREATE INDEX IF NOT EXISTS fix_annotations_fingerprint_monitor_idx
    ON fix_annotations(fingerprint_id, monitor_from, annotation_id);
