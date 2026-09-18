//go:build darwin || linux

package localapp

import (
	"errors"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

type fileIntelligenceLease struct {
	file *os.File
	once sync.Once
	err  error
}

func tryAcquireIntelligenceLease(
	path string,
) (intelligenceLease, bool, error) {
	fd, err := unix.Open(
		path,
		unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return nil, false, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, false, errors.New("open intelligence lease")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, false, errors.New("intelligence lease must be a regular file")
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, false, err
	}
	err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == unix.EWOULDBLOCK || err == unix.EAGAIN {
		_ = file.Close()
		return nil, false, nil
	}
	if err != nil {
		_ = file.Close()
		return nil, false, err
	}
	return &fileIntelligenceLease{file: file}, true, nil
}

func (lease *fileIntelligenceLease) Release() error {
	if lease == nil {
		return nil
	}
	lease.once.Do(func() {
		lease.err = errors.Join(
			unix.Flock(int(lease.file.Fd()), unix.LOCK_UN),
			lease.file.Close(),
		)
	})
	return lease.err
}
