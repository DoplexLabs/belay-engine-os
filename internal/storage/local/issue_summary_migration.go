package local

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"sort"
)

const issueSummaryMigrationVersion = 12

var errIssueSummaryGenerationDrift = errors.New("issue summary generation drift")

func (s *Store) resumeIssueSummaryMigration(ctx context.Context) error {
	var applied int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		issueSummaryMigrationVersion,
	).Scan(&applied); err != nil {
		return errors.New("inspect issue summary migration")
	}
	if applied == 0 {
		return nil
	}
	if err := s.ensureIssueSummaryRuntimeIndexes(ctx); err != nil {
		return err
	}
	if err := s.ensurePersistedIssueCursorEpoch(ctx); err != nil {
		return err
	}
	for attempts := 0; attempts < 3; attempts++ {
		current, build, materialized, readiness, err := s.issueSummaryBuildState(ctx)
		if err != nil {
			return err
		}
		if readiness == "ready" && materialized == current {
			return s.verifyIssueSummaryReadiness(ctx)
		}
		prepared, err := s.issueSummaryPhaseComplete(ctx, "prepare")
		if err != nil {
			return err
		}
		if !prepared || build != current || readiness == "ready" {
			if err := s.resetIssueSummaryBuild(ctx, current); err != nil {
				return err
			}
		}
		if err := s.materializeCurrentIssueSummaries(ctx); err != nil {
			return err
		}
		if err := s.materializeCurrentIssueCoverage(ctx); err != nil {
			return err
		}
		if err := s.finishIssueSummaryBuild(ctx); err != nil {
			if errors.Is(err, errIssueSummaryGenerationDrift) {
				continue
			}
			return err
		}
		return s.verifyIssueSummaryReadiness(ctx)
	}
	return errors.New("issue summary generation changed during startup")
}

func (s *Store) ensureIssueSummaryRuntimeIndexes(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS issue_summary_order_snapshot_idx
		ON issue_summary_revisions(
			severity_rank DESC, repeated DESC, last_observed_at DESC, issue_id,
			visible_from_generation, visible_until_generation
		)`); err != nil {
		return errors.New("ensure issue summary runtime indexes")
	}
	return nil
}

func (s *Store) ensurePersistedIssueCursorEpoch(ctx context.Context) error {
	var epoch sql.NullString
	if err := s.db.QueryRowContext(ctx, `
		SELECT cursor_epoch FROM issue_summary_metadata WHERE singleton = 1`,
	).Scan(&epoch); err != nil {
		return errors.New("read issue cursor epoch")
	}
	if epoch.Valid && epoch.String != "" {
		return nil
	}
	value, err := newIssueCursorEpoch(s.random)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin issue cursor epoch persistence")
	}
	defer tx.Rollback()
	if err := withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		_, err := tx.ExecContext(ctx, `
			UPDATE issue_summary_metadata
			SET cursor_epoch = ?, updated_at = ?
			WHERE singleton = 1 AND (cursor_epoch IS NULL OR cursor_epoch = '')`,
			value,
			formatProjectionTime(s.nowUTC()),
		)
		return err
	}); err != nil {
		return errors.New("persist issue cursor epoch")
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit issue cursor epoch persistence")
	}
	return nil
}

func newIssueCursorEpoch(randomSource io.Reader) (string, error) {
	random := make([]byte, 32)
	if _, err := io.ReadFull(randomSource, random); err != nil {
		return "", errors.New("generate issue cursor epoch")
	}
	value := base64.RawURLEncoding.EncodeToString(random)
	zeroBytes(random)
	return value, nil
}

func (s *Store) issueSummaryBuildState(
	ctx context.Context,
) (current, build, materialized int64, readiness string, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT ipm.current_generation, ism.build_generation,
			ism.materialized_generation, ism.readiness
		FROM issue_projection_metadata ipm
		JOIN issue_summary_metadata ism ON ism.singleton = ipm.singleton
		WHERE ipm.singleton = 1`,
	).Scan(&current, &build, &materialized, &readiness)
	if err != nil {
		err = errors.New("read issue summary build state")
	}
	return
}

