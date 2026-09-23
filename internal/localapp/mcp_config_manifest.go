package localapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"
)

const (
	mcpManifestVersion  = 1
	mcpManifestMaxBytes = 64 << 10
)

type manifestLoadState int

const (
	manifestMissing manifestLoadState = iota
	manifestValid
	manifestInvalid
)

type mcpOwnershipManifest struct {
	Version        int                          `json:"version"`
	InstallationID string                       `json:"installation_id"`
	Targets        map[string]mcpManifestTarget `json:"targets"`
}

type mcpManifestTarget struct {
	Scope      string   `json:"scope"`
	Command    string   `json:"command"`
	Args       []string `json:"args"`
	VerifiedAt string   `json:"verified_at"`
}

func loadMCPManifest(path, expectedInstallationID string) (mcpOwnershipManifest, manifestLoadState) {
	file, _, size, err := openRegularNoFollow(path)
	if errors.Is(err, os.ErrNotExist) {
		return mcpOwnershipManifest{}, manifestMissing
	}
	if err != nil || size <= 0 || size > mcpManifestMaxBytes {
		return mcpOwnershipManifest{}, manifestInvalid
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !privateFilePermissions(runtime.GOOS, info) {
		return mcpOwnershipManifest{}, manifestInvalid
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return mcpOwnershipManifest{}, manifestInvalid
	}
	decoder := json.NewDecoder(io.LimitReader(file, mcpManifestMaxBytes))
	decoder.DisallowUnknownFields()
	var manifest mcpOwnershipManifest
	if err := decoder.Decode(&manifest); err != nil {
		return mcpOwnershipManifest{}, manifestInvalid
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF ||
		!validMCPManifest(manifest, expectedInstallationID) {
		return mcpOwnershipManifest{}, manifestInvalid
	}
	return manifest, manifestValid
}

func validMCPManifest(manifest mcpOwnershipManifest, expectedInstallationID string) bool {
	if manifest.Version != mcpManifestVersion ||
		!validInstallationID(manifest.InstallationID) ||
		(expectedInstallationID != "" && manifest.InstallationID != expectedInstallationID) ||
		len(manifest.Targets) > 4 {
		return false
	}
	for agent, target := range manifest.Targets {
		if agent != "codex" && agent != "claude" && agent != "cursor" && agent != "antigravity" {
			return false
		}
		if target.Scope != "user" || !validManifestIdentity(target) {
			return false
		}
		verifiedAt, err := time.Parse(time.RFC3339, target.VerifiedAt)
		if err != nil || target.VerifiedAt != verifiedAt.UTC().Format(time.RFC3339) {
			return false
		}
	}
	return true
}

func validManifestIdentity(target mcpManifestTarget) bool {
	if !filepath.IsAbs(target.Command) || hasUnsafePathText(target.Command) {
		return false
	}
	if slices.Equal(target.Args, []string{"mcp"}) {
		return true
	}
	return len(target.Args) == 3 &&
		target.Args[0] == "mcp" &&
		target.Args[1] == "--home" &&
		filepath.IsAbs(target.Args[2]) &&
		!hasUnsafePathText(target.Args[2])
}

func writeMCPManifest(path string, manifest mcpOwnershipManifest) error {
	if !validMCPManifest(manifest, manifest.InstallationID) {
		return errors.New("invalid MCP ownership manifest")
	}
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("MCP ownership manifest must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return errors.New("encode MCP ownership manifest")
	}
	body = append(body, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".mcp-config-*.json")
	if err != nil {
		return errors.New("create MCP ownership manifest")
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return errors.New("restrict MCP ownership manifest")
	}
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return errors.New("write MCP ownership manifest")
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return errors.New("sync MCP ownership manifest")
	}
	if err := temp.Close(); err != nil {
		return errors.New("close MCP ownership manifest")
	}
	if err := os.Rename(tempPath, path); err != nil {
		return errors.New("activate MCP ownership manifest")
	}
	return nil
}

func acquireMCPConfigLock(ctx context.Context, path string) (func(), error) {
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	file, err := openOrCreatePrivateRegular(path)
	if err != nil {
		return nil, err
	}
	if err := lockMCPConfigFile(ctx, file); err != nil {
		file.Close()
		return nil, err
	}
	var once bool
	return func() {
		if once {
			return
		}
		once = true
		unlockMCPConfigFile(file)
		file.Close()
	}, nil
}
