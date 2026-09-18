package local

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

var (
	ErrFixInvalidInput           = errors.New("fix annotation input is invalid")
	ErrFixIssueNotFound          = errors.New("fix annotation issue was not found")
	ErrFixAnnotationNotFound     = errors.New("fix annotation was not found")
	ErrFixIneligible             = errors.New("issue is ineligible for fix annotation")
	ErrFixIdempotencyConflict    = errors.New("fix annotation idempotency conflict")
	ErrFixAlreadyRetracted       = errors.New("fix annotation is already retracted")
	ErrFixHistorySnapshotInvalid = errors.New("fix annotation history snapshot is invalid")
)

type FixIneligibleError struct {
	Reason string
}

func (e *FixIneligibleError) Error() string {
	return "issue is ineligible for fix annotation"
}

func (e *FixIneligibleError) Unwrap() error {
	return ErrFixIneligible
}

type FixAnnotationInput struct {
	Claims         model.FixActionClaims
	ChangeKind     model.FixChangeKind
	RecordedVia    string
	IdempotencyKey string
}

type FixAnnotationResult struct {
	Annotation model.FixAnnotation
	Replayed   bool
}

type FixRetractionInput struct {
	IssueID        string
	AnnotationID   string
	Reason         model.FixRetractionReason
	RecordedVia    string
	IdempotencyKey string
}

type FixRetractionResult struct {
	Retraction model.FixRetraction
	Replayed   bool
}

type fixIssueAggregate struct {
	AnalysisStatus model.AnalysisStatus
	Category       string
	Experimental   bool
	ScopeQuality   model.ScopeQuality
}

type fixAnchor struct {
	RevisionID         string
	OccurrenceID       string
	SessionID          string
	FingerprintID      string
	FingerprintVersion string
	Origin             string
	DetectorID         string
	DetectorVersion    string
	ScopeQuality       model.ScopeQuality
	FingerprintScopeID string
	Category           string
	TitleCode          string
	Severity           string
	Confidence         string
	Harness            string
	AnalysisStatus     model.AnalysisStatus
	AnalysisGeneration int64
	FirstObservedAt    time.Time
	LastObservedAt     time.Time
	CitedEventIDs      []string
}

type storedFixAnnotation struct {
	Annotation         model.FixAnnotation
	RequestFingerprint string
	BaselineCitations  int
}

type storedFixRetraction struct {
	Retraction         model.FixRetraction
	RequestFingerprint string
}

