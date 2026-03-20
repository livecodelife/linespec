package provenance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNewSARIFDocument(t *testing.T) {
	doc := NewSARIFDocument()

	if doc.Schema != "https://json.schemastore.org/sarif-2.1.0.json" {
		t.Errorf("Expected schema to be SARIF 2.1.0, got %s", doc.Schema)
	}

	if doc.Version != "2.1.0" {
		t.Errorf("Expected version 2.1.0, got %s", doc.Version)
	}

	if len(doc.Runs) != 1 {
		t.Errorf("Expected exactly 1 run, got %d", len(doc.Runs))
	}

	run := doc.Runs[0]
	if run.Tool == nil {
		t.Error("Expected tool descriptor to be present")
	}

	if len(run.Results) != 0 {
		t.Errorf("Expected empty results array, got %d results", len(run.Results))
	}
}

func TestGetAllRules(t *testing.T) {
	rules := GetAllRules()

	if len(rules) != 19 {
		t.Errorf("Expected 19 rules, got %d", len(rules))
	}

	// Check that all expected rule IDs are present
	expectedIDs := map[string]bool{
		"PROV001": true, "PROV002": true, "PROV003": true,
		"PROV004": true, "PROV005": true, "PROV006": true,
		"PROV007": true, "PROV008": true, "PROV009": true,
		"PROV010": true, "PROV011": true, "PROV012": true,
		"PROV013": true, "PROV014": true, "PROV015": true,
		"PROV016": true, "PROV017": true, "PROV018": true,
		"PROV019": true,
	}

	for _, rule := range rules {
		if !expectedIDs[rule.ID] {
			t.Errorf("Unexpected rule ID: %s", rule.ID)
		}
		delete(expectedIDs, rule.ID)

		if rule.Name == "" {
			t.Errorf("Rule %s has no name", rule.ID)
		}

		if rule.ShortDescription == nil || rule.ShortDescription.Text == "" {
			t.Errorf("Rule %s has no short description", rule.ID)
		}

		if rule.DefaultConfiguration == nil {
			t.Errorf("Rule %s has no default configuration", rule.ID)
		}
	}

	if len(expectedIDs) > 0 {
		for id := range expectedIDs {
			t.Errorf("Missing rule ID: %s", id)
		}
	}
}

func TestSeverityToSARIFLevel(t *testing.T) {
	tests := []struct {
		severity    Severity
		enforcement string
		expected    string
	}{
		{SeverityError, "strict", "error"},
		{SeverityError, "warn", "error"},
		{SeverityWarning, "strict", "warning"},
		{SeverityWarning, "warn", "warning"},
		{SeverityHint, "none", "note"},
	}

	for _, test := range tests {
		level := SeverityToSARIFLevel(test.severity, test.enforcement)
		if level != test.expected {
			t.Errorf("SeverityToSARIFLevel(%s, %s) = %s, expected %s",
				test.severity, test.enforcement, level, test.expected)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		filePath string
		repoRoot string
		expected string
	}{
		{"/repo/provenance/test.yml", "/repo", "provenance/test.yml"},
		{"provenance/test.yml", "/repo", "provenance/test.yml"},
	}

	for _, test := range tests {
		result := NormalizePath(test.filePath, test.repoRoot)
		if result != test.expected {
			t.Errorf("NormalizePath(%s, %s) = %s, expected %s",
				test.filePath, test.repoRoot, result, test.expected)
		}
	}
}

func TestComputeFileHash(t *testing.T) {
	// Create a temporary file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	content := []byte("test content")
	if err := os.WriteFile(tmpFile, content, 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	hash, err := ComputeFileHash(tmpFile)
	if err != nil {
		t.Errorf("ComputeFileHash failed: %v", err)
	}

	if hash == "" {
		t.Error("Expected non-empty hash")
	}

	// The hash should be 64 characters (256 bits in hex)
	if len(hash) != 64 {
		t.Errorf("Expected hash length of 64, got %d", len(hash))
	}

	// Test non-existent file
	_, err = ComputeFileHash(filepath.Join(tmpDir, "nonexistent.txt"))
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}

func TestLintResultToSARIF(t *testing.T) {
	// Create a mock loader with a record
	loader := NewLoader(t.TempDir(), nil)
	record := &Record{
		ID:       "prov-2026-001",
		FilePath: filepath.Join(loader.Dir, "prov-2026-001.yml"),
		Status:   StatusOpen,
		Title:    "Test Record",
	}
	loader.Records = append(loader.Records, record)

	// Create the record file so it can be hashed
	os.WriteFile(record.FilePath, []byte("id: prov-2026-001\n"), 0644)

	// Create a lint result with issues
	result := &LintResult{
		Enforcement: "strict",
		Issues: []Issue{
			{
				RecordID: "prov-2026-001",
				Field:    "associated_specs",
				Message:  "No associated specs (open) [strict]",
				Severity: SeverityError,
			},
		},
		PassedCount: 0,
		ErrorCount:  1,
	}

	doc := result.ToSARIF(loader, "/repo", []string{record.FilePath})

	// Verify structure
	if len(doc.Runs) != 1 {
		t.Fatalf("Expected 1 run, got %d", len(doc.Runs))
	}

	run := doc.Runs[0]

	// Check tool descriptor
	if run.Tool == nil || run.Tool.Driver == nil {
		t.Fatal("Expected tool descriptor")
	}

	if len(run.Tool.Driver.Rules) != 19 {
		t.Errorf("Expected 19 rules in catalog, got %d", len(run.Tool.Driver.Rules))
	}

	// Check results
	if len(run.Results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(run.Results))
	}

	sarifResult := run.Results[0]
	if sarifResult.RuleID != "PROV010" {
		t.Errorf("Expected rule ID PROV010, got %s", sarifResult.RuleID)
	}

	if sarifResult.Level != "error" {
		t.Errorf("Expected level error, got %s", sarifResult.Level)
	}

	if sarifResult.Message == nil || sarifResult.Message.Text == "" {
		t.Error("Expected message to be present")
	}

	// Check location
	if len(sarifResult.Locations) == 0 {
		t.Fatal("Expected at least one location")
	}

	location := sarifResult.Locations[0]
	if location.PhysicalLocation == nil {
		t.Fatal("Expected physical location")
	}

	if location.PhysicalLocation.ArtifactLocation == nil {
		t.Fatal("Expected artifact location")
	}

	if location.PhysicalLocation.ArtifactLocation.URIBaseID != "%SRCROOT%" {
		t.Errorf("Expected uriBaseId to be %%SRCROOT%%, got %s",
			location.PhysicalLocation.ArtifactLocation.URIBaseID)
	}

	// Check artifacts
	if len(run.Artifacts) != 1 {
		t.Errorf("Expected 1 artifact, got %d", len(run.Artifacts))
	}

	artifact := run.Artifacts[0]
	if artifact.Location == nil {
		t.Error("Expected artifact location")
	}

	if artifact.Hashes == nil || artifact.Hashes["sha-256"] == "" {
		t.Error("Expected sha-256 hash in artifact")
	}
}

func TestSARIFDocumentToJSON(t *testing.T) {
	doc := NewSARIFDocument()

	jsonBytes, err := doc.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}

	// Verify it's valid JSON by parsing it
	var parsed map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
		t.Errorf("Generated invalid JSON: %v", err)
	}

	// Verify required fields
	if _, ok := parsed["$schema"]; !ok {
		t.Error("Missing $schema field")
	}

	if _, ok := parsed["version"]; !ok {
		t.Error("Missing version field")
	}

	if _, ok := parsed["runs"]; !ok {
		t.Error("Missing runs field")
	}
}

