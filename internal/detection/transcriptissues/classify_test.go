package transcriptissues

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestVerificationClassifierUnderstandsPackageScriptsAndProjectTargets(
	t *testing.T,
) {
	tests := []struct {
		command string
		config  issueintel.ProjectConfig
		want    string
	}{
		{command: "pnpm run check", want: commandClassBuild},
		{command: "npm test", want: commandClassTest},
		{command: "python -m pytest", want: commandClassTest},
		{command: "make verify", config: issueintel.ProjectConfig{
			VerificationCommands: []string{"make verify"},
		}, want: commandClassBuild},
		{command: "pnpm run lint", want: commandClassLint},
		{command: "tsc --noEmit", want: commandClassTypecheck},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			if got := classifyCommand(test.command, test.config); got != test.want {
				t.Fatalf("class = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClassifyVerificationCommandRejectsOrdinaryCommands(t *testing.T) {
	if class, ok := ClassifyVerificationCommand(
		"pnpm run check",
		issueintel.ProjectConfig{},
	); !ok || class != commandClassBuild {
		t.Fatalf("verification classification = %q/%t", class, ok)
	}
	if class, ok := ClassifyVerificationCommand(
		"git status --short",
		issueintel.ProjectConfig{},
	); ok || class != "" {
		t.Fatalf("ordinary command classification = %q/%t", class, ok)
	}
}

func TestVerificationClassifierDoesNotScanArgumentsOrPaths(t *testing.T) {
	commands := []string{
		"grep TODO . | grep -v _test.go",
		"grep lint internal/detection/transcriptissues",
		"grep build docs/build-notes.md",
		"rg check internal/checks",
		"cat docs/format/checklist.md",
		"sed -n 1,80p internal/build/check.go",
		"bash ./scripts/run-tests.sh",
		"sh ./scripts/lint.sh",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			if got := classifyCommand(
				command,
				issueintel.ProjectConfig{},
			); got != commandClassOther {
				t.Fatalf("class = %q, want %q", got, commandClassOther)
			}
		})
	}
}

func TestVerificationClassifierRecognizesBoundedExecutableBasenames(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{command: "./scripts/run-tests.sh", want: commandClassTest},
		{command: "./scripts/lint.sh", want: commandClassLint},
		{command: "./scripts/project-build", want: commandClassBuild},
		{command: "./scripts/check", want: commandClassBuild},
		{command: "./scripts/format-check.sh", want: commandClassFormat},
		{command: "./scripts/type-check.sh", want: commandClassTypecheck},
		{command: "./scripts/contest.sh", want: commandClassOther},
		{command: "./scripts/checklist.sh", want: commandClassOther},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			if got := classifyCommand(
				test.command,
				issueintel.ProjectConfig{},
			); got != test.want {
				t.Fatalf("class = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizeCommandStripsVolatileArguments(t *testing.T) {
	first := normalizeCommand(
		"go test ./pkg/123 --port 4312 /tmp/run-a deadbeef123",
	)
	second := normalizeCommand(
		"go test ./pkg/456 --port 9921 /private/tmp/run-b cafebabe999",
	)
	if first != second ||
		!strings.Contains(first, "<n>") ||
		!strings.Contains(first, "<port>") ||
		!strings.Contains(first, "<tmp>") ||
		!strings.Contains(first, "<hash>") {
		t.Fatalf("normalized commands = %q / %q", first, second)
	}
}

func TestRetainedCommandAndErrorHelpersShareDetectorSemantics(t *testing.T) {
	exitCode := 1
	call := transcript.Turn{
		Role: transcript.RoleToolCall,
		Payload: transcript.Payload{
			RawCommand: "go test ./internal/service -count=17",
		},
	}
	class, signature, raw, ok := RetainedCommandInfo(
		call,
		issueintel.ProjectConfig{},
	)
	if !ok || class != commandClassTest ||
		signature != "go test ./internal/service -count=<n>" ||
		raw != call.Payload.RawCommand {
		t.Fatalf(
			"retained command = %q/%q/%q/%t",
			class,
			signature,
			raw,
			ok,
		)
	}
	result := transcript.Turn{
		Role: transcript.RoleToolResult,
		Payload: transcript.Payload{
			ToolResult: "failed: /tmp/run-17/service_test.go:42",
			ExitCode:   &exitCode,
		},
	}
	if !ToolResultFailed(result) {
		t.Fatal("explicit non-zero result was not classified as failed")
	}
	if got := NormalizedErrorSignature(result); got == "" {
		t.Fatal("normalized error signature was not extracted")
	}
}

func TestExportedTrajectoryClassifiersReuseDetectorSemantics(t *testing.T) {
	first, ok := NormalizedCommandSignature(transcript.Turn{
		Role: transcript.RoleToolCall,
		Payload: transcript.Payload{
			RawCommand: "run-task 123 --port 4312 /tmp/run-a",
		},
	})
	if !ok {
		t.Fatal("normalized command signature was not extracted")
	}
	second, ok := NormalizedCommandSignature(transcript.Turn{
		Role: transcript.RoleToolCall,
		Payload: transcript.Payload{
			RawCommand: "run-task 456 --port 9921 /private/tmp/run-b",
		},
	})
	if !ok || first != second {
		t.Fatalf("normalized signatures = %q / %q", first, second)
	}
	if !IsCompletionClaim(transcript.Turn{
		Role:    transcript.RoleAssistant,
		Payload: transcript.Payload{Text: "Implementation is complete."},
	}) {
		t.Fatal("completion claim was not recognized")
	}
	if IsCompletionClaim(transcript.Turn{
		Role:    transcript.RoleAssistant,
		Payload: transcript.Payload{Text: "I am still investigating."},
	}) {
		t.Fatal("ordinary assistant text was treated as completion")
	}
}

func TestStructuredToolResultsRequireExplicitFailureFields(t *testing.T) {
	patchResult := transcript.Turn{
		Role: transcript.RoleToolResult,
		Payload: transcript.Payload{
			ToolResult: `{"file":{"patch":"return errors.New(\"not failed\")"}}`,
		},
	}
	if turnFailed(patchResult) {
		t.Fatal("structured patch output was misclassified as a failure")
	}
	errorResult := transcript.Turn{
		Role: transcript.RoleToolResult,
		Payload: transcript.Payload{
			ToolResult: `{"error":"permission denied for /tmp/run-123"}`,
		},
	}
	signature := normalizedErrorSignature(errorResult)
	if !turnFailed(errorResult) ||
		signature != "permission denied for <tmp>" {
		t.Fatalf("structured failure/signature = %t/%q", turnFailed(errorResult), signature)
	}
}

func TestCommandRepairFamilyIsConservativeAndUnwrapsEnvironment(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{command: "go test -v ./x", want: "go test"},
		{
			command: "env GOCACHE=/tmp/x go test -v ./x",
			want:    "go test",
		},
		{command: "rg -n TODO internal", want: "rg"},
		{
			command: "wc -l report.txt && awk '{print $1}' report.txt && rg TODO .",
			want:    "wc",
		},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			got, ok := CommandRepairFamily(transcript.Turn{
				Role: transcript.RoleToolCall,
				Payload: transcript.Payload{
					RawCommand: test.command,
				},
			})
			if !ok || got != test.want {
				t.Fatalf("repair family = %q/%t, want %q/true", got, ok, test.want)
			}
		})
	}
}

