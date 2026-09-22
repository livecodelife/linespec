package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFindAllLinespecConfigs_RespectsGitignore reproduces prov-2026-f0b20266's
// third walk site: findAllLinespecConfigs only ever excluded ".git" by name,
// so a gitignored build/vendor directory containing a stray .linespec.yml
// (e.g. one vendored inside a dependency's build output) would be picked up
// as if it were a real project config.
func TestFindAllLinespecConfigs_RespectsGitignore(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		".gitignore":               "vendor/\n",
		".linespec.yml":            "service:\n  name: real\n",
		"vendor/pkg/.linespec.yml": "service:\n  name: vendored\n",
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := findAllLinespecConfigs(dir)

	if len(got) != 1 {
		t.Fatalf("expected exactly 1 config (the gitignored vendor/ one excluded), got %v", got)
	}
	if got[0] != filepath.Join(dir, ".linespec.yml") {
		t.Errorf("expected the real root .linespec.yml, got %s", got[0])
	}
}
