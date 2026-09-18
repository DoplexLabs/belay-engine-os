CREATE TABLE IF NOT EXISTS habit_debriefs (
    session_key TEXT PRIMARY KEY,
    project_identity TEXT NOT NULL,
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

CREATE INDEX IF NOT EXISTS habit_debriefs_project_idx
    ON habit_debriefs(project_identity, generated_at DESC, session_key);
