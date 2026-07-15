package provenance

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fswrite_transitions_test.go covers the fswrite blueprint's (prov-2026-8d2f5f2a)
// integration into the record lifecycle: Open/AddScope materialize write
// permission for newly-declared scope atomically with the status/scope change,
// and rolled-back transitions leave permissions untouched; Complete/Deprecate
// give up a record's exclusive claim on the write access it granted; Next
// reconciles the entire write-bit projection on every call, unconditionally —
// enforcement must not depend on the Claude Code plugin hooks being installed.
//
// The pre-existing linter (linter.go, outside this blueprint's affected_scope)
// requires an open (non-terminal, strict-mode) record to have both its
// affected_scope paths AND its associated_specs paths exist ON DISK RELATIVE TO
// THE PROCESS CWD, not repo-relative — matching how the real `linespec` binary
// is always invoked from the repo root. openableRecord/setupOpenableRecord below
// satisfy both so Open() can actually succeed in these tests.

// openableRecord returns YAML for a record that passes strict-mode lint once
// open: affected_scope declares pkg/thing.go and associated_specs declares
// spec.md, both of which setupOpenableRecord creates on disk.
func openableRecord(id, status string) string {
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
		"associated_specs:\n  - path: spec.md\n" +
		"associated_traces: []\n" +
		"monitors: []\n" +
		"tags: []\n"
}

// setupOpenableRecord writes id's record (openableRecord) plus the pkg/thing.go
// scope file it declares (starting locked, 0o444, as the governed tree defaults
// to) and the spec.md proof file, then t.Chdir's into repo — matching how the
// real linespec binary is always invoked from the repo root, which the
// pre-existing linter's on-disk scope/spec checks assume. Returns pkg/thing.go's
// absolute path.
func setupOpenableRecord(t *testing.T, cmds *Commands, repo, id string) string {
	t.Helper()
	writeAndCommitRecord(t, cmds, repo, id, openableRecord(id, "draft"))
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, "pkg", "thing.go")
	writePerm(t, target, 0o444)
	writePerm(t, filepath.Join(repo, "spec.md"), 0o644)
	t.Chdir(repo)
	return target
}

// --- patternParentDir ---------------------------------------------------------

// TestPatternParentDir covers the literal-prefix-directory extraction Reconcile
// relies on (prov-2026-b4006eda) to unlock a glob or regex affected_scope
// pattern's parent directory even when it names no existing file yet.
func TestPatternParentDir(t *testing.T) {
	cases := map[string]string{
		"pkg/*.go":                "pkg",
		"pkg/sub/*.go":            "pkg/sub",
		"*.go":                    ".",
		"emptydir/*.go":           "emptydir",
		"pkg/fil?.go":             "pkg",
		`re:^pkg/.*\.go$`:         "pkg",
		`re:^pkg/thing\.go$`:      "pkg",
		`re:nonexistent_\d+`:      ".",
		".github/workflows/*.yml": ".github/workflows",
		"docs.v2/*.md":            "docs.v2",
		"pkg/v1.2/*.go":           "pkg/v1.2",
	}
	for pattern, want := range cases {
		if got := patternParentDir(pattern); got != want {
			t.Errorf("patternParentDir(%q) = %q, want %q", pattern, got, want)
		}
	}
}

// --- Open --------------------------------------------------------------------

