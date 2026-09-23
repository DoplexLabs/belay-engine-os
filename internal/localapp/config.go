package localapp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
)

const (
	ConfigVersion  = 1
	DefaultDirName = ".belay"
)

type Paths struct {
	Root              string
	Config            string
	Database          string
	CodexSpool        string
	ClaudeSpool       string
	CursorSpool       string
	AntigravitySpool  string
	Logs              string
	BundledBin        string
	TranscriptCursors string
	MCPManifest       string
	MCPConfigLock     string
}

type Config struct {
	Version             int    `json:"version"`
	InstallationID      string `json:"installation_id"`
	NumbatBinary        string `json:"numbat_binary,omitempty"`
	NumbatSHA256        string `json:"numbat_sha256,omitempty"`
	NumbatVersionMarker string `json:"numbat_version_marker,omitempty"`
}

func ResolvePaths(explicitRoot string) (Paths, error) {
	root := strings.TrimSpace(explicitRoot)
	if root == "" {
		root = strings.TrimSpace(os.Getenv("BELAY_HOME"))
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("resolve home directory: %w", err)
		}
		root = filepath.Join(home, DefaultDirName)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve Belay home: %w", err)
	}
	return Paths{
		Root:        absolute,
		Config:      filepath.Join(absolute, "config.json"),
		Database:    filepath.Join(absolute, "belay.sqlite"),
		CodexSpool:  filepath.Join(absolute, "live", "codex.ndjson"),
		ClaudeSpool: filepath.Join(absolute, "live", "claude.ndjson"),
		CursorSpool: filepath.Join(absolute, "live", "cursor.ndjson"),
		AntigravitySpool: filepath.Join(
			absolute,
			"live",
			"antigravity.ndjson",
		),
		Logs:       filepath.Join(absolute, "logs"),
		BundledBin: filepath.Join(absolute, "bin", numbatExecutableName),
		TranscriptCursors: filepath.Join(
			absolute,
			"transcripts",
			"cursors",
		),
		MCPManifest: filepath.Join(absolute, "mcp-config-ownership.json"),
		MCPConfigLock: filepath.Join(
			os.TempDir(),
			"belay-mcp-config-locks",
			fmt.Sprintf("%x.lock", sha256.Sum256([]byte(absolute))),
		),
	}, nil
}

