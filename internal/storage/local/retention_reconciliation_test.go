package local_test

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/analysis"
	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

type retainedKeyProvider struct {
	mu  sync.Mutex
	key []byte
}

func (provider *retainedKeyProvider) Load(
	_ context.Context,
	_ string,
) ([]byte, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.key) == 0 {
		return nil, local.ErrKeyNotFound
	}
	return append([]byte(nil), provider.key...), nil
}

func (provider *retainedKeyProvider) Create(
	_ context.Context,
	_ string,
) ([]byte, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.key) != 0 {
		return nil, local.ErrKeyAlreadyExists
	}
	provider.key = bytes.Repeat([]byte{0x53}, 32)
	return append([]byte(nil), provider.key...), nil
}

func TestFindingOnlyPruneBecomesPendingAndConvergesAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "belay.sqlite")
	provider := &retainedKeyProvider{}
	store, err := local.Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	event := retainedTestEvent(now.Add(-time.Hour))
	if _, err := store.AppendEventResolved(ctx, event); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordFinding(ctx, local.Finding{
		FindingID:     "finding-retention-reconcile",
		SourceRunID:   event.Source.RunID,
		SessionKey:    event.Session.Key,
		DetectedAt:    now.Add(-48 * time.Hour),
		RuleID:        "rule.retention",
		RuleVersion:   "1",
		Severity:      "medium",
		SourceAgent:   "codex",
		Confidence:    "high",
		CitedEventIDs: []string{event.EventID},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkSessionDirty(ctx, event.Session.Key, "finding_imported"); err != nil {
		t.Fatal(err)
	}
	if _, err := analysis.NewReconciler(store).Drain(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := store.QueryIssues(ctx, model.IssueQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Data) != 1 ||
		before.Data[0].Origin != "numbat" ||
		before.Data[0].AnalysisStatus != model.AnalysisCurrent {
		t.Fatalf("initial Numbat issue = %+v", before)
	}

	result, err := store.Prune(
		ctx,
		local.RetentionPolicy{MaxAge: 24 * time.Hour},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.PrunedFindingCount != 1 || result.PrunedEventCount != 0 {
		t.Fatalf("finding-only prune = %+v", result)
	}
	pending, err := store.QueryIssues(ctx, model.IssueQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending.Data) != 1 ||
		pending.Data[0].AnalysisStatus != model.AnalysisPending ||
		pending.Analysis.PendingSessions != 1 {
		t.Fatalf("pending retained issue = %+v", pending)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := local.Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	report, err := analysis.NewReconciler(reopened).Startup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Current != 1 {
		t.Fatalf("startup reconciliation report = %+v", report)
	}
	converged, err := reopened.QueryIssues(ctx, model.IssueQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(converged.Data) != 0 ||
		converged.Analysis.CurrentSessions != 1 ||
		!converged.Analysis.Complete {
		t.Fatalf("converged issues = %+v", converged)
	}
}

func retainedTestEvent(occurredAt time.Time) model.Event {
	const eventID = "00000000-0000-7000-8000-000000000181"
	return model.Event{
		SchemaVersion:  model.EventSchemaVersion,
		EventID:        eventID,
		InstallationID: "inst_retention_test",
		OccurredAt:     occurredAt,
		ObservedAt:     occurredAt.Add(time.Second),
		Source: model.Source{
			Engine:           "numbat",
			EngineVersion:    "0.3.0-test",
			SchemaVersion:    "0.3.0",
			RecordType:       "event",
			RunID:            "run-retention-reconcile",
			RecordID:         "source-retention-reconcile",
			Kind:             "artifact",
			Agent:            "codex",
			AdapterVersion:   "numbat-0.3.0/v1",
			DeduplicationKey: fmt.Sprintf("sha256:%064x", 181),
			Sequence:         1,
		},
		Session: model.SessionRef{Key: "session-retention-reconcile"},
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
}
