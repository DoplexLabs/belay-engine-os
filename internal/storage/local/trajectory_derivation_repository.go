package local

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	trajectoryderive "github.com/DoplexLabs/belay-engine/internal/trajectory/derive"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	maxTrajectoryDerivationPayloadBytes = 256 << 10
	maxTrajectoryDerivationDiagnostics  = trajectoryderive.MaxDiagnostics
	maxTrajectoryDerivationBatch        = 100
	maxTrajectorySessionScan            = 10000
	maxTrajectoryDerivationAttempts     = 16
	partialTrajectoryRetryDelay         = 30 * time.Second
	failedTrajectoryRetryBaseDelay      = 5 * time.Second
	failedTrajectoryRetryMaxDelay       = 10 * time.Minute
)

var (
	ErrTrajectoryDerivationNotFound = errors.New("trajectory derivation state was not found")
	ErrTrajectoryDerivationStale    = errors.New("trajectory derivation input is stale")
)

type TrajectoryDerivationStatus string

const (
	TrajectoryDerivationComplete TrajectoryDerivationStatus = "complete"
	TrajectoryDerivationPartial  TrajectoryDerivationStatus = "partial"
	TrajectoryDerivationFailed   TrajectoryDerivationStatus = "failed"
)

type TrajectoryDerivationClaim struct {
	SessionKey            string `json:"session_key"`
	DerivationVersion     string `json:"derivation_version"`
	TranscriptFingerprint string `json:"transcript_fingerprint"`
	CanonicalFingerprint  string `json:"canonical_fingerprint"`
}

type TrajectoryDerivationState struct {
	TrajectoryDerivationClaim
	Status       TrajectoryDerivationStatus    `json:"status"`
	Retryable    bool                          `json:"retryable"`
	AttemptCount int                           `json:"attempt_count"`
	RetryAt      *time.Time                    `json:"retry_at,omitempty"`
	FailureCode  string                        `json:"failure_code,omitempty"`
	AttemptedAt  time.Time                     `json:"attempted_at"`
	CompletedAt  *time.Time                    `json:"completed_at,omitempty"`
	Coverage     trajectoryderive.Coverage     `json:"coverage"`
	Diagnostics  []trajectoryderive.Diagnostic `json:"diagnostics,omitempty"`
}

type trajectoryInputIndexes struct {
	SessionKey              string
	Agent                   string
	NativeSessionID         string
	ProjectPath             string
	GitRemoteURL            string
	ProjectIdentity         string
	StartedAt               sql.NullString
	EndedAt                 sql.NullString
	WallDurationMS          int64
	TotalInputTokens        sql.NullInt64
	TotalOutputTokens       sql.NullInt64
	TotalTokens             sql.NullInt64
	TotalCacheReadTokens    sql.NullInt64
	TotalCacheWriteTokens   sql.NullInt64
	TotalCostUSD            sql.NullFloat64
	TurnCount               int
	UserTurnCount           int
	AssistantTurnCount      int
	ToolCallCount           int
	ToolResultCount         int
	SystemTurnCount         int
	CompactionSummaryCount  int
	Coverage                string
	RetainedTurnCount       int
	LatestTurnCreatedAt     string
	SealedTurnBytes         int64
	CanonicalEventCount     int
	CanonicalEventWatermark int64
}

type transcriptFingerprintMaterial struct {
	SessionKey             string
	Agent                  string
	NativeSessionID        string
	ProjectPath            string
	GitRemoteURL           string
	ProjectIdentity        string
	StartedAt              sql.NullString
	EndedAt                sql.NullString
	WallDurationMS         int64
	TotalInputTokens       sql.NullInt64
	TotalOutputTokens      sql.NullInt64
	TotalTokens            sql.NullInt64
	TotalCacheReadTokens   sql.NullInt64
	TotalCacheWriteTokens  sql.NullInt64
	TotalCostUSD           sql.NullFloat64
	TurnCount              int
	UserTurnCount          int
	AssistantTurnCount     int
	ToolCallCount          int
	ToolResultCount        int
	SystemTurnCount        int
	CompactionSummaryCount int
	Coverage               string
	RetainedTurnCount      int
	LatestTurnCreatedAt    string
	SealedTurnBytes        int64
}

