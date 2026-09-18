package evalrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const MissionPackBenchmarkRunManifestSchemaVersion = "belay.missionpack-benchmark-run.v1"

type MissionPackBenchmarkPrepareOptions struct {
	BenchmarkRoot       string
	RunRoot             string
	GradleCacheTemplate string
	Schedule            MissionPackBenchmarkSchedule
	Sequence            int
	ClaudeExecutable    string
	CodexExecutable     string
	ClaudeModel         string
	CodexModel          string
	MaxBudgetUSD        float64
	MaxTokenOperations  int64
	WallTimeoutSeconds  int
}

type MissionPackBenchmarkRunManifest struct {
	SchemaVersion            string                            `json:"schema_version"`
	PreparedAt               time.Time                         `json:"prepared_at"`
	Entry                    MissionPackBenchmarkScheduleEntry `json:"entry"`
	BenchmarkRoot            string                            `json:"benchmark_root"`
	RunRoot                  string                            `json:"run_root"`
	WorkspacePath            string                            `json:"workspace_path"`
	HarnessHomePath          string                            `json:"harness_home_path"`
	BelayHomePath            string                            `json:"belay_home_path"`
	GradleHomePath           string                            `json:"gradle_home_path"`
	RawEventsPath            string                            `json:"raw_events_path"`
	StderrPath               string                            `json:"stderr_path"`
	PromptSHA256             string                            `json:"prompt_sha256"`
	ExecutionPolicyPath      string                            `json:"execution_policy_path,omitempty"`
	ExecutionPolicySHA256    string                            `json:"execution_policy_sha256,omitempty"`
	InstructionChannel       string                            `json:"instruction_channel"`
	MaxBudgetUSD             float64                           `json:"max_budget_usd"`
	MaxTokenOperations       int64                             `json:"max_token_operations"`
	WallTimeoutSeconds       int                               `json:"wall_timeout_seconds"`
	DependencyCacheReady     bool                              `json:"dependency_cache_ready"`
	Launchable               bool                              `json:"launchable"`
	Blockers                 []string                          `json:"blockers,omitempty"`
	Environment              map[string]string                 `json:"environment"`
	InheritedEnvironmentKeys []string                          `json:"inherited_environment_keys"`
	Command                  MissionPackBenchmarkCommand       `json:"command"`
}

type MissionPackBenchmarkCommand struct {
	Executable string   `json:"executable"`
	Arguments  []string `json:"arguments"`
	Directory  string   `json:"directory"`
}

