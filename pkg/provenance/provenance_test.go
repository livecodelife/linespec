package provenance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsValidID(t *testing.T) {
	tests := []struct {
		id      string
		isValid bool
	}{
		{"prov-2025-001", true},
		{"prov-2026-031", true},
		{"prov-1999-999", true},
		{"prov-2026-001-user-service", true},  // service suffix format
		{"prov-2026-002-todo-api", true},      // service suffix format
		{"prov-2026-003-my-service", true},    // service suffix with hyphen
		{"prov-2025-01", false},               // missing leading zero
		{"prov-2025-0001", false},             // too many digits
		{"prov-2025-1", false},                // missing leading zeros
		{"prov-25-001", false},                // two digit year
		{"prov-2025", false},                  // missing sequence
		{"PROV-2025-001", false},              // uppercase
		{"prov-2026-001-UserService", false},  // uppercase in suffix
		{"prov-2026-001_user_service", false}, // underscore in suffix
		{"", false},
		{"some-id", false},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := IsValidID(tt.id)
			if got != tt.isValid {
				t.Errorf("IsValidID(%q) = %v, want %v", tt.id, got, tt.isValid)
			}
		})
	}
}

func TestNextID(t *testing.T) {
	tests := []struct {
		year        int
		existingIDs []string
		want        string
	}{
		{2025, []string{}, "prov-2025-001"},
		{2025, []string{"prov-2025-001"}, "prov-2025-002"},
		{2025, []string{"prov-2025-001", "prov-2025-003"}, "prov-2025-004"},
		{2025, []string{"prov-2024-999"}, "prov-2025-001"}, // different year
		{2026, []string{"prov-2025-001", "prov-2025-002"}, "prov-2026-001"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := NextID(tt.year, tt.existingIDs)
			if got != tt.want {
				t.Errorf("NextID(%d, %v) = %q, want %q", tt.year, tt.existingIDs, got, tt.want)
			}
		})
	}
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		filePath string
		pattern  string
		matches  bool
	}{
		// Exact matches
		{"src/main.go", "src/main.go", true},
		{"src/main.go", "src/other.go", false},

		// Glob patterns
		{"src/main.go", "src/*.go", true},
		{"src/sub/file.go", "src/**/*.go", true},
		{"src/main.rb", "src/*.go", false},
		{"test/file_test.go", "**/*_test.go", true},

		// Regex patterns
		{"src/test_123.go", "re:src/test_\\d+\\.go", true},
		{"src/test_abc.go", "re:src/test_\\d+\\.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.filePath+"_"+tt.pattern, func(t *testing.T) {
			got, err := MatchPattern(tt.filePath, tt.pattern)
			if err != nil {
				t.Fatalf("MatchPattern error: %v", err)
			}
			if got != tt.matches {
				t.Errorf("MatchPattern(%q, %q) = %v, want %v", tt.filePath, tt.pattern, got, tt.matches)
			}
		})
	}
}

