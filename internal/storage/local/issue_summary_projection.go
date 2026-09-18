package local

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"
)

type issueSummaryProjection struct {
	issueID             string
	fingerprintID       string
	fingerprintVersion  string
	origin              string
	detectorID          string
	detectorVersion     string
	category            string
	titleCode           string
	sourceSignalCode    *string
	severity            string
	severityRank        int
	confidence          string
	scopeQuality        string
	firstObservedAt     string
	lastObservedAt      string
	occurrenceCount     int
	sessionCount        int
	repeated            int
	analysisStatus      string
	evidenceComplete    int
	retainedHistoryOnly int
	experimental        int
	harnesses           []string
	sessions            []string
}

type issueCoverageProjection struct {
	currentSessions   int
	pendingSessions   int
	failedSessions    int
	truncatedSessions int
	unscopedSessions  int
	analysisThrough   string
	complete          int
}

func (s *Store) refreshIssueProjectionIfReadyTx(
	ctx context.Context,
	tx *sql.Tx,
	generation int64,
	now string,
	affectedIssueIDs map[string]struct{},
) error {
	var readiness string
	var current, materialized int64
	if err := tx.QueryRowContext(ctx, `
		SELECT ism.readiness, ipm.current_generation,
			ism.materialized_generation
		FROM issue_summary_metadata ism
		JOIN issue_projection_metadata ipm ON ipm.singleton = ism.singleton
		WHERE ism.singleton = 1`,
	).Scan(&readiness, &current, &materialized); err != nil {
		return errors.New("read issue materialization state")
	}
	if readiness == "building" {
		return nil
	}
	if readiness != "ready" ||
		current != generation ||
		materialized != generation-1 {
		return errors.New("issue summary projection is not ready")
	}

	issueIDs := make([]string, 0, len(affectedIssueIDs))
	for issueID := range affectedIssueIDs {
		if issueID != "" {
			issueIDs = append(issueIDs, issueID)
		}
	}
	sort.Strings(issueIDs)
	for _, issueID := range issueIDs {
		if err := refreshIssueSummaryRevisionTx(
			ctx, tx, issueID, generation, now,
		); err != nil {
			return err
		}
	}
	if err := refreshIssueCoverageRevisionTx(ctx, tx, generation, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO issue_projection_generation_times (generation, generated_at)
		VALUES (?, ?)`,
		generation, now,
	); err != nil {
		return errors.New("record issue projection generation time")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE issue_summary_metadata
		SET materialized_generation = ?, updated_at = ?
		WHERE singleton = 1 AND readiness = 'ready'`,
		generation, now,
	); err != nil {
		return errors.New("advance issue materialized generation")
	}
	return s.compactIssueSummaryRevisionsTx(ctx, tx, generation, now)
}

func activeIssueIDsForSessionTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionID string,
) (map[string]struct{}, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT issue_id
		FROM issue_occurrences
		WHERE session_key = ? AND visible_until_generation IS NULL
		ORDER BY issue_id`,
		sessionID,
	)
	if err != nil {
		return nil, errors.New("read session issue identities")
	}
	defer rows.Close()
	result := make(map[string]struct{})
	for rows.Next() {
		var issueID string
		if err := rows.Scan(&issueID); err != nil {
			return nil, errors.New("decode session issue identity")
		}
		result[issueID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read session issue identities")
	}
	return result, nil
}

func mergeIssueIDs(target map[string]struct{}, source map[string]struct{}) {
	for issueID := range source {
		target[issueID] = struct{}{}
	}
}

func refreshIssueSummaryRevisionTx(
	ctx context.Context,
	tx *sql.Tx,
	issueID string,
	generation int64,
	now string,
) error {
	next, exists, err := aggregateCurrentIssueSummaryTx(ctx, tx, issueID)
	if err != nil {
		return err
	}
	current, revisionID, active, err := readActiveIssueSummaryTx(ctx, tx, issueID)
	if err != nil {
		return err
	}
	if !exists {
		if !active {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE issue_summary_revisions
			SET visible_until_generation = ?
			WHERE summary_revision_id = ? AND visible_until_generation IS NULL`,
			generation, revisionID,
		); err != nil {
			return errors.New("close removed issue summary")
		}
		return nil
	}
	if active && equalIssueSummaryProjection(current, next) {
		return nil
	}
	if active {
		if _, err := tx.ExecContext(ctx, `
			UPDATE issue_summary_revisions
			SET visible_until_generation = ?
			WHERE summary_revision_id = ? AND visible_until_generation IS NULL`,
			generation, revisionID,
		); err != nil {
			return errors.New("close prior issue summary")
		}
	}
	return insertIssueSummaryRevisionTx(ctx, tx, next, generation, now)
}

