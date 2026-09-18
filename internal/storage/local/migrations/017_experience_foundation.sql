CREATE TABLE IF NOT EXISTS experience_candidates (
    candidate_id TEXT PRIMARY KEY,
    project_identity TEXT NOT NULL,
    family TEXT NOT NULL
        CHECK (
            family IN (
                'correction',
                'successful_procedure',
                'failed_approach'
            )
        ),
    lifecycle_state TEXT NOT NULL CHECK (lifecycle_state = 'candidate'),
    instruction_authority TEXT NOT NULL CHECK (instruction_authority = 'none'),
    created_at TEXT NOT NULL,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    inserted_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS experience_candidates_project_idx
    ON experience_candidates(
        project_identity,
        created_at DESC,
        candidate_id
    );
CREATE INDEX IF NOT EXISTS experience_candidates_family_idx
    ON experience_candidates(
        family,
        created_at DESC,
        candidate_id
    );

CREATE TABLE IF NOT EXISTS experiences (
    experience_id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version >= 1),
    origin_candidate_id TEXT NOT NULL,
    project_identity TEXT NOT NULL,
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
    initial_lifecycle_state TEXT NOT NULL
        CHECK (
            initial_lifecycle_state IN (
                'approved',
                'active',
                'paused',
                'contradicted',
                'superseded',
                'expired'
            )
        ),
    lifecycle_state TEXT NOT NULL
        CHECK (
            lifecycle_state IN (
                'approved',
                'active',
                'paused',
                'contradicted',
                'superseded',
                'expired'
            )
        ),
    intervention_strength TEXT NOT NULL
        CHECK (
            intervention_strength IN (
                'observe',
                'advise',
                'clarify',
                'require_verification'
            )
        ),
    content_hash TEXT NOT NULL,
    previous_experience_id TEXT,
    previous_version INTEGER,
    approved_at TEXT NOT NULL,
    activated_at TEXT,
    expires_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    PRIMARY KEY (experience_id, version),
    UNIQUE (project_identity, origin_candidate_id, version),
    CHECK (
        (
            version = 1
            AND previous_experience_id IS NULL
            AND previous_version IS NULL
        )
        OR (
            version > 1
            AND previous_experience_id = experience_id
            AND previous_version = version - 1
        )
    ),
    CHECK (
        (lifecycle_state = 'active' AND activated_at IS NOT NULL)
        OR (lifecycle_state != 'active' AND activated_at IS NULL)
    ),
    FOREIGN KEY (origin_candidate_id)
        REFERENCES experience_candidates(candidate_id),
    FOREIGN KEY (previous_experience_id, previous_version)
        REFERENCES experiences(experience_id, version)
);

CREATE INDEX IF NOT EXISTS experiences_project_state_idx
    ON experiences(
        project_identity,
        lifecycle_state,
        updated_at DESC,
        experience_id,
        version DESC
    );
CREATE INDEX IF NOT EXISTS experiences_origin_idx
    ON experiences(
        project_identity,
        origin_candidate_id,
        version DESC
    );

CREATE TABLE IF NOT EXISTS experience_transitions (
    transition_id TEXT PRIMARY KEY,
    experience_id TEXT NOT NULL,
    experience_version INTEGER NOT NULL CHECK (experience_version >= 1),
    from_state TEXT NOT NULL
        CHECK (
            from_state IN (
                'approved',
                'active',
                'paused',
                'contradicted',
                'superseded',
                'expired'
            )
        ),
    to_state TEXT NOT NULL
        CHECK (
            to_state IN (
                'approved',
                'active',
                'paused',
                'contradicted',
                'superseded',
                'expired'
            )
        ),
    reason_code TEXT NOT NULL,
    actor_kind TEXT NOT NULL
        CHECK (actor_kind IN ('user', 'deterministic_worker')),
    occurred_at TEXT NOT NULL,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    inserted_at TEXT NOT NULL,
    FOREIGN KEY (experience_id, experience_version)
        REFERENCES experiences(experience_id, version)
);

