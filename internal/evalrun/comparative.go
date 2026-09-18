package evalrun

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	acquisition "github.com/DoplexLabs/belay-engine/internal/acquisition/transcript"
)

type ComparativeBaseline string

const (
	BaselineNoMemory      ComparativeBaseline = "no_memory"
	BaselineStaticFile    ComparativeBaseline = "static_instruction_file"
	BaselineNativeMemory  ComparativeBaseline = "native_harness_memory"
	BaselineRetrievalOnly ComparativeBaseline = "retrieval_only_belay"
	BaselineCompiledBelay ComparativeBaseline = "compiled_belay_verifier"
)

var comparativeBaselines = []ComparativeBaseline{
	BaselineNoMemory,
	BaselineStaticFile,
	BaselineNativeMemory,
	BaselineRetrievalOnly,
	BaselineCompiledBelay,
}

type ComparativeOptions struct {
	CodexExecutable string
	Model           string
	Repetitions     int
}

type ComparativeResult struct {
	SchemaVersion     string             `json:"schema_version"`
	ExperimentStatus  string             `json:"experiment_status"`
	Task              string             `json:"task"`
	TargetRule        string             `json:"target_rule"`
	Model             string             `json:"model"`
	PriceTableVersion string             `json:"price_table_version"`
	ScoringMode       string             `json:"scoring_mode"`
	Runs              []ComparativeRun   `json:"runs"`
	Summary           ComparativeSummary `json:"summary"`
	CompletedAt       time.Time          `json:"completed_at"`
}

type ComparativeRun struct {
	Baseline               ComparativeBaseline  `json:"baseline"`
	Repetition             int                  `json:"repetition"`
	InstructionChannel     string               `json:"instruction_channel"`
	RawEventsPath          string               `json:"raw_events_path"`
	TaskSuccess            bool                 `json:"task_success"`
	ApplicableViolations   int                  `json:"applicable_violations"`
	Corrections            int                  `json:"corrections"`
	VerificationCompliance bool                 `json:"verification_compliance"`
	VerifierState          string               `json:"verifier_state,omitempty"`
	CommandAttempts        int                  `json:"command_attempts"`
	FailedAttempts         int                  `json:"failed_attempts"`
	CompliantAttempts      int                  `json:"compliant_attempts"`
	LatencyMS              int64                `json:"latency_ms"`
	InputTokens            int64                `json:"input_tokens"`
	CacheReadTokens        int64                `json:"cache_read_tokens"`
	CacheWriteTokens       int64                `json:"cache_write_tokens"`
	OutputTokens           int64                `json:"output_tokens"`
	TotalTokens            int64                `json:"total_tokens"`
	CostUSD                *float64             `json:"cost_usd"`
	Commands               []ComparativeCommand `json:"commands"`
	FinalMessage           string               `json:"final_message,omitempty"`
	Error                  string               `json:"error,omitempty"`
}