type canonicalFingerprintMaterial struct {
	SessionKey     string
	EventCount     int
	EventWatermark int64
}

func (s *Store) ListDirtyTrajectorySessions(
	ctx context.Context,
	derivationVersion string,
	limit int,
) ([]TrajectoryDerivationClaim, error) {
	if err := validateDerivationVersion(derivationVersion); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = maxTrajectoryDerivationBatch
	}
	if limit > maxTrajectoryDerivationBatch {
		return nil, errors.New("trajectory derivation batch exceeds safety limit")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, errors.New("begin trajectory derivation dirty-session query")
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, trajectoryInputSelectSQL+`
		ORDER BY ts.session_key
		LIMIT ?`,
		maxTrajectorySessionScan+1,
	)
	if err != nil {
		return nil, errors.New("query trajectory derivation inputs")
	}
	defer rows.Close()
	inputs := make([]trajectoryInputIndexes, 0)
	for rows.Next() {
		input, err := scanTrajectoryInputIndexes(rows)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, input)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query trajectory derivation inputs")
	}
	if err := rows.Close(); err != nil {
		return nil, errors.New("finish trajectory derivation input query")
	}
	if len(inputs) > maxTrajectorySessionScan {
		return nil, errors.New("trajectory session scan exceeds safety limit")
	}

	result := make([]TrajectoryDerivationClaim, 0, limit)
	now := s.nowUTC()
	for _, input := range inputs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		claim, err := trajectoryClaim(input, derivationVersion)
		if err != nil {
			return nil, err
		}
		state, err := s.getTrajectoryDerivationStateTx(
			ctx,
			tx,
			input.SessionKey,
			derivationVersion,
		)
		switch {
		case errors.Is(err, ErrTrajectoryDerivationNotFound):
			result = append(result, claim)
		case err != nil:
			return nil, err
		case state.TranscriptFingerprint != claim.TranscriptFingerprint ||
			state.CanonicalFingerprint != claim.CanonicalFingerprint:
			result = append(result, claim)
		case state.Retryable &&
			(state.Status == TrajectoryDerivationPartial ||
				state.Status == TrajectoryDerivationFailed) &&
			state.RetryAt != nil &&
			!state.RetryAt.After(now):
			result = append(result, claim)
		}
		if len(result) == limit {
			break
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, errors.New("finish trajectory derivation dirty-session query")
	}
	return result, nil
}

func (s *Store) MarkTrajectoryDerivationCurrent(
	ctx context.Context,
	claim TrajectoryDerivationClaim,
	coverage trajectoryderive.Coverage,
	diagnostics []trajectoryderive.Diagnostic,
) (TrajectoryDerivationState, error) {
	status := TrajectoryDerivationPartial
	if coverage.FullyDerived {
		status = TrajectoryDerivationComplete
	}
	now := s.nowUTC()
	completedAt := now
	state := TrajectoryDerivationState{
		TrajectoryDerivationClaim: claim,
		Status:                    status,
		Retryable:                 status == TrajectoryDerivationPartial,
		AttemptedAt:               now,
		CompletedAt:               &completedAt,
		Coverage:                  coverage,
		Diagnostics: append(
			[]trajectoryderive.Diagnostic(nil),
			diagnostics...,
		),
	}
	return s.persistTrajectoryDerivationState(ctx, state)
}

func (s *Store) RecordTrajectoryDerivationFailure(
	ctx context.Context,
	claim TrajectoryDerivationClaim,
	failureCode string,
	retryable bool,
) (TrajectoryDerivationState, error) {
	state := TrajectoryDerivationState{
		TrajectoryDerivationClaim: claim,
		Status:                    TrajectoryDerivationFailed,
		Retryable:                 retryable,
		FailureCode:               failureCode,
		AttemptedAt:               s.nowUTC(),
	}
	return s.persistTrajectoryDerivationState(ctx, state)
}

