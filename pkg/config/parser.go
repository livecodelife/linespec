package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadConfig searches for .linespec.yml starting from the given directory
// and walking up to parent directories
func LoadConfig(startDir string) (*LineSpecConfig, error) {
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

	// Database defaults
	if config.Infrastructure.Database && config.Database == nil {
		config.Database = defaults.Database
	}

	if config.Database != nil {
		if config.Database.Type == "" {
			config.Database.Type = "mysql"
		}
		if config.Database.Image == "" {
			switch config.Database.Type {
			case "mysql":
				config.Database.Image = "mysql:8.4"
				if config.Database.Port == 0 {
					config.Database.Port = 3306
				}
			case "postgresql":
				config.Database.Image = "postgres:16-alpine"
				if config.Database.Port == 0 {
					config.Database.Port = 5432
				}
			}
		}
		// Database name defaults to service name + _development for Rails apps
		if config.Database.Database == "" {
			config.Database.Database = config.Service.Name + "_development"
		}
		// Username defaults to service name + _user for Rails apps
		if config.Database.Username == "" {
			config.Database.Username = config.Service.Name + "_user"
		}
		// Password defaults to service name + _password for Rails apps
		if config.Database.Password == "" {
			config.Database.Password = config.Service.Name + "_password"
		}
		// Host defaults to "db" for internal container communication
		if config.Database.Host == "" {
			config.Database.Host = "db"
		}
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
		config.ContainerNaming.NetworkAlias = "real-db"
	}
	if config.ContainerNaming.MigrateContainer == "" {
		config.ContainerNaming.MigrateContainer = "linespec-migrate-"
	}
	if config.ContainerNaming.ProjectMountPath == "" {
		config.ContainerNaming.ProjectMountPath = "/app/project"
	}
	if config.ContainerNaming.RegistryMountPath == "" {
		config.ContainerNaming.RegistryMountPath = "/app/registry"
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
		if config.Database == nil {
			return fmt.Errorf("database configuration required when infrastructure.database is true")
		}
		if config.Database.Type == "" {
			return fmt.Errorf("database.type is required")
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