func TestOpen_MaterializesWritePermissionForAffectedScope(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	id := "prov-2026-bbbb0001"
	target := setupOpenableRecord(t, cmds, repo, id)

	if err := cmds.Open(OpenOptions{RecordID: id}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !isWritableOnDisk(t, target) {
		t.Errorf("pkg/thing.go should be writable after opening the record that declares it in affected_scope")
	}
}

// notYetExistingPatternRecord returns YAML for a record whose sole
// affected_scope entry is pattern (an exact path, glob, or "re:"-prefixed
// regex naming a file that does not exist yet) and whose associated_specs
// declares spec.md, exactly like openableRecord — strict enforcement requires
// an open (non-brief) record to carry at least one associated_spec
// regardless of this bug's fix, so callers must create spec.md on disk before
// Open (prov-2026-b4006eda).
func notYetExistingPatternRecord(id, status, pattern string) string {
	return "id: " + id + "\n" +
		"title: Test record\n" +
		"status: " + status + "\n" +
		"created_at: \"2026-01-01\"\n" +
		"author: test@example.com\n" +
		"intent: Build the thing.\n" +
		"constraints:\n  - Must work\n" +
		"affected_scope:\n  - " + pattern + "\n" +
		"forbidden_scope: []\n" +
		"type: blueprint\n" +
		"supersedes: \"\"\n" +
		"superseded_by: \"\"\n" +
		"related: []\n" +
		"sealed_at_sha: \"\"\n" +
		"associated_specs:\n  - path: spec.md\n" +
		"associated_traces: []\n" +
		"monitors: []\n" +
		"tags: []\n"
}

// TestOpen_SucceedsAndUnlocksParentDirWhenScopePathMissing replaces
// TestOpen_FailsCleanlyWithoutPermissionSideEffectsWhenScopePathMissing
// (prov-2026-b4006eda): a declared-but-not-yet-existing affected_scope path is
// the headline "declare the path before you create the file" workflow
// fswrite.go Reconcile's not-yet-existing-path dir-unlock loop is built
// around (prov-2026-8d2f5f2a) — Open must not reject it, and its
// MaterializeScope call must unlock the parent directory immediately so the
// declared file can be created.
func TestOpen_SucceedsAndUnlocksParentDirWhenScopePathMissing(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	id := "prov-2026-bbbb0002"
	writeAndCommitRecord(t, cmds, repo, id, notYetExistingPatternRecord(id, "draft", "pkg/thing.go"))
	writePerm(t, filepath.Join(repo, "spec.md"), 0o644)

	// The directory starts locked (as the governed tree defaults to) so
	// unlocking it is actually exercised, not just already-true by default.
	// pkg/thing.go itself is deliberately never created — it does not exist
	// anywhere in this repo.
	pkgDir := filepath.Join(repo, "pkg")
	if err := os.Mkdir(pkgDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	if err := cmds.Open(OpenOptions{RecordID: id}); err != nil {
		t.Fatalf("expected Open to succeed for a declared-but-not-yet-existing affected_scope path, got: %v", err)
	}
	if got := loadedRecord(t, cmds, id).Status; got != StatusOpen {
		t.Errorf("in-memory status = %q, want open", got)
	}
	if !isWritableOnDisk(t, pkgDir) {
		t.Errorf("pkg/ should be unlocked by Open's MaterializeScope so pkg/thing.go can be created")
	}
}

// TestOpen_SucceedsWhenGlobScopeMatchesNoFilesYet covers the bug's step-5
// repro: a glob into an otherwise-empty directory (as opposed to step-4's
// already-working case where a matching file already lived there) must also
// open successfully, and reconcile (not MaterializeScope, which has no single
// concrete path to chmod for a glob) must then unlock its parent directory.
func TestOpen_SucceedsWhenGlobScopeMatchesNoFilesYet(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	id := "prov-2026-bbbb0016"
	writeAndCommitRecord(t, cmds, repo, id, notYetExistingPatternRecord(id, "draft", "pkg/*.go"))
	writePerm(t, filepath.Join(repo, "spec.md"), 0o644)

	pkgDir := filepath.Join(repo, "pkg")
	if err := os.Mkdir(pkgDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	if err := cmds.Open(OpenOptions{RecordID: id}); err != nil {
		t.Fatalf("expected Open to succeed for a glob scope matching no files yet, got: %v", err)
	}

	if err := cmds.Next(NextOptions{}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !isWritableOnDisk(t, pkgDir) {
		t.Errorf("pkg/ should be unlocked by reconcile so a file matching pkg/*.go can be created")
	}
}

// TestOpen_SucceedsWhenRegexScopeMatchesNoFilesYet mirrors
// TestOpen_SucceedsWhenGlobScopeMatchesNoFilesYet for a "re:"-prefixed regex
// scope pattern — the third pattern kind the bug's constraints require to be
// openable when it names only not-yet-existing files.
func TestOpen_SucceedsWhenRegexScopeMatchesNoFilesYet(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	id := "prov-2026-bbbb0017"
	writeAndCommitRecord(t, cmds, repo, id, notYetExistingPatternRecord(id, "draft", `re:^pkg/.*\.go$`))
	writePerm(t, filepath.Join(repo, "spec.md"), 0o644)

	pkgDir := filepath.Join(repo, "pkg")
	if err := os.Mkdir(pkgDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	if err := cmds.Open(OpenOptions{RecordID: id}); err != nil {
		t.Fatalf("expected Open to succeed for a regex scope matching no files yet, got: %v", err)
	}

	if err := cmds.Next(NextOptions{}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !isWritableOnDisk(t, pkgDir) {
		t.Errorf("pkg/ should be unlocked by reconcile so a file matching the regex can be created")
	}
}

// TestOpen_FailsCleanlyWithoutPermissionSideEffectsWhenScopePathIsDirectory
// carries forward the "genuinely invalid Open fails cleanly, no permission
// side effects" guarantee that
// TestOpen_SucceedsAndUnlocksParentDirWhenScopePathMissing no longer covers,
// retargeted onto a still-invalid case per prov-2026-b4006eda's constraints: a
// scope path that exists but is a directory (not a file) is not a
// not-yet-existing declaration and must keep failing Open.
func TestOpen_FailsCleanlyWithoutPermissionSideEffectsWhenScopePathIsDirectory(t *testing.T) {
	cmds, repo, buf := newTransitionTestRepo(t, false)
	id := "prov-2026-bbbb0018"
	writeAndCommitRecord(t, cmds, repo, id, validRecord(id, "draft", "blueprint"))

	// "pkg/thing.go" resolves to a directory here, not a file.
	pkgDir := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(filepath.Join(pkgDir, "thing.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(pkgDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(pkgDir, 0o755) }) // let t.TempDir() clean up pkg/thing.go
	t.Chdir(repo)

	if err := cmds.Open(OpenOptions{RecordID: id}); err == nil {
		t.Fatal("expected Open to fail when a declared affected_scope path resolves to a directory")
	}
	if !strings.Contains(buf.String(), "does not pass validation") {
		t.Errorf("expected validation-failure message, got:\n%s", buf.String())
	}
	if isWritableOnDisk(t, pkgDir) {
		t.Errorf("a failed Open must not unlock the scope path's parent directory")
	}
}

func TestOpen_RollsBackPermissionsOnCommitRejection(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, true)
	id := "prov-2026-bbbb0003"
	target := setupOpenableRecord(t, cmds, repo, id)

	installRejectingPreCommitHook(t, repo)

	if err := cmds.Open(OpenOptions{RecordID: id}); err == nil {
		t.Fatal("expected Open to fail when the auto-commit is rejected")
	}

	if isWritableOnDisk(t, target) {
		t.Errorf("pkg/thing.go must stay locked — a rejected Open transition must leave permissions untouched")
	}
}

func TestOpen_RollsBackPermissionsOnLintFailure(t *testing.T) {
	cmds, repo, buf := newTransitionTestRepo(t, false)
	id := "prov-2026-bbbb0004"
	writeAndCommitRecord(t, cmds, repo, id, invalidImprint(id, "draft"))
	t.Chdir(repo)

	target := filepath.Join(repo, "pkg", "thing.go")
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePerm(t, target, 0o444)

	if err := cmds.Open(OpenOptions{RecordID: id}); err == nil {
		t.Fatal("expected Open to fail lint (invalidImprint has a dangling implements)")
	}
	if !strings.Contains(buf.String(), "does not pass validation") {
		t.Errorf("expected validation-failure message, got:\n%s", buf.String())
	}
	if isWritableOnDisk(t, target) {
		t.Errorf("pkg/thing.go is not in invalidImprint's (empty) affected_scope, so it must remain locked")
	}
}

// --- Complete / Deprecate re-lock on close ------------------------------------

func TestComplete_RelocksScopeNoLongerCoveredByAnyOpenRecord(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	id := "prov-2026-bbbb0005"
	target := setupOpenableRecord(t, cmds, repo, id)

	if err := cmds.Open(OpenOptions{RecordID: id}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !isWritableOnDisk(t, target) {
		t.Fatalf("precondition: pkg/thing.go should be writable after Open")
	}

	if err := cmds.Complete(CompleteOptions{RecordID: id}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if isWritableOnDisk(t, target) {
		t.Errorf("pkg/thing.go should be re-locked after completing the only open record that covered it")
	}
}

func TestComplete_LeavesScopeWritableWhenAnotherOpenRecordStillCoversIt(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	idA := "prov-2026-bbbb0006"
	idB := "prov-2026-bbbb0007"

	// Write and commit both records before a single LoadAll — the Loader appends
	// on every LoadAll call, so calling the writeAndCommitRecord helper (which
	// itself calls LoadAll) twice would double-load idA and fail graph-building
	// on a spurious duplicate ID.
	for _, rec := range []struct{ id, yaml string }{
		{idA, openableRecord(idA, "draft")},
		{idB, openableRecord(idB, "draft")}, // same affected_scope: pkg/thing.go
	} {
		rel := "provenance/" + rec.id + ".yml"
		if err := os.WriteFile(filepath.Join(repo, rel), []byte(rec.yaml), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", rel, err)
		}
		gitExec(t, repo, "add", rel)
		gitExec(t, repo, "commit", "-m", "add record ["+rec.id+"]")
	}
	if err := cmds.Loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, "pkg", "thing.go")
	writePerm(t, target, 0o444)
	writePerm(t, filepath.Join(repo, "spec.md"), 0o644)
	t.Chdir(repo)

	if err := cmds.Open(OpenOptions{RecordID: idA}); err != nil {
		t.Fatalf("Open idA: %v", err)
	}
	if err := cmds.Open(OpenOptions{RecordID: idB}); err != nil {
		t.Fatalf("Open idB: %v", err)
	}

	if err := cmds.Complete(CompleteOptions{RecordID: idA}); err != nil {
		t.Fatalf("Complete idA: %v", err)
	}
	if !isWritableOnDisk(t, target) {
		t.Errorf("pkg/thing.go should stay writable — %s is still open and still declares it", idB)
	}
}

func TestDeprecate_RelocksScopeNoLongerCoveredByAnyOpenRecord(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	id := "prov-2026-bbbb0008"
	target := setupOpenableRecord(t, cmds, repo, id)

	if err := cmds.Open(OpenOptions{RecordID: id}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !isWritableOnDisk(t, target) {
		t.Fatalf("precondition: pkg/thing.go should be writable after Open")
	}

	// Deprecate only forbids already-deprecated/superseded statuses; open records
	// can be deprecated directly.
	if err := cmds.Deprecate(DeprecateOptions{RecordID: id}); err != nil {
		t.Fatalf("Deprecate: %v", err)
	}
	if isWritableOnDisk(t, target) {
		t.Errorf("pkg/thing.go should be re-locked after deprecating the only open record that covered it")
	}
}

// --- AddScope (scope widening) -------------------------------------------------

// observedRecord returns a record with no declared affected_scope (observed
// mode) so LockScope's initial auto-populate-from-git-history path runs.
func observedRecord(id, status string) string {
	return "id: " + id + "\n" +
		"title: Test record\n" +
		"status: " + status + "\n" +
		"created_at: \"2026-01-01\"\n" +
		"author: test@example.com\n" +
		"intent: Build the thing.\n" +
		"constraints:\n  - Must work\n" +
		"affected_scope: []\n" +
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

// withChecker wires a CommitChecker onto cmds, mirroring what NewCommands does
// in production (commands.go:115) — the shared newTransitionTestRepo helper
// (transition_rollback_test.go, outside this blueprint's affected_scope) does
// not construct one, since none of its own tests call LockScope.
func withChecker(cmds *Commands) *Commands {
	cmds.Checker = NewCommitChecker(cmds.Git, cmds.Loader)
	return cmds
}

func TestAddScope_WideningOpenRecordMaterializesNewlyAddedPath(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	cmds = withChecker(cmds)
	id := "prov-2026-bbbb0009"
	setupOpenableRecord(t, cmds, repo, id)

	if err := cmds.Open(OpenOptions{RecordID: id}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Commit a second file tagged with this record's ID, outside its currently
	// declared affected_scope (pkg/thing.go only).
	other := filepath.Join(repo, "pkg", "other.go")
	writePerm(t, other, 0o444)
	gitExec(t, repo, "add", "pkg/other.go")
	gitExec(t, repo, "commit", "-m", "touch pkg/other.go ["+id+"]")

	if isWritableOnDisk(t, other) {
		t.Fatalf("precondition: pkg/other.go should still be locked before widening scope")
	}

	if err := cmds.AddScope(AddScopeOptions{RecordID: id}); err != nil {
		t.Fatalf("AddScope: %v", err)
	}

	rec := loadedRecord(t, cmds, id)
	if !slices.Contains(rec.AffectedScope, "pkg/other.go") {
		t.Errorf("affected_scope should now include pkg/other.go, got %v", rec.AffectedScope)
	}
	if !isWritableOnDisk(t, other) {
		t.Errorf("pkg/other.go should be writable immediately after widening scope to cover it")
	}
	// The original path's permission (already writable from Open) is untouched.
	if !isWritableOnDisk(t, filepath.Join(repo, "pkg", "thing.go")) {
		t.Errorf("pkg/thing.go should remain writable after widening scope to add pkg/other.go")
	}
}

func TestAddScope_ErrorsWhenNothingNewToAdd(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	cmds = withChecker(cmds)
	id := "prov-2026-bbbb0010"
	setupOpenableRecord(t, cmds, repo, id)

	if err := cmds.Open(OpenOptions{RecordID: id}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := cmds.AddScope(AddScopeOptions{RecordID: id}); err == nil {
		t.Fatal("expected AddScope to error when already allowlist with nothing new committed to add")
	}
}

func TestAddScope_ErrorsWhenNotYetAllowlist(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	cmds = withChecker(cmds)
	id := "prov-2026-bbbb0012"
	writeAndCommitRecord(t, cmds, repo, id, observedRecord(id, "draft"))
	t.Chdir(repo)

	if err := cmds.AddScope(AddScopeOptions{RecordID: id}); err == nil {
		t.Fatal("expected AddScope to error on a record that is still in observed mode (use lock-scope first)")
	}
}

func TestLockScope_ErrorsWhenAlreadyAllowlist(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	cmds = withChecker(cmds)
	id := "prov-2026-bbbb0013"
	setupOpenableRecord(t, cmds, repo, id)

	if err := cmds.Open(OpenOptions{RecordID: id}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := cmds.LockScope(LockScopeOptions{RecordID: id}); err == nil {
		t.Fatal("expected LockScope to error on an already-allowlist record and point at add-scope")
	}
}

func TestLockScope_DraftRecordDoesNotMaterializePermissions(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	cmds = withChecker(cmds)
	id := "prov-2026-bbbb0011"
	writeAndCommitRecord(t, cmds, repo, id, observedRecord(id, "draft"))
	t.Chdir(repo)

	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, "pkg", "draftfile.go")
	writePerm(t, target, 0o444)
	gitExec(t, repo, "add", "pkg/draftfile.go")
	gitExec(t, repo, "commit", "-m", "touch pkg/draftfile.go ["+id+"]")

	if err := cmds.LockScope(LockScopeOptions{RecordID: id}); err != nil {
		t.Fatalf("LockScope: %v", err)
	}
	rec := loadedRecord(t, cmds, id)
	if !slices.Contains(rec.AffectedScope, "pkg/draftfile.go") {
		t.Errorf("affected_scope should be populated even for a draft record, got %v", rec.AffectedScope)
	}
	// Permissions only apply to OPEN records (constraint 1) — a draft record's
	// scope must not materialize write access.
	if isWritableOnDisk(t, target) {
		t.Errorf("pkg/draftfile.go must stay locked — the record is still draft, not open")
	}
}

// --- Next (unconditional reconcile) --------------------------------------------

// TestNext_ReconcilesWriteBitsUnconditionally proves reconcile no longer
// depends on anything external setting an env var (prov-2026-8d2f5f2a):
// calling Next with no special setup must self-heal write bits that have
// drifted out of sync with the current set of open records' scopes, in
// either direction, since enforcement must not depend on the Claude Code
// plugin hooks being installed.
func TestNext_ReconcilesWriteBitsUnconditionally(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	id := "prov-2026-bbbb0014"
	target := setupOpenableRecord(t, cmds, repo, id)

	if err := cmds.Open(OpenOptions{RecordID: id}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !isWritableOnDisk(t, target) {
		t.Fatalf("precondition: pkg/thing.go should be writable after Open")
	}

	// Reconcile only ever walks git-tracked files (fswrite.go's Reconcile never
	// reaches into untracked build output) — commit it so it is one.
	gitExec(t, repo, "add", "pkg/thing.go")
	gitExec(t, repo, "commit", "-m", "add pkg/thing.go ["+id+"]")

	// Simulate drift: something outside this CLI (a `git checkout`, a stale
	// clone, manual tampering) re-locked a path that an open record still
	// covers.
	if err := LockFile(target); err != nil {
		t.Fatalf("LockFile: %v", err)
	}
	if isWritableOnDisk(t, target) {
		t.Fatalf("precondition: pkg/thing.go should be locked before calling Next")
	}

	if err := cmds.Next(NextOptions{}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !isWritableOnDisk(t, target) {
		t.Errorf("Next should have reconciled pkg/thing.go back to writable, with no env var or special setup required")
	}

	// Now drift the other way: complete the record without going through
	// relockClosedScope's own path (simulate an out-of-band unlock that
	// outlived the record's closure), and confirm Next re-locks it too.
	if err := cmds.Complete(CompleteOptions{RecordID: id}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := UnlockFile(target); err != nil {
		t.Fatalf("UnlockFile: %v", err)
	}
	if !isWritableOnDisk(t, target) {
		t.Fatalf("precondition: pkg/thing.go should be writable before calling Next")
	}

	if err := cmds.Next(NextOptions{}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if isWritableOnDisk(t, target) {
		t.Errorf("Next should have reconciled pkg/thing.go back to locked — no open record covers it anymore")
	}
}
