package local

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	canonical "github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/missionpack"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	defaultMissionPackIssues       = 10
	defaultMissionPackSessions     = 20
	defaultMissionPackCommandTurns = 500
	defaultMissionPackEvents       = 500
	maxMissionPackPathCandidates   = 200
	maxMissionPackCandidateIDs     = 100
	minMissionPackFactSessions     = 2
	maxMissionPackToolFacts        = 3
)

func (s *Store) ResolveMissionPackProject(
	ctx context.Context,
	selector missionpack.ProjectSelector,
) (missionpack.ResolvedProject, error) {
	selector.IssueID = strings.TrimSpace(selector.IssueID)
	selector.RemoteIdentity = strings.TrimSpace(selector.RemoteIdentity)
	selector.ProjectRoot = filepath.Clean(strings.TrimSpace(selector.ProjectRoot))
	selector.ProjectPath = filepath.Clean(strings.TrimSpace(selector.ProjectPath))
	if selector.IssueID != "" {
		if selector.RemoteIdentity != "" ||
			selector.ProjectRoot != "." ||
			selector.ProjectPath != "." {
			return missionpack.ResolvedProject{}, errors.New(
				"invalid Mission Pack project selector",
			)
		}
		return s.resolveMissionPackIssueProject(ctx, selector.IssueID)
	}
	if selector.RemoteIdentity == "" &&
		(selector.ProjectRoot == "." || selector.ProjectRoot == "") &&
		(selector.ProjectPath == "." || selector.ProjectPath == "") {
		return missionpack.ResolvedProject{}, errors.New(
			"invalid Mission Pack project selector",
		)
	}
	if selector.RemoteIdentity != "" {
		return s.resolveMissionPackRemoteProject(
			ctx,
			selector.RemoteIdentity,
		)
	}
	target := selector.ProjectRoot
	if target == "." || target == "" {
		target = selector.ProjectPath
	}
	return s.resolveMissionPackPathProject(ctx, target)
}

func (s *Store) resolveMissionPackIssueProject(
	ctx context.Context,
	issueID string,
) (missionpack.ResolvedProject, error) {
	if issueID == "" || len(issueID) > maxCostIssueIdentityBytes {
		return missionpack.ResolvedProject{}, errors.New(
			"invalid Mission Pack issue selector",
		)
	}
	var identity string
	err := s.db.QueryRowContext(ctx, `
		SELECT project_identity
		FROM cost_issues
		WHERE issue_id = ?`,
		issueID,
	).Scan(&identity)
	if errors.Is(err, sql.ErrNoRows) {
		return missionpack.ResolvedProject{}, missionpack.ErrProjectNotFound
	}
	if err != nil {
		return missionpack.ResolvedProject{}, errors.New(
			"resolve Mission Pack issue project",
		)
	}
	return s.missionPackProjectByIdentity(ctx, identity)
}

