package provenance

import (
	"os"
	"strings"
	"testing"
)

func TestCheckLockedScope_NoLockedRecords(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-locked-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "none")

	openRecord := &Record{
		ID:            "prov-2026-a1b2c3d4",
		Title:         "Open record",
		Status:        StatusOpen,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		AffectedScope: []string{"pkg/foo.go"},
	}
	loader.Records = append(loader.Records, openRecord)

	result := linter.LintAll()

	for _, issue := range result.Issues {
		if strings.Contains(issue.Message, "overlaps with locked record") {
			t.Errorf("Unexpected locked scope error: %v", issue)
		}
	}
}

func TestCheckLockedScope_OverlapWithError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-locked-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "none")

	lockedRecord := &Record{
		ID:            "prov-2026-a1b2c3d4",
		Title:         "Locked layer",
		Status:        StatusImplemented,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		Locked:        true,
		AffectedScope: []string{"pkg/proxy/*.go"},
	}

	openRecord := &Record{
		ID:            "prov-2026-b2c3d4e5",
		Title:         "Open record",
		Status:        StatusOpen,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		AffectedScope: []string{"pkg/proxy/http.go"},
	}

	loader.Records = append(loader.Records, lockedRecord, openRecord)

	result := linter.LintAll()

	found := false
	for _, issue := range result.Issues {
		if strings.Contains(issue.Message, "overlaps with locked record") {
			found = true
			if issue.Severity != SeverityError {
				t.Errorf("Expected error severity, got %s", issue.Severity)
			}
			if issue.Field != "affected_scope" {
				t.Errorf("Expected field 'affected_scope', got %s", issue.Field)
			}
		}
	}
	if !found {
		t.Errorf("Expected locked scope overlap error, got issues: %v", result.Issues)
	}
}

func TestCheckLockedScope_NoOverlap(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-locked-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "none")

	lockedRecord := &Record{
		ID:            "prov-2026-a1b2c3d4",
		Title:         "Locked layer",
		Status:        StatusImplemented,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		Locked:        true,
		AffectedScope: []string{"pkg/proxy/*.go"},
	}

	openRecord := &Record{
		ID:            "prov-2026-b2c3d4e5",
		Title:         "Open record",
		Status:        StatusOpen,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		AffectedScope: []string{"pkg/auth/*.go"},
	}

	loader.Records = append(loader.Records, lockedRecord, openRecord)

	result := linter.LintAll()

	for _, issue := range result.Issues {
		if strings.Contains(issue.Message, "overlaps with locked record") {
			t.Errorf("Unexpected locked scope error: %v", issue)
		}
	}
}

func TestCheckLockedScope_SupersedesAllowed(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-locked-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "none")

	lockedRecord := &Record{
		ID:            "prov-2026-a1b2c3d4",
		Title:         "Locked layer",
		Status:        StatusImplemented,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		Locked:        true,
		AffectedScope: []string{"pkg/proxy/*.go"},
	}

	openRecord := &Record{
		ID:            "prov-2026-b2c3d4e5",
		Title:         "Open record",
		Status:        StatusOpen,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		AffectedScope: []string{"pkg/proxy/http.go"},
		Supersedes:    "prov-2026-a1b2c3d4",
	}

	loader.Records = append(loader.Records, lockedRecord, openRecord)

	result := linter.LintAll()

	for _, issue := range result.Issues {
		if strings.Contains(issue.Message, "overlaps with locked record") {
			t.Errorf("Unexpected locked scope error (should be allowed via supersedes): %v", issue)
		}
	}
}

func TestCheckLockedScope_AssociatedSpecsOverlap(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-locked-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "none")

	lockedRecord := &Record{
		ID:        "prov-2026-a1b2c3d4",
		Title:     "Locked layer",
		Status:    StatusImplemented,
		CreatedAt: "2026-04-03",
		Author:    "test@test.com",
		Intent:    "Test intent",
		Locked:    true,
		AssociatedSpecs: []AssociatedSpec{
			{Path: "specs/auth/login.linespec"},
		},
	}

	openRecord := &Record{
		ID:            "prov-2026-b2c3d4e5",
		Title:         "Open record",
		Status:        StatusOpen,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		AffectedScope: []string{"specs/auth/*.linespec"},
	}

	loader.Records = append(loader.Records, lockedRecord, openRecord)

	result := linter.LintAll()

	found := false
	for _, issue := range result.Issues {
		if strings.Contains(issue.Message, "overlaps with locked record") {
			found = true
			if issue.Field != "affected_scope" {
				t.Errorf("Expected field 'affected_scope', got %s", issue.Field)
			}
		}
	}
	if !found {
		t.Errorf("Expected locked scope overlap error for associated_specs, got issues: %v", result.Issues)
	}
}