func LoadOrCreateConfig(paths Paths) (Config, error) {
	if err := ensurePrivateDirectories(paths); err != nil {
		return Config{}, err
	}
	body, err := os.ReadFile(paths.Config)
	if err == nil {
		var config Config
		if err := json.Unmarshal(body, &config); err != nil {
			return Config{}, errors.New("parse Belay Local config")
		}
		if config.Version != ConfigVersion || !validInstallationID(config.InstallationID) {
			return Config{}, errors.New("Belay Local config has an unsupported version or installation identity")
		}
		return config, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read Belay Local config: %w", err)
	}
	installationID, err := newInstallationID()
	if err != nil {
		return Config{}, err
	}
	config := Config{
		Version:        ConfigVersion,
		InstallationID: installationID,
	}
	if err := writeConfigAtomic(paths.Config, config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func SaveConfig(path string, config Config) error {
	if config.Version != ConfigVersion || !validInstallationID(config.InstallationID) {
		return errors.New("refusing to write invalid Belay Local config")
	}
	return writeConfigAtomic(path, config)
}

func ResolveNumbatBinary(paths Paths, config Config, explicit string) (string, error) {
	executable, _ := os.Executable()
	return ResolveNumbatBinaryForExecutable(paths, config, explicit, executable)
}

func ResolveNumbatBinaryForExecutable(
	paths Paths,
	config Config,
	explicit string,
	belayExecutable string,
) (string, error) {
	candidates := []string{
		strings.TrimSpace(explicit),
		strings.TrimSpace(os.Getenv("BELAY_NUMBAT_BIN")),
		strings.TrimSpace(config.NumbatBinary),
		paths.BundledBin,
	}
	if strings.TrimSpace(belayExecutable) != "" {
		candidates = append(
			candidates,
			filepath.Join(filepath.Dir(belayExecutable), numbatExecutableName),
		)
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if usableExecutable(candidate) {
			absolute, err := filepath.Abs(candidate)
			if err != nil {
				return "", err
			}
			return absolute, nil
		}
	}
	path, err := execLookPath("numbat")
	if err == nil {
		return path, nil
	}
	return "", errors.New("pinned Numbat binary not found; set --numbat, BELAY_NUMBAT_BIN, or install it beside Belay")
}

func MaterializePinnedNumbat(
	ctx context.Context,
	paths Paths,
	source string,
	pin numbat.BinaryPin,
) (string, error) {
	if pin.SHA256 == "" || pin.VersionMarker == "" {
		return "", errors.New("Numbat pin is incomplete")
	}
	body, checksum, err := readNumbatSource(source)
	if err != nil {
		return "", errors.New("read pinned Numbat source")
	}
	if checksum != pin.SHA256 {
		return "", errors.New("Numbat checksum mismatch")
	}
	destination, err := materializeNumbatBody(paths, body, checksum)
	if err != nil {
		return "", err
	}
	if err := numbat.VerifyBinary(ctx, destination, pin); err != nil {
		return "", err
	}
	return destination, nil
}

// MaterializeUnverifiedNumbat gives development-only Numbat binaries a durable,
// private path before their location is persisted or written into harness hooks.
// It does not turn the binary into a verified release artifact.
func MaterializeUnverifiedNumbat(paths Paths, source string) (string, error) {
	body, checksum, err := readNumbatSource(source)
	if err != nil {
		return "", errors.New("read development Numbat source")
	}
	return materializeNumbatBody(paths, body, checksum)
}

func readNumbatSource(source string) ([]byte, string, error) {
	const maximumBinaryBytes = 256 << 20

	file, _, size, err := openRegularNoFollow(source)
	if err != nil {
		return nil, "", err
	}
	if size < 0 || size > maximumBinaryBytes {
		file.Close()
		return nil, "", errors.New("Numbat binary exceeds size limit")
	}
	body, err := io.ReadAll(io.LimitReader(file, maximumBinaryBytes+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil || len(body) > maximumBinaryBytes {
		return nil, "", errors.New("read Numbat binary")
	}
	sum := sha256.Sum256(body)
	return body, fmt.Sprintf("%x", sum[:]), nil
}

func materializeNumbatBody(
	paths Paths,
	body []byte,
	checksum string,
) (string, error) {
	if err := ensurePrivateDirectory(filepath.Dir(paths.BundledBin)); err != nil {
		return "", err
	}
	destination := materializedNumbatPath(paths.BundledBin, checksum)
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o500)
	if errors.Is(err, os.ErrExist) {
		_, existingChecksum, readErr := readNumbatSource(destination)
		if readErr != nil || existingChecksum != checksum {
			return "", errors.New("cached Numbat binary failed integrity check")
		}
		if err := os.Chmod(destination, 0o500); err != nil {
			return "", errors.New("restrict cached Numbat binary")
		}
		return destination, nil
	}
	if err != nil {
		return "", errors.New("create private Numbat binary")
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(destination)
		}
	}()
	if _, err := output.Write(body); err != nil {
		output.Close()
		return "", errors.New("write private Numbat binary")
	}
	if err := output.Sync(); err != nil {
		output.Close()
		return "", errors.New("sync private Numbat binary")
	}
	if err := output.Chmod(0o500); err != nil {
		output.Close()
		return "", errors.New("restrict private Numbat binary")
	}
	if err := output.Close(); err != nil {
		return "", errors.New("close private Numbat binary")
	}
	cleanup = false
	return destination, nil
}

var execLookPath = func(file string) (string, error) {
	path, err := exec.LookPath(file)
	if err != nil {
		return "", err
	}
	return filepath.Abs(path)
}

func ensurePrivateDirectories(paths Paths) error {
	for _, directory := range []string{
		paths.Root,
		filepath.Dir(paths.CodexSpool),
		filepath.Dir(paths.ClaudeSpool),
		filepath.Dir(paths.CursorSpool),
		filepath.Dir(paths.AntigravitySpool),
		paths.Logs,
		filepath.Dir(paths.BundledBin),
		paths.TranscriptCursors,
	} {
		if err := ensurePrivateDirectory(directory); err != nil {
			return err
		}
	}
	return nil
}

func ensurePrivateDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create private Belay directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect private Belay directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("private Belay path must be a real directory")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("restrict private Belay directory: %w", err)
	}
	return nil
}

func writeConfigAtomic(path string, config Config) error {
	body, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Belay Local config: %w", err)
	}
	body = append(body, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return fmt.Errorf("create Belay Local config: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("restrict Belay Local config: %w", err)
	}
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return fmt.Errorf("write Belay Local config: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync Belay Local config: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close Belay Local config: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("activate Belay Local config: %w", err)
	}
	return nil
}

func newInstallationID() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate Belay installation identity: %w", err)
	}
	return "inst_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func validInstallationID(value string) bool {
	if !strings.HasPrefix(value, "inst_") {
		return false
	}
	suffix := strings.TrimPrefix(value, "inst_")
	if len(suffix) < 8 || len(suffix) > 128 {
		return false
	}
	for _, character := range suffix {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}
