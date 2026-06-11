package provenance

// Tests for prov-2026-bc57fbdc — relocating the governance-overlap signal to
// lifecycle transitions and running overlapping sealed records' specs at
// completion. Reuses helpers from transition_rollback_test.go (same package):
// newTransitionTestRepo, writeAndCommitRecord, loadedRecord, gitExec, gitOutput.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// customRecord builds a record YAML with explicit scope and associated specs.
// Each entry in specRunCommands becomes one associated_spec whose run_command is
// that string (so "false" fails, "true" passes).
func customRecord(id, status, sealedSHA string, scope, specRunCommands []string) string {
	scopeYAML := " []"
	if len(scope) > 0 {
		scopeYAML = ""
		for _, p := range scope {
			scopeYAML += "\n  - " + p
		}
	}
	specsYAML := " []"
	if len(specRunCommands) > 0 {
		specsYAML = ""
		for i, rc := range specRunCommands {
			specsYAML += fmt.Sprintf("\n  - path: spec_%d.txt\n    type: shell\n    run_command: %q", i, rc)
		}
	}
	return fmt.Sprintf(`id: %s
title: Record %s
status: %s
created_at: "2026-01-01"
author: test@example.com
sealed_at_sha: %q
intent: Governs the thing.
constraints:
  - Must hold
affected_scope:%s
forbidden_scope: []
type: blueprint
supersedes: ""
superseded_by: ""
related: []
associated_specs:%s
associated_traces: []
monitors: []
tags: []
`, id, id, status, sealedSHA, scopeYAML, specsYAML)
}

// writeRecord writes and commits a record file WITHOUT loading it (callers reload
// the loader once after writing all records, since LoadAll appends).
func writeRecord(t *testing.T, repo, id, yaml string) string {
	t.Helper()
	rel := "provenance/" + id + ".yml"
	abs := filepath.Join(repo, rel)
	if err := os.WriteFile(abs, []byte(yaml), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", rel, err)
	}
	gitExec(t, repo, "add", rel)
	gitExec(t, repo, "commit", "-m", "add record ["+id+"]")
	return abs
}

// reloadRecords resets the loader and reloads all records from disk exactly once.
func reloadRecords(t *testing.T, cmds *Commands) {
	t.Helper()
	cmds.Loader.Records = nil
	cmds.Loader.RecordsByID = map[string]*Record{}
	if err := cmds.Loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
}

// commitFileChange writes a file and commits it with a message tagging recordID,
// so the file becomes part of that record's actually-changed set.
func commitFileChange(t *testing.T, repo, rel, content, recordID string) {
	t.Helper()
	abs := filepath.Join(repo, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, repo, "add", rel)
	gitExec(t, repo, "commit", "-m", "change ["+recordID+"]")
}

// --- Completion-time spec teeth --------------------------------------------

func TestCompletion_BlocksWhenTouchedSealedRecordSpecFails(t *testing.T) {
	cmds, repo, buf := newTransitionTestRepo(t, false)
	cmds.Config.RunAssociatedSpecsOnComplete = true
	headSHA := gitOutput(t, repo, "rev-parse", "HEAD")

	sealedID := "prov-2026-cc000001"
	writeRecord(t, repo, sealedID,
		customRecord(sealedID, "implemented", headSHA, []string{"pkg/foo.go"}, []string{"false"}))

	rID := "prov-2026-cc000002"
	rAbs := writeRecord(t, repo, rID,
		customRecord(rID, "open", "", []string{"pkg/foo.go"}, nil))
	commitFileChange(t, repo, "pkg/foo.go", "package foo\n", rID)
	reloadRecords(t, cmds)

	err := cmds.Complete(CompleteOptions{RecordID: rID})
	if err == nil {
		t.Fatal("expected completion to be blocked by the failing spec of a touched sealed record")
	}
	if got := loadedRecord(t, cmds, rID).Status; got != StatusOpen {
		t.Errorf("in-memory status = %q, want open (rolled back)", got)
	}
	if data, _ := os.ReadFile(rAbs); !strings.Contains(string(data), "status: open") {
		t.Errorf("on-disk record should be rolled back to open:\n%s", data)
	}
	if msg := buf.String(); !strings.Contains(msg, "Cannot complete") || !strings.Contains(msg, "rolled back") {
		t.Errorf("expected a clear block-and-rollback message, got:\n%s", msg)
	}
}

func TestCompletion_SucceedsWhenTouchedSealedRecordSpecPasses(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)
	cmds.Config.RunAssociatedSpecsOnComplete = true
	headSHA := gitOutput(t, repo, "rev-parse", "HEAD")

	sealedID := "prov-2026-cc000011"
	writeRecord(t, repo, sealedID,
		customRecord(sealedID, "implemented", headSHA, []string{"pkg/foo.go"}, []string{"true"}))

	rID := "prov-2026-cc000012"
	writeRecord(t, repo, rID,
		customRecord(rID, "open", "", []string{"pkg/foo.go"}, nil))
	commitFileChange(t, repo, "pkg/foo.go", "package foo\n", rID)
	reloadRecords(t, cmds)

	if err := cmds.Complete(CompleteOptions{RecordID: rID}); err != nil {
		t.Fatalf("completion should succeed when the touched sealed record's spec passes: %v", err)
	}
	if got := loadedRecord(t, cmds, rID).Status; got != StatusImplemented {
		t.Errorf("in-memory status = %q, want implemented", got)
	}
}

