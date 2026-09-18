package localapp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultIntelligenceLeaseRetry = 2 * time.Second
	intelligenceLeaseFileName     = "intelligence.lock"
)

type intelligenceLease interface {
	Release() error
}

type IntelligenceRuntimeOptions struct {
	RetryInterval time.Duration
	RunOwner      func(context.Context) error
	OnError       func(error)
}

type IntelligenceReadiness struct {
	Lease     string   `json:"lease"`
	Ownership string   `json:"ownership"`
	Workers   []string `json:"workers"`
}

func StartIntelligenceRuntime(
	ctx context.Context,
	home string,
	options IntelligenceRuntimeOptions,
) (context.CancelFunc, <-chan struct{}) {
	if ctx == nil {
		ctx = context.Background()
	}
	runtimeCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runIntelligenceRuntime(runtimeCtx, home, options)
	}()
	return cancel, done
}

func InspectIntelligenceReadiness(home string) IntelligenceReadiness {
	readiness := IntelligenceReadiness{
		Lease:     "unavailable",
		Ownership: "not_reported",
		Workers: []string{
			"recent_transcripts",
			"cost_issue_analysis",
		},
	}
	path, err := prepareIntelligenceLeasePath(home)
	if err != nil {
		return readiness
	}
	lease, acquired, err := tryAcquireIntelligenceLease(path)
	if err != nil {
		return readiness
	}
	if !acquired {
		readiness.Lease = "contended"
		return readiness
	}
	if err := lease.Release(); err != nil {
		return readiness
	}
	readiness.Lease = "available"
	return readiness
}

func runIntelligenceRuntime(
	ctx context.Context,
	home string,
	options IntelligenceRuntimeOptions,
) {
	if options.RunOwner == nil {
		return
	}
	retryInterval := options.RetryInterval
	if retryInterval <= 0 {
		retryInterval = defaultIntelligenceLeaseRetry
	}
	path, pathErr := prepareIntelligenceLeasePath(home)
	leaseFailureReported := false
	ownerFailureReported := false
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if pathErr != nil {
			if !leaseFailureReported && options.OnError != nil {
				options.OnError(pathErr)
				leaseFailureReported = true
			}
			if !waitIntelligenceRetry(ctx, retryInterval) {
				return
			}
			path, pathErr = prepareIntelligenceLeasePath(home)
			continue
		}

		lease, acquired, err := tryAcquireIntelligenceLease(path)
		if err != nil {
			if !leaseFailureReported && options.OnError != nil {
				options.OnError(err)
				leaseFailureReported = true
			}
			if !waitIntelligenceRetry(ctx, retryInterval) {
				return
			}
			continue
		}
		if !acquired {
			if !waitIntelligenceRetry(ctx, retryInterval) {
				return
			}
			continue
		}

		leaseFailureReported = false
		ownerErr := options.RunOwner(ctx)
		releaseErr := lease.Release()
		if ctx.Err() != nil {
			return
		}
		if err := errors.Join(ownerErr, releaseErr); err != nil &&
			!ownerFailureReported &&
			options.OnError != nil {
			options.OnError(err)
			ownerFailureReported = true
		}
		if !waitIntelligenceRetry(ctx, retryInterval) {
			return
		}
	}
}

func prepareIntelligenceLeasePath(home string) (string, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return "", errors.New("Belay home is required for intelligence lease")
	}
	if err := ensurePrivateDirectory(home); err != nil {
		return "", err
	}
	return filepath.Join(home, intelligenceLeaseFileName), nil
}

func waitIntelligenceRetry(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
