package localapp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecutableFileInfoByPlatform(t *testing.T) {
	directory := t.TempDir()
	plain := filepath.Join(directory, "numbat")
	if err := os.WriteFile(plain, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(directory, "numbat-exec")
	if err := os.WriteFile(executable, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	windowsExecutable := filepath.Join(directory, "numbat.EXE")
	if err := os.WriteFile(windowsExecutable, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	stat := func(path string) os.FileInfo {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	directoryInfo := stat(directory)

	cases := []struct {
		name string
		goos string
		path string
		want bool
	}{
		{name: "unix exec bit", goos: "darwin", path: executable, want: true},
		{name: "unix no exec bit", goos: "linux", path: plain, want: false},
		{name: "unix ignores extension", goos: "darwin", path: windowsExecutable, want: false},
		{name: "windows exe extension", goos: "windows", path: windowsExecutable, want: true},
		{name: "windows no extension", goos: "windows", path: executable, want: false},
	}
	for _, test := range cases {
		if got := executableFileInfo(test.goos, test.path, stat(test.path)); got != test.want {
			t.Errorf("%s: executableFileInfo = %v, want %v", test.name, got, test.want)
		}
	}
	if executableFileInfo("windows", directory+".exe", directoryInfo) {
		t.Error("directory reported as executable")
	}
	if executableFileInfo("darwin", "", nil) {
		t.Error("nil info reported as executable")
	}
}

func TestWindowsExecutableExtension(t *testing.T) {
	for path, want := range map[string]bool{
		`C:\Tools\belay.exe`:  true,
		`C:\Tools\claude.CMD`: true,
		`C:\Tools\codex.bat`:  true,
		`C:\Tools\run.com`:    true,
		`C:\Tools\numbat`:     false,
		`C:\Tools\notes.txt`:  false,
		`numbat.exe-0123abcd`: false,
	} {
		if got := windowsExecutableExtension(path); got != want {
			t.Errorf("windowsExecutableExtension(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestPrivateFilePermissions(t *testing.T) {
	directory := t.TempDir()
	private := filepath.Join(directory, "private")
	if err := os.WriteFile(private, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(directory, "shared")
	if err := os.WriteFile(shared, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	privateInfo, err := os.Stat(private)
	if err != nil {
		t.Fatal(err)
	}
	sharedInfo, err := os.Stat(shared)
	if err != nil {
		t.Fatal(err)
	}
	if !privateFilePermissions("darwin", privateInfo) {
		t.Error("0600 file rejected on darwin")
	}
	if privateFilePermissions("linux", sharedInfo) && sharedInfo.Mode().Perm() != 0o600 {
		t.Error("0644 file accepted on linux")
	}
	if !privateFilePermissions("windows", sharedInfo) {
		t.Error("regular file rejected on windows")
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if privateFilePermissions("windows", directoryInfo) {
		t.Error("directory accepted on windows")
	}
}

func TestMaterializedNumbatPathKeepsSuffixLast(t *testing.T) {
	checksum := "0123456789abcdef0123456789abcdef"
	got := materializedNumbatPath(filepath.Join("home", "bin", numbatExecutableName), checksum)
	want := filepath.Join("home", "bin", "numbat-0123456789abcdef"+executableSuffix(currentGOOS()))
	if got != want {
		t.Fatalf("materializedNumbatPath = %q, want %q", got, want)
	}
}
