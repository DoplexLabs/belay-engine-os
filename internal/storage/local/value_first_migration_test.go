package local

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

const (
	valueFirstSafeSignal    = "tamper.guardrails_off"
	valueFirstOtherSignal   = "permission.denied"
	valueFirstInvalidSignal = "Tamper Guardrails\nOff"
)

type valueFirstMigrationFixture struct {
	path           string
	provider       *memoryKeyProvider
	safeIssueID    string
	invalidIssueID string
	belayIssueID   string
	oldPage        model.IssuePage
}

func TestMigration013UpgradeBackfillsSignalsRotatesEpochAndExpiresCursor(
	t *testing.T,
) {
	fixture := createValueFirstMigrationFixture(t)
	removeMigration013(t, fixture.path)

	store, err := Open(fixture.path, fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	assertValueFirstMigrationReady(t, store)
	assertIssueSourceSignal(t, store, fixture.safeIssueID, valueFirstSafeSignal)
	assertIssueSourceSignal(t, store, fixture.invalidIssueID, "")
	assertIssueSourceSignal(t, store, fixture.belayIssueID, "")
	if got := mustIssueCursorEpoch(t, store); got == fixture.oldPage.CursorEpoch {
		t.Fatalf("migration 013 did not rotate cursor epoch %q", got)
	}
	if _, err := store.QueryIssues(context.Background(), model.IssueQuery{
		CursorEpoch:         fixture.oldPage.CursorEpoch,
		Snapshot:            fixture.oldPage.Snapshot,
		RetentionGeneration: fixture.oldPage.RetentionGeneration,
		IssuedAt:            fixture.oldPage.IssuedAt,
	}); !errors.Is(err, model.ErrIssueSnapshotExpired) {
		t.Fatalf("old issue cursor error = %v, want expired", err)
	}
}

func TestMigration013ResumesAfterInterruption(t *testing.T) {
	t.Run("after DDL", func(t *testing.T) {
		fixture := createValueFirstMigrationFixture(t)
		removeMigration013(t, fixture.path)
		raw := openRawMigrationDB(t, fixture.path)
		applyMigration013DDL(t, raw)
		if err := raw.Close(); err != nil {
			t.Fatal(err)
		}

		store, err := Open(fixture.path, fixture.provider)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		assertValueFirstMigrationReady(t, store)
		assertIssueSourceSignal(t, store, fixture.safeIssueID, valueFirstSafeSignal)
		assertIssueSourceSignal(t, store, fixture.invalidIssueID, "")
	})

	t.Run("between occurrence batches", func(t *testing.T) {
		fixture := createValueFirstMigrationFixture(t)
		removeMigration013(t, fixture.path)
		raw := openRawMigrationDB(t, fixture.path)
		applyMigration013DDL(t, raw)
		prepareInterruptedValueFirstMigration(t, raw)
		if err := raw.Close(); err != nil {
			t.Fatal(err)
		}

		store, err := Open(fixture.path, fixture.provider)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		assertValueFirstMigrationReady(t, store)
		assertIssueSourceSignal(t, store, fixture.safeIssueID, valueFirstSafeSignal)
		assertIssueSourceSignal(t, store, fixture.invalidIssueID, "")
		assertIssueSourceSignal(t, store, fixture.belayIssueID, "")
	})
}

func TestMigration013RepairsInvalidNumbatSourceMapping(t *testing.T) {
	fixture := createValueFirstMigrationFixture(t)
	raw := openRawMigrationDB(t, fixture.path)
	if _, err := raw.Exec(`
		UPDATE issue_occurrences
		SET source_signal_code = ?
		WHERE issue_id = ?`,
		valueFirstOtherSignal,
		fixture.invalidIssueID,
	); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		UPDATE issue_summary_revisions
		SET source_signal_code = ?
		WHERE issue_id = ? AND visible_until_generation IS NULL`,
		valueFirstOtherSignal,
		fixture.invalidIssueID,
	); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(fixture.path, fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	assertValueFirstMigrationReady(t, store)
	assertIssueSourceSignal(t, store, fixture.invalidIssueID, "")
}

func TestSourceSignalSummaryRequiresCompleteEqualValues(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 8, 15, 0, 0, 0, time.UTC)
	fingerprint, issueID, err := store.DeriveIssueIdentity(
		"failure.v1",
		"numbat_finding",
		"shared-value-first-scope",
		"stable",
	)
	if err != nil {
		t.Fatal(err)
	}
	var occurrences []model.IssueOccurrence
	for index, sessionID := range []string{
		"session-value-first-a",
		"session-value-first-b",
	} {
		event := storageTestEvent(
			[]string{
				"00000000-0000-7000-8000-000000001301",
				"00000000-0000-7000-8000-000000001302",
			}[index],
			sessionID,
			1,
			base.Add(time.Duration(index)*time.Minute),
		)
		_, err := store.AppendEventResolved(ctx, event)
		if err != nil {
			t.Fatal(err)
		}
		occurrence := valueFirstNumbatOccurrence(
			event,
			fingerprint,
			issueID,
			"finding-runtime-"+sessionID,
			model.SafeSourceSignalCode(valueFirstSafeSignal),
		)
		if _, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
			SessionKey:        sessionID,
			Origin:            "numbat",
			ClaimedGeneration: dirtyTargetGeneration(t, store, sessionID),
			Status:            model.AnalysisCurrent,
			ScopeQuality:      model.ScopeUnscoped,
			Occurrences:       []model.IssueOccurrence{occurrence},
		}); err != nil {
			t.Fatal(err)
		}
		occurrences = append(occurrences, occurrence)
	}
	assertIssueSummarySourceSignal(t, store, issueID, valueFirstSafeSignal)

	target, err := store.MarkSessionDirty(
		ctx,
		occurrences[1].SessionID,
		"source_signal_changed",
	)
	if err != nil {
		t.Fatal(err)
	}
	occurrences[1].SourceSignalCode = model.SafeSourceSignalCode(valueFirstOtherSignal)
	if _, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
		SessionKey:        occurrences[1].SessionID,
		Origin:            "numbat",
		ClaimedGeneration: target,
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeUnscoped,
		Occurrences:       []model.IssueOccurrence{occurrences[1]},
	}); err != nil {
		t.Fatal(err)
	}
	assertIssueSummarySourceSignal(t, store, issueID, "")

	target, err = store.MarkSessionDirty(
		ctx,
		occurrences[1].SessionID,
		"source_signal_missing",
	)
	if err != nil {
		t.Fatal(err)
	}
	occurrences[1].SourceSignalCode = nil
	if _, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
		SessionKey:        occurrences[1].SessionID,
		Origin:            "numbat",
		ClaimedGeneration: target,
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeUnscoped,
		Occurrences:       []model.IssueOccurrence{occurrences[1]},
	}); err != nil {
		t.Fatal(err)
	}
	assertIssueSummarySourceSignal(t, store, issueID, "")
}

func TestSourceSignalWritesRequireSafeNumbatAndAuthorizedRebuild(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000001303",
		"session-value-first-writes",
		1,
		time.Date(2026, 9, 8, 16, 0, 0, 0, time.UTC),
	)
	_, err := store.AppendEventResolved(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, issueID, err := store.DeriveIssueIdentity(
		"failure.v1",
		"numbat_finding",
		event.Session.Key,
		"write-guards",
	)
	if err != nil {
		t.Fatal(err)
	}
	valid := valueFirstNumbatOccurrence(
		event,
		fingerprint,
		issueID,
		"finding-write-guards",
		model.SafeSourceSignalCode(valueFirstSafeSignal),
	)
	invalid := valid
	invalid.SourceSignalCode = stringPointer(valueFirstInvalidSignal)
	if _, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
		SessionKey:        event.Session.Key,
		Origin:            "numbat",
		ClaimedGeneration: dirtyTargetGeneration(t, store, event.Session.Key),
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeUnscoped,
		Occurrences:       []model.IssueOccurrence{invalid},
	}); err == nil {
		t.Fatal("invalid Numbat source signal unexpectedly accepted")
	}
	belay := valid
	belay.Origin = "belay"
	belay.OriginRecordID = ""
	if _, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
		SessionKey:        event.Session.Key,
		Origin:            "belay",
		ClaimedGeneration: dirtyTargetGeneration(t, store, event.Session.Key),
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeUnscoped,
		Occurrences:       []model.IssueOccurrence{belay},
	}); err == nil {
		t.Fatal("Belay source signal unexpectedly accepted")
	}
	if _, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
		SessionKey:        event.Session.Key,
		Origin:            "numbat",
		ClaimedGeneration: dirtyTargetGeneration(t, store, event.Session.Key),
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeUnscoped,
		Occurrences:       []model.IssueOccurrence{valid},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE issue_occurrences SET source_signal_code = ?
		WHERE issue_id = ?`,
		valueFirstOtherSignal,
		issueID,
	); err == nil {
		t.Fatal("unauthorized source signal update unexpectedly succeeded")
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		_, err := tx.ExecContext(ctx, `
			UPDATE issue_occurrences SET source_signal_code = ?
			WHERE issue_id = ?`,
			valueFirstOtherSignal,
			issueID,
		)
		return err
	}); err != nil {
		_ = tx.Rollback()
		t.Fatalf("authorized projection rebuild update: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx, err = store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		_, err := tx.ExecContext(ctx, `
			UPDATE issue_occurrences SET source_signal_code = ?
			WHERE issue_id = ?`,
			valueFirstInvalidSignal,
			issueID,
		)
		return err
	})
	_ = tx.Rollback()
	if err == nil {
		t.Fatal("database constraint accepted invalid source signal")
	}
}

func createValueFirstMigrationFixture(t *testing.T) valueFirstMigrationFixture {
	t.Helper()
	ctx := context.Background()
	fixture := valueFirstMigrationFixture{
		path:     filepath.Join(t.TempDir(), "value-first.sqlite"),
		provider: newMemoryKeyProvider(),
	}
	store, err := Open(fixture.path, fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 8, 14, 0, 0, 0, time.UTC)
	fixture.safeIssueID = appendValueFirstMigrationIssue(
		t,
		store,
		"safe",
		"00000000-0000-7000-8000-000000001311",
		base,
		"numbat",
		valueFirstSafeSignal,
		model.SafeSourceSignalCode(valueFirstSafeSignal),
	)
	fixture.invalidIssueID = appendValueFirstMigrationIssue(
		t,
		store,
		"invalid",
		"00000000-0000-7000-8000-000000001312",
		base.Add(time.Minute),
		"numbat",
		valueFirstInvalidSignal,
		nil,
	)
	fixture.belayIssueID = appendValueFirstMigrationIssue(
		t,
		store,
		"belay",
		"00000000-0000-7000-8000-000000001313",
		base.Add(2*time.Minute),
		"belay",
		"",
		nil,
	)
	fixture.oldPage, err = store.QueryIssues(ctx, model.IssueQuery{
		Filter: model.IssueFilter{
			AttentionKind: model.AttentionKindAll,
			Experimental:  model.ExperimentalInclude,
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture.oldPage.Data) != 3 {
		t.Fatalf("fixture issues = %+v", fixture.oldPage.Data)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func appendValueFirstMigrationIssue(
	t *testing.T,
	store *Store,
	label string,
	eventID string,
	observedAt time.Time,
	origin string,
	ruleID string,
	sourceSignalCode *string,
) string {
	t.Helper()
	ctx := context.Background()
	event := storageTestEvent(eventID, "session-value-first-"+label, 1, observedAt)
	_, err := store.AppendEventResolved(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	detectorID := "explicit_command_failure"
	if origin == "numbat" {
		detectorID = "numbat_finding"
	}
	fingerprint, issueID, err := store.DeriveIssueIdentity(
		"failure.v1",
		detectorID,
		event.Session.Key,
		label,
	)
	if err != nil {
		t.Fatal(err)
	}
	occurrence := testIssueOccurrence(
		event.Session.Key,
		"codex",
		event.EventID,
		fingerprint,
		issueID,
		"medium",
		"high",
		observedAt,
		model.ScopeUnscoped,
	)
	if origin == "numbat" {
		findingID := "finding-value-first-" + label
		if _, err := store.RecordFinding(ctx, Finding{
			FindingID:     findingID,
			SourceRunID:   event.Source.RunID,
			SessionKey:    event.Session.Key,
			DetectedAt:    observedAt,
			RuleID:        ruleID,
			RuleVersion:   "1",
			Severity:      "medium",
			SourceAgent:   "codex",
			Confidence:    "high",
			CitedEventIDs: []string{event.EventID},
		}); err != nil {
			t.Fatal(err)
		}
		occurrence = valueFirstNumbatOccurrence(
			event,
			fingerprint,
			issueID,
			findingID,
			sourceSignalCode,
		)
	}
	if _, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
		SessionKey:        event.Session.Key,
		Origin:            origin,
		ClaimedGeneration: dirtyTargetGeneration(t, store, event.Session.Key),
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeUnscoped,
		Occurrences:       []model.IssueOccurrence{occurrence},
	}); err != nil {
		t.Fatal(err)
	}
	return issueID
}

func valueFirstNumbatOccurrence(
	event model.Event,
	fingerprint string,
	issueID string,
	findingID string,
	sourceSignalCode *string,
) model.IssueOccurrence {
	occurrence := testIssueOccurrence(
		event.Session.Key,
		event.Source.Agent,
		event.EventID,
		fingerprint,
		issueID,
		"medium",
		"high",
		event.OccurredAt,
		model.ScopeUnscoped,
	)
	occurrence.Origin = "numbat"
	occurrence.OriginRecordID = findingID
	occurrence.Provenance.DetectorID = "numbat_finding"
	occurrence.Category = "numbat_finding"
	occurrence.TitleCode = "issue.numbat_finding"
	occurrence.SourceSignalCode = sourceSignalCode
	return occurrence
}

func removeMigration013(t *testing.T, path string) {
	t.Helper()
	raw := openRawMigrationDB(t, path)
	for _, statement := range []string{
		"ALTER TABLE issue_summary_revisions DROP COLUMN source_signal_code",
		"ALTER TABLE issue_occurrences DROP COLUMN source_signal_code",
		"DELETE FROM local_migration_progress WHERE migration_version = 13",
		"DELETE FROM schema_migrations WHERE version = 13",
	} {
		if _, err := raw.Exec(statement); err != nil {
			raw.Close()
			t.Fatalf("remove migration 013 with %q: %v", statement, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
}

func applyMigration013DDL(t *testing.T, raw *sql.DB) {
	t.Helper()
	body, err := migrationFiles.ReadFile("migrations/013_value_first_source_signals.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(string(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		INSERT INTO schema_migrations(version, applied_at) VALUES (13, ?)`,
		formatProjectionTime(time.Now().UTC()),
	); err != nil {
		t.Fatal(err)
	}
}

