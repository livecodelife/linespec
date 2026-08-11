package provenance

import (
	"os"
	"path/filepath"
	"testing"
)

// These tests reproduce GitHub issue #163: .linespec/hash_manifest.json
// conflicts on essentially every rebase in a repo where multiple branches
// seal records independently, and nothing detects a manifest entry that a
// bad merge silently dropped.
//
// TestIssue163_MissingManifestEntryIsDetected reproduces the "nothing
// detects a corrupt manifest" half of the issue: validateImmutability
// (pkg/provenance/linter.go) treats an implemented record with no manifest
// entry as "pre-dates the hash manifest" and returns without error. That is
// indistinguishable from a record a bad rebase/merge silently dropped from
// the manifest. Once the manifest is non-empty, an implemented record
// missing from it should be flagged instead of silently passing.
func TestIssue163_MissingManifestEntryIsDetected(t *testing.T) {
	dir := t.TempDir()
	h := NewHasher(dir)

	sealed := makeTestRecord("prov-2026-bbb00001", "implemented")
	dropped := makeTestRecord("prov-2026-bbb00002", "implemented")

	// Seed a manifest that only knows about "sealed" — as if "dropped"'s
	// entry was lost by a bad rebase/merge of hash_manifest.json.
	if err := h.SealRecord(sealed, []*Record{sealed}); err != nil {
		t.Fatalf("SealRecord: %v", err)
	}

	loader := &Loader{Records: []*Record{sealed, dropped}}
	linter := NewLinter(loader, "warn")
	linter.Hasher = h

	result := linter.LintAll()

	found := false
	for _, issue := range result.Issues {
		if issue.RecordID == dropped.ID && issue.Field == "integrity" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected lint to flag an implemented record missing from a non-empty hash manifest, but it passed silently")
	}
}

// TestIssue163_ManifestPathIgnoresConfigDir reproduces the "-c ignores the
// manifest path" half of the issue: NewCommandsWithEmbedder always builds
// the Hasher from repoRoot (pkg/provenance/commands.go), never from the
// resolved provenance config's own location, so `compile -c
// packages/foo/.linespec.yml` and a root-level compile both target the same
// <repoRoot>/.linespec/hash_manifest.json — the package config's manifest
// silently clobbers the root manifest instead of getting its own.
func TestIssue163_ManifestPathIgnoresConfigDir(t *testing.T) {
	repoRoot := t.TempDir()

	rootProvDir := filepath.Join(repoRoot, "provenance")
	pkgProvDir := filepath.Join(repoRoot, "packages", "foo", "provenance")
	for _, d := range []string{rootProvDir, pkgProvDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("MkdirAll %s: %v", d, err)
		}
	}

	rootCfg := &ProvenanceConfig{Dir: rootProvDir, Enforcement: "warn"}
	rootCmds, err := NewCommandsWithEmbedder(rootCfg, repoRoot, os.Stdout, false, nil)
	if err != nil {
		t.Fatalf("NewCommandsWithEmbedder (root): %v", err)
	}

	pkgCfg := &ProvenanceConfig{Dir: pkgProvDir, Enforcement: "warn"}
	pkgCmds, err := NewCommandsWithEmbedder(pkgCfg, repoRoot, os.Stdout, false, nil)
	if err != nil {
		t.Fatalf("NewCommandsWithEmbedder (packages/foo): %v", err)
	}

	rootManifest := rootCmds.Linter.Hasher.ManifestPath()
	pkgManifest := pkgCmds.Linter.Hasher.ManifestPath()

	if rootManifest == pkgManifest {
		t.Fatalf("expected the packages/foo config to use its own hash manifest distinct from the root manifest, both resolved to %s", rootManifest)
	}
}
