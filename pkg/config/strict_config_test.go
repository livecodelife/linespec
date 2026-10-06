package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Config mistakes must be reported, not ignored (prov-2026-79b59b40).
// LoadConfigFile used a non-strict yaml.Unmarshal, so a misspelled key was
// dropped and the default applied, and an unknown database type only reached a
// Debug log in the runner. These tests drive LoadConfigFile with real file
// content and assert on the error a user would see.

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".linespec.yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestStrictConfig_UnknownKeysRejected(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantKey string
	}{
		{
			name: "unknown top-level key",
			content: `
service:
  name: my-api
  port: 3000
infrastucture:
  database: false
`,
			wantKey: "infrastucture",
		},
		{
			name: "misspelled nested service key",
			content: `
service:
  name: my-api
  port: 3000
  start_comand: "npm start"
`,
			wantKey: "start_comand",
		},
		{
			name: "misspelled nested database key",
			content: `
service:
  name: my-api
  port: 3000
infrastructure:
  database: true
database:
  type: mysql
  database: app
  username: u
  password: p
  host: real-db
  init_scrpt: schema.sql
`,
			wantKey: "init_scrpt",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfigFile(writeConfigFile(t, tt.content))
			if err == nil {
				t.Fatalf("bug: LoadConfigFile accepted an unknown key %q silently", tt.wantKey)
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("error should name the offending key %q, got: %v", tt.wantKey, err)
			}
		})
	}
}

func TestStrictConfig_UnknownDatabaseTypeRejected(t *testing.T) {
	content := `
service:
  name: my-api
  port: 3000
infrastructure:
  database: true
database:
  type: postgres
  database: app
  username: u
  password: p
  host: real-db
`
	_, err := LoadConfigFile(writeConfigFile(t, content))
	if err == nil {
		t.Fatal("bug: database type \"postgres\" (not a supported type) loaded without error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "postgres") {
		t.Errorf("error should name the bad type, got: %v", err)
	}
	for _, supported := range []string{"mysql", "postgresql", "mongodb", "oracle"} {
		if !strings.Contains(msg, supported) {
			t.Errorf("error should list supported type %q, got: %v", supported, err)
		}
	}
}

func TestStrictConfig_ValidConfigStillLoads(t *testing.T) {
	content := `
service:
  name: my-api
  port: 3000
infrastructure:
  database: true
database:
  type: postgresql
  database: app
  username: u
  password: p
  host: real-db
provenance:
  embedding:
    provider: voyage
    index_model: voyage-4-large
`
	if _, err := LoadConfigFile(writeConfigFile(t, content)); err != nil {
		t.Fatalf("valid config (including provenance section) must load: %v", err)
	}
}

// Every shipped example must survive strictness. The strict decode below is
// done directly so the test also fails today if an example carries a key that
// strict loading would reject — the pre-flight the record calls for.
func TestStrictConfig_ExamplesStillLoad(t *testing.T) {
	var paths []string
	err := filepath.Walk(filepath.Join("..", "..", "examples"), func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (info.Name() == ".linespec.yml" || info.Name() == ".linespec.yaml") {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk examples: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no examples/**/.linespec.yml found; the guard would be vacuous")
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			if _, err := LoadConfigFile(p); err != nil {
				t.Errorf("LoadConfigFile: %v", err)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			dec := yaml.NewDecoder(bytes.NewReader(data))
			dec.KnownFields(true)
			var cfg LineSpecConfig
			if err := dec.Decode(&cfg); err != nil {
				t.Errorf("example carries a key strict loading would reject: %v", err)
			}
		})
	}
}
