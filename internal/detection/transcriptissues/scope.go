package transcriptissues

import (
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func scopedProjects(project preparedProject) []preparedProject {
	if !localPathIdentity(project.project.Identity) {
		return []preparedProject{project}
	}
	grouped := make(map[string][]preparedSession)
	for _, session := range project.sessions {
		scope := sessionScopePath(session, project.project.Path)
		grouped[scope] = append(grouped[scope], session)
	}
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]preparedProject, 0, len(keys))
	for _, scope := range keys {
		scoped := project
		scoped.project.Path = scope
		scoped.scopeIdentity = project.project.Identity
		if scope != project.project.Path {
			scoped.scopeIdentity += "\x00" + scope
		}
		scoped.sessions = grouped[scope]
		result = append(result, scoped)
	}
	return result
}

func localPathIdentity(value string) bool {
	return filepath.IsAbs(strings.TrimSpace(value))
}

func sessionScopePath(
	session preparedSession,
	fallback string,
) string {
	counts := make(map[string]int)
	for _, turn := range session.turns {
		path := normalizedScopePath(turn.Payload.CWD)
		if path == "" {
			continue
		}
		path = repositoryRoot(path)
		if transientScopePath(path) {
			continue
		}
		counts[path]++
	}
	fallback = normalizedScopePath(fallback)
	if fallback == "" {
		fallback = session.metadata.ProjectPath
	}
	if len(counts) == 0 {
		return fallback
	}
	best, bestCount := "", -1
	for path, count := range counts {
		if path == fallback && len(counts) > 1 {
			continue
		}
		if count > bestCount || (count == bestCount && path < best) {
			best, bestCount = path, count
		}
	}
	if best == "" {
		return fallback
	}
	return best
}

func normalizedScopePath(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "file://") {
		parsed, err := url.Parse(value)
		if err == nil {
			value = parsed.Path
		}
	}
	if !filepath.IsAbs(value) {
		return ""
	}
	return filepath.Clean(value)
}

func repositoryRoot(path string) string {
	current := path
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		current = parent
	}
}

func transientScopePath(path string) bool {
	clean := filepath.ToSlash(path)
	if clean == "/tmp" ||
		strings.HasPrefix(clean, "/tmp/") ||
		clean == "/private/tmp" ||
		strings.HasPrefix(clean, "/private/tmp/") ||
		strings.HasPrefix(clean, "/var/folders/") {
		return true
	}
	for _, segment := range []string{
		"/.codex/",
		"/.agents/",
		"/.toolbox/",
	} {
		if strings.Contains(clean, segment) {
			return true
		}
	}
	return false
}
