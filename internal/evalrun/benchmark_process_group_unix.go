//go:build darwin || linux

package evalrun

import (
	"os/exec"
	"syscall"
)

// configureBenchmarkProcessGroup runs the benchmark harness in its own process
// group so cancellation kills the whole tree, not only the direct child.
func configureBenchmarkProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
}
