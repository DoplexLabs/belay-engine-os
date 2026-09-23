package local

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
)

// KeyDirectoryName is the private directory beside a Local database that holds
// platform-wrapped data keys on platforms without a system keychain.
const KeyDirectoryName = "keys"

// NewPlatformKeyProvider selects the data-key store for the running platform:
// macOS Keychain on darwin, DPAPI-wrapped key files beside the database on
// Windows, and a provider that refuses to open encrypted stores elsewhere.
func NewPlatformKeyProvider(databasePath string) KeyProvider {
	return newPlatformKeyProvider(runtime.GOOS, databasePath)
}

func newPlatformKeyProvider(goos, databasePath string) KeyProvider {
	switch goos {
	case "darwin":
		return NewMacOSKeychainProvider()
	case "windows":
		provider, err := newWindowsKeyProvider(
			filepath.Join(filepath.Dir(databasePath), KeyDirectoryName),
		)
		if err != nil {
			return unsupportedKeyProvider{err: err}
		}
		return provider
	default:
		return unsupportedKeyProvider{
			err: errors.New("no supported local data key store on " + goos),
		}
	}
}

type unsupportedKeyProvider struct {
	err error
}

func (p unsupportedKeyProvider) Load(context.Context, string) ([]byte, error) {
	return nil, p.err
}

func (p unsupportedKeyProvider) Create(context.Context, string) ([]byte, error) {
	return nil, p.err
}
