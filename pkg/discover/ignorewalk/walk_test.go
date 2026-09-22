package ignorewalk

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func collectFiles(t *testing.T, root string, skipDirNames map[string]bool) []string {
	t.Helper()
	var got []string
	err := Walk(root, skipDirNames, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			t.Fatal(relErr)
		}
		got = append(got, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	return got
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// TestWalk_RespectsRootGitignore reproduces the linq-lens scenario from
// prov-2026-f0b20266: a build output directory (.next/) is gitignored but
// not in the hardcoded denylist, so it must still be skipped once a
// .gitignore excludes it.
func TestWalk_RespectsRootGitignore(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".gitignore":           ".next/\n*.log\n",
		"app/page.tsx":         "export default function Page() {}\n",
		".next/chunks/main.js": "minified junk",
		"debug.log":            "noise",
	})

	got := collectFiles(t, root, map[string]bool{".git": true})

	if contains(got, ".next/chunks/main.js") {
		t.Errorf(".next/chunks/main.js should be excluded by .gitignore, got files: %v", got)
	}
	if contains(got, "debug.log") {
		t.Errorf("debug.log should be excluded by .gitignore, got files: %v", got)
	}
	if !contains(got, "app/page.tsx") {
		t.Errorf("app/page.tsx should be included, got files: %v", got)
	}
	if !contains(got, ".gitignore") {
		t.Errorf(".gitignore itself should be included (it is not self-excluding), got files: %v", got)
	}
}

// TestWalk_NestedGitignoreOverridesParent verifies a nested .gitignore can
// re-include a path a parent's broader rule would otherwise exclude,
// matching git's own last-match-wins precedence across nested .gitignore
// files. (A directory itself excluded by a parent pattern, e.g. "vendor/",
// is never descended into at all — by design, matching real git — so this
// exercises a file-level pattern instead, which vendor/ is not.)
func TestWalk_NestedGitignoreOverridesParent(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".gitignore":                "*.generated.go\n",
		"vendor/.gitignore":         "!keep.generated.go\n",
		"vendor/keep.generated.go":  "package vendor\n",
		"vendor/other.generated.go": "package vendor\n",
	})

	got := collectFiles(t, root, map[string]bool{".git": true})

	if !contains(got, "vendor/keep.generated.go") {
		t.Errorf("vendor/keep.generated.go should be re-included by the nested .gitignore, got files: %v", got)
	}
	if contains(got, "vendor/other.generated.go") {
		t.Errorf("vendor/other.generated.go should still be excluded by the root .gitignore, got files: %v", got)
	}
}

// TestWalk_FallbackDenylistAppliesWithNoGitignore ensures repos without any
// .gitignore file still get the hardcoded denylist applied — the fix must
// not regress that baseline behavior.
func TestWalk_FallbackDenylistAppliesWithNoGitignore(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"main.go":                 "package main\n",
		"node_modules/pkg/idx.js": "module.exports = {}\n",
	})

	got := collectFiles(t, root, map[string]bool{"node_modules": true, ".git": true})

	if contains(got, "node_modules/pkg/idx.js") {
		t.Errorf("node_modules should still be excluded by the fallback denylist, got files: %v", got)
	}
	if !contains(got, "main.go") {
		t.Errorf("main.go should be included, got files: %v", got)
	}
}

// TestWalk_DenylistAndGitignoreBothApply confirms the denylist remains
// active even when a .gitignore is present, per the constraint that it MAY
// remain as a fallback rather than being replaced.
func TestWalk_DenylistAndGitignoreBothApply(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".gitignore":              "*.log\n",
		"main.go":                 "package main\n",
		"node_modules/pkg/idx.js": "module.exports = {}\n",
	})

	got := collectFiles(t, root, map[string]bool{"node_modules": true, ".git": true})

	if contains(got, "node_modules/pkg/idx.js") {
		t.Errorf("node_modules should be excluded by the denylist even with a .gitignore present, got files: %v", got)
	}
}
