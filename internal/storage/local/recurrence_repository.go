package local

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

type monitoringAttemptRow struct {
	attempt    model.FixAttemptMonitoring
	severity   string
	harness    string
	activityAt time.Time
	stateRank  int
}

type monitoringSummarySeed struct {
	issueID         string
	annotationID    string
	activeCount     int
	observedCount   int
	historicalCount int
	stateRank       int
	severityRank    int
	activityAt      time.Time
}

func (s *Store) QueryFixMonitoring(
	ctx context.Context,
	query model.FixMonitoringQuery,
) (model.FixMonitoringPage, error) {
	query.Limit = boundedReadLimit(query.Limit, 20, 100)
	if err := validateFixMonitoringFilter(query.Filter); err != nil {
		return model.FixMonitoringPage{}, err
	}
	if query.Cursor != nil &&
		(query.Cursor.StateRank < 1 || query.Cursor.StateRank > 6 ||
			query.Cursor.SeverityRank < 0 || query.Cursor.SeverityRank > 5 ||
			query.Cursor.ActivityAt.IsZero() ||
			!validOpaquePrefixedID(query.Cursor.IssueID, "iss_")) {
		return model.FixMonitoringPage{}, model.ErrFixMonitoringSnapshotInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return model.FixMonitoringPage{}, errors.New("begin fix monitoring read")
	}
	defer tx.Rollback()
	snapshot, err := s.fixMonitoringSnapshot(ctx, tx, query.Snapshot)
	if err != nil {
		return model.FixMonitoringPage{}, err
	}
	seeds, err := s.selectMonitoringSummarySeeds(ctx, tx, snapshot, query)
	if err != nil {
		return model.FixMonitoringPage{}, err
	}
	hasMore := len(seeds) > query.Limit
	if hasMore {
		seeds = seeds[:query.Limit]
	}
	annotationIDs := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		annotationIDs = append(annotationIDs, seed.annotationID)
	}
	rows, err := s.readMonitoringAttempts(ctx, tx, snapshot, "", annotationIDs)
	if err != nil {
		return model.FixMonitoringPage{}, err
	}
	byAnnotation := make(map[string]monitoringAttemptRow, len(rows))
	for _, row := range rows {
		byAnnotation[row.attempt.AnnotationID] = row
	}
	result := make([]model.FixMonitoringSummary, 0, len(seeds))
	for _, seed := range seeds {
		driver, ok := byAnnotation[seed.annotationID]
		if !ok ||
			driver.attempt.IssueID != seed.issueID ||
			driver.stateRank != seed.stateRank ||
			monitoringSeverityRank(driver.attempt.Subject.Severity) != seed.severityRank ||
			!driver.activityAt.Equal(seed.activityAt) {
			return model.FixMonitoringPage{}, errors.New("monitoring summary selection changed")
		}
		summary := model.FixMonitoringSummary{
			FixAttemptMonitoring: driver.attempt,
			ActiveAttemptCount:   seed.activeCount,
			ObservedAttemptCount: seed.observedCount,
		}
		summary.HistoricalMatchingEvidenceCount = seed.historicalCount
		result = append(result, summary)
	}
	var position *model.FixMonitoringPosition
	if len(result) != 0 {
		value := monitoringPosition(result[len(result)-1])
		position = &value
	}
	if err := tx.Commit(); err != nil {
		return model.FixMonitoringPage{}, errors.New("complete fix monitoring read")
	}
	return model.FixMonitoringPage{
		Data:                nonNilMonitoringSummaries(result),
		Snapshot:            snapshot,
		HasMore:             hasMore,
		Position:            position,
		EvidenceEvaluatedAt: s.nowUTC(),
	}, nil
}

