package localapp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/initialization"
	"github.com/DoplexLabs/belay-engine/internal/pipeline"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

type memoryKeyProvider struct {
	keys map[string][]byte
}

func TestStartHistoricalInitializationTransitionsWithoutBlocking(t *testing.T) {
	for _, test := range []struct {
		name      string
		scanError error
		wantState string
		wantCode  *string
	}{
		{
			name:      "success",
			wantState: initialization.StateReady,
		},
		{
			name:      "failure",
			scanError: errors.New("PRIVATE_SCAN_ERROR_/Users/private/project"),
			wantState: initialization.StateDegraded,
			wantCode:  stringPointer(initialization.ErrorHistoricalScanIncomplete),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tracker := initialization.NewTracker(true, time.Now)
			started := make(chan struct{})
			release := make(chan struct{})
			done := StartHistoricalInitialization(
				context.Background(),
				tracker,
				func(context.Context) error {
					close(started)
					<-release
					return test.scanError
				},
			)
			<-started
			if status := tracker.InitializationStatus(); status.State != initialization.StateInitializing {
				t.Fatalf("blocked scan status = %+v", status)
			}
			close(release)
			<-done
			status := tracker.InitializationStatus()
			if status.State != test.wantState {
				t.Fatalf("terminal status = %+v, want %q", status, test.wantState)
			}
			if test.wantCode == nil {
				if status.ErrorCode != nil {
					t.Fatalf("error code = %v, want null", status.ErrorCode)
				}
			} else if status.ErrorCode == nil || *status.ErrorCode != *test.wantCode {
				t.Fatalf("error code = %v, want %q", status.ErrorCode, *test.wantCode)
			}
			if status.ErrorCode != nil &&
				strings.Contains(*status.ErrorCode, "PRIVATE_SCAN_ERROR") {
				t.Fatalf("raw scan error leaked into status: %+v", status)
			}
		})
	}
}

func TestStartHistoricalInitializationCancellationDoesNotReportFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tracker := initialization.NewTracker(true, time.Now)
	done := StartHistoricalInitialization(ctx, tracker, func(scanCtx context.Context) error {
		<-scanCtx.Done()
		return scanCtx.Err()
	})
	cancel()
	<-done
	if status := tracker.InitializationStatus(); status.State != initialization.StateInitializing {
		t.Fatalf("canceled process status = %+v", status)
	}
}

func (p *memoryKeyProvider) Load(_ context.Context, storeID string) ([]byte, error) {
	key, ok := p.keys[storeID]
	if !ok {
		return nil, local.ErrKeyNotFound
	}
	return append([]byte(nil), key...), nil
}

func (p *memoryKeyProvider) Create(_ context.Context, storeID string) ([]byte, error) {
	if _, ok := p.keys[storeID]; ok {
		return nil, local.ErrKeyAlreadyExists
	}
	key := bytes.Repeat([]byte{0x42}, 32)
	p.keys[storeID] = key
	return append([]byte(nil), key...), nil
}

func TestDiscoverAndScanImportsCodexAndClaude(t *testing.T) {
	root := t.TempDir()
	codexFixture, err := filepath.Abs(filepath.Join("..", "..", "testdata", "numbat", "v0.3.0", "live", "codex-sanitized.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	claudeFixture, err := filepath.Abs(filepath.Join("..", "..", "testdata", "numbat", "v0.3.0", "live", "claude-sanitized.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "fake-numbat")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  agents)
    printf '%%s\n' '[{"agent":"Codex","present":true,"detected":true},{"agent":"Claude Code","present":true,"detected":true},{"agent":"cursor","present":false,"detected":false},{"agent":"Antigravity","present":false,"detected":false}]'
    ;;
  scan)
    if [ "$3" = "codex" ]; then
      /bin/cat %q
    else
      /bin/cat %q
    fi
    ;;
  *)
    exit 2
    ;;
