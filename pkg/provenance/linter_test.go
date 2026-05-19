package provenance

import (
	"encoding/json"
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

	// Test 3: Terminal-state records produce a warning (not error) for missing paths
	record3 := &Record{
		ID:            "prov-2025-003",
		Status:        StatusImplemented,
		AffectedScope: []string{filepath.Join(tmpDir, "nonexistent.go")},
	}
	result3 := &LintResult{}
	linter.validateScopePaths(record3, result3)
	if result3.ErrorCount != 0 {
		t.Errorf("Expected 0 errors for terminal-state record with missing path, got %d", result3.ErrorCount)
	}
	if result3.WarningCount != 1 {
		t.Errorf("Expected 1 warning for terminal-state record with missing path, got %d", result3.WarningCount)
	}

	// Test 4: Draft records are skipped entirely
	record4 := &Record{
		ID:            "prov-2025-004",
		Status:        StatusDraft,
		AffectedScope: []string{filepath.Join(tmpDir, "nonexistent.go")},
	}
	result4 := &LintResult{}
	linter.validateScopePaths(record4, result4)
	if result4.ErrorCount != 0 || result4.WarningCount != 0 {
		t.Errorf("Expected no issues for draft record, got errors=%d warnings=%d", result4.ErrorCount, result4.WarningCount)
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

func TestValidateImplements_CrossRepoReference_InvalidFormat(t *testing.T) {
	// Colon-prefixed repo references (e.g. "product:prov-2026-001") are not valid
	// provenance IDs. Cross-repo resolution happens via the cache loader, not via
	// a special ID syntax — the implements field always holds a bare prov-YYYY-NNN ID.
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

	if result.ErrorCount != 1 {
		t.Errorf("Expected 1 error for colon-prefixed ID (invalid format), got %d: %v", result.ErrorCount, result.Issues)
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

func TestValidateImplements_UnresolvedErrorMentionsSharedRepos(t *testing.T) {
	// The error message for an unresolved implements reference must mention shared_repos
	// and linespec provenance sync so the user knows how to fix a cross-repo reference.
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	blueprint := &Record{ID: "prov-2026-002", Type: RecordTypeBlueprint, Implements: "prov-2026-aabbccdd"}
	loader.Records = []*Record{blueprint}
	loader.RecordsByID = map[string]*Record{"prov-2026-002": blueprint}

	linter := NewLinter(loader, "strict")
	result := &LintResult{}
	linter.validateImplements(blueprint, result)

	if result.ErrorCount != 1 {
		t.Fatalf("Expected 1 error, got %d", result.ErrorCount)
	}
	msg := result.Issues[0].Message
	if !strings.Contains(msg, "shared_repos") {
		t.Errorf("Error message should mention shared_repos, got: %s", msg)
	}
	if !strings.Contains(msg, "linespec provenance sync") {
		t.Errorf("Error message should mention 'linespec provenance sync', got: %s", msg)
	}
}

// --- Per-type field enforcement tests ---

func TestValidateRequiredFields_BriefRequiresConstraints(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "strict")

	brief := &Record{
		ID:          "prov-2026-001",
		Type:        RecordTypeBrief,
		Title:       "Test brief",
		Status:      StatusOpen,
		CreatedAt:   "2026-01-01",
		Author:      "test@example.com",
		Intent:      "some intent",
		Constraints: []string{},
	}
	result := &LintResult{}
	linter.validateRequiredFields(brief, result)

	if result.ErrorCount != 1 {
		t.Errorf("Expected 1 error for Brief missing constraints, got %d: %v", result.ErrorCount, result.Issues)
	}
	if result.ErrorCount == 1 && result.Issues[0].Field != "constraints" {
		t.Errorf("Expected error on field 'constraints', got %q", result.Issues[0].Field)
	}
}

func TestValidateRequiredFields_BriefWithConstraintsPasses(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "strict")

	brief := &Record{
		ID:          "prov-2026-001",
		Type:        RecordTypeBrief,
		Title:       "Test brief",
		Status:      StatusOpen,
		CreatedAt:   "2026-01-01",
		Author:      "test@example.com",
		Intent:      "some intent",
		Constraints: []string{"must do x"},
	}
	result := &LintResult{}
	linter.validateRequiredFields(brief, result)

	if result.ErrorCount != 0 {
		t.Errorf("Expected 0 errors for Brief with constraints, got %d: %v", result.ErrorCount, result.Issues)
	}
}

func TestValidateRequiredFields_ImprintRequiresImplements(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "strict")

	imprint := &Record{
		ID:         "prov-2026-002",
		Type:       RecordTypeImprint,
		Title:      "Test imprint",
		Status:     StatusOpen,
		CreatedAt:  "2026-01-01",
		Author:     "test@example.com",
		Intent:     "some intent",
		Implements: "",
	}
	result := &LintResult{}
	linter.validateRequiredFields(imprint, result)

	var implementsErrors []Issue
	for _, i := range result.Issues {
		if i.Field == "implements" {
			implementsErrors = append(implementsErrors, i)
		}
	}
	if len(implementsErrors) != 1 {
		t.Errorf("Expected 1 error on 'implements' for Imprint missing implements, got %d: %v", len(implementsErrors), result.Issues)
	}
}

func TestValidateRequiredFields_BriefConstraintsRequiredAtAllEnforcementLevels(t *testing.T) {
	// The Brief constraints requirement is always-on (graph integrity), not adoption maturity
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)

	brief := &Record{
		ID:          "prov-2026-001",
		Type:        RecordTypeBrief,
		Title:       "Test",
		Status:      StatusOpen,
		CreatedAt:   "2026-01-01",
		Author:      "test@example.com",
		Intent:      "intent",
		Constraints: []string{},
	}

	for _, level := range []string{"none", "warn", "strict"} {
		linter := NewLinter(loader, level)
		result := &LintResult{}
		linter.validateRequiredFields(brief, result)
		if result.ErrorCount == 0 {
			t.Errorf("enforcement=%s: expected error for Brief missing constraints", level)
		}
	}
}