func (s *Store) QueryIssueFixMonitoring(
	ctx context.Context,
	query model.FixMonitoringDetailQuery,
) (model.FixMonitoringDetailPage, error) {
	if !validOpaquePrefixedID(query.IssueID, "iss_") {
		return model.FixMonitoringDetailPage{}, model.ErrFixMonitoringNotFound
	}
	query.Limit = boundedReadLimit(query.Limit, 20, 100)
	if query.Cursor != nil &&
		(query.Cursor.RecordedAt.IsZero() ||
			!validOpaquePrefixedID(query.Cursor.AnnotationID, "fxa_")) {
		return model.FixMonitoringDetailPage{}, model.ErrFixMonitoringSnapshotInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return model.FixMonitoringDetailPage{}, errors.New("begin issue fix monitoring read")
	}
	defer tx.Rollback()
	snapshot, err := s.fixMonitoringSnapshot(ctx, tx, query.Snapshot)
	if err != nil {
		return model.FixMonitoringDetailPage{}, err
	}
	rows, err := s.readMonitoringAttempts(ctx, tx, snapshot, query.IssueID, nil)
	if err != nil {
		return model.FixMonitoringDetailPage{}, err
	}
	currentIssue, err := s.currentIssueSummaryTx(ctx, tx, query.IssueID, snapshot.ProjectionGeneration)
	if err != nil {
		return model.FixMonitoringDetailPage{}, err
	}
	if len(rows) == 0 && currentIssue == nil {
		return model.FixMonitoringDetailPage{}, model.ErrFixMonitoringNotFound
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].attempt.RecordedAt.Equal(rows[j].attempt.RecordedAt) {
			return rows[i].attempt.RecordedAt.After(rows[j].attempt.RecordedAt)
		}
		return rows[i].attempt.AnnotationID > rows[j].attempt.AnnotationID
	})
	if query.Cursor != nil {
		filtered := rows[:0]
		for _, row := range rows {
			if row.attempt.RecordedAt.Before(query.Cursor.RecordedAt) ||
				(row.attempt.RecordedAt.Equal(query.Cursor.RecordedAt) &&
					row.attempt.AnnotationID < query.Cursor.AnnotationID) {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	hasMore := len(rows) > query.Limit
	if hasMore {
		rows = rows[:query.Limit]
	}
	data := make([]model.FixAttemptMonitoring, 0, len(rows))
	for _, row := range rows {
		data = append(data, row.attempt)
	}
	var position *model.FixMonitoringDetailPosition
	if len(data) != 0 {
		position = &model.FixMonitoringDetailPosition{
			RecordedAt:   data[len(data)-1].RecordedAt,
			AnnotationID: data[len(data)-1].AnnotationID,
		}
	}
	if err := tx.Commit(); err != nil {
		return model.FixMonitoringDetailPage{}, errors.New("complete issue fix monitoring read")
	}
	return model.FixMonitoringDetailPage{
		IssueID:             query.IssueID,
		CurrentIssue:        currentIssue,
		Data:                data,
		Snapshot:            snapshot,
		HasMore:             hasMore,
		Position:            position,
		EvidenceEvaluatedAt: s.nowUTC(),
	}, nil
}

func (s *Store) QueryFixRecurrenceObservations(
	ctx context.Context,
	query model.FixRecurrenceObservationQuery,
) (model.FixRecurrenceObservationPage, error) {
	if !validOpaquePrefixedID(query.IssueID, "iss_") ||
		!validOpaquePrefixedID(query.AnnotationID, "fxa_") {
		return model.FixRecurrenceObservationPage{}, model.ErrFixMonitoringNotFound
	}
	query.Limit = boundedReadLimit(query.Limit, 20, 100)
	if query.Cursor != nil &&
		(query.Cursor.FirstQualifyingEventAt.IsZero() ||
			!validOpaquePrefixedID(query.Cursor.RecurrenceID, "fxo_")) {
		return model.FixRecurrenceObservationPage{}, model.ErrFixMonitoringSnapshotInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return model.FixRecurrenceObservationPage{}, errors.New("begin recurrence observation read")
	}
	defer tx.Rollback()
	snapshot, err := s.fixMonitoringSnapshot(ctx, tx, query.Snapshot)
	if err != nil {
		return model.FixRecurrenceObservationPage{}, err
	}
	var annotationIssue string
	if err := tx.QueryRowContext(ctx, `
		SELECT issue_id
		FROM fix_annotations
		WHERE annotation_id = ? AND sequence <= ?`,
		query.AnnotationID,
		snapshot.AnnotationSequence,
	).Scan(&annotationIssue); err != nil || annotationIssue != query.IssueID {
		return model.FixRecurrenceObservationPage{}, model.ErrFixMonitoringNotFound
	}
	args := []any{query.AnnotationID, snapshot.ObservationSequence}
	cursorClause := ""
	if query.Cursor != nil {
		cursorOrder, ok := normalizedUnixNano(query.Cursor.FirstQualifyingEventAt)
		if !ok {
			return model.FixRecurrenceObservationPage{}, model.ErrFixMonitoringSnapshotInvalid
		}
		cursorClause = `
			AND (
				first_qualifying_event_order_ns < ? OR
				(first_qualifying_event_order_ns = ? AND recurrence_id < ?)
			)`
		args = append(args, cursorOrder, cursorOrder, query.Cursor.RecurrenceID)
	}
	args = append(args, query.Limit+1)
	rows, err := tx.QueryContext(ctx, `
		SELECT recurrence_id, annotation_id, issue_id, occurrence_id,
			session_id, fingerprint_id, fingerprint_version, origin,
			detector_id, detector_version, analysis_generation,
			projection_generation, first_qualifying_event_order_ns,
			last_qualifying_event_order_ns, qualifying_citation_count,
			evidence_complete, same_session_as_anchor, observed_at
		FROM fix_recurrence_observations
		WHERE annotation_id = ? AND sequence <= ?`+cursorClause+`
		ORDER BY first_qualifying_event_order_ns DESC, recurrence_id DESC
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return model.FixRecurrenceObservationPage{}, errors.New("query recurrence observations")
	}
	defer rows.Close()
	data := make([]model.FixRecurrenceObservation, 0, query.Limit+1)
	for rows.Next() {
		observation, err := s.scanRecurrenceObservationTx(ctx, tx, rows, snapshot)
		if err != nil {
			return model.FixRecurrenceObservationPage{}, err
		}
		data = append(data, observation)
	}
	if err := rows.Err(); err != nil {
		return model.FixRecurrenceObservationPage{}, errors.New("query recurrence observations")
	}
	hasMore := len(data) > query.Limit
	if hasMore {
		data = data[:query.Limit]
	}
	var position *model.FixRecurrenceObservationPosition
	if len(data) != 0 {
		last := data[len(data)-1]
		position = &model.FixRecurrenceObservationPosition{
			FirstQualifyingEventAt: last.FirstQualifyingEventAt,
			RecurrenceID:           last.RecurrenceID,
		}
	}
	if err := tx.Commit(); err != nil {
		return model.FixRecurrenceObservationPage{}, errors.New("complete recurrence observation read")
	}
	return model.FixRecurrenceObservationPage{
		IssueID:             query.IssueID,
		AnnotationID:        query.AnnotationID,
		Data:                data,
		Snapshot:            snapshot,
		HasMore:             hasMore,
		Position:            position,
		EvidenceEvaluatedAt: s.nowUTC(),
	}, nil
}

func (s *Store) fixMonitoringSnapshot(
	ctx context.Context,
	tx *sql.Tx,
	requested model.FixMonitoringSnapshot,
) (model.FixMonitoringSnapshot, error) {
	var readiness string
	if err := tx.QueryRowContext(ctx, `
		SELECT readiness FROM fix_monitoring_metadata WHERE singleton = 1`,
	).Scan(&readiness); err != nil {
		return model.FixMonitoringSnapshot{}, errors.New("read fix monitoring readiness")
	}
	switch readiness {
	case model.FixMonitoringReadinessCatchingUp:
		return model.FixMonitoringSnapshot{}, model.ErrFixMonitoringCatchingUp
	case model.FixMonitoringReadinessFailed:
		return model.FixMonitoringSnapshot{}, model.ErrFixMonitoringCatchupFailed
	case model.FixMonitoringReadinessReady:
	default:
		return model.FixMonitoringSnapshot{}, errors.New("read fix monitoring readiness")
	}
	var current, oldest, retention int64
	if err := tx.QueryRowContext(ctx, `
		SELECT current_generation, oldest_retained_generation,
			retention_generation
		FROM issue_projection_metadata WHERE singleton = 1`,
	).Scan(&current, &oldest, &retention); err != nil {
		return model.FixMonitoringSnapshot{}, errors.New("read fix monitoring generation")
	}
	latest := model.FixMonitoringSnapshot{
		ProjectionGeneration: current,
		RetentionGeneration:  retention,
		IssuedAt:             s.nowUTC(),
	}
	for query, target := range map[string]*int64{
		"SELECT COALESCE(MAX(sequence), 0) FROM event_read_order":            &latest.EventGeneration,
		"SELECT COALESCE(MAX(sequence), 0) FROM fix_annotations":             &latest.AnnotationSequence,
		"SELECT COALESCE(MAX(sequence), 0) FROM fix_annotation_retractions":  &latest.RetractionSequence,
		"SELECT COALESCE(MAX(sequence), 0) FROM fix_recurrence_jobs":         &latest.JobSequence,
		"SELECT COALESCE(MAX(sequence), 0) FROM fix_recurrence_job_events":   &latest.JobEventSequence,
		"SELECT COALESCE(MAX(sequence), 0) FROM fix_recurrence_observations": &latest.ObservationSequence,
	} {
		if err := tx.QueryRowContext(ctx, query).Scan(target); err != nil {
			return model.FixMonitoringSnapshot{}, errors.New("read fix monitoring snapshot")
		}
	}
	if requested.Empty() {
		return latest, nil
	}
	if requested.IssuedAt.IsZero() ||
		requested.ProjectionGeneration < 0 ||
		requested.EventGeneration < 0 ||
		requested.RetentionGeneration < 1 ||
		requested.AnnotationSequence < 0 ||
		requested.RetractionSequence < 0 ||
		requested.JobSequence < 0 ||
		requested.JobEventSequence < 0 ||
		requested.ObservationSequence < 0 {
		return model.FixMonitoringSnapshot{}, model.ErrFixMonitoringSnapshotInvalid
	}
	now := s.nowUTC()
	issued := requested.IssuedAt.UTC()
	if issued.After(now) {
		return model.FixMonitoringSnapshot{}, model.ErrFixMonitoringSnapshotInvalid
	}
	if now.Sub(issued) > issueCursorLifetime ||
		requested.ProjectionGeneration < oldest ||
		requested.RetentionGeneration != retention {
		return model.FixMonitoringSnapshot{}, model.ErrFixMonitoringSnapshotExpired
	}
	if requested.ProjectionGeneration > latest.ProjectionGeneration ||
		requested.EventGeneration > latest.EventGeneration ||
		requested.AnnotationSequence > latest.AnnotationSequence ||
		requested.RetractionSequence > latest.RetractionSequence ||
		requested.JobSequence > latest.JobSequence ||
		requested.JobEventSequence > latest.JobEventSequence ||
		requested.ObservationSequence > latest.ObservationSequence {
		return model.FixMonitoringSnapshot{}, model.ErrFixMonitoringSnapshotInvalid
	}
	return requested, nil
}

func (s *Store) selectMonitoringSummarySeeds(
	ctx context.Context,
	tx *sql.Tx,
	snapshot model.FixMonitoringSnapshot,
	query model.FixMonitoringQuery,
) ([]monitoringSummarySeed, error) {
	statement, args := monitoringSummarySeedQuery(snapshot, query)
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, errors.New("select bounded monitoring summaries")
	}
	defer rows.Close()
	result := make([]monitoringSummarySeed, 0, query.Limit+1)
	for rows.Next() {
		var seed monitoringSummarySeed
		var activityAt string
		if err := rows.Scan(
			&seed.issueID,
			&seed.annotationID,
			&seed.activeCount,
			&seed.observedCount,
			&seed.historicalCount,
			&seed.stateRank,
			&seed.severityRank,
			&activityAt,
		); err != nil {
			return nil, errors.New("read bounded monitoring summary")
		}
		seed.activityAt, err = parseProjectionTime(activityAt)
		if err != nil {
			return nil, errors.New("decode monitoring summary activity")
		}
		result = append(result, seed)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read bounded monitoring summaries")
	}
	return result, nil
}

func monitoringSummarySeedQuery(
	snapshot model.FixMonitoringSnapshot,
	query model.FixMonitoringQuery,
) (string, []any) {
	// Driver classification intentionally uses short-circuiting, indexed
	// existence probes. Full per-session coverage is loaded only after the
	// limit+1 driver page has been selected.
	statement := `
		WITH
		parameters AS (
			SELECT
				? AS observation_sequence,
				? AS retraction_sequence,
				? AS annotation_sequence,
				? AS event_generation,
				? AS projection_generation,
				? AS job_sequence,
				? AS job_event_sequence
		),
		attempts AS (
			SELECT
				fa.annotation_id,
				fa.issue_id,
				fa.change_kind,
				fa.recorded_at,
				fms.fingerprint_scope_id,
				fms.scope_capture_status,
				fms.severity,
				fms.anchor_harness,
				fms.origin,
				fms.detector_id,
				fms.fingerprint_version,
				fms.negative_comparison_mode,
				fms.monitor_from_order_ns,
				CASE WHEN far.annotation_id IS NULL THEN 1 ELSE 0 END AS active,
				(
					SELECT COUNT(*)
					FROM fix_recurrence_observations fro
					WHERE fro.annotation_id = fa.annotation_id
						AND fro.sequence <= p.observation_sequence
				) AS observation_count,
				(
					SELECT MAX(fro.observed_at)
					FROM fix_recurrence_observations fro
					WHERE fro.annotation_id = fa.annotation_id
						AND fro.sequence <= p.observation_sequence
				) AS last_observed_at,
				p.event_generation,
				p.projection_generation,
				p.job_sequence,
				p.job_event_sequence
			FROM fix_annotations fa
			JOIN fix_monitoring_subjects fms
				ON fms.annotation_id = fa.annotation_id
			CROSS JOIN parameters p
			LEFT JOIN fix_annotation_retractions far
				ON far.annotation_id = fa.annotation_id
				AND far.sequence <= p.retraction_sequence
			WHERE fa.sequence <= p.annotation_sequence
		),
		classified AS (
			SELECT
				a.*,
				CASE
					WHEN a.active = 0 THEN 6
					WHEN a.observation_count > 0 THEN 1
					WHEN a.scope_capture_status <> 'captured'
						OR a.monitor_from_order_ns IS NULL
						OR a.negative_comparison_mode <> 'supported' THEN 3
					WHEN EXISTS (
						SELECT 1
						FROM dirty_sessions ds
						LEFT JOIN session_scopes ss
							ON ss.session_key = ds.session_key
						LEFT JOIN session_analysis_revisions va
							ON va.session_key = ds.session_key
							AND va.visible_from_generation <=
								a.projection_generation
							AND (
								va.visible_until_generation IS NULL OR
								va.visible_until_generation >
									a.projection_generation
							)
						WHERE (
								ss.project_scope_id =
									a.fingerprint_scope_id OR
								ss.project_scope_id IS NULL
							)
							AND EXISTS (
								SELECT 1
								FROM events later_event
								JOIN event_read_order later_order
									ON later_order.event_id =
										later_event.event_id
								WHERE later_event.session_key =
										ds.session_key
									AND later_event.occurred_at_order_ns >
										a.monitor_from_order_ns
									AND later_order.sequence <=
										a.event_generation
								LIMIT 1
							)
							AND NOT (
								va.revision_id IS NOT NULL
								AND va.status = 'current'
								AND NOT EXISTS (
									SELECT 1
									FROM events pending_event
									JOIN event_read_order pending_order
										ON pending_order.event_id =
											pending_event.event_id
									WHERE pending_event.session_key =
											ds.session_key
										AND pending_order.sequence <=
											a.event_generation
										AND pending_order.sequence >
											va.analyzed_event_generation
									LIMIT 1
								)
								AND EXISTS (
									SELECT 1
									FROM session_analysis_capabilities sac
									WHERE sac.revision_id = va.revision_id
										AND sac.fingerprint_scope_id =
											a.fingerprint_scope_id
										AND sac.origin = a.origin
										AND sac.detector_id = a.detector_id
										AND sac.fingerprint_version =
											a.fingerprint_version
										AND sac.negative_comparison_mode =
											'supported'
										AND sac.analysis_through_order_ns >
											a.monitor_from_order_ns
									LIMIT 1
								)
								AND EXISTS (
									SELECT 1
									FROM fix_recurrence_jobs frj
									JOIN fix_recurrence_job_events frje
										ON frje.job_id = frj.job_id
									WHERE frj.revision_id = va.revision_id
										AND frj.sequence <= a.job_sequence
										AND frje.sequence <=
											a.job_event_sequence
										AND frje.event_kind = 'complete'
									LIMIT 1
								)
							)
						LIMIT 1
					) THEN 2
					WHEN NOT EXISTS (
						SELECT 1
						FROM dirty_sessions ds
						LEFT JOIN session_scopes ss
							ON ss.session_key = ds.session_key
						WHERE (
								ss.project_scope_id =
									a.fingerprint_scope_id OR
								ss.project_scope_id IS NULL
							)
							AND EXISTS (
								SELECT 1
								FROM events later_event
								JOIN event_read_order later_order
									ON later_order.event_id =
										later_event.event_id
								WHERE later_event.session_key =
										ds.session_key
									AND later_event.occurred_at_order_ns >
										a.monitor_from_order_ns
									AND later_order.sequence <=
										a.event_generation
								LIMIT 1
							)
						LIMIT 1
					) THEN 4
					ELSE 5
				END AS state_rank
			FROM attempts a
		),
		states AS (
			SELECT
				classified.*,
				CASE state_rank
					WHEN 1 THEN 'matching_evidence_observed'
					WHEN 2 THEN 'monitoring_incomplete'
					WHEN 3 THEN 'comparison_unavailable'
					WHEN 4 THEN 'awaiting_later_evidence'
					WHEN 5 THEN 'no_later_match_observed'
					ELSE 'retracted'
				END AS state_code,
				CASE severity
					WHEN 'critical' THEN 5
					WHEN 'high' THEN 4
					WHEN 'medium' THEN 3
					WHEN 'low' THEN 2
					WHEN 'info' THEN 1
					ELSE 0
				END AS severity_rank,
				COALESCE(last_observed_at, recorded_at) AS activity_at
			FROM classified
		),
		issue_counts AS (
			SELECT
				issue_id,
				SUM(active) AS active_count,
				SUM(CASE
					WHEN active = 1 AND observation_count > 0 THEN 1 ELSE 0
				END) AS observed_count,
				SUM(observation_count) AS historical_count
			FROM states
			GROUP BY issue_id
		),
		eligible AS (
			SELECT s.*, ic.active_count, ic.observed_count, ic.historical_count
			FROM states s
			JOIN issue_counts ic ON ic.issue_id = s.issue_id
			WHERE s.active = 1 OR (ic.active_count = 0 AND ? = 1)
		),
		ranked AS (
			SELECT
				eligible.*,
				ROW_NUMBER() OVER (
					PARTITION BY issue_id
					ORDER BY
						state_rank ASC,
						CASE WHEN last_observed_at IS NULL THEN 1 ELSE 0 END ASC,
						last_observed_at DESC,
						recorded_at DESC,
						annotation_id DESC
				) AS driver_rank
			FROM eligible
		),
		drivers AS (
			SELECT * FROM ranked WHERE driver_rank = 1
		)
		SELECT
			issue_id,
			annotation_id,
			active_count,
			observed_count,
			historical_count,
			state_rank,
			severity_rank,
			activity_at
		FROM drivers
		WHERE 1 = 1`
	args := []any{
		snapshot.ObservationSequence,
		snapshot.RetractionSequence,
		snapshot.AnnotationSequence,
		snapshot.EventGeneration,
		snapshot.ProjectionGeneration,
		snapshot.JobSequence,
		snapshot.JobEventSequence,
	}
	args = append(args, boolInt(query.Filter.IncludeRetracted))
	if query.Filter.State != "" {
		statement += " AND state_code = ?"
		args = append(args, query.Filter.State)
	}
	if query.Filter.ChangeKind != "" {
		statement += " AND change_kind = ?"
		args = append(args, query.Filter.ChangeKind)
	}
	if query.Filter.Severity != "" {
		statement += " AND severity = ?"
		args = append(args, query.Filter.Severity)
	}
	if query.Filter.Harness != "" {
		statement += " AND LOWER(anchor_harness) = LOWER(?)"
		args = append(args, query.Filter.Harness)
	}
	if query.Filter.RecordedAfter != nil {
		statement += " AND recorded_at >= ?"
		args = append(args, formatProjectionTime(query.Filter.RecordedAfter.UTC()))
	}
	if query.Filter.IssueID != "" {
		statement += " AND issue_id = ?"
		args = append(args, query.Filter.IssueID)
	}
	if query.Cursor != nil {
		cursorAt := formatProjectionTime(query.Cursor.ActivityAt)
		statement += `
			AND (
				state_rank > ? OR
				(state_rank = ? AND severity_rank < ?) OR
				(state_rank = ? AND severity_rank = ? AND activity_at < ?) OR
				(state_rank = ? AND severity_rank = ? AND activity_at = ?
					AND issue_id > ?)
			)`
		args = append(
			args,
			query.Cursor.StateRank,
			query.Cursor.StateRank,
			query.Cursor.SeverityRank,
			query.Cursor.StateRank,
			query.Cursor.SeverityRank,
			cursorAt,
			query.Cursor.StateRank,
			query.Cursor.SeverityRank,
			cursorAt,
			query.Cursor.IssueID,
		)
	}
	statement += `
		ORDER BY state_rank ASC, severity_rank DESC, activity_at DESC, issue_id ASC
		LIMIT ?`
	args = append(args, query.Limit+1)
	return statement, args
}

func (s *Store) readMonitoringAttempts(
	ctx context.Context,
	tx *sql.Tx,
	snapshot model.FixMonitoringSnapshot,
	issueID string,
	annotationIDs []string,
) ([]monitoringAttemptRow, error) {
	clause := ""
	args := []any{snapshot.RetractionSequence, snapshot.AnnotationSequence}
	if issueID != "" {
		clause = " AND fa.issue_id = ?"
		args = append(args, issueID)
	}
	if len(annotationIDs) != 0 {
		clause += " AND fa.annotation_id IN (" +
			strings.TrimRight(strings.Repeat("?,", len(annotationIDs)), ",") + ")"
		for _, annotationID := range annotationIDs {
			args = append(args, annotationID)
		}
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT
			fa.annotation_id, fa.issue_id, fa.change_kind, fa.recorded_at,
			fa.monitor_from, fa.anchor_session_id,
			fms.fingerprint_scope_id, fms.scope_capture_status,
			fms.category, fms.title_code, fms.severity, fms.confidence,
			fms.anchor_harness, fms.origin, fms.detector_id,
			fms.detector_version, fms.fingerprint_version,
			fms.negative_comparison_mode, fms.monitor_from_order_ns,
			fms.captured_at, far.reason, far.retracted_at
		FROM fix_annotations fa
		JOIN fix_monitoring_subjects fms
			ON fms.annotation_id = fa.annotation_id
		LEFT JOIN fix_annotation_retractions far
			ON far.annotation_id = fa.annotation_id AND far.sequence <= ?
		WHERE fa.sequence <= ?`+clause+`
		ORDER BY fa.sequence`,
		args...,
	)
	if err != nil {
		return nil, errors.New("read fix monitoring attempts")
	}
	defer rows.Close()
	var result []monitoringAttemptRow
	for rows.Next() {
		var row monitoringAttemptRow
		var recordedAt, monitorFrom, capturedAt string
		var scope, category, title, severity, confidence, harness sql.NullString
		var monitorOrder sql.NullInt64
		var retractionReason, retractedAt sql.NullString
		if err := rows.Scan(
			&row.attempt.AnnotationID,
			&row.attempt.IssueID,
			&row.attempt.ChangeKind,
			&recordedAt,
			&monitorFrom,
			&row.attempt.Subject.AnnotationID,
			&scope,
			&row.attempt.Subject.ScopeCaptureStatus,
			&category,
			&title,
			&severity,
			&confidence,
			&harness,
			&row.attempt.Subject.Origin,
			&row.attempt.Subject.DetectorID,
			&row.attempt.Subject.DetectorVersion,
			&row.attempt.Subject.FingerprintVersion,
			&row.attempt.Subject.NegativeComparisonMode,
			&monitorOrder,
			&capturedAt,
			&retractionReason,
			&retractedAt,
		); err != nil {
			return nil, errors.New("read fix monitoring attempt")
		}
		row.attempt.Subject.AnnotationID = row.attempt.AnnotationID
		row.attempt.Subject.FingerprintScopeID = scope.String
		row.attempt.Subject.Category = nullableStringPointer(category)
		row.attempt.Subject.TitleCode = nullableStringPointer(title)
		row.attempt.Subject.Severity = nullableStringPointer(severity)
		row.attempt.Subject.Confidence = nullableStringPointer(confidence)
		row.attempt.Subject.AnchorHarness = nullableStringPointer(harness)
		row.attempt.Subject.MonitorFromOrderNS = nullableInt64Pointer(monitorOrder)
		var err error
		row.attempt.RecordedAt, err = parseProjectionTime(recordedAt)
		if err != nil {
			return nil, errors.New("decode fix monitoring recorded time")
		}
		row.attempt.MonitorFrom, err = parseProjectionTime(monitorFrom)
		if err != nil {
			return nil, errors.New("decode fix monitoring baseline")
		}
		row.attempt.Subject.CapturedAt, err = parseProjectionTime(capturedAt)
		if err != nil {
			return nil, errors.New("decode fix monitoring subject time")
		}
		row.attempt.State = model.FixStateActive
		if retractedAt.Valid {
			value, err := parseProjectionTime(retractedAt.String)
			if err != nil {
				return nil, errors.New("decode fix monitoring retraction")
			}
			row.attempt.State = model.FixStateRetracted
			row.attempt.RetractedAt = &value
			reason := retractionReason.String
			row.attempt.RetractionReason = &reason
		}
		row.severity = severity.String
		row.harness = harness.String
		if err := s.populateAttemptMonitoring(ctx, tx, snapshot, &row); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read fix monitoring attempts")
	}
	return result, nil
}

func (s *Store) populateAttemptMonitoring(
	ctx context.Context,
	tx *sql.Tx,
	snapshot model.FixMonitoringSnapshot,
	row *monitoringAttemptRow,
) error {
	var count, same int
	var last sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(same_session_as_anchor), 0),
			MAX(observed_at)
		FROM fix_recurrence_observations
		WHERE annotation_id = ? AND sequence <= ?`,
		row.attempt.AnnotationID,
		snapshot.ObservationSequence,
	).Scan(&count, &same, &last); err != nil {
		return errors.New("read fix recurrence totals")
	}
	row.attempt.HistoricalMatchingEvidenceCount = count
	if row.attempt.State == model.FixStateActive {
		row.attempt.FixRecurrenceCount = count
		row.attempt.SameAnchorSessionObservationCount = same
		row.attempt.OtherSessionObservationCount = count - same
	}
	if last.Valid {
		value, err := parseProjectionTime(last.String)
		if err != nil {
			return errors.New("decode recurrence observation time")
		}
		row.attempt.LastRecurrenceObservedAt = &value
		row.activityAt = value
	} else {
		row.activityAt = row.attempt.RecordedAt
	}
	if row.attempt.State == model.FixStateActive {
		coverage, err := s.monitoringCoverageTx(ctx, tx, snapshot, row.attempt.Subject)
		if err != nil {
			return err
		}
		row.attempt.Coverage = coverage
		row.attempt.AnalysisComplete = coverage.Complete
		row.attempt.CountIsLowerBound = count > 0 && !coverage.Complete
		row.attempt.FutureComparisonAvailable =
			row.attempt.Subject.ScopeCaptureStatus == "captured" &&
				row.attempt.Subject.NegativeComparisonMode ==
					model.FixNegativeComparisonSupported
		if !row.attempt.FutureComparisonAvailable {
			reason := model.FixComparisonCapabilityUnavailable
			switch {
			case row.attempt.Subject.ScopeCaptureStatus != "captured":
				reason = model.FixComparisonScopeUnavailable
			case row.attempt.Subject.MonitorFromOrderNS == nil:
				reason = model.FixComparisonBaselineTimeUnavailable
			case row.attempt.Subject.NegativeComparisonMode ==
				model.FixNegativeComparisonPositiveOnly:
				reason = model.FixComparisonSourcePositiveOnly
			}
			row.attempt.FutureComparisonUnavailableReason = &reason
		}
	}
	if err := s.populateAttemptEvidence(ctx, tx, &row.attempt, snapshot); err != nil {
		return err
	}
	switch {
	case row.attempt.State == model.FixStateRetracted:
		row.attempt.FixRecurrenceState = model.FixRecurrenceRetracted
	case count > 0:
		row.attempt.FixRecurrenceState = model.FixRecurrenceMatchingEvidence
	case !row.attempt.FutureComparisonAvailable:
		row.attempt.FixRecurrenceState = model.FixRecurrenceComparisonUnavailable
	case !row.attempt.Coverage.Complete:
		row.attempt.FixRecurrenceState = model.FixRecurrenceMonitoringIncomplete
	case row.attempt.Coverage.ComparableCurrent == 0:
		row.attempt.FixRecurrenceState = model.FixRecurrenceAwaitingEvidence
	default:
		row.attempt.FixRecurrenceState = model.FixRecurrenceNoLaterMatch
	}
	row.stateRank = recurrenceStateRank(row.attempt.FixRecurrenceState)
	return nil
}

func (s *Store) monitoringCoverageTx(
	ctx context.Context,
	tx *sql.Tx,
	snapshot model.FixMonitoringSnapshot,
	subject model.FixMonitoringSubject,
) (model.FixMonitoringCoverage, error) {
	var result model.FixMonitoringCoverage
	if subject.ScopeCaptureStatus != "captured" ||
		subject.MonitorFromOrderNS == nil ||
		subject.NegativeComparisonMode != model.FixNegativeComparisonSupported {
		return result, nil
	}
	rows, err := tx.QueryContext(ctx, `
		WITH event_sessions AS (
			SELECT e.session_key, MAX(ero.sequence) AS latest_event_generation
			FROM events e
			JOIN event_read_order ero ON ero.event_id = e.event_id
			WHERE ero.sequence <= ?
				AND e.occurred_at_order_ns > ?
			GROUP BY e.session_key
		),
		visible_analysis AS (
			SELECT revision_id, session_key, status,
				analyzed_event_generation
			FROM session_analysis_revisions
			WHERE visible_from_generation <= ?
				AND (
					visible_until_generation IS NULL OR
					visible_until_generation > ?
				)
		)
		SELECT es.session_key, es.latest_event_generation, va.status,
			va.analyzed_event_generation,
			sac.analysis_through_order_ns,
			EXISTS (
				SELECT 1
				FROM fix_recurrence_jobs frj
				JOIN fix_recurrence_job_events frje
					ON frje.job_id = frj.job_id
				WHERE frj.revision_id = va.revision_id
					AND frj.sequence <= ?
					AND frje.sequence <= ?
					AND frje.event_kind = 'complete'
			) AS job_complete
		FROM event_sessions es
		LEFT JOIN visible_analysis va ON va.session_key = es.session_key
		LEFT JOIN session_scopes ss ON ss.session_key = es.session_key
		LEFT JOIN session_analysis_capabilities sac
			ON sac.revision_id = va.revision_id
			AND sac.fingerprint_scope_id = ?
			AND sac.origin = ?
			AND sac.detector_id = ?
			AND sac.fingerprint_version = ?
			AND sac.negative_comparison_mode = 'supported'
		WHERE ss.project_scope_id = ? OR ss.project_scope_id IS NULL
		ORDER BY es.session_key`,
		snapshot.EventGeneration,
		*subject.MonitorFromOrderNS,
		snapshot.ProjectionGeneration,
		snapshot.ProjectionGeneration,
		snapshot.JobSequence,
		snapshot.JobEventSequence,
		subject.FingerprintScopeID,
		subject.Origin,
		subject.DetectorID,
		subject.FingerprintVersion,
		subject.FingerprintScopeID,
	)
	if err != nil {
		return result, errors.New("read fix monitoring coverage")
	}
	var maximum int64
	for rows.Next() {
		var sessionID string
		var latestEventGeneration int64
		var status sql.NullString
		var analyzedGeneration sql.NullInt64
		var watermark sql.NullInt64
		var complete int
		if err := rows.Scan(
			&sessionID,
			&latestEventGeneration,
			&status,
			&analyzedGeneration,
			&watermark,
			&complete,
		); err != nil {
			rows.Close()
			return result, errors.New("read fix monitoring coverage")
		}
		if watermark.Valid && watermark.Int64 > maximum {
			maximum = watermark.Int64
		}
		switch {
		case !status.Valid:
			result.ComparablePending++
		case !analyzedGeneration.Valid ||
			latestEventGeneration > analyzedGeneration.Int64:
			result.ComparablePending++
		case status.String == string(model.AnalysisFailed):
			result.ComparableFailed++
		case status.String == string(model.AnalysisTruncated):
			result.ComparableTruncated++
		case !watermark.Valid || watermark.Int64 <= *subject.MonitorFromOrderNS:
			result.ComparablePending++
		case status.String == string(model.AnalysisCurrent) && complete == 1:
			result.ComparableCurrent++
		default:
			result.ComparablePending++
		}
	}
	if err := rows.Close(); err != nil {
		return result, errors.New("read fix monitoring coverage")
	}
	if maximum > 0 {
		value := time.Unix(0, maximum).UTC()
		result.AnalysisThrough = &value
	}
	result.Complete = result.ComparablePending == 0 &&
		result.ComparableFailed == 0 &&
		result.ComparableTruncated == 0
	return result, nil
}

func (s *Store) populateAttemptEvidence(
	ctx context.Context,
	tx *sql.Tx,
	attempt *model.FixAttemptMonitoring,
	snapshot model.FixMonitoringSnapshot,
) error {
	var baseline, retained int
	if err := tx.QueryRowContext(ctx, `
		SELECT fa.baseline_citation_count, COUNT(ero.event_id)
		FROM fix_annotations fa
		LEFT JOIN fix_annotation_events fae
			ON fae.annotation_id = fa.annotation_id
		LEFT JOIN events e
			ON e.event_id = fae.event_id
		LEFT JOIN event_read_order ero
			ON ero.event_id = e.event_id AND ero.sequence <= ?
		WHERE fa.annotation_id = ?
		GROUP BY fa.annotation_id`,
		snapshot.EventGeneration,
		attempt.AnnotationID,
	).Scan(&baseline, &retained); err != nil {
		return errors.New("read fix anchor evidence")
	}
	attempt.AnchorEvidenceCurrentlyRetained = evidenceState(baseline, retained)
	rows, err := tx.QueryContext(ctx, `
		SELECT fro.qualifying_citation_count, COUNT(ero.event_id)
		FROM fix_recurrence_observations fro
		LEFT JOIN fix_recurrence_observation_events froe
			ON froe.recurrence_id = fro.recurrence_id
		LEFT JOIN events e
			ON e.event_id = froe.event_id
		LEFT JOIN event_read_order ero
			ON ero.event_id = e.event_id AND ero.sequence <= ?
		WHERE fro.annotation_id = ? AND fro.sequence <= ?
		GROUP BY fro.recurrence_id`,
		snapshot.EventGeneration,
		attempt.AnnotationID,
		snapshot.ObservationSequence,
	)
	if err != nil {
		return errors.New("read recurrence evidence totals")
	}
	for rows.Next() {
		var total, kept int
		if err := rows.Scan(&total, &kept); err != nil {
			rows.Close()
			return errors.New("read recurrence evidence totals")
		}
		switch evidenceState(total, kept) {
		case model.FixRecurrenceEvidenceAvailable:
			attempt.RecurrenceEvidence.Available++
		case model.FixRecurrenceEvidencePartial:
			attempt.RecurrenceEvidence.Partial++
		case model.FixRecurrenceEvidencePruned:
			attempt.RecurrenceEvidence.Pruned++
		default:
			attempt.RecurrenceEvidence.Unknown++
		}
	}
	if err := rows.Close(); err != nil {
		return errors.New("read recurrence evidence totals")
	}
	return nil
}

func (s *Store) scanRecurrenceObservationTx(
	ctx context.Context,
	tx *sql.Tx,
	row rowScanner,
	snapshot model.FixMonitoringSnapshot,
) (model.FixRecurrenceObservation, error) {
	var result model.FixRecurrenceObservation
	var first, last int64
	var evidenceComplete, sameSession int
	var observedAt string
	if err := row.Scan(
		&result.RecurrenceID,
		&result.AnnotationID,
		&result.IssueID,
		&result.OccurrenceID,
		&result.SessionID,
		&result.FingerprintID,
		&result.FingerprintVersion,
		&result.Origin,
		&result.DetectorID,
		&result.DetectorVersion,
		&result.AnalysisGeneration,
		&result.ProjectionGeneration,
		&first,
		&last,
		&result.QualifyingCitationCount,
		&evidenceComplete,
		&sameSession,
		&observedAt,
	); err != nil {
		return result, errors.New("read recurrence observation")
	}
	result.FirstQualifyingEventAt = time.Unix(0, first).UTC()
	result.LastQualifyingEventAt = time.Unix(0, last).UTC()
	result.EvidenceComplete = evidenceComplete == 1
	result.SameSessionAsAnchor = sameSession == 1
	var err error
	result.ObservedAt, err = parseProjectionTime(observedAt)
	if err != nil {
		return result, errors.New("decode recurrence observation time")
	}
	var retained int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM fix_recurrence_observation_events froe
		JOIN events e ON e.event_id = froe.event_id
		JOIN event_read_order ero ON ero.event_id = e.event_id
		WHERE froe.recurrence_id = ? AND ero.sequence <= ?`,
		result.RecurrenceID,
		snapshot.EventGeneration,
	).Scan(&retained); err != nil {
		result.RetainedEventIDs = []string{}
		result.EvidenceCurrentlyRetained = model.FixRecurrenceEvidenceUnknown
		return result, nil
	}
	eventRows, err := tx.QueryContext(ctx, `
		SELECT e.event_id
		FROM fix_recurrence_observation_events froe
		JOIN events e ON e.event_id = froe.event_id
		JOIN event_read_order ero ON ero.event_id = e.event_id
		WHERE froe.recurrence_id = ? AND ero.sequence <= ?
		ORDER BY e.occurred_at_order_ns, e.event_id
		LIMIT 50`,
		result.RecurrenceID,
		snapshot.EventGeneration,
	)
	if err != nil {
		result.RetainedEventIDs = []string{}
		result.EvidenceCurrentlyRetained = model.FixRecurrenceEvidenceUnknown
		return result, nil
	}
	for eventRows.Next() {
		var eventID string
		if err := eventRows.Scan(&eventID); err != nil {
			eventRows.Close()
			result.RetainedEventIDs = []string{}
			result.EvidenceCurrentlyRetained = model.FixRecurrenceEvidenceUnknown
			return result, nil
		}
		if model.IsCanonicalUUIDv7(eventID) {
			result.RetainedEventIDs = append(result.RetainedEventIDs, eventID)
		}
	}
	if err := eventRows.Close(); err != nil {
		result.RetainedEventIDs = []string{}
		result.EvidenceCurrentlyRetained = model.FixRecurrenceEvidenceUnknown
		return result, nil
	}
	truncated := retained > 50
	missing := result.QualifyingCitationCount - retained
	if missing < 0 {
		missing = 0
	}
	result.RetainedEventCount = &retained
	result.MissingEventCount = &missing
	result.EvidenceTruncated = &truncated
	result.EvidenceCurrentlyRetained =
		evidenceState(result.QualifyingCitationCount, retained)
	if result.RetainedEventIDs == nil {
		result.RetainedEventIDs = []string{}
	}
	return result, nil
}

