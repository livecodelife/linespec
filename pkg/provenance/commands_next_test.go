package provenance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// commands_next_test.go covers Commands.Next's fswrite reconcile gating
// (prov-2026-8d2f5f2a): the reconcile pass only runs when the
// LINESPEC_PROVENANCE_RECONCILE env var is set (only session-start.sh sets it),
// so an ordinary `next` call never pays for, or risks, a filesystem permission
// sweep. It also covers the c.reconcile() helper's cold-start skip wiring.

// setupReconcileTestRepo builds a real git repo with one open, allowlist-mode
// record covering pkg/covered.go, plus a covered and an uncovered tracked file,
// both starting in the OPPOSITE of the state reconcile would produce, so a
// passing test actually exercises the sweep in both directions.
func setupReconcileTestRepo(t *testing.T) (cmds *Commands, repo string, covered, uncovered string) {
	t.Helper()
	cmds, repo, _ = newTransitionTestRepo(t, false)
	id := "prov-2026-cccc0001"
	writeAndCommitRecord(t, cmds, repo, id, validRecord(id, "open", "blueprint")) // affected_scope: [pkg/thing.go]

	// validRecord declares pkg/thing.go as its scope — reuse that name so the
	// "covered" file lines up with the record without introducing new YAML.
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	covered = filepath.Join(repo, "pkg", "thing.go")
	uncovered = filepath.Join(repo, "pkg", "uncovered.go")
	writePerm(t, covered, 0o444)   // starts locked; reconcile should unlock it
	writePerm(t, uncovered, 0o644) // starts writable; reconcile should lock it
	gitExec(t, repo, "add", "pkg/thing.go", "pkg/uncovered.go")
	gitExec(t, repo, "commit", "-m", "add pkg files")

	return cmds, repo, covered, uncovered
}

func TestNext_WithoutReconcileEnvVar_LeavesPermissionsUntouched(t *testing.T) {
	cmds, _, covered, uncovered := setupReconcileTestRepo(t)

	if err := cmds.Next(NextOptions{Format: "json"}); err != nil {
		t.Fatalf("Next: %v", err)
	}

	if isWritableOnDisk(t, covered) {
		t.Errorf("without the reconcile env var, Next must not unlock pkg/thing.go")
	}
	if !isWritableOnDisk(t, uncovered) {
		t.Errorf("without the reconcile env var, Next must not lock pkg/uncovered.go")
	}
}

func TestNext_WithReconcileEnvVar_SweepsPermissions(t *testing.T) {
	cmds, _, covered, uncovered := setupReconcileTestRepo(t)
	t.Setenv(reconcileEnvVar, "1")

	if err := cmds.Next(NextOptions{Format: "json"}); err != nil {
		t.Fatalf("Next: %v", err)
	}

	if !isWritableOnDisk(t, covered) {
		t.Errorf("with the reconcile env var set, Next should unlock pkg/thing.go (covered by the open record)")
	}
	if isWritableOnDisk(t, uncovered) {
		t.Errorf("with the reconcile env var set, Next should lock pkg/uncovered.go (not covered by any open record)")
	}
}

func TestNext_ReconcileEnvVarEmptyString_DoesNotTrigger(t *testing.T) {
	// Setting the var to the empty string must behave the same as unset — the
	// gate checks os.Getenv(...) != "".
	cmds, _, covered, uncovered := setupReconcileTestRepo(t)
	t.Setenv(reconcileEnvVar, "")

	if err := cmds.Next(NextOptions{Format: "json"}); err != nil {
		t.Fatalf("Next: %v", err)
	}

	if isWritableOnDisk(t, covered) {
		t.Errorf("empty env var must not trigger reconcile (pkg/thing.go should stay locked)")
	}
	if !isWritableOnDisk(t, uncovered) {
		t.Errorf("empty env var must not trigger reconcile (pkg/uncovered.go should stay writable)")
	}
}

func TestNext_ReconcileFailureIsNonFatal(t *testing.T) {
	// If reconcile fails (e.g. RepoRoot points somewhere `git ls-files` cannot
	// run), Next must still succeed and render advice rather than propagating
	// the reconcile error — the reconcile pass is best-effort only.
	cmds, _, _, _ := setupReconcileTestRepo(t)
	t.Setenv(reconcileEnvVar, "1")
	cmds.RepoRoot = filepath.Join(t.TempDir(), "does-not-exist")

	if err := cmds.Next(NextOptions{Format: "json"}); err != nil {
		t.Fatalf("Next must not fail even if the reconcile pass errors, got: %v", err)
	}
}

func TestNext_JSONOutputStillValidWithReconcileEnabled(t *testing.T) {
	cmds, repo, _, _ := setupReconcileTestRepo(t)
	t.Setenv(reconcileEnvVar, "1")

	outPath := filepath.Join(repo, "next-out.json")
	f, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cmds.Formatter = NewFormatter(f, false)

	if err := cmds.Next(NextOptions{Format: "json"}); err != nil {
		f.Close()
		t.Fatalf("Next: %v", err)
	}
	f.Close()

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var payload struct {
		Primary *NextAction `json:"primary"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Next JSON output invalid: %v\noutput: %s", err, data)
	}
	if payload.Primary == nil {
		t.Fatalf("expected a primary action in JSON output, got: %s", data)
	}
}

// --- c.reconcile() cold-start wiring ---------------------------------------

func TestCommandsReconcile_ColdStartSkipsWhenNoRecordsLoaded(t *testing.T) {
	cmds, _, _ := newTransitionTestRepo(t, false)
	// No records loaded at all — cold-start must skip enforcement entirely
	// rather than attempting to list tracked files.
	result, err := cmds.reconcile()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(result.Locked) != 0 || len(result.Unlocked) != 0 {
		t.Errorf("cold-start reconcile should be a no-op, got locked=%v unlocked=%v", result.Locked, result.Unlocked)
	}
}

func TestCommandsReconcile_RunsOnceRecordsExist(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	id := "prov-2026-cccc0002"
	writeAndCommitRecord(t, cmds, repo, id, validRecord(id, "open", "blueprint"))

	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	uncovered := filepath.Join(repo, "pkg", "other.go")
	writePerm(t, uncovered, 0o644)
	gitExec(t, repo, "add", "pkg/other.go")
	gitExec(t, repo, "commit", "-m", "add pkg/other.go")

	if _, err := cmds.reconcile(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if isWritableOnDisk(t, uncovered) {
		t.Errorf("pkg/other.go should be locked once at least one record exists and does not cover it")
	}
}