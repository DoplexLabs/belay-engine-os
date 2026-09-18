package transcriptissues

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

type commandFixture struct {
	Cases []struct {
		Name     string `json:"name"`
		Want     string `json:"want"`
		NotWant  string `json:"not_want"`
		Sessions []struct {
			Key   string `json:"key"`
			Agent string `json:"agent"`
			Turns []struct {
				Role         transcript.Role `json:"role"`
				Tool         string          `json:"tool"`
				Text         string          `json:"text"`
				Command      string          `json:"command"`
				Result       string          `json:"result"`
				CallID       string          `json:"call_id"`
				ExitCode     *int            `json:"exit_code"`
				File         string          `json:"file"`
				Content      string          `json:"content"`
				InputTokens  *int64          `json:"input_tokens"`
				OutputTokens *int64          `json:"output_tokens"`
				CostUSD      *float64        `json:"cost_usd"`
			} `json:"turns"`
		} `json:"sessions"`
	} `json:"cases"`
}

func TestCommandDetectorFixtures(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(
		"..",
		"..",
		"..",
		"testdata",
		"transcriptissues",
		"command",
		"cases.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var fixture commandFixture
	if json.Unmarshal(body, &fixture) != nil {
		t.Fatal("decode command detector fixtures")
	}
	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			input := commandFixtureInput(t, test.Sessions)
			analysis, err := AnalyzeProject(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			detectors := make(map[string]bool)
			for _, issue := range analysis.Issues {
				detectors[issue.DetectorID] = true
				if len(issue.Excerpts) < 2 ||
					len(issue.Excerpts) > maxExcerpts ||
					issue.SessionCount == 0 ||
					len(issue.Trend) != 8 {
					t.Fatalf("incomplete issue = %+v", issue)
				}
			}
			if test.Want != "" && !detectors[test.Want] {
				t.Fatalf("missing %q in %+v", test.Want, analysis.Issues)
			}
			if test.NotWant != "" && detectors[test.NotWant] {
				t.Fatalf("unexpected %q in %+v", test.NotWant, analysis.Issues)
			}
		})
	}
}

func TestMatchingToolResultRejectsNearbyDifferentTool(t *testing.T) {
	turns := []transcript.Turn{
		{
			TurnID:   "call",
			Role:     transcript.RoleToolCall,
			ToolName: "exec_command",
			Payload: transcript.Payload{
				ToolCallID: "call-1",
			},
		},
		{
			TurnID:   "wrong",
			Role:     transcript.RoleToolResult,
			ToolName: "apply_patch",
		},
		{
			TurnID:   "right",
			Role:     transcript.RoleToolResult,
			ToolName: "exec_command",
		},
	}
	index, result, ok := matchingToolResult(turns, 0, turns[0])
	if !ok || index != 2 || result.TurnID != "right" {
		t.Fatalf("matched result = %d/%+v/%t", index, result, ok)
	}
}

func commandFixtureInput(
	t *testing.T,
	sessions []struct {
		Key   string `json:"key"`
		Agent string `json:"agent"`
		Turns []struct {
			Role         transcript.Role `json:"role"`
			Tool         string          `json:"tool"`
			Text         string          `json:"text"`
			Command      string          `json:"command"`
			Result       string          `json:"result"`
			CallID       string          `json:"call_id"`
			ExitCode     *int            `json:"exit_code"`
			File         string          `json:"file"`
			Content      string          `json:"content"`
			InputTokens  *int64          `json:"input_tokens"`
			OutputTokens *int64          `json:"output_tokens"`
			CostUSD      *float64        `json:"cost_usd"`
		} `json:"turns"`
	},
) issueintel.ProjectInput {
	t.Helper()
	base := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	input := issueintel.ProjectInput{
		Project: issueintel.Project{
			Identity: "https://example.invalid/project.git",
			Path:     "/synthetic/project",
		},
		Now: time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC),
	}
	sequence := 0
	for _, source := range sessions {
		metadata := transcript.Session{
			SessionKey:      source.Key,
			Agent:           source.Agent,
			NativeSessionID: source.Key,
			ProjectPath:     input.Project.Path,
			ProjectIdentity: input.Project.Identity,
			StartedAt:       base.Add(time.Duration(sequence) * time.Second),
			Coverage:        transcript.CoverageComplete,
		}
		var turns []transcript.Turn
		for index, value := range source.Turns {
			occurredAt := base.Add(time.Duration(sequence) * time.Second)
			sequence++
			payload := transcript.Payload{
				Text:              value.Text,
				RawCommand:        value.Command,
				ToolResult:        value.Result,
				ToolCallID:        value.CallID,
				ExitCode:          value.ExitCode,
				SourceFileID:      "src_fixture",
				ParserVersion:     "fixture",
				PriceTableVersion: "fixture",
				JSONLByteOffset:   int64(index),
			}
			if value.File != "" {
				encoded, err := json.Marshal(map[string]string{
					"file_path": value.File,
					"content":   value.Content,
				})
				if err != nil {
					t.Fatal(err)
				}
				payload.ToolInput = encoded
			}
			turns = append(turns, transcript.Turn{
				TurnID:       source.Key + "_" + time.Duration(index).String(),
				SessionKey:   source.Key,
				TurnIndex:    int64(index),
				OccurredAt:   occurredAt,
				Role:         value.Role,
				ToolName:     value.Tool,
				InputTokens:  value.InputTokens,
				OutputTokens: value.OutputTokens,
				CostUSD:      value.CostUSD,
				Payload:      payload,
			})
			metadata.EndedAt = occurredAt
		}
		input.Sessions = append(input.Sessions, issueintel.Session{
			Metadata: metadata,
			Turns:    turns,
		})
	}
	return input
}
