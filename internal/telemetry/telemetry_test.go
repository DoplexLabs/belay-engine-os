package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type capture struct {
	mu     sync.Mutex
	events []Event
	status int
}

func (c *capture) handler(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var event Event
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&event) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	c.events = append(c.events, event)
	status := c.status
	if status == 0 {
		status = http.StatusNoContent
	}
	w.WriteHeader(status)
}

func newTestClient(t *testing.T, endpoint string, env map[string]string, now *time.Time) *Client {
	t.Helper()
	root := t.TempDir()
	return New(root, "0.0.1-alpha.6",
		WithEnvironment(func(key string) string {
			if key == EnvEndpoint && endpoint != "" {
				return endpoint
			}
			return env[key]
		}),
		WithClock(func() time.Time { return *now }),
		WithHarnessProbe(func() []string { return []string{"claude"} }),
	)
}

func TestRecordActivitySendsInstallOnceAndActiveOncePerDay(t *testing.T) {
	server := &capture{}
	ts := httptest.NewServer(http.HandlerFunc(server.handler))
	defer ts.Close()
	now := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	client := newTestClient(t, ts.URL, nil, &now)

	sent, err := client.RecordActivity(context.Background())
	if err != nil || strings.Join(sent, ",") != "install,active" {
		t.Fatalf("first run sent %v err %v", sent, err)
	}
	sent, err = client.RecordActivity(context.Background())
	if err != nil || len(sent) != 0 {
		t.Fatalf("same day must send nothing: %v %v", sent, err)
	}
	now = now.Add(26 * time.Hour)
	sent, err = client.RecordActivity(context.Background())
	if err != nil || strings.Join(sent, ",") != "active" {
		t.Fatalf("next day must send only active: %v %v", sent, err)
	}
	if len(server.events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(server.events))
	}
	first := server.events[0]
	if first.SchemaVersion != SchemaVersion || first.Event != EventInstall || !validTelemetryID(first.TelemetryID) ||
		first.Version != "0.0.1-alpha.6" || first.OS == "" || first.Arch == "" ||
		strings.Join(first.Harnesses, ",") != "claude" || first.SentAt == "" {
		t.Fatalf("unexpected payload: %+v", first)
	}
	encoded, _ := json.Marshal(first)
	var fields map[string]any
	_ = json.Unmarshal(encoded, &fields)
	if len(fields) != 8 {
		t.Fatalf("wire payload must have exactly the documented 8 fields, got %v", fields)
	}
	for _, event := range server.events[1:] {
		if event.TelemetryID != first.TelemetryID {
			t.Fatal("telemetry id must be stable across runs")
		}
	}
	body, err := os.ReadFile(filepath.Join(client.root, StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), first.TelemetryID) {
		t.Fatal("state file must persist the telemetry id")
	}
	info, _ := os.Stat(filepath.Join(client.root, StateFileName))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state permissions = %o", info.Mode().Perm())
	}
}

func TestRecordActivityRespectsOptOutEnvironmentAndDevBuilds(t *testing.T) {
	server := &capture{}
	ts := httptest.NewServer(http.HandlerFunc(server.handler))
	defer ts.Close()
	now := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)

	env := newTestClient(t, ts.URL, map[string]string{EnvDisable: "0"}, &now)
	if sent, err := env.RecordActivity(context.Background()); err != nil || len(sent) != 0 {
		t.Fatalf("env disable must send nothing: %v %v", sent, err)
	}
	if status := env.Status(); status.Enabled || !strings.Contains(status.Reason, EnvDisable) {
		t.Fatalf("unexpected status: %+v", status)
	}

	opted := newTestClient(t, ts.URL, nil, &now)
	if err := opted.SetOptOut(true); err != nil {
		t.Fatal(err)
	}
	if sent, err := opted.RecordActivity(context.Background()); err != nil || len(sent) != 0 {
		t.Fatalf("opt-out must send nothing: %v %v", sent, err)
	}
	if status := opted.Status(); status.Enabled || status.TelemetryID == "" {
		t.Fatalf("opt-out status should still show the id: %+v", status)
	}
	if err := opted.SetOptOut(false); err != nil {
		t.Fatal(err)
	}
	if !opted.Status().Enabled {
		t.Fatal("opt back in must re-enable")
	}

	dev := New(t.TempDir(), "dev", WithEnvironment(func(string) string { return "" }))
	if sent, err := dev.RecordActivity(context.Background()); err != nil || len(sent) != 0 {
		t.Fatalf("dev builds must send nothing: %v %v", sent, err)
	}
	if status := dev.Status(); status.Enabled || status.Endpoint != DefaultEndpoint {
		t.Fatalf("unexpected dev status: %+v", status)
	}
	if len(server.events) != 0 {
		t.Fatalf("no events should have reached the server, got %d", len(server.events))
	}
}

