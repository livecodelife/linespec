package records_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/livecodelife/linespec/pkg/discover/boundaries"
	"github.com/livecodelife/linespec/pkg/discover/records"
	"github.com/livecodelife/linespec/pkg/discover/routes"
	"github.com/livecodelife/linespec/pkg/discover/stubs"
)

// --- deriveTitle (via Plan output) ---

func TestPlan_TitlesFromPackageName(t *testing.T) {
	in := records.Input{
		Groups: []routes.Group{
			{Name: "handlers", Routes: []routes.Route{{Method: "GET", Path: "/health"}}},
		},
		ProvenanceDir: t.TempDir(),
	}
	results, _ := records.Plan(in, nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "handlers endpoints" {
		t.Errorf("Title = %q; want %q", results[0].Title, "handlers endpoints")
	}
}

func TestPlan_TitlesFromControllerName(t *testing.T) {
	in := records.Input{
		Groups: []routes.Group{
			{Name: "UsersController", Routes: []routes.Route{{Method: "GET", Path: "/users"}}},
		},
		ProvenanceDir: t.TempDir(),
	}
	results, _ := records.Plan(in, nil)
	if results[0].Title != "UsersController endpoints" {
		t.Errorf("Title = %q; want %q", results[0].Title, "UsersController endpoints")
	}
}

func TestPlan_TitlesFromFilePath(t *testing.T) {
	in := records.Input{
		Groups: []routes.Group{
			{Name: "app/controllers/users_controller.rb", Routes: []routes.Route{{Method: "GET", Path: "/users"}}},
		},
		ProvenanceDir: t.TempDir(),
	}
	results, _ := records.Plan(in, nil)
	if results[0].Title != "users_controller endpoints" {
		t.Errorf("Title = %q; want %q", results[0].Title, "users_controller endpoints")
	}
}

// --- Plan ---

func TestPlan_NoFilesWritten(t *testing.T) {
	dir := t.TempDir()
	in := records.Input{
		Groups: []routes.Group{
			{Name: "handlers", Routes: []routes.Route{
				{Method: "GET", Path: "/api/v1/users"},
				{Method: "POST", Path: "/api/v1/users"},
			}},
		},
		ProvenanceDir: dir,
	}

	results, sum := records.Plan(in, nil)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].RouteCount != 2 {
		t.Errorf("RouteCount = %d; want 2", results[0].RouteCount)
	}
	if sum.RouteCount != 2 {
		t.Errorf("Summary.RouteCount = %d; want 2", sum.RouteCount)
	}
	if sum.RecordsCreated != 1 {
		t.Errorf("Summary.RecordsCreated = %d; want 1", sum.RecordsCreated)
	}

	// No files should be written
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("Plan must not write files, got %d files", len(entries))
	}
}

func TestPlan_UniqueIDs(t *testing.T) {
	in := records.Input{
		Groups: []routes.Group{
			{Name: "handlers", Routes: []routes.Route{{Method: "GET", Path: "/a"}}},
			{Name: "api", Routes: []routes.Route{{Method: "GET", Path: "/b"}}},
			{Name: "admin", Routes: []routes.Route{{Method: "GET", Path: "/c"}}},
		},
		ProvenanceDir: t.TempDir(),
	}
	results, _ := records.Plan(in, nil)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	seen := map[string]bool{}
	for _, r := range results {
		if seen[r.RecordID] {
			t.Errorf("duplicate record ID: %s", r.RecordID)
		}
		seen[r.RecordID] = true
	}
}

func TestPlan_IDsAvoidExisting(t *testing.T) {
	existing := []string{"prov-2026-aabbccdd"}
	in := records.Input{
		Groups:        []routes.Group{{Name: "handlers", Routes: []routes.Route{{Method: "GET", Path: "/"}}}},
		ProvenanceDir: t.TempDir(),
	}
	results, _ := records.Plan(in, existing)
	if len(results) == 0 {
		t.Fatal("expected 1 result")
	}
	if results[0].RecordID == "prov-2026-aabbccdd" {
		t.Error("generated ID collides with existing ID")
	}
}

// --- Write ---

func TestWrite_CreatesRecordFiles(t *testing.T) {
	dir := t.TempDir()
	in := records.Input{
		Groups: []routes.Group{
			{
				Name: "handlers",
				Routes: []routes.Route{
					{Method: "GET", Path: "/api/v1/users", Source: routes.SourceLocation{File: "handlers/users.go"}},
					{Method: "POST", Path: "/api/v1/users", Source: routes.SourceLocation{File: "handlers/users.go"}},
				},
			},
		},
		ProvenanceDir: dir,
		Author:        "test@example.com",
	}

	results, sum, err := records.Write(in, nil)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if sum.RecordsCreated != 1 {
		t.Errorf("Summary.RecordsCreated = %d; want 1", sum.RecordsCreated)
	}

	// File should exist on disk
	if _, err := os.Stat(results[0].FilePath); err != nil {
		t.Errorf("record file not found: %s", results[0].FilePath)
	}
}

