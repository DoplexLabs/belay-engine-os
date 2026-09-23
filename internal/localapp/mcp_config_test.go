package localapp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type mcpRunnerCall struct {
	executable string
	args       []string
}

type scriptedMCPRunner struct {
	t       *testing.T
	results []mcpCommandResult
	calls   []mcpRunnerCall
}

func (runner *scriptedMCPRunner) run(
	_ context.Context,
	executable string,
	args []string,
) mcpCommandResult {
	runner.t.Helper()
	runner.calls = append(runner.calls, mcpRunnerCall{
		executable: executable,
		args:       append([]string(nil), args...),
	})
	if len(runner.results) == 0 {
		runner.t.Fatalf("unexpected MCP CLI call: %s %q", executable, args)
	}
	result := runner.results[0]
	runner.results = runner.results[1:]
	return result
}

func TestResolveBelayMCPIdentityUsesExactDefaultAndCustomHomeArgs(t *testing.T) {
	executable := writeMCPExecutable(t, "belay")
	defaultRoot, err := defaultBelayRoot()
	if err != nil {
		t.Fatal(err)
	}
	defaultIdentity, err := ResolveBelayMCPIdentity(executable, defaultRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(defaultIdentity.Args, []string{"mcp"}) {
		t.Fatalf("default args = %q", defaultIdentity.Args)
	}
	customRoot := filepath.Join(t.TempDir(), "private home")
	customIdentity, err := ResolveBelayMCPIdentity(executable, customRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(customIdentity.Args, []string{"mcp", "--home", customRoot}) {
		t.Fatalf("custom args = %q", customIdentity.Args)
	}
	if !filepath.IsAbs(customIdentity.Command) {
		t.Fatalf("command is not absolute: %q", customIdentity.Command)
	}
}

func TestManageMCPConfigInstallsClaudeAndPersistsVerifiedIdentity(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	claude := writeMCPExecutable(t, "claude")
	identity, err := ResolveBelayMCPIdentity(belay, paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	runner := &scriptedMCPRunner{
		t: t,
		results: []mcpCommandResult{
			claudeAbsentResult(),
			{exitCode: 0},
			claudePresentResult(identity),
		},
	}
	result, err := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:          paths,
			InstallationID: "inst_abcdefgh",
			Executable:     belay,
			Action:         MCPConfigInstall,
		},
		mcpTestDependencies(t, runner, "", claude),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.OverallStatus != "complete" ||
		result.Targets[0].Status != "skipped_not_detected" ||
		result.Targets[1].Status != "installed" ||
		!result.Targets[1].Changed {
		t.Fatalf("install result = %+v", result)
	}
	wantAdd := append(
		[]string{"mcp", "add", "--scope", "user", "belay", "--", identity.Command},
		identity.Args...,
	)
	if len(runner.calls) != 3 || !slices.Equal(runner.calls[1].args, wantAdd) {
		t.Fatalf("calls = %+v, want add %q", runner.calls, wantAdd)
	}
	manifest, state := loadMCPManifest(paths.MCPManifest, "inst_abcdefgh")
	if state != manifestValid {
		t.Fatalf("manifest state = %v", state)
	}
	if got := manifest.Targets["claude"]; got.Command != identity.Command ||
		!slices.Equal(got.Args, identity.Args) {
		t.Fatalf("manifest target = %+v", got)
	}
	info, err := os.Stat(paths.MCPManifest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode = %o", info.Mode().Perm())
	}
}

func TestManageMCPConfigPreservesForeignAndUnverifiableClaudeEntries(t *testing.T) {
	tests := []struct {
		name   string
		status mcpCommandResult
		want   string
	}{
		{
			name: "foreign",
			status: claudePresentResult(MCPIdentity{
				Command: "/foreign/belay",
				Args:    []string{"mcp"},
			}),
			want: "foreign_preserved",
		},
		{
			name:   "unverifiable",
			status: mcpCommandResult{exitCode: 0, stdout: "unknown layout\nsecret=value\n"},
			want:   "unverifiable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths, err := ResolvePaths(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			belay := writeMCPExecutable(t, "belay")
			claude := writeMCPExecutable(t, "claude")
			runner := &scriptedMCPRunner{
				t:       t,
				results: []mcpCommandResult{test.status},
			}
			result, err := manageMCPConfig(
				context.Background(),
				MCPConfigRequest{
					Paths:          paths,
					InstallationID: "inst_abcdefgh",
					Executable:     belay,
					Action:         MCPConfigInstall,
				},
				mcpTestDependencies(t, runner, "", claude),
			)
			if err == nil {
				t.Fatal("install succeeded")
			}
			if result.Targets[1].Status != test.want || len(runner.calls) != 1 {
				t.Fatalf("result = %+v calls=%+v", result, runner.calls)
			}
			body, readErr := json.Marshal(result)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.Contains(string(body), "secret=value") ||
				strings.Contains(string(body), "/foreign/belay") {
				t.Fatalf("result leaked CLI data: %s", body)
			}
		})
	}
}

func TestManageMCPConfigUpdatesRecognizedClaudeAndUninstallsOwnedEntry(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	claude := writeMCPExecutable(t, "claude")
	current, err := ResolveBelayMCPIdentity(belay, paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	old := MCPIdentity{Command: "/old/archive/bin/belay", Args: append([]string(nil), current.Args...)}
	manifest := mcpOwnershipManifest{
		Version:        mcpManifestVersion,
		InstallationID: "inst_abcdefgh",
		Targets: map[string]mcpManifestTarget{
			"claude": {
				Scope:      "user",
				Command:    old.Command,
				Args:       old.Args,
				VerifiedAt: "2026-09-09T20:00:00Z",
			},
		},
	}
	if err := writeMCPManifest(paths.MCPManifest, manifest); err != nil {
		t.Fatal(err)
	}
	updateRunner := &scriptedMCPRunner{
		t: t,
		results: []mcpCommandResult{
			claudePresentResult(old),
			claudePresentResult(old),
			{exitCode: 0},
			claudeAbsentResult(),
			{exitCode: 0},
			claudePresentResult(current),
		},
	}
	update, err := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:          paths,
			InstallationID: "inst_abcdefgh",
			Executable:     belay,
			Action:         MCPConfigInstall,
		},
		mcpTestDependencies(t, updateRunner, "", claude),
	)
	if err != nil || update.Targets[1].Status != "updated" {
		t.Fatalf("update = %+v err=%v", update, err)
	}
	updatedManifest, state := loadMCPManifest(paths.MCPManifest, "inst_abcdefgh")
	if state != manifestValid || updatedManifest.Targets["claude"].Command != current.Command {
		t.Fatalf("updated manifest = %+v state=%v", updatedManifest, state)
	}

	uninstallRunner := &scriptedMCPRunner{
		t: t,
		results: []mcpCommandResult{
			claudePresentResult(current),
			claudePresentResult(current),
			{exitCode: 0},
			claudeAbsentResult(),
		},
	}
	uninstall, err := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:          paths,
			InstallationID: "inst_abcdefgh",
			Executable:     belay,
			Action:         MCPConfigUninstall,
		},
		mcpTestDependencies(t, uninstallRunner, "", claude),
	)
	if err == nil || uninstall.Targets[1].Status != "removed" ||
		uninstall.Targets[0].Status != "unavailable" {
		t.Fatalf("uninstall = %+v err=%v", uninstall, err)
	}
	removedManifest, state := loadMCPManifest(paths.MCPManifest, "inst_abcdefgh")
	if state != manifestValid {
		t.Fatalf("removed manifest state = %v", state)
	}
	if _, present := removedManifest.Targets["claude"]; present {
		t.Fatalf("removed target remains: %+v", removedManifest)
	}
}

