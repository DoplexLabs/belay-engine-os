CREATE TABLE IF NOT EXISTS experience_review_actions (
    action_id TEXT PRIMARY KEY
        CHECK (length(action_id) >= 1 AND length(action_id) <= 256),
    proposal_id TEXT NOT NULL
        CHECK (length(proposal_id) >= 1 AND length(proposal_id) <= 256),
    candidate_id TEXT NOT NULL
        CHECK (length(candidate_id) >= 1 AND length(candidate_id) <= 256),
    project_identity TEXT NOT NULL
        CHECK (length(project_identity) >= 1 AND length(project_identity) <= 4096),
    disposition TEXT NOT NULL
        CHECK (disposition IN ('defer', 'reject')),
    occurred_at TEXT NOT NULL,
    available_after TEXT,
    approval_token_issued_at TEXT NOT NULL,
    payload BLOB NOT NULL CHECK (length(payload) <= 66560),
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    inserted_at TEXT NOT NULL,
    CHECK (
        (
            disposition = 'defer'
            AND available_after IS NOT NULL
            AND available_after > occurred_at
        )
        OR (
            disposition = 'reject'
            AND available_after IS NULL
        )
    ),
    FOREIGN KEY (proposal_id)
        REFERENCES experience_semantic_proposals(proposal_id),
    FOREIGN KEY (candidate_id)
        REFERENCES experience_candidates(candidate_id)
);

CREATE INDEX IF NOT EXISTS experience_review_actions_proposal_latest_idx
    ON experience_review_actions(
        proposal_id,
        occurred_at DESC,
        action_id DESC
    );
