CREATE TABLE IF NOT EXISTS experience_semantic_proposals (
    proposal_id TEXT PRIMARY KEY
        CHECK (length(proposal_id) >= 1 AND length(proposal_id) <= 256),
    candidate_id TEXT NOT NULL
        CHECK (length(candidate_id) >= 1 AND length(candidate_id) <= 256),
    project_identity TEXT NOT NULL
        CHECK (length(project_identity) >= 1 AND length(project_identity) <= 4096),
    experience_type TEXT NOT NULL
        CHECK (
            experience_type IN (
                'preference',
                'procedure',
                'warning',
                'constraint',
                'fact'
            )
        ),
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('project', 'session')),
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
    FOREIGN KEY (candidate_id)
        REFERENCES experience_candidates(candidate_id)
);

CREATE INDEX IF NOT EXISTS experience_semantic_proposals_candidate_idx
    ON experience_semantic_proposals(
        candidate_id,
        generated_at DESC,
        proposal_id
    );

CREATE UNIQUE INDEX IF NOT EXISTS experience_semantic_proposals_source_idx
    ON experience_semantic_proposals(
        candidate_id,
        harness,
        prompt_version
    );

CREATE INDEX IF NOT EXISTS experience_semantic_proposals_project_idx
    ON experience_semantic_proposals(
        project_identity,
        generated_at DESC,
        proposal_id
    );
