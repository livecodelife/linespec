package config

// ServiceConfig defines the service under test
import "time"

type ServiceConfig struct {
	Name           string            `yaml:"name"`
	ServiceDir     string            `yaml:"service_dir"` // Directory containing service code (e.g., "user-service")
	Type           string            `yaml:"type"`        // web, worker, consumer
	Framework      string            `yaml:"framework"`
	Port           int               `yaml:"port"`
	HealthEndpoint string            `yaml:"health_endpoint"`
	DockerCompose  string            `yaml:"docker_compose"`
	BuildContext   string            `yaml:"build_context"`
	StartCommand   string            `yaml:"start_command"`
	Environment    map[string]string `yaml:"environment"`
}

// DatabaseConfig defines database requirements
type DatabaseConfig struct {
	Type       string `yaml:"type"` // mysql, postgresql
	Image      string `yaml:"image"`
	Port       int    `yaml:"port"`
	Container  string `yaml:"container"` // service name in docker-compose
	InitScript string `yaml:"init_script"`
	Database   string `yaml:"database"`
	Username   string `yaml:"username"`
	Password   string `yaml:"password"`
}

// InfrastructureConfig defines required infrastructure
type InfrastructureConfig struct {
	Kafka      bool `yaml:"kafka"`
	Database   bool `yaml:"database"`
	Redis      bool `yaml:"redis"`
	ExternalDB bool `yaml:"external_db"` // Don't manage DB, service has its own
}

// LineSpecConfig is the root configuration structure
type LineSpecConfig struct {
	Service        ServiceConfig        `yaml:"service"`
	Database       *DatabaseConfig      `yaml:"database,omitempty"`
	Infrastructure InfrastructureConfig `yaml:"infrastructure"`
	Dependencies   []DependencyConfig   `yaml:"dependencies,omitempty"`
	Provenance     *ProvenanceConfig    `yaml:"provenance,omitempty"`
	Created        time.Time            `yaml:"-"`
	BaseDir        string               `yaml:"-"`
}

// ProvenanceConfig defines provenance record settings
type ProvenanceConfig struct {
	Enforcement       string   `yaml:"enforcement"`         // none | warn | strict
	Dir               string   `yaml:"dir"`                 // relative to repo root
	SharedRepos       []string `yaml:"shared_repos"`        // paths or URLs to shared provenance repositories
	CommitTagRequired bool     `yaml:"commit_tag_required"` // whether commits must reference a prov ID
	AutoAffectedScope bool     `yaml:"auto_affected_scope"` // whether to auto-populate affected_scope from git diffs
}

// DependencyConfig defines external service dependencies
type DependencyConfig struct {
	Name    string            `yaml:"name"`
	Type    string            `yaml:"type"` // http, database
	Host    string            `yaml:"host"`
	Port    int               `yaml:"port"`
	Proxy   bool              `yaml:"proxy"` // Whether to mock this dependency
	Headers map[string]string `yaml:"headers,omitempty"`
}

// Default configurations for common frameworks
func DefaultConfig(framework string) *LineSpecConfig {
	switch framework {
	case "rails":
		return &LineSpecConfig{
			Service: ServiceConfig{
				Type:           "web",
				Framework:      "rails",
				HealthEndpoint: "/up",
				DockerCompose:  "docker-compose.yml",
			},
			Database: &DatabaseConfig{
				Type:  "mysql",
				Image: "mysql:8.4",
				Port:  3306,
			},
			Infrastructure: InfrastructureConfig{
				Database: true,
				Kafka:    false,
			},
		}
	case "fastapi":
		return &LineSpecConfig{
			Service: ServiceConfig{
				Type:           "web",
				Framework:      "fastapi",
				HealthEndpoint: "/health",
				DockerCompose:  "docker-compose.yml",
			},
			Infrastructure: InfrastructureConfig{
				Database: false,
				Kafka:    false,
			},
		}
	default:
		return &LineSpecConfig{
			Service: ServiceConfig{
				Type:           "web",
				Framework:      "unknown",
				HealthEndpoint: "/",
				DockerCompose:  "docker-compose.yml",
			},
			Infrastructure: InfrastructureConfig{},
		}
	}
}
