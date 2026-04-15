package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseConfigRequiresExplicitCredentials(t *testing.T) {
	// Configs without explicit credentials must fail with a clear error
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "missing database name",
			content: `
service:
  name: my-api
  port: 3000
infrastructure:
  database: true
database:
  type: mysql
  username: myuser
  password: mypass
`,
			wantErr: ".database is required",
		},
		{
			name: "missing username",
			content: `
service:
  name: my-api
  port: 3000
infrastructure:
  database: true
database:
  type: mysql
  database: mydb
  password: mypass
`,
			wantErr: ".username is required",
		},
		{
			name: "missing password",
			content: `
service:
  name: my-api
  port: 3000
infrastructure:
  database: true
database:
  type: mysql
  database: mydb
  username: myuser
`,
			wantErr: ".password is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			configPath := filepath.Join(tempDir, ".linespec.yml")
			if err := os.WriteFile(configPath, []byte(tc.content), 0644); err != nil {
				t.Fatalf("Failed to write test config: %v", err)
			}
			_, err := LoadConfigFile(configPath)
			if err == nil {
				t.Fatal("Expected error for missing credentials, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestDatabaseConfigDefaults(t *testing.T) {
	// Create a temporary config file with explicit credentials
	tempDir := t.TempDir()
	configContent := `
service:
  name: my-api
  port: 3000
  framework: rails
infrastructure:
  database: true
database:
  type: mysql
  database: my_db
  username: my_user
  password: my_pass
`
	configPath := filepath.Join(tempDir, ".linespec.yml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	config, err := LoadConfigFile(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFile() failed: %v", err)
	}

	if config.Database == nil {
		t.Fatal("Database config should not be nil")
	}

	// Explicit credentials are preserved unchanged
	if config.Database.Database != "my_db" {
		t.Errorf("Database.Database = %q, expected my_db", config.Database.Database)
	}
	if config.Database.Username != "my_user" {
		t.Errorf("Database.Username = %q, expected my_user", config.Database.Username)
	}
	if config.Database.Password != "my_pass" {
		t.Errorf("Database.Password = %q, expected my_pass", config.Database.Password)
	}

	// Image and port are still inferred from type
	if config.Database.Image != "mysql:8.4" {
		t.Errorf("Database.Image = %q, expected mysql:8.4", config.Database.Image)
	}
	if config.Database.Port != 3306 {
		t.Errorf("Database.Port = %d, expected 3306", config.Database.Port)
	}
	// Host defaults to "db"
	if config.Database.Host != "db" {
		t.Errorf("Database.Host = %q, expected db", config.Database.Host)
	}
}

func TestDatabaseConfigPostgreSQLDefaults(t *testing.T) {
	// Explicit credentials are required; image and port are still inferred from type
	tempDir := t.TempDir()
	configContent := `
service:
  name: todo-service
  port: 5432
  framework: fastapi
infrastructure:
  database: true
database:
  type: postgresql
  database: todo_db
  username: todo_user
  password: todo_pass
`
	configPath := filepath.Join(tempDir, ".linespec.yml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	config, err := LoadConfigFile(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFile() failed: %v", err)
	}

	if config.Database.Database != "todo_db" {
		t.Errorf("Database.Database = %q, expected todo_db", config.Database.Database)
	}
	if config.Database.Image != "postgres:16-alpine" {
		t.Errorf("Database.Image = %q, expected postgres:16-alpine", config.Database.Image)
	}
	if config.Database.Port != 5432 {
		t.Errorf("Database.Port = %d, expected 5432", config.Database.Port)
	}
}

