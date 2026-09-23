//go:build windows

package local

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
)

func TestWindowsKeyProviderRoundTripsThroughDPAPI(t *testing.T) {
	database := filepath.Join(t.TempDir(), "belay.sqlite")
	provider := newPlatformKeyProvider("windows", database)
	if _, ok := provider.(*FileKeyProvider); !ok {
		t.Fatalf("windows provider = %T, want *FileKeyProvider", provider)
	}
	created, err := provider.Create(context.Background(), testKeyStoreID)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := provider.Load(context.Background(), testKeyStoreID)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 32 || !bytes.Equal(created, loaded) {
		t.Fatalf("DPAPI round trip mismatch: %x vs %x", created, loaded)
	}
}
