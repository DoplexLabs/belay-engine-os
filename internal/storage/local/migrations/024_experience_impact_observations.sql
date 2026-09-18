CREATE TABLE IF NOT EXISTS experience_impact_observations (
    observation_id TEXT PRIMARY KEY,
    application_id TEXT NOT NULL,
    experience_id TEXT NOT NULL,
    experience_version INTEGER NOT NULL CHECK (experience_version >= 1),
    project_identity TEXT NOT NULL,
    session_key TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    window_start_turn INTEGER NOT NULL CHECK (window_start_turn >= 0),
    window_end_turn INTEGER NOT NULL
        CHECK (window_end_turn >= window_start_turn),
    derivation_version TEXT NOT NULL,
    input_hash TEXT NOT NULL,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    inserted_at TEXT NOT NULL,
    UNIQUE (application_id, input_hash, derivation_version),
    FOREIGN KEY (application_id)
        REFERENCES experience_applications(application_id),
    FOREIGN KEY (experience_id, experience_version)
        REFERENCES experiences(experience_id, version)
);

CREATE INDEX IF NOT EXISTS experience_impact_observations_application_idx
    ON experience_impact_observations(
        application_id,
        observed_at DESC,
        observation_id
    );

CREATE INDEX IF NOT EXISTS experience_impact_observations_project_idx
    ON experience_impact_observations(
        project_identity,
        observed_at DESC,
        observation_id
    );

CREATE INDEX IF NOT EXISTS experience_impact_observations_session_idx
    ON experience_impact_observations(
        session_key,
        observed_at DESC,
        observation_id
    );
