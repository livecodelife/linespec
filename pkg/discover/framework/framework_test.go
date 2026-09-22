package framework_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/discover/framework"
)

const testDescYAML = `
name: testframework
language: go
detection:
  - manifest: go.mod
    contains:
      - "testframework/core"
route_queries:
  - pattern: |
      (call_expression function: (identifier) @fn)
    filter:
      fn: "^route$"
grouping_strategy: package
boundary_queries:
  - protocol: postgresql
    direction: read
    pattern: |
      (call_expression function: (identifier) @q)
    captures:
      target: q
`

func TestLoader_UserDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "testframework.yml"), []byte(testDescYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	descs, err := framework.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	d, ok := descs["testframework"]
	if !ok {
		t.Fatalf("expected 'testframework' in loaded descriptions, got keys: %v", keys(descs))
	}
	if d.Language != "go" {
		t.Errorf("Language: got %q, want %q", d.Language, "go")
	}
	if d.GroupingStrategy != "package" {
		t.Errorf("GroupingStrategy: got %q, want %q", d.GroupingStrategy, "package")
	}
	if len(d.RouteQueries) != 1 {
		t.Errorf("RouteQueries: got %d, want 1", len(d.RouteQueries))
	}
	if len(d.BoundaryQueries) != 1 {
		t.Errorf("BoundaryQueries: got %d, want 1", len(d.BoundaryQueries))
	}
}

func TestLoader_EmptyUserDir(t *testing.T) {
	descs, err := framework.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load with empty user dir: %v", err)
	}
	// Built-ins only — no error expected
	_ = descs
}

func TestLoader_NoUserDir(t *testing.T) {
	descs, err := framework.Load("")
	if err != nil {
		t.Fatalf("Load with no user dir: %v", err)
	}
	_ = descs
}

func TestLoader_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.yml"), []byte("name: [invalid yaml: {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := framework.Load(dir)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestLoader_MissingName(t *testing.T) {
	dir := t.TempDir()
	yaml := "language: go\ngrouping_strategy: package\n"
	if err := os.WriteFile(filepath.Join(dir, "noname.yml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := framework.Load(dir)
	if err == nil {
		t.Fatal("expected error for missing name field, got nil")
	}
}

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example\n\nrequire testframework/core v1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	userDescDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(userDescDir, "testframework.yml"), []byte(testDescYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	descs, err := framework.Load(userDescDir)
	if err != nil {
		t.Fatal(err)
	}

	result := framework.Detect(dir, descs)
	if result == nil {
		t.Fatal("expected detection result, got nil")
	}
	if result.Framework != "testframework" {
		t.Errorf("Framework: got %q, want %q", result.Framework, "testframework")
	}
	if result.Language != "go" {
		t.Errorf("Language: got %q, want %q", result.Language, "go")
	}
}

func TestDetect_NoMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	descs, _ := framework.Load("")
	result := framework.Detect(dir, descs)
	// No built-ins yet — expect nil
	if result != nil {
		t.Errorf("expected nil result for unrecognized project, got %+v", result)
	}
}

// TestLoader_Builtin_Nextjs verifies nextjs.yml (prov-2026-4446307d) loads
// with its filesystem_routes section intact, since it has no
// route_queries/group_queries at all — the field this framework relies on
// entirely instead.
func TestLoader_Builtin_Nextjs(t *testing.T) {
	descs, err := framework.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	d, ok := descs["nextjs"]
	if !ok {
		t.Fatalf("expected built-in 'nextjs' framework, got keys: %v", keys(descs))
	}
	if d.Language != "typescript" {
		t.Errorf("Language: got %q, want %q", d.Language, "typescript")
	}
	if d.FilesystemRoutes == nil {
		t.Fatal("expected FilesystemRoutes to be set")
	}
	if d.FilesystemRoutes.AppDir != "app" {
		t.Errorf("AppDir: got %q, want %q", d.FilesystemRoutes.AppDir, "app")
	}
	if d.FilesystemRoutes.RouteFile != "route" {
		t.Errorf("RouteFile: got %q, want %q", d.FilesystemRoutes.RouteFile, "route")
	}
	if len(d.BoundaryQueries) == 0 {
		t.Error("expected at least one boundary query (DB client + fetch)")
	}
}

// TestDetect_Nextjs verifies auto-detection from package.json's "next"
// dependency, per prov-2026-4446307d's constraint.
func TestDetect_Nextjs(t *testing.T) {
	dir := t.TempDir()
	pkgJSON := `{"name": "my-app", "dependencies": {"next": "^14.2.0", "react": "^18.0.0"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	descs, err := framework.Load("")
	if err != nil {
		t.Fatal(err)
	}
	result := framework.Detect(dir, descs)
	if result == nil {
		t.Fatal("expected detection result for a package.json with a next dependency, got nil")
	}
	if result.Framework != "nextjs" {
		t.Errorf("Framework: got %q, want %q", result.Framework, "nextjs")
	}
	if result.Language != "typescript" {
		t.Errorf("Language: got %q, want %q", result.Language, "typescript")
	}
}

// TestDetect_Nextjs_NoFalsePositiveOnUnrelatedNextSubstring guards the
// detection pattern's precision: a package merely containing the substring
// "next" (e.g. a hypothetical "next-gen-utils" dependency) must not trigger
// detection — only the literal "next" dependency key does.
func TestDetect_Nextjs_NoFalsePositiveOnUnrelatedNextSubstring(t *testing.T) {
	dir := t.TempDir()
	pkgJSON := `{"name": "my-app", "dependencies": {"next-gen-utils": "^1.0.0"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	descs, err := framework.Load("")
	if err != nil {
		t.Fatal(err)
	}
	result := framework.Detect(dir, descs)
	if result != nil && result.Framework == "nextjs" {
		t.Errorf("expected no nextjs detection for an unrelated \"next-gen-utils\" dependency, got %+v", result)
	}
}

func keys(m map[string]*framework.Description) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
