package pipeline_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/limits"
	"github.com/DoplexLabs/belay-engine/internal/pipeline"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

var acceptanceClock = time.Date(2026, 9, 8, 20, 0, 0, 0, time.UTC)

func TestEdgeAcceptanceRecordFamiliesAndRouting(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	input := appendNDJSON(t, allEventTypesFixture(t), otherRecordFamilies(t)...)

	report, err := newTestImporter(store).Import(ctx, bytes.NewReader(input))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	counters := []struct {
		name string
		got  int64
		want int64
	}{
		{"lines", report.Lines, 23},
		{"events accepted", int64(report.EventsAccepted), 18},
		{"event duplicates", int64(report.EventDuplicates), 0},
		{"findings accepted", int64(report.FindingsAccepted), 1},
		{"finding duplicates", int64(report.FindingDuplicates), 0},
		{"summaries accepted", int64(report.SummariesAccepted), 1},
		{"diagnostics accepted", int64(report.DiagnosticsAccepted), 1},
		{"indicators ignored", int64(report.IndicatorsIgnored), 1},
		{"enforcement rejected", int64(report.EnforcementRejected), 1},
		{"quarantined", int64(report.Quarantined), 1},
		{"malformed", int64(report.Malformed), 0},
		{"oversized", int64(report.Oversized), 0},
	}
	for _, counter := range counters {
		t.Run(counter.name, func(t *testing.T) {
			if counter.got != counter.want {
				t.Errorf("counter = %d, want %d", counter.got, counter.want)
			}
		})
	}

	tableCounts := map[string]int{
		"events":      18,
		"findings":    1,
		"import_runs": 1,
		"quarantine":  1,
		"diagnostics": 1,
	}
	for table, want := range tableCounts {
		t.Run("table "+table, func(t *testing.T) {
			if got := countTable(t, store, table); got != want {
				t.Errorf("%s row count = %d, want %d", table, got, want)
			}
		})
	}

	summary, err := store.ImportRun(ctx, "run-synthetic")
	if err != nil {
		t.Fatalf("ImportRun() error = %v", err)
	}
	if summary.Status != "complete" || !summary.Complete {
		t.Errorf("import summary completion = (%q, %t), want (complete, true)", summary.Status, summary.Complete)
	}
	if summary.ArtifactsScanned != 1 || summary.EventsEmitted != 18 ||
		summary.FindingsEmitted != 1 || summary.IndicatorsEmitted != 1 ||
		summary.Diagnostics != 1 {
		t.Errorf("import summary counters = %+v", summary)
	}

	quarantines, err := store.Quarantines(ctx)
	if err != nil {
		t.Fatalf("Quarantines() error = %v", err)
	}
	if len(quarantines) != 1 || quarantines[0].Category != "enforcement_rejected" {
		t.Errorf("quarantines = %+v, want one enforcement rejection", quarantines)
	}
	diagnosticCodes, err := store.DiagnosticCodes(ctx)
	if err != nil {
		t.Fatalf("DiagnosticCodes() error = %v", err)
	}
	if !slices.Equal(diagnosticCodes, []string{"numbat.warn"}) {
		t.Errorf("diagnostic codes = %q, want numbat.warn", diagnosticCodes)
	}
	finding, err := store.GetFinding(ctx, "finding-sanitized-1")
	if err != nil {
		t.Fatalf("GetFinding() error = %v", err)
	}
	timeline := onlySessionTimeline(t, ctx, store)
	var citedCanonicalID string
	for _, event := range timeline {
		if event.Source.RecordID == "synthetic-e07" {
			citedCanonicalID = event.EventID
			break
		}
	}
	if citedCanonicalID == "" ||
		!slices.Equal(finding.CitedEventIDs, []string{citedCanonicalID}) {
		t.Errorf(
			"finding citations = %q, want canonical event ID %q",
			finding.CitedEventIDs,
			citedCanonicalID,
		)
	}
}

