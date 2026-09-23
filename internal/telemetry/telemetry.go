// Package telemetry sends Belay's product telemetry: a de-identified install
// ping and at most one "active" ping per day. The
// payload is a fixed set of fields listed in docs/contracts/telemetry-v1.md
// and nothing else ever leaves the machine through this package. It is
// fail-open (a failed send never affects the product), skipped for dev
// builds, and switched off with `belay telemetry off` or BELAY_TELEMETRY=0.
package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	SchemaVersion   = "belay.telemetry.v1"
	DefaultEndpoint = "https://getbelay.vercel.app/api/telemetry"
	EnvDisable      = "BELAY_TELEMETRY"
	EnvDoNotTrack   = "DO_NOT_TRACK"
	EnvEndpoint     = "BELAY_TELEMETRY_ENDPOINT"
	StateFileName   = "telemetry.json"

	EventInstall = "install"
	EventActive  = "active"

	stateVersion   = 1
	sendTimeout    = 5 * time.Second
	maxResponseLen = 4096
)

// Notice is printed once per Belay home, right after the first successful
// send, because telemetry is on by default.
const Notice = "belay: sends a de-identified usage ping (random ID, version, OS, chip, which of claude/codex/cursor/antigravity are installed). No content, paths, or names. Turn it off: belay telemetry off\n"

// Event is the complete wire payload. Adding a field is a contract change.
type Event struct {
	SchemaVersion string   `json:"schema_version"`
	Event         string   `json:"event"`
	TelemetryID   string   `json:"telemetry_id"`
	Version       string   `json:"version"`
	OS            string   `json:"os"`
	Arch          string   `json:"arch"`
	Harnesses     []string `json:"harnesses"`
	SentAt        string   `json:"sent_at"`
}

// State is the on-disk record in the Belay home directory. The telemetry ID
// is random, generated here, and deliberately distinct from the local
// installation ID used inside the evidence store.
type State struct {
	Version           int    `json:"version"`
	TelemetryID       string `json:"telemetry_id"`
	OptedOut          bool   `json:"opted_out"`
	InstallReported   bool   `json:"install_reported"`
	LastActiveDay     string `json:"last_active_day,omitempty"`
	Disclosed         bool   `json:"disclosed,omitempty"`
	LastSendError     string `json:"last_send_error,omitempty"`
	LastSendAttemptAt string `json:"last_send_attempt_at,omitempty"`
}

type Status struct {
	Enabled     bool     `json:"enabled"`
	Reason      string   `json:"reason"`
	TelemetryID string   `json:"telemetry_id,omitempty"`
	Endpoint    string   `json:"endpoint"`
	Fields      []string `json:"fields"`
}

type Client struct {
	root      string
	version   string
	endpoint  string
	http      *http.Client
	now       func() time.Time
	harnesses func() []string
	getenv    func(string) string
	notice    io.Writer
}

type Option func(*Client)

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.http = client
		}
	}
}

func WithClock(now func() time.Time) Option {
	return func(c *Client) {
		if now != nil {
			c.now = now
		}
	}
}

func WithHarnessProbe(probe func() []string) Option {
	return func(c *Client) {
		if probe != nil {
			c.harnesses = probe
		}
	}
}

// WithNotice sets where the one-time disclosure is written; nil stays silent.
func WithNotice(writer io.Writer) Option {
	return func(c *Client) {
		c.notice = writer
	}
}

func WithEnvironment(getenv func(string) string) Option {
	return func(c *Client) {
		if getenv != nil {
			c.getenv = getenv
		}
	}
}

// New builds a client for the Belay home directory and product version.
func New(root, version string, options ...Option) *Client {
	client := &Client{
		root:      strings.TrimSpace(root),
		version:   strings.TrimSpace(version),
		http:      &http.Client{Timeout: sendTimeout},
		now:       time.Now,
		harnesses: installedHarnesses,
		getenv:    os.Getenv,
	}
	for _, option := range options {
		if option != nil {
			option(client)
		}
	}
	client.endpoint = strings.TrimSpace(client.getenv(EnvEndpoint))
	if client.endpoint == "" {
		client.endpoint = DefaultEndpoint
	}
	return client
}

// Fields lists every wire field so status output and docs stay honest.
func Fields() []string {
	return []string{
		"schema_version", "event (install or active)", "telemetry_id (random, Belay-generated)",
		"version", "os", "arch",
		"harnesses (any of claude, codex, cursor, antigravity present)",
		"sent_at",
	}
}

func (c *Client) statePath() string {
	return filepath.Join(c.root, StateFileName)
}

// loadState reads the state file, creating and persisting a fresh random ID
// the first time so the ID is stable from the moment it is ever shown.
func (c *Client) loadState() (State, error) {
	body, err := os.ReadFile(c.statePath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return State{}, fmt.Errorf("read telemetry state: %w", err)
	}
	var state State
	if err == nil && json.Unmarshal(body, &state) == nil &&
		state.Version == stateVersion && validTelemetryID(state.TelemetryID) {
		return state, nil
	}
	id, err := newTelemetryID()
	if err != nil {
		return State{}, err
	}
	state = State{Version: stateVersion, TelemetryID: id}
	if err := c.saveState(state); err != nil {
		return State{}, err
	}
	return state, nil
}

func (c *Client) saveState(state State) error {
	if c.root == "" {
		return errors.New("telemetry state requires a Belay home")
	}
	if err := os.MkdirAll(c.root, 0o700); err != nil {
		return fmt.Errorf("prepare telemetry state: %w", err)
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return errors.New("encode telemetry state")
	}
	temp := c.statePath() + ".tmp"
	if err := os.WriteFile(temp, body, 0o600); err != nil {
		return fmt.Errorf("write telemetry state: %w", err)
	}
	if err := os.Rename(temp, c.statePath()); err != nil {
		os.Remove(temp)
		return fmt.Errorf("commit telemetry state: %w", err)
	}
	return nil
}