func TestWrite_RecordFileNameFormat(t *testing.T) {
	dir := t.TempDir()
	in := records.Input{
		Groups: []routes.Group{
			{Name: "handlers", Routes: []routes.Route{{Method: "GET", Path: "/"}}},
		},
		ProvenanceDir: dir,
	}
	results, _, err := records.Write(in, nil)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	base := filepath.Base(results[0].FilePath)
	if !strings.HasPrefix(base, "prov-2026-") || !strings.HasSuffix(base, ".yml") {
		t.Errorf("unexpected file name format: %s", base)
	}
}

func TestWrite_MultipleGroupsProduceMultipleRecords(t *testing.T) {
	dir := t.TempDir()
	in := records.Input{
		Groups: []routes.Group{
			{Name: "handlers", Routes: []routes.Route{{Method: "GET", Path: "/a"}}},
			{Name: "api", Routes: []routes.Route{{Method: "GET", Path: "/b"}}},
		},
		ProvenanceDir: dir,
	}
	results, _, err := records.Write(in, nil)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	ids := map[string]bool{}
	for _, r := range results {
		if ids[r.RecordID] {
			t.Errorf("duplicate record ID: %s", r.RecordID)
		}
		ids[r.RecordID] = true
		if _, err := os.Stat(r.FilePath); err != nil {
			t.Errorf("record file not found: %s", r.FilePath)
		}
	}
}

func TestWrite_AffectedScopeFromRouteSourceFiles(t *testing.T) {
	dir := t.TempDir()
	in := records.Input{
		Groups: []routes.Group{
			{
				Name: "handlers",
				Routes: []routes.Route{
					{Method: "GET", Path: "/a", Source: routes.SourceLocation{File: "handlers/a.go"}},
					{Method: "POST", Path: "/b", Source: routes.SourceLocation{File: "handlers/b.go"}},
					{Method: "DELETE", Path: "/c", Source: routes.SourceLocation{File: "handlers/a.go"}}, // duplicate
				},
			},
		},
		ProvenanceDir: dir,
	}
	results, _, err := records.Write(in, nil)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read the written YAML to verify affected_scope
	data, err := os.ReadFile(results[0].FilePath)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "handlers/a.go") {
		t.Error("expected affected_scope to contain handlers/a.go")
	}
	if !strings.Contains(content, "handlers/b.go") {
		t.Error("expected affected_scope to contain handlers/b.go")
	}
	// Check deduplication: "a.go" should appear only once
	if strings.Count(content, "handlers/a.go") != 1 {
		t.Error("handlers/a.go appears more than once in affected_scope (not deduplicated)")
	}
}

func TestWrite_AssociatedSpecsFromStubResults(t *testing.T) {
	dir := t.TempDir()
	in := records.Input{
		Groups: []routes.Group{
			{
				Name: "handlers",
				Routes: []routes.Route{
					{Method: "GET", Path: "/api/v1/users"},
					{Method: "POST", Path: "/api/v1/users"},
				},
			},
		},
		StubResults: []stubs.Result{
			{FilePath: "specs/get_api_v1_users.linespec", Method: "GET", Path: "/api/v1/users"},
			{FilePath: "specs/post_api_v1_users.linespec", Method: "POST", Path: "/api/v1/users"},
		},
		ProvenanceDir: dir,
	}
	results, _, err := records.Write(in, nil)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(results[0].FilePath)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "get_api_v1_users.linespec") {
		t.Error("expected associated_specs to contain get_api_v1_users.linespec")
	}
	if !strings.Contains(content, "post_api_v1_users.linespec") {
		t.Error("expected associated_specs to contain post_api_v1_users.linespec")
	}
}

func TestWrite_RecordStatusIsDraft(t *testing.T) {
	dir := t.TempDir()
	in := records.Input{
		Groups:        []routes.Group{{Name: "handlers", Routes: []routes.Route{{Method: "GET", Path: "/"}}}},
		ProvenanceDir: dir,
	}
	results, _, err := records.Write(in, nil)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, _ := os.ReadFile(results[0].FilePath)
	if !strings.Contains(string(data), "status: draft") {
		t.Error("expected status: draft in generated record")
	}
}

func TestWrite_RecordTypeIsBlueprint(t *testing.T) {
	dir := t.TempDir()
	in := records.Input{
		Groups:        []routes.Group{{Name: "handlers", Routes: []routes.Route{{Method: "GET", Path: "/"}}}},
		ProvenanceDir: dir,
	}
	results, _, err := records.Write(in, nil)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, _ := os.ReadFile(results[0].FilePath)
	if !strings.Contains(string(data), "type: blueprint") {
		t.Error("expected type: blueprint in generated record")
	}
}