func PrepareMissionPackBenchmarkRun(
	ctx context.Context,
	options MissionPackBenchmarkPrepareOptions,
) (MissionPackBenchmarkRunManifest, error) {
	if err := ValidateMissionPackBenchmarkSchedule(options.Schedule); err != nil {
		return MissionPackBenchmarkRunManifest{}, err
	}
	entry, err := benchmarkScheduleEntry(options.Schedule, options.Sequence)
	if err != nil {
		return MissionPackBenchmarkRunManifest{}, err
	}
	benchmarkRoot, err := existingDirectory(
		options.BenchmarkRoot,
		"benchmark root",
	)
	if err != nil {
		return MissionPackBenchmarkRunManifest{}, err
	}
	runRoot, err := newRunDirectory(options.RunRoot)
	if err != nil {
		return MissionPackBenchmarkRunManifest{}, err
	}
	taskRoot := filepath.Join(
		benchmarkRoot,
		benchmarkTaskDirectory(entry.TaskID),
	)
	if _, err := existingDirectory(taskRoot, "benchmark task root"); err != nil {
		return MissionPackBenchmarkRunManifest{}, err
	}

	workspace := filepath.Join(runRoot, "workspace")
	if err := copyTree(taskRoot, workspace, map[string]bool{
		".gradle": true,
		"build":   true,
	}); err != nil {
		return MissionPackBenchmarkRunManifest{}, fmt.Errorf(
			"copy benchmark task: %w",
			err,
		)
	}
	instructionChannel, appendix, err := installBenchmarkArm(
		benchmarkRoot,
		workspace,
		entry,
	)
	if err != nil {
		return MissionPackBenchmarkRunManifest{}, err
	}
	if err := initializeBenchmarkGit(ctx, workspace); err != nil {
		return MissionPackBenchmarkRunManifest{}, err
	}

	promptPath := filepath.Join(
		benchmarkRoot,
		"config",
		strings.ReplaceAll(entry.TaskID, "_", "-")+"-prompt.txt",
	)
	promptBody, err := os.ReadFile(promptPath)
	if err != nil {
		return MissionPackBenchmarkRunManifest{}, fmt.Errorf(
			"read frozen task prompt: %w",
			err,
		)
	}
	prompt := string(promptBody)
	if strings.TrimSpace(prompt) == "" {
		return MissionPackBenchmarkRunManifest{}, errors.New(
			"frozen task prompt is empty",
		)
	}
	if appendix != "" {
		prompt += "\n" + strings.TrimSpace(appendix)
	}
	promptDigest := sha256.Sum256([]byte(prompt))

	harnessHome := filepath.Join(runRoot, "harness-home")
	belayHome := filepath.Join(runRoot, "belay-home")
	gradleHome := filepath.Join(runRoot, "gradle-home")
	rawRoot := filepath.Join(runRoot, "raw")
	for _, path := range []string{
		harnessHome,
		belayHome,
		gradleHome,
		rawRoot,
		filepath.Join(runRoot, "score"),
	} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return MissionPackBenchmarkRunManifest{}, fmt.Errorf(
				"create isolated run directory: %w",
				err,
			)
		}
	}
	executionPolicyPath, executionPolicySHA256, err :=
		installBenchmarkExecutionPolicy(
			benchmarkRoot,
			harnessHome,
			entry.Harness,
		)
	if err != nil {
		return MissionPackBenchmarkRunManifest{}, err
	}

	cacheReady := false
	if strings.TrimSpace(options.GradleCacheTemplate) != "" {
		template, err := existingDirectory(
			options.GradleCacheTemplate,
			"Gradle cache template",
		)
		if err != nil {
			return MissionPackBenchmarkRunManifest{}, err
		}
		if err := cloneTree(ctx, template, gradleHome); err != nil {
			return MissionPackBenchmarkRunManifest{}, fmt.Errorf(
				"clone Gradle cache template: %w",
				err,
			)
		}
		cacheReady = true
	}

	blockers := make([]string, 0, 3)
	if !cacheReady {
		blockers = append(blockers, "gradle_cache_template_missing")
	}
	if options.MaxBudgetUSD <= 0 {
		blockers = append(blockers, "per_session_budget_not_signed")
	}
	if options.MaxTokenOperations <= 0 {
		blockers = append(blockers, "token_operations_cap_not_signed")
	}
	if options.WallTimeoutSeconds <= 0 {
		blockers = append(blockers, "wall_timeout_not_signed")
	}
	command, environment, commandBlockers := benchmarkHarnessCommand(
		entry,
		workspace,
		harnessHome,
		belayHome,
		gradleHome,
		prompt,
		options,
	)
	blockers = append(blockers, commandBlockers...)

	rawEvents := filepath.Join(rawRoot, string(entry.Harness)+"-events.jsonl")
	stderr := filepath.Join(rawRoot, string(entry.Harness)+"-stderr.log")
	return MissionPackBenchmarkRunManifest{
		SchemaVersion:         MissionPackBenchmarkRunManifestSchemaVersion,
		PreparedAt:            time.Now().UTC(),
		Entry:                 entry,
		BenchmarkRoot:         benchmarkRoot,
		RunRoot:               runRoot,
		WorkspacePath:         workspace,
		HarnessHomePath:       harnessHome,
		BelayHomePath:         belayHome,
		GradleHomePath:        gradleHome,
		RawEventsPath:         rawEvents,
		StderrPath:            stderr,
		PromptSHA256:          hex.EncodeToString(promptDigest[:]),
		ExecutionPolicyPath:   executionPolicyPath,
		ExecutionPolicySHA256: executionPolicySHA256,
		InstructionChannel:    instructionChannel,
		MaxBudgetUSD:          options.MaxBudgetUSD,
		MaxTokenOperations:    options.MaxTokenOperations,
		WallTimeoutSeconds:    options.WallTimeoutSeconds,
		DependencyCacheReady:  cacheReady,
		Launchable:            len(blockers) == 0,
		Blockers:              blockers,
		Environment:           environment,
		InheritedEnvironmentKeys: benchmarkInheritedEnvironmentKeys(
			os.Environ(),
		),
		Command: command,
	}, nil
}

