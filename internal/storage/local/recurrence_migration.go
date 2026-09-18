package local

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	fixRecurrenceMigrationVersion = 11
	migrationBackfillBatchSize    = 250
)

type migrationEventRow struct {
	sequence   int64
	eventID    string
	occurredAt string
}

type migrationOccurrenceRow struct {
	rowID        int64
	revisionID   string
	origin       string
	scopeQuality string
	sessionScope sql.NullString
	findingScope sql.NullString
}

type migrationAnalysisRow struct {
	rowID int64
}

type migrationSubjectRow struct {
	sequence           int64
	annotationID       string
	monitorFrom        string
	origin             string
	detectorID         string
	detectorVersion    string
	fingerprintVersion string
	scopeID            sql.NullString
	category           sql.NullString
	titleCode          sql.NullString
	severity           sql.NullString
	confidence         sql.NullString
	harness            sql.NullString
}

func (s *Store) resumeFixRecurrenceMigration(ctx context.Context) error {
	var applied int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		fixRecurrenceMigrationVersion,
	).Scan(&applied); err != nil {
		return errors.New("inspect recurrence migration")
	}
	if applied == 0 {
		return nil
	}
	var completed int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM local_migration_progress
		WHERE migration_version = ?
			AND phase IN (
				'event_order',
				'occurrence_scope',
				'analysis_watermark',
				'monitoring_subject'
			)
			AND complete = 1`,
		fixRecurrenceMigrationVersion,
	).Scan(&completed); err != nil {
		return errors.New("inspect recurrence migration completion")
	}
	if completed == 4 {
		return nil
	}
	if err := s.backfillEventOrderNS(ctx); err != nil {
		return err
	}
	if err := s.backfillOccurrenceScopes(ctx); err != nil {
		return err
	}
	if err := s.backfillAnalysisWatermarks(ctx); err != nil {
		return err
	}
	if err := s.backfillMonitoringSubjects(ctx); err != nil {
		return err
	}
	now := formatProjectionTime(s.nowUTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		if isSQLiteBusy(err) {
			return ErrMaintenanceBusy
		}
		return errors.New("initialize local database")
	}
	defer tx.Rollback()
	if err := withMutationTx(ctx, tx, mutationRecurrenceWorker, func() error {
		_, err := tx.ExecContext(ctx, `
			UPDATE fix_monitoring_metadata
			SET updated_at = ?
			WHERE singleton = 1`,
			now,
		)
		return err
	}); err != nil {
		if isSQLiteBusy(err) {
			return ErrMaintenanceBusy
		}
		return errors.New("initialize local database")
	}
	if err := tx.Commit(); err != nil {
		if isSQLiteBusy(err) {
			return ErrMaintenanceBusy
		}
		return errors.New("initialize local database")
	}
	return nil
}

func (s *Store) backfillEventOrderNS(ctx context.Context) error {
	for {
		after, complete, err := s.migrationProgress(ctx, "event_order")
		if err != nil || complete {
			return err
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT ero.sequence, e.event_id, e.occurred_at
			FROM event_read_order ero
			JOIN events e ON e.event_id = ero.event_id
			WHERE ero.sequence > ?
			ORDER BY ero.sequence
			LIMIT ?`,
			after,
			migrationBackfillBatchSize,
		)
		if err != nil {
			return errors.New("read recurrence event backfill")
		}
		var batch []migrationEventRow
		for rows.Next() {
			var row migrationEventRow
			if err := rows.Scan(&row.sequence, &row.eventID, &row.occurredAt); err != nil {
				rows.Close()
				return errors.New("read recurrence event backfill")
			}
			batch = append(batch, row)
		}
		if err := rows.Close(); err != nil {
			return errors.New("read recurrence event backfill")
		}
		if len(batch) == 0 {
			return s.completeMigrationPhase(ctx, "event_order", after)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return errors.New("begin recurrence event backfill")
		}
		err = withMutationTx(ctx, tx, mutationPayloadUpgrade, func() error {
			for _, row := range batch {
				parsed, parseErr := time.Parse(time.RFC3339Nano, row.occurredAt)
				order, valid := projectionOrderNS(parsed)
				if parseErr != nil || !valid {
					continue
				}
				if _, err := tx.ExecContext(ctx, `
					UPDATE events
					SET occurred_at_order_ns = ?
					WHERE event_id = ? AND occurred_at_order_ns IS NULL`,
					order,
					row.eventID,
				); err != nil {
					return errors.New("write recurrence event backfill")
				}
			}
			return s.writeMigrationProgressTx(
				ctx, tx, "event_order", batch[len(batch)-1].sequence, false,
			)
		})
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			return err
		}
	}
}