func (s *Store) EvaluateFixEligibility(
	ctx context.Context,
	query model.FixEligibilityQuery,
) (model.FixEligibility, error) {
	if !validIssueID(query.IssueID) {
		return model.FixEligibility{}, ErrFixInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return model.FixEligibility{}, errors.New("begin fix eligibility read")
	}
	defer tx.Rollback()
	epoch, retentionGeneration, err := s.validateFixEpochTx(
		ctx, tx, query.CursorEpoch, query.RetentionGeneration,
	)
	if err != nil {
		return model.FixEligibility{}, err
	}
	snapshot, err := s.issueSnapshot(ctx, tx, query.Snapshot, query.IssuedAt)
	if err != nil {
		return model.FixEligibility{}, err
	}
	eligibility, _, err := s.fixEligibilityAndAnchorTx(ctx, tx, query.IssueID, snapshot)
	if err != nil {
		return model.FixEligibility{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.FixEligibility{}, errors.New("complete fix eligibility read")
	}
	eligibility.CursorEpoch = epoch
	eligibility.Snapshot = snapshot
	eligibility.RetentionGeneration = retentionGeneration
	return eligibility, nil
}

func (s *Store) RecordFixAnnotation(
	ctx context.Context,
	input FixAnnotationInput,
) (FixAnnotationResult, error) {
	input.Claims.IssuedAt = input.Claims.IssuedAt.UTC()
	input.Claims.ExpiresAt = input.Claims.ExpiresAt.UTC()
	if !validFixAnnotationInput(input) {
		return FixAnnotationResult{}, ErrFixInvalidInput
	}
	annotationID, err := s.deriveFixAnnotationID(input.IdempotencyKey)
	if err != nil {
		return FixAnnotationResult{}, err
	}
	requestFingerprint, err := s.deriveFixAnnotationRequestFingerprint(
		input.Claims,
		input.ChangeKind,
		input.RecordedVia,
	)
	if err != nil {
		return FixAnnotationResult{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FixAnnotationResult{}, errors.New("begin fix annotation persistence")
	}
	defer tx.Rollback()
	if _, _, err := s.validateFixEpochTx(
		ctx,
		tx,
		input.Claims.CursorEpoch,
		input.Claims.RetentionGeneration,
	); err != nil {
		return FixAnnotationResult{}, err
	}
	existing, err := s.readFixAnnotationByIDTx(ctx, tx, annotationID)
	switch {
	case err == nil:
		if existing.RequestFingerprint != requestFingerprint {
			return FixAnnotationResult{}, ErrFixIdempotencyConflict
		}
		if err := tx.Commit(); err != nil {
			return FixAnnotationResult{}, errors.New("complete fix annotation replay")
		}
		return FixAnnotationResult{Annotation: existing.Annotation, Replayed: true}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return FixAnnotationResult{}, err
	}

	now := s.nowUTC()
	if now.After(input.Claims.ExpiresAt) {
		return FixAnnotationResult{}, model.ErrIssueSnapshotExpired
	}
	snapshot, err := s.issueSnapshot(
		ctx,
		tx,
		input.Claims.Snapshot,
		input.Claims.IssuedAt,
	)
	if err != nil {
		return FixAnnotationResult{}, err
	}
	eligibility, anchor, err := s.fixEligibilityAndAnchorTx(
		ctx,
		tx,
		input.Claims.IssueID,
		snapshot,
	)
	if err != nil {
		return FixAnnotationResult{}, err
	}
	if !eligibility.Eligible {
		return FixAnnotationResult{}, &FixIneligibleError{Reason: eligibility.Reason}
	}

	recordedAt := formatProjectionTime(now)
	result, err := tx.ExecContext(ctx, `
		INSERT INTO fix_annotations (
			annotation_id, request_fingerprint, issue_id, anchor_revision_id,
			anchor_occurrence_id, anchor_session_id, fingerprint_id,
			fingerprint_version, origin, detector_id, detector_version,
			scope_quality, issue_snapshot_generation, anchor_analysis_generation,
			anchor_first_observed_at, anchor_last_observed_at,
			baseline_citation_count, change_kind, change_catalog_version,
			recorded_via, recorded_at, monitor_from
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		)
		ON CONFLICT(annotation_id) DO NOTHING`,
		annotationID,
		requestFingerprint,
		input.Claims.IssueID,
		anchor.RevisionID,
		anchor.OccurrenceID,
		anchor.SessionID,
		anchor.FingerprintID,
		anchor.FingerprintVersion,
		anchor.Origin,
		anchor.DetectorID,
		anchor.DetectorVersion,
		anchor.ScopeQuality,
		snapshot,
		anchor.AnalysisGeneration,
		formatProjectionTime(anchor.FirstObservedAt),
		formatProjectionTime(anchor.LastObservedAt),
		len(anchor.CitedEventIDs),
		input.ChangeKind,
		model.FixChangeCatalogVersion,
		input.RecordedVia,
		recordedAt,
		recordedAt,
	)
	if err != nil {
		return FixAnnotationResult{}, errors.New("persist fix annotation")
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return FixAnnotationResult{}, errors.New("inspect fix annotation persistence")
	}
	if inserted == 0 {
		existing, err := s.readFixAnnotationByIDTx(ctx, tx, annotationID)
		if err != nil {
			return FixAnnotationResult{}, errors.New("resolve concurrent fix annotation")
		}
		if existing.RequestFingerprint != requestFingerprint {
			return FixAnnotationResult{}, ErrFixIdempotencyConflict
		}
		if err := tx.Commit(); err != nil {
			return FixAnnotationResult{}, errors.New("complete concurrent fix annotation replay")
		}
		return FixAnnotationResult{Annotation: existing.Annotation, Replayed: true}, nil
	}
	for _, eventID := range anchor.CitedEventIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO fix_annotation_events (annotation_id, event_id)
			VALUES (?, ?)`,
			annotationID,
			eventID,
		); err != nil {
			return FixAnnotationResult{}, errors.New("persist fix annotation evidence link")
		}
	}
	mode := model.FixNegativeComparisonSupported
	if anchor.Origin == "numbat" {
		mode = model.FixNegativeComparisonPositiveOnly
	}
	scopeCaptureStatus := "unavailable"
	var fingerprintScopeID any
	var monitorFromOrderNS any
	if validProjectScopeID(anchor.FingerprintScopeID) {
		scopeCaptureStatus = "captured"
		fingerprintScopeID = anchor.FingerprintScopeID
		monitorFromOrderNS = now.UnixNano()
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO fix_monitoring_subjects (
			annotation_id, fingerprint_scope_id, scope_capture_status,
			category, title_code, severity, confidence, anchor_harness,
			origin, detector_id, detector_version, fingerprint_version,
			negative_comparison_mode, monitor_from_order_ns, captured_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		annotationID,
		fingerprintScopeID,
		scopeCaptureStatus,
		anchor.Category,
		anchor.TitleCode,
		anchor.Severity,
		anchor.Confidence,
		anchor.Harness,
		anchor.Origin,
		anchor.DetectorID,
		anchor.DetectorVersion,
		anchor.FingerprintVersion,
		mode,
		monitorFromOrderNS,
		recordedAt,
	); err != nil {
		return FixAnnotationResult{}, errors.New("persist fix monitoring subject")
	}
	annotation := model.FixAnnotation{
		AnnotationID:             annotationID,
		IssueID:                  input.Claims.IssueID,
		AnchorRevisionID:         anchor.RevisionID,
		AnchorOccurrenceID:       anchor.OccurrenceID,
		AnchorSessionID:          anchor.SessionID,
		FingerprintID:            anchor.FingerprintID,
		FingerprintVersion:       anchor.FingerprintVersion,
		Origin:                   anchor.Origin,
		DetectorID:               anchor.DetectorID,
		DetectorVersion:          anchor.DetectorVersion,
		ScopeQuality:             anchor.ScopeQuality,
		IssueSnapshotGeneration:  snapshot,
		AnchorAnalysisGeneration: anchor.AnalysisGeneration,
		AnchorFirstObservedAt:    anchor.FirstObservedAt,
		AnchorLastObservedAt:     anchor.LastObservedAt,
		ChangeKind:               input.ChangeKind,
		ChangeCatalogVersion:     model.FixChangeCatalogVersion,
		RecordedVia:              input.RecordedVia,
		RecordedAt:               now,
		MonitorFrom:              now,
		EvidenceCurrentlyRetained: evidenceRetentionStatus(
			len(anchor.CitedEventIDs),
			len(anchor.CitedEventIDs),
		),
		State: model.FixStateActive,
	}
	if err := tx.Commit(); err != nil {
		return FixAnnotationResult{}, errors.New("commit fix annotation persistence")
	}
	return FixAnnotationResult{Annotation: annotation}, nil
}

func (s *Store) validateFixEpochTx(
	ctx context.Context,
	tx *sql.Tx,
	claimedEpoch string,
	claimedRetentionGeneration int64,
) (string, int64, error) {
	if claimedEpoch == "" || claimedRetentionGeneration < 1 {
		return "", 0, model.ErrIssueSnapshotInvalid
	}
	var retentionGeneration, current, materialized int64
	var epoch, readiness string
	if err := tx.QueryRowContext(ctx, `
		SELECT ism.cursor_epoch, ism.readiness, ipm.retention_generation,
			ipm.current_generation, ism.materialized_generation
		FROM issue_summary_metadata ism
		JOIN issue_projection_metadata ipm ON ipm.singleton = ism.singleton
		WHERE ism.singleton = 1`,
	).Scan(
		&epoch,
		&readiness,
		&retentionGeneration,
		&current,
		&materialized,
	); err != nil {
		return "", 0, errors.New("read fix action epoch")
	}
	if readiness != "ready" ||
		materialized != current ||
		claimedEpoch != epoch ||
		claimedRetentionGeneration != retentionGeneration {
		return "", 0, model.ErrIssueSnapshotExpired
	}
	return epoch, retentionGeneration, nil
}

func (s *Store) RetractFixAnnotation(
	ctx context.Context,
	input FixRetractionInput,
) (FixRetractionResult, error) {
	if !validFixRetractionInput(input) {
		return FixRetractionResult{}, ErrFixInvalidInput
	}
	retractionID, err := s.deriveFixRetractionID(input.IdempotencyKey)
	if err != nil {
		return FixRetractionResult{}, err
	}
	requestFingerprint, err := s.deriveFixRetractionRequestFingerprint(
		input.IssueID,
		input.AnnotationID,
		input.Reason,
		input.RecordedVia,
	)
	if err != nil {
		return FixRetractionResult{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FixRetractionResult{}, errors.New("begin fix retraction persistence")
	}
	defer tx.Rollback()
	existing, err := readFixRetractionByIDTx(ctx, tx, retractionID)
	switch {
	case err == nil:
		if existing.RequestFingerprint != requestFingerprint {
			return FixRetractionResult{}, ErrFixIdempotencyConflict
		}
		if err := tx.Commit(); err != nil {
			return FixRetractionResult{}, errors.New("complete fix retraction replay")
		}
		return FixRetractionResult{Retraction: existing.Retraction, Replayed: true}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return FixRetractionResult{}, err
	}

	var persistedIssueID string
	if err := tx.QueryRowContext(ctx, `
		SELECT issue_id FROM fix_annotations WHERE annotation_id = ?`,
		input.AnnotationID,
	).Scan(&persistedIssueID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FixRetractionResult{}, ErrFixAnnotationNotFound
		}
		return FixRetractionResult{}, errors.New("read fix annotation for retraction")
	}
	if persistedIssueID != input.IssueID {
		return FixRetractionResult{}, ErrFixAnnotationNotFound
	}
	var priorRetractionID string
	err = tx.QueryRowContext(ctx, `
		SELECT retraction_id
		FROM fix_annotation_retractions
		WHERE annotation_id = ?`,
		input.AnnotationID,
	).Scan(&priorRetractionID)
	switch {
	case err == nil:
		return FixRetractionResult{}, ErrFixAlreadyRetracted
	case !errors.Is(err, sql.ErrNoRows):
		return FixRetractionResult{}, errors.New("inspect fix annotation retraction")
	}

	retractedAt := s.nowUTC()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO fix_annotation_retractions (
			retraction_id, request_fingerprint, annotation_id, reason,
			recorded_via, retracted_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		retractionID,
		requestFingerprint,
		input.AnnotationID,
		input.Reason,
		input.RecordedVia,
		formatProjectionTime(retractedAt),
	)
	if err != nil {
		return FixRetractionResult{}, errors.New("persist fix annotation retraction")
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return FixRetractionResult{}, errors.New("inspect fix annotation retraction")
	}
	if inserted == 0 {
		existing, readErr := readFixRetractionByIDTx(ctx, tx, retractionID)
		if readErr == nil {
			if existing.RequestFingerprint != requestFingerprint {
				return FixRetractionResult{}, ErrFixIdempotencyConflict
			}
			if err := tx.Commit(); err != nil {
				return FixRetractionResult{}, errors.New("complete concurrent fix retraction replay")
			}
			return FixRetractionResult{Retraction: existing.Retraction, Replayed: true}, nil
		}
		if !errors.Is(readErr, sql.ErrNoRows) {
			return FixRetractionResult{}, readErr
		}
		return FixRetractionResult{}, ErrFixAlreadyRetracted
	}
	retraction := model.FixRetraction{
		RetractionID: retractionID,
		AnnotationID: input.AnnotationID,
		IssueID:      input.IssueID,
		Reason:       input.Reason,
		RecordedVia:  input.RecordedVia,
		RetractedAt:  retractedAt,
	}
	if err := tx.Commit(); err != nil {
		return FixRetractionResult{}, errors.New("commit fix annotation retraction")
	}
	return FixRetractionResult{Retraction: retraction}, nil
}

func (s *Store) QueryFixAnnotations(
	ctx context.Context,
	query model.FixAnnotationQuery,
) (model.FixAnnotationPage, error) {
	if !validIssueID(query.IssueID) {
		return model.FixAnnotationPage{}, ErrFixInvalidInput
	}
	query.Limit = boundedReadLimit(query.Limit, 20, 100)
	if query.AnnotationSnapshot < 0 || query.RetractionSnapshot < 0 ||
		(query.Cursor != nil && query.AnnotationSnapshot == 0) {
		return model.FixAnnotationPage{}, ErrFixHistorySnapshotInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return model.FixAnnotationPage{}, errors.New("begin fix annotation history read")
	}
	defer tx.Rollback()
	var currentAnnotations, currentRetractions int64
	if err := tx.QueryRowContext(ctx, `
		SELECT
			COALESCE((SELECT MAX(sequence) FROM fix_annotations), 0),
			COALESCE((SELECT MAX(sequence) FROM fix_annotation_retractions), 0)`,
	).Scan(&currentAnnotations, &currentRetractions); err != nil {
		return model.FixAnnotationPage{}, errors.New("read fix annotation history snapshot")
	}
	if query.AnnotationSnapshot == 0 && query.RetractionSnapshot == 0 && query.Cursor == nil {
		query.AnnotationSnapshot = currentAnnotations
		query.RetractionSnapshot = currentRetractions
	} else if query.AnnotationSnapshot > currentAnnotations ||
		query.RetractionSnapshot > currentRetractions {
		return model.FixAnnotationPage{}, ErrFixHistorySnapshotInvalid
	}

	clauses := []string{
		"fa.issue_id = ?",
		"fa.sequence <= ?",
	}
	args := []any{
		query.RetractionSnapshot,
		query.IssueID,
		query.AnnotationSnapshot,
	}
	if query.Cursor != nil {
		if query.Cursor.IssueID != query.IssueID ||
			query.Cursor.RecordedAt.IsZero() ||
			!validFixAnnotationID(query.Cursor.AnnotationID) {
			return model.FixAnnotationPage{}, ErrFixHistorySnapshotInvalid
		}
		clauses = append(clauses, `(
			fa.recorded_at < ? OR
			(fa.recorded_at = ? AND fa.annotation_id < ?)
		)`)
		recordedAt := formatProjectionTime(query.Cursor.RecordedAt)
		args = append(args, recordedAt, recordedAt, query.Cursor.AnnotationID)
	}
	args = append(args, query.Limit+1)
	rows, err := tx.QueryContext(ctx, `
		SELECT
			fa.annotation_id, fa.request_fingerprint, fa.issue_id,
			fa.anchor_revision_id, fa.anchor_occurrence_id, fa.anchor_session_id,
			fa.fingerprint_id, fa.fingerprint_version, fa.origin,
			fa.detector_id, fa.detector_version, fa.scope_quality,
			fa.issue_snapshot_generation, fa.anchor_analysis_generation,
			fa.anchor_first_observed_at, fa.anchor_last_observed_at,
			fa.baseline_citation_count, fa.change_kind,
			fa.change_catalog_version, fa.recorded_via, fa.recorded_at,
			fa.monitor_from,
			(SELECT COUNT(*) FROM fix_annotation_events fae
				WHERE fae.annotation_id = fa.annotation_id),
			far.reason, far.retracted_at
		FROM fix_annotations fa
		LEFT JOIN fix_annotation_retractions far
			ON far.annotation_id = fa.annotation_id
			AND far.sequence <= ?
		WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY fa.recorded_at DESC, fa.annotation_id DESC
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return model.FixAnnotationPage{}, errors.New("query fix annotation history")
	}
	defer rows.Close()
	annotations := make([]model.FixAnnotation, 0, query.Limit+1)
	for rows.Next() {
		stored, err := scanStoredFixAnnotation(rows)
		if err != nil {
			return model.FixAnnotationPage{}, err
		}
		annotations = append(annotations, stored.Annotation)
	}
	if err := rows.Err(); err != nil {
		return model.FixAnnotationPage{}, errors.New("read fix annotation history")
	}
	hasMore := len(annotations) > query.Limit
	if hasMore {
		annotations = annotations[:query.Limit]
	}
	evaluatedAt := s.nowUTC()
	if err := tx.Commit(); err != nil {
		return model.FixAnnotationPage{}, errors.New("complete fix annotation history read")
	}
	return model.FixAnnotationPage{
		Data:                annotations,
		AnnotationSnapshot:  query.AnnotationSnapshot,
		RetractionSnapshot:  query.RetractionSnapshot,
		HasMore:             hasMore,
		EvidenceEvaluatedAt: evaluatedAt,
	}, nil
}

func (s *Store) fixEligibilityAndAnchorTx(
	ctx context.Context,
	tx *sql.Tx,
	issueID string,
	snapshot int64,
) (model.FixEligibility, fixAnchor, error) {
	aggregate, err := readFixIssueAggregateTx(ctx, tx, issueID, snapshot)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.FixEligibility{}, fixAnchor{}, ErrFixIssueNotFound
		}
		return model.FixEligibility{}, fixAnchor{}, err
	}
	eligibility := model.FixEligibility{
		Eligible: false,
		Reason:   model.FixEligibilityEligible,
		IssueID:  issueID,
		Snapshot: snapshot,
	}
	switch {
	case aggregate.AnalysisStatus != model.AnalysisCurrent:
		eligibility.Reason = model.FixEligibilityAnalysisNotCurrent
		return eligibility, fixAnchor{}, nil
	case aggregate.Experimental:
		eligibility.Reason = model.FixEligibilityExperimentalSignal
		return eligibility, fixAnchor{}, nil
	case aggregate.Category == model.AttentionKindEvidenceGap:
		eligibility.Reason = model.FixEligibilityEvidenceGap
		return eligibility, fixAnchor{}, nil
	case aggregate.ScopeQuality != model.ScopeResolved &&
		aggregate.ScopeQuality != model.ScopeLexical:
		eligibility.Reason = model.FixEligibilityScopeUnavailable
		return eligibility, fixAnchor{}, nil
	}
	anchor, err := s.readFixAnchorTx(ctx, tx, issueID, snapshot)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.FixEligibility{}, fixAnchor{}, ErrFixIssueNotFound
		}
		return model.FixEligibility{}, fixAnchor{}, err
	}
	if anchor.AnalysisStatus != model.AnalysisCurrent {
		eligibility.Reason = model.FixEligibilityAnalysisNotCurrent
		return eligibility, fixAnchor{}, nil
	}
	eligibility.Eligible = true
	return eligibility, anchor, nil
}

