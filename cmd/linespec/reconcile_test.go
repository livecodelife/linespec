package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/provenance"
)

// reconcile_test.go reproduces prov-2026-bde50f4d's third constraint: when the
// reconcile command is run without specifying a linespec config file, it must
// run per config file, applying write restrictions correctly for each
// directory containing a config file, rather than only ever the repo-root one.

// gitExecInDir runs a git command in dir, matching the helper convention used
// by pkg/provenance's own git-backed tests.
func gitExecInDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// buildFanOutMonorepo lays out and git-commits a two-config repo (repo root +
// pkg/foo/), each with its own provenance/ directory and one open record, then
// chdirs the test into it so provSetup()'s os.Getwd()-based repoRoot matches.
// Returns the repo root.
func buildFanOutMonorepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	gitExecInDir(t, dir, "init")
	gitExecInDir(t, dir, "config", "user.email", "test@example.com")
	gitExecInDir(t, dir, "config", "user.name", "Test User")
	gitExecInDir(t, dir, "config", "commit.gpgsign", "false")

	write := func(rel, content string) {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", rel, err)
		}
	}

	rootRecord := "id: prov-2026-aaaa0002\n" +
		"title: Root record\n" +
		"status: open\n" +
		"created_at: \"2026-01-01\"\n" +
		"author: test@example.com\n" +
		"intent: Cover root.go.\n" +
		"constraints:\n  - Must work\n" +
		"affected_scope:\n  - root.go\n" +
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

	fooRecord := "id: prov-2026-bbbb0002\n" +
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

	write(".linespec.yml", "provenance:\n  dir: provenance\n")
	write("pkg/foo/.linespec.yml", "provenance:\n  dir: provenance\n")
	write("root.go", "package root\n")
	write("uncovered.go", "package root\n")
	write("pkg/foo/foo.go", "package foo\n")
	write("pkg/foo/other.go", "package foo\n")
	write("provenance/prov-2026-aaaa0002.yml", rootRecord)
	write("pkg/foo/provenance/prov-2026-bbbb0002.yml", fooRecord)

	gitExecInDir(t, dir, "add", ".")
	gitExecInDir(t, dir, "commit", "-m", "init monorepo")

	return dir
}

func isWritable(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat %s: %v", path, err)
	}
	return info.Mode().Perm()&0o200 != 0
}

// TestRunProvReconcile_NoConfigFlagFansOutPerDiscoveredConfig reproduces
// prov-2026-bde50f4d constraint 3: running reconcile without -c must apply
// each directory's own closest config, not just the repo-root one — root.go
// gets the root record's scope, pkg/foo/foo.go gets pkg/foo's own record's
// scope, and neither pass locks a file outside its own directory.
func TestRunProvReconcile_NoConfigFlagFansOutPerDiscoveredConfig(t *testing.T) {
	dir := buildFanOutMonorepo(t)
	t.Chdir(dir)

	if ok := runProvReconcile(provenance.ReconcileOptions{}); !ok {
		t.Fatalf("runProvReconcile returned false")
	}

	cases := map[string]bool{
		"root.go":          true,  // covered by the root record's affected_scope
		"uncovered.go":     false, // in root's own directory, uncovered
		"pkg/foo/foo.go":   true,  // covered by pkg/foo's own record's affected_scope
		"pkg/foo/other.go": false, // in pkg/foo's own directory, uncovered
	}
	for rel, want := range cases {
		got := isWritable(t, filepath.Join(dir, rel))
		if got != want {
			t.Errorf("%s writable = %v, want %v", rel, got, want)
		}
	}
}
