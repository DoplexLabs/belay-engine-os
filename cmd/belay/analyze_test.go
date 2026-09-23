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

// writeFakeExecutable places an executable shell stub named name in dir.
func writeFakeExecutable(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

// isolateSemanticHarnessHome points HOME at an empty directory so the
// Cursor CLI and Antigravity CLI marker directories under it are absent
// unless a test creates them, regardless of what the developer machine has.
func isolateSemanticHarnessHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestSelectSemanticHarnessHonorsDetectionAndPreference(t *testing.T) {
	isolateSemanticHarnessHome(t)
	bin := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		writeFakeExecutable(t, bin, name)
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
			numbat.AgentCursor: {
				Agent:    "cursor",
				Present:  true,
				Detected: true,
			},
			numbat.AgentAntigravity: {
				Agent:    "Antigravity",
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
	harness, err = selectSemanticHarness(inventory, "claude-code")
	if err != nil || harness != localapp.SemanticHarnessClaude {
		t.Fatalf("claude-code harness/error = %q/%v", harness, err)
	}
	// A detected Cursor or Antigravity IDE row says nothing about the
	// CLI; without the CLI on PATH an explicit request must name what is
	// missing, and auto must skip them.
	_, err = selectSemanticHarness(inventory, "cursor")
	if err == nil ||
		!strings.Contains(err.Error(), "Cursor CLI was not detected") {
		t.Fatalf("cursor rejection = %v, want Cursor CLI not detected", err)
	}
	_, err = selectSemanticHarness(inventory, "antigravity")
	if err == nil ||
		!strings.Contains(err.Error(), "Antigravity CLI was not detected") {
		t.Fatalf(
			"antigravity rejection = %v, want Antigravity CLI not detected",
			err,
		)
	}
	_, err = selectSemanticHarness(inventory, "windsurf")
	if err == nil || !strings.Contains(
		err.Error(),
		"--agent must be auto, claude, codex, cursor, or antigravity",
	) {
		t.Fatalf("unknown harness rejection = %v", err)
	}
}

func TestSelectSemanticHarnessAutoReportsNoHarness(t *testing.T) {
	isolateSemanticHarnessHome(t)
	t.Setenv("PATH", t.TempDir())
	inventory := numbat.Inventory{
		LaunchTargets: map[numbat.Agent]numbat.InventoryRow{
			numbat.AgentClaude: {Agent: "claude", Present: true, Detected: true},
			numbat.AgentCodex:  {Agent: "codex", Present: true, Detected: true},
		},
	}
	_, err := selectSemanticHarness(inventory, "auto")
	if !errors.Is(err, errNoSemanticHarness) {
		t.Fatalf("auto without harnesses = %v, want errNoSemanticHarness", err)
	}
	_, err = selectSemanticHarness(inventory, "claude")
	if err == nil ||
		!strings.Contains(err.Error(), "Claude Code was not detected") {
		t.Fatalf("claude rejection = %v, want Claude Code not detected", err)
	}
	// An inventory row alone never selects Claude or Codex: the harness
	// must also be detected on PATH.
	if _, err := selectSemanticHarness(inventory, "codex"); err == nil {
		t.Fatal("Codex selected without a codex executable on PATH")
	}
}

func TestSelectSemanticHarnessPicksCursorCLIWithoutInventoryRow(t *testing.T) {
	home := isolateSemanticHarnessHome(t)
	bin := t.TempDir()
	// The Cursor CLI ships as `agent` alongside a real ~/.cursor directory.
	writeFakeExecutable(t, bin, "agent")
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	// No Cursor row at all: the CLI installs independently of the IDE, so
	// the Numbat inventory must not gate it.
	inventory := numbat.Inventory{
		LaunchTargets: map[numbat.Agent]numbat.InventoryRow{
			numbat.AgentClaude: {Agent: "claude", Present: true, Detected: true},
			numbat.AgentCodex:  {Agent: "codex", Present: true, Detected: true},
		},
	}
	for _, preferred := range []string{"auto", "cursor", "cursor-agent"} {
		harness, err := selectSemanticHarness(inventory, preferred)
		if err != nil || harness != localapp.SemanticHarnessCursor {
			t.Fatalf(
				"--agent %s harness/error = %q/%v, want cursor",
				preferred,
				harness,
				err,
			)
		}
	}
	_, err := selectSemanticHarness(inventory, "antigravity")
	if err == nil ||
		!strings.Contains(err.Error(), "Antigravity CLI was not detected") {
		t.Fatalf("antigravity rejection = %v", err)
	}
	// Claude and Codex still outrank the Cursor CLI when detected.
	writeFakeExecutable(t, bin, "codex")
	harness, err := selectSemanticHarness(inventory, "auto")
	if err != nil || harness != localapp.SemanticHarnessCodex {
		t.Fatalf("auto with codex harness/error = %q/%v", harness, err)
	}
}

func TestSelectSemanticHarnessPicksAntigravityCLIWithoutInventoryRow(
	t *testing.T,
) {
	home := isolateSemanticHarnessHome(t)
	bin := t.TempDir()
	// The Antigravity CLI is `agy` on PATH (a plain executable, not the
	// IDE's launcher) alongside a real ~/.gemini/antigravity-cli directory.
	writeFakeExecutable(t, bin, "agy")
	if err := os.MkdirAll(
		filepath.Join(home, ".gemini", "antigravity-cli"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	inventory := numbat.Inventory{
		LaunchTargets: map[numbat.Agent]numbat.InventoryRow{
			numbat.AgentClaude: {Agent: "claude", Present: true, Detected: true},
			numbat.AgentCodex:  {Agent: "codex", Present: true, Detected: true},
		},
	}
	for _, preferred := range []string{"auto", "antigravity", "agy"} {
		harness, err := selectSemanticHarness(inventory, preferred)
		if err != nil || harness != localapp.SemanticHarnessAntigravity {
			t.Fatalf(
				"--agent %s harness/error = %q/%v, want antigravity",
				preferred,
				harness,
				err,
			)
		}
	}
	_, err := selectSemanticHarness(inventory, "cursor")
	if err == nil ||
		!strings.Contains(err.Error(), "Cursor CLI was not detected") {
		t.Fatalf("cursor rejection = %v", err)
	}
	// The Cursor CLI outranks the Antigravity CLI under auto.
	writeFakeExecutable(t, bin, "cursor-agent")
	harness, err := selectSemanticHarness(inventory, "auto")
	if err != nil || harness != localapp.SemanticHarnessCursor {
		t.Fatalf("auto with cursor-agent harness/error = %q/%v", harness, err)
	}
}

func TestSemanticHarnessDisplayNames(t *testing.T) {
	tests := map[localapp.SemanticHarness]string{
		localapp.SemanticHarnessClaude:      "Claude Code",
		localapp.SemanticHarnessCodex:       "Codex",
		localapp.SemanticHarnessCursor:      "Cursor Agent",
		localapp.SemanticHarnessAntigravity: "Antigravity",
	}
	for harness, want := range tests {
		if got := semanticHarnessDisplayName(harness); got != want {
			t.Fatalf("display name for %q = %q, want %q", harness, got, want)
		}
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