func (s *Store) currentIssueSummaryTx(
	ctx context.Context,
	tx *sql.Tx,
	issueID string,
	generation int64,
) (*model.IssueSummary, error) {
	row := tx.QueryRowContext(ctx, `
		WITH visible AS (
			SELECT io.*
			FROM issue_occurrences io
			WHERE io.issue_id = ?
				AND io.visible_from_generation <= ?
				AND (
					io.visible_until_generation IS NULL OR
					io.visible_until_generation > ?
				)
		)
		SELECT issue_id, MIN(fingerprint_id), MIN(fingerprint_version),
			MIN(origin), MIN(detector_id), MAX(detector_version),
			MIN(category), MIN(title_code),
			CASE MAX(
				CASE severity
					WHEN 'critical' THEN 5
					WHEN 'high' THEN 4
					WHEN 'medium' THEN 3
					WHEN 'low' THEN 2
					ELSE 1
				END
			)
				WHEN 5 THEN 'critical'
				WHEN 4 THEN 'high'
				WHEN 3 THEN 'medium'
				WHEN 2 THEN 'low'
				ELSE 'info'
			END,
			CASE MIN(
				CASE confidence
					WHEN 'low' THEN 1
					WHEN 'medium' THEN 2
					ELSE 3
				END
			)
				WHEN 1 THEN 'low'
				WHEN 2 THEN 'medium'
				ELSE 'high'
			END,
			CASE MAX(
				CASE scope_quality
					WHEN 'conflict' THEN 4
					WHEN 'unscoped' THEN 3
					WHEN 'lexical' THEN 2
					ELSE 1
				END
			)
				WHEN 4 THEN 'conflict'
				WHEN 3 THEN 'unscoped'
				WHEN 2 THEN 'lexical'
				ELSE 'resolved'
			END,
			MIN(first_observed_at), MAX(last_observed_at),
			COUNT(*), COUNT(DISTINCT session_key),
			GROUP_CONCAT(DISTINCT harness),
			CASE MAX(
				CASE analysis_status
					WHEN 'failed' THEN 4
					WHEN 'pending' THEN 3
					WHEN 'truncated' THEN 2
					ELSE 1
				END
			)
				WHEN 4 THEN 'failed'
				WHEN 3 THEN 'pending'
				WHEN 2 THEN 'truncated'
				ELSE 'current'
			END,
			MIN(evidence_complete), MIN(retained_history_only),
			MAX(experimental)
		FROM visible
		GROUP BY issue_id`,
		issueID,
		generation,
		generation,
	)
	var result model.IssueSummary
	var first, last, harnesses string
	var evidence, retained, experimental int
	if err := row.Scan(
		&result.IssueID,
		&result.FingerprintID,
		&result.FingerprintVersion,
		&result.Origin,
		&result.DetectorID,
		&result.DetectorVersion,
		&result.Category,
		&result.TitleCode,
		&result.Severity,
		&result.Confidence,
		&result.ScopeQuality,
		&first,
		&last,
		&result.OccurrenceCount,
		&result.SessionCount,
		&harnesses,
		&result.AnalysisStatus,
		&evidence,
		&retained,
		&experimental,
	); errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, errors.New("read current issue for monitoring")
	}
	result.FirstObservedAt, _ = parseProjectionTime(first)
	result.LastObservedAt, _ = parseProjectionTime(last)
	result.EvidenceComplete = evidence == 1
	result.RetainedHistoryOnly = retained == 1
	result.Experimental = experimental == 1
	if harnesses != "" {
		result.Harnesses = strings.Split(harnesses, ",")
		sort.Strings(result.Harnesses)
	} else {
		result.Harnesses = []string{}
	}
	return &result, nil
}