func ValidateMissionPackBenchmarkSchedule(
	schedule MissionPackBenchmarkSchedule,
) error {
	if schedule.SchemaVersion != MissionPackBenchmarkScheduleSchemaVersion {
		return errors.New("unsupported mission-pack benchmark schedule schema")
	}
	if len(schedule.PlanSHA256) != sha256.Size*2 {
		return errors.New("mission-pack benchmark schedule plan hash is invalid")
	}
	if len(schedule.Entries) == 0 {
		return errors.New("mission-pack benchmark schedule is empty")
	}
	seen := make(map[string]bool, len(schedule.Entries))
	phase := ""
	taskID := ""
	for index, entry := range schedule.Entries {
		if entry.Sequence != index+1 {
			return errors.New("mission-pack benchmark schedule sequence is invalid")
		}
		if entry.Block < 1 || entry.RunID == "" {
			return errors.New("mission-pack benchmark schedule entry is invalid")
		}
		switch entry.Phase {
		case "pilot", "phase_a", "phase_b", "phase_c":
		default:
			return errors.New("mission-pack benchmark schedule phase is invalid")
		}
		if entry.TaskID != "task_a" && entry.TaskID != "task_b" {
			return errors.New("mission-pack benchmark schedule task is invalid")
		}
		if index == 0 {
			phase = entry.Phase
			taskID = entry.TaskID
		} else if entry.Phase != phase || entry.TaskID != taskID {
			return errors.New(
				"mission-pack benchmark schedule mixes phases or tasks",
			)
		}
		if seen[entry.RunID] {
			return errors.New("mission-pack benchmark schedule run IDs are not unique")
		}
		seen[entry.RunID] = true
		switch entry.Harness {
		case MissionPackHarnessClaude, MissionPackHarnessCodex:
		default:
			return errors.New("mission-pack benchmark schedule harness is invalid")
		}
		switch entry.Arm {
		case MissionPackArmNoContext,
			MissionPackArmStatic,
			MissionPackArmHuman,
			MissionPackArmPack:
		default:
			return errors.New("mission-pack benchmark schedule arm is invalid")
		}
	}
	return nil
}

func benchmarkTaskDirectory(taskID string) string {
	if taskID == "task_b" {
		return "task-b"
	}
	return "task"
}

func benchmarkScheduleEntry(
	schedule MissionPackBenchmarkSchedule,
	sequence int,
) (MissionPackBenchmarkScheduleEntry, error) {
	if sequence < 1 || sequence > len(schedule.Entries) {
		return MissionPackBenchmarkScheduleEntry{}, errors.New(
			"mission-pack benchmark sequence is outside the schedule",
		)
	}
	return schedule.Entries[sequence-1], nil
}

func existingDirectory(path, label string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", label)
	}
	return absolute, nil
}

func newRunDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("benchmark run root is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve benchmark run root: %w", err)
	}
	entries, err := os.ReadDir(absolute)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(absolute, 0o700); err != nil {
			return "", fmt.Errorf("create benchmark run root: %w", err)
		}
	case err != nil:
		return "", fmt.Errorf("inspect benchmark run root: %w", err)
	case len(entries) != 0:
		return "", errors.New("benchmark run root must be new or empty")
	}
	return absolute, nil
}

