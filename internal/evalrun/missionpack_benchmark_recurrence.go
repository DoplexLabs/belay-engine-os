package evalrun

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	acquisitiontranscript "github.com/DoplexLabs/belay-engine/internal/acquisition/transcript"
	"github.com/DoplexLabs/belay-engine/internal/detection/transcriptissues"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	MissionPackBenchmarkRecurrenceInputSchemaVersion = "belay.missionpack-benchmark-recurrence-input.v1"
	MissionPackBenchmarkRecurrenceSchemaVersion      = "belay.missionpack-benchmark-recurrence.v1"
	MissionPackBenchmarkFailureFingerprintVersion    = "belay.benchmark-failure-fingerprint.v1"
	MissionPackBenchmarkCaptureParserVersion         = "belay.benchmark-capture-parser.v1"
)

type MissionPackBenchmarkRecurrenceInput struct {
	SchemaVersion string                                   `json:"schema_version"`
	Runs          []MissionPackBenchmarkRecurrenceRunInput `json:"runs"`
}

type MissionPackBenchmarkRecurrenceRunInput struct {
	RunID         string                      `json:"run_id"`
	Harness       MissionPackBenchmarkHarness `json:"harness"`
	RawEventsPath string                      `json:"raw_events_path"`
}

type MissionPackBenchmarkRecurrenceReport struct {
	SchemaVersion       string                                   `json:"schema_version"`
	ParserVersion       string                                   `json:"parser_version"`
	NativeParserVersion string                                   `json:"native_parser_version"`
	FingerprintVersion  string                                   `json:"fingerprint_version"`
	RunCount            int                                      `json:"run_count"`
	FailedAttempts      []MissionPackBenchmarkFailedAttempt      `json:"failed_attempts"`
	Fingerprints        []MissionPackBenchmarkFailureFingerprint `json:"fingerprints"`
}

type MissionPackBenchmarkFailedAttempt struct {
	RunID                 string `json:"run_id"`
	Harness               string `json:"harness"`
	TurnIndex             int64  `json:"turn_index"`
	JSONLByteOffset       int64  `json:"jsonl_byte_offset"`
	CommandClass          string `json:"command_class"`
	NormalizedCommand     string `json:"normalized_command"`
	NormalizedFailureLine string `json:"normalized_failure_line"`
	FingerprintID         string `json:"fingerprint_id"`
}

type MissionPackBenchmarkFailureFingerprint struct {
	FingerprintID         string   `json:"fingerprint_id"`
	CommandClass          string   `json:"command_class"`
	NormalizedCommand     string   `json:"normalized_command"`
	NormalizedFailureLine string   `json:"normalized_failure_line"`
	AttemptCount          int      `json:"attempt_count"`
	SessionCount          int      `json:"session_count"`
	Sessions              []string `json:"sessions"`
	Recurrent             bool     `json:"recurrent"`
}