esac
`, codexFixture, claudeFixture)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := numbat.NewClient(binary)
	if err != nil {
		t.Fatal(err)
	}
	store, err := local.OpenWithOptions(filepath.Join(root, "belay.sqlite"), local.OpenOptions{
		KeyProvider: &memoryKeyProvider{keys: make(map[string][]byte)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	inventory, reports, err := DiscoverAndScan(context.Background(), client, store, Config{
		Version:             ConfigVersion,
		InstallationID:      "inst_runtime_test",
		NumbatVersionMarker: "numbat-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.LaunchTargets) != 4 || len(reports) != 2 {
		t.Fatalf(
			"inventory/reports = %d/%d, want 4/2",
			len(inventory.LaunchTargets),
			len(reports),
		)
	}
	for _, report := range reports {
		if report.Error != "" || report.ExitCode != 0 || report.Import.EventsAccepted == 0 {
			t.Fatalf("scan report = %+v", report)
		}
	}
	sessions, _, err := store.ListSessions(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(sessions))
	}
	issues, err := store.QueryIssues(
		context.Background(),
		model.IssueQuery{Limit: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !issues.Analysis.Complete || issues.Analysis.CurrentSessions != 2 {
		t.Fatalf("historical analysis coverage = %+v", issues.Analysis)
	}
}

// Antigravity is hook-only in the pinned Numbat: its conversations are
// encrypted and `scan --agent antigravity` is rejected. DiscoverAndScan must
// therefore never launch that scan even when the inventory reports Antigravity
// present, and must not count the omission as a scan failure.
func TestDiscoverAndScanNeverScansHookOnlyAntigravity(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "calls")
	codexFixture, err := filepath.Abs(filepath.Join(
		"..", "..", "testdata", "numbat", "v0.3.0", "live", "codex-sanitized.ndjson",
	))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "fake-numbat")
	script := fmt.Sprintf(`#!/bin/sh
{
	for arg in "$@"; do
		printf '<%%s>' "$arg"
	done
	printf '\n'
} >> %q
case "$1" in
  agents)
    printf '%%s\n' '[{"agent":"Codex","present":true,"detected":true},{"agent":"Claude Code","present":false,"detected":false},{"agent":"cursor","present":false,"detected":false},{"agent":"Antigravity","present":true,"detected":true,"at_rest":"n/a (hook-only; deferred JSONL transcript format)","hook":"hooks","wired":"no","setup_hint":"install: numbat hook install --agent antigravity"}]'
    ;;
  scan)
    if [ "$3" = "codex" ]; then
      /bin/cat %q
    else
      printf '%%s\n' 'scan --agent antigravity is not supported' >&2
      exit 2
    fi
    ;;
  *)
    exit 2
    ;;
