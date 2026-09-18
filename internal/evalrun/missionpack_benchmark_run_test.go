package evalrun

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestPrepareMissionPackBenchmarkRunIsolatesNoContextRun(t *testing.T) {
	benchmarkRoot := benchmarkFixture(t)
	runRoot := filepath.Join(t.TempDir(), "run")
	schedule := benchmarkRunSchedule(MissionPackHarnessCodex, MissionPackArmNoContext)

	manifest, err := PrepareMissionPackBenchmarkRun(
		context.Background(),
		MissionPackBenchmarkPrepareOptions{
			BenchmarkRoot:      benchmarkRoot,
			RunRoot:            runRoot,
			Schedule:           schedule,
			Sequence:           1,
			CodexModel:         "openai.gpt-5.6-sol",
			MaxBudgetUSD:       10,
			MaxTokenOperations: 250000,
			WallTimeoutSeconds: 2700,
			CodexExecutable:    "codex",
			ClaudeExecutable:   "claude",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.InstructionChannel != "none" ||
		manifest.DependencyCacheReady ||
		manifest.Launchable ||
		!slices.Contains(manifest.Blockers, "gradle_cache_template_missing") {
		t.Fatalf("manifest = %+v", manifest)
	}
	if manifest.Environment["CODEX_HOME"] != manifest.HarnessHomePath ||
		manifest.Environment["BELAY_HOME"] != manifest.BelayHomePath ||
		manifest.Environment["GRADLE_USER_HOME"] != manifest.GradleHomePath ||
		manifest.Environment["HOME"] != manifest.HarnessHomePath {
		t.Fatalf("environment = %+v", manifest.Environment)
	}
	if slices.Contains(manifest.Command.Arguments, "--ignore-rules") ||
		!slices.Contains(manifest.Command.Arguments, "--ephemeral") ||
		!slices.Contains(manifest.Command.Arguments, "--approve-for-me") ||
		!argumentPairPresent(
			manifest.Command.Arguments,
			"--add-dir",
			manifest.GradleHomePath,
		) ||
		slices.Contains(manifest.Command.Arguments, "--ignore-user-config") ||
		slices.Contains(manifest.Command.Arguments, "--sandbox") {
		t.Fatalf("arguments = %q", manifest.Command.Arguments)
	}
	policyBody, err := os.ReadFile(manifest.ExecutionPolicyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(policyBody) != "frozen codex benchmark rule\n" ||
		manifest.ExecutionPolicySHA256 == "" {
		t.Fatalf(
			"execution policy = %q, %q",
			policyBody,
			manifest.ExecutionPolicySHA256,
		)
	}
	if _, err := os.Stat(filepath.Join(manifest.WorkspacePath, ".git")); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"AGENTS.md", "CLAUDE.md"} {
		if _, err := os.Stat(filepath.Join(manifest.WorkspacePath, forbidden)); !os.IsNotExist(err) {
			t.Fatalf("%s unexpectedly present: %v", forbidden, err)
		}
	}
}

func argumentPairPresent(arguments []string, flag, value string) bool {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == flag && arguments[index+1] == value {
			return true
		}
	}
	return false
}

func TestPrepareMissionPackBenchmarkRunUsesHarnessStaticFile(t *testing.T) {
	benchmarkRoot := benchmarkFixture(t)
	schedule := benchmarkRunSchedule(MissionPackHarnessClaude, MissionPackArmStatic)
	cache := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "marker"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest, err := PrepareMissionPackBenchmarkRun(
		context.Background(),
		MissionPackBenchmarkPrepareOptions{
			BenchmarkRoot:       benchmarkRoot,
			RunRoot:             filepath.Join(t.TempDir(), "run"),
			GradleCacheTemplate: cache,
			Schedule:            schedule,
			Sequence:            1,
			ClaudeModel:         "claude-test",
			MaxBudgetUSD:        10,
			MaxTokenOperations:  250000,
			WallTimeoutSeconds:  2700,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.InstructionChannel != "CLAUDE.md" ||
		!manifest.DependencyCacheReady ||
		!manifest.Launchable {
		t.Fatalf("manifest = %+v", manifest)
	}
	body, err := os.ReadFile(filepath.Join(manifest.WorkspacePath, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "frozen static guidance\n" {
		t.Fatalf("CLAUDE.md = %q", body)
	}
	if _, err := os.Stat(filepath.Join(manifest.GradleHomePath, "marker")); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(manifest.Command.Arguments, "--strict-mcp-config") ||
		!slices.Contains(manifest.Command.Arguments, "--no-session-persistence") ||
		!slices.Contains(manifest.Command.Arguments, "project") ||
		!slices.Contains(
			manifest.Command.Arguments,
			`{"mcpServers":{}}`,
		) {
		t.Fatalf("arguments = %q", manifest.Command.Arguments)
	}
}

func benchmarkFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"config/task-a-prompt.txt":     "Frozen task prompt.\n",
		"config/codex-benchmark.rules": "frozen codex benchmark rule\n",
		"task/settings.gradle.kts":     "rootProject.name = \"fixture\"\n",
		"task/src/Contract.kt":         "interface Contract\n",
		"arms/static/AGENTS.md":        "frozen static guidance\n",
		"arms/static/CLAUDE.md":        "frozen static guidance\n",
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func benchmarkRunSchedule(
	harness MissionPackBenchmarkHarness,
	arm MissionPackBenchmarkArm,
) MissionPackBenchmarkSchedule {
	return MissionPackBenchmarkSchedule{
		SchemaVersion: MissionPackBenchmarkScheduleSchemaVersion,
		PlanSHA256:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Entries: []MissionPackBenchmarkScheduleEntry{{
			Sequence: 1,
			Block:    1,
			Phase:    "pilot",
			TaskID:   "task_a",
			Harness:  harness,
			Arm:      arm,
			RunID:    "belay-mp-v1-task_a-pilot-b001-" + string(harness) + "-test",
		}},
	}
}
