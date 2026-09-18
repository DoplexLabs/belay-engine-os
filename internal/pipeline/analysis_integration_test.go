package pipeline_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
	"github.com/DoplexLabs/belay-engine/internal/analysis"
	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/canonical/numbatmap"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

func TestFindingProjectScopeHashBecomesPrivateLocalHint(t *testing.T) {
	ctx := context.Background()
	store, databasePath := openTestStore(t)
	rawHash := strings.Repeat("ab", 32)
	event := analysisEventRecord(
		"run-finding-scope",
		"event-finding-scope",
		"session-finding-scope",
		"session.start",
		"2026-09-08T18:00:00Z",
	)
	finding := map[string]any{
		"schema_version":      "0.3.0",
		"record_type":         "finding",
		"run_id":              "run-finding-scope",
		"endpoint":            endpoint(),
		"finding_id":          "finding-project-scope",
		"detected_at":         "2026-09-08T18:00:01Z",
		"rule_id":             "rule.project-scope",
		"rule_version":        "1",
		"severity":            "low",
		"source_agent":        "codex",
		"source_type":         "hook",
		"project_path_hash":   rawHash,
		"session_id":          "session-finding-scope",
		"title":               "Scoped finding",
		"observed_event_type": "session.start",
		"evidence_refs":       []any{evidence()},
		"cited_event_ids":     []string{"event-finding-scope"},
		"redacted":            true,
		"confidence":          "high",
	}
	report, err := newTestImporter(store).Import(
		ctx,
		bytes.NewReader(appendNDJSON(t, nil, event, finding)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.EventsAccepted != 1 || report.FindingsAccepted != 1 {
		t.Fatalf("import report = %+v", report)
	}
	expected, err := store.DeriveNumbatProjectScopeHash(rawHash)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := store.GetFinding(ctx, "finding-project-scope")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ProjectScopeHint != expected.ID ||
		persisted.ProjectScopeHint == rawHash ||
		expected.Quality != model.ScopeLexical {
		t.Fatalf("persisted scope hint = %q, derived = %+v", persisted.ProjectScopeHint, expected)
	}
	page, err := store.QueryFindings(ctx, model.FindingQuery{
		Filter: model.FindingFilter{Limit: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].ProjectScopeHint != expected.ID {
		t.Fatalf("finding page = %+v", page)
	}
	for name, value := range map[string]any{
		"persisted finding": persisted,
		"finding summary":   page.Data[0],
	} {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte(rawHash)) ||
			bytes.Contains(body, []byte(expected.ID)) {
			t.Fatalf("%s exposed private scope material: %s", name, body)
		}
	}
	assertDatabaseFilesExclude(t, databasePath, rawHash)
}

func TestValidFindingReplayBackfillsScopeAndDirtiesPersistedSession(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	event := analysisEventRecord(
		"run-finding-replay",
		"event-finding-replay",
		"session-finding-replay",
		"session.start",
		"2026-09-08T18:00:00Z",
	)
	finding := analysisFindingRecord(
		"run-finding-replay",
		"finding-replay",
		"session-finding-replay",
		"event-finding-replay",
		"2026-09-08T18:00:01Z",
	)
	report, err := newTestImporter(store).Import(
		ctx,
		bytes.NewReader(appendNDJSON(t, nil, event, finding)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.EventsAccepted != 1 || report.FindingsAccepted != 1 {
		t.Fatalf("initial report = %+v", report)
	}
	if _, err := analysis.NewReconciler(store).Drain(ctx); err != nil {
		t.Fatal(err)
	}

	rawHash := strings.Repeat("cd", 32)
	finding["project_path_hash"] = rawHash
	report, err = newTestImporter(store).Import(
		ctx,
		bytes.NewReader(appendNDJSON(t, nil, finding)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.FindingDuplicates != 1 || report.Quarantined != 0 {
		t.Fatalf("replay report = %+v", report)
	}
	work, err := store.ClaimDirtySession(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	expectedSession := numbatmap.SessionKey("codex", "session-finding-replay", "")
	if work.SessionKey != expectedSession {
		t.Fatalf("dirty session = %q, want %q", work.SessionKey, expectedSession)
	}
	persisted, err := store.GetFinding(ctx, "finding-replay")
	if err != nil {
		t.Fatal(err)
	}
	expectedScope, err := store.DeriveNumbatProjectScopeHash(rawHash)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.SessionKey != expectedSession ||
		persisted.ProjectScopeHint != expectedScope.ID {
		t.Fatalf("persisted replay = %+v", persisted)
	}
}

func TestFindingIdentityCollisionIsQuarantinedWithoutDirtyingWrongSession(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	firstEvent := analysisEventRecord(
		"run-finding-first",
		"event-finding-first",
		"session-finding-first",
		"session.start",
		"2026-09-08T18:00:00Z",
	)
	secondEvent := analysisEventRecord(
		"run-finding-second",
		"event-finding-second",
		"session-finding-second",
		"session.start",
		"2026-09-08T18:00:00Z",
	)
	original := analysisFindingRecord(
		"run-finding-first",
		"finding-collision",
		"session-finding-first",
		"event-finding-first",
		"2026-09-08T18:00:01Z",
	)
	if _, err := newTestImporter(store).Import(
		ctx,
		bytes.NewReader(appendNDJSON(t, nil, firstEvent, secondEvent, original)),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := analysis.NewReconciler(store).Drain(ctx); err != nil {
		t.Fatal(err)
	}

	collision := analysisFindingRecord(
		"run-finding-second",
		"finding-collision",
		"session-finding-second",
		"event-finding-second",
		"2026-09-08T18:00:01Z",
	)
	collision["project_path_hash"] = strings.Repeat("ef", 32)
	report, err := newTestImporter(store).Import(
		ctx,
		bytes.NewReader(appendNDJSON(t, nil, collision)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Quarantined != 1 || report.FindingDuplicates != 0 {
		t.Fatalf("collision report = %+v", report)
	}
	quarantines, err := store.Quarantines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(quarantines) != 1 ||
		quarantines[0].Category != "finding_identity_conflict" {
		t.Fatalf("quarantines = %+v", quarantines)
	}
	if _, err := store.ClaimDirtySession(ctx, time.Now().UTC()); !errors.Is(err, local.ErrNoDirtySession) {
		t.Fatalf("ClaimDirtySession() error = %v, want no dirty session", err)
	}
	persisted, err := store.GetFinding(ctx, "finding-collision")
	if err != nil {
		t.Fatal(err)
	}
	expectedSession := numbatmap.SessionKey("codex", "session-finding-first", "")
	if persisted.SessionKey != expectedSession || persisted.ProjectScopeHint != "" {
		t.Fatalf("persisted finding after collision = %+v", persisted)
	}
}

func TestDuplicateRescanPopulatesScopeAndEnrichmentByResolvedEventID(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	projectPath := t.TempDir()
	recordMap := analysisEventRecord(
		"run-enrichment",
		"event-enrichment",
		"session-enrichment",
		"command.exec",
		"2026-09-08T18:00:00Z",
	)
	recordMap["project_path"] = projectPath
	recordMap["command"] = "go test ./pkg/private-target"
	body := marshalLine(t, recordMap)
	parsed, err := numbat.ParseLine(body)
	if err != nil {
		t.Fatal(err)
	}
	raw := parsed.(numbat.EventRecord)
	event, err := numbatmap.Event(raw, numbatmap.Options{
		InstallationID: "inst_edge_acceptance",
		EngineVersion:  "numbat-test-0.3.0",
		ObservedAt:     acceptanceClock,
		Sequence:       1,
		Random:         &deterministicReader{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEventResolved(ctx, event); err != nil {
		t.Fatal(err)
	}
	if enrichments, err := store.EventEnrichments(ctx, event.Session.Key); err != nil {
		t.Fatal(err)
	} else if len(enrichments) != 0 {
		t.Fatalf("precondition enrichments = %+v, want none", enrichments)
	}

	report, err := newTestImporter(store).Import(ctx, bytes.NewReader(append(body, '\n')))
	if err != nil {
		t.Fatal(err)
	}
	if report.EventsAccepted != 0 || report.EventDuplicates != 1 {
		t.Fatalf("replay report = %+v", report)
	}
	enrichments, err := store.EventEnrichments(ctx, event.Session.Key)
	if err != nil {
		t.Fatal(err)
	}
	enrichment, ok := enrichments[event.EventID]
	if !ok {
		t.Fatalf("enrichment keys = %v, want resolved event %q", enrichments, event.EventID)
	}
	if enrichment.CommandSignatureID == "" || enrichment.CommandClass != "test" {
		t.Fatalf("enrichment = %+v", enrichment)
	}
	scope, err := store.GetSessionScope(ctx, event.Session.Key)
	if err != nil {
		t.Fatal(err)
	}
	if scope.ProjectScopeID == "" || scope.Quality == model.ScopeUnscoped {
		t.Fatalf("scope = %+v", scope)
	}
}

func TestImportReconcileAndIssueQueryEndToEnd(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	projectPath := t.TempDir()
	exec := analysisEventRecord(
		"run-issue",
		"event-exec",
		"session-issue",
		"command.exec",
		"2026-09-08T18:00:00Z",
	)
	exec["project_path"] = projectPath
	exec["command"] = "go test ./pkg/api"
	exec["tool_call_id"] = "call-issue"
	result := analysisEventRecord(
		"run-issue",
		"event-result",
		"session-issue",
		"command.result",
		"2026-09-08T18:00:01Z",
	)
	result["project_path"] = projectPath
	result["tool_call_id"] = "call-issue"
	result["exit_code"] = 1
	finding := map[string]any{
		"schema_version":      "0.3.0",
		"record_type":         "finding",
		"run_id":              "run-issue",
		"endpoint":            endpoint(),
		"finding_id":          "finding-issue",
		"detected_at":         "2026-09-08T18:00:02Z",
		"rule_id":             "rule.explicit_failure",
		"rule_version":        "1",
		"severity":            "medium",
		"source_agent":        "codex",
		"source_type":         "hook",
		"session_id":          "session-issue",
		"title":               "Observed failure",
		"observed_event_type": "command.result",
		"evidence_refs":       []any{evidence()},
		"cited_event_ids":     []string{"event-result"},
		"redacted":            true,
		"confidence":          "high",
	}
	input := appendNDJSON(t, nil, exec, result, finding)
	report, err := newTestImporter(store).Import(ctx, bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if report.EventsAccepted != 2 || report.FindingsAccepted != 1 {
		t.Fatalf("import report = %+v", report)
	}
	reconcile, err := analysis.NewReconciler(store).Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reconcile.Current != 1 || reconcile.Occurrences != 2 {
		t.Fatalf("reconcile report = %+v", reconcile)
	}
	page, err := store.QueryIssues(ctx, model.IssueQuery{
		Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 2 || !page.Analysis.Complete ||
		page.Analysis.CurrentSessions != 1 {
		t.Fatalf("issue page = %+v", page)
	}
	origins := map[string]bool{}
	var projectionGeneration int64
	for _, issue := range page.Data {
		origins[issue.Origin] = true
		occurrences, err := store.QueryIssueOccurrences(
			ctx,
			model.IssueOccurrenceQuery{
				IssueID:             issue.IssueID,
				Limit:               20,
				CursorEpoch:         page.CursorEpoch,
				Snapshot:            page.Snapshot,
				RetentionGeneration: page.RetentionGeneration,
				IssuedAt:            page.IssuedAt,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(occurrences.Data) != 1 ||
			len(occurrences.Data[0].Evidence.CitedEventIDs) == 0 {
			t.Fatalf("occurrences for %s = %+v", issue.Origin, occurrences)
		}
		if projectionGeneration == 0 {
			projectionGeneration = occurrences.Data[0].AnalysisGeneration
		} else if occurrences.Data[0].AnalysisGeneration != projectionGeneration {
			t.Fatalf(
				"origins published in different generations: got %d and %d",
				projectionGeneration,
				occurrences.Data[0].AnalysisGeneration,
			)
		}
	}
	if !origins["belay"] || !origins["numbat"] {
		t.Fatalf("issue origins = %v", origins)
	}
}

func TestEnrichmentConflictIsFailOpenAndLaterRecordImports(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	projectPath := t.TempDir()
	poison := analysisEventRecord(
		"run-enrichment-poison",
		"event-enrichment-poison",
		"session-enrichment-poison",
		"command.exec",
		"2026-09-08T18:00:00Z",
	)
	poison["project_path"] = projectPath
	poison["command"] = "go test ./pkg/first"
	if _, err := newTestImporter(store).Import(
		ctx,
		bytes.NewReader(appendNDJSON(t, nil, poison)),
	); err != nil {
		t.Fatal(err)
	}

	conflictingReplay := analysisEventRecord(
		"run-enrichment-poison",
		"event-enrichment-poison",
		"session-enrichment-poison",
		"command.exec",
		"2026-09-08T18:00:00Z",
	)
	conflictingReplay["project_path"] = projectPath
	conflictingReplay["command"] = "cargo build"
	valid := analysisEventRecord(
		"run-enrichment-poison",
		"event-after-poison",
		"session-enrichment-poison",
		"command.exec",
		"2026-09-08T18:00:01Z",
	)
	valid["project_path"] = projectPath
	valid["command"] = "go test ./pkg/after"

	report, err := newTestImporter(store).Import(
		ctx,
		bytes.NewReader(appendNDJSON(t, nil, conflictingReplay, valid)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.EventDuplicates != 1 || report.EventsAccepted != 1 {
		t.Fatalf("import report = %+v", report)
	}
	if count, err := store.Count(ctx, "analysis_diagnostics"); err != nil || count != 1 {
		t.Fatalf("analysis diagnostic count = %d, error = %v", count, err)
	}
	sessions, _, err := store.ListSessions(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].EventCount != 2 {
		t.Fatalf("sessions = %+v", sessions)
	}
	enrichments, err := store.EventEnrichments(ctx, sessions[0].SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(enrichments) != 2 {
		t.Fatalf("enrichments = %+v", enrichments)
	}
}

func TestHistoricalLiveCoalescingDoesNotInflateRepeatedAttempts(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	projectPath := t.TempDir()
	var records []any
	for attempt := 0; attempt < 2; attempt++ {
		timestamp := time.Date(2026, 9, 8, 18, 0, attempt, 0, time.UTC).
			Format(time.RFC3339Nano)
		for _, sourceType := range []string{"artifact", "hook"} {
			record := analysisEventRecord(
				"run-coalesce",
				"event-"+sourceType+"-"+string(rune('a'+attempt)),
				"session-coalesce",
				"command.exec",
				timestamp,
			)
			record["source_type"] = sourceType
			record["project_path"] = projectPath
			record["command"] = "go test ./pkg/coalesce"
			record["tool_call_id"] = "call-" + string(rune('a'+attempt))
			records = append(records, record)
		}
	}
	if _, err := newTestImporter(store).Import(
		ctx,
		bytes.NewReader(appendNDJSON(t, nil, records...)),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := analysis.NewReconciler(store).Drain(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := store.QueryIssues(ctx, model.IssueQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range page.Data {
		if issue.DetectorID == "repeated_command_attempts" {
			t.Fatalf("historical/live duplicates produced repeated issue: %+v", issue)
		}
	}
}

func analysisEventRecord(
	runID string,
	eventID string,
	sessionID string,
	eventType string,
	timestamp string,
) map[string]any {
	record := validEventRecord(runID, eventID, sessionID)
	record["timestamp"] = timestamp
	record["actor"] = "assistant"
	record["event_type"] = eventType
	return record
}

func analysisFindingRecord(
	runID string,
	findingID string,
	sessionID string,
	citedEventID string,
	detectedAt string,
) map[string]any {
	return map[string]any{
		"schema_version":      "0.3.0",
		"record_type":         "finding",
		"run_id":              runID,
		"endpoint":            endpoint(),
		"finding_id":          findingID,
		"detected_at":         detectedAt,
		"rule_id":             "rule.replay",
		"rule_version":        "1",
		"severity":            "low",
		"source_agent":        "codex",
		"source_type":         "artifact",
		"session_id":          sessionID,
		"title":               "Replay finding",
		"observed_event_type": "session.start",
		"evidence_refs":       []any{evidence()},
		"cited_event_ids":     []string{citedEventID},
		"redacted":            true,
		"confidence":          "high",
	}
}