func TestManageMCPConfigRefusesCodexAddWhenDuplicateNameOverwriteIsUnsafe(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	codex := writeMCPExecutable(t, "codex")
	runner := &scriptedMCPRunner{
		t:       t,
		results: []mcpCommandResult{codexAbsentResult()},
	}
	result, err := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:          paths,
			InstallationID: "inst_abcdefgh",
			Executable:     belay,
			Action:         MCPConfigInstall,
		},
		mcpTestDependencies(t, runner, codex, ""),
	)
	if err == nil {
		t.Fatal("Codex install succeeded without duplicate-name safety")
	}
	if result.Targets[0].Status != "unavailable" ||
		result.Targets[0].ErrorCode == nil ||
		*result.Targets[0].ErrorCode != "install_failed" ||
		len(runner.calls) != 1 {
		t.Fatalf("result = %+v calls=%+v", result, runner.calls)
	}
}

func TestManageMCPConfigAllowsCodexAddOnlyWithExplicitAbsentOptIn(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	codex := writeMCPExecutable(t, "codex")
	identity, err := ResolveBelayMCPIdentity(belay, paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	runner := &scriptedMCPRunner{
		t: t,
		results: []mcpCommandResult{
			codexAbsentResult(),
			{exitCode: 0},
			codexPresentResult(identity),
		},
	}
	result, err := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:            paths,
			InstallationID:   "inst_abcdefgh",
			Executable:       belay,
			Action:           MCPConfigInstall,
			AllowCodexMCPAdd: true,
		},
		mcpTestDependencies(t, runner, codex, ""),
	)
	if err != nil || result.Targets[0].Status != "installed" {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	wantAdd := append(
		[]string{"mcp", "add", "belay", "--", identity.Command},
		identity.Args...,
	)
	if len(runner.calls) != 3 || !slices.Equal(runner.calls[1].args, wantAdd) {
		t.Fatalf("calls = %+v, want add %q", runner.calls, wantAdd)
	}
}

