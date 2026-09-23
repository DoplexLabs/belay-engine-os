package numbat

import "runtime"

// HostEnvironmentNames lists the only environment variables Belay forwards to
// the Numbat and agent-CLI subprocesses it launches. The list is an allowlist:
// nothing else from the parent environment reaches those commands.
func HostEnvironmentNames() []string {
	return hostEnvironmentNames(runtime.GOOS)
}

func hostEnvironmentNames(goos string) []string {
	names := []string{
		"HOME",
		"USER",
		"TMPDIR",
		"PATH",
		"LANG",
		"LC_ALL",
		"CODEX_HOME",
		"CLAUDE_CONFIG_DIR",
		"XDG_CONFIG_HOME",
		"XDG_DATA_HOME",
	}
	if goos == "windows" {
		// Windows processes resolve the profile, per-user application data,
		// temp directories, and executable extensions from these variables.
		// SYSTEMROOT is added by os/exec itself when absent.
		names = append(names,
			"USERPROFILE",
			"USERNAME",
			"HOMEDRIVE",
			"HOMEPATH",
			"APPDATA",
			"LOCALAPPDATA",
			"PROGRAMDATA",
			"SYSTEMROOT",
			"SYSTEMDRIVE",
			"COMSPEC",
			"PATHEXT",
			"TEMP",
			"TMP",
		)
	}
	return names
}