func validateFixMonitoringFilter(filter model.FixMonitoringFilter) error {
	if filter.State != "" && !model.ValidFixRecurrenceState(filter.State) {
		return model.ErrFixMonitoringSnapshotInvalid
	}
	if filter.ChangeKind != "" && !filter.ChangeKind.Valid() {
		return model.ErrFixMonitoringSnapshotInvalid
	}
	if filter.RecordedAfter != nil && filter.RecordedAfter.IsZero() {
		return model.ErrFixMonitoringSnapshotInvalid
	}
	if filter.IssueID != "" && !validOpaquePrefixedID(filter.IssueID, "iss_") {
		return model.ErrFixMonitoringSnapshotInvalid
	}
	return nil
}

func monitoringDriver(
	rows []monitoringAttemptRow,
	includeRetracted bool,
) (monitoringAttemptRow, int, int, int, bool) {
	var eligible []monitoringAttemptRow
	active, observed, historical := 0, 0, 0
	for _, row := range rows {
		historical += row.attempt.HistoricalMatchingEvidenceCount
		if row.attempt.State == model.FixStateActive {
			active++
			if row.attempt.FixRecurrenceCount > 0 {
				observed++
			}
			eligible = append(eligible, row)
		}
	}
	if len(eligible) == 0 {
		if !includeRetracted {
			return monitoringAttemptRow{}, active, observed, historical, false
		}
		eligible = append(eligible, rows...)
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].stateRank != eligible[j].stateRank {
			return eligible[i].stateRank < eligible[j].stateRank
		}
		left, right := eligible[i].attempt.LastRecurrenceObservedAt,
			eligible[j].attempt.LastRecurrenceObservedAt
		if left != nil || right != nil {
			if left == nil {
				return false
			}
			if right == nil {
				return true
			}
			if !left.Equal(*right) {
				return left.After(*right)
			}
		}
		if !eligible[i].attempt.RecordedAt.Equal(eligible[j].attempt.RecordedAt) {
			return eligible[i].attempt.RecordedAt.After(eligible[j].attempt.RecordedAt)
		}
		return eligible[i].attempt.AnnotationID > eligible[j].attempt.AnnotationID
	})
	return eligible[0], active, observed, historical, true
}

