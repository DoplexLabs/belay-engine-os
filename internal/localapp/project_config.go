package localapp

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/detection/transcriptissues"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/missionpack"
)

const maxProjectConfigBytes = 1 << 20

var (
	makeTargetPattern = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*:(?:[^=]|$)`)
	tomlHeaderPattern = regexp.MustCompile(`^\s*\[([^\]]+)\]\s*$`)
	tomlKeyPattern    = regexp.MustCompile(`^\s*([A-Za-z0-9_.-]+)\s*=`)
)

func loadProjectConfig(projectPath string) issueintel.ProjectConfig {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return issueintel.ProjectConfig{}
	}
	result := issueintel.ProjectConfig{
		HasClaudeInstructions: regularProjectFile(
			filepath.Join(projectPath, "CLAUDE.md"),
		),
		HasCodexInstructions: regularProjectFile(
			filepath.Join(projectPath, "AGENTS.md"),
		),
	}
	for _, command := range discoverVerificationCommands(projectPath) {
		result.VerificationCommands = append(
			result.VerificationCommands,
			command.Command,
		)
	}
	sort.Strings(result.VerificationCommands)
	return result
}

func discoverVerificationCommands(
	projectPath string,
) []missionpack.DiscoveredCommand {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if !filepath.IsAbs(projectPath) {
		return nil
	}
	type sourceCommands struct {
		path     string
		commands map[string]bool
	}
	sources := []sourceCommands{
		{
			path:     filepath.Join(projectPath, "package.json"),
			commands: make(map[string]bool),
		},
		{
			path:     filepath.Join(projectPath, "Makefile"),
			commands: make(map[string]bool),
		},
		{
			path:     filepath.Join(projectPath, "makefile"),
			commands: make(map[string]bool),
		},
		{
			path:     filepath.Join(projectPath, "pyproject.toml"),
			commands: make(map[string]bool),
		},
	}
	loadPackageScripts(
		sources[0].path,
		selectPackageManager(projectPath),
		sources[0].commands,
	)
	loadMakeTargets(sources[1].path, sources[1].commands)
	loadMakeTargets(sources[2].path, sources[2].commands)
	loadPyprojectCommands(sources[3].path, sources[3].commands)

	var result []missionpack.DiscoveredCommand
	for _, source := range sources {
		body, err := readBoundedProjectFile(source.path)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(body)
		relative, err := filepath.Rel(projectPath, source.path)
		if err != nil || strings.HasPrefix(relative, "..") {
			continue
		}
		for command := range source.commands {
			class, ok := transcriptissues.ClassifyVerificationCommand(
				command,
				issueintel.ProjectConfig{
					VerificationCommands: []string{command},
				},
			)
			if !ok {
				continue
			}
			result = append(result, missionpack.DiscoveredCommand{
				Command:      command,
				Class:        class,
				SourceFile:   filepath.ToSlash(relative),
				SourceSHA256: hex.EncodeToString(sum[:]),
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Command != result[j].Command {
			return result[i].Command < result[j].Command
		}
		return result[i].SourceFile < result[j].SourceFile
	})
	return result
}

func selectPackageManager(projectPath string) string {
	body, err := readBoundedProjectFile(filepath.Join(projectPath, "package.json"))
	if err == nil {
		var manifest struct {
			PackageManager string `json:"packageManager"`
		}
		if json.Unmarshal(body, &manifest) == nil {
			value := strings.ToLower(strings.TrimSpace(manifest.PackageManager))
			if index := strings.IndexByte(value, '@'); index >= 0 {
				value = value[:index]
			}
			switch value {
			case "npm", "pnpm", "yarn", "bun":
				return value
			}
		}
	}
	for _, candidate := range []struct {
		manager string
		files   []string
	}{
		{"pnpm", []string{"pnpm-lock.yaml"}},
		{"yarn", []string{"yarn.lock"}},
		{"bun", []string{"bun.lock", "bun.lockb"}},
		{"npm", []string{"package-lock.json", "npm-shrinkwrap.json"}},
	} {
		for _, name := range candidate.files {
			if regularProjectFile(filepath.Join(projectPath, name)) {
				return candidate.manager
			}
		}
	}
	return "npm"
}

func loadPackageScripts(path, manager string, result map[string]bool) {
	body, err := readBoundedProjectFile(path)
	if err != nil {
		return
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(body, &manifest) != nil {
		return
	}
	for name, body := range manifest.Scripts {
		if !looksLikeVerification(name + " " + body) {
			continue
		}
		prefix := manager + " run "
		if manager == "yarn" {
			prefix = "yarn "
		}
		result[prefix+name] = true
		if name == "test" {
			result[manager+" test"] = true
		}
	}
}

func loadMakeTargets(path string, result map[string]bool) {
	body, err := readBoundedProjectFile(path)
	if err != nil {
		return
	}
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		match := makeTargetPattern.FindStringSubmatch(scanner.Text())
		if len(match) != 2 || !looksLikeVerification(match[1]) {
			continue
		}
		result["make "+match[1]] = true
	}
}

func loadPyprojectCommands(path string, result map[string]bool) {
	body, err := readBoundedProjectFile(path)
	if err != nil {
		return
	}
	section := ""
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		line := scanner.Text()
		if match := tomlHeaderPattern.FindStringSubmatch(line); len(match) == 2 {
			section = strings.ToLower(match[1])
			switch {
			case strings.HasPrefix(section, "tool.pytest"):
				result["pytest"] = true
			case strings.HasPrefix(section, "tool.ruff"):
				result["ruff check"] = true
			case strings.HasPrefix(section, "tool.mypy"):
				result["mypy"] = true
			case strings.HasPrefix(section, "tool.pyright"):
				result["pyright"] = true
			case strings.HasPrefix(section, "tool.black"):
				result["black --check"] = true
			}
			continue
		}
		match := tomlKeyPattern.FindStringSubmatch(line)
		if len(match) != 2 || !looksLikeVerification(match[1]) {
			continue
		}
		switch {
		case strings.Contains(section, "poe.tasks"):
			result["poe "+match[1]] = true
		case strings.Contains(section, ".scripts"):
			result["hatch run "+match[1]] = true
		}
	}
}

func looksLikeVerification(value string) bool {
	value = strings.ToLower(value)
	for _, marker := range []string{
		"test",
		"check",
		"verify",
		"lint",
		"type",
		"build",
		"compile",
		"format",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func regularProjectFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil &&
		info.Mode().IsRegular() &&
		info.Mode()&os.ModeSymlink == 0
}

func readBoundedProjectFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		if err != nil {
			return nil, err
		}
		return nil, os.ErrInvalid
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := io.LimitReader(file, maxProjectConfigBytes+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(body) > maxProjectConfigBytes {
		return nil, os.ErrInvalid
	}
	return body, nil
}
