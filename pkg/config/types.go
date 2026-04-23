package config

import (
	"strings"
	"time"
)

// SchemaDiscoveryConfig defines schema discovery settings
type SchemaDiscoveryConfig struct {
	Mode          string   `yaml:"mode"`           // auto, static, none
	Tables        []string `yaml:"tables"`         // explicit list when mode is static
	ExcludeTables []string `yaml:"exclude_tables"` // tables to ignore in auto mode
	CacheFile     string   `yaml:"cache_file"`     // path to cache discovered schema
}

// PayloadConfig defines payload loading and parsing configuration
type PayloadConfig struct {
	Directory        string            `yaml:"directory"`         // payload directory name (default: payloads)
	StatusField      string            `yaml:"status_field"`      // field path for HTTP status code (default: "status")
	AuthExtraction   map[string]string `yaml:"auth_extraction"`   // custom auth extraction rules
	SupportedFormats []string          `yaml:"supported_formats"` // list of supported formats
}

// ServiceConfig defines the service under test

// FrameworkConfig defines the interface for framework-specific configuration
type FrameworkConfig interface {
	GetStartCommand(port string) []string
	GetMigrationCommand() []string
	NeedsWarmup() bool
	GetWarmupEndpoint() string
	GetWarmupDelay() time.Duration
}

