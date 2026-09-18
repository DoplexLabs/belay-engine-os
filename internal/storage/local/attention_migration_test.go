package local

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func TestMigration009BackfillsActiveAndRetainedExperimentalRevisions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pre-attention.sqlite")
	db, err := openGuardedSQLite(mustSQLiteDSN(t, path))
	if err != nil {
		t.Fatal(err)
	}
	migrations := []string{
		"001_initial.sql",
		"002_encrypted_payloads_and_lifecycle.sql",
		"003_stable_read_order.sql",
		"004_findings_session_filter.sql",
		"005_issue_projection.sql",
		"006_projection_timestamp_encoding.sql",
		"007_numbat_finding_project_scope_hint.sql",
		"008_connection_local_mutation_guards.sql",
	}
	for index, name := range migrations {
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx,
			"INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)",
			index+1,
			time.Now().UTC().Format(time.RFC3339Nano),
		); err != nil {
			t.Fatalf("record %s: %v", name, err)
		}
	}

	now := time.Date(2026, 9, 8, 19, 0, 0, 0, time.UTC)
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000921",
		"session-migration-009",
		1,
		now,
	)
	eventBody, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO events (
			event_id, source_deduplication_key, schema_version, installation_id,
			session_key, occurred_at, observed_at, source_sequence, event_type,
			actor, action, outcome, source_agent, source_kind, source_record_id,
			source_run_id, historical, canonical_json, canonical_encoding, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventID,
		event.Source.DeduplicationKey,
		event.SchemaVersion,
		event.InstallationID,
		event.Session.Key,
		event.OccurredAt.Format(time.RFC3339Nano),
		event.ObservedAt.Format(time.RFC3339Nano),
		event.Source.Sequence,
		event.Observation.Type,
		event.Observation.Actor,
		event.Observation.Action,
		event.Observation.Outcome,
		event.Source.Agent,
		event.Source.Kind,
		event.Source.RecordID,
		event.Source.RunID,
		boolInt(event.Historical.IsHistorical),
		eventBody,
		payloadEncodingPlaintext,
		formatProjectionTime(now),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		"INSERT INTO event_read_order(sequence, event_id) VALUES (1, ?)",
		event.EventID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE issue_projection_metadata
		SET current_generation = 2, oldest_retained_generation = 0
		WHERE singleton = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO session_analysis_revisions (
			revision_id, session_key, status, scope_quality, target_generation,
			analyzed_generation, error_code, visible_from_generation,
			created_at, updated_at
		) VALUES (?, ?, 'current', 'unscoped', 1, 1, NULL, 2, ?, ?)`,
		"sar_migration_009",
		event.Session.Key,
		formatProjectionTime(now),
		formatProjectionTime(now),
	); err != nil {
		t.Fatal(err)
	}
	evidence, err := json.Marshal(model.IssueEvidence{
		CitedEventIDs: []string{event.EventID},
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, revision := range []struct {
		id    string
		from  int64
		until any
	}{
		{id: "ior_migration_009_retained", from: 1, until: int64(2)},
		{id: "ior_migration_009_active", from: 2, until: nil},
	} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO issue_occurrences (
				revision_id, occurrence_id, issue_id, fingerprint_id,
				fingerprint_version, origin, origin_record_id, session_key,
				harness, detector_id, detector_version, projection_version,
				category, title_code, severity, confidence, scope_quality,
				first_observed_at, last_observed_at, evidence_complete,
				retained_history_only, analysis_status, analysis_generation,
				evidence_payload, evidence_encoding, visible_from_generation,
				visible_until_generation, created_at, updated_at
			) VALUES (
				?, ?, ?, ?, '1', 'belay', NULL, ?, 'codex',
				'repeated_command_attempts', '1.0.0', 'belay.detectors.v1',
				'attention', 'issue.repeated_command_attempts', 'info', 'medium',
				'unscoped', ?, ?, 1, 0, 'current', ?, ?, ?, ?, ?, ?, ?
			)`,
			revision.id,
			"occ_migration_009",
			"iss_"+digest,
			"ifp_"+digest,
			event.Session.Key,
			formatProjectionTime(now),
			formatProjectionTime(now),
			revision.from,
			evidence,
			payloadEncodingPlaintext,
			revision.from,
			revision.until,
			formatProjectionTime(now),
			formatProjectionTime(now),
		); err != nil {
			t.Fatalf("insert legacy issue revision: %v", err)
		}
	}
	gapDigest := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO issue_occurrences (
			revision_id, occurrence_id, issue_id, fingerprint_id,
			fingerprint_version, origin, origin_record_id, session_key,
			harness, detector_id, detector_version, projection_version,
			category, title_code, severity, confidence, scope_quality,
			first_observed_at, last_observed_at, evidence_complete,
			retained_history_only, analysis_status, analysis_generation,
			evidence_payload, evidence_encoding, visible_from_generation,
			visible_until_generation, created_at, updated_at
		) VALUES (
			'ior_migration_009_gap', 'occ_migration_009_gap', ?, ?, '1',
			'belay', NULL, ?, 'codex', 'verification_not_observed', '1.0.0',
			'belay.detectors.v1', 'evidence_gap',
			'issue.verification_not_observed', 'info', 'medium', 'unscoped',
			?, ?, 1, 0, 'current', 2, ?, ?, 2, NULL, ?, ?
		)`,
		"iss_"+gapDigest,
		"ifp_"+gapDigest,
		event.Session.Key,
		formatProjectionTime(now),
		formatProjectionTime(now),
		evidence,
		payloadEncodingPlaintext,
		formatProjectionTime(now),
		formatProjectionTime(now),
	); err != nil {
		t.Fatalf("insert legacy evidence gap: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path, newMemoryKeyProvider())
	if err != nil {
		t.Fatalf("Open() upgraded store: %v", err)
	}
	defer store.Close()
	var migrationCount, experimentalCount int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations",
	).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM issue_occurrences WHERE experimental = 1`,
	).Scan(&experimentalCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 27 || experimentalCount != 2 {
		t.Fatalf(
			"migration count/experimental revisions = %d/%d, want 27/2",
			migrationCount,
			experimentalCount,
		)
	}
	defaultPage, err := store.QueryIssues(ctx, model.IssueQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultPage.Data) != 0 {
		t.Fatalf("upgraded experimental issue exposed by default: %+v", defaultPage.Data)
	}
	explicitPage, err := store.QueryIssues(ctx, model.IssueQuery{
		Filter: model.IssueFilter{
			AttentionKind: model.AttentionKindAll,
			Experimental:  model.ExperimentalInclude,
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(explicitPage.Data) != 2 {
		t.Fatalf("explicit upgraded issue = %+v", explicitPage.Data)
	}
	var foundExperimental, foundEvidenceGap bool
	for _, summary := range explicitPage.Data {
		foundExperimental = foundExperimental || summary.Experimental
		foundEvidenceGap = foundEvidenceGap || summary.Category == "evidence_gap"
	}
	if !foundExperimental || !foundEvidenceGap {
		t.Fatalf("upgraded attention metadata = %+v", explicitPage.Data)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE issue_occurrences SET experimental = 0
		WHERE revision_id = 'ior_migration_009_active'`); err == nil {
		t.Fatal("migration authorization leaked after upgrade")
	}
}

func mustSQLiteDSN(t *testing.T, path string) string {
	t.Helper()
	dsn, err := sqliteDSN(path)
	if err != nil {
		t.Fatal(err)
	}
	return dsn
}
