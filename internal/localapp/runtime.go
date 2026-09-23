package localapp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
	"github.com/DoplexLabs/belay-engine/internal/analysis"
	"github.com/DoplexLabs/belay-engine/internal/initialization"
	"github.com/DoplexLabs/belay-engine/internal/pipeline"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

type HarnessScan struct {
	Agent    string          `json:"agent"`
	Detected bool            `json:"detected"`
	Import   pipeline.Report `json:"import"`
	ExitCode int             `json:"exit_code"`
	Error    string          `json:"error,omitempty"`
}

type HookResult struct {
	Agent    string `json:"agent"`
	Action   string `json:"action"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// AgentSpool maps a supported Numbat launch agent onto its private live spool.
// An unsupported agent yields an empty path so callers skip it rather than
// writing hook output somewhere unowned.
func AgentSpool(paths Paths, agent numbat.Agent) string {
	switch agent {
	case numbat.AgentCodex:
		return paths.CodexSpool
	case numbat.AgentClaude:
		return paths.ClaudeSpool
	case numbat.AgentCursor:
		return paths.CursorSpool
	case numbat.AgentAntigravity:
		return paths.AntigravitySpool
	default:
		return ""
	}
}

func StartHistoricalInitialization(
	ctx context.Context,
	tracker *initialization.Tracker,
	scan func(context.Context) error,
) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if tracker == nil || scan == nil || ctx.Err() != nil {
			return
		}
		err := scan(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			tracker.MarkHistoricalScanIncomplete()
			return
		}
		tracker.MarkReady()
	}()
	return done
}

func DiscoverAndScan(
	ctx context.Context,
	client *numbat.Client,
	store *local.Store,
	config Config,
) (numbat.Inventory, []HarnessScan, error) {
	if client == nil || store == nil {
		return numbat.Inventory{}, nil, fmt.Errorf("Local scan requires Numbat and the Local store")
	}
	discoveryCtx, cancelDiscovery := context.WithTimeout(ctx, 30*time.Second)
	inventory, _, err := client.Discover(discoveryCtx)
	cancelDiscovery()
	if err != nil {
		return numbat.Inventory{}, nil, err
	}
	var reports []HarnessScan
	var scanErrors []error
	for _, agent := range numbat.SupportedAgents() {
		row, present := inventory.LaunchTargets[agent]
		if !present || !row.Present {
			continue
		}
		if !agent.HistoricalScanSupported() {
			// Hook-only harnesses (Antigravity) have no at-rest artifacts the
			// pinned Numbat can scan, and asking would fail before launch. The
			// harness still gets a report row so callers see it was detected;
			// the zero Import and exit code 0 say there was nothing to scan
			// rather than that a scan failed. Its evidence arrives through the
			// live hook spool instead.
			reports = append(reports, HarnessScan{
				Agent:    agent.String(),
				Detected: true,
				ExitCode: 0,
			})
			continue
		}
		report := HarnessScan{Agent: agent.String(), Detected: true, ExitCode: -1}
		scanCtx, cancelScan := context.WithTimeout(ctx, 15*time.Minute)
		scan, startErr := client.StartHistoricalScan(scanCtx, agent)
		if startErr != nil {
			cancelScan()
			report.Error = "could not start historical scan"
			scanErrors = append(scanErrors, fmt.Errorf("%s historical scan failed", agent.String()))
			reports = append(reports, report)
			continue
		}
		engineVersion := config.NumbatVersionMarker
		if engineVersion == "" {
			engineVersion = numbat.ResearchCommit
		}
		report.Import, err = pipeline.New(store, config.InstallationID, engineVersion).Import(scanCtx, scan.Stdout)
		_ = scan.Stdout.Close()
		command, waitErr := scan.Wait()
		if err == nil {
			_, _ = analysis.NewReconciler(store).Drain(scanCtx)
		}
		cancelScan()
		report.ExitCode = command.ExitCode
		switch {
		case err != nil:
			report.Error = "Belay could not import the historical scan"
			scanErrors = append(scanErrors, fmt.Errorf("%s historical import failed", agent.String()))
		case waitErr != nil:
			report.Error = "Numbat historical scan did not complete cleanly"
			scanErrors = append(scanErrors, fmt.Errorf("%s historical scan failed", agent.String()))
		}
		reports = append(reports, report)
	}
	return inventory, reports, errors.Join(scanErrors...)
}

func ImportLive(
	ctx context.Context,
	paths Paths,
	store *local.Store,
	config Config,
) ([]TailResult, error) {
	engineVersion := config.NumbatVersionMarker
	if engineVersion == "" {
		engineVersion = numbat.ResearchCommit
	}
	type source struct {
		agent  string
		spool  string
		cursor string
	}
	supported := numbat.SupportedAgents()
	sources := make([]source, 0, len(supported))
	for _, agent := range supported {
		spool := AgentSpool(paths, agent)
		if spool == "" {
			continue
		}
		sources = append(sources, source{
			agent: agent.String(),
			spool: spool,
			cursor: filepath.Join(
				paths.Root,
				"live",
				agent.String()+".cursor.json",
			),
		})
	}
	results := make([]TailResult, 0, len(sources))
	var importErrors []error
	reconciler := analysis.NewReconciler(store)
	for _, item := range sources {
		if err := ctx.Err(); err != nil {
			importErrors = append(importErrors, err)
			break
		}
		result, err := ImportSpoolOnceAfterCheckpoint(
			ctx,
			item.spool,
			item.cursor,
			func(sequenceBase int64) StreamImporter {
				return pipeline.New(store, config.InstallationID, engineVersion).
					WithSequenceBase(sequenceBase)
			},
			func(callbackCtx context.Context, _ TailResult) {
				_, _ = reconciler.Drain(callbackCtx)
			},
		)
		results = append(results, result)
		if err != nil {
			importErrors = append(importErrors, fmt.Errorf("%s live import: %w", item.agent, err))
		}
	}
	// Retry durable failed/pending analysis even when no spool advanced during
	// this poll. Newly imported live work has already crossed its cursor save.
	if ctx.Err() == nil {
		_, _ = reconciler.Drain(ctx)
	}
	return results, errors.Join(importErrors...)
}

func PollLive(
	ctx context.Context,
	paths Paths,
	store *local.Store,
	config Config,
	interval time.Duration,
	onError func(error),
) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := ImportLive(ctx, paths, store, config); err != nil && onError != nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func ManageHooks(
	ctx context.Context,
	client *numbat.Client,
	paths Paths,
	action string,
) ([]HookResult, error) {
	type target struct {
		agent numbat.Agent
		spool string
	}
	supported := numbat.SupportedAgents()
	targets := make([]target, 0, len(supported))
	for _, agent := range supported {
		spool := AgentSpool(paths, agent)
		if spool == "" {
			continue
		}
		targets = append(targets, target{agent: agent, spool: spool})
	}
	results := make([]HookResult, 0, len(targets))
	if action == "install" {
		discoveryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		inventory, _, err := client.Discover(discoveryCtx)
		cancel()
		if err != nil {
			return results, errors.New("hook target discovery failed")
		}
		detected := targets[:0]
		for _, item := range targets {
			row, present := inventory.LaunchTargets[item.agent]
			if present && row.Detected {
				detected = append(detected, item)
			}
		}
		targets = detected
	}
	var hookErrors []error
	for _, item := range targets {
		result := HookResult{Agent: item.agent.String(), Action: action, ExitCode: -1}
		var command numbat.CommandResult
		var err error
		commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		switch action {
		case "install":
			err = PrepareSpool(item.spool)
			if err == nil {
				command, err = client.InstallMonitorHook(commandCtx, item.agent, item.spool)
			}
		case "status":
			command, err = client.MonitorHookStatus(commandCtx, item.agent)
		case "uninstall":
			command, err = client.UninstallMonitorHook(commandCtx, item.agent)
		default:
			err = fmt.Errorf("unknown hook action")
		}
		cancel()
		result.ExitCode = command.ExitCode
		if err != nil {
			result.Error = "hook operation failed"
			hookErrors = append(hookErrors, fmt.Errorf("%s hook %s failed", item.agent.String(), action))
		}
		results = append(results, result)
	}
	return results, errors.Join(hookErrors...)
}