CREATE INDEX IF NOT EXISTS experience_transitions_experience_idx
    ON experience_transitions(
        experience_id,
        experience_version,
        occurred_at,
        transition_id
    );

CREATE TABLE IF NOT EXISTS experience_evidence (
    evidence_set_id TEXT NOT NULL,
    evidence_index INTEGER NOT NULL CHECK (evidence_index >= 0),
    experience_id TEXT NOT NULL,
    experience_version INTEGER NOT NULL CHECK (experience_version >= 1),
    source_kind TEXT NOT NULL
        CHECK (
            source_kind IN (
                'transcript_turn',
                'canonical_event',
                'outcome_observation',
                'workspace_hash',
                'user_recorded_outcome'
            )
        ),
    session_key TEXT,
    turn_index INTEGER CHECK (turn_index IS NULL OR turn_index >= 0),
    event_id TEXT,
    outcome_id TEXT,
    occurred_at TEXT,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    inserted_at TEXT NOT NULL,
    PRIMARY KEY (
        experience_id,
        experience_version,
        evidence_set_id,
        evidence_index
    ),
    FOREIGN KEY (experience_id, experience_version)
        REFERENCES experiences(experience_id, version)
);

CREATE INDEX IF NOT EXISTS experience_evidence_experience_idx
    ON experience_evidence(
        experience_id,
        experience_version,
        evidence_index
    );
CREATE INDEX IF NOT EXISTS experience_evidence_session_idx
    ON experience_evidence(
        session_key,
        turn_index,
        experience_id,
        experience_version
    );
CREATE INDEX IF NOT EXISTS experience_evidence_event_idx
    ON experience_evidence(event_id, experience_id, experience_version);
CREATE INDEX IF NOT EXISTS experience_evidence_outcome_idx
    ON experience_evidence(outcome_id, experience_id, experience_version);

