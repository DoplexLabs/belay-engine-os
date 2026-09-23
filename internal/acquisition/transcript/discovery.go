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

var uuidAnywhere = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// cursorTranscriptDir is the directory Cursor writes agent transcripts into,
// below ~/.cursor/projects/<hash>/. Subagent threads are the same JSONL format
// in a directory nested under it.
const cursorTranscriptDir = "agent-transcripts"

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
	// Cursor documents no environment override for ~/.cursor the way Claude Code
	// and Codex do, so BELAY_CURSOR_HOME is Belay's own escape hatch (it is what
	// keeps the tests hermetic); it is never read as a Cursor-supported setting.
	cursorRoot := strings.TrimSpace(os.Getenv("BELAY_CURSOR_HOME"))
	if cursorRoot == "" {
		cursorRoot = filepath.Join(home, ".cursor")
	}
	claude, claudeErr := discoverClaude(filepath.Join(claudeRoot, "projects"))
	codex, codexErr := discoverCodex(filepath.Join(codexRoot, "sessions"))
	cursor, cursorErr := discoverCursor(filepath.Join(cursorRoot, "projects"))
	result := append(claude, codex...)
	result = append(result, cursor...)
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
	return result, errors.Join(claudeErr, codexErr, cursorErr)
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

// discoverCursor walks ~/.cursor/projects/<hash>/agent-transcripts/**/*.jsonl.
//
// A transcript directly inside agent-transcripts is the top-level thread; any
// transcript below a further directory is a subagent thread of the thread that
// directory is named for, so both share one GroupKey and only the top-level one
// is Primary. The <hash> project directory is carried opaquely (see
// Source.ProjectKey) so two projects never share a group; it is never decoded
// into a path.
func discoverCursor(root string) ([]Source, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var result []Source
	err = walkExisting(root, func(path string, entry fs.DirEntry) {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") {
			return
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) < 3 || parts[1] != cursorTranscriptDir {
			return
		}
		projectKey := parts[0]
		thread := parts[2:]
		conversationID := cursorConversationID(trimJSONLSuffix(thread[0]))
		if projectKey == "" || conversationID == "" {
			return
		}
		result = append(result, Source{
			Agent:           AgentCursor,
			Path:            path,
			GroupKey:        strings.Join([]string{AgentCursor, projectKey, conversationID}, "\x00"),
			NativeSessionID: conversationID,
			Primary:         len(thread) == 1,
			ProjectKey:      projectKey,
		})
	})
	return result, err
}

// cursorConversationID derives the conversation identity from a transcript file
// stem (or the directory a subagent thread hangs under). Cursor has named these
// both by a bare conversation UUID and by a decorated stem carrying one, so a
// UUID anywhere in the stem wins; otherwise the stem itself is the identity.
// Nothing is invented: a stem that names no conversation simply is one.
func cursorConversationID(stem string) string {
	stem = strings.TrimSpace(stem)
	if id := uuidAnywhere.FindString(stem); id != "" {
		return id
	}
	return stem
}

func trimJSONLSuffix(name string) string {
	if strings.EqualFold(filepath.Ext(name), ".jsonl") {
		return name[:len(name)-len(".jsonl")]
	}
	return name
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