// --- validateNotApplicableFields tests ---

func TestValidateNotApplicableFields_ExtendsOnNonBug(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "strict")

	for _, rt := range []RecordType{RecordTypeBrief, RecordTypeBlueprint, RecordTypeImprint} {
		record := &Record{ID: "prov-2026-001", Type: rt, Extends: "prov-2026-002"}
		result := &LintResult{}
		linter.validateNotApplicableFields(record, result)
		if result.ErrorCount != 1 {
			t.Errorf("type=%s: expected 1 error for extends on non-Bug, got %d", rt, result.ErrorCount)
		}
	}
}

func TestValidateNotApplicableFields_ExtendsOnBugAllowed(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "strict")

	record := &Record{ID: "prov-2026-001", Type: RecordTypeBug, Extends: "prov-2026-002"}
	result := &LintResult{}
	linter.validateNotApplicableFields(record, result)

	if result.ErrorCount != 0 {
		t.Errorf("Expected no error for extends on Bug, got %d: %v", result.ErrorCount, result.Issues)
	}
}

func TestValidateNotApplicableFields_BriefScopeWarnings(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "strict")

	record := &Record{
		ID:             "prov-2026-001",
		Type:           RecordTypeBrief,
		AffectedScope:  []string{"pkg/foo.go"},
		ForbiddenScope: []string{"pkg/bar.go"},
		AssociatedSpecs: []AssociatedSpec{
			{Path: "pkg/foo_test.go"},
		},
	}
	result := &LintResult{}
	linter.validateNotApplicableFields(record, result)

	if result.WarningCount != 3 {
		t.Errorf("Expected 3 warnings for Brief with scope/specs, got %d: %v", result.WarningCount, result.Issues)
	}
}

func TestValidateNotApplicableFields_ImprintTracesAndMonitorsWarnings(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "strict")

	record := &Record{
		ID:               "prov-2026-001",
		Type:             RecordTypeImprint,
		AssociatedTraces: []string{"trace-123"},
		Monitors:         []string{"dashboard-url"},
	}
	result := &LintResult{}
	linter.validateNotApplicableFields(record, result)

	if result.WarningCount != 2 {
		t.Errorf("Expected 2 warnings for Imprint with traces/monitors, got %d: %v", result.WarningCount, result.Issues)
	}
}

// --- validateBugConditionals tests ---

