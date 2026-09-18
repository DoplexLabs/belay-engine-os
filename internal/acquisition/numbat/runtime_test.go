package numbat

import (
	"context"
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
	if _, err := client.InstallMonitorHook(context.Background(), AgentCodex, codexSpool); err != nil {
		t.Fatal(err)
	}
	if _, err := client.InstallMonitorHook(context.Background(), AgentClaude, claudeSpool); err != nil {
		t.Fatal(err)
	}
	if _, err := client.MonitorHookStatus(context.Background(), AgentCodex); err != nil {
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
		"<hook><status><--agent><codex>",
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
