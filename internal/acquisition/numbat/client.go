package numbat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"unicode"
)

const (
	maxCommandOutputBytes = 1 << 20
	maxStderrBytes        = 16 << 10
)

var (
	homePathPattern = regexp.MustCompile(`/(?:Users|home)/[^/\s]+`)
	queryPattern    = regexp.MustCompile(`\?[^\s]+`)
	secretPattern   = regexp.MustCompile(`(?i)\b(token|secret|password|authorization|api[_-]?key)\b(\s*[:=]\s*)[^\s]+`)
)

// Client invokes the pinned Numbat executable through fixed command shapes.
// It intentionally exposes no generic command or option escape hatch.
type Client struct {
	executable  string
	environment []string
}

type CommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

type CommandError struct {
	Operation string
	Result    CommandResult
	Cause     error
}

func (e *CommandError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("numbat %s failed: %v", e.Operation, e.Cause)
}

func (e *CommandError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func NewClient(executable string) (*Client, error) {
	if strings.TrimSpace(executable) == "" || strings.IndexByte(executable, 0) >= 0 {
		return nil, errors.New("numbat executable path is required")
	}
	return &Client{
		executable:  executable,
		environment: allowedEnvironment(),
	}, nil
}

func (c *Client) command(ctx context.Context, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, c.executable, args...)
	command.Env = append([]string(nil), c.environment...)
	return command
}

func allowedEnvironment() []string {
	names := HostEnvironmentNames()
	result := make([]string, 0, len(names))
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			result = append(result, name+"="+value)
		}
	}
	return result
}

func (c *Client) run(ctx context.Context, operation string, args ...string) (CommandResult, error) {
	var stdout boundedCapture
	stdout.limit = maxCommandOutputBytes
	var stderr boundedCapture
	stderr.limit = maxStderrBytes

	command := c.command(ctx, args...)
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()

	result := CommandResult{
		ExitCode: exitCode(command, err),
		Stdout:   stdout.String(),
		Stderr:   sanitizeStderr(stderr.String(), stderr.truncated),
	}
	if err == nil && stdout.truncated {
		err = errors.New("command output exceeded limit")
	}
	if err == nil {
		return result, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	return result, &CommandError{Operation: operation, Result: result, Cause: err}
}

func exitCode(command *exec.Cmd, err error) int {
	if command.ProcessState != nil {
		return command.ProcessState.ExitCode()
	}
	if err == nil {
		return 0
	}
	return -1
}

type boundedCapture struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (w *boundedCapture) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := w.limit - w.buffer.Len()
	if remaining <= 0 {
		w.truncated = true
		return originalLength, nil
	}
	if len(data) > remaining {
		_, _ = w.buffer.Write(data[:remaining])
		w.truncated = true
		return originalLength, nil
	}
	_, _ = w.buffer.Write(data)
	return originalLength, nil
}

func (w *boundedCapture) String() string {
	return w.buffer.String()
}

func sanitizeStderr(value string, truncated bool) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	value = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return r
		default:
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}
	}, value)
	value = homePathPattern.ReplaceAllString(value, "[HOME]")
	value = secretPattern.ReplaceAllString(value, "$1$2[REDACTED]")
	value = queryPattern.ReplaceAllString(value, "?[REDACTED]")
	value = strings.TrimSpace(value)
	if truncated {
		value += " [truncated]"
	}
	return value
}