func AnalyzeMissionPackBenchmarkRecurrence(
	ctx context.Context,
	input MissionPackBenchmarkRecurrenceInput,
) (MissionPackBenchmarkRecurrenceReport, error) {
	if input.SchemaVersion != MissionPackBenchmarkRecurrenceInputSchemaVersion {
		return MissionPackBenchmarkRecurrenceReport{}, errors.New(
			"unsupported mission-pack benchmark recurrence input schema",
		)
	}
	if len(input.Runs) == 0 {
		return MissionPackBenchmarkRecurrenceReport{}, errors.New(
			"mission-pack benchmark recurrence input is empty",
		)
	}
	seenRuns := make(map[string]bool, len(input.Runs))
	var attempts []MissionPackBenchmarkFailedAttempt
	for _, run := range input.Runs {
		run.RunID = strings.TrimSpace(run.RunID)
		run.RawEventsPath = strings.TrimSpace(run.RawEventsPath)
		if run.RunID == "" || run.RawEventsPath == "" || seenRuns[run.RunID] {
			return MissionPackBenchmarkRecurrenceReport{}, errors.New(
				"mission-pack benchmark recurrence run is invalid",
			)
		}
		seenRuns[run.RunID] = true
		value, err := analyzeMissionPackBenchmarkRun(ctx, run)
		if err != nil {
			return MissionPackBenchmarkRecurrenceReport{}, fmt.Errorf(
				"analyze recurrence run %s: %w",
				run.RunID,
				err,
			)
		}
		attempts = append(attempts, value...)
	}
	sort.Slice(attempts, func(left, right int) bool {
		if attempts[left].RunID != attempts[right].RunID {
			return attempts[left].RunID < attempts[right].RunID
		}
		return attempts[left].TurnIndex < attempts[right].TurnIndex
	})

	type aggregate struct {
		class    string
		command  string
		failure  string
		attempts int
		sessions map[string]bool
	}
	byFingerprint := make(map[string]*aggregate)
	for _, attempt := range attempts {
		value := byFingerprint[attempt.FingerprintID]
		if value == nil {
			value = &aggregate{
				class:    attempt.CommandClass,
				command:  attempt.NormalizedCommand,
				failure:  attempt.NormalizedFailureLine,
				sessions: make(map[string]bool),
			}
			byFingerprint[attempt.FingerprintID] = value
		}
		value.attempts++
		value.sessions[attempt.RunID] = true
	}
	fingerprints := make(
		[]MissionPackBenchmarkFailureFingerprint,
		0,
		len(byFingerprint),
	)
	for fingerprintID, value := range byFingerprint {
		sessions := make([]string, 0, len(value.sessions))
		for session := range value.sessions {
			sessions = append(sessions, session)
		}
		sort.Strings(sessions)
		fingerprints = append(
			fingerprints,
			MissionPackBenchmarkFailureFingerprint{
				FingerprintID:         fingerprintID,
				CommandClass:          value.class,
				NormalizedCommand:     value.command,
				NormalizedFailureLine: value.failure,
				AttemptCount:          value.attempts,
				SessionCount:          len(sessions),
				Sessions:              sessions,
				Recurrent:             len(sessions) >= 2,
			},
		)
	}
	sort.Slice(fingerprints, func(left, right int) bool {
		if fingerprints[left].SessionCount != fingerprints[right].SessionCount {
			return fingerprints[left].SessionCount >
				fingerprints[right].SessionCount
		}
		if fingerprints[left].AttemptCount != fingerprints[right].AttemptCount {
			return fingerprints[left].AttemptCount >
				fingerprints[right].AttemptCount
		}
		return fingerprints[left].FingerprintID <
			fingerprints[right].FingerprintID
	})
	return MissionPackBenchmarkRecurrenceReport{
		SchemaVersion:       MissionPackBenchmarkRecurrenceSchemaVersion,
		ParserVersion:       MissionPackBenchmarkCaptureParserVersion,
		NativeParserVersion: acquisitiontranscript.ParserVersion,
		FingerprintVersion:  MissionPackBenchmarkFailureFingerprintVersion,
		RunCount:            len(input.Runs),
		FailedAttempts:      attempts,
		Fingerprints:        fingerprints,
	}, nil
}

func analyzeMissionPackBenchmarkRun(
	ctx context.Context,
	run MissionPackBenchmarkRecurrenceRunInput,
) ([]MissionPackBenchmarkFailedAttempt, error) {
	var turns []transcript.Turn
	switch run.Harness {
	case MissionPackHarnessClaude:
		value, err := parseBenchmarkClaudeTurns(ctx, run.RawEventsPath)
		if err != nil {
			return nil, err
		}
		turns = value
	case MissionPackHarnessCodex:
		value, err := parseBenchmarkCodexExecTurns(ctx, run.RawEventsPath)
		if err != nil {
			return nil, err
		}
		turns = value
	default:
		return nil, errors.New("unsupported benchmark recurrence harness")
	}
	var attempts []MissionPackBenchmarkFailedAttempt
	for callIndex, call := range turns {
		class, normalized, _, ok := transcriptissues.RetainedCommandInfo(
			call,
			issueintel.ProjectConfig{},
		)
		if !ok || call.Role != transcript.RoleToolCall {
			continue
		}
		result, ok := benchmarkMatchingToolResult(
			turns,
			callIndex,
			call,
		)
		if !ok || !transcriptissues.ToolResultFailed(result) {
			continue
		}
		failure := transcriptissues.NormalizedErrorSignature(result)
		if failure == "" {
			failure = transcriptissues.NormalizedFirstFailureLine(result)
		}
		if failure == "" {
			continue
		}
		sum := sha256.Sum256(
			[]byte(class + "\x00" + normalized + "\x00" + failure),
		)
		attempts = append(attempts, MissionPackBenchmarkFailedAttempt{
			RunID:                 run.RunID,
			Harness:               string(run.Harness),
			TurnIndex:             call.TurnIndex,
			JSONLByteOffset:       call.Payload.JSONLByteOffset,
			CommandClass:          class,
			NormalizedCommand:     normalized,
			NormalizedFailureLine: failure,
			FingerprintID:         "mbf_" + hex.EncodeToString(sum[:16]),
		})
	}
	return attempts, nil
}

