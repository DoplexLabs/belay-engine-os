CREATE TABLE IF NOT EXISTS transcript_project_analysis_state (
    project_identity TEXT PRIMARY KEY,
    transcript_generation INTEGER NOT NULL DEFAULT 0
        CHECK (transcript_generation >= 0),
    analyzed_generation INTEGER NOT NULL DEFAULT 0
        CHECK (
            analyzed_generation >= 0
            AND analyzed_generation <= transcript_generation
        ),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    analyzed_at TEXT
);

CREATE INDEX IF NOT EXISTS transcript_project_analysis_dirty_idx
    ON transcript_project_analysis_state(
        transcript_generation,
        analyzed_generation,
        updated_at,
        project_identity
    );

INSERT INTO transcript_project_analysis_state (
    project_identity,
    transcript_generation,
    analyzed_generation,
    created_at,
    updated_at
)
SELECT
    project_identity,
    1,
    0,
    MIN(created_at),
    MAX(updated_at)
FROM transcript_sessions
GROUP BY project_identity
ON CONFLICT(project_identity) DO NOTHING;

CREATE TABLE IF NOT EXISTS cost_issues (
    issue_id TEXT PRIMARY KEY,
    detector_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    project_identity TEXT NOT NULL,
    wasted_minutes REAL NOT NULL CHECK (wasted_minutes >= 0),
    wasted_tokens INTEGER NOT NULL CHECK (wasted_tokens >= 0),
    wasted_usd REAL CHECK (wasted_usd IS NULL OR wasted_usd >= 0),
    wasted_usd_known INTEGER NOT NULL
        CHECK (
            wasted_usd_known IN (0, 1)
            AND (
                (wasted_usd_known = 0 AND wasted_usd IS NULL)
                OR (wasted_usd_known = 1 AND wasted_usd IS NOT NULL)
            )
        ),
    lower_bound INTEGER NOT NULL CHECK (lower_bound IN (0, 1)),
    session_count INTEGER NOT NULL CHECK (session_count >= 1),
    first_seen TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(project_identity, detector_id, fingerprint)
);

CREATE INDEX IF NOT EXISTS cost_issues_rank_idx
    ON cost_issues(
        wasted_usd_known DESC,
        wasted_usd DESC,
        session_count DESC,
        last_seen DESC,
        issue_id
    );
CREATE INDEX IF NOT EXISTS cost_issues_project_rank_idx
    ON cost_issues(
        project_identity,
        wasted_usd_known DESC,
        wasted_usd DESC,
        session_count DESC,
        last_seen DESC,
        issue_id
    );
CREATE INDEX IF NOT EXISTS cost_issues_detector_rank_idx
    ON cost_issues(
        detector_id,
        wasted_usd_known DESC,
        wasted_usd DESC,
        session_count DESC,
        last_seen DESC,
        issue_id
    );

CREATE TABLE IF NOT EXISTS correction_candidates (
    candidate_id TEXT PRIMARY KEY,
    project_identity TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS correction_candidates_project_idx
    ON correction_candidates(project_identity, occurred_at, candidate_id);
