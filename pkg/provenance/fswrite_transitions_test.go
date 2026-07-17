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

// openableRecordWithScope is like openableRecord but lets the caller supply
// the affected_scope pattern (exact, glob, or regex), so tests can exercise
// declaring a not-yet-existing path through every pattern kind.
func openableRecordWithScope(id, status, scopePattern string) string {
	return "id: " + id + "\n" +
		"title: Test record\n" +
		"status: " + status + "\n" +
		"created_at: \"2026-01-01\"\n" +
		"author: test@example.com\n" +
		"intent: Build the thing.\n" +
		"constraints:\n  - Must work\n" +
		"affected_scope:\n  - " + scopePattern + "\n" +
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

// setupOpenableRecordWithScope is setupOpenableRecord's not-yet-existing-path
// counterpart: it writes id's record (declaring scopePattern, which must not
// match anything on disk) plus the spec.md proof file, then t.Chdir's into
// repo. It deliberately does NOT create scopePattern itself — the whole point
// is that Open must succeed despite the declared path not existing yet.
func setupOpenableRecordWithScope(t *testing.T, cmds *Commands, repo, id, scopePattern string) {
	t.Helper()
	writeAndCommitRecord(t, cmds, repo, id, openableRecordWithScope(id, "draft", scopePattern))
	writePerm(t, filepath.Join(repo, "spec.md"), 0o644)
	t.Chdir(repo)
}

// lockDir makes an already-existing directory non-writable, matching the
// governed tree's default-locked posture, so a test can observe Open (or a
// later Reconcile) actually flip the write bit rather than finding it already
// set by os.MkdirAll's default mode.
func lockDir(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o555); err != nil {
		t.Fatalf("Chmod %s: %v", path, err)
	}
}

func TestOpen_SucceedsAndUnlocksParentDirWhenExactAffectedScopePathDoesNotExistYet(t *testing.T) {
	// The headline "declare before you create it" case (prov-2026-b4006eda):
	// affected_scope names a brand-new exact path, pkg/newfile.go, which does
	// not exist anywhere. Open must succeed, and — since MaterializeScope
	// handles exact not-yet-existing paths directly — pkg/ must be unlocked as
	// part of the same atomic transition, with no need for a separate reconcile.
	cmds, repo, _ := newTransitionTestRepo(t, false)
	id := "prov-2026-bbbb0002"
	pkgDir := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lockDir(t, pkgDir)
	setupOpenableRecordWithScope(t, cmds, repo, id, "pkg/newfile.go")

	if err := cmds.Open(OpenOptions{RecordID: id}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !isWritableOnDisk(t, pkgDir) {
		t.Errorf("pkg/ should be unlocked so pkg/newfile.go (declared but not yet created) can be created")
	}
	if _, err := os.Stat(filepath.Join(pkgDir, "newfile.go")); err == nil {
		t.Errorf("Open must not create the declared file itself, only unlock its parent directory")
	}
}

func TestOpen_SucceedsAndReconcileUnlocksBaseDirWhenGlobAffectedScopeMatchesNoFilesYet(t *testing.T) {
	// The glob variant of the same not-yet-existing-path case: affected_scope
	// is "emptydir/*.go", naming a directory that exists but currently holds no
	// matching file at all (not even an unrelated one — the earlier draft of
	// this bug wrongly assumed this already worked). Open must succeed, and a
	// subsequent reconcile pass (fswrite.go Reconcile, driven here via `next`)
	// must unlock emptydir/ so a matching file can be created — MaterializeScope
	// itself skips glob patterns by design, so the unlock is reconcile's job.
	cmds, repo, _ := newTransitionTestRepo(t, false)
	id := "prov-2026-bbbb0015"
	emptyDir := filepath.Join(repo, "emptydir")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lockDir(t, emptyDir)
	setupOpenableRecordWithScope(t, cmds, repo, id, "emptydir/*.go")

	if err := cmds.Open(OpenOptions{RecordID: id}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := cmds.Next(NextOptions{}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !isWritableOnDisk(t, emptyDir) {
		t.Errorf("emptydir/ should be unlocked after reconcile so a file matching emptydir/*.go can be created")
	}
}

func TestOpen_SucceedsAndReconcileUnlocksBaseDirWhenRegexAffectedScopeMatchesNoFilesYet(t *testing.T) {
	// The regex variant: affected_scope is "re:newdir/newfile\.go$", naming a
	// directory that exists but has no file matching the regex anywhere in the
	// repo. Open must succeed, and a subsequent reconcile pass must unlock
	// newdir/ for the same reason as the glob case above.
	cmds, repo, _ := newTransitionTestRepo(t, false)
	id := "prov-2026-bbbb0016"
	newDir := filepath.Join(repo, "newdir")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lockDir(t, newDir)
	setupOpenableRecordWithScope(t, cmds, repo, id, `re:newdir/newfile\.go$`)

	if err := cmds.Open(OpenOptions{RecordID: id}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := cmds.Next(NextOptions{}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !isWritableOnDisk(t, newDir) {
		t.Errorf("newdir/ should be unlocked after reconcile so a file matching the regex can be created")
	}
}

func TestOpen_FailsCleanlyWithoutPermissionSideEffectsWhenScopePathIsADirectory(t *testing.T) {
	// A scope path that resolves to a directory is a genuinely invalid
	// declaration — not a "create this later" declaration — and must keep
	// blocking Open with no permission side effects, unlike the not-yet-
	// existing-path cases above that this bug fixes (constraint: the
	// relaxation must not weaken this "genuinely invalid pattern" guarantee).
	cmds, repo, buf := newTransitionTestRepo(t, false)
	id := "prov-2026-bbbb0017"
	writeAndCommitRecord(t, cmds, repo, id, openableRecordWithScope(id, "draft", "pkg/thing.go"))
	// pkg/thing.go is declared as a file but actually exists as a directory.
	// pkg/ itself starts locked, matching the governed tree's default posture.
	if err := os.MkdirAll(filepath.Join(repo, "pkg", "thing.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(repo, "pkg")
	lockDir(t, pkgDir)
	// t.TempDir's cleanup needs to unlink pkg/thing.go/, which requires pkg/ to
	// be writable again — restore it regardless of test outcome.
	t.Cleanup(func() { _ = os.Chmod(pkgDir, 0o755) })
	writePerm(t, filepath.Join(repo, "spec.md"), 0o644)
	t.Chdir(repo)

	if err := cmds.Open(OpenOptions{RecordID: id}); err == nil {
		t.Fatal("expected Open to fail when a declared affected_scope path is a directory")
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
