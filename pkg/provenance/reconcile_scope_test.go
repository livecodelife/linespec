package provenance

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// reconcile_scope_test.go reproduces prov-2026-bde50f4d end to end: a monorepo
// where a nested directory (pkg/foo) has its own .linespec.yml and its own
// provenance/ records. Before the fix, the repo-root config's reconcile pass
// walked every git-tracked file in the repo and locked pkg/foo's files too,
// since nothing bounded the pass to the root config's own directory. The
// closest parent directory of a linespec config file must govern a path, and
// a directory-specific config must take precedence over a higher one for its
// own directory.

// buildScopedMonorepo lays out and git-commits a two-config repo:
//   - repo root: .linespec.yml + provenance/ with one open record covering
//     pkg/root.go
//   - pkg/foo/: its own .linespec.yml + provenance/ with one open record
//     covering pkg/foo/foo.go
//
// pkg/root.go, pkg/uncovered.go, pkg/foo/foo.go, pkg/foo/other.go all start
// writable (0o644) so reconcile's effect in each direction is observable.
// Returns the repo root plus a Commands wired for the root config and one
// wired for the pkg/foo config.
func buildScopedMonorepo(t *testing.T) (repo string, rootCmds, fooCmds *Commands) {
	t.Helper()
	clearGitEnvForTest(t)

	repo = t.TempDir()
	gitExec(t, repo, "init")
	gitExec(t, repo, "config", "user.email", "test@example.com")
	gitExec(t, repo, "config", "user.name", "Test User")
	gitExec(t, repo, "config", "commit.gpgsign", "false")

	rootProvDir := filepath.Join(repo, "provenance")
	fooDir := filepath.Join(repo, "pkg", "foo")
	fooProvDir := filepath.Join(fooDir, "provenance")
	for _, d := range []string{rootProvDir, fooProvDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", d, err)
		}
	}

	write := func(rel, content string, mode os.FileMode) {
		abs := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(abs, []byte(content), mode); err != nil {
			t.Fatalf("WriteFile %s: %v", rel, err)
		}
	}

	write(".linespec.yml", "provenance:\n  dir: provenance\n", 0o644)
	write("pkg/foo/.linespec.yml", "provenance:\n  dir: provenance\n", 0o644)
	write("pkg/root.go", "package root\n", 0o644)
	write("pkg/uncovered.go", "package root\n", 0o644)
	write("pkg/foo/foo.go", "package foo\n", 0o644)
	write("pkg/foo/other.go", "package foo\n", 0o644)
	write("provenance/prov-2026-aaaa0001.yml", rootRecordYAML(), 0o644)
	write("pkg/foo/provenance/prov-2026-bbbb0001.yml", fooRecordYAML(), 0o644)

	gitExec(t, repo, "add", ".")
	gitExec(t, repo, "commit", "-m", "init monorepo")

	rootLoader := NewLoader(rootProvDir, nil)
	if err := rootLoader.LoadAll(); err != nil {
		t.Fatalf("root LoadAll: %v", err)
	}
	rootCmds = &Commands{
		Loader:    rootLoader,
		Formatter: NewFormatter(os.Stdout, false),
		Config:    &ProvenanceConfig{Dir: rootProvDir, ConfigFileDir: "."},
		RepoRoot:  repo,
	}

	fooLoader := NewLoader(fooProvDir, nil)
	if err := fooLoader.LoadAll(); err != nil {
		t.Fatalf("foo LoadAll: %v", err)
	}
	fooCmds = &Commands{
		Loader:    fooLoader,
		Formatter: NewFormatter(os.Stdout, false),
		Config:    &ProvenanceConfig{Dir: fooProvDir, ConfigFileDir: "pkg/foo"},
		RepoRoot:  repo,
	}

	return repo, rootCmds, fooCmds
}

func rootRecordYAML() string {
	return "id: prov-2026-aaaa0001\n" +
		"title: Root record\n" +
		"status: open\n" +
		"created_at: \"2026-01-01\"\n" +
		"author: test@example.com\n" +
		"intent: Cover pkg/root.go.\n" +
		"constraints:\n  - Must work\n" +
		"affected_scope:\n  - pkg/root.go\n" +
		"forbidden_scope: []\n" +
		"type: blueprint\n" +
		"supersedes: \"\"\n" +
		"superseded_by: \"\"\n" +
		"related: []\n" +
		"sealed_at_sha: \"\"\n" +
		"associated_specs: []\n" +
		"associated_traces: []\n" +
		"monitors: []\n" +
		"tags: []\n"
}

func fooRecordYAML() string {
	return "id: prov-2026-bbbb0001\n" +
		"title: Foo record\n" +
		"status: open\n" +
		"created_at: \"2026-01-01\"\n" +
		"author: test@example.com\n" +
		"intent: Cover pkg/foo/foo.go.\n" +
		"constraints:\n  - Must work\n" +
		"affected_scope:\n  - pkg/foo/foo.go\n" +
		"forbidden_scope: []\n" +
		"type: blueprint\n" +
		"supersedes: \"\"\n" +
		"superseded_by: \"\"\n" +
		"related: []\n" +
		"sealed_at_sha: \"\"\n" +
		"associated_specs: []\n" +
		"associated_traces: []\n" +
		"monitors: []\n" +
		"tags: []\n"
}

