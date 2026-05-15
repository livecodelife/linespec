package provenance

import (
	"strings"
	"testing"
)

func makeRecord(id string, rtype RecordType, status Status) Record {
	return Record{ID: id, Type: rtype, Status: status}
}

// --- stripImprints ---

func TestStripImprints_RemovesImprintRecords(t *testing.T) {
	records := []Record{
		makeRecord("prov-2026-aaa00001", RecordTypeBlueprint, StatusOpen),
		makeRecord("prov-2026-aaa00002", RecordTypeImprint, StatusOpen),
		makeRecord("prov-2026-aaa00003", RecordTypeBrief, StatusOpen),
	}
	out := stripImprints(records)
	if len(out) != 2 {
		t.Fatalf("expected 2 records, got %d", len(out))
	}
	for _, r := range out {
		if r.Type == RecordTypeImprint {
			t.Errorf("imprint %s should have been stripped", r.ID)
		}
	}
}

// --- filterSuperseded ---

func TestFilterSuperseded_KeepsSupersedingRemovesSuperseded(t *testing.T) {
	records := []Record{
		{ID: "prov-2026-old00001", Type: RecordTypeBlueprint, Status: StatusSuperseded},
		{ID: "prov-2026-new00001", Type: RecordTypeBlueprint, Status: StatusOpen, Supersedes: "prov-2026-old00001"},
	}
	out := filterSuperseded(records)
	if len(out) != 1 {
		t.Fatalf("expected 1 record, got %d", len(out))
	}
	if out[0].ID != "prov-2026-new00001" {
		t.Errorf("expected superseding record to be retained")
	}
}

func TestFilterSuperseded_KeepsUnsupersededRecords(t *testing.T) {
	records := []Record{
		makeRecord("prov-2026-aaa00001", RecordTypeBlueprint, StatusOpen),
		makeRecord("prov-2026-aaa00002", RecordTypeBlueprint, StatusImplemented),
	}
	out := filterSuperseded(records)
	if len(out) != 2 {
		t.Fatalf("expected 2 records, got %d", len(out))
	}
}

// --- promoteBugs ---

func TestPromoteBugs_ChangesBugToBlueprint(t *testing.T) {
	records := []Record{
		makeRecord("prov-2026-bug00001", RecordTypeBug, StatusOpen),
		makeRecord("prov-2026-bp000001", RecordTypeBlueprint, StatusOpen),
	}
	out := promoteBugs(records)
	for _, r := range out {
		if r.Type == RecordTypeBug {
			t.Errorf("bug record %s should have been promoted to blueprint", r.ID)
		}
	}
}

// --- cleanDanglingRefs ---

func TestCleanDanglingRefs_RemovesRefsToFilteredRecords(t *testing.T) {
	records := []Record{
		{
			ID:         "prov-2026-aaa00001",
			Type:       RecordTypeBlueprint,
			Status:     StatusOpen,
			Supersedes: "prov-2026-gone0001",
			Implements: "prov-2026-gone0002",
			Related:    []string{"prov-2026-gone0003", "prov-2026-aaa00002"},
		},
		makeRecord("prov-2026-aaa00002", RecordTypeBlueprint, StatusOpen),
	}
	out := cleanDanglingRefs(records)
	r := out[0]
	if r.Supersedes != "" {
		t.Errorf("dangling Supersedes should be cleared, got %q", r.Supersedes)
	}
	if r.Implements != "" {
		t.Errorf("dangling Implements should be cleared, got %q", r.Implements)
	}
	if len(r.Related) != 1 || r.Related[0] != "prov-2026-aaa00002" {
		t.Errorf("only present related ref should be kept, got %v", r.Related)
	}
}

// --- resetStatus ---

func TestResetStatus_SetsOpenAndClearsSeal(t *testing.T) {
	records := []Record{
		{ID: "prov-2026-aaa00001", Status: StatusImplemented, SealedAtSHA: "abc123"},
		{ID: "prov-2026-aaa00002", Status: StatusSuperseded, SealedAtSHA: "def456"},
	}
	out := resetStatus(records)
	for _, r := range out {
		if r.Status != StatusOpen {
			t.Errorf("expected status open, got %q on %s", r.Status, r.ID)
		}
		if r.SealedAtSHA != "" {
			t.Errorf("expected sealed_at_sha cleared, got %q on %s", r.SealedAtSHA, r.ID)
		}
	}
}

// --- hashBytes / rootHash ---

func TestHashBytes_Deterministic(t *testing.T) {
	h1 := hashBytes([]byte("hello"))
	h2 := hashBytes([]byte("hello"))
	if h1 != h2 {
		t.Errorf("hashBytes not deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex SHA-256, got %d chars", len(h1))
	}
}

