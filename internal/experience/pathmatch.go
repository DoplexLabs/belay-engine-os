package experience

import (
	"path"
	"strings"
)

// RelativePathPatternMatches applies Belay's validated, slash-separated path
// pattern semantics. A ** segment matches zero or more complete path segments;
// other segments use path.Match semantics.
func RelativePathPatternMatches(pattern, file string) bool {
	patternParts := strings.Split(pattern, "/")
	fileParts := strings.Split(file, "/")
	var match func(int, int) bool
	match = func(patternIndex, fileIndex int) bool {
		if patternIndex == len(patternParts) {
			return fileIndex == len(fileParts)
		}
		if patternParts[patternIndex] == "**" {
			if match(patternIndex+1, fileIndex) {
				return true
			}
			return fileIndex < len(fileParts) &&
				match(patternIndex, fileIndex+1)
		}
		if fileIndex == len(fileParts) {
			return false
		}
		matched, err := path.Match(
			patternParts[patternIndex],
			fileParts[fileIndex],
		)
		return err == nil && matched &&
			match(patternIndex+1, fileIndex+1)
	}
	return match(0, 0)
}

func fileMatchesAnyRelativePathPattern(
	file string,
	patterns []string,
) bool {
	for _, pattern := range patterns {
		if RelativePathPatternMatches(pattern, file) {
			return true
		}
	}
	return false
}
