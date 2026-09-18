//go:build darwin || linux

package localapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunInstalledSemanticHarnessCancellationKillsProcessGroup(
	t *testing.T,
) {
	harnessDir := t.TempDir()
	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	writeSemanticHarness(t, harnessDir, "claude", `
sh -c '
	trap "" HUP TERM
	printf "%s\n" "$$" > "$1"
	while :; do sleep 60; done
' child "$BELAY_TEST_CHILD_PID_PATH" &
wait "$!"
`)
	t.Setenv("PATH", harnessDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BELAY_TEST_CHILD_PID_PATH", childPIDPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := RunInstalledSemanticHarness(
			ctx,
			SemanticHarnessClaude,
			[]byte("bounded prompt"),
			[]byte(`{"type":"object"}`),
		)
		result <- err
	}()

	childPID := waitForSemanticChildPID(t, childPIDPath)
	defer syscall.Kill(childPID, syscall.SIGKILL)
	cancelledAt := time.Now()
	cancel()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled semantic harness returned no error")
		}
		if !errors.Is(err, context.Canceled) &&
			!strings.Contains(err.Error(), "signal: killed") {
			t.Fatalf("cancellation error = %v", err)
		}
		if elapsed := time.Since(cancelledAt); elapsed >
			semanticHarnessWaitDelay+time.Second {
			t.Fatalf("cancellation took %s", elapsed)
		}
	case <-time.After(semanticHarnessWaitDelay + 2*time.Second):
		t.Fatal("semantic harness cancellation did not return promptly")
	}

	deadline := time.Now().Add(2 * time.Second)
	for semanticProcessExists(childPID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if semanticProcessExists(childPID) {
		t.Fatalf("semantic harness child %d survived cancellation", childPID)
	}
}

func waitForSemanticChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(body)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("semantic harness child did not start")
	return 0
}

func semanticProcessExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