func TestCompletion_NoSpecOverlap_EmitsNeutralNonBlockingFYI(t *testing.T) {
	cmds, repo, buf := newTransitionTestRepo(t, false)
	cmds.Config.RunAssociatedSpecsOnComplete = true
	headSHA := gitOutput(t, repo, "rev-parse", "HEAD")

	sealedID := "prov-2026-cc000021"
	// Sealed record with NO associated specs.
	writeRecord(t, repo, sealedID,
		customRecord(sealedID, "implemented", headSHA, []string{"pkg/foo.go"}, nil))

	rID := "prov-2026-cc000022"
	writeRecord(t, repo, rID,
		customRecord(rID, "open", "", []string{"pkg/foo.go"}, nil))
	commitFileChange(t, repo, "pkg/foo.go", "package foo\n", rID)
	reloadRecords(t, cmds)

	if err := cmds.Complete(CompleteOptions{RecordID: rID}); err != nil {
		t.Fatalf("completion should not be blocked when there are no specs to run: %v", err)
	}
	if got := loadedRecord(t, cmds, rID).Status; got != StatusImplemented {
		t.Errorf("status = %q, want implemented", got)
	}
	msg := buf.String()
	if !strings.Contains(msg, "(non-blocking)") || !strings.Contains(msg, sealedID) {
		t.Errorf("expected a single neutral non-blocking FYI naming %s, got:\n%s", sealedID, msg)
	}
}

// --- Open lifecycle heads-up ------------------------------------------------

func TestOpen_DeclaredOverlap_EmitsNonBlockingFYI(t *testing.T) {
	cmds, repo, buf := newTransitionTestRepo(t, false)
	headSHA := gitOutput(t, repo, "rev-parse", "HEAD")

	// Use a scope path that exists relative to the test's cwd (the package dir),
	// because the open lint guard (non-terminal) errors on missing scope paths.
	const scopeFile = "commands.go"

	sealedID := "prov-2026-cc000031"
	writeRecord(t, repo, sealedID,
		customRecord(sealedID, "implemented", headSHA, []string{scopeFile}, nil))

	// R is opened (non-terminal), so under strict enforcement it needs an
	// associated_spec whose path exists relative to the test cwd.
	rID := "prov-2026-cc000032"
	rYAML := `id: ` + rID + `
title: Opening record
status: draft
created_at: "2026-01-01"
author: test@example.com
sealed_at_sha: ""
intent: Governs commands.
constraints:
  - Must hold
affected_scope:
  - ` + scopeFile + `
forbidden_scope: []
type: blueprint
supersedes: ""
superseded_by: ""
related: []
associated_specs:
  - path: ` + scopeFile + `
    type: shell
    run_command: "true"
associated_traces: []
monitors: []
tags: []
`
	writeRecord(t, repo, rID, rYAML)
	reloadRecords(t, cmds)

	if err := cmds.Open(OpenOptions{RecordID: rID}); err != nil {
		t.Fatalf("open should succeed: %v", err)
	}
	if got := loadedRecord(t, cmds, rID).Status; got != StatusOpen {
		t.Errorf("status = %q, want open", got)
	}
	msg := buf.String()
	if !strings.Contains(msg, "(non-blocking)") || !strings.Contains(msg, sealedID) {
		t.Errorf("expected a non-blocking declared-overlap FYI naming %s, got:\n%s", sealedID, msg)
	}
}

// --- Signal removed from routine check & lint -------------------------------

func TestCheckStaged_NoStaleScopeWarning(t *testing.T) {
	cmds, repo, buf := newTransitionTestRepo(t, false)
	cmds.Checker = NewCommitChecker(cmds.Git, cmds.Loader)
	headSHA := gitOutput(t, repo, "rev-parse", "HEAD")

	sealedID := "prov-2026-cc000041"
	writeRecord(t, repo, sealedID,
		customRecord(sealedID, "implemented", headSHA, []string{"pkg/foo.go"}, nil))

	// Stage a change to a file inside the sealed record's scope.
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pkg", "foo.go"), []byte("package foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, repo, "add", "pkg/foo.go")

	msgFile := filepath.Join(repo, "MSG.txt")
	if err := os.WriteFile(msgFile, []byte("Change something [prov-2026-99999999]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cmds.Check(CheckOptions{Staged: true, MessageFile: msgFile}); err != nil {
		t.Fatalf("check should pass (no forbidden violations): %v", err)
	}
	if msg := buf.String(); strings.Contains(msg, "governed by implemented record") {
		t.Errorf("check must no longer emit stale-scope/governance-overlap warnings, got:\n%s", msg)
	}
}

func TestLintAll_NoScopeOverlapWarning(t *testing.T) {
	a := &Record{ID: "prov-2026-cc000051", Title: "A", Status: StatusOpen, Type: RecordTypeBlueprint,
		Intent: "x", Constraints: []string{"c"}, AffectedScope: []string{"pkg/shared.go"}}
	b := &Record{ID: "prov-2026-cc000052", Title: "B", Status: StatusOpen, Type: RecordTypeBlueprint,
		Intent: "x", Constraints: []string{"c"}, AffectedScope: []string{"pkg/shared.go"}}
	loader := &Loader{Records: []*Record{a, b}, RecordsByID: map[string]*Record{a.ID: a, b.ID: b}}
	linter := NewLinter(loader, "warn")

	result := linter.LintAll()
	for _, iss := range result.Issues {
		if strings.Contains(iss.Message, "Scope overlap") {
			t.Errorf("LintAll must not surface scope-overlap warnings, got: %q", iss.Message)
		}
	}
}