func matchesMonitoringFilter(
	row monitoringAttemptRow,
	filter model.FixMonitoringFilter,
) bool {
	return (filter.State == "" || row.attempt.FixRecurrenceState == filter.State) &&
		(filter.ChangeKind == "" || row.attempt.ChangeKind == filter.ChangeKind) &&
		(filter.Severity == "" || row.severity == filter.Severity) &&
		(filter.Harness == "" || strings.EqualFold(row.harness, filter.Harness)) &&
		(filter.RecordedAfter == nil ||
			!row.attempt.RecordedAt.Before(filter.RecordedAfter.UTC())) &&
		(filter.IssueID == "" || row.attempt.IssueID == filter.IssueID)
}

func compareMonitoringSummary(
	left model.FixMonitoringSummary,
	right model.FixMonitoringSummary,
) int {
	return compareMonitoringPosition(monitoringPosition(left), monitoringPosition(right))
}

func monitoringPosition(item model.FixMonitoringSummary) model.FixMonitoringPosition {
	activity := item.RecordedAt
	if item.LastRecurrenceObservedAt != nil {
		activity = *item.LastRecurrenceObservedAt
	}
	return model.FixMonitoringPosition{
		StateRank:    recurrenceStateRank(item.FixRecurrenceState),
		SeverityRank: monitoringSeverityRank(item.Subject.Severity),
		ActivityAt:   activity,
		IssueID:      item.IssueID,
	}
}