func newBugLoader(t *testing.T) (*Loader, *Record, *Record) {
	t.Helper()
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	loader := NewLoader(tmpDir, nil)
	blueprint := &Record{ID: "prov-2026-bp1", Type: RecordTypeBlueprint}
	anotherBug := &Record{ID: "prov-2026-bg1", Type: RecordTypeBug}
	loader.Records = []*Record{blueprint, anotherBug}
	loader.RecordsByID = map[string]*Record{
		"prov-2026-bp1": blueprint,
		"prov-2026-bg1": anotherBug,
	}
	return loader, blueprint, anotherBug
}

func TestValidateBugConditionals_NeitherSupersedesNorExtends(t *testing.T) {
	loader, _, _ := newBugLoader(t)
	linter := NewLinter(loader, "strict")

	bug := &Record{ID: "prov-2026-001", Type: RecordTypeBug, Supersedes: "", Extends: ""}
	result := &LintResult{}
	linter.validateBugConditionals(bug, result)

	if result.ErrorCount != 1 {
		t.Errorf("Expected 1 error when Bug has neither supersedes nor extends, got %d: %v", result.ErrorCount, result.Issues)
	}
}

func TestValidateBugConditionals_BothSupersedesAndExtends(t *testing.T) {
	loader, blueprint, _ := newBugLoader(t)
	linter := NewLinter(loader, "strict")

	bug := &Record{ID: "prov-2026-001", Type: RecordTypeBug, Supersedes: blueprint.ID, Extends: blueprint.ID}
	result := &LintResult{}
	linter.validateBugConditionals(bug, result)

	if result.ErrorCount != 1 {
		t.Errorf("Expected 1 error when Bug has both supersedes and extends, got %d: %v", result.ErrorCount, result.Issues)
	}
}

func TestValidateBugConditionals_SupersedesBlueprint(t *testing.T) {
	loader, blueprint, _ := newBugLoader(t)
	linter := NewLinter(loader, "strict")

	bug := &Record{ID: "prov-2026-001", Type: RecordTypeBug, Supersedes: blueprint.ID}
	result := &LintResult{}
	linter.validateBugConditionals(bug, result)

	if result.ErrorCount != 0 {
		t.Errorf("Expected 0 errors for Bug superseding Blueprint, got %d: %v", result.ErrorCount, result.Issues)
	}
}

func TestValidateBugConditionals_SupersedesBug(t *testing.T) {
	loader, _, anotherBug := newBugLoader(t)
	linter := NewLinter(loader, "strict")

	bug := &Record{ID: "prov-2026-001", Type: RecordTypeBug, Supersedes: anotherBug.ID}
	result := &LintResult{}
	linter.validateBugConditionals(bug, result)

	if result.ErrorCount != 0 {
		t.Errorf("Expected 0 errors for Bug superseding Bug, got %d: %v", result.ErrorCount, result.Issues)
	}
}

func TestValidateBugConditionals_SupersedesBriefIsError(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	brief := &Record{ID: "prov-2026-br1", Type: RecordTypeBrief}
	loader.Records = []*Record{brief}
	loader.RecordsByID = map[string]*Record{"prov-2026-br1": brief}
	linter := NewLinter(loader, "strict")

	bug := &Record{ID: "prov-2026-001", Type: RecordTypeBug, Supersedes: "prov-2026-br1"}
	result := &LintResult{}
	linter.validateBugConditionals(bug, result)

	if result.ErrorCount != 1 {
		t.Errorf("Expected 1 error for Bug superseding Brief, got %d: %v", result.ErrorCount, result.Issues)
	}
}

func TestValidateBugConditionals_ExtendsBlueprint(t *testing.T) {
	loader, blueprint, _ := newBugLoader(t)
	linter := NewLinter(loader, "strict")

	bug := &Record{ID: "prov-2026-001", Type: RecordTypeBug, Extends: blueprint.ID}
	result := &LintResult{}
	linter.validateBugConditionals(bug, result)

	if result.ErrorCount != 0 {
		t.Errorf("Expected 0 errors for Bug extending Blueprint, got %d: %v", result.ErrorCount, result.Issues)
	}
}