func TestEdgeAcceptanceUnresolvedFindingCitationIsQuarantined(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	record := otherRecordFamilies(t)[0].(map[string]any)
	record["cited_event_ids"] = []string{"missing-upstream-event"}

	report, err := newTestImporter(store).Import(
		ctx,
		bytes.NewReader(appendNDJSON(t, nil, record)),
	)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if report.FindingsAccepted != 0 || report.Quarantined != 1 {
		t.Fatalf("report = %+v, want unresolved finding quarantined", report)
	}
	quarantines, err := store.Quarantines(ctx)
	if err != nil {
		t.Fatalf("Quarantines() error = %v", err)
	}
	if len(quarantines) != 1 ||
		quarantines[0].Category != "unresolved_finding_citation" {
		t.Fatalf("quarantines = %+v", quarantines)
	}
}

func TestEdgeAcceptanceOversizedFindingCitationSetIsQuarantined(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	record := otherRecordFamilies(t)[0].(map[string]any)
	citations := make([]string, limits.MaxFindingCitedEventIDs+1)
	for index := range citations {
		citations[index] = fmt.Sprintf("event-%03d", index)
	}
	record["cited_event_ids"] = citations

	report, err := newTestImporter(store).Import(
		ctx,
		bytes.NewReader(appendNDJSON(t, nil, record)),
	)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if report.FindingsAccepted != 0 || report.Quarantined != 1 {
		t.Fatalf("report = %+v, want oversized finding quarantined", report)
	}
	quarantines, err := store.Quarantines(ctx)
	if err != nil {
		t.Fatalf("Quarantines() error = %v", err)
	}
	if len(quarantines) != 1 ||
		quarantines[0].Category != "invalid_record" ||
		quarantines[0].Reason != "finding cited_event_ids exceeds limit" {
		t.Fatalf("quarantines = %+v", quarantines)
	}
}

func TestEdgeAcceptanceAllEventTypesInSourceOrder(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	report, err := newTestImporter(store).Import(ctx, bytes.NewReader(allEventTypesFixture(t)))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if report.EventsAccepted != 18 || report.Quarantined != 0 {
		t.Fatalf("report = %+v, want 18 accepted events and no quarantine", report)
	}

	timeline := onlySessionTimeline(t, ctx, store)
	wantTypes := []string{
		"session.start",
		"prompt.user",
		"message.assistant",
		"message.reasoning",
		"tool.call",
		"tool.result",
		"command.exec",
		"command.result",
		"file.read",
		"file.write",
		"file.delete",
		"permission.requested",
		"permission.approved",
		"permission.denied",
		"config.agent",
		"config.mcp",
		"network.indicator",
		"session.end",
	}
	gotTypes := make([]string, len(timeline))
	for index, event := range timeline {
		gotTypes[index] = event.Observation.Type
		if event.Source.Sequence != int64(index+1) {
			t.Errorf("event %d source sequence = %d, want %d", index, event.Source.Sequence, index+1)
		}
	}
	if !slices.Equal(gotTypes, wantTypes) {
		t.Errorf("timeline event types =\n%q\nwant\n%q", gotTypes, wantTypes)
	}
}

