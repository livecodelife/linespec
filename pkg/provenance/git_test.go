package provenance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