func (s *Store) resetIssueSummaryBuild(ctx context.Context, generation int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin issue summary build reset")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		var current int64
		if err := tx.QueryRowContext(ctx, `
			SELECT current_generation
			FROM issue_projection_metadata WHERE singleton = 1`,
		).Scan(&current); err != nil {
			return errors.New("read issue summary reset generation")
		}
		if current != generation {
			return errIssueSummaryGenerationDrift
		}
		for _, statement := range []string{
			"DELETE FROM issue_summary_harnesses",
			"DELETE FROM issue_summary_sessions",
			"DELETE FROM issue_summary_revisions",
			"DELETE FROM issue_analysis_coverage_revisions",
			"DELETE FROM issue_projection_generation_times",
			"DELETE FROM local_migration_progress WHERE migration_version = 12",
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return errors.New("reset issue summary materialization")
			}
		}
		now := formatProjectionTime(s.nowUTC())
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO issue_projection_generation_times (generation, generated_at)
			VALUES (?, ?)`,
			generation, now,
		); err != nil {
			return errors.New("initialize issue generation timestamp")
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE issue_summary_metadata
			SET readiness = 'building', build_generation = ?,
				materialized_generation = 0,
				oldest_materialized_generation = 0, updated_at = ?
			WHERE singleton = 1`,
			generation, now,
		); err != nil {
			return errors.New("initialize issue summary build")
		}
		return s.writeIssueSummaryProgressTx(ctx, tx, "prepare", 0, true)
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit issue summary build reset")
	}
	return nil
}

