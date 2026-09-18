package evalrun

import (
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

func TestAssessCommandVerificationRejectsZeroExitWithoutFocusedTestExecution(
	t *testing.T,
) {
	exitCode := 0
	got := AssessCommandVerification(CommandVerificationObservation{
		Command:  "GOCACHE=/tmp/cache go test -count=1 ./cmd/belay-eval -run TestRequired",
		ExitCode: &exitCode,
		Stdout:   "ok  example/cmd/belay-eval  0.123s [no tests to run]\n",
	})
	if got.State != experience.VerifierUnknown ||
		got.Reason != "focused_go_test_not_executed" ||
		got.IntendedCheckExecuted {
		t.Fatalf("assessment = %+v", got)
	}
}

func TestAssessCommandVerificationAcceptsExecutedFocusedTest(t *testing.T) {
	exitCode := 0
	got := AssessCommandVerification(CommandVerificationObservation{
		Command:  "go test -count=1 ./cmd/belay-eval -run=TestRequired",
		ExitCode: &exitCode,
		Stdout:   "ok  example/cmd/belay-eval  0.123s\n",
	})
	if got.State != experience.VerifierSatisfied ||
		got.Reason != "command_succeeded" ||
		!got.IntendedCheckExecuted {
		t.Fatalf("assessment = %+v", got)
	}
}

func TestAssessCommandVerificationPreservesBroadAndFailedCommands(t *testing.T) {
	success := 0
	broad := AssessCommandVerification(CommandVerificationObservation{
		Command:  "go test ./...",
		ExitCode: &success,
		Stdout: "ok  example/internal/evalrun  0.123s\n" +
			"?   example/internal/empty  [no test files]\n",
	})
	if broad.State != experience.VerifierSatisfied ||
		!broad.IntendedCheckExecuted {
		t.Fatalf("broad assessment = %+v", broad)
	}

	failure := 1
	failed := AssessCommandVerification(CommandVerificationObservation{
		Command:  "go test ./cmd/belay-eval -run TestRequired",
		ExitCode: &failure,
		Stderr:   "FAIL\n",
	})
	if failed.State != experience.VerifierViolated ||
		failed.Reason != "command_failed" ||
		!failed.IntendedCheckExecuted {
		t.Fatalf("failed assessment = %+v", failed)
	}
}
