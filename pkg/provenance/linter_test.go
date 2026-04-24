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

// ---- validateSupersessionType tests ----

func TestValidateSupersessionType_SameTier_Passes(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	oldRecord := &Record{ID: "prov-2026-001", Type: RecordTypeBlueprint}
	newRecord := &Record{ID: "prov-2026-002", Type: RecordTypeBlueprint, Supersedes: "prov-2026-001"}
	loader.Records = []*Record{oldRecord, newRecord}
	loader.RecordsByID = map[string]*Record{"prov-2026-001": oldRecord, "prov-2026-002": newRecord}

	linter := NewLinter(loader, "strict")
	result := &LintResult{}
	linter.validateSupersessionType(newRecord, result)

	if result.ErrorCount != 0 {
		t.Errorf("Expected no errors for same-tier supersession, got: %v", result.Issues)
	}
}

func TestValidateSupersessionType_CrossTier_Errors(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	oldRecord := &Record{ID: "prov-2026-001", Type: RecordTypeBrief}
	newRecord := &Record{ID: "prov-2026-002", Type: RecordTypeBlueprint, Supersedes: "prov-2026-001"}
	loader.Records = []*Record{oldRecord, newRecord}
	loader.RecordsByID = map[string]*Record{"prov-2026-001": oldRecord, "prov-2026-002": newRecord}

	linter := NewLinter(loader, "strict")
	result := &LintResult{}
	linter.validateSupersessionType(newRecord, result)

	if result.ErrorCount != 1 {
		t.Errorf("Expected 1 error for cross-tier supersession, got %d: %v", result.ErrorCount, result.Issues)
	}
	if len(result.Issues) > 0 && result.Issues[0].Field != "supersedes" {
		t.Errorf("Expected field 'supersedes', got %q", result.Issues[0].Field)
	}
}

func TestValidateSupersessionType_DefaultBlueprint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	// Both records have no type set — should default to blueprint and pass
	oldRecord := &Record{ID: "prov-2026-001"}
	newRecord := &Record{ID: "prov-2026-002", Supersedes: "prov-2026-001"}
	loader.Records = []*Record{oldRecord, newRecord}
	loader.RecordsByID = map[string]*Record{"prov-2026-001": oldRecord, "prov-2026-002": newRecord}

	linter := NewLinter(loader, "strict")
	result := &LintResult{}
	linter.validateSupersessionType(newRecord, result)

	if result.ErrorCount != 0 {
		t.Errorf("Expected no errors when both records default to blueprint, got: %v", result.Issues)
	}
}

func TestValidateSupersessionType_MissingTarget_NoDoubleError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	newRecord := &Record{ID: "prov-2026-002", Type: RecordTypeBlueprint, Supersedes: "prov-2026-nonexistent"}
	loader.Records = []*Record{newRecord}
	loader.RecordsByID = map[string]*Record{"prov-2026-002": newRecord}

	linter := NewLinter(loader, "strict")
	result := &LintResult{}
	linter.validateSupersessionType(newRecord, result)

	// Should produce no additional error — validateSupersedes already handles missing targets
	if result.ErrorCount != 0 {
		t.Errorf("Expected no error from type check when target missing, got: %v", result.Issues)
	}
}

// ---- validateImplements tests ----

func TestValidateImplements_ValidBlueprintImplementsBrief(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	brief := &Record{ID: "prov-2026-001", Type: RecordTypeBrief}
	blueprint := &Record{ID: "prov-2026-002", Type: RecordTypeBlueprint, Implements: "prov-2026-001"}
	loader.Records = []*Record{brief, blueprint}
	loader.RecordsByID = map[string]*Record{"prov-2026-001": brief, "prov-2026-002": blueprint}

	linter := NewLinter(loader, "strict")
	result := &LintResult{}
	linter.validateImplements(blueprint, result)

	if result.ErrorCount != 0 || result.WarningCount != 0 {
		t.Errorf("Expected no issues for blueprint implements brief, got: %v", result.Issues)
	}
}

func TestValidateImplements_ValidImprintImplementsBlueprint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	blueprint := &Record{ID: "prov-2026-001", Type: RecordTypeBlueprint}
	imprint := &Record{ID: "prov-2026-002", Type: RecordTypeImprint, Implements: "prov-2026-001"}
	loader.Records = []*Record{blueprint, imprint}
	loader.RecordsByID = map[string]*Record{"prov-2026-001": blueprint, "prov-2026-002": imprint}

	linter := NewLinter(loader, "strict")
	result := &LintResult{}
	linter.validateImplements(imprint, result)

	if result.ErrorCount != 0 || result.WarningCount != 0 {
		t.Errorf("Expected no issues for imprint implements blueprint, got: %v", result.Issues)
	}
}

func TestValidateImplements_ImprintImplementsBrief_SkipsTier(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	brief := &Record{ID: "prov-2026-001", Type: RecordTypeBrief}
	imprint := &Record{ID: "prov-2026-002", Type: RecordTypeImprint, Implements: "prov-2026-001"}
	loader.Records = []*Record{brief, imprint}
	loader.RecordsByID = map[string]*Record{"prov-2026-001": brief, "prov-2026-002": imprint}

	linter := NewLinter(loader, "strict")
	result := &LintResult{}
	linter.validateImplements(imprint, result)

	if result.ErrorCount != 1 {
		t.Errorf("Expected 1 error for imprint implementing brief (skip), got %d: %v", result.ErrorCount, result.Issues)
	}
}

