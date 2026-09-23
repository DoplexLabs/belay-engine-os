package local

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// keyFileMagic prefixes every protected key file so a foreign or truncated file
// is rejected before it reaches the platform unprotect call.
const keyFileMagic = "belay-local-data-key.v1\n"
const keyFileMaxBytes = 16 << 10

// keyProtector wraps a 32-byte key with a platform user-scoped secret. The
// entropy binds the wrapped blob to one store so a blob copied between stores
// cannot be unwrapped. Implementations must not return key material in errors.
type keyProtector interface {
	Protect(key, entropy []byte) ([]byte, error)
	Unprotect(blob, entropy []byte) ([]byte, error)
}

// FileKeyProvider stores one wrapped Local data key per persisted store ID in a
// private directory. It is the Windows counterpart of the macOS Keychain
// provider: the wrapping secret never lives beside the database in plaintext,
// and the file itself is only usable by the Windows account that created it.
type FileKeyProvider struct {
	directory string
	protector keyProtector
	random    io.Reader
}

func newFileKeyProvider(directory string, protector keyProtector) *FileKeyProvider {
	return &FileKeyProvider{
		directory: directory,
		protector: protector,
		random:    rand.Reader,
	}
}

func (p *FileKeyProvider) Load(ctx context.Context, storeID string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validPersistedStoreID(storeID) {
		return nil, errors.New("local store ID has invalid format")
	}
	path := p.keyPath(storeID)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, errors.New("inspect local data key file")
	}
	if !info.Mode().IsRegular() || info.Size() > keyFileMaxBytes {
		return nil, errors.New("local data key file is not a private regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read local data key file")
	}
	if !strings.HasPrefix(string(body), keyFileMagic) {
		return nil, errors.New("local data key file has an unknown format")
	}
	blob := body[len(keyFileMagic):]
	key, err := p.protector.Unprotect(blob, keyEntropy(storeID))
	if err != nil {
		return nil, errors.New("unwrap local data key with the platform secret")
	}
	if len(key) != 32 {
		zeroBytes(key)
		return nil, errors.New("decode local data key file")
	}
	return key, nil
}

func (p *FileKeyProvider) Create(ctx context.Context, storeID string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validPersistedStoreID(storeID) {
		return nil, errors.New("local store ID has invalid format")
	}
	if err := os.MkdirAll(p.directory, 0o700); err != nil {
		return nil, errors.New("create local data key directory")
	}
	if info, err := os.Lstat(p.directory); err != nil ||
		!info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("local data key directory must be a real directory")
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(p.random, key); err != nil {
		zeroBytes(key)
		return nil, errors.New("generate local data key")
	}
	blob, err := p.protector.Protect(key, keyEntropy(storeID))
	if err != nil {
		zeroBytes(key)
		return nil, errors.New("wrap local data key with the platform secret")
	}
	path := p.keyPath(storeID)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		zeroBytes(key)
		return nil, ErrKeyAlreadyExists
	}
	if err != nil {
		zeroBytes(key)
		return nil, errors.New("create local data key file")
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write([]byte(keyFileMagic)); err != nil {
		zeroBytes(key)
		return nil, errors.New("write local data key file")
	}
	if _, err := file.Write(blob); err != nil {
		zeroBytes(key)
		return nil, errors.New("write local data key file")
	}
	if err := file.Sync(); err != nil {
		zeroBytes(key)
		return nil, errors.New("sync local data key file")
	}
	if err := file.Close(); err != nil {
		zeroBytes(key)
		return nil, errors.New("close local data key file")
	}
	cleanup = false
	return key, nil
}

func (p *FileKeyProvider) keyPath(storeID string) string {
	return filepath.Join(p.directory, storeID+".key")
}

func keyEntropy(storeID string) []byte {
	return []byte(keychainService + ":" + storeID)
}
