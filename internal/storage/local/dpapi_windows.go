//go:build windows

package local

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// dpapiProtector wraps keys with the Windows Data Protection API in user
// scope. Only the same Windows account can unwrap the blob, and the call never
// shows UI, so it behaves like the noninteractive macOS Keychain path.
type dpapiProtector struct{}

func (dpapiProtector) Protect(key, entropy []byte) ([]byte, error) {
	return dpapiCall(key, entropy, func(in, entropyBlob *windows.DataBlob, out *windows.DataBlob) error {
		return windows.CryptProtectData(
			in,
			nil,
			entropyBlob,
			0,
			nil,
			windows.CRYPTPROTECT_UI_FORBIDDEN,
			out,
		)
	})
}

func (dpapiProtector) Unprotect(blob, entropy []byte) ([]byte, error) {
	return dpapiCall(blob, entropy, func(in, entropyBlob *windows.DataBlob, out *windows.DataBlob) error {
		return windows.CryptUnprotectData(
			in,
			nil,
			entropyBlob,
			0,
			nil,
			windows.CRYPTPROTECT_UI_FORBIDDEN,
			out,
		)
	})
}

func dpapiCall(
	input, entropy []byte,
	call func(in, entropyBlob *windows.DataBlob, out *windows.DataBlob) error,
) ([]byte, error) {
	if len(input) == 0 {
		return nil, errors.New("empty DPAPI input")
	}
	in := windows.DataBlob{Size: uint32(len(input)), Data: &input[0]}
	var entropyBlob *windows.DataBlob
	if len(entropy) > 0 {
		entropyBlob = &windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	}
	var out windows.DataBlob
	if err := call(&in, entropyBlob, &out); err != nil {
		return nil, err
	}
	if out.Data == nil {
		return nil, errors.New("DPAPI returned no data")
	}
	defer func() {
		_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	}()
	result := make([]byte, out.Size)
	copy(result, unsafe.Slice(out.Data, out.Size))
	// Clear the DPAPI-owned buffer before releasing it so plaintext key bytes
	// do not linger in freed process memory.
	zeroBytes(unsafe.Slice(out.Data, out.Size))
	return result, nil
}

func newWindowsKeyProvider(directory string) (KeyProvider, error) {
	return newFileKeyProvider(directory, dpapiProtector{}), nil
}
