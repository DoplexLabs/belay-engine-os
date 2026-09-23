package localapp

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// numbatExecutableName is the packaged sibling Numbat file name for the
// running platform. Windows executables carry an .exe suffix.
var numbatExecutableName = executableName("numbat")

// NumbatExecutableName returns the packaged sibling Numbat file name.
func NumbatExecutableName() string {
	return numbatExecutableName
}

func executableName(base string) string {
	return base + executableSuffix(runtime.GOOS)
}

func executableSuffix(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// usableExecutable reports whether path is a regular file Belay may execute.
// Unix relies on the executable mode bits. Windows has no such bit in Go's
// portable FileMode, so a small fixed extension allowlist is used instead.
func usableExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && executableFileInfo(runtime.GOOS, path, info)
}

func executableFileInfo(goos, path string, info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() {
		return false
	}
	if goos == "windows" {
		return windowsExecutableExtension(path)
	}
	return info.Mode()&0o111 != 0
}

func windowsExecutableExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".exe", ".com", ".bat", ".cmd":
		return true
	default:
		return false
	}
}

// privateFilePermissions reports whether a file Belay wrote with mode 0o600 still
// carries private permissions. Windows does not expose POSIX permission bits, so
// only the regular-file check applies there; ACLs inherited from the private
// %USERPROFILE% tree provide per-user isolation.
func privateFilePermissions(goos string, info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() {
		return false
	}
	if goos == "windows" {
		return true
	}
	return info.Mode().Perm() == 0o600
}

// materializedNumbatPath derives the private cached copy name for a verified
// Numbat body, keeping the platform executable suffix at the end of the name.
func materializedNumbatPath(bundledBin, checksum string) string {
	suffix := executableSuffix(runtime.GOOS)
	base := strings.TrimSuffix(bundledBin, suffix)
	return base + "-" + checksum[:16] + suffix
}

func currentGOOS() string {
	return runtime.GOOS
}