func prepareInterruptedValueFirstMigration(t *testing.T, raw *sql.DB) {
	t.Helper()
	var generation int64
	if err := raw.QueryRow(`
		SELECT current_generation FROM issue_projection_metadata
		WHERE singleton = 1`,
	).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	now := formatProjectionTime(time.Now().UTC())
	for _, statement := range []string{
		"DELETE FROM issue_summary_harnesses",
		"DELETE FROM issue_summary_sessions",
		"DELETE FROM issue_summary_revisions",
		"DELETE FROM issue_analysis_coverage_revisions",
		"DELETE FROM issue_projection_generation_times",
		"DELETE FROM local_migration_progress WHERE migration_version IN (12, 13)",
	} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`
		UPDATE issue_summary_metadata
		SET readiness = 'building', build_generation = ?,
			materialized_generation = 0,
			oldest_materialized_generation = 0,
			updated_at = ?
		WHERE singleton = 1`,
		generation,
		now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		INSERT INTO local_migration_progress (
			migration_version, phase, after_sequence, complete, updated_at
		) VALUES (13, 'prepare', 0, 1, ?)`,
		now,
	); err != nil {
		t.Fatal(err)
	}
	var rowID int64
	var revisionID, origin, ruleID string
	if err := raw.QueryRow(`
		SELECT io.rowid, io.revision_id, io.origin, COALESCE(f.rule_id, '')
		FROM issue_occurrences io
		LEFT JOIN findings f
			ON io.origin = 'numbat' AND f.finding_id = io.origin_record_id
		ORDER BY io.rowid
		LIMIT 1`,
	).Scan(&rowID, &revisionID, &origin, &ruleID); err != nil {
		t.Fatal(err)
	}
	var code any
	if origin == "numbat" {
		if safe := model.SafeSourceSignalCode(ruleID); safe != nil {
			code = *safe
		}
	}
	if _, err := raw.Exec(`
		UPDATE issue_occurrences SET source_signal_code = ?
		WHERE revision_id = ?`,
		code,
		revisionID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		INSERT INTO local_migration_progress (
			migration_version, phase, after_sequence, complete, updated_at
		) VALUES (13, 'occurrences', ?, 0, ?)`,
		rowID,
		now,
	); err != nil {
		t.Fatal(err)
	}
}

