package pipeline_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/pipeline"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

func TestLiveNumbatSanitizedConformance(t *testing.T) {
	testCases := []struct {
		name              string
		fixture           string
		runID             string
		sourceAgent       string
		events            int
		indicators        int
		syntheticCanaries []string
		provenance        liveFixtureProvenance
	}{
		{
			name:        "codex",
			fixture:     "codex-sanitized.ndjson",
			runID:       "synthetic-live-codex-run",
			sourceAgent: "codex",
			events:      8,
			indicators:  1,
			syntheticCanaries: []string{
				"SYNTHETIC_CODEX_ENDPOINT_HOST_CANARY_91d3",
				"SYNTHETIC_CODEX_ENDPOINT_USER_CANARY_45a8",
				"SYNTHETIC_CODEX_ENDPOINT_UID_CANARY_7c2e",
				"/synthetic/live-codex/ABSOLUTE_PATH_CANARY_8f31/project",
				"/synthetic/live-codex/EVIDENCE_PATH_CANARY_59ba/session.ndjson",
				"SYNTHETIC_PROMPT_CONTENT_CANARY_2de7",
				"SYNTHETIC_ASSISTANT_CONTENT_CANARY_104c",
				"SYNTHETIC_TOOL_CALL_CONTENT_CANARY_c631",
				"SYNTHETIC_TOOL_RESULT_CONTENT_CANARY_5ab2",
				"COMMAND_SECRET_CANARY_b804",
				"URL_SECRET_CANARY_d927",
				"SYNTHETIC_COMMAND_OUTPUT_CANARY_072f",
			},
			provenance: liveFixtureProvenance{
				kind:               "codex",
				runID:              "synthetic-live-codex-run",
				sourceAgent:        "codex",
				eventIDPrefix:      "synthetic-live-codex-event-",
				sessionID:          "synthetic-live-codex-session",
				projectPath:        "/synthetic/live-codex/ABSOLUTE_PATH_CANARY_8f31/project",
				evidencePath:       "/synthetic/live-codex/EVIDENCE_PATH_CANARY_59ba/session.ndjson",
				evidenceType:       "codex_rollout",
				indicatorValue:     "synthetic-indicator.example.test",
				projectPathHash:    "synthetic-live-codex-project-hash",
				endpointHostname:   "SYNTHETIC_CODEX_ENDPOINT_HOST_CANARY_91d3",
				endpointUsername:   "SYNTHETIC_CODEX_ENDPOINT_USER_CANARY_45a8",
				endpointUID:        "SYNTHETIC_CODEX_ENDPOINT_UID_CANARY_7c2e",
				expectedEvents:     8,
				expectedIndicators: 1,
			},
		},
		{
			name:        "claude",
			fixture:     "claude-sanitized.ndjson",
			runID:       "synthetic-live-claude-run",
			sourceAgent: "claude-code",
			events:      7,
			indicators:  1,
			syntheticCanaries: []string{
				"SYNTHETIC_CLAUDE_ENDPOINT_HOST_CANARY_3e91",
				"SYNTHETIC_CLAUDE_ENDPOINT_USER_CANARY_f621",
				"SYNTHETIC_CLAUDE_ENDPOINT_UID_CANARY_b430",
				"/synthetic/live-claude/ABSOLUTE_PATH_CANARY_e4b7/project",
				"/synthetic/live-claude/EVIDENCE_PATH_CANARY_6ad2/session.jsonl",
				"SYNTHETIC_CLAUDE_PROMPT_CONTENT_CANARY_73cc",
				"SYNTHETIC_CLAUDE_ASSISTANT_CONTENT_CANARY_d185",
				"CLAUDE_COMMAND_SECRET_CANARY_5e29",
				"CLAUDE_URL_SECRET_CANARY_81a0",
				"SYNTHETIC_CLAUDE_MCP_CONTENT_CANARY_4f96",
			},
			provenance: liveFixtureProvenance{
				kind:               "claude",
				runID:              "synthetic-live-claude-run",
				sourceAgent:        "claude-code",
				eventIDPrefix:      "synthetic-live-claude-event-",
				sessionID:          "synthetic-live-claude-session",
				projectPath:        "/synthetic/live-claude/ABSOLUTE_PATH_CANARY_e4b7/project",
				evidencePath:       "/synthetic/live-claude/EVIDENCE_PATH_CANARY_6ad2/session.jsonl",
				evidenceType:       "claude_jsonl",
				indicatorValue:     "synthetic-claude-indicator.example.test",
				projectPathHash:    "synthetic-live-claude-project-hash",
				endpointHostname:   "SYNTHETIC_CLAUDE_ENDPOINT_HOST_CANARY_3e91",
				endpointUsername:   "SYNTHETIC_CLAUDE_ENDPOINT_USER_CANARY_f621",
				endpointUID:        "SYNTHETIC_CLAUDE_ENDPOINT_UID_CANARY_b430",
				expectedEvents:     7,
				expectedIndicators: 1,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := readLiveFixture(t, testCase.fixture)
			assertLiveFixtureProvenance(t, fixture, testCase.provenance)

			databasePath := filepath.Join(t.TempDir(), "belay-live-conformance.sqlite")
			store, err := local.Open(databasePath, liveConformanceKeyProvider{})
			if err != nil {
				t.Fatalf("local.Open() error = %v", err)
			}

			importer := pipeline.New(
				store,
				"inst_live_conformance",
				"numbat dev+f0778c09dc48 (schema 0.3.0)",
			).WithClock(func() time.Time {
				return time.Date(2003, 3, 3, 0, 0, 0, 0, time.UTC)
			}).WithRandom(&liveDeterministicReader{})

			first, err := importer.Import(context.Background(), bytes.NewReader(fixture))
			if err != nil {
				_ = store.Close()
				t.Fatalf("first Import() error = %v", err)
			}
			assertLiveReport(t, first, testCase.events, testCase.indicators, false)

			second, err := importer.Import(context.Background(), bytes.NewReader(fixture))
			if err != nil {
				_ = store.Close()
				t.Fatalf("replay Import() error = %v", err)
			}
			assertLiveReport(t, second, testCase.events, testCase.indicators, true)

			summary, err := store.ImportRun(context.Background(), testCase.runID)
			if err != nil {
				_ = store.Close()
				t.Fatalf("ImportRun() error = %v", err)
			}
			if summary.Status != "complete" || !summary.Complete ||
				summary.EventsEmitted != testCase.events ||
				summary.IndicatorsEmitted != testCase.indicators {
				t.Errorf("ImportRun() = %+v, want complete with %d events and %d indicators",
					summary, testCase.events, testCase.indicators)
			}

			canonicalJSON := liveCanonicalJSON(t, store, testCase.events, testCase.sourceAgent)
			assertByteSlicesExclude(t, "canonical JSON", canonicalJSON, testCase.syntheticCanaries)

			if err := store.Close(); err != nil {
				t.Fatalf("Store.Close() error = %v", err)
			}
			assertLiveDatabaseFilesExclude(t, databasePath, testCase.syntheticCanaries)
		})
	}
}