func readFixIssueAggregateTx(
	ctx context.Context,
	tx *sql.Tx,
	issueID string,
	snapshot int64,
) (fixIssueAggregate, error) {
	var result fixIssueAggregate
	var experimental int
	err := tx.QueryRowContext(ctx, `
		WITH visible AS (
			SELECT
				io.category,
				io.experimental,
				io.scope_quality,
				CASE sar.status
					WHEN 'failed' THEN 4
					WHEN 'pending' THEN 3
					WHEN 'truncated' THEN 2
					ELSE 1
				END AS analysis_status_rank
			FROM issue_occurrences io
			JOIN session_analysis_revisions sar
				ON sar.session_key = io.session_key
				AND sar.visible_from_generation <= ?
				AND (
					sar.visible_until_generation IS NULL OR
					sar.visible_until_generation > ?
				)
			WHERE io.issue_id = ?
				AND io.visible_from_generation <= ?
				AND (
					io.visible_until_generation IS NULL OR
					io.visible_until_generation > ?
				)
		)
		SELECT
			CASE MAX(analysis_status_rank)
				WHEN 4 THEN 'failed'
				WHEN 3 THEN 'pending'
				WHEN 2 THEN 'truncated'
				ELSE 'current'
			END,
			MIN(category),
			MAX(experimental),
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
			END
		FROM visible
		HAVING COUNT(*) > 0`,
		snapshot,
		snapshot,
		issueID,
		snapshot,
		snapshot,
	).Scan(
		&result.AnalysisStatus,
		&result.Category,
		&experimental,
		&result.ScopeQuality,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fixIssueAggregate{}, sql.ErrNoRows
		}
		return fixIssueAggregate{}, errors.New("read fix issue eligibility")
	}
	result.Experimental = experimental == 1
	return result, nil
}