func TestManageMCPConfigCodexOptInStillPreservesConflict(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	codex := writeMCPExecutable(t, "codex")
	runner := &scriptedMCPRunner{
		t: t,
		results: []mcpCommandResult{
			codexPresentResult(MCPIdentity{
				Command: "/foreign/belay",
				Args:    []string{"mcp"},
			}),
		},
	}
	result, err := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:            paths,
			InstallationID:   "inst_abcdefgh",
			Executable:       belay,
			Action:           MCPConfigInstall,
			AllowCodexMCPAdd: true,
		},
		mcpTestDependencies(t, runner, codex, ""),
	)
	if err == nil || result.Targets[0].Status != "foreign_preserved" ||
		len(runner.calls) != 1 {
		t.Fatalf("result = %+v err=%v calls=%+v", result, err, runner.calls)
	}
}

func TestManageMCPConfigCodexOptInDoesNotUpdateRecognizedPriorEntry(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	codex := writeMCPExecutable(t, "codex")
	current, err := ResolveBelayMCPIdentity(belay, paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	recognized := MCPIdentity{
		Command: "/old/archive/bin/belay",
		Args:    append([]string(nil), current.Args...),
	}
	if err := writeMCPManifest(paths.MCPManifest, mcpOwnershipManifest{
		Version:        mcpManifestVersion,
		InstallationID: "inst_abcdefgh",
		Targets: map[string]mcpManifestTarget{
			"codex": {
				Scope:      "user",
				Command:    recognized.Command,
				Args:       recognized.Args,
				VerifiedAt: "2026-09-09T20:00:00Z",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedMCPRunner{
		t:       t,
		results: []mcpCommandResult{codexPresentResult(recognized)},
	}
	result, err := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:            paths,
			InstallationID:   "inst_abcdefgh",
			Executable:       belay,
			Action:           MCPConfigInstall,
			AllowCodexMCPAdd: true,
		},
		mcpTestDependencies(t, runner, codex, ""),
	)
	if err == nil ||
		result.Targets[0].Status != "unavailable" ||
		result.Targets[0].Ownership != "recognized" ||
		result.Targets[0].Changed ||
		len(runner.calls) != 1 {
		t.Fatalf("result = %+v err=%v calls=%+v", result, err, runner.calls)
	}
	if !slices.Equal(runner.calls[0].args, []string{"mcp", "get", "belay", "--json"}) {
		t.Fatalf("unexpected Codex commands: %+v", runner.calls)
	}
}

func TestManageMCPConfigStatusClassifiesExactCodexIdentity(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	codex := writeMCPExecutable(t, "codex")
	identity, err := ResolveBelayMCPIdentity(belay, paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	runner := &scriptedMCPRunner{
		t:       t,
		results: []mcpCommandResult{codexPresentResult(identity)},
	}
	result, err := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:      paths,
			Executable: belay,
			Action:     MCPConfigStatus,
		},
		mcpTestDependencies(t, runner, codex, ""),
	)
	if err == nil {
		t.Fatal("status unexpectedly complete with Claude unavailable")
	}
	if result.Targets[0].Status != "owned_current" ||
		result.Targets[0].Ownership != "current" ||
		!slices.Equal(runner.calls[0].args, []string{"mcp", "get", "belay", "--json"}) {
		t.Fatalf("result = %+v calls=%+v", result, runner.calls)
	}
	if _, statErr := os.Stat(paths.MCPManifest); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("status created manifest: %v", statErr)
	}
}

func TestManageMCPConfigRunsSymlinkedCLIUsingLookPathPath(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	toolbox := writeMCPExecutable(t, "toolbox")
	codex := filepath.Join(t.TempDir(), "codex")
	if err := os.Symlink(toolbox, codex); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedMCPRunner{
		t:       t,
		results: []mcpCommandResult{codexAbsentResult()},
	}
	result, err := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:      paths,
			Executable: belay,
			Action:     MCPConfigStatus,
		},
		mcpTestDependencies(t, runner, codex, ""),
	)
	if err == nil || result.Targets[0].Status != "absent" || len(runner.calls) != 1 {
		t.Fatalf("result = %+v err=%v calls=%+v", result, err, runner.calls)
	}
	if got := runner.calls[0].executable; got != codex {
		t.Fatalf("runner executable = %q, want symlink %q instead of target %q", got, codex, toolbox)
	}
}