func TestValidateBugConditionals_ExtendsImprintIsError(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	imp := &Record{ID: "prov-2026-im1", Type: RecordTypeImprint}
	loader.Records = []*Record{imp}
	loader.RecordsByID = map[string]*Record{"prov-2026-im1": imp}
	linter := NewLinter(loader, "strict")

	bug := &Record{ID: "prov-2026-001", Type: RecordTypeBug, Extends: "prov-2026-im1"}
	result := &LintResult{}
	linter.validateBugConditionals(bug, result)

	if result.ErrorCount != 1 {
		t.Errorf("Expected 1 error for Bug extending Imprint, got %d: %v", result.ErrorCount, result.Issues)
	}
}

// --- validateSupersessionType tests (Bug exceptions + Imprint same-parent) ---

func TestValidateSupersessionType_BugSupersedesBlueprint(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	blueprint := &Record{ID: "prov-2026-bp1", Type: RecordTypeBlueprint}
	bug := &Record{ID: "prov-2026-001", Type: RecordTypeBug, Supersedes: "prov-2026-bp1"}
	loader.Records = []*Record{blueprint, bug}
	loader.RecordsByID = map[string]*Record{"prov-2026-bp1": blueprint, "prov-2026-001": bug}
	linter := NewLinter(loader, "strict")

	result := &LintResult{}
	linter.validateSupersessionType(bug, result)

	if result.ErrorCount != 0 {
		t.Errorf("Bug superseding Blueprint must be allowed, got %d errors: %v", result.ErrorCount, result.Issues)
	}
}

func TestValidateSupersessionType_ImprintSupersessesImprintSameParent(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	old := &Record{ID: "prov-2026-im1", Type: RecordTypeImprint, Implements: "prov-2026-bp1"}
	newImp := &Record{ID: "prov-2026-im2", Type: RecordTypeImprint, Implements: "prov-2026-bp1", Supersedes: "prov-2026-im1"}
	loader.Records = []*Record{old, newImp}
	loader.RecordsByID = map[string]*Record{"prov-2026-im1": old, "prov-2026-im2": newImp}
	linter := NewLinter(loader, "strict")

	result := &LintResult{}
	linter.validateSupersessionType(newImp, result)

	if result.ErrorCount != 0 {
		t.Errorf("Imprint superseding Imprint with same implements must be allowed, got %d errors: %v", result.ErrorCount, result.Issues)
	}
}

func TestValidateSupersessionType_ImprintSupersedesImprintDifferentParent(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	old := &Record{ID: "prov-2026-im1", Type: RecordTypeImprint, Implements: "prov-2026-bp1"}
	newImp := &Record{ID: "prov-2026-im2", Type: RecordTypeImprint, Implements: "prov-2026-bp2", Supersedes: "prov-2026-im1"}
	loader.Records = []*Record{old, newImp}
	loader.RecordsByID = map[string]*Record{"prov-2026-im1": old, "prov-2026-im2": newImp}
	linter := NewLinter(loader, "strict")

	result := &LintResult{}
	linter.validateSupersessionType(newImp, result)

	if result.ErrorCount != 1 {
		t.Errorf("Imprint superseding Imprint with different implements must error, got %d: %v", result.ErrorCount, result.Issues)
	}
}

// --- validateImplements: Brief and Bug cannot use implements ---

func TestValidateImplements_BriefCannotUseImplements(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	target := &Record{ID: "prov-2026-bp1", Type: RecordTypeBlueprint}
	brief := &Record{ID: "prov-2026-001", Type: RecordTypeBrief, Implements: "prov-2026-bp1"}
	loader.Records = []*Record{target, brief}
	loader.RecordsByID = map[string]*Record{"prov-2026-bp1": target, "prov-2026-001": brief}
	linter := NewLinter(loader, "strict")

	result := &LintResult{}
	linter.validateImplements(brief, result)

	if result.ErrorCount != 1 {
		t.Errorf("Expected 1 error for Brief using implements, got %d: %v", result.ErrorCount, result.Issues)
	}
}

func TestValidateImplements_BugCannotUseImplements(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	target := &Record{ID: "prov-2026-bp1", Type: RecordTypeBlueprint}
	bug := &Record{ID: "prov-2026-001", Type: RecordTypeBug, Implements: "prov-2026-bp1"}
	loader.Records = []*Record{target, bug}
	loader.RecordsByID = map[string]*Record{"prov-2026-bp1": target, "prov-2026-001": bug}
	linter := NewLinter(loader, "strict")

	result := &LintResult{}
	linter.validateImplements(bug, result)

	if result.ErrorCount != 1 {
		t.Errorf("Expected 1 error for Bug using implements, got %d: %v", result.ErrorCount, result.Issues)
	}
}

