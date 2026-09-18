package localapp

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestIntelligenceRuntimeMutualExclusionAndTakeover(t *testing.T) {
	home := t.TempDir()
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	var active atomic.Int32
	var overlap atomic.Bool

	firstCtx, cancelFirstCtx := context.WithCancel(context.Background())
	stopFirst, firstDone := StartIntelligenceRuntime(
		firstCtx,
		home,
		IntelligenceRuntimeOptions{
			RetryInterval: 10 * time.Millisecond,
			RunOwner: func(ctx context.Context) error {
				if active.Add(1) != 1 {
					overlap.Store(true)
				}
				close(firstStarted)
				<-ctx.Done()
				active.Add(-1)
				return ctx.Err()
			},
		},
	)
	defer stopFirst()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first runtime did not acquire the lease")
	}
	contended := InspectIntelligenceReadiness(home)
	if contended.Lease != "contended" ||
		contended.Ownership != "not_reported" {
		t.Fatalf("contended readiness = %+v", contended)
	}

	startedAt := time.Now()
	secondCtx, cancelSecondCtx := context.WithCancel(context.Background())
	stopSecond, secondDone := StartIntelligenceRuntime(
		secondCtx,
		home,
		IntelligenceRuntimeOptions{
			RetryInterval: 10 * time.Millisecond,
			RunOwner: func(ctx context.Context) error {
				if active.Add(1) != 1 {
					overlap.Store(true)
				}
				close(secondStarted)
				<-ctx.Done()
				active.Add(-1)
				return ctx.Err()
			},
		},
	)
	defer stopSecond()
	if time.Since(startedAt) > 100*time.Millisecond {
		t.Fatal("contending runtime startup blocked")
	}
	select {
	case <-secondStarted:
		t.Fatal("second runtime owned the lease concurrently")
	case <-time.After(50 * time.Millisecond):
	}

	cancelFirstCtx()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first runtime did not release the lease")
	}
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second runtime did not take over the released lease")
	}
	if overlap.Load() {
		t.Fatal("intelligence owners overlapped")
	}

	cancelSecondCtx()
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second runtime did not release the lease")
	}
	readiness := InspectIntelligenceReadiness(home)
	if readiness.Lease != "available" ||
		readiness.Ownership != "not_reported" {
		t.Fatalf("post-release readiness = %+v", readiness)
	}
}