func assertValueFirstMigrationReady(t *testing.T, store *Store) {
	t.Helper()
	var migrations, ready int
	var readiness string
	var current, materialized int64
	if err := store.db.QueryRow(`
		SELECT COUNT(*) FROM schema_migrations WHERE version = 13`,
	).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`
		SELECT COUNT(*) FROM local_migration_progress
		WHERE migration_version = 13 AND phase = 'ready' AND complete = 1`,
	).Scan(&ready); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`
		SELECT ism.readiness, ipm.current_generation, ism.materialized_generation
		FROM issue_summary_metadata ism
		JOIN issue_projection_metadata ipm ON ipm.singleton = ism.singleton
		WHERE ism.singleton = 1`,
	).Scan(&readiness, &current, &materialized); err != nil {
		t.Fatal(err)
	}
	if migrations != 1 || ready != 1 ||
		readiness != "ready" || current != materialized {
		t.Fatalf(
			"value-first readiness migrations=%d ready=%d state=%q generations=%d/%d",
			migrations,
			ready,
			readiness,
			current,
			materialized,
		)
	}
}

func assertIssueSourceSignal(
	t *testing.T,
	store *Store,
	issueID string,
	want string,
) {
	t.Helper()
	assertIssueSummarySourceSignal(t, store, issueID, want)
	ctx := context.Background()
	occurrences, err := store.QueryIssueOccurrences(ctx, model.IssueOccurrenceQuery{
		IssueID: issueID,
		Limit:   10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(occurrences.Data) == 0 {
		t.Fatalf("issue %s has no occurrences", issueID)
	}
	for _, occurrence := range occurrences.Data {
		assertOptionalSourceSignal(
			t,
			"occurrence "+occurrence.OccurrenceID,
			occurrence.SourceSignalCode,
			want,
		)
	}
}

func assertIssueSummarySourceSignal(
	t *testing.T,
	store *Store,
	issueID string,
	want string,
) {
	t.Helper()
	ctx := context.Background()
	page, err := store.QueryIssues(ctx, model.IssueQuery{
		Filter: model.IssueFilter{
			IssueID:       issueID,
			AttentionKind: model.AttentionKindAll,
			Experimental:  model.ExperimentalInclude,
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 {
		t.Fatalf("issue %s summaries = %+v", issueID, page.Data)
	}
	assertOptionalSourceSignal(t, "summary", page.Data[0].SourceSignalCode, want)
}

func assertOptionalSourceSignal(
	t *testing.T,
	label string,
	got *string,
	want string,
) {
	t.Helper()
	if want == "" {
		if got != nil {
			t.Fatalf("%s source signal = %q, want null", label, *got)
		}
		return
	}
	if got == nil || *got != want {
		t.Fatalf("%s source signal = %v, want %q", label, got, want)
	}
}

func stringPointer(value string) *string {
	return &value
}