func (s *Store) GetTrajectoryDerivationState(
	ctx context.Context,
	sessionKey string,
	derivationVersion string,
) (TrajectoryDerivationState, error) {
	if err := validateStorageIdentifier("trajectory derivation session key", sessionKey); err != nil {
		return TrajectoryDerivationState{}, err
	}
	if err := validateDerivationVersion(derivationVersion); err != nil {
		return TrajectoryDerivationState{}, err
	}
	return s.getTrajectoryDerivationStateTx(
		ctx,
		s.db,
		sessionKey,
		derivationVersion,
	)
}

// IsTrajectoryDerivationStateCurrent verifies that a persisted state is bound
// to the exact current transcript and canonical-event fingerprints.
func (s *Store) IsTrajectoryDerivationStateCurrent(
	ctx context.Context,
	state TrajectoryDerivationState,
) (bool, error) {
	if err := validateStorageIdentifier(
		"trajectory derivation session key",
		state.SessionKey,
	); err != nil {
		return false, err
	}
	if err := validateDerivationVersion(state.DerivationVersion); err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, errors.New(
			"begin trajectory derivation freshness check",
		)
	}
	defer tx.Rollback()
	input, err := readTrajectoryInputIndexesTx(ctx, tx, state.SessionKey)
	if errors.Is(err, ErrTrajectoryDerivationStale) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	current, err := trajectoryClaim(input, state.DerivationVersion)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, errors.New(
			"finish trajectory derivation freshness check",
		)
	}
	return current.TranscriptFingerprint == state.TranscriptFingerprint &&
		current.CanonicalFingerprint == state.CanonicalFingerprint, nil
}