func compareMonitoringPosition(left, right model.FixMonitoringPosition) int {
	if left.StateRank != right.StateRank {
		if left.StateRank < right.StateRank {
			return -1
		}
		return 1
	}
	if left.SeverityRank != right.SeverityRank {
		if left.SeverityRank > right.SeverityRank {
			return -1
		}
		return 1
	}
	if !left.ActivityAt.Equal(right.ActivityAt) {
		if left.ActivityAt.After(right.ActivityAt) {
			return -1
		}
		return 1
	}
	return strings.Compare(left.IssueID, right.IssueID)
}

func recurrenceStateRank(state string) int {
	switch state {
	case model.FixRecurrenceMatchingEvidence:
		return 1
	case model.FixRecurrenceMonitoringIncomplete:
		return 2
	case model.FixRecurrenceComparisonUnavailable:
		return 3
	case model.FixRecurrenceAwaitingEvidence:
		return 4
	case model.FixRecurrenceNoLaterMatch:
		return 5
	case model.FixRecurrenceRetracted:
		return 6
	default:
		return 7
	}
}

func monitoringSeverityRank(value *string) int {
	if value == nil {
		return 0
	}
	switch *value {
	case "critical":
		return 5
	case "high":
		return 4
	case "medium":
		return 3
	case "low":
		return 2
	case "info":
		return 1
	default:
		return 0
	}
}

func evidenceState(total, retained int) string {
	switch {
	case total > 0 && retained == total:
		return model.FixRecurrenceEvidenceAvailable
	case retained > 0:
		return model.FixRecurrenceEvidencePartial
	case total > 0:
		return model.FixRecurrenceEvidencePruned
	default:
		return model.FixRecurrenceEvidenceUnknown
	}
}

func nullableStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	copy := value.String
	return &copy
}

func nullableInt64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	copy := value.Int64
	return &copy
}

func normalizedUnixNano(value time.Time) (int64, bool) {
	return projectionOrderNS(value)
}

func nonNilMonitoringSummaries(
	value []model.FixMonitoringSummary,
) []model.FixMonitoringSummary {
	if value == nil {
		return []model.FixMonitoringSummary{}
	}
	return value
}
