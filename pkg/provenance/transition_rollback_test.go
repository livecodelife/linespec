package provenance

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTransitionTestRepo builds a real git repo with an initial commit and returns
// a fully wired Commands (Loader + Linter+Hasher + Git + Config) plus the repo
// root and the buffer the formatter writes to.
func newTransitionTestRepo(t *testing.T, commitOnStatusChange bool) (*Commands, string, *bytes.Buffer) {
	t.Helper()
	clearGitEnvForTest(t)

	repo := t.TempDir()
	gitExec(t, repo, "init")
	gitExec(t, repo, "config", "user.email", "test@example.com")
	gitExec(t, repo, "config", "user.name", "Test User")
	gitExec(t, repo, "config", "commit.gpgsign", "false")

	provDir := filepath.Join(repo, "provenance")
	if err := os.MkdirAll(provDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// An initial commit so HEAD exists (Complete needs a HEAD SHA to seal against).
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("WriteFile README: %v", err)
	}
	gitExec(t, repo, "add", "README.md")
	gitExec(t, repo, "commit", "-m", "init")

	loader := NewLoader(provDir, nil)
	linter := NewLinter(loader, "strict")
	linter.Hasher = NewHasher(repo)

	var buf bytes.Buffer
	cmds := &Commands{
		Loader:    loader,
		Linter:    linter,
		Git:       NewGit(repo),
		Formatter: NewFormatter(&buf, false),
		Config: &ProvenanceConfig{
			Enforcement:          "strict",
			Dir:                  provDir,
			CommitOnStatusChange: commitOnStatusChange,
		},
		RepoRoot: repo,
	}
	return cmds, repo, &buf
}

// writeAndCommitRecord writes a record file into provenance/ and commits it so it
// is part of HEAD, then loads it into the Commands' loader.
func writeAndCommitRecord(t *testing.T, cmds *Commands, repo, id, yaml string) string {
	t.Helper()
	rel := "provenance/" + id + ".yml"
	abs := filepath.Join(repo, rel)
	if err := os.WriteFile(abs, []byte(yaml), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", rel, err)
	}
	gitExec(t, repo, "add", rel)
	gitExec(t, repo, "commit", "-m", "add record ["+id+"]")
	if err := cmds.Loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	return abs
}

// installRejectingPreCommitHook writes a pre-commit hook that always rejects the
// commit, simulating a hook that blocks a status-change auto-commit.
func installRejectingPreCommitHook(t *testing.T, repo string) {
	t.Helper()
	hookDir := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatalf("MkdirAll hooks: %v", err)
	}
	hook := "#!/bin/sh\necho 'pre-commit: rejected for test' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(hookDir, "pre-commit"), []byte(hook), 0o755); err != nil {
		t.Fatalf("WriteFile hook: %v", err)
	}
}

// validRecord returns a record that passes lint in any status.
func validRecord(id, status, recordType string) string {
	return "id: " + id + "\n" +
		"title: Test record\n" +
		"status: " + status + "\n" +
		"created_at: \"2026-01-01\"\n" +
		"author: test@example.com\n" +
		"intent: Build the thing.\n" +
		"constraints:\n  - Must work\n" +
		"affected_scope:\n  - pkg/thing.go\n" +
		"forbidden_scope: []\n" +
		"type: " + recordType + "\n" +
		"supersedes: \"\"\n" +
		"superseded_by: \"\"\n" +
		"related: []\n" +
		"sealed_at_sha: \"\"\n" +
		"associated_specs: []\n" +
		"associated_traces: []\n" +
		"monitors: []\n" +
		"tags: []\n"
}

// recordWithSpec returns a record that passes lint and declares one associated_spec
// at specPath, run via run_command. The path must exist on disk (Complete's
// existence check runs before the spec is ever executed) — callers create it.
func recordWithSpec(id, status, specPath, runCommand string) string {
	return "id: " + id + "\n" +
		"title: Test record\n" +
		"status: " + status + "\n" +
		"created_at: \"2026-01-01\"\n" +
		"author: test@example.com\n" +
		"intent: Build the thing.\n" +
		"constraints:\n  - Must work\n" +
		"affected_scope:\n  - pkg/thing.go\n" +
		"forbidden_scope: []\n" +
		"type: blueprint\n" +
		"supersedes: \"\"\n" +
		"superseded_by: \"\"\n" +
		"related: []\n" +
		"sealed_at_sha: \"\"\n" +
		"associated_specs:\n" +
		"  - path: " + specPath + "\n" +
		"    run_command: \"" + runCommand + "\"\n" +
		"associated_traces: []\n" +
		"monitors: []\n" +
		"tags: []\n"
}

