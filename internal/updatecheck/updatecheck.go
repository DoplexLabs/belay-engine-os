// Package updatecheck performs Belay's optional, content-free release check.
// It contacts the public GitHub releases API at most once every 18 hours,
// caches only release metadata in the Belay home, and never downloads or
// installs an update.
package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	SchemaVersion   = "belay.update-check.v1"
	DefaultEndpoint = "https://api.github.com/repos/DoplexLabs/belay/releases?per_page=10"
	EnvDisable      = "BELAY_UPDATE_CHECK"
	EnvEndpoint     = "BELAY_UPDATE_ENDPOINT"
	StateFileName   = "updates.json"
	CheckInterval   = 18 * time.Hour

	stateVersion = 1
	maxBodySize  = 2 << 20
	sendTimeout  = 5 * time.Second
)

type Status struct {
	SchemaVersion   string `json:"schema_version"`
	Enabled         bool   `json:"enabled"`
	Reason          string `json:"reason"`
	Checking        bool   `json:"checking"`
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseName     string `json:"release_name,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty"`
	PublishedAt     string `json:"published_at,omitempty"`
	CheckedAt       string `json:"checked_at,omitempty"`
	NextCheckAt     string `json:"next_check_at,omitempty"`
}

type State struct {
	Version          int    `json:"version"`
	OptedOut         bool   `json:"opted_out"`
	LastCheckedAt    string `json:"last_checked_at,omitempty"`
	ETag             string `json:"etag,omitempty"`
	LatestVersion    string `json:"latest_version,omitempty"`
	LatestName       string `json:"latest_name,omitempty"`
	LatestReleaseURL string `json:"latest_release_url,omitempty"`
	LatestPublished  string `json:"latest_published_at,omitempty"`
	DismissedVersion string `json:"dismissed_version,omitempty"`
	RemindAfter      string `json:"remind_after,omitempty"`
	LastError        string `json:"last_error,omitempty"`
}

type release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []asset   `json:"assets"`
}

type asset struct {
	Name string `json:"name"`
}

type Client struct {
	mu       sync.Mutex
	checkMu  sync.Mutex
	root     string
	version  string
	endpoint string
	http     *http.Client
	now      func() time.Time
	getenv   func(string) string
	checking bool
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

func WithEnvironment(getenv func(string) string) Option {
	return func(c *Client) {
		if getenv != nil {
			c.getenv = getenv
		}
	}
}

func New(root, version string, options ...Option) *Client {
	client := &Client{
		root:    strings.TrimSpace(root),
		version: normalizeVersion(version),
		http:    &http.Client{Timeout: sendTimeout},
		now:     time.Now,
		getenv:  os.Getenv,
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

func (c *Client) Start(ctx context.Context) {
	c.mu.Lock()
	if c.checking || !c.enabledLocked() {
		c.mu.Unlock()
		return
	}
	c.checking = true
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			c.checking = false
			c.mu.Unlock()
		}()
		_, _ = c.Check(ctx, false)
	}()
}

func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, _ := c.loadStateLocked()
	return c.statusLocked(state)
}

func (c *Client) Check(ctx context.Context, force bool) (Status, error) {
	c.checkMu.Lock()
	defer c.checkMu.Unlock()

	c.mu.Lock()
	state, err := c.loadStateLocked()
	if err != nil {
		status := c.statusLocked(state)
		c.mu.Unlock()
		return status, err
	}
	if !c.enabledLocked() || state.OptedOut {
		status := c.statusLocked(state)
		c.mu.Unlock()
		return status, nil
	}
	now := c.now().UTC()
	if !force {
		if checkedAt, ok := parseTime(state.LastCheckedAt); ok &&
			now.Before(checkedAt.Add(CheckInterval)) {
			status := c.statusLocked(state)
			c.mu.Unlock()
			return status, nil
		}
	}
	endpoint := c.endpoint
	version := c.version
	httpClient := c.http
	etag := state.ETag
	c.mu.Unlock()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return c.Status(), errors.New("build update request")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "belay/"+version)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}

	response, err := httpClient.Do(request)
	c.mu.Lock()
	defer c.mu.Unlock()
	state, loadErr := c.loadStateLocked()
	if loadErr != nil {
		return c.statusLocked(state), loadErr
	}
	state.LastCheckedAt = now.Format(time.RFC3339)
	if err != nil {
		state.LastError = boundedError(err)
		saveErr := c.saveStateLocked(state)
		return c.statusLocked(state), errors.Join(fmt.Errorf("check for updates: %w", err), saveErr)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusNotModified:
		state.LastError = ""
	case http.StatusOK:
		latest, readErr := readLatestRelease(response.Body)
		if readErr != nil {
			state.LastError = boundedError(readErr)
			saveErr := c.saveStateLocked(state)
			return c.statusLocked(state), errors.Join(readErr, saveErr)
		}
		state.ETag = strings.TrimSpace(response.Header.Get("ETag"))
		state.LatestVersion = latest.version
		state.LatestName = latest.name
		state.LatestReleaseURL = latest.url
		state.LatestPublished = latest.publishedAt
		state.LastError = ""
	default:
		requestErr := fmt.Errorf("update endpoint returned %d", response.StatusCode)
		state.LastError = boundedError(requestErr)
		saveErr := c.saveStateLocked(state)
		return c.statusLocked(state), errors.Join(requestErr, saveErr)
	}
	if err := c.saveStateLocked(state); err != nil {
		return c.statusLocked(state), err
	}
	return c.statusLocked(state), nil
}