func (s *Store) persistTrajectoryDerivationState(
	ctx context.Context,
	state TrajectoryDerivationState,
) (TrajectoryDerivationState, error) {
	if err := validateTrajectoryDerivationStateBase(state); err != nil {
		return TrajectoryDerivationState{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TrajectoryDerivationState{}, errors.New("begin trajectory derivation persistence")
	}
	defer tx.Rollback()
	current, err := readTrajectoryInputIndexesTx(ctx, tx, state.SessionKey)
	if err != nil {
		return TrajectoryDerivationState{}, err
	}
	currentClaim, err := trajectoryClaim(current, state.DerivationVersion)
	if err != nil {
		return TrajectoryDerivationState{}, err
	}
	if currentClaim.TranscriptFingerprint != state.TranscriptFingerprint ||
		currentClaim.CanonicalFingerprint != state.CanonicalFingerprint {
		return TrajectoryDerivationState{}, ErrTrajectoryDerivationStale
	}
	var previous *TrajectoryDerivationState
	existing, err := s.getTrajectoryDerivationStateTx(
		ctx,
		tx,
		state.SessionKey,
		state.DerivationVersion,
	)
	switch {
	case errors.Is(err, ErrTrajectoryDerivationNotFound):
	case err != nil:
		return TrajectoryDerivationState{}, err
	default:
		previous = &existing
	}
	scheduleTrajectoryDerivationRetry(&state, previous)
	if err := validateTrajectoryDerivationState(state); err != nil {
		return TrajectoryDerivationState{}, err
	}
	recordID := trajectoryDerivationRecordID(
		state.SessionKey,
		state.DerivationVersion,
	)
	_, payload, err := s.sealDomainJSON(
		"trajectory_derivation_state",
		recordID,
		"payload",
		state,
		maxTrajectoryDerivationPayloadBytes,
	)
	if err != nil {
		return TrajectoryDerivationState{}, err
	}
	now := formatProjectionTime(state.AttemptedAt)
	err = withMutationTx(ctx, tx, mutationTrajectoryDerivation, func() error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO trajectory_derivation_state (
				session_key, derivation_version, status,
				transcript_fingerprint, canonical_fingerprint, retryable,
				attempt_count, retry_at, failure_code, attempted_at,
				completed_at, payload, payload_encoding, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_key, derivation_version) DO UPDATE SET
				status = excluded.status,
				transcript_fingerprint = excluded.transcript_fingerprint,
				canonical_fingerprint = excluded.canonical_fingerprint,
				retryable = excluded.retryable,
				attempt_count = excluded.attempt_count,
				retry_at = excluded.retry_at,
				failure_code = excluded.failure_code,
				attempted_at = excluded.attempted_at,
				completed_at = excluded.completed_at,
				payload = excluded.payload,
				updated_at = excluded.updated_at`,
			state.SessionKey,
			state.DerivationVersion,
			state.Status,
			state.TranscriptFingerprint,
			state.CanonicalFingerprint,
			boolInt(state.Retryable),
			state.AttemptCount,
			nullableTrajectoryRetryAt(state.RetryAt),
			nullable(state.FailureCode),
			now,
			nullableTrajectoryCompletedAt(state.CompletedAt),
			payload,
			payloadEncodingAESGCM,
			now,
			now,
		)
		if err != nil {
			return fmt.Errorf("persist trajectory derivation state: %w", err)
		}
		return nil
	})
	if err != nil {
		return TrajectoryDerivationState{}, err
	}
	if err := tx.Commit(); err != nil {
		return TrajectoryDerivationState{}, errors.New("commit trajectory derivation persistence")
	}
	return state, nil
}

type trajectoryStateQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) getTrajectoryDerivationStateTx(
	ctx context.Context,
	query trajectoryStateQuerier,
	sessionKey string,
	derivationVersion string,
) (TrajectoryDerivationState, error) {
	state, err := s.scanTrajectoryDerivationState(query.QueryRowContext(
		ctx,
		trajectoryDerivationStateSelectSQL+`
		WHERE session_key = ? AND derivation_version = ?`,
		sessionKey,
		derivationVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return TrajectoryDerivationState{}, ErrTrajectoryDerivationNotFound
	}
	return state, err
}

const trajectoryDerivationStateSelectSQL = `
	SELECT session_key, derivation_version, status, transcript_fingerprint,
		canonical_fingerprint, retryable, attempt_count, retry_at,
		failure_code, attempted_at, completed_at, payload, payload_encoding
	FROM trajectory_derivation_state `

func (s *Store) scanTrajectoryDerivationState(
	scanner rowScanner,
) (TrajectoryDerivationState, error) {
	var indexed TrajectoryDerivationState
	var status, attemptedAtValue, encoding string
	var retryable int
	var retryAtValue, failureCode, completedAtValue sql.NullString
	var payload []byte
	if err := scanner.Scan(
		&indexed.SessionKey,
		&indexed.DerivationVersion,
		&status,
		&indexed.TranscriptFingerprint,
		&indexed.CanonicalFingerprint,
		&retryable,
		&indexed.AttemptCount,
		&retryAtValue,
		&failureCode,
		&attemptedAtValue,
		&completedAtValue,
		&payload,
		&encoding,
	); err != nil {
		return TrajectoryDerivationState{}, err
	}
	if err := validateSealedPayloadSize(
		"trajectory derivation state",
		payload,
		maxTrajectoryDerivationPayloadBytes,
	); err != nil {
		return TrajectoryDerivationState{}, err
	}
	plaintext, err := s.cipher.open(
		"trajectory_derivation_state",
		trajectoryDerivationRecordID(
			indexed.SessionKey,
			indexed.DerivationVersion,
		),
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return TrajectoryDerivationState{}, err
	}
	var state TrajectoryDerivationState
	if err := json.Unmarshal(plaintext, &state); err != nil {
		return TrajectoryDerivationState{}, errors.New("decode trajectory derivation state payload")
	}
	attemptedAt, attemptedErr := parseProjectionTime(attemptedAtValue)
	var retryAt *time.Time
	var retryErr error
	if retryAtValue.Valid {
		value, err := parseProjectionTime(retryAtValue.String)
		retryErr = err
		retryAt = &value
	}
	var completedAt *time.Time
	var completedErr error
	if completedAtValue.Valid {
		value, err := parseProjectionTime(completedAtValue.String)
		completedErr = err
		completedAt = &value
	}
	if attemptedErr != nil || retryErr != nil || completedErr != nil ||
		state.SessionKey != indexed.SessionKey ||
		state.DerivationVersion != indexed.DerivationVersion ||
		string(state.Status) != status ||
		state.TranscriptFingerprint != indexed.TranscriptFingerprint ||
		state.CanonicalFingerprint != indexed.CanonicalFingerprint ||
		state.Retryable != (retryable == 1) ||
		state.AttemptCount != indexed.AttemptCount ||
		!equalTrajectoryTimePointer(state.RetryAt, retryAt) ||
		state.FailureCode != failureCode.String ||
		!state.AttemptedAt.Equal(attemptedAt) ||
		!equalTrajectoryTimePointer(state.CompletedAt, completedAt) {
		return TrajectoryDerivationState{}, errors.New(
			"trajectory derivation index does not match payload",
		)
	}
	if err := validateTrajectoryDerivationState(state); err != nil {
		return TrajectoryDerivationState{}, errors.New(
			"invalid persisted trajectory derivation state",
		)
	}
	return state, nil
}

const trajectoryInputSelectSQL = `
	SELECT
		ts.session_key, ts.agent, ts.native_session_id, ts.project_path,
		ts.git_remote_url, ts.project_identity, ts.started_at, ts.ended_at,
		ts.wall_duration_ms, ts.total_input_tokens, ts.total_output_tokens,
		ts.total_tokens, ts.total_cache_read_tokens,
		ts.total_cache_write_tokens, ts.total_cost_usd, ts.turn_count,
		ts.user_turn_count, ts.assistant_turn_count, ts.tool_call_count,
		ts.tool_result_count, ts.system_turn_count,
		ts.compaction_summary_count, ts.coverage,
		(SELECT COUNT(*) FROM transcript_turns tt
			WHERE tt.session_key = ts.session_key),
		COALESCE((
			SELECT MAX(tt.created_at) FROM transcript_turns tt
			WHERE tt.session_key = ts.session_key
		), ''),
		COALESCE((
			SELECT SUM(length(tt.payload)) FROM transcript_turns tt
			WHERE tt.session_key = ts.session_key
		), 0),
		(SELECT COUNT(*) FROM events e
			WHERE e.session_key = ts.session_key),
		COALESCE((
			SELECT MAX(ero.sequence)
			FROM events e
			JOIN event_read_order ero ON ero.event_id = e.event_id
			WHERE e.session_key = ts.session_key
		), 0)
	FROM transcript_sessions ts `

func readTrajectoryInputIndexesTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionKey string,
) (trajectoryInputIndexes, error) {
	input, err := scanTrajectoryInputIndexes(tx.QueryRowContext(
		ctx,
		trajectoryInputSelectSQL+` WHERE ts.session_key = ?`,
		sessionKey,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return trajectoryInputIndexes{}, ErrTrajectoryDerivationStale
	}
	return input, err
}

func scanTrajectoryInputIndexes(scanner rowScanner) (trajectoryInputIndexes, error) {
	var input trajectoryInputIndexes
	if err := scanner.Scan(
		&input.SessionKey,
		&input.Agent,
		&input.NativeSessionID,
		&input.ProjectPath,
		&input.GitRemoteURL,
		&input.ProjectIdentity,
		&input.StartedAt,
		&input.EndedAt,
		&input.WallDurationMS,
		&input.TotalInputTokens,
		&input.TotalOutputTokens,
		&input.TotalTokens,
		&input.TotalCacheReadTokens,
		&input.TotalCacheWriteTokens,
		&input.TotalCostUSD,
		&input.TurnCount,
		&input.UserTurnCount,
		&input.AssistantTurnCount,
		&input.ToolCallCount,
		&input.ToolResultCount,
		&input.SystemTurnCount,
		&input.CompactionSummaryCount,
		&input.Coverage,
		&input.RetainedTurnCount,
		&input.LatestTurnCreatedAt,
		&input.SealedTurnBytes,
		&input.CanonicalEventCount,
		&input.CanonicalEventWatermark,
	); err != nil {
		return trajectoryInputIndexes{}, err
	}
	return input, nil
}

func trajectoryClaim(
	input trajectoryInputIndexes,
	derivationVersion string,
) (TrajectoryDerivationClaim, error) {
	transcriptFingerprint, err := hashTrajectoryFingerprint(
		"transcript.v1",
		transcriptFingerprintMaterial{
			SessionKey:             input.SessionKey,
			Agent:                  input.Agent,
			NativeSessionID:        input.NativeSessionID,
			ProjectPath:            input.ProjectPath,
			GitRemoteURL:           input.GitRemoteURL,
			ProjectIdentity:        input.ProjectIdentity,
			StartedAt:              input.StartedAt,
			EndedAt:                input.EndedAt,
			WallDurationMS:         input.WallDurationMS,
			TotalInputTokens:       input.TotalInputTokens,
			TotalOutputTokens:      input.TotalOutputTokens,
			TotalTokens:            input.TotalTokens,
			TotalCacheReadTokens:   input.TotalCacheReadTokens,
			TotalCacheWriteTokens:  input.TotalCacheWriteTokens,
			TotalCostUSD:           input.TotalCostUSD,
			TurnCount:              input.TurnCount,
			UserTurnCount:          input.UserTurnCount,
			AssistantTurnCount:     input.AssistantTurnCount,
			ToolCallCount:          input.ToolCallCount,
			ToolResultCount:        input.ToolResultCount,
			SystemTurnCount:        input.SystemTurnCount,
			CompactionSummaryCount: input.CompactionSummaryCount,
			Coverage:               input.Coverage,
			RetainedTurnCount:      input.RetainedTurnCount,
			LatestTurnCreatedAt:    input.LatestTurnCreatedAt,
			SealedTurnBytes:        input.SealedTurnBytes,
		},
	)
	if err != nil {
		return TrajectoryDerivationClaim{}, err
	}
	canonicalFingerprint, err := hashTrajectoryFingerprint(
		"canonical.v1",
		canonicalFingerprintMaterial{
			SessionKey:     input.SessionKey,
			EventCount:     input.CanonicalEventCount,
			EventWatermark: input.CanonicalEventWatermark,
		},
	)
	if err != nil {
		return TrajectoryDerivationClaim{}, err
	}
	return TrajectoryDerivationClaim{
		SessionKey:            input.SessionKey,
		DerivationVersion:     derivationVersion,
		TranscriptFingerprint: transcriptFingerprint,
		CanonicalFingerprint:  canonicalFingerprint,
	}, nil
}

func hashTrajectoryFingerprint(kind string, value any) (string, error) {
	body, err := json.Marshal(struct {
		Kind  string
		Value any
	}{
		Kind:  kind,
		Value: value,
	})
	if err != nil {
		return "", errors.New("encode trajectory input fingerprint")
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateTrajectoryDerivationState(state TrajectoryDerivationState) error {
	if err := validateTrajectoryDerivationStateBase(state); err != nil {
		return err
	}
	switch state.Status {
	case TrajectoryDerivationComplete:
		if state.AttemptCount != 0 || state.RetryAt != nil {
			return errors.New("invalid complete trajectory derivation retry state")
		}
	case TrajectoryDerivationPartial:
		if state.AttemptCount < 1 ||
			state.AttemptCount > maxTrajectoryDerivationAttempts ||
			state.RetryAt == nil ||
			!state.RetryAt.After(state.AttemptedAt) {
			return errors.New("invalid partial trajectory derivation retry state")
		}
	case TrajectoryDerivationFailed:
		if state.AttemptCount < 1 ||
			state.AttemptCount > maxTrajectoryDerivationAttempts ||
			state.Retryable != (state.RetryAt != nil) ||
			state.RetryAt != nil && !state.RetryAt.After(state.AttemptedAt) {
			return errors.New("invalid failed trajectory derivation retry state")
		}
	}
	return nil
}

func validateTrajectoryDerivationStateBase(state TrajectoryDerivationState) error {
	if err := validateStorageIdentifier(
		"trajectory derivation session key",
		state.SessionKey,
	); err != nil {
		return err
	}
	if err := validateDerivationVersion(state.DerivationVersion); err != nil {
		return err
	}
	if !validTrajectoryFingerprint(state.TranscriptFingerprint) ||
		!validTrajectoryFingerprint(state.CanonicalFingerprint) ||
		state.AttemptedAt.IsZero() ||
		len(state.Diagnostics) > maxTrajectoryDerivationDiagnostics ||
		state.Coverage.MachineEnvelopesExcluded < 0 {
		return errors.New("invalid trajectory derivation state")
	}
	switch state.Coverage.Transcript {
	case "", transcript.CoverageComplete, transcript.CoveragePartial, transcript.CoverageLive:
	default:
		return errors.New("invalid trajectory derivation coverage")
	}
	for _, diagnostic := range state.Diagnostics {
		if !fixedCodePattern.MatchString(string(diagnostic.Code)) {
			return errors.New("invalid trajectory derivation diagnostic")
		}
		if diagnostic.Citation != nil {
			if err := diagnostic.Citation.Validate(); err != nil {
				return errors.New("invalid trajectory derivation diagnostic citation")
			}
		}
	}
	switch state.Status {
	case TrajectoryDerivationComplete:
		if state.Retryable || state.FailureCode != "" ||
			state.CompletedAt == nil || !state.Coverage.FullyDerived {
			return errors.New("invalid complete trajectory derivation state")
		}
	case TrajectoryDerivationPartial:
		if !state.Retryable || state.FailureCode != "" ||
			state.CompletedAt == nil || state.Coverage.FullyDerived {
			return errors.New("invalid partial trajectory derivation state")
		}
	case TrajectoryDerivationFailed:
		if state.CompletedAt != nil ||
			!fixedCodePattern.MatchString(state.FailureCode) {
			return errors.New("invalid failed trajectory derivation state")
		}
	default:
		return errors.New("invalid trajectory derivation status")
	}
	return nil
}

func scheduleTrajectoryDerivationRetry(
	state *TrajectoryDerivationState,
	previous *TrajectoryDerivationState,
) {
	if state == nil {
		return
	}
	switch state.Status {
	case TrajectoryDerivationComplete:
		state.AttemptCount = 0
		state.RetryAt = nil
	case TrajectoryDerivationPartial:
		state.AttemptCount = nextTrajectoryDerivationAttempt(
			*state,
			previous,
		)
		retryAt := state.AttemptedAt.Add(partialTrajectoryRetryDelay)
		state.RetryAt = &retryAt
	case TrajectoryDerivationFailed:
		state.AttemptCount = nextTrajectoryDerivationAttempt(
			*state,
			previous,
		)
		if !state.Retryable {
			state.RetryAt = nil
			return
		}
		delay := failedTrajectoryRetryDelay(state.AttemptCount)
		retryAt := state.AttemptedAt.Add(delay)
		state.RetryAt = &retryAt
	}
}

func nextTrajectoryDerivationAttempt(
	state TrajectoryDerivationState,
	previous *TrajectoryDerivationState,
) int {
	if previous == nil ||
		previous.Status != state.Status ||
		previous.TranscriptFingerprint != state.TranscriptFingerprint ||
		previous.CanonicalFingerprint != state.CanonicalFingerprint {
		return 1
	}
	if previous.AttemptCount >= maxTrajectoryDerivationAttempts {
		return maxTrajectoryDerivationAttempts
	}
	return previous.AttemptCount + 1
}

func failedTrajectoryRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := failedTrajectoryRetryBaseDelay
	for index := 1; index < attempt; index++ {
		if delay >= failedTrajectoryRetryMaxDelay/2 {
			return failedTrajectoryRetryMaxDelay
		}
		delay *= 2
	}
	if delay > failedTrajectoryRetryMaxDelay {
		return failedTrajectoryRetryMaxDelay
	}
	return delay
}

func validateDerivationVersion(value string) error {
	if strings.TrimSpace(value) == "" ||
		value != strings.TrimSpace(value) ||
		len(value) > 256 {
		return errors.New("invalid trajectory derivation version")
	}
	return nil
}

func validTrajectoryFingerprint(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 ||
		!strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func trajectoryDerivationRecordID(sessionKey, derivationVersion string) string {
	sum := sha256.Sum256([]byte(sessionKey + "\x00" + derivationVersion))
	return hex.EncodeToString(sum[:])
}

func nullableTrajectoryCompletedAt(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatProjectionTime(*value)
}

func nullableTrajectoryRetryAt(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatProjectionTime(*value)
}

func equalTrajectoryTimePointer(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