// --- prov-2026-7b402f2f: brief records must not receive associated_specs enforcement issues ---

func TestValidateAssociatedSpecs_BriefOpenNoSpecs_NoEnforcementIssues(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "linter-test")
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)

	for _, enforcement := range []string{"strict", "warn", "none"} {
		linter := NewLinter(loader, enforcement)
		brief := &Record{
			ID:     "prov-2026-001",
			Type:   RecordTypeBrief,
			Status: StatusOpen,
		}
		result := &LintResult{}
		linter.validateAssociatedSpecs(brief, result)

		for _, issue := range result.Issues {
			if issue.Field == "associated_specs" {
				t.Errorf("enforcement=%s: open brief with no associated_specs produced unexpected issue: %s", enforcement, issue.Message)
			}
		}
	}
}

// ---- Hash manifest / validateImmutability tests ----

func makeImplementedRecord(id string) *Record {
	return &Record{
		ID:          id,
		Title:       "Test record " + id,
		Status:      StatusImplemented,
		CreatedAt:   "2026-05-05",
		Author:      "test",
		Intent:      "intent",
		Type:        RecordTypeBlueprint,
		SealedAtSHA: "abc1234",
	}
}

func TestValidateAssociatedSpecs_TerminalStatusWarns(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "strict")

	terminalStatuses := []Status{StatusImplemented, StatusSuperseded, StatusDeprecated}
	for _, status := range terminalStatuses {
		record := &Record{
			ID:     "prov-2025-001",
			Status: status,
			AssociatedSpecs: []AssociatedSpec{
				{Path: filepath.Join(tmpDir, "gone.linespec")},
			},
		}
		result := &LintResult{}
		linter.validateAssociatedSpecs(record, result)
		if result.ErrorCount != 0 {
			t.Errorf("status %s: expected 0 errors for missing proof artifact, got %d", status, result.ErrorCount)
		}
		if result.WarningCount != 1 {
			t.Errorf("status %s: expected 1 warning for missing proof artifact, got %d", status, result.WarningCount)
		}
	}
}

func TestValidateAssociatedSpecs_OpenStatusErrors(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "strict")

	record := &Record{
		ID:     "prov-2025-001",
		Status: StatusOpen,
		AssociatedSpecs: []AssociatedSpec{
			{Path: filepath.Join(tmpDir, "gone.linespec")},
		},
	}
	result := &LintResult{}
	linter.validateAssociatedSpecs(record, result)
	if result.ErrorCount != 1 {
		t.Errorf("expected 1 error for open record with missing proof artifact, got %d", result.ErrorCount)
	}
	if result.WarningCount != 0 {
		t.Errorf("expected 0 warnings for open record with missing proof artifact, got %d", result.WarningCount)
	}
}

func TestValidateScopePaths_TerminalStatusWarnsGlob(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "strict")

	terminalStatuses := []Status{StatusImplemented, StatusSuperseded, StatusDeprecated}
	for _, status := range terminalStatuses {
		record := &Record{
			ID:            "prov-2025-001",
			Status:        status,
			AffectedScope: []string{filepath.Join(tmpDir, "*.deleted")},
		}
		result := &LintResult{}
		linter.validateScopePaths(record, result)
		if result.ErrorCount != 0 {
			t.Errorf("status %s: expected 0 errors for unmatched glob, got %d", status, result.ErrorCount)
		}
		if result.WarningCount != 1 {
			t.Errorf("status %s: expected 1 warning for unmatched glob, got %d", status, result.WarningCount)
		}
	}
}

func TestValidateScopePaths_TerminalStatusWarnsRegex(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	originalWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(originalWd)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "strict")

	terminalStatuses := []Status{StatusImplemented, StatusSuperseded, StatusDeprecated}
	for _, status := range terminalStatuses {
		record := &Record{
			ID:            "prov-2025-001",
			Status:        status,
			AffectedScope: []string{"re:deleted_\\d+\\.go"},
		}
		result := &LintResult{}
		linter.validateScopePaths(record, result)
		if result.ErrorCount != 0 {
			t.Errorf("status %s: expected 0 errors for unmatched regex, got %d", status, result.ErrorCount)
		}
		if result.WarningCount != 1 {
			t.Errorf("status %s: expected 1 warning for unmatched regex, got %d", status, result.WarningCount)
		}
	}
}

