package local

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"

	sqlite "modernc.org/sqlite"
)

type mutationPurpose string

const (
	mutationPayloadUpgrade         mutationPurpose = "payload_upgrade"
	mutationRetentionPrune         mutationPurpose = "retention_prune"
	mutationProjectionRebuild      mutationPurpose = "projection_rebuild"
	mutationRecurrenceWorker       mutationPurpose = "recurrence_worker"
	mutationTranscriptIngestion    mutationPurpose = "transcript_ingestion"
	mutationTranscriptRetention    mutationPurpose = "transcript_retention"
	mutationCostIssueAnalysis      mutationPurpose = "cost_issue_analysis"
	mutationSemanticInsight        mutationPurpose = "semantic_insight"
	mutationCostIssueFix           mutationPurpose = "cost_issue_fix"
	mutationExperienceCandidate    mutationPurpose = "experience_candidate"
	mutationExperienceSemantic     mutationPurpose = "experience_semantic_proposal"
	mutationExperienceRegistry     mutationPurpose = "experience_registry"
	mutationTrajectory             mutationPurpose = "trajectory"
	mutationOutcome                mutationPurpose = "outcome"
	mutationExperienceApplication  mutationPurpose = "experience_application"
	mutationExperienceGeneration   mutationPurpose = "experience_generation"
	mutationExperienceReviewAction mutationPurpose = "experience_review_action"
	mutationMissionPackPreview     mutationPurpose = "mission_pack_preview"
	mutationMissionPackReceipt     mutationPurpose = "mission_pack_receipt"
	mutationExperienceImpact       mutationPurpose = "experience_impact"
	mutationTrajectoryDerivation   mutationPurpose = "trajectory_derivation"
	mutationHabitDebrief           mutationPurpose = "habit_debrief"
	guardedSQLiteDriverName                        = "belay_local_sqlite"
)

var registerGuardedSQLiteDriver sync.Once

func openGuardedSQLite(dsn string) (*sql.DB, error) {
	registerGuardedSQLiteDriver.Do(func() {
		sqliteDriver := &sqlite.Driver{}
		sqliteDriver.RegisterConnectionHook(initializeMutationConnection)
		sql.Register(guardedSQLiteDriverName, sqliteDriver)
	})
	return sql.Open(guardedSQLiteDriverName, dsn)
}

func initializeMutationConnection(
	connection sqlite.ExecQuerierContext,
	_ string,
) error {
	ctx := context.Background()
	if _, err := connection.ExecContext(ctx, "PRAGMA recursive_triggers = ON", nil); err != nil {
		return errors.New("enable recursive local mutation guards")
	}
	if _, err := connection.ExecContext(ctx, mutationAuthorizationTableSQL, nil); err != nil {
		return errors.New("initialize local mutation authorization")
	}
	ready, err := mutationTablesReady(ctx, connection)
	if err != nil {
		return err
	}
	if !ready {
		return nil
	}
	if _, err := connection.ExecContext(ctx, mutationTriggerSQL, nil); err != nil {
		return errors.New("install connection-local mutation guards")
	}
	fixReady, err := feature3MutationTablesReady(ctx, connection)
	if err != nil {
		return err
	}
	if fixReady {
		if _, err := connection.ExecContext(ctx, fixMutationTriggerSQL, nil); err != nil {
			return errors.New("install connection-local fix mutation guards")
		}
	}
	recurrenceReady, err := recurrenceMutationTablesReady(ctx, connection)
	if err != nil {
		return err
	}
	if recurrenceReady {
		if _, err := connection.ExecContext(ctx, recurrenceMutationTriggerSQL, nil); err != nil {
			return errors.New("install connection-local recurrence mutation guards")
		}
	}
	summaryReady, err := issueSummaryMutationTablesReady(ctx, connection)
	if err != nil {
		return err
	}
	if summaryReady {
		if _, err := connection.ExecContext(ctx, issueSummaryMutationTriggerSQL, nil); err != nil {
			return errors.New("install connection-local issue summary mutation guards")
		}
	}
	transcriptReady, err := transcriptMutationTablesReady(ctx, connection)
	if err != nil {
		return err
	}
	if transcriptReady {
		if _, err := connection.ExecContext(ctx, transcriptMutationTriggerSQL, nil); err != nil {
			return errors.New("install connection-local transcript mutation guards")
		}
	}
	costIssueReady, err := costIssueMutationTablesReady(ctx, connection)
	if err != nil {
		return err
	}
	if costIssueReady {
		if _, err := connection.ExecContext(ctx, costIssueMutationTriggerSQL, nil); err != nil {
			return errors.New("install connection-local cost issue mutation guards")
		}
	}
	insightReady, err := insightMutationTablesReady(ctx, connection)
	if err != nil {
		return err
	}
	if insightReady {
		if _, err := connection.ExecContext(ctx, insightMutationTriggerSQL, nil); err != nil {
			return errors.New("install connection-local insight mutation guards")
		}
	}
	experienceReady, err := experienceMutationTablesReady(ctx, connection)
	if err != nil {
		return err
	}
	if experienceReady {
		if _, err := connection.ExecContext(ctx, experienceMutationTriggerSQL, nil); err != nil {
			return errors.New("install connection-local experience mutation guards")
		}
	}
	impactReady, err := experienceImpactMutationTablesReady(ctx, connection)
	if err != nil {
		return err
	}
	if impactReady {
		if _, err := connection.ExecContext(
			ctx,
			experienceImpactMutationTriggerSQL,
			nil,
		); err != nil {
			return errors.New(
				"install connection-local experience impact mutation guards",
			)
		}
	}
	trajectoryDerivationReady, err := trajectoryDerivationMutationTablesReady(
		ctx,
		connection,
	)
	if err != nil {
		return err
	}
	if trajectoryDerivationReady {
		if _, err := connection.ExecContext(
			ctx,
			trajectoryDerivationMutationTriggerSQL,
			nil,
		); err != nil {
			return errors.New("install connection-local trajectory derivation mutation guards")
		}
	}
	habitDebriefReady, err := habitDebriefMutationTablesReady(ctx, connection)
	if err != nil {
		return err
	}
	if habitDebriefReady {
		if _, err := connection.ExecContext(
			ctx,
			habitDebriefMutationTriggerSQL,
			nil,
		); err != nil {
			return errors.New("install connection-local habit debrief mutation guards")
		}
	}
	return nil
}

