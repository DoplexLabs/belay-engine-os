package local

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func TestIssueAttentionAndExperimentalFilters(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	observedAt := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000901",
		"session-attention-filters",
		1,
		observedAt,
	)
	appended, err := store.AppendEventResolved(ctx, event)
	if err != nil {
		t.Fatal(err)
	}

	makeOccurrence := func(detector, category string, experimental bool) model.IssueOccurrence {
		fingerprint, issueID, err := store.DeriveIssueIdentity(
			"1",
			detector,
			event.Session.Key,
			detector,
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
			"low",
			"high",
			observedAt,
			model.ScopeUnscoped,
		)
		occurrence.Provenance.DetectorID = detector
		occurrence.Category = category
		occurrence.TitleCode = "issue." + detector
		occurrence.Experimental = experimental
		return occurrence
	}
	stable := makeOccurrence("stable_detector", "command_failure", false)
	experimental := makeOccurrence("repeated_command_attempts", "attention", true)
	evidenceGap := makeOccurrence("verification_not_observed", "evidence_gap", false)
	if _, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
		SessionKey:        event.Session.Key,
		ClaimedGeneration: appended.ReadGeneration,
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeUnscoped,
		Occurrences:       []model.IssueOccurrence{stable, experimental, evidenceGap},
	}); err != nil {
		t.Fatal(err)
	}

	defaultPage, err := store.QueryIssues(ctx, model.IssueQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultPage.Data) != 1 ||
		defaultPage.Data[0].IssueID != stable.IssueID ||
		defaultPage.Data[0].Experimental {
		t.Fatalf("default issues = %+v", defaultPage.Data)
	}

	allPage, err := store.QueryIssues(ctx, model.IssueQuery{
		Filter: model.IssueFilter{
			AttentionKind: model.AttentionKindAll,
			Experimental:  model.ExperimentalInclude,
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(allPage.Data) != 3 {
		t.Fatalf("all issues = %+v", allPage.Data)
	}
	experimentalFound := false
	for _, summary := range allPage.Data {
		if summary.IssueID == experimental.IssueID {
			experimentalFound = summary.Experimental
		}
	}
	if !experimentalFound {
		t.Fatalf("experimental summary missing flag: %+v", allPage.Data)
	}

	gapPage, err := store.QueryIssues(ctx, model.IssueQuery{
		Filter: model.IssueFilter{
			AttentionKind: model.AttentionKindEvidenceGap,
			Experimental:  model.ExperimentalStable,
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(gapPage.Data) != 1 || gapPage.Data[0].IssueID != evidenceGap.IssueID {
		t.Fatalf("evidence gaps = %+v", gapPage.Data)
	}

	experimentalPage, err := store.QueryIssues(ctx, model.IssueQuery{
		Filter: model.IssueFilter{
			AttentionKind: model.AttentionKindAll,
			Experimental:  model.ExperimentalOnly,
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(experimentalPage.Data) != 1 ||
		experimentalPage.Data[0].IssueID != experimental.IssueID {
		t.Fatalf("experimental-only issues = %+v", experimentalPage.Data)
	}
	occurrences, err := store.QueryIssueOccurrences(
		ctx,
		model.IssueOccurrenceQuery{
			IssueID: experimental.IssueID,
			Limit:   10,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(occurrences.Data) != 1 || !occurrences.Data[0].Experimental {
		t.Fatalf("experimental occurrence = %+v", occurrences.Data)
	}

	for _, filter := range []model.IssueFilter{
		{AttentionKind: "unsupported"},
		{Experimental: "unsupported"},
	} {
		if _, err := store.QueryIssues(ctx, model.IssueQuery{Filter: filter}); err == nil {
			t.Fatalf("unsupported filter accepted: %+v", filter)
		}
	}
}

func TestIssueSnapshotClockAndErrorIdentity(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 20, 0, 0, 0, time.UTC)
	store, err := OpenWithOptions(
		filepath.Join(t.TempDir(), "belay.sqlite"),
		OpenOptions{
			KeyProvider: newMemoryKeyProvider(),
			Clock:       func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000902",
		"session-snapshot-clock",
		1,
		now.Add(-time.Hour),
	)
	if _, err := store.AppendEventResolved(ctx, event); err != nil {
		t.Fatal(err)
	}

	fresh, err := store.QueryIssues(ctx, model.IssueQuery{})
	if err != nil {
		t.Fatal(err)
	}
	boundary := issueQueryForSnapshot(
		t, store, fresh.Snapshot, now.Add(-issueCursorLifetime),
	)
	if _, err := store.QueryIssues(ctx, boundary); err != nil {
		t.Fatalf("boundary snapshot error = %v", err)
	}
	expired := issueQueryForSnapshot(
		t,
		store,
		fresh.Snapshot,
		now.Add(-issueCursorLifetime-time.Nanosecond),
	)
	if _, err := store.QueryIssues(ctx, expired); !errors.Is(err, model.ErrIssueSnapshotExpired) ||
		!errors.Is(err, ErrIssueSnapshotExpired) {
		t.Fatalf("expired snapshot error = %v", err)
	}
	future := issueQueryForSnapshot(
		t, store, fresh.Snapshot, now.Add(time.Nanosecond),
	)
	if _, err := store.QueryIssues(ctx, future); !errors.Is(err, model.ErrIssueSnapshotInvalid) ||
		!errors.Is(err, ErrIssueSnapshotInvalid) {
		t.Fatalf("future snapshot error = %v", err)
	}
	newer := issueQueryForSnapshot(t, store, fresh.Snapshot+1, now)
	if _, err := store.QueryIssues(ctx, newer); !errors.Is(err, model.ErrIssueSnapshotInvalid) {
		t.Fatalf("newer-generation snapshot error = %v", err)
	}
	negative := issueQueryForSnapshot(t, store, -1, now)
	if _, err := store.QueryIssues(ctx, negative); !errors.Is(err, model.ErrIssueSnapshotInvalid) {
		t.Fatalf("negative-generation snapshot error = %v", err)
	}
	missingIssuedAt := issueQueryForSnapshot(t, store, fresh.Snapshot, now)
	missingIssuedAt.IssuedAt = time.Time{}
	if _, err := store.QueryIssues(ctx, missingIssuedAt); !errors.Is(
		err,
		model.ErrIssueSnapshotInvalid,
	) {
		t.Fatalf("missing-issued-at snapshot error = %v", err)
	}
}

func TestIssueSummaryAggregatesExperimentalFlag(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 8, 20, 30, 0, 0, time.UTC)
	fingerprint, issueID, err := store.DeriveIssueIdentity(
		"1",
		"mixed_experimental_detector",
		"shared_scope",
		"fixed",
	)
	if err != nil {
		t.Fatal(err)
	}
	for index, experimental := range []bool{false, true} {
		event := storageTestEvent(
			[]string{
				"00000000-0000-7000-8000-000000000903",
				"00000000-0000-7000-8000-000000000904",
			}[index],
			[]string{"session-experimental-a", "session-experimental-b"}[index],
			1,
			base.Add(time.Duration(index)*time.Minute),
		)
		if _, err := store.AppendEventResolved(ctx, event); err != nil {
			t.Fatal(err)
		}
		occurrence := testIssueOccurrence(
			event.Session.Key,
			"codex",
			event.EventID,
			fingerprint,
			issueID,
			"low",
			"high",
			event.OccurredAt,
			model.ScopeUnscoped,
		)
		occurrence.Provenance.DetectorID = "mixed_experimental_detector"
		occurrence.Experimental = experimental
		if _, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
			SessionKey:        event.Session.Key,
			ClaimedGeneration: 1,
			Status:            model.AnalysisCurrent,
			ScopeQuality:      model.ScopeUnscoped,
			Occurrences:       []model.IssueOccurrence{occurrence},
		}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.QueryIssues(ctx, model.IssueQuery{
		Filter: model.IssueFilter{
			AttentionKind: model.AttentionKindAll,
			Experimental:  model.ExperimentalInclude,
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 ||
		page.Data[0].OccurrenceCount != 2 ||
		!page.Data[0].Experimental {
		t.Fatalf("experimental aggregation = %+v", page.Data)
	}
}