func TestRootHash_ConcatenatesInOrder(t *testing.T) {
	h1 := hashBytes([]byte("layer1"))
	h2 := hashBytes([]byte("layer2"))
	rh := rootHash([]string{h1, h2})
	expected := hashBytes([]byte(h1 + h2))
	if rh != expected {
		t.Errorf("rootHash mismatch: got %q, want %q", rh, expected)
	}
}

// --- nextVersion ---

func TestNextVersion_EmptyManifestReturnsV1(t *testing.T) {
	v := nextVersion(map[string]ManifestVersion{})
	if v != "v1" {
		t.Errorf("expected v1, got %q", v)
	}
}

func TestNextVersion_Increments(t *testing.T) {
	existing := map[string]ManifestVersion{
		"v1": {}, "v2": {}, "v3": {},
	}
	v := nextVersion(existing)
	if v != "v4" {
		t.Errorf("expected v4, got %q", v)
	}
}

// --- newManifestVersion ---

func TestNewManifestVersion_HashesAndOrderedRootHash(t *testing.T) {
	layers := map[string][]byte{
		"provenance": []byte("prov data"),
		"specs":      []byte("spec data"),
	}
	mv := newManifestVersion(layers)

	provHash := hashBytes([]byte("prov data"))
	specsHash := hashBytes([]byte("spec data"))
	expectedRoot := rootHash([]string{provHash, specsHash})

	if mv.Layers["provenance"].SHA256 != provHash {
		t.Errorf("provenance hash mismatch")
	}
	if mv.Layers["specs"].SHA256 != specsHash {
		t.Errorf("specs hash mismatch")
	}
	if mv.RootHash != expectedRoot {
		t.Errorf("root_hash mismatch: got %q, want %q", mv.RootHash, expectedRoot)
	}
	if mv.CreatedAt == "" {
		t.Error("created_at should be set")
	}
}

func TestNewManifestVersion_URLsEmptyByDefault(t *testing.T) {
	mv := newManifestVersion(map[string][]byte{"provenance": []byte("x")})
	if mv.Layers["provenance"].URL != "" {
		t.Errorf("URL should be empty by default")
	}
}

func TestNewManifestVersion_UnknownLayerExcludedFromRootHash(t *testing.T) {
	layers := map[string][]byte{
		"provenance": []byte("prov"),
		"unknown":    []byte("ignored"),
	}
	mv := newManifestVersion(layers)
	if _, ok := mv.Layers["unknown"]; ok {
		t.Error("unknown layer should not appear in manifest")
	}
	expected := rootHash([]string{hashBytes([]byte("prov"))})
	if mv.RootHash != expected {
		t.Errorf("root_hash should only cover known layers")
	}
}

// --- applyTransformPipeline (integration) ---

func TestApplyTransformPipeline_EndToEnd(t *testing.T) {
	records := []Record{
		{ID: "prov-2026-brief001", Type: RecordTypeBrief, Status: StatusOpen},
		{ID: "prov-2026-bp000001", Type: RecordTypeBlueprint, Status: StatusImplemented, SealedAtSHA: "abc"},
		{ID: "prov-2026-imp00001", Type: RecordTypeImprint, Status: StatusOpen, Implements: "prov-2026-bp000001"},
		{ID: "prov-2026-old00001", Type: RecordTypeBlueprint, Status: StatusSuperseded},
		{ID: "prov-2026-new00001", Type: RecordTypeBlueprint, Status: StatusOpen, Supersedes: "prov-2026-old00001"},
		{ID: "prov-2026-bug00001", Type: RecordTypeBug, Status: StatusOpen, Extends: "prov-2026-bp000001"},
	}
	out := applyTransformPipeline(records)

	ids := map[string]Record{}
	for _, r := range out {
		ids[r.ID] = r
	}

	if _, ok := ids["prov-2026-imp00001"]; ok {
		t.Error("imprint should be stripped")
	}
	if _, ok := ids["prov-2026-old00001"]; ok {
		t.Error("superseded record should be filtered")
	}
	if r, ok := ids["prov-2026-bug00001"]; !ok {
		t.Error("bug record should be retained")
	} else if r.Type != RecordTypeBlueprint {
		t.Errorf("bug should be promoted to blueprint, got %q", r.Type)
	}
	if r, ok := ids["prov-2026-bp000001"]; ok {
		if r.Status != StatusOpen {
			t.Errorf("status should be reset to open, got %q", r.Status)
		}
		if r.SealedAtSHA != "" {
			t.Error("sealed_at_sha should be cleared")
		}
	}
	if r, ok := ids["prov-2026-new00001"]; ok {
		if r.Supersedes != "" {
			t.Errorf("dangling Supersedes ref should be cleaned (target was filtered)")
		}
	} else {
		t.Error("superseding record should be retained")
	}
	if !strings.Contains(out[0].ID, "prov-") {
		t.Error("output should contain valid records")
	}
}
