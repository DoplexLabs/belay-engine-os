package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

var (
	runLegacySemanticProjects = func(
		ctx context.Context,
		store *local.Store,
		harness localapp.SemanticHarness,
	) (localapp.SemanticAnalysisReport, error) {
		return localapp.AnalyzeSemanticProjects(
			ctx,
			store,
			harness,
			localapp.RunInstalledSemanticHarness,
		)
	}
	runHabitDebriefs = func(
		ctx context.Context,
		store *local.Store,
		harness localapp.SemanticHarness,
	) (localapp.HabitAnalysisReport, error) {
		service, err := localapp.NewHabitDebriefService(
			store,
			localapp.WithHabitDebriefPreferredHarness(string(harness)),
		)
		if err != nil {
			return localapp.HabitAnalysisReport{}, err
		}
		return service.AnalyzeHabitSessionsOnce(ctx, 0)
	}
	runExperienceSemanticProjects = func(
		ctx context.Context,
		store *local.Store,
		harness localapp.SemanticHarness,
	) (localapp.ExperienceProjectAnalysisReport, error) {
		return localapp.AnalyzeExperienceProjectsOnce(
			ctx,
			store,
			harness,
			localapp.RunInstalledExperienceSemanticHarness,
		)
	}
)

func runAnalyze(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
) error {
	flags := flag.NewFlagSet("analyze", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeFlags := addLocalRuntimeFlags(flags)
	agent := flags.String(
		"agent",
		"auto",
		"installed harness to use: auto, claude, or codex",
	)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("analyze accepts no positional arguments")
	}
	runtime, err := prepareRuntime(ctx, runtimeFlags)
	if err != nil {
		return err
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	inventory, _, err := runtime.client.Discover(discoveryCtx)
	cancel()
	if err != nil {
		return err
	}
	harness, err := selectSemanticHarness(inventory, *agent)
	if err != nil {
		return err
	}
	store, err := openLocalCommandStore(runtime.paths.Database)
	if err != nil {
		return err
	}
	defer store.Close()
	if _, err := localapp.AnalyzeTranscriptIssuesOnce(ctx, store, 100); err != nil {
		return err
	}
	fmt.Fprintf(
		stderr,
		"belay analyze: Analyzing with your %s\n",
		semanticHarnessDisplayName(harness),
	)
	report, err := runSemanticProjectAnalyses(
		ctx,
		store,
		harness,
	)
	if writeErr := writeJSON(stdout, report); writeErr != nil {
		return writeErr
	}
	return err
}

func runSemanticProjectAnalyses(
	ctx context.Context,
	store *local.Store,
	harness localapp.SemanticHarness,
) (localapp.SemanticAnalysisReport, error) {
	report, legacyErr := runLegacySemanticProjects(ctx, store, harness)
	if ctx.Err() != nil {
		return report, errors.Join(legacyErr, ctx.Err())
	}
	experienceReport, experienceErr := runExperienceSemanticProjects(
		ctx,
		store,
		harness,
	)
	report.AddExperience(experienceReport)
	if ctx.Err() != nil {
		return report, errors.Join(legacyErr, experienceErr, ctx.Err())
	}
	var habitErr error
	if store != nil {
		habitReport, err := runHabitDebriefs(ctx, store, harness)
		report.Habits = &habitReport
		habitErr = err
	}
	return report, errors.Join(legacyErr, experienceErr, habitErr)
}

func selectSemanticHarness(
	inventory numbat.Inventory,
	preferred string,
) (localapp.SemanticHarness, error) {
	preferred = strings.ToLower(strings.TrimSpace(preferred))
	if preferred == "" {
		preferred = "auto"
	}
	var candidates []struct {
		agent   numbat.Agent
		harness localapp.SemanticHarness
	}
	switch preferred {
	case "auto":
		candidates = []struct {
			agent   numbat.Agent
			harness localapp.SemanticHarness
		}{
			{numbat.AgentClaude, localapp.SemanticHarnessClaude},
			{numbat.AgentCodex, localapp.SemanticHarnessCodex},
		}
	case "claude", "claude-code":
		candidates = append(candidates, struct {
			agent   numbat.Agent
			harness localapp.SemanticHarness
		}{numbat.AgentClaude, localapp.SemanticHarnessClaude})
	case "codex":
		candidates = append(candidates, struct {
			agent   numbat.Agent
			harness localapp.SemanticHarness
		}{numbat.AgentCodex, localapp.SemanticHarnessCodex})
	default:
		return "", errors.New("--agent must be auto, claude, or codex")
	}
	for _, candidate := range candidates {
		row, ok := inventory.LaunchTargets[candidate.agent]
		if !ok || (!row.Present && !row.Detected) {
			continue
		}
		if installed, ok := localapp.InstalledSemanticHarness(
			string(candidate.harness),
		); ok {
			return installed, nil
		}
	}
	if preferred == "auto" {
		return "", errors.New(
			"no Claude Code or Codex harness detected by belay agents",
		)
	}
	return "", fmt.Errorf(
		"%s was not detected by belay agents",
		preferred,
	)
}

func semanticHarnessDisplayName(harness localapp.SemanticHarness) string {
	if harness == localapp.SemanticHarnessClaude {
		return "Claude Code"
	}
	return "Codex"
}
