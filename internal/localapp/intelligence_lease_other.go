//go:build !darwin && !linux

package localapp

import (
	"errors"
	"os"
	"sync"
)

type directoryIntelligenceLease struct {
	path string
	once sync.Once
	err  error
}

func tryAcquireIntelligenceLease(
	path string,
) (intelligenceLease, bool, error) {
	lockDirectory := path + ".d"
	err := os.Mkdir(lockDirectory, 0o700)
	if errors.Is(err, os.ErrExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &directoryIntelligenceLease{path: lockDirectory}, true, nil
}

func (lease *directoryIntelligenceLease) Release() error {
	if lease == nil {
		return nil
	}
	lease.once.Do(func() {
		lease.err = os.Remove(lease.path)
	})
	return lease.err
}