func (s *Store) materializeCurrentIssueSummaries(ctx context.Context) error {
	for {
		after, complete, err := s.issueSummaryProgress(ctx, "summaries")
		if err != nil || complete {
			return err
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT rowid, issue_id
			FROM issue_occurrences
			WHERE visible_until_generation IS NULL AND rowid > ?
			ORDER BY rowid
			LIMIT ?`,
			after, migrationBackfillBatchSize,
		)
		if err != nil {
			return errors.New("read issue summary migration batch")
		}
		var last int64
		issueSet := make(map[string]struct{})
		for rows.Next() {
			var issueID string
			if err := rows.Scan(&last, &issueID); err != nil {
				rows.Close()
				return errors.New("decode issue summary migration batch")
			}
			issueSet[issueID] = struct{}{}
		}
		if err := rows.Close(); err != nil {
			return errors.New("close issue summary migration batch")
		}
		if last == 0 {
			return s.completeIssueSummaryPhase(ctx, "summaries", after)
		}
		issueIDs := make([]string, 0, len(issueSet))
		for issueID := range issueSet {
			issueIDs = append(issueIDs, issueID)
		}
		sort.Strings(issueIDs)
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return errors.New("begin issue summary migration batch")
		}
		err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
			var generation int64
			if err := tx.QueryRowContext(ctx, `
				SELECT build_generation FROM issue_summary_metadata
				WHERE singleton = 1 AND readiness = 'building'`,
			).Scan(&generation); err != nil {
				return errors.New("read issue summary build generation")
			}
			for _, issueID := range issueIDs {
				if err := materializeCurrentIssueSummaryTx(
					ctx, tx, issueID, generation, formatProjectionTime(s.nowUTC()),
				); err != nil {
					return err
				}
			}
			return s.writeIssueSummaryProgressTx(ctx, tx, "summaries", last, false)
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

func materializeCurrentIssueSummaryTx(
	ctx context.Context,
	tx *sql.Tx,
	issueID string,
	generation int64,
	now string,
) error {
	return refreshIssueSummaryRevisionTx(ctx, tx, issueID, generation, now)
}

func (s *Store) materializeCurrentIssueCoverage(ctx context.Context) error {
	complete, err := s.issueSummaryPhaseComplete(ctx, "coverage")
	if err != nil || complete {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin issue coverage migration")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		var generation int64
		if err := tx.QueryRowContext(ctx, `
			SELECT build_generation FROM issue_summary_metadata
			WHERE singleton = 1 AND readiness = 'building'`,
		).Scan(&generation); err != nil {
			return errors.New("read issue coverage generation")
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM issue_analysis_coverage_revisions",
		); err != nil {
			return errors.New("reset current issue coverage")
		}
		now := formatProjectionTime(s.nowUTC())
		coverage, err := aggregateCurrentIssueCoverageTx(ctx, tx)
		if err != nil {
			return err
		}
		if err := insertIssueCoverageRevisionTx(
			ctx, tx, coverage, generation, now,
		); err != nil {
			return err
		}
		return s.writeIssueSummaryProgressTx(ctx, tx, "coverage", 0, true)
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit issue coverage migration")
	}
	return nil
}

func (s *Store) finishIssueSummaryBuild(ctx context.Context) error {
	ready, err := s.issueSummaryPhaseComplete(ctx, "ready")
	if err != nil {
		return err
	}
	if ready {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin issue summary readiness")
	}
	defer tx.Rollback()
	err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		var current, build int64
		if err := tx.QueryRowContext(ctx, `
			SELECT ipm.current_generation, ism.build_generation
			FROM issue_projection_metadata ipm
			JOIN issue_summary_metadata ism ON ism.singleton = ipm.singleton
			WHERE ipm.singleton = 1`,
		).Scan(&current, &build); err != nil {
			return errors.New("read issue summary readiness generation")
		}
		if current != build {
			return errIssueSummaryGenerationDrift
		}
		now := formatProjectionTime(s.nowUTC())
		if _, err := tx.ExecContext(ctx, `
			UPDATE issue_summary_metadata
			SET readiness = 'ready', materialized_generation = ?,
				oldest_materialized_generation = ?, updated_at = ?
			WHERE singleton = 1`,
			current, current, now,
		); err != nil {
			return errors.New("mark issue summary ready")
		}
		return s.writeIssueSummaryProgressTx(ctx, tx, "ready", 0, true)
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit issue summary readiness")
	}
	return nil
}

func (s *Store) verifyIssueSummaryReadiness(ctx context.Context) error {
	var current, materialized int64
	var epoch, readiness string
	if err := s.db.QueryRowContext(ctx, `
		SELECT ipm.current_generation, ism.materialized_generation,
			COALESCE(ism.cursor_epoch, ''), ism.readiness
		FROM issue_projection_metadata ipm
		JOIN issue_summary_metadata ism ON ism.singleton = ipm.singleton
		WHERE ipm.singleton = 1`,
	).Scan(&current, &materialized, &epoch, &readiness); err != nil {
		return errors.New("verify issue summary readiness")
	}
	if epoch == "" || readiness != "ready" || materialized != current {
		return errors.New("issue summary projection is not ready")
	}
	return nil
}

func (s *Store) issueSummaryPhaseComplete(ctx context.Context, phase string) (bool, error) {
	_, complete, err := s.issueSummaryProgress(ctx, phase)
	return complete, err
}

func (s *Store) issueSummaryProgress(
	ctx context.Context,
	phase string,
) (int64, bool, error) {
	var after int64
	var complete int
	err := s.db.QueryRowContext(ctx, `
		SELECT after_sequence, complete
		FROM local_migration_progress
		WHERE migration_version = ? AND phase = ?`,
		issueSummaryMigrationVersion, phase,
	).Scan(&after, &complete)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, errors.New("read issue summary migration progress")
	}
	return after, complete == 1, nil
}

func (s *Store) completeIssueSummaryPhase(
	ctx context.Context,
	phase string,
	after int64,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("begin issue summary phase completion")
	}
	defer tx.Rollback()
	if err := withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		return s.writeIssueSummaryProgressTx(ctx, tx, phase, after, true)
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.New("commit issue summary phase completion")
	}
	return nil
}

func (s *Store) writeIssueSummaryProgressTx(
	ctx context.Context,
	tx *sql.Tx,
	phase string,
	after int64,
	complete bool,
) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO local_migration_progress (
			migration_version, phase, after_sequence, complete, updated_at
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(migration_version, phase) DO UPDATE SET
			after_sequence = excluded.after_sequence,
			complete = excluded.complete,
			updated_at = excluded.updated_at`,
		issueSummaryMigrationVersion, phase, after, boolInt(complete),
		formatProjectionTime(s.nowUTC()),
	)
	if err != nil {
		return errors.New("write issue summary migration progress")
	}
	return nil
}
