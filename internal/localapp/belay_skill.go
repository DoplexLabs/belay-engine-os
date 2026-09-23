package localapp

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
)

//go:embed belay_skill/SKILL.md
var belaySkillBody string

const belaySkillOwnershipMarker = "<!-- managed-by: belay-local -->"

type BelaySkillInstallResult struct {
	Agent    string `json:"agent"`
	Detected bool   `json:"detected"`
	Status   string `json:"status"`
	Changed  bool   `json:"changed"`
}

func InstallBelaySkills(
	inventory numbat.Inventory,
) ([]BelaySkillInstallResult, error) {
	supported := numbat.SupportedAgents()
	results := make([]BelaySkillInstallResult, 0, len(supported))
	var installErrors []error
	for _, agent := range supported {
		row, ok := inventory.LaunchTargets[agent]
		detected := ok && (row.Present || row.Detected)
		result := BelaySkillInstallResult{
			Agent:    agent.String(),
			Detected: detected,
			Status:   "unavailable",
		}
		if !detected {
			results = append(results, result)
			continue
		}
		root, err := skillConfigRoot(agent)
		if err != nil {
			result.Status = "failed"
			results = append(results, result)
			installErrors = append(installErrors, err)
			continue
		}
		changed, err := installBelaySkillAt(root)
		if err != nil {
			result.Status = "failed"
			if errors.Is(err, errBelaySkillConflict) {
				result.Status = "conflict"
			}
			results = append(results, result)
			installErrors = append(installErrors, err)
			continue
		}
		result.Changed = changed
		result.Status = "unchanged"
		if changed {
			result.Status = "installed"
		}
		results = append(results, result)
	}
	return results, errors.Join(installErrors...)
}

var errBelaySkillConflict = errors.New("existing belay skill is not managed by Belay Local")

func skillConfigRoot(agent numbat.Agent) (string, error) {
	var environmentName, defaultDirectory string
	switch agent {
	case numbat.AgentClaude:
		environmentName = "CLAUDE_CONFIG_DIR"
		defaultDirectory = ".claude"
	case numbat.AgentCodex:
		environmentName = "CODEX_HOME"
		defaultDirectory = ".codex"
	case numbat.AgentCursor:
		// Cursor documents no configuration-root override, so the Agent
		// Skills directory is always resolved under the real home directory.
		environmentName = ""
		defaultDirectory = ".cursor"
	case numbat.AgentAntigravity:
		// Antigravity 2.0 reads global Agent Skills from
		// ~/.gemini/config/skills/<skill>/SKILL.md and documents no
		// environment override for that root, so it is always resolved
		// under the real home directory.
		environmentName = ""
		defaultDirectory = filepath.Join(".gemini", "config")
	default:
		return "", errors.New("unsupported skill agent")
	}
	var root string
	if environmentName != "" {
		root = strings.TrimSpace(os.Getenv(environmentName))
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("resolve skill home directory")
		}
		root = filepath.Join(home, defaultDirectory)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", errors.New("resolve skill configuration directory")
	}
	return absolute, nil
}

func installBelaySkillAt(root string) (bool, error) {
	if err := ensureRealDirectoryPath(root); err != nil {
		return false, err
	}
	skillsDirectory := filepath.Join(root, "skills")
	if err := ensureRealDirectory(skillsDirectory); err != nil {
		return false, err
	}
	skillDirectory := filepath.Join(skillsDirectory, "belay")
	if err := ensureRealDirectory(skillDirectory); err != nil {
		return false, err
	}
	target := filepath.Join(skillDirectory, "SKILL.md")
	existing, err := os.ReadFile(target)
	switch {
	case err == nil:
		info, statErr := os.Lstat(target)
		if statErr != nil || !info.Mode().IsRegular() ||
			info.Mode()&os.ModeSymlink != 0 {
			return false, errors.New("Belay skill target is not a regular file")
		}
		if string(existing) == belaySkillBody {
			return false, nil
		}
		if !strings.Contains(string(existing), belaySkillOwnershipMarker) {
			return false, errBelaySkillConflict
		}
	case !errors.Is(err, os.ErrNotExist):
		return false, errors.New("read existing Belay skill")
	}
	if err := writeSkillAtomic(target, []byte(belaySkillBody)); err != nil {
		return false, err
	}
	return true, nil
}

func ensureRealDirectoryPath(path string) error {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return errors.New("skill configuration path must be absolute")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return errors.New("create skill configuration directory")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("skill configuration path must be a real directory")
	}
	return nil
}

func ensureRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil {
			return errors.New("create skill directory")
		}
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("skill path must be a real directory")
	}
	return nil
}

func writeSkillAtomic(path string, body []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".belay-skill-*.md")
	if err != nil {
		return errors.New("create temporary Belay skill")
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return errors.New("restrict temporary Belay skill")
	}
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return errors.New("write temporary Belay skill")
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return errors.New("sync temporary Belay skill")
	}
	if err := temp.Close(); err != nil {
		return errors.New("close temporary Belay skill")
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("activate Belay skill: %w", err)
	}
	return nil
}
