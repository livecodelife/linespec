package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/embeddings"
	"github.com/livecodelife/linespec/v3/pkg/provenance"
)

// newFakeOpenAIEmbeddingServer starts an httptest server that answers the
// OpenAI-compatible /v1/embeddings wire format, standing in for a local
// server (LM Studio, Ollama, vLLM, ...) reached via base_url. It returns a
// vector of the given width; callers that actually exercise storage use a
// width other than the Voyage-specific 2048 to prove the store no longer
// assumes it.
func newFakeOpenAIEmbeddingServer(t *testing.T, width int) *httptest.Server {
	t.Helper()
	vector := make([]float64, width)
	for i := range vector {
		vector[i] = 0.001
	}
	body, err := json.Marshal(map[string]any{
		"object": "list",
		"data": []map[string]any{
			{"object": "embedding", "embedding": vector, "index": 0},
		},
		"model": "text-embedding-3-small",
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server
}

// writeCustomConfig writes a .linespec.yml-shaped file (any name, passed via
// -c) declaring a valid provenance.embedding block pointed at server, and
// returns its path.
func writeCustomConfig(t *testing.T, dir string, server *httptest.Server) string {
	t.Helper()
	content := fmt.Sprintf(`provenance:
  dir: provenance
  embedding:
    provider: openai
    api_key: test-key
    base_url: %s/v1
`, server.URL)
	path := filepath.Join(dir, "custom.linespec.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// TestIndexHonorsConfigFlag verifies that `linespec provenance index -c <file>`
// (and the other provCmds-based subcommands sharing reloadConfigIfNeeded) build
// their embedder from the custom config file's provenance.embedding block,
// instead of silently discarding it and reporting "Embedding API not configured".
func TestIndexHonorsConfigFlag(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	server := newFakeOpenAIEmbeddingServer(t, 768)
	configPath := writeCustomConfig(t, dir, server)

	cmds := provCmds(configPath)

	if cmds.Embedder == nil || !cmds.Embedder.IsConfigured() {
		t.Fatalf("provCmds(%q).Embedder not configured; -c embedding block was not propagated", configPath)
	}

	// No records exist, so Index short-circuits to "already indexed" once past
	// the embedder-configured check — reaching that (rather than
	// errEmbeddingNotConfigured) is what confirms the -c flag took effect.
	if err := cmds.Index(provenance.IndexOptions{}); err != nil {
		t.Errorf("Index() with configured -c embedder returned error: %v", err)
	}
}

// TestIndexStoresNonVoyageWidth is the regression test for the storage-layer
// half of the base_url bug: reaching a local OpenAI-compatible embedder over
// base_url is not enough on its own if the store still rejects every vector
// that isn't the Voyage-specific 2048 wide. It indexes a real implemented
// record against a fake server emitting 768-dim vectors (the width
// nomic-embed-text-v1.5 actually produces) and verifies the vector is
// genuinely persisted — and that a fresh Store (a later process, as `index`,
// `search`, `audit`, and `complete` each open their own) recovers that width
// from the file itself, without being told, and still enforces it.
func TestIndexStoresNonVoyageWidth(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	const localWidth = 768
	server := newFakeOpenAIEmbeddingServer(t, localWidth)
	configPath := writeCustomConfig(t, dir, server)

	provDir := filepath.Join(dir, "provenance")
	if err := os.MkdirAll(provDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	const recordID = "prov-2026-e2eabcd1"
	recordYAML := fmt.Sprintf(`id: %s
title: 'test record for local embedder indexing'
status: implemented
created_at: "2026-08-21"
author: test@example.com
intent: test intent for indexing
type: blueprint
`, recordID)
	if err := os.WriteFile(filepath.Join(provDir, recordID+".yml"), []byte(recordYAML), 0o644); err != nil {
		t.Fatalf("WriteFile record: %v", err)
	}

	cmds := provCmds(configPath)
	if err := cmds.Index(provenance.IndexOptions{}); err != nil {
		t.Fatalf("Index() failed: %v", err)
	}

	store := embeddings.NewStore(dir)
	exists, err := store.Exists(recordID)
	if err != nil {
		t.Fatalf("Exists() failed: %v", err)
	}
	if !exists {
		t.Fatal("expected the record's 768-dim vector to be stored, but it was not found")
	}

	// A brand new Store value, exactly like the one the next `index`/`search`/
	// `audit`/`complete` invocation would construct, must recover the 768-dim
	// width from the file itself and still reject a mismatched (e.g.
	// Voyage-shaped 2048) write — proving the width wasn't silently coerced
	// back to the old hardcoded constant.
	fresh := embeddings.NewStore(dir)
	mismatched := make([]float32, 2048)
	err = fresh.Write(embeddings.RecordEmbedding{RecordID: "prov-2026-other0000", Vector: mismatched})
	if err == nil {
		t.Fatal("expected dimension mismatch error writing a 2048-dim vector into a 768-dim store")
	}
}

// TestSearchHonorsConfigFlag verifies the same reloadConfigIfNeeded fix for
// `linespec provenance search -c <file>`, which shares the bug via provCmds.
func TestSearchHonorsConfigFlag(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	server := newFakeOpenAIEmbeddingServer(t, 768)
	configPath := writeCustomConfig(t, dir, server)

	cmds := provCmds(configPath)

	if cmds.Embedder == nil || !cmds.Embedder.IsConfigured() {
		t.Fatalf("provCmds(%q).Embedder not configured; -c embedding block was not propagated", configPath)
	}

	// Search always calls GenerateQuery, so a nil/unconfigured embedder fails
	// immediately with errEmbeddingNotConfigured; reaching the fake server (and
	// an empty-store "no results" outcome) proves the -c embedder was used.
	if err := cmds.Search(provenance.SearchOptions{Query: "test query", Limit: 5}); err != nil {
		t.Errorf("Search() with configured -c embedder returned error: %v", err)
	}
}

// TestConfigFlagRejectsUnknownEmbeddingKey exercises the CLI-visible outcome
// of an unknown key under provenance.embedding reached via -c/--config: it
// must not be silently swallowed such that unrelated provenance settings
// (here, `dir`) are discarded too, and it must not leave cfg.Embedding
// looking "configured" with a zero-value provider (which would fail
// embeddings.NewClient or, worse, silently talk to the wrong endpoint).
func TestConfigFlagRejectsUnknownEmbeddingKey(t *testing.T) {
	dir := t.TempDir()
	content := `provenance:
  dir: custom-provenance-dir
  enforcement: strict
  embedding:
    provider: openai
    api_key: test-key
    bogus_field: true
`
	configPath := filepath.Join(dir, "custom.linespec.yml")
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := loadProvenanceConfigFromFile(configPath)

	if cfg.Embedding != nil {
		t.Errorf("cfg.Embedding = %+v, want nil: an unknown embedding key must not produce a falsely-configured embedder", cfg.Embedding)
	}
	wantDir := filepath.Join(dir, "custom-provenance-dir")
	if cfg.Dir != wantDir {
		t.Errorf("cfg.Dir = %q, want %q: an embedding-key typo must not discard the rest of the provenance: block", cfg.Dir, wantDir)
	}
	if cfg.Enforcement != "strict" {
		t.Errorf("cfg.Enforcement = %q, want %q: an embedding-key typo must not discard the rest of the provenance: block", cfg.Enforcement, "strict")
	}
}
