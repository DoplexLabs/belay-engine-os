ALTER TABLE issue_occurrences
    ADD COLUMN experimental INTEGER NOT NULL DEFAULT 0
        CHECK (experimental IN (0, 1));

UPDATE issue_occurrences
SET experimental = 1
WHERE detector_id = 'repeated_command_attempts';

CREATE INDEX IF NOT EXISTS issue_occurrences_attention_idx
    ON issue_occurrences(
        experimental, category, visible_from_generation,
        visible_until_generation, issue_id
    );
