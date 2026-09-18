CREATE TABLE IF NOT EXISTS mission_pack_receipt_applications (
    receipt_id TEXT NOT NULL,
    experience_id TEXT NOT NULL,
    experience_version INTEGER NOT NULL CHECK (experience_version >= 1),
    application_id TEXT,
    linked_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (receipt_id, experience_id, experience_version),
    CHECK (
        (application_id IS NULL AND linked_at IS NULL)
        OR (application_id IS NOT NULL AND linked_at IS NOT NULL)
    ),
    FOREIGN KEY (receipt_id)
        REFERENCES mission_pack_receipts(receipt_id),
    FOREIGN KEY (experience_id, experience_version)
        REFERENCES experiences(experience_id, version),
    FOREIGN KEY (application_id)
        REFERENCES experience_applications(application_id)
);

CREATE INDEX IF NOT EXISTS mission_pack_receipt_applications_pending_idx
    ON mission_pack_receipt_applications(
        receipt_id,
        experience_id,
        experience_version
    )
    WHERE application_id IS NULL;

CREATE INDEX IF NOT EXISTS mission_pack_receipt_applications_application_idx
    ON mission_pack_receipt_applications(application_id, receipt_id);
