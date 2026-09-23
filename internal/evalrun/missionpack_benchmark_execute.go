package evalrun

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	acquisition "github.com/DoplexLabs/belay-engine/internal/acquisition/transcript"
)

const MissionPackBenchmarkExecutionSchemaVersion = "belay.missionpack-benchmark-execution.v1"

type MissionPackBenchmarkExecution struct {
	SchemaVersion       string                                `json:"schema_version"`
	Entry               MissionPackBenchmarkScheduleEntry     `json:"entry"`
	StartedAt           time.Time                             `json:"started_at"`
	EndedAt             time.Time                             `json:"ended_at"`
	ElapsedMS           int64                                 `json:"elapsed_ms"`
	HarnessExitCode     int                                   `json:"harness_exit_code"`
	HarnessTimedOut     bool                                  `json:"harness_timed_out"`
	HarnessError        string                                `json:"harness_error,omitempty"`
	RawEventsPath       string                                `json:"raw_events_path"`
	RawEventsSHA256     string                                `json:"raw_events_sha256"`
	StderrPath          string                                `json:"stderr_path"`
	StderrSHA256        string                                `json:"stderr_sha256"`
	WorkspaceDiffPath   string                                `json:"workspace_diff_path"`
	WorkspaceDiffSHA256 string                                `json:"workspace_diff_sha256"`
	WorkspaceStatusPath string                                `json:"workspace_status_path"`
	ScorePath           string                                `json:"score_path"`
	HiddenSuitePassed   bool                                  `json:"hidden_suite_passed"`
	AcceptedCompletion  bool                                  `json:"accepted_completion"`
	UnderCaps           bool                                  `json:"under_caps"`
	WallCapHit          bool                                  `json:"wall_cap_hit"`
	TokenCapHit         bool                                  `json:"token_cap_hit"`
	SpendCapHit         bool                                  `json:"spend_cap_hit"`
	Countable           bool                                  `json:"countable"`
	ExclusionReasons    []string                              `json:"exclusion_reasons,omitempty"`
	Contamination       []MissionPackBenchmarkContamination   `json:"contamination,omitempty"`
	HarnessProvenance   MissionPackBenchmarkHarnessProvenance `json:"harness_provenance"`
	Usage               MissionPackBenchmarkUsage             `json:"usage"`
}

type MissionPackBenchmarkHarnessProvenance struct {
	RequestedExecutable string `json:"requested_executable"`
	ResolvedPath        string `json:"resolved_path"`
	ExecutableSHA256    string `json:"executable_sha256"`
	Version             string `json:"version"`
}

type MissionPackBenchmarkContamination struct {
	Kind    string `json:"kind"`
	Command string `json:"command"`
}

type MissionPackBenchmarkUsage struct {
	Model            string   `json:"model"`
	InputTokens      int64    `json:"input_tokens"`
	OutputTokens     int64    `json:"output_tokens"`
	CacheReadTokens  int64    `json:"cache_read_tokens"`
	CacheWriteTokens int64    `json:"cache_write_tokens"`
	TotalTokens      int64    `json:"total_tokens"`
	TokenOperations  int64    `json:"token_operations"`
	CostUSD          *float64 `json:"cost_usd"`
	CostSource       string   `json:"cost_source"`
}

