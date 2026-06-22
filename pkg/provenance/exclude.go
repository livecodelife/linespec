package provenance

import (
	"regexp"
	"strings"
)

// IsPathExcluded reports whether filePath matches any entry in excludePaths.
// Each entry may be:
//   - A regex, recognized by leading and trailing slashes (e.g. /.*_gen\.go$/)
//   - A glob pattern (contains * or ?)
//   - A directory prefix (e.g. "docs" or "docs/" matches any file under docs/)
//   - A full file path (exact match)
//
// Matching is evaluated in the order listed above. The first matching entry
// makes the file exempt. An invalid regex or glob is silently skipped (no match).
func IsPathExcluded(filePath string, excludePaths []string) bool {
	for _, pattern := range excludePaths {
		if matchesExcludePattern(filePath, pattern) {
			return true
		}
	}
	return false
}

// matchesExcludePattern checks whether filePath matches a single exclude_paths entry.
func matchesExcludePattern(filePath, pattern string) bool {
	if pattern == "" {
		return false
	}

	// Regex: /pattern/ (leading and trailing slash delimiters)
	if len(pattern) >= 2 && pattern[0] == '/' && pattern[len(pattern)-1] == '/' {
		re, err := regexp.Compile(pattern[1 : len(pattern)-1])
		if err != nil {
			return false
		}
		return re.MatchString(filePath)
	}

	// Glob: contains * or ?
	if strings.ContainsAny(pattern, "*?") {
		matched, err := MatchPattern(filePath, pattern)
		if err != nil {
			return false
		}
		return matched
	}

	// Exact match
	if filePath == pattern {
		return true
	}

	// Directory prefix: "docs" or "docs/" both match "docs/file.md"
	dirPrefix := strings.TrimSuffix(pattern, "/") + "/"
	return strings.HasPrefix(filePath, dirPrefix)
}
