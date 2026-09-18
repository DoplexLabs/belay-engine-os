package local

import (
	"context"
	"database/sql"
	"errors"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

const valueFirstMigrationVersion = 13

type valueFirstOccurrenceBackfill struct {
	rowID      int64
	revisionID string
	origin     string
	ruleID     string
}

func (s *Store) resumeValueFirstMigration(ctx context.Context) error {
	var applied int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		valueFirstMigrationVersion,
	).Scan(&applied); err != nil {
		return errors.New("inspect value-first migration")
	}
	if applied == 0 {
		return s.resumeIssueSummaryMigration(ctx)
	}
	if err := s.ensureValueFirstColumns(ctx); err != nil {
		return err
	}
	ready, err := s.valueFirstPhaseComplete(ctx, "ready")
	if err != nil {
		return err
	}
	if ready {
		repair, err := s.valueFirstRepairRequired(ctx)
		if err != nil {
			return err
		}
		if !repair {
			return s.resumeIssueSummaryMigration(ctx)
		}
		if err := s.resetValueFirstProgress(ctx); err != nil {
			return err
		}
	}
	prepared, err := s.valueFirstPhaseComplete(ctx, "prepare")
	if err != nil {
		return err
	}
	if !prepared {
		if err := s.prepareValueFirstMigration(ctx); err != nil {
			return err
		}
	}
	if err := s.backfillValueFirstOccurrences(ctx); err != nil {
		return err
	}
	if err := s.resumeIssueSummaryMigration(ctx); err != nil {
		return err
	}
	if err := s.finishValueFirstMigration(ctx); err != nil {
		return err
	}
	return s.verifyIssueSummaryReadiness(ctx)
}

func (s *Store) ensureValueFirstColumns(ctx context.Context) error {
	for _, column := range []struct {
		table string
		name  string
		sql   string
	}{
		{
			table: "issue_occurrences",
			name:  "source_signal_code",
			sql: `ALTER TABLE issue_occurrences
				ADD COLUMN source_signal_code TEXT
				CHECK (
					source_signal_code IS NULL OR (
						length(source_signal_code) BETWEEN 1 AND 64
						AND source_signal_code = lower(source_signal_code)
						AND substr(source_signal_code, 1, 1) GLOB '[a-z0-9]'
						AND source_signal_code NOT GLOB '*[^a-z0-9_.-]*'
					)
				)`,
		},
		{
			table: "issue_summary_revisions",
			name:  "source_signal_code",
			sql: `ALTER TABLE issue_summary_revisions
				ADD COLUMN source_signal_code TEXT
				CHECK (
					source_signal_code IS NULL OR (
						length(source_signal_code) BETWEEN 1 AND 64
						AND source_signal_code = lower(source_signal_code)
						AND substr(source_signal_code, 1, 1) GLOB '[a-z0-9]'
						AND source_signal_code NOT GLOB '*[^a-z0-9_.-]*'
					)
				)`,
		},
	} {
		present, err := sqliteColumnExists(ctx, s.db, column.table, column.name)
		if err != nil {
			return err
		}
		if present {
			continue
		}
		if _, err := s.db.ExecContext(ctx, column.sql); err != nil {
			return errors.New("repair value-first migration columns")
		}
	}
	return nil
}

func sqliteColumnExists(
	ctx context.Context,
	db *sql.DB,
	table string,
	column string,
) (bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false, errors.New("inspect migration table columns")
	}
	defer rows.Close()
	for rows.Next() {
		var sequence, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(
			&sequence,
			&name,
			&columnType,
			&notNull,
			&defaultValue,
			&primaryKey,
		); err != nil {
			return false, errors.New("decode migration table columns")
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, errors.New("inspect migration table columns")
	}
	return false, nil
}