func parseBenchmarkClaudeTurns(
	ctx context.Context,
	path string,
) ([]transcript.Turn, error) {
	nativeSessionID, err := benchmarkNativeSessionID(path)
	if err != nil {
		return nil, err
	}
	body, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	parsed, err := acquisitiontranscript.Parse(
		ctx,
		acquisitiontranscript.Source{
			Agent:           acquisitiontranscript.AgentClaude,
			Path:            path,
			NativeSessionID: nativeSessionID,
			Primary:         true,
		},
		body,
		0,
		acquisitiontranscript.ParseOptions{
			FinalizePendingUsage: true,
			Boundary: acquisitiontranscript.Boundary{
				SessionKey: func(agent, nativeSessionID string) string {
					sum := sha256.Sum256(
						[]byte(agent + "\x00" + nativeSessionID),
					)
					return "ses_" + hex.EncodeToString(sum[:16])
				},
				ScrubSecrets: func(value string) (string, int) {
					return value, 0
				},
			},
		},
	)
	if err != nil {
		return nil, err
	}
	return parsed.Turns, nil
}

func parseBenchmarkCodexExecTurns(
	ctx context.Context,
	path string,
) ([]transcript.Turn, error) {
	body, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	type commandItem struct {
		ID               string `json:"id"`
		Type             string `json:"type"`
		Command          string `json:"command"`
		AggregatedOutput string `json:"aggregated_output"`
		ExitCode         *int   `json:"exit_code"`
	}
	type event struct {
		Type string      `json:"type"`
		Item commandItem `json:"item"`
	}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	var (
		offset int64
		index  int64
		turns  []transcript.Turn
	)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := append([]byte(nil), scanner.Bytes()...)
		lineOffset := offset
		offset += int64(len(line)) + 1
		var record event
		if json.Unmarshal(line, &record) != nil ||
			record.Type != "item.completed" ||
			record.Item.Type != "command_execution" ||
			strings.TrimSpace(record.Item.Command) == "" {
			continue
		}
		callID := strings.TrimSpace(record.Item.ID)
		input, _ := json.Marshal(map[string]string{
			"command": record.Item.Command,
		})
		turns = append(turns, transcript.Turn{
			TurnIndex: index,
			Role:      transcript.RoleToolCall,
			ToolName:  "Bash",
			Payload: transcript.Payload{
				ToolInput:       input,
				RawCommand:      record.Item.Command,
				ToolCallID:      callID,
				JSONLByteOffset: lineOffset,
			},
		})
		index++
		isError := record.Item.ExitCode != nil && *record.Item.ExitCode != 0
		turns = append(turns, transcript.Turn{
			TurnIndex: index,
			Role:      transcript.RoleToolResult,
			ToolName:  "Bash",
			Payload: transcript.Payload{
				ToolResult:      record.Item.AggregatedOutput,
				ToolCallID:      callID,
				ToolIsError:     &isError,
				ExitCode:        record.Item.ExitCode,
				JSONLByteOffset: lineOffset,
			},
		})
		index++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return turns, nil
}

func benchmarkMatchingToolResult(
	turns []transcript.Turn,
	callIndex int,
	call transcript.Turn,
) (transcript.Turn, bool) {
	callID := strings.TrimSpace(call.Payload.ToolCallID)
	for index := callIndex + 1; index < len(turns) && index-callIndex <= 8; index++ {
		value := turns[index]
		if value.Role != transcript.RoleToolResult {
			continue
		}
		resultID := strings.TrimSpace(value.Payload.ToolCallID)
		if callID != "" && resultID != "" {
			if callID == resultID {
				return value, true
			}
			continue
		}
		if strings.TrimSpace(call.ToolName) == "" ||
			strings.TrimSpace(value.ToolName) == "" ||
			strings.EqualFold(call.ToolName, value.ToolName) {
			return value, true
		}
	}
	return transcript.Turn{}, false
}

func benchmarkNativeSessionID(path string) (string, error) {
	body, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer body.Close()
	scanner := bufio.NewScanner(body)
	buffer := make([]byte, 64<<10)
	scanner.Buffer(buffer, 4<<20)
	for scanner.Scan() {
		var record map[string]json.RawMessage
		if json.Unmarshal(scanner.Bytes(), &record) != nil {
			continue
		}
		for _, key := range []string{"session_id", "thread_id"} {
			var value string
			if json.Unmarshal(record[key], &value) == nil &&
				strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value), nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errors.New("native session ID is missing")
}
