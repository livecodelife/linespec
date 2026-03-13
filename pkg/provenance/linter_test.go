package provenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAssociatedSpecs_AllStatErrors(t *testing.T) {
	// Create a temp directory for testing
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a file that exists
	existingFile := filepath.Join(tmpDir, "existing.txt")
	if err := os.WriteFile(existingFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create a directory (should be flagged as error)
	testDir := filepath.Join(tmpDir, "testdir")
	if err := os.Mkdir(testDir, 0755); err != nil {
		t.Fatalf("Failed to create test dir: %v", err)
	}

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "strict")

	// Test 1: Non-existent file should error
	record1 := &Record{
		ID:     "prov-2025-001",
		Status: StatusOpen,
		AssociatedSpecs: []AssociatedSpec{
			{Path: filepath.Join(tmpDir, "nonexistent.txt")},
		},
	}
	result1 := &LintResult{}
	linter.validateAssociatedSpecs(record1, result1)
	if result1.ErrorCount != 1 {
		t.Errorf("Expected 1 error for non-existent file, got %d", result1.ErrorCount)
	}

	// Test 2: Existing file should pass
	record2 := &Record{
		ID:     "prov-2025-002",
		Status: StatusOpen,
		AssociatedSpecs: []AssociatedSpec{
			{Path: existingFile},
		},
	}
	result2 := &LintResult{}
	linter.validateAssociatedSpecs(record2, result2)
	if result2.ErrorCount != 0 {
		t.Errorf("Expected 0 errors for existing file, got %d: %v", result2.ErrorCount, result2.Issues)
	}

	// Test 3: Directory should error
	record3 := &Record{
		ID:     "prov-2025-003",
		Status: StatusOpen,
		AssociatedSpecs: []AssociatedSpec{
			{Path: testDir},
		},
	}
	result3 := &LintResult{}
	linter.validateAssociatedSpecs(record3, result3)
	if result3.ErrorCount != 1 {
		t.Errorf("Expected 1 error for directory path, got %d", result3.ErrorCount)
	}
}

func TestValidateScopePaths_ExactPath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test files
	existingFile := filepath.Join(tmpDir, "existing.go")
	if err := os.WriteFile(existingFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "strict")

	// Test 1: Non-existent exact path should error for open records
	record1 := &Record{
		ID:            "prov-2025-001",
		Status:        StatusOpen,
		AffectedScope: []string{filepath.Join(tmpDir, "nonexistent.go")},
	}
	result1 := &LintResult{}
	linter.validateScopePaths(record1, result1)
	if result1.ErrorCount != 1 {
		t.Errorf("Expected 1 error for non-existent exact path, got %d: %v", result1.ErrorCount, result1.Issues)
	}

	// Test 2: Existing exact path should pass
	record2 := &Record{
		ID:            "prov-2025-002",
		Status:        StatusOpen,
		AffectedScope: []string{existingFile},
	}
	result2 := &LintResult{}
	linter.validateScopePaths(record2, result2)
	if result2.ErrorCount != 0 {
		t.Errorf("Expected 0 errors for existing exact path, got %d: %v", result2.ErrorCount, result2.Issues)
	}

	// Test 3: Non-open records should not validate scope paths
	record3 := &Record{
		ID:            "prov-2025-003",
		Status:        StatusImplemented,
		AffectedScope: []string{filepath.Join(tmpDir, "nonexistent.go")},
	}
	result3 := &LintResult{}
	linter.validateScopePaths(record3, result3)
	if result3.ErrorCount != 0 {
		t.Errorf("Expected 0 errors for non-open record, got %d", result3.ErrorCount)
	}
}

func TestValidateScopePaths_GlobPattern(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test files
	if err := os.WriteFile(filepath.Join(tmpDir, "file1.go"), []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "file2.go"), []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "strict")

	// Test 1: Glob matching files should pass
	record1 := &Record{
		ID:            "prov-2025-001",
		Status:        StatusOpen,
		AffectedScope: []string{filepath.Join(tmpDir, "*.go")},
	}
	result1 := &LintResult{}
	linter.validateScopePaths(record1, result1)
	if result1.ErrorCount != 0 {
		t.Errorf("Expected 0 errors for glob matching files, got %d: %v", result1.ErrorCount, result1.Issues)
	}

	// Test 2: Glob matching no files should error
	record2 := &Record{
		ID:            "prov-2025-002",
		Status:        StatusOpen,
		AffectedScope: []string{filepath.Join(tmpDir, "*.nonexistent")},
	}
	result2 := &LintResult{}
	linter.validateScopePaths(record2, result2)
	if result2.ErrorCount != 1 {
		t.Errorf("Expected 1 error for glob matching no files, got %d: %v", result2.ErrorCount, result2.Issues)
	}
}

func TestValidateScopePaths_RegexPattern(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Change to temp dir for regex matching
	originalWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(originalWd)

	// Create test files
	if err := os.WriteFile(filepath.Join(tmpDir, "test_123.go"), []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "test_456.go"), []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "strict")

	// Test 1: Regex matching files should pass
	record1 := &Record{
		ID:            "prov-2025-001",
		Status:        StatusOpen,
		AffectedScope: []string{"re:test_\\d+\\.go"},
	}
	result1 := &LintResult{}
	linter.validateScopePaths(record1, result1)
	if result1.ErrorCount != 0 {
		t.Errorf("Expected 0 errors for regex matching files, got %d: %v", result1.ErrorCount, result1.Issues)
	}

	// Test 2: Regex matching no files should error
	record2 := &Record{
		ID:            "prov-2025-002",
		Status:        StatusOpen,
		AffectedScope: []string{"re:nonexistent_\\d+\\.go"},
	}
	result2 := &LintResult{}
	linter.validateScopePaths(record2, result2)
	if result2.ErrorCount != 1 {
		t.Errorf("Expected 1 error for regex matching no files, got %d: %v", result2.ErrorCount, result2.Issues)
	}
}

func TestValidateScopePaths_DirectoryError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a directory
	testDir := filepath.Join(tmpDir, "pkg")
	if err := os.Mkdir(testDir, 0755); err != nil {
		t.Fatalf("Failed to create test dir: %v", err)
	}

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "strict")

	// Directory path should error
	record := &Record{
		ID:            "prov-2025-001",
		Status:        StatusOpen,
		AffectedScope: []string{testDir},
	}
	result := &LintResult{}
	linter.validateScopePaths(record, result)

	foundDirError := false
	for _, issue := range result.Issues {
		if issue.Severity == SeverityError && strings.Contains(issue.Message, "directory") {
			foundDirError = true
			break
		}
	}
	if !foundDirError {
		t.Errorf("Expected error for directory path, got issues: %v", result.Issues)
	}
}