CREATE TABLE IF NOT EXISTS trajectory_edges (
    edge_id TEXT PRIMARY KEY,
    project_identity TEXT NOT NULL,
    session_key TEXT NOT NULL,
    from_kind TEXT NOT NULL,
    from_ref_key TEXT NOT NULL,
    relation TEXT NOT NULL,
    to_kind TEXT NOT NULL,
    to_ref_key TEXT NOT NULL,
    evidence_class TEXT NOT NULL
        CHECK (
            evidence_class IN (
                'observed',
                'deterministic_inference',
                'semantic_hypothesis'
            )
        ),
    confidence TEXT NOT NULL CHECK (confidence IN ('high', 'medium', 'low')),
    derivation_version TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    inserted_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS trajectory_edges_session_idx
    ON trajectory_edges(
        session_key,
        occurred_at,
        edge_id
    );
CREATE INDEX IF NOT EXISTS trajectory_edges_project_relation_idx
    ON trajectory_edges(
        project_identity,
        relation,
        occurred_at,
        edge_id
    );
CREATE INDEX IF NOT EXISTS trajectory_edges_from_idx
    ON trajectory_edges(from_kind, from_ref_key, occurred_at, edge_id);
CREATE INDEX IF NOT EXISTS trajectory_edges_to_idx
    ON trajectory_edges(to_kind, to_ref_key, occurred_at, edge_id);

CREATE TABLE IF NOT EXISTS outcome_observations (
    outcome_id TEXT PRIMARY KEY,
    project_identity TEXT NOT NULL,
    session_key TEXT NOT NULL,
    kind TEXT NOT NULL,
    result TEXT NOT NULL
        CHECK (result IN ('succeeded', 'failed', 'observed', 'unknown')),
    evidence_class TEXT NOT NULL
        CHECK (evidence_class IN ('observed', 'deterministic_inference')),
    confidence TEXT NOT NULL CHECK (confidence IN ('high', 'medium', 'low')),
    derivation_version TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    inserted_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS outcome_observations_session_idx
    ON outcome_observations(session_key, occurred_at, outcome_id);
CREATE INDEX IF NOT EXISTS outcome_observations_project_kind_idx
    ON outcome_observations(
        project_identity,
        kind,
        occurred_at,
        outcome_id
    );

CREATE TABLE IF NOT EXISTS experience_applications (
    application_id TEXT PRIMARY KEY,
    experience_id TEXT NOT NULL,
    experience_version INTEGER NOT NULL CHECK (experience_version >= 1),
    project_identity TEXT NOT NULL,
    session_key TEXT,
    delivery_kind TEXT NOT NULL CHECK (delivery_kind IN ('mission_pack', 'hook')),
    delivery_state TEXT NOT NULL
        CHECK (
            delivery_state IN (
                'pending',
                'delivered',
                'not_delivered',
                'unknown'
            )
        ),
    opportunity_state TEXT NOT NULL
        CHECK (
            opportunity_state IN ('observed', 'not_observed', 'unknown')
        ),
    applicability_state TEXT NOT NULL
        CHECK (
            applicability_state IN ('applicable', 'not_applicable', 'unknown')
        ),
    verifier_state TEXT NOT NULL
        CHECK (
            verifier_state IN (
                'not_evaluated',
                'satisfied',
                'violated',
                'unknown'
            )
        ),
    task_outcome_state TEXT NOT NULL
        CHECK (
            task_outcome_state IN (
                'not_observed',
                'succeeded',
                'failed',
                'unknown'
            )
    ),
    delivered_at TEXT,
    evaluated_at TEXT,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    inserted_at TEXT NOT NULL,
    FOREIGN KEY (experience_id, experience_version)
        REFERENCES experiences(experience_id, version)
);

CREATE INDEX IF NOT EXISTS experience_applications_experience_idx
    ON experience_applications(
        experience_id,
        experience_version,
        delivered_at,
        application_id
    );
CREATE INDEX IF NOT EXISTS experience_applications_session_idx
    ON experience_applications(session_key, delivered_at, application_id);
CREATE INDEX IF NOT EXISTS experience_applications_project_idx
    ON experience_applications(
        project_identity,
        delivered_at,
        application_id
    );

CREATE TABLE IF NOT EXISTS experience_generations (
    project_identity TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK (generation >= 1),
    compiled_hash TEXT NOT NULL,
    previous_generation INTEGER,
    state TEXT NOT NULL CHECK (state IN ('active', 'inactive')),
    compiled_at TEXT NOT NULL,
    activated_at TEXT NOT NULL,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (project_identity, generation),
    CHECK (
        previous_generation IS NULL
        OR previous_generation >= 1
    ),
    FOREIGN KEY (project_identity, previous_generation)
        REFERENCES experience_generations(project_identity, generation)
);

CREATE UNIQUE INDEX IF NOT EXISTS experience_generations_active_idx
    ON experience_generations(project_identity)
    WHERE state = 'active';
CREATE INDEX IF NOT EXISTS experience_generations_history_idx
    ON experience_generations(
        project_identity,
        generation DESC
    );

CREATE TABLE IF NOT EXISTS mission_pack_receipts (
    receipt_id TEXT PRIMARY KEY,
    pack_id TEXT NOT NULL,
    project_identity TEXT NOT NULL,
    harness TEXT NOT NULL CHECK (harness IN ('claude', 'codex')),
    generation INTEGER NOT NULL CHECK (generation >= 1),
    accepted_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    task_hint_hash TEXT NOT NULL DEFAULT '',
    binding_state TEXT NOT NULL
        CHECK (
            binding_state IN (
                'pending',
                'bound',
                'ambiguous',
                'expired',
                'cancelled'
            )
        ),
    bound_session_key TEXT,
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (
        (binding_state = 'bound' AND bound_session_key IS NOT NULL)
        OR (binding_state != 'bound' AND bound_session_key IS NULL)
    ),
    FOREIGN KEY (project_identity, generation)
        REFERENCES experience_generations(project_identity, generation)
);

CREATE INDEX IF NOT EXISTS mission_pack_receipts_pending_idx
    ON mission_pack_receipts(
        project_identity,
        harness,
        binding_state,
        accepted_at,
        expires_at,
        receipt_id
    );
CREATE INDEX IF NOT EXISTS mission_pack_receipts_pack_idx
    ON mission_pack_receipts(pack_id, accepted_at, receipt_id);