func (s *Store) prepareValueFirstMigration(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin value-first migration preparation")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		var current int64
		if err := tx.QueryRowContext(ctx, `
			SELECT current_generation FROM issue_projection_metadata
			WHERE singleton = 1`,
		).Scan(&current); err != nil {
			return errors.New("read value-first migration generation")
		}
		for _, statement := range []string{
			"DELETE FROM issue_summary_harnesses",
			"DELETE FROM issue_summary_sessions",
			"DELETE FROM issue_summary_revisions",
			"DELETE FROM issue_analysis_coverage_revisions",
			"DELETE FROM issue_projection_generation_times",
			"DELETE FROM local_migration_progress WHERE migration_version IN (12, 13)",
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return errors.New("reset value-first materialization")
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE issue_summary_metadata
			SET readiness = 'building', build_generation = ?,
				materialized_generation = 0,
				oldest_materialized_generation = 0,
				updated_at = ?
			WHERE singleton = 1`,
			current,
			formatProjectionTime(s.nowUTC()),
		); err != nil {
			return errors.New("mark value-first materialization building")
		}
		return s.writeValueFirstProgressTx(ctx, tx, "prepare", 0, true)
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit value-first migration preparation")
	}
	return nil
}

func (s *Store) backfillValueFirstOccurrences(ctx context.Context) error {
	for {
		after, complete, err := s.valueFirstProgress(ctx, "occurrences")
		if err != nil || complete {
			return err
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT io.rowid, io.revision_id, io.origin, COALESCE(f.rule_id, '')
			FROM issue_occurrences io
			LEFT JOIN findings f
				ON io.origin = 'numbat' AND f.finding_id = io.origin_record_id
			WHERE io.rowid > ?
			ORDER BY io.rowid
			LIMIT ?`,
			after,
			migrationBackfillBatchSize,
		)
		if err != nil {
			return errors.New("read value-first occurrence backfill")
		}
		var batch []valueFirstOccurrenceBackfill
		var last int64
		for rows.Next() {
			var row valueFirstOccurrenceBackfill
			if err := rows.Scan(&row.rowID, &row.revisionID, &row.origin, &row.ruleID); err != nil {
				rows.Close()
				return errors.New("decode value-first occurrence backfill")
			}
			last = row.rowID
			batch = append(batch, row)
		}
		if err := rows.Close(); err != nil {
			return errors.New("close value-first occurrence backfill")
		}
		if last == 0 {
			return s.completeValueFirstPhase(ctx, "occurrences", after)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return errors.New("begin value-first occurrence backfill")
		}
		err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
			for _, row := range batch {
				var code any
				if row.origin == "numbat" {
					if safe := model.SafeSourceSignalCode(row.ruleID); safe != nil {
						code = *safe
					}
				}
				if _, err := tx.ExecContext(ctx, `
					UPDATE issue_occurrences
					SET source_signal_code = ?
					WHERE revision_id = ?`,
					code,
					row.revisionID,
				); err != nil {
					return errors.New("backfill value-first occurrence")
				}
			}
			return s.writeValueFirstProgressTx(
				ctx,
				tx,
				"occurrences",
				last,
				false,
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

func (s *Store) finishValueFirstMigration(ctx context.Context) error {
	ready, err := s.valueFirstPhaseComplete(ctx, "ready")
	if err != nil || ready {
		return err
	}
	epoch, err := newIssueCursorEpoch(s.random)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin value-first migration completion")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		var current, build, materialized int64
		var readiness string
		if err := tx.QueryRowContext(ctx, `
			SELECT ipm.current_generation, ism.build_generation,
				ism.materialized_generation, ism.readiness
			FROM issue_projection_metadata ipm
			JOIN issue_summary_metadata ism ON ism.singleton = ipm.singleton
			WHERE ipm.singleton = 1`,
		).Scan(&current, &build, &materialized, &readiness); err != nil {
			return errors.New("verify value-first migration readiness")
		}
		if readiness != "ready" || current != build || current != materialized {
			return errors.New("value-first summary rebuild is not ready")
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE issue_summary_metadata
			SET cursor_epoch = ?, updated_at = ?
			WHERE singleton = 1`,
			epoch,
			formatProjectionTime(s.nowUTC()),
		); err != nil {
			return errors.New("rotate value-first issue cursor epoch")
		}
		return s.writeValueFirstProgressTx(ctx, tx, "ready", 0, true)
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit value-first migration completion")
	}
	return nil
}

func (s *Store) resetValueFirstProgress(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin value-first repair reset")
	}
	defer tx.Rollback()
	if err := withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		_, err := tx.ExecContext(ctx,
			"DELETE FROM local_migration_progress WHERE migration_version = ?",
			valueFirstMigrationVersion,
		)
		return err
	}); err != nil {
		return errors.New("reset value-first repair progress")
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit value-first repair reset")
	}
	return nil
}

func (s *Store) valueFirstRepairRequired(ctx context.Context) (bool, error) {
	var repair int
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM issue_occurrences io
			LEFT JOIN findings f
				ON io.origin = 'numbat' AND f.finding_id = io.origin_record_id
			WHERE io.source_signal_code IS NOT (
				CASE
					WHEN io.origin = 'numbat'
						AND f.rule_id IS NOT NULL
						AND length(f.rule_id) BETWEEN 1 AND 64
						AND f.rule_id = lower(f.rule_id)
						AND substr(f.rule_id, 1, 1) GLOB '[a-z0-9]'
						AND f.rule_id NOT GLOB '*[^a-z0-9_.-]*'
					THEN f.rule_id
					ELSE NULL
				END
			)
			LIMIT 1
		)`,
	).Scan(&repair); err != nil {
		return false, errors.New("inspect value-first occurrence repair")
	}
	if repair == 1 {
		return true, nil
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM issue_summary_revisions sr
			WHERE sr.visible_until_generation IS NULL
				AND COALESCE(sr.source_signal_code, '') <> COALESCE((
					SELECT CASE
						WHEN COUNT(io.source_signal_code) = COUNT(*)
							AND MIN(io.source_signal_code) = MAX(io.source_signal_code)
						THEN MIN(io.source_signal_code)
						ELSE NULL
					END
					FROM issue_occurrences io
					WHERE io.issue_id = sr.issue_id
						AND io.visible_until_generation IS NULL
				), '')
			LIMIT 1
		)`,
	).Scan(&repair); err != nil {
		return false, errors.New("inspect value-first summary repair")
	}
	return repair == 1, nil
}

func (s *Store) valueFirstPhaseComplete(ctx context.Context, phase string) (bool, error) {
	_, complete, err := s.valueFirstProgress(ctx, phase)
	return complete, err
}

func (s *Store) valueFirstProgress(
	ctx context.Context,
	phase string,
) (int64, bool, error) {
	var after int64
	var complete int
	err := s.db.QueryRowContext(ctx, `
		SELECT after_sequence, complete
		FROM local_migration_progress
		WHERE migration_version = ? AND phase = ?`,
		valueFirstMigrationVersion,
		phase,
	).Scan(&after, &complete)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, errors.New("read value-first migration progress")
	}
	return after, complete == 1, nil
}

func (s *Store) completeValueFirstPhase(
	ctx context.Context,
	phase string,
	after int64,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin value-first phase completion")
	}
	defer tx.Rollback()
	if err := withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		return s.writeValueFirstProgressTx(ctx, tx, phase, after, true)
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit value-first phase completion")
	}
	return nil
}

func (s *Store) writeValueFirstProgressTx(
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
			after_sequence = excluded.after_sequence,
			complete = excluded.complete,
			updated_at = excluded.updated_at`,
		valueFirstMigrationVersion,
		phase,
		after,
		boolInt(complete),
		formatProjectionTime(s.nowUTC()),
	); err != nil {
		return errors.New("write value-first migration progress")
	}
	return nil
}
