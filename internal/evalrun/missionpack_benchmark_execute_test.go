package evalrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecuteMissionPackBenchmarkRunCapturesAndScores(t *testing.T) {
	benchmarkRoot := benchmarkFixture(t)
	scoreScript := filepath.Join(benchmarkRoot, "scripts", "score-workspace.sh")
	if err := os.MkdirAll(filepath.Dir(scoreScript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		scoreScript,
		[]byte("#!/bin/sh\nprintf '%s\\n' '{\"schema_version\":\"belay.missionpack-benchmark-score.v1\",\"accepted_completion\":true}' > \"$2\"\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "ready"), []byte("yes"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := PrepareMissionPackBenchmarkRun(
		context.Background(),
		MissionPackBenchmarkPrepareOptions{
			BenchmarkRoot:       benchmarkRoot,
			RunRoot:             filepath.Join(t.TempDir(), "run"),
			GradleCacheTemplate: cache,
			Schedule: benchmarkRunSchedule(
				MissionPackHarnessCodex,
				MissionPackArmNoContext,
			),
			Sequence:           1,
			CodexModel:         "openai.gpt-5.6-sol",
			MaxBudgetUSD:       10,
			MaxTokenOperations: 250000,
			WallTimeoutSeconds: 30,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(t.TempDir(), "fake-codex")
	if err := os.WriteFile(
		harness,
		[]byte(strings.Join([]string{
			"#!/bin/sh",
			"if [ \"${1:-}\" = \"--version\" ]; then printf '%s\\n' 'fake-codex 1.0'; exit 0; fi",
			"printf '%s\\n' '{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":120,\"cached_input_tokens\":20,\"cache_write_input_tokens\":0,\"output_tokens\":30}}'",
			"printf '%s\\n' 'fake harness stderr' >&2",
			"printf '%s\\n' 'implementation' > src/Implementation.kt",
			"",
		}, "\n")),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	manifest.Command.Executable = harness
	manifest.Command.Arguments = []string{"--model", "openai.gpt-5.6-sol"}

	if _, err := ExecuteMissionPackBenchmarkRun(
		context.Background(),
		manifest,
		false,
	); err == nil || !strings.Contains(err.Error(), "explicit confirmation") {
		t.Fatalf("unconfirmed execution error = %v", err)
	}
	result, err := ExecuteMissionPackBenchmarkRun(
		context.Background(),
		manifest,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.AcceptedCompletion ||
		!result.HiddenSuitePassed ||
		!result.UnderCaps ||
		!result.Countable ||
		result.HarnessExitCode != 0 ||
		result.HarnessTimedOut ||
		result.RawEventsSHA256 == "" ||
		result.WorkspaceDiffSHA256 == "" {
		t.Fatalf("result = %+v", result)
	}
	if result.HarnessProvenance.Version != "fake-codex 1.0" ||
		result.HarnessProvenance.ExecutableSHA256 == "" {
		t.Fatalf("provenance = %+v", result.HarnessProvenance)
	}
	if result.Usage.InputTokens != 120 ||
		result.Usage.CacheReadTokens != 20 ||
		result.Usage.OutputTokens != 30 ||
		result.Usage.TotalTokens != 150 ||
		result.Usage.CostUSD == nil {
		t.Fatalf("usage = %+v", result.Usage)
	}
	status, err := os.ReadFile(result.WorkspaceStatusPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(status), "src/Implementation.kt") {
		t.Fatalf("workspace status = %q", status)
	}
}

func TestExecuteMissionPackBenchmarkRunRecordsCapHitAsCountedFailure(
	t *testing.T,
) {
	benchmarkRoot := benchmarkFixture(t)
	scoreScript := filepath.Join(benchmarkRoot, "scripts", "score-workspace.sh")
	if err := os.MkdirAll(filepath.Dir(scoreScript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		scoreScript,
		[]byte("#!/bin/sh\nprintf '%s\\n' '{\"schema_version\":\"belay.missionpack-benchmark-score.v1\",\"accepted_completion\":true}' > \"$2\"\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := PrepareMissionPackBenchmarkRun(
		context.Background(),
		MissionPackBenchmarkPrepareOptions{
			BenchmarkRoot:       benchmarkRoot,
			RunRoot:             filepath.Join(t.TempDir(), "run"),
			GradleCacheTemplate: cache,
			Schedule: benchmarkRunSchedule(
				MissionPackHarnessCodex,
				MissionPackArmNoContext,
			),
			Sequence:           1,
			CodexModel:         "openai.gpt-5.6-sol",
			MaxBudgetUSD:       10,
			MaxTokenOperations: 100,
			WallTimeoutSeconds: 30,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(t.TempDir(), "fake-codex")
	if err := os.WriteFile(
		harness,
		[]byte("#!/bin/sh\nif [ \"${1:-}\" = \"--version\" ]; then printf '%s\\n' 'fake-codex 1.0'; exit 0; fi\nprintf '%s\\n' '{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":120,\"output_tokens\":30}}'\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	manifest.Command.Executable = harness
	manifest.Command.Arguments = []string{"--model", "openai.gpt-5.6-sol"}
	result, err := ExecuteMissionPackBenchmarkRun(
		context.Background(),
		manifest,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.HiddenSuitePassed ||
		result.AcceptedCompletion ||
		result.UnderCaps ||
		!result.TokenCapHit ||
		!result.Countable {
		t.Fatalf("result = %+v", result)
	}
}

func TestObserveMissionPackBenchmarkContamination(t *testing.T) {
	codexBody := []byte(strings.Join([]string{
		`{"type":"item.completed","item":{"type":"command_execution","command":"./gradlew --offline smokeTest"}}`,
		`{"type":"item.completed","item":{"type":"command_execution","command":"git status --short"}}`,
		`{"type":"item.completed","item":{"type":"command_execution","command":"curl https://example.test/reference"}}`,
	}, "\n"))
	got := observeMissionPackBenchmarkContamination(
		MissionPackHarnessCodex,
		codexBody,
	)
	if len(got) != 1 ||
		got[0].Kind != "network_capable_command" ||
		!strings.Contains(got[0].Command, "curl") {
		t.Fatalf("Codex contamination = %+v", got)
	}

	claudeBody := []byte(strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"I will test locally."},{"type":"tool_use","name":"Bash","input":{"command":"./gradlew smokeTest"}}]}}`,
		`{"type":"user","message":{"content":"The words curl and network in a prompt are not commands."}}`,
	}, "\n"))
	got = observeMissionPackBenchmarkContamination(
		MissionPackHarnessClaude,
		claudeBody,
	)
	if len(got) != 1 ||
		got[0].Kind != "gradle_without_offline_mode" {
		t.Fatalf("Claude contamination = %+v", got)
	}
}

func TestObserveMissionPackBenchmarkClaudeUsagePrefersHarnessCost(t *testing.T) {
	cost := 0.42
	body := []byte(strings.Join([]string{
		`{"type":"assistant","message":{"model":"claude-opus-4-6","usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":40,"cache_creation_input_tokens":10}}}`,
		`{"type":"result","total_cost_usd":0.42}`,
	}, "\n"))
	usage := observeMissionPackBenchmarkUsage(
		MissionPackHarnessClaude,
		[]string{"--model", "claude-opus-4-6"},
		body,
	)
	if usage.Model != "claude-opus-4-6" ||
		usage.InputTokens != 100 ||
		usage.OutputTokens != 20 ||
		usage.CacheReadTokens != 40 ||
		usage.CacheWriteTokens != 10 ||
		usage.TokenOperations != 170 ||
		usage.CostUSD == nil ||
		*usage.CostUSD != cost ||
		usage.CostSource != "harness_reported" {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestObserveMissionPackBenchmarkInfrastructureFailure(t *testing.T) {
	claude := []byte(
		`{"type":"assistant","error":"authentication_failed"}`,
	)
	if got := observeMissionPackBenchmarkInfrastructureFailure(
		MissionPackHarnessClaude,
		claude,
	); got != "harness_authentication_failure" {
		t.Fatalf("Claude failure = %q", got)
	}
	codex := []byte(
		`{"type":"turn.failed","error":{"message":"failed to load AWS credentials"}}`,
	)
	if got := observeMissionPackBenchmarkInfrastructureFailure(
		MissionPackHarnessCodex,
		codex,
	); got != "harness_authentication_failure" {
		t.Fatalf("Codex failure = %q", got)
	}
}

func TestBenchmarkEnvironmentOnlyInheritsRecordedAllowlistedKeys(t *testing.T) {
	got := benchmarkEnvironment(
		[]string{
			"PATH=/usr/bin",
			"AWS_PROFILE=benchmark",
			"OPENAI_API_KEY=allowed-secret",
			"CODEX_SESSION_ID=must-not-leak",
			"BASH_ENV=/tmp/host-injection",
			"UNRELATED_SECRET=must-not-leak",
		},
		map[string]string{
			"HOME":       "/isolated/home",
			"CODEX_HOME": "/isolated/home",
		},
		[]string{
			"PATH",
			"AWS_PROFILE",
			"OPENAI_API_KEY",
			"CODEX_SESSION_ID",
			"BASH_ENV",
			"UNRELATED_SECRET",
		},
	)
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		"PATH=/usr/bin",
		"AWS_PROFILE=benchmark",
		"OPENAI_API_KEY=allowed-secret",
		"HOME=/isolated/home",
		"CODEX_HOME=/isolated/home",
		"CI=1",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("environment %q missing %q", joined, want)
		}
	}
	for _, forbidden := range []string{
		"CODEX_SESSION_ID",
		"BASH_ENV",
		"UNRELATED_SECRET",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("environment %q contains %q", joined, forbidden)
		}
	}
}