func TestEdgeAcceptanceMalformedAndOversizedLinesContinueWithoutPayloadPersistence(t *testing.T) {
	ctx := context.Background()
	store, databasePath := openTestStore(t)

	const (
		malformedCanary = "MALFORMED_LINE_PAYLOAD_CANARY_1f8cc6"
		oversizedCanary = "OVERSIZED_LINE_PAYLOAD_CANARY_7a51de"
	)
	malformed := []byte(`{"schema_version":"0.3.0","record_type":"event","payload":"` + malformedCanary)
	oversized := append([]byte(oversizedCanary), bytes.Repeat([]byte("x"), pipeline.MaxUpstreamRecordBytes+1)...)
	valid := marshalLine(t, validEventRecord("run-continuation", "continuation-event", "session-continuation"))

	var input bytes.Buffer
	input.Write(malformed)
	input.WriteByte('\n')
	input.Write(oversized)
	input.WriteByte('\n')
	input.Write(valid)
	input.WriteByte('\n')

	report, err := newTestImporter(store).Import(ctx, &input)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if report.Lines != 3 || report.EventsAccepted != 1 || report.Quarantined != 2 ||
		report.Malformed != 1 || report.Oversized != 1 {
		t.Errorf("report = %+v, want continuation after one malformed and one oversized line", report)
	}
	if got := countTable(t, store, "events"); got != 1 {
		t.Errorf("events row count = %d, want 1", got)
	}

	quarantines, err := store.Quarantines(ctx)
	if err != nil {
		t.Fatalf("Quarantines() error = %v", err)
	}
	wantCategories := []string{"malformed_json", "oversized_record"}
	if len(quarantines) != len(wantCategories) {
		t.Fatalf("quarantine rows = %d, want %d", len(quarantines), len(wantCategories))
	}
	for index, row := range quarantines {
		if row.LineNumber != int64(index+1) || row.Category != wantCategories[index] {
			t.Errorf("quarantine row %d = %+v, want line %d category %q",
				index, row, index+1, wantCategories[index])
		}
		if !strings.HasPrefix(row.RecordSHA256, "sha256:") ||
			len(row.RecordSHA256) != len("sha256:")+64 {
			t.Errorf("quarantine digest = %q, want payload-free SHA-256", row.RecordSHA256)
		}
		storedMetadata := row.Category + row.Reason + row.RecordSHA256
		for _, canary := range []string{malformedCanary, oversizedCanary} {
			if strings.Contains(storedMetadata, canary) {
				t.Errorf("quarantine metadata leaked %q", canary)
			}
		}
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	assertDatabaseFilesExclude(t, databasePath, malformedCanary, oversizedCanary)
}

func TestEdgeAcceptanceEnforcementIsRejected(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	input := appendNDJSON(t, nil, enforcementRecord())

	report, err := newTestImporter(store).Import(ctx, bytes.NewReader(input))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if report.Lines != 1 || report.EnforcementRejected != 1 || report.Quarantined != 1 {
		t.Errorf("report = %+v, want one rejected and quarantined enforcement record", report)
	}
	for _, table := range []string{"events", "findings", "diagnostics"} {
		if got := countTable(t, store, table); got != 0 {
			t.Errorf("%s row count = %d, want 0", table, got)
		}
	}
	if got := countTable(t, store, "import_runs"); got != 1 {
		t.Errorf("import_runs row count = %d, want 1", got)
	}

	quarantines, err := store.Quarantines(ctx)
	if err != nil {
		t.Fatalf("Quarantines() error = %v", err)
	}
	if len(quarantines) != 1 ||
		quarantines[0].Category != "enforcement_rejected" ||
		quarantines[0].Reason != "Belay V1 does not accept enforcement records" ||
		!strings.HasPrefix(quarantines[0].RecordSHA256, "sha256:") {
		t.Errorf("enforcement quarantines = %+v", quarantines)
	}
}

func TestEdgeAcceptancePrivacyCanariesNeverReachCanonicalOrSQLite(t *testing.T) {
	ctx := context.Background()
	store, databasePath := openTestStore(t)
	input, prohibited := privacyCanaryFixture(t)

	report, err := newTestImporter(store).Import(ctx, bytes.NewReader(input))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if report.EventsAccepted != 18 || report.Quarantined != 0 {
		t.Fatalf("report = %+v, want all privacy records accepted", report)
	}

	timeline := onlySessionTimeline(t, ctx, store)
	var sawRelativeFile, sawSafeURL bool
	for _, event := range timeline {
		if event.Observation.Type == "file.read" && event.Observation.Resource != nil {
			sawRelativeFile = event.Observation.Resource.Kind == "file" &&
				event.Observation.Resource.Name == "read-safe.go"
		}
		if event.Observation.Type == "network.indicator" && event.Observation.Resource != nil {
			sawSafeURL = event.Observation.Resource.Kind == "network" &&
				event.Observation.Resource.Name == "https://safe.example.test:8443"
		}
	}
	if !sawRelativeFile {
		t.Error("minimized relative file metadata was not retained")
	}
	if !sawSafeURL {
		t.Error("safe URL scheme and host metadata was not retained")
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	assertDatabaseFilesExclude(t, databasePath, prohibited...)
}

func TestEdgeAcceptanceReplayDeduplicatesEvents(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	fixture := allEventTypesFixture(t)
	importer := newTestImporter(store)

	first, err := importer.Import(ctx, bytes.NewReader(fixture))
	if err != nil {
		t.Fatalf("first Import() error = %v", err)
	}
	second, err := importer.Import(ctx, bytes.NewReader(fixture))
	if err != nil {
		t.Fatalf("second Import() error = %v", err)
	}
	if first.EventsAccepted != 18 || first.EventDuplicates != 0 {
		t.Errorf("first report = %+v, want 18 accepted and no duplicates", first)
	}
	if second.EventsAccepted != 0 || second.EventDuplicates != 18 {
		t.Errorf("second report = %+v, want no accepted and 18 duplicates", second)
	}
	if got := countTable(t, store, "events"); got != 18 {
		t.Errorf("events row count after replay = %d, want 18", got)
	}
}

func TestEdgeAcceptanceTimelineOrderingIsDeterministic(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	sessionKey := "ses_11111111111111111111111111111111"
	early := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	late := early.Add(time.Minute)

	events := []model.Event{
		canonicalEvent("00000000-0000-7000-8000-000000000004", sessionKey, 2, early, 4),
		canonicalEvent("00000000-0000-7000-8000-000000000003", sessionKey, 1, late, 3),
		canonicalEvent("00000000-0000-7000-8000-000000000002", sessionKey, 1, early, 2),
		canonicalEvent("00000000-0000-7000-8000-000000000001", sessionKey, 1, early, 1),
	}
	for _, event := range events {
		inserted, err := store.AppendEvent(ctx, event)
		if err != nil {
			t.Fatalf("AppendEvent(%s) error = %v", event.EventID, err)
		}
		if !inserted {
			t.Fatalf("AppendEvent(%s) did not insert", event.EventID)
		}
	}

	want := []string{
		"00000000-0000-7000-8000-000000000001",
		"00000000-0000-7000-8000-000000000002",
		"00000000-0000-7000-8000-000000000003",
		"00000000-0000-7000-8000-000000000004",
	}
	for read := 0; read < 5; read++ {
		timeline, _, err := store.GetSessionTimeline(ctx, sessionKey, 100)
		if err != nil {
			t.Fatalf("GetSessionTimeline() read %d error = %v", read, err)
		}
		got := make([]string, len(timeline))
		for index, event := range timeline {
			got[index] = event.EventID
		}
		if !slices.Equal(got, want) {
			t.Errorf("GetSessionTimeline() read %d order = %q, want %q", read, got, want)
		}
	}
}

type deterministicReader struct {
	next byte
}

func (reader *deterministicReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		reader.next++
		buffer[index] = reader.next
	}
	return len(buffer), nil
}

func newTestImporter(store *local.Store) *pipeline.Importer {
	return pipeline.New(store, "inst_edge_acceptance", "numbat-test-0.3.0").
		WithClock(func() time.Time { return acceptanceClock }).
		WithRandom(&deterministicReader{})
}

func openTestStore(t *testing.T) (*local.Store, string) {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "belay-local.sqlite")
	store, err := local.Open(databasePath, acceptanceKeyProvider{})
	if err != nil {
		t.Fatalf("local.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store, databasePath
}

type acceptanceKeyProvider struct{}

func (acceptanceKeyProvider) Load(context.Context, string) ([]byte, error) {
	return bytes.Repeat([]byte{0x5a}, 32), nil
}

func (acceptanceKeyProvider) Create(context.Context, string) ([]byte, error) {
	return nil, errors.New("acceptance key provider must not create keys")
}

func allEventTypesFixture(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "numbat", "v0.3.0", "all-event-types.ndjson")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return body
}

func appendNDJSON(t *testing.T, prefix []byte, records ...any) []byte {
	t.Helper()
	var output bytes.Buffer
	if len(bytes.TrimSpace(prefix)) > 0 {
		output.Write(bytes.TrimSpace(prefix))
		output.WriteByte('\n')
	}
	for _, record := range records {
		output.Write(marshalLine(t, record))
		output.WriteByte('\n')
	}
	return output.Bytes()
}

func marshalLine(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return body
}

func endpoint() map[string]any {
	return map[string]any{
		"hostname":  "sanitized-host",
		"os":        "darwin",
		"arch":      "arm64",
		"username":  "sanitized-user",
		"uid":       "uid-sanitized",
		"device_id": "device-sanitized",
	}
}

func evidence() map[string]any {
	return map[string]any{
		"artifact_type": "codex_rollout",
		"local_path":    "/sanitized/input/session.ndjson",
		"line":          1,
	}
}

func otherRecordFamilies(t *testing.T) []any {
	t.Helper()
	return []any{
		map[string]any{
			"schema_version":      "0.3.0",
			"record_type":         "finding",
			"run_id":              "run-synthetic",
			"endpoint":            endpoint(),
			"finding_id":          "finding-sanitized-1",
			"detected_at":         "2026-09-08T18:01:00Z",
			"rule_id":             "rule.sanitized",
			"rule_version":        "1",
			"severity":            "medium",
			"source_agent":        "codex",
			"source_type":         "artifact",
			"session_id":          "synthetic-session",
			"title":               "Sanitized finding",
			"observed_event_type": "command.exec",
			"observed_actor":      "assistant",
			"evidence_refs":       []any{evidence()},
			"cited_event_ids":     []string{"synthetic-e07"},
			"redacted":            true,
			"confidence":          "high",
		},
		map[string]any{
			"schema_version":     "0.3.0",
			"record_type":        "scan_summary",
			"run_id":             "run-synthetic",
			"endpoint":           endpoint(),
			"status":             "complete",
			"artifacts_scanned":  1,
			"events_emitted":     18,
			"findings_emitted":   1,
			"indicators_emitted": 1,
			"diagnostics":        1,
		},
		map[string]any{
			"schema_version": "0.3.0",
			"record_type":    "diagnostic",
			"run_id":         "run-synthetic",
			"endpoint":       endpoint(),
			"timestamp":      "2026-09-08T18:01:01Z",
			"level":          "warn",
			"message":        "Sanitized diagnostic message",
		},
		map[string]any{
			"schema_version":    "0.3.0",
			"record_type":       "indicator",
			"run_id":            "run-synthetic",
			"endpoint":          endpoint(),
			"type":              "domain",
			"value":             "safe.example.test",
			"count":             1,
			"source_agent":      "codex",
			"sample_event_id":   "synthetic-e17",
			"sample_session_id": "synthetic-session",
		},
		enforcementRecord(),
	}
}

func enforcementRecord() map[string]any {
	return map[string]any{
		"schema_version":   "0.3.0",
		"record_type":      "enforcement",
		"run_id":           "run-synthetic",
		"endpoint":         endpoint(),
		"decision_id":      "enf-0123456789abcdef01234567",
		"timestamp":        "2026-09-08T18:01:02Z",
		"decision":         "no_override",
		"mode":             "monitor",
		"reason":           "monitor_mode",
		"source_agent":     "codex",
		"source_type":      "hook",
		"session_id":       "synthetic-session",
		"action_event_ids": []string{"synthetic-e07"},
		"rule_ids":         []string{"rule.sanitized"},
	}
}

func validEventRecord(runID, eventID, sessionID string) map[string]any {
	return map[string]any{
		"schema_version": "0.3.0",
		"record_type":    "event",
		"run_id":         runID,
		"endpoint":       endpoint(),
		"event_id":       eventID,
		"source_agent":   "codex",
		"source_type":    "hook",
		"timestamp":      "2026-09-08T18:02:00Z",
		"session_id":     sessionID,
		"actor":          "system",
		"event_type":     "session.start",
		"confidence":     "high",
		"evidence": map[string]any{
			"artifact_type": "hook",
		},
	}
}

func onlySessionTimeline(t *testing.T, ctx context.Context, store *local.Store) []model.Event {
	t.Helper()
	sessions, _, err := store.ListSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("ListSessions() returned %d sessions, want 1", len(sessions))
	}
	timeline, _, err := store.GetSessionTimeline(ctx, sessions[0].SessionID, 100)
	if err != nil {
		t.Fatalf("GetSessionTimeline() error = %v", err)
	}
	return timeline
}

