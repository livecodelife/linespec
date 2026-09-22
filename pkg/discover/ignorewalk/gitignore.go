package ignorewalk

import (
	"os"
	"regexp"
	"strings"
)

// rule is one compiled, non-comment, non-blank line from a .gitignore file.
type rule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

// ruleSet is every rule contributed by one .gitignore file, scoped to
// baseDir — the walk-root-relative, "/"-separated directory the .gitignore
// file lives in ("" for the walk root itself).
type ruleSet struct {
	baseDir string
	rules   []rule
}

// loadGitignore parses the .gitignore at path, if it exists, into a ruleSet
// scoped to baseDir. It returns (nil, nil) when the file does not exist or
// contains no rules — an unparsable individual pattern line is skipped
// rather than failing the whole file, since a typo in one line of a
// project's .gitignore should not blind discover to every other rule in it.
func loadGitignore(path, baseDir string) (*ruleSet, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var rules []rule
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		line = strings.TrimRight(line, " \t")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		negate := false
		if strings.HasPrefix(line, "!") {
			negate = true
			line = line[1:]
		}

		dirOnly := false
		if strings.HasSuffix(line, "/") {
			dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		if line == "" {
			continue
		}

		re, err := compilePattern(line)
		if err != nil {
			continue
		}
		rules = append(rules, rule{re: re, negate: negate, dirOnly: dirOnly})
	}
	if len(rules) == 0 {
		return nil, nil
	}
	return &ruleSet{baseDir: baseDir, rules: rules}, nil
}

// evaluate reports whether any rule in rs matches relPath (relative to
// rs.baseDir, "/"-separated) and, if so, the resulting ignore verdict —
// standard gitignore semantics apply the last matching line in the file, so
// a later negated ("!") pattern un-ignores an earlier match.
func (rs *ruleSet) evaluate(relPath string, isDir bool) (matched, ignored bool) {
	for _, r := range rs.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if r.re.MatchString(relPath) {
			matched = true
			ignored = !r.negate
		}
	}
	return matched, ignored
}

// compilePattern translates one gitignore pattern (already stripped of its
// optional leading "!" and trailing "/") into a regexp matched against a
// "/"-joined path relative to the pattern's own .gitignore directory.
func compilePattern(pattern string) (*regexp.Regexp, error) {
	anchored := strings.HasPrefix(pattern, "/")
	pattern = strings.TrimPrefix(pattern, "/")
	anchored = anchored || strings.Contains(pattern, "/")

	body := translatePattern(pattern)
	if anchored {
		return regexp.Compile("^" + body + "$")
	}
	// No slash anywhere in the pattern: it matches the basename at any
	// depth under the .gitignore's directory, not just direct children.
	return regexp.Compile("^(?:.*/)?" + body + "$")
}

// translatePattern converts gitignore glob syntax (*, ?, [...], ** in its
// "**/", "/**", and "/**/ " forms) into the body of a regexp matched against
// a "/"-separated relative path.
func translatePattern(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); {
		switch {
		case strings.HasPrefix(pattern[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 3
		case strings.HasPrefix(pattern[i:], "/**"):
			b.WriteString("(?:/.*)?")
			i += 3
		case pattern[i] == '*':
			b.WriteString("[^/]*")
			i++
		case pattern[i] == '?':
			b.WriteString("[^/]")
			i++
		case pattern[i] == '[':
			end := strings.IndexByte(pattern[i:], ']')
			if end == -1 {
				b.WriteString(regexp.QuoteMeta(pattern[i:]))
				i = len(pattern)
				continue
			}
			b.WriteString(pattern[i : i+end+1])
			i += end + 1
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
			i++
		}
	}
	return b.String()
}