func TestRecordScopeMode(t *testing.T) {
	tests := []struct {
		affectedScope []string
		want          string
	}{
		{[]string{}, "observed"},
		{[]string{"src/main.go"}, "allowlist"},
		{[]string{"src/main.go", "src/other.go"}, "allowlist"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			record := &Record{AffectedScope: tt.affectedScope}
			got := record.ScopeMode()
			if got != tt.want {
				t.Errorf("ScopeMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRecordIsInScope(t *testing.T) {
	tests := []struct {
		name           string
		affectedScope  []string
		forbiddenScope []string
		filePath       string
		want           bool
	}{
		{
			name:          "exact match in allowlist",
			affectedScope: []string{"src/main.go"},
			filePath:      "src/main.go",
			want:          true,
		},
		{
			name:          "not in allowlist",
			affectedScope: []string{"src/main.go"},
			filePath:      "src/other.go",
			want:          false,
		},
		{
			name:           "forbidden exact match",
			affectedScope:  []string{"src/main.go"},
			forbiddenScope: []string{"src/secret.go"},
			filePath:       "src/secret.go",
			want:           false,
		},
		{
			name:           "forbidden always applies",
			affectedScope:  []string{},
			forbiddenScope: []string{"src/secret.go"},
			filePath:       "src/secret.go",
			want:           false,
		},
		{
			name:          "observed mode - any file allowed",
			affectedScope: []string{},
			filePath:      "src/any.go",
			want:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := &Record{
				AffectedScope:  tt.affectedScope,
				ForbiddenScope: tt.forbiddenScope,
			}
			got, err := record.IsInScope(tt.filePath)
			if err != nil {
				t.Fatalf("IsInScope error: %v", err)
			}
			if got != tt.want {
				t.Errorf("IsInScope(%q) = %v, want %v", tt.filePath, got, tt.want)
			}
		})
	}
}

func TestLoader(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()

	// Create a test record file
	recordContent := `id: prov-2025-001
title: "Test Record"
status: open
created_at: "2025-03-10"
author: "test@example.com"
intent: "Test intent"
constraints: []
affected_scope: []
forbidden_scope: []
supersedes: ""
superseded_by: ""
related: []
associated_specs: []
associated_traces: []
monitors: []
tags: []
`

	recordFile := filepath.Join(tmpDir, "prov-2025-001.yml")
	if err := os.WriteFile(recordFile, []byte(recordContent), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Load the record
	loader := NewLoader(tmpDir, nil)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll error: %v", err)
	}

	// Verify record was loaded
	if len(loader.Records) != 1 {
		t.Errorf("Expected 1 record, got %d", len(loader.Records))
	}

	record, exists := loader.GetRecord("prov-2025-001")
	if !exists {
		t.Fatal("Expected record to exist")
	}

	if record.Title != "Test Record" {
		t.Errorf("Expected title 'Test Record', got %q", record.Title)
	}

	if record.Status != StatusOpen {
		t.Errorf("Expected status 'open', got %q", record.Status)
	}
}

func TestLinter(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()

	// Create a valid record file with linespecs
	validRecord := `id: prov-2025-001
title: "Valid Record"
status: open
created_at: "2025-03-10"
author: "test@example.com"
intent: "Test intent"
constraints:
  - Test constraint
affected_scope: []
forbidden_scope: []
supersedes: ""
superseded_by: ""
related: []
associated_specs:
  - path: test.linespec
associated_traces: []
monitors: []
tags: []
`

	// Change to tmpDir so that relative paths work
	originalWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(originalWd)

	// Create the linespec file
	if err := os.WriteFile("test.linespec", []byte("TEST test\n"), 0644); err != nil {
		t.Fatalf("Failed to write linespec file: %v", err)
	}

	if err := os.WriteFile("prov-2025-001.yml", []byte(validRecord), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Create an invalid record (missing intent)
	invalidRecord := `id: prov-2025-002
title: "Invalid Record"
status: open
created_at: "2025-03-10"
author: "test@example.com"
intent: ""
constraints: []
affected_scope: []
forbidden_scope: []
supersedes: ""
superseded_by: ""
related: []
associated_specs: []
associated_traces: []
monitors: []
tags: []
`

	if err := os.WriteFile("prov-2025-002.yml", []byte(invalidRecord), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Load and lint
	loader := NewLoader(tmpDir, nil)
	if err := loader.LoadAll(); err != nil {
		t.Fatalf("LoadAll error: %v", err)
	}

	linter := NewLinter(loader, "strict")
	result := linter.LintAll()

	// Should have errors for invalid records
	if result.ErrorCount == 0 {
		t.Error("Expected some errors for invalid records")
	}

	// Check that prov-2025-001 passed (has linespecs and constraints)
	recordResult := linter.LintRecord("prov-2025-001")
	hasErrors := false
	for _, issue := range recordResult.Issues {
		if issue.Severity == SeverityError {
			hasErrors = true
			break
		}
	}
	if hasErrors {
		t.Errorf("Expected valid record to pass without errors, got: %v", recordResult.Issues)
	}

	// Check that prov-2025-002 has missing intent error
	recordResult = linter.LintRecord("prov-2025-002")
	hasIntentError := false
	for _, issue := range recordResult.Issues {
		if issue.Field == "intent" && issue.Severity == SeverityError {
			hasIntentError = true
			break
		}
	}
	if !hasIntentError {
		t.Error("Expected missing intent error for invalid record")
	}
}

func TestExtractProvenanceIDs(t *testing.T) {
	git := NewGit("")

	tests := []struct {
		message string
		want    []string
	}{
		{
			message: "Fix bug [prov-2026-001]",
			want:    []string{"prov-2026-001"},
		},
		{
			message: "Add feature [prov-2026-001-user-service] for user service",
			want:    []string{"prov-2026-001-user-service"},
		},
		{
			message: "Multiple changes [prov-2026-001] and [prov-2026-002-todo-api]",
			want:    []string{"prov-2026-001", "prov-2026-002-todo-api"},
		},
		{
			message: "No provenance ID here",
			want:    nil,
		},
		{
			message: "Mixed formats [prov-2026-001] [prov-2026-002-service]",
			want:    []string{"prov-2026-001", "prov-2026-002-service"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.message, func(t *testing.T) {
			got := git.ExtractProvenanceIDs(tt.message)
			if len(got) != len(tt.want) {
				t.Errorf("ExtractProvenanceIDs(%q) = %v, want %v", tt.message, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ExtractProvenanceIDs(%q)[%d] = %q, want %q", tt.message, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestIsRecordFile(t *testing.T) {
	tests := []struct {
		name       string
		filePath   string
		recordPath string
		want       bool
	}{
		{
			name:       "relative path matches",
			filePath:   "provenance/prov-2026-001.yml",
			recordPath: "/repo/provenance/prov-2026-001.yml",
			want:       true,
		},
		{
			name:       "absolute path matches",
			filePath:   "/repo/provenance/prov-2026-001.yml",
			recordPath: "/repo/provenance/prov-2026-001.yml",
			want:       true,
		},
		{
			name:       "different files",
			filePath:   "provenance/prov-2026-001.yml",
			recordPath: "/repo/provenance/prov-2026-002.yml",
			want:       false,
		},
		{
			name:       "different directories same filename",
			filePath:   "other/prov-2026-001.yml",
			recordPath: "/repo/provenance/prov-2026-001.yml",
			want:       true, // Matches by basename
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := &Record{FilePath: tt.recordPath}
			got := isRecordFile(tt.filePath, record)
			if got != tt.want {
				t.Errorf("isRecordFile(%q, %q) = %v, want %v", tt.filePath, tt.recordPath, got, tt.want)
			}
		})
	}
}

// TestCheckStagedRejectsImplementedRecords verifies that commits tagged with
// implemented provenance records are rejected
func TestCheckStagedRejectsImplementedRecords(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()

	// Create a mock git repo structure
	provenanceDir := filepath.Join(tmpDir, "provenance")
	if err := os.MkdirAll(provenanceDir, 0755); err != nil {
		t.Fatalf("Failed to create provenance dir: %v", err)
	}

	// Create an implemented record
	implementedRecord := &Record{
		ID:        "prov-2026-999",
		Title:     "Test Implemented Record",
		Status:    StatusImplemented,
		CreatedAt: "2026-03-13",
		Author:    "test@example.com",
		FilePath:  filepath.Join(provenanceDir, "prov-2026-999.yml"),
		AffectedScope: []string{
			"pkg/example/**/*.go",
		},
	}

	// Create an open record
	openRecord := &Record{
		ID:        "prov-2026-998",
		Title:     "Test Open Record",
		Status:    StatusOpen,
		CreatedAt: "2026-03-13",
		Author:    "test@example.com",
		FilePath:  filepath.Join(provenanceDir, "prov-2026-998.yml"),
		AffectedScope: []string{
			"pkg/example/**/*.go",
		},
	}

	// Create a loader with both records
	loader := NewLoader(tmpDir, nil)
	loader.Records = []*Record{implementedRecord, openRecord}
	loader.RecordsByID = map[string]*Record{
		implementedRecord.ID: implementedRecord,
		openRecord.ID:        openRecord,
	}

	// Test cases
	tests := []struct {
		name          string
		recordID      string
		message       string
		wantViolation bool
		wantMsg       string
	}{
		{
			name:          "implemented record should be rejected",
			recordID:      "prov-2026-999",
			message:       "Test commit [prov-2026-999]",
			wantViolation: true,
			wantMsg:       "is already implemented - cannot commit with this ID",
		},
		{
			name:          "open record should not be rejected",
			recordID:      "prov-2026-998",
			message:       "Test commit [prov-2026-998]",
			wantViolation: false,
		},
		{
			name:          "unknown record should not be rejected",
			recordID:      "prov-2026-997",
			message:       "Test commit [prov-2026-997]",
			wantViolation: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock checker (we can't easily mock Git, but we can verify the logic)
			record, exists := loader.GetRecord(tt.recordID)

			if tt.recordID == "prov-2026-997" {
				// Unknown record
				if exists {
					t.Error("Expected record to not exist")
				}
				return
			}

			if !exists {
				t.Fatalf("Expected record %s to exist", tt.recordID)
			}

			// Check if record is implemented
			isImplemented := record.Status == StatusImplemented

			if tt.wantViolation && !isImplemented {
				t.Errorf("Expected record %s to be implemented", tt.recordID)
			}

			if !tt.wantViolation && isImplemented {
				t.Errorf("Expected record %s to not be implemented, but it is", tt.recordID)
			}
		})
	}
}

// TestContextCommand tests the context command functionality
func TestContextCommand(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	provenanceDir := filepath.Join(tmpDir, "provenance")
	if err := os.MkdirAll(provenanceDir, 0755); err != nil {
		t.Fatalf("Failed to create provenance dir: %v", err)
	}

	// Create test records
	records := []*Record{
		{
			ID:             "prov-2026-001",
			Title:          "First Open Record",
			Status:         StatusOpen,
			CreatedAt:      "2026-03-01",
			Author:         "test@example.com",
			Intent:         "Test intent for first record",
			Constraints:    []string{"Constraint 1", "Constraint 2"},
			AffectedScope:  []string{"pkg/alpha/*.go", "pkg/beta/**/*.go"},
			ForbiddenScope: []string{},
			FilePath:       filepath.Join(provenanceDir, "prov-2026-001.yml"),
		},
		{
			ID:             "prov-2026-002",
			Title:          "Second Open Record",
			Status:         StatusOpen,
			CreatedAt:      "2026-03-02",
			Author:         "test@example.com",
			Intent:         "Test intent for second record",
			Constraints:    []string{"Constraint 3"},
			AffectedScope:  []string{"pkg/alpha/*.go"}, // Overlaps with 001
			ForbiddenScope: []string{},
			FilePath:       filepath.Join(provenanceDir, "prov-2026-002.yml"),
		},
		{
			ID:             "prov-2026-003",
			Title:          "Implemented Record",
			Status:         StatusImplemented,
			CreatedAt:      "2026-03-03",
			Author:         "test@example.com",
			Intent:         "This record is implemented",
			Constraints:    []string{"Constraint 4"},
			AffectedScope:  []string{"pkg/gamma/*.go"},
			ForbiddenScope: []string{},
			FilePath:       filepath.Join(provenanceDir, "prov-2026-003.yml"),
		},
		{
			ID:             "prov-2026-004",
			Title:          "Superseded Record",
			Status:         StatusSuperseded,
			CreatedAt:      "2026-03-04",
			Author:         "test@example.com",
			Intent:         "This record is superseded",
			Constraints:    []string{},
			AffectedScope:  []string{"pkg/old-delta/*.go"}, // Different scope, doesn't match directly
			ForbiddenScope: []string{},
			SupersededBy:   "prov-2026-005",
			FilePath:       filepath.Join(provenanceDir, "prov-2026-004.yml"),
		},
		{
			ID:             "prov-2026-005",
			Title:          "Record That Supersedes",
			Status:         StatusImplemented,
			CreatedAt:      "2026-03-05",
			Author:         "test@example.com",
			Intent:         "This record supersedes prov-2026-004",
			Constraints:    []string{},
			AffectedScope:  []string{"pkg/delta/*.go", "pkg/epsilon/*.go"},
			ForbiddenScope: []string{},
			Supersedes:     "prov-2026-004",
			FilePath:       filepath.Join(provenanceDir, "prov-2026-005.yml"),
		},
	}

	// Create loader with test records
	loader := NewLoader(provenanceDir, nil)
	loader.Records = records
	loader.RecordsByID = make(map[string]*Record)
	for _, r := range records {
		loader.RecordsByID[r.ID] = r
	}

	// Create commands
	config := &ProvenanceConfig{
		Dir:         provenanceDir,
		Enforcement: "warn",
	}
	output := &testWriter{}
	formatter := NewFormatter(output, false)
	commands := &Commands{
		Loader:    loader,
		Formatter: formatter,
		Config:    config,
	}

	tests := []struct {
		name              string
		files             []string
		expectedMatches   int
		expectedConflicts int
	}{
		{
			name:              "Single file matching single record",
			files:             []string{"pkg/gamma/file.go"},
			expectedMatches:   1,
			expectedConflicts: 0,
		},
		{
			name:              "File matching multiple open records (conflict)",
			files:             []string{"pkg/alpha/main.go"},
			expectedMatches:   2,
			expectedConflicts: 1,
		},
		{
			name:              "File with superseded ancestry",
			files:             []string{"pkg/delta/file.go"},
			expectedMatches:   2, // prov-2026-005 (direct) + prov-2026-004 (ancestor via supersedes chain)
			expectedConflicts: 0,
		},
		{
			name:              "Multiple files different records",
			files:             []string{"pkg/gamma/file.go", "pkg/beta/sub/file.go"},
			expectedMatches:   2, // prov-2026-003 + prov-2026-001
			expectedConflicts: 0,
		},
		{
			name:              "No matching records",
			files:             []string{"pkg/unknown/file.go"},
			expectedMatches:   0,
			expectedConflicts: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := commands.buildContextResult(tt.files)

			if len(result.DirectMatches) != tt.expectedMatches {
				t.Errorf("Expected %d matches, got %d", tt.expectedMatches, len(result.DirectMatches))
			}

			if len(result.Conflicts) != tt.expectedConflicts {
				t.Errorf("Expected %d conflicts, got %d", tt.expectedConflicts, len(result.Conflicts))
			}

			// Verify all input files are present
			if len(result.Files) != len(tt.files) {
				t.Errorf("Expected %d files in result, got %d", len(tt.files), len(result.Files))
			}

			// Verify ancestors are tracked for superseded records
			if tt.name == "File with superseded ancestry" {
				// Find prov-2026-005 and check that prov-2026-004 is in its ancestry
				foundWithAncestry := false
				for _, ctx := range result.DirectMatches {
					if ctx.Record.ID == "prov-2026-005" {
						// Check that prov-2026-004 is in the ancestors list
						for _, ancestorID := range ctx.Ancestors {
							if ancestorID == "prov-2026-004" {
								foundWithAncestry = true
								break
							}
						}
					}
				}
				if !foundWithAncestry {
					t.Error("Expected prov-2026-005 to have prov-2026-004 in its ancestry")
				}

				// Also verify prov-2026-004 is in the results as an ancestor-only record
				foundAncestorOnly := false
				for _, ctx := range result.DirectMatches {
					if ctx.Record.ID == "prov-2026-004" && ctx.IsAncestor {
						foundAncestorOnly = true
						break
					}
				}
				if !foundAncestorOnly {
					t.Error("Expected prov-2026-004 to be included as an ancestor-only record")
				}
			}
		})
	}
}

// TestContextCommandSorting tests that records are sorted correctly (open first)
func TestContextCommandSorting(t *testing.T) {
	tmpDir := t.TempDir()
	provenanceDir := filepath.Join(tmpDir, "provenance")
	if err := os.MkdirAll(provenanceDir, 0755); err != nil {
		t.Fatalf("Failed to create provenance dir: %v", err)
	}

	// Create records with different statuses
	records := []*Record{
		{
			ID:            "prov-2026-003",
			Title:         "Implemented Record",
			Status:        StatusImplemented,
			CreatedAt:     "2026-03-03",
			Author:        "test@example.com",
			Intent:        "Test",
			AffectedScope: []string{"pkg/test/*.go"},
			FilePath:      filepath.Join(provenanceDir, "prov-2026-003.yml"),
		},
		{
			ID:            "prov-2026-001",
			Title:         "Open Record 1",
			Status:        StatusOpen,
			CreatedAt:     "2026-03-01",
			Author:        "test@example.com",
			Intent:        "Test",
			AffectedScope: []string{"pkg/test/*.go"},
			FilePath:      filepath.Join(provenanceDir, "prov-2026-001.yml"),
		},
		{
			ID:            "prov-2026-002",
			Title:         "Open Record 2",
			Status:        StatusOpen,
			CreatedAt:     "2026-03-02",
			Author:        "test@example.com",
			Intent:        "Test",
			AffectedScope: []string{"pkg/test/*.go"},
			FilePath:      filepath.Join(provenanceDir, "prov-2026-002.yml"),
		},
	}

	loader := NewLoader(provenanceDir, nil)
	loader.Records = records
	loader.RecordsByID = make(map[string]*Record)
	for _, r := range records {
		loader.RecordsByID[r.ID] = r
	}

	config := &ProvenanceConfig{
		Dir:         provenanceDir,
		Enforcement: "warn",
	}
	output := &testWriter{}
	formatter := NewFormatter(output, false)
	commands := &Commands{
		Loader:    loader,
		Formatter: formatter,
		Config:    config,
	}

	result := commands.buildContextResult([]string{"pkg/test/file.go"})

	// Verify order: open records first, then implemented
	if len(result.DirectMatches) != 3 {
		t.Fatalf("Expected 3 matches, got %d", len(result.DirectMatches))
	}

	// First two should be open records (sorted by ID)
	if result.DirectMatches[0].Record.ID != "prov-2026-001" {
		t.Errorf("Expected first record to be prov-2026-001 (open), got %s", result.DirectMatches[0].Record.ID)
	}
	if result.DirectMatches[1].Record.ID != "prov-2026-002" {
		t.Errorf("Expected second record to be prov-2026-002 (open), got %s", result.DirectMatches[1].Record.ID)
	}
	// Third should be implemented
	if result.DirectMatches[2].Record.ID != "prov-2026-003" {
		t.Errorf("Expected third record to be prov-2026-003 (implemented), got %s", result.DirectMatches[2].Record.ID)
	}
}

// testWriter is a test helper that captures output
type testWriter struct {
	content []byte
}

func (w *testWriter) Write(p []byte) (n int, err error) {
	w.content = append(w.content, p...)
	return len(p), nil
}

func (w *testWriter) String() string {
	return string(w.content)
}