func TestManageMCPConfigTimeoutDoesNotExposeCapturedOutput(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	claude := writeMCPExecutable(t, "claude")
	runner := &scriptedMCPRunner{
		t: t,
		results: []mcpCommandResult{
			{exitCode: -1, stdout: "token=private-timeout-output"},
		},
	}
	result, err := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:      paths,
			Executable: belay,
			Action:     MCPConfigStatus,
		},
		mcpTestDependencies(t, runner, "", claude),
	)
	if err == nil || result.Targets[1].Status != "unavailable" ||
		result.Targets[1].ErrorCode == nil ||
		*result.Targets[1].ErrorCode != "status_timeout" {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	body, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(body), "private-timeout-output") ||
		strings.Contains(err.Error(), "private-timeout-output") {
		t.Fatalf("timeout leaked output: %s %v", body, err)
	}
}

func TestStrictStatusParsersRejectUnsafeOrUnknownLaunchData(t *testing.T) {
	codex := codexPresentResult(MCPIdentity{
		Command: "/bin/belay",
		Args:    []string{"mcp"},
	})
	var codexPayload map[string]any
	if err := json.Unmarshal([]byte(codex.stdout), &codexPayload); err != nil {
		t.Fatal(err)
	}
	codexPayload["startup_timeout_sec"] = 30
	codexBody, err := json.Marshal(codexPayload)
	if err != nil {
		t.Fatal(err)
	}
	codex.stdout = string(codexBody)
	if got := parseCodexMCPStatus(codex); got.kind != mcpInspectionUnverifiable {
		t.Fatalf("Codex unsafe settings = %+v", got)
	}
	for name, mutate := range map[string]func(map[string]any){
		"top_level": func(payload map[string]any) {
			payload["future_launch_setting"] = true
		},
		"transport": func(payload map[string]any) {
			transport := payload["transport"].(map[string]any)
			transport["future_transport_setting"] = "unsafe"
		},
	} {
		t.Run(name, func(t *testing.T) {
			var payload map[string]any
			base := codexPresentResult(MCPIdentity{
				Command: "/bin/belay",
				Args:    []string{"mcp"},
			})
			if err := json.Unmarshal([]byte(base.stdout), &payload); err != nil {
				t.Fatal(err)
			}
			mutate(payload)
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			base.stdout = string(body)
			if got := parseCodexMCPStatus(base); got.kind != mcpInspectionUnverifiable {
				t.Fatalf("unknown field classified as %+v", got)
			}
		})
	}
	claude := claudePresentResult(MCPIdentity{
		Command: "/bin/belay",
		Args:    []string{"mcp", "--home", "/path with spaces"},
	})
	if got := parseClaudeMCPStatus(claude); got.kind != mcpInspectionUnverifiable {
		t.Fatalf("Claude ambiguous args = %+v", got)
	}
}

