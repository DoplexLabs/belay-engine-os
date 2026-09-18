//go:build darwin || linux

package localapp

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type fileIdentity struct {
	device uint64
	inode  uint64
}

func openRegularNoFollow(path string) (*os.File, fileIdentity, int64, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fileIdentity{}, 0, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return nil, fileIdentity{}, 0, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, fileIdentity{}, 0, errors.New("path is not a regular file")
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		unix.Close(fd)
		return nil, fileIdentity{}, 0, errors.New("open regular file")
	}
	return file, fileIdentity{device: uint64(stat.Dev), inode: stat.Ino}, stat.Size, nil
}

func openOrCreatePrivateRegular(path string) (*os.File, error) {
	fd, err := unix.Open(
		path,
		unix.O_WRONLY|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return nil, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, errors.New("path is not a regular file")
	}
	if err := unix.Fchmod(fd, 0o600); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("restrict file: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		unix.Close(fd)
		return nil, errors.New("open private file")
	}
	return file, nil
}
