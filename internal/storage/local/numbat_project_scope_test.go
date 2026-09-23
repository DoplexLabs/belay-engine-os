package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/limits"
)

func TestMigration007FreshStorePersistsAndQueriesOpaqueFindingScope(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "belay.sqlite")
	provider := newMemoryKeyProvider()
	store, err := Open(path, provider)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	var migrationCount, columnCount int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations",
	).Scan(&migrationCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if migrationCount != 30 {
		t.Fatalf("migration count = %d, want 30", migrationCount)
	}
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM pragma_table_info('findings')
		WHERE name = 'project_scope_hint'`,
	).Scan(&columnCount); err != nil {
		t.Fatalf("inspect findings project scope column: %v", err)
	}
	if columnCount != 1 {
		t.Fatalf("project_scope_hint column count = %d, want 1", columnCount)
	}

	rawHash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	scope, err := store.DeriveNumbatProjectScopeHash(rawHash)
	if err != nil {
		t.Fatalf("DeriveNumbatProjectScopeHash() error = %v", err)
	}
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000701",
		"session-numbat-project-scope",
		1,
		time.Date(2026, 9, 8, 14, 0, 0, 0, time.UTC),
	)
	if inserted, err := store.AppendEvent(ctx, event); err != nil || !inserted {
		t.Fatalf("AppendEvent() = (%t, %v), want inserted", inserted, err)
	}
	finding := Finding{
		FindingID:        "finding-numbat-project-scope",
		SourceRunID:      event.Source.RunID,
		SessionKey:       event.Session.Key,
		ProjectScopeHint: scope.ID,
		DetectedAt:       event.OccurredAt.Add(time.Second),
		RuleID:           "numbat.project-scope",
		RuleVersion:      "1",
		Severity:         "low",
		SourceAgent:      "codex",
		Confidence:       "medium",
		CitedEventIDs:    []string{event.EventID},
	}
	if inserted, err := store.RecordFinding(ctx, finding); err != nil || !inserted {
		t.Fatalf("RecordFinding() = (%t, %v), want inserted", inserted, err)
	}

	roundTrip, err := store.GetFinding(ctx, finding.FindingID)
	if err != nil {
		t.Fatalf("GetFinding() error = %v", err)
	}
	if roundTrip.ProjectScopeHint != scope.ID {
		t.Fatalf("GetFinding() scope = %q, want %q", roundTrip.ProjectScopeHint, scope.ID)
	}
	page, err := store.QueryFindings(ctx, model.FindingQuery{
		Filter: model.FindingFilter{Limit: 10},
	})
	if err != nil {
		t.Fatalf("QueryFindings() error = %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ProjectScopeHint != scope.ID {
		t.Fatalf("QueryFindings() data = %+v", page.Data)
	}
	for name, value := range map[string]any{
		"local finding":   roundTrip,
		"finding summary": page.Data[0],
	} {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if strings.Contains(string(body), scope.ID) ||
			strings.Contains(string(body), "ProjectScopeHint") {
			t.Fatalf("%s JSON exposed internal scope hint: %s", name, body)
		}
	}

	var storedHint string
	if err := store.db.QueryRowContext(ctx, `
		SELECT project_scope_hint
		FROM findings
		WHERE finding_id = ?`,
		finding.FindingID,
	).Scan(&storedHint); err != nil {
		t.Fatalf("query stored finding scope: %v", err)
	}
	if storedHint != scope.ID {
		t.Fatalf("stored scope = %q, want %q", storedHint, scope.ID)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	assertSQLiteFilesExclude(t, path, rawHash)
}

func TestMigration007UpgradeReadsExistingFindingWithoutScopeHint(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000702",
		"session-before-project-scope-hint",
		1,
		time.Date(2026, 9, 7, 14, 0, 0, 0, time.UTC),
	)
	finding := Finding{
		FindingID:     "finding-before-project-scope-hint",
		SourceRunID:   event.Source.RunID,
		SessionKey:    event.Session.Key,
		DetectedAt:    event.OccurredAt.Add(time.Second),
		RuleID:        "legacy.rule",
		RuleVersion:   "1",
		Severity:      "low",
		SourceAgent:   "codex",
		Confidence:    "medium",
		CitedEventIDs: []string{event.Source.RecordID},
	}
	createLegacyPlaintextDatabase(t, path, event, finding)

	store, err := Open(path, newMemoryKeyProvider())
	if err != nil {
		t.Fatalf("Open() upgraded store error = %v", err)
	}
	defer store.Close()
	roundTrip, err := store.GetFinding(ctx, finding.FindingID)
	if err != nil {
		t.Fatalf("GetFinding() upgraded finding error = %v", err)
	}
	if roundTrip.ProjectScopeHint != "" {
		t.Fatalf("upgraded finding scope = %q, want empty", roundTrip.ProjectScopeHint)
	}
	page, err := store.QueryFindings(ctx, model.FindingQuery{
		Filter: model.FindingFilter{Limit: 10},
	})
	if err != nil {
		t.Fatalf("QueryFindings() upgraded store error = %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ProjectScopeHint != "" {
		t.Fatalf("upgraded finding summaries = %+v", page.Data)
	}
}

func TestRecordFindingRejectsInvalidProjectScopeHint(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000703",
		"session-invalid-project-scope-hint",
		1,
		time.Date(2026, 9, 8, 15, 0, 0, 0, time.UTC),
	)
	if _, err := store.AppendEvent(ctx, event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	inserted, err := store.RecordFinding(ctx, Finding{
		FindingID:        "finding-invalid-project-scope-hint",
		SourceRunID:      event.Source.RunID,
		SessionKey:       event.Session.Key,
		ProjectScopeHint: "psc_upstream-hash-must-not-be-stored",
		DetectedAt:       event.OccurredAt,
		RuleID:           "invalid.scope",
		RuleVersion:      "1",
		Severity:         "low",
		SourceAgent:      "codex",
		Confidence:       "low",
		CitedEventIDs:    []string{event.EventID},
	})
	if err == nil || inserted {
		t.Fatalf("RecordFinding() = (%t, %v), want validation failure", inserted, err)
	}
	if count, countErr := store.Count(ctx, "findings"); countErr != nil || count != 0 {
		t.Fatalf("finding count = %d, error = %v; want 0", count, countErr)
	}
}

func TestRecordFindingRejectsCitedEventIDLimit(t *testing.T) {
	store := openStorageTestStore(t)
	citations := make([]string, limits.MaxFindingCitedEventIDs+1)
	for index := range citations {
		citations[index] = fmt.Sprintf("event-%03d", index)
	}
	inserted, err := store.RecordFinding(context.Background(), Finding{
		FindingID:     "finding-over-citation-limit",
		SourceRunID:   "run-over-citation-limit",
		SessionKey:    "session-over-citation-limit",
		DetectedAt:    time.Date(2026, 9, 8, 16, 0, 0, 0, time.UTC),
		RuleID:        "rule.limit",
		RuleVersion:   "1",
		Severity:      "low",
		SourceAgent:   "codex",
		Confidence:    "high",
		CitedEventIDs: citations,
	})
	if inserted || !errors.Is(err, ErrFindingCitationLimit) {
		t.Fatalf("RecordFinding() = (%t, %v), want citation limit", inserted, err)
	}
	if count, countErr := store.Count(context.Background(), "findings"); countErr != nil || count != 0 {
		t.Fatalf("finding count = %d, error = %v; want 0", count, countErr)
	}
}

func TestDuplicateFindingIdentityRequiredBeforeProjectScopeBackfill(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000704",
		"session-duplicate-project-scope-hint",
		1,
		time.Date(2026, 9, 8, 16, 0, 0, 0, time.UTC),
	)
	if inserted, err := store.AppendEvent(ctx, event); err != nil || !inserted {
		t.Fatalf("AppendEvent() = (%t, %v), want inserted", inserted, err)
	}
	secondEvent := storageTestEvent(
		"00000000-0000-7000-8000-000000000705",
		event.Session.Key,
		2,
		event.OccurredAt.Add(time.Second),
	)
	if inserted, err := store.AppendEvent(ctx, secondEvent); err != nil || !inserted {
		t.Fatalf("AppendEvent(second) = (%t, %v), want inserted", inserted, err)
	}
	original := Finding{
		FindingID:     "finding-duplicate-project-scope-hint",
		SourceRunID:   event.Source.RunID,
		SessionKey:    event.Session.Key,
		DetectedAt:    event.OccurredAt,
		RuleID:        "original.rule",
		RuleVersion:   "1",
		Severity:      "low",
		SourceAgent:   "codex",
		Confidence:    "medium",
		CitedEventIDs: []string{event.EventID, secondEvent.EventID},
	}
	if inserted, err := store.RecordFinding(ctx, original); err != nil || !inserted {
		t.Fatalf("RecordFinding(original) = (%t, %v), want inserted", inserted, err)
	}
	firstScope, err := store.DeriveNumbatProjectScopeHash(
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	)
	if err != nil {
		t.Fatal(err)
	}
	secondScope, err := store.DeriveNumbatProjectScopeHash(
		"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*Finding)
	}{
		{"source run", func(value *Finding) { value.SourceRunID = "other-run" }},
		{"session", func(value *Finding) { value.SessionKey = "other-session" }},
		{"detected at", func(value *Finding) { value.DetectedAt = value.DetectedAt.Add(time.Nanosecond) }},
		{"rule ID", func(value *Finding) { value.RuleID = "other.rule" }},
		{"rule version", func(value *Finding) { value.RuleVersion = "2" }},
		{"severity", func(value *Finding) { value.Severity = "high" }},
		{"source agent", func(value *Finding) { value.SourceAgent = "claude-code" }},
		{"confidence", func(value *Finding) { value.Confidence = "high" }},
		{"cited ID order", func(value *Finding) {
			value.CitedEventIDs = []string{secondEvent.EventID, event.EventID}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			replay := original
			replay.ProjectScopeHint = firstScope.ID
			test.mutate(&replay)
			result, err := store.RecordFindingResolved(ctx, replay)
			if result != (RecordFindingResult{}) ||
				!errors.Is(err, ErrFindingIdentityConflict) {
				t.Fatalf("RecordFindingResolved() = (%+v, %v), want identity conflict", result, err)
			}
			roundTrip, getErr := store.GetFinding(ctx, original.FindingID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if roundTrip.ProjectScopeHint != "" {
				t.Fatalf("conflicting replay backfilled scope = %q", roundTrip.ProjectScopeHint)
			}
		})
	}

	replay := original
	replay.ProjectScopeHint = firstScope.ID
	replay.DetectedAt = original.DetectedAt.In(time.FixedZone("test-offset", -7*60*60))
	result, err := store.RecordFindingResolved(ctx, replay)
	if err != nil || result.Inserted || !result.ScopeBackfilled ||
		result.SessionKey != original.SessionKey {
		t.Fatalf("RecordFindingResolved(valid replay) = (%+v, %v)", result, err)
	}
	roundTrip, err := store.GetFinding(ctx, original.FindingID)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.ProjectScopeHint != firstScope.ID {
		t.Fatalf("backfilled scope = %q, want %q", roundTrip.ProjectScopeHint, firstScope.ID)
	}
	if roundTrip.SourceRunID != original.SourceRunID ||
		roundTrip.SessionKey != original.SessionKey ||
		!roundTrip.DetectedAt.Equal(original.DetectedAt) ||
		roundTrip.RuleID != original.RuleID ||
		roundTrip.RuleVersion != original.RuleVersion ||
		roundTrip.Severity != original.Severity ||
		roundTrip.SourceAgent != original.SourceAgent ||
		roundTrip.Confidence != original.Confidence ||
		!slices.Equal(roundTrip.CitedEventIDs, original.CitedEventIDs) {
		t.Fatalf("duplicate replay mutated immutable finding fields: %+v", roundTrip)
	}

	replay.ProjectScopeHint = secondScope.ID
	result, err = store.RecordFindingResolved(ctx, replay)
	if err != nil || result.Inserted || result.ScopeBackfilled ||
		result.SessionKey != original.SessionKey {
		t.Fatalf("RecordFindingResolved(second replay) = (%+v, %v)", result, err)
	}
	roundTrip, err = store.GetFinding(ctx, original.FindingID)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.ProjectScopeHint != firstScope.ID {
		t.Fatalf("second replay replaced scope = %q, want %q", roundTrip.ProjectScopeHint, firstScope.ID)
	}
	if count, countErr := store.Count(ctx, "findings"); countErr != nil || count != 1 {
		t.Fatalf("finding count = %d, error = %v; want 1", count, countErr)
	}
}

func TestQueryFindingsForAnalysisBoundsLegacyCitations(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000705",
		"session-legacy-citation-limit",
		1,
		time.Date(2026, 9, 8, 17, 0, 0, 0, time.UTC),
	)
	if inserted, err := store.AppendEvent(ctx, event); err != nil || !inserted {
		t.Fatalf("AppendEvent() = (%t, %v)", inserted, err)
	}
	finding := Finding{
		FindingID:     "finding-legacy-citation-limit",
		SourceRunID:   event.Source.RunID,
		SessionKey:    event.Session.Key,
		DetectedAt:    event.OccurredAt,
		RuleID:        "legacy.rule",
		RuleVersion:   "1",
		Severity:      "low",
		SourceAgent:   "codex",
		Confidence:    "high",
		CitedEventIDs: []string{event.EventID},
	}
	if inserted, err := store.RecordFinding(ctx, finding); err != nil || !inserted {
		t.Fatalf("RecordFinding() = (%t, %v)", inserted, err)
	}

	legacyCitations := make([]string, limits.MaxFindingCitedEventIDs+17)
	for index := range legacyCitations {
		legacyCitations[index] = fmt.Sprintf("legacy-event-%03d", index)
	}
	body, err := json.Marshal(legacyCitations)
	if err != nil {
		t.Fatal(err)
	}
	body, err = store.cipher.seal(
		"finding",
		finding.FindingID,
		"cited_event_ids_json",
		body,
	)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := withMutationTx(ctx, tx, mutationPayloadUpgrade, func() error {
		_, updateErr := tx.ExecContext(ctx, `
			UPDATE findings
			SET cited_event_ids_json = ?, cited_event_ids_encoding = ?
			WHERE finding_id = ?`,
			body,
			payloadEncodingAESGCM,
			finding.FindingID,
		)
		return updateErr
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	page, truncated, err := store.QueryFindingsForAnalysis(ctx, model.FindingQuery{
		Filter: model.FindingFilter{
			SessionID: event.Session.Key,
			Limit:     1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(page.Data) != 1 ||
		len(page.Data[0].CitedEventIDs) != limits.MaxFindingCitedEventIDs {
		t.Fatalf("analysis findings = %d/%t citations=%d",
			len(page.Data), truncated, len(page.Data[0].CitedEventIDs))
	}
	if page.Data[0].CitedEventIDs[0] != "legacy-event-000" ||
		page.Data[0].CitedEventIDs[limits.MaxFindingCitedEventIDs-1] != "legacy-event-049" {
		t.Fatalf("bounded citations = %q", page.Data[0].CitedEventIDs)
	}
	full, err := store.QueryFindings(ctx, model.FindingQuery{
		Filter: model.FindingFilter{
			SessionID: event.Session.Key,
			Limit:     1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Data) != 1 || len(full.Data[0].CitedEventIDs) != len(legacyCitations) {
		t.Fatalf("regular finding query citations = %d, want %d",
			len(full.Data[0].CitedEventIDs), len(legacyCitations))
	}
}
