CREATE TABLE IF NOT EXISTS experience_semantic_decisions (
    decision_id TEXT PRIMARY KEY
        CHECK (length(decision_id) >= 1 AND length(decision_id) <= 256),
    candidate_id TEXT NOT NULL
        CHECK (length(candidate_id) >= 1 AND length(candidate_id) <= 256),
    project_identity TEXT NOT NULL
        CHECK (length(project_identity) >= 1 AND length(project_identity) <= 4096),
    disposition TEXT NOT NULL
        CHECK (disposition IN ('propose', 'reject', 'defer')),
    reason_code TEXT NOT NULL
        CHECK (
            reason_code IN (
                'reusable_supported',
                'temporary_or_task_specific',
                'insufficient_context',
                'unsafe_or_overbroad',
                'not_reusable'
            )
        ),
    proposal_id TEXT
        CHECK (
            proposal_id IS NULL
            OR (length(proposal_id) >= 1 AND length(proposal_id) <= 256)
        ),
    harness TEXT NOT NULL CHECK (harness IN ('claude', 'codex')),
    prompt_version TEXT NOT NULL
        CHECK (length(prompt_version) >= 1 AND length(prompt_version) <= 256),
    input_hash TEXT NOT NULL CHECK (length(input_hash) = 71),
    output_hash TEXT NOT NULL CHECK (length(output_hash) = 71),
    generated_at TEXT NOT NULL,
    payload BLOB NOT NULL CHECK (length(payload) <= 2097152),
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    inserted_at TEXT NOT NULL,
    CHECK (
        (
            disposition = 'propose'
            AND reason_code = 'reusable_supported'
            AND proposal_id IS NOT NULL
        )
        OR (
            disposition = 'reject'
            AND reason_code IN (
                'temporary_or_task_specific',
                'unsafe_or_overbroad',
                'not_reusable'
            )
            AND proposal_id IS NULL
        )
        OR (
            disposition = 'defer'
            AND reason_code = 'insufficient_context'
            AND proposal_id IS NULL
        )
    ),
    FOREIGN KEY (candidate_id)
        REFERENCES experience_candidates(candidate_id),
    FOREIGN KEY (proposal_id)
        REFERENCES experience_semantic_proposals(proposal_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS experience_semantic_decisions_source_idx
    ON experience_semantic_decisions(
        candidate_id,
        harness,
        prompt_version
    );

CREATE INDEX IF NOT EXISTS experience_semantic_decisions_candidate_idx
    ON experience_semantic_decisions(
        candidate_id,
        generated_at DESC,
        decision_id
    );

CREATE INDEX IF NOT EXISTS experience_semantic_decisions_disposition_idx
    ON experience_semantic_decisions(
        disposition,
        generated_at DESC,
        decision_id
    );

CREATE INDEX IF NOT EXISTS experience_semantic_decisions_project_idx
    ON experience_semantic_decisions(
        project_identity,
        generated_at DESC,
        decision_id
    );