func (c *Client) SetOptOut(optOut bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.loadStateLocked()
	if err != nil {
		return err
	}
	state.OptedOut = optOut
	return c.saveStateLocked(state)
}

func (c *Client) Remind(version string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.loadStateLocked()
	if err != nil {
		return err
	}
	if normalizeVersion(version) == "" ||
		normalizeVersion(version) != state.LatestVersion {
		return errors.New("update reminder version is not current")
	}
	state.RemindAfter = c.now().UTC().Add(CheckInterval).Format(time.RFC3339)
	return c.saveStateLocked(state)
}

func (c *Client) Dismiss(version string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.loadStateLocked()
	if err != nil {
		return err
	}
	if normalizeVersion(version) == "" ||
		normalizeVersion(version) != state.LatestVersion {
		return errors.New("dismissed update version is not current")
	}
	state.DismissedVersion = state.LatestVersion
	state.RemindAfter = ""
	return c.saveStateLocked(state)
}

func (c *Client) enabledLocked() bool {
	return !c.disabledByEnvironmentLocked() && !c.devBuildLocked()
}

func (c *Client) disabledByEnvironmentLocked() bool {
	switch strings.ToLower(strings.TrimSpace(c.getenv(EnvDisable))) {
	case "0", "false", "off", "no", "disabled":
		return true
	}
	return false
}

func (c *Client) devBuildLocked() bool {
	return (c.version == "" || c.version == "dev") &&
		strings.TrimSpace(c.getenv(EnvEndpoint)) == ""
}

func (c *Client) statusLocked(state State) Status {
	status := Status{
		SchemaVersion:  SchemaVersion,
		CurrentVersion: c.version,
		Checking:       c.checking,
	}
	switch {
	case c.disabledByEnvironmentLocked():
		status.Reason = "disabled by " + EnvDisable
	case state.OptedOut:
		status.Reason = "opted out with belay updates off"
	case c.devBuildLocked():
		status.Reason = "dev builds check nothing"
	default:
		status.Enabled = true
		status.Reason = "enabled"
	}
	status.LatestVersion = state.LatestVersion
	status.ReleaseName = state.LatestName
	status.ReleaseURL = state.LatestReleaseURL
	status.PublishedAt = state.LatestPublished
	status.CheckedAt = state.LastCheckedAt
	if checkedAt, ok := parseTime(state.LastCheckedAt); ok {
		status.NextCheckAt = checkedAt.Add(CheckInterval).Format(time.RFC3339)
	}
	available := status.Enabled &&
		compareVersions(state.LatestVersion, c.version) > 0 &&
		state.DismissedVersion != state.LatestVersion
	if remindAfter, ok := parseTime(state.RemindAfter); ok &&
		c.now().UTC().Before(remindAfter) {
		available = false
	}
	status.UpdateAvailable = available
	return status
}

func (c *Client) statePath() string {
	return filepath.Join(c.root, StateFileName)
}

func (c *Client) loadStateLocked() (State, error) {
	state := State{Version: stateVersion}
	body, err := os.ReadFile(c.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("read update state: %w", err)
	}
	if err := json.Unmarshal(body, &state); err != nil ||
		state.Version != stateVersion {
		return State{Version: stateVersion}, errors.New("parse update state")
	}
	return state, nil
}

func (c *Client) saveStateLocked(state State) error {
	if c.root == "" {
		return errors.New("update state requires a Belay home")
	}
	if err := os.MkdirAll(c.root, 0o700); err != nil {
		return fmt.Errorf("prepare update state: %w", err)
	}
	state.Version = stateVersion
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return errors.New("encode update state")
	}
	temp := c.statePath() + ".tmp"
	if err := os.WriteFile(temp, body, 0o600); err != nil {
		return fmt.Errorf("write update state: %w", err)
	}
	if err := os.Rename(temp, c.statePath()); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("commit update state: %w", err)
	}
	return nil
}

type latestRelease struct {
	version     string
	name        string
	url         string
	publishedAt string
}

