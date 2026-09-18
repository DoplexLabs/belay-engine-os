package local

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

const testStoreID = "store_0123456789abcdef0123456789abcdef"

func TestMacOSKeychainProviderLoadAndCreateWithoutRealKeychain(t *testing.T) {
	key := bytes.Repeat([]byte{0x7b}, 32)
	runner := &recordingSecurityRunner{
		results: []securityCommandResult{
			{stdout: []byte(base64.RawStdEncoding.EncodeToString(key) + "\n")},
			{},
		},
	}
	provider := &MacOSKeychainProvider{
		runner: runner,
		random: bytes.NewReader(key),
		goos:   "darwin",
	}

	loaded, err := provider.Load(context.Background(), testStoreID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !bytes.Equal(loaded, key) {
		t.Fatalf("Load() returned unexpected key")
	}
	created, err := provider.Create(context.Background(), testStoreID)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !bytes.Equal(created, key) {
		t.Fatalf("Create() returned unexpected key")
	}
	if len(runner.calls) != 2 {
		t.Fatalf("security calls = %d, want 2", len(runner.calls))
	}
	if !slices.Equal(runner.calls[0].args, []string{
		"find-generic-password",
		"-a", testStoreID,
		"-s", keychainService,
		"-w",
	}) {
		t.Fatalf("Load() arguments = %q", runner.calls[0].args)
	}
	if !slices.Equal(runner.calls[1].args, []string{"-q", "-i"}) {
		t.Fatalf("Create() arguments = %q, want interactive command-input mode", runner.calls[1].args)
	}
	encoded := base64.RawStdEncoding.EncodeToString(key)
	for _, argument := range runner.calls[1].args {
		if strings.Contains(argument, encoded) {
			t.Fatal("Create() exposed key material in process arguments")
		}
	}
	wantStdin := "add-generic-password -a " + testStoreID +
		" -s " + keychainService +
		" -w " + encoded + "\n"
	if runner.calls[1].stdin != wantStdin {
		t.Fatalf("Create() stdin = %q, want one complete command", runner.calls[1].stdin)
	}
	if strings.Count(runner.calls[1].stdin, "\n") != 1 ||
		strings.HasSuffix(strings.TrimSuffix(runner.calls[1].stdin, "\n"), " -w") {
		t.Fatalf("Create() stdin used prompt-only -w behavior: %q", runner.calls[1].stdin)
	}
}

func TestMacOSKeychainProviderErrorsArePayloadFree(t *testing.T) {
	const secretCanary = "KEYCHAIN_ERROR_SECRET_CANARY"
	key := bytes.Repeat([]byte{0x41}, 32)
	encodedKey := base64.RawStdEncoding.EncodeToString(key)
	provider := &MacOSKeychainProvider{
		runner: &recordingSecurityRunner{
			results: []securityCommandResult{
				{stdout: []byte(secretCanary), exitCode: 44, err: errors.New(secretCanary)},
				{stdout: []byte(secretCanary), exitCode: 1, err: errors.New(secretCanary)},
				{stdout: []byte(secretCanary), exitCode: 1, err: errors.New(secretCanary)},
			},
		},
		random: bytes.NewReader(key),
		goos:   "darwin",
	}
	if _, err := provider.Load(context.Background(), testStoreID); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Load() error = %v, want ErrKeyNotFound", err)
	}
	if _, err := provider.Load(context.Background(), testStoreID); err == nil {
		t.Fatal("Load() generic error unexpectedly succeeded")
	} else if strings.Contains(err.Error(), secretCanary) {
		t.Fatalf("Load() leaked command output in error: %v", err)
	}
	if _, err := provider.Create(context.Background(), testStoreID); err == nil {
		t.Fatal("Create() unexpectedly succeeded")
	} else if strings.Contains(err.Error(), secretCanary) || strings.Contains(err.Error(), encodedKey) {
		t.Fatalf("Create() leaked command payload in error: %v", err)
	}
}