// invalidImprint returns an imprint whose implements points at a record that does
// not exist, which is an error-level lint violation (PROV022) regardless of status.
func invalidImprint(id, status string) string {
	return "id: " + id + "\n" +
		"title: Invalid imprint\n" +
		"status: " + status + "\n" +
		"created_at: \"2026-01-01\"\n" +
		"author: test@example.com\n" +
		"intent: Dangling implements.\n" +
		"constraints: []\n" +
		"affected_scope: []\n" +
		"forbidden_scope: []\n" +
		"type: imprint\n" +
		"implements: prov-2026-doesnotexist\n" +
		"supersedes: \"\"\n" +
		"superseded_by: \"\"\n" +
		"related: []\n" +
		"sealed_at_sha: \"\"\n" +
		"associated_specs: []\n" +
		"associated_traces: []\n" +
		"monitors: []\n" +
		"tags: []\n"
}

func stagedFiles(t *testing.T, repo string) string {
	t.Helper()
	return gitOutput(t, repo, "diff", "--cached", "--name-only")
}

func loadedRecord(t *testing.T, cmds *Commands, id string) *Record {
	t.Helper()
	r, ok := cmds.Loader.GetRecord(id)
	if !ok {
		t.Fatalf("record %s not loaded", id)
	}
	return r
}

// --- Git.Unstage -----------------------------------------------------------

func TestGit_Unstage(t *testing.T) {
	clearGitEnvForTest(t)
	repo := t.TempDir()
	gitExec(t, repo, "init")
	gitExec(t, repo, "config", "user.email", "test@example.com")
	gitExec(t, repo, "config", "user.name", "Test User")
	gitExec(t, repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, repo, "add", "a.txt")
	if got := stagedFiles(t, repo); got != "a.txt" {
		t.Fatalf("precondition: expected a.txt staged, got %q", got)
	}

	if err := NewGit(repo).Unstage("a.txt"); err != nil {
		t.Fatalf("Unstage: %v", err)
	}
	if got := stagedFiles(t, repo); got != "" {
		t.Fatalf("expected nothing staged after Unstage, got %q", got)
	}
	// Working-tree content must be untouched.
	if data, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(data) != "x" {
		t.Fatalf("Unstage altered working tree: %q", data)
	}
}

// --- Complete --------------------------------------------------------------

