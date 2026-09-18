package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/evalrun"
	"github.com/DoplexLabs/belay-engine/internal/experience"
)

func TestRunCompletesPrivateEvalCapsuleThroughOutputPath(t *testing.T) {
	directory := t.TempDir()
	draftPath := filepath.Join(directory, "draft.json")
	replayPath := filepath.Join(directory, "replay.json")
	outputPath := filepath.Join(directory, "result", "capsule.json")
	writeJSONFile(t, draftPath, validDraftCapsule())
	writeJSONFile(t, replayPath, validReplayContract())

	var stdout bytes.Buffer
	if err := run([]string{
		"--complete-private-eval-capsule",
		"--capsule-draft", draftPath,
		"--capsule-replay", replayPath,
		"--output", outputPath,
	}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	absoluteOutput, err := filepath.Abs(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(stdout.String()); got != absoluteOutput {
		t.Fatalf("stdout = %q, want %q", got, absoluteOutput)
	}
	body, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var completed evalrun.PrivateEvalCapsule
	if err := json.Unmarshal(body, &completed); err != nil {
		t.Fatal(err)
	}
	if completed.Readiness != "executable" ||
		completed.Evaluation.TaskState != "reconstructed" ||
		completed.Replay == nil ||
		completed.CapsuleID == "evc_draft" {
		t.Fatalf("completed capsule = %+v", completed)
	}
}

func TestRunRejectsUntrustedCapsuleJSONAndMixedModes(t *testing.T) {
	tests := []struct {
		name          string
		corruptDraft  bool
		corruptReplay bool
		extraArg      string
		want          string
	}{
		{
			name:         "unknown draft field",
			corruptDraft: true,
			want:         "unknown field",
		},
		{
			name:          "trailing replay value",
			corruptReplay: true,
			want:          "trailing JSON value",
		},
		{
			name:     "mixed evaluation modes",
			extraArg: "--comparative",
			want:     "cannot be combined",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			draftPath := filepath.Join(directory, "draft.json")
			replayPath := filepath.Join(directory, "replay.json")
			draft := marshalJSON(t, validDraftCapsule())
			if test.corruptDraft {
				draft = append(
					draft[:len(draft)-1],
					[]byte(`,"unexpected":true}`)...,
				)
			}
			replay := marshalJSON(t, validReplayContract())
			if test.corruptReplay {
				replay = append(replay, []byte("\n{}\n")...)
			}
			if err := os.WriteFile(draftPath, draft, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(replayPath, replay, 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{
				"--complete-private-eval-capsule",
				"--capsule-draft", draftPath,
				"--capsule-replay", replayPath,
			}
			if test.extraArg != "" {
				args = append(args, test.extraArg)
			}
			err := run(args, &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestRunSelectionValueModeRequiresStrictExclusiveInput(t *testing.T) {
	directory := t.TempDir()
	inputPath := filepath.Join(directory, "selection.json")
	if err := os.WriteFile(
		inputPath,
		[]byte(`{"schema_version":"belay.selection-value-input.v1","unexpected":true}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing input",
			args: []string{"--materialize-selection-value"},
			want: "--selection-input is required",
		},
		{
			name: "input without mode",
			args: []string{"--selection-input", inputPath},
			want: "requires --materialize-selection-value",
		},
		{
			name: "mixed mode",
			args: []string{
				"--materialize-selection-value",
				"--selection-input", inputPath,
				"--comparative",
			},
			want: "cannot be combined",
		},
		{
			name: "unknown input field",
			args: []string{
				"--materialize-selection-value",
				"--selection-input", inputPath,
				"--root", filepath.Join(directory, "run"),
			},
			want: "unknown field",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := run(test.args, &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestRunMaterializesMissionPackBenchmarkScheduleFromStrictPlan(
	t *testing.T,
) {
	directory := t.TempDir()
	planPath := filepath.Join(directory, "plan.json")
	outputPath := filepath.Join(directory, "schedule.json")
	plan := evalrun.MissionPackBenchmarkPlan{
		SchemaVersion:     evalrun.MissionPackBenchmarkPlanSchemaVersion,
		StudyID:           "belay-mp-v1",
		Phase:             "pilot",
		TaskID:            "task_a",
		RandomizationSeed: "e16546310598ef48ffddcc1a8f18978400addf714f693b97bdfb7cf4dd20ca11",
		BlocksPerHarness:  4,
		Arms: []evalrun.MissionPackBenchmarkArm{
			evalrun.MissionPackArmNoContext,
		},
		Harnesses: []evalrun.MissionPackBenchmarkHarness{
			evalrun.MissionPackHarnessClaude,
			evalrun.MissionPackHarnessCodex,
		},
	}
	writeJSONFile(t, planPath, plan)
	var stdout bytes.Buffer
	if err := run([]string{
		"--missionpack-benchmark-schedule",
		"--benchmark-plan", planPath,
		"--output", outputPath,
	}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var schedule evalrun.MissionPackBenchmarkSchedule
	if err := json.Unmarshal(body, &schedule); err != nil {
		t.Fatal(err)
	}
	if len(schedule.Entries) != 8 ||
		schedule.Entries[0].RunID == "" ||
		schedule.PlanSHA256 == "" {
		t.Fatalf("schedule = %+v", schedule)
	}

	unknownPath := filepath.Join(directory, "unknown.json")
	if err := os.WriteFile(
		unknownPath,
		[]byte(`{"schema_version":"belay.missionpack-benchmark-plan.v1","unexpected":true}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	err = run([]string{
		"--missionpack-benchmark-schedule",
		"--benchmark-plan", unknownPath,
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown plan error = %v", err)
	}
}

func TestRunPreparesMissionPackBenchmarkRunWithoutHarnessInvocation(
	t *testing.T,
) {
	directory := t.TempDir()
	benchmarkRoot := filepath.Join(directory, "benchmark")
	for name, body := range map[string]string{
		"config/task-a-prompt.txt":     "Frozen task prompt.\n",
		"config/codex-benchmark.rules": "frozen codex benchmark rule\n",
		"task/settings.gradle.kts":     "rootProject.name = \"fixture\"\n",
		"task/src/Contract.kt":         "interface Contract\n",
		"arms/static/AGENTS.md":        "static\n",
		"arms/static/CLAUDE.md":        "static\n",
	} {
		path := filepath.Join(benchmarkRoot, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	schedulePath := filepath.Join(directory, "schedule.json")
	writeJSONFile(t, schedulePath, evalrun.MissionPackBenchmarkSchedule{
		SchemaVersion: evalrun.MissionPackBenchmarkScheduleSchemaVersion,
		PlanSHA256:    strings.Repeat("a", 64),
		Entries: []evalrun.MissionPackBenchmarkScheduleEntry{{
			Sequence: 1,
			Block:    1,
			Phase:    "pilot",
			TaskID:   "task_a",
			Harness:  evalrun.MissionPackHarnessCodex,
			Arm:      evalrun.MissionPackArmNoContext,
			RunID:    "belay-mp-v1-task_a-pilot-b001-codex-n",
		}},
	})
	runRoot := filepath.Join(directory, "run")
	outputPath := filepath.Join(directory, "manifest.json")
	if err := run([]string{
		"--missionpack-benchmark-prepare",
		"--benchmark-schedule", schedulePath,
		"--benchmark-root", benchmarkRoot,
		"--benchmark-sequence", "1",
		"--root", runRoot,
		"--benchmark-codex-model", "openai.gpt-5.6-sol",
		"--max-budget-usd", "10",
		"--max-token-operations", "250000",
		"--wall-timeout-seconds", "2700",
		"--output", outputPath,
	}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest evalrun.MissionPackBenchmarkRunManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion !=
		evalrun.MissionPackBenchmarkRunManifestSchemaVersion ||
		manifest.Entry.RunID == "" ||
		manifest.Command.Executable != "codex" ||
		manifest.Launchable {
		t.Fatalf("manifest = %+v", manifest)
	}
	if !strings.Contains(
		strings.Join(manifest.Blockers, ","),
		"gradle_cache_template_missing",
	) {
		t.Fatalf("blockers = %q", manifest.Blockers)
	}
	err = run([]string{
		"--missionpack-benchmark-execute",
		"--benchmark-manifest", outputPath,
		"--confirm-paid-benchmark-run",
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not launchable") {
		t.Fatalf("execute unlaunchable manifest error = %v", err)
	}
}

func validDraftCapsule() evalrun.PrivateEvalCapsule {
	return evalrun.PrivateEvalCapsule{
		SchemaVersion: evalrun.PrivateEvalCapsuleSchemaVersion,
		CapsuleID:     "evc_draft",
		Readiness:     "draft",
		ReadinessGaps: []string{"task_setup_missing"},
		Evaluation: evalrun.CapsuleEvaluationContract{
			TaskState: "reconstruction_required",
		},
	}
}

func validReplayContract() evalrun.CapsuleReplayContract {
	return evalrun.CapsuleReplayContract{
		SchemaVersion: evalrun.CapsuleReplaySchemaVersion,
		Source: evalrun.CapsuleReplaySource{
			Kind:       "git_revision",
			Repository: "https://example.com/project.git",
			Revision:   "0123456789abcdef0123456789abcdef01234567",
		},
		Task: evalrun.CapsuleReplayTask{
			Prompt:           "Make the bounded change and verify it.",
			WorkingDirectory: ".",
			AllowedMutationPaths: []string{
				"internal/example",
			},
		},
		Outcome: evalrun.CapsuleOutcomeContract{
			Kind:             "command_succeeded",
			Command:          "go test ./internal/example",
			ExpectedExitCode: 0,
		},
		TreatmentVerifier: experience.Verifier{
			Kind: experience.VerifierCommandSucceeded,
			Command: &experience.CommandVerifierSpec{
				Command:          "go test ./internal/example",
				CommandClass:     "go_test",
				ScrubbingVersion: "belay.redaction.v1",
			},
		},
		Isolation: evalrun.CapsuleReplayIsolation{
			Kind:             "disposable_git_worktree",
			ResetBetweenRuns: true,
		},
	}
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.WriteFile(path, marshalJSON(t, value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func marshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
