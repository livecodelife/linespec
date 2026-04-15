package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultNetworkAlias is the container network alias used when no containerNaming.network_alias
// is set in .linespec.yml. Exported so the runner can reference the same value.
const DefaultNetworkAlias = "real-db"

// LoadConfig searches for .linespec.yml starting from the given directory
// and walking up to parent directories. Supports LINESPEC_CONFIG env var override.
func LoadConfig(startDir string) (*LineSpecConfig, error) {
	// Check for LINESPEC_CONFIG environment variable first
	if envConfig := os.Getenv("LINESPEC_CONFIG"); envConfig != "" {
		return LoadConfigFile(envConfig)
	}

	currentDir, err := filepath.Abs(startDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Walk up directory tree looking for .linespec.yml
	for {
		configPath := filepath.Join(currentDir, ".linespec.yml")
		if _, err := os.Stat(configPath); err == nil {
			return LoadConfigFile(configPath)
		}

		// Check for .linespec.yaml as alternative
		configPath = filepath.Join(currentDir, ".linespec.yaml")
		if _, err := os.Stat(configPath); err == nil {
			return LoadConfigFile(configPath)
		}

		// Check if we should stop walking (reached root or .git)
		if _, err := os.Stat(filepath.Join(currentDir, ".git")); err == nil {
			break
		}

		parent := filepath.Dir(currentDir)
		if parent == currentDir {
			break // Reached filesystem root
		}
		currentDir = parent
	}

	return nil, fmt.Errorf("no .linespec.yml found in %s or parent directories", startDir)
}

// LoadConfigFile loads a specific .linespec.yml file
func LoadConfigFile(path string) (*LineSpecConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config LineSpecConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Set base directory
	config.BaseDir = filepath.Dir(path)

	// Apply defaults based on framework if certain fields are empty
	applyDefaults(&config)

	// Validate required fields
	if err := validate(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

// applyDefaults fills in default values based on framework
func applyDefaults(config *LineSpecConfig) {
	defaults := DefaultConfig(config.Service.Framework)

	if config.Service.Port == 0 {
		config.Service.Port = defaults.Service.Port
	}
	if config.Service.HealthEndpoint == "" {
		config.Service.HealthEndpoint = defaults.Service.HealthEndpoint
	}
	if config.Service.DockerCompose == "" {
		config.Service.DockerCompose = defaults.Service.DockerCompose
	}

	// Database defaults — normalise both forms into config.Databases, then keep
	// config.Database pointing at Databases[0] for backward-compatible runner code.

	// 1. If infrastructure.database is set but no database config provided at all, use framework defaults.
	if config.Infrastructure.Database && config.Database == nil && len(config.Databases) == 0 {
		config.Database = defaults.Database
	}

	// 2. If the singular `database:` form is set but `databases:` is empty, promote it.
	if config.Database != nil && len(config.Databases) == 0 {
		entry := *config.Database
		if entry.Name == "" {
			entry.Name = "primary"
		}
		config.Databases = []DatabaseConfig{entry}
	}

	// 3. Apply per-entry defaults to every entry in Databases.
	for i := range config.Databases {
		db := &config.Databases[i]
		if db.Type == "" {
			db.Type = "mysql"
		}
		if db.Image == "" {
			switch db.Type {
			case "mysql":
				db.Image = "mysql:8.4"
				if db.Port == 0 {
					db.Port = 3306
				}
			case "postgresql":
				db.Image = "postgres:16-alpine"
				if db.Port == 0 {
					db.Port = 5432
				}
			case "mongodb":
				db.Image = "mongo:7"
				if db.Port == 0 {
					db.Port = 27017
				}
			}
		}
		// Host defaults: single unnamed database keeps "db" for backward compat;
		// named databases default their host to their name so each gets a unique alias.
		if db.Host == "" {
			if db.Name != "" && db.Name != "primary" {
				db.Host = db.Name
			} else if len(config.Databases) == 1 {
				db.Host = "db"
			} else {
				db.Host = db.Name
			}
		}
		// Proxy defaults to true for all database types (enables mock interception).
		if db.Proxy == nil {
			defaultProxy := true
			db.Proxy = &defaultProxy
		}
	}

	// 4. Keep config.Database pointing at Databases[0] so existing code that reads it still works.
	if len(config.Databases) > 0 {
		config.Database = &config.Databases[0]
	}

	// Container naming defaults
	if config.ContainerNaming == nil {
		config.ContainerNaming = &ContainerNaming{}
	}
	if config.ContainerNaming.DatabaseContainer == "" {
		config.ContainerNaming.DatabaseContainer = "linespec-shared-db"
	}
	if config.ContainerNaming.NetworkName == "" {
		config.ContainerNaming.NetworkName = "linespec-shared-net"
	}
	if config.ContainerNaming.NetworkAlias == "" {
		config.ContainerNaming.NetworkAlias = DefaultNetworkAlias
	}
	if config.ContainerNaming.MigrateContainer == "" {
		config.ContainerNaming.MigrateContainer = "linespec-migrate-{{ .ServiceName }}"
	}
	if config.ContainerNaming.KafkaContainer == "" {
		config.ContainerNaming.KafkaContainer = "linespec-shared-kafka"
	}
	if config.ContainerNaming.ProxyContainer == "" {
		config.ContainerNaming.ProxyContainer = "proxy-{{ .Type }}-{{ .SpecName }}"
	}
	if config.ContainerNaming.AppContainer == "" {
		config.ContainerNaming.AppContainer = "app-{{ .SpecName }}"
	}
	if config.ContainerNaming.ProjectMountPath == "" {
		config.ContainerNaming.ProjectMountPath = "/app/project"
	}
	if config.ContainerNaming.RegistryMountPath == "" {
		config.ContainerNaming.RegistryMountPath = "/app/registry"
	}

	// Port configuration defaults
	if config.PortConfig == nil {
		config.PortConfig = &PortConfig{}
	}
	if config.PortConfig.MinPort == 0 {
		config.PortConfig.MinPort = 10000
	}
	if config.PortConfig.MaxPort == 0 {
		config.PortConfig.MaxPort = 65535
	}
	// DynamicPorts defaults to true
	// FixedProxyPort defaults to 0 (dynamic allocation)

	// Schema discovery configuration defaults
	if config.SchemaDiscovery == nil {
		config.SchemaDiscovery = &SchemaDiscoveryConfig{
			Mode: "auto", // Default to auto-discovery
		}
	}
	if config.SchemaDiscovery.Mode == "" {
		config.SchemaDiscovery.Mode = "auto"
	}

	// Test timeout default
	if config.TestTimeoutSeconds == 0 {
		config.TestTimeoutSeconds = 180 // 3 minutes
	}

	// Payload configuration defaults
	if config.Payload == nil {
		config.Payload = &PayloadConfig{}
	}
	if config.Payload.Directory == "" {
		config.Payload.Directory = "payloads"
	}
	if config.Payload.StatusField == "" {
		config.Payload.StatusField = "status"
	}
	// Default supported formats
	if len(config.Payload.SupportedFormats) == 0 {
		config.Payload.SupportedFormats = []string{"json", "yaml", "yml"}
	}
}

// validate checks that required configuration is present
func validate(config *LineSpecConfig) error {
	if config.Service.Name == "" {
		return fmt.Errorf("service.name is required")
	}
	if config.Service.Port == 0 {
		return fmt.Errorf("service.port is required")
	}
	if config.Infrastructure.Database {
		if len(config.Databases) == 0 {
			return fmt.Errorf("database configuration required when infrastructure.database is true")
		}
		// Validate each database entry.
		seenHosts := make(map[string]string)
		for i, db := range config.Databases {
			label := db.Name
			if label == "" {
				label = fmt.Sprintf("databases[%d]", i)
			}
			if db.Type == "" {
				return fmt.Errorf("%s.type is required", label)
			}
			if db.Database == "" {
				return fmt.Errorf("%s.database is required; set it explicitly in .linespec.yml", label)
			}
			if db.Username == "" {
				return fmt.Errorf("%s.username is required; set it explicitly in .linespec.yml", label)
			}
			if db.Password == "" {
				return fmt.Errorf("%s.password is required; set it explicitly in .linespec.yml", label)
			}
			// Require name on every entry when there are multiple databases.
			if len(config.Databases) > 1 && db.Name == "" {
				return fmt.Errorf("databases[%d].name is required when multiple databases are configured", i)
			}
			// Reject duplicate host aliases — each proxy must occupy a unique network alias.
			if prev, exists := seenHosts[db.Host]; exists {
				return fmt.Errorf("databases %q and %q share the same host alias %q; each database must have a unique host", prev, label, db.Host)
			}
			seenHosts[db.Host] = label
		}
	}
	return nil
}

// GetHealthURL returns the full health check URL
func (c *LineSpecConfig) GetHealthURL(hostPort string) string {
	return fmt.Sprintf("http://localhost:%s%s", hostPort, c.Service.HealthEndpoint)
}

// GetDockerComposePath returns the absolute path to docker-compose.yml
func (c *LineSpecConfig) GetDockerComposePath() string {
	if filepath.IsAbs(c.Service.DockerCompose) {
		return c.Service.DockerCompose
	}
	return filepath.Join(c.BaseDir, c.Service.DockerCompose)
}
