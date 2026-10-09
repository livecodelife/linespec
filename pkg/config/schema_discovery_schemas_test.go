package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Specifies prov-2026-059ba152: SchemaDiscoveryConfig.Schemas ([]string,
// yaml "schema_discovery.schemas"), defaulting to ["public"].

func loadSchemaCfg(t *testing.T, body string) *LineSpecConfig {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".linespec.yml"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

func TestSchemaDiscoverySchemasParsed(t *testing.T) {
	cfg := loadSchemaCfg(t, `
service:
  name: svc
  port: 3000
schema_discovery:
  mode: auto
  schemas:
    - cnp_global
    - cnp_ops_ca
`)
	want := []string{"cnp_global", "cnp_ops_ca"}
	if !reflect.DeepEqual(cfg.SchemaDiscovery.Schemas, want) {
		t.Fatalf("Schemas = %v, want %v (order must be preserved)", cfg.SchemaDiscovery.Schemas, want)
	}
}

func TestSchemaDiscoverySchemasDefaultsToPublic(t *testing.T) {
	cases := map[string]string{
		"no schema_discovery block": "service:\n  name: svc\n  port: 3000\n",
		"block without schemas":     "service:\n  name: svc\n  port: 3000\nschema_discovery:\n  mode: auto\n",
		"explicit empty list":       "service:\n  name: svc\n  port: 3000\nschema_discovery:\n  schemas: []\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := loadSchemaCfg(t, body)
			if !reflect.DeepEqual(cfg.SchemaDiscovery.Schemas, []string{"public"}) {
				t.Fatalf("Schemas = %v, want [public]", cfg.SchemaDiscovery.Schemas)
			}
		})
	}
}