func habitDebriefMutationTablesReady(
	ctx context.Context,
	connection driver.QueryerContext,
) (bool, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT COUNT(*)
		FROM main.sqlite_schema
		WHERE type = 'table'
			AND name = 'habit_debriefs'`,
		nil,
	)
	if err != nil {
		return false, errors.New("inspect habit debrief mutation schema")
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil {
		return false, errors.New("inspect habit debrief mutation schema")
	}
	count, ok := values[0].(int64)
	if !ok {
		return false, errors.New("inspect habit debrief mutation schema")
	}
	return count == 1, nil
}

func mutationTablesReady(
	ctx context.Context,
	connection driver.QueryerContext,
) (bool, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT COUNT(*)
		FROM main.sqlite_schema
		WHERE type = 'table'
			AND name IN (
				'events',
				'findings',
				'issue_occurrences',
				'session_analysis_revisions',
				'analysis_diagnostics'
			)`,
		nil,
	)
	if err != nil {
		return false, errors.New("inspect local mutation schema")
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil {
		if errors.Is(err, io.EOF) {
			return false, errors.New("inspect local mutation schema")
		}
		return false, errors.New("inspect local mutation schema")
	}
	count, ok := values[0].(int64)
	if !ok {
		return false, errors.New("inspect local mutation schema")
	}
	return count == 5, nil
}

func feature3MutationTablesReady(
	ctx context.Context,
	connection driver.QueryerContext,
) (bool, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT COUNT(*)
		FROM main.sqlite_schema
		WHERE type = 'table'
			AND name IN (
				'fix_annotations',
				'fix_annotation_retractions'
			)`,
		nil,
	)
	if err != nil {
		return false, errors.New("inspect local fix mutation schema")
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil {
		return false, errors.New("inspect local fix mutation schema")
	}
	count, ok := values[0].(int64)
	if !ok {
		return false, errors.New("inspect local fix mutation schema")
	}
	return count == 2, nil
}

func recurrenceMutationTablesReady(
	ctx context.Context,
	connection driver.QueryerContext,
) (bool, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT COUNT(*)
		FROM main.sqlite_schema
		WHERE type = 'table'
			AND name IN (
				'fix_monitoring_metadata',
				'fix_monitoring_subjects',
				'session_analysis_capabilities',
				'fix_recurrence_jobs',
				'fix_recurrence_job_events',
				'fix_recurrence_observations',
				'fix_recurrence_observation_events'
			)`,
		nil,
	)
	if err != nil {
		return false, errors.New("inspect local recurrence mutation schema")
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil {
		return false, errors.New("inspect local recurrence mutation schema")
	}
	count, ok := values[0].(int64)
	if !ok {
		return false, errors.New("inspect local recurrence mutation schema")
	}
	return count == 7, nil
}

func issueSummaryMutationTablesReady(
	ctx context.Context,
	connection driver.QueryerContext,
) (bool, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT COUNT(*)
		FROM main.sqlite_schema
		WHERE type = 'table'
			AND name IN (
				'issue_summary_metadata',
				'issue_projection_generation_times',
				'issue_summary_revisions',
				'issue_summary_harnesses',
				'issue_summary_sessions',
				'issue_analysis_coverage_revisions'
			)`,
		nil,
	)
	if err != nil {
		return false, errors.New("inspect local issue summary mutation schema")
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil {
		return false, errors.New("inspect local issue summary mutation schema")
	}
	count, ok := values[0].(int64)
	if !ok {
		return false, errors.New("inspect local issue summary mutation schema")
	}
	return count == 6, nil
}

func transcriptMutationTablesReady(
	ctx context.Context,
	connection driver.QueryerContext,
) (bool, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT COUNT(*)
		FROM main.sqlite_schema
		WHERE type = 'table'
			AND name IN ('transcript_sessions', 'transcript_turns')`,
		nil,
	)
	if err != nil {
		return false, errors.New("inspect local transcript mutation schema")
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil {
		return false, errors.New("inspect local transcript mutation schema")
	}
	count, ok := values[0].(int64)
	if !ok {
		return false, errors.New("inspect local transcript mutation schema")
	}
	return count == 2, nil
}

