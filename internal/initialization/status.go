// Package initialization owns the process-local, payload-free status of
// Belay's asynchronous historical initialization.
package initialization

import (
	"sync"
	"time"
)

const (
	SchemaVersion = "belay.initialization.v1"

	StateInitializing = "initializing"
	StateReady        = "ready"
	StateDegraded     = "degraded"

	ErrorHistoricalScanIncomplete = "historical_scan_incomplete"
)

type Status struct {
	State       string     `json:"state"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	ErrorCode   *string    `json:"error_code"`
}

type Provider interface {
	InitializationStatus() Status
}

type Tracker struct {
	mu     sync.RWMutex
	status Status
	now    func() time.Time
}

func NewTracker(historicalScanRequested bool, clock func() time.Time) *Tracker {
	if clock == nil {
		clock = time.Now
	}
	now := clock().UTC()
	status := Status{
		State:     StateInitializing,
		StartedAt: now,
	}
	if !historicalScanRequested {
		completedAt := now
		status.State = StateReady
		status.CompletedAt = &completedAt
	}
	return &Tracker{status: status, now: clock}
}

func (t *Tracker) InitializationStatus() Status {
	if t == nil {
		return Status{}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return cloneStatus(t.status)
}

func (t *Tracker) MarkReady() {
	t.complete(StateReady, nil)
}

func (t *Tracker) MarkHistoricalScanIncomplete() {
	code := ErrorHistoricalScanIncomplete
	t.complete(StateDegraded, &code)
}

func (t *Tracker) complete(state string, errorCode *string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status.State != StateInitializing {
		return
	}
	completedAt := t.now().UTC()
	t.status.State = state
	t.status.CompletedAt = &completedAt
	t.status.ErrorCode = cloneString(errorCode)
}

func Valid(status Status) bool {
	if status.StartedAt.IsZero() {
		return false
	}
	switch status.State {
	case StateInitializing:
		return status.CompletedAt == nil && status.ErrorCode == nil
	case StateReady:
		return status.CompletedAt != nil &&
			!status.CompletedAt.Before(status.StartedAt) &&
			status.ErrorCode == nil
	case StateDegraded:
		return status.CompletedAt != nil &&
			!status.CompletedAt.Before(status.StartedAt) &&
			status.ErrorCode != nil &&
			*status.ErrorCode == ErrorHistoricalScanIncomplete
	default:
		return false
	}
}

func cloneStatus(status Status) Status {
	status.CompletedAt = cloneTime(status.CompletedAt)
	status.ErrorCode = cloneString(status.ErrorCode)
	return status
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