func ExecuteMissionPackBenchmarkRun(
	ctx context.Context,
	manifest MissionPackBenchmarkRunManifest,
	paidRunConfirmed bool,
) (MissionPackBenchmarkExecution, error) {
	if !paidRunConfirmed {
		return MissionPackBenchmarkExecution{}, errors.New(
			"paid benchmark execution requires explicit confirmation",
		)
	}
	if err := validateExecutableBenchmarkManifest(manifest); err != nil {
		return MissionPackBenchmarkExecution{}, err
	}
	provenance, err := observeMissionPackBenchmarkHarnessProvenance(
		ctx,
		manifest,
	)
	if err != nil {
		return MissionPackBenchmarkExecution{}, err
	}

	rawEvents, err := createExclusiveOutput(manifest.RawEventsPath)
	if err != nil {
		return MissionPackBenchmarkExecution{}, err
	}
	defer rawEvents.Close()
	stderr, err := createExclusiveOutput(manifest.StderrPath)
	if err != nil {
		return MissionPackBenchmarkExecution{}, err
	}
	defer stderr.Close()

	executionContext, cancel := context.WithTimeout(
		ctx,
		time.Duration(manifest.WallTimeoutSeconds)*time.Second,
	)
	defer cancel()
	command := exec.CommandContext(
		executionContext,
		manifest.Command.Executable,
		manifest.Command.Arguments...,
	)
	command.Dir = manifest.Command.Directory
	command.Env = benchmarkEnvironment(
		os.Environ(),
		manifest.Environment,
		manifest.InheritedEnvironmentKeys,
	)
	command.Stdout = rawEvents
	command.Stderr = stderr
	configureBenchmarkProcessGroup(command)
	command.WaitDelay = 5 * time.Second

	startedAt := time.Now().UTC()
	commandErr := command.Run()
	endedAt := time.Now().UTC()
	if err := rawEvents.Sync(); err != nil {
		return MissionPackBenchmarkExecution{}, fmt.Errorf(
			"sync benchmark raw events: %w",
			err,
		)
	}
	if err := stderr.Sync(); err != nil {
		return MissionPackBenchmarkExecution{}, fmt.Errorf(
			"sync benchmark stderr: %w",
			err,
		)
	}

	exitCode := 0
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	} else if commandErr != nil {
		exitCode = -1
	}
	timedOut := errors.Is(executionContext.Err(), context.DeadlineExceeded)
	harnessError := ""
	if commandErr != nil {
		harnessError = commandErr.Error()
	}

	diffPath := filepath.Join(manifest.RunRoot, "raw", "workspace.diff")
	statusPath := filepath.Join(manifest.RunRoot, "raw", "workspace-status.txt")
	if err := captureGitArtifact(
		ctx,
		manifest.WorkspacePath,
		diffPath,
		"diff",
		"--binary",
		"HEAD",
	); err != nil {
		return MissionPackBenchmarkExecution{}, err
	}
	if err := captureGitArtifact(
		ctx,
		manifest.WorkspacePath,
		statusPath,
		"status",
		"--porcelain=v1",
		"--untracked-files=all",
	); err != nil {
		return MissionPackBenchmarkExecution{}, err
	}

	scorePath := filepath.Join(manifest.RunRoot, "score", "result.json")
	scoreContext, scoreCancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		20*time.Minute,
	)
	defer scoreCancel()
	scoreCommand := exec.CommandContext(
		scoreContext,
		filepath.Join(manifest.BenchmarkRoot, "scripts", "score-workspace.sh"),
		manifest.WorkspacePath,
		scorePath,
		manifest.Entry.TaskID,
	)
	scoreCommand.Dir = manifest.BenchmarkRoot
	scoreOutput, scoreErr := scoreCommand.CombinedOutput()
	if scoreErr != nil {
		return MissionPackBenchmarkExecution{}, fmt.Errorf(
			"score benchmark workspace: %w: %s",
			scoreErr,
			strings.TrimSpace(string(scoreOutput)),
		)
	}
	accepted, err := benchmarkScoreAccepted(scorePath)
	if err != nil {
		return MissionPackBenchmarkExecution{}, err
	}

	rawBody, err := os.ReadFile(manifest.RawEventsPath)
	if err != nil {
		return MissionPackBenchmarkExecution{}, fmt.Errorf(
			"read benchmark raw events: %w",
			err,
		)
	}
	usage := observeMissionPackBenchmarkUsage(
		manifest.Entry.Harness,
		manifest.Command.Arguments,
		rawBody,
	)
	contamination := observeMissionPackBenchmarkContamination(
		manifest.Entry.Harness,
		rawBody,
	)
	exclusionReasons := make([]string, 0, 1)
	if reason := observeMissionPackBenchmarkInfrastructureFailure(
		manifest.Entry.Harness,
		rawBody,
	); reason != "" {
		exclusionReasons = append(exclusionReasons, reason)
	}
	if usage.CostUSD == nil {
		exclusionReasons = append(
			exclusionReasons,
			"cost_unavailable_for_spend_cap",
		)
	}
	wallCapHit := timedOut
	tokenCapHit := usage.TokenOperations > manifest.MaxTokenOperations
	spendCapHit := usage.CostUSD != nil &&
		*usage.CostUSD > manifest.MaxBudgetUSD
	underCaps := !wallCapHit &&
		!tokenCapHit &&
		!spendCapHit &&
		usage.CostUSD != nil
	rawHash, err := sha256Path(manifest.RawEventsPath)
	if err != nil {
		return MissionPackBenchmarkExecution{}, err
	}
	stderrHash, err := sha256Path(manifest.StderrPath)
	if err != nil {
		return MissionPackBenchmarkExecution{}, err
	}
	diffHash, err := sha256Path(diffPath)
	if err != nil {
		return MissionPackBenchmarkExecution{}, err
	}
	return MissionPackBenchmarkExecution{
		SchemaVersion:       MissionPackBenchmarkExecutionSchemaVersion,
		Entry:               manifest.Entry,
		StartedAt:           startedAt,
		EndedAt:             endedAt,
		ElapsedMS:           endedAt.Sub(startedAt).Milliseconds(),
		HarnessExitCode:     exitCode,
		HarnessTimedOut:     timedOut,
		HarnessError:        harnessError,
		RawEventsPath:       manifest.RawEventsPath,
		RawEventsSHA256:     rawHash,
		StderrPath:          manifest.StderrPath,
		StderrSHA256:        stderrHash,
		WorkspaceDiffPath:   diffPath,
		WorkspaceDiffSHA256: diffHash,
		WorkspaceStatusPath: statusPath,
		ScorePath:           scorePath,
		HiddenSuitePassed:   accepted,
		AcceptedCompletion:  accepted && underCaps && len(contamination) == 0,
		UnderCaps:           underCaps,
		WallCapHit:          wallCapHit,
		TokenCapHit:         tokenCapHit,
		SpendCapHit:         spendCapHit,
		Countable:           len(exclusionReasons) == 0,
		ExclusionReasons:    exclusionReasons,
		Contamination:       contamination,
		HarnessProvenance:   provenance,
		Usage:               usage,
	}, nil
}

