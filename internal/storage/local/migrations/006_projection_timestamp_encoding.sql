UPDATE session_scopes SET
    created_at = strftime('%Y-%m-%dT%H:%M:%f000000Z', created_at),
    updated_at = strftime('%Y-%m-%dT%H:%M:%f000000Z', updated_at);

UPDATE event_enrichments SET
    created_at = strftime('%Y-%m-%dT%H:%M:%f000000Z', created_at),
    updated_at = strftime('%Y-%m-%dT%H:%M:%f000000Z', updated_at);

UPDATE dirty_sessions SET
    retry_at = CASE WHEN retry_at IS NULL THEN NULL
        ELSE strftime('%Y-%m-%dT%H:%M:%f000000Z', retry_at) END,
    claimed_at = CASE WHEN claimed_at IS NULL THEN NULL
        ELSE strftime('%Y-%m-%dT%H:%M:%f000000Z', claimed_at) END,
    updated_at = strftime('%Y-%m-%dT%H:%M:%f000000Z', updated_at);

UPDATE issue_projection_metadata SET
    last_successful_analysis_at =
        CASE WHEN last_successful_analysis_at IS NULL THEN NULL
        ELSE strftime('%Y-%m-%dT%H:%M:%f000000Z', last_successful_analysis_at) END;

UPDATE session_analysis_revisions SET
    created_at = strftime('%Y-%m-%dT%H:%M:%f000000Z', created_at),
    updated_at = strftime('%Y-%m-%dT%H:%M:%f000000Z', updated_at);

UPDATE issue_occurrences SET
    first_observed_at = strftime('%Y-%m-%dT%H:%M:%f000000Z', first_observed_at),
    last_observed_at = strftime('%Y-%m-%dT%H:%M:%f000000Z', last_observed_at),
    created_at = strftime('%Y-%m-%dT%H:%M:%f000000Z', created_at),
    updated_at = strftime('%Y-%m-%dT%H:%M:%f000000Z', updated_at);

UPDATE analysis_diagnostics SET
    first_observed_at = strftime('%Y-%m-%dT%H:%M:%f000000Z', first_observed_at),
    last_observed_at = strftime('%Y-%m-%dT%H:%M:%f000000Z', last_observed_at);