func TestCodexAbsenceDetectionIsClosedWorld(t *testing.T) {
	const expected = "Error: No MCP server named 'belay' found."
	tests := []struct {
		name   string
		stdout string
		stderr string
		absent bool
	}{
		{
			name:   "clean_stdout",
			stdout: expected + "\n",
			absent: true,
		},
		{
			name:   "clean_stderr",
			stderr: expected + "\n",
			absent: true,
		},
		{
			name:   "expected_plus_extra_stdout",
			stdout: expected + "\nadditional output\n",
		},
		{
			name:   "expected_plus_extra_stderr",
			stderr: expected + "\nadditional output\n",
		},
		{
			name:   "stdout_expected_stderr_extra",
			stdout: expected + "\n",
			stderr: "additional output\n",
		},
		{
			name:   "stderr_expected_stdout_extra",
			stdout: "additional output\n",
			stderr: expected + "\n",
		},
		{
			name:   "expected_split_across_streams",
			stdout: "Error: No MCP server named 'belay'",
			stderr: " found.\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := mcpCommandResult{
				exitCode: 1,
				stdout:   test.stdout,
				stderr:   test.stderr,
			}
			inspection := parseCodexMCPStatus(result)
			if test.absent {
				if inspection.kind != mcpInspectionAbsent {
					t.Fatalf("inspection = %+v, want absent", inspection)
				}
				return
			}
			if inspection.kind != mcpInspectionUnavailable ||
				inspection.errorCode != "status_failed" {
				t.Fatalf("inspection = %+v, want unavailable/status_failed", inspection)
			}
		})
	}
}

func TestMCPManifestRejectsLooseModeAndSymlink(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifest := mcpOwnershipManifest{
		Version:        mcpManifestVersion,
		InstallationID: "inst_abcdefgh",
		Targets:        map[string]mcpManifestTarget{},
	}
	if err := writeMCPManifest(paths.MCPManifest, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths.MCPManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, state := loadMCPManifest(paths.MCPManifest, "inst_abcdefgh"); state != manifestInvalid {
		t.Fatalf("loose manifest state = %v", state)
	}
	target := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(target, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "manifest-link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, state := loadMCPManifest(link, "inst_abcdefgh"); state != manifestInvalid {
		t.Fatalf("symlink manifest state = %v", state)
	}
}

func TestMCPManifestAcceptsEverySupportedAgentAndRejectsOthers(t *testing.T) {
	target := func(command string) mcpManifestTarget {
		return mcpManifestTarget{
			Scope:      "user",
			Command:    command,
			Args:       []string{"mcp"},
			VerifiedAt: "2026-09-09T20:00:00Z",
		}
	}
	command := filepath.Join(t.TempDir(), "belay")
	full := mcpOwnershipManifest{
		Version:        mcpManifestVersion,
		InstallationID: "inst_abcdefgh",
		Targets: map[string]mcpManifestTarget{
			"codex":       target(command),
			"claude":      target(command),
			"cursor":      target(command),
			"antigravity": target(command),
		},
	}
	if !validMCPManifest(full, "inst_abcdefgh") {
		t.Fatalf("four-target manifest rejected: %+v", full)
	}
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writeMCPManifest(paths.MCPManifest, full); err != nil {
		t.Fatal(err)
	}
	loaded, state := loadMCPManifest(paths.MCPManifest, "inst_abcdefgh")
	if state != manifestValid || len(loaded.Targets) != 4 ||
		loaded.Targets["antigravity"].Command != command {
		t.Fatalf("manifest = %+v state=%v", loaded, state)
	}
	for _, agent := range []string{"gemini", "Antigravity", "antigravity ", "windsurf"} {
		unknown := mcpOwnershipManifest{
			Version:        mcpManifestVersion,
			InstallationID: "inst_abcdefgh",
			Targets:        map[string]mcpManifestTarget{agent: target(command)},
		}
		if validMCPManifest(unknown, "inst_abcdefgh") {
			t.Fatalf("agent %q accepted", agent)
		}
	}
	tooMany := mcpOwnershipManifest{
		Version:        mcpManifestVersion,
		InstallationID: "inst_abcdefgh",
		Targets:        map[string]mcpManifestTarget{},
	}
	for _, agent := range []string{"codex", "claude", "cursor", "antigravity", "extra"} {
		tooMany.Targets[agent] = target(command)
	}
	if validMCPManifest(tooMany, "inst_abcdefgh") {
		t.Fatal("five-target manifest accepted")
	}
}

func TestDefaultMCPTargetsListEveryHarnessInStableOrder(t *testing.T) {
	targets := defaultMCPTargets()
	agents := make([]string, 0, len(targets))
	for _, target := range targets {
		agents = append(agents, target.agent)
	}
	if !slices.Equal(agents, []string{"codex", "claude", "cursor", "antigravity"}) {
		t.Fatalf("targets = %q", agents)
	}
	for _, target := range targets[2:] {
		if target.file == nil || !target.duplicateAddSafe || target.executable != "" {
			t.Fatalf("file target %q = %+v", target.agent, target)
		}
	}
}

func TestStatusAndUninstallDoNotCreateMissingBelayHomeOrManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing-home")
	paths, err := ResolvePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	for _, action := range []MCPConfigAction{MCPConfigStatus, MCPConfigUninstall} {
		t.Run(string(action), func(t *testing.T) {
			runner := &scriptedMCPRunner{t: t}
			_, _ = manageMCPConfig(
				context.Background(),
				MCPConfigRequest{Paths: paths, Executable: belay, Action: action},
				mcpTestDependencies(t, runner, "", ""),
			)
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("Belay home was created: %v", err)
			}
		})
	}
}

