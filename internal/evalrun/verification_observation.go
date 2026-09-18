package evalrun

import (
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

const CommandVerificationAssessmentSchemaVersion = "belay.command-verification-assessment.v1"

type CommandVerificationObservation struct {
	Command      string `json:"command"`
	CommandClass string `json:"command_class,omitempty"`
	ExitCode     *int   `json:"exit_code,omitempty"`
	Stdout       string `json:"stdout,omitempty"`
	Stderr       string `json:"stderr,omitempty"`
}

type CommandVerificationAssessment struct {
	SchemaVersion         string                   `json:"schema_version"`
	State                 experience.VerifierState `json:"state"`
	Reason                string                   `json:"reason"`
	IntendedCheckExecuted bool                     `json:"intended_check_executed"`
}

// AssessCommandVerification distinguishes a successful process from proof that
// its intended verification actually ran. Go exits zero when a focused
// `go test -run` pattern matches no tests, so that observation is unknown
// rather than satisfied.
func AssessCommandVerification(
	observation CommandVerificationObservation,
) CommandVerificationAssessment {
	result := CommandVerificationAssessment{
		SchemaVersion: CommandVerificationAssessmentSchemaVersion,
		State:         experience.VerifierUnknown,
	}
	if strings.TrimSpace(observation.Command) == "" {
		result.Reason = "missing_command"
		return result
	}
	if observation.ExitCode == nil {
		result.Reason = "missing_exit_code"
		return result
	}
	if *observation.ExitCode != 0 {
		result.State = experience.VerifierViolated
		result.Reason = "command_failed"
		result.IntendedCheckExecuted = true
		return result
	}
	if focusedSinglePackageGoTest(observation.Command) &&
		goTestReportedNoExecution(
			observation.Stdout+"\n"+observation.Stderr,
		) {
		result.Reason = "focused_go_test_not_executed"
		return result
	}
	result.State = experience.VerifierSatisfied
	result.Reason = "command_succeeded"
	result.IntendedCheckExecuted = true
	return result
}

func focusedSinglePackageGoTest(command string) bool {
	fields := strings.Fields(command)
	goIndex := -1
	for index, field := range fields {
		if field == "go" {
			goIndex = index
			break
		}
		if field == "env" || strings.Contains(field, "=") {
			continue
		}
		return false
	}
	if goIndex < 0 || goIndex+1 >= len(fields) ||
		fields[goIndex+1] != "test" {
		return false
	}
	hasRun := false
	packages := 0
	for index := goIndex + 2; index < len(fields); index++ {
		field := fields[index]
		switch {
		case field == "-run" || field == "--run":
			hasRun = true
			index++
		case strings.HasPrefix(field, "-run=") ||
			strings.HasPrefix(field, "--run="):
			hasRun = true
		case strings.HasPrefix(field, "-"):
			if goTestFlagConsumesValue(field) {
				index++
			}
		default:
			packages++
		}
	}
	return hasRun && packages == 1
}

func goTestFlagConsumesValue(value string) bool {
	if strings.Contains(value, "=") {
		return false
	}
	switch value {
	case "-count", "-timeout", "-tags", "-coverprofile", "-cpu",
		"-parallel", "-vet", "-exec":
		return true
	default:
		return false
	}
}

func goTestReportedNoExecution(output string) bool {
	const maxInspectionBytes = 64 << 10
	if len(output) > maxInspectionBytes {
		const half = maxInspectionBytes / 2
		output = output[:half] + output[len(output)-half:]
	}
	output = strings.ToLower(output)
	return strings.Contains(output, "[no tests to run]") ||
		strings.Contains(output, "[no test files]")
}