func TestRecordActivityIsFailOpenAndRetriesLater(t *testing.T) {
	server := &capture{status: http.StatusInternalServerError}
	ts := httptest.NewServer(http.HandlerFunc(server.handler))
	defer ts.Close()
	now := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	client := newTestClient(t, ts.URL, nil, &now)
	sent, err := client.RecordActivity(context.Background())
	if err == nil || len(sent) != 0 {
		t.Fatalf("server failure must be reported and nothing marked sent: %v %v", sent, err)
	}
	state, loadErr := client.loadState()
	if loadErr != nil || state.InstallReported || state.LastActiveDay != "" || state.LastSendError == "" {
		t.Fatalf("failure must leave events pending: %+v %v", state, loadErr)
	}
	server.status = http.StatusNoContent
	sent, err = client.RecordActivity(context.Background())
	if err != nil || strings.Join(sent, ",") != "install,active" {
		t.Fatalf("retry should send both: %v %v", sent, err)
	}
	unreachable := New(t.TempDir(), "1.0.0", WithEnvironment(func(key string) string {
		if key == EnvEndpoint {
			return "http://127.0.0.1:1"
		}
		return ""
	}), WithHarnessProbe(func() []string { return nil }))
	if _, err := unreachable.RecordActivity(context.Background()); err == nil {
		t.Fatal("unreachable endpoint should return an error, not panic or hang")
	}
	var netErr error = errors.New("x")
	_ = netErr
}

func TestRecordActivityHonorsDoNotTrack(t *testing.T) {
	server := &capture{}
	ts := httptest.NewServer(http.HandlerFunc(server.handler))
	defer ts.Close()
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		value    string
		disabled bool
	}{
		{"1", true}, {"true", true}, {" YES ", true}, {"on", true},
		{"0", false}, {"", false}, {"false", false},
	}
	for _, tc := range cases {
		client := newTestClient(t, ts.URL, map[string]string{EnvDoNotTrack: tc.value}, &now)
		sent, err := client.RecordActivity(context.Background())
		if err != nil {
			t.Fatalf("DO_NOT_TRACK=%q: %v", tc.value, err)
		}
		status := client.Status()
		if tc.disabled {
			if len(sent) != 0 || status.Enabled || !strings.Contains(status.Reason, EnvDoNotTrack) {
				t.Fatalf("DO_NOT_TRACK=%q must disable: sent=%v status=%+v", tc.value, sent, status)
			}
		} else if len(sent) == 0 || !status.Enabled {
			t.Fatalf("DO_NOT_TRACK=%q must not disable: sent=%v status=%+v", tc.value, sent, status)
		}
	}
	// BELAY_TELEMETRY wins the reason when both are set.
	both := newTestClient(t, ts.URL, map[string]string{EnvDisable: "0", EnvDoNotTrack: "1"}, &now)
	if status := both.Status(); status.Enabled || !strings.Contains(status.Reason, EnvDisable) {
		t.Fatalf("unexpected status with both switches: %+v", status)
	}
}

func TestRecordActivityPrintsNoticeOnceAfterFirstSuccessfulSend(t *testing.T) {
	server := &capture{status: http.StatusInternalServerError}
	ts := httptest.NewServer(http.HandlerFunc(server.handler))
	defer ts.Close()
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	root := t.TempDir()
	var notice bytes.Buffer
	newClient := func() *Client {
		return New(root, "0.0.1-alpha.8",
			WithEnvironment(func(key string) string {
				if key == EnvEndpoint {
					return ts.URL
				}
				return ""
			}),
			WithClock(func() time.Time { return now }),
			WithHarnessProbe(func() []string { return nil }),
			WithNotice(&notice),
		)
	}
	if _, err := newClient().RecordActivity(context.Background()); err == nil {
		t.Fatal("failed send must return an error")
	}
	if notice.Len() != 0 {
		t.Fatalf("no notice before a successful send, got %q", notice.String())
	}
	server.status = http.StatusNoContent
	if _, err := newClient().RecordActivity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if notice.String() != Notice {
		t.Fatalf("expected the notice once, got %q", notice.String())
	}
	now = now.Add(26 * time.Hour)
	if _, err := newClient().RecordActivity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if notice.String() != Notice {
		t.Fatalf("notice must print only once per home, got %q", notice.String())
	}
	body, err := os.ReadFile(filepath.Join(root, StateFileName))
	if err != nil || !strings.Contains(string(body), `"disclosed": true`) {
		t.Fatalf("disclosed flag must persist: %v %s", err, body)
	}
	silent := New(t.TempDir(), "0.0.1-alpha.8",
		WithEnvironment(func(key string) string {
			if key == EnvEndpoint {
				return ts.URL
			}
			return ""
		}),
		WithClock(func() time.Time { return now }),
		WithHarnessProbe(func() []string { return nil }),
	)
	if _, err := silent.RecordActivity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if notice.String() != Notice {
		t.Fatal("a client without a notice writer must stay silent")
	}
}

