package local

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

const issueCursorLifetime = 15 * time.Minute

var (
	ErrIssueSnapshotExpired = model.ErrIssueSnapshotExpired
	ErrIssueSnapshotInvalid = model.ErrIssueSnapshotInvalid
)

func (s *Store) QueryIssues(
	ctx context.Context,
	query model.IssueQuery,
) (model.IssuePage, error) {
	query.Limit = boundedReadLimit(query.Limit, 20, 100)
	if err := normalizeIssueFilterModes(&query.Filter); err != nil {
		return model.IssuePage{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		if ctx.Err() != nil {
			return model.IssuePage{}, ctx.Err()
		}
		return model.IssuePage{}, errors.New("begin issue snapshot read")
	}
	defer tx.Rollback()
	state, err := s.issueMaterializedSnapshot(
		ctx,
		tx,
		query.CursorEpoch,
		query.Snapshot,
		query.RetentionGeneration,
		query.IssuedAt,
	)
	if err != nil {
		return model.IssuePage{}, err
	}
	matchClauses := []string{"1 = 1"}
	matchArgs := make([]any, 0, 16)
	if query.Filter.Harness != "" {
		matchClauses = append(matchClauses, `EXISTS (
			SELECT 1 FROM issue_summary_harnesses ish
			WHERE ish.summary_revision_id = sr.summary_revision_id
				AND ish.harness = LOWER(?)
		)`)
		matchArgs = append(matchArgs, query.Filter.Harness)
	}
	if query.Filter.ObservedAfter != nil {
		matchClauses = append(matchClauses, "sr.last_observed_at >= ?")
		matchArgs = append(matchArgs, formatProjectionTime(*query.Filter.ObservedAfter))
	}
	if query.Filter.SessionID != "" {
		matchClauses = append(matchClauses, `EXISTS (
			SELECT 1 FROM issue_summary_sessions iss
			WHERE iss.summary_revision_id = sr.summary_revision_id
				AND iss.session_key = ?
		)`)
		matchArgs = append(matchArgs, query.Filter.SessionID)
	}
	switch query.Filter.AttentionKind {
	case model.AttentionKindIssue:
		matchClauses = append(matchClauses, "sr.category <> 'evidence_gap'")
	case model.AttentionKindEvidenceGap:
		matchClauses = append(matchClauses, "sr.category = 'evidence_gap'")
	case model.AttentionKindAll:
	}
	switch query.Filter.Experimental {
	case model.ExperimentalStable:
		matchClauses = append(matchClauses, "sr.experimental = 0")
	case model.ExperimentalOnly:
		matchClauses = append(matchClauses, "sr.experimental = 1")
	case model.ExperimentalInclude:
	}

	summaryClauses := []string{"1 = 1"}
	summaryArgs := make([]any, 0, 16)
	if query.Filter.Severity != "" {
		summaryClauses = append(summaryClauses, "sr.severity = ?")
		summaryArgs = append(summaryArgs, query.Filter.Severity)
	}
	if query.Filter.Category != "" {
		summaryClauses = append(summaryClauses, "sr.category = ?")
		summaryArgs = append(summaryArgs, query.Filter.Category)
	}
	if query.Filter.Origin != "" {
		summaryClauses = append(summaryClauses, "sr.origin = ?")
		summaryArgs = append(summaryArgs, query.Filter.Origin)
	}
	if query.Filter.AnalysisStatus != "" {
		summaryClauses = append(summaryClauses, "sr.analysis_status = ?")
		summaryArgs = append(summaryArgs, query.Filter.AnalysisStatus)
	}
	if query.Filter.Recurrence == "single" {
		summaryClauses = append(summaryClauses, "sr.repeated = 0")
	} else if query.Filter.Recurrence == "repeated" {
		summaryClauses = append(summaryClauses, "sr.repeated = 1")
	}
	if query.Filter.FingerprintID != "" {
		summaryClauses = append(summaryClauses, "sr.fingerprint_id = ?")
		summaryArgs = append(summaryArgs, query.Filter.FingerprintID)
	}
	if query.Filter.IssueID != "" {
		summaryClauses = append(summaryClauses, "sr.issue_id = ?")
		summaryArgs = append(summaryArgs, query.Filter.IssueID)
	}
	if query.Cursor != nil {
		repeated := 0
		if query.Cursor.Repeated {
			repeated = 1
		}
		summaryClauses = append(summaryClauses, `(
			sr.severity_rank < ? OR
			(sr.severity_rank = ? AND sr.repeated < ?) OR
			(sr.severity_rank = ? AND sr.repeated = ? AND sr.last_observed_at < ?) OR
			(sr.severity_rank = ? AND sr.repeated = ? AND sr.last_observed_at = ? AND sr.issue_id > ?)
		)`)
		lastObserved := formatProjectionTime(query.Cursor.LastObserved)
		summaryArgs = append(
			summaryArgs,
			query.Cursor.SeverityRank,
			query.Cursor.SeverityRank,
			repeated,
			query.Cursor.SeverityRank,
			repeated,
			lastObserved,
			query.Cursor.SeverityRank,
			repeated,
			lastObserved,
			query.Cursor.IssueID,
		)
	}

	args := []any{state.snapshot, state.snapshot}
	args = append(args, matchArgs...)
	args = append(args, summaryArgs...)
	args = append(args, query.Limit+1)
	rows, err := tx.QueryContext(ctx, `
			SELECT
				sr.issue_id, sr.fingerprint_id, sr.fingerprint_version, sr.origin,
				sr.detector_id, sr.detector_version, sr.category, sr.title_code,
				sr.source_signal_code, sr.severity, sr.severity_rank,
				sr.confidence, sr.scope_quality,
			sr.first_observed_at, sr.last_observed_at, sr.occurrence_count,
			sr.session_count, sr.repeated,
			COALESCE((
				SELECT GROUP_CONCAT(ordered.harness)
				FROM (
					SELECT ish.harness
					FROM issue_summary_harnesses ish
					WHERE ish.summary_revision_id = sr.summary_revision_id
					ORDER BY ish.harness
				) ordered
			), ''),
			sr.analysis_status, sr.evidence_complete,
			sr.retained_history_only, sr.experimental
		FROM issue_summary_revisions sr INDEXED BY issue_summary_order_snapshot_idx
		WHERE sr.visible_from_generation <= ?
			AND (
				sr.visible_until_generation IS NULL OR
				sr.visible_until_generation > ?
			)
			AND `+strings.Join(matchClauses, " AND ")+`
			AND `+strings.Join(summaryClauses, " AND ")+`
		ORDER BY sr.severity_rank DESC, sr.repeated DESC,
			sr.last_observed_at DESC, sr.issue_id ASC
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return model.IssuePage{}, fmt.Errorf("query issues: %w", err)
	}
	defer rows.Close()
	summaries := make([]model.IssueSummary, 0, query.Limit+1)
	for rows.Next() {
		summary, err := scanIssueSummary(rows)
		if err != nil {
			return model.IssuePage{}, err
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return model.IssuePage{}, err
	}
	hasMore := len(summaries) > query.Limit
	if hasMore {
		summaries = summaries[:query.Limit]
	}
	coverage, err := materializedIssueAnalysisCoverage(ctx, tx, state.snapshot)
	if err != nil {
		return model.IssuePage{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.IssuePage{}, errors.New("complete issue snapshot read")
	}
	return model.IssuePage{
		Data:                summaries,
		Analysis:            coverage,
		CursorEpoch:         state.epoch,
		Snapshot:            state.snapshot,
		RetentionGeneration: state.retentionGeneration,
		IssuedAt:            state.issuedAt,
		HasMore:             hasMore,
	}, nil
}

func (s *Store) QueryIssueOccurrences(
	ctx context.Context,
	query model.IssueOccurrenceQuery,
) (model.IssueOccurrencePage, error) {
	if query.IssueID == "" {
		return model.IssueOccurrencePage{}, errors.New("issue ID is required")
	}
	query.Limit = boundedReadLimit(query.Limit, 20, 100)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		if ctx.Err() != nil {
			return model.IssueOccurrencePage{}, ctx.Err()
		}
		return model.IssueOccurrencePage{}, errors.New("begin issue occurrence snapshot read")
	}
	defer tx.Rollback()
	state, err := s.issueMaterializedSnapshot(
		ctx,
		tx,
		query.CursorEpoch,
		query.Snapshot,
		query.RetentionGeneration,
		query.IssuedAt,
	)
	if err != nil {
		return model.IssueOccurrencePage{}, err
	}
	clauses := []string{
		"io.issue_id = ?",
		"io.visible_from_generation <= ?",
		"(io.visible_until_generation IS NULL OR io.visible_until_generation > ?)",
		"sar.visible_from_generation <= ?",
		"(sar.visible_until_generation IS NULL OR sar.visible_until_generation > ?)",
	}
	args := []any{
		query.IssueID,
		state.snapshot,
		state.snapshot,
		state.snapshot,
		state.snapshot,
	}
	if query.Cursor != nil {
		clauses = append(clauses, `(
			io.last_observed_at < ? OR
			(io.last_observed_at = ? AND io.occurrence_id > ?)
		)`)
		lastObserved := formatProjectionTime(query.Cursor.LastObserved)
		args = append(args, lastObserved, lastObserved, query.Cursor.OccurrenceID)
	}
	args = append(args, query.Limit+1)
	rows, err := tx.QueryContext(ctx, `
		SELECT
			io.revision_id, io.occurrence_id, io.issue_id, io.fingerprint_id,
			io.fingerprint_version, io.origin, COALESCE(io.origin_record_id, ''),
				io.session_key, io.harness, io.detector_id, io.detector_version,
				io.projection_version, io.category, io.title_code,
				io.source_signal_code, io.severity,
			io.confidence, io.scope_quality, io.first_observed_at,
			io.last_observed_at, io.evidence_complete, io.retained_history_only,
			io.experimental, sar.status, io.analysis_generation, io.evidence_payload,
			io.evidence_encoding, COALESCE(io.fingerprint_scope_id, '')
		FROM issue_occurrences io
		JOIN session_analysis_revisions sar ON sar.session_key = io.session_key
		WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY io.last_observed_at DESC, io.occurrence_id ASC
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return model.IssueOccurrencePage{}, fmt.Errorf("query issue occurrences: %w", err)
	}
	defer rows.Close()
	occurrences := make([]model.IssueOccurrence, 0, query.Limit+1)
	for rows.Next() {
		occurrence, err := s.scanIssueOccurrence(rows)
		if err != nil {
			return model.IssueOccurrencePage{}, err
		}
		occurrences = append(occurrences, occurrence)
	}
	if err := rows.Err(); err != nil {
		return model.IssueOccurrencePage{}, err
	}
	hasMore := len(occurrences) > query.Limit
	if hasMore {
		occurrences = occurrences[:query.Limit]
	}
	if err := tx.Commit(); err != nil {
		return model.IssueOccurrencePage{}, errors.New("complete issue occurrence snapshot read")
	}
	return model.IssueOccurrencePage{
		Data:                occurrences,
		CursorEpoch:         state.epoch,
		Snapshot:            state.snapshot,
		RetentionGeneration: state.retentionGeneration,
		IssuedAt:            state.issuedAt,
		HasMore:             hasMore,
	}, nil
}

type issueMaterializedSnapshotState struct {
	epoch               string
	snapshot            int64
	retentionGeneration int64
	issuedAt            time.Time
}

func (s *Store) issueMaterializedSnapshot(
	ctx context.Context,
	queryer queryRower,
	claimedEpoch string,
	requested int64,
	claimedRetentionGeneration int64,
	issuedAt time.Time,
) (issueMaterializedSnapshotState, error) {
	var state issueMaterializedSnapshotState
	var current, materialized, oldest int64
	var readiness string
	if err := queryer.QueryRowContext(ctx, `
		SELECT COALESCE(ism.cursor_epoch, ''), ism.readiness,
			ipm.current_generation, ism.materialized_generation,
			ism.oldest_materialized_generation, ipm.retention_generation
		FROM issue_summary_metadata ism
		JOIN issue_projection_metadata ipm ON ipm.singleton = ism.singleton
		WHERE ism.singleton = 1`,
	).Scan(
		&state.epoch,
		&readiness,
		&current,
		&materialized,
		&oldest,
		&state.retentionGeneration,
	); err != nil {
		if ctx.Err() != nil {
			return issueMaterializedSnapshotState{}, ctx.Err()
		}
		return issueMaterializedSnapshotState{}, errors.New("read issue materialized snapshot")
	}
	if state.epoch == "" ||
		readiness != "ready" ||
		materialized != current {
		return issueMaterializedSnapshotState{}, errors.New("issue summary projection is not ready")
	}
	now := s.nowUTC()
	freshRequest := requested == 0 &&
		claimedEpoch == "" &&
		claimedRetentionGeneration == 0 &&
		issuedAt.IsZero()
	if freshRequest {
		state.snapshot = current
		state.issuedAt = now
		return state, nil
	}
	if requested < 0 ||
		claimedEpoch == "" ||
		claimedRetentionGeneration < 1 ||
		issuedAt.IsZero() {
		return issueMaterializedSnapshotState{}, model.ErrIssueSnapshotInvalid
	}
	if claimedEpoch != state.epoch ||
		claimedRetentionGeneration != state.retentionGeneration ||
		requested < oldest {
		return issueMaterializedSnapshotState{}, model.ErrIssueSnapshotExpired
	}
	if requested > current {
		return issueMaterializedSnapshotState{}, model.ErrIssueSnapshotInvalid
	}
	issuedAt = issuedAt.UTC()
	if issuedAt.After(now) {
		return issueMaterializedSnapshotState{}, model.ErrIssueSnapshotInvalid
	}
	if now.Sub(issuedAt) > issueCursorLifetime {
		return issueMaterializedSnapshotState{}, model.ErrIssueSnapshotExpired
	}
	state.snapshot = requested
	state.issuedAt = issuedAt
	return state, nil
}

func (s *Store) issueSnapshot(
	ctx context.Context,
	queryer queryRower,
	requested int64,
	issuedAt time.Time,
) (int64, error) {
	var current, oldest int64
	if err := queryer.QueryRowContext(ctx, `
		SELECT current_generation, oldest_retained_generation
		FROM issue_projection_metadata
		WHERE singleton = 1`,
	).Scan(&current, &oldest); err != nil {
		return 0, errors.New("read issue projection generation")
	}
	if requested == 0 {
		return current, nil
	}
	if requested < 0 {
		return 0, model.ErrIssueSnapshotInvalid
	}
	if issuedAt.IsZero() {
		return 0, model.ErrIssueSnapshotInvalid
	}
	if requested < oldest {
		return 0, model.ErrIssueSnapshotExpired
	}
	if requested > current {
		return 0, model.ErrIssueSnapshotInvalid
	}
	now := s.nowUTC()
	issuedAt = issuedAt.UTC()
	if issuedAt.After(now) {
		return 0, model.ErrIssueSnapshotInvalid
	}
	if now.Sub(issuedAt) > issueCursorLifetime {
		return 0, model.ErrIssueSnapshotExpired
	}
	return requested, nil
}

func normalizeIssueFilterModes(filter *model.IssueFilter) error {
	if filter.AttentionKind == "" {
		filter.AttentionKind = model.AttentionKindIssue
	}
	switch filter.AttentionKind {
	case model.AttentionKindIssue, model.AttentionKindEvidenceGap, model.AttentionKindAll:
	default:
		return errors.New("unsupported issue attention kind")
	}
	if filter.Experimental == "" {
		filter.Experimental = model.ExperimentalStable
	}
	switch filter.Experimental {
	case model.ExperimentalStable, model.ExperimentalInclude, model.ExperimentalOnly:
	default:
		return errors.New("unsupported issue experimental mode")
	}
	return nil
}

func issueAnalysisCoverage(
	ctx context.Context,
	queryer queryRower,
	snapshot int64,
) (model.IssueAnalysisCoverage, error) {
	var result model.IssueAnalysisCoverage
	var analysisThrough sql.NullString
	if err := queryer.QueryRowContext(ctx, `
		WITH event_sessions AS (
			SELECT DISTINCT session_key FROM events
		),
		visible_analysis AS (
			SELECT session_key, status, scope_quality, created_at
			FROM session_analysis_revisions
			WHERE visible_from_generation <= ?
				AND (visible_until_generation IS NULL OR visible_until_generation > ?)
		)
		SELECT
			COALESCE(SUM(CASE WHEN va.status = 'current' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN va.status = 'pending' OR va.status IS NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN va.status = 'failed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN va.status = 'truncated' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN COALESCE(va.scope_quality, 'unscoped') = 'unscoped'
				THEN 1 ELSE 0 END), 0),
			MAX(CASE WHEN va.status = 'current' THEN va.created_at END)
		FROM event_sessions es
		LEFT JOIN visible_analysis va ON va.session_key = es.session_key`,
		snapshot,
		snapshot,
	).Scan(
		&result.CurrentSessions,
		&result.PendingSessions,
		&result.FailedSessions,
		&result.TruncatedSessions,
		&result.UnscopedSessions,
		&analysisThrough,
	); err != nil {
		return model.IssueAnalysisCoverage{}, errors.New("read issue analysis coverage")
	}
	if analysisThrough.Valid {
		parsed, err := parseProjectionTime(analysisThrough.String)
		if err != nil {
			return model.IssueAnalysisCoverage{}, errors.New("decode issue analysis timestamp")
		}
		result.AnalysisThrough = parsed
	}
	result.Complete = result.PendingSessions == 0 &&
		result.FailedSessions == 0 &&
		result.TruncatedSessions == 0
	return result, nil
}

func materializedIssueAnalysisCoverage(
	ctx context.Context,
	queryer queryRower,
	snapshot int64,
) (model.IssueAnalysisCoverage, error) {
	var result model.IssueAnalysisCoverage
	var analysisThrough sql.NullString
	if err := queryer.QueryRowContext(ctx, `
		SELECT current_sessions, pending_sessions, failed_sessions,
			truncated_sessions, unscoped_sessions, analysis_through, complete
		FROM issue_analysis_coverage_revisions
		WHERE visible_from_generation <= ?
			AND (
				visible_until_generation IS NULL OR
				visible_until_generation > ?
			)
		ORDER BY visible_from_generation DESC
		LIMIT 1`,
		snapshot, snapshot,
	).Scan(
		&result.CurrentSessions,
		&result.PendingSessions,
		&result.FailedSessions,
		&result.TruncatedSessions,
		&result.UnscopedSessions,
		&analysisThrough,
		&result.Complete,
	); err != nil {
		if ctx.Err() != nil {
			return model.IssueAnalysisCoverage{}, ctx.Err()
		}
		if errors.Is(err, sql.ErrNoRows) {
			return model.IssueAnalysisCoverage{}, model.ErrIssueSnapshotExpired
		}
		return model.IssueAnalysisCoverage{}, errors.New("read materialized issue analysis coverage")
	}
	if analysisThrough.Valid {
		parsed, err := parseProjectionTime(analysisThrough.String)
		if err != nil {
			return model.IssueAnalysisCoverage{}, errors.New("decode issue analysis timestamp")
		}
		result.AnalysisThrough = parsed
	}
	return result, nil
}

func scanIssueSummary(row rowScanner) (model.IssueSummary, error) {
	var result model.IssueSummary
	var severityRank, sessionCount, repeated int
	var firstObserved, lastObserved, harnesses string
	var evidenceComplete, retainedHistoryOnly, experimental int
	var sourceSignalCode sql.NullString
	if err := row.Scan(
		&result.IssueID,
		&result.FingerprintID,
		&result.FingerprintVersion,
		&result.Origin,
		&result.DetectorID,
		&result.DetectorVersion,
		&result.Category,
		&result.TitleCode,
		&sourceSignalCode,
		&result.Severity,
		&severityRank,
		&result.Confidence,
		&result.ScopeQuality,
		&firstObserved,
		&lastObserved,
		&result.OccurrenceCount,
		&sessionCount,
		&repeated,
		&harnesses,
		&result.AnalysisStatus,
		&evidenceComplete,
		&retainedHistoryOnly,
		&experimental,
	); err != nil {
		return model.IssueSummary{}, err
	}
	if sourceSignalCode.Valid {
		result.SourceSignalCode = &sourceSignalCode.String
	}
	result.SessionCount = sessionCount
	result.EvidenceComplete = evidenceComplete == 1
	result.RetainedHistoryOnly = retainedHistoryOnly == 1
	result.Experimental = experimental == 1
	var err error
	result.FirstObservedAt, err = parseProjectionTime(firstObserved)
	if err != nil {
		return model.IssueSummary{}, errors.New("decode issue first-observed timestamp")
	}
	result.LastObservedAt, err = parseProjectionTime(lastObserved)
	if err != nil {
		return model.IssueSummary{}, errors.New("decode issue last-observed timestamp")
	}
	if harnesses != "" {
		result.Harnesses = strings.Split(harnesses, ",")
		sort.Strings(result.Harnesses)
	}
	return result, nil
}

func (s *Store) scanIssueOccurrence(row rowScanner) (model.IssueOccurrence, error) {
	var revisionID string
	var result model.IssueOccurrence
	var firstObserved, lastObserved, encoding string
	var evidenceComplete, retainedHistoryOnly, experimental int
	var sourceSignalCode sql.NullString
	var evidence []byte
	if err := row.Scan(
		&revisionID,
		&result.OccurrenceID,
		&result.IssueID,
		&result.FingerprintID,
		&result.FingerprintVersion,
		&result.Origin,
		&result.OriginRecordID,
		&result.SessionID,
		&result.Harness,
		&result.Provenance.DetectorID,
		&result.Provenance.DetectorVersion,
		&result.Provenance.ProjectionVersion,
		&result.Category,
		&result.TitleCode,
		&sourceSignalCode,
		&result.Severity,
		&result.Confidence,
		&result.ScopeQuality,
		&firstObserved,
		&lastObserved,
		&evidenceComplete,
		&retainedHistoryOnly,
		&experimental,
		&result.AnalysisStatus,
		&result.AnalysisGeneration,
		&evidence,
		&encoding,
		&result.FingerprintScopeID,
	); err != nil {
		return model.IssueOccurrence{}, err
	}
	if sourceSignalCode.Valid {
		result.SourceSignalCode = &sourceSignalCode.String
	}
	result.Provenance.FingerprintVersion = result.FingerprintVersion
	result.EvidenceComplete = evidenceComplete == 1
	result.RetainedHistoryOnly = retainedHistoryOnly == 1
	result.Experimental = experimental == 1
	var err error
	result.FirstObservedAt, err = parseProjectionTime(firstObserved)
	if err != nil {
		return model.IssueOccurrence{}, errors.New("decode issue occurrence start")
	}
	result.LastObservedAt, err = parseProjectionTime(lastObserved)
	if err != nil {
		return model.IssueOccurrence{}, errors.New("decode issue occurrence end")
	}
	evidence, err = s.cipher.open(
		"issue_occurrence",
		revisionID,
		"evidence_payload",
		encoding,
		evidence,
	)
	if err != nil {
		return model.IssueOccurrence{}, err
	}
	if err := json.Unmarshal(evidence, &result.Evidence); err != nil {
		return model.IssueOccurrence{}, errors.New("decode issue occurrence evidence")
	}
	return result, nil
}