func costIssueMutationTablesReady(
	ctx context.Context,
	connection driver.QueryerContext,
) (bool, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT COUNT(*)
		FROM main.sqlite_schema
		WHERE type = 'table'
			AND name IN (
				'transcript_project_analysis_state',
				'cost_issues',
				'correction_candidates',
				'project_issue_cost_totals'
			)`,
		nil,
	)
	if err != nil {
		return false, errors.New("inspect local cost issue mutation schema")
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil {
		return false, errors.New("inspect local cost issue mutation schema")
	}
	count, ok := values[0].(int64)
	if !ok {
		return false, errors.New("inspect local cost issue mutation schema")
	}
	return count == 4, nil
}

func insightMutationTablesReady(
	ctx context.Context,
	connection driver.QueryerContext,
) (bool, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT COUNT(*)
		FROM main.sqlite_schema
		WHERE type = 'table'
			AND name IN ('insights', 'cost_issue_fixes')`,
		nil,
	)
	if err != nil {
		return false, errors.New("inspect local insight mutation schema")
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil {
		return false, errors.New("inspect local insight mutation schema")
	}
	count, ok := values[0].(int64)
	if !ok {
		return false, errors.New("inspect local insight mutation schema")
	}
	return count == 2, nil
}

func experienceMutationTablesReady(
	ctx context.Context,
	connection driver.QueryerContext,
) (bool, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT COUNT(*)
		FROM main.sqlite_schema
		WHERE type = 'table'
			AND name IN (
				'experience_candidates',
				'experience_semantic_proposals',
				'experience_semantic_decisions',
				'experiences',
				'experience_transitions',
				'experience_evidence',
				'trajectory_edges',
				'outcome_observations',
				'experience_applications',
				'experience_generations',
				'experience_review_actions',
				'mission_pack_previews',
				'mission_pack_receipts',
				'mission_pack_receipt_applications'
			)`,
		nil,
	)
	if err != nil {
		return false, errors.New("inspect local experience mutation schema")
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil {
		return false, errors.New("inspect local experience mutation schema")
	}
	count, ok := values[0].(int64)
	if !ok {
		return false, errors.New("inspect local experience mutation schema")
	}
	return count == 14, nil
}

func trajectoryDerivationMutationTablesReady(
	ctx context.Context,
	connection driver.QueryerContext,
) (bool, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT COUNT(*)
		FROM main.sqlite_schema
		WHERE type = 'table'
			AND name = 'trajectory_derivation_state'`,
		nil,
	)
	if err != nil {
		return false, errors.New("inspect trajectory derivation mutation schema")
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil {
		return false, errors.New("inspect trajectory derivation mutation schema")
	}
	count, ok := values[0].(int64)
	if !ok {
		return false, errors.New("inspect trajectory derivation mutation schema")
	}
	return count == 1, nil
}

func experienceImpactMutationTablesReady(
	ctx context.Context,
	connection driver.QueryerContext,
) (bool, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT COUNT(*)
		FROM main.sqlite_schema
		WHERE type = 'table'
			AND name = 'experience_impact_observations'`,
		nil,
	)
	if err != nil {
		return false, errors.New("inspect experience impact mutation schema")
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil {
		return false, errors.New("inspect experience impact mutation schema")
	}
	count, ok := values[0].(int64)
	if !ok {
		return false, errors.New("inspect experience impact mutation schema")
	}
	return count == 1, nil
}

func (s *Store) installMutationGuards(ctx context.Context) error {
	connection, err := s.db.Conn(ctx)
	if err != nil {
		return errors.New("acquire local mutation connection")
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, mutationAuthorizationTableSQL); err != nil {
		return errors.New("initialize local mutation authorization")
	}
	if _, err := connection.ExecContext(ctx, mutationTriggerSQL); err != nil {
		return errors.New("install connection-local mutation guards")
	}
	if _, err := connection.ExecContext(ctx, fixMutationTriggerSQL); err != nil {
		return errors.New("install connection-local fix mutation guards")
	}
	if _, err := connection.ExecContext(ctx, recurrenceMutationTriggerSQL); err != nil {
		return errors.New("install connection-local recurrence mutation guards")
	}
	if _, err := connection.ExecContext(ctx, issueSummaryMutationTriggerSQL); err != nil {
		return errors.New("install connection-local issue summary mutation guards")
	}
	if _, err := connection.ExecContext(ctx, transcriptMutationTriggerSQL); err != nil {
		return errors.New("install connection-local transcript mutation guards")
	}
	if _, err := connection.ExecContext(ctx, costIssueMutationTriggerSQL); err != nil {
		return errors.New("install connection-local cost issue mutation guards")
	}
	if _, err := connection.ExecContext(ctx, insightMutationTriggerSQL); err != nil {
		return errors.New("install connection-local insight mutation guards")
	}
	if _, err := connection.ExecContext(ctx, experienceMutationTriggerSQL); err != nil {
		return errors.New("install connection-local experience mutation guards")
	}
	if _, err := connection.ExecContext(
		ctx,
		experienceImpactMutationTriggerSQL,
	); err != nil {
		return errors.New(
			"install connection-local experience impact mutation guards",
		)
	}
	if _, err := connection.ExecContext(
		ctx,
		trajectoryDerivationMutationTriggerSQL,
	); err != nil {
		return errors.New("install connection-local trajectory derivation mutation guards")
	}
	if _, err := connection.ExecContext(ctx, habitDebriefMutationTriggerSQL); err != nil {
		return errors.New("install connection-local habit debrief mutation guards")
	}
	return nil
}

func withMutationTx(
	ctx context.Context,
	tx *sql.Tx,
	purpose mutationPurpose,
	operation func() error,
) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO temp.belay_mutation_authorization (purpose)
		VALUES (?)`,
		purpose,
	); err != nil {
		return errors.New("activate local mutation authorization")
	}
	operationErr := operation()
	_, clearErr := tx.ExecContext(ctx, `
		DELETE FROM temp.belay_mutation_authorization
		WHERE purpose = ?`,
		purpose,
	)
	if operationErr != nil {
		return operationErr
	}
	if clearErr != nil {
		return errors.New("clear local mutation authorization")
	}
	return nil
}

