package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

func TestRunSemanticProjectAnalysesRunsBothBranchesAndAggregatesErrors(
	t *testing.T,
) {
	previousLegacy := runLegacySemanticProjects
	previousExperience := runExperienceSemanticProjects
	t.Cleanup(func() {
		runLegacySemanticProjects = previousLegacy
		runExperienceSemanticProjects = previousExperience
	})
	legacyErr := errors.New("legacy failed")
	experienceErr := errors.New("experience failed")
	var calls []string
	runLegacySemanticProjects = func(
		context.Context,
		*local.Store,
		localapp.SemanticHarness,
	) (localapp.SemanticAnalysisReport, error) {
		calls = append(calls, "legacy")
		return localapp.SemanticAnalysisReport{
			Projects: 2,
			Clusters: 3,
			Fixes:    4,
		}, legacyErr
	}
	runExperienceSemanticProjects = func(
		context.Context,
		*local.Store,
		localapp.SemanticHarness,
	) (localapp.ExperienceProjectAnalysisReport, error) {
		calls = append(calls, "experience")
		return localapp.ExperienceProjectAnalysisReport{
			ProjectsConsidered: 5,
			ProjectsCompiled:   4,
			ProjectsAnalyzed:   3,
			ProjectFailures:    2,
			CandidatesInserted: 7,
			CandidatesReplayed: 8,
			Proposals:          9,
			Rejections:         10,
			Defers:             11,
		}, experienceErr
	}
	report, err := runSemanticProjectAnalyses(
		context.Background(),
		nil,
		localapp.SemanticHarnessCodex,
	)
	if got, want := strings.Join(calls, ","), "legacy,experience"; got != want {
		t.Fatalf("analysis calls = %q, want %q", got, want)
	}
	if !errors.Is(err, legacyErr) || !errors.Is(err, experienceErr) {
		t.Fatalf("joined analysis error = %v", err)
	}
	if report.Projects != 2 ||
		report.Clusters != 3 ||
		report.Fixes != 4 ||
		report.ExperienceProjectsConsidered != 5 ||
		report.ExperienceProjectsCompiled != 4 ||
		report.ExperienceProjectsAnalyzed != 3 ||
		report.ExperienceProjectFailures != 2 ||
		report.ExperienceCandidatesInserted != 7 ||
		report.ExperienceCandidatesReplayed != 8 ||
		report.ExperienceProposals != 9 ||
		report.ExperienceRejections != 10 ||
		report.ExperienceDefers != 11 {
		t.Fatalf("combined semantic report = %+v", report)
	}
}

func TestSelectSemanticHarnessHonorsDetectionAndPreference(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		path := filepath.Join(bin, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	inventory := numbat.Inventory{
		LaunchTargets: map[numbat.Agent]numbat.InventoryRow{
			numbat.AgentClaude: {
				Agent:    "claude",
				Present:  true,
				Detected: true,
			},
			numbat.AgentCodex: {
				Agent:    "codex",
				Present:  true,
				Detected: true,
			},
		},
	}
	harness, err := selectSemanticHarness(inventory, "auto")
	if err != nil || harness != localapp.SemanticHarnessClaude {
		t.Fatalf("auto harness/error = %q/%v", harness, err)
	}
	harness, err = selectSemanticHarness(inventory, "codex")
	if err != nil || harness != localapp.SemanticHarnessCodex {
		t.Fatalf("Codex harness/error = %q/%v", harness, err)
	}
	if _, err := selectSemanticHarness(inventory, "cursor"); err == nil {
		t.Fatal("unsupported semantic harness was accepted")
	}
}

func TestQuickstartAnalyzeFlagsReachLaunchOptions(t *testing.T) {
	previous := launchLocal
	defer func() { launchLocal = previous }()
	var captured localLaunchOptions
	launchLocal = func(
		_ context.Context,
		options localLaunchOptions,
		_, _ io.Writer,
	) error {
		captured = options
		return nil
	}
	var stdout, stderr bytes.Buffer
	if err := runQuickstart(
		context.Background(),
		[]string{
			"--no-analyze",
			"--analyze-agent",
			"codex",
			"--no-open",
		},
		&stdout,
		&stderr,
	); err != nil {
		t.Fatal(err)
	}
	if captured.analyze ||
		captured.analyzeAgent != "codex" ||
		captured.openBrowser {
		t.Fatalf("quickstart options = %+v", captured)
	}
}
