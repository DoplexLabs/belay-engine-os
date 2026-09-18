package local

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const keychainService = "dev.doplex.belay.local.data-key.v1"
const keychainCommandTimeout = 5 * time.Second
const persistedStoreIDPrefix = "store_"
const persistedStoreIDHexLength = 32

type securityCommandResult struct {
	stdout   []byte
	exitCode int
	err      error
}

type securityCommandRunner interface {
	run(ctx context.Context, stdin io.Reader, args ...string) securityCommandResult
}

type execSecurityCommandRunner struct{}

func (execSecurityCommandRunner) run(ctx context.Context, stdin io.Reader, args ...string) securityCommandResult {
	command := exec.CommandContext(ctx, "/usr/bin/security", args...)
	command.Stdin = stdin
	var stdout bytes.Buffer
	command.Stdout = &stdout
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err := command.Run()
	result := securityCommandResult{stdout: stdout.Bytes(), err: err}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.exitCode = exitError.ExitCode()
	}
	return result
}

// MacOSKeychainProvider stores one Local data key per persisted random store
// ID. It invokes /usr/bin/security directly without a shell.
type MacOSKeychainProvider struct {
	runner  securityCommandRunner
	random  io.Reader
	goos    string
	timeout time.Duration
}

func NewMacOSKeychainProvider() *MacOSKeychainProvider {
	return &MacOSKeychainProvider{
		runner:  execSecurityCommandRunner{},
		random:  rand.Reader,
		goos:    runtime.GOOS,
		timeout: keychainCommandTimeout,
	}
}

func (p *MacOSKeychainProvider) Load(ctx context.Context, storeID string) ([]byte, error) {
	if p.goos != "darwin" {
		return nil, errors.New("macOS Keychain is unavailable on this platform")
	}
	if storeID == "" {
		return nil, errors.New("local store ID is required")
	}
	commandContext, cancel := p.commandContext(ctx)
	defer cancel()
	result := p.runner.run(
		commandContext,
		nil,
		"find-generic-password",
		"-a", storeID,
		"-s", keychainService,
		"-w",
	)
	if result.err != nil {
		if errors.Is(commandContext.Err(), context.DeadlineExceeded) {
			return nil, errors.New("macOS Keychain command timed out")
		}
		if result.exitCode == 44 {
			return nil, ErrKeyNotFound
		}
		return nil, errors.New("load local data key from macOS Keychain")
	}
	encoded := strings.TrimSpace(string(result.stdout))
	key, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("decode local data key from macOS Keychain")
	}
	return key, nil
}

func (p *MacOSKeychainProvider) Create(ctx context.Context, storeID string) ([]byte, error) {
	if p.goos != "darwin" {
		return nil, errors.New("macOS Keychain is unavailable on this platform")
	}
	if !validPersistedStoreID(storeID) {
		return nil, errors.New("local store ID has invalid format")
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(p.random, key); err != nil {
		zeroBytes(key)
		return nil, errors.New("generate local data key")
	}
	encoded := make([]byte, base64.RawStdEncoding.EncodedLen(len(key)))
	base64.RawStdEncoding.Encode(encoded, key)
	defer zeroBytes(encoded)

	var commandInput bytes.Buffer
	commandInput.Grow(
		len("add-generic-password -a  -s  -w \n") +
			len(storeID) + len(keychainService) + len(encoded),
	)
	commandInput.WriteString("add-generic-password -a ")
	commandInput.WriteString(storeID)
	commandInput.WriteString(" -s ")
	commandInput.WriteString(keychainService)
	commandInput.WriteString(" -w ")
	commandInput.Write(encoded)
	commandInput.WriteByte('\n')
	defer zeroBytes(commandInput.Bytes())

	commandContext, cancel := p.commandContext(ctx)
	defer cancel()
	result := p.runner.run(
		commandContext,
		bytes.NewReader(commandInput.Bytes()),
		"-q",
		"-i",
	)
	if result.err != nil {
		zeroBytes(key)
		if errors.Is(commandContext.Err(), context.DeadlineExceeded) {
			return nil, errors.New("macOS Keychain command timed out")
		}
		if result.exitCode == 45 {
			return nil, ErrKeyAlreadyExists
		}
		return nil, errors.New("store local data key in macOS Keychain")
	}
	return key, nil
}

func validPersistedStoreID(storeID string) bool {
	if len(storeID) != len(persistedStoreIDPrefix)+persistedStoreIDHexLength ||
		!strings.HasPrefix(storeID, persistedStoreIDPrefix) {
		return false
	}
	for _, character := range storeID[len(persistedStoreIDPrefix):] {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func (p *MacOSKeychainProvider) commandContext(parent context.Context) (context.Context, context.CancelFunc) {
	timeout := p.timeout
	if timeout <= 0 {
		timeout = keychainCommandTimeout
	}
	return context.WithTimeout(parent, timeout)
}
