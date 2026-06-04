package provenance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cleanGitEnv returns os.Environ() with all GIT_* variables removed so that
// subprocess git commands are not misdirected by a parent git hook's GIT_DIR.
func cleanGitEnv() []string {
	env := os.Environ()
	out := env[:0]
	for _, e := range env {
		if !strings.HasPrefix(e, "GIT_") {
			out = append(out, e)
		}
	}
	return out
}

// gitExec runs a git command in the given directory and fails the test on error.
func gitExec(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitOutput runs a git command in the given directory and returns its stdout.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// clearGitEnvForTest temporarily removes all GIT_* environment variables from
// the process environment for the duration of the test. This prevents a parent
// git hook's GIT_DIR or GIT_INDEX_FILE from misdirecting provenance functions
// that spawn git subprocesses internally.
func clearGitEnvForTest(t *testing.T) {
	t.Helper()
	type kv struct{ k, v string }
	var saved []kv
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "GIT_") {
			parts := strings.SplitN(e, "=", 2)
			val := ""
			if len(parts) == 2 {
				val = parts[1]
			}
			os.Unsetenv(parts[0])
			saved = append(saved, kv{parts[0], val})
		}
	}
	t.Cleanup(func() {
		for _, pair := range saved {
			os.Setenv(pair.k, pair.v)
		}
	})
}

// makeProvRecord returns a minimal provenance record YAML string.
// affectedScope is a slice of patterns; pass nil for no scope restriction (observed mode).
func makeProvRecord(id, status, supersedes, supersededBy string, affectedScope []string) string {
	scopeYAML := "[]"
	if len(affectedScope) > 0 {
		lines := ""
		for _, p := range affectedScope {
			lines += "\n  - " + p
		}
		scopeYAML = lines
	}
	return fmt.Sprintf(`id: %s
title: Test record %s
status: %s
created_at: "2026-01-01"
author: test@example.com
intent: ""
constraints: []
affected_scope: %s
forbidden_scope: []
supersedes: %s
superseded_by: %s
related: []
sealed_at_sha: ""
associated_specs: []
associated_traces: []
monitors: []
tags: []
`, id, id, status, scopeYAML, supersedes, supersededBy)
}