func aggregateCurrentIssueSummaryTx(
	ctx context.Context,
	tx *sql.Tx,
	issueID string,
) (issueSummaryProjection, bool, error) {
	var result issueSummaryProjection
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM issue_occurrences io
		JOIN session_analysis_revisions sar
			ON sar.session_key = io.session_key
			AND sar.visible_until_generation IS NULL
		WHERE io.issue_id = ? AND io.visible_until_generation IS NULL`,
		issueID,
	).Scan(&count); err != nil {
		return issueSummaryProjection{}, false, errors.New("count current issue summary")
	}
	if count == 0 {
		return issueSummaryProjection{}, false, nil
	}
	row := tx.QueryRowContext(ctx, `
		WITH visible AS (
			SELECT io.*, sar.status AS current_status
			FROM issue_occurrences io
			JOIN session_analysis_revisions sar
				ON sar.session_key = io.session_key
				AND sar.visible_until_generation IS NULL
			WHERE io.issue_id = ? AND io.visible_until_generation IS NULL
		)
			SELECT
				MIN(fingerprint_id), MIN(fingerprint_version), MIN(origin),
				MIN(detector_id), MAX(detector_version), MIN(category),
				MIN(title_code),
				CASE
					WHEN COUNT(source_signal_code) = COUNT(*)
						AND MIN(source_signal_code) = MAX(source_signal_code)
					THEN MIN(source_signal_code)
					ELSE NULL
				END,
				MAX(CASE severity WHEN 'critical' THEN 5 WHEN 'high' THEN 4
				WHEN 'medium' THEN 3 WHEN 'low' THEN 2 ELSE 1 END),
			CASE MAX(CASE severity WHEN 'critical' THEN 5 WHEN 'high' THEN 4
				WHEN 'medium' THEN 3 WHEN 'low' THEN 2 ELSE 1 END)
				WHEN 5 THEN 'critical' WHEN 4 THEN 'high' WHEN 3 THEN 'medium'
				WHEN 2 THEN 'low' ELSE 'info' END,
			CASE MIN(CASE confidence WHEN 'low' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END)
				WHEN 1 THEN 'low' WHEN 2 THEN 'medium' ELSE 'high' END,
			CASE MAX(CASE scope_quality WHEN 'conflict' THEN 4 WHEN 'unscoped' THEN 3
				WHEN 'lexical' THEN 2 ELSE 1 END)
				WHEN 4 THEN 'conflict' WHEN 3 THEN 'unscoped'
				WHEN 2 THEN 'lexical' ELSE 'resolved' END,
			MIN(first_observed_at), MAX(last_observed_at), COUNT(*),
			COUNT(DISTINCT session_key),
			CASE WHEN COUNT(DISTINCT session_key) >= 2 THEN 1 ELSE 0 END,
			CASE MAX(CASE current_status WHEN 'failed' THEN 4 WHEN 'pending' THEN 3
				WHEN 'truncated' THEN 2 ELSE 1 END)
				WHEN 4 THEN 'failed' WHEN 3 THEN 'pending'
				WHEN 2 THEN 'truncated' ELSE 'current' END,
			MIN(evidence_complete), MIN(retained_history_only), MAX(experimental)
		FROM visible`,
		issueID,
	)
	var sourceSignalCode sql.NullString
	err := row.Scan(
		&result.fingerprintID, &result.fingerprintVersion, &result.origin,
		&result.detectorID, &result.detectorVersion, &result.category,
		&result.titleCode, &sourceSignalCode,
		&result.severityRank, &result.severity,
		&result.confidence, &result.scopeQuality, &result.firstObservedAt,
		&result.lastObservedAt, &result.occurrenceCount, &result.sessionCount,
		&result.repeated, &result.analysisStatus, &result.evidenceComplete,
		&result.retainedHistoryOnly, &result.experimental,
	)
	if err != nil {
		return issueSummaryProjection{}, false, errors.New("aggregate current issue summary")
	}
	if sourceSignalCode.Valid {
		result.sourceSignalCode = &sourceSignalCode.String
	}
	result.issueID = issueID
	result.harnesses, err = readIssueSummaryRelationValuesTx(
		ctx, tx, "harness", issueID,
	)
	if err != nil {
		return issueSummaryProjection{}, false, err
	}
	result.sessions, err = readIssueSummaryRelationValuesTx(
		ctx, tx, "session_key", issueID,
	)
	if err != nil {
		return issueSummaryProjection{}, false, err
	}
	return result, true, nil
}

func readIssueSummaryRelationValuesTx(
	ctx context.Context,
	tx *sql.Tx,
	column string,
	issueID string,
) ([]string, error) {
	if column != "harness" && column != "session_key" {
		return nil, errors.New("unsupported issue summary relation")
	}
	rows, err := tx.QueryContext(ctx,
		"SELECT DISTINCT "+column+` FROM issue_occurrences
		WHERE issue_id = ? AND visible_until_generation IS NULL
		ORDER BY `+column,
		issueID,
	)
	if err != nil {
		return nil, errors.New("read current issue summary relation")
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, errors.New("decode current issue summary relation")
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read current issue summary relation")
	}
	return values, nil
}

func readActiveIssueSummaryTx(
	ctx context.Context,
	tx *sql.Tx,
	issueID string,
) (issueSummaryProjection, string, bool, error) {
	var result issueSummaryProjection
	var revisionID string
	row := tx.QueryRowContext(ctx, `
		SELECT summary_revision_id, fingerprint_id, fingerprint_version,
			origin, detector_id, detector_version, category, title_code,
			source_signal_code, severity, severity_rank, confidence, scope_quality,
			first_observed_at, last_observed_at, occurrence_count,
			session_count, repeated, analysis_status, evidence_complete,
			retained_history_only, experimental
		FROM issue_summary_revisions
		WHERE issue_id = ? AND visible_until_generation IS NULL`,
		issueID,
	)
	var sourceSignalCode sql.NullString
	err := row.Scan(
		&revisionID, &result.fingerprintID, &result.fingerprintVersion,
		&result.origin, &result.detectorID, &result.detectorVersion,
		&result.category, &result.titleCode, &sourceSignalCode, &result.severity,
		&result.severityRank, &result.confidence, &result.scopeQuality,
		&result.firstObservedAt, &result.lastObservedAt,
		&result.occurrenceCount, &result.sessionCount, &result.repeated,
		&result.analysisStatus, &result.evidenceComplete,
		&result.retainedHistoryOnly, &result.experimental,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return issueSummaryProjection{}, "", false, nil
	}
	if err != nil {
		return issueSummaryProjection{}, "", false, errors.New("read active issue summary")
	}
	if sourceSignalCode.Valid {
		result.sourceSignalCode = &sourceSignalCode.String
	}
	result.issueID = issueID
	result.harnesses, err = readStoredIssueSummaryRelationsTx(
		ctx, tx, "issue_summary_harnesses", "harness", revisionID,
	)
	if err != nil {
		return issueSummaryProjection{}, "", false, err
	}
	result.sessions, err = readStoredIssueSummaryRelationsTx(
		ctx, tx, "issue_summary_sessions", "session_key", revisionID,
	)
	if err != nil {
		return issueSummaryProjection{}, "", false, err
	}
	return result, revisionID, true, nil
}

func readStoredIssueSummaryRelationsTx(
	ctx context.Context,
	tx *sql.Tx,
	table string,
	column string,
	revisionID string,
) ([]string, error) {
	if (table != "issue_summary_harnesses" || column != "harness") &&
		(table != "issue_summary_sessions" || column != "session_key") {
		return nil, errors.New("unsupported stored issue summary relation")
	}
	rows, err := tx.QueryContext(ctx,
		"SELECT "+column+" FROM "+table+
			" WHERE summary_revision_id = ? ORDER BY "+column,
		revisionID,
	)
	if err != nil {
		return nil, errors.New("read stored issue summary relation")
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, errors.New("decode stored issue summary relation")
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read stored issue summary relation")
	}
	return values, nil
}

func equalIssueSummaryProjection(
	left issueSummaryProjection,
	right issueSummaryProjection,
) bool {
	return reflect.DeepEqual(left, right)
}

func insertIssueSummaryRevisionTx(
	ctx context.Context,
	tx *sql.Tx,
	summary issueSummaryProjection,
	generation int64,
	now string,
) error {
	revisionID := stableLocalID("isr_", summary.issueID, fmt.Sprint(generation))
	if _, err := tx.ExecContext(ctx, `
			INSERT INTO issue_summary_revisions (
				summary_revision_id, issue_id, fingerprint_id, fingerprint_version,
				origin, detector_id, detector_version, category, title_code,
				source_signal_code, severity, severity_rank, confidence, scope_quality,
				first_observed_at, last_observed_at, occurrence_count,
				session_count, repeated, analysis_status, evidence_complete,
				retained_history_only, experimental, visible_from_generation,
				created_at
			) VALUES (
				?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
			)`,
		revisionID, summary.issueID, summary.fingerprintID,
		summary.fingerprintVersion, summary.origin, summary.detectorID,
		summary.detectorVersion, summary.category, summary.titleCode,
		nullableOptionalString(summary.sourceSignalCode),
		summary.severity, summary.severityRank, summary.confidence,
		summary.scopeQuality, summary.firstObservedAt, summary.lastObservedAt,
		summary.occurrenceCount, summary.sessionCount, summary.repeated,
		summary.analysisStatus, summary.evidenceComplete,
		summary.retainedHistoryOnly, summary.experimental, generation, now,
	); err != nil {
		return errors.New("persist current issue summary")
	}
	for _, harness := range summary.harnesses {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO issue_summary_harnesses (summary_revision_id, harness)
			VALUES (?, ?)`,
			revisionID, harness,
		); err != nil {
			return errors.New("persist issue summary harness")
		}
	}
	for _, sessionID := range summary.sessions {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO issue_summary_sessions (summary_revision_id, session_key)
			VALUES (?, ?)`,
			revisionID, sessionID,
		); err != nil {
			return errors.New("persist issue summary session")
		}
	}
	return nil
}

func refreshIssueCoverageRevisionTx(
	ctx context.Context,
	tx *sql.Tx,
	generation int64,
	now string,
) error {
	next, err := aggregateCurrentIssueCoverageTx(ctx, tx)
	if err != nil {
		return err
	}
	var current issueCoverageProjection
	var revisionID string
	err = tx.QueryRowContext(ctx, `
		SELECT coverage_revision_id, current_sessions, pending_sessions,
			failed_sessions, truncated_sessions, unscoped_sessions,
			COALESCE(analysis_through, ''), complete
		FROM issue_analysis_coverage_revisions
		WHERE visible_until_generation IS NULL`,
	).Scan(
		&revisionID, &current.currentSessions, &current.pendingSessions,
		&current.failedSessions, &current.truncatedSessions,
		&current.unscopedSessions, &current.analysisThrough, &current.complete,
	)
	switch {
	case err == nil && current == next:
		return nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return errors.New("read active issue coverage")
	case err == nil:
		if _, err := tx.ExecContext(ctx, `
			UPDATE issue_analysis_coverage_revisions
			SET visible_until_generation = ?
			WHERE coverage_revision_id = ? AND visible_until_generation IS NULL`,
			generation, revisionID,
		); err != nil {
			return errors.New("close prior issue coverage")
		}
	}
	return insertIssueCoverageRevisionTx(ctx, tx, next, generation, now)
}

func aggregateCurrentIssueCoverageTx(
	ctx context.Context,
	tx *sql.Tx,
) (issueCoverageProjection, error) {
	var result issueCoverageProjection
	var analysisThrough sql.NullString
	if err := tx.QueryRowContext(ctx, `
		WITH event_sessions AS (
			SELECT DISTINCT session_key FROM events
		),
		active_analysis AS (
			SELECT session_key, status, scope_quality, created_at
			FROM session_analysis_revisions
			WHERE visible_until_generation IS NULL
		)
		SELECT
			COALESCE(SUM(CASE WHEN aa.status = 'current' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN aa.status = 'pending' OR aa.status IS NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN aa.status = 'failed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN aa.status = 'truncated' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN COALESCE(aa.scope_quality, 'unscoped') = 'unscoped'
				THEN 1 ELSE 0 END), 0),
			MAX(CASE WHEN aa.status = 'current' THEN aa.created_at END)
		FROM event_sessions es
		LEFT JOIN active_analysis aa ON aa.session_key = es.session_key`,
	).Scan(
		&result.currentSessions, &result.pendingSessions,
		&result.failedSessions, &result.truncatedSessions,
		&result.unscopedSessions, &analysisThrough,
	); err != nil {
		return issueCoverageProjection{}, errors.New("aggregate current issue coverage")
	}
	if analysisThrough.Valid {
		result.analysisThrough = analysisThrough.String
	}
	result.complete = boolInt(
		result.pendingSessions == 0 &&
			result.failedSessions == 0 &&
			result.truncatedSessions == 0,
	)
	return result, nil
}

func insertIssueCoverageRevisionTx(
	ctx context.Context,
	tx *sql.Tx,
	coverage issueCoverageProjection,
	generation int64,
	now string,
) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO issue_analysis_coverage_revisions (
			coverage_revision_id, current_sessions, pending_sessions,
			failed_sessions, truncated_sessions, unscoped_sessions,
			analysis_through, complete, visible_from_generation, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		stableLocalID("iac_", fmt.Sprint(generation)),
		coverage.currentSessions, coverage.pendingSessions,
		coverage.failedSessions, coverage.truncatedSessions,
		coverage.unscopedSessions, nullable(coverage.analysisThrough),
		coverage.complete, generation, now,
	); err != nil {
		return errors.New("persist current issue coverage")
	}
	return nil
}

func (s *Store) compactIssueSummaryRevisionsTx(
	ctx context.Context,
	tx *sql.Tx,
	currentGeneration int64,
	now string,
) error {
	nowTime, err := parseProjectionTime(now)
	if err != nil {
		return errors.New("decode issue projection generation time")
	}
	var compactThrough sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT MAX(generation)
		FROM issue_projection_generation_times
		WHERE generation <= ? AND generated_at < ?`,
		currentGeneration,
		formatProjectionTime(nowTime.Add(-issueCursorLifetime)),
	).Scan(&compactThrough); err != nil {
		return errors.New("select issue projection compaction boundary")
	}
	if !compactThrough.Valid {
		return nil
	}
	var oldest int64
	if err := tx.QueryRowContext(ctx, `
		SELECT oldest_materialized_generation
		FROM issue_summary_metadata WHERE singleton = 1`,
	).Scan(&oldest); err != nil {
		return errors.New("read oldest issue materialized generation")
	}
	if compactThrough.Int64 <= oldest {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM issue_summary_revisions
		WHERE visible_until_generation IS NOT NULL
			AND visible_until_generation <= ?`,
		compactThrough.Int64,
	); err != nil {
		return errors.New("compact issue summary revisions")
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM issue_analysis_coverage_revisions
		WHERE visible_until_generation IS NOT NULL
			AND visible_until_generation <= ?`,
		compactThrough.Int64,
	); err != nil {
		return errors.New("compact issue coverage revisions")
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM issue_projection_generation_times
		WHERE generation < ?`,
		compactThrough.Int64,
	); err != nil {
		return errors.New("compact issue generation timestamps")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE issue_summary_metadata
		SET oldest_materialized_generation = ?, updated_at = ?
		WHERE singleton = 1 AND oldest_materialized_generation < ?`,
		compactThrough.Int64, now, compactThrough.Int64,
	); err != nil {
		return errors.New("advance oldest issue materialized generation")
	}
	return nil
}

func issueProjectionGeneratedAt(
	ctx context.Context,
	queryer queryRower,
	generation int64,
) (time.Time, error) {
	var value string
	if err := queryer.QueryRowContext(ctx, `
		SELECT generated_at FROM issue_projection_generation_times
		WHERE generation = ?`,
		generation,
	).Scan(&value); err != nil {
		return time.Time{}, errors.New("read issue projection generation time")
	}
	result, err := parseProjectionTime(value)
	if err != nil {
		return time.Time{}, errors.New("decode issue projection generation time")
	}
	return result, nil
}
