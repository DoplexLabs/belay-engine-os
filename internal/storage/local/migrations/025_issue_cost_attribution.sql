CREATE TABLE IF NOT EXISTS project_issue_cost_totals (
    project_identity TEXT PRIMARY KEY,
    attributed_minutes REAL NOT NULL
        CHECK (attributed_minutes >= 0),
    attributed_tokens INTEGER NOT NULL
        CHECK (attributed_tokens >= 0),
    attributed_usd REAL
        CHECK (attributed_usd IS NULL OR attributed_usd >= 0),
    attributed_usd_known INTEGER NOT NULL
        CHECK (
            attributed_usd_known IN (0, 1)
            AND (
                (attributed_usd_known = 0 AND attributed_usd IS NULL)
                OR (attributed_usd_known = 1 AND attributed_usd IS NOT NULL)
            )
        ),
    lower_bound INTEGER NOT NULL CHECK (lower_bound IN (0, 1)),
    updated_at TEXT NOT NULL
);

UPDATE transcript_turns
SET input_tokens = MAX(
    input_tokens
        - COALESCE(cache_read_tokens, 0)
        - COALESCE(cache_write_tokens, 0),
    0
)
WHERE input_tokens IS NOT NULL
  AND session_key IN (
      SELECT session_key
      FROM transcript_sessions
      WHERE agent = 'codex'
  );

UPDATE transcript_sessions
SET total_input_tokens = (
        SELECT SUM(input_tokens)
        FROM transcript_turns
        WHERE transcript_turns.session_key = transcript_sessions.session_key
    ),
    total_output_tokens = (
        SELECT SUM(output_tokens)
        FROM transcript_turns
        WHERE transcript_turns.session_key = transcript_sessions.session_key
    ),
    total_cache_read_tokens = (
        SELECT SUM(cache_read_tokens)
        FROM transcript_turns
        WHERE transcript_turns.session_key = transcript_sessions.session_key
    ),
    total_cache_write_tokens = (
        SELECT SUM(cache_write_tokens)
        FROM transcript_turns
        WHERE transcript_turns.session_key = transcript_sessions.session_key
    ),
    total_tokens = CASE
        WHEN EXISTS (
            SELECT 1
            FROM transcript_turns
            WHERE transcript_turns.session_key = transcript_sessions.session_key
              AND (
                  input_tokens IS NOT NULL
                  OR output_tokens IS NOT NULL
                  OR cache_read_tokens IS NOT NULL
                  OR cache_write_tokens IS NOT NULL
              )
        )
        THEN (
            SELECT
                COALESCE(SUM(input_tokens), 0)
                + COALESCE(SUM(output_tokens), 0)
                + COALESCE(SUM(cache_read_tokens), 0)
                + COALESCE(SUM(cache_write_tokens), 0)
            FROM transcript_turns
            WHERE transcript_turns.session_key = transcript_sessions.session_key
        )
        ELSE NULL
    END,
    updated_at = CURRENT_TIMESTAMP;

UPDATE transcript_project_analysis_state
SET analyzed_generation = 0,
    analyzed_at = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE transcript_generation > 0;