func (s *Store) resolveMissionPackRemoteProject(
	ctx context.Context,
	remote string,
) (missionpack.ResolvedProject, error) {
	if remote == "" || len(remote) > maxCostIssueProjectBytes {
		return missionpack.ResolvedProject{}, errors.New(
			"invalid Mission Pack remote selector",
		)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT project_identity
		FROM transcript_sessions
		WHERE project_identity = ? OR git_remote_url = ?
		ORDER BY project_identity
		LIMIT 2`,
		remote,
		remote,
	)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return missionpack.ResolvedProject{}, contextErr
		}
		return missionpack.ResolvedProject{}, errors.New(
			"resolve Mission Pack remote project",
		)
	}
	defer rows.Close()
	var identities []string
	for rows.Next() {
		var identity string
		if err := rows.Scan(&identity); err != nil {
			return missionpack.ResolvedProject{}, errors.New(
				"read Mission Pack remote project",
			)
		}
		identities = append(identities, identity)
	}
	if err := rows.Err(); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return missionpack.ResolvedProject{}, contextErr
		}
		return missionpack.ResolvedProject{}, errors.New(
			"resolve Mission Pack remote project",
		)
	}
	if len(identities) == 0 {
		return missionpack.ResolvedProject{}, missionpack.ErrProjectNotFound
	}
	if len(identities) > 1 {
		return missionpack.ResolvedProject{}, errors.New(
			"Mission Pack project is ambiguous",
		)
	}
	return s.missionPackProjectByIdentity(ctx, identities[0])
}

func (s *Store) resolveMissionPackPathProject(
	ctx context.Context,
	target string,
) (missionpack.ResolvedProject, error) {
	target = filepath.Clean(strings.TrimSpace(target))
	if !filepath.IsAbs(target) || len(target) > maxCostIssueProjectBytes {
		return missionpack.ResolvedProject{}, errors.New(
			"invalid Mission Pack path selector",
		)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_identity, project_path, git_remote_url
		FROM transcript_sessions
		ORDER BY COALESCE(ended_at, started_at, updated_at) DESC,
			session_key ASC
		LIMIT ?`,
		maxMissionPackPathCandidates,
	)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return missionpack.ResolvedProject{}, contextErr
		}
		return missionpack.ResolvedProject{}, errors.New(
			"resolve Mission Pack path project",
		)
	}
	defer rows.Close()
	type candidate struct {
		identity string
		path     string
		remote   string
	}
	byIdentity := make(map[string]candidate)
	for rows.Next() {
		var value candidate
		if err := rows.Scan(
			&value.identity,
			&value.path,
			&value.remote,
		); err != nil {
			return missionpack.ResolvedProject{}, errors.New(
				"read Mission Pack path project",
			)
		}
		value.path = filepath.Clean(value.path)
		if _, exists := byIdentity[value.identity]; exists ||
			!pathContains(value.path, target) {
			continue
		}
		byIdentity[value.identity] = value
	}
	if err := rows.Err(); err != nil {
		return missionpack.ResolvedProject{}, errors.New(
			"resolve Mission Pack path project",
		)
	}
	var selected candidate
	for _, value := range byIdentity {
		if selected.identity == "" || len(value.path) > len(selected.path) {
			selected = value
			continue
		}
		if len(value.path) == len(selected.path) &&
			value.identity != selected.identity {
			return missionpack.ResolvedProject{}, errors.New(
				"Mission Pack project is ambiguous",
			)
		}
	}
	if selected.identity == "" {
		return missionpack.ResolvedProject{}, missionpack.ErrProjectNotFound
	}
	kind := "path"
	if selected.remote != "" && selected.identity == selected.remote {
		kind = "remote"
	}
	return missionpack.ResolvedProject{
		Identity:     selected.identity,
		IdentityKind: kind,
		Path:         selected.path,
	}, nil
}

func (s *Store) missionPackProjectByIdentity(
	ctx context.Context,
	identity string,
) (missionpack.ResolvedProject, error) {
	var path, remote string
	err := s.db.QueryRowContext(ctx, `
		SELECT project_path, git_remote_url
		FROM transcript_sessions
		WHERE project_identity = ?
		ORDER BY COALESCE(ended_at, started_at, updated_at) DESC,
			session_key ASC
		LIMIT 1`,
		identity,
	).Scan(&path, &remote)
	if errors.Is(err, sql.ErrNoRows) {
		return missionpack.ResolvedProject{}, missionpack.ErrProjectNotFound
	}
	if err != nil {
		return missionpack.ResolvedProject{}, errors.New(
			"read Mission Pack project",
		)
	}
	kind := "path"
	if remote != "" && identity == remote {
		kind = "remote"
	}
	return missionpack.ResolvedProject{
		Identity:     identity,
		IdentityKind: kind,
		Path:         filepath.Clean(path),
	}, nil
}