func countTable(t *testing.T, store *local.Store, table string) int {
	t.Helper()
	count, err := store.Count(context.Background(), table)
	if err != nil {
		t.Fatalf("Count(%q) error = %v", table, err)
	}
	return count
}

func privacyCanaryFixture(t *testing.T) ([]byte, []string) {
	t.Helper()
	const (
		rawPrompt    = "RAW_PROMPT_CANARY_0c56b4"
		completion   = "COMPLETION_CANARY_a81e73"
		reasoning    = "REASONING_CANARY_90bca2"
		hostname     = "ENDPOINT_HOSTNAME_CANARY_78de21"
		username     = "ENDPOINT_USERNAME_CANARY_4d9a32"
		uid          = "ENDPOINT_UID_CANARY_f2308c"
		deviceID     = "ENDPOINT_DEVICE_CANARY_c5ab91"
		projectPath  = "/private/PROJECT_PATH_CANARY_7ce240/workspace"
		evidencePath = "/private/EVIDENCE_PATH_CANARY_83f9d1/session.ndjson"
		urlUserinfo  = "URL_USERINFO_CANARY_26ecbd"
		urlPassword  = "URL_PASSWORD_CANARY_f9750a"
		urlQuery     = "URL_QUERY_CANARY_c67b18"
		urlFragment  = "URL_FRAGMENT_CANARY_629de0"
		bearer       = "BEARER_TOKEN_CANARY_513af86e"
		commandToken = "COMMAND_TOKEN_CANARY_41d2bc"
		stdout       = "STDOUT_CANARY_358a6e"
		stderr       = "STDERR_CANARY_8b07d4"
		fileContent  = "FILE_CONTENT_CANARY_ef53b9"
		diffText     = "DIFF_TEXT_CANARY_7d6a04"
		approvalText = "APPROVAL_TEXT_CANARY_e261f8"
	)

	var output bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(allEventTypesFixture(t)))
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode privacy fixture line: %v", err)
		}
		record["endpoint"] = map[string]any{
			"hostname":  hostname,
			"os":        "darwin",
			"arch":      "arm64",
			"username":  username,
			"uid":       uid,
			"device_id": deviceID,
		}
		record["project_path"] = projectPath
		record["evidence"] = map[string]any{
			"artifact_type": "codex_rollout",
			"local_path":    evidencePath,
			"line":          record["evidence"].(map[string]any)["line"],
		}

		switch record["event_type"] {
		case "prompt.user":
			record["content_preview"] = rawPrompt
			record["content"] = rawPrompt
			record["content_bytes"] = len(rawPrompt)
		case "message.assistant":
			record["content_preview"] = completion
			record["content"] = completion
			record["content_bytes"] = len(completion)
		case "message.reasoning":
			record["content_preview"] = reasoning
			record["content"] = reasoning
			record["content_bytes"] = len(reasoning)
		case "tool.call":
			record["file_path"] = projectPath + "/tool-safe.go"
		case "tool.result":
			record["content_preview"] = fileContent
		case "command.exec":
			record["command"] = "curl https://safe.example.test/api?token=" + commandToken +
				" -H 'Authorization: Bearer " + bearer + "'"
		case "command.result":
			record["command"] = "npm test -- --token=" + commandToken
			record["content_preview"] = stdout + " " + stderr
		case "file.read":
			record["file_path"] = projectPath + "/read-safe.go"
		case "file.write":
			record["file_path"] = projectPath + "/write-safe.go"
			record["content_preview"] = diffText
		case "file.delete":
			record["file_path"] = projectPath + "/delete-safe.go"
		case "permission.requested":
			record["approval_reason"] = approvalText
		case "network.indicator":
			record["url"] = "https://" + urlUserinfo + ":" + urlPassword +
				"@safe.example.test:8443/private?" + "token=" + urlQuery + "#" + urlFragment
		}
		output.Write(marshalLine(t, record))
		output.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan privacy fixture: %v", err)
	}

	prohibited := []string{
		rawPrompt,
		completion,
		reasoning,
		hostname,
		username,
		uid,
		deviceID,
		projectPath,
		"/private/PROJECT_PATH_CANARY_7ce240",
		evidencePath,
		"/private/EVIDENCE_PATH_CANARY_83f9d1",
		urlUserinfo,
		urlPassword,
		urlQuery,
		urlFragment,
		bearer,
		commandToken,
		stdout,
		stderr,
		fileContent,
		diffText,
		approvalText,
	}
	return output.Bytes(), prohibited
}

