package experience

import "testing"

func TestRelativePathPatternMatchesPreservesSlashSeparatedDoubleStar(t *testing.T) {
	tests := []struct {
		pattern string
		file    string
		want    bool
	}{
		{pattern: "generated/**", file: "generated/client.go", want: true},
		{pattern: "generated/**", file: "generated/api/client.go", want: true},
		{pattern: "generated/**", file: "generated", want: true},
		{pattern: "**/client.go", file: "client.go", want: true},
		{pattern: "**/client.go", file: "generated/api/client.go", want: true},
		{pattern: "internal/*/client.go", file: "internal/api/client.go", want: true},
		{pattern: "internal/*/client.go", file: "internal/api/v2/client.go", want: false},
		{pattern: "generated/**", file: "internal/generated/client.go", want: false},
	}
	for _, test := range tests {
		t.Run(test.pattern+" "+test.file, func(t *testing.T) {
			if got := RelativePathPatternMatches(
				test.pattern,
				test.file,
			); got != test.want {
				t.Fatalf(
					"RelativePathPatternMatches(%q, %q) = %v, want %v",
					test.pattern,
					test.file,
					got,
					test.want,
				)
			}
		})
	}
}