func pathContains(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if !filepath.IsAbs(parent) || !filepath.IsAbs(child) {
		return false
	}
	relative, err := filepath.Rel(parent, child)
	return err == nil &&
		relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (s *Store) ReadMissionPackEvidence(
	ctx context.Context,
	projectIdentity string,
	limits missionpack.Limits,
) (missionpack.EvidenceSnapshot, error) {
	projectIdentity = strings.TrimSpace(projectIdentity)
	if projectIdentity == "" ||
		len(projectIdentity) > maxCostIssueProjectBytes {
		return missionpack.EvidenceSnapshot{}, errors.New(
			"invalid Mission Pack project identity",
		)
	}
	limits = normalizeMissionPackLimits(limits)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return missionpack.EvidenceSnapshot{}, errors.New(
			"begin Mission Pack evidence snapshot",
		)
	}
	defer tx.Rollback()

	result := missionpack.EvidenceSnapshot{
		Candidates: make(map[string]issueintel.CorrectionCandidate),
	}
	analysisCompletedAt, err := readMissionPackSourceState(
		ctx,
		tx,
		projectIdentity,
		&result,
	)
	if err != nil {
		return missionpack.EvidenceSnapshot{}, err
	}
	result.Issues, err = s.readMissionPackIssues(
		ctx,
		tx,
		projectIdentity,
		limits,
	)
	if err != nil {
		return missionpack.EvidenceSnapshot{}, err
	}
	result.Insight, err = s.readMissionPackInsight(
		ctx,
		tx,
		projectIdentity,
	)
	if err != nil {
		return missionpack.EvidenceSnapshot{}, err
	}
	if result.Insight != nil &&
		!analysisCompletedAt.IsZero() &&
		result.Insight.GeneratedAt.Before(analysisCompletedAt) {
		result.InsightStale = true
	}
	if result.Insight != nil {
		result.Candidates, err = s.readMissionPackCandidates(
			ctx,
			tx,
			projectIdentity,
			*result.Insight,
		)
		if err != nil {
			return missionpack.EvidenceSnapshot{}, err
		}
	}
	result.Sessions, err = readMissionPackSessions(
		ctx,
		tx,
		projectIdentity,
		limits.Sessions,
	)
	if err != nil {
		return missionpack.EvidenceSnapshot{}, err
	}
	result.SuccessfulCommands, err = s.readMissionPackCommands(
		ctx,
		tx,
		result.Sessions,
		limits.CommandTurns,
	)
	if err != nil {
		return missionpack.EvidenceSnapshot{}, err
	}
	result.Facts, err = s.readMissionPackFacts(
		ctx,
		tx,
		result.Sessions,
		limits.Events,
	)
	if err != nil {
		return missionpack.EvidenceSnapshot{}, err
	}
	result.Coverage.CanonicalContextAvailable = len(result.Facts) > 0
	if err := tx.Commit(); err != nil {
		return missionpack.EvidenceSnapshot{}, errors.New(
			"commit Mission Pack evidence snapshot",
		)
	}
	return result, nil
}

func normalizeMissionPackLimits(value missionpack.Limits) missionpack.Limits {
	if value.Issues <= 0 || value.Issues > defaultMissionPackIssues {
		value.Issues = defaultMissionPackIssues
	}
	if value.Sessions <= 0 || value.Sessions > defaultMissionPackSessions {
		value.Sessions = defaultMissionPackSessions
	}
	if value.CommandTurns <= 0 ||
		value.CommandTurns > defaultMissionPackCommandTurns {
		value.CommandTurns = defaultMissionPackCommandTurns
	}
	if value.Events <= 0 || value.Events > defaultMissionPackEvents {
		value.Events = defaultMissionPackEvents
	}
	value.IssueID = strings.TrimSpace(value.IssueID)
	return value
}

