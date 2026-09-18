package numbat

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type Agent uint8

const (
	AgentCodex Agent = iota + 1
	AgentClaude
)

func (a Agent) String() string {
	switch a {
	case AgentCodex:
		return "codex"
	case AgentClaude:
		return "claude"
	default:
		return ""
	}
}

func (a Agent) MarshalText() ([]byte, error) {
	value, err := validateAgent(a)
	if err != nil {
		return nil, err
	}
	return []byte(value), nil
}

func parseAgent(value string) (Agent, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case AgentCodex.String():
		return AgentCodex, true
	case AgentClaude.String(), "claude code", "claude-code":
		return AgentClaude, true
	default:
		return 0, false
	}
}

func validateAgent(agent Agent) (string, error) {
	value := agent.String()
	if value == "" {
		return "", errors.New("unsupported Numbat launch agent")
	}
	return value, nil
}

type HistoricalScan struct {
	Stdout io.ReadCloser

	command   *exec.Cmd
	stderr    *boundedCapture
	operation string
	context   context.Context
	once      sync.Once
	result    CommandResult
	err       error
}

func (c *Client) StartHistoricalScan(ctx context.Context, agent Agent) (*HistoricalScan, error) {
	agentName, err := validateAgent(agent)
	if err != nil {
		return nil, err
	}
	command := c.command(ctx,
		"scan",
		"--agent", agentName,
		"--emit", "all",
		"--output", "stdout",
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &boundedCapture{limit: maxStderrBytes}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		_ = stdout.Close()
		return nil, err
	}
	return &HistoricalScan{
		Stdout:    stdout,
		command:   command,
		stderr:    stderr,
		operation: "historical scan",
		context:   ctx,
	}, nil
}

func (s *HistoricalScan) Wait() (CommandResult, error) {
	s.once.Do(func() {
		err := s.command.Wait()
		s.result = CommandResult{
			ExitCode: exitCode(s.command, err),
			Stderr:   sanitizeStderr(s.stderr.String(), s.stderr.truncated),
		}
		if err == nil {
			return
		}
		if contextErr := s.context.Err(); contextErr != nil {
			err = contextErr
		}
		s.err = &CommandError{
			Operation: s.operation,
			Result:    s.result,
			Cause:     err,
		}
	})
	return s.result, s.err
}

func (c *Client) InstallMonitorHook(ctx context.Context, agent Agent, spoolPath string) (CommandResult, error) {
	agentName, err := validateAgent(agent)
	if err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	if err := validateSpoolPath(spoolPath); err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	return c.run(ctx, "hook install",
		"hook", "install",
		"--agent", agentName,
		"--emit", "all",
		"--output", "file",
		"--output-file", spoolPath,
	)
}

func (c *Client) MonitorHookStatus(ctx context.Context, agent Agent) (CommandResult, error) {
	agentName, err := validateAgent(agent)
	if err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	return c.run(ctx, "hook status", "hook", "status", "--agent", agentName)
}

func (c *Client) UninstallMonitorHook(ctx context.Context, agent Agent) (CommandResult, error) {
	agentName, err := validateAgent(agent)
	if err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	return c.run(ctx, "hook uninstall", "hook", "uninstall", "--agent", agentName)
}

func validateSpoolPath(path string) error {
	if strings.TrimSpace(path) == "" || strings.IndexByte(path, 0) >= 0 {
		return errors.New("hook spool path is required")
	}
	if !filepath.IsAbs(path) {
		return errors.New("hook spool path must be absolute")
	}
	return nil
}