func TestValidateImplements_BlueprintImplementsBlueprint_Sideways(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	bp1 := &Record{ID: "prov-2026-001", Type: RecordTypeBlueprint}
	bp2 := &Record{ID: "prov-2026-002", Type: RecordTypeBlueprint, Implements: "prov-2026-001"}
	loader.Records = []*Record{bp1, bp2}
	loader.RecordsByID = map[string]*Record{"prov-2026-001": bp1, "prov-2026-002": bp2}

	linter := NewLinter(loader, "strict")
	result := &LintResult{}
	linter.validateImplements(bp2, result)

	if result.ErrorCount != 1 {
		t.Errorf("Expected 1 error for blueprint implementing blueprint, got %d: %v", result.ErrorCount, result.Issues)
	}
}

func TestValidateImplements_BriefImplementsAnything_Errors(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	target := &Record{ID: "prov-2026-001", Type: RecordTypeBrief}
	brief := &Record{ID: "prov-2026-002", Type: RecordTypeBrief, Implements: "prov-2026-001"}
	loader.Records = []*Record{target, brief}
	loader.RecordsByID = map[string]*Record{"prov-2026-001": target, "prov-2026-002": brief}

	linter := NewLinter(loader, "strict")
	result := &LintResult{}
	linter.validateImplements(brief, result)

	if result.ErrorCount != 1 {
		t.Errorf("Expected 1 error for brief using implements, got %d: %v", result.ErrorCount, result.Issues)
	}
}

func TestValidateImplements_MissingTarget_Errors(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	blueprint := &Record{ID: "prov-2026-002", Type: RecordTypeBlueprint, Implements: "prov-2026-nonexistent"}
	loader.Records = []*Record{blueprint}
	loader.RecordsByID = map[string]*Record{"prov-2026-002": blueprint}

	linter := NewLinter(loader, "strict")
	result := &LintResult{}
	linter.validateImplements(blueprint, result)

	if result.ErrorCount != 1 {
		t.Errorf("Expected 1 error for unresolved implements, got %d: %v", result.ErrorCount, result.Issues)
	}
	if len(result.Issues) > 0 && result.Issues[0].Field != "implements" {
		t.Errorf("Expected field 'implements', got %q", result.Issues[0].Field)
	}
}

func TestValidateImplements_CrossRepoReference_Warns(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	blueprint := &Record{ID: "prov-2026-002", Type: RecordTypeBlueprint, Implements: "product:prov-2026-001"}
	loader.Records = []*Record{blueprint}
	loader.RecordsByID = map[string]*Record{"prov-2026-002": blueprint}

	linter := NewLinter(loader, "strict")
	result := &LintResult{}
	linter.validateImplements(blueprint, result)

	if result.ErrorCount != 0 {
		t.Errorf("Expected no errors for cross-repo reference, got: %v", result.Issues)
	}
	if result.WarningCount != 1 {
		t.Errorf("Expected 1 warning for cross-repo reference, got %d: %v", result.WarningCount, result.Issues)
	}
}

func TestValidateImplements_InvalidIDFormat_Errors(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	blueprint := &Record{ID: "prov-2026-002", Type: RecordTypeBlueprint, Implements: "not-a-valid-id"}
	loader.Records = []*Record{blueprint}
	loader.RecordsByID = map[string]*Record{"prov-2026-002": blueprint}

	linter := NewLinter(loader, "strict")
	result := &LintResult{}
	linter.validateImplements(blueprint, result)

	if result.ErrorCount != 1 {
		t.Errorf("Expected 1 error for invalid ID format, got %d: %v", result.ErrorCount, result.Issues)
	}
}

func TestValidateImplements_EmptyField_NoIssue(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	blueprint := &Record{ID: "prov-2026-002", Type: RecordTypeBlueprint}
	loader.Records = []*Record{blueprint}
	loader.RecordsByID = map[string]*Record{"prov-2026-002": blueprint}

	linter := NewLinter(loader, "strict")
	result := &LintResult{}
	linter.validateImplements(blueprint, result)

	if result.ErrorCount != 0 || result.WarningCount != 0 {
		t.Errorf("Expected no issues for missing implements field, got: %v", result.Issues)
	}
}

func TestValidateImplements_NoneEnforcement_StillErrors(t *testing.T) {
	// Implements checks are always-on regardless of enforcement level
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	brief := &Record{ID: "prov-2026-001", Type: RecordTypeBrief}
	imprint := &Record{ID: "prov-2026-002", Type: RecordTypeImprint, Implements: "prov-2026-001"}
	loader.Records = []*Record{brief, imprint}
	loader.RecordsByID = map[string]*Record{"prov-2026-001": brief, "prov-2026-002": imprint}

	linter := NewLinter(loader, "none") // enforcement: none
	result := &LintResult{}
	linter.validateImplements(imprint, result)

	if result.ErrorCount != 1 {
		t.Errorf("Expected 1 error even at enforcement=none, got %d: %v", result.ErrorCount, result.Issues)
	}
}