func readLatestRelease(reader io.Reader) (latestRelease, error) {
	var releases []release
	decoder := json.NewDecoder(io.LimitReader(reader, maxBodySize))
	if err := decoder.Decode(&releases); err != nil {
		return latestRelease{}, errors.New("decode update response")
	}
	var latest latestRelease
	for _, candidate := range releases {
		version := normalizeVersion(candidate.TagName)
		if candidate.Draft || version == "" || !hasCompatibleReleaseAsset(candidate.Assets) {
			continue
		}
		if latest.version != "" && compareVersions(version, latest.version) <= 0 {
			continue
		}
		name := strings.TrimSpace(candidate.Name)
		if name == "" {
			name = "Belay " + version
		}
		latest = latestRelease{
			version:     version,
			name:        name,
			url:         strings.TrimSpace(candidate.HTMLURL),
			publishedAt: candidate.PublishedAt.UTC().Format(time.RFC3339),
		}
	}
	if latest.version == "" {
		return latestRelease{}, errors.New("no compatible Belay release found")
	}
	return latest, nil
}

func hasCompatibleReleaseAsset(assets []asset) bool {
	return hasReleaseAsset(assets, releaseAssetSuffix(runtime.GOOS, runtime.GOARCH))
}

// releaseAssetSuffix names the published archive for the running platform.
// Windows builds look for a zip archive of their own architecture. Every other
// platform keeps checking the Apple Silicon channel, which is the only published
// macOS archive; Intel macOS and Linux have no release channel of their own.
func releaseAssetSuffix(goos, goarch string) string {
	if goos == "windows" {
		return "-windows-" + goarch + ".zip"
	}
	return "-darwin-arm64.tar.gz"
}

func hasReleaseAsset(assets []asset, suffix string) bool {
	for _, candidate := range assets {
		name := strings.ToLower(strings.TrimSpace(candidate.Name))
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func normalizeVersion(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "v"))
	if value == "" || value == "dev" {
		return value
	}
	if _, ok := parseVersion(value); !ok {
		return ""
	}
	return value
}

type parsedVersion struct {
	core       [3]int64
	prerelease []string
}

func parseVersion(value string) (parsedVersion, bool) {
	value = strings.SplitN(value, "+", 2)[0]
	coreValue, prereleaseValue, hasPrerelease := strings.Cut(value, "-")
	coreParts := strings.Split(coreValue, ".")
	if len(coreParts) != 3 {
		return parsedVersion{}, false
	}
	var parsed parsedVersion
	for index, part := range coreParts {
		if part == "" {
			return parsedVersion{}, false
		}
		number, err := strconv.ParseInt(part, 10, 64)
		if err != nil || number < 0 {
			return parsedVersion{}, false
		}
		parsed.core[index] = number
	}
	if hasPrerelease {
		parsed.prerelease = strings.Split(prereleaseValue, ".")
		for _, part := range parsed.prerelease {
			if part == "" {
				return parsedVersion{}, false
			}
		}
	}
	return parsed, true
}

func compareVersions(left, right string) int {
	leftParsed, leftOK := parseVersion(normalizeVersion(left))
	rightParsed, rightOK := parseVersion(normalizeVersion(right))
	if !leftOK || !rightOK {
		return 0
	}
	for index := range leftParsed.core {
		if leftParsed.core[index] < rightParsed.core[index] {
			return -1
		}
		if leftParsed.core[index] > rightParsed.core[index] {
			return 1
		}
	}
	if len(leftParsed.prerelease) == 0 && len(rightParsed.prerelease) == 0 {
		return 0
	}
	if len(leftParsed.prerelease) == 0 {
		return 1
	}
	if len(rightParsed.prerelease) == 0 {
		return -1
	}
	limit := min(len(leftParsed.prerelease), len(rightParsed.prerelease))
	for index := 0; index < limit; index++ {
		comparison := compareIdentifier(
			leftParsed.prerelease[index],
			rightParsed.prerelease[index],
		)
		if comparison != 0 {
			return comparison
		}
	}
	switch {
	case len(leftParsed.prerelease) < len(rightParsed.prerelease):
		return -1
	case len(leftParsed.prerelease) > len(rightParsed.prerelease):
		return 1
	default:
		return 0
	}
}

func compareIdentifier(left, right string) int {
	leftNumber, leftErr := strconv.ParseInt(left, 10, 64)
	rightNumber, rightErr := strconv.ParseInt(right, 10, 64)
	switch {
	case leftErr == nil && rightErr == nil:
		switch {
		case leftNumber < rightNumber:
			return -1
		case leftNumber > rightNumber:
			return 1
		default:
			return 0
		}
	case leftErr == nil:
		return -1
	case rightErr == nil:
		return 1
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func parseTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	return parsed, err == nil
}

func boundedError(err error) string {
	message := err.Error()
	if len(message) > 200 {
		message = message[:200]
	}
	return message
}
