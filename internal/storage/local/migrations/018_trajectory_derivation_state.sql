CREATE TABLE IF NOT EXISTS trajectory_derivation_state (
    session_key TEXT NOT NULL
        REFERENCES transcript_sessions(session_key) ON DELETE CASCADE,
    derivation_version TEXT NOT NULL
        CHECK (
            length(derivation_version) >= 1
            AND length(derivation_version) <= 256
        ),
    status TEXT NOT NULL
        CHECK (status IN ('complete', 'partial', 'failed')),
    transcript_fingerprint TEXT NOT NULL
        CHECK (length(transcript_fingerprint) = 71),
    canonical_fingerprint TEXT NOT NULL
        CHECK (length(canonical_fingerprint) = 71),
    retryable INTEGER NOT NULL CHECK (retryable IN (0, 1)),
    attempt_count INTEGER NOT NULL
        CHECK (attempt_count >= 0 AND attempt_count <= 16),
    retry_at TEXT,
    failure_code TEXT
        CHECK (
            failure_code IS NULL
            OR (
                length(failure_code) >= 1
                AND length(failure_code) <= 64
            )
        ),
    attempted_at TEXT NOT NULL,
    completed_at TEXT,
    payload BLOB NOT NULL
        CHECK (length(payload) <= 263168),
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (session_key, derivation_version),
    CHECK (
        (
            status = 'complete'
            AND retryable = 0
            AND attempt_count = 0
            AND retry_at IS NULL
            AND failure_code IS NULL
            AND completed_at IS NOT NULL
        )
        OR (
            status = 'partial'
            AND retryable = 1
            AND attempt_count >= 1
            AND retry_at IS NOT NULL
            AND failure_code IS NULL
            AND completed_at IS NOT NULL
        )
        OR (
            status = 'failed'
            AND attempt_count >= 1
            AND failure_code IS NOT NULL
            AND completed_at IS NULL
            AND (
                (retryable = 1 AND retry_at IS NOT NULL)
                OR (retryable = 0 AND retry_at IS NULL)
            )
        )
    )
);

CREATE INDEX IF NOT EXISTS trajectory_derivation_state_retry_idx
    ON trajectory_derivation_state(
        derivation_version,
        retryable,
        status,
        retry_at,
        session_key
    );
