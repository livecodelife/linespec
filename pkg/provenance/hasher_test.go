package provenance

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func makeTestRecord(id, status string) *Record {
	return &Record{
		ID:     id,
		Title:  "Test record " + id,
		Status: Status(status),
		Type:   RecordTypeImprint,
	}
}

func TestCompileManifest_MissingManifest(t *testing.T) {
	dir := t.TempDir()
	h := NewHasher(dir)

	records := []*Record{
		makeTestRecord("prov-2026-aaa00001", "implemented"),
		makeTestRecord("prov-2026-aaa00002", "open"),
	}

	changed, err := h.CompileManifest(records)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true when manifest did not exist")
	}

	if !h.ManifestExists() {
		t.Fatal("manifest file should exist after compile")
	}

	m, err := h.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest after compile: %v", err)
	}
	if len(m.Records) != 2 {
		t.Fatalf("expected 2 records in manifest, got %d", len(m.Records))
	}
}

func TestCompileManifest_Idempotent(t *testing.T) {
	dir := t.TempDir()
	h := NewHasher(dir)

	records := []*Record{
		makeTestRecord("prov-2026-bbb00001", "implemented"),
	}

	// First compile — should write
	changed, err := h.CompileManifest(records)
	if err != nil {
		t.Fatalf("first compile: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true on first compile")
	}

	// Record mtime before second compile
	manifestPath := filepath.Join(dir, ".linespec", "hash_manifest.json")
	info1, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatalf("stat manifest: %v", err)
	}

	// Second compile with same records — should be a no-op
	changed, err = h.CompileManifest(records)
	if err != nil {
		t.Fatalf("second compile: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false when manifest is already up to date")
	}

	info2, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatalf("stat manifest after second compile: %v", err)
	}
	if info2.ModTime() != info1.ModTime() {
		t.Fatal("manifest file should not have been rewritten on idempotent compile")
	}
}

func TestCompileManifest_StaleEntry(t *testing.T) {
	dir := t.TempDir()
	h := NewHasher(dir)

	r := makeTestRecord("prov-2026-ccc00001", "implemented")
	records := []*Record{r}

	// Compile once to establish baseline
	if _, err := h.CompileManifest(records); err != nil {
		t.Fatalf("initial compile: %v", err)
	}

	// Mutate the record so its hash changes
	r.Title = "Modified title"

	changed, err := h.CompileManifest(records)
	if err != nil {
		t.Fatalf("compile after mutation: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true after record was mutated")
	}

	// Verify stored hash now matches the mutated record
	stored, current, ok, err := h.VerifyRecord(r)
	if err != nil {
		t.Fatalf("VerifyRecord: %v", err)
	}
	if !ok {
		t.Fatal("record should be in manifest after compile")
	}
	if stored != current {
		t.Fatalf("stored hash %s does not match current %s after compile", stored, current)
	}
}

// TestWriteManifest_LineBasedFormat pins the on-disk shape the rest of the
// merge-safety guarantees depend on: one compact JSON object per record, one
// per line, sorted by ID, with no wrapping object and no aggregate hash
// fields. A single JSON document with a nested "records" map plus
// full_graph_hash/active_subset_hash fields (the pre-#163 format) is exactly
// what made every seal touch the same two lines regardless of which record
// was sealed.
func TestWriteManifest_LineBasedFormat(t *testing.T) {
	dir := t.TempDir()
	h := NewHasher(dir)

	a := makeTestRecord("prov-2026-line0002", "implemented")
	b := makeTestRecord("prov-2026-line0001", "implemented")
	if err := h.SealRecord(a, []*Record{a}); err != nil {
		t.Fatalf("SealRecord a: %v", err)
	}
	if err := h.SealRecord(b, []*Record{b}); err != nil {
		t.Fatalf("SealRecord b: %v", err)
	}

	data, err := os.ReadFile(h.ManifestPath())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines in manifest, got %d: %q", len(lines), string(data))
	}
	if !strings.Contains(lines[0], "prov-2026-line0001") || !strings.Contains(lines[1], "prov-2026-line0002") {
		t.Errorf("expected lines sorted by ID, got %q", lines)
	}
	if bytes.Contains(data, []byte("full_graph_hash")) || bytes.Contains(data, []byte("active_subset_hash")) {
		t.Errorf("manifest must not persist aggregate hash fields — they force every seal to touch the same lines, guaranteeing merge conflicts (issue #163); got %s", data)
	}
	for _, line := range lines {
		if strings.HasSuffix(strings.TrimSpace(line), ",") {
			t.Errorf("manifest line must not depend on a trailing comma shared with the next line (breaks independent insertion): %q", line)
		}
	}
}

