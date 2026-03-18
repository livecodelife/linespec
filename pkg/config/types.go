package config

// ServiceConfig defines the service under test
import (
	"strings"
	"time"
)

// FrameworkConfig defines the interface for framework-specific configuration
type FrameworkConfig interface {
	GetStartCommand(port string) []string
	GetMigrationCommand() []string
	NeedsWarmup() bool
	GetWarmupEndpoint() string
	GetWarmupDelay() time.Duration
}

// RailsFrameworkConfig implements FrameworkConfig for Ruby on Rails
type RailsFrameworkConfig struct{}

func (r *RailsFrameworkConfig) GetStartCommand(port string) []string {
	return []string{"bash", "-c", "rm -f tmp/pids/server.pid && bundle exec rails server -b 0.0.0.0 -p " + port}
}

func (r *RailsFrameworkConfig) GetMigrationCommand() []string {
	return []string{"bundle", "exec", "rails", "db:migrate"}
}

func (r *RailsFrameworkConfig) NeedsWarmup() bool {
	return true
}

func (r *RailsFrameworkConfig) GetWarmupEndpoint() string {
	return "/up"
}

func (r *RailsFrameworkConfig) GetWarmupDelay() time.Duration {
	return 100 * time.Millisecond
}

// FastAPIFrameworkConfig implements FrameworkConfig for FastAPI
type FastAPIFrameworkConfig struct{}

func (f *FastAPIFrameworkConfig) GetStartCommand(port string) []string {
	return []string{"python", "-m", "uvicorn", "main:app", "--host", "0.0.0.0", "--port", port}
}

func (f *FastAPIFrameworkConfig) GetMigrationCommand() []string {
	return nil // FastAPI typically uses Alembic which is framework-agnostic
}

func (f *FastAPIFrameworkConfig) NeedsWarmup() bool {
	return false
}

func (f *FastAPIFrameworkConfig) GetWarmupEndpoint() string {
	return "/health"
}

func (f *FastAPIFrameworkConfig) GetWarmupDelay() time.Duration {
	return 0
}

// DjangoFrameworkConfig implements FrameworkConfig for Django
type DjangoFrameworkConfig struct{}

func (d *DjangoFrameworkConfig) GetStartCommand(port string) []string {
	return []string{"python", "manage.py", "runserver", "0.0.0.0:" + port}
}

func (d *DjangoFrameworkConfig) GetMigrationCommand() []string {
	return []string{"python", "manage.py", "migrate"}
}

func (d *DjangoFrameworkConfig) NeedsWarmup() bool {
	return true
}

func (d *DjangoFrameworkConfig) GetWarmupEndpoint() string {
	return "/health"
}

func (d *DjangoFrameworkConfig) GetWarmupDelay() time.Duration {
	return 100 * time.Millisecond
}

// ExpressFrameworkConfig implements FrameworkConfig for Node.js/Express
type ExpressFrameworkConfig struct{}

func (e *ExpressFrameworkConfig) GetStartCommand(port string) []string {
	return []string{"npm", "start"}
}

func (e *ExpressFrameworkConfig) GetMigrationCommand() []string {
	return nil // Express doesn't have built-in migrations
}

func (e *ExpressFrameworkConfig) NeedsWarmup() bool {
	return false
}

func (e *ExpressFrameworkConfig) GetWarmupEndpoint() string {
	return "/health"
}

func (e *ExpressFrameworkConfig) GetWarmupDelay() time.Duration {
	return 0
}

// GenericFrameworkConfig implements FrameworkConfig for custom/unknown frameworks
type GenericFrameworkConfig struct {
	CustomStartCommand  string
	CustomMigrationCmd  string
	NeedsWarmupFlag     bool
	WarmupEndpointValue string
	WarmupDelayMs       int
}

func (g *GenericFrameworkConfig) GetStartCommand(port string) []string {
	if g.CustomStartCommand != "" {
		return []string{"sh", "-c", g.CustomStartCommand}
	}
	return []string{"sh", "-c", "echo 'No start command specified'"}
}

func (g *GenericFrameworkConfig) GetMigrationCommand() []string {
	if g.CustomMigrationCmd != "" {
		return []string{"sh", "-c", g.CustomMigrationCmd}
	}
	return nil
}

func (g *GenericFrameworkConfig) NeedsWarmup() bool {
	return g.NeedsWarmupFlag
}

func (g *GenericFrameworkConfig) GetWarmupEndpoint() string {
	if g.WarmupEndpointValue != "" {
		return g.WarmupEndpointValue
	}
	return "/"
}