func (s *Store) backfillOccurrenceScopes(ctx context.Context) error {
	for {
		after, complete, err := s.migrationProgress(ctx, "occurrence_scope")
		if err != nil || complete {
			return err
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT io.rowid, io.revision_id, io.origin, io.scope_quality,
				ss.project_scope_id, f.project_scope_hint
			FROM issue_occurrences io
			LEFT JOIN session_scopes ss ON ss.session_key = io.session_key
			LEFT JOIN findings f
				ON io.origin = 'numbat' AND f.finding_id = io.origin_record_id
			WHERE io.rowid > ?
			ORDER BY io.rowid
			LIMIT ?`,
			after,
			migrationBackfillBatchSize,
		)
		if err != nil {
			return errors.New("read recurrence occurrence backfill")
		}
		var batch []migrationOccurrenceRow
		for rows.Next() {
			var row migrationOccurrenceRow
			if err := rows.Scan(
				&row.rowID,
				&row.revisionID,
				&row.origin,
				&row.scopeQuality,
				&row.sessionScope,
				&row.findingScope,
			); err != nil {
				rows.Close()
				return errors.New("read recurrence occurrence backfill")
			}
			batch = append(batch, row)
		}
		if err := rows.Close(); err != nil {
			return errors.New("read recurrence occurrence backfill")
		}
		if len(batch) == 0 {
			return s.completeMigrationPhase(ctx, "occurrence_scope", after)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return errors.New("begin recurrence occurrence backfill")
		}
		err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
			for _, row := range batch {
				scope := ""
				switch {
				case row.origin == "belay" &&
					(row.scopeQuality == "resolved" || row.scopeQuality == "lexical") &&
					validProjectScopeID(row.sessionScope.String):
					scope = row.sessionScope.String
				case row.origin == "numbat" &&
					(row.scopeQuality == "resolved" || row.scopeQuality == "lexical") &&
					validProjectScopeID(row.findingScope.String):
					scope = row.findingScope.String
				}
				if scope == "" {
					continue
				}
				if _, err := tx.ExecContext(ctx, `
					UPDATE issue_occurrences
					SET fingerprint_scope_id = ?
					WHERE revision_id = ? AND fingerprint_scope_id IS NULL`,
					scope,
					row.revisionID,
				); err != nil {
					return errors.New("write recurrence occurrence backfill")
				}
			}
			return s.writeMigrationProgressTx(
				ctx, tx, "occurrence_scope", batch[len(batch)-1].rowID, false,
			)
		})
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			return err
		}
	}
}

func (s *Store) backfillAnalysisWatermarks(ctx context.Context) error {
	for {
		after, complete, err := s.migrationProgress(ctx, "analysis_watermark")
		if err != nil || complete {
			return err
		}
		rows, err := s.db.QueryContext(ctx, `
				SELECT rowid
				FROM session_analysis_revisions
				WHERE rowid > ?
				ORDER BY rowid
			LIMIT ?`,
			after,
			migrationBackfillBatchSize,
		)
		if err != nil {
			return errors.New("read recurrence analysis backfill")
		}
		var batch []migrationAnalysisRow
		for rows.Next() {
			var row migrationAnalysisRow
			if err := rows.Scan(&row.rowID); err != nil {
				rows.Close()
				return errors.New("read recurrence analysis backfill")
			}
			batch = append(batch, row)
		}
		if err := rows.Close(); err != nil {
			return errors.New("read recurrence analysis backfill")
		}
		if len(batch) == 0 {
			return s.completeMigrationPhase(ctx, "analysis_watermark", after)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return errors.New("begin recurrence analysis backfill")
		}
		err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
			// Migration 011 cannot prove which retained events each legacy
			// immutable revision actually analyzed. Preserve the schema
			// defaults (generation zero and a NULL watermark) so legacy
			// coverage remains unknown/incomplete until normal reanalysis
			// publishes a new revision with exact values.
			return s.writeMigrationProgressTx(
				ctx, tx, "analysis_watermark", batch[len(batch)-1].rowID, false,
			)
		})
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			return err
		}
	}
}

func (s *Store) backfillMonitoringSubjects(ctx context.Context) error {
	for {
		after, complete, err := s.migrationProgress(ctx, "monitoring_subject")
		if err != nil || complete {
			return err
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT
				fa.sequence, fa.annotation_id, fa.monitor_from, fa.origin,
				fa.detector_id, fa.detector_version, fa.fingerprint_version,
				io.fingerprint_scope_id, io.category, io.title_code,
				io.severity, io.confidence, io.harness
			FROM fix_annotations fa
			LEFT JOIN issue_occurrences io
				ON io.revision_id = fa.anchor_revision_id
			WHERE fa.sequence > ?
			ORDER BY fa.sequence
			LIMIT ?`,
			after,
			migrationBackfillBatchSize,
		)
		if err != nil {
			return errors.New("read recurrence subject backfill")
		}
		var batch []migrationSubjectRow
		for rows.Next() {
			var row migrationSubjectRow
			if err := rows.Scan(
				&row.sequence,
				&row.annotationID,
				&row.monitorFrom,
				&row.origin,
				&row.detectorID,
				&row.detectorVersion,
				&row.fingerprintVersion,
				&row.scopeID,
				&row.category,
				&row.titleCode,
				&row.severity,
				&row.confidence,
				&row.harness,
			); err != nil {
				rows.Close()
				return errors.New("read recurrence subject backfill")
			}
			batch = append(batch, row)
		}
		if err := rows.Close(); err != nil {
			return errors.New("read recurrence subject backfill")
		}
		if len(batch) == 0 {
			return s.completeMigrationPhase(ctx, "monitoring_subject", after)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return errors.New("begin recurrence subject backfill")
		}
		for _, row := range batch {
			mode := "supported"
			if row.origin == "numbat" {
				mode = "positive_only"
			}
			status := "unavailable"
			var scope any
			var order any
			if parsed, parseErr := time.Parse(time.RFC3339Nano, row.monitorFrom); parseErr == nil &&
				validProjectScopeID(row.scopeID.String) {
				normalizedOrder, valid := projectionOrderNS(parsed)
				if valid {
					status = "captured"
					scope = row.scopeID.String
					order = normalizedOrder
				}
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT OR IGNORE INTO fix_monitoring_subjects (
					annotation_id, fingerprint_scope_id, scope_capture_status,
					category, title_code, severity, confidence, anchor_harness,
					origin, detector_id, detector_version, fingerprint_version,
					negative_comparison_mode, monitor_from_order_ns, captured_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				row.annotationID,
				scope,
				status,
				nullableSQLString(row.category),
				nullableSQLString(row.titleCode),
				nullableSQLString(row.severity),
				nullableSQLString(row.confidence),
				nullableSQLString(row.harness),
				row.origin,
				row.detectorID,
				row.detectorVersion,
				row.fingerprintVersion,
				mode,
				order,
				formatProjectionTime(s.nowUTC()),
			); err != nil {
				_ = tx.Rollback()
				return errors.New("write recurrence subject backfill")
			}
		}
		if err := s.writeMigrationProgressTx(
			ctx, tx, "monitoring_subject", batch[len(batch)-1].sequence, false,
		); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return errors.New("commit recurrence subject backfill")
		}
	}
}

