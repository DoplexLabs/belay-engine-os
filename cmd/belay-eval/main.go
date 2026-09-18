// Command belay-eval runs isolated Belay product-value evaluations.
//
// It is intentionally not part of the end-user Belay command surface.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/evalrun"
)

const maxCapsuleJSONBytes = 4 << 20

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fatal(err)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	var root string
	var output string
	var comparative bool
	var missionPackBenchmarkSchedule bool
	var missionPackBenchmarkPrepare bool
	var missionPackBenchmarkExecute bool
	var missionPackBenchmarkRecurrence bool
	var privateEvalCapsule bool
	var completePrivateEvalCapsule bool
	var materializeSelectionValue bool
	var capsuleDatabase string
	var capsuleProposal string
	var capsuleDraft string
	var capsuleReplay string
	var selectionInput string
	var benchmarkPlan string
	var benchmarkSchedule string
	var benchmarkManifest string
	var benchmarkRecurrenceInput string
	var benchmarkRoot string
	var benchmarkGradleCache string
	var benchmarkSequence int
	var realClaude bool
	var realCodex bool
	var claudeExecutable string
	var claudeModel string
	var codexExecutable string
	var benchmarkCodexModel string
	var model string
	var maxBudgetUSD float64
	var maxTokenOperations int64
	var wallTimeoutSeconds int
	var confirmPaidBenchmarkRun bool
	var repetitions int
	flags := flag.NewFlagSet("belay-eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&root, "root", "", "directory for disposable evaluation state")
	flags.StringVar(&output, "output", "", "optional JSON artifact path")
	flags.BoolVar(
		&comparative,
		"comparative",
		false,
		"run the five-baseline C6 comparative pilot",
	)
	flags.BoolVar(
		&missionPackBenchmarkSchedule,
		"missionpack-benchmark-schedule",
		false,
		"materialize a deterministic Mission Pack benchmark schedule from strict JSON",
	)
	flags.BoolVar(
		&missionPackBenchmarkPrepare,
		"missionpack-benchmark-prepare",
		false,
		"prepare one isolated scheduled Mission Pack benchmark run without invoking a harness",
	)
	flags.BoolVar(
		&missionPackBenchmarkExecute,
		"missionpack-benchmark-execute",
		false,
		"execute and externally score one launchable Mission Pack benchmark manifest",
	)
	flags.BoolVar(
		&missionPackBenchmarkRecurrence,
		"missionpack-benchmark-recurrence",
		false,
		"derive frozen cross-session failed-command fingerprints from benchmark raw streams",
	)
	flags.BoolVar(
		&privateEvalCapsule,
		"private-eval-capsule",
		false,
		"export an evidence-bound private eval capsule from a disposable store copy",
	)
	flags.BoolVar(
		&completePrivateEvalCapsule,
		"complete-private-eval-capsule",
		false,
		"complete a draft private eval capsule from engineering JSON inputs",
	)
	flags.BoolVar(
		&materializeSelectionValue,
		"materialize-selection-value",
		false,
		"build and materialize a selection-value evaluation from strict JSON",
	)
	flags.StringVar(
		&capsuleDatabase,
		"capsule-db",
		"",
		"path to a disposable Belay SQLite copy",
	)
	flags.StringVar(
		&capsuleProposal,
		"capsule-proposal",
		"",
		"current semantic proposal ID to bind into the capsule",
	)
	flags.StringVar(
		&capsuleDraft,
		"capsule-draft",
		"",
		"path to a draft private eval capsule JSON file",
	)
	flags.StringVar(
		&capsuleReplay,
		"capsule-replay",
		"",
		"path to a private eval replay contract JSON file",
	)
	flags.StringVar(
		&selectionInput,
		"selection-input",
		"",
		"path to a selection-value input JSON file",
	)
	flags.StringVar(
		&benchmarkPlan,
		"benchmark-plan",
		"",
		"path to a Mission Pack benchmark plan JSON file",
	)
	flags.StringVar(
		&benchmarkSchedule,
		"benchmark-schedule",
		"",
		"path to a materialized Mission Pack benchmark schedule JSON file",
	)
	flags.StringVar(
		&benchmarkManifest,
		"benchmark-manifest",
		"",
		"path to a prepared Mission Pack benchmark run manifest",
	)
	flags.StringVar(
		&benchmarkRecurrenceInput,
		"benchmark-recurrence-input",
		"",
		"path to a strict Mission Pack benchmark recurrence input JSON file",
	)
	flags.StringVar(
		&benchmarkRoot,
		"benchmark-root",
		"",
		"path to the belay-benchmark-cryptoswift-kmp repository",
	)
	flags.StringVar(
		&benchmarkGradleCache,
		"benchmark-gradle-cache",
		"",
		"path to the frozen benchmark Gradle cache template",
	)
	flags.IntVar(
		&benchmarkSequence,
		"benchmark-sequence",
		0,
		"one-based schedule sequence to prepare",
	)
	flags.BoolVar(
		&realClaude,
		"real-claude",
		false,
		"invoke installed Claude headlessly for semantic proposal generation",
	)
	flags.BoolVar(
		&realCodex,
		"real-codex",
		false,
		"invoke installed Codex ephemerally for the destination session",
	)
	flags.StringVar(
		&claudeExecutable,
		"claude",
		"claude",
		"Claude executable name or path",
	)
	flags.StringVar(
		&claudeModel,
		"claude-model",
		"FOUNDER_SIGN_OFF_REQUIRED",
		"fixed Claude model for the Mission Pack benchmark",
	)
	flags.StringVar(
		&codexExecutable,
		"codex",
		"codex",
		"Codex executable name or path",
	)
	flags.StringVar(
		&benchmarkCodexModel,
		"benchmark-codex-model",
		"FOUNDER_SIGN_OFF_REQUIRED",
		"fixed Codex model for the Mission Pack benchmark",
	)
	flags.StringVar(
		&model,
		"model",
		"openai.gpt-5.6-sol",
		"fixed Codex model for comparative evaluation",
	)
	flags.Float64Var(
		&maxBudgetUSD,
		"max-budget-usd",
		0,
		"fixed per-session benchmark spend cap",
	)
	flags.Int64Var(
		&maxTokenOperations,
		"max-token-operations",
		0,
		"fixed per-session benchmark input-plus-output token cap",
	)
	flags.IntVar(
		&wallTimeoutSeconds,
		"wall-timeout-seconds",
		0,
		"fixed per-session benchmark wall-clock cap",
	)
	flags.BoolVar(
		&confirmPaidBenchmarkRun,
		"confirm-paid-benchmark-run",
		false,
		"confirm that this invocation may contact the pinned model provider and spend the signed budget",
	)
	flags.IntVar(
		&repetitions,
		"repetitions",
		1,
		"paired comparative repetitions (1-5)",
	)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("belay-eval accepts no positional arguments")
	}

	if missionPackBenchmarkSchedule {
		if missionPackBenchmarkPrepare || missionPackBenchmarkExecute ||
			missionPackBenchmarkRecurrence ||
			privateEvalCapsule || completePrivateEvalCapsule ||
			materializeSelectionValue || comparative || realClaude || realCodex {
			return errors.New(
				"Mission Pack benchmark scheduling cannot be combined with other evaluation modes",
			)
		}
		if strings.TrimSpace(benchmarkPlan) == "" {
			return errors.New(
				"--benchmark-plan is required with --missionpack-benchmark-schedule",
			)
		}
	} else if strings.TrimSpace(benchmarkPlan) != "" {
		return errors.New(
			"--benchmark-plan requires --missionpack-benchmark-schedule",
		)
	}
	if missionPackBenchmarkPrepare {
		if missionPackBenchmarkExecute || missionPackBenchmarkRecurrence ||
			privateEvalCapsule || completePrivateEvalCapsule ||
			materializeSelectionValue || comparative || realClaude || realCodex {
			return errors.New(
				"Mission Pack benchmark preparation cannot be combined with other evaluation modes",
			)
		}
		if strings.TrimSpace(benchmarkSchedule) == "" {
			return errors.New(
				"--benchmark-schedule is required with --missionpack-benchmark-prepare",
			)
		}
		if strings.TrimSpace(benchmarkRoot) == "" {
			return errors.New(
				"--benchmark-root is required with --missionpack-benchmark-prepare",
			)
		}
		if benchmarkSequence < 1 {
			return errors.New(
				"--benchmark-sequence must be positive with --missionpack-benchmark-prepare",
			)
		}
		if strings.TrimSpace(root) == "" {
			return errors.New(
				"--root is required with --missionpack-benchmark-prepare",
			)
		}
	} else if strings.TrimSpace(benchmarkSchedule) != "" ||
		strings.TrimSpace(benchmarkRoot) != "" ||
		strings.TrimSpace(benchmarkGradleCache) != "" ||
		benchmarkSequence != 0 {
		return errors.New(
			"benchmark run inputs require --missionpack-benchmark-prepare",
		)
	}
	if missionPackBenchmarkExecute {
		if missionPackBenchmarkRecurrence ||
			privateEvalCapsule || completePrivateEvalCapsule ||
			materializeSelectionValue || comparative || realClaude || realCodex {
			return errors.New(
				"Mission Pack benchmark execution cannot be combined with other evaluation modes",
			)
		}
		if strings.TrimSpace(benchmarkManifest) == "" {
			return errors.New(
				"--benchmark-manifest is required with --missionpack-benchmark-execute",
			)
		}
		if !confirmPaidBenchmarkRun {
			return errors.New(
				"--confirm-paid-benchmark-run is required with --missionpack-benchmark-execute",
			)
		}
	} else if strings.TrimSpace(benchmarkManifest) != "" ||
		confirmPaidBenchmarkRun {
		return errors.New(
			"benchmark execution inputs require --missionpack-benchmark-execute",
		)
	}
	if missionPackBenchmarkRecurrence {
		if privateEvalCapsule || completePrivateEvalCapsule ||
			materializeSelectionValue || comparative || realClaude || realCodex {
			return errors.New(
				"Mission Pack benchmark recurrence analysis cannot be combined with other evaluation modes",
			)
		}
		if strings.TrimSpace(benchmarkRecurrenceInput) == "" {
			return errors.New(
				"--benchmark-recurrence-input is required with --missionpack-benchmark-recurrence",
			)
		}
	} else if strings.TrimSpace(benchmarkRecurrenceInput) != "" {
		return errors.New(
			"--benchmark-recurrence-input requires --missionpack-benchmark-recurrence",
		)
	}

	if completePrivateEvalCapsule {
		if privateEvalCapsule || materializeSelectionValue ||
			comparative || missionPackBenchmarkPrepare ||
			missionPackBenchmarkExecute || missionPackBenchmarkRecurrence ||
			realClaude || realCodex {
			return errors.New(
				"private eval capsule completion cannot be combined with other evaluation modes",
			)
		}
		if strings.TrimSpace(capsuleDatabase) != "" ||
			strings.TrimSpace(capsuleProposal) != "" {
			return errors.New(
				"private eval capsule completion cannot be combined with capsule export inputs",
			)
		}
	} else if strings.TrimSpace(capsuleDraft) != "" ||
		strings.TrimSpace(capsuleReplay) != "" {
		return errors.New(
			"--capsule-draft and --capsule-replay require --complete-private-eval-capsule",
		)
	}
	if materializeSelectionValue {
		if privateEvalCapsule || completePrivateEvalCapsule ||
			comparative || missionPackBenchmarkPrepare ||
			missionPackBenchmarkExecute || missionPackBenchmarkRecurrence ||
			realClaude || realCodex {
			return errors.New(
				"selection value materialization cannot be combined with other evaluation modes",
			)
		}
		if strings.TrimSpace(selectionInput) == "" {
			return errors.New(
				"--selection-input is required with --materialize-selection-value",
			)
		}
	} else if strings.TrimSpace(selectionInput) != "" {
		return errors.New(
			"--selection-input requires --materialize-selection-value",
		)
	}

	if root == "" && !privateEvalCapsule && !completePrivateEvalCapsule &&
		!missionPackBenchmarkSchedule && !missionPackBenchmarkPrepare &&
		!missionPackBenchmarkExecute && !missionPackBenchmarkRecurrence {
		value, err := os.MkdirTemp("", "belay-c5-cross-harness-")
		if err != nil {
			return err
		}
		root = value
	}
	var result any
	if missionPackBenchmarkSchedule {
		var input evalrun.MissionPackBenchmarkPlan
		if err := decodeStrictJSONFile(
			benchmarkPlan,
			"Mission Pack benchmark plan",
			&input,
		); err != nil {
			return err
		}
		value, err := evalrun.BuildMissionPackBenchmarkSchedule(input)
		if err != nil {
			return err
		}
		result = value
	} else if missionPackBenchmarkPrepare {
		var schedule evalrun.MissionPackBenchmarkSchedule
		if err := decodeStrictJSONFile(
			benchmarkSchedule,
			"Mission Pack benchmark schedule",
			&schedule,
		); err != nil {
			return err
		}
		value, err := evalrun.PrepareMissionPackBenchmarkRun(
			context.Background(),
			evalrun.MissionPackBenchmarkPrepareOptions{
				BenchmarkRoot:       benchmarkRoot,
				RunRoot:             root,
				GradleCacheTemplate: benchmarkGradleCache,
				Schedule:            schedule,
				Sequence:            benchmarkSequence,
				ClaudeExecutable:    claudeExecutable,
				CodexExecutable:     codexExecutable,
				ClaudeModel:         claudeModel,
				CodexModel:          benchmarkCodexModel,
				MaxBudgetUSD:        maxBudgetUSD,
				MaxTokenOperations:  maxTokenOperations,
				WallTimeoutSeconds:  wallTimeoutSeconds,
			},
		)
		if err != nil {
			return err
		}
		result = value
	} else if missionPackBenchmarkExecute {
		var manifest evalrun.MissionPackBenchmarkRunManifest
		if err := decodeStrictJSONFile(
			benchmarkManifest,
			"Mission Pack benchmark run manifest",
			&manifest,
		); err != nil {
			return err
		}
		value, err := evalrun.ExecuteMissionPackBenchmarkRun(
			context.Background(),
			manifest,
			confirmPaidBenchmarkRun,
		)
		if err != nil {
			return err
		}
		result = value
	} else if missionPackBenchmarkRecurrence {
		var input evalrun.MissionPackBenchmarkRecurrenceInput
		if err := decodeStrictJSONFile(
			benchmarkRecurrenceInput,
			"Mission Pack benchmark recurrence input",
			&input,
		); err != nil {
			return err
		}
		inputPath, err := filepath.Abs(benchmarkRecurrenceInput)
		if err != nil {
			return err
		}
		for index := range input.Runs {
			if !filepath.IsAbs(input.Runs[index].RawEventsPath) {
				input.Runs[index].RawEventsPath = filepath.Join(
					filepath.Dir(inputPath),
					input.Runs[index].RawEventsPath,
				)
			}
		}
		value, err := evalrun.AnalyzeMissionPackBenchmarkRecurrence(
			context.Background(),
			input,
		)
		if err != nil {
			return err
		}
		result = value
	} else if materializeSelectionValue {
		var input evalrun.SelectionValueInput
		if err := decodeStrictJSONFile(
			selectionInput,
			"selection value input",
			&input,
		); err != nil {
			return err
		}
		value, err := evalrun.PrepareSelectionValueEvaluation(
			context.Background(),
			root,
			input,
		)
		if err != nil {
			return err
		}
		result = value
	} else if completePrivateEvalCapsule {
		value, err := completeCapsuleFromFiles(capsuleDraft, capsuleReplay)
		if err != nil {
			return err
		}
		result = value
	} else if privateEvalCapsule {
		if comparative || missionPackBenchmarkPrepare ||
			missionPackBenchmarkExecute || missionPackBenchmarkRecurrence ||
			realClaude || realCodex {
			return fmt.Errorf(
				"private eval capsule export cannot be combined with harness evaluation modes",
			)
		}
		value, err := evalrun.LoadPrivateEvalCapsule(
			context.Background(),
			capsuleDatabase,
			capsuleProposal,
		)
		if err != nil {
			return err
		}
		result = value
	} else if comparative {
		value, err := evalrun.RunComparativePilot(
			context.Background(),
			root,
			evalrun.ComparativeOptions{
				CodexExecutable: codexExecutable,
				Model:           model,
				Repetitions:     repetitions,
			},
		)
		if err != nil {
			return err
		}
		result = value
	} else {
		value, err := evalrun.RunCrossHarnessProof(
			context.Background(),
			root,
			evalrun.CrossHarnessOptions{
				RealClaude:      realClaude,
				RealCodex:       realCodex,
				CodexExecutable: codexExecutable,
			},
		)
		if err != nil {
			return err
		}
		result = value
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if output != "" {
		path, err := filepath.Abs(output)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, path)
		return err
	}
	_, err = stdout.Write(data)
	return err
}