// TestLoadManifest_ParsesLegacyFormat verifies a manifest written by a
// pre-#163 build of LineSpec (a single JSON document with a top-level
// "records" object and aggregate hash fields) still loads correctly, so
// existing repos are not forced through a manual migration step — the next
// write converts it to the new line-based format automatically.
func TestLoadManifest_ParsesLegacyFormat(t *testing.T) {
	dir := t.TempDir()
	h := NewHasher(dir)

	legacy := `{
  "records": {
    "prov-2026-legacy01": "abc123",
    "prov-2026-legacy02": "def456"
  },
  "full_graph_hash": "stale",
  "active_subset_hash": "stale"
}`
	if err := os.MkdirAll(filepath.Dir(h.ManifestPath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.ManifestPath(), []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := h.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Records["prov-2026-legacy01"] != "abc123" || m.Records["prov-2026-legacy02"] != "def456" {
		t.Fatalf("expected legacy records to load, got %+v", m.Records)
	}

	// Sealing a new record should migrate the whole manifest to the
	// line-based format, preserving the pre-existing entries.
	r := makeTestRecord("prov-2026-legacy03", "implemented")
	if err := h.SealRecord(r, []*Record{r}); err != nil {
		t.Fatalf("SealRecord: %v", err)
	}
	data, err := os.ReadFile(h.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("full_graph_hash")) {
		t.Errorf("expected legacy manifest to be migrated away from the aggregate-hash format on next write, got %s", data)
	}
	m2, err := h.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest after migration: %v", err)
	}
	if len(m2.Records) != 3 {
		t.Fatalf("expected 3 records after migration seal, got %d: %+v", len(m2.Records), m2.Records)
	}
}

// runGit runs a git command in dir, failing the test on error, and returning
// combined stdout+stderr.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// runGitMerge runs `git merge --no-edit <branch>` in dir without failing the
// test on a merge conflict, so callers can assert on the outcome themselves.
func runGitMerge(t *testing.T, dir, branch string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", "merge", "--no-edit", branch)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestSealRecord_ConcurrentBranchesMergeWithoutConflict reproduces the
// steady-state scenario from issue #163 end to end with real git: two
// branches, each sealing a different record off a common base, must merge
// back together without a text conflict in the manifest. Before the fix this
// conflicted on essentially every rebase because both branches rewrote the
// same full_graph_hash/active_subset_hash lines regardless of which record
// each branch sealed. The new records are chosen to land in different gaps
// of the sorted manifest (matching real random-hex record IDs spread across
// many existing records), so their single-line insertions do not even share
// a diff anchor with each other — see
// TestSealRecord_AdjacentInsertions_UnionMergeWithoutConflict for the case
// where two insertions do land at the same anchor.
func TestSealRecord_ConcurrentBranchesMergeWithoutConflict(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "test")

	h := NewHasher(repo)
	base := []*Record{
		makeTestRecord("prov-2026-aaa0001", "implemented"),
		makeTestRecord("prov-2026-mmm0001", "implemented"),
		makeTestRecord("prov-2026-zzz0001", "implemented"),
	}
	if _, err := h.CompileManifest(base); err != nil {
		t.Fatalf("CompileManifest: %v", err)
	}
	runGit(t, repo, "add", ".linespec/hash_manifest.json")
	runGit(t, repo, "commit", "-q", "-m", "base manifest")
	runGit(t, repo, "branch", "-M", "main")

	// Branch A seals a new record sorting between "aaa0001" and "mmm0001".
	runGit(t, repo, "checkout", "-q", "-b", "branch-a")
	recA := makeTestRecord("prov-2026-ddd0001", "implemented")
	if err := h.SealRecord(recA, append(base, recA)); err != nil {
		t.Fatalf("SealRecord A: %v", err)
	}
	runGit(t, repo, "commit", "-q", "-am", "seal branch-a record")

	// Branch B, off the same base commit, seals a different new record
	// sorting between "mmm0001" and "zzz0001" — a different insertion point.
	runGit(t, repo, "checkout", "-q", "main")
	runGit(t, repo, "checkout", "-q", "-b", "branch-b")
	recB := makeTestRecord("prov-2026-ppp0001", "implemented")
	if err := h.SealRecord(recB, append(base, recB)); err != nil {
		t.Fatalf("SealRecord B: %v", err)
	}
	runGit(t, repo, "commit", "-q", "-am", "seal branch-b record")

	// Merge branch-b into branch-a — this is the operation that used to
	// conflict on every seal.
	runGit(t, repo, "checkout", "-q", "branch-a")
	out, err := runGitMerge(t, repo, "branch-b")
	if err != nil {
		t.Fatalf("expected clean merge of two branches sealing different records, got conflict:\n%s", out)
	}

	m, err := h.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest after merge: %v", err)
	}
	for _, id := range []string{"prov-2026-aaa0001", "prov-2026-mmm0001", "prov-2026-zzz0001", "prov-2026-ddd0001", "prov-2026-ppp0001"} {
		if _, ok := m.Records[id]; !ok {
			t.Errorf("expected merged manifest to contain %s, got %+v", id, m.Records)
		}
	}
}