const mutationAuthorizationTableSQL = `
	CREATE TEMP TABLE IF NOT EXISTS belay_mutation_authorization (
		purpose TEXT PRIMARY KEY
			CHECK (
				purpose IN (
					'payload_upgrade',
					'retention_prune',
					'projection_rebuild',
					'recurrence_worker',
					'transcript_ingestion',
					'transcript_retention',
					'cost_issue_analysis',
					'semantic_insight',
					'cost_issue_fix',
					'experience_candidate',
					'experience_semantic_proposal',
					'experience_registry',
					'trajectory',
					'outcome',
					'experience_application',
					'experience_generation',
					'experience_review_action',
					'mission_pack_preview',
					'mission_pack_receipt',
					'experience_impact',
					'trajectory_derivation',
					'habit_debrief'
				)
			)
	) WITHOUT ROWID;
	DELETE FROM temp.belay_mutation_authorization;`

const mutationTriggerSQL = `
	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_events_update
	BEFORE UPDATE ON main.events
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'payload_upgrade'
	)
	BEGIN
		SELECT RAISE(ABORT, 'canonical events are append-only');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_events_delete
	BEFORE DELETE ON main.events
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'retention_prune'
	)
	BEGIN
		SELECT RAISE(ABORT, 'canonical events are append-only');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_findings_update
	BEFORE UPDATE ON main.findings
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'payload_upgrade'
	)
	BEGIN
		SELECT RAISE(ABORT, 'findings are append-only');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_findings_delete
	BEFORE DELETE ON main.findings
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'retention_prune'
	)
	BEGIN
		SELECT RAISE(ABORT, 'findings are append-only');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_issue_occurrences_update
	BEFORE UPDATE ON main.issue_occurrences
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN (
			'projection_rebuild',
			'payload_upgrade',
			'retention_prune'
		)
	)
	BEGIN
		SELECT RAISE(ABORT, 'issue occurrence mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_issue_occurrences_delete
	BEFORE DELETE ON main.issue_occurrences
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('projection_rebuild', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'issue occurrence mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_session_analysis_revisions_update
	BEFORE UPDATE ON main.session_analysis_revisions
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('projection_rebuild', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'analysis revision mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_session_analysis_revisions_delete
	BEFORE DELETE ON main.session_analysis_revisions
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('projection_rebuild', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'analysis revision mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_analysis_diagnostics_update
	BEFORE UPDATE ON main.analysis_diagnostics
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('projection_rebuild', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'analysis diagnostic mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_analysis_diagnostics_delete
	BEFORE DELETE ON main.analysis_diagnostics
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('projection_rebuild', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'analysis diagnostic mutation is not authorized');
	END;`

const transcriptMutationTriggerSQL = `
	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_transcript_turns_update
	BEFORE UPDATE ON main.transcript_turns
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('transcript_ingestion', 'transcript_retention')
	)
	BEGIN
		SELECT RAISE(ABORT, 'transcript turn mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_transcript_turns_delete
	BEFORE DELETE ON main.transcript_turns
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('transcript_ingestion', 'transcript_retention')
	)
	BEGIN
		SELECT RAISE(ABORT, 'transcript turn deletion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_transcript_sessions_update
	BEFORE UPDATE ON main.transcript_sessions
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('transcript_ingestion', 'transcript_retention')
	)
	BEGIN
		SELECT RAISE(ABORT, 'transcript session mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_transcript_sessions_delete
	BEFORE DELETE ON main.transcript_sessions
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('transcript_ingestion', 'transcript_retention')
	)
	BEGIN
		SELECT RAISE(ABORT, 'transcript session deletion is not authorized');
	END;`

