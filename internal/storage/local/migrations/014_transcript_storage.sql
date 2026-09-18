CREATE TABLE IF NOT EXISTS transcript_sessions (
    session_key TEXT PRIMARY KEY,
    agent TEXT NOT NULL,
    native_session_id TEXT NOT NULL,
    project_path TEXT NOT NULL,
    git_remote_url TEXT NOT NULL,
    project_identity TEXT NOT NULL,
    started_at TEXT,
    ended_at TEXT,
    wall_duration_ms INTEGER NOT NULL DEFAULT 0
        CHECK (wall_duration_ms >= 0),
    total_input_tokens INTEGER
        CHECK (total_input_tokens IS NULL OR total_input_tokens >= 0),
    total_output_tokens INTEGER
        CHECK (total_output_tokens IS NULL OR total_output_tokens >= 0),
    total_tokens INTEGER
        CHECK (total_tokens IS NULL OR total_tokens >= 0),
    total_cache_read_tokens INTEGER
        CHECK (total_cache_read_tokens IS NULL OR total_cache_read_tokens >= 0),
    total_cache_write_tokens INTEGER
        CHECK (total_cache_write_tokens IS NULL OR total_cache_write_tokens >= 0),
    total_cost_usd REAL
        CHECK (total_cost_usd IS NULL OR total_cost_usd >= 0),
    turn_count INTEGER NOT NULL DEFAULT 0 CHECK (turn_count >= 0),
    user_turn_count INTEGER NOT NULL DEFAULT 0 CHECK (user_turn_count >= 0),
    assistant_turn_count INTEGER NOT NULL DEFAULT 0
        CHECK (assistant_turn_count >= 0),
    tool_call_count INTEGER NOT NULL DEFAULT 0 CHECK (tool_call_count >= 0),
    tool_result_count INTEGER NOT NULL DEFAULT 0
        CHECK (tool_result_count >= 0),
    system_turn_count INTEGER NOT NULL DEFAULT 0 CHECK (system_turn_count >= 0),
    compaction_summary_count INTEGER NOT NULL DEFAULT 0
        CHECK (compaction_summary_count >= 0),
    coverage TEXT NOT NULL
        CHECK (coverage IN ('complete', 'partial', 'live')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS transcript_sessions_project_idx
    ON transcript_sessions(project_identity, ended_at DESC, session_key);
CREATE INDEX IF NOT EXISTS transcript_sessions_coverage_idx
    ON transcript_sessions(coverage, updated_at, session_key);

CREATE TABLE IF NOT EXISTS transcript_turns (
    turn_id TEXT PRIMARY KEY,
    source_record_key TEXT NOT NULL UNIQUE,
    session_key TEXT NOT NULL
        REFERENCES transcript_sessions(session_key) ON DELETE CASCADE,
    turn_index INTEGER NOT NULL CHECK (turn_index >= 0),
    occurred_at TEXT NOT NULL,
    role TEXT NOT NULL
        CHECK (
            role IN (
                'user',
                'assistant',
                'tool_call',
                'tool_result',
                'system',
                'compaction_summary'
            )
        ),
    tool_name TEXT NOT NULL,
    model TEXT NOT NULL,
    input_tokens INTEGER CHECK (input_tokens IS NULL OR input_tokens >= 0),
    output_tokens INTEGER CHECK (output_tokens IS NULL OR output_tokens >= 0),
    cache_read_tokens INTEGER
        CHECK (cache_read_tokens IS NULL OR cache_read_tokens >= 0),
    cache_write_tokens INTEGER
        CHECK (cache_write_tokens IS NULL OR cache_write_tokens >= 0),
    cost_usd REAL CHECK (cost_usd IS NULL OR cost_usd >= 0),
    payload BLOB NOT NULL,
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS transcript_turns_session_order_idx
    ON transcript_turns(session_key, turn_index, occurred_at, turn_id);
CREATE INDEX IF NOT EXISTS transcript_turns_session_role_idx
    ON transcript_turns(session_key, role, turn_index, turn_id);
CREATE INDEX IF NOT EXISTS transcript_turns_session_tool_idx
    ON transcript_turns(session_key, tool_name, turn_index, turn_id);
CREATE INDEX IF NOT EXISTS transcript_turns_occurred_idx
    ON transcript_turns(occurred_at, session_key, turn_id);
