ALTER TABLE issue_occurrences
    ADD COLUMN source_signal_code TEXT
        CHECK (
            source_signal_code IS NULL OR (
                length(source_signal_code) BETWEEN 1 AND 64
                AND source_signal_code = lower(source_signal_code)
                AND substr(source_signal_code, 1, 1) GLOB '[a-z0-9]'
                AND source_signal_code NOT GLOB '*[^a-z0-9_.-]*'
            )
        );

ALTER TABLE issue_summary_revisions
    ADD COLUMN source_signal_code TEXT
        CHECK (
            source_signal_code IS NULL OR (
                length(source_signal_code) BETWEEN 1 AND 64
                AND source_signal_code = lower(source_signal_code)
                AND substr(source_signal_code, 1, 1) GLOB '[a-z0-9]'
                AND source_signal_code NOT GLOB '*[^a-z0-9_.-]*'
            )
        );
