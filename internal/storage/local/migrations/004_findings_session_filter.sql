CREATE INDEX IF NOT EXISTS findings_session_read_filter_idx
    ON findings(session_key, detected_at DESC, finding_id DESC);
