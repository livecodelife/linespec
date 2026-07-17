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

func keys(m map[string]*framework.Description) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