func TestWrite_CreatesProvenanceDirIfMissing(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "nested", "provenance")
	in := records.Input{
		Groups:        []routes.Group{{Name: "handlers", Routes: []routes.Route{{Method: "GET", Path: "/"}}}},
		ProvenanceDir: dir,
	}
	_, _, err := records.Write(in, nil)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Error("provenance directory was not created")
	}
}

func TestWrite_TagsIncludeDiscover(t *testing.T) {
	dir := t.TempDir()
	in := records.Input{
		Groups:        []routes.Group{{Name: "handlers", Routes: []routes.Route{{Method: "GET", Path: "/"}}}},
		ProvenanceDir: dir,
	}
	results, _, err := records.Write(in, nil)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, _ := os.ReadFile(results[0].FilePath)
	if !strings.Contains(string(data), "discover") {
		t.Error("expected 'discover' tag in generated record")
	}
}

// --- Summary ---

func TestSummary_RouteCount(t *testing.T) {
	in := records.Input{
		Groups: []routes.Group{
			{Name: "a", Routes: []routes.Route{{Method: "GET", Path: "/a"}, {Method: "POST", Path: "/a"}}},
			{Name: "b", Routes: []routes.Route{{Method: "GET", Path: "/b"}}},
		},
		ProvenanceDir: t.TempDir(),
	}
	_, sum := records.Plan(in, nil)
	if sum.RouteCount != 3 {
		t.Errorf("RouteCount = %d; want 3", sum.RouteCount)
	}
}

func TestSummary_BoundaryCount(t *testing.T) {
	in := records.Input{
		Groups: []routes.Group{
			{Name: "handlers", Routes: []routes.Route{{Method: "GET", Path: "/a"}}},
		},
		Boundaries: map[string][]boundaries.Hit{
			"handlers.GetA": {
				{Protocol: "postgresql", Direction: "read", Target: "users"},
				{Protocol: "redis", Direction: "read", Target: "session"},
			},
			"handlers.PostA": {
				{Protocol: "postgresql", Direction: "write", Target: "users"},
			},
		},
		ProvenanceDir: t.TempDir(),
	}
	_, sum := records.Plan(in, nil)
	if sum.BoundaryCount != 3 {
		t.Errorf("BoundaryCount = %d; want 3", sum.BoundaryCount)
	}
}

// --- FormatTable ---

func TestFormatTable_Empty(t *testing.T) {
	out := records.FormatTable(nil, records.Summary{})
	if !strings.Contains(out, "No records") {
		t.Errorf("expected 'No records' in empty output, got: %s", out)
	}
}

func TestFormatTable_Basic(t *testing.T) {
	results := []records.Result{
		{GroupName: "handlers", RecordID: "prov-2026-aabbccdd", Title: "handlers endpoints", RouteCount: 3},
		{GroupName: "api", RecordID: "prov-2026-11223344", Title: "api endpoints", RouteCount: 5},
	}
	sum := records.Summary{RouteCount: 8, BoundaryCount: 4, RecordsCreated: 2}
	out := records.FormatTable(results, sum)

	assertContains(t, out, "handlers endpoints")
	assertContains(t, out, "prov-2026-aabbccdd")
	assertContains(t, out, "api endpoints")
	assertContains(t, out, "prov-2026-11223344")
	assertContains(t, out, "8")
	assertContains(t, out, "4")
	assertContains(t, out, "2")
}

// --- FormatJSON ---

func TestFormatJSON_Basic(t *testing.T) {
	results := []records.Result{
		{GroupName: "handlers", RecordID: "prov-2026-aabbccdd", Title: "handlers endpoints", RouteCount: 3, FilePath: "provenance/prov-2026-aabbccdd.yml"},
	}
	sum := records.Summary{RouteCount: 3, BoundaryCount: 1, RecordsCreated: 1}
	data, err := records.FormatJSON(results, sum)
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}
	s := string(data)
	assertContains(t, s, `"record_id"`)
	assertContains(t, s, `"prov-2026-aabbccdd"`)
	assertContains(t, s, `"route_count"`)
	assertContains(t, s, `"records_created"`)
	assertContains(t, s, `"boundary_count"`)
	assertContains(t, s, `"unclassified"`)
}

func TestFormatJSON_Empty(t *testing.T) {
	data, err := records.FormatJSON(nil, records.Summary{})
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}
	if !strings.Contains(string(data), `"records"`) {
		t.Error("expected 'records' key in empty JSON output")
	}
}

// --- helpers ---

func assertContains(t *testing.T, content, substr string) {
	t.Helper()
	if !strings.Contains(content, substr) {
		t.Errorf("expected output to contain %q\ngot:\n%s", substr, content)
	}
}