func TestHashRecord_Deterministic(t *testing.T) {
	r := makeImplementedRecord("prov-2026-test01")
	h1, err := HashRecord(r)
	if err != nil {
		t.Fatalf("HashRecord error: %v", err)
	}
	h2, err := HashRecord(r)
	if err != nil {
		t.Fatalf("HashRecord error: %v", err)
	}
	if h1 != h2 {
		t.Errorf("HashRecord is not deterministic: %q != %q", h1, h2)
	}
}

func TestHashRecord_ChangeSensitive(t *testing.T) {
	r := makeImplementedRecord("prov-2026-test01")
	h1, _ := HashRecord(r)

	r2 := *r
	r2.Intent = "changed intent"
	h2, _ := HashRecord(&r2)

	if h1 == h2 {
		t.Error("HashRecord should differ when content changes")
	}
}

func TestHashRecord_FilePathExcluded(t *testing.T) {
	r := makeImplementedRecord("prov-2026-test01")
	r.FilePath = ""
	h1, _ := HashRecord(r)

	r2 := *r
	r2.FilePath = "/some/path/to/record.yml"
	h2, _ := HashRecord(&r2)

	if h1 != h2 {
		t.Error("HashRecord should be identical regardless of FilePath")
	}
}

func TestHasher_SealAndVerify(t *testing.T) {
	tmp := t.TempDir()
	linespecDir := filepath.Join(tmp, ".linespec")
	if err := os.MkdirAll(linespecDir, 0755); err != nil {
		t.Fatal(err)
	}

	h := &Hasher{manifestPath: filepath.Join(linespecDir, "hash_manifest.json")}
	r := makeImplementedRecord("prov-2026-seal01")

	if err := h.SealRecord(r, []*Record{r}); err != nil {
		t.Fatalf("SealRecord: %v", err)
	}

	stored, current, ok, err := h.VerifyRecord(r)
	if err != nil {
		t.Fatalf("VerifyRecord: %v", err)
	}
	if !ok {
		t.Fatal("VerifyRecord: record not found in manifest")
	}
	if stored != current {
		t.Errorf("stored %q != current %q", stored, current)
	}
}

func TestHasher_TamperDetected(t *testing.T) {
	tmp := t.TempDir()
	linespecDir := filepath.Join(tmp, ".linespec")
	if err := os.MkdirAll(linespecDir, 0755); err != nil {
		t.Fatal(err)
	}

	h := &Hasher{manifestPath: filepath.Join(linespecDir, "hash_manifest.json")}
	r := makeImplementedRecord("prov-2026-tamper01")

	if err := h.SealRecord(r, []*Record{r}); err != nil {
		t.Fatalf("SealRecord: %v", err)
	}

	// Tamper with the record after sealing.
	r.Intent = "tampered"

	stored, current, ok, err := h.VerifyRecord(r)
	if err != nil {
		t.Fatalf("VerifyRecord: %v", err)
	}
	if !ok {
		t.Fatal("VerifyRecord: record not found in manifest")
	}
	if stored == current {
		t.Error("Expected hash mismatch after tampering, but hashes match")
	}
}

func TestHasher_GraphHashes(t *testing.T) {
	tmp := t.TempDir()
	linespecDir := filepath.Join(tmp, ".linespec")
	if err := os.MkdirAll(linespecDir, 0755); err != nil {
		t.Fatal(err)
	}

	h := &Hasher{manifestPath: filepath.Join(linespecDir, "hash_manifest.json")}

	active := makeImplementedRecord("prov-2026-active01")
	deprecated := makeImplementedRecord("prov-2026-depr01")
	deprecated.Status = StatusDeprecated
	all := []*Record{active, deprecated}

	if err := h.SealRecord(active, all); err != nil {
		t.Fatalf("SealRecord active: %v", err)
	}

	m, err := h.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}

	if m.FullGraphHash == "" {
		t.Error("FullGraphHash should not be empty")
	}
	if m.ActiveSubsetHash == "" {
		t.Error("ActiveSubsetHash should not be empty")
	}
	if m.FullGraphHash == m.ActiveSubsetHash {
		t.Error("FullGraphHash and ActiveSubsetHash should differ when inactive records exist")
	}
}