const costIssueMutationTriggerSQL = `
	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_cost_issues_insert
	BEFORE INSERT ON main.cost_issues
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'cost_issue_analysis'
	)
	BEGIN
		SELECT RAISE(ABORT, 'cost issue insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_cost_issues_update
	BEFORE UPDATE ON main.cost_issues
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'cost_issue_analysis'
	)
	BEGIN
		SELECT RAISE(ABORT, 'cost issue mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_cost_issues_delete
	BEFORE DELETE ON main.cost_issues
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('cost_issue_analysis', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'cost issue deletion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_correction_candidates_insert
	BEFORE INSERT ON main.correction_candidates
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'cost_issue_analysis'
	)
	BEGIN
		SELECT RAISE(ABORT, 'correction candidate insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_correction_candidates_update
	BEFORE UPDATE ON main.correction_candidates
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'cost_issue_analysis'
	)
	BEGIN
		SELECT RAISE(ABORT, 'correction candidate mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_correction_candidates_delete
	BEFORE DELETE ON main.correction_candidates
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('cost_issue_analysis', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'correction candidate deletion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_project_issue_cost_totals_insert
	BEFORE INSERT ON main.project_issue_cost_totals
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'cost_issue_analysis'
	)
	BEGIN
		SELECT RAISE(ABORT, 'project issue cost insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_project_issue_cost_totals_update
	BEFORE UPDATE ON main.project_issue_cost_totals
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'cost_issue_analysis'
	)
	BEGIN
		SELECT RAISE(ABORT, 'project issue cost mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_project_issue_cost_totals_delete
	BEFORE DELETE ON main.project_issue_cost_totals
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('cost_issue_analysis', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'project issue cost deletion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_transcript_project_state_insert
	BEFORE INSERT ON main.transcript_project_analysis_state
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('transcript_ingestion', 'transcript_retention')
	)
	BEGIN
		SELECT RAISE(ABORT, 'transcript project state insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_transcript_project_state_update
	BEFORE UPDATE ON main.transcript_project_analysis_state
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN (
			'transcript_ingestion',
			'transcript_retention',
			'cost_issue_analysis'
		)
	)
	BEGIN
		SELECT RAISE(ABORT, 'transcript project state mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_transcript_project_state_delete
	BEFORE DELETE ON main.transcript_project_analysis_state
	BEGIN
		SELECT RAISE(ABORT, 'transcript project state is durable');
	END;`

const insightMutationTriggerSQL = `
	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_insights_insert
	BEFORE INSERT ON main.insights
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'semantic_insight'
	)
	BEGIN
		SELECT RAISE(ABORT, 'insight insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_insights_update
	BEFORE UPDATE ON main.insights
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'semantic_insight'
	)
	BEGIN
		SELECT RAISE(ABORT, 'insight mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_insights_delete
	BEFORE DELETE ON main.insights
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('semantic_insight', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'insight deletion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_cost_issue_fixes_insert
	BEFORE INSERT ON main.cost_issue_fixes
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'cost_issue_fix'
	)
	BEGIN
		SELECT RAISE(ABORT, 'cost issue fix insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_cost_issue_fixes_update
	BEFORE UPDATE ON main.cost_issue_fixes
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'cost_issue_fix'
	)
	BEGIN
		SELECT RAISE(ABORT, 'cost issue fix mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_cost_issue_fixes_delete
	BEFORE DELETE ON main.cost_issue_fixes
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'retention_prune'
	)
	BEGIN
		SELECT RAISE(ABORT, 'cost issue fix deletion is not authorized');
	END;`

