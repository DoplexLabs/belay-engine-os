//go:build !darwin && !linux

package localapp

import (
	"errors"
	"os"
)

type fileIdentity struct {
	device uint64
	inode  uint64
}

func openRegularNoFollow(path string) (*os.File, fileIdentity, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fileIdentity{}, 0, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fileIdentity{}, 0, errors.New("path is not a regular file")
	}
	file, err := os.Open(path)
	return file, fileIdentity{}, info.Size(), err
}

func openOrCreatePrivateRegular(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, errors.New("path is not a regular file")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
}
