package local

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/pricing"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	defaultTranscriptTurnLimit    = 500
	maxTranscriptTurnLimit        = 10000
	defaultTranscriptSessionLimit = 50
	maxTranscriptSessionLimit     = 200
	defaultTranscriptProjectLimit = 25
	maxTranscriptProjectLimit     = 101
)

const reindexTranscriptSessionSQL = `
	WITH ranked AS MATERIALIZED (
		SELECT rowid AS turn_rowid,
			ROW_NUMBER() OVER (
				ORDER BY occurred_at, source_record_key, turn_id
			) - 1 AS next_turn_index
		FROM transcript_turns
		WHERE session_key = ?
	)
	UPDATE transcript_turns
	SET turn_index = ranked.next_turn_index
	FROM ranked
	WHERE transcript_turns.rowid = ranked.turn_rowid
		AND transcript_turns.turn_index <> ranked.next_turn_index`

var validTranscriptRoles = map[transcript.Role]bool{
	transcript.RoleUser:              true,
	transcript.RoleAssistant:         true,
	transcript.RoleToolCall:          true,
	transcript.RoleToolResult:        true,
	transcript.RoleSystem:            true,
	transcript.RoleCompactionSummary: true,
}

// AppendTranscriptBatch persists replay-safe native transcript turns and
// refreshes the plaintext session projection from the retained turn rows.
func (s *Store) AppendTranscriptBatch(
	ctx context.Context,
	session transcript.Session,
	turns []transcript.Turn,
) (int, error) {
	if err := validateTranscriptSession(session); err != nil {
		return 0, err
	}
	type encryptedTurn struct {
		turn    transcript.Turn
		payload []byte
	}
	encrypted := make([]encryptedTurn, 0, len(turns))
	for _, turn := range turns {
		if err := validateTranscriptTurn(session.SessionKey, turn); err != nil {
			return 0, err
		}
		if strings.TrimSpace(turn.Payload.PriceTableVersion) == "" {
			turn.Payload.PriceTableVersion = pricing.Version
		}
		payload, err := json.Marshal(turn.Payload)
		if err != nil {
			return 0, errors.New("encode transcript turn payload")
		}
		payload, err = s.cipher.seal(
			"transcript_turn",
			turn.TurnID,
			"payload",
			payload,
		)
		if err != nil {
			return 0, err
		}
		encrypted = append(encrypted, encryptedTurn{turn: turn, payload: payload})
	}

	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, errors.New("begin transcript batch persistence")
	}
	defer tx.Rollback()

	inserted := 0
	err = withMutationTx(ctx, tx, mutationTranscriptIngestion, func() error {
		existing, existed, err := transcriptSessionMetadataTx(
			ctx,
			tx,
			session.SessionKey,
		)
		if err != nil {
			return err
		}
		if err := upsertTranscriptSessionMetadataTx(ctx, tx, session, now); err != nil {
			return err
		}
		for _, value := range encrypted {
			turn := value.turn
			result, err := tx.ExecContext(ctx, `
				INSERT INTO transcript_turns (
					turn_id, source_record_key, session_key, turn_index,
					occurred_at, role, tool_name, model, input_tokens,
					output_tokens, cache_read_tokens, cache_write_tokens,
					cost_usd, price_table_version, payload, payload_encoding,
					created_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(source_record_key) DO NOTHING`,
				turn.TurnID,
				turn.SourceRecordKey,
				turn.SessionKey,
				turn.TurnIndex,
				formatProjectionTime(turn.OccurredAt),
				turn.Role,
				turn.ToolName,
				turn.Model,
				nullableInt64(turn.InputTokens),
				nullableInt64(turn.OutputTokens),
				nullableInt64(turn.CacheReadTokens),
				nullableInt64(turn.CacheWriteTokens),
				nullableFloat64(turn.CostUSD),
				turn.Payload.PriceTableVersion,
				value.payload,
				payloadEncodingAESGCM,
				now,
			)
			if err != nil {
				return fmt.Errorf("append transcript turn: %w", err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return errors.New("inspect transcript turn persistence")
			}
			if affected == 0 {
				var existingTurnID, existingSessionKey string
				if err := tx.QueryRowContext(ctx, `
					SELECT turn_id, session_key
					FROM transcript_turns
					WHERE source_record_key = ?`,
					turn.SourceRecordKey,
				).Scan(&existingTurnID, &existingSessionKey); err != nil {
					return errors.New("resolve replayed transcript turn")
				}
				if existingTurnID != turn.TurnID ||
					existingSessionKey != turn.SessionKey {
					return errors.New("transcript source record identity conflict")
				}
			}
			inserted += int(affected)
		}
		if inserted > 0 {
			if err := reindexTranscriptSessionTx(ctx, tx, session.SessionKey); err != nil {
				return err
			}
		}
		if err := recomputeTranscriptSessionTx(
			ctx,
			tx,
			session.SessionKey,
			now,
		); err != nil {
			return err
		}
		changed := !existed ||
			transcriptSessionMetadataChanged(existing, session) ||
			inserted > 0
		if !changed {
			return nil
		}
		projects := map[string]struct{}{session.ProjectIdentity: {}}
		if existed && existing.ProjectIdentity != session.ProjectIdentity {
			projects[existing.ProjectIdentity] = struct{}{}
		}
		for projectIdentity := range projects {
			if err := markTranscriptProjectDirtyTx(
				ctx,
				tx,
				projectIdentity,
				now,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, errors.New("commit transcript batch persistence")
	}
	return inserted, nil
}

// repriceTranscriptCosts refreshes token-bearing turns against the bundled
// catalog. Unknown models remain NULL, and a future catalog version will retry
// them without requiring transcript files to be rescanned.
func (s *Store) repriceTranscriptCosts(ctx context.Context) error {
	var appliedVersion string
	if err := s.db.QueryRowContext(ctx, `
		SELECT price_table_version
		FROM transcript_pricing_metadata
		WHERE singleton = 1`,
	).Scan(&appliedVersion); err != nil {
		return errors.New("read transcript pricing metadata")
	}
	if appliedVersion == pricing.Version {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT turn_id, session_key, model, input_tokens, output_tokens,
			cache_read_tokens, cache_write_tokens
		FROM transcript_turns
		WHERE (
				COALESCE(input_tokens, 0) > 0
				OR COALESCE(output_tokens, 0) > 0
				OR COALESCE(cache_read_tokens, 0) > 0
				OR COALESCE(cache_write_tokens, 0) > 0
			)
			AND COALESCE(price_table_version, '') <> ?`,
		pricing.Version,
	)
	if err != nil {
		return errors.New("read transcript turns for repricing")
	}
	type pricedTurn struct {
		turnID     string
		sessionKey string
		costUSD    *float64
	}
	var turns []pricedTurn
	for rows.Next() {
		var turnID, sessionKey, model string
		var input, output, cacheRead, cacheWrite sql.NullInt64
		if err := rows.Scan(
			&turnID,
			&sessionKey,
			&model,
			&input,
			&output,
			&cacheRead,
			&cacheWrite,
		); err != nil {
			rows.Close()
			return errors.New("scan transcript turn for repricing")
		}
		turns = append(turns, pricedTurn{
			turnID:     turnID,
			sessionKey: sessionKey,
			costUSD: pricing.EstimateUSD(
				model,
				pricing.Usage{
					InputTokens:      nullableInt64Pointer(input),
					OutputTokens:     nullableInt64Pointer(output),
					CacheReadTokens:  nullableInt64Pointer(cacheRead),
					CacheWriteTokens: nullableInt64Pointer(cacheWrite),
				},
			),
		})
	}
	if err := rows.Close(); err != nil {
		return errors.New("close transcript repricing rows")
	}
	if err := rows.Err(); err != nil {
		return errors.New("iterate transcript turns for repricing")
	}
	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin transcript repricing")
	}
	defer tx.Rollback()
	if err := withMutationTx(ctx, tx, mutationTranscriptIngestion, func() error {
		sessions := make(map[string]bool)
		for _, turn := range turns {
			if _, err := tx.ExecContext(ctx, `
				UPDATE transcript_turns
				SET cost_usd = ?, price_table_version = ?
				WHERE turn_id = ?`,
				nullableFloat64(turn.costUSD),
				pricing.Version,
				turn.turnID,
			); err != nil {
				return errors.New("reprice transcript turn")
			}
			sessions[turn.sessionKey] = true
		}
		for sessionKey := range sessions {
			if err := recomputeTranscriptSessionTx(
				ctx,
				tx,
				sessionKey,
				now,
			); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE transcript_pricing_metadata
			SET price_table_version = ?, updated_at = ?
			WHERE singleton = 1`,
			pricing.Version,
			now,
		); err != nil {
			return errors.New("update transcript pricing metadata")
		}
		return nil
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit transcript repricing")
	}
	return nil
}

func transcriptSessionMetadataTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionKey string,
) (transcript.Session, bool, error) {
	var result transcript.Session
	err := tx.QueryRowContext(ctx, `
		SELECT session_key, agent, native_session_id, project_path,
			git_remote_url, project_identity, coverage
		FROM transcript_sessions
		WHERE session_key = ?`,
		sessionKey,
	).Scan(
		&result.SessionKey,
		&result.Agent,
		&result.NativeSessionID,
		&result.ProjectPath,
		&result.GitRemoteURL,
		&result.ProjectIdentity,
		&result.Coverage,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return transcript.Session{}, false, nil
	}
	if err != nil {
		return transcript.Session{}, false, errors.New(
			"read transcript session metadata",
		)
	}
	return result, true, nil
}

func (s *Store) QueryTranscriptTurns(
	ctx context.Context,
	sessionKey string,
	limit int,
) ([]transcript.Turn, error) {
	if sessionKey == "" || len(sessionKey) > 256 {
		return nil, errors.New("invalid transcript turn query")
	}
	if limit <= 0 {
		limit = defaultTranscriptTurnLimit
	}
	if limit > maxTranscriptTurnLimit {
		limit = maxTranscriptTurnLimit
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT turn_id, source_record_key, session_key, turn_index,
			occurred_at, role, tool_name, model, input_tokens, output_tokens,
			cache_read_tokens, cache_write_tokens, cost_usd, payload,
			payload_encoding
		FROM transcript_turns
		WHERE session_key = ?
		ORDER BY turn_index, occurred_at, turn_id
		LIMIT ?`,
		sessionKey,
		limit,
	)
	if err != nil {
		return nil, errors.New("query transcript turns")
	}
	defer rows.Close()

	result := make([]transcript.Turn, 0)
	for rows.Next() {
		turn, err := s.scanTranscriptTurn(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, turn)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query transcript turns")
	}
	return result, nil
}

func (s *Store) GetTranscriptSession(
	ctx context.Context,
	sessionKey string,
) (transcript.Session, error) {
	if sessionKey == "" || len(sessionKey) > 256 {
		return transcript.Session{}, errors.New("invalid transcript session")
	}
	return scanTranscriptSession(s.db.QueryRowContext(ctx, `
		SELECT session_key, agent, native_session_id, project_path,
			git_remote_url, project_identity, started_at, ended_at,
			wall_duration_ms, total_input_tokens, total_output_tokens,
			total_tokens, total_cache_read_tokens, total_cache_write_tokens,
			total_cost_usd, turn_count, user_turn_count, assistant_turn_count,
			tool_call_count, tool_result_count, system_turn_count,
			compaction_summary_count, coverage
		FROM transcript_sessions
		WHERE session_key = ?`,
		sessionKey,
	))
}

func (s *Store) QueryTranscriptSessions(
	ctx context.Context,
	query transcript.SessionQuery,
) ([]transcript.Session, error) {
	if query.Limit <= 0 {
		query.Limit = defaultTranscriptSessionLimit
	}
	if query.Limit > maxTranscriptSessionLimit {
		query.Limit = maxTranscriptSessionLimit
	}
	if len(query.Agent) > 128 ||
		len(query.ProjectIdentity) > 4096 ||
		query.Coverage != "" && !validTranscriptCoverage(query.Coverage) {
		return nil, errors.New("invalid transcript session query")
	}
	clauses := []string{"1 = 1"}
	args := make([]any, 0, 4)
	if query.Agent != "" {
		clauses = append(clauses, "agent = ?")
		args = append(args, query.Agent)
	}
	if query.ProjectIdentity != "" {
		clauses = append(clauses, "project_identity = ?")
		args = append(args, query.ProjectIdentity)
	}
	if query.Coverage != "" {
		clauses = append(clauses, "coverage = ?")
		args = append(args, query.Coverage)
	}
	args = append(args, query.Limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_key, agent, native_session_id, project_path,
			git_remote_url, project_identity, started_at, ended_at,
			wall_duration_ms, total_input_tokens, total_output_tokens,
			total_tokens, total_cache_read_tokens, total_cache_write_tokens,
			total_cost_usd, turn_count, user_turn_count, assistant_turn_count,
			tool_call_count, tool_result_count, system_turn_count,
			compaction_summary_count, coverage
		FROM transcript_sessions
		WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY COALESCE(ended_at, started_at, updated_at) DESC, session_key ASC
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, errors.New("query transcript sessions")
	}
	defer rows.Close()
	result := make([]transcript.Session, 0)
	for rows.Next() {
		session, err := scanTranscriptSession(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query transcript sessions")
	}
	return result, nil
}

// QueryTranscriptSessionsByKeys reads transcript session metadata for up to
// 100 canonical session keys and returns it keyed by session. Sessions
// without a transcript are simply absent.
func (s *Store) QueryTranscriptSessionsByKeys(
	ctx context.Context,
	sessionKeys []string,
) (map[string]transcript.Session, error) {
	result := make(map[string]transcript.Session, len(sessionKeys))
	if len(sessionKeys) == 0 {
		return result, nil
	}
	if len(sessionKeys) > 100 {
		return nil, errors.New("transcript session key query exceeds limit")
	}
	placeholders := make([]string, 0, len(sessionKeys))
	args := make([]any, 0, len(sessionKeys))
	for _, key := range sessionKeys {
		if key == "" || len(key) > 256 {
			return nil, errors.New("invalid transcript session key")
		}
		placeholders = append(placeholders, "?")
		args = append(args, key)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_key, agent, native_session_id, project_path,
			git_remote_url, project_identity, started_at, ended_at,
			wall_duration_ms, total_input_tokens, total_output_tokens,
			total_tokens, total_cache_read_tokens, total_cache_write_tokens,
			total_cost_usd, turn_count, user_turn_count, assistant_turn_count,
			tool_call_count, tool_result_count, system_turn_count,
			compaction_summary_count, coverage
		FROM transcript_sessions
		WHERE session_key IN (`+strings.Join(placeholders, ", ")+`)`,
		args...,
	)
	if err != nil {
		return nil, errors.New("query transcript sessions by key")
	}
	defer rows.Close()
	for rows.Next() {
		session, err := scanTranscriptSession(rows)
		if err != nil {
			return nil, err
		}
		result[session.SessionKey] = session
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query transcript sessions by key")
	}
	return result, nil
}

func (s *Store) ListTranscriptProjectIdentities(
	ctx context.Context,
	limit int,
) ([]string, error) {
	if limit <= 0 {
		limit = defaultTranscriptProjectLimit
	}
	if limit > maxTranscriptProjectLimit {
		return nil, errors.New("invalid transcript project identity limit")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT TRIM(project_identity)
		FROM transcript_sessions
		WHERE TRIM(project_identity) <> ''
		ORDER BY 1 ASC
		LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, errors.New("list transcript project identities")
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var projectIdentity string
		if err := rows.Scan(&projectIdentity); err != nil {
			return nil, errors.New("read transcript project identity")
		}
		result = append(result, projectIdentity)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("list transcript project identities")
	}
	return result, nil
}

func (s *Store) TranscriptCoverage(
	ctx context.Context,
) (transcript.CoverageCounts, error) {
	var result transcript.CoverageCounts
	err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(DISTINCT session_key) FROM events),
			(SELECT COUNT(*) FROM transcript_sessions),
			(
				SELECT COUNT(DISTINCT e.session_key)
				FROM events e
				JOIN transcript_sessions ts ON ts.session_key = e.session_key
				WHERE ts.coverage IN ('complete', 'live')
					AND EXISTS (
					SELECT 1 FROM transcript_turns tt
					WHERE tt.session_key = e.session_key
				)
			),
			(
				SELECT COUNT(DISTINCT e.session_key)
				FROM events e
				WHERE NOT EXISTS (
					SELECT 1 FROM transcript_turns tt
					WHERE tt.session_key = e.session_key
				)
			),
			(
				SELECT COUNT(*) FROM transcript_sessions
				WHERE coverage = 'complete'
			),
			(
				SELECT COUNT(*) FROM transcript_sessions
				WHERE coverage = 'live'
			),
			(
				SELECT COUNT(DISTINCT e.session_key)
				FROM events e
				JOIN transcript_sessions ts ON ts.session_key = e.session_key
				WHERE ts.coverage = 'complete'
					AND EXISTS (
						SELECT 1 FROM transcript_turns tt
						WHERE tt.session_key = e.session_key
					)
			),
			(
				SELECT COUNT(DISTINCT e.session_key)
				FROM events e
				JOIN transcript_sessions ts ON ts.session_key = e.session_key
				WHERE ts.coverage = 'live'
					AND EXISTS (
						SELECT 1 FROM transcript_turns tt
						WHERE tt.session_key = e.session_key
					)
			),
			(
				SELECT COUNT(DISTINCT e.session_key)
				FROM events e
				JOIN transcript_sessions ts ON ts.session_key = e.session_key
				WHERE ts.coverage = 'partial'
					AND EXISTS (
						SELECT 1 FROM transcript_turns tt
						WHERE tt.session_key = e.session_key
					)
			),
			(
				SELECT COUNT(DISTINCT e.session_key)
				FROM events e
				WHERE NOT EXISTS (
					SELECT 1 FROM transcript_turns tt
					WHERE tt.session_key = e.session_key
				)
			),
			(
				SELECT COUNT(*)
				FROM transcript_sessions ts
				WHERE EXISTS (
					SELECT 1 FROM transcript_turns tt
					WHERE tt.session_key = ts.session_key
				)
					AND NOT EXISTS (
						SELECT 1 FROM events e
						WHERE e.session_key = ts.session_key
					)
			)`,
	).Scan(
		&result.CanonicalSessions,
		&result.TranscriptSessions,
		&result.WithTranscript,
		&result.WithoutTranscript,
		&result.Complete,
		&result.Live,
		&result.CanonicalCompleteWithTranscript,
		&result.CanonicalLiveWithTranscript,
		&result.Partial,
		&result.CanonicalWithoutTranscript,
		&result.TranscriptOnly,
	)
	if err != nil {
		return transcript.CoverageCounts{}, errors.New("read transcript coverage")
	}
	result.CanonicalCompleteOrLiveWithTranscript =
		result.CanonicalCompleteWithTranscript + result.CanonicalLiveWithTranscript
	result.CanonicalPartial = result.Partial
	return result, nil
}

func validateTranscriptSession(session transcript.Session) error {
	if session.SessionKey == "" || len(session.SessionKey) > 256 ||
		session.Agent == "" || len(session.Agent) > 128 ||
		session.NativeSessionID == "" || len(session.NativeSessionID) > 512 ||
		session.ProjectPath == "" || len(session.ProjectPath) > 4096 ||
		len(session.GitRemoteURL) > 4096 ||
		session.ProjectIdentity == "" || len(session.ProjectIdentity) > 4096 {
		return errors.New("invalid transcript session")
	}
	if !validTranscriptCoverage(session.Coverage) {
		return errors.New("invalid transcript session coverage")
	}
	return nil
}

func validTranscriptCoverage(value transcript.SessionCoverage) bool {
	switch value {
	case transcript.CoverageComplete, transcript.CoveragePartial, transcript.CoverageLive:
		return true
	default:
		return false
	}
}

func validateTranscriptTurn(sessionKey string, turn transcript.Turn) error {
	if turn.TurnID == "" || len(turn.TurnID) > 512 ||
		turn.SourceRecordKey == "" || len(turn.SourceRecordKey) > 1024 ||
		turn.SessionKey != sessionKey ||
		turn.TurnIndex < 0 ||
		turn.OccurredAt.IsZero() ||
		!validTranscriptRoles[turn.Role] ||
		len(turn.ToolName) > 512 ||
		len(turn.Model) > 512 ||
		turn.Payload.JSONLByteOffset < 0 ||
		len(turn.Payload.ToolInput) > 0 && !json.Valid(turn.Payload.ToolInput) ||
		!validNullableCount(turn.InputTokens) ||
		!validNullableCount(turn.OutputTokens) ||
		!validNullableCount(turn.CacheReadTokens) ||
		!validNullableCount(turn.CacheWriteTokens) ||
		!validNullableCost(turn.CostUSD) {
		return errors.New("invalid transcript turn")
	}
	return nil
}

func validNullableCount(value *int64) bool {
	return value == nil || *value >= 0
}

func validNullableCost(value *float64) bool {
	return value == nil || *value >= 0 && !math.IsNaN(*value) && !math.IsInf(*value, 0)
}

func upsertTranscriptSessionMetadataTx(
	ctx context.Context,
	tx *sql.Tx,
	session transcript.Session,
	now string,
) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO transcript_sessions (
			session_key, agent, native_session_id, project_path, git_remote_url,
			project_identity, coverage, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_key) DO UPDATE SET
			agent = excluded.agent,
			native_session_id = excluded.native_session_id,
			project_path = excluded.project_path,
			git_remote_url = excluded.git_remote_url,
			project_identity = excluded.project_identity,
			coverage = excluded.coverage,
			updated_at = excluded.updated_at`,
		session.SessionKey,
		session.Agent,
		session.NativeSessionID,
		session.ProjectPath,
		session.GitRemoteURL,
		session.ProjectIdentity,
		session.Coverage,
		now,
		now,
	)
	if err != nil {
		return errors.New("upsert transcript session metadata")
	}
	return nil
}

func recomputeTranscriptSessionTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionKey string,
	now string,
) error {
	var startedAt, endedAt sql.NullString
	var inputTokens, outputTokens, totalTokens sql.NullInt64
	var cacheReadTokens, cacheWriteTokens sql.NullInt64
	var costUSD sql.NullFloat64
	var unpricedBillableTurns int
	var turnCount, userTurns, assistantTurns, toolCalls, toolResults int
	var systemTurns, compactionSummaries int
	if err := tx.QueryRowContext(ctx, `
		SELECT MIN(occurred_at), MAX(occurred_at),
			SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens),
			SUM(cache_write_tokens), SUM(cost_usd),
			COALESCE(SUM(
				CASE
					WHEN cost_usd IS NULL
						AND (
							COALESCE(input_tokens, 0) > 0
							OR COALESCE(output_tokens, 0) > 0
							OR COALESCE(cache_read_tokens, 0) > 0
							OR COALESCE(cache_write_tokens, 0) > 0
						)
					THEN 1
					ELSE 0
				END
			), 0),
			COUNT(*),
			COALESCE(SUM(CASE WHEN role = 'user' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN role = 'assistant' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN role = 'tool_call' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN role = 'tool_result' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN role = 'system' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(
				CASE WHEN role = 'compaction_summary' THEN 1 ELSE 0 END
			), 0)
		FROM transcript_turns
		WHERE session_key = ?`,
		sessionKey,
	).Scan(
		&startedAt,
		&endedAt,
		&inputTokens,
		&outputTokens,
		&cacheReadTokens,
		&cacheWriteTokens,
		&costUSD,
		&unpricedBillableTurns,
		&turnCount,
		&userTurns,
		&assistantTurns,
		&toolCalls,
		&toolResults,
		&systemTurns,
		&compactionSummaries,
	); err != nil {
		return errors.New("aggregate transcript session")
	}
	var wallDurationMS int64
	if startedAt.Valid && endedAt.Valid {
		started, err := time.Parse(time.RFC3339Nano, startedAt.String)
		if err != nil {
			return errors.New("decode transcript session start")
		}
		ended, err := time.Parse(time.RFC3339Nano, endedAt.String)
		if err != nil {
			return errors.New("decode transcript session end")
		}
		wallDurationMS = ended.Sub(started).Milliseconds()
		if wallDurationMS < 0 {
			return errors.New("invalid transcript session duration")
		}
	}
	if inputTokens.Valid || outputTokens.Valid ||
		cacheReadTokens.Valid || cacheWriteTokens.Valid {
		totalTokens.Valid = true
		totalTokens.Int64 = inputTokens.Int64 +
			outputTokens.Int64 +
			cacheReadTokens.Int64 +
			cacheWriteTokens.Int64
	}
	if unpricedBillableTurns > 0 {
		costUSD = sql.NullFloat64{}
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE transcript_sessions
		SET started_at = ?, ended_at = ?, wall_duration_ms = ?,
			total_input_tokens = ?, total_output_tokens = ?,
			total_tokens = ?, total_cache_read_tokens = ?,
			total_cache_write_tokens = ?, total_cost_usd = ?, turn_count = ?,
			user_turn_count = ?, assistant_turn_count = ?, tool_call_count = ?,
			tool_result_count = ?, system_turn_count = ?,
			compaction_summary_count = ?, updated_at = ?
		WHERE session_key = ?`,
		nullableSQLString(startedAt),
		nullableSQLString(endedAt),
		wallDurationMS,
		nullableSQLInt64(inputTokens),
		nullableSQLInt64(outputTokens),
		nullableSQLInt64(totalTokens),
		nullableSQLInt64(cacheReadTokens),
		nullableSQLInt64(cacheWriteTokens),
		nullableSQLFloat64(costUSD),
		turnCount,
		userTurns,
		assistantTurns,
		toolCalls,
		toolResults,
		systemTurns,
		compactionSummaries,
		now,
		sessionKey,
	)
	if err != nil {
		return errors.New("refresh transcript session aggregate")
	}
	return nil
}

func reindexTranscriptSessionTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionKey string,
) error {
	_, err := tx.ExecContext(ctx, reindexTranscriptSessionSQL, sessionKey)
	if err != nil {
		return errors.New("reindex transcript session")
	}
	return nil
}

func (s *Store) scanTranscriptTurn(row rowScanner) (transcript.Turn, error) {
	var result transcript.Turn
	var occurredAt, encoding string
	var inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens sql.NullInt64
	var costUSD sql.NullFloat64
	var payload []byte
	if err := row.Scan(
		&result.TurnID,
		&result.SourceRecordKey,
		&result.SessionKey,
		&result.TurnIndex,
		&occurredAt,
		&result.Role,
		&result.ToolName,
		&result.Model,
		&inputTokens,
		&outputTokens,
		&cacheReadTokens,
		&cacheWriteTokens,
		&costUSD,
		&payload,
		&encoding,
	); err != nil {
		return transcript.Turn{}, errors.New("read transcript turn")
	}
	var err error
	result.OccurredAt, err = time.Parse(time.RFC3339Nano, occurredAt)
	if err != nil {
		return transcript.Turn{}, errors.New("decode transcript turn timestamp")
	}
	result.InputTokens = nullableInt64Pointer(inputTokens)
	result.OutputTokens = nullableInt64Pointer(outputTokens)
	result.CacheReadTokens = nullableInt64Pointer(cacheReadTokens)
	result.CacheWriteTokens = nullableInt64Pointer(cacheWriteTokens)
	result.CostUSD = nullableFloat64Pointer(costUSD)
	payload, err = s.cipher.open(
		"transcript_turn",
		result.TurnID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return transcript.Turn{}, err
	}
	if err := json.Unmarshal(payload, &result.Payload); err != nil {
		return transcript.Turn{}, errors.New("decode transcript turn payload")
	}
	return result, nil
}

func scanTranscriptSession(row rowScanner) (transcript.Session, error) {
	var result transcript.Session
	var startedAt, endedAt sql.NullString
	var inputTokens, outputTokens, totalTokens sql.NullInt64
	var cacheReadTokens, cacheWriteTokens sql.NullInt64
	var costUSD sql.NullFloat64
	err := row.Scan(
		&result.SessionKey,
		&result.Agent,
		&result.NativeSessionID,
		&result.ProjectPath,
		&result.GitRemoteURL,
		&result.ProjectIdentity,
		&startedAt,
		&endedAt,
		&result.WallDurationMS,
		&inputTokens,
		&outputTokens,
		&totalTokens,
		&cacheReadTokens,
		&cacheWriteTokens,
		&costUSD,
		&result.TurnCount,
		&result.UserTurnCount,
		&result.AssistantTurnCount,
		&result.ToolCallCount,
		&result.ToolResultCount,
		&result.SystemTurnCount,
		&result.CompactionSummaryCount,
		&result.Coverage,
	)
	if err != nil {
		return transcript.Session{}, err
	}
	if startedAt.Valid {
		result.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt.String)
		if err != nil {
			return transcript.Session{}, errors.New("decode transcript session start")
		}
	}
	if endedAt.Valid {
		result.EndedAt, err = time.Parse(time.RFC3339Nano, endedAt.String)
		if err != nil {
			return transcript.Session{}, errors.New("decode transcript session end")
		}
	}
	result.TotalInputTokens = nullableInt64Pointer(inputTokens)
	result.TotalOutputTokens = nullableInt64Pointer(outputTokens)
	result.TotalTokens = nullableInt64Pointer(totalTokens)
	result.TotalCacheReadTokens = nullableInt64Pointer(cacheReadTokens)
	result.TotalCacheWriteTokens = nullableInt64Pointer(cacheWriteTokens)
	result.TotalCostUSD = nullableFloat64Pointer(costUSD)
	return result, nil
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableFloat64(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableSQLInt64(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func nullableSQLFloat64(value sql.NullFloat64) any {
	if !value.Valid {
		return nil
	}
	return value.Float64
}

func nullableFloat64Pointer(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	result := value.Float64
	return &result
}