func TestGetRuleIDForIssue(t *testing.T) {
	tests := []struct {
		issue    Issue
		expected SARIFRuleID
	}{
		{Issue{Field: "id"}, PROV004},
		{Issue{Field: "status"}, PROV003},
		{Issue{Field: "created_at"}, PROV005},
		{Issue{Field: "supersedes"}, PROV006},
		{Issue{Field: "superseded_by"}, PROV007},
		{Issue{Field: "related"}, PROV008},
		{Issue{Field: "title"}, PROV013},
		{Issue{Field: "constraints"}, PROV012},
		{Issue{Field: "", Message: "Missing required field: id"}, PROV002},
		{Issue{Field: "", Message: "Invalid yaml syntax"}, PROV001},
	}

	for _, test := range tests {
		result := GetRuleIDForIssue(test.issue)
		if result != test.expected {
			t.Errorf("GetRuleIDForIssue(%+v) = %s, expected %s",
				test.issue, result, test.expected)
		}
	}
}

func TestGetAnalyzedFiles(t *testing.T) {
	// Create a mock loader
	loader := NewLoader(t.TempDir(), nil)

	// Create some records
	records := []*Record{
		{ID: "prov-2026-001", FilePath: filepath.Join(loader.Dir, "prov-2026-001.yml")},
		{ID: "prov-2026-002", FilePath: filepath.Join(loader.Dir, "prov-2026-002.yml")},
	}
	loader.Records = records

	// Build the ID index (important!)
	loader.RecordsByID = map[string]*Record{
		"prov-2026-001": records[0],
		"prov-2026-002": records[1],
	}

	// Create a lint result with issues for two different records
	result := &LintResult{
		Issues: []Issue{
			{RecordID: "prov-2026-001", Field: "id", Message: "test"},
			{RecordID: "prov-2026-001", Field: "status", Message: "test2"}, // Same record, should only appear once
			{RecordID: "prov-2026-002", Field: "title", Message: "test3"},
		},
	}

	analyzedFiles := GetAnalyzedFiles(result, loader)

	if len(analyzedFiles) != 2 {
		t.Errorf("Expected 2 unique analyzed files, got %d", len(analyzedFiles))
	}

	// Check that both files are present
	has001 := false
	has002 := false
	for _, file := range analyzedFiles {
		if filepath.Base(file) == "prov-2026-001.yml" {
			has001 = true
		}
		if filepath.Base(file) == "prov-2026-002.yml" {
			has002 = true
		}
	}

	if !has001 {
		t.Error("Missing prov-2026-001.yml in analyzed files")
	}
	if !has002 {
		t.Error("Missing prov-2026-002.yml in analyzed files")
	}
}

func TestCleanRunHasEmptyResults(t *testing.T) {
	// A clean run (no issues) should have an empty results array
	result := &LintResult{
		Enforcement: "warn",
		Issues:      []Issue{},
		PassedCount: 10,
	}

	loader := NewLoader(t.TempDir(), nil)
	doc := result.ToSARIF(loader, "/repo", []string{})

	if len(doc.Runs) != 1 {
		t.Fatalf("Expected 1 run, got %d", len(doc.Runs))
	}

	// Results should be empty but present
	if doc.Runs[0].Results == nil {
		t.Error("Results array should not be nil")
	}

	if len(doc.Runs[0].Results) != 0 {
		t.Errorf("Expected 0 results for clean run, got %d", len(doc.Runs[0].Results))
	}

	// Rules should still be present
	if len(doc.Runs[0].Tool.Driver.Rules) != 19 {
		t.Errorf("Expected 19 rules even for clean run, got %d", len(doc.Runs[0].Tool.Driver.Rules))
	}
}