// redirectHome points os.UserHomeDir at a private directory. Windows resolves
// it through USERPROFILE rather than HOME, so both are set.
func redirectHome(t *testing.T, home string) {
	t.Helper()

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func TestInstalledHarnessesReportsCursorFromItsRealHomeDirectory(t *testing.T) {
	home := t.TempDir()
	redirectHome(t, home)
	if containsHarness(installedHarnesses(), "cursor") {
		t.Fatal("cursor reported without a ~/.cursor directory")
	}
	if err := os.Mkdir(filepath.Join(home, ".cursor"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !containsHarness(installedHarnesses(), "cursor") {
		t.Fatal("cursor not reported for a real ~/.cursor directory")
	}
}

func TestInstalledHarnessesRejectsNonDirectoryAndSymlinkedCursorHome(t *testing.T) {
	t.Run("regular file", func(t *testing.T) {
		home := t.TempDir()
		redirectHome(t, home)
		if err := os.WriteFile(filepath.Join(home, ".cursor"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if containsHarness(installedHarnesses(), "cursor") {
			t.Fatal("cursor reported for a regular ~/.cursor file")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		home := t.TempDir()
		target := t.TempDir()
		redirectHome(t, home)
		if err := os.Symlink(target, filepath.Join(home, ".cursor")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if containsHarness(installedHarnesses(), "cursor") {
			t.Fatal("cursor reported for a symlinked ~/.cursor")
		}
	})
}

func TestInstalledHarnessesReportsAntigravityFromItsRealAppDataDirectory(t *testing.T) {
	home := t.TempDir()
	redirectHome(t, home)
	if containsHarness(installedHarnesses(), "antigravity") {
		t.Fatal("antigravity reported without a ~/.gemini/antigravity directory")
	}
	// A bare ~/.gemini (Gemini CLI, or Antigravity's config root alone) is
	// not Antigravity: only its app-data directory counts.
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if containsHarness(installedHarnesses(), "antigravity") {
		t.Fatal("antigravity reported for ~/.gemini without an antigravity directory")
	}
	if err := os.Mkdir(filepath.Join(home, ".gemini", "antigravity"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !containsHarness(installedHarnesses(), "antigravity") {
		t.Fatal("antigravity not reported for a real ~/.gemini/antigravity directory")
	}
}

func TestInstalledHarnessesRejectsNonDirectoryAndSymlinkedAntigravityHome(t *testing.T) {
	t.Run("regular file", func(t *testing.T) {
		home := t.TempDir()
		redirectHome(t, home)
		if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(home, ".gemini", "antigravity"), nil, 0o600,
		); err != nil {
			t.Fatal(err)
		}
		if containsHarness(installedHarnesses(), "antigravity") {
			t.Fatal("antigravity reported for a regular ~/.gemini/antigravity file")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		home := t.TempDir()
		target := t.TempDir()
		redirectHome(t, home)
		if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(
			target, filepath.Join(home, ".gemini", "antigravity"),
		); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if containsHarness(installedHarnesses(), "antigravity") {
			t.Fatal("antigravity reported for a symlinked ~/.gemini/antigravity")
		}
	})
}

// The wire contract is a fixed field set, so the documented harness values and
// the one-time disclosure must name exactly the harnesses Belay can report.
func TestHarnessFieldDocumentationNamesEverySupportedHarness(t *testing.T) {
	joined := strings.Join(Fields(), "\n")
	if !strings.Contains(joined, "harnesses (any of claude, codex, cursor, antigravity present)") {
		t.Fatalf("Fields() harness description = %q", joined)
	}
	if len(Fields()) != 8 {
		t.Fatalf("Fields() = %v, want the fixed eight-field contract", Fields())
	}
	if !strings.Contains(Notice, "claude/codex/cursor/antigravity") {
		t.Fatalf("Notice must disclose every harness value: %q", Notice)
	}
}

func containsHarness(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
