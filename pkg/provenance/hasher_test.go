package provenance

import (
	"os"
	"path/filepath"
	"testing"
)

func makeTestRecord(id, status string) *Record {
	return &Record{
		ID:     id,
		Title:  "Test record " + id,
		Status: Status(status),
		Type:   RecordTypeImprint,
	}
}

func TestCompileManifest_MissingManifest(t *testing.T) {
	dir := t.TempDir()
	h := NewHasher(dir)

	records := []*Record{
		makeTestRecord("prov-2026-aaa00001", "implemented"),
		makeTestRecord("prov-2026-aaa00002", "open"),
	}

	changed, err := h.CompileManifest(records)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true when manifest did not exist")
	}

	if !h.ManifestExists() {
		t.Fatal("manifest file should exist after compile")
	}

	m, err := h.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest after compile: %v", err)
	}
	if len(m.Records) != 2 {
		t.Fatalf("expected 2 records in manifest, got %d", len(m.Records))
	}
	if m.FullGraphHash == "" || m.ActiveSubsetHash == "" {
		t.Fatal("graph hashes should be populated")
	}
}

func TestCompileManifest_Idempotent(t *testing.T) {
	dir := t.TempDir()
	h := NewHasher(dir)

	records := []*Record{
		makeTestRecord("prov-2026-bbb00001", "implemented"),
	}

	// First compile — should write
	changed, err := h.CompileManifest(records)
	if err != nil {
		t.Fatalf("first compile: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true on first compile")
	}

	// Record mtime before second compile
	manifestPath := filepath.Join(dir, ".linespec", "hash_manifest.json")
	info1, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatalf("stat manifest: %v", err)
	}

	// Second compile with same records — should be a no-op
	changed, err = h.CompileManifest(records)
	if err != nil {
		t.Fatalf("second compile: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false when manifest is already up to date")
	}

	info2, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatalf("stat manifest after second compile: %v", err)
	}
	if info2.ModTime() != info1.ModTime() {
		t.Fatal("manifest file should not have been rewritten on idempotent compile")
	}
}

func TestCompileManifest_StaleEntry(t *testing.T) {
	dir := t.TempDir()
	h := NewHasher(dir)

	r := makeTestRecord("prov-2026-ccc00001", "implemented")
	records := []*Record{r}

	// Compile once to establish baseline
	if _, err := h.CompileManifest(records); err != nil {
		t.Fatalf("initial compile: %v", err)
	}

	// Mutate the record so its hash changes
	r.Title = "Modified title"

	changed, err := h.CompileManifest(records)
	if err != nil {
		t.Fatalf("compile after mutation: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true after record was mutated")
	}

	// Verify stored hash now matches the mutated record
	stored, current, ok, err := h.VerifyRecord(r)
	if err != nil {
		t.Fatalf("VerifyRecord: %v", err)
	}
	if !ok {
		t.Fatal("record should be in manifest after compile")
	}
	if stored != current {
		t.Fatalf("stored hash %s does not match current %s after compile", stored, current)
	}
}