func (g *GenericFrameworkConfig) GetWarmupDelay() time.Duration {
	return time.Duration(g.WarmupDelayMs) * time.Millisecond
}

// GetFrameworkConfig returns the appropriate FrameworkConfig for a framework name
func GetFrameworkConfig(framework string, customStartCmd, customMigrationCmd string, needsWarmup bool, warmupEndpoint string, warmupDelayMs int) FrameworkConfig {
	switch framework {
	case "rails":
		return &RailsFrameworkConfig{}
	case "fastapi":
		return &FastAPIFrameworkConfig{}
	case "django":
		return &DjangoFrameworkConfig{}
	case "express":
		return &ExpressFrameworkConfig{}
	default:
		return &GenericFrameworkConfig{
			CustomStartCommand:  customStartCmd,
			CustomMigrationCmd:  customMigrationCmd,
			NeedsWarmupFlag:     needsWarmup,
			WarmupEndpointValue: warmupEndpoint,
			WarmupDelayMs:       warmupDelayMs,
		}
	}
}

type ServiceConfig struct {
	Name             string            `yaml:"name"`
	ServiceDir       string            `yaml:"service_dir"` // Directory containing service code (e.g., "user-service")
	Type             string            `yaml:"type"`        // web, worker, consumer
	Framework        string            `yaml:"framework"`
	Port             int               `yaml:"port"`
	HealthEndpoint   string            `yaml:"health_endpoint"`
	DockerCompose    string            `yaml:"docker_compose"`
	BuildContext     string            `yaml:"build_context"`
	StartCommand     string            `yaml:"start_command"`
	MigrationCommand string            `yaml:"migration_command"`      // Custom migration command (overrides framework default)
	WarmupEndpoint   string            `yaml:"warmup_endpoint"`        // Custom warmup endpoint (overrides framework default)
	WarmupDelayMs    int               `yaml:"warmup_delay_ms"`        // Custom warmup delay in milliseconds
	NeedsWarmup      *bool             `yaml:"needs_warmup,omitempty"` // Whether framework needs warmup (overrides framework default)
	Environment      map[string]string `yaml:"environment"`
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
	Host       string `yaml:"host"` // Host for external databases (when not using container)
}

// ContainerNaming defines configurable container and network naming
// Supports template variables: {{ .ServiceName }}, {{ .SpecName }}, {{ .Type }}
type ContainerNaming struct {
	DatabaseContainer string `yaml:"database_container"`  // Template for DB container name
	NetworkAlias      string `yaml:"network_alias"`       // Network alias for database (e.g., "real-db")
	KafkaContainer    string `yaml:"kafka_container"`     // Template for Kafka container name
	ProxyContainer    string `yaml:"proxy_container"`     // Template for proxy container name
	AppContainer      string `yaml:"app_container"`       // Template for app container name
	NetworkName       string `yaml:"network_name"`        // Template for network name
	MigrateContainer  string `yaml:"migrate_container"`   // Template for migration container name
	ProjectMountPath  string `yaml:"project_mount_path"`  // Mount path for project (default: /app/project)
	RegistryMountPath string `yaml:"registry_mount_path"` // Mount path for registry (default: /app/registry)
}

// PortConfig defines dynamic port allocation settings
type PortConfig struct {
	MinPort        int  `yaml:"min_port"`         // Minimum port number for dynamic allocation
	MaxPort        int  `yaml:"max_port"`         // Maximum port number for dynamic allocation
	DynamicPorts   bool `yaml:"dynamic_ports"`    // Enable dynamic port allocation (default: true)
	FixedProxyPort int  `yaml:"fixed_proxy_port"` // Fixed port for proxy verification (0 = dynamic)
}

// ContainerNameParams holds parameters for container name template substitution
type ContainerNameParams struct {
	ServiceName string
	SpecName    string
	Type        string // "db", "http", "kafka", etc.
}

// GetDatabaseContainer returns the database container name with template substitution
func (c *ContainerNaming) GetDatabaseContainer(params ContainerNameParams) string {
	if c.DatabaseContainer == "" {
		c.DatabaseContainer = "linespec-shared-db"
	}
	return substituteTemplate(c.DatabaseContainer, params)
}

// GetKafkaContainer returns the Kafka container name with template substitution
func (c *ContainerNaming) GetKafkaContainer(params ContainerNameParams) string {
	if c.KafkaContainer == "" {
		c.KafkaContainer = "linespec-shared-kafka"
	}
	return substituteTemplate(c.KafkaContainer, params)
}

// GetProxyContainer returns the proxy container name with template substitution
func (c *ContainerNaming) GetProxyContainer(params ContainerNameParams) string {
	if c.ProxyContainer == "" {
		c.ProxyContainer = "proxy-{{ .Type }}-{{ .SpecName }}"
	}
	return substituteTemplate(c.ProxyContainer, params)
}

// GetAppContainer returns the app container name with template substitution
func (c *ContainerNaming) GetAppContainer(params ContainerNameParams) string {
	if c.AppContainer == "" {
		c.AppContainer = "app-{{ .SpecName }}"
	}
	return substituteTemplate(c.AppContainer, params)
}

// GetMigrateContainer returns the migration container name with template substitution
func (c *ContainerNaming) GetMigrateContainer(params ContainerNameParams) string {
	if c.MigrateContainer == "" {
		c.MigrateContainer = "linespec-migrate-{{ .ServiceName }}"
	}
	return substituteTemplate(c.MigrateContainer, params)
}

// GetNetworkName returns the network name with template substitution
func (c *ContainerNaming) GetNetworkName(params ContainerNameParams) string {
	if c.NetworkName == "" {
		c.NetworkName = "linespec-shared-net"
	}
	return substituteTemplate(c.NetworkName, params)
}

// GetProjectMountPath returns the project mount path
func (c *ContainerNaming) GetProjectMountPath() string {
	if c.ProjectMountPath == "" {
		c.ProjectMountPath = "/app/project"
	}
	return c.ProjectMountPath
}

// GetRegistryMountPath returns the registry mount path
func (c *ContainerNaming) GetRegistryMountPath() string {
	if c.RegistryMountPath == "" {
		c.RegistryMountPath = "/app/registry"
	}
	return c.RegistryMountPath
}

// substituteTemplate performs simple template substitution for container names
// Supports: {{ .ServiceName }}, {{ .SpecName }}, {{ .Type }}
func substituteTemplate(template string, params ContainerNameParams) string {
	result := template
	result = strings.ReplaceAll(result, "{{ .ServiceName }}", params.ServiceName)
	result = strings.ReplaceAll(result, "{{ .SpecName }}", params.SpecName)
	result = strings.ReplaceAll(result, "{{ .Type }}", params.Type)
	return result
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
	Service         ServiceConfig        `yaml:"service"`
	Database        *DatabaseConfig      `yaml:"database,omitempty"`
	Infrastructure  InfrastructureConfig `yaml:"infrastructure"`
	Dependencies    []DependencyConfig   `yaml:"dependencies,omitempty"`
	Provenance      *ProvenanceConfig    `yaml:"provenance,omitempty"`
	ContainerNaming *ContainerNaming     `yaml:"container_naming,omitempty"`
	PortConfig      *PortConfig          `yaml:"ports,omitempty"`
	Created         time.Time            `yaml:"-"`
	BaseDir         string               `yaml:"-"`
}

// EmbeddingConfig defines the embedding API configuration
type EmbeddingConfig struct {
	Provider            string  `yaml:"provider"`             // voyage, openai, etc.
	IndexModel          string  `yaml:"index_model"`          // e.g., voyage-4-large (for indexing documents at 2048 dims)
	QueryModel          string  `yaml:"query_model"`          // e.g., voyage-4-lite (for queries at 2048 dims)
	APIKey              string  `yaml:"api_key"`              // Can be "${ENV_VAR_NAME}" or literal
	SimilarityThreshold float64 `yaml:"similarity_threshold"` // default: 0.50
	IndexOnComplete     bool    `yaml:"index_on_complete"`    // default: true
}

// ProvenanceConfig defines provenance record settings
type ProvenanceConfig struct {
	Enforcement       string           `yaml:"enforcement"`         // none | warn | strict
	Dir               string           `yaml:"dir"`                 // relative to repo root
	SharedRepos       []string         `yaml:"shared_repos"`        // paths or URLs to shared provenance repositories
	CommitTagRequired bool             `yaml:"commit_tag_required"` // whether commits must reference a prov ID
	AutoAffectedScope bool             `yaml:"auto_affected_scope"` // whether to auto-populate affected_scope from git diffs
	Embedding         *EmbeddingConfig `yaml:"embedding,omitempty"` // embedding API configuration
}

// DependencyConfig defines external service dependencies
type DependencyConfig struct {
	Name      string            `yaml:"name"`
	Type      string            `yaml:"type"` // http, database
	Host      string            `yaml:"host"`
	Port      int               `yaml:"port"`
	Proxy     bool              `yaml:"proxy"`      // Whether to mock this dependency
	HostAlias string            `yaml:"host_alias"` // Custom hostname alias for the service
	Headers   map[string]string `yaml:"headers,omitempty"`
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