func TestCheckLockedScope_OpenAssociatedSpecsOverlap(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-locked-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "none")

	lockedRecord := &Record{
		ID:            "prov-2026-a1b2c3d4",
		Title:         "Locked layer",
		Status:        StatusImplemented,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		Locked:        true,
		AffectedScope: []string{"pkg/proxy/*.go"},
	}

	openRecord := &Record{
		ID:        "prov-2026-b2c3d4e5",
		Title:     "Open record",
		Status:    StatusOpen,
		CreatedAt: "2026-04-03",
		Author:    "test@test.com",
		Intent:    "Test intent",
		AssociatedSpecs: []AssociatedSpec{
			{Path: "pkg/proxy/http.go"},
		},
	}

	loader.Records = append(loader.Records, lockedRecord, openRecord)

	result := linter.LintAll()

	found := false
	for _, issue := range result.Issues {
		if strings.Contains(issue.Message, "overlaps with locked record") {
			found = true
			if issue.Field != "associated_specs" {
				t.Errorf("Expected field 'associated_specs', got %s", issue.Field)
			}
		}
	}
	if !found {
		t.Errorf("Expected locked scope overlap error for open associated_specs, got issues: %v", result.Issues)
	}
}

func TestCheckLockedScope_BothAssociatedSpecsOverlap(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-locked-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "none")

	lockedRecord := &Record{
		ID:        "prov-2026-a1b2c3d4",
		Title:     "Locked layer",
		Status:    StatusImplemented,
		CreatedAt: "2026-04-03",
		Author:    "test@test.com",
		Intent:    "Test intent",
		Locked:    true,
		AssociatedSpecs: []AssociatedSpec{
			{Path: "specs/auth/login.linespec"},
		},
	}

	openRecord := &Record{
		ID:        "prov-2026-b2c3d4e5",
		Title:     "Open record",
		Status:    StatusOpen,
		CreatedAt: "2026-04-03",
		Author:    "test@test.com",
		Intent:    "Test intent",
		AssociatedSpecs: []AssociatedSpec{
			{Path: "specs/auth/login.linespec"},
		},
	}

	loader.Records = append(loader.Records, lockedRecord, openRecord)

	result := linter.LintAll()

	found := false
	for _, issue := range result.Issues {
		if strings.Contains(issue.Message, "overlaps with locked record") {
			found = true
			if issue.Field != "associated_specs" {
				t.Errorf("Expected field 'associated_specs', got %s", issue.Field)
			}
		}
	}
	if !found {
		t.Errorf("Expected locked scope overlap error for both associated_specs, got issues: %v", result.Issues)
	}
}

func TestCheckLockedScope_ImplementedNonLockedNoError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-locked-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "none")

	implementedRecord := &Record{
		ID:            "prov-2026-a1b2c3d4",
		Title:         "Implemented but not locked",
		Status:        StatusImplemented,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		Locked:        false,
		AffectedScope: []string{"pkg/proxy/*.go"},
	}

	openRecord := &Record{
		ID:            "prov-2026-b2c3d4e5",
		Title:         "Open record",
		Status:        StatusOpen,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		AffectedScope: []string{"pkg/proxy/http.go"},
	}

	loader.Records = append(loader.Records, implementedRecord, openRecord)

	result := linter.LintAll()

	for _, issue := range result.Issues {
		if strings.Contains(issue.Message, "overlaps with locked record") {
			t.Errorf("Unexpected locked scope error for non-locked record: %v", issue)
		}
	}
}

func TestContainsString(t *testing.T) {
	tests := []struct {
		slice    []string
		s        string
		expected bool
	}{
		{[]string{"a", "b", "c"}, "b", true},
		{[]string{"a", "b", "c"}, "d", false},
		{[]string{}, "a", false},
		{[]string{"a"}, "a", true},
	}

	for _, tt := range tests {
		result := containsString(tt.slice, tt.s)
		if result != tt.expected {
			t.Errorf("containsString(%v, %q) = %v, expected %v", tt.slice, tt.s, result, tt.expected)
		}
	}
}

func TestLockLayerOptions(t *testing.T) {
	opts := LockLayerOptions{
		Title:      "Test locked layer",
		NoEdit:     true,
		ConfigFile: ".linespec.yml",
	}

	if opts.Title != "Test locked layer" {
		t.Errorf("Expected Title 'Test locked layer', got %s", opts.Title)
	}
	if !opts.NoEdit {
		t.Error("Expected NoEdit to be true")
	}
	if opts.ConfigFile != ".linespec.yml" {
		t.Errorf("Expected ConfigFile '.linespec.yml', got %s", opts.ConfigFile)
	}
}