func readMissionPackSourceState(
	ctx context.Context,
	tx *sql.Tx,
	projectIdentity string,
	result *missionpack.EvidenceSnapshot,
) (time.Time, error) {
	var analyzedAt sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT transcript_generation, analyzed_generation, analyzed_at
		FROM transcript_project_analysis_state
		WHERE project_identity = ?`,
		projectIdentity,
	).Scan(
		&result.SourceState.TranscriptGeneration,
		&result.SourceState.AnalyzedGeneration,
		&analyzedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, missionpack.ErrProjectNotFound
	}
	if err != nil {
		return time.Time{}, errors.New("read Mission Pack source generations")
	}
	var analysisCompletedAt time.Time
	if analyzedAt.Valid {
		analysisCompletedAt, err = parseProjectionTime(analyzedAt.String)
		if err != nil {
			return time.Time{}, errors.New(
				"decode Mission Pack analysis completion timestamp",
			)
		}
	}
	result.SourceState.AnalysisStatus = missionpack.AnalysisStatusCurrent
	if result.SourceState.AnalyzedGeneration <
		result.SourceState.TranscriptGeneration {
		result.SourceState.AnalysisStatus = missionpack.AnalysisStatusPending
	}

	var dataThrough sql.NullString
	var complete, partial, live int
	err = tx.QueryRowContext(ctx, `
		SELECT MAX(COALESCE(ended_at, started_at, updated_at)),
			COALESCE(SUM(CASE WHEN coverage = 'complete' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN coverage = 'partial' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN coverage = 'live' THEN 1 ELSE 0 END), 0)
		FROM transcript_sessions
		WHERE project_identity = ?`,
		projectIdentity,
	).Scan(&dataThrough, &complete, &partial, &live)
	if err != nil {
		return time.Time{}, errors.New("read Mission Pack transcript coverage")
	}
	if dataThrough.Valid {
		parsed, parseErr := parseProjectionTime(dataThrough.String)
		if parseErr != nil {
			return time.Time{}, errors.New(
				"decode Mission Pack data-through timestamp",
			)
		}
		result.SourceState.DataThrough = parsed
	}
	switch {
	case partial > 0:
		result.Coverage.TranscriptStatus =
			missionpack.TranscriptCoveragePartial
	case live > 0:
		result.Coverage.TranscriptStatus =
			missionpack.TranscriptCoverageLive
	case complete > 0:
		result.Coverage.TranscriptStatus =
			missionpack.TranscriptCoverageComplete
	default:
		result.Coverage.TranscriptStatus =
			missionpack.TranscriptCoverageUnknown
	}
	return analysisCompletedAt, nil
}

func (s *Store) readMissionPackIssues(
	ctx context.Context,
	tx *sql.Tx,
	projectIdentity string,
	limits missionpack.Limits,
) ([]issueintel.Issue, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT issue_id, detector_id, fingerprint, project_identity,
			wasted_minutes, wasted_tokens, wasted_usd, wasted_usd_known,
			lower_bound, session_count, first_seen, last_seen, payload,
			payload_encoding
		FROM cost_issues
		WHERE project_identity = ?
		ORDER BY wasted_usd_known DESC, wasted_usd DESC,
			session_count DESC, last_seen DESC, issue_id ASC
		LIMIT ?`,
		projectIdentity,
		limits.Issues,
	)
	if err != nil {
		return nil, errors.New("read Mission Pack cost issues")
	}
	var result []issueintel.Issue
	for rows.Next() {
		issue, scanErr := s.scanCostIssue(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		result = append(result, issue)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, errors.New("read Mission Pack cost issues")
	}
	if err := rows.Close(); err != nil {
		return nil, errors.New("close Mission Pack cost issues")
	}
	if limits.IssueID == "" || containsMissionPackIssue(result, limits.IssueID) {
		return result, nil
	}
	issue, err := s.scanCostIssue(tx.QueryRowContext(ctx, `
		SELECT issue_id, detector_id, fingerprint, project_identity,
			wasted_minutes, wasted_tokens, wasted_usd, wasted_usd_known,
			lower_bound, session_count, first_seen, last_seen, payload,
			payload_encoding
		FROM cost_issues
		WHERE project_identity = ? AND issue_id = ?`,
		projectIdentity,
		limits.IssueID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, missionpack.ErrProjectNotFound
	}
	if err != nil {
		return nil, err
	}
	return append(result, issue), nil
}

func containsMissionPackIssue(values []issueintel.Issue, issueID string) bool {
	for _, value := range values {
		if value.IssueID == issueID {
			return true
		}
	}
	return false
}

func (s *Store) readMissionPackInsight(
	ctx context.Context,
	tx *sql.Tx,
	projectIdentity string,
) (*issueintel.InsightRecord, error) {
	var insightID, indexedProject, harness, model, promptVersion string
	var inputHash, generatedAtValue, encoding string
	var payload []byte
	err := tx.QueryRowContext(ctx, `
		SELECT insight_id, project_identity, harness, model,
			prompt_version, input_hash, generated_at, payload,
			payload_encoding
		FROM insights
		WHERE project_identity = ?
		ORDER BY generated_at DESC, insight_id ASC
		LIMIT 1`,
		projectIdentity,
	).Scan(
		&insightID,
		&indexedProject,
		&harness,
		&model,
		&promptVersion,
		&inputHash,
		&generatedAtValue,
		&payload,
		&encoding,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("read Mission Pack insight")
	}
	payload, err = s.cipher.open(
		"insight",
		insightID,
		"payload",
		encoding,
		payload,
	)
	if err != nil {
		return nil, err
	}
	var record issueintel.InsightRecord
	if json.Unmarshal(payload, &record) != nil ||
		record.InsightID != insightID ||
		record.Project.Identity != indexedProject ||
		record.Harness != harness ||
		record.Model != model ||
		record.PromptVersion != promptVersion ||
		record.InputHash != inputHash {
		return nil, errors.New("decode Mission Pack insight")
	}
	generatedAt, err := parseProjectionTime(generatedAtValue)
	if err != nil || !record.GeneratedAt.Equal(generatedAt) {
		return nil, errors.New("decode Mission Pack insight timestamp")
	}
	return &record, nil
}

func (s *Store) readMissionPackCandidates(
	ctx context.Context,
	tx *sql.Tx,
	projectIdentity string,
	insight issueintel.InsightRecord,
) (map[string]issueintel.CorrectionCandidate, error) {
	ids := make(map[string]bool)
	for _, cluster := range insight.Result.Clusters {
		for _, candidateID := range cluster.CandidateIDs {
			candidateID = strings.TrimSpace(candidateID)
			if candidateID != "" && len(ids) < maxMissionPackCandidateIDs {
				ids[candidateID] = true
			}
		}
	}
	result := make(map[string]issueintel.CorrectionCandidate, len(ids))
	for candidateID := range ids {
		var payload []byte
		var encoding string
		err := tx.QueryRowContext(ctx, `
			SELECT payload, payload_encoding
			FROM correction_candidates
			WHERE project_identity = ? AND candidate_id = ?`,
			projectIdentity,
			candidateID,
		).Scan(&payload, &encoding)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, errors.New("read Mission Pack correction candidate")
		}
		payload, err = s.cipher.open(
			"correction_candidate",
			candidateID,
			"payload",
			encoding,
			payload,
		)
		if err != nil {
			return nil, err
		}
		var candidate issueintel.CorrectionCandidate
		if json.Unmarshal(payload, &candidate) != nil ||
			candidate.CandidateID != candidateID ||
			candidate.Project.Identity != projectIdentity {
			return nil, errors.New(
				"decode Mission Pack correction candidate",
			)
		}
		result[candidateID] = candidate
	}
	return result, nil
}

