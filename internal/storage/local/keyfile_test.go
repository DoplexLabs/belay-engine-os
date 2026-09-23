package local

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// xorProtector is a deterministic stand-in for DPAPI so the file lifecycle can
// be tested on every platform. It is never used outside tests.
type xorProtector struct {
	failProtect   bool
	failUnprotect bool
}

func (p xorProtector) Protect(key, entropy []byte) ([]byte, error) {
	if p.failProtect {
		return nil, errors.New("protect failed")
	}
	return xorWithEntropy(key, entropy), nil
}

func (p xorProtector) Unprotect(blob, entropy []byte) ([]byte, error) {
	if p.failUnprotect {
		return nil, errors.New("unprotect failed")
	}
	return xorWithEntropy(blob, entropy), nil
}

func xorWithEntropy(input, entropy []byte) []byte {
	mask := sha256.Sum256(entropy)
	result := make([]byte, len(input))
	for index, value := range input {
		result[index] = value ^ mask[index%len(mask)]
	}
	return result
}

const testKeyStoreID = "store_0123456789abcdef0123456789abcdef"

func TestFileKeyProviderCreateThenLoadRoundTrips(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "keys")
	provider := newFileKeyProvider(directory, xorProtector{})
	provider.random = bytes.NewReader(bytes.Repeat([]byte{0x5a}, 32))

	created, err := provider.Create(context.Background(), testKeyStoreID)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 32 || !bytes.Equal(created, bytes.Repeat([]byte{0x5a}, 32)) {
		t.Fatalf("created key = %x", created)
	}
	loaded, err := provider.Load(context.Background(), testKeyStoreID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded, created) {
		t.Fatalf("loaded key %x != created %x", loaded, created)
	}

	body, err := os.ReadFile(filepath.Join(directory, testKeyStoreID+".key"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(body, []byte(keyFileMagic)) {
		t.Fatalf("key file lacks magic prefix: %q", body)
	}
	if bytes.Contains(body, created) {
		t.Fatal("key file stores the plaintext key")
	}
}

func TestFileKeyProviderLoadReportsMissingKey(t *testing.T) {
	provider := newFileKeyProvider(filepath.Join(t.TempDir(), "keys"), xorProtector{})
	_, err := provider.Load(context.Background(), testKeyStoreID)
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Load error = %v, want ErrKeyNotFound", err)
	}
}

func TestFileKeyProviderCreateRefusesExistingKey(t *testing.T) {
	provider := newFileKeyProvider(filepath.Join(t.TempDir(), "keys"), xorProtector{})
	if _, err := provider.Create(context.Background(), testKeyStoreID); err != nil {
		t.Fatal(err)
	}
	_, err := provider.Create(context.Background(), testKeyStoreID)
	if !errors.Is(err, ErrKeyAlreadyExists) {
		t.Fatalf("second Create error = %v, want ErrKeyAlreadyExists", err)
	}
}

func TestFileKeyProviderRejectsInvalidStoreID(t *testing.T) {
	provider := newFileKeyProvider(filepath.Join(t.TempDir(), "keys"), xorProtector{})
	for _, storeID := range []string{"", "store_short", "../escape", "store_" + string(bytes.Repeat([]byte("g"), 32))} {
		if _, err := provider.Create(context.Background(), storeID); err == nil {
			t.Fatalf("Create(%q) succeeded", storeID)
		}
		if _, err := provider.Load(context.Background(), storeID); err == nil ||
			errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("Load(%q) error = %v, want format rejection", storeID, err)
		}
	}
}

func TestFileKeyProviderRejectsForeignOrCorruptFiles(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "keys")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, testKeyStoreID+".key")
	provider := newFileKeyProvider(directory, xorProtector{})

	if err := os.WriteFile(path, []byte("not a belay key file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Load(context.Background(), testKeyStoreID); err == nil ||
		errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("foreign file Load error = %v", err)
	}

	short := append([]byte(keyFileMagic), 1, 2, 3)
	if err := os.WriteFile(path, short, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Load(context.Background(), testKeyStoreID); err == nil {
		t.Fatal("short blob Load succeeded")
	}

	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), keyFileMaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Load(context.Background(), testKeyStoreID); err == nil {
		t.Fatal("oversized file Load succeeded")
	}
}

func TestFileKeyProviderBindsBlobToStoreID(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "keys")
	provider := newFileKeyProvider(directory, xorProtector{})
	created, err := provider.Create(context.Background(), testKeyStoreID)
	if err != nil {
		t.Fatal(err)
	}
	otherStoreID := "store_fedcba9876543210fedcba9876543210"
	source := filepath.Join(directory, testKeyStoreID+".key")
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, otherStoreID+".key"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := provider.Load(context.Background(), otherStoreID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(loaded, created) {
		t.Fatal("blob copied between stores unwrapped to the same key")
	}
}

func TestFileKeyProviderSurfacesProtectorFailuresWithoutKeyMaterial(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "keys")
	failing := newFileKeyProvider(directory, xorProtector{failProtect: true})
	if _, err := failing.Create(context.Background(), testKeyStoreID); err == nil {
		t.Fatal("Create with failing protector succeeded")
	}
	if _, err := os.Stat(filepath.Join(directory, testKeyStoreID+".key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed Create left a key file: %v", err)
	}

	working := newFileKeyProvider(directory, xorProtector{})
	if _, err := working.Create(context.Background(), testKeyStoreID); err != nil {
		t.Fatal(err)
	}
	broken := newFileKeyProvider(directory, xorProtector{failUnprotect: true})
	_, err := broken.Load(context.Background(), testKeyStoreID)
	if err == nil || errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Load with failing unprotect error = %v", err)
	}
}

func TestNewPlatformKeyProviderSelectsByPlatform(t *testing.T) {
	database := filepath.Join(t.TempDir(), "belay.sqlite")
	if _, ok := newPlatformKeyProvider("darwin", database).(*MacOSKeychainProvider); !ok {
		t.Fatal("darwin did not select the macOS Keychain provider")
	}
	linux := newPlatformKeyProvider("linux", database)
	if _, err := linux.Load(context.Background(), testKeyStoreID); err == nil ||
		errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("linux provider Load error = %v, want unsupported", err)
	}
	if _, err := linux.Create(context.Background(), testKeyStoreID); err == nil {
		t.Fatal("linux provider Create succeeded")
	}
}
