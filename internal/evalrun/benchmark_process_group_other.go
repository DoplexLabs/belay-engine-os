//go:build !darwin && !linux

package evalrun

import "os/exec"

// configureBenchmarkProcessGroup has no process-group support on this platform.
// Cancellation terminates only the direct harness process.
func configureBenchmarkProcessGroup(*exec.Cmd) {}
