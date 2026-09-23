package numbat

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoricalScanStreamsStdoutAndUsesExactArgs(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "args")
	t.Setenv("NUMBAT_TEST_LOG", logPath)
	client := newFakeClient(t, `
printf '%s\n' "$@" > "$NUMBAT_TEST_LOG"
printf '%s\n' '{"record_type":"scan_summary"}'
`)
	scan, err := client.StartHistoricalScan(context.Background(), AgentClaude)
	if err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(scan.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if err := scan.Stdout.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := scan.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v; stderr = %q", err, result.Stderr)
	}
	if result.ExitCode != 0 {
		t.Fatalf("Wait() exit code = %d, want 0", result.ExitCode)
	}
	if got, want := string(output), "{\"record_type\":\"scan_summary\"}\n"; got != want {
		t.Fatalf("scan stdout = %q, want %q", got, want)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "scan\n--agent\nclaude\n--emit\nall\n--output\nstdout\n"
	if got := string(args); got != want {
		t.Fatalf("StartHistoricalScan() args = %q, want %q", got, want)
	}
}

func TestMonitorHookWrappersUseExactSafeArgsAndSeparateSpools(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "calls")
	t.Setenv("NUMBAT_TEST_LOG", logPath)
	client := newFakeClient(t, `
{
	for arg in "$@"; do
		printf '<%s>' "$arg"
	done
	printf '\n'
} >> "$NUMBAT_TEST_LOG"
`)
	codexSpool := filepath.Join(t.TempDir(), "codex.ndjson")
	claudeSpool := filepath.Join(t.TempDir(), "claude.ndjson")
	cursorSpool := filepath.Join(t.TempDir(), "cursor.ndjson")
	antigravitySpool := filepath.Join(t.TempDir(), "antigravity.ndjson")
	if _, err := client.InstallMonitorHook(context.Background(), AgentCodex, codexSpool); err != nil {
		t.Fatal(err)
	}
	if _, err := client.InstallMonitorHook(context.Background(), AgentClaude, claudeSpool); err != nil {
		t.Fatal(err)
	}
	if _, err := client.InstallMonitorHook(context.Background(), AgentCursor, cursorSpool); err != nil {
		t.Fatal(err)
	}
	if _, err := client.InstallMonitorHook(context.Background(), AgentAntigravity, antigravitySpool); err != nil {
		t.Fatal(err)
	}
	if _, err := client.MonitorHookStatus(context.Background(), AgentCodex); err != nil {
		t.Fatal(err)
	}
	if _, err := client.MonitorHookStatus(context.Background(), AgentCursor); err != nil {
		t.Fatal(err)
	}
	if _, err := client.MonitorHookStatus(context.Background(), AgentAntigravity); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UninstallMonitorHook(context.Background(), AgentClaude); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	want := strings.Join([]string{
		"<hook><install><--agent><codex><--emit><all><--output><file><--output-file><" + codexSpool + ">",
		"<hook><install><--agent><claude><--emit><all><--output><file><--output-file><" + claudeSpool + ">",
		"<hook><install><--agent><cursor><--emit><all><--output><file><--output-file><" + cursorSpool + ">",
		"<hook><install><--agent><antigravity><--emit><all><--output><file><--output-file><" + antigravitySpool + ">",
		"<hook><status><--agent><codex>",
		"<hook><status><--agent><cursor>",
		"<hook><status><--agent><antigravity>",
		"<hook><uninstall><--agent><claude>",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("hook calls:\n%s\nwant:\n%s", got, want)
	}
	for _, forbidden := range []string{"--enforce", "http", "--content", "--include-reasoning"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("hook arguments contain forbidden option %q: %s", forbidden, got)
		}
	}
}

func TestRuntimeRejectsUnsupportedAgentAndUnsafeSpoolShape(t *testing.T) {
	client := newFakeClient(t, "exit 0\n")
	if _, err := client.StartHistoricalScan(context.Background(), Agent(255)); err == nil {
		t.Fatal("StartHistoricalScan() error = nil, want unsupported-agent error")
	}
	if _, err := client.InstallMonitorHook(context.Background(), AgentCodex, "--enforce"); err == nil {
		t.Fatal("InstallMonitorHook() error = nil, want relative spool rejection")
	}
}

func TestSupportedAgentsCoverEveryLaunchTargetInReportOrder(t *testing.T) {
	want := []string{"codex", "claude", "cursor", "antigravity"}
	supported := SupportedAgents()
	if len(supported) != len(want) {
		t.Fatalf("SupportedAgents() = %v, want %v", supported, want)
	}
	for index, agent := range supported {
		if agent.String() != want[index] {
			t.Fatalf("SupportedAgents()[%d] = %q, want %q", index, agent.String(), want[index])
		}
		parsed, ok := parseAgent(want[index])
		if !ok || parsed != agent {
			t.Fatalf("parseAgent(%q) = %v/%v, want %v", want[index], parsed, ok, agent)
		}
	}
}

// TestHistoricalScanRefusesHookOnlyAgent pins that Belay never asks the pinned
// Numbat to scan Antigravity at rest: the pinned build rejects
// `scan --agent antigravity` because Antigravity is hook-only, and Belay must
// not spawn a process it knows will fail.
func TestHistoricalScanRefusesHookOnlyAgent(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "args")
	t.Setenv("NUMBAT_TEST_LOG", logPath)
	client := newFakeClient(t, `
printf '%s\n' "$@" > "$NUMBAT_TEST_LOG"
`)
	if AgentAntigravity.HistoricalScanSupported() {
		t.Fatal("AgentAntigravity.HistoricalScanSupported() = true, want false")
	}
	for _, agent := range []Agent{AgentCodex, AgentClaude, AgentCursor} {
		if !agent.HistoricalScanSupported() {
			t.Fatalf("%s.HistoricalScanSupported() = false, want true", agent)
		}
	}
	if _, err := client.StartHistoricalScan(context.Background(), AgentAntigravity); err == nil {
		t.Fatal("StartHistoricalScan(antigravity) error = nil, want hook-only rejection")
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("StartHistoricalScan(antigravity) launched Numbat; stat err = %v", err)
	}
}

func TestHistoricalScanUsesCursorAgentName(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "args")
	t.Setenv("NUMBAT_TEST_LOG", logPath)
	client := newFakeClient(t, `
printf '%s\n' "$@" > "$NUMBAT_TEST_LOG"
`)
	scan, err := client.StartHistoricalScan(context.Background(), AgentCursor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(scan.Stdout); err != nil {
		t.Fatal(err)
	}
	if err := scan.Stdout.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := scan.Wait(); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "scan\n--agent\ncursor\n--emit\nall\n--output\nstdout\n"
	if got := string(args); got != want {
		t.Fatalf("StartHistoricalScan(cursor) args = %q, want %q", got, want)
	}
}