// TestSealRecord_AdjacentInsertions_UnionMergeWithoutConflict covers the
// residual case two independent single-line insertions can still collide on:
// both branches' new record happens to sort into the very same gap (e.g. both
// after the current last entry), so git's default 3-way merge sees two
// insertions at the same anchor line and reports a conflict — it has no way
// to know which one should come first. This is exactly what `.gitattributes`
// wires `merge=union` for. Because each manifest line is a fully
// self-contained JSON object with no shared punctuation (no trailing commas,
// no wrapping array/object), a union merge — which just keeps every added
// line from both sides — can never produce invalid JSON here, unlike the old
// single-JSON-document format the issue reported ("merge=union produces
// invalid JSON: duplicate keys, broken sort").
func TestSealRecord_AdjacentInsertions_UnionMergeWithoutConflict(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte(".linespec/hash_manifest.json merge=union\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".gitattributes")

	h := NewHasher(repo)
	base := []*Record{makeTestRecord("prov-2026-base0001", "implemented")}
	if _, err := h.CompileManifest(base); err != nil {
		t.Fatalf("CompileManifest: %v", err)
	}
	runGit(t, repo, "add", ".linespec/hash_manifest.json")
	runGit(t, repo, "commit", "-q", "-m", "base manifest")
	runGit(t, repo, "branch", "-M", "main")

	// Both branches seal a record sorting after the only base entry — the
	// same insertion anchor.
	runGit(t, repo, "checkout", "-q", "-b", "branch-a")
	recA := makeTestRecord("prov-2026-zzz0001a", "implemented")
	if err := h.SealRecord(recA, append(base, recA)); err != nil {
		t.Fatalf("SealRecord A: %v", err)
	}
	runGit(t, repo, "commit", "-q", "-am", "seal branch-a record")

	runGit(t, repo, "checkout", "-q", "main")
	runGit(t, repo, "checkout", "-q", "-b", "branch-b")
	recB := makeTestRecord("prov-2026-zzz0001b", "implemented")
	if err := h.SealRecord(recB, append(base, recB)); err != nil {
		t.Fatalf("SealRecord B: %v", err)
	}
	runGit(t, repo, "commit", "-q", "-am", "seal branch-b record")

	runGit(t, repo, "checkout", "-q", "branch-a")
	out, err := runGitMerge(t, repo, "branch-b")
	if err != nil {
		t.Fatalf("expected merge=union to auto-resolve same-anchor insertions without conflict, got:\n%s", out)
	}

	m, err := h.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest after union merge: %v (manifest may be corrupt)", err)
	}
	for _, id := range []string{"prov-2026-base0001", "prov-2026-zzz0001a", "prov-2026-zzz0001b"} {
		if _, ok := m.Records[id]; !ok {
			t.Errorf("expected union-merged manifest to contain %s, got %+v", id, m.Records)
		}
	}
}
