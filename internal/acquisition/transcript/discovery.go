package transcript

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var uuidSuffix = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func Discover() ([]Source, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	claudeRoot := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if claudeRoot == "" {
		claudeRoot = filepath.Join(home, ".claude")
	}
	codexRoot := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexRoot == "" {
		codexRoot = filepath.Join(home, ".codex")
	}
	claude, claudeErr := discoverClaude(filepath.Join(claudeRoot, "projects"))
	codex, codexErr := discoverCodex(filepath.Join(codexRoot, "sessions"))
	result := append(claude, codex...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Agent != result[j].Agent {
			return result[i].Agent < result[j].Agent
		}
		if result[i].GroupKey != result[j].GroupKey {
			return result[i].GroupKey < result[j].GroupKey
		}
		if result[i].Primary != result[j].Primary {
			return result[i].Primary
		}
		return result[i].Path < result[j].Path
	})
	return result, errors.Join(claudeErr, codexErr)
}

func discoverClaude(root string) ([]Source, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var result []Source
	err = walkExisting(root, func(path string, entry fs.DirEntry) {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			return
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		var sessionID, projectDirectory string
		primary := false
		switch {
		case len(parts) == 2:
			sessionID = strings.TrimSuffix(parts[1], ".jsonl")
			projectDirectory = parts[0]
			primary = true
		case len(parts) == 4 &&
			parts[2] == "subagents" &&
			strings.HasPrefix(parts[3], "agent-"):
			sessionID = parts[1]
			projectDirectory = parts[0]
		default:
			return
		}
		if sessionID == "" {
			return
		}
		result = append(result, Source{
			Agent:           AgentClaude,
			Path:            path,
			GroupKey:        strings.Join([]string{AgentClaude, projectDirectory, sessionID}, "\x00"),
			NativeSessionID: sessionID,
			Primary:         primary,
		})
	})
	return result, err
}

func discoverCodex(root string) ([]Source, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var result []Source
	err = walkExisting(root, func(path string, entry fs.DirEntry) {
		if entry.IsDir() ||
			!strings.HasPrefix(entry.Name(), "rollout-") ||
			filepath.Ext(entry.Name()) != ".jsonl" {
			return
		}
		base := strings.TrimSuffix(entry.Name(), ".jsonl")
		sessionID := uuidSuffix.FindString(base)
		if sessionID == "" {
			return
		}
		result = append(result, Source{
			Agent:           AgentCodex,
			Path:            path,
			GroupKey:        AgentCodex + "\x00" + path,
			NativeSessionID: sessionID,
			Primary:         true,
		})
	})
	return result, err
}

func walkExisting(root string, visit func(string, fs.DirEntry)) error {
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() || entry.IsDir() {
			visit(path, entry)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