// TestCommandsReconcile_RootPassNeverTouchesNestedConfigDirectory reproduces
// the reported bug directly: reconciling the repo-root config must not lock
// (or otherwise alter) any file under pkg/foo, since pkg/foo/.linespec.yml is
// a closer parent config for that subtree.
func TestCommandsReconcile_RootPassNeverTouchesNestedConfigDirectory(t *testing.T) {
	repo, rootCmds, _ := buildScopedMonorepo(t)

	result, err := rootCmds.reconcile()
	if err != nil {
		t.Fatalf("root reconcile: %v", err)
	}

	if !isWritableOnDisk(t, filepath.Join(repo, "pkg", "root.go")) {
		t.Errorf("pkg/root.go should stay writable (covered by the root record's affected_scope)")
	}
	if isWritableOnDisk(t, filepath.Join(repo, "pkg", "uncovered.go")) {
		t.Errorf("pkg/uncovered.go should be locked (in root's own directory, not covered by any open record)")
	}

	for _, rel := range []string{"pkg/foo/foo.go", "pkg/foo/other.go"} {
		if !isWritableOnDisk(t, filepath.Join(repo, rel)) {
			t.Errorf("%s must be untouched by the root reconcile pass — it is governed by the closer pkg/foo config, not root", rel)
		}
	}
	for _, rel := range []string{"pkg/foo/foo.go", "pkg/foo/other.go"} {
		if slices.Contains(result.Locked, rel) {
			t.Errorf("root reconcile result must never report %s as locked, got Locked=%v", rel, result.Locked)
		}
	}
}

// TestCommandsReconcile_NestedConfigGovernsOnlyItsOwnDirectory reproduces the
// other half: pkg/foo's own reconcile pass must apply its own record's scope
// (unlocking pkg/foo/foo.go, locking pkg/foo/other.go) without reaching
// outside pkg/foo into the repo root's files.
func TestCommandsReconcile_NestedConfigGovernsOnlyItsOwnDirectory(t *testing.T) {
	repo, _, fooCmds := buildScopedMonorepo(t)

	if _, err := fooCmds.reconcile(); err != nil {
		t.Fatalf("foo reconcile: %v", err)
	}

	if !isWritableOnDisk(t, filepath.Join(repo, "pkg", "foo", "foo.go")) {
		t.Errorf("pkg/foo/foo.go should be writable (covered by pkg/foo's own open record)")
	}
	if isWritableOnDisk(t, filepath.Join(repo, "pkg", "foo", "other.go")) {
		t.Errorf("pkg/foo/other.go should be locked (in pkg/foo's own directory, not covered by its record)")
	}

	// Root-owned files were never in this pass's boundary at all.
	if !isWritableOnDisk(t, filepath.Join(repo, "pkg", "root.go")) {
		t.Errorf("pkg/root.go must be untouched by the pkg/foo reconcile pass")
	}
	if !isWritableOnDisk(t, filepath.Join(repo, "pkg", "uncovered.go")) {
		t.Errorf("pkg/uncovered.go must be untouched by the pkg/foo reconcile pass")
	}
}

// TestCommandsReconcile_BothPassesTogetherPartitionTheWholeTree runs both
// configs' reconcile passes back to back (in either order) and asserts the
// final state matches each directory's own closest config — the fan-out
// behavior `linespec provenance reconcile` must exhibit when run without -c
// (prov-2026-bde50f4d constraint 3), applied here directly against Commands
// rather than the CLI layer.
func TestCommandsReconcile_BothPassesTogetherPartitionTheWholeTree(t *testing.T) {
	repo, rootCmds, fooCmds := buildScopedMonorepo(t)

	// Order must not matter: run foo before root.
	if _, err := fooCmds.reconcile(); err != nil {
		t.Fatalf("foo reconcile: %v", err)
	}
	if _, err := rootCmds.reconcile(); err != nil {
		t.Fatalf("root reconcile: %v", err)
	}

	writable := map[string]bool{
		"pkg/root.go":      true,
		"pkg/uncovered.go": false,
		"pkg/foo/foo.go":   true,
		"pkg/foo/other.go": false,
		// AlwaysWritablePaths derives each config's own .linespec.yml entry
		// from its ConfigFileDir, so the repo-root config's .linespec.yml and
		// the nested pkg/foo config's own .linespec.yml are both always
		// writable at their own actual paths (prov-2026-c65450b6) — matching
		// the documented "always-writable" exemption for the config file that
		// enables the restriction, not just the repo-root one.
		".linespec.yml":         true,
		"pkg/foo/.linespec.yml": true,
	}
	for rel, want := range writable {
		got := isWritableOnDisk(t, filepath.Join(repo, rel))
		if got != want {
			t.Errorf("%s writable = %v, want %v", rel, got, want)
		}
	}
}