func TestNormalizedFirstFailureLineSkipsGenericExitWrapper(t *testing.T) {
	turn := transcript.Turn{
		Role: transcript.RoleToolResult,
		Payload: transcript.Payload{
			ToolResult: "Exit code 1\nSyntaxError: unexpected identifier 'translate'",
		},
	}
	if got := NormalizedFirstFailureLine(turn); got !=
		"syntaxerror: unexpected identifier 'translate'" {
		t.Fatalf("normalized failure line = %q", got)
	}
}

func TestRetainedVerificationCommandRecognizesUnittestFromToolInput(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{
			command: "python3 -m unittest -v 2>&1 | tail -20",
			want:    "python3 -m unittest -v 2>&1 | tail -20",
		},
		{
			command: `cd "$(pwd)" && python3 -m unittest -v`,
			want:    "python3 -m unittest -v",
		},
	}
	for _, test := range tests {
		input, err := json.Marshal(map[string]string{
			"command": test.command,
		})
		if err != nil {
			t.Fatal(err)
		}
		class, raw, ok := RetainedVerificationCommand(
			transcript.Turn{
				Role:     transcript.RoleToolCall,
				ToolName: "Bash",
				Payload:  transcript.Payload{ToolInput: input},
			},
			issueintel.ProjectConfig{},
		)
		if !ok || class != commandClassTest || raw != test.want {
			t.Fatalf(
				"verification %q = %q/%q/%t, want test/%q/true",
				test.command,
				class,
				raw,
				ok,
				test.want,
			)
		}
	}
}

func TestMachineGeneratedEnvelopeBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{
			name:  "mode raw query envelope",
			value: "MODE: planning\nRAW QUERY:\nNo, inspect the failure.",
			want:  true,
		},
		{
			name:  "compact task json",
			value: `{"description":"Investigate failure","prompt":"No, inspect the logs."}`,
			want:  true,
		},
		{
			name:  "ordinary mode prose",
			value: "No, keep the words MODE: planning and RAW QUERY: in the documentation.",
		},
		{
			name:  "ordinary description prompt prose",
			value: "No, the description and prompt are separate concepts here.",
		},
		{
			name:  "json words only",
			value: `{"message":"The description and prompt are documented."}`,
		},
		{
			name:  "nested task-shaped json",
			value: `{"task":{"description":"Investigate","prompt":"Inspect logs"}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsMachineGeneratedEnvelope(test.value); got != test.want {
				t.Fatalf(
					"IsMachineGeneratedEnvelope(%q) = %t, want %t",
					test.value,
					got,
					test.want,
				)
			}
		})
	}
}

func TestHighConfidenceCorrectionMarkerRequiresEarlyCue(t *testing.T) {
	for _, value := range []string{
		"why is the build still failing; do not delegate...",
		"wait, I think you're wrong about the failing test",
		"Again, edit the schema source instead.",
		"No, inspect the implementation before changing it.",
	} {
		if marker := HighConfidenceCorrectionMarker(value); marker == "" {
			t.Errorf("early correction cue was rejected: %q", value)
		}
	}
	for _, value := range []string{
		"TASK: Add speaker-notes scripts to an existing PowerPoint deck. Do NOT...",
		strings.Repeat("delegated task context ", 5) +
			"review every package and do not skip the final verification",
		strings.Repeat("skill body guidance ", 5) +
			"the example says the previous approach was wrong",
	} {
		if marker := HighConfidenceCorrectionMarker(value); marker != "" {
			t.Errorf(
				"late correction cue %q was accepted in %q",
				marker,
				value,
			)
		}
	}
}

func TestEditedFilesExtractsStructuredAndPatchPaths(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"patch":     "*** Update File: internal/a.go\n@@\n",
		"file_path": "internal/b.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	files := ExtractEditedFiles(transcript.Turn{
		Role:     transcript.RoleToolCall,
		ToolName: "apply_patch",
		Payload:  transcript.Payload{ToolInput: input},
	})
	if len(files) != 2 ||
		files[0] != "internal/a.go" ||
		files[1] != "internal/b.go" {
		t.Fatalf("files = %v", files)
	}
}
