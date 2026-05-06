package provenance

import (
	"bytes"
	"strings"
	"testing"
)

func makeTestCommands(records []*Record) *Commands {
	loader := &Loader{
		Records:     records,
		RecordsByID: make(map[string]*Record),
	}
	for _, r := range records {
		loader.RecordsByID[r.ID] = r
	}
	var buf bytes.Buffer
	return &Commands{
		Loader:    loader,
		Formatter: NewFormatter(&buf, false),
	}
}

func TestGenerate_BlueprintTarget(t *testing.T) {
	bp := &Record{
		ID:          "prov-2026-aabbccdd",
		Title:       "Widget service",
		Status:      StatusOpen,
		Type:        RecordTypeBlueprint,
		Intent:      "Build the widget service.",
		Constraints: []string{"Must be fast"},
		Tags:        []string{"widgets"},
	}
	cmds := makeTestCommands([]*Record{bp})

	var out bytes.Buffer
	cmds.Formatter.Output = &out

	err := cmds.Generate(GenerateOptions{RecordID: "prov-2026-aabbccdd"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	// Title should be the record title as h1, no "Blueprint:" prefix
	if !strings.Contains(got, "# Widget service") {
		t.Errorf("expected h1 title, got:\n%s", got)
	}
	if strings.Contains(got, "Blueprint:") {
		t.Errorf("no 'Blueprint:' prefix in Phoenix format, got:\n%s", got)
	}
	if !strings.Contains(got, "- Must be fast") {
		t.Errorf("expected constraint as bullet, got:\n%s", got)
	}
	if !strings.Contains(got, "Build the widget service.") {
		t.Errorf("expected intent prose, got:\n%s", got)
	}
}

func TestGenerate_BriefTarget_WithBlueprints(t *testing.T) {
	brief := &Record{
		ID:     "prov-2026-brief001",
		Title:  "Auth system",
		Status: StatusOpen,
		Type:   RecordTypeBrief,
		Intent: "Handle authentication.",
	}
	bp := &Record{
		ID:          "prov-2026-bp000001",
		Title:       "JWT implementation",
		Status:      StatusOpen,
		Type:        RecordTypeBlueprint,
		Implements:  "prov-2026-brief001",
		Intent:      "Implement JWT.",
		Constraints: []string{"Token must expire in 24h"},
	}
	cmds := makeTestCommands([]*Record{brief, bp})

	var out bytes.Buffer
	cmds.Formatter.Output = &out

	err := cmds.Generate(GenerateOptions{RecordID: "prov-2026-brief001"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	// Brief title becomes h1, no "Brief:" prefix
	if !strings.Contains(got, "# Auth system") {
		t.Errorf("expected h1 title for brief, got:\n%s", got)
	}
	if strings.Contains(got, "Brief:") || strings.Contains(got, "Blueprint:") {
		t.Errorf("no type prefixes in Phoenix format, got:\n%s", got)
	}
	// Blueprint becomes h2 subsection
	if !strings.Contains(got, "## JWT implementation") {
		t.Errorf("expected h2 for blueprint subsection, got:\n%s", got)
	}
	if !strings.Contains(got, "- Token must expire in 24h") {
		t.Errorf("expected constraint as bullet, got:\n%s", got)
	}
}

func TestGenerate_BriefTarget_SupersededBlueprintReplaced(t *testing.T) {
	brief := &Record{
		ID:     "prov-2026-brief001",
		Title:  "Auth system",
		Status: StatusOpen,
		Type:   RecordTypeBrief,
	}
	old := &Record{
		ID:           "prov-2026-bp000001",
		Title:        "Old JWT implementation",
		Status:       StatusSuperseded,
		Type:         RecordTypeBlueprint,
		Implements:   "prov-2026-brief001",
		SupersededBy: "prov-2026-bp000002",
	}
	replacement := &Record{
		ID:          "prov-2026-bp000002",
		Title:       "New JWT implementation",
		Status:      StatusOpen,
		Type:        RecordTypeBlueprint,
		Constraints: []string{"Use RS256"},
	}
	cmds := makeTestCommands([]*Record{brief, old, replacement})

	var out bytes.Buffer
	cmds.Formatter.Output = &out

	err := cmds.Generate(GenerateOptions{RecordID: "prov-2026-brief001"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "Old JWT") {
		t.Errorf("superseded blueprint should not appear, got:\n%s", got)
	}
	if !strings.Contains(got, "New JWT implementation") {
		t.Errorf("expected replacement blueprint in output, got:\n%s", got)
	}
	// Verify no metadata leaks into output
	if strings.Contains(got, "prov-2026-") {
		t.Errorf("record IDs should not appear in Phoenix format, got:\n%s", got)
	}
}

func TestGenerate_BugExtendsMerged(t *testing.T) {
	bp := &Record{
		ID:          "prov-2026-bp000001",
		Title:       "Cache layer",
		Status:      StatusOpen,
		Type:        RecordTypeBlueprint,
		Intent:      "Add caching.",
		Constraints: []string{"Cache TTL must be configurable"},
	}
	bug := &Record{
		ID:          "prov-2026-bug00001",
		Title:       "Cache invalidation fix",
		Status:      StatusOpen,
		Type:        RecordTypeBug,
		Extends:     "prov-2026-bp000001",
		Intent:      "Fix stale cache entries.",
		Constraints: []string{"Invalidate on write"},
	}
	cmds := makeTestCommands([]*Record{bp, bug})

	var out bytes.Buffer
	cmds.Formatter.Output = &out

	err := cmds.Generate(GenerateOptions{RecordID: "prov-2026-bp000001"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "- Cache TTL must be configurable") {
		t.Errorf("expected original constraint as bullet, got:\n%s", got)
	}
	if !strings.Contains(got, "- Invalidate on write") {
		t.Errorf("expected merged bug constraint as bullet, got:\n%s", got)
	}
	if !strings.Contains(got, "Fix stale cache entries.") {
		t.Errorf("expected merged bug intent prose, got:\n%s", got)
	}
}

func TestGenerate_BugSupersedesBlueprint_FullGraph(t *testing.T) {
	old := &Record{
		ID:           "prov-2026-bp000001",
		Title:        "Old cache layer",
		Status:       StatusSuperseded,
		Type:         RecordTypeBlueprint,
		SupersededBy: "prov-2026-bug00001",
	}
	bug := &Record{
		ID:          "prov-2026-bug00001",
		Title:       "Cache layer rewrite",
		Status:      StatusOpen,
		Type:        RecordTypeBug,
		Supersedes:  "prov-2026-bp000001",
		Constraints: []string{"Use Redis"},
	}
	cmds := makeTestCommands([]*Record{old, bug})

	var out bytes.Buffer
	cmds.Formatter.Output = &out

	err := cmds.Generate(GenerateOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "Old cache layer") {
		t.Errorf("superseded blueprint should not appear, got:\n%s", got)
	}
	if !strings.Contains(got, "Cache layer rewrite") {
		t.Errorf("expected bug replacement in output, got:\n%s", got)
	}
	if !strings.Contains(got, "- Use Redis") {
		t.Errorf("expected bug constraint as bullet, got:\n%s", got)
	}
}

func TestGenerate_ImprintTargetError(t *testing.T) {
	imp := &Record{
		ID:     "prov-2026-imp00001",
		Title:  "Some imprint",
		Status: StatusOpen,
		Type:   RecordTypeImprint,
	}
	cmds := makeTestCommands([]*Record{imp})

	err := cmds.Generate(GenerateOptions{RecordID: "prov-2026-imp00001"})
	if err == nil {
		t.Fatal("expected error for imprint target")
	}
	if !strings.Contains(err.Error(), "imprint") {
		t.Errorf("expected imprint error message, got: %v", err)
	}
}

func TestGenerate_BugTargetError(t *testing.T) {
	bug := &Record{
		ID:     "prov-2026-bug00001",
		Title:  "Some bug",
		Status: StatusOpen,
		Type:   RecordTypeBug,
	}
	cmds := makeTestCommands([]*Record{bug})

	err := cmds.Generate(GenerateOptions{RecordID: "prov-2026-bug00001"})
	if err == nil {
		t.Fatal("expected error for bug target")
	}
	if !strings.Contains(err.Error(), "bug") {
		t.Errorf("expected bug error message, got: %v", err)
	}
}

func TestGenerate_RecordNotFoundError(t *testing.T) {
	cmds := makeTestCommands([]*Record{})

	err := cmds.Generate(GenerateOptions{RecordID: "prov-2026-missing1"})
	if err == nil {
		t.Fatal("expected error for missing record")
	}
}

func TestGenerate_ImprintsExcludedFromFullGraph(t *testing.T) {
	bp := &Record{
		ID:     "prov-2026-bp000001",
		Title:  "Visible blueprint",
		Status: StatusOpen,
		Type:   RecordTypeBlueprint,
	}
	imp := &Record{
		ID:     "prov-2026-imp00001",
		Title:  "Should not appear",
		Status: StatusOpen,
		Type:   RecordTypeImprint,
	}
	cmds := makeTestCommands([]*Record{bp, imp})

	var out bytes.Buffer
	cmds.Formatter.Output = &out

	err := cmds.Generate(GenerateOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "Should not appear") {
		t.Errorf("imprint should be excluded from full graph output, got:\n%s", got)
	}
	if !strings.Contains(got, "Visible blueprint") {
		t.Errorf("expected blueprint in output, got:\n%s", got)
	}
}

func TestGenerate_YAMLFormat(t *testing.T) {
	bp := &Record{
		ID:          "prov-2026-bp000001",
		Title:       "Widget service",
		Status:      StatusOpen,
		Type:        RecordTypeBlueprint,
		Constraints: []string{"Must be fast"},
	}
	cmds := makeTestCommands([]*Record{bp})

	var out bytes.Buffer
	cmds.Formatter.Output = &out

	err := cmds.Generate(GenerateOptions{RecordID: "prov-2026-bp000001", Format: "yaml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "linespec_spec_version") {
		t.Errorf("expected YAML front matter, got:\n%s", got)
	}
	if !strings.Contains(got, "specifications:") {
		t.Errorf("expected specifications key in YAML, got:\n%s", got)
	}
	if !strings.Contains(got, "Must be fast") {
		t.Errorf("expected constraint in YAML, got:\n%s", got)
	}
}

func TestGenerate_FullGraph_OnlyActiveRecords(t *testing.T) {
	active := &Record{
		ID:     "prov-2026-bp000001",
		Title:  "Active blueprint",
		Status: StatusOpen,
		Type:   RecordTypeBlueprint,
	}
	draft := &Record{
		ID:     "prov-2026-bp000002",
		Title:  "Draft blueprint",
		Status: StatusDraft,
		Type:   RecordTypeBlueprint,
	}
	cmds := makeTestCommands([]*Record{active, draft})

	var out bytes.Buffer
	cmds.Formatter.Output = &out

	err := cmds.Generate(GenerateOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Active blueprint") {
		t.Errorf("expected active blueprint in output, got:\n%s", got)
	}
	if strings.Contains(got, "Draft blueprint") {
		t.Errorf("draft blueprint should not appear, got:\n%s", got)
	}
}