func TestHasher_ManifestAtomicWrite(t *testing.T) {
	tmp := t.TempDir()
	linespecDir := filepath.Join(tmp, ".linespec")
	if err := os.MkdirAll(linespecDir, 0755); err != nil {
		t.Fatal(err)
	}

	h := &Hasher{manifestPath: filepath.Join(linespecDir, "hash_manifest.json")}
	r := makeImplementedRecord("prov-2026-atomic01")

	if err := h.SealRecord(r, []*Record{r}); err != nil {
		t.Fatalf("SealRecord: %v", err)
	}

	// Ensure the manifest is valid JSON after writing.
	data, err := os.ReadFile(h.manifestPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var m hashManifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if m.Records["prov-2026-atomic01"] == "" {
		t.Error("expected record hash in manifest")
	}
}

func TestValidateImmutability_NoManifest(t *testing.T) {
	tmp := t.TempDir()
	loader := NewLoader(tmp, nil)
	linter := NewLinter(loader, "strict")
	linter.Hasher = &Hasher{manifestPath: filepath.Join(tmp, ".linespec", "hash_manifest.json")}

	record := makeImplementedRecord("prov-2026-nomfst")
	result := &LintResult{}
	linter.validateImmutability(record, result)

	// When the manifest doesn't exist the system is not yet active — no issues emitted.
	if result.WarningCount != 0 || result.ErrorCount != 0 {
		t.Errorf("expected no issues when manifest is absent, got %d warnings %d errors", result.WarningCount, result.ErrorCount)
	}
}

func TestValidateImmutability_NoEntryInManifest(t *testing.T) {
	tmp := t.TempDir()
	linespecDir := filepath.Join(tmp, ".linespec")
	if err := os.MkdirAll(linespecDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Manifest exists but has no entry for this record.
	h := &Hasher{manifestPath: filepath.Join(linespecDir, "hash_manifest.json")}
	other := makeImplementedRecord("prov-2026-other01")
	if err := h.SealRecord(other, []*Record{other}); err != nil {
		t.Fatalf("SealRecord: %v", err)
	}

	loader := NewLoader(tmp, nil)
	linter := NewLinter(loader, "strict")
	linter.Hasher = h

	record := makeImplementedRecord("prov-2026-noentry")
	result := &LintResult{}
	linter.validateImmutability(record, result)

	// Record pre-dates the manifest — no issues emitted.
	if result.WarningCount != 0 || result.ErrorCount != 0 {
		t.Errorf("expected no issues for record not in manifest, got %d warnings %d errors", result.WarningCount, result.ErrorCount)
	}
}

func TestValidateImmutability_Clean(t *testing.T) {
	tmp := t.TempDir()
	linespecDir := filepath.Join(tmp, ".linespec")
	if err := os.MkdirAll(linespecDir, 0755); err != nil {
		t.Fatal(err)
	}

	h := &Hasher{manifestPath: filepath.Join(linespecDir, "hash_manifest.json")}
	record := makeImplementedRecord("prov-2026-clean01")
	if err := h.SealRecord(record, []*Record{record}); err != nil {
		t.Fatalf("SealRecord: %v", err)
	}

	loader := NewLoader(tmp, nil)
	linter := NewLinter(loader, "strict")
	linter.Hasher = h

	result := &LintResult{}
	linter.validateImmutability(record, result)

	if result.ErrorCount != 0 || result.WarningCount != 0 {
		t.Errorf("expected no issues for clean record, got %d errors %d warnings", result.ErrorCount, result.WarningCount)
	}
}

func TestValidateImmutability_Tampered(t *testing.T) {
	tmp := t.TempDir()
	linespecDir := filepath.Join(tmp, ".linespec")
	if err := os.MkdirAll(linespecDir, 0755); err != nil {
		t.Fatal(err)
	}

	h := &Hasher{manifestPath: filepath.Join(linespecDir, "hash_manifest.json")}
	record := makeImplementedRecord("prov-2026-tamp01")
	if err := h.SealRecord(record, []*Record{record}); err != nil {
		t.Fatalf("SealRecord: %v", err)
	}

	// Modify record after sealing.
	record.Intent = "tampered"

	loader := NewLoader(tmp, nil)
	linter := NewLinter(loader, "strict")
	linter.Hasher = h

	result := &LintResult{}
	linter.validateImmutability(record, result)

	if result.ErrorCount != 1 {
		t.Errorf("expected 1 PROV-IMM error for tampered record, got %d errors %d warnings", result.ErrorCount, result.WarningCount)
	}
	if len(result.Issues) > 0 && !strings.Contains(result.Issues[0].Message, "PROV-IMM") {
		t.Errorf("expected PROV-IMM in error message, got: %s", result.Issues[0].Message)
	}
}

// --- prov-2026-d9f69b27: imprint scope containment (PROV023) ---

func TestValidateImprintScopeContainment_WithinBlueprintScope_Passes(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	file := filepath.Join(tmpDir, "foo.go")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	loader := NewLoader(tmpDir, nil)
	bp := &Record{ID: "prov-2026-bp1", Type: RecordTypeBlueprint, Status: StatusOpen, AffectedScope: []string{file}}
	imp := &Record{ID: "prov-2026-imp1", Type: RecordTypeImprint, Status: StatusOpen, Implements: "prov-2026-bp1", AffectedScope: []string{file}}
	loader.Records = []*Record{bp, imp}
	loader.RecordsByID = map[string]*Record{"prov-2026-bp1": bp, "prov-2026-imp1": imp}

	result := &LintResult{}
	NewLinter(loader, "strict").validateImprintScopeContainment(imp, result)

	if result.ErrorCount != 0 {
		t.Errorf("expected no errors when imprint scope is within blueprint scope, got: %v", result.Issues)
	}
}

func TestValidateImprintScopeContainment_ExceedsBlueprintScope_Errors(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	bpFile := filepath.Join(tmpDir, "bp.go")
	impFile := filepath.Join(tmpDir, "other.go")
	for _, f := range []string{bpFile, impFile} {
		if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
	}

	loader := NewLoader(tmpDir, nil)
	bp := &Record{ID: "prov-2026-bp1", Type: RecordTypeBlueprint, Status: StatusOpen, AffectedScope: []string{bpFile}}
	imp := &Record{ID: "prov-2026-imp1", Type: RecordTypeImprint, Status: StatusOpen, Implements: "prov-2026-bp1", AffectedScope: []string{impFile}}
	loader.Records = []*Record{bp, imp}
	loader.RecordsByID = map[string]*Record{"prov-2026-bp1": bp, "prov-2026-imp1": imp}

	result := &LintResult{}
	NewLinter(loader, "strict").validateImprintScopeContainment(imp, result)

	if result.ErrorCount != 1 {
		t.Errorf("expected 1 PROV023 error when imprint scope exceeds blueprint scope, got %d: %v", result.ErrorCount, result.Issues)
	}
	if len(result.Issues) > 0 && !strings.Contains(result.Issues[0].Message, "PROV023") {
		t.Errorf("expected PROV023 in error message, got: %s", result.Issues[0].Message)
	}
}

func TestValidateImprintScopeContainment_BlueprintNoScope_Passes(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	file := filepath.Join(tmpDir, "any.go")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	loader := NewLoader(tmpDir, nil)
	// Blueprint has no affected_scope → unconstrained
	bp := &Record{ID: "prov-2026-bp1", Type: RecordTypeBlueprint, Status: StatusOpen, AffectedScope: nil}
	imp := &Record{ID: "prov-2026-imp1", Type: RecordTypeImprint, Status: StatusOpen, Implements: "prov-2026-bp1", AffectedScope: []string{file}}
	loader.Records = []*Record{bp, imp}
	loader.RecordsByID = map[string]*Record{"prov-2026-bp1": bp, "prov-2026-imp1": imp}

	result := &LintResult{}
	NewLinter(loader, "strict").validateImprintScopeContainment(imp, result)

	if result.ErrorCount != 0 {
		t.Errorf("expected no errors when blueprint has no affected_scope, got: %v", result.Issues)
	}
}