type liveFixtureProvenance struct {
	kind               string
	runID              string
	sourceAgent        string
	eventIDPrefix      string
	sessionID          string
	projectPath        string
	evidencePath       string
	evidenceType       string
	indicatorValue     string
	projectPathHash    string
	endpointHostname   string
	endpointUsername   string
	endpointUID        string
	expectedEvents     int
	expectedIndicators int
}

type liveFixtureRecord struct {
	SchemaVersion string `json:"schema_version"`
	RecordType    string `json:"record_type"`
	RunID         string `json:"run_id"`
	Endpoint      struct {
		Hostname string `json:"hostname"`
		OS       string `json:"os"`
		Arch     string `json:"arch"`
		Username string `json:"username"`
		UID      string `json:"uid"`
	} `json:"endpoint"`
	EventID               string `json:"event_id"`
	SourceAgent           string `json:"source_agent"`
	SourceType            string `json:"source_type"`
	ProjectPath           string `json:"project_path"`
	SessionID             string `json:"session_id"`
	Value                 string `json:"value"`
	SampleEventID         string `json:"sample_event_id"`
	SampleSessionID       string `json:"sample_session_id"`
	SampleProjectPathHash string `json:"sample_project_path_hash"`
	Evidence              *struct {
		ArtifactType string `json:"artifact_type"`
		LocalPath    string `json:"local_path"`
		Line         int    `json:"line"`
		JSONPointer  string `json:"json_pointer"`
		SHA256       string `json:"sha256"`
	} `json:"evidence"`
}

