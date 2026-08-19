package main

import "testing"

// TestIndexHonorsConfigFlag verifies that `linespec provenance index -c <file>`
// (and the other provCmds-based subcommands sharing reloadConfigIfNeeded) build
// their embedder from the custom config file's provenance.embedding block,
// instead of silently discarding it and reporting "Embedding API not configured".
func TestIndexHonorsConfigFlag(t *testing.T) {
	t.Skip("placeholder: implemented alongside reloadConfigIfNeeded embedder fix (prov-2026-57aff9e1)")
}

// TestSearchHonorsConfigFlag verifies the same reloadConfigIfNeeded fix for
// `linespec provenance search -c <file>`, which shares the bug via provCmds.
func TestSearchHonorsConfigFlag(t *testing.T) {
	t.Skip("placeholder: implemented alongside reloadConfigIfNeeded embedder fix (prov-2026-57aff9e1)")
}
