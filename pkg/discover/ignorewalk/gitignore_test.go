package ignorewalk

import "testing"

func TestCompilePattern(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{"bare name matches at root", "node_modules", "node_modules", true},
		{"bare name matches nested", "node_modules", "packages/app/node_modules", true},
		{"bare name does not match partial", "node_modules", "node_modules_backup", false},
		{"leading slash anchors to root", "/build", "build", true},
		{"leading slash rejects nested", "/build", "packages/app/build", false},
		{"single star does not cross slash", "*.log", "app.log", true},
		{"single star does not cross slash nested", "*.log", "logs/app.log", true},
		{"single star does not match across a directory boundary", "app/*.log", "app/nested/app.log", false},
		{"leading doublestar matches any depth", "**/*.log", "a/b/c/app.log", true},
		{"trailing doublestar matches everything inside", "dist/**", "dist/assets/app.js", true},
		{"trailing doublestar matches the directory itself", "dist/**", "dist", true},
		{"question mark matches single char", "a?.txt", "ab.txt", true},
		{"question mark does not match slash", "a?.txt", "a/.txt", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			re, err := compilePattern(c.pattern)
			if err != nil {
				t.Fatalf("compilePattern(%q): %v", c.pattern, err)
			}
			got := re.MatchString(c.path)
			if got != c.want {
				t.Errorf("compilePattern(%q).MatchString(%q) = %v; want %v", c.pattern, c.path, got, c.want)
			}
		})
	}
}

func TestRuleSetEvaluate_Negation(t *testing.T) {
	rs, err := loadGitignoreFromLines([]string{
		"*.log",
		"!important.log",
	}, "")
	if err != nil {
		t.Fatalf("loadGitignoreFromLines: %v", err)
	}

	matched, ignored := rs.evaluate("app.log", false)
	if !matched || !ignored {
		t.Errorf("app.log: matched=%v ignored=%v; want matched=true ignored=true", matched, ignored)
	}

	matched, ignored = rs.evaluate("important.log", false)
	if !matched || ignored {
		t.Errorf("important.log: matched=%v ignored=%v; want matched=true ignored=false (un-ignored)", matched, ignored)
	}
}

func TestRuleSetEvaluate_DirOnly(t *testing.T) {
	rs, err := loadGitignoreFromLines([]string{"build/"}, "")
	if err != nil {
		t.Fatalf("loadGitignoreFromLines: %v", err)
	}

	if matched, _ := rs.evaluate("build", false); matched {
		t.Error("build/ pattern should not match a file named build")
	}
	if matched, ignored := rs.evaluate("build", true); !matched || !ignored {
		t.Errorf("build/ pattern should match directory build: matched=%v ignored=%v", matched, ignored)
	}
}

// loadGitignoreFromLines is a test helper mirroring loadGitignore's parsing
// without requiring a file on disk.
func loadGitignoreFromLines(lines []string, baseDir string) (*ruleSet, error) {
	var rules []rule
	for _, line := range lines {
		negate := false
		if len(line) > 0 && line[0] == '!' {
			negate = true
			line = line[1:]
		}
		dirOnly := false
		if len(line) > 0 && line[len(line)-1] == '/' {
			dirOnly = true
			line = line[:len(line)-1]
		}
		re, err := compilePattern(line)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule{re: re, negate: negate, dirOnly: dirOnly})
	}
	return &ruleSet{baseDir: baseDir, rules: rules}, nil
}
