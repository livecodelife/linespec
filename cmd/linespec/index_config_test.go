package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/provenance"
)

// newFakeOpenAIEmbeddingServer starts an httptest server that answers the
// OpenAI-compatible /v1/embeddings wire format, standing in for a local
// server (LM Studio, Ollama, vLLM, ...) reached via base_url. It returns a
// 2048-dim vector to match the store's fixed embedding dimension.
func newFakeOpenAIEmbeddingServer(t *testing.T) *httptest.Server {
	t.Helper()
	vector := make([]float64, 2048)
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

	server := newFakeOpenAIEmbeddingServer(t)
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

// TestSearchHonorsConfigFlag verifies the same reloadConfigIfNeeded fix for
// `linespec provenance search -c <file>`, which shares the bug via provCmds.
func TestSearchHonorsConfigFlag(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	server := newFakeOpenAIEmbeddingServer(t)
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