func assertLiveFixtureProvenance(
	t *testing.T,
	fixture []byte,
	expected liveFixtureProvenance,
) {
	t.Helper()
	var events, indicators int
	for lineIndex, line := range bytes.Split(bytes.TrimSpace(fixture), []byte{'\n'}) {
		var record liveFixtureRecord
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decode fixture line %d: %v", lineIndex+1, err)
		}
		if record.SchemaVersion != "0.3.0" ||
			record.RunID != expected.runID ||
			record.Endpoint.Hostname != expected.endpointHostname ||
			record.Endpoint.OS != "darwin" ||
			record.Endpoint.Arch != "arm64" ||
			record.Endpoint.Username != expected.endpointUsername ||
			record.Endpoint.UID != expected.endpointUID {
			t.Errorf("fixture line %d has non-allowlisted common provenance: %+v", lineIndex+1, record)
		}
		switch record.RecordType {
		case "event":
			events++
			wantEventID := fmt.Sprintf("%s%03d", expected.eventIDPrefix, events)
			if record.EventID != wantEventID ||
				record.SourceAgent != expected.sourceAgent ||
				record.SourceType != "artifact" ||
				record.ProjectPath != expected.projectPath ||
				record.SessionID != expected.sessionID {
				t.Errorf("fixture event %d has non-allowlisted provenance: %+v", events, record)
			}
			if record.Evidence == nil {
				t.Errorf("fixture event %d has no evidence provenance", events)
				continue
			}
			wantSHA256 := syntheticEvidenceSHA256(expected.kind, events)
			decodedSHA256, err := hex.DecodeString(record.Evidence.SHA256)
			if err != nil || len(decodedSHA256) != 32 ||
				record.Evidence.SHA256 != wantSHA256 ||
				record.Evidence.ArtifactType != expected.evidenceType ||
				record.Evidence.LocalPath != expected.evidencePath ||
				record.Evidence.Line != events ||
				(record.Evidence.JSONPointer != "" &&
					!strings.HasPrefix(record.Evidence.JSONPointer, "/synthetic/")) {
				t.Errorf("fixture event %d has non-synthetic evidence provenance: %+v", events, record.Evidence)
			}
		case "indicator":
			indicators++
			if record.SourceAgent != expected.sourceAgent ||
				record.Value != expected.indicatorValue ||
				!strings.HasPrefix(record.SampleEventID, expected.eventIDPrefix) ||
				record.SampleSessionID != expected.sessionID ||
				record.SampleProjectPathHash != expected.projectPathHash {
				t.Errorf("fixture indicator has non-allowlisted provenance: %+v", record)
			}
		case "scan_summary":
		default:
			t.Errorf("fixture line %d has non-allowlisted record type %q", lineIndex+1, record.RecordType)
		}
	}
	if events != expected.expectedEvents || indicators != expected.expectedIndicators {
		t.Errorf(
			"fixture provenance counts = events:%d indicators:%d, want events:%d indicators:%d",
			events,
			indicators,
			expected.expectedEvents,
			expected.expectedIndicators,
		)
	}
}