func (s *Store) readFixAnchorTx(
	ctx context.Context,
	tx *sql.Tx,
	issueID string,
	snapshot int64,
) (fixAnchor, error) {
	var result fixAnchor
	var firstObserved, lastObserved string
	var fingerprintScopeID sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT
			io.revision_id, io.occurrence_id, io.session_key,
			io.fingerprint_id, io.fingerprint_version, io.origin,
			io.detector_id, io.detector_version, io.scope_quality,
			io.fingerprint_scope_id, io.category, io.title_code, io.severity,
			io.confidence, io.harness,
			sar.status, io.analysis_generation,
			io.first_observed_at, io.last_observed_at
		FROM issue_occurrences io
		JOIN session_analysis_revisions sar
			ON sar.session_key = io.session_key
			AND sar.visible_from_generation <= ?
			AND (
				sar.visible_until_generation IS NULL OR
				sar.visible_until_generation > ?
			)
		WHERE io.issue_id = ?
			AND io.visible_from_generation <= ?
			AND (
				io.visible_until_generation IS NULL OR
				io.visible_until_generation > ?
			)
		ORDER BY io.last_observed_at DESC, io.occurrence_id ASC
		LIMIT 1`,
		snapshot,
		snapshot,
		issueID,
		snapshot,
		snapshot,
	).Scan(
		&result.RevisionID,
		&result.OccurrenceID,
		&result.SessionID,
		&result.FingerprintID,
		&result.FingerprintVersion,
		&result.Origin,
		&result.DetectorID,
		&result.DetectorVersion,
		&result.ScopeQuality,
		&fingerprintScopeID,
		&result.Category,
		&result.TitleCode,
		&result.Severity,
		&result.Confidence,
		&result.Harness,
		&result.AnalysisStatus,
		&result.AnalysisGeneration,
		&firstObserved,
		&lastObserved,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fixAnchor{}, sql.ErrNoRows
		}
		return fixAnchor{}, errors.New("read fix annotation anchor")
	}
	result.FingerprintScopeID = fingerprintScopeID.String
	result.FirstObservedAt, err = parseProjectionTime(firstObserved)
	if err != nil {
		return fixAnchor{}, errors.New("decode fix anchor start")
	}
	result.LastObservedAt, err = parseProjectionTime(lastObserved)
	if err != nil {
		return fixAnchor{}, errors.New("decode fix anchor end")
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT event_id
		FROM issue_occurrence_events
		WHERE revision_id = ?
		ORDER BY event_id`,
		result.RevisionID,
	)
	if err != nil {
		return fixAnchor{}, errors.New("read fix anchor citations")
	}
	defer rows.Close()
	for rows.Next() {
		var eventID string
		if err := rows.Scan(&eventID); err != nil {
			return fixAnchor{}, errors.New("read fix anchor citation")
		}
		result.CitedEventIDs = append(result.CitedEventIDs, eventID)
	}
	if err := rows.Err(); err != nil {
		return fixAnchor{}, errors.New("read fix anchor citations")
	}
	return result, nil
}

