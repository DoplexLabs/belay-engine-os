package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/localapp"
)

func TestRunMCPConfigWritesDeterministicJSONBeforeAggregateError(t *testing.T) {
	oldManager := manageMCPConfiguration
	oldExecutable := currentExecutablePath
	t.Cleanup(func() {
		manageMCPConfiguration = oldManager
		currentExecutablePath = oldExecutable
	})
	executable := filepath.Join(t.TempDir(), "belay")
	if err := os.WriteFile(executable, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	currentExecutablePath = func() (string, error) { return executable, nil }
	var captured localapp.MCPConfigRequest
	manageMCPConfiguration = func(
		_ context.Context,
		request localapp.MCPConfigRequest,
	) (localapp.MCPConfigResult, error) {
		captured = request
		code := "conflicting_entry"
		return localapp.MCPConfigResult{
			SchemaVersion: localapp.MCPConfigSchemaVersion,
			Action:        request.Action,
			OverallStatus: "partial",
			Targets: []localapp.MCPConfigTargetResult{
				{
					Agent:     "codex",
					Detected:  true,
					Scope:     "user",
					Status:    "foreign_preserved",
					Ownership: "foreign",
					ErrorCode: &code,
				},
			},
		}, errors.New("private-command-output")
	}
	home := filepath.Join(t.TempDir(), "belay home")
	var stdout, stderr bytes.Buffer
	err := runMCPConfig(
		context.Background(),
		[]string{"install", "--home", home},
		&stdout,
		&stderr,
	)
	if err == nil {
		t.Fatal("runMCPConfig() error = nil")
	}
	if captured.Action != localapp.MCPConfigInstall ||
		captured.Paths.Root != home ||
		captured.InstallationID == "" ||
		captured.AllowCodexMCPAdd {
		t.Fatalf("request = %+v", captured)
	}
	output := stdout.String()
	for _, required := range []string{
		`"schema_version": "belay.mcp-config.v1"`,
		`"overall_status": "partial"`,
		`"status": "foreign_preserved"`,
	} {
		if !strings.Contains(output, required) {
			t.Fatalf("JSON missing %q:\n%s", required, output)
		}
	}
	for _, prohibited := range []string{"private-command-output", executable, home} {
		if strings.Contains(output, prohibited) || strings.Contains(stderr.String(), prohibited) {
			t.Fatalf("command output leaked %q", prohibited)
		}
	}
}

func TestRunMCPConfigRejectsUnknownActionAndTrailingArguments(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"replace"},
		{"status", "extra"},
		{"status", "--allow-codex-mcp-add"},
	} {
		var stdout, stderr bytes.Buffer
		if err := runMCPConfig(
			context.Background(),
			args,
			&stdout,
			&stderr,
		); err == nil {
			t.Fatalf("runMCPConfig(%q) succeeded", args)
		}
	}
}

func TestMCPConfigInstallHelpExplainsCodexOptIn(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runMCPConfig(
		context.Background(),
		[]string{"install", "--help"},
		&stdout,
		&stderr,
	)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("install --help error = %v", err)
	}
	for _, required := range []string{
		"allow-codex-mcp-add",
		"non-atomic duplicate-name behavior",
		"strict absence verification",
	} {
		if !strings.Contains(stderr.String(), required) {
			t.Fatalf("install help missing %q:\n%s", required, stderr.String())
		}
	}
}

func TestOnboardMCPConfigurationEmitsFixedPayloadFreeSummary(t *testing.T) {
	oldManager := manageMCPConfiguration
	t.Cleanup(func() { manageMCPConfiguration = oldManager })
	manageMCPConfiguration = func(
		_ context.Context,
		request localapp.MCPConfigRequest,
	) (localapp.MCPConfigResult, error) {
		if request.Action != localapp.MCPConfigInstall {
			t.Fatalf("action = %q", request.Action)
		}
		code := "status_unparseable"
		return localapp.MCPConfigResult{
			SchemaVersion: localapp.MCPConfigSchemaVersion,
			Action:        request.Action,
			OverallStatus: "partial",
			Targets: []localapp.MCPConfigTargetResult{
				{Agent: "codex", Status: "already_installed"},
				{
					Agent:     "claude",
					Status:    "unverifiable",
					ErrorCode: &code,
				},
			},
		}, errors.New("token=private-cli-output")
	}
	var stderr bytes.Buffer
	complete := onboardMCPConfiguration(
		context.Background(),
		preparedRuntime{
			paths:           localapp.Paths{Root: "/private/home"},
			config:          localapp.Config{InstallationID: "inst_abcdefgh"},
			belayExecutable: "/private/bin/belay",
		},
		"quickstart",
		false,
		&stderr,
	)
	if complete {
		t.Fatal("onboarding reported complete")
	}
	if got := stderr.String(); got !=
		"belay quickstart: mcp codex=already_installed claude=unverifiable\n" {
		t.Fatalf("summary = %q", got)
	}
	if strings.Contains(stderr.String(), "private-cli-output") {
		t.Fatal("summary leaked manager error")
	}
}

func TestStandaloneInstallCreatesStableIdentityReusedByQuickstart(t *testing.T) {
	oldManager := manageMCPConfiguration
	oldExecutable := currentExecutablePath
	t.Cleanup(func() {
		manageMCPConfiguration = oldManager
		currentExecutablePath = oldExecutable
	})
	executable := filepath.Join(t.TempDir(), "belay")
	if err := os.WriteFile(executable, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	currentExecutablePath = func() (string, error) { return executable, nil }
	var requests []localapp.MCPConfigRequest
	manageMCPConfiguration = func(
		_ context.Context,
		request localapp.MCPConfigRequest,
	) (localapp.MCPConfigResult, error) {
		requests = append(requests, request)
		return localapp.MCPConfigResult{
			SchemaVersion: localapp.MCPConfigSchemaVersion,
			Action:        request.Action,
			OverallStatus: "complete",
			Targets: []localapp.MCPConfigTargetResult{
				{Agent: "codex", Status: "skipped_not_detected"},
				{Agent: "claude", Status: "skipped_not_detected"},
			},
		}, nil
	}
	home := filepath.Join(t.TempDir(), "belay-home")
	var stdout, stderr bytes.Buffer
	if err := runMCPConfig(
		context.Background(),
		[]string{"install", "--home", home, "--allow-codex-mcp-add"},
		&stdout,
		&stderr,
	); err != nil {
		t.Fatal(err)
	}
	paths, err := localapp.ResolvePaths(home)
	if err != nil {
		t.Fatal(err)
	}
	config, err := localapp.LoadOrCreateConfig(paths)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.Database); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("standalone install created Local database: %v", err)
	}
	var quickstartStderr bytes.Buffer
	if !onboardMCPConfiguration(
		context.Background(),
		preparedRuntime{
			paths:           paths,
			config:          config,
			belayExecutable: executable,
		},
		"quickstart",
		true,
		&quickstartStderr,
	) {
		t.Fatal("quickstart onboarding failed")
	}
	if len(requests) != 2 ||
		requests[0].InstallationID == "" ||
		requests[0].InstallationID != requests[1].InstallationID ||
		!requests[0].AllowCodexMCPAdd ||
		!requests[1].AllowCodexMCPAdd {
		t.Fatalf("requests = %+v", requests)
	}
	if !strings.Contains(
		stderr.String(),
		"accepts non-atomic duplicate-name behavior",
	) {
		t.Fatalf("install warning missing: %q", stderr.String())
	}
}
