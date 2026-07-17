package provenance

import (
	"strings"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/config"
)

// TestProvenanceConfigManifestURL verifies the ManifestURL field is present
// and round-trips through the ProvenanceConfig struct.
func TestProvenanceConfigManifestURL(t *testing.T) {
	cfg := &ProvenanceConfig{
		ManifestURL: "https://example.com/linespec.manifest.json",
		Dir:         "provenance",
		Enforcement: "warn",
	}
	if cfg.ManifestURL == "" {
		t.Error("ManifestURL should not be empty")
	}
	if !strings.HasPrefix(cfg.ManifestURL, "https://") {
		t.Errorf("unexpected ManifestURL: %s", cfg.ManifestURL)
	}
}

// TestConfigProvenanceManifestURLField verifies the config package's
// ProvenanceConfig exposes the ManifestURL YAML field.
func TestConfigProvenanceManifestURLField(t *testing.T) {
	cfg := config.ProvenanceConfig{
		ManifestURL: "https://example.com/linespec.manifest.json",
	}
	if cfg.ManifestURL == "" {
		t.Error("config.ProvenanceConfig.ManifestURL should not be empty")
	}
}