func observeMissionPackBenchmarkInfrastructureFailure(
	harness MissionPackBenchmarkHarness,
	body []byte,
) string {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for scanner.Scan() {
		var event struct {
			Type  string `json:"type"`
			Error any    `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		encoded, _ := json.Marshal(event.Error)
		message := strings.ToLower(string(encoded))
		switch harness {
		case MissionPackHarnessClaude:
			if strings.Contains(message, "authentication_failed") ||
				strings.Contains(message, "not logged in") {
				return "harness_authentication_failure"
			}
		case MissionPackHarnessCodex:
			if strings.Contains(message, "unauthorized") ||
				strings.Contains(message, "failed to load aws credentials") ||
				strings.Contains(message, "missing bearer") {
				return "harness_authentication_failure"
			}
		}
	}
	return ""
}

func observeMissionPackBenchmarkHarnessProvenance(
	ctx context.Context,
	manifest MissionPackBenchmarkRunManifest,
) (MissionPackBenchmarkHarnessProvenance, error) {
	requested := strings.TrimSpace(manifest.Command.Executable)
	resolved, err := exec.LookPath(requested)
	if err != nil {
		return MissionPackBenchmarkHarnessProvenance{}, fmt.Errorf(
			"resolve benchmark harness executable: %w",
			err,
		)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return MissionPackBenchmarkHarnessProvenance{}, fmt.Errorf(
			"resolve benchmark harness path: %w",
			err,
		)
	}
	if target, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil {
		resolved = target
	}
	digest, err := sha256Path(resolved)
	if err != nil {
		return MissionPackBenchmarkHarnessProvenance{}, err
	}
	versionContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	versionCommand := exec.CommandContext(versionContext, resolved, "--version")
	versionCommand.Dir = manifest.WorkspacePath
	versionCommand.Env = benchmarkEnvironment(
		os.Environ(),
		manifest.Environment,
		manifest.InheritedEnvironmentKeys,
	)
	versionOutput, err := versionCommand.CombinedOutput()
	if err != nil {
		return MissionPackBenchmarkHarnessProvenance{}, fmt.Errorf(
			"read benchmark harness version: %w: %s",
			err,
			strings.TrimSpace(string(versionOutput)),
		)
	}
	version := strings.TrimSpace(string(versionOutput))
	if version == "" {
		return MissionPackBenchmarkHarnessProvenance{}, errors.New(
			"benchmark harness version is empty",
		)
	}
	if len(version) > 512 {
		version = version[:512]
	}
	return MissionPackBenchmarkHarnessProvenance{
		RequestedExecutable: requested,
		ResolvedPath:        resolved,
		ExecutableSHA256:    digest,
		Version:             version,
	}, nil
}

var benchmarkNetworkCommandPatterns = []*regexp.Regexp{
	regexp.MustCompile(
		`(?i)(^|[\s;&|()])(?:curl|wget|ftp|sftp|scp|ssh|nc|ncat|netcat|telnet)(?:\s|$)`,
	),
	regexp.MustCompile(
		`(?i)(^|[\s;&|()])git\s+(?:clone|fetch|pull|push|ls-remote)(?:\s|$)`,
	),
	regexp.MustCompile(
		`(?i)(^|[\s;&|()])gh\s+(?:api|repo\s+clone|release\s+download)(?:\s|$)`,
	),
	regexp.MustCompile(
		`(?i)(^|[\s;&|()])(?:npm|pnpm|yarn|pip|pip3|cargo|brew)\s+(?:add|ci|download|fetch|install|search|update|view)(?:\s|$)`,
	),
	regexp.MustCompile(
		`(?i)(^|[\s;&|()])go\s+(?:get|install|mod\s+download)(?:\s|$)`,
	),
}

func observeMissionPackBenchmarkContamination(
	harness MissionPackBenchmarkHarness,
	body []byte,
) []MissionPackBenchmarkContamination {
	commands := benchmarkHarnessCommands(harness, body)
	result := make([]MissionPackBenchmarkContamination, 0)
	for _, command := range commands {
		kind := benchmarkNetworkCommandKind(command)
		if kind == "" {
			continue
		}
		result = append(result, MissionPackBenchmarkContamination{
			Kind:    kind,
			Command: command,
		})
	}
	return result
}

func benchmarkHarnessCommands(
	harness MissionPackBenchmarkHarness,
	body []byte,
) []string {
	commands := make([]string, 0)
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for scanner.Scan() {
		switch harness {
		case MissionPackHarnessCodex:
			var event struct {
				Type string `json:"type"`
				Item *struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"item"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) == nil &&
				event.Type == "item.completed" &&
				event.Item != nil &&
				event.Item.Type == "command_execution" &&
				strings.TrimSpace(event.Item.Command) != "" {
				commands = append(commands, event.Item.Command)
			}
		case MissionPackHarnessClaude:
			var event struct {
				Type    string `json:"type"`
				Message struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) != nil ||
				event.Type != "assistant" ||
				len(event.Message.Content) == 0 {
				continue
			}
			var blocks []struct {
				Type  string `json:"type"`
				Name  string `json:"name"`
				Input struct {
					Command string `json:"command"`
				} `json:"input"`
			}
			if json.Unmarshal(event.Message.Content, &blocks) != nil {
				continue
			}
			for _, block := range blocks {
				if block.Type == "tool_use" &&
					(block.Name == "Bash" || block.Name == "Shell") &&
					strings.TrimSpace(block.Input.Command) != "" {
					commands = append(commands, block.Input.Command)
				}
			}
		}
	}
	return commands
}

func benchmarkNetworkCommandKind(command string) string {
	for _, pattern := range benchmarkNetworkCommandPatterns {
		if pattern.MatchString(command) {
			return "network_capable_command"
		}
	}
	lower := strings.ToLower(command)
	if (strings.Contains(lower, "gradlew") ||
		regexp.MustCompile(`(^|[\s;&|()])gradle(?:\s|$)`).MatchString(lower)) &&
		!strings.Contains(lower, "--offline") {
		return "gradle_without_offline_mode"
	}
	return ""
}

func validateExecutableBenchmarkManifest(
	manifest MissionPackBenchmarkRunManifest,
) error {
	if manifest.SchemaVersion != MissionPackBenchmarkRunManifestSchemaVersion {
		return errors.New("unsupported mission-pack benchmark run manifest schema")
	}
	if !manifest.Launchable || len(manifest.Blockers) != 0 {
		return errors.New("mission-pack benchmark run manifest is not launchable")
	}
	if manifest.MaxBudgetUSD <= 0 ||
		manifest.MaxTokenOperations <= 0 ||
		manifest.WallTimeoutSeconds <= 0 {
		return errors.New("mission-pack benchmark run caps are invalid")
	}
	if strings.TrimSpace(manifest.Command.Executable) == "" ||
		len(manifest.Command.Arguments) == 0 {
		return errors.New("mission-pack benchmark command is incomplete")
	}
	if manifest.Command.Directory != manifest.WorkspacePath {
		return errors.New("mission-pack benchmark command directory is invalid")
	}
	if err := validateBenchmarkEnvironment(manifest); err != nil {
		return err
	}
	for _, value := range []struct {
		label string
		path  string
		root  string
	}{
		{"workspace", manifest.WorkspacePath, manifest.RunRoot},
		{"harness home", manifest.HarnessHomePath, manifest.RunRoot},
		{"Belay home", manifest.BelayHomePath, manifest.RunRoot},
		{"Gradle home", manifest.GradleHomePath, manifest.RunRoot},
		{"raw events", manifest.RawEventsPath, manifest.RunRoot},
		{"stderr", manifest.StderrPath, manifest.RunRoot},
	} {
		if !pathWithin(value.root, value.path) {
			return fmt.Errorf("benchmark %s escapes the run root", value.label)
		}
	}
	if _, err := existingDirectory(manifest.BenchmarkRoot, "benchmark root"); err != nil {
		return err
	}
	if _, err := existingDirectory(manifest.WorkspacePath, "benchmark workspace"); err != nil {
		return err
	}
	switch manifest.Entry.Harness {
	case MissionPackHarnessCodex:
		if !pathWithin(manifest.HarnessHomePath, manifest.ExecutionPolicyPath) {
			return errors.New(
				"benchmark execution policy escapes the harness home",
			)
		}
		digest, err := sha256Path(manifest.ExecutionPolicyPath)
		if err != nil {
			return fmt.Errorf(
				"hash benchmark execution policy: %w",
				err,
			)
		}
		if digest != manifest.ExecutionPolicySHA256 {
			return errors.New(
				"benchmark execution policy hash is invalid",
			)
		}
	case MissionPackHarnessClaude:
		if manifest.ExecutionPolicyPath != "" ||
			manifest.ExecutionPolicySHA256 != "" {
			return errors.New(
				"Claude benchmark execution policy is invalid",
			)
		}
	}
	return nil
}

func pathWithin(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative != "." &&
		relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func createExclusiveOutput(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create benchmark output directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create benchmark output: %w", err)
	}
	return file, nil
}

func benchmarkEnvironment(
	parent []string,
	overrides map[string]string,
	inheritedKeys []string,
) []string {
	allowed := make(map[string]bool, len(inheritedKeys))
	for _, key := range inheritedKeys {
		if benchmarkEnvironmentKeyAllowed(key) {
			allowed[key] = true
		}
	}
	values := make(map[string]string, len(overrides)+len(allowed)+3)
	for _, item := range parent {
		key, value, ok := strings.Cut(item, "=")
		if ok && allowed[key] {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	values["CI"] = "1"
	values["NO_COLOR"] = "1"
	values["TERM"] = "dumb"
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func benchmarkInheritedEnvironmentKeys(parent []string) []string {
	seen := make(map[string]bool)
	keys := make([]string, 0)
	for _, item := range parent {
		key, _, ok := strings.Cut(item, "=")
		if !ok || seen[key] || !benchmarkEnvironmentKeyAllowed(key) {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func benchmarkEnvironmentKeyAllowed(key string) bool {
	switch key {
	case "PATH",
		"TMPDIR",
		"TMP",
		"TEMP",
		"LANG",
		"LANGUAGE",
		"LC_ALL",
		"LC_CTYPE",
		"TZ",
		"SHELL",
		"SSL_CERT_FILE",
		"SSL_CERT_DIR",
		"NODE_EXTRA_CA_CERTS",
		"HTTP_PROXY",
		"HTTPS_PROXY",
		"NO_PROXY",
		"http_proxy",
		"https_proxy",
		"no_proxy",
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_BASE_URL",
		"CODEX_API_KEY",
		"CODEX_API_KEY_FILE",
		"OPENAI_API_KEY",
		"OPENAI_BASE_URL":
		return true
	}
	for _, prefix := range []string{
		"AWS_",
		"AMAZON_BEDROCK_",
		"BEDROCK_",
		"CLAUDE_CODE_USE_",
	} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func validateBenchmarkEnvironment(
	manifest MissionPackBenchmarkRunManifest,
) error {
	expected := map[string]string{
		"BELAY_HOME":       manifest.BelayHomePath,
		"GRADLE_USER_HOME": manifest.GradleHomePath,
		"HOME":             manifest.HarnessHomePath,
		"XDG_CACHE_HOME": filepath.Join(
			manifest.HarnessHomePath,
			".cache",
		),
		"XDG_CONFIG_HOME": filepath.Join(
			manifest.HarnessHomePath,
			".config",
		),
		"XDG_DATA_HOME": filepath.Join(
			manifest.HarnessHomePath,
			".local",
			"share",
		),
	}
	switch manifest.Entry.Harness {
	case MissionPackHarnessClaude:
		expected["CLAUDE_CONFIG_DIR"] = manifest.HarnessHomePath
	case MissionPackHarnessCodex:
		expected["CODEX_HOME"] = manifest.HarnessHomePath
	default:
		return errors.New("mission-pack benchmark harness is invalid")
	}
	if len(manifest.Environment) != len(expected) {
		return errors.New("mission-pack benchmark environment is invalid")
	}
	for key, value := range expected {
		if manifest.Environment[key] != value {
			return fmt.Errorf(
				"mission-pack benchmark environment %s is invalid",
				key,
			)
		}
	}
	seen := make(map[string]bool, len(manifest.InheritedEnvironmentKeys))
	for _, key := range manifest.InheritedEnvironmentKeys {
		if seen[key] || !benchmarkEnvironmentKeyAllowed(key) {
			return fmt.Errorf(
				"mission-pack benchmark inherited environment key %s is invalid",
				key,
			)
		}
		seen[key] = true
	}
	return nil
}

func captureGitArtifact(
	ctx context.Context,
	workspace string,
	outputPath string,
	arguments ...string,
) error {
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = workspace
	body, err := command.Output()
	if err != nil {
		return fmt.Errorf("capture benchmark git artifact: %w", err)
	}
	if err := os.WriteFile(outputPath, body, 0o600); err != nil {
		return fmt.Errorf("write benchmark git artifact: %w", err)
	}
	return nil
}

func benchmarkScoreAccepted(path string) (bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read benchmark score: %w", err)
	}
	var value struct {
		SchemaVersion      string `json:"schema_version"`
		AcceptedCompletion bool   `json:"accepted_completion"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		var loose map[string]json.RawMessage
		if json.Unmarshal(body, &loose) != nil {
			return false, fmt.Errorf("decode benchmark score: %w", err)
		}
		if raw, ok := loose["accepted_completion"]; ok {
			if json.Unmarshal(raw, &value.AcceptedCompletion) != nil {
				return false, errors.New("benchmark score acceptance is invalid")
			}
		} else {
			return false, errors.New("benchmark score acceptance is missing")
		}
	}
	return value.AcceptedCompletion, nil
}

func observeMissionPackBenchmarkUsage(
	harness MissionPackBenchmarkHarness,
	arguments []string,
	body []byte,
) MissionPackBenchmarkUsage {
	model := argumentValue(arguments, "--model")
	usage := MissionPackBenchmarkUsage{Model: model}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	var directCost *float64
	for scanner.Scan() {
		switch harness {
		case MissionPackHarnessCodex:
			var event struct {
				Type  string `json:"type"`
				Usage struct {
					InputTokens           int64 `json:"input_tokens"`
					OutputTokens          int64 `json:"output_tokens"`
					CachedInputTokens     int64 `json:"cached_input_tokens"`
					CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) == nil &&
				event.Type == "turn.completed" {
				usage.InputTokens += event.Usage.InputTokens
				usage.OutputTokens += event.Usage.OutputTokens
				usage.CacheReadTokens += event.Usage.CachedInputTokens
				usage.CacheWriteTokens += event.Usage.CacheWriteInputTokens
			}
		case MissionPackHarnessClaude:
			var event struct {
				Type    string `json:"type"`
				Message *struct {
					Model string `json:"model"`
					Usage struct {
						InputTokens              int64 `json:"input_tokens"`
						OutputTokens             int64 `json:"output_tokens"`
						CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
						CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
					} `json:"usage"`
				} `json:"message"`
				TotalCostUSD *float64 `json:"total_cost_usd"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) != nil {
				continue
			}
			if event.Message != nil && event.Type == "assistant" {
				if event.Message.Model != "" {
					usage.Model = event.Message.Model
				}
				usage.InputTokens += event.Message.Usage.InputTokens
				usage.OutputTokens += event.Message.Usage.OutputTokens
				usage.CacheReadTokens += event.Message.Usage.CacheReadInputTokens
				usage.CacheWriteTokens += event.Message.Usage.CacheCreationInputTokens
			}
			if event.TotalCostUSD != nil {
				value := *event.TotalCostUSD
				directCost = &value
			}
		}
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	usage.TokenOperations = usage.TotalTokens
	if harness == MissionPackHarnessClaude {
		usage.TokenOperations +=
			usage.CacheReadTokens + usage.CacheWriteTokens
	}
	if directCost != nil {
		usage.CostUSD = directCost
		usage.CostSource = "harness_reported"
		return usage
	}
	usage.CostUSD = acquisition.EstimateCostUSD(
		usage.Model,
		acquisition.PricingUsage{
			InputTokens:       &usage.InputTokens,
			OutputTokens:      &usage.OutputTokens,
			CacheReadTokens:   &usage.CacheReadTokens,
			CacheWriteTokens:  &usage.CacheWriteTokens,
			InputIncludesRead: harness == MissionPackHarnessCodex,
		},
	)
	if usage.CostUSD == nil {
		usage.CostSource = "unknown_model"
	} else {
		usage.CostSource = acquisition.PriceTableVersion
	}
	return usage
}

func argumentValue(arguments []string, name string) string {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == name {
			return arguments[index+1]
		}
	}
	return ""
}

func sha256Path(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open artifact for hashing: %w", err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("hash artifact: %w", err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