esac
`, logPath, codexFixture)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := numbat.NewClient(binary)
	if err != nil {
		t.Fatal(err)
	}
	store, err := local.OpenWithOptions(filepath.Join(root, "belay.sqlite"), local.OpenOptions{
		KeyProvider: &memoryKeyProvider{keys: make(map[string][]byte)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	inventory, reports, err := DiscoverAndScan(context.Background(), client, store, Config{
		Version:             ConfigVersion,
		InstallationID:      "inst_antigravity_scan_test",
		NumbatVersionMarker: "numbat-test",
	})
	if err != nil {
		t.Fatalf("DiscoverAndScan() error = %v, want none for a hook-only harness", err)
	}
	row, ok := inventory.LaunchTargets[numbat.AgentAntigravity]
	if !ok || !row.Present || !row.Detected {
		t.Fatalf("antigravity inventory row = %+v/%v, want present and detected", row, ok)
	}
	if len(reports) != 2 {
		t.Fatalf("reports = %+v, want codex scan and antigravity hook-only row", reports)
	}
	if reports[0].Agent != "codex" || reports[0].Error != "" ||
		reports[0].ExitCode != 0 || reports[0].Import.EventsAccepted == 0 {
		t.Fatalf("codex report = %+v, want a successful scan", reports[0])
	}
	want := HarnessScan{Agent: "antigravity", Detected: true, ExitCode: 0}
	if reports[1].Agent != want.Agent || reports[1].Detected != want.Detected ||
		reports[1].ExitCode != want.ExitCode || reports[1].Error != "" ||
		reports[1].Import != (pipeline.Report{}) {
		t.Fatalf("antigravity report = %+v, want %+v with a zero import", reports[1], want)
	}
	calls := readHookRuntimeLog(t, logPath)
	if strings.Contains(calls, "<antigravity>") {
		t.Fatalf("Numbat was launched for antigravity: %s", calls)
	}
	wantCalls := strings.Join([]string{
		"<agents><--format><json>",
		"<scan><--agent><codex><--emit><all><--output><stdout>",
		"",
	}, "\n")
	if calls != wantCalls {
		t.Fatalf("Numbat calls:\n%s\nwant:\n%s", calls, wantCalls)
	}
}

func TestImportLiveAttemptsHarnessesIndependently(t *testing.T) {
	tests := []struct {
		name          string
		failingAgent  string
		successIndex  int
		failureIndex  int
		successCursor string
	}{
		{
			name:          "Codex failure does not prevent Claude import",
			failingAgent:  "codex",
			successIndex:  1,
			failureIndex:  0,
			successCursor: "claude.cursor.json",
		},
		{
			name:          "Claude failure preserves Codex import",
			failingAgent:  "claude",
			successIndex:  0,
			failureIndex:  1,
			successCursor: "codex.cursor.json",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			paths := hookRuntimePaths(root)
			if err := os.MkdirAll(filepath.Join(root, "live"), 0o700); err != nil {
				t.Fatal(err)
			}
			for fixture, spool := range map[string]string{
				filepath.Join("..", "..", "testdata", "numbat", "v0.3.0", "live", "codex-sanitized.ndjson"):  paths.CodexSpool,
				filepath.Join("..", "..", "testdata", "numbat", "v0.3.0", "live", "claude-sanitized.ndjson"): paths.ClaudeSpool,
			} {
				body, err := os.ReadFile(fixture)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(spool, body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(
				filepath.Join(root, "live", test.failingAgent+".cursor.json"),
				[]byte("{invalid"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			store, err := local.OpenWithOptions(filepath.Join(root, "belay.sqlite"), local.OpenOptions{
				KeyProvider: &memoryKeyProvider{keys: make(map[string][]byte)},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()

			results, err := ImportLive(context.Background(), paths, store, Config{
				Version:             ConfigVersion,
				InstallationID:      "inst_live_isolation_test",
				NumbatVersionMarker: "numbat-test",
			})
			if err == nil {
				t.Fatal("ImportLive() error = nil, want one harness failure")
			}
			if !strings.Contains(err.Error(), test.failingAgent+" live import") {
				t.Fatalf("ImportLive() error = %v, want failing harness attribution", err)
			}
			if len(results) != 4 {
				t.Fatalf("ImportLive() result count = %d, want 4 attempted harnesses", len(results))
			}
			if results[test.successIndex].Import.EventsAccepted == 0 {
				t.Fatalf("successful harness result = %+v, want accepted events", results[test.successIndex])
			}
			if results[test.failureIndex].Import.EventsAccepted != 0 {
				t.Fatalf("failed harness result = %+v, want no accepted events", results[test.failureIndex])
			}
			if _, err := os.Stat(filepath.Join(root, "live", test.successCursor)); err != nil {
				t.Fatalf("successful harness cursor was not checkpointed: %v", err)
			}
		})
	}
}

func TestImportLiveImportsTheCursorSpool(t *testing.T) {
	root := t.TempDir()
	paths := hookRuntimePaths(root)
	if err := os.MkdirAll(filepath.Join(root, "live"), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join(
		"..", "..", "testdata", "numbat", "v0.3.0", "live", "codex-sanitized.ndjson",
	))
	if err != nil {
		t.Fatal(err)
	}
	// Numbat emits the same canonical records for Cursor, so the sanitized
	// Codex stream is retargeted rather than duplicated as a new fixture.
	body := bytes.ReplaceAll(fixture, []byte("codex"), []byte("cursor"))
	if err := os.WriteFile(paths.CursorSpool, body, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := local.OpenWithOptions(filepath.Join(root, "belay.sqlite"), local.OpenOptions{
		KeyProvider: &memoryKeyProvider{keys: make(map[string][]byte)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	results, err := ImportLive(context.Background(), paths, store, Config{
		Version:             ConfigVersion,
		InstallationID:      "inst_cursor_live_test",
		NumbatVersionMarker: "numbat-test",
	})
	if err != nil {
		t.Fatalf("ImportLive() error = %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("ImportLive() result count = %d, want 4", len(results))
	}
	if results[2].Import.EventsAccepted == 0 {
		t.Fatalf("cursor live result = %+v, want accepted events", results[2])
	}
	if _, err := os.Stat(filepath.Join(root, "live", "cursor.cursor.json")); err != nil {
		t.Fatalf("cursor live checkpoint missing: %v", err)
	}
}

// Antigravity has no readable transcript, so the live hook spool is its only
// evidence path; ImportLive must read live/antigravity.ndjson.
func TestImportLiveImportsTheAntigravitySpool(t *testing.T) {
	root := t.TempDir()
	paths := hookRuntimePaths(root)
	if err := os.MkdirAll(filepath.Join(root, "live"), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join(
		"..", "..", "testdata", "numbat", "v0.3.0", "live", "codex-sanitized.ndjson",
	))
	if err != nil {
		t.Fatal(err)
	}
	// Numbat emits the same canonical hook records for Antigravity, so the
	// sanitized Codex stream is retargeted rather than duplicated.
	body := bytes.ReplaceAll(fixture, []byte("codex"), []byte("antigravity"))
	if err := os.WriteFile(paths.AntigravitySpool, body, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := local.OpenWithOptions(filepath.Join(root, "belay.sqlite"), local.OpenOptions{
		KeyProvider: &memoryKeyProvider{keys: make(map[string][]byte)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	results, err := ImportLive(context.Background(), paths, store, Config{
		Version:             ConfigVersion,
		InstallationID:      "inst_antigravity_live_test",
		NumbatVersionMarker: "numbat-test",
	})
	if err != nil {
		t.Fatalf("ImportLive() error = %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("ImportLive() result count = %d, want 4", len(results))
	}
	if results[3].Import.EventsAccepted == 0 {
		t.Fatalf("antigravity live result = %+v, want accepted events", results[3])
	}
	for _, other := range results[:3] {
		if other.Import.EventsAccepted != 0 {
			t.Fatalf("non-antigravity result = %+v, want no accepted events", other)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "live", "antigravity.cursor.json")); err != nil {
		t.Fatalf("antigravity live checkpoint missing: %v", err)
	}
}

func TestManageHooksInstallSkipsAbsentHarnesses(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "calls")
	client := newHookRuntimeClient(t, logPath,
		`[{"agent":"codex","present":true,"detected":false},`+
			`{"agent":"Claude Code","present":false,"detected":false},`+
			`{"agent":"cursor","present":false,"detected":false},`+
			`{"agent":"Antigravity","present":false,"detected":false}]`,
		"exit 0",
	)
	paths := hookRuntimePaths(root)

	results, err := ManageHooks(context.Background(), client, paths, "install")
	if err != nil {
		t.Fatalf("ManageHooks() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("ManageHooks() results = %+v, want none", results)
	}
	if got, want := readHookRuntimeLog(t, logPath), "<agents><--format><json>\n"; got != want {
		t.Fatalf("Numbat calls = %q, want %q", got, want)
	}
	for _, spool := range []string{
		paths.CodexSpool, paths.ClaudeSpool, paths.CursorSpool, paths.AntigravitySpool,
	} {
		if _, err := os.Stat(spool); !os.IsNotExist(err) {
			t.Fatalf("absent harness spool %q exists or stat failed: %v", spool, err)
		}
	}
}

func TestManageHooksInstallOnlyDetectedHarness(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "calls")
	client := newHookRuntimeClient(t, logPath,
		`[{"agent":"codex","present":true,"detected":true},`+
			`{"agent":"Claude Code","present":true,"detected":false},`+
			`{"agent":"cursor","present":true,"detected":false},`+
			`{"agent":"Antigravity","present":true,"detected":false}]`,
		"exit 0",
	)
	paths := hookRuntimePaths(root)

	results, err := ManageHooks(context.Background(), client, paths, "install")
	if err != nil {
		t.Fatalf("ManageHooks() error = %v", err)
	}
	if len(results) != 1 || results[0].Agent != "codex" || results[0].Error != "" || results[0].ExitCode != 0 {
		t.Fatalf("ManageHooks() results = %+v, want one successful Codex install", results)
	}
	wantCalls := strings.Join([]string{
		"<agents><--format><json>",
		"<hook><install><--agent><codex><--emit><all><--output><file><--output-file><" + paths.CodexSpool + ">",
		"",
	}, "\n")
	if got := readHookRuntimeLog(t, logPath); got != wantCalls {
		t.Fatalf("Numbat calls:\n%s\nwant:\n%s", got, wantCalls)
	}
	if _, err := os.Stat(paths.CodexSpool); err != nil {
		t.Fatalf("Codex spool was not prepared: %v", err)
	}
	for _, spool := range []string{paths.ClaudeSpool, paths.CursorSpool, paths.AntigravitySpool} {
		if _, err := os.Stat(spool); !os.IsNotExist(err) {
			t.Fatalf("undetected harness spool %q exists or stat failed: %v", spool, err)
		}
	}
}

func TestManageHooksInstallsDetectedCursorHook(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "calls")
	client := newHookRuntimeClient(t, logPath,
		`[{"agent":"codex","present":false,"detected":false},`+
			`{"agent":"Claude Code","present":false,"detected":false},`+
			`{"agent":"cursor","present":true,"detected":true},`+
			`{"agent":"Antigravity","present":false,"detected":false}]`,
		"exit 0",
	)
	paths := hookRuntimePaths(root)

	results, err := ManageHooks(context.Background(), client, paths, "install")
	if err != nil {
		t.Fatalf("ManageHooks() error = %v", err)
	}
	if len(results) != 1 || results[0].Agent != "cursor" ||
		results[0].Error != "" || results[0].ExitCode != 0 {
		t.Fatalf("ManageHooks() results = %+v, want one successful Cursor install", results)
	}
	wantCalls := strings.Join([]string{
		"<agents><--format><json>",
		"<hook><install><--agent><cursor><--emit><all><--output><file><--output-file><" +
			paths.CursorSpool + ">",
		"",
	}, "\n")
	if got := readHookRuntimeLog(t, logPath); got != wantCalls {
		t.Fatalf("Numbat calls:\n%s\nwant:\n%s", got, wantCalls)
	}
	if _, err := os.Stat(paths.CursorSpool); err != nil {
		t.Fatalf("Cursor spool was not prepared: %v", err)
	}
}

// Antigravity is hook-only, so a detected install must receive exactly the
// same monitor hook install as every other harness, writing to its own spool.
func TestManageHooksInstallsDetectedAntigravityHook(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "calls")
	client := newHookRuntimeClient(t, logPath,
		`[{"agent":"codex","present":false,"detected":false},`+
			`{"agent":"Claude Code","present":false,"detected":false},`+
			`{"agent":"cursor","present":false,"detected":false},`+
			`{"agent":"Antigravity","present":true,"detected":true,`+
			`"at_rest":"n/a (hook-only; deferred JSONL transcript format)",`+
			`"hook":"hooks","wired":"no",`+
			`"setup_hint":"install: numbat hook install --agent antigravity"}]`,
		"exit 0",
	)
	paths := hookRuntimePaths(root)

	results, err := ManageHooks(context.Background(), client, paths, "install")
	if err != nil {
		t.Fatalf("ManageHooks() error = %v", err)
	}
	if len(results) != 1 || results[0].Agent != "antigravity" ||
		results[0].Error != "" || results[0].ExitCode != 0 {
		t.Fatalf("ManageHooks() results = %+v, want one successful Antigravity install", results)
	}
	wantCalls := strings.Join([]string{
		"<agents><--format><json>",
		"<hook><install><--agent><antigravity><--emit><all><--output><file><--output-file><" +
			paths.AntigravitySpool + ">",
		"",
	}, "\n")
	if got := readHookRuntimeLog(t, logPath); got != wantCalls {
		t.Fatalf("Numbat calls:\n%s\nwant:\n%s", got, wantCalls)
	}
	if _, err := os.Stat(paths.AntigravitySpool); err != nil {
		t.Fatalf("Antigravity spool was not prepared: %v", err)
	}
	for _, spool := range []string{paths.CodexSpool, paths.ClaudeSpool, paths.CursorSpool} {
		if _, err := os.Stat(spool); !os.IsNotExist(err) {
			t.Fatalf("undetected harness spool %q exists or stat failed: %v", spool, err)
		}
	}
}

func TestManageHooksInstallReturnsPartialFailure(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "calls")
	client := newHookRuntimeClient(t, logPath,
		`[{"agent":"codex","present":true,"detected":true},`+
			`{"agent":"Claude Code","present":true,"detected":true},`+
			`{"agent":"cursor","present":false,"detected":false},`+
			`{"agent":"Antigravity","present":false,"detected":false}]`,
		`if [ "$1" = "hook" ] && [ "$4" = "claude" ]; then
	printf '%s\n' 'token=do-not-report' >&2
	exit 9