func syntheticEvidenceSHA256(kind string, line int) string {
	if kind == "claude" {
		return hex.EncodeToString([]byte(fmt.Sprintf(
			"SYNTHETIC_CLAUDE_EVIDENCE_%06d",
			line,
		)))
	}
	return strings.Repeat(strconv.Itoa(line), 64)
}

func readLiveFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "numbat", "v0.3.0", "live", name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return body
}

func assertLiveReport(
	t *testing.T,
	report pipeline.Report,
	events int,
	indicators int,
	replay bool,
) {
	t.Helper()
	wantAccepted, wantDuplicates := events, 0
	if replay {
		wantAccepted, wantDuplicates = 0, events
	}
	if report.Lines != int64(events+indicators+1) ||
		report.EventsAccepted != wantAccepted ||
		report.EventDuplicates != wantDuplicates ||
		report.IndicatorsIgnored != indicators ||
		report.SummariesAccepted != 1 ||
		report.FindingsAccepted != 0 ||
		report.FindingDuplicates != 0 ||
		report.DiagnosticsAccepted != 0 ||
		report.EnforcementRejected != 0 ||
		report.Quarantined != 0 ||
		report.Malformed != 0 ||
		report.Oversized != 0 {
		t.Errorf("Import() report = %+v", report)
	}
}

func liveCanonicalJSON(
	t *testing.T,
	store *local.Store,
	wantEvents int,
	wantSourceAgent string,
) [][]byte {
	t.Helper()
	ctx := context.Background()
	sessions, _, err := store.ListSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("ListSessions() returned %d sessions, want 1", len(sessions))
	}
	events, _, err := store.GetSessionTimeline(ctx, sessions[0].SessionID, 100)
	if err != nil {
		t.Fatalf("GetSessionTimeline() error = %v", err)
	}
	if len(events) != wantEvents {
		t.Fatalf("GetSessionTimeline() returned %d events, want %d", len(events), wantEvents)
	}

	result := make([][]byte, 0, len(events))
	for _, event := range events {
		if event.Source.Agent != wantSourceAgent {
			t.Errorf("canonical source agent = %q, want %q", event.Source.Agent, wantSourceAgent)
		}
		body, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("json.Marshal(canonical event) error = %v", err)
		}
		result = append(result, body)
	}
	return result
}

func assertByteSlicesExclude(
	t *testing.T,
	subject string,
	bodies [][]byte,
	prohibited []string,
) {
	t.Helper()
	for index, body := range bodies {
		for _, value := range prohibited {
			if bytes.Contains(body, []byte(value)) {
				t.Errorf("%s %d contains privacy canary %q", subject, index+1, value)
			}
		}
	}
}

func assertLiveDatabaseFilesExclude(t *testing.T, databasePath string, prohibited []string) {
	t.Helper()
	inspected := 0
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm"} {
		body, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read SQLite file %s: %v", path, err)
		}
		inspected++
		for _, value := range prohibited {
			if bytes.Contains(body, []byte(value)) {
				t.Errorf("SQLite file %s contains privacy canary %q", filepath.Base(path), value)
			}
		}
	}
	if inspected == 0 {
		t.Fatal("no SQLite database or sidecar files were available for byte inspection")
	}
}

type liveConformanceKeyProvider struct{}

func (liveConformanceKeyProvider) Load(context.Context, string) ([]byte, error) {
	return bytes.Repeat([]byte{0x7c}, 32), nil
}

func (liveConformanceKeyProvider) Create(context.Context, string) ([]byte, error) {
	return nil, errors.New("live conformance key provider must not create keys")
}

type liveDeterministicReader struct {
	next byte
}

func (reader *liveDeterministicReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		reader.next++
		buffer[index] = reader.next
	}
	return len(buffer), nil
}