const experienceMutationTriggerSQL = `
	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_candidates_insert
	BEFORE INSERT ON main.experience_candidates
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'experience_candidate'
	)
	BEGIN
		SELECT RAISE(ABORT, 'experience candidate insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_candidates_update
	BEFORE UPDATE ON main.experience_candidates
	BEGIN
		SELECT RAISE(ABORT, 'experience candidates are immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_candidates_delete
	BEFORE DELETE ON main.experience_candidates
	BEGIN
		SELECT RAISE(ABORT, 'experience candidates are durable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_semantic_proposals_insert
	BEFORE INSERT ON main.experience_semantic_proposals
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'experience_semantic_proposal'
	)
	BEGIN
		SELECT RAISE(ABORT, 'experience semantic proposal insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_semantic_proposals_update
	BEFORE UPDATE ON main.experience_semantic_proposals
	BEGIN
		SELECT RAISE(ABORT, 'experience semantic proposals are immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_semantic_proposals_delete
	BEFORE DELETE ON main.experience_semantic_proposals
	BEGIN
		SELECT RAISE(ABORT, 'experience semantic proposals are durable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_semantic_decisions_insert
	BEFORE INSERT ON main.experience_semantic_decisions
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'experience_semantic_proposal'
	)
	BEGIN
		SELECT RAISE(ABORT, 'experience semantic decision insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_semantic_decisions_update
	BEFORE UPDATE ON main.experience_semantic_decisions
	BEGIN
		SELECT RAISE(ABORT, 'experience semantic decisions are immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_semantic_decisions_delete
	BEFORE DELETE ON main.experience_semantic_decisions
	BEGIN
		SELECT RAISE(ABORT, 'experience semantic decisions are durable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_review_actions_insert
	BEFORE INSERT ON main.experience_review_actions
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'experience_review_action'
	)
	BEGIN
		SELECT RAISE(ABORT, 'experience review action insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_review_actions_update
	BEFORE UPDATE ON main.experience_review_actions
	BEGIN
		SELECT RAISE(ABORT, 'experience review actions are immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_review_actions_delete
	BEFORE DELETE ON main.experience_review_actions
	BEGIN
		SELECT RAISE(ABORT, 'experience review actions are durable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experiences_insert
	BEFORE INSERT ON main.experiences
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'experience_registry'
	)
	BEGIN
		SELECT RAISE(ABORT, 'experience insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experiences_update
	BEFORE UPDATE ON main.experiences
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'experience_registry'
	)
	BEGIN
		SELECT RAISE(ABORT, 'experience projection mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experiences_immutable_update
	BEFORE UPDATE ON main.experiences
	WHEN
		NEW.experience_id IS NOT OLD.experience_id
		OR NEW.version IS NOT OLD.version
		OR NEW.origin_candidate_id IS NOT OLD.origin_candidate_id
		OR NEW.project_identity IS NOT OLD.project_identity
		OR NEW.experience_type IS NOT OLD.experience_type
		OR NEW.initial_lifecycle_state IS NOT OLD.initial_lifecycle_state
		OR NEW.intervention_strength IS NOT OLD.intervention_strength
		OR NEW.content_hash IS NOT OLD.content_hash
		OR NEW.previous_experience_id IS NOT OLD.previous_experience_id
		OR NEW.previous_version IS NOT OLD.previous_version
		OR NEW.approved_at IS NOT OLD.approved_at
		OR NEW.expires_at IS NOT OLD.expires_at
		OR NEW.created_at IS NOT OLD.created_at
		OR NEW.payload IS NOT OLD.payload
		OR NEW.payload_encoding IS NOT OLD.payload_encoding
	BEGIN
		SELECT RAISE(ABORT, 'experience versions are immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experiences_delete
	BEFORE DELETE ON main.experiences
	BEGIN
		SELECT RAISE(ABORT, 'experience versions are immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_transitions_insert
	BEFORE INSERT ON main.experience_transitions
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'experience_registry'
	)
	BEGIN
		SELECT RAISE(ABORT, 'experience transition insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_transitions_update
	BEFORE UPDATE ON main.experience_transitions
	BEGIN
		SELECT RAISE(ABORT, 'experience transitions are append-only');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_transitions_delete
	BEFORE DELETE ON main.experience_transitions
	BEGIN
		SELECT RAISE(ABORT, 'experience transitions are append-only');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_evidence_insert
	BEFORE INSERT ON main.experience_evidence
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'experience_registry'
	)
	BEGIN
		SELECT RAISE(ABORT, 'experience evidence insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_evidence_update
	BEFORE UPDATE ON main.experience_evidence
	BEGIN
		SELECT RAISE(ABORT, 'experience evidence is immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_evidence_delete
	BEFORE DELETE ON main.experience_evidence
	BEGIN
		SELECT RAISE(ABORT, 'experience evidence is durable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_trajectory_edges_insert
	BEFORE INSERT ON main.trajectory_edges
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'trajectory'
	)
	BEGIN
		SELECT RAISE(ABORT, 'trajectory edge insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_trajectory_edges_update
	BEFORE UPDATE ON main.trajectory_edges
	BEGIN
		SELECT RAISE(ABORT, 'trajectory edges are immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_trajectory_edges_delete
	BEFORE DELETE ON main.trajectory_edges
	BEGIN
		SELECT RAISE(ABORT, 'trajectory edges are durable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_outcome_observations_insert
	BEFORE INSERT ON main.outcome_observations
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'outcome'
	)
	BEGIN
		SELECT RAISE(ABORT, 'outcome insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_outcome_observations_update
	BEFORE UPDATE ON main.outcome_observations
	BEGIN
		SELECT RAISE(ABORT, 'outcome observations are immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_outcome_observations_delete
	BEFORE DELETE ON main.outcome_observations
	BEGIN
		SELECT RAISE(ABORT, 'outcome observations are durable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_applications_insert
	BEFORE INSERT ON main.experience_applications
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'experience_application'
	)
	BEGIN
		SELECT RAISE(ABORT, 'experience application insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_applications_update
	BEFORE UPDATE ON main.experience_applications
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'experience_application'
	)
	BEGIN
		SELECT RAISE(ABORT, 'experience application update is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_applications_identity_update
	BEFORE UPDATE ON main.experience_applications
	WHEN
		NEW.application_id IS NOT OLD.application_id
		OR NEW.experience_id IS NOT OLD.experience_id
		OR NEW.experience_version IS NOT OLD.experience_version
		OR NEW.project_identity IS NOT OLD.project_identity
		OR NEW.session_key IS NOT OLD.session_key
		OR NEW.delivery_kind IS NOT OLD.delivery_kind
		OR NEW.delivery_state IS NOT OLD.delivery_state
		OR NEW.delivered_at IS NOT OLD.delivered_at
		OR NEW.payload_encoding IS NOT OLD.payload_encoding
		OR NEW.inserted_at IS NOT OLD.inserted_at
	BEGIN
		SELECT RAISE(ABORT, 'experience application delivery identity is immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_applications_delete
	BEFORE DELETE ON main.experience_applications
	BEGIN
		SELECT RAISE(ABORT, 'experience applications are durable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_generations_insert
	BEFORE INSERT ON main.experience_generations
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'experience_generation'
	)
	BEGIN
		SELECT RAISE(ABORT, 'experience generation insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_generations_update
	BEFORE UPDATE ON main.experience_generations
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'experience_generation'
	)
	BEGIN
		SELECT RAISE(ABORT, 'experience generation mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_generations_delete
	BEFORE DELETE ON main.experience_generations
	BEGIN
		SELECT RAISE(ABORT, 'experience generations are durable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_mission_pack_previews_insert
	BEFORE INSERT ON main.mission_pack_previews
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'mission_pack_preview'
	)
	BEGIN
		SELECT RAISE(ABORT, 'mission pack preview insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_mission_pack_previews_update
	BEFORE UPDATE ON main.mission_pack_previews
	BEGIN
		SELECT RAISE(ABORT, 'mission pack previews are immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_mission_pack_previews_delete
	BEFORE DELETE ON main.mission_pack_previews
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'mission_pack_preview'
	)
	BEGIN
		SELECT RAISE(ABORT, 'mission pack preview deletion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_mission_pack_receipts_insert
	BEFORE INSERT ON main.mission_pack_receipts
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'mission_pack_receipt'
	)
	BEGIN
		SELECT RAISE(ABORT, 'mission pack receipt insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_mission_pack_receipts_update
	BEFORE UPDATE ON main.mission_pack_receipts
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'mission_pack_receipt'
	)
	BEGIN
		SELECT RAISE(ABORT, 'mission pack receipt mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_mission_pack_receipts_delete
	BEFORE DELETE ON main.mission_pack_receipts
	BEGIN
		SELECT RAISE(ABORT, 'mission pack receipts are durable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_mission_pack_receipt_applications_insert
	BEFORE INSERT ON main.mission_pack_receipt_applications
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'mission_pack_receipt'
	)
	BEGIN
		SELECT RAISE(ABORT, 'mission pack receipt application insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_mission_pack_receipt_applications_update
	BEFORE UPDATE ON main.mission_pack_receipt_applications
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'mission_pack_receipt'
	)
	BEGIN
		SELECT RAISE(ABORT, 'mission pack receipt application mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_mission_pack_receipt_applications_identity_update
	BEFORE UPDATE ON main.mission_pack_receipt_applications
	WHEN
		NEW.receipt_id IS NOT OLD.receipt_id
		OR NEW.experience_id IS NOT OLD.experience_id
		OR NEW.experience_version IS NOT OLD.experience_version
		OR NEW.created_at IS NOT OLD.created_at
	BEGIN
		SELECT RAISE(ABORT, 'mission pack receipt application identity is immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_mission_pack_receipt_applications_delete
	BEFORE DELETE ON main.mission_pack_receipt_applications
	BEGIN
		SELECT RAISE(ABORT, 'mission pack receipt application links are durable');
	END;`