// disabledByEnvironment names the environment switch that turns telemetry
// off, or "" when neither is set.
func (c *Client) disabledByEnvironment() string {
	switch strings.ToLower(strings.TrimSpace(c.getenv(EnvDisable))) {
	case "0", "false", "off", "no", "disabled":
		return EnvDisable
	}
	switch strings.ToLower(strings.TrimSpace(c.getenv(EnvDoNotTrack))) {
	case "1", "true", "yes", "on":
		return EnvDoNotTrack
	}
	return ""
}

func (c *Client) devBuild() bool {
	return (c.version == "" || c.version == "dev") && strings.TrimSpace(c.getenv(EnvEndpoint)) == ""
}

// Status explains whether a ping would be sent and why.
func (c *Client) Status() Status {
	status := Status{Endpoint: c.endpoint, Fields: Fields()}
	state, err := c.loadState()
	if err == nil {
		status.TelemetryID = state.TelemetryID
	}
	switch {
	case c.disabledByEnvironment() != "":
		status.Reason = "disabled by " + c.disabledByEnvironment()
	case err == nil && state.OptedOut:
		status.Reason = "opted out with belay telemetry off"
	case c.devBuild():
		status.Reason = "dev builds send nothing"
	default:
		status.Enabled = true
		status.Reason = "enabled"
	}
	return status
}

// SetOptOut records the user's choice. It never sends anything.
func (c *Client) SetOptOut(optOut bool) error {
	state, err := c.loadState()
	if err != nil {
		return err
	}
	state.OptedOut = optOut
	return c.saveState(state)
}

// RecordActivity sends the install ping once and the active ping at most
// once per UTC day. It returns the events sent. Send failures are recorded
// in the state file and retried on a later run; they are never fatal.
func (c *Client) RecordActivity(ctx context.Context) ([]string, error) {
	if c.disabledByEnvironment() != "" || c.devBuild() {
		return nil, nil
	}
	state, err := c.loadState()
	if err != nil {
		return nil, err
	}
	if state.OptedOut {
		return nil, nil
	}
	today := c.now().UTC().Format("2006-01-02")
	var pending []string
	if !state.InstallReported {
		pending = append(pending, EventInstall)
	}
	if state.LastActiveDay != today {
		pending = append(pending, EventActive)
	}
	if len(pending) == 0 {
		return nil, nil
	}
	var sent []string
	for _, event := range pending {
		state.LastSendAttemptAt = c.now().UTC().Format(time.RFC3339)
		if err := c.send(ctx, event, state.TelemetryID); err != nil {
			state.LastSendError = boundedError(err)
			_ = c.saveState(state)
			return sent, err
		}
		state.LastSendError = ""
		switch event {
		case EventInstall:
			state.InstallReported = true
		case EventActive:
			state.LastActiveDay = today
		}
		sent = append(sent, event)
	}
	if !state.Disclosed && c.notice != nil {
		fmt.Fprint(c.notice, Notice)
		state.Disclosed = true
	}
	return sent, c.saveState(state)
}

func (c *Client) send(ctx context.Context, event, telemetryID string) error {
	payload := Event{
		SchemaVersion: SchemaVersion,
		Event:         event,
		TelemetryID:   telemetryID,
		Version:       c.version,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Harnesses:     c.harnesses(),
		SentAt:        c.now().UTC().Format(time.RFC3339),
	}
	if payload.Harnesses == nil {
		payload.Harnesses = []string{}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("encode telemetry event")
	}
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(sendCtx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("build telemetry request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "belay/"+c.version)
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("send telemetry: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("telemetry endpoint returned %d", response.StatusCode)
	}
	return nil
}

func installedHarnesses() []string {
	result := []string{}
	if _, err := exec.LookPath("claude"); err == nil {
		result = append(result, "claude")
	}
	if _, err := exec.LookPath("codex"); err == nil {
		result = append(result, "codex")
	}
	// Cursor's CLI is not reliably on PATH and the names it does use are too
	// generic to probe, so presence is the real ~/.cursor directory instead.
	if cursorHomePresent() {
		result = append(result, "cursor")
	}
	// Antigravity's launcher binaries are not reliably on PATH either, so
	// presence is Antigravity 2.0's real app-data directory,
	// ~/.gemini/antigravity.
	if antigravityHomePresent() {
		result = append(result, "antigravity")
	}
	return result
}

// cursorHomePresent reports whether ~/.cursor exists as a real directory.
// A symlink is not accepted: it could point anywhere, and this value leaves
// the machine.
func cursorHomePresent() bool {
	return realHomeSubdirectoryPresent(".cursor")
}

// antigravityHomePresent reports whether ~/.gemini/antigravity exists as a
// real directory, under the same symlink rule as cursorHomePresent.
func antigravityHomePresent() bool {
	return realHomeSubdirectoryPresent(".gemini", "antigravity")
}

// realHomeSubdirectoryPresent reports whether the named path under the home
// directory exists as a real directory. Lstat is deliberate: a symlink at the
// final element is rejected because it could point anywhere.
func realHomeSubdirectoryPresent(elements ...string) bool {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return false
	}
	info, err := os.Lstat(filepath.Join(append([]string{home}, elements...)...))
	if err != nil {
		return false
	}
	return info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func newTelemetryID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("generate telemetry identifier")
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

func validTelemetryID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, r := range value {
		switch index {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
				return false
			}
		}
	}
	return true
}

func boundedError(err error) string {
	message := err.Error()
	if len(message) > 200 {
		message = message[:200]
	}
	return message
}