func TestDatabaseConfigExplicitValues(t *testing.T) {
	// Create a temporary config file with explicit database values
	tempDir := t.TempDir()
	configContent := `
service:
  name: my-api
  port: 3000
  framework: rails
infrastructure:
  database: true
database:
  type: mysql
  database: custom_db
  username: custom_user
  password: custom_pass
  host: custom-host
  port: 3307
  image: mysql:8.0
`
	configPath := filepath.Join(tempDir, ".linespec.yml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Load and apply defaults
	config, err := LoadConfigFile(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFile() failed: %v", err)
	}

	// Verify explicit values are preserved (not overwritten by defaults)
	if config.Database.Database != "custom_db" {
		t.Errorf("Database.Database = %q, expected custom_db", config.Database.Database)
	}

	if config.Database.Username != "custom_user" {
		t.Errorf("Database.Username = %q, expected custom_user", config.Database.Username)
	}

	if config.Database.Password != "custom_pass" {
		t.Errorf("Database.Password = %q, expected custom_pass", config.Database.Password)
	}

	if config.Database.Host != "custom-host" {
		t.Errorf("Database.Host = %q, expected custom-host", config.Database.Host)
	}

	if config.Database.Port != 3307 {
		t.Errorf("Database.Port = %d, expected 3307", config.Database.Port)
	}

	if config.Database.Image != "mysql:8.0" {
		t.Errorf("Database.Image = %q, expected mysql:8.0", config.Database.Image)
	}
}

func TestContainerNamingDefaults(t *testing.T) {
	// Create a temporary config file
	tempDir := t.TempDir()
	configContent := `
service:
  name: my-service
  port: 3000
infrastructure:
  database: true
database:
  type: mysql
  database: my_db
  username: my_user
  password: my_pass
`
	configPath := filepath.Join(tempDir, ".linespec.yml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Load and apply defaults
	config, err := LoadConfigFile(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFile() failed: %v", err)
	}

	// Verify container naming defaults
	if config.ContainerNaming == nil {
		t.Fatal("ContainerNaming should not be nil")
	}

	if config.ContainerNaming.DatabaseContainer != "linespec-shared-db" {
		t.Errorf("DatabaseContainer = %q, expected linespec-shared-db", config.ContainerNaming.DatabaseContainer)
	}

	if config.ContainerNaming.NetworkName != "linespec-shared-net" {
		t.Errorf("NetworkName = %q, expected linespec-shared-net", config.ContainerNaming.NetworkName)
	}

	if config.ContainerNaming.NetworkAlias != "real-db" {
		t.Errorf("NetworkAlias = %q, expected real-db", config.ContainerNaming.NetworkAlias)
	}

	if config.ContainerNaming.MigrateContainer != "linespec-migrate-{{ .ServiceName }}" {
		t.Errorf("MigrateContainer = %q, expected linespec-migrate-{{ .ServiceName }}", config.ContainerNaming.MigrateContainer)
	}

	if config.ContainerNaming.ProjectMountPath != "/app/project" {
		t.Errorf("ProjectMountPath = %q, expected /app/project", config.ContainerNaming.ProjectMountPath)
	}

	if config.ContainerNaming.RegistryMountPath != "/app/registry" {
		t.Errorf("RegistryMountPath = %q, expected /app/registry", config.ContainerNaming.RegistryMountPath)
	}
}

func TestContainerNamingCustomValues(t *testing.T) {
	// Create a temporary config file with custom container naming
	tempDir := t.TempDir()
	configContent := `
service:
  name: my-service
  port: 3000
infrastructure:
  database: true
database:
  type: mysql
  database: my_db
  username: my_user
  password: my_pass
container_naming:
  database_container: my-custom-db
  network_name: my-custom-net
  network_alias: db-alias
  migrate_container: my-migrate-
  project_mount_path: /custom/project
  registry_mount_path: /custom/registry
`
	configPath := filepath.Join(tempDir, ".linespec.yml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Load and apply defaults
	config, err := LoadConfigFile(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFile() failed: %v", err)
	}

	// Verify custom values are preserved
	if config.ContainerNaming.DatabaseContainer != "my-custom-db" {
		t.Errorf("DatabaseContainer = %q, expected my-custom-db", config.ContainerNaming.DatabaseContainer)
	}

	if config.ContainerNaming.NetworkName != "my-custom-net" {
		t.Errorf("NetworkName = %q, expected my-custom-net", config.ContainerNaming.NetworkName)
	}

	if config.ContainerNaming.NetworkAlias != "db-alias" {
		t.Errorf("NetworkAlias = %q, expected db-alias", config.ContainerNaming.NetworkAlias)
	}

	if config.ContainerNaming.MigrateContainer != "my-migrate-" {
		t.Errorf("MigrateContainer = %q, expected my-migrate-", config.ContainerNaming.MigrateContainer)
	}

	if config.ContainerNaming.ProjectMountPath != "/custom/project" {
		t.Errorf("ProjectMountPath = %q, expected /custom/project", config.ContainerNaming.ProjectMountPath)
	}

	if config.ContainerNaming.RegistryMountPath != "/custom/registry" {
		t.Errorf("RegistryMountPath = %q, expected /custom/registry", config.ContainerNaming.RegistryMountPath)
	}
}

func TestServiceConfigWithFrameworkOverrides(t *testing.T) {
	// Create a temporary config file with framework overrides
	tempDir := t.TempDir()
	configContent := `
service:
  name: my-api
  port: 3000
  framework: rails
  start_command: bundle exec rails server
  migration_command: bundle exec rake db:migrate
  warmup_endpoint: /health
  warmup_delay_ms: 500
  needs_warmup: true
infrastructure:
  database: true
database:
  type: mysql
  database: my_db
  username: my_user
  password: my_pass
`
	configPath := filepath.Join(tempDir, ".linespec.yml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Load config
	config, err := LoadConfigFile(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFile() failed: %v", err)
	}

	// Verify custom framework values
	if config.Service.StartCommand != "bundle exec rails server" {
		t.Errorf("StartCommand = %q, expected bundle exec rails server", config.Service.StartCommand)
	}

	if config.Service.MigrationCommand != "bundle exec rake db:migrate" {
		t.Errorf("MigrationCommand = %q, expected bundle exec rake db:migrate", config.Service.MigrationCommand)
	}

	if config.Service.WarmupEndpoint != "/health" {
		t.Errorf("WarmupEndpoint = %q, expected /health", config.Service.WarmupEndpoint)
	}

	if config.Service.WarmupDelayMs != 500 {
		t.Errorf("WarmupDelayMs = %d, expected 500", config.Service.WarmupDelayMs)
	}

	if config.Service.NeedsWarmup == nil || !*config.Service.NeedsWarmup {
		t.Error("NeedsWarmup should be true")
	}
}
