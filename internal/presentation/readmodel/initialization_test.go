package readmodel

import (
	"context"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/initialization"
)

func TestInitializationAndStatsShareProviderSnapshot(t *testing.T) {
	startedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	tracker := initialization.NewTracker(true, func() time.Time { return startedAt })
	service := New(
		issueTestCoreRepository{},
		WithInitializationProvider(tracker),
	)

	status, err := service.GetInitialization()
	if err != nil {
		t.Fatal(err)
	}
	stats, err := service.GetStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.SchemaVersion != initialization.SchemaVersion ||
		status.Initialization.State != initialization.StateInitializing ||
		stats.Initialization == nil ||
		stats.Initialization.State != status.Initialization.State ||
		!stats.Initialization.StartedAt.Equal(status.Initialization.StartedAt) ||
		stats.Initialization.CompletedAt != nil ||
		stats.Initialization.ErrorCode != nil {
		t.Fatalf("initialization=%+v stats=%+v", status, stats.Initialization)
	}

	tracker.MarkReady()
	stats, err = service.GetStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Initialization == nil ||
		stats.Initialization.State != initialization.StateReady ||
		stats.Initialization.CompletedAt == nil {
		t.Fatalf("ready stats initialization = %+v", stats.Initialization)
	}
}

func TestStatsWithoutInitializationProviderRemainCompatible(t *testing.T) {
	stats, err := New(issueTestCoreRepository{}).GetStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Initialization != nil {
		t.Fatalf("legacy stats initialization = %+v, want null", stats.Initialization)
	}
	if _, err := New(issueTestCoreRepository{}).GetInitialization(); err == nil {
		t.Fatal("initialization read without provider succeeded")
	}
}