// setupSupersessionRepo creates a temporary git repo with an old open provenance record
// committed to HEAD. It returns (repoDir, provDir, oldRecordRelPath).
func setupSupersessionRepo(t *testing.T, oldID string) (string, string, string) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "supersession-test")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}

	gitExec(t, tmpDir, "init")
	gitExec(t, tmpDir, "config", "user.email", "test@example.com")
	gitExec(t, tmpDir, "config", "user.name", "Test User")
	gitExec(t, tmpDir, "config", "commit.gpgsign", "false")

	provDir := filepath.Join(tmpDir, "provenance")
	if err := os.MkdirAll(provDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	oldRelPath := "provenance/" + oldID + ".yml"
	oldPath := filepath.Join(tmpDir, oldRelPath)
	if err := os.WriteFile(oldPath, []byte(makeProvRecord(oldID, "implemented", `""`, `""`, nil)), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	gitExec(t, tmpDir, "add", oldRelPath)
	gitExec(t, tmpDir, "commit", "-m", "Initial: create old record ["+oldID+"]")

	return tmpDir, provDir, oldRelPath
}

func TestCheckForStaleScopeWarnings_MessageFormat(t *testing.T) {
	clearGitEnvForTest(t)
	tmpDir, err := os.MkdirTemp("", "stale-scope-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	gitExec(t, tmpDir, "init")
	gitExec(t, tmpDir, "config", "user.email", "test@example.com")
	gitExec(t, tmpDir, "config", "user.name", "Test User")
	gitExec(t, tmpDir, "config", "commit.gpgsign", "false")

	testFile := filepath.Join(tmpDir, "pkg", "test.go")
	if err := os.MkdirAll(filepath.Dir(testFile), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}
	if err := os.WriteFile(testFile, []byte("package test"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	gitExec(t, tmpDir, "add", ".")
	gitExec(t, tmpDir, "commit", "-m", "Initial commit")

	sealedSHA := gitOutput(t, tmpDir, "rev-parse", "HEAD")

	// Create a CommitChecker
	git := NewGit(tmpDir)
	loader := NewLoader(tmpDir, nil)
	checker := NewCommitChecker(git, loader)

	// Create an implemented record with the sealed SHA
	record := &Record{
		ID:            "prov-2026-032",
		Status:        StatusImplemented,
		SealedAtSHA:   sealedSHA,
		AffectedScope: []string{"pkg/test.go"},
	}

	// Test: User is modifying a file in scope that hasn't changed since sealing
	changedFiles := []string{"pkg/test.go"}
	warnings := checker.CheckForStaleScopeWarnings(record, changedFiles)

	if len(warnings) != 1 {
		t.Fatalf("Expected 1 warning, got %d", len(warnings))
	}

	warning := warnings[0]

	// Verify the warning contains the expected components
	if warning.RecordID != "prov-2026-032" {
		t.Errorf("Expected RecordID to be 'prov-2026-032', got %q", warning.RecordID)
	}

	if warning.File != "pkg/test.go" {
		t.Errorf("Expected File to be 'pkg/test.go', got %q", warning.File)
	}

	// Verify message contains required elements per prov-2026-032
	if !strings.Contains(warning.Message, "You are modifying") {
		t.Errorf("Message should indicate user is modifying a file")
	}

	if !strings.Contains(warning.Message, "prov-2026-032") {
		t.Errorf("Message should contain the record ID")
	}

	if !strings.Contains(warning.Message, sealedSHA[:7]) {
		t.Errorf("Message should contain the sealed SHA short form")
	}

	if !strings.Contains(warning.Message, "Implemented records should not need further changes") {
		t.Errorf("Message should explain that implemented records shouldn't need changes")
	}

	if !strings.Contains(warning.Message, "create a superseding record") {
		t.Errorf("Message should suggest creating a superseding record")
	}

	if !strings.Contains(warning.Message, "linespec provenance create") {
		t.Errorf("Message should include the CLI command to create a superseding record")
	}

	if !strings.Contains(warning.Message, "--supersedes prov-2026-032") {
		t.Errorf("Message should include the --supersedes flag with the correct record ID")
	}
}

// TestCheckStaged_SupersessionTransition verifies that CheckStaged allows the old record
// file to be staged when a new record with --supersedes is being committed.
func TestCheckStaged_SupersessionTransition(t *testing.T) {
	clearGitEnvForTest(t)
	const oldID = "prov-2026-aaa00001"
	const newID = "prov-2026-bbb00002"

	tmpDir, provDir, oldRelPath := setupSupersessionRepo(t, oldID)
	defer os.RemoveAll(tmpDir)

	// Update the old record: open → superseded
	oldPath := filepath.Join(tmpDir, oldRelPath)
	if err := os.WriteFile(oldPath, []byte(makeProvRecord(oldID, "superseded", `""`, newID, nil)), 0644); err != nil {
		t.Fatalf("WriteFile old record: %v", err)
	}

	// New record has affected_scope restricted to an implementation file (not the old record
	// file). Without the supersession exception the old record file would fail the scope check.
	newRelPath := "provenance/" + newID + ".yml"
	newPath := filepath.Join(tmpDir, newRelPath)
	if err := os.WriteFile(newPath, []byte(makeProvRecord(newID, "open", oldID, `""`, []string{"pkg/impl.go"})), 0644); err != nil {
		t.Fatalf("WriteFile new record: %v", err)
	}

	// Stage both files
	gitExec(t, tmpDir, "add", oldRelPath, newRelPath)

	// Initialize loader from disk (sees the modified state of both records)
	loader := NewLoader(provDir, nil)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	checker := NewCommitChecker(NewGit(tmpDir), loader)

	// Write commit message file tagged with the new record ID
	msgFile := filepath.Join(tmpDir, "COMMIT_EDITMSG")
	if err := os.WriteFile(msgFile, []byte("Create superseding record ["+newID+"]\n"), 0644); err != nil {
		t.Fatalf("WriteFile msgFile: %v", err)
	}

	violations, err := checker.CheckStaged(msgFile, false)
	if err != nil {
		t.Fatalf("CheckStaged returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("Expected no violations for valid supersession transition, got %d: %v", len(violations), violations)
	}
}

// TestCheckStaged_SupersessionTransition_WrongSupersededBy verifies that CheckStaged still
// rejects the old record file when superseded_by points to a different record.
func TestCheckStaged_SupersessionTransition_WrongSupersededBy(t *testing.T) {
	clearGitEnvForTest(t)
	const oldID = "prov-2026-aaa00003"
	const newID = "prov-2026-bbb00004"
	const otherID = "prov-2026-ccc00005"

	tmpDir, provDir, oldRelPath := setupSupersessionRepo(t, oldID)
	defer os.RemoveAll(tmpDir)

	// Update old record to point to a DIFFERENT record (not newID)
	oldPath := filepath.Join(tmpDir, oldRelPath)
	if err := os.WriteFile(oldPath, []byte(makeProvRecord(oldID, "superseded", `""`, otherID, nil)), 0644); err != nil {
		t.Fatalf("WriteFile old record: %v", err)
	}

	// New record has a restricted scope so that the old record file triggers a scope check.
	newRelPath := "provenance/" + newID + ".yml"
	newPath := filepath.Join(tmpDir, newRelPath)
	if err := os.WriteFile(newPath, []byte(makeProvRecord(newID, "open", oldID, `""`, []string{"pkg/impl.go"})), 0644); err != nil {
		t.Fatalf("WriteFile new record: %v", err)
	}

	// Also create the other record so the loader doesn't fail on missing reference
	otherRelPath := "provenance/" + otherID + ".yml"
	otherPath := filepath.Join(tmpDir, otherRelPath)
	if err := os.WriteFile(otherPath, []byte(makeProvRecord(otherID, "open", `""`, `""`, nil)), 0644); err != nil {
		t.Fatalf("WriteFile other record: %v", err)
	}
	gitExec(t, tmpDir, "add", otherRelPath)
	gitExec(t, tmpDir, "commit", "-m", "add other record")

	gitExec(t, tmpDir, "add", oldRelPath, newRelPath)

	loader := NewLoader(provDir, nil)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	checker := NewCommitChecker(NewGit(tmpDir), loader)

	msgFile := filepath.Join(tmpDir, "COMMIT_EDITMSG")
	if err := os.WriteFile(msgFile, []byte("Create superseding record ["+newID+"]\n"), 0644); err != nil {
		t.Fatalf("WriteFile msgFile: %v", err)
	}

	violations, err := checker.CheckStaged(msgFile, false)
	if err != nil {
		t.Fatalf("CheckStaged returned error: %v", err)
	}
	// The old record file should NOT be allowed because superseded_by doesn't match newID
	if len(violations) == 0 {
		t.Error("Expected violations when superseded_by points to wrong record, got none")
	}
}

// TestCheckCommit_SupersessionTransition verifies that CheckCommit allows the old record
// file in a historical supersession commit.
func TestCheckCommit_SupersessionTransition(t *testing.T) {
	clearGitEnvForTest(t)
	const oldID = "prov-2026-aaa00006"
	const newID = "prov-2026-bbb00007"

	tmpDir, provDir, oldRelPath := setupSupersessionRepo(t, oldID)
	defer os.RemoveAll(tmpDir)

	// Update old record: open → superseded
	oldPath := filepath.Join(tmpDir, oldRelPath)
	if err := os.WriteFile(oldPath, []byte(makeProvRecord(oldID, "superseded", `""`, newID, nil)), 0644); err != nil {
		t.Fatalf("WriteFile old record: %v", err)
	}

	// New record has a restricted scope so that the old record file must pass via the exception.
	newRelPath := "provenance/" + newID + ".yml"
	newPath := filepath.Join(tmpDir, newRelPath)
	if err := os.WriteFile(newPath, []byte(makeProvRecord(newID, "open", oldID, `""`, []string{"pkg/impl.go"})), 0644); err != nil {
		t.Fatalf("WriteFile new record: %v", err)
	}

	// Commit the supersession
	gitExec(t, tmpDir, "add", oldRelPath, newRelPath)
	gitExec(t, tmpDir, "commit", "-m", "Supersede old record ["+newID+"]")

	commitSHA := gitOutput(t, tmpDir, "rev-parse", "HEAD")

	loader := NewLoader(provDir, nil)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	checker := NewCommitChecker(NewGit(tmpDir), loader)

	violations, err := checker.CheckCommit(commitSHA)
	if err != nil {
		t.Fatalf("CheckCommit returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("Expected no violations for valid supersession commit, got %d: %v", len(violations), violations)
	}
}

// TestCheckStaged_SupersessionTransition_OpenRecordRejected verifies that the supersession
// exception does not fire when the old record is open (only implemented records may be superseded).
func TestCheckStaged_SupersessionTransition_OpenRecordRejected(t *testing.T) {
	clearGitEnvForTest(t)
	const oldID = "prov-2026-aaa00010"
	const newID = "prov-2026-bbb00011"

	// setupSupersessionRepo creates the old record as implemented; override it to open.
	tmpDir, provDir, oldRelPath := setupSupersessionRepo(t, oldID)
	defer os.RemoveAll(tmpDir)

	// Overwrite the already-committed record so HEAD has it as open.
	oldPath := filepath.Join(tmpDir, oldRelPath)
	if err := os.WriteFile(oldPath, []byte(makeProvRecord(oldID, "open", `""`, `""`, nil)), 0644); err != nil {
		t.Fatalf("WriteFile old record: %v", err)
	}
	gitExec(t, tmpDir, "add", oldRelPath)
	gitExec(t, tmpDir, "commit", "-m", "reset old record to open ["+oldID+"]")

	// Now stage the old record as superseded and add the new record.
	if err := os.WriteFile(oldPath, []byte(makeProvRecord(oldID, "superseded", `""`, newID, nil)), 0644); err != nil {
		t.Fatalf("WriteFile old record superseded: %v", err)
	}
	newRelPath := "provenance/" + newID + ".yml"
	newPath := filepath.Join(tmpDir, newRelPath)
	if err := os.WriteFile(newPath, []byte(makeProvRecord(newID, "open", oldID, `""`, []string{"pkg/impl.go"})), 0644); err != nil {
		t.Fatalf("WriteFile new record: %v", err)
	}
	gitExec(t, tmpDir, "add", oldRelPath, newRelPath)

	loader := NewLoader(provDir, nil)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	checker := NewCommitChecker(NewGit(tmpDir), loader)

	msgFile := filepath.Join(tmpDir, "COMMIT_EDITMSG")
	if err := os.WriteFile(msgFile, []byte("Supersede open record ["+newID+"]\n"), 0644); err != nil {
		t.Fatalf("WriteFile msgFile: %v", err)
	}

	violations, err := checker.CheckStaged(msgFile, false)
	if err != nil {
		t.Fatalf("CheckStaged returned error: %v", err)
	}
	if len(violations) == 0 {
		t.Error("Expected violations when superseding an open record, got none")
	}
}

// TestCheckStaged_DraftSelfModification verifies that a draft record's own YAML file
// is allowed in a commit tagged with that record's ID, even when affected_scope is
// set to specific files that do not include the record file itself.
func TestCheckStaged_DraftSelfModification(t *testing.T) {
	clearGitEnvForTest(t)
	const draftID = "prov-2026-ddd00001"

	tmpDir, err := os.MkdirTemp("", "draft-self-mod-test")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	gitExec(t, tmpDir, "init")
	gitExec(t, tmpDir, "config", "user.email", "test@example.com")
	gitExec(t, tmpDir, "config", "user.name", "Test User")
	gitExec(t, tmpDir, "config", "commit.gpgsign", "false")

	// Commit an unrelated file so HEAD exists
	placeholder := filepath.Join(tmpDir, "README.md")
	if err := os.WriteFile(placeholder, []byte("readme"), 0644); err != nil {
		t.Fatalf("WriteFile placeholder: %v", err)
	}
	gitExec(t, tmpDir, "add", "README.md")
	gitExec(t, tmpDir, "commit", "-m", "Initial commit")

	provDir := filepath.Join(tmpDir, "provenance")
	if err := os.MkdirAll(provDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Draft record with affected_scope restricted to pkg/impl.go — does NOT include its own file
	draftRelPath := "provenance/" + draftID + ".yml"
	draftPath := filepath.Join(tmpDir, draftRelPath)
	if err := os.WriteFile(draftPath, []byte(makeProvRecord(draftID, "draft", `""`, `""`, []string{"pkg/impl.go"})), 0644); err != nil {
		t.Fatalf("WriteFile draft record: %v", err)
	}

	gitExec(t, tmpDir, "add", draftRelPath)

	loader := NewLoader(provDir, nil)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	checker := NewCommitChecker(NewGit(tmpDir), loader)

	msgFile := filepath.Join(tmpDir, "COMMIT_EDITMSG")
	if err := os.WriteFile(msgFile, []byte("Create draft record ["+draftID+"]\n"), 0644); err != nil {
		t.Fatalf("WriteFile msgFile: %v", err)
	}

	violations, err := checker.CheckStaged(msgFile, false)
	if err != nil {
		t.Fatalf("CheckStaged returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("Expected no violations for draft self-modification, got %d: %v", len(violations), violations)
	}
}

// TestCheckStaged_HashManifestExempt verifies that .linespec/hash_manifest.json staged
// alongside a completion commit never triggers a scope violation, even when the record
// has a restricted affected_scope that does not include the manifest.
func TestCheckStaged_HashManifestExempt(t *testing.T) {
	clearGitEnvForTest(t)
	const recID = "prov-2026-hm00001"

	tmpDir, provDir, recRelPath := setupSupersessionRepo(t, recID)
	defer os.RemoveAll(tmpDir)

	// Re-write the committed record with a narrow affected_scope (allowlist mode).
	recPath := filepath.Join(tmpDir, recRelPath)
	if err := os.WriteFile(recPath, []byte(makeProvRecord(recID, "open", `""`, `""`, []string{"pkg/impl.go"})), 0644); err != nil {
		t.Fatalf("WriteFile record: %v", err)
	}
	gitExec(t, tmpDir, "add", recRelPath)
	gitExec(t, tmpDir, "commit", "-m", "open record ["+recID+"]")

	// Stage the hash manifest (simulating what `linespec provenance complete` writes).
	manifestDir := filepath.Join(tmpDir, ".linespec")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manifestRelPath := ".linespec/hash_manifest.json"
	manifestPath := filepath.Join(tmpDir, manifestRelPath)
	if err := os.WriteFile(manifestPath, []byte(`{"records":{},"full_graph_hash":"abc","active_subset_hash":"def"}`), 0644); err != nil {
		t.Fatalf("WriteFile manifest: %v", err)
	}
	gitExec(t, tmpDir, "add", manifestRelPath)

	loader := NewLoader(provDir, nil)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	checker := NewCommitChecker(NewGit(tmpDir), loader)

	msgFile := filepath.Join(tmpDir, "COMMIT_EDITMSG")
	if err := os.WriteFile(msgFile, []byte("Complete provenance record ["+recID+"]\n"), 0644); err != nil {
		t.Fatalf("WriteFile msgFile: %v", err)
	}

	violations, err := checker.CheckStaged(msgFile, false)
	if err != nil {
		t.Fatalf("CheckStaged returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("Expected no violations for hash manifest staged with completion, got %d: %v", len(violations), violations)
	}
}

// TestCheckCommit_HashManifestExempt verifies that .linespec/hash_manifest.json in a
// historical commit does not trigger a scope violation.
func TestCheckCommit_HashManifestExempt(t *testing.T) {
	clearGitEnvForTest(t)
	const recID = "prov-2026-hm00002"

	tmpDir, provDir, recRelPath := setupSupersessionRepo(t, recID)
	defer os.RemoveAll(tmpDir)

	// Re-commit the record with a narrow affected_scope.
	recPath := filepath.Join(tmpDir, recRelPath)
	if err := os.WriteFile(recPath, []byte(makeProvRecord(recID, "open", `""`, `""`, []string{"pkg/impl.go"})), 0644); err != nil {
		t.Fatalf("WriteFile record: %v", err)
	}
	gitExec(t, tmpDir, "add", recRelPath)
	gitExec(t, tmpDir, "commit", "-m", "open record ["+recID+"]")

	// Commit the hash manifest — tagged with the record ID.
	manifestDir := filepath.Join(tmpDir, ".linespec")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manifestRelPath := ".linespec/hash_manifest.json"
	manifestPath := filepath.Join(tmpDir, manifestRelPath)
	if err := os.WriteFile(manifestPath, []byte(`{"records":{},"full_graph_hash":"abc","active_subset_hash":"def"}`), 0644); err != nil {
		t.Fatalf("WriteFile manifest: %v", err)
	}
	gitExec(t, tmpDir, "add", manifestRelPath)
	gitExec(t, tmpDir, "commit", "-m", "Complete provenance record ["+recID+"]")

	commitSHA := gitOutput(t, tmpDir, "rev-parse", "HEAD")

	loader := NewLoader(provDir, nil)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	checker := NewCommitChecker(NewGit(tmpDir), loader)

	violations, err := checker.CheckCommit(commitSHA)
	if err != nil {
		t.Fatalf("CheckCommit returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("Expected no violations for hash manifest in completion commit, got %d: %v", len(violations), violations)
	}
}