const trajectoryDerivationMutationTriggerSQL = `
	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_trajectory_derivation_insert
	BEFORE INSERT ON main.trajectory_derivation_state
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'trajectory_derivation'
	)
	BEGIN
		SELECT RAISE(ABORT, 'trajectory derivation insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_trajectory_derivation_update
	BEFORE UPDATE ON main.trajectory_derivation_state
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'trajectory_derivation'
	)
	BEGIN
		SELECT RAISE(ABORT, 'trajectory derivation mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_trajectory_derivation_identity_update
	BEFORE UPDATE ON main.trajectory_derivation_state
	WHEN
		NEW.session_key IS NOT OLD.session_key
		OR NEW.derivation_version IS NOT OLD.derivation_version
		OR NEW.payload_encoding IS NOT OLD.payload_encoding
		OR NEW.created_at IS NOT OLD.created_at
	BEGIN
		SELECT RAISE(ABORT, 'trajectory derivation identity is immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_trajectory_derivation_delete
	BEFORE DELETE ON main.trajectory_derivation_state
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('transcript_retention', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'trajectory derivation deletion is not authorized');
	END;`

const experienceImpactMutationTriggerSQL = `
	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_impact_insert
	BEFORE INSERT ON main.experience_impact_observations
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'experience_impact'
	)
	BEGIN
		SELECT RAISE(ABORT, 'experience impact insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_impact_update
	BEFORE UPDATE ON main.experience_impact_observations
	BEGIN
		SELECT RAISE(ABORT, 'experience impact observations are immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_experience_impact_delete
	BEFORE DELETE ON main.experience_impact_observations
	BEGIN
		SELECT RAISE(ABORT, 'experience impact observations are durable');
	END;`

const fixMutationTriggerSQL = `
	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_fix_annotations_update
	BEFORE UPDATE ON main.fix_annotations
	BEGIN
		SELECT RAISE(ABORT, 'fix annotations are append-only');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_fix_annotations_delete
	BEFORE DELETE ON main.fix_annotations
	BEGIN
		SELECT RAISE(ABORT, 'fix annotations are append-only');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_fix_retractions_update
	BEFORE UPDATE ON main.fix_annotation_retractions
	BEGIN
		SELECT RAISE(ABORT, 'fix annotation retractions are append-only');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_fix_retractions_delete
	BEFORE DELETE ON main.fix_annotation_retractions
	BEGIN
		SELECT RAISE(ABORT, 'fix annotation retractions are append-only');
	END;`