func TestMacOSKeychainProviderCreateMapsDuplicateExitCode(t *testing.T) {
	provider := &MacOSKeychainProvider{
		runner: &recordingSecurityRunner{
			results: []securityCommandResult{
				{exitCode: 45, err: errors.New("duplicate command payload")},
			},
		},
		random: bytes.NewReader(bytes.Repeat([]byte{0x41}, 32)),
		goos:   "darwin",
	}
	if _, err := provider.Create(context.Background(), testStoreID); !errors.Is(err, ErrKeyAlreadyExists) {
		t.Fatalf("Create() error = %v, want ErrKeyAlreadyExists", err)
	}
}

func TestMacOSKeychainProviderCreateRejectsInvalidStoreIDBeforeInvocation(t *testing.T) {
	tests := []string{
		"",
		"store_test",
		"store_0123456789abcdef0123456789abcde",
		"store_0123456789abcdef0123456789abcdef0",
		"store_0123456789ABCDEF0123456789ABCDEF",
		"store_0123456789abcdef;delete-generic",
		"store_0123456789abcdef\nadd-generic",
	}
	for _, storeID := range tests {
		t.Run(storeID, func(t *testing.T) {
			runner := &recordingSecurityRunner{}
			provider := &MacOSKeychainProvider{
				runner: runner,
				random: bytes.NewReader(bytes.Repeat([]byte{0x41}, 32)),
				goos:   "darwin",
			}
			if _, err := provider.Create(context.Background(), storeID); err == nil {
				t.Fatal("Create() error = nil, want invalid store ID rejection")
			} else if storeID != "" && strings.Contains(err.Error(), storeID) {
				t.Fatalf("Create() error included untrusted store ID: %v", err)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("security calls = %d, want zero", len(runner.calls))
			}
		})
	}
}

func TestMacOSKeychainProviderCommandsHaveBoundedTimeout(t *testing.T) {
	tests := []struct {
		name string
		call func(*MacOSKeychainProvider) error
	}{
		{
			name: "load",
			call: func(provider *MacOSKeychainProvider) error {
				_, err := provider.Load(context.Background(), testStoreID)
				return err
			},
		},
		{
			name: "create",
			call: func(provider *MacOSKeychainProvider) error {
				_, err := provider.Create(context.Background(), testStoreID)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &MacOSKeychainProvider{
				runner:  blockingSecurityRunner{},
				random:  bytes.NewReader(bytes.Repeat([]byte{0x41}, 32)),
				goos:    "darwin",
				timeout: 10 * time.Millisecond,
			}
			started := time.Now()
			err := test.call(provider)
			if err == nil {
				t.Fatal("Keychain timeout unexpectedly succeeded")
			}
			if !strings.Contains(err.Error(), "timed out") {
				t.Fatalf("Keychain timeout error = %v", err)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("Keychain timeout took %s, want bounded execution", elapsed)
			}
		})
	}
}

type securityCall struct {
	args  []string
	stdin string
}

type recordingSecurityRunner struct {
	results []securityCommandResult
	calls   []securityCall
}

type blockingSecurityRunner struct{}

func (blockingSecurityRunner) run(
	ctx context.Context,
	_ io.Reader,
	_ ...string,
) securityCommandResult {
	<-ctx.Done()
	return securityCommandResult{err: ctx.Err()}
}

func (runner *recordingSecurityRunner) run(
	_ context.Context,
	stdin io.Reader,
	args ...string,
) securityCommandResult {
	var body []byte
	if stdin != nil {
		body, _ = io.ReadAll(stdin)
	}
	runner.calls = append(runner.calls, securityCall{
		args:  append([]string(nil), args...),
		stdin: string(body),
	})
	if len(runner.results) == 0 {
		return securityCommandResult{err: errors.New("unexpected security invocation")}
	}
	result := runner.results[0]
	runner.results = runner.results[1:]
	return result
}