func readMissionPackSessions(
	ctx context.Context,
	tx *sql.Tx,
	projectIdentity string,
	limit int,
) ([]missionpack.EvidenceSession, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT session_key, agent, project_path, coverage,
			started_at, ended_at
		FROM transcript_sessions
		WHERE project_identity = ?
		ORDER BY COALESCE(ended_at, started_at, updated_at) DESC,
			session_key ASC
		LIMIT ?`,
		projectIdentity,
		limit,
	)
	if err != nil {
		return nil, errors.New("read Mission Pack transcript sessions")
	}
	defer rows.Close()
	var result []missionpack.EvidenceSession
	for rows.Next() {
		var value missionpack.EvidenceSession
		var startedAt, endedAt sql.NullString
		if err := rows.Scan(
			&value.SessionKey,
			&value.Harness,
			&value.ProjectPath,
			&value.Coverage,
			&startedAt,
			&endedAt,
		); err != nil {
			return nil, errors.New("read Mission Pack transcript session")
		}
		if startedAt.Valid {
			value.StartedAt, err = parseProjectionTime(startedAt.String)
			if err != nil {
				return nil, errors.New(
					"decode Mission Pack session start",
				)
			}
		}
		if endedAt.Valid {
			value.EndedAt, err = parseProjectionTime(endedAt.String)
			if err != nil {
				return nil, errors.New(
					"decode Mission Pack session end",
				)
			}
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read Mission Pack transcript sessions")
	}
	return result, nil
}

func (s *Store) readMissionPackCommands(
	ctx context.Context,
	tx *sql.Tx,
	sessions []missionpack.EvidenceSession,
	limit int,
) ([]missionpack.SuccessfulCommand, error) {
	if len(sessions) == 0 {
		return nil, nil
	}
	placeholders, args := missionPackSessionArgs(sessions)
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, `
		SELECT turn_id, source_record_key, session_key, turn_index,
			occurred_at, role, tool_name, model, input_tokens, output_tokens,
			cache_read_tokens, cache_write_tokens, cost_usd, payload,
			payload_encoding
		FROM transcript_turns
		WHERE session_key IN (`+placeholders+`)
			AND role IN ('tool_call', 'tool_result')
		ORDER BY occurred_at DESC, turn_index DESC, turn_id ASC
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, errors.New("read Mission Pack command turns")
	}
	var turns []transcript.Turn
	for rows.Next() {
		turn, scanErr := s.scanTranscriptTurn(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		turns = append(turns, turn)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, errors.New("read Mission Pack command turns")
	}
	if err := rows.Close(); err != nil {
		return nil, errors.New("close Mission Pack command turns")
	}
	return successfulMissionPackCommands(turns), nil
}

