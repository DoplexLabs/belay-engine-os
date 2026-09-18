//go:build !darwin && !linux

package localapp

import "os/exec"

func configureSemanticHarnessCommand(command *exec.Cmd) {
	command.WaitDelay = semanticHarnessWaitDelay
}
