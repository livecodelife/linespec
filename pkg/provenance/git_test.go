package provenance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitExec runs a git command in the given directory and fails the test on error.
func gitExec(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
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
	if err := os.WriteFile(oldPath, []byte(makeProvRecord(oldID, "open", `""`, `""`, nil)), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	gitExec(t, tmpDir, "add", oldRelPath)
	gitExec(t, tmpDir, "commit", "-m", "Initial: create old record ["+oldID+"]")

	return tmpDir, provDir, oldRelPath
}

func TestCheckForStaleScopeWarnings_MessageFormat(t *testing.T) {
	// Create a temporary git repo
	tmpDir, err := os.MkdirTemp("", "stale-scope-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init git repo: %v", err)
	}

	// Configure git user
	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git email: %v", err)
	}
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git name: %v", err)
	}
	cmd = exec.Command("git", "config", "commit.gpgsign", "false")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to disable commit signing: %v", err)
	}

	// Create a test file
	testFile := filepath.Join(tmpDir, "pkg", "test.go")
	if err := os.MkdirAll(filepath.Dir(testFile), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}
	if err := os.WriteFile(testFile, []byte("package test"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Make initial commit
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to add files: %v", err)
	}
	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to commit: %v", err)
	}

	// Get the sealed SHA (current HEAD)
	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = tmpDir
	sealedSHABytes, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get HEAD SHA: %v", err)
	}
	sealedSHA := strings.TrimSpace(string(sealedSHABytes))

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

	// Get the commit SHA
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = tmpDir
	shaBytes, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	commitSHA := strings.TrimSpace(string(shaBytes))

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
