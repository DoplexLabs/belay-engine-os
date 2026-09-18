package analysis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/detection"
	"github.com/DoplexLabs/belay-engine/internal/limits"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

func TestCommandAndPermissionClassification(t *testing.T) {
	commands := map[string]string{
		"go test ./...":          detection.CommandClassTest,
		"pnpm run typecheck":     detection.CommandClassTypecheck,
		"cargo fmt --check":      detection.CommandClassFormatCheck,
		"golangci-lint run":      detection.CommandClassLint,
		"npm run build":          detection.CommandClassBuild,
		"go run test":            detection.CommandClassOther,
		"cargo run -- test":      detection.CommandClassOther,
		"cargo test":             detection.CommandClassTest,
		"custom-tool --argument": detection.CommandClassOther,
		"":                       detection.CommandClassUnknown,
	}
	for command, want := range commands {
		if got := classifyCommand(command); got != want {
			t.Errorf("classifyCommand(%q) = %q, want %q", command, got, want)
		}
	}

	permissions := []struct {
		record numbat.EventRecord
		want   string
	}{
		{record: numbat.EventRecord{URL: "https://example.test"}, want: "permission.network"},
		{record: numbat.EventRecord{MCPServer: "local"}, want: "permission.mcp"},
		{record: numbat.EventRecord{FilePath: "/tmp/file"}, want: "permission.file"},
		{record: numbat.EventRecord{Command: "go test"}, want: "permission.shell"},
		{record: numbat.EventRecord{ToolName: "custom"}, want: "permission.tool"},
		{record: numbat.EventRecord{}, want: "permission.unknown"},
	}
	for _, test := range permissions {
		if got := classifyPermission(test.record); got != test.want {
			t.Errorf("classifyPermission(%+v) = %q, want %q", test.record, got, test.want)
		}
	}
}

func TestDetectorIdentityDoesNotRepeatScopeDimension(t *testing.T) {
	store := openAnalysisStore(t)
	reconciler := NewReconciler(store)
	scope := local.SessionScope{
		SessionKey:     "session-scope-once",
		ProjectScopeID: "psc_scope",
		Quality:        model.ScopeResolved,
	}
	result := detection.CatalogResult{
		CatalogVersion: detection.CatalogVersion,
		Status:         detection.StatusCurrent,
		Matches: []detection.Match{{
			DetectorID:         "test_detector",
			DetectorVersion:    "1",
			FingerprintVersion: "1",
			Category:           "test_issue",
			TitleCode:          "issue.test",
			Severity:           "low",
			Confidence:         "high",
			Experimental:       true,
			FirstObservedAt:    time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC),
			LastObservedAt:     time.Date(2026, 9, 8, 18, 0, 1, 0, time.UTC),
			EvidenceComplete:   true,
			Fingerprint: []detection.FingerprintDimension{
				{Name: "command_signature_id", Value: "cmd_signature"},
				{Name: "project_scope_id", Value: scope.ProjectScopeID},
			},
		}},
	}
	occurrences, err := reconciler.detectorOccurrences(scope, nil, result)
	if err != nil {
		t.Fatal(err)
	}
	if len(occurrences) != 1 {
		t.Fatalf("occurrences = %+v", occurrences)
	}
	wantFingerprint, _, err := store.DeriveIssueIdentity(
		"1",
		"test_detector",
		scope.ProjectScopeID,
		"command_signature_id",
		"cmd_signature",
	)
	if err != nil {
		t.Fatal(err)
	}
	if occurrences[0].FingerprintID != wantFingerprint {
		t.Fatalf(
			"fingerprint = %q, want scope represented exactly once as %q",
			occurrences[0].FingerprintID,
			wantFingerprint,
		)
	}
	if !occurrences[0].Experimental {
		t.Fatal("experimental detector metadata was not propagated")
	}
}