func copyTree(
	source string,
	destination string,
	skip map[string]bool,
) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return os.MkdirAll(destination, 0o700)
		}
		if skip[entry.Name()] {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(destination, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported task entry: %s", relative)
		}
		return copyRegularFile(path, target, info.Mode().Perm())
	})
}

func copyRegularFile(source, destination string, mode fs.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}

func cloneTree(ctx context.Context, source, destination string) error {
	sourceContents := source + string(os.PathSeparator) + "."
	arguments := []string{"-R", sourceContents, destination}
	if runtime.GOOS == "darwin" {
		arguments = []string{"-cR", sourceContents, destination}
	}
	command := exec.CommandContext(ctx, "cp", arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"copy dependency cache: %w: %s",
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return nil
}

func installBenchmarkArm(
	benchmarkRoot string,
	workspace string,
	entry MissionPackBenchmarkScheduleEntry,
) (string, string, error) {
	switch entry.Arm {
	case MissionPackArmNoContext:
		return "none", "", nil
	case MissionPackArmStatic:
		fileName := "AGENTS.md"
		if entry.Harness == MissionPackHarnessClaude {
			fileName = "CLAUDE.md"
		}
		source := filepath.Join(benchmarkRoot, "arms", "static", fileName)
		info, err := os.Stat(source)
		if err != nil {
			return "", "", fmt.Errorf("read frozen static arm: %w", err)
		}
		if err := copyRegularFile(
			source,
			filepath.Join(workspace, fileName),
			info.Mode().Perm(),
		); err != nil {
			return "", "", fmt.Errorf("install static arm: %w", err)
		}
		return fileName, "", nil
	case MissionPackArmPack:
		body, err := os.ReadFile(filepath.Join(
			benchmarkRoot,
			"arms",
			"missionpack",
			"PACK.md",
		))
		if err != nil {
			return "", "", fmt.Errorf("read frozen Mission Pack arm: %w", err)
		}
		return "initial_prompt_appendix", string(body), nil
	case MissionPackArmHuman:
		brief := "brief-a.md"
		if entry.Block%2 == 0 {
			brief = "brief-b.md"
		}
		body, err := os.ReadFile(filepath.Join(
			benchmarkRoot,
			"arms",
			"human",
			brief,
		))
		if err != nil {
			return "", "", fmt.Errorf("read frozen human brief arm: %w", err)
		}
		return "initial_prompt_appendix", string(body), nil
	default:
		return "", "", errors.New("unsupported mission-pack benchmark arm")
	}
}

func initializeBenchmarkGit(ctx context.Context, workspace string) error {
	commands := [][]string{
		{"init", "-b", "main", "--quiet"},
		{"add", "--all"},
		{
			"-c", "user.name=Belay Benchmark",
			"-c", "user.email=benchmark@localhost",
			"commit", "--quiet", "-m", "frozen benchmark baseline",
		},
	}
	for _, arguments := range commands {
		command := exec.CommandContext(ctx, "git", arguments...)
		command.Dir = workspace
		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf(
				"initialize benchmark git workspace: %w: %s",
				err,
				strings.TrimSpace(string(output)),
			)
		}
	}
	return nil
}