func (s *Store) migrationProgress(
	ctx context.Context,
	phase string,
) (int64, bool, error) {
	var after int64
	var complete int
	err := s.db.QueryRowContext(ctx, `
		SELECT after_sequence, complete
		FROM local_migration_progress
		WHERE migration_version = ? AND phase = ?`,
		fixRecurrenceMigrationVersion,
		phase,
	).Scan(&after, &complete)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, errors.New("read recurrence migration progress")
	}
	return after, complete == 1, nil
}

func (s *Store) completeMigrationPhase(
	ctx context.Context,
	phase string,
	after int64,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin recurrence migration completion")
	}
	defer tx.Rollback()
	if err := s.writeMigrationProgressTx(ctx, tx, phase, after, true); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit recurrence migration completion")
	}
	return nil
}

func (s *Store) writeMigrationProgressTx(
	ctx context.Context,
	tx *sql.Tx,
	phase string,
	after int64,
	complete bool,
) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO local_migration_progress (
			migration_version, phase, after_sequence, complete, updated_at
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(migration_version, phase) DO UPDATE SET
			after_sequence = MAX(after_sequence, excluded.after_sequence),
			complete = MAX(complete, excluded.complete),
			updated_at = excluded.updated_at`,
		fixRecurrenceMigrationVersion,
		phase,
		after,
		boolInt(complete),
		formatProjectionTime(s.nowUTC()),
	); err != nil {
		return errors.New("write recurrence migration progress")
	}
	return nil
}

func validProjectScopeID(value string) bool {
	return len(value) == 56 && len(value) > 4 && value[:4] == "psc_" &&
		opaqueIdentityPattern.MatchString(value)
}

func nullableSQLString(value sql.NullString) any {
	if value.Valid && value.String != "" {
		return value.String
	}
	return nil
}