func TestRecordLockedField(t *testing.T) {
	record := &Record{
		ID:     "prov-2026-a1b2c3d4",
		Title:  "Test",
		Status: StatusImplemented,
		Locked: true,
	}

	if !record.Locked {
		t.Error("Expected Locked to be true")
	}

	record2 := &Record{
		ID:     "prov-2026-b2c3d4e5",
		Title:  "Test 2",
		Status: StatusOpen,
	}

	if record2.Locked {
		t.Error("Expected Locked to be false by default")
	}
}

func TestCheckLockedScope_MultipleLockedRecords(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-locked-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "none")

	lockedRecord1 := &Record{
		ID:            "prov-2026-a1b2c3d4",
		Title:         "Locked layer 1",
		Status:        StatusImplemented,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		Locked:        true,
		AffectedScope: []string{"pkg/proxy/*.go"},
	}

	lockedRecord2 := &Record{
		ID:            "prov-2026-b2c3d4e5",
		Title:         "Locked layer 2",
		Status:        StatusImplemented,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		Locked:        true,
		AffectedScope: []string{"pkg/auth/*.go"},
	}

	openRecord := &Record{
		ID:            "prov-2026-c3d4e5f6",
		Title:         "Open record",
		Status:        StatusOpen,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		AffectedScope: []string{"pkg/auth/login.go", "pkg/proxy/http.go"},
		Supersedes:    "prov-2026-a1b2c3d4",
	}

	loader.Records = append(loader.Records, lockedRecord1, lockedRecord2, openRecord)

	result := linter.LintAll()

	locked1Errors := 0
	locked2Errors := 0
	for _, issue := range result.Issues {
		if strings.Contains(issue.Message, "overlaps with locked record") {
			if strings.Contains(issue.Message, "prov-2026-a1b2c3d4") {
				locked1Errors++
			}
			if strings.Contains(issue.Message, "prov-2026-b2c3d4e5") {
				locked2Errors++
			}
		}
	}

	if locked1Errors > 0 {
		t.Errorf("Should not have errors for locked1 (superseded), got %d", locked1Errors)
	}
	if locked2Errors == 0 {
		t.Errorf("Should have errors for locked2 overlap, got issues: %v", result.Issues)
	}
}

func TestCheckLockedScope_GlobPatternOverlap(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-locked-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "none")

	lockedRecord := &Record{
		ID:            "prov-2026-a1b2c3d4",
		Title:         "Locked layer",
		Status:        StatusImplemented,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		Locked:        true,
		AffectedScope: []string{"pkg/**/*.go"},
	}

	openRecord := &Record{
		ID:            "prov-2026-b2c3d4e5",
		Title:         "Open record",
		Status:        StatusOpen,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		AffectedScope: []string{"pkg/proxy/*.go"},
	}

	loader.Records = append(loader.Records, lockedRecord, openRecord)

	result := linter.LintAll()

	found := false
	for _, issue := range result.Issues {
		if strings.Contains(issue.Message, "overlaps with locked record") {
			found = true
		}
	}
	if !found {
		t.Errorf("Expected locked scope overlap error for glob patterns, got issues: %v", result.Issues)
	}
}

func TestCheckLockedScope_ExactPathMatch(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "linter-locked-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	loader := NewLoader(tmpDir, nil)
	linter := NewLinter(loader, "none")

	lockedRecord := &Record{
		ID:            "prov-2026-a1b2c3d4",
		Title:         "Locked layer",
		Status:        StatusImplemented,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		Locked:        true,
		AffectedScope: []string{"pkg/proxy/proxy.go"},
	}

	openRecord := &Record{
		ID:            "prov-2026-b2c3d4e5",
		Title:         "Open record",
		Status:        StatusOpen,
		CreatedAt:     "2026-04-03",
		Author:        "test@test.com",
		Intent:        "Test intent",
		AffectedScope: []string{"pkg/proxy/proxy.go"},
	}

	loader.Records = append(loader.Records, lockedRecord, openRecord)

	result := linter.LintAll()

	found := false
	for _, issue := range result.Issues {
		if strings.Contains(issue.Message, "overlaps with locked record") {
			found = true
		}
	}
	if !found {
		t.Errorf("Expected locked scope overlap error for exact path match, got issues: %v", result.Issues)
	}
}

