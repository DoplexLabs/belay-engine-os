package localhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestCostIssueStoreAnalyzerAndHTTPIntegration(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	projectPath := t.TempDir()
	store, err := local.OpenWithOptions(
		filepath.Join(t.TempDir(), "belay.sqlite"),
		local.OpenOptions{
			KeyProvider: &httpIntegrationKeyProvider{},
			Clock:       func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	session := transcript.Session{
		SessionKey:      "ses_cost_http_integration",
		Agent:           "codex",
		NativeSessionID: "native-cost-http-integration",
		ProjectPath:     projectPath,
		ProjectIdentity: projectPath,
		StartedAt:       now.Add(-10 * time.Minute),
		EndedAt:         now,
		Coverage:        transcript.CoverageComplete,
	}
	turns := make([]transcript.Turn, 0, 6)
	for attempt := 0; attempt < 3; attempt++ {
		callID := "call-" + string(rune('a'+attempt))
		callAt := now.Add(time.Duration(attempt*2-8) * time.Minute)
		turns = append(
			turns,
			transcript.Turn{
				TurnID:          "turn-call-" + callID,
				SourceRecordKey: "source-call-" + callID,
				SessionKey:      session.SessionKey,
				TurnIndex:       int64(attempt * 2),
				OccurredAt:      callAt,
				Role:            transcript.RoleToolCall,
				ToolName:        "exec_command",
				Payload: transcript.Payload{
					RawCommand:      "go test ./pkg/123",
					ToolCallID:      callID,
					SourceFileID:    "rollout.jsonl",
					JSONLByteOffset: int64(attempt * 200),
				},
			},
			transcript.Turn{
				TurnID:          "turn-result-" + callID,
				SourceRecordKey: "source-result-" + callID,
				SessionKey:      session.SessionKey,
				TurnIndex:       int64(attempt*2 + 1),
				OccurredAt:      callAt.Add(time.Minute),
				Role:            transcript.RoleToolResult,
				ToolName:        "exec_command",
				InputTokens:     int64Pointer(100),
				CostUSD:         float64Pointer(0.25),
				Payload: transcript.Payload{
					ToolResult:      "FAIL package/example: undefined symbol 42",
					ToolCallID:      callID,
					ToolIsError:     boolPointer(true),
					SourceFileID:    "rollout.jsonl",
					JSONLByteOffset: int64(attempt*200 + 100),
				},
			},
		)
	}
	if _, err := store.AppendTranscriptBatch(ctx, session, turns); err != nil {
		t.Fatal(err)
	}
	report, err := localapp.AnalyzeTranscriptIssuesOnce(ctx, store, 10)
	if err != nil {
		t.Fatal(err)
	}
	if report.Projects != 1 || report.Issues < 1 {
		t.Fatalf("analysis report = %+v", report)
	}

	server, err := New(
		readmodel.New(
			store,
			readmodel.WithCostIssueRepository(store),
			readmodel.WithClock(func() time.Time { return now }),
		),
		"launch-secret",
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/cost-issues?limit=5",
		nil,
	)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Authorization", "Bearer launch-secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body)
	}
	var list readmodel.CostIssueList
	if err := json.NewDecoder(response.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) < 1 ||
		list.Data[0].DetectorID != issueintel.DetectorRetryLoop ||
		len(list.Data[0].Excerpts) < 2 ||
		list.Data[0].Cost.WastedUSD == nil {
		t.Fatalf("integrated cost issue list = %+v", list)
	}
}

func int64Pointer(value int64) *int64 {
	return &value
}

func float64Pointer(value float64) *float64 {
	return &value
}

func boolPointer(value bool) *bool {
	return &value
}
