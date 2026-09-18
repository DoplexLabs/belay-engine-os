package numbatmap

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
)

func TestSafeCommandSummaryAllowlist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		command     string
		wantName    string
		wantSummary string
	}{
		{
			name:        "safe executable and option names",
			command:     "go test ./... --count=1 -race --token=secret",
			wantName:    "go",
			wantSummary: "go --count -race",
		},
		{
			name:        "separated option value omitted",
			command:     "rg --glob *.go needle",
			wantName:    "",
			wantSummary: "",
		},
		{
			name:    "environment prefix",
			command: "TOKEN=secret go test ./...",
		},
		{
			name:    "shell wrapper",
			command: "bash -c go test ./...",
		},
		{
			name:    "absolute executable",
			command: "/usr/bin/go test ./...",
		},
		{
			name:    "unknown executable",
			command: "private-tool --help",
		},
		{
			name:    "pipe",
			command: "go test ./... | cat",
		},
		{
			name:    "redirect",
			command: "go test ./... > result.txt",
		},
		{
			name:    "substitution",
			command: "go test $(cat target)",
		},
		{
			name:    "control character",
			command: "go test\n./...",
		},
		{
			name:        "unsafe option families omitted",
			command:     "go test -Dsecret --password=secret --help",
			wantName:    "go",
			wantSummary: "go --help",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := commandName(test.command); got != test.wantName {
				t.Fatalf("commandName(%q) = %q, want %q", test.command, got, test.wantName)
			}
			if got := commandSummary(test.command); got != test.wantSummary {
				t.Fatalf(
					"commandSummary(%q) = %q, want %q",
					test.command,
					got,
					test.wantSummary,
				)
			}
		})
	}
}

func TestToolErrorOutcomeMappingIsExplicitAndFailClosed(t *testing.T) {
	t.Parallel()

	success := 0
	failure := 1
	tests := []struct {
		name      string
		eventType string
		tags      []string
		exitCode  *int
		want      string
	}{
		{
			name:      "closed tool error tag",
			eventType: "tool.result",
			tags:      []string{"tool_error"},
			want:      "failed",
		},
		{
			name:      "explicit failure agrees",
			eventType: "tool.result",
			tags:      []string{"tool_error"},
			exitCode:  &failure,
			want:      "failed",
		},
		{
			name:      "explicit success contradicts tag",
			eventType: "tool.result",
			tags:      []string{"tool_error"},
			exitCode:  &success,
			want:      "unknown",
		},
		{
			name:      "nearby tag is not accepted",
			eventType: "tool.result",
			tags:      []string{"tool_error_detail"},
			want:      "unknown",
		},
		{
			name:      "tag is ignored on non-result event",
			eventType: "tool.call",
			tags:      []string{"tool_error"},
			want:      "unknown",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			record := numbat.EventRecord{
				EventType: test.eventType,
				Tags:      test.tags,
				ExitCode:  test.exitCode,
			}
			if got := outcome(record); got != test.want {
				t.Fatalf("outcome() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestToolErrorMappingDoesNotRetainRawContent(t *testing.T) {
	t.Parallel()

	const canary = "PRIVATE_TOOL_ERROR_BODY_CANARY"
	event, err := Event(numbat.EventRecord{
		RunID:       "run-1",
		EventID:     "event-1",
		SessionID:   "session-1",
		SourceAgent: "codex",
		SourceType:  "artifact",
		Timestamp:   "2026-09-09T12:00:00Z",
		Actor:       "tool",
		EventType:   "tool.result",
		ToolName:    "read_file",
		Tags:        []string{"tool_error"},
		Content:     canary,
	}, Options{
		InstallationID: "installation-1",
		EngineVersion:  "test",
		ObservedAt:     time.Date(2026, 9, 9, 12, 0, 1, 0, time.UTC),
		Sequence:       1,
		Random:         bytes.NewReader(make([]byte, 32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.Observation.Outcome != "failed" {
		t.Fatalf("outcome = %q, want failed", event.Observation.Outcome)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), canary) {
		t.Fatalf("canonical event retained raw content: %s", encoded)
	}
}
