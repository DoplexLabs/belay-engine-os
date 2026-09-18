CREATE TABLE IF NOT EXISTS insights (
    insight_id TEXT PRIMARY KEY,
    project_identity TEXT NOT NULL UNIQUE,
    harness TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    prompt_version TEXT NOT NULL,
    input_hash TEXT NOT NULL,
    generated_at TEXT NOT NULL,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS insights_provenance_idx
    ON insights(
        harness,
        model,
        prompt_version,
        generated_at DESC,
        insight_id
    );

CREATE TABLE IF NOT EXISTS cost_issue_fixes (
    fix_id TEXT PRIMARY KEY,
    issue_id TEXT NOT NULL,
    project_identity TEXT NOT NULL,
    kind TEXT NOT NULL,
    target_file TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('proposed', 'applied')),
    content_sha256 TEXT NOT NULL DEFAULT '',
    applied_path TEXT NOT NULL DEFAULT '',
    git_commit TEXT NOT NULL DEFAULT '',
    proposed_at TEXT NOT NULL,
    applied_at TEXT,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS cost_issue_fixes_issue_idx
    ON cost_issue_fixes(issue_id, proposed_at DESC, fix_id);
