package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestProvenanceConfigExcludePathsYAML(t *testing.T) {
	t.Run("parses exclude_paths from YAML", func(t *testing.T) {
		raw := `
provenance:
  enforcement: strict
  exclude_paths:
    - CHANGELOG.md
    - docs/
    - /.*_gen\.go$/
    - migrations/**
`
		var cfg LineSpecConfig
		if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}

		if cfg.Provenance == nil {
			t.Fatal("expected Provenance to be non-nil")
		}

		want := []string{
			"CHANGELOG.md",
			"docs/",
			`/.*_gen\.go$/`,
			"migrations/**",
		}

		if len(cfg.Provenance.ExcludePaths) != len(want) {
			t.Fatalf("ExcludePaths length: got %d, want %d", len(cfg.Provenance.ExcludePaths), len(want))
		}

		for i, v := range want {
			if cfg.Provenance.ExcludePaths[i] != v {
				t.Errorf("ExcludePaths[%d]: got %q, want %q", i, cfg.Provenance.ExcludePaths[i], v)
			}
		}
	})

	t.Run("empty exclude_paths is valid", func(t *testing.T) {
		raw := `
provenance:
  enforcement: warn
`
		var cfg LineSpecConfig
		if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}

		if cfg.Provenance == nil {
			t.Fatal("expected Provenance to be non-nil")
		}

		if len(cfg.Provenance.ExcludePaths) != 0 {
			t.Errorf("expected empty ExcludePaths, got %v", cfg.Provenance.ExcludePaths)
		}
	})

	t.Run("exclude_paths coexists with other provenance fields", func(t *testing.T) {
		raw := `
provenance:
  dir: records
  enforcement: strict
  commit_tag_required: true
  exclude_paths:
    - vendor/
    - "*.generated.go"
`
		var cfg LineSpecConfig
		if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}

		if cfg.Provenance.Dir != "records" {
			t.Errorf("Dir: got %q, want %q", cfg.Provenance.Dir, "records")
		}
		if !cfg.Provenance.CommitTagRequired {
			t.Error("expected CommitTagRequired to be true")
		}
		if len(cfg.Provenance.ExcludePaths) != 2 {
			t.Errorf("expected 2 ExcludePaths, got %d", len(cfg.Provenance.ExcludePaths))
		}
	})
}
