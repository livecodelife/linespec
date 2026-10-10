package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Specifies prov-2026-ad21b28e: SchemaDiscoveryConfig.Validate (string, yaml
// "schema_discovery.validate"), one of off|warn|error, default "off".

func sdvLoad(body string) (*LineSpecConfig, error) {
	dir, err := os.MkdirTemp("", "sdv")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, ".linespec.yml"), []byte(body), 0644); err != nil {
		return nil, err
	}
	return LoadConfig(dir)
}

const sdvBase = "service:\n  name: svc\n  port: 3000\n"

func TestSchemaDiscoveryValidateAcceptsOffWarnError(t *testing.T) {
	for _, v := range []string{"off", "warn", "error"} {
		t.Run(v, func(t *testing.T) {
			cfg, err := sdvLoad(sdvBase + "schema_discovery:\n  mode: auto\n  validate: " + v + "\n")
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.SchemaDiscovery.Validate != v {
				t.Fatalf("Validate = %q, want %q", cfg.SchemaDiscovery.Validate, v)
			}
		})
	}
}

func TestSchemaDiscoveryValidateDefaultsToOff(t *testing.T) {
	cases := map[string]string{
		"no schema_discovery block": sdvBase,
		"block without validate":    sdvBase + "schema_discovery:\n  mode: auto\n",
		"explicit empty":            sdvBase + "schema_discovery:\n  validate: \"\"\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, err := sdvLoad(body)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.SchemaDiscovery.Validate != "off" {
				t.Fatalf("Validate = %q, want \"off\"", cfg.SchemaDiscovery.Validate)
			}
		})
	}
}

func TestSchemaDiscoveryValidateInvalidValueIsConfigError(t *testing.T) {
	for _, v := range []string{"strict", "true", "on", "Warn", "ERROR"} {
		t.Run(v, func(t *testing.T) {
			_, err := sdvLoad(sdvBase + "schema_discovery:\n  validate: " + v + "\n")
			if err == nil {
				t.Fatalf("validate: %s must be a config error at load, got nil", v)
			}
			if !strings.Contains(err.Error(), "validate") {
				t.Fatalf("error must name the key \"validate\": %v", err)
			}
		})
	}
}

func TestSchemaDiscoveryValidateUnknownSiblingKeyStillRejected(t *testing.T) {
	_, err := sdvLoad(sdvBase + "schema_discovery:\n  validate: warn\n  validat: warn\n")
	if err == nil {
		t.Fatal("unknown sibling key under schema_discovery must still be rejected (KnownFields)")
	}
	if !strings.Contains(err.Error(), "validat") {
		t.Fatalf("error should name the unknown key: %v", err)
	}
}