func assertDatabaseFilesExclude(t *testing.T, databasePath string, prohibited ...string) {
	t.Helper()
	inspected := 0
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm"} {
		body, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("read SQLite file %s: %v", path, err)
		}
		inspected++
		for _, canary := range prohibited {
			if bytes.Contains(body, []byte(canary)) {
				t.Errorf("SQLite file %s leaked prohibited canary %q", filepath.Base(path), canary)
			}
		}
	}
	if inspected == 0 {
		t.Fatal("no SQLite database or sidecar files were available for byte inspection")
	}
}

func canonicalEvent(eventID, sessionKey string, sequence int64, occurredAt time.Time, unique int) model.Event {
	return model.Event{
		SchemaVersion:  model.EventSchemaVersion,
		EventID:        eventID,
		InstallationID: "inst_edge_acceptance",
		OccurredAt:     occurredAt,
		ObservedAt:     acceptanceClock,
		Source: model.Source{
			Engine:           "numbat",
			EngineVersion:    "numbat-test-0.3.0",
			SchemaVersion:    "0.3.0",
			RecordType:       "event",
			RunID:            "run-ordering",
			RecordID:         fmt.Sprintf("record-%d", unique),
			Kind:             "artifact",
			Agent:            "codex",
			AdapterVersion:   "numbat-0.3.0/v1",
			DeduplicationKey: fmt.Sprintf("sha256:%064x", unique),
			Sequence:         sequence,
		},
		Session: model.SessionRef{Key: sessionKey},
		Observation: model.Observation{
			Type:     "session.start",
			Actor:    "system",
			Action:   "session",
			Outcome:  "unknown",
			Resource: &model.Resource{Kind: "session", Name: "agent-session"},
		},
		Coverage: model.Coverage{
			Depth:      "artifact",
			Confidence: "high",
		},
		Redaction: model.Redaction{
			PolicyVersion: model.RedactionVersion,
		},
		Historical: model.Historical{
			IsHistorical:         true,
			ReconstructionSource: "codex_rollout",
		},
	}
}
