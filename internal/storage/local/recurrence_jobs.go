package local

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"sort"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

var (
	ErrNoRecurrenceJob    = errors.New("no recurrence job is ready")
	ErrStaleRecurrenceJob = errors.New("recurrence job claim is stale")
)

const (
	defaultRecurrenceLease = 30 * time.Second
	maxRecurrenceBatch     = 100
)

type recurrenceCandidate struct {
	sequence           int64
	annotationID       string
	issueID            string
	anchorSessionID    string
	issueSnapshot      int64
	monitorFromOrderNS sql.NullInt64
	fingerprintScopeID sql.NullString
	fingerprintID      string
	fingerprintVersion string
	origin             string
}

type recurrenceMatch struct {
	revisionID           string
	occurrenceID         string
	sessionID            string
	fingerprintID        string
	fingerprintVersion   string
	origin               string
	detectorID           string
	detectorVersion      string
	analysisGeneration   int64
	projectionGeneration int64
	evidenceComplete     int
	eventIDs             []string
	eventOrders          []int64
}

func (s *Store) ClaimRecurrenceJob(
	ctx context.Context,
	now time.Time,
	lease time.Duration,
) (model.FixRecurrenceJobClaim, error) {
	if now.IsZero() {
		now = s.nowUTC()
	}
	now = now.UTC()
	if lease <= 0 {
		lease = defaultRecurrenceLease
	}
	token, err := randomClaimToken(s.random)
	if err != nil {
		return model.FixRecurrenceJobClaim{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.FixRecurrenceJobClaim{}, errors.New("begin recurrence job claim")
	}
	defer tx.Rollback()
	var result model.FixRecurrenceJobClaim
	var state string
	var priorGeneration int64
	err = tx.QueryRowContext(ctx, `
		SELECT
			job_id, session_id, revision_id, projection_generation,
			annotation_snapshot, attempt_after_sequence, claim_generation,
			attempt_count, state
		FROM fix_recurrence_jobs
		WHERE (
			state IN ('pending', 'failed')
			AND (retry_at IS NULL OR retry_at <= ?)
		) OR (
			state = 'claimed'
			AND lease_expires_at IS NOT NULL
			AND lease_expires_at <= ?
		)
		ORDER BY sequence
		LIMIT 1`,
		formatProjectionTime(now),
		formatProjectionTime(now),
	).Scan(
		&result.JobID,
		&result.SessionID,
		&result.RevisionID,
		&result.ProjectionGeneration,
		&result.AnnotationSnapshot,
		&result.AttemptAfterSequence,
		&priorGeneration,
		&result.AttemptCount,
		&state,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.FixRecurrenceJobClaim{}, ErrNoRecurrenceJob
	}
	if err != nil {
		return model.FixRecurrenceJobClaim{}, errors.New("read recurrence job claim")
	}
	err = withMutationTx(ctx, tx, mutationRecurrenceWorker, func() error {
		updated, err := tx.ExecContext(ctx, `
			UPDATE fix_recurrence_jobs
			SET state = 'claimed',
				claim_generation = claim_generation + 1,
				claim_token = ?,
				lease_expires_at = ?,
				attempt_count = attempt_count + 1,
				claimed_at = ?,
				updated_at = ?
			WHERE job_id = ?
				AND claim_generation = ?
				AND state = ?`,
			token,
			formatProjectionTime(now.Add(lease)),
			formatProjectionTime(now),
			formatProjectionTime(now),
			result.JobID,
			priorGeneration,
			state,
		)
		if err != nil {
			return errors.New("claim recurrence job")
		}
		count, err := updated.RowsAffected()
		if err != nil || count != 1 {
			return ErrStaleRecurrenceJob
		}
		return nil
	})
	if err != nil {
		return model.FixRecurrenceJobClaim{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.FixRecurrenceJobClaim{}, errors.New("commit recurrence job claim")
	}
	result.ClaimGeneration = priorGeneration + 1
	result.ClaimToken = token
	result.AttemptCount++
	return result, nil
}

func (s *Store) ProcessRecurrenceBatch(
	ctx context.Context,
	claim model.FixRecurrenceJobClaim,
	limit int,
) (model.FixRecurrenceBatchResult, error) {
	if !validOpaquePrefixedID(claim.JobID, "fxj_") ||
		claim.ClaimGeneration < 1 ||
		claim.ClaimToken == "" {
		return model.FixRecurrenceBatchResult{}, ErrStaleRecurrenceJob
	}
	if limit <= 0 || limit > maxRecurrenceBatch {
		limit = maxRecurrenceBatch
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.FixRecurrenceBatchResult{}, errors.New("begin recurrence job batch")
	}
	defer tx.Rollback()
	var persisted model.FixRecurrenceJobClaim
	var state string
	if err := tx.QueryRowContext(ctx, `
		SELECT
			session_id, revision_id, projection_generation,
			annotation_snapshot, attempt_after_sequence, attempt_count, state
		FROM fix_recurrence_jobs
		WHERE job_id = ? AND claim_generation = ? AND claim_token = ?`,
		claim.JobID,
		claim.ClaimGeneration,
		claim.ClaimToken,
	).Scan(
		&persisted.SessionID,
		&persisted.RevisionID,
		&persisted.ProjectionGeneration,
		&persisted.AnnotationSnapshot,
		&persisted.AttemptAfterSequence,
		&persisted.AttemptCount,
		&state,
	); err != nil || state != "claimed" {
		return model.FixRecurrenceBatchResult{}, ErrStaleRecurrenceJob
	}
	candidates, err := readRecurrenceCandidatesTx(ctx, tx, persisted, limit)
	if err != nil {
		return model.FixRecurrenceBatchResult{}, err
	}
	result := model.FixRecurrenceBatchResult{}
	nextSequence := persisted.AttemptAfterSequence
	err = withMutationTx(ctx, tx, mutationRecurrenceWorker, func() error {
		for _, candidate := range candidates {
			nextSequence = candidate.sequence
			result.Processed++
			var retracted int
			if err := tx.QueryRowContext(ctx, `
				SELECT COUNT(*)
				FROM fix_annotation_retractions
				WHERE annotation_id = ?`,
				candidate.annotationID,
			).Scan(&retracted); err != nil {
				return errors.New("recheck recurrence retraction")
			}
			if retracted != 0 ||
				!candidate.monitorFromOrderNS.Valid ||
				!candidate.fingerprintScopeID.Valid {
				continue
			}
			matches, err := readRecurrenceMatchesTx(
				ctx,
				tx,
				persisted,
				candidate,
			)
			if err != nil {
				return err
			}
			for _, match := range matches {
				inserted, err := s.insertRecurrenceObservationTx(
					ctx,
					tx,
					candidate,
					match,
				)
				if err != nil {
					return err
				}
				if inserted {
					result.Observed++
				}
			}
		}
		result.NextSequence = nextSequence
		result.Completed = len(candidates) < limit ||
			nextSequence >= persisted.AnnotationSnapshot
		if result.Completed {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO fix_recurrence_job_events (
					job_id, event_kind, attempt_number, recorded_at
				) VALUES (?, 'complete', ?, ?)
				ON CONFLICT(job_id, event_kind, attempt_number) DO NOTHING`,
				claim.JobID,
				persisted.AttemptCount,
				formatProjectionTime(s.nowUTC()),
			); err != nil {
				return errors.New("record recurrence job completion")
			}
		}
		state := "claimed"
		var token any = claim.ClaimToken
		var lease any = formatProjectionTime(s.nowUTC().Add(defaultRecurrenceLease))
		if result.Completed {
			state = "complete"
			token = nil
			lease = nil
		}
		updated, err := tx.ExecContext(ctx, `
			UPDATE fix_recurrence_jobs
			SET attempt_after_sequence = ?,
				state = ?,
				claim_token = ?,
				lease_expires_at = ?,
				updated_at = ?
			WHERE job_id = ?
				AND claim_generation = ?
				AND claim_token = ?
				AND state = 'claimed'`,
			nextSequence,
			state,
			token,
			lease,
			formatProjectionTime(s.nowUTC()),
			claim.JobID,
			claim.ClaimGeneration,
			claim.ClaimToken,
		)
		if err != nil {
			return errors.New("advance recurrence job")
		}
		count, err := updated.RowsAffected()
		if err != nil || count != 1 {
			return ErrStaleRecurrenceJob
		}
		return nil
	})
	if err != nil {
		return model.FixRecurrenceBatchResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.FixRecurrenceBatchResult{}, errors.New("commit recurrence job batch")
	}
	return result, nil
}

func (s *Store) FailRecurrenceJob(
	ctx context.Context,
	claim model.FixRecurrenceJobClaim,
	code string,
	retryAt time.Time,
) error {
	if !fixedCodePattern.MatchString(code) || retryAt.IsZero() {
		return errors.New("invalid recurrence job failure")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin recurrence job failure")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationRecurrenceWorker, func() error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO fix_recurrence_job_events (
				job_id, event_kind, attempt_number, recorded_at
			) VALUES (?, 'failed', ?, ?)
			ON CONFLICT(job_id, event_kind, attempt_number) DO NOTHING`,
			claim.JobID,
			claim.AttemptCount,
			formatProjectionTime(s.nowUTC()),
		); err != nil {
			return errors.New("record recurrence job failure")
		}
		updated, err := tx.ExecContext(ctx, `
			UPDATE fix_recurrence_jobs
			SET state = 'failed',
				claim_token = NULL,
				lease_expires_at = NULL,
				retry_at = ?,
				updated_at = ?
			WHERE job_id = ?
				AND claim_generation = ?
				AND claim_token = ?
				AND state = 'claimed'`,
			formatProjectionTime(retryAt),
			formatProjectionTime(s.nowUTC()),
			claim.JobID,
			claim.ClaimGeneration,
			claim.ClaimToken,
		)
		if err != nil {
			return errors.New("fail recurrence job")
		}
		count, err := updated.RowsAffected()
		if err != nil || count != 1 {
			return ErrStaleRecurrenceJob
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit recurrence job failure")
	}
	return nil
}

func readRecurrenceCandidatesTx(
	ctx context.Context,
	tx *sql.Tx,
	job model.FixRecurrenceJobClaim,
	limit int,
) ([]recurrenceCandidate, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			fa.sequence, fa.annotation_id, fa.issue_id, fa.anchor_session_id,
			fa.issue_snapshot_generation, fms.monitor_from_order_ns,
			fms.fingerprint_scope_id, fa.fingerprint_id,
			fa.fingerprint_version, fa.origin
		FROM fix_annotations fa
		JOIN fix_monitoring_subjects fms
			ON fms.annotation_id = fa.annotation_id
		WHERE fa.sequence > ?
			AND fa.sequence <= ?
		ORDER BY fa.sequence
		LIMIT ?`,
		job.AttemptAfterSequence,
		job.AnnotationSnapshot,
		limit,
	)
	if err != nil {
		return nil, errors.New("read recurrence annotation batch")
	}
	defer rows.Close()
	var result []recurrenceCandidate
	for rows.Next() {
		var candidate recurrenceCandidate
		if err := rows.Scan(
			&candidate.sequence,
			&candidate.annotationID,
			&candidate.issueID,
			&candidate.anchorSessionID,
			&candidate.issueSnapshot,
			&candidate.monitorFromOrderNS,
			&candidate.fingerprintScopeID,
			&candidate.fingerprintID,
			&candidate.fingerprintVersion,
			&candidate.origin,
		); err != nil {
			return nil, errors.New("read recurrence annotation batch")
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read recurrence annotation batch")
	}
	return result, nil
}

func readRecurrenceMatchesTx(
	ctx context.Context,
	tx *sql.Tx,
	job model.FixRecurrenceJobClaim,
	candidate recurrenceCandidate,
) ([]recurrenceMatch, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			io.revision_id, io.occurrence_id, io.session_key, io.fingerprint_id,
			io.fingerprint_version, io.origin, io.detector_id,
			io.detector_version, io.analysis_generation,
			io.visible_from_generation, io.evidence_complete,
			ioe.event_id, e.occurred_at_order_ns
		FROM issue_occurrences io
		JOIN session_analysis_revisions sar
			ON sar.revision_id = ?
		JOIN issue_occurrence_events ioe
			ON ioe.revision_id = io.revision_id
		JOIN events e
			ON e.event_id = ioe.event_id
			AND e.session_key = io.session_key
		WHERE io.revision_id IN (
			SELECT revision_id
			FROM issue_occurrences
			WHERE session_key = ?
				AND visible_from_generation = ?
		)
			AND io.issue_id = ?
			AND io.fingerprint_id = ?
			AND io.fingerprint_version = ?
			AND io.origin = ?
			AND io.fingerprint_scope_id = ?
			AND io.scope_quality IN ('resolved', 'lexical')
			AND io.experimental = 0
			AND io.category <> 'evidence_gap'
			AND io.analysis_status = 'current'
			AND sar.status = 'current'
			AND io.visible_from_generation > ?
			AND e.occurred_at_order_ns > ?
		ORDER BY io.occurrence_id, e.occurred_at_order_ns, e.event_id`,
		job.RevisionID,
		job.SessionID,
		job.ProjectionGeneration,
		candidate.issueID,
		candidate.fingerprintID,
		candidate.fingerprintVersion,
		candidate.origin,
		candidate.fingerprintScopeID.String,
		candidate.issueSnapshot,
		candidate.monitorFromOrderNS.Int64,
	)
	if err != nil {
		return nil, errors.New("read recurrence matches")
	}
	defer rows.Close()
	byOccurrence := make(map[string]*recurrenceMatch)
	var order []string
	for rows.Next() {
		var match recurrenceMatch
		var eventID string
		var eventOrder int64
		if err := rows.Scan(
			&match.revisionID,
			&match.occurrenceID,
			&match.sessionID,
			&match.fingerprintID,
			&match.fingerprintVersion,
			&match.origin,
			&match.detectorID,
			&match.detectorVersion,
			&match.analysisGeneration,
			&match.projectionGeneration,
			&match.evidenceComplete,
			&eventID,
			&eventOrder,
		); err != nil {
			return nil, errors.New("read recurrence matches")
		}
		current := byOccurrence[match.occurrenceID]
		if current == nil {
			copy := match
			current = &copy
			byOccurrence[match.occurrenceID] = current
			order = append(order, match.occurrenceID)
		}
		current.eventIDs = append(current.eventIDs, eventID)
		current.eventOrders = append(current.eventOrders, eventOrder)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read recurrence matches")
	}
	sort.Strings(order)
	result := make([]recurrenceMatch, 0, len(order))
	for _, occurrenceID := range order {
		result = append(result, *byOccurrence[occurrenceID])
	}
	return result, nil
}

func (s *Store) insertRecurrenceObservationTx(
	ctx context.Context,
	tx *sql.Tx,
	candidate recurrenceCandidate,
	match recurrenceMatch,
) (bool, error) {
	recurrenceID, err := s.deriveFixRecurrenceID(
		candidate.annotationID,
		match.occurrenceID,
	)
	if err != nil {
		return false, err
	}
	first := match.eventOrders[0]
	last := match.eventOrders[len(match.eventOrders)-1]
	result, err := tx.ExecContext(ctx, `
		INSERT INTO fix_recurrence_observations (
			recurrence_id, annotation_id, issue_id, revision_id,
			occurrence_id, session_id, fingerprint_id, fingerprint_version,
			origin, detector_id, detector_version, analysis_generation,
			projection_generation, first_qualifying_event_order_ns,
			last_qualifying_event_order_ns, qualifying_citation_count,
			evidence_complete, same_session_as_anchor, observed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		recurrenceID,
		candidate.annotationID,
		candidate.issueID,
		match.revisionID,
		match.occurrenceID,
		match.sessionID,
		match.fingerprintID,
		match.fingerprintVersion,
		match.origin,
		match.detectorID,
		match.detectorVersion,
		match.analysisGeneration,
		match.projectionGeneration,
		first,
		last,
		len(match.eventIDs),
		match.evidenceComplete,
		boolInt(match.sessionID == candidate.anchorSessionID),
		formatProjectionTime(s.nowUTC()),
	)
	if err != nil {
		return false, errors.New("insert recurrence observation")
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, errors.New("inspect recurrence observation")
	}
	if inserted == 0 {
		var annotationID, occurrenceID string
		if err := tx.QueryRowContext(ctx, `
			SELECT annotation_id, occurrence_id
			FROM fix_recurrence_observations
			WHERE recurrence_id = ? OR
				(annotation_id = ? AND occurrence_id = ?)
			LIMIT 1`,
			recurrenceID,
			candidate.annotationID,
			match.occurrenceID,
		).Scan(&annotationID, &occurrenceID); err != nil ||
			annotationID != candidate.annotationID ||
			occurrenceID != match.occurrenceID {
			return false, errors.New("recurrence observation identity collision")
		}
		return false, nil
	}
	for _, eventID := range match.eventIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO fix_recurrence_observation_events (
				recurrence_id, event_id
			) VALUES (?, ?)`,
			recurrenceID,
			eventID,
		); err != nil {
			return false, errors.New("insert recurrence observation citation")
		}
	}
	return true, nil
}

func randomClaimToken(source io.Reader) (string, error) {
	if source == nil {
		source = rand.Reader
	}
	bytes := make([]byte, 24)
	if _, err := io.ReadFull(source, bytes); err != nil {
		return "", errors.New("generate recurrence claim token")
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func int64String(value int64) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	negative := value < 0
	if negative {
		value = -value
	}
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		buffer[index] = '-'
	}
	return string(buffer[index:])
}

func (s *Store) PrepareFixMonitoringCatchup(ctx context.Context) error {
	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin fix monitoring catch-up")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationRecurrenceWorker, func() error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE fix_monitoring_metadata
			SET readiness = 'catching_up',
				retry_at = NULL,
				failure_code = NULL,
				catchup_started_at = COALESCE(catchup_started_at, ?),
				updated_at = ?
			WHERE singleton = 1 AND readiness <> 'ready'`,
			now,
			now,
		); err != nil {
			return errors.New("start fix monitoring catch-up")
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT revision_id, session_key, visible_from_generation
			FROM session_analysis_revisions
			WHERE status = 'current'
			ORDER BY visible_from_generation, revision_id`)
		if err != nil {
			return errors.New("read legacy analysis revisions")
		}
		var revisions []struct {
			id         string
			sessionID  string
			generation int64
		}
		for rows.Next() {
			var item struct {
				id         string
				sessionID  string
				generation int64
			}
			if err := rows.Scan(&item.id, &item.sessionID, &item.generation); err != nil {
				rows.Close()
				return errors.New("read legacy analysis revisions")
			}
			revisions = append(revisions, item)
		}
		if err := rows.Close(); err != nil {
			return errors.New("read legacy analysis revisions")
		}
		for _, revision := range revisions {
			if err := s.enqueueRecurrenceJobTx(
				ctx,
				tx,
				revision.sessionID,
				revision.id,
				revision.generation,
				now,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit fix monitoring catch-up")
	}
	return s.markLegacyBelaySessionsDirty(ctx)
}

func (s *Store) markLegacyBelaySessionsDirty(ctx context.Context) error {
	_, complete, err := s.migrationProgress(ctx, "catchup_dirty")
	if err != nil || complete {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin legacy Belay reanalysis")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		rows, err := tx.QueryContext(ctx, `
			SELECT DISTINCT session_key
			FROM issue_occurrences
			WHERE origin = 'belay'
			ORDER BY session_key`)
		if err != nil {
			return errors.New("read legacy Belay sessions")
		}
		var sessions []string
		for rows.Next() {
			var sessionID string
			if err := rows.Scan(&sessionID); err != nil {
				rows.Close()
				return errors.New("read legacy Belay sessions")
			}
			sessions = append(sessions, sessionID)
		}
		if err := rows.Close(); err != nil {
			return errors.New("read legacy Belay sessions")
		}
		now := formatProjectionTime(s.nowUTC())
		for _, sessionID := range sessions {
			if _, err := s.markSessionDirtyTx(
				ctx,
				tx,
				sessionID,
				"recurrence_capability_backfill",
				s.sessionScopeQualityTx(ctx, tx, sessionID),
				now,
			); err != nil {
				return err
			}
		}
		return s.writeMigrationProgressTx(ctx, tx, "catchup_dirty", 1, true)
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit legacy Belay reanalysis")
	}
	return nil
}

func (s *Store) CompleteFixMonitoringCatchup(ctx context.Context) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.New("begin fix monitoring readiness completion")
	}
	defer tx.Rollback()
	var outstandingJobs, outstandingAnalysis int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM fix_recurrence_jobs
		WHERE state <> 'complete'`,
	).Scan(&outstandingJobs); err != nil {
		return false, errors.New("read fix monitoring jobs")
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM dirty_sessions
		WHERE state IN ('pending', 'claimed', 'failed')`,
	).Scan(&outstandingAnalysis); err != nil {
		return false, errors.New("read fix monitoring analysis")
	}
	if outstandingJobs != 0 || outstandingAnalysis != 0 {
		return false, nil
	}
	err = withMutationTx(ctx, tx, mutationRecurrenceWorker, func() error {
		_, err := tx.ExecContext(ctx, `
			UPDATE fix_monitoring_metadata
			SET readiness = 'ready',
				retry_at = NULL,
				failure_code = NULL,
				catchup_completed_at = ?,
				updated_at = ?
			WHERE singleton = 1`,
			formatProjectionTime(s.nowUTC()),
			formatProjectionTime(s.nowUTC()),
		)
		return err
	})
	if err != nil {
		return false, errors.New("complete fix monitoring readiness")
	}
	if err := tx.Commit(); err != nil {
		return false, errors.New("commit fix monitoring readiness")
	}
	return true, nil
}

func (s *Store) FailFixMonitoringCatchup(
	ctx context.Context,
	code string,
	retryAt time.Time,
) error {
	if !fixedCodePattern.MatchString(code) || retryAt.IsZero() {
		return errors.New("invalid fix monitoring catch-up failure")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin fix monitoring catch-up failure")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationRecurrenceWorker, func() error {
		_, err := tx.ExecContext(ctx, `
			UPDATE fix_monitoring_metadata
			SET readiness = 'failed',
				attempt_count = attempt_count + 1,
				retry_at = ?,
				failure_code = ?,
				updated_at = ?
			WHERE singleton = 1`,
			formatProjectionTime(retryAt),
			code,
			formatProjectionTime(s.nowUTC()),
		)
		return err
	})
	if err != nil {
		return errors.New("record fix monitoring catch-up failure")
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit fix monitoring catch-up failure")
	}
	return nil
}

func (s *Store) FixMonitoringReadiness(ctx context.Context) (string, error) {
	var readiness string
	if err := s.db.QueryRowContext(ctx, `
		SELECT readiness
		FROM fix_monitoring_metadata
		WHERE singleton = 1`,
	).Scan(&readiness); err != nil {
		return "", errors.New("read fix monitoring readiness")
	}
	return readiness, nil
}