func completeCapsuleFromFiles(
	draftPath string,
	replayPath string,
) (evalrun.PrivateEvalCapsule, error) {
	if strings.TrimSpace(draftPath) == "" ||
		strings.TrimSpace(replayPath) == "" {
		return evalrun.PrivateEvalCapsule{}, errors.New(
			"--capsule-draft and --capsule-replay are required",
		)
	}
	var draft evalrun.PrivateEvalCapsule
	if err := decodeStrictJSONFile(
		draftPath,
		"private eval capsule draft",
		&draft,
	); err != nil {
		return evalrun.PrivateEvalCapsule{}, err
	}
	var replay evalrun.CapsuleReplayContract
	if err := decodeStrictJSONFile(
		replayPath,
		"private eval capsule replay",
		&replay,
	); err != nil {
		return evalrun.PrivateEvalCapsule{}, err
	}
	return evalrun.CompletePrivateEvalCapsule(draft, replay)
}

func decodeStrictJSONFile(path, label string, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must be a regular file", label)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", label, err)
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxCapsuleJSONBytes+1))
	if err != nil {
		return fmt.Errorf("read %s: %w", label, err)
	}
	if len(body) > maxCapsuleJSONBytes {
		return fmt.Errorf("%s exceeds the size limit", label)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", label, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%s contains a trailing JSON value", label)
		}
		return fmt.Errorf("%s contains trailing JSON: %w", label, err)
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "belay-eval:", err)
	os.Exit(1)
}
