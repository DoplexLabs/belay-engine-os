package localmcp

import (
	"path"
	"path/filepath"
	"strings"
)

func absoluteInputPath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if filepath.IsAbs(value) || strings.HasPrefix(filepath.ToSlash(value), "/") {
		return true
	}
	return len(value) >= 3 &&
		((value[0] >= 'A' && value[0] <= 'Z') ||
			(value[0] >= 'a' && value[0] <= 'z')) &&
		value[1] == ':' &&
		(value[2] == '\\' || value[2] == '/')
}

func cleanInputPath(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(filepath.ToSlash(value), "/") {
		return path.Clean(strings.ReplaceAll(value, "\\", "/"))
	}
	return filepath.Clean(value)
}
