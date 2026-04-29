package phoenix

import (
	"crypto/sha1" //nolint:gosec
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/livecodelife/linespec/pkg/config"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func specContent(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write spec file: %v", err)
	}
	return path
}

func loadIndex(t *testing.T, path string) evidenceIndex {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read evidence.json: %v", err)
	}
	var idx evidenceIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatalf("parse evidence.json: %v", err)
	}
	return idx
}

func sha1Hex(data []byte) string {
	h := sha1.New() //nolint:gosec
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func TestEmitEvidence_HappyPath(t *testing.T) {
	dir := t.TempDir()
	specA := specContent(t, dir, "a.linespec", "TEST a\n")
	specB := specContent(t, dir, "b.linespec", "TEST b\n")

	cfg := &config.PhoenixConfig{
		Root: dir,
		IUMappings: []config.PhoenixIUMapping{
			{IUID: "iu-001", Spec: specA},
			{IUID: "iu-002", Spec: specB},
		},
	}
	results := []SpecResult{
		{Path: specA, Passed: true},
		{Path: specB, Passed: false},
	}

	if err := EmitEvidence(cfg, results, dir); err != nil {
		t.Fatalf("EmitEvidence: %v", err)
	}

	evidencePath := filepath.Join(dir, ".phoenix", "graphs", "evidence.json")
	idx := loadIndex(t, evidencePath)

	if len(idx.Records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(idx.Records))
	}

	pass := idx.Records[0]
	if pass.IUID != "iu-001" {
		t.Errorf("expected iu-001, got %q", pass.IUID)
	}
	if pass.Status != "PASS" {
		t.Errorf("expected PASS, got %q", pass.Status)
	}
	if pass.Kind != evidenceKind {
		t.Errorf("expected kind %q, got %q", evidenceKind, pass.Kind)
	}
	if !uuidRe.MatchString(pass.EvidenceID) {
		t.Errorf("evidence_id %q is not a valid UUID v4", pass.EvidenceID)
	}
	if len(pass.CanonIDs) != 0 {
		t.Errorf("canon_ids must be empty, got %v", pass.CanonIDs)
	}
	expectedHash := sha1Hex([]byte("TEST a\n"))
	if pass.ArtifactHash != expectedHash {
		t.Errorf("artifact_hash: expected %q, got %q", expectedHash, pass.ArtifactHash)
	}
	if pass.Timestamp == "" {
		t.Error("timestamp must not be empty")
	}

	fail := idx.Records[1]
	if fail.Status != "FAIL" {
		t.Errorf("expected FAIL, got %q", fail.Status)
	}
}

func TestEmitEvidence_AppendToExisting(t *testing.T) {
	dir := t.TempDir()
	specA := specContent(t, dir, "a.linespec", "TEST a\n")

	cfg := &config.PhoenixConfig{
		Root:       dir,
		IUMappings: []config.PhoenixIUMapping{{IUID: "iu-001", Spec: specA}},
	}
	results := []SpecResult{{Path: specA, Passed: true}}

	// First run
	if err := EmitEvidence(cfg, results, dir); err != nil {
		t.Fatalf("first EmitEvidence: %v", err)
	}
	// Second run
	if err := EmitEvidence(cfg, results, dir); err != nil {
		t.Fatalf("second EmitEvidence: %v", err)
	}

	idx := loadIndex(t, filepath.Join(dir, ".phoenix", "graphs", "evidence.json"))
	if len(idx.Records) != 2 {
		t.Errorf("expected 2 records after two runs, got %d", len(idx.Records))
	}
}

func TestEmitEvidence_UnmappedSpecSkipped(t *testing.T) {
	dir := t.TempDir()
	specA := specContent(t, dir, "a.linespec", "TEST a\n")
	specB := specContent(t, dir, "b.linespec", "TEST b\n")

	// Only specA is mapped; specB ran but is not in IU mappings.
	cfg := &config.PhoenixConfig{
		Root:       dir,
		IUMappings: []config.PhoenixIUMapping{{IUID: "iu-001", Spec: specA}},
	}
	results := []SpecResult{
		{Path: specA, Passed: true},
		{Path: specB, Passed: true},
	}

	if err := EmitEvidence(cfg, results, dir); err != nil {
		t.Fatalf("EmitEvidence: %v", err)
	}

	idx := loadIndex(t, filepath.Join(dir, ".phoenix", "graphs", "evidence.json"))
	if len(idx.Records) != 1 {
		t.Errorf("expected 1 record (only mapped spec), got %d", len(idx.Records))
	}
	if idx.Records[0].IUID != "iu-001" {
		t.Errorf("expected iu-001, got %q", idx.Records[0].IUID)
	}
}

func TestEmitEvidence_MappedSpecDidNotRun(t *testing.T) {
	dir := t.TempDir()
	specA := specContent(t, dir, "a.linespec", "TEST a\n")

	cfg := &config.PhoenixConfig{
		Root:       dir,
		IUMappings: []config.PhoenixIUMapping{{IUID: "iu-001", Spec: specA}},
	}
	// specA is mapped but not in results (e.g. test was interrupted before it ran).
	results := []SpecResult{}

	if err := EmitEvidence(cfg, results, dir); err != nil {
		t.Fatalf("EmitEvidence: %v", err)
	}

	evidencePath := filepath.Join(dir, ".phoenix", "graphs", "evidence.json")
	if _, err := os.Stat(evidencePath); !os.IsNotExist(err) {
		t.Error("evidence.json should not be created when no mapped specs ran")
	}
}

func TestEmitEvidence_NilConfig(t *testing.T) {
	dir := t.TempDir()
	if err := EmitEvidence(nil, nil, dir); err != nil {
		t.Errorf("nil config should return nil, got %v", err)
	}
}

func TestEmitEvidence_EmptyMappings(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.PhoenixConfig{Root: dir, IUMappings: nil}
	if err := EmitEvidence(cfg, nil, dir); err != nil {
		t.Errorf("empty mappings should return nil, got %v", err)
	}
}

func TestEmitEvidence_UnwritableRoot(t *testing.T) {
	dir := t.TempDir()
	specA := specContent(t, dir, "a.linespec", "TEST a\n")

	// Point Phoenix root at a file path so MkdirAll fails.
	badRoot := filepath.Join(dir, "not-a-dir.txt")
	if err := os.WriteFile(badRoot, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.PhoenixConfig{
		Root:       badRoot,
		IUMappings: []config.PhoenixIUMapping{{IUID: "iu-001", Spec: specA}},
	}
	results := []SpecResult{{Path: specA, Passed: true}}

	err := EmitEvidence(cfg, results, dir)
	if err == nil {
		t.Error("expected an error when Phoenix root is unwritable, got nil")
	}
}

func TestNewUUID_Version4(t *testing.T) {
	seen := make(map[string]bool)
	for range 20 {
		id, err := newUUID()
		if err != nil {
			t.Fatalf("newUUID: %v", err)
		}
		if !uuidRe.MatchString(id) {
			t.Errorf("UUID %q does not match v4 format", id)
		}
		if seen[id] {
			t.Errorf("duplicate UUID: %q", id)
		}
		seen[id] = true
	}
}
