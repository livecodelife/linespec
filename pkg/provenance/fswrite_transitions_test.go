package provenance

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fswrite_transitions_test.go covers the fswrite blueprint's (prov-2026-8d2f5f2a)
// integration into the record lifecycle: Open/LockScope materialize write
// permission for newly-declared scope atomically with the status/scope change,
// and rolled-back transitions leave permissions untouched; Complete/Deprecate
// give up a record's exclusive claim on the write access it granted.
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

func TestOpen_FailsCleanlyWithoutPermissionSideEffectsWhenScopePathMissing(t *testing.T) {
	// validRecord (transition_rollback_test.go) declares pkg/thing.go, which does
	// not exist anywhere here — the pre-existing linter blocks Open for a missing
	// scope path on a non-terminal record. Materialize must never run before that
	// lint gate, so nothing is unlocked as a side effect of the failed attempt.
	cmds, repo, buf := newTransitionTestRepo(t, false)
	id := "prov-2026-bbbb0002"
	writeAndCommitRecord(t, cmds, repo, id, validRecord(id, "draft", "blueprint"))
	t.Chdir(repo)

	if err := cmds.Open(OpenOptions{RecordID: id}); err == nil {
		t.Fatal("expected Open to fail when a declared affected_scope path does not exist on disk")
	}
	if !strings.Contains(buf.String(), "does not pass validation") {
		t.Errorf("expected validation-failure message, got:\n%s", buf.String())
	}
	if _, err := os.Stat(filepath.Join(repo, "pkg")); err == nil {
		t.Errorf("a failed Open must not create or unlock the scope path's parent directory")
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

// --- LockScope (scope widening) -----------------------------------------------

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

func TestLockScope_WideningOpenRecordMaterializesNewlyAddedPath(t *testing.T) {
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

	if err := cmds.LockScope(LockScopeOptions{RecordID: id}); err != nil {
		t.Fatalf("LockScope (widen): %v", err)
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

func TestLockScope_ErrorsWhenAlreadyAllowlistWithNothingNewToAdd(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	cmds = withChecker(cmds)
	id := "prov-2026-bbbb0010"
	setupOpenableRecord(t, cmds, repo, id)

	if err := cmds.Open(OpenOptions{RecordID: id}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := cmds.LockScope(LockScopeOptions{RecordID: id}); err == nil {
		t.Fatal("expected LockScope to error when already allowlist with nothing new committed to add")
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

func TestLockScope_DryRunDoesNotPersistScopeOrMaterializePermissions(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	cmds = withChecker(cmds)
	id := "prov-2026-bbbb0012"
	writeAndCommitRecord(t, cmds, repo, id, observedRecord(id, "open"))

	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, "pkg", "dryrun.go")
	writePerm(t, target, 0o444)
	gitExec(t, repo, "add", "pkg/dryrun.go")
	gitExec(t, repo, "commit", "-m", "touch pkg/dryrun.go ["+id+"]")

	if err := cmds.LockScope(LockScopeOptions{RecordID: id, DryRun: true}); err != nil {
		t.Fatalf("LockScope dry-run: %v", err)
	}

	// In-memory scope must be restored to its pre-call state (empty) — dry-run
	// must not leave the auto-populated scope mutated on the loaded record.
	rec := loadedRecord(t, cmds, id)
	if len(rec.AffectedScope) != 0 {
		t.Errorf("dry-run must not persist scope changes in-memory, got %v", rec.AffectedScope)
	}
	if isWritableOnDisk(t, target) {
		t.Errorf("dry-run must not materialize write permission for pkg/dryrun.go")
	}
	data, err := os.ReadFile(filepath.Join(repo, "provenance", id+".yml"))
	if err != nil {
		t.Fatalf("ReadFile record: %v", err)
	}
	if strings.Contains(string(data), "pkg/dryrun.go") {
		t.Errorf("dry-run must not save the record file to disk, got:\n%s", data)
	}
}

func TestLockScope_WideningDryRunDoesNotMaterializeOrPersist(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	cmds = withChecker(cmds)
	id := "prov-2026-bbbb0013"
	setupOpenableRecord(t, cmds, repo, id)

	if err := cmds.Open(OpenOptions{RecordID: id}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	other := filepath.Join(repo, "pkg", "other_dryrun.go")
	writePerm(t, other, 0o444)
	gitExec(t, repo, "add", "pkg/other_dryrun.go")
	gitExec(t, repo, "commit", "-m", "touch pkg/other_dryrun.go ["+id+"]")

	if err := cmds.LockScope(LockScopeOptions{RecordID: id, DryRun: true}); err != nil {
		t.Fatalf("LockScope widen dry-run: %v", err)
	}

	rec := loadedRecord(t, cmds, id)
	if slices.Contains(rec.AffectedScope, "pkg/other_dryrun.go") {
		t.Errorf("dry-run widening must not persist the new path in-memory, got %v", rec.AffectedScope)
	}
	if isWritableOnDisk(t, other) {
		t.Errorf("dry-run widening must not materialize write permission for pkg/other_dryrun.go")
	}
}