func successfulMissionPackCommands(
	turns []transcript.Turn,
) []missionpack.SuccessfulCommand {
	type call struct {
		command string
		turn    transcript.Turn
	}
	calls := make(map[string]call)
	results := make(map[string]transcript.Turn)
	for _, turn := range turns {
		key := turn.SessionKey + "\x00" +
			strings.TrimSpace(turn.Payload.ToolCallID)
		if turn.Payload.ToolCallID == "" {
			continue
		}
		switch turn.Role {
		case transcript.RoleToolCall:
			command := missionPackCommand(turn)
			if command != "" {
				if _, exists := calls[key]; !exists {
					calls[key] = call{command: command, turn: turn}
				}
			}
		case transcript.RoleToolResult:
			current, exists := results[key]
			if !exists || missionPackResultEvidenceRank(turn) >
				missionPackResultEvidenceRank(current) {
				results[key] = turn
			}
		}
	}
	var result []missionpack.SuccessfulCommand
	for key, value := range calls {
		outcome, ok := results[key]
		if !ok || !missionPackCommandSucceeded(outcome) {
			continue
		}
		turnIndex := value.turn.TurnIndex
		byteOffset := value.turn.Payload.JSONLByteOffset
		observedAt := value.turn.OccurredAt.UTC()
		result = append(result, missionpack.SuccessfulCommand{
			Command:     value.command,
			SucceededAt: outcome.OccurredAt.UTC(),
			Source: missionpack.SourceRef{
				Kind:            "transcript_turn",
				SessionKey:      value.turn.SessionKey,
				TurnIndex:       &turnIndex,
				SourceFileID:    value.turn.Payload.SourceFileID,
				JSONLByteOffset: &byteOffset,
				ObservedAt:      &observedAt,
			},
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].SucceededAt.Equal(result[j].SucceededAt) {
			return result[i].SucceededAt.After(result[j].SucceededAt)
		}
		if result[i].Command != result[j].Command {
			return result[i].Command < result[j].Command
		}
		return result[i].Source.SessionKey < result[j].Source.SessionKey
	})
	return result
}

func missionPackResultEvidenceRank(turn transcript.Turn) int {
	if turn.Payload.ExitCode != nil {
		return 2
	}
	if turn.Payload.ToolIsError != nil {
		return 1
	}
	return 0
}

func missionPackCommand(turn transcript.Turn) string {
	if command := strings.TrimSpace(turn.Payload.RawCommand); command != "" {
		return command
	}
	if len(turn.Payload.ToolInput) == 0 {
		return ""
	}
	var value any
	if json.Unmarshal(turn.Payload.ToolInput, &value) != nil {
		return ""
	}
	return findMissionPackCommand(value)
}