func TestReconcilerPublishesDetectorFailureAndContinuesFailOpen(t *testing.T) {
	ctx := context.Background()
	store := openAnalysisStore(t)
	appendAnalysisEvent(t, store, "session-failure", 1)
	catalog, err := detection.NewCatalog(failingDetector{})
	if err != nil {
		t.Fatal(err)
	}
	reconciler := NewReconcilerWithCatalog(store, catalog)
	reconciler.now = func() time.Time {
		return time.Date(2026, 9, 8, 20, 0, 0, 0, time.UTC)
	}
	report, err := reconciler.Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 1 || report.Current != 0 {
		t.Fatalf("report = %+v", report)
	}
	page, err := store.QueryIssues(ctx, model.IssueQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Analysis.FailedSessions != 1 || page.Analysis.Complete {
		t.Fatalf("analysis coverage = %+v", page.Analysis)
	}
	if count, err := store.Count(ctx, "analysis_diagnostics"); err != nil || count != 1 {
		t.Fatalf("analysis diagnostic count = %d, error = %v", count, err)
	}
}

func TestAtomicProjectionFailurePublishesFailedWithoutPartialOrigin(t *testing.T) {
	ctx := context.Background()
	store := openAnalysisStore(t)
	event := appendAnalysisEventWithAgent(
		t,
		store,
		"session-atomic-failure",
		1,
		"bad harness",
	)
	if _, err := store.RecordFinding(ctx, local.Finding{
		FindingID:     "finding-atomic-failure",
		SourceRunID:   event.Source.RunID,
		SessionKey:    event.Session.Key,
		DetectedAt:    event.OccurredAt,
		RuleID:        "rule.atomic",
		RuleVersion:   "1",
		Severity:      "low",
		SourceAgent:   "codex",
		Confidence:    "high",
		CitedEventIDs: []string{event.EventID},
	}); err != nil {
		t.Fatal(err)
	}
	catalog, err := detection.NewCatalog(singleMatchDetector{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := NewReconcilerWithCatalog(store, catalog).Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 1 || report.Current != 0 || report.Occurrences != 0 {
		t.Fatalf("report = %+v", report)
	}
	if count, err := store.Count(ctx, "issue_occurrences"); err != nil || count != 0 {
		t.Fatalf("partial occurrence count = %d, error = %v", count, err)
	}
	page, err := store.QueryIssues(ctx, model.IssueQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Analysis.FailedSessions != 1 || page.Analysis.Complete {
		t.Fatalf("analysis coverage = %+v", page.Analysis)
	}
}

func TestReconcilerBoundsNumbatGroupsAndContinuesLaterSessions(t *testing.T) {
	ctx := context.Background()
	store := openAnalysisStore(t)
	overflowEvent := appendAnalysisEvent(t, store, "session-numbat-overflow", 1)
	for index := 0; index < projectionOriginLimit+1; index++ {
		if _, err := store.RecordFinding(ctx, local.Finding{
			FindingID:     fmt.Sprintf("finding-%03d", index),
			SourceRunID:   overflowEvent.Source.RunID,
			SessionKey:    overflowEvent.Session.Key,
			DetectedAt:    overflowEvent.OccurredAt.Add(time.Duration(index) * time.Millisecond),
			RuleID:        fmt.Sprintf("rule.%03d", index),
			RuleVersion:   "1",
			Severity:      "low",
			SourceAgent:   "codex",
			Confidence:    "high",
			CitedEventIDs: []string{overflowEvent.EventID},
		}); err != nil {
			t.Fatal(err)
		}
	}
	appendAnalysisEvent(t, store, "session-after-overflow", 2)

	report, err := NewReconciler(store).Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Truncated != 1 || report.Current != 1 ||
		report.Occurrences != projectionOriginLimit {
		t.Fatalf("report = %+v", report)
	}
	page, err := store.QueryIssues(ctx, model.IssueQuery{
		Filter: model.IssueFilter{SessionID: overflowEvent.Session.Key},
		Limit:  projectionOriginLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != projectionOriginLimit ||
		page.Analysis.TruncatedSessions != 1 ||
		page.Analysis.CurrentSessions != 1 {
		t.Fatalf("issue page = %+v", page)
	}
}

func TestReconcilerBoundsNumbatFindingInputWithLookAhead(t *testing.T) {
	ctx := context.Background()
	store := openAnalysisStore(t)
	event := appendAnalysisEvent(t, store, "session-numbat-input-overflow", 1)
	for index := 0; index < numbatFindingInputLimit+1; index++ {
		if _, err := store.RecordFinding(ctx, local.Finding{
			FindingID:     fmt.Sprintf("finding-input-%04d", index),
			SourceRunID:   event.Source.RunID,
			SessionKey:    event.Session.Key,
			DetectedAt:    event.OccurredAt,
			RuleID:        "rule.same",
			RuleVersion:   "1",
			Severity:      "low",
			SourceAgent:   "codex",
			Confidence:    "high",
			CitedEventIDs: []string{event.EventID},
		}); err != nil {
			t.Fatal(err)
		}
	}

	reconciler := NewReconciler(store)
	findings, truncated, err := reconciler.sessionFindings(ctx, event.Session.Key)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(findings) != numbatFindingInputLimit {
		t.Fatalf(
			"sessionFindings() = %d/%t, want %d/true",
			len(findings),
			truncated,
			numbatFindingInputLimit,
		)
	}
	if findings[0].FindingID !=
		fmt.Sprintf("finding-input-%04d", numbatFindingInputLimit) {
		t.Fatalf("first finding = %q", findings[0].FindingID)
	}
	if findings[len(findings)-1].FindingID != "finding-input-0001" {
		t.Fatalf("last retained finding = %q, want finding-input-0001", findings[len(findings)-1].FindingID)
	}

	report, err := reconciler.Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Truncated != 1 || report.Current != 0 || report.Occurrences != 1 {
		t.Fatalf("report = %+v", report)
	}
	page, err := store.QueryIssues(ctx, model.IssueQuery{
		Filter: model.IssueFilter{SessionID: event.Session.Key},
		Limit:  10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 ||
		page.Analysis.TruncatedSessions != 1 ||
		page.Analysis.Complete {
		t.Fatalf("issue page = %+v", page)
	}
}

func TestReconcilerMarksTruncatedAtAggregateCitationBudget(t *testing.T) {
	ctx := context.Background()
	store := openAnalysisStore(t)
	sessionID := "session-numbat-citation-budget"
	first := appendAnalysisEvent(t, store, sessionID, 1)
	eventIDs := []string{first.EventID}
	for index := 2; index <= limits.MaxFindingCitedEventIDs; index++ {
		event := first
		event.EventID = fmt.Sprintf("event-%s-%03d", sessionID, index)
		event.OccurredAt = first.OccurredAt.Add(time.Duration(index) * time.Millisecond)
		event.ObservedAt = event.OccurredAt
		event.Source.RecordID = fmt.Sprintf("record-%s-%03d", sessionID, index)
		event.Source.DeduplicationKey = fmt.Sprintf("dedup-%s-%03d", sessionID, index)
		event.Source.Sequence = int64(index)
		if inserted, err := store.AppendEvent(ctx, event); err != nil || !inserted {
			t.Fatalf("AppendEvent(%d) = (%t, %v)", index, inserted, err)
		}
		eventIDs = append(eventIDs, event.EventID)
	}
	findingCount := numbatCitationInputLimit/limits.MaxFindingCitedEventIDs + 1
	for index := 0; index < findingCount; index++ {
		if _, err := store.RecordFinding(ctx, local.Finding{
			FindingID:     fmt.Sprintf("finding-citation-budget-%03d", index),
			SourceRunID:   first.Source.RunID,
			SessionKey:    sessionID,
			DetectedAt:    first.OccurredAt.Add(time.Duration(index) * time.Second),
			RuleID:        "rule.citation-budget",
			RuleVersion:   "1",
			Severity:      "low",
			SourceAgent:   "codex",
			Confidence:    "high",
			CitedEventIDs: append([]string(nil), eventIDs...),
		}); err != nil {
			t.Fatal(err)
		}
	}

	report, err := NewReconciler(store).Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Truncated != 1 || report.Current != 0 || report.Occurrences != 1 {
		t.Fatalf("report = %+v", report)
	}
	page, err := store.QueryIssues(ctx, model.IssueQuery{
		Filter: model.IssueFilter{SessionID: sessionID},
		Limit:  10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 ||
		page.Data[0].AnalysisStatus != model.AnalysisTruncated ||
		page.Analysis.TruncatedSessions != 1 {
		t.Fatalf("issue page = %+v", page)
	}
}

func TestNumbatOccurrencesDefensivelyBoundsLegacyCitationInputs(t *testing.T) {
	reconciler := NewReconciler(openAnalysisStore(t))
	scope := local.SessionScope{
		SessionKey: "session-legacy-citation-budget",
		Quality:    model.ScopeUnscoped,
	}
	base := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)

	oversized := model.FindingSummary{
		FindingID:     "finding-legacy-oversized",
		SessionID:     scope.SessionKey,
		DetectedAt:    base,
		RuleID:        "rule.legacy",
		RuleVersion:   "1",
		Severity:      "low",
		Harness:       "codex",
		Confidence:    "high",
		CitedEventIDs: make([]string, limits.MaxFindingCitedEventIDs+1),
	}
	for index := range oversized.CitedEventIDs {
		oversized.CitedEventIDs[index] = fmt.Sprintf("legacy-event-%03d", index)
	}
	occurrences, truncated, err := reconciler.numbatOccurrences(
		scope,
		nil,
		[]model.FindingSummary{oversized},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(occurrences) != 1 ||
		len(occurrences[0].Evidence.CitedEventIDs) != detection.MaxCitations {
		t.Fatalf("per-finding result = %d/%t citations=%d",
			len(occurrences), truncated, len(occurrences[0].Evidence.CitedEventIDs))
	}

	findingCount := numbatCitationInputLimit/limits.MaxFindingCitedEventIDs + 1
	findings := make([]model.FindingSummary, findingCount)
	for index := range findings {
		citations := make([]string, limits.MaxFindingCitedEventIDs)
		for citation := range citations {
			citations[citation] = fmt.Sprintf(
				"aggregate-event-%03d-%03d",
				index,
				citation,
			)
		}
		findings[index] = model.FindingSummary{
			FindingID:     fmt.Sprintf("finding-aggregate-%03d", index),
			SessionID:     scope.SessionKey,
			DetectedAt:    base.Add(time.Duration(index) * time.Second),
			RuleID:        "rule.aggregate",
			RuleVersion:   "1",
			Severity:      "low",
			Harness:       "codex",
			Confidence:    "high",
			CitedEventIDs: citations,
		}
	}
	occurrences, truncated, err = reconciler.numbatOccurrences(scope, nil, findings)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(occurrences) != 1 {
		t.Fatalf("aggregate result = %d/%t", len(occurrences), truncated)
	}
	wantLast := base.Add(time.Duration(findingCount-2) * time.Second)
	if !occurrences[0].LastObservedAt.Equal(wantLast) {
		t.Fatalf("last observed = %s, want bounded %s",
			occurrences[0].LastObservedAt, wantLast)
	}
}

func TestNumbatFindingScopeHintsPartitionFingerprints(t *testing.T) {
	store := openAnalysisStore(t)
	event := appendAnalysisEvent(t, store, "session-finding-hints", 1)
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
	sessionScope := local.SessionScope{
		SessionKey: event.Session.Key,
		Quality:    model.ScopeUnscoped,
	}
	findings := []model.FindingSummary{
		{
			FindingID:        "finding-hint-a",
			SessionID:        event.Session.Key,
			ProjectScopeHint: firstScope.ID,
			DetectedAt:       event.OccurredAt,
			RuleID:           "rule.same",
			RuleVersion:      "1",
			Severity:         "low",
			Harness:          "codex",
			Confidence:       "high",
			CitedEventIDs:    []string{event.EventID},
		},
		{
			FindingID:        "finding-hint-b",
			SessionID:        event.Session.Key,
			ProjectScopeHint: secondScope.ID,
			DetectedAt:       event.OccurredAt.Add(time.Second),
			RuleID:           "rule.same",
			RuleVersion:      "1",
			Severity:         "low",
			Harness:          "codex",
			Confidence:       "high",
			CitedEventIDs:    []string{event.EventID},
		},
		{
			FindingID:     "finding-session-fallback",
			SessionID:     event.Session.Key,
			DetectedAt:    event.OccurredAt.Add(2 * time.Second),
			RuleID:        "rule.same",
			RuleVersion:   "1",
			Severity:      "low",
			Harness:       "codex",
			Confidence:    "high",
			CitedEventIDs: []string{event.EventID},
		},
	}
	occurrences, truncated, err := NewReconciler(store).numbatOccurrences(
		sessionScope,
		[]model.Event{event},
		findings,
	)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(occurrences) != 3 {
		t.Fatalf("occurrences/truncated = %d/%t, want 3/false", len(occurrences), truncated)
	}
	material := []string{
		"rule_id", "rule.same",
		"rule_version", "1",
		"event_type", "session.start",
	}
	expectedQualities := make(map[string]model.ScopeQuality)
	for _, expectedScope := range []struct {
		identity string
		quality  model.ScopeQuality
	}{
		{identity: firstScope.ID, quality: model.ScopeLexical},
		{identity: secondScope.ID, quality: model.ScopeLexical},
		{identity: event.Session.Key, quality: model.ScopeUnscoped},
	} {
		fingerprintID, _, err := store.DeriveIssueIdentity(
			numbatFingerprintV1,
			numbatDetectorID,
			expectedScope.identity,
			material...,
		)
		if err != nil {
			t.Fatal(err)
		}
		expectedQualities[fingerprintID] = expectedScope.quality
	}
	for _, occurrence := range occurrences {
		wantQuality, ok := expectedQualities[occurrence.FingerprintID]
		if !ok {
			t.Fatalf("unexpected fingerprint %q", occurrence.FingerprintID)
		}
		if occurrence.ScopeQuality != wantQuality {
			t.Fatalf(
				"fingerprint %q quality = %q, want %q",
				occurrence.FingerprintID,
				occurrence.ScopeQuality,
				wantQuality,
			)
		}
		delete(expectedQualities, occurrence.FingerprintID)
	}
	if len(expectedQualities) != 0 {
		t.Fatalf("missing scoped fingerprints = %v", expectedQualities)
	}
}

func TestNumbatFindingScopeHintDoesNotOverrideConflictScope(t *testing.T) {
	store := openAnalysisStore(t)
	event := appendAnalysisEvent(t, store, "session-finding-hint-conflict", 1)
	hintedScope, err := store.DeriveNumbatProjectScopeHash(
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	)
	if err != nil {
		t.Fatal(err)
	}
	sessionScope := local.SessionScope{
		SessionKey: event.Session.Key,
		Quality:    model.ScopeConflict,
	}
	finding := model.FindingSummary{
		FindingID:        "finding-hint-conflict",
		SessionID:        event.Session.Key,
		ProjectScopeHint: hintedScope.ID,
		DetectedAt:       event.OccurredAt,
		RuleID:           "rule.conflict",
		RuleVersion:      "1",
		Severity:         "low",
		Harness:          "codex",
		Confidence:       "high",
		CitedEventIDs:    []string{event.EventID},
	}
	occurrences, truncated, err := NewReconciler(store).numbatOccurrences(
		sessionScope,
		[]model.Event{event},
		[]model.FindingSummary{finding},
	)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(occurrences) != 1 {
		t.Fatalf("occurrences/truncated = %d/%t, want 1/false", len(occurrences), truncated)
	}
	material := []string{
		"rule_id", finding.RuleID,
		"rule_version", finding.RuleVersion,
		"event_type", event.Observation.Type,
	}
	sessionFingerprint, _, err := store.DeriveIssueIdentity(
		numbatFingerprintV1,
		numbatDetectorID,
		event.Session.Key,
		material...,
	)
	if err != nil {
		t.Fatal(err)
	}
	hintedFingerprint, _, err := store.DeriveIssueIdentity(
		numbatFingerprintV1,
		numbatDetectorID,
		hintedScope.ID,
		material...,
	)
	if err != nil {
		t.Fatal(err)
	}
	if occurrences[0].FingerprintID != sessionFingerprint ||
		occurrences[0].FingerprintID == hintedFingerprint ||
		occurrences[0].ScopeQuality != model.ScopeConflict {
		t.Fatalf("conflict occurrence = %+v", occurrences[0])
	}
}

func TestNumbatFindingSourceSignalIsSafeAndDoesNotChangeFingerprint(t *testing.T) {
	store := openAnalysisStore(t)
	event := appendAnalysisEvent(t, store, "session-source-signal", 1)
	scope := local.SessionScope{
		SessionKey: event.Session.Key,
		Quality:    model.ScopeUnscoped,
	}
	base := model.FindingSummary{
		FindingID:     "finding-source-signal",
		SessionID:     event.Session.Key,
		DetectedAt:    event.OccurredAt,
		RuleVersion:   "1",
		Severity:      "low",
		Harness:       "codex",
		Confidence:    "high",
		CitedEventIDs: []string{event.EventID},
	}

	base.RuleID = "tamper.guardrails_off"
	safe, truncated, err := NewReconciler(store).numbatOccurrences(
		scope,
		[]model.Event{event},
		[]model.FindingSummary{base},
	)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(safe) != 1 ||
		safe[0].SourceSignalCode == nil ||
		*safe[0].SourceSignalCode != base.RuleID {
		t.Fatalf("safe source signal occurrence = %+v, truncated=%t", safe, truncated)
	}
	material := []string{
		"rule_id", base.RuleID,
		"rule_version", base.RuleVersion,
		"event_type", event.Observation.Type,
	}
	wantFingerprint, _, err := store.DeriveIssueIdentity(
		numbatFingerprintV1,
		numbatDetectorID,
		event.Session.Key,
		material...,
	)
	if err != nil {
		t.Fatal(err)
	}
	if safe[0].FingerprintID != wantFingerprint {
		t.Fatalf(
			"source signal changed fingerprint = %q, want %q",
			safe[0].FingerprintID,
			wantFingerprint,
		)
	}

	base.FindingID = "finding-source-signal-invalid"
	base.RuleID = "tamper.guardrails_off\ninjected"
	invalid, truncated, err := NewReconciler(store).numbatOccurrences(
		scope,
		[]model.Event{event},
		[]model.FindingSummary{base},
	)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(invalid) != 1 || invalid[0].SourceSignalCode != nil {
		t.Fatalf("invalid source signal occurrence = %+v, truncated=%t", invalid, truncated)
	}
}

func TestReconcilerRejectsStaleGenerationThenConverges(t *testing.T) {
	ctx := context.Background()
	store := openAnalysisStore(t)
	appendAnalysisEvent(t, store, "session-stale", 1)
	reconciler := NewReconciler(store)
	first := true
	reconciler.beforePublish = func(work local.DirtySession) {
		if !first {
			return
		}
		first = false
		if _, err := store.MarkSessionDirty(
			ctx,
			work.SessionKey,
			"test_stale_generation",
		); err != nil {
			t.Errorf("MarkSessionDirty() error = %v", err)
		}
	}
	report, err := reconciler.Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Stale != 1 || report.Current != 1 || report.Claimed != 2 {
		t.Fatalf("report = %+v", report)
	}
	page, err := store.QueryIssues(ctx, model.IssueQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !page.Analysis.Complete || page.Analysis.CurrentSessions != 1 {
		t.Fatalf("analysis coverage = %+v", page.Analysis)
	}
}

func TestStartupRecoversClaimedWorkAfterCrash(t *testing.T) {
	ctx := context.Background()
	store := openAnalysisStore(t)
	appendAnalysisEvent(t, store, "session-crash", 1)
	if _, err := store.ClaimDirtySession(
		ctx,
		time.Date(2026, 9, 8, 20, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimDirtySession(ctx, time.Now()); !errors.Is(err, local.ErrNoDirtySession) {
		t.Fatalf("second claim error = %v, want no ready work", err)
	}

	report, err := NewReconciler(store).Startup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Current != 1 {
		t.Fatalf("startup report = %+v", report)
	}
	page, err := store.QueryIssues(ctx, model.IssueQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !page.Analysis.Complete || page.Analysis.CurrentSessions != 1 {
		t.Fatalf("analysis coverage = %+v", page.Analysis)
	}
}

type failingDetector struct{}

func (failingDetector) ID() string                 { return "test_failing_detector" }
func (failingDetector) Version() string            { return "1" }
func (failingDetector) FingerprintVersion() string { return "1" }
func (failingDetector) Evaluate(
	context.Context,
	detection.SessionInput,
) (detection.DetectorResult, error) {
	return detection.DetectorResult{}, errors.New("private detector failure")
}

type singleMatchDetector struct{}

func (singleMatchDetector) ID() string                 { return "single_match_detector" }
func (singleMatchDetector) Version() string            { return "1" }
func (singleMatchDetector) FingerprintVersion() string { return "1" }
func (singleMatchDetector) Evaluate(
	_ context.Context,
	input detection.SessionInput,
) (detection.DetectorResult, error) {
	event := input.Events[0]
	return detection.DetectorResult{
		AbsenceCapability: detection.AbsenceSupported,
		Matches: []detection.Match{{
			DetectorID:         "single_match_detector",
			DetectorVersion:    "1",
			FingerprintVersion: "1",
			Category:           "test_issue",
			TitleCode:          "issue.test",
			Severity:           "low",
			Confidence:         "high",
			FirstObservedAt:    event.OccurredAt,
			LastObservedAt:     event.OccurredAt,
			CitedEventIDs:      []string{event.EventID},
			EvidenceComplete:   true,
			Fingerprint: []detection.FingerprintDimension{{
				Name:  "session_id",
				Value: input.SessionID,
			}},
		}},
	}, nil
}

type analysisKeyProvider struct {
	key []byte
}

func (provider analysisKeyProvider) Load(context.Context, string) ([]byte, error) {
	return append([]byte(nil), provider.key...), nil
}

func (analysisKeyProvider) Create(context.Context, string) ([]byte, error) {
	return nil, errors.New("test key must already exist")
}

func openAnalysisStore(t *testing.T) *local.Store {
	t.Helper()
	store, err := local.Open(
		filepath.Join(t.TempDir(), "belay.sqlite"),
		analysisKeyProvider{key: bytes.Repeat([]byte{0x31}, 32)},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func appendAnalysisEvent(
	t *testing.T,
	store *local.Store,
	sessionID string,
	sequence int64,
) model.Event {
	return appendAnalysisEventWithAgent(t, store, sessionID, sequence, "codex")
}

func appendAnalysisEventWithAgent(
	t *testing.T,
	store *local.Store,
	sessionID string,
	sequence int64,
	agent string,
) model.Event {
	t.Helper()
	occurredAt := time.Date(2026, 9, 8, 18, 0, int(sequence), 0, time.UTC)
	event := model.Event{
		SchemaVersion:  model.EventSchemaVersion,
		EventID:        "event-" + sessionID,
		InstallationID: "inst_analysis_test",
		OccurredAt:     occurredAt,
		ObservedAt:     occurredAt,
		Source: model.Source{
			Engine:           "numbat",
			EngineVersion:    "test",
			SchemaVersion:    "0.3.0",
			RecordType:       "event",
			RunID:            "run-" + sessionID,
			RecordID:         "record-" + sessionID,
			Kind:             "artifact",
			Agent:            agent,
			AdapterVersion:   "numbat-0.3.0/v1",
			DeduplicationKey: "dedup-" + sessionID,
			Sequence:         sequence,
		},
		Session: model.SessionRef{Key: sessionID},
		Observation: model.Observation{
			Type:    "session.start",
			Actor:   "system",
			Action:  "session",
			Outcome: "unknown",
		},
		Coverage: model.Coverage{
			Depth:      "artifact",
			Confidence: "high",
		},
		Redaction: model.Redaction{PolicyVersion: model.RedactionVersion},
		Historical: model.Historical{
			IsHistorical:         true,
			ReconstructionSource: "test",
		},
	}
	if _, err := store.AppendEventResolved(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	return event
}
