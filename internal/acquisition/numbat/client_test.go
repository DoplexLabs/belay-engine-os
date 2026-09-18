package numbat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewClientRequiresExecutable(t *testing.T) {
	if _, err := NewClient(""); err == nil {
		t.Fatal("NewClient() error = nil, want executable validation")
	}
}

func TestCommandTimeoutAndCrash(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		client := newFakeClient(t, `
if [ "$1" = "hook" ] && [ "$2" = "status" ]; then
	exec sleep 5
fi
exit 0
`)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		result, err := client.MonitorHookStatus(ctx, AgentCodex)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("MonitorHookStatus() error = %v, want deadline exceeded", err)
		}
		if result.ExitCode == 0 {
			t.Fatalf("MonitorHookStatus() exit code = %d, want failure", result.ExitCode)
		}
	})

	t.Run("crash", func(t *testing.T) {
		client := newFakeClient(t, `
if [ "$1" = "hook" ] && [ "$2" = "status" ]; then
	kill -TERM $$
fi
exit 0
`)
		result, err := client.MonitorHookStatus(context.Background(), AgentClaude)
		if err == nil {
			t.Fatal("MonitorHookStatus() error = nil, want crash error")
		}
		if result.ExitCode != -1 {
			t.Fatalf("MonitorHookStatus() exit code = %d, want -1", result.ExitCode)
		}
	})
}

func TestStderrIsBoundedAndSanitized(t *testing.T) {
	client := newFakeClient(t, `
printf 'token=super-secret https://example.test/path?credential=yes /Users/private-user/project\n' >&2
i=0
while [ "$i" -lt 20000 ]; do
	printf 'x' >&2
	i=$((i + 1))
done
exit 7
`)
	result, err := client.MonitorHookStatus(context.Background(), AgentCodex)
	if err == nil {
		t.Fatal("MonitorHookStatus() error = nil, want exit error")
	}
	for _, prohibited := range []string{"super-secret", "credential=yes", "private-user"} {
		if strings.Contains(result.Stderr, prohibited) {
			t.Errorf("sanitized stderr contains %q: %q", prohibited, result.Stderr)
		}
	}
	if !strings.Contains(result.Stderr, "[truncated]") {
		t.Fatalf("sanitized stderr missing truncation marker: %q", result.Stderr)
	}
	if len(result.Stderr) > maxStderrBytes+256 {
		t.Fatalf("sanitized stderr length = %d, want bounded output", len(result.Stderr))
	}
}

func newFakeClient(t *testing.T, body string) *Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-numbat")
	script := "#!/bin/sh\n" + body
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(path)
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := os.LookupEnv("NUMBAT_TEST_LOG"); ok {
		client.environment = append(client.environment, "NUMBAT_TEST_LOG="+value)
	}
	return client
}