func findMissionPackCommand(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"command", "cmd", "script"} {
			if candidate, ok := typed[key].(string); ok {
				return strings.TrimSpace(candidate)
			}
		}
		for _, nested := range typed {
			if candidate := findMissionPackCommand(nested); candidate != "" {
				return candidate
			}
		}
	case []any:
		for _, nested := range typed {
			if candidate := findMissionPackCommand(nested); candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

func missionPackCommandSucceeded(turn transcript.Turn) bool {
	if turn.Payload.ToolIsError != nil && *turn.Payload.ToolIsError {
		return false
	}
	if turn.Payload.ExitCode != nil {
		return *turn.Payload.ExitCode == 0
	}
	return turn.Payload.ToolIsError != nil && !*turn.Payload.ToolIsError
}

func (s *Store) readMissionPackFacts(
	ctx context.Context,
	tx *sql.Tx,
	sessions []missionpack.EvidenceSession,
	limit int,
) ([]missionpack.CanonicalFact, error) {
	if len(sessions) == 0 {
		return nil, nil
	}
	placeholders, args := missionPackSessionArgs(sessions)
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, `
		SELECT e.event_id, e.session_key, e.canonical_json,
			e.canonical_encoding
		FROM events e
		WHERE e.session_key IN (`+placeholders+`)
		ORDER BY e.occurred_at DESC, e.source_sequence DESC, e.event_id ASC
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, errors.New("read Mission Pack canonical events")
	}
	type observedFact struct {
		kind     string
		value    string
		sessions map[string]bool
		at       time.Time
		sources  []missionpack.SourceRef
	}
	facts := make(map[string]*observedFact)
	for rows.Next() {
		var eventID, sessionKey, encoding string
		var payload []byte
		if err := rows.Scan(
			&eventID,
			&sessionKey,
			&payload,
			&encoding,
		); err != nil {
			rows.Close()
			return nil, errors.New("read Mission Pack canonical event")
		}
		event, err := s.decodeEvent(eventID, encoding, payload)
		if err != nil {
			rows.Close()
			return nil, err
		}
		kind, value := missionPackFact(event)
		if kind == "" || value == "" {
			continue
		}
		key := kind + "\x00" + value
		current := facts[key]
		if current == nil {
			current = &observedFact{
				kind:     kind,
				value:    value,
				sessions: make(map[string]bool),
			}
			facts[key] = current
		}
		current.sessions[sessionKey] = true
		if event.OccurredAt.After(current.at) {
			current.at = event.OccurredAt
		}
		if len(current.sources) < missionpack.MaxSourcesPerItem &&
			!missionPackSourcesContainSession(
				current.sources,
				sessionKey,
			) {
			observedAt := event.OccurredAt.UTC()
			current.sources = append(current.sources, missionpack.SourceRef{
				Kind:       "numbat_event",
				SessionKey: sessionKey,
				EventID:    event.EventID,
				ObservedAt: &observedAt,
			})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, errors.New("read Mission Pack canonical events")
	}
	if err := rows.Close(); err != nil {
		return nil, errors.New("close Mission Pack canonical events")
	}
	var result []missionpack.CanonicalFact
	for key, value := range facts {
		if len(value.sessions) < minMissionPackFactSessions {
			continue
		}
		result = append(result, missionpack.CanonicalFact{
			FactID:       key,
			Kind:         value.kind,
			Value:        value.value,
			SessionCount: len(value.sessions),
			ObservedAt:   value.at.UTC(),
			Sources:      value.sources,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		leftRank := missionPackFactRank(result[i])
		rightRank := missionPackFactRank(result[j])
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if result[i].SessionCount != result[j].SessionCount {
			return result[i].SessionCount > result[j].SessionCount
		}
		if !result[i].ObservedAt.Equal(result[j].ObservedAt) {
			return result[i].ObservedAt.After(result[j].ObservedAt)
		}
		if result[i].Value != result[j].Value {
			return result[i].Value < result[j].Value
		}
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].FactID < result[j].FactID
	})
	selected := make([]missionpack.CanonicalFact, 0, missionpack.MaxContextFacts)
	toolFacts := 0
	for _, fact := range result {
		if fact.Kind != missionpack.CanonicalFactFileWritten {
			if toolFacts == maxMissionPackToolFacts {
				continue
			}
			toolFacts++
		}
		selected = append(selected, fact)
		if len(selected) == missionpack.MaxContextFacts {
			break
		}
	}
	return selected, nil
}

func missionPackFact(event canonical.Event) (string, string) {
	if event.Observation.Resource == nil {
		return "", ""
	}
	resource := event.Observation.Resource
	value := strings.TrimSpace(resource.Name)
	if !safeMissionPackFactValue(value) {
		return "", ""
	}
	switch {
	case event.Observation.Type == "file.write" &&
		resource.Kind == "file":
		value = filepath.ToSlash(filepath.Clean(
			strings.ReplaceAll(value, "\\", "/"),
		))
		if filepath.IsAbs(value) || value == "." ||
			strings.HasPrefix(value, "../") ||
			isWindowsAbsoluteMissionPackPath(value) {
			return "", ""
		}
		return missionpack.CanonicalFactFileWritten, value
	case (event.Observation.Type == "command.exec" ||
		event.Observation.Type == "command.result") &&
		resource.Kind == "command" &&
		safeMissionPackExecutableName(value) &&
		!genericMissionPackExecutable(value):
		return missionpack.CanonicalFactCommandExecutable, value
	case (event.Observation.Type == "tool.call" ||
		event.Observation.Type == "tool.result") &&
		resource.Kind == "mcp" &&
		safeMissionPackContextName(value) &&
		!genericMissionPackTool(value):
		return missionpack.CanonicalFactMCPTool, value
	default:
		return "", ""
	}
}

func missionPackSourcesContainSession(
	sources []missionpack.SourceRef,
	sessionKey string,
) bool {
	for _, source := range sources {
		if source.SessionKey == sessionKey {
			return true
		}
	}
	return false
}

func isWindowsAbsoluteMissionPackPath(value string) bool {
	return len(value) >= 2 &&
		((value[0] >= 'A' && value[0] <= 'Z') ||
			(value[0] >= 'a' && value[0] <= 'z')) &&
		value[1] == ':'
}

func missionPackFactRank(value missionpack.CanonicalFact) int {
	switch {
	case value.Kind == missionpack.CanonicalFactFileWritten:
		return 0
	case value.Kind == missionpack.CanonicalFactCommandExecutable &&
		specializedMissionPackExecutable(value.Value):
		return 1
	case value.Kind == missionpack.CanonicalFactMCPTool:
		return 2
	case value.Kind == missionpack.CanonicalFactCommandExecutable:
		return 3
	default:
		return 4
	}
}

func specializedMissionPackExecutable(value string) bool {
	value = strings.TrimSuffix(
		strings.ToLower(strings.TrimSpace(value)),
		".exe",
	)
	switch value {
	case "go", "make", "cargo", "pnpm", "npm", "yarn", "bun",
		"python", "python3", "pytest":
		return true
	default:
		return false
	}
}

func genericMissionPackExecutable(value string) bool {
	value = strings.TrimSuffix(
		strings.ToLower(strings.TrimSpace(value)),
		".exe",
	)
	switch value {
	case "pwd", "cd", "ls", "cat", "grep", "rg", "sed", "awk",
		"head", "tail", "echo", "printf",
		"sh", "bash", "zsh", "fish", "dash", "ash", "ksh", "csh",
		"tcsh", "pwsh", "powershell", "cmd", "nu",
		"nushell", "xonsh", "shell",
		"belay", "numbat", "claude", "codex":
		return true
	default:
		return false
	}
}

func genericMissionPackTool(value string) bool {
	value = strings.TrimSpace(value)
	fullName := normalizedMissionPackToolName(value)
	if belayLocalMCPTool(fullName) {
		return true
	}
	if strings.HasSuffix(fullName, "toolsearch") ||
		strings.HasSuffix(fullName, "toolsearchtool") ||
		strings.HasSuffix(fullName, "readfile") ||
		strings.HasSuffix(fullName, "writefile") ||
		strings.HasSuffix(fullName, "editfile") ||
		strings.HasSuffix(fullName, "listfiles") ||
		strings.HasSuffix(fullName, "applypatch") ||
		strings.HasSuffix(fullName, "execcommand") ||
		strings.HasSuffix(fullName, "writestdin") ||
		strings.HasSuffix(fullName, "viewimage") ||
		strings.HasSuffix(fullName, "requestuserinput") ||
		strings.HasSuffix(fullName, "listmcpresources") ||
		strings.HasSuffix(fullName, "listmcpresourcetemplates") ||
		strings.HasSuffix(fullName, "readmcpresource") {
		return true
	}
	if separator := strings.LastIndexAny(value, "/\\:."); separator >= 0 {
		value = value[separator+1:]
	}
	switch normalizedMissionPackToolName(value) {
	case "read", "readfile", "write", "writefile", "edit", "editfile",
		"glob", "grep", "search", "list", "listfiles",
		"shell", "bash", "exec", "execcommand":
		return true
	default:
		return false
	}
}

func belayLocalMCPTool(normalized string) bool {
	const (
		belayPrefix         = "belay"
		belayLocalPrefix    = "belaylocal"
		belayMCPPrefix      = "belaymcp"
		belayLocalMCPPrefix = "belaylocalmcp"
		mcpBelayPrefix      = "mcpbelay"
		mcpBelayLocalPrefix = "mcpbelaylocal"
	)
	for _, tool := range []string{
		"listsessions",
		"getsession",
		"getsessiontimeline",
		"queryactivity",
		"listfindings",
		"getstats",
		"listissues",
		"getissue",
		"lookupsessionevents",
		"gettopissues",
		"getissueexcerpts",
		"proposefix",
		"recordfixapplied",
		"getfixstatus",
		"getmissionpack",
	} {
		switch normalized {
		case tool,
			belayPrefix + tool,
			belayLocalPrefix + tool,
			belayMCPPrefix + tool,
			belayLocalMCPPrefix + tool,
			mcpBelayPrefix + tool,
			mcpBelayLocalPrefix + tool:
			return true
		}
	}
	return false
}

func normalizedMissionPackToolName(value string) string {
	var normalized strings.Builder
	for _, character := range strings.ToLower(value) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			normalized.WriteRune(character)
		}
	}
	return normalized.String()
}

func safeMissionPackContextName(value string) bool {
	if utf8.RuneCountInString(value) > 128 {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) {
			return false
		}
		switch {
		case unicode.IsLetter(character), unicode.IsDigit(character):
		case strings.ContainsRune("._+-/:@\\", character):
		default:
			return false
		}
	}
	return true
}

func safeMissionPackExecutableName(value string) bool {
	return utf8.RuneCountInString(value) <= 64 &&
		!strings.ContainsAny(value, `/\:@`) &&
		safeMissionPackContextName(value)
}

func safeMissionPackFactValue(value string) bool {
	if value == "" || utf8.RuneCountInString(value) > 240 ||
		strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func missionPackSessionArgs(
	sessions []missionpack.EvidenceSession,
) (string, []any) {
	placeholders := make([]string, 0, len(sessions))
	args := make([]any, 0, len(sessions)+1)
	for _, session := range sessions {
		placeholders = append(placeholders, "?")
		args = append(args, session.SessionKey)
	}
	return strings.Join(placeholders, ","), args
}