func (s *Store) readFixAnnotationByIDTx(
	ctx context.Context,
	tx *sql.Tx,
	annotationID string,
) (storedFixAnnotation, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT
			fa.annotation_id, fa.request_fingerprint, fa.issue_id,
			fa.anchor_revision_id, fa.anchor_occurrence_id, fa.anchor_session_id,
			fa.fingerprint_id, fa.fingerprint_version, fa.origin,
			fa.detector_id, fa.detector_version, fa.scope_quality,
			fa.issue_snapshot_generation, fa.anchor_analysis_generation,
			fa.anchor_first_observed_at, fa.anchor_last_observed_at,
			fa.baseline_citation_count, fa.change_kind,
			fa.change_catalog_version, fa.recorded_via, fa.recorded_at,
			fa.monitor_from,
			(SELECT COUNT(*) FROM fix_annotation_events fae
				WHERE fae.annotation_id = fa.annotation_id),
			far.reason, far.retracted_at
		FROM fix_annotations fa
		LEFT JOIN fix_annotation_retractions far
			ON far.annotation_id = fa.annotation_id
		WHERE fa.annotation_id = ?`,
		annotationID,
	)
	return scanStoredFixAnnotation(row)
}

func scanStoredFixAnnotation(row rowScanner) (storedFixAnnotation, error) {
	var result storedFixAnnotation
	var firstObserved, lastObserved, recordedAt, monitorFrom string
	var remainingCitations int
	var retractionReason, retractedAt sql.NullString
	if err := row.Scan(
		&result.Annotation.AnnotationID,
		&result.RequestFingerprint,
		&result.Annotation.IssueID,
		&result.Annotation.AnchorRevisionID,
		&result.Annotation.AnchorOccurrenceID,
		&result.Annotation.AnchorSessionID,
		&result.Annotation.FingerprintID,
		&result.Annotation.FingerprintVersion,
		&result.Annotation.Origin,
		&result.Annotation.DetectorID,
		&result.Annotation.DetectorVersion,
		&result.Annotation.ScopeQuality,
		&result.Annotation.IssueSnapshotGeneration,
		&result.Annotation.AnchorAnalysisGeneration,
		&firstObserved,
		&lastObserved,
		&result.BaselineCitations,
		&result.Annotation.ChangeKind,
		&result.Annotation.ChangeCatalogVersion,
		&result.Annotation.RecordedVia,
		&recordedAt,
		&monitorFrom,
		&remainingCitations,
		&retractionReason,
		&retractedAt,
	); err != nil {
		return storedFixAnnotation{}, err
	}
	var err error
	result.Annotation.AnchorFirstObservedAt, err = parseProjectionTime(firstObserved)
	if err != nil {
		return storedFixAnnotation{}, errors.New("decode fix anchor first-observed timestamp")
	}
	result.Annotation.AnchorLastObservedAt, err = parseProjectionTime(lastObserved)
	if err != nil {
		return storedFixAnnotation{}, errors.New("decode fix anchor last-observed timestamp")
	}
	result.Annotation.RecordedAt, err = parseProjectionTime(recordedAt)
	if err != nil {
		return storedFixAnnotation{}, errors.New("decode fix annotation timestamp")
	}
	result.Annotation.MonitorFrom, err = parseProjectionTime(monitorFrom)
	if err != nil {
		return storedFixAnnotation{}, errors.New("decode fix monitoring timestamp")
	}
	result.Annotation.EvidenceCurrentlyRetained = evidenceRetentionStatus(
		result.BaselineCitations,
		remainingCitations,
	)
	result.Annotation.State = model.FixStateActive
	if retractionReason.Valid {
		result.Annotation.State = model.FixStateRetracted
		result.Annotation.RetractionReason = retractionReason.String
		if !retractedAt.Valid {
			return storedFixAnnotation{}, errors.New("fix retraction timestamp is missing")
		}
		value, err := parseProjectionTime(retractedAt.String)
		if err != nil {
			return storedFixAnnotation{}, errors.New("decode fix retraction timestamp")
		}
		result.Annotation.RetractedAt = &value
	}
	return result, nil
}

func readFixRetractionByIDTx(
	ctx context.Context,
	tx *sql.Tx,
	retractionID string,
) (storedFixRetraction, error) {
	var result storedFixRetraction
	var retractedAt string
	err := tx.QueryRowContext(ctx, `
		SELECT
			far.retraction_id, far.request_fingerprint, far.annotation_id,
			fa.issue_id, far.reason, far.recorded_via, far.retracted_at
		FROM fix_annotation_retractions far
		JOIN fix_annotations fa ON fa.annotation_id = far.annotation_id
		WHERE far.retraction_id = ?`,
		retractionID,
	).Scan(
		&result.Retraction.RetractionID,
		&result.RequestFingerprint,
		&result.Retraction.AnnotationID,
		&result.Retraction.IssueID,
		&result.Retraction.Reason,
		&result.Retraction.RecordedVia,
		&retractedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storedFixRetraction{}, sql.ErrNoRows
		}
		return storedFixRetraction{}, errors.New("read fix annotation retraction")
	}
	result.Retraction.RetractedAt, err = parseProjectionTime(retractedAt)
	if err != nil {
		return storedFixRetraction{}, errors.New("decode fix retraction timestamp")
	}
	return result, nil
}

func evidenceRetentionStatus(baseline, remaining int) string {
	switch {
	case baseline == 0:
		return model.FixEvidenceUnknown
	case remaining == baseline:
		return model.FixEvidenceAvailable
	case remaining == 0:
		return model.FixEvidencePruned
	default:
		return model.FixEvidencePartial
	}
}

func validFixAnnotationInput(input FixAnnotationInput) bool {
	return validFixActionClaims(input.Claims) &&
		input.ChangeKind.Valid() &&
		input.RecordedVia == model.FixRecordedViaLocalUI &&
		model.IsCanonicalUUIDv4(input.IdempotencyKey)
}

func validFixRetractionInput(input FixRetractionInput) bool {
	return validIssueID(input.IssueID) &&
		validFixAnnotationID(input.AnnotationID) &&
		input.Reason.Valid() &&
		input.RecordedVia == model.FixRecordedViaLocalUI &&
		model.IsCanonicalUUIDv4(input.IdempotencyKey)
}

func validIssueID(value string) bool {
	return strings.HasPrefix(value, "iss_") && opaqueIdentityPattern.MatchString(value)
}

func validFixAnnotationID(value string) bool {
	return validOpaquePrefixedID(value, "fxa_")
}

func validOpaquePrefixedID(value, prefix string) bool {
	if len(value) != len(prefix)+52 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, character := range strings.TrimPrefix(value, prefix) {
		if (character < 'a' || character > 'z') &&
			(character < '2' || character > '7') {
			return false
		}
	}
	return true
}

func (e *FixIneligibleError) ReasonCode() string {
	return e.Reason
}