func mcpTestDependencies(
	t *testing.T,
	runner *scriptedMCPRunner,
	codex, claude string,
) mcpConfigDependencies {
	t.Helper()
	// Point os.UserHomeDir-backed detection at an empty private directory so no
	// test can read or rewrite the developer's real ~/.cursor or ~/.gemini
	// registries.
	home := t.TempDir()
	return mcpConfigDependencies{
		home: func() (string, error) { return home, nil },
		lookPath: func(name string) (string, error) {
			switch name {
			case "codex":
				if codex != "" {
					return codex, nil
				}
			case "claude":
				if claude != "" {
					return claude, nil
				}
			}
			return "", execNotFoundError(name)
		},
		run: runner.run,
		now: func() time.Time {
			return time.Date(2026, 9, 9, 20, 0, 0, 0, time.UTC)
		},
		lock: func(context.Context, string) (func(), error) {
			return func() {}, nil
		},
	}
}

func execNotFoundError(name string) error {
	return errors.New(name + " not found")
}

func writeMCPExecutable(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func codexPresentResult(identity MCPIdentity) mcpCommandResult {
	body, _ := json.Marshal(map[string]any{
		"name":                "belay",
		"enabled":             true,
		"disabled_reason":     nil,
		"enabled_tools":       nil,
		"disabled_tools":      nil,
		"startup_timeout_sec": nil,
		"tool_timeout_sec":    nil,
		"transport": map[string]any{
			"type":     "stdio",
			"command":  identity.Command,
			"args":     identity.Args,
			"env":      nil,
			"env_vars": []string{},
			"cwd":      nil,
		},
	})
	return mcpCommandResult{exitCode: 0, stdout: string(body)}
}

func codexAbsentResult() mcpCommandResult {
	return mcpCommandResult{
		exitCode: 1,
		stderr:   "Error: No MCP server named 'belay' found.\n",
	}
}

func claudePresentResult(identity MCPIdentity) mcpCommandResult {
	return mcpCommandResult{
		exitCode: 0,
		stdout: "belay:\n" +
			"  Scope: User config (available in all your projects)\n" +
			"  Status: \u2718 Failed to connect\n" +
			"  Issue: connection closed\n" +
			"  Type: stdio\n" +
			"  Command: " + identity.Command + "\n" +
			"  Args: " + strings.Join(identity.Args, " ") + "\n" +
			"  Environment:\n\n" +
			"To remove this server, run: claude mcp remove belay -s user\n",
	}
}

func claudeAbsentResult() mcpCommandResult {
	return mcpCommandResult{
		exitCode: 1,
		stdout:   `No MCP server named "belay". Configured servers: other`,
	}
}
