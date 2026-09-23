CREATE TABLE IF NOT EXISTS evidence_episodes (
    episode_id TEXT PRIMARY KEY,
    project_identity TEXT NOT NULL,
    session_key TEXT NOT NULL,
    kind TEXT NOT NULL,
    started_at TEXT NOT NULL,
    ended_at TEXT NOT NULL,
    confidence TEXT NOT NULL,
    complete INTEGER NOT NULL CHECK (complete IN (0, 1)),
    derivation_version TEXT NOT NULL,
    input_hash TEXT NOT NULL,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    inserted_at TEXT NOT NULL,
    UNIQUE(session_key, kind, derivation_version, input_hash)
);

CREATE INDEX IF NOT EXISTS evidence_episodes_session_idx
    ON evidence_episodes(session_key, started_at, episode_id);

CREATE INDEX IF NOT EXISTS evidence_episodes_project_kind_idx
    ON evidence_episodes(project_identity, kind, ended_at DESC, episode_id);

CREATE INDEX IF NOT EXISTS evidence_episodes_derivation_idx
    ON evidence_episodes(derivation_version, input_hash, episode_id);

UPDATE transcript_project_analysis_state
SET analysis_version = '';
