CREATE TABLE IF NOT EXISTS session_identity_observations (
    observation_id TEXT PRIMARY KEY,
    source_kind TEXT NOT NULL,
    source_agent TEXT NOT NULL,
    source_session_key TEXT NOT NULL,
    native_namespace TEXT NOT NULL,
    native_id_hash TEXT NOT NULL,
    project_identity TEXT NOT NULL DEFAULT '',
    artifact_type TEXT NOT NULL DEFAULT '',
    source_run_id TEXT NOT NULL DEFAULT '',
    started_at TEXT,
    ended_at TEXT,
    coverage TEXT NOT NULL,
    derivation_version TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(source_kind, source_agent, source_session_key, native_namespace)
);

CREATE INDEX IF NOT EXISTS session_identity_native_idx
    ON session_identity_observations(
        source_agent,
        native_id_hash,
        source_kind,
        source_session_key
    );

CREATE INDEX IF NOT EXISTS session_identity_source_idx
    ON session_identity_observations(
        source_kind,
        source_agent,
        observed_at DESC,
        source_session_key
    );

CREATE TABLE IF NOT EXISTS session_identity_links (
    link_id TEXT PRIMARY KEY,
    left_session_key TEXT NOT NULL,
    right_session_key TEXT NOT NULL,
    relation TEXT NOT NULL CHECK (relation = 'same_session'),
    basis TEXT NOT NULL,
    confidence TEXT NOT NULL CHECK (confidence = 'high'),
    state TEXT NOT NULL
        CHECK (state IN ('active', 'ambiguous', 'rejected', 'superseded')),
    derivation_version TEXT NOT NULL,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (left_session_key <> right_session_key),
    UNIQUE(
        left_session_key,
        right_session_key,
        basis,
        derivation_version
    )
);

CREATE INDEX IF NOT EXISTS session_identity_links_left_idx
    ON session_identity_links(state, left_session_key, right_session_key);

CREATE INDEX IF NOT EXISTS session_identity_links_right_idx
    ON session_identity_links(state, right_session_key, left_session_key);
