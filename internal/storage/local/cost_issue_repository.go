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

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	defaultCostIssueLimit        = 50
	maxCostIssueLimit            = 500
	defaultDirtyProjectLimit     = 25
	maxDirtyProjectLimit         = 500
	maxTranscriptProjectSessions = 500
	maxTranscriptProjectTurns    = 500000
	transcriptProjectTurnBatch   = 500
	maxCostIssuePayloadBytes     = 1 << 20
	maxCorrectionPayloadBytes    = 256 << 10
	maxCostIssueIdentityBytes    = 512
	maxCostIssueFingerprintBytes = 4096
	maxCostIssueProjectBytes     = 4096
	maxCostIssueDetectorBytes    = 128
)

var ErrTranscriptProjectGenerationChanged = errors.New(
	"project transcript generation changed during analysis",
)

type CostIssueQuery = issueintel.Query

type DirtyTranscriptProject struct {
	Project              issueintel.Project
	TranscriptGeneration int64
	AnalyzedGeneration   int64
}

// ReplaceProjectIssueAnalysis atomically replaces all transcript-derived issue
// intelligence for one project and marks the project's current transcript
// generation analyzed.
func (s *Store) ReplaceProjectIssueAnalysis(
	ctx context.Context,
	project issueintel.Project,
	expectedGeneration int64,
	analysis issueintel.Analysis,
) error {
	if err := validateIssueProject(project); err != nil {
		return err
	}
	if expectedGeneration < 1 {
		return errors.New("invalid expected transcript generation")
	}
	if err := validateAttributedCost(analysis.AttributedCost); err != nil {
		return err
	}

	type encryptedIssue struct {
		issue   issueintel.Issue
		payload []byte
	}
	issues := make([]encryptedIssue, 0, len(analysis.Issues))
	issueIDs := make(map[string]struct{}, len(analysis.Issues))
	fingerprints := make(map[string]struct{}, len(analysis.Issues))
	for _, issue := range analysis.Issues {
		if err := validateCostIssue(project, issue); err != nil {
			return err
		}
		if _, exists := issueIDs[issue.IssueID]; exists {
			return errors.New("duplicate cost issue ID")
		}
		issueIDs[issue.IssueID] = struct{}{}
		fingerprintKey := issue.DetectorID + "\x00" + issue.Fingerprint
		if _, exists := fingerprints[fingerprintKey]; exists {
			return errors.New("duplicate project detector fingerprint")
		}
		fingerprints[fingerprintKey] = struct{}{}
		payload, err := json.Marshal(issue)
		if err != nil {
			return errors.New("encode cost issue payload")
		}
		if len(payload) > maxCostIssuePayloadBytes {
			return errors.New("cost issue payload exceeds safety limit")
		}
		payload, err = s.cipher.seal(
			"cost_issue",
			issue.IssueID,
			"payload",
			payload,
		)
		if err != nil {
			return err
		}
		issues = append(issues, encryptedIssue{issue: issue, payload: payload})
	}

	type encryptedCandidate struct {
		candidate issueintel.CorrectionCandidate
		payload   []byte
	}
	candidates := make([]encryptedCandidate, 0, len(analysis.CorrectionCandidates))
	candidateIDs := make(map[string]struct{}, len(analysis.CorrectionCandidates))
	for _, candidate := range analysis.CorrectionCandidates {
		if err := validateCorrectionCandidate(project, candidate); err != nil {
			return err
		}
		if _, exists := candidateIDs[candidate.CandidateID]; exists {
			return errors.New("duplicate correction candidate ID")
		}
		candidateIDs[candidate.CandidateID] = struct{}{}
		payload, err := json.Marshal(candidate)
		if err != nil {
			return errors.New("encode correction candidate payload")
		}
		if len(payload) > maxCorrectionPayloadBytes {
			return errors.New("correction candidate payload exceeds safety limit")
		}
		payload, err = s.cipher.seal(
			"correction_candidate",
			candidate.CandidateID,
			"payload",
			payload,
		)
		if err != nil {
			return err
		}
		candidates = append(candidates, encryptedCandidate{
			candidate: candidate,
			payload:   payload,
		})
	}

	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin project issue analysis replacement")
	}
	defer tx.Rollback()

	err = withMutationTx(ctx, tx, mutationCostIssueAnalysis, func() error {
		var generation int64
		if err := tx.QueryRowContext(ctx, `
			SELECT transcript_generation
			FROM transcript_project_analysis_state
			WHERE project_identity = ?`,
			project.Identity,
		).Scan(&generation); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("project has no transcript generation state")
			}
			return errors.New("read project transcript generation")
		}
		if generation != expectedGeneration {
			return ErrTranscriptProjectGenerationChanged
		}
		if _, err := tx.ExecContext(
			ctx,
			"DELETE FROM cost_issues WHERE project_identity = ?",
			project.Identity,
		); err != nil {
			return errors.New("replace project cost issues")
		}
		if _, err := tx.ExecContext(
			ctx,
			"DELETE FROM correction_candidates WHERE project_identity = ?",
			project.Identity,
		); err != nil {
			return errors.New("replace project correction candidates")
		}
		if _, err := tx.ExecContext(
			ctx,
			"DELETE FROM project_issue_cost_totals WHERE project_identity = ?",
			project.Identity,
		); err != nil {
			return errors.New("replace project issue cost total")
		}
		for _, value := range issues {
			issue := value.issue
			known := issue.Cost.WastedUSD != nil
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO cost_issues (
					issue_id, detector_id, fingerprint, project_identity,
					wasted_minutes, wasted_tokens, wasted_usd,
					wasted_usd_known, lower_bound, session_count,
					first_seen, last_seen, payload, payload_encoding,
					created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				issue.IssueID,
				issue.DetectorID,
				issue.Fingerprint,
				project.Identity,
				issue.Cost.WastedMinutes,
				issue.Cost.WastedTokens,
				nullableFloat64(issue.Cost.WastedUSD),
				boolInt(known),
				boolInt(issue.Cost.LowerBound),
				issue.SessionCount,
				formatProjectionTime(issue.FirstSeen),
				formatProjectionTime(issue.LastSeen),
				value.payload,
				payloadEncodingAESGCM,
				now,
				now,
			); err != nil {
				return fmt.Errorf("persist cost issue: %w", err)
			}
		}
		for _, value := range candidates {
			candidate := value.candidate
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO correction_candidates (
					candidate_id, project_identity, occurred_at, payload,
					payload_encoding, created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				candidate.CandidateID,
				project.Identity,
				formatProjectionTime(candidate.OccurredAt),
				value.payload,
				payloadEncodingAESGCM,
				now,
				now,
			); err != nil {
				return fmt.Errorf("persist correction candidate: %w", err)
			}
		}
		attributed := analysis.AttributedCost
		attributedKnown := attributed.WastedUSD != nil ||
			!attributed.LowerBound
		attributedUSD := attributed.WastedUSD
		if attributedKnown && attributedUSD == nil {
			zero := 0.0
			attributedUSD = &zero
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO project_issue_cost_totals (
				project_identity, attributed_minutes, attributed_tokens,
				attributed_usd, attributed_usd_known, lower_bound, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			project.Identity,
			attributed.WastedMinutes,
			attributed.WastedTokens,
			nullableFloat64(attributedUSD),
			boolInt(attributedKnown),
			boolInt(attributed.LowerBound),
			now,
		); err != nil {
			return fmt.Errorf("persist project issue cost total: %w", err)
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE transcript_project_analysis_state
			SET analyzed_generation = ?,
				analyzed_at = ?,
				updated_at = ?
			WHERE project_identity = ?
				AND transcript_generation = ?`,
			expectedGeneration,
			now,
			now,
			project.Identity,
			expectedGeneration,
		)
		if err != nil {
			return errors.New("advance project analyzed generation")
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return ErrTranscriptProjectGenerationChanged
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit project issue analysis replacement")
	}
	return nil
}

func (s *Store) QueryCostIssues(
	ctx context.Context,
	query CostIssueQuery,
) ([]issueintel.Issue, error) {
	if query.Limit <= 0 {
		query.Limit = defaultCostIssueLimit
	}
	if query.Limit > maxCostIssueLimit ||
		len(query.ProjectIdentity) > maxCostIssueProjectBytes ||
		len(query.DetectorID) > maxCostIssueDetectorBytes {
		return nil, errors.New("invalid cost issue query")
	}
	clauses := []string{"1 = 1"}
	args := make([]any, 0, 3)
	if query.ProjectIdentity != "" {
		clauses = append(clauses, "project_identity = ?")
		args = append(args, query.ProjectIdentity)
	}
	if query.DetectorID != "" {
		clauses = append(clauses, "detector_id = ?")
		args = append(args, query.DetectorID)
	}
	args = append(args, query.Limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT issue_id, detector_id, fingerprint, project_identity,
			wasted_minutes, wasted_tokens, wasted_usd, wasted_usd_known,
			lower_bound, session_count, first_seen, last_seen, payload,
			payload_encoding
		FROM cost_issues
		WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY wasted_usd_known DESC, wasted_usd DESC,
			session_count DESC, last_seen DESC, issue_id ASC
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, errors.New("query cost issues")
	}
	defer rows.Close()

	result := make([]issueintel.Issue, 0)
	for rows.Next() {
		issue, err := s.scanCostIssue(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("query cost issues")
	}
	return result, nil
}

func (s *Store) GetCostIssue(
	ctx context.Context,
	issueID string,
) (issueintel.Issue, error) {
	if issueID == "" || len(issueID) > maxCostIssueIdentityBytes {
		return issueintel.Issue{}, errors.New("invalid cost issue ID")
	}
	return s.scanCostIssue(s.db.QueryRowContext(ctx, `
		SELECT issue_id, detector_id, fingerprint, project_identity,
			wasted_minutes, wasted_tokens, wasted_usd, wasted_usd_known,
			lower_bound, session_count, first_seen, last_seen, payload,
			payload_encoding
		FROM cost_issues
		WHERE issue_id = ?`,
		issueID,
	))
}

func (s *Store) ListDirtyTranscriptProjects(
	ctx context.Context,
	limits ...int,
) ([]DirtyTranscriptProject, error) {
	if len(limits) > 1 {
		return nil, errors.New("invalid dirty transcript project limit")
	}
	limit := 0
	if len(limits) == 1 {
		limit = limits[0]
	}
	if limit <= 0 {
		limit = defaultDirtyProjectLimit
	}
	if limit > maxDirtyProjectLimit {
		return nil, errors.New("invalid dirty transcript project limit")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT state.project_identity,
			COALESCE((
				SELECT project_path
				FROM transcript_sessions session
				WHERE session.project_identity = state.project_identity
				ORDER BY COALESCE(
					session.ended_at,
					session.started_at,
					session.updated_at
				) DESC, session.session_key ASC
				LIMIT 1
			), ''),
			state.transcript_generation,
			state.analyzed_generation
		FROM transcript_project_analysis_state state
		WHERE state.transcript_generation > state.analyzed_generation
		ORDER BY state.updated_at ASC, state.project_identity ASC
		LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, errors.New("list dirty transcript projects")
	}
	defer rows.Close()
	result := make([]DirtyTranscriptProject, 0)
	for rows.Next() {
		var value DirtyTranscriptProject
		if err := rows.Scan(
			&value.Project.Identity,
			&value.Project.Path,
			&value.TranscriptGeneration,
			&value.AnalyzedGeneration,
		); err != nil {
			return nil, errors.New("read dirty transcript project")
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("list dirty transcript projects")
	}
	return result, nil
}

// LoadTranscriptProjectData returns complete decrypted detector input for one
// project. It errors instead of returning partial data when a safety cap is
// exceeded.
func (s *Store) LoadTranscriptProjectData(
	ctx context.Context,
	projectIdentity string,
) (issueintel.ProjectInput, error) {
	if projectIdentity == "" || len(projectIdentity) > maxCostIssueProjectBytes {
		return issueintel.ProjectInput{}, errors.New("invalid transcript project identity")
	}
	result := issueintel.ProjectInput{
		Project: issueintel.Project{Identity: projectIdentity},
		Now:     s.nowUTC(),
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
		WHERE project_identity = ?
		ORDER BY COALESCE(started_at, ended_at, updated_at) ASC,
			session_key ASC
		LIMIT ?`,
		projectIdentity,
		maxTranscriptProjectSessions+1,
	)
	if err != nil {
		return issueintel.ProjectInput{}, errors.New("load transcript project sessions")
	}
	for rows.Next() {
		session, err := scanTranscriptSession(rows)
		if err != nil {
			rows.Close()
			return issueintel.ProjectInput{}, err
		}
		result.Project.Path = session.ProjectPath
		result.Sessions = append(result.Sessions, issueintel.Session{
			Metadata: session,
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return issueintel.ProjectInput{}, errors.New("load transcript project sessions")
	}
	if err := rows.Close(); err != nil {
		return issueintel.ProjectInput{}, errors.New("close transcript project sessions")
	}
	if len(result.Sessions) > maxTranscriptProjectSessions {
		return issueintel.ProjectInput{}, errors.New(
			"transcript project session safety limit exceeded",
		)
	}

	turnCount := 0
	for sessionIndex := range result.Sessions {
		sessionKey := result.Sessions[sessionIndex].Metadata.SessionKey
		var cursor transcriptProjectTurnCursor
		for {
			batch, err := s.loadEncryptedTranscriptProjectTurnBatch(
				ctx,
				sessionKey,
				cursor,
			)
			if err != nil {
				return issueintel.ProjectInput{}, err
			}
			if turnCount+len(batch) > maxTranscriptProjectTurns {
				return issueintel.ProjectInput{}, errors.New(
					"transcript project turn safety limit exceeded",
				)
			}
			for _, encrypted := range batch {
				turn, err := s.decryptTranscriptProjectTurn(encrypted)
				if err != nil {
					return issueintel.ProjectInput{}, err
				}
				if turn.SessionKey != sessionKey {
					return issueintel.ProjectInput{}, errors.New(
						"transcript project turn has no loaded session",
					)
				}
				result.Sessions[sessionIndex].Turns = append(
					result.Sessions[sessionIndex].Turns,
					turn,
				)
			}
			turnCount += len(batch)
			if len(batch) < transcriptProjectTurnBatch {
				break
			}
			last := batch[len(batch)-1]
			cursor = transcriptProjectTurnCursor{
				set:        true,
				turnIndex:  last.turn.TurnIndex,
				occurredAt: last.occurredAt,
				turnID:     last.turn.TurnID,
			}
		}
	}
	return result, nil
}

type transcriptProjectTurnCursor struct {
	set        bool
	turnIndex  int64
	occurredAt string
	turnID     string
}

type encryptedTranscriptProjectTurn struct {
	turn       transcript.Turn
	occurredAt string
	payload    []byte
	encoding   string
}

func (s *Store) loadEncryptedTranscriptProjectTurnBatch(
	ctx context.Context,
	sessionKey string,
	cursor transcriptProjectTurnCursor,
) ([]encryptedTranscriptProjectTurn, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT turn_id, source_record_key, session_key, turn_index,
			occurred_at, role, tool_name, model, input_tokens, output_tokens,
			cache_read_tokens, cache_write_tokens, cost_usd, payload,
			payload_encoding
		FROM transcript_turns
		WHERE session_key = ?
			AND (
				? = 0
				OR turn_index > ?
				OR (turn_index = ? AND occurred_at > ?)
				OR (
					turn_index = ?
					AND occurred_at = ?
					AND turn_id > ?
				)
			)
		ORDER BY turn_index ASC, occurred_at ASC, turn_id ASC
		LIMIT ?`,
		sessionKey,
		boolInt(cursor.set),
		cursor.turnIndex,
		cursor.turnIndex,
		cursor.occurredAt,
		cursor.turnIndex,
		cursor.occurredAt,
		cursor.turnID,
		transcriptProjectTurnBatch,
	)
	if err != nil {
		return nil, errors.New("load transcript project turns")
	}
	result := make([]encryptedTranscriptProjectTurn, 0, transcriptProjectTurnBatch)
	for rows.Next() {
		var value encryptedTranscriptProjectTurn
		var inputTokens, outputTokens sql.NullInt64
		var cacheReadTokens, cacheWriteTokens sql.NullInt64
		var costUSD sql.NullFloat64
		if err := rows.Scan(
			&value.turn.TurnID,
			&value.turn.SourceRecordKey,
			&value.turn.SessionKey,
			&value.turn.TurnIndex,
			&value.occurredAt,
			&value.turn.Role,
			&value.turn.ToolName,
			&value.turn.Model,
			&inputTokens,
			&outputTokens,
			&cacheReadTokens,
			&cacheWriteTokens,
			&costUSD,
			&value.payload,
			&value.encoding,
		); err != nil {
			rows.Close()
			return nil, errors.New("read transcript turn")
		}
		value.turn.InputTokens = nullableInt64Pointer(inputTokens)
		value.turn.OutputTokens = nullableInt64Pointer(outputTokens)
		value.turn.CacheReadTokens = nullableInt64Pointer(cacheReadTokens)
		value.turn.CacheWriteTokens = nullableInt64Pointer(cacheWriteTokens)
		value.turn.CostUSD = nullableFloat64Pointer(costUSD)
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, errors.New("load transcript project turns")
	}
	if err := rows.Close(); err != nil {
		return nil, errors.New("close transcript project turns")
	}
	return result, nil
}

func (s *Store) decryptTranscriptProjectTurn(
	value encryptedTranscriptProjectTurn,
) (transcript.Turn, error) {
	var err error
	value.turn.OccurredAt, err = time.Parse(time.RFC3339Nano, value.occurredAt)
	if err != nil {
		return transcript.Turn{}, errors.New("decode transcript turn timestamp")
	}
	payload, err := s.cipher.open(
		"transcript_turn",
		value.turn.TurnID,
		"payload",
		value.encoding,
		value.payload,
	)
	if err != nil {
		return transcript.Turn{}, err
	}
	if err := json.Unmarshal(payload, &value.turn.Payload); err != nil {
		return transcript.Turn{}, errors.New("decode transcript turn payload")
	}
	return value.turn, nil
}

func markTranscriptProjectDirtyTx(
	ctx context.Context,
	tx *sql.Tx,
	projectIdentity string,
	now string,
) error {
	if projectIdentity == "" || len(projectIdentity) > maxCostIssueProjectBytes {
		return errors.New("invalid dirty transcript project identity")
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO transcript_project_analysis_state (
			project_identity, transcript_generation, analyzed_generation,
			created_at, updated_at
		) VALUES (?, 1, 0, ?, ?)
		ON CONFLICT(project_identity) DO UPDATE SET
			transcript_generation =
				transcript_project_analysis_state.transcript_generation + 1,
			updated_at = excluded.updated_at`,
		projectIdentity,
		now,
		now,
	)
	if err != nil {
		return errors.New("mark transcript project dirty")
	}
	return nil
}

func (s *Store) scanCostIssue(row rowScanner) (issueintel.Issue, error) {
	var issueID, detectorID, fingerprint, projectIdentity string
	var wastedMinutes float64
	var wastedTokens int64
	var wastedUSD sql.NullFloat64
	var known, lowerBound int
	var sessionCount int
	var firstSeenValue, lastSeenValue string
	var payload []byte
	var encoding string
	if err := row.Scan(
		&issueID,
		&detectorID,
		&fingerprint,
		&projectIdentity,
		&wastedMinutes,
		&wastedTokens,
		&wastedUSD,
		&known,
		&lowerBound,
		&sessionCount,
		&firstSeenValue,
		&lastSeenValue,
		&payload,
		&encoding,
	); err != nil {
		return issueintel.Issue{}, err
	}
	firstSeen, err := parseProjectionTime(firstSeenValue)
	if err != nil {
		return issueintel.Issue{}, errors.New("decode cost issue first seen")
	}
	lastSeen, err := parseProjectionTime(lastSeenValue)
	if err != nil {
		return issueintel.Issue{}, errors.New("decode cost issue last seen")
	}
	payload, err = s.cipher.open(
		"cost_issue",
		issueID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return issueintel.Issue{}, err
	}
	var result issueintel.Issue
	if err := json.Unmarshal(payload, &result); err != nil {
		return issueintel.Issue{}, errors.New("decode cost issue payload")
	}
	if result.IssueID != issueID ||
		result.DetectorID != detectorID ||
		result.Fingerprint != fingerprint ||
		result.Project.Identity != projectIdentity ||
		result.Cost.WastedMinutes != wastedMinutes ||
		result.Cost.WastedTokens != wastedTokens ||
		(result.Cost.WastedUSD != nil) != (known == 1) ||
		result.Cost.LowerBound != (lowerBound == 1) ||
		result.SessionCount != sessionCount ||
		!result.FirstSeen.Equal(firstSeen) ||
		!result.LastSeen.Equal(lastSeen) {
		return issueintel.Issue{}, errors.New("cost issue index does not match payload")
	}
	if result.Cost.WastedUSD != nil &&
		(!wastedUSD.Valid || *result.Cost.WastedUSD != wastedUSD.Float64) {
		return issueintel.Issue{}, errors.New("cost issue cost index does not match payload")
	}
	if result.Cost.WastedUSD == nil && wastedUSD.Valid {
		return issueintel.Issue{}, errors.New("cost issue cost index does not match payload")
	}
	return result, nil
}

func validateIssueProject(project issueintel.Project) error {
	if project.Identity == "" ||
		len(project.Identity) > maxCostIssueProjectBytes ||
		len(project.Path) > maxCostIssueProjectBytes {
		return errors.New("invalid issue project")
	}
	return nil
}

func validateCostIssue(project issueintel.Project, issue issueintel.Issue) error {
	if issue.IssueID == "" ||
		len(issue.IssueID) > maxCostIssueIdentityBytes ||
		issue.DetectorID == "" ||
		len(issue.DetectorID) > maxCostIssueDetectorBytes ||
		issue.Fingerprint == "" ||
		len(issue.Fingerprint) > maxCostIssueFingerprintBytes ||
		issue.Project.Identity != project.Identity ||
		len(issue.Project.Path) > maxCostIssueProjectBytes ||
		issue.Cost.WastedMinutes < 0 ||
		math.IsNaN(issue.Cost.WastedMinutes) ||
		math.IsInf(issue.Cost.WastedMinutes, 0) ||
		issue.Cost.WastedTokens < 0 ||
		!validNullableCost(issue.Cost.WastedUSD) ||
		issue.SessionCount < 1 ||
		issue.SessionCount != len(issue.Sessions) ||
		len(issue.Excerpts) < 2 ||
		len(issue.Excerpts) > 5 ||
		issue.FirstSeen.IsZero() ||
		issue.LastSeen.IsZero() ||
		issue.LastSeen.Before(issue.FirstSeen) {
		return errors.New("invalid cost issue")
	}
	return nil
}

func validateAttributedCost(cost issueintel.Cost) error {
	if cost.WastedMinutes < 0 ||
		math.IsNaN(cost.WastedMinutes) ||
		math.IsInf(cost.WastedMinutes, 0) ||
		cost.WastedTokens < 0 ||
		!validNullableCost(cost.WastedUSD) {
		return errors.New("invalid attributed issue cost")
	}
	return nil
}

func validateCorrectionCandidate(
	project issueintel.Project,
	candidate issueintel.CorrectionCandidate,
) error {
	if candidate.CandidateID == "" ||
		len(candidate.CandidateID) > maxCostIssueIdentityBytes ||
		candidate.Project.Identity != project.Identity ||
		len(candidate.Project.Path) > maxCostIssueProjectBytes ||
		candidate.Citation.SessionKey == "" ||
		candidate.Citation.TurnIndex < 0 ||
		candidate.Citation.JSONLByteOffset < 0 ||
		candidate.OccurredAt.IsZero() {
		return errors.New("invalid correction candidate")
	}
	return nil
}

func transcriptSessionMetadataChanged(
	existing transcript.Session,
	next transcript.Session,
) bool {
	return existing.Agent != next.Agent ||
		existing.NativeSessionID != next.NativeSessionID ||
		existing.ProjectPath != next.ProjectPath ||
		existing.GitRemoteURL != next.GitRemoteURL ||
		existing.ProjectIdentity != next.ProjectIdentity ||
		existing.Coverage != next.Coverage
}