const recurrenceMutationTriggerSQL = `
	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_fix_monitoring_subjects_update
	BEFORE UPDATE ON main.fix_monitoring_subjects
	BEGIN
		SELECT RAISE(ABORT, 'fix monitoring subjects are append-only');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_fix_monitoring_subjects_delete
	BEFORE DELETE ON main.fix_monitoring_subjects
	BEGIN
		SELECT RAISE(ABORT, 'fix monitoring subjects are append-only');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_analysis_capabilities_update
	BEFORE UPDATE ON main.session_analysis_capabilities
	BEGIN
		SELECT RAISE(ABORT, 'analysis capabilities are immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_analysis_capabilities_delete
	BEFORE DELETE ON main.session_analysis_capabilities
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('projection_rebuild', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'analysis capability mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_recurrence_jobs_update
	BEFORE UPDATE ON main.fix_recurrence_jobs
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'recurrence_worker'
	)
	BEGIN
		SELECT RAISE(ABORT, 'recurrence job mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_recurrence_jobs_delete
	BEFORE DELETE ON main.fix_recurrence_jobs
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'retention_prune'
	)
	BEGIN
		SELECT RAISE(ABORT, 'recurrence job deletion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_recurrence_job_events_update
	BEFORE UPDATE ON main.fix_recurrence_job_events
	BEGIN
		SELECT RAISE(ABORT, 'recurrence job events are append-only');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_recurrence_job_events_delete
	BEFORE DELETE ON main.fix_recurrence_job_events
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'retention_prune'
	)
	BEGIN
		SELECT RAISE(ABORT, 'recurrence job events are append-only');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_recurrence_observations_update
	BEFORE UPDATE ON main.fix_recurrence_observations
	BEGIN
		SELECT RAISE(ABORT, 'recurrence observations are append-only');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_recurrence_observations_delete
	BEFORE DELETE ON main.fix_recurrence_observations
	BEGIN
		SELECT RAISE(ABORT, 'recurrence observations are append-only');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_recurrence_observation_events_update
	BEFORE UPDATE ON main.fix_recurrence_observation_events
	BEGIN
		SELECT RAISE(ABORT, 'recurrence observation events are immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_recurrence_observation_events_delete
	BEFORE DELETE ON main.fix_recurrence_observation_events
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'retention_prune'
	)
	BEGIN
		SELECT RAISE(ABORT, 'recurrence observation event deletion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_fix_monitoring_metadata_update
	BEFORE UPDATE ON main.fix_monitoring_metadata
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'recurrence_worker'
	)
	BEGIN
		SELECT RAISE(ABORT, 'fix monitoring metadata mutation is not authorized');
	END;`

const issueSummaryMutationTriggerSQL = `
	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_issue_summary_revisions_update
	BEFORE UPDATE ON main.issue_summary_revisions
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('projection_rebuild', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'issue summary mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_issue_summary_revisions_delete
	BEFORE DELETE ON main.issue_summary_revisions
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('projection_rebuild', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'issue summary deletion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_issue_summary_harnesses_update
	BEFORE UPDATE ON main.issue_summary_harnesses
	BEGIN
		SELECT RAISE(ABORT, 'issue summary harnesses are immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_issue_summary_harnesses_delete
	BEFORE DELETE ON main.issue_summary_harnesses
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('projection_rebuild', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'issue summary harness deletion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_issue_summary_sessions_update
	BEFORE UPDATE ON main.issue_summary_sessions
	BEGIN
		SELECT RAISE(ABORT, 'issue summary sessions are immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_issue_summary_sessions_delete
	BEFORE DELETE ON main.issue_summary_sessions
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('projection_rebuild', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'issue summary session deletion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_issue_coverage_update
	BEFORE UPDATE ON main.issue_analysis_coverage_revisions
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('projection_rebuild', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'issue coverage mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_issue_coverage_delete
	BEFORE DELETE ON main.issue_analysis_coverage_revisions
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('projection_rebuild', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'issue coverage deletion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_issue_generation_times_update
	BEFORE UPDATE ON main.issue_projection_generation_times
	BEGIN
		SELECT RAISE(ABORT, 'issue generation timestamps are immutable');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_issue_generation_times_delete
	BEFORE DELETE ON main.issue_projection_generation_times
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('projection_rebuild', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'issue generation timestamp deletion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_issue_summary_metadata_update
	BEFORE UPDATE ON main.issue_summary_metadata
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('projection_rebuild', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'issue summary metadata mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_issue_summary_metadata_delete
	BEFORE DELETE ON main.issue_summary_metadata
	BEGIN
		SELECT RAISE(ABORT, 'issue summary metadata cannot be deleted');
	END;`

const habitDebriefMutationTriggerSQL = `
	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_habit_debriefs_insert
	BEFORE INSERT ON main.habit_debriefs
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'habit_debrief'
	)
	BEGIN
		SELECT RAISE(ABORT, 'habit debrief insertion is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_habit_debriefs_update
	BEFORE UPDATE ON main.habit_debriefs
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose = 'habit_debrief'
	)
	BEGIN
		SELECT RAISE(ABORT, 'habit debrief mutation is not authorized');
	END;

	CREATE TEMP TRIGGER IF NOT EXISTS belay_guard_habit_debriefs_delete
	BEFORE DELETE ON main.habit_debriefs
	WHEN NOT EXISTS (
		SELECT 1 FROM belay_mutation_authorization
		WHERE purpose IN ('habit_debrief', 'transcript_retention', 'retention_prune')
	)
	BEGIN
		SELECT RAISE(ABORT, 'habit debrief deletion is not authorized');
	END;`