// frameworkDefaults holds the default configuration for known frameworks.
// These are used as starting values that can be overridden by .linespec.yml fields.
var frameworkDefaults = map[string]GenericFrameworkConfig{
	"rails": {
		CustomStartCommand:  "rm -f tmp/pids/server.pid && bundle exec rails server -b 0.0.0.0 -p ${PORT}",
		CustomMigrationCmd:  "bundle exec rails db:migrate",
		NeedsWarmupFlag:     true,
		WarmupEndpointValue: "/up",
		WarmupDelayMs:       100,
	},
	"fastapi": {
		CustomStartCommand:  "python -m uvicorn main:app --host 0.0.0.0 --port ${PORT}",
		WarmupEndpointValue: "/health",
	},
	"django": {
		CustomStartCommand:  "python manage.py runserver 0.0.0.0:${PORT}",
		CustomMigrationCmd:  "python manage.py migrate",
		NeedsWarmupFlag:     true,
		WarmupEndpointValue: "/health",
		WarmupDelayMs:       100,
	},
	"express": {
		CustomStartCommand:  "npm start",
		WarmupEndpointValue: "/health",
	},
	"chi": {
		CustomStartCommand:  "PORT=${PORT} go run .",
		WarmupEndpointValue: "/health",
	},
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
		cmd := strings.ReplaceAll(g.CustomStartCommand, "${PORT}", port)
		return []string{"sh", "-c", cmd}
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

// GetFrameworkConfig returns a FrameworkConfig for the given framework name.
// Known framework names (rails, fastapi, django, express) provide sensible defaults
// that can be overridden by the non-zero override parameters from .linespec.yml.
func GetFrameworkConfig(framework string, customStartCmd, customMigrationCmd string, needsWarmup *bool, warmupEndpoint string, warmupDelayMs int) FrameworkConfig {
	cfg := GenericFrameworkConfig{}
	if defaults, ok := frameworkDefaults[framework]; ok {
		cfg = defaults
	}
	if customStartCmd != "" {
		cfg.CustomStartCommand = customStartCmd
	}
	if customMigrationCmd != "" {
		cfg.CustomMigrationCmd = customMigrationCmd
	}
	if needsWarmup != nil {
		cfg.NeedsWarmupFlag = *needsWarmup
	}
	if warmupEndpoint != "" {
		cfg.WarmupEndpointValue = warmupEndpoint
	}
	if warmupDelayMs > 0 {
		cfg.WarmupDelayMs = warmupDelayMs
	}
	return &cfg
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
	Name       string `yaml:"name"`            // Logical name used to identify this database (required when using databases: list)
	Type       string `yaml:"type"` // mysql, postgresql
	Image      string `yaml:"image"`
	Port       int    `yaml:"port"`
	Container  string `yaml:"container"` // service name in docker-compose
	InitScript string `yaml:"init_script"`
	Database   string `yaml:"database"`
	Username   string `yaml:"username"`
	Password   string `yaml:"password"`
	Host       string `yaml:"host"`            // Host alias the app uses to connect (proxy occupies this alias on the Docker network)
	Proxy      *bool  `yaml:"proxy,omitempty"` // Whether to use a proxy for this database (enables interception)
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
	Kafka      bool   `yaml:"kafka"`
	Database   bool   `yaml:"database"`
	Redis      bool   `yaml:"redis"`
	GRPC       bool   `yaml:"grpc"`
	ExternalDB bool   `yaml:"external_db"`  // Don't manage DB, service has its own
	ProxyImage string `yaml:"proxy_image"`   // Docker image for protocol proxies (default: linespec:latest)
}

// LineSpecConfig is the root configuration structure
type LineSpecConfig struct {
	Service             ServiceConfig       `yaml:"service"`
	Database            *DatabaseConfig     `yaml:"database,omitempty"` // Single-database form (backward compat). Normalised into Databases by applyDefaults.
	Databases           []DatabaseConfig    `yaml:"databases,omitempty"` // Multi-database form. Takes precedence over Database when both are set.
	Infrastructure      InfrastructureConfig `yaml:"infrastructure"`
	Dependencies        []DependencyConfig  `yaml:"dependencies,omitempty"`
	Provenance          *ProvenanceConfig   `yaml:"provenance,omitempty"`
	ContainerNaming     *ContainerNaming    `yaml:"container_naming,omitempty"`
	PortConfig          *PortConfig         `yaml:"ports,omitempty"`
	SchemaDiscovery     *SchemaDiscoveryConfig `yaml:"schema_discovery,omitempty"`
	Payload             *PayloadConfig      `yaml:"payload,omitempty"`
	TestTimeoutSeconds  int                 `yaml:"timeout_seconds,omitempty"`
	StrictPassthrough   bool                `yaml:"strict_passthrough,omitempty"`
	GRPCDescriptorSet   string              `yaml:"grpc_descriptor_set,omitempty"`
	Created             time.Time           `yaml:"-"`
	BaseDir             string              `yaml:"-"`
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
	Enforcement                  string           `yaml:"enforcement"`                      // none | warn | strict
	Dir                          string           `yaml:"dir"`                              // relative to repo root
	SharedRepos                  []string         `yaml:"shared_repos"`                     // paths or URLs to shared provenance repositories
	CommitTagRequired            bool             `yaml:"commit_tag_required"`              // whether commits must reference a prov ID
	AutoAffectedScope            bool             `yaml:"auto_affected_scope"`              // whether to auto-populate affected_scope from git diffs
	RunAssociatedSpecsOnComplete bool             `yaml:"run_associated_specs_on_complete"` // whether to run associated_specs before committing a completion transition
	Embedding                    *EmbeddingConfig `yaml:"embedding,omitempty"`              // embedding API configuration
}

// DependencyConfig defines external service dependencies
type DependencyConfig struct {
	Name              string            `yaml:"name"`
	Type              string            `yaml:"type"` // http, grpc, database
	Host              string            `yaml:"host"`
	Port              int               `yaml:"port"`
	Proxy             bool              `yaml:"proxy"`
	HostAlias         string            `yaml:"host_alias"`
	Headers           map[string]string `yaml:"headers,omitempty"`
	GRPCDescriptorSet string            `yaml:"grpc_descriptor_set,omitempty"`
}

// DefaultConfig returns baseline config defaults for a given framework name.
// Health endpoints are derived from frameworkDefaults. Rails also gets database
// defaults since it conventionally requires a relational database.
func DefaultConfig(framework string) *LineSpecConfig {
	healthEndpoint := "/"
	if fw, ok := frameworkDefaults[framework]; ok && fw.WarmupEndpointValue != "" {
		healthEndpoint = fw.WarmupEndpointValue
	}

	cfg := &LineSpecConfig{
		Service: ServiceConfig{
			Type:           "web",
			Framework:      framework,
			HealthEndpoint: healthEndpoint,
			DockerCompose:  "docker-compose.yml",
		},
		Infrastructure: InfrastructureConfig{},
	}

	if framework == "rails" {
		cfg.Database = &DatabaseConfig{
			Type:  "mysql",
			Image: "mysql:8.4",
			Port:  3306,
		}
		cfg.Infrastructure.Database = true
	}

	return cfg
}
