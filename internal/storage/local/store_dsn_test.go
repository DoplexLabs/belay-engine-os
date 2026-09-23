package local

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLiteURIPathRootsWindowsDrivePaths(t *testing.T) {
	tests := []struct {
		name    string
		slashed string
		want    string
	}{
		{
			name:    "windows drive",
			slashed: "C:/Users/payel/.belay/belay.sqlite",
			want:    "file:///C:/Users/payel/.belay/belay.sqlite",
		},
		{
			name:    "windows drive with space",
			slashed: "D:/Belay Home/belay.sqlite",
			want:    "file:///D:/Belay%20Home/belay.sqlite",
		},
		{
			name:    "unix absolute",
			slashed: "/home/user/.belay/belay.sqlite",
			want:    "file:///home/user/.belay/belay.sqlite",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rendered := (&url.URL{Scheme: "file", Path: sqliteURIPath(test.slashed)}).String()
			if rendered != test.want {
				t.Fatalf("file URI = %q, want %q", rendered, test.want)
			}
		})
	}
}

func TestSQLiteDSNHasEmptyAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "belay.sqlite")
	dsn, err := sqliteDSN(path)
	if err != nil {
		t.Fatalf("sqliteDSN() error = %v", err)
	}
	if !strings.HasPrefix(dsn, "file:///") {
		t.Fatalf("sqliteDSN() = %q, want an empty URI authority (file:///...)", dsn)
	}
}