func TestComplete_RollsBackOnCommitRejection(t *testing.T) {
	cmds, repo, buf := newTransitionTestRepo(t, true)
	id := "prov-2026-aaaa0001"
	abs := writeAndCommitRecord(t, cmds, repo, id, validRecord(id, "open", "blueprint"))
	before, _ := os.ReadFile(abs)

	installRejectingPreCommitHook(t, repo)

	err := cmds.Complete(CompleteOptions{RecordID: id})
	if err == nil {
		t.Fatal("expected Complete to fail when the auto-commit is rejected")
	}

	// On-disk record restored byte-for-byte (still open, not sealed).
	after, _ := os.ReadFile(abs)
	if !bytes.Equal(before, after) {
		t.Errorf("record file not restored after rollback:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	// In-memory record reflects the rolled-back status.
	if got := loadedRecord(t, cmds, id).Status; got != StatusOpen {
		t.Errorf("in-memory status = %q, want open", got)
	}
	if got := loadedRecord(t, cmds, id).SealedAtSHA; got != "" {
		t.Errorf("sealed_at_sha = %q, want empty after rollback", got)
	}
	// Manifest created by the seal must be removed again.
	if cmds.Linter.Hasher.ManifestExists() {
		t.Errorf("hash manifest should not exist after rollback")
	}
	// Nothing left staged.
	if got := stagedFiles(t, repo); got != "" {
		t.Errorf("expected clean index after rollback, staged: %q", got)
	}
	if msg := buf.String(); !strings.Contains(msg, "rolled back") || !strings.Contains(msg, "still open") {
		t.Errorf("error message should explain the rollback, got:\n%s", msg)
	}
}

func TestComplete_RollsBackOnLintFailure_WithoutAutoCommit(t *testing.T) {
	// commit_on_status_change is OFF — the lint guard must still protect the record.
	cmds, repo, buf := newTransitionTestRepo(t, false)
	id := "prov-2026-aaaa0002"
	abs := writeAndCommitRecord(t, cmds, repo, id, invalidImprint(id, "open"))
	before, _ := os.ReadFile(abs)

	err := cmds.Complete(CompleteOptions{RecordID: id})
	if err == nil {
		t.Fatal("expected Complete to fail when the sealed record would not pass lint")
	}

	after, _ := os.ReadFile(abs)
	if !bytes.Equal(before, after) {
		t.Errorf("record file not restored after lint-failure rollback:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	if got := loadedRecord(t, cmds, id).Status; got != StatusOpen {
		t.Errorf("in-memory status = %q, want open (transition must not stick)", got)
	}
	if cmds.Linter.Hasher.ManifestExists() {
		t.Errorf("hash manifest should not exist after lint-failure rollback")
	}
	if msg := buf.String(); !strings.Contains(msg, "does not pass validation") {
		t.Errorf("error message should explain the validation failure, got:\n%s", msg)
	}
}

func TestComplete_SucceedsAndCommitsWhenValid(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, true)
	id := "prov-2026-aaaa0003"
	abs := writeAndCommitRecord(t, cmds, repo, id, validRecord(id, "open", "blueprint"))

	if err := cmds.Complete(CompleteOptions{RecordID: id}); err != nil {
		t.Fatalf("Complete should succeed for a valid record: %v", err)
	}

	if got := loadedRecord(t, cmds, id).Status; got != StatusImplemented {
		t.Errorf("in-memory status = %q, want implemented", got)
	}
	if data, _ := os.ReadFile(abs); !strings.Contains(string(data), "status: implemented") {
		t.Errorf("on-disk record should be implemented:\n%s", data)
	}
	if !cmds.Linter.Hasher.ManifestExists() {
		t.Errorf("seal should have written the hash manifest")
	}
	if head := gitOutput(t, repo, "log", "-1", "--format=%s"); !strings.Contains(head, "Complete provenance record ["+id+"]") {
		t.Errorf("HEAD commit = %q, want the completion commit", head)
	}
	if got := stagedFiles(t, repo); got != "" {
		t.Errorf("index should be clean after a successful commit, staged: %q", got)
	}
}

func TestComplete_RollsBackWhenOwnAssociatedSpecFails(t *testing.T) {
	// Reproduces prov-2026-8d5c9376: a record whose own associated_spec fails must
	// not be reported as verified and sealed — Complete must actually execute the
	// spec (via the same mechanism as run-specs), roll back, and exit nonzero,
	// exactly like run-specs would report ✗ failed / exit 1 for the same record.
	cmds, repo, buf := newTransitionTestRepo(t, true)
	t.Chdir(repo)
	cmds.Config.RunAssociatedSpecsOnComplete = true
	id := "prov-2026-aaaa0007"

	specPath := filepath.Join(repo, "spec", "dummy.txt")
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(specPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile spec: %v", err)
	}
	gitExec(t, repo, "add", "spec/dummy.txt")
	gitExec(t, repo, "commit", "-m", "add spec fixture")

	abs := writeAndCommitRecord(t, cmds, repo, id, recordWithSpec(id, "open", "spec/dummy.txt", "false"))
	before, _ := os.ReadFile(abs)

	err := cmds.Complete(CompleteOptions{RecordID: id})
	if err == nil {
		t.Fatal("expected Complete to fail when the record's own associated spec fails")
	}

	after, _ := os.ReadFile(abs)
	if !bytes.Equal(before, after) {
		t.Errorf("record file not restored after own-spec-failure rollback:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	if got := loadedRecord(t, cmds, id).Status; got != StatusOpen {
		t.Errorf("in-memory status = %q, want open (transition must not stick)", got)
	}
	if got := loadedRecord(t, cmds, id).SealedAtSHA; got != "" {
		t.Errorf("sealed_at_sha = %q, want empty after rollback", got)
	}
	if cmds.Linter.Hasher.ManifestExists() {
		t.Errorf("hash manifest should not exist after own-spec-failure rollback")
	}
	if got := stagedFiles(t, repo); got != "" {
		t.Errorf("expected clean index after rollback, staged: %q", got)
	}
	if head := gitOutput(t, repo, "log", "-1", "--format=%s"); strings.Contains(head, "Complete provenance record ["+id+"]") {
		t.Errorf("no completion commit should have been made, HEAD = %q", head)
	}
	if msg := buf.String(); !strings.Contains(msg, "associated specs failed") || !strings.Contains(msg, "rolled back") || !strings.Contains(msg, "still open") {
		t.Errorf("error message should explain the spec failure and rollback, got:\n%s", msg)
	}
}

func TestComplete_SucceedsWhenOwnAssociatedSpecPasses(t *testing.T) {
	cmds, repo, buf := newTransitionTestRepo(t, true)
	t.Chdir(repo)
	cmds.Config.RunAssociatedSpecsOnComplete = true
	id := "prov-2026-aaaa0008"

	specPath := filepath.Join(repo, "spec", "dummy.txt")
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(specPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile spec: %v", err)
	}
	gitExec(t, repo, "add", "spec/dummy.txt")
	gitExec(t, repo, "commit", "-m", "add spec fixture")

	writeAndCommitRecord(t, cmds, repo, id, recordWithSpec(id, "open", "spec/dummy.txt", "true"))

	if err := cmds.Complete(CompleteOptions{RecordID: id}); err != nil {
		t.Fatalf("Complete should succeed when the record's own associated spec passes: %v", err)
	}

	if got := loadedRecord(t, cmds, id).Status; got != StatusImplemented {
		t.Errorf("in-memory status = %q, want implemented", got)
	}
	// FormatCompleteSuccess must report the ACTUAL executed outcome (passed), not
	// merely that the path exists on disk.
	if msg := buf.String(); !strings.Contains(msg, "✓ passed") {
		t.Errorf("success output should report the spec's real pass outcome, got:\n%s", msg)
	}
}

func TestComplete_GateDisabled_ReportsPathExistenceOnlyAndDoesNotExecute(t *testing.T) {
	// run_associated_specs_on_complete is false (the default) — Complete must not
	// execute the spec at all, and must fall back to reporting mere path existence.
	cmds, repo, buf := newTransitionTestRepo(t, true)
	t.Chdir(repo)
	id := "prov-2026-aaaa0009"

	specPath := filepath.Join(repo, "spec", "dummy.txt")
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(specPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile spec: %v", err)
	}
	gitExec(t, repo, "add", "spec/dummy.txt")
	gitExec(t, repo, "commit", "-m", "add spec fixture")

	// run_command "false" would fail the record if it were ever executed — proving
	// that a disabled gate really does skip execution rather than merely ignoring
	// the outcome.
	writeAndCommitRecord(t, cmds, repo, id, recordWithSpec(id, "open", "spec/dummy.txt", "false"))

	if err := cmds.Complete(CompleteOptions{RecordID: id}); err != nil {
		t.Fatalf("Complete should succeed when the gate is disabled, even though the spec command would fail: %v", err)
	}
	if got := loadedRecord(t, cmds, id).Status; got != StatusImplemented {
		t.Errorf("in-memory status = %q, want implemented", got)
	}
	if msg := buf.String(); !strings.Contains(msg, "spec/dummy.txt") || !strings.Contains(msg, "✓") || strings.Contains(msg, "✓ passed") {
		t.Errorf("success output should report path existence only (not an executed outcome), got:\n%s", msg)
	}
}

// --- Open ------------------------------------------------------------------

func TestOpen_RollsBackOnLintFailure_WithoutAutoCommit(t *testing.T) {
	cmds, repo, buf := newTransitionTestRepo(t, false)
	id := "prov-2026-aaaa0004"
	abs := writeAndCommitRecord(t, cmds, repo, id, invalidImprint(id, "draft"))
	before, _ := os.ReadFile(abs)

	err := cmds.Open(OpenOptions{RecordID: id})
	if err == nil {
		t.Fatal("expected Open to fail when the record would not pass lint")
	}

	after, _ := os.ReadFile(abs)
	if !bytes.Equal(before, after) {
		t.Errorf("record file not restored after rollback:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	if got := loadedRecord(t, cmds, id).Status; got != StatusDraft {
		t.Errorf("in-memory status = %q, want draft (transition must not stick)", got)
	}
	if msg := buf.String(); !strings.Contains(msg, "does not pass validation") {
		t.Errorf("error message should explain the validation failure, got:\n%s", msg)
	}
}

func TestOpen_RollsBackOnCommitRejection(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, true)
	id := "prov-2026-aaaa0005"
	abs := writeAndCommitRecord(t, cmds, repo, id, validRecord(id, "draft", "blueprint"))
	before, _ := os.ReadFile(abs)

	installRejectingPreCommitHook(t, repo)

	if err := cmds.Open(OpenOptions{RecordID: id}); err == nil {
		t.Fatal("expected Open to fail when the auto-commit is rejected")
	}

	if after, _ := os.ReadFile(abs); !bytes.Equal(before, after) {
		t.Errorf("record file not restored after rollback:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	if got := loadedRecord(t, cmds, id).Status; got != StatusDraft {
		t.Errorf("in-memory status = %q, want draft", got)
	}
	if got := stagedFiles(t, repo); got != "" {
		t.Errorf("expected clean index after rollback, staged: %q", got)
	}
}

// --- Deprecate -------------------------------------------------------------

func TestDeprecate_RollsBackOnCommitRejection(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, true)
	id := "prov-2026-aaaa0006"
	abs := writeAndCommitRecord(t, cmds, repo, id, validRecord(id, "implemented", "blueprint"))
	before, _ := os.ReadFile(abs)

	installRejectingPreCommitHook(t, repo)

	if err := cmds.Deprecate(DeprecateOptions{RecordID: id}); err == nil {
		t.Fatal("expected Deprecate to fail when the auto-commit is rejected")
	}

	if after, _ := os.ReadFile(abs); !bytes.Equal(before, after) {
		t.Errorf("record file not restored after rollback:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	if got := loadedRecord(t, cmds, id).Status; got != StatusImplemented {
		t.Errorf("in-memory status = %q, want implemented (rolled back)", got)
	}
	if got := stagedFiles(t, repo); got != "" {
		t.Errorf("expected clean index after rollback, staged: %q", got)
	}
}