type ComparativeCommand struct {
	Command  string `json:"command"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Status   string `json:"status"`
}

type ComparativeSummary struct {
	BaselinesCompleted  int                          `json:"baselines_completed"`
	TaskSuccesses       int                          `json:"task_successes"`
	TotalViolations     int                          `json:"total_violations"`
	TotalFailedAttempts int                          `json:"total_failed_attempts"`
	TotalTokens         int64                        `json:"total_tokens"`
	TotalCostUSD        *float64                     `json:"total_cost_usd"`
	ByBaseline          []ComparativeBaselineSummary `json:"by_baseline"`
	CompiledVsRetrieval string                       `json:"compiled_vs_retrieval"`
	Decision            string                       `json:"decision"`
}

type ComparativeBaselineSummary struct {
	Baseline             ComparativeBaseline `json:"baseline"`
	Runs                 int                 `json:"runs"`
	TaskSuccesses        int                 `json:"task_successes"`
	ApplicableViolations int                 `json:"applicable_violations"`
	FailedAttempts       int                 `json:"failed_attempts"`
	MeanLatencyMS        float64             `json:"mean_latency_ms"`
	MeanTokens           float64             `json:"mean_tokens"`
	TotalCostUSD         *float64            `json:"total_cost_usd"`
}

func RunComparativePilot(
	ctx context.Context,
	root string,
	options ComparativeOptions,
) (ComparativeResult, error) {
	if ctx == nil {
		return ComparativeResult{}, errors.New(
			"comparative evaluation requires context",
		)
	}
	if strings.TrimSpace(root) == "" {
		return ComparativeResult{}, errors.New(
			"comparative evaluation requires a root",
		)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return ComparativeResult{}, fmt.Errorf(
			"resolve comparative root: %w",
			err,
		)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return ComparativeResult{}, fmt.Errorf(
			"create comparative root: %w",
			err,
		)
	}
	executable := strings.TrimSpace(options.CodexExecutable)
	if executable == "" {
		executable = "codex"
	}
	executable, err = exec.LookPath(executable)
	if err != nil {
		return ComparativeResult{}, fmt.Errorf(
			"resolve installed Codex executable: %w",
			err,
		)
	}
	model := strings.TrimSpace(options.Model)
	if model == "" {
		model = "openai.gpt-5.6-sol"
	}
	repetitions := options.Repetitions
	if repetitions == 0 {
		repetitions = 1
	}
	if repetitions < 1 || repetitions > 5 {
		return ComparativeResult{}, errors.New(
			"comparative repetitions must be between 1 and 5",
		)
	}
	const task = "Run go test ./... to verify this repository. Do not modify project files. Report the exact command and whether it passed."
	result := ComparativeResult{
		SchemaVersion:     "belay.comparative-evaluation.v1",
		ExperimentStatus:  "paired-repeated-pilot",
		Task:              task,
		TargetRule:        "Run Go tests with the baseline-specific writable GOCACHE path.",
		Model:             model,
		PriceTableVersion: acquisition.PriceTableVersion,
		ScoringMode:       "exact_command_and_exit_code",
		Runs: make(
			[]ComparativeRun,
			0,
			len(comparativeBaselines)*repetitions,
		),
	}
	for repetition := 1; repetition <= repetitions; repetition++ {
		for offset := range comparativeBaselines {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			index := (offset + repetition - 1) % len(comparativeBaselines)
			baseline := comparativeBaselines[index]
			run := runComparativeBaseline(
				ctx,
				root,
				executable,
				model,
				task,
				baseline,
				repetition,
			)
			result.Runs = append(result.Runs, run)
		}
	}
	result.Summary = summarizeComparativeRuns(result.Runs)
	result.CompletedAt = time.Now().UTC().Round(0)
	return result, nil
}

func runComparativeBaseline(
	ctx context.Context,
	root string,
	executable string,
	model string,
	task string,
	baseline ComparativeBaseline,
	repetition int,
) ComparativeRun {
	run := ComparativeRun{
		Baseline:   baseline,
		Repetition: repetition,
		Commands:   []ComparativeCommand{},
	}
	runRoot := filepath.Join(
		root,
		fmt.Sprintf("repetition-%02d", repetition),
		string(baseline),
	)
	projectPath := filepath.Join(runRoot, "project")
	if err := initializeProject(ctx, projectPath); err != nil {
		run.Error = err.Error()
		return run
	}
	cachePath := filepath.Join(
		"/tmp",
		fmt.Sprintf(
			"belay-c6-go-cache-%s-%02d",
			baseline,
			repetition,
		),
	)
	blockedCache := filepath.Join(runRoot, "blocked-go-cache")
	if err := os.WriteFile(blockedCache, []byte("not a directory\n"), 0o600); err != nil {
		run.Error = fmt.Sprintf("create blocked cache: %v", err)
		return run
	}
	rule := "Run Go tests with GOCACHE=" + cachePath + "."
	prompt := task
	extraArgs := make([]string, 0)
	switch baseline {
	case BaselineNoMemory:
		run.InstructionChannel = "none"
	case BaselineStaticFile:
		run.InstructionChannel = "AGENTS.md"
		if err := os.WriteFile(
			filepath.Join(projectPath, "AGENTS.md"),
			[]byte("# Project verification\n\n"+rule+"\n"),
			0o600,
		); err != nil {
			run.Error = fmt.Sprintf("write static instructions: %v", err)
			return run
		}
	case BaselineNativeMemory:
		run.InstructionChannel = "codex.developer_instructions"
		extraArgs = append(
			extraArgs,
			"-c",
			"developer_instructions="+strconv.Quote(rule),
		)
	case BaselineRetrievalOnly:
		run.InstructionChannel = "retrieved_prompt"
		prompt = "Belay retrieved guidance (not verified): " + rule + "\n\n" + task
	case BaselineCompiledBelay:
		run.InstructionChannel = "approved_mission_pack"
		prompt = strings.Join([]string{
			"Belay Mission Pack — user-approved project guidance:",
			rule,
			"Deterministic verifier: the Go test command must succeed.",
			"",
			task,
		}, "\n")
		run.VerifierState = "pending"
	default:
		run.Error = "unsupported baseline"
		return run
	}

	args := []string{
		"exec",
		"--json",
		"--ephemeral",
		"--approve-for-me",
		"--ignore-rules",
		"--model",
		model,
	}
	args = append(args, extraArgs...)
	args = append(args, "-C", projectPath, prompt)
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = projectPath
	command.Env = append(os.Environ(), "GOCACHE="+blockedCache)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	startedAt := time.Now()
	commandErr := command.Run()
	run.LatencyMS = time.Since(startedAt).Milliseconds()
	eventsPath := filepath.Join(runRoot, "codex-events.jsonl")
	run.RawEventsPath = eventsPath
	if err := os.WriteFile(eventsPath, stdout.Bytes(), 0o600); err != nil {
		run.Error = fmt.Sprintf("write events: %v", err)
		return run
	}
	observed, err := parseComparativeCodexEvents(
		stdout.Bytes(),
		cachePath,
	)
	if err != nil {
		run.Error = err.Error()
		return run
	}
	run.Commands = observed.Commands
	run.FinalMessage = observed.FinalMessage
	run.CommandAttempts = observed.CommandAttempts
	run.FailedAttempts = observed.FailedAttempts
	run.CompliantAttempts = observed.CompliantAttempts
	run.TaskSuccess = observed.SuccessfulAttempts > 0
	run.VerificationCompliance = run.TaskSuccess
	if observed.CompliantAttempts == 0 {
		run.ApplicableViolations = 1
	}
	run.InputTokens = observed.InputTokens
	run.CacheReadTokens = observed.CacheReadTokens
	run.CacheWriteTokens = observed.CacheWriteTokens
	run.OutputTokens = observed.OutputTokens
	run.TotalTokens = run.InputTokens + run.OutputTokens
	run.CostUSD = acquisition.EstimateCostUSD(
		model,
		acquisition.PricingUsage{
			InputTokens:       int64Pointer(run.InputTokens),
			OutputTokens:      int64Pointer(run.OutputTokens),
			CacheReadTokens:   int64Pointer(run.CacheReadTokens),
			CacheWriteTokens:  int64Pointer(run.CacheWriteTokens),
			InputIncludesRead: true,
		},
	)
	if baseline == BaselineCompiledBelay {
		if run.TaskSuccess && run.ApplicableViolations == 0 {
			run.VerifierState = "satisfied"
		} else if run.CommandAttempts > 0 {
			run.VerifierState = "violated"
		} else {
			run.VerifierState = "unknown"
		}
	}
	if commandErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = commandErr.Error()
		}
		run.Error = detail
	}
	return run
}

type comparativeCodexObservation struct {
	Commands           []ComparativeCommand
	FinalMessage       string
	CommandAttempts    int
	FailedAttempts     int
	SuccessfulAttempts int
	CompliantAttempts  int
	InputTokens        int64
	CacheReadTokens    int64
	CacheWriteTokens   int64
	OutputTokens       int64
}

func parseComparativeCodexEvents(
	body []byte,
	cachePath string,
) (comparativeCodexObservation, error) {
	type item struct {
		Type             string `json:"type"`
		Text             string `json:"text"`
		Command          string `json:"command"`
		AggregatedOutput string `json:"aggregated_output"`
		ExitCode         *int   `json:"exit_code"`
		Status           string `json:"status"`
	}
	type usage struct {
		InputTokens           int64 `json:"input_tokens"`
		CachedInputTokens     int64 `json:"cached_input_tokens"`
		CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
		OutputTokens          int64 `json:"output_tokens"`
	}
	type envelope struct {
		Type  string `json:"type"`
		Item  *item  `json:"item"`
		Usage usage  `json:"usage"`
	}
	result := comparativeCodexObservation{
		Commands: []ComparativeCommand{},
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	for scanner.Scan() {
		var event envelope
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return result, fmt.Errorf(
				"decode comparative Codex event: %w",
				err,
			)
		}
		if event.Type == "turn.completed" {
			result.InputTokens = event.Usage.InputTokens
			result.CacheReadTokens = event.Usage.CachedInputTokens
			result.CacheWriteTokens = event.Usage.CacheWriteInputTokens
			result.OutputTokens = event.Usage.OutputTokens
		}
		if event.Type != "item.completed" || event.Item == nil {
			continue
		}
		switch event.Item.Type {
		case "agent_message":
			result.FinalMessage = event.Item.Text
		case "command_execution":
			command := ComparativeCommand{
				Command:  event.Item.Command,
				ExitCode: event.Item.ExitCode,
				Status:   event.Item.Status,
			}
			result.Commands = append(result.Commands, command)
			if !strings.Contains(event.Item.Command, "go test ./...") {
				continue
			}
			result.CommandAttempts++
			if event.Item.ExitCode != nil && *event.Item.ExitCode == 0 {
				result.SuccessfulAttempts++
				if strings.Contains(event.Item.Command, cachePath) {
					result.CompliantAttempts++
				}
			} else {
				result.FailedAttempts++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf(
			"scan comparative Codex events: %w",
			err,
		)
	}
	return result, nil
}

func summarizeComparativeRuns(
	runs []ComparativeRun,
) ComparativeSummary {
	var summary ComparativeSummary
	var totalCost float64
	costComplete := true
	retrieval := make(map[int]*ComparativeRun)
	compiled := make(map[int]*ComparativeRun)
	grouped := make(map[ComparativeBaseline][]ComparativeRun)
	for index := range runs {
		run := &runs[index]
		grouped[run.Baseline] = append(grouped[run.Baseline], *run)
		if run.Error == "" {
			summary.BaselinesCompleted++
		}
		if run.TaskSuccess {
			summary.TaskSuccesses++
		}
		summary.TotalViolations += run.ApplicableViolations
		summary.TotalFailedAttempts += run.FailedAttempts
		summary.TotalTokens += run.TotalTokens
		if run.CostUSD == nil {
			costComplete = false
		} else {
			totalCost += *run.CostUSD
		}
		switch run.Baseline {
		case BaselineRetrievalOnly:
			retrieval[run.Repetition] = run
		case BaselineCompiledBelay:
			compiled[run.Repetition] = run
		}
	}
	if costComplete {
		summary.TotalCostUSD = &totalCost
	}
	summary.ByBaseline = make(
		[]ComparativeBaselineSummary,
		0,
		len(comparativeBaselines),
	)
	for _, baseline := range comparativeBaselines {
		values := grouped[baseline]
		if len(values) == 0 {
			continue
		}
		item := ComparativeBaselineSummary{
			Baseline: baseline,
			Runs:     len(values),
		}
		var latencyTotal, tokenTotal int64
		var baselineCost float64
		baselineCostComplete := true
		for _, run := range values {
			if run.TaskSuccess {
				item.TaskSuccesses++
			}
			item.ApplicableViolations += run.ApplicableViolations
			item.FailedAttempts += run.FailedAttempts
			latencyTotal += run.LatencyMS
			tokenTotal += run.TotalTokens
			if run.CostUSD == nil {
				baselineCostComplete = false
			} else {
				baselineCost += *run.CostUSD
			}
		}
		item.MeanLatencyMS = float64(latencyTotal) / float64(len(values))
		item.MeanTokens = float64(tokenTotal) / float64(len(values))
		if baselineCostComplete {
			item.TotalCostUSD = &baselineCost
		}
		summary.ByBaseline = append(summary.ByBaseline, item)
	}
	summary.CompiledVsRetrieval = "not_comparable"
	summary.Decision = "insufficient_evidence"
	if len(retrieval) > 0 && len(retrieval) == len(compiled) {
		comparable := true
		compiledWins := 0
		retrievalWins := 0
		for repetition, retrievalRun := range retrieval {
			compiledRun, present := compiled[repetition]
			if !present ||
				retrievalRun.Error != "" ||
				compiledRun.Error != "" {
				comparable = false
				break
			}
			compiledScore := comparativeRunScore(*compiledRun)
			retrievalScore := comparativeRunScore(*retrievalRun)
			switch {
			case compiledScore > retrievalScore:
				compiledWins++
			case retrievalScore > compiledScore:
				retrievalWins++
			}
		}
		if comparable {
			switch {
			case compiledWins > 0 && retrievalWins == 0:
				summary.CompiledVsRetrieval = "compiled_better"
				summary.Decision = "expand_evaluation"
			case retrievalWins > 0 && compiledWins == 0:
				summary.CompiledVsRetrieval = "compiled_worse"
				summary.Decision = "do_not_advance_runtime_gate"
			case compiledWins == 0 && retrievalWins == 0:
				summary.CompiledVsRetrieval = "neutral"
				summary.Decision = "do_not_advance_runtime_gate"
			default:
				summary.CompiledVsRetrieval = "mixed"
				summary.Decision = "expand_evaluation"
			}
		}
	}
	return summary
}

func comparativeRunScore(run ComparativeRun) int {
	score := 0
	if run.TaskSuccess {
		score += 2
	}
	if run.VerificationCompliance {
		score++
	}
	score -= run.ApplicableViolations
	return score
}