// TestLockLayer_CreatesLockedRecord covers the half of lock-layer that nothing
// else in this file touches.
//
// Every other test here exercises checkLockedScope — whether an existing locked
// record blocks an overlapping open one. That is the enforcement half, and it
// works. Nothing called LockLayer itself, so the half that PRODUCES a locked
// record was never run: createRecord wrote the file without registering it with
// the loader, Complete could not resolve the id it had just written, and the
// command died at step 2 of 4 leaving a stray draft and no lock.
func TestLockLayer_CreatesLockedRecord(t *testing.T) {
	cmds, _, buf := newTransitionTestRepo(t, false)

	if err := cmds.LockLayer(LockLayerOptions{
		Title:  "Applied migrations are history",
		Intent: "An applied migration is a fact about a database, not an instance of a template.",
		Scope:  []string{"supabase/migrations/**"},
		NoEdit: true,
	}); err != nil {
		t.Fatalf("LockLayer: %v\n%s", err, buf.String())
	}

	ids := cmds.Loader.GetAllIDs()
	if len(ids) != 1 {
		t.Fatalf("expected exactly one record after LockLayer, got %d", len(ids))
	}

	inMemory, exists := cmds.Loader.GetRecord(ids[0])
	if !exists {
		t.Fatalf("record %s is not resolvable through the loader after LockLayer", ids[0])
	}

	// Assert against the FILE, not the in-memory record. SaveRecord's
	// formatting-preserving path rewrites only status, sealed_at_sha and
	// superseded_by, so a field set in memory can be silently absent on disk —
	// and the in-memory copy agrees with itself either way. Reading the file
	// back is what makes this test able to fail.
	record, err := cmds.Loader.LoadFile(inMemory.FilePath)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	if record.Intent == "" {
		t.Error("intent is empty on disk; the intent never reached the file")
	}
	if len(record.AffectedScope) != 1 || record.AffectedScope[0] != "supabase/migrations/**" {
		t.Errorf("affected_scope on disk = %v, want [supabase/migrations/**]", record.AffectedScope)
	}

	// The three things that make a record an actual lock rather than an
	// ordinary allowlist. A record missing any one of them lints clean and
	// locks nothing, which is the failure worth catching.
	if record.Status != StatusImplemented {
		t.Errorf("status = %q, want %q", record.Status, StatusImplemented)
	}
	if !record.Locked {
		t.Error("locked = false; lock-layer must produce a locked record")
	}
	if record.SealedAtSHA == "" {
		t.Error("sealed_at_sha is empty; the record was never sealed against HEAD")
	}

	// The locked flag is an exported field, so it is part of the sealed content
	// hash. Setting it after Complete would leave the record permanently
	// failing its own integrity check — green here only if it was sealed WITH
	// the flag already set.
	stored, current, ok, err := cmds.Linter.Hasher.VerifyRecord(record)
	if err != nil {
		t.Fatalf("VerifyRecord: %v", err)
	}
	_ = inMemory
	if !ok {
		t.Errorf("sealed content hash does not match the record on disk:\n stored  = %s\n current = %s", stored, current)
	}
}

// TestLockLayer_NoEditRequiresIntent guards the refusal that replaced writing a
// record nobody could ever complete. A locked record is sealed on creation, so
// with no editor and no intent there is no point at which prose could arrive.
func TestLockLayer_NoEditRequiresIntent(t *testing.T) {
	cmds, _, _ := newTransitionTestRepo(t, false)

	err := cmds.LockLayer(LockLayerOptions{Title: "No intent anywhere", NoEdit: true})
	if err == nil {
		t.Fatal("expected LockLayer to refuse --no-edit with no intent")
	}
	if ids := cmds.Loader.GetAllIDs(); len(ids) != 0 {
		t.Errorf("refusal left %d record(s) behind; it must refuse before creating one", len(ids))
	}
}

// TestCreateRecord_IsResolvableInSameProcess states the underlying invariant
// directly, so the next command that creates a record and then acts on it does
// not have to rediscover this. LockLayer is only the first caller to need it.
func TestCreateRecord_IsResolvableInSameProcess(t *testing.T) {
	cmds, _, _ := newTransitionTestRepo(t, false)

	record, err := cmds.createRecord(CreateOptions{Title: "A record", NoEdit: true})
	if err != nil {
		t.Fatalf("createRecord: %v", err)
	}

	if _, exists := cmds.Loader.GetRecord(record.ID); !exists {
		t.Fatalf("record %s was written to disk but is not resolvable by id in the same process", record.ID)
	}
}
