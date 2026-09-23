//go:build !windows

package local

import "errors"

func newWindowsKeyProvider(string) (KeyProvider, error) {
	return nil, errors.New("Windows data protection is unavailable on this platform")
}