fi
exit 0`,
	)
	paths := hookRuntimePaths(root)

	results, err := ManageHooks(context.Background(), client, paths, "install")
	if err == nil {
		t.Fatal("ManageHooks() error = nil, want partial failure")
	}
	if len(results) != 2 {
		t.Fatalf("ManageHooks() result count = %d, want 2", len(results))
	}
	if results[0].Agent != "codex" || results[0].Error != "" || results[0].ExitCode != 0 {
		t.Fatalf("Codex result = %+v, want success", results[0])
	}
	if results[1].Agent != "claude" || results[1].Error != "hook operation failed" || results[1].ExitCode != 9 {
		t.Fatalf("Claude result = %+v, want generic failure", results[1])
	}
	if strings.Contains(err.Error(), "do-not-report") {
		t.Fatalf("ManageHooks() error leaked command stderr: %v", err)
	}
}

func TestManageHooksStatusAndUninstallDoNotRequireDetection(t *testing.T) {
	for _, action := range []string{"status", "uninstall"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			logPath := filepath.Join(root, "calls")
			client := newHookRuntimeClient(t, logPath, `[]`, "exit 0")
			paths := hookRuntimePaths(root)

			results, err := ManageHooks(context.Background(), client, paths, action)
			if err != nil {
				t.Fatalf("ManageHooks() error = %v", err)
			}
			if len(results) != 4 {
				t.Fatalf("ManageHooks() result count = %d, want 4", len(results))
			}
			calls := readHookRuntimeLog(t, logPath)
			if strings.Contains(calls, "<agents>") {
				t.Fatalf("ManageHooks(%q) unexpectedly discovered inventory: %s", action, calls)
			}
			for _, agent := range []string{"codex", "claude", "cursor", "antigravity"} {
				want := "<hook><" + action + "><--agent><" + agent + ">"
				if !strings.Contains(calls, want) {
					t.Errorf("ManageHooks(%q) calls missing %q: %s", action, want, calls)
				}
			}
		})
	}
}

func hookRuntimePaths(root string) Paths {
	return Paths{
		Root:        root,
		CodexSpool:  filepath.Join(root, "live", "codex.ndjson"),
		ClaudeSpool: filepath.Join(root, "live", "claude.ndjson"),
		CursorSpool: filepath.Join(root, "live", "cursor.ndjson"),
		AntigravitySpool: filepath.Join(
			root, "live", "antigravity.ndjson",
		),
	}
}

func newHookRuntimeClient(t *testing.T, logPath, inventory, hookBody string) *numbat.Client {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "fake-numbat")
	script := fmt.Sprintf(`#!/bin/sh
{
	for arg in "$@"; do
		printf '<%%s>' "$arg"
	done
	printf '\n'
} >> %q
if [ "$1" = "agents" ]; then
	printf '%%s\n' %q
	exit 0
fi
%s
`, logPath, inventory, hookBody)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := numbat.NewClient(binary)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func readHookRuntimeLog(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
