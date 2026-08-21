package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeMinimalImplementedRecord writes a minimal implemented provenance record
// (prov-YYYY-XXXXXXXX.yml) into dir so record-discovery tests have something
// concrete to find.
func writeMinimalImplementedRecord(t *testing.T, dir, id string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	content := fmt.Sprintf(`id: %s
title: placeholder record for discovery test
status: implemented
created_at: "2026-08-21"
author: test@example.com
intent: placeholder
constraints:
  - placeholder
affected_scope: []
type: blueprint
`, id)
	path := filepath.Join(dir, id+".yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestIndexHonorsConfigFlagAbsoluteDir reproduces issue #199: -c/--config's
// provenance.embedding block is honored (prov-2026-57aff9e1), but record
// discovery still isn't routed through the same config. loadProvenanceConfigFromFile
// (main_stable.go) resolves provenance.dir via filepath.Join(configDir, dir) even
// when dir is already absolute -- filepath.Join does not special-case an absolute
// second argument, so an absolute provenance.dir gets prefixed with the config
// file's own directory instead of used verbatim, producing a path that still looks
// absolute (so the later filepath.IsAbs guard in NewCommandsWithEmbedder doesn't
// catch it) but resolves to nothing. LoadFromDir treats a missing directory as zero
// records rather than an error, so Index() reports "already have embeddings"
// without ever having found the records the -c config's dir actually points at.
func TestIndexHonorsConfigFlagAbsoluteDir(t *testing.T) {
	scratchDir := t.TempDir() // where the -c config file lives
	recordsDir := t.TempDir() // absolute provenance.dir target -- deliberately not under scratchDir or the repo
	writeMinimalImplementedRecord(t, recordsDir, "prov-2026-aaaaaaaa")

	t.Chdir(scratchDir)

	server := newFakeOpenAIEmbeddingServer(t)
	content := fmt.Sprintf(`provenance:
  dir: %s
  embedding:
    provider: openai
    api_key: test-key
    base_url: %s/v1
`, recordsDir, server.URL)
	configPath := filepath.Join(scratchDir, "custom.linespec.yml")
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cmds := provCmds(configPath)

	if len(cmds.Loader.Records) != 1 {
		t.Fatalf("Loader.Records has %d record(s), want 1 -- record discovery did not follow the -c config's absolute provenance.dir (%s)", len(cmds.Loader.Records), recordsDir)
	}
}
