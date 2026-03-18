package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigFromEnvVar(t *testing.T) {
	// Create temp config file
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "custom-config.yml")
	configContent := `
service:
  name: test-service
  port: 3000
`
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	// Set environment variable
	os.Setenv("LINESPEC_CONFIG", configFile)
	defer os.Unsetenv("LINESPEC_CONFIG")

	// Load config
	cfg, err := LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Service.Name != "test-service" {
		t.Errorf("Expected service name 'test-service', got '%s'", cfg.Service.Name)
	}
	if cfg.Service.Port != 3000 {
		t.Errorf("Expected port 3000, got %d", cfg.Service.Port)
	}
}

func TestLoadConfigWithYAMLExtension(t *testing.T) {
	// Create temp directory with .linespec.yaml
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, ".linespec.yaml")
	configContent := `
service:
  name: yaml-service
  port: 4000
`
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	// Load config
	cfg, err := LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Service.Name != "yaml-service" {
		t.Errorf("Expected service name 'yaml-service', got '%s'", cfg.Service.Name)
	}
}

func TestSchemaDiscoveryDefaults(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, ".linespec.yml")
	configContent := `
service:
  name: test-service
  port: 3000
`
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	cfg, err := LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.SchemaDiscovery == nil {
		t.Fatal("Expected SchemaDiscovery to be initialized")
	}
	if cfg.SchemaDiscovery.Mode != "auto" {
		t.Errorf("Expected default mode 'auto', got '%s'", cfg.SchemaDiscovery.Mode)
	}
}

func TestPayloadDefaults(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, ".linespec.yml")
	configContent := `
service:
  name: test-service
  port: 3000
`
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	cfg, err := LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Payload == nil {
		t.Fatal("Expected Payload to be initialized")
	}
	if cfg.Payload.Directory != "payloads" {
		t.Errorf("Expected default directory 'payloads', got '%s'", cfg.Payload.Directory)
	}
	if cfg.Payload.StatusField != "status" {
		t.Errorf("Expected default status field 'status', got '%s'", cfg.Payload.StatusField)
	}
	if len(cfg.Payload.SupportedFormats) != 3 {
		t.Errorf("Expected 3 supported formats, got %d", len(cfg.Payload.SupportedFormats))
	}
}

func TestSchemaDiscoveryCustomConfig(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, ".linespec.yml")
	configContent := `
service:
  name: test-service
  port: 3000
schema_discovery:
  mode: static
  tables:
    - users
    - todos
  exclude_tables:
    - migrations
`
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	cfg, err := LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.SchemaDiscovery.Mode != "static" {
		t.Errorf("Expected mode 'static', got '%s'", cfg.SchemaDiscovery.Mode)
	}
	if len(cfg.SchemaDiscovery.Tables) != 2 {
		t.Errorf("Expected 2 tables, got %d", len(cfg.SchemaDiscovery.Tables))
	}
}

func TestPayloadCustomConfig(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, ".linespec.yml")
	configContent := `
service:
  name: test-service
  port: 3000
payload:
  directory: custom_payloads
  status_field: code
  supported_formats:
    - json
    - xml
`
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	cfg, err := LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Payload.Directory != "custom_payloads" {
		t.Errorf("Expected directory 'custom_payloads', got '%s'", cfg.Payload.Directory)
	}
	if cfg.Payload.StatusField != "code" {
		t.Errorf("Expected status field 'code', got '%s'", cfg.Payload.StatusField)
	}
	if len(cfg.Payload.SupportedFormats) != 2 {
		t.Errorf("Expected 2 supported formats, got %d", len(cfg.Payload.SupportedFormats))
	}
}