func benchmarkHarnessCommand(
	entry MissionPackBenchmarkScheduleEntry,
	workspace string,
	harnessHome string,
	belayHome string,
	gradleHome string,
	prompt string,
	options MissionPackBenchmarkPrepareOptions,
) (
	MissionPackBenchmarkCommand,
	map[string]string,
	[]string,
) {
	environment := map[string]string{
		"BELAY_HOME":       belayHome,
		"GRADLE_USER_HOME": gradleHome,
		"HOME":             harnessHome,
		"XDG_CACHE_HOME":   filepath.Join(harnessHome, ".cache"),
		"XDG_CONFIG_HOME":  filepath.Join(harnessHome, ".config"),
		"XDG_DATA_HOME":    filepath.Join(harnessHome, ".local", "share"),
	}
	blockers := make([]string, 0, 1)
	switch entry.Harness {
	case MissionPackHarnessClaude:
		executable := strings.TrimSpace(options.ClaudeExecutable)
		if executable == "" {
			executable = "claude"
		}
		model := strings.TrimSpace(options.ClaudeModel)
		if model == "" || strings.Contains(model, "SIGN_OFF_REQUIRED") {
			blockers = append(blockers, "claude_model_not_signed")
			model = "FOUNDER_SIGN_OFF_REQUIRED"
		}
		environment["CLAUDE_CONFIG_DIR"] = harnessHome
		arguments := []string{
			"--print",
			"--output-format", "stream-json",
			"--verbose",
			"--no-session-persistence",
			"--include-hook-events",
			"--forward-subagent-text",
			"--strict-mcp-config",
			"--mcp-config", `{"mcpServers":{}}`,
			"--disable-slash-commands",
			"--no-chrome",
			"--setting-sources", "project",
			"--tools", "Bash,Edit,Read,Write,Glob,Grep",
			"--permission-mode", "bypassPermissions",
			"--permission-prompts", "none",
			"--max-budget-usd", strconv.FormatFloat(
				options.MaxBudgetUSD,
				'f',
				2,
				64,
			),
			"--model", model,
			prompt,
		}
		return MissionPackBenchmarkCommand{
			Executable: executable,
			Arguments:  arguments,
			Directory:  workspace,
		}, environment, blockers
	case MissionPackHarnessCodex:
		executable := strings.TrimSpace(options.CodexExecutable)
		if executable == "" {
			executable = "codex"
		}
		model := strings.TrimSpace(options.CodexModel)
		if model == "" || strings.Contains(model, "SIGN_OFF_REQUIRED") {
			blockers = append(blockers, "codex_model_not_signed")
			model = "FOUNDER_SIGN_OFF_REQUIRED"
		}
		environment["CODEX_HOME"] = harnessHome
		arguments := []string{
			"exec",
			"--json",
			"--ephemeral",
			"--approve-for-me",
			"--add-dir", gradleHome,
			"--model", model,
			"--cd", workspace,
			prompt,
		}
		return MissionPackBenchmarkCommand{
			Executable: executable,
			Arguments:  arguments,
			Directory:  workspace,
		}, environment, blockers
	default:
		panic("validated benchmark harness was not handled")
	}
}

func installBenchmarkExecutionPolicy(
	benchmarkRoot string,
	harnessHome string,
	harness MissionPackBenchmarkHarness,
) (string, string, error) {
	if harness != MissionPackHarnessCodex {
		return "", "", nil
	}
	source := filepath.Join(
		benchmarkRoot,
		"config",
		"codex-benchmark.rules",
	)
	body, err := os.ReadFile(source)
	if err != nil {
		return "", "", fmt.Errorf(
			"read frozen Codex execution policy: %w",
			err,
		)
	}
	if strings.TrimSpace(string(body)) == "" {
		return "", "", errors.New(
			"frozen Codex execution policy is empty",
		)
	}
	rulesRoot := filepath.Join(harnessHome, "rules")
	if err := os.MkdirAll(rulesRoot, 0o700); err != nil {
		return "", "", fmt.Errorf(
			"create Codex execution policy directory: %w",
			err,
		)
	}
	destination := filepath.Join(rulesRoot, "benchmark.rules")
	if err := os.WriteFile(destination, body, 0o600); err != nil {
		return "", "", fmt.Errorf(
			"write Codex execution policy: %w",
			err,
		)
	}
	digest := sha256.Sum256(body)
	return destination, hex.EncodeToString(digest[:]), nil
}
