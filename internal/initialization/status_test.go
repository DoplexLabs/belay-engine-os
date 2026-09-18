package initialization

import (
	"sync"
	"testing"
	"time"
)

func TestTrackerInitialAndTerminalStates(t *testing.T) {
	startedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(time.Minute)
	times := []time.Time{startedAt, completedAt}
	index := 0
	clock := func() time.Time {
		value := times[index]
		if index < len(times)-1 {
			index++
		}
		return value
	}

	tracker := NewTracker(true, clock)
	initial := tracker.InitializationStatus()
	if !Valid(initial) ||
		initial.State != StateInitializing ||
		initial.CompletedAt != nil ||
		initial.ErrorCode != nil {
		t.Fatalf("initial status = %+v", initial)
	}
	tracker.MarkReady()
	ready := tracker.InitializationStatus()
	if !Valid(ready) ||
		ready.State != StateReady ||
		ready.CompletedAt == nil ||
		!ready.CompletedAt.Equal(completedAt) ||
		ready.ErrorCode != nil {
		t.Fatalf("ready status = %+v", ready)
	}
	tracker.MarkHistoricalScanIncomplete()
	if got := tracker.InitializationStatus(); got.State != StateReady {
		t.Fatalf("terminal status changed = %+v", got)
	}
}

func TestTrackerDegradedStatusUsesFixedPayloadFreeCode(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	tracker := NewTracker(true, func() time.Time {
		now = now.Add(time.Second)
		return now
	})
	tracker.MarkHistoricalScanIncomplete()
	status := tracker.InitializationStatus()
	if !Valid(status) ||
		status.State != StateDegraded ||
		status.ErrorCode == nil ||
		*status.ErrorCode != ErrorHistoricalScanIncomplete {
		t.Fatalf("degraded status = %+v", status)
	}
}

func TestTrackerWithoutHistoricalScanStartsReady(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	status := NewTracker(false, func() time.Time { return now }).InitializationStatus()
	if !Valid(status) ||
		status.State != StateReady ||
		status.CompletedAt == nil ||
		!status.CompletedAt.Equal(now) {
		t.Fatalf("no-scan status = %+v", status)
	}
}

func TestTrackerConcurrentReadsAndCompletion(t *testing.T) {
	tracker := NewTracker(true, time.Now)
	var wait sync.WaitGroup
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for read := 0; read < 100; read++ {
				_ = tracker.InitializationStatus()
			}
		}()
	}
	tracker.MarkReady()
	wait.Wait()
	if status := tracker.InitializationStatus(); !Valid(status) || status.State != StateReady {
		t.Fatalf("concurrent status = %+v", status)
	}
}
