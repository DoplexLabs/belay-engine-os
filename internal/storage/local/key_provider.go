package local

import (
	"context"
	"errors"
)

var (
	ErrKeyNotFound      = errors.New("local data key not found")
	ErrKeyAlreadyExists = errors.New("local data key already exists")
)

// KeyProvider persists a 32-byte Local data key outside SQLite. Implementations
// must return ErrKeyNotFound when no key exists and must never include key
// material in returned errors.
type KeyProvider interface {
	Load(ctx context.Context, storeID string) ([]byte, error)
	Create(ctx context.Context, storeID string) ([]byte, error)
}
