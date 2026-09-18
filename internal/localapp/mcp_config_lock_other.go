//go:build !darwin && !linux

package localapp

import (
	"context"
	"os"
)

var fallbackMCPConfigLock = func() chan struct{} {
	lock := make(chan struct{}, 1)
	lock <- struct{}{}
	return lock
}()

func lockMCPConfigFile(ctx context.Context, _ *os.File) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-fallbackMCPConfigLock:
		return nil
	}
}

func unlockMCPConfigFile(_ *os.File) {
	fallbackMCPConfigLock <- struct{}{}
}
