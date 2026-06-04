package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/livecodelife/linespec/pkg/config"
	"github.com/livecodelife/linespec/pkg/docker"
	"github.com/livecodelife/linespec/pkg/dsl"
	"github.com/livecodelife/linespec/pkg/interpolate"
	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/schema"
	"github.com/livecodelife/linespec/pkg/types"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
)

// persistentServiceContainers holds references to containers that are kept alive
// across tests within a suite, avoiding per-test startup overhead.
type persistentServiceContainers struct {
	// Per-database state — keyed by db.Host (the proxy's network alias).
	dbProxies     map[string]string // db.Host → proxy container name
	dbContainers  map[string]string // db.Host → real DB container name (empty for shared MySQL)
	dbVerifyPorts map[string]string // db.Host → sidecar verify port
	dbHostPorts   map[string]string // db.Host → "localhost:port" of real DB (for truncation)
	dbTypes       map[string]string // db.Host → "mysql"|"postgresql"|"mongodb"

	httpProxyName   string
	grpcProxyName   string
	redisProxyName  string
	appName         string
	appHostPort     string
	httpVerifyPort  string
	grpcVerifyPort  string
	redisVerifyPort string
}

type TestSuite struct {
	orch            *docker.DockerOrchestrator
	networkName     string
	dbHostPort      string
	kafkaReady      bool
	cwd             string
	tempDir         string                            // Temp directory for shared files like schema cache
	serviceConfigs  map[string]*config.LineSpecConfig // Discovered service configurations
	containerNaming    *config.ContainerNaming           // Container naming configuration
	sharedSchemaJSON []byte // Raw JSON schema written to per-test tempDir and passed to MySQL proxies via --schema-file
	persistentContainers map[string]*persistentServiceContainers
	persistentMu         sync.Mutex
}

func NewTestSuite() (*TestSuite, error) {
	orch, err := docker.NewDockerOrchestrator()
	if err != nil {
		return nil, err
	}
	cwd, _ := os.Getwd()

	// Create temp directory for shared files
	tempDir, err := os.MkdirTemp("", "linespec-suite-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create suite temp directory: %w", err)
	}

	// Initialize default container naming
	containerNaming := &config.ContainerNaming{
		DatabaseContainer: "linespec-shared-db",
		NetworkName:       "linespec-shared-net",
		NetworkAlias:      config.DefaultNetworkAlias,
		MigrateContainer:  "linespec-migrate-{{ .ServiceName }}",
		KafkaContainer:    "linespec-shared-kafka",
		ProxyContainer:    "proxy-{{ .Type }}-{{ .SpecName }}",
		AppContainer:      "app-{{ .SpecName }}",
		ProjectMountPath:  "/app/project",
		RegistryMountPath: "/app/registry",
	}

	return &TestSuite{
		orch:                 orch,
		networkName:          containerNaming.NetworkName,
		cwd:                  cwd,
		tempDir:              tempDir,
		serviceConfigs:       make(map[string]*config.LineSpecConfig),
		containerNaming:      containerNaming,
		persistentContainers: make(map[string]*persistentServiceContainers),
	}, nil
}

// DiscoverServices searches for services with .linespec.yml configuration files
// in the current directory and subdirectories (up to 2 levels deep for performance)
func (s *TestSuite) DiscoverServices() error {
	logger.Debug("Discovering services from .linespec.yml files")

	// Walk current directory looking for .linespec.yml files
	err := filepath.Walk(s.cwd, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip directories we can't read
		}

		// Skip hidden directories and vendor
		if info.IsDir() && (strings.HasPrefix(info.Name(), ".") || info.Name() == "vendor" || info.Name() == "node_modules") {
			return filepath.SkipDir
		}

		// Only look for .linespec.yml at the root of service directories
		if !info.IsDir() && info.Name() == ".linespec.yml" {
			serviceDir := filepath.Dir(path)
			serviceName := filepath.Base(serviceDir)

			// Load the configuration
			cfg, err := config.LoadConfigFile(path)
			if err != nil {
				logger.Debug("Failed to load config from %s: %v", path, err)
				return nil
			}

			// Store the service configuration
			s.serviceConfigs[serviceName] = cfg
			logger.Debug("Discovered service: %s at %s", serviceName, serviceDir)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to discover services: %w", err)
	}

	if len(s.serviceConfigs) == 0 {
		logger.Debug("No services discovered from .linespec.yml files")
	}

	return nil
}

// FindInitScript returns the init SQL path for the first MySQL service that has
// database.init_script explicitly configured in its .linespec.yml.
func (s *TestSuite) FindInitScript() string {
	for serviceName, cfg := range s.serviceConfigs {
		if cfg.Database == nil || cfg.Database.Type == "postgresql" {
			continue
		}
		if cfg.Database.InitScript == "" {
			continue
		}
		initScriptPath := filepath.Join(cfg.BaseDir, cfg.Database.InitScript)
		if _, err := os.Stat(initScriptPath); err == nil {
			content, err := os.ReadFile(initScriptPath)
			if err == nil && !containsPostgresSyntax(string(content)) {
				logger.Debug("Found MySQL init script from config in service %s: %s", serviceName, initScriptPath)
				return initScriptPath
			}
		}
	}
	logger.Debug("No MySQL init script configured, database will start empty")
	return ""
}

// containsPostgresSyntax checks if SQL content contains PostgreSQL-specific syntax
func containsPostgresSyntax(content string) bool {
	postgresPatterns := []string{
		"pg_database",
		"pg_tables",
		"SERIAL PRIMARY KEY",
		"TIMESTAMP WITH TIME ZONE",
		"\\gexec",
		"\\c ",
	}

	contentLower := strings.ToLower(content)
	for _, pattern := range postgresPatterns {
		if strings.Contains(contentLower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

func (s *TestSuite) SetupSharedInfrastructure(ctx context.Context) error {
	// Clean up any existing infrastructure first
	s.CleanupSharedInfrastructure(context.Background())

	// Discover services from .linespec.yml files
	if err := s.DiscoverServices(); err != nil {
		return fmt.Errorf("failed to discover services: %w", err)
	}

	// Apply container naming from the discovered service config so that user-configured
	// values (e.g. network_alias) override the defaults set in NewTestSuite.
	s.applyContainerNamingFromConfig()

	// Create shared network
	_, err := s.orch.CreateNetwork(ctx, s.networkName)
	if err != nil {
		return fmt.Errorf("failed to create network: %w", err)
	}

	// Only start shared MySQL if there are MySQL services configured
	if s.hasMySQLServices() {
		// Start shared MySQL
		// Find init.sql from discovered services or fallback to common locations
		initSqlPath := s.FindInitScript()

		var binds []string
		if initSqlPath != "" {
			// Support custom init script filenames
			initScriptName := filepath.Base(initSqlPath)
			binds = []string{fmt.Sprintf("%s:/docker-entrypoint-initdb.d/%s", initSqlPath, initScriptName)}
		}

		// Get database configuration from first MySQL service or use defaults
		dbConfig := s.getSharedDatabaseConfig()

		if err = s.orch.EnsureImage(ctx, "mysql:8.4"); err != nil {
			return fmt.Errorf("failed to pull MySQL image: %w", err)
		}
		_, err = s.orch.StartContainer(ctx, &container.Config{
			Image: "mysql:8.4",
			Env: []string{
				"MYSQL_ROOT_PASSWORD=rootpassword",
				fmt.Sprintf("MYSQL_DATABASE=%s", dbConfig.Database),
				fmt.Sprintf("MYSQL_USER=%s", dbConfig.Username),
				fmt.Sprintf("MYSQL_PASSWORD=%s", dbConfig.Password),
			},
		}, &container.HostConfig{
			Binds: binds,
			PortBindings: map[nat.Port][]nat.PortBinding{
				"3306/tcp": {{HostIP: "0.0.0.0", HostPort: "0"}},
			},
		}, &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{s.networkName: {Aliases: []string{s.containerNaming.NetworkAlias}}},
		}, s.containerNaming.DatabaseContainer)
		if err != nil {
			return fmt.Errorf("failed to start MySQL: %w", err)
		}

		logger.Debug("Waiting for shared DB to be ready")
		// Get host port for direct connection from host (with retry)
		s.dbHostPort, err = s.waitForContainerPort(ctx, s.containerNaming.DatabaseContainer, "3306/tcp", 30*time.Second)
		if err != nil {
			return fmt.Errorf("failed to get shared DB host port: %w", err)
		}
		if err := s.orch.WaitTCPInternal(ctx, s.networkName, "localhost:"+s.dbHostPort, 60*time.Second); err != nil {
			return fmt.Errorf("shared DB not ready: %w", err)
		}

		// Additional wait for MySQL to fully initialize and accept connections
		// Use actual MySQL ping to verify readiness instead of fixed delays
		logger.Debug("Verifying MySQL is ready")
		if err := s.waitForMySQL(ctx, "localhost", s.dbHostPort, dbConfig.Username, dbConfig.Password, dbConfig.Database, 30*time.Second); err != nil {
			return fmt.Errorf("MySQL not accepting connections: %w", err)
		}
		logger.Debug("MySQL is ready")

		// Wait for init.sql to complete (if provided)
		if initSqlPath != "" {
			if err := s.waitForDBInit(ctx); err != nil {
				return fmt.Errorf("failed waiting for DB init: %w", err)
			}
		}
	} else {
		logger.Debug("No MySQL services found, skipping shared MySQL infrastructure")
	}

	// Run migrations for all discovered services based on their framework
	logger.Debug("Running migrations for discovered services")
	for serviceName, cfg := range s.serviceConfigs {
		serviceDir := cfg.BaseDir
		if serviceDir == "" {
			serviceDir = filepath.Join(s.cwd, serviceName)
		}
		if err := s.runMigrationsForConfig(ctx, cfg, serviceName, serviceDir); err != nil {
			logger.Debug("Failed to run migrations for %s: %v", serviceName, err)
			// Continue with other services, don't fail completely
		}
	}
	logger.Debug("Migrations complete")

	// Fetch schema for all discovered tables after migrations complete.
	// Only applies to the shared MySQL database; PostgreSQL is per-service and handled in RunSpec.
	// Schema is cached to .linespec/schema-cache.json keyed on db config hash to skip re-fetching
	// on warm runs when the database and config haven't changed.
	dbConfig := s.getSharedDatabaseConfig()
	if dbConfig != nil && s.dbHostPort != "" {
		schemaDiscovery := s.getSchemaDiscoveryConfig()
		tables, err := s.discoverTables(ctx, dbConfig, schemaDiscovery)
		if err != nil {
			logger.Debug("Failed to discover tables: %v", err)
		} else {
			cacheKey := s.schemaConfigHash(dbConfig, tables)
			cacheFile := filepath.Join(s.cwd, ".linespec", "schema-cache.json")
			var schemaCache SchemaCache
			if cached, ok := s.loadSchemaCache(cacheFile, cacheKey); ok {
				schemaCache = cached
				logger.Debug("Schema loaded from cache (%d tables)", len(schemaCache))
			} else {
				schemaCache, err = s.fetchSchemaFromDatabase(ctx, tables, "localhost", s.dbHostPort,
					dbConfig.Username, dbConfig.Password, dbConfig.Database)
				if err != nil {
					logger.Debug("Failed to fetch shared schema: %v", err)
				} else {
					s.saveSchemaCache(cacheFile, cacheKey, schemaCache)
				}
			}
			if schemaCache != nil {
				if schemaData, encErr := json.Marshal(schemaCache); encErr != nil {
					logger.Debug("Failed to marshal schema: %v", encErr)
				} else {
					s.sharedSchemaJSON = schemaData
					logger.Debug("Shared schema cached (%d tables, %d bytes JSON)", len(schemaCache), len(s.sharedSchemaJSON))
				}
			}
		}
	}

	// Start shared Kafka
	if err = s.orch.EnsureImage(ctx, "confluentinc/cp-kafka:latest"); err != nil {
		return fmt.Errorf("failed to pull Kafka image: %w", err)
	}
	_, err = s.orch.StartContainer(ctx, &container.Config{
		Image:    "confluentinc/cp-kafka:latest",
		Hostname: "kafka",
		Env: []string{
			"KAFKA_BROKER_ID=1",
			"KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=PLAINTEXT:PLAINTEXT,PLAINTEXT_HOST:PLAINTEXT,CONTROLLER:PLAINTEXT",
			"KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://kafka:29092,PLAINTEXT_HOST://localhost:9092",
			"KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1",
			"KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1",
			"KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1",
			"KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS=0",
			"KAFKA_AUTO_CREATE_TOPICS_ENABLE=true",
			"KAFKA_PROCESS_ROLES=broker,controller",
			"KAFKA_NODE_ID=1",
			"KAFKA_CONTROLLER_QUORUM_VOTERS=1@kafka:29093",
			"KAFKA_LISTENERS=PLAINTEXT://kafka:29092,CONTROLLER://kafka:29093,PLAINTEXT_HOST://0.0.0.0:9092",
			"KAFKA_INTER_BROKER_LISTENER_NAME=PLAINTEXT",
			"KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER",
			// cp-kafka 7.x+ KRaft mode requires a base64url-encoded UUID (22 chars, RawURLEncoding)
			"CLUSTER_ID=" + kafkaClusterID(),
		},
	}, &container.HostConfig{
		PortBindings: map[nat.Port][]nat.PortBinding{
			"9092/tcp":  {{HostIP: "0.0.0.0", HostPort: "0"}},
			"29092/tcp": {{HostIP: "0.0.0.0", HostPort: "0"}},
		},
	}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{s.networkName: {Aliases: []string{"kafka"}}},
	}, s.containerNaming.GetKafkaContainer(config.ContainerNameParams{}))
	if err != nil {
		return fmt.Errorf("failed to start Kafka: %w", err)
	}

	// Get Kafka host port for direct connection from host (with retry)
	// Use port 9092 (PLAINTEXT_HOST, 0.0.0.0): cp-kafka EXPOSEs 9092 in its Dockerfile
	// so it reliably appears in NetworkSettings.Ports. Port 29092 is the internal broker
	// listener bound to the container hostname, not 0.0.0.0, so it is not reliably
	// published by Docker's NAT. Services inside the Docker network still use kafka:29092.
	kafkaHostPort, err := s.waitForContainerPort(ctx, s.containerNaming.GetKafkaContainer(config.ContainerNameParams{}), "9092/tcp", 30*time.Second)
	if err != nil {
		return fmt.Errorf("failed to get Kafka host port: %w", err)
	}

	// Wait for Kafka to be ready (actual TCP connection check)
	logger.Debug("Waiting for Kafka to be ready")
	if err := s.orch.WaitTCPInternal(ctx, s.networkName, "localhost:"+kafkaHostPort, 60*time.Second); err != nil {
		return fmt.Errorf("kafka not ready: %w", err)
	}
	s.kafkaReady = true

	logger.Debug("Shared infrastructure ready")
	return nil
}

// getSharedDatabaseConfig returns the first MySQL database config found across all
// discovered services, or nil if no MySQL database is configured.
func (s *TestSuite) getSharedDatabaseConfig() *config.DatabaseConfig {
	for _, cfg := range s.serviceConfigs {
		for i := range cfg.Databases {
			if cfg.Databases[i].Type == "mysql" {
				return &cfg.Databases[i]
			}
		}
	}
	return nil
}

// hasMySQLServices returns true if any discovered service has a MySQL database configured.
func (s *TestSuite) hasMySQLServices() bool {
	for _, cfg := range s.serviceConfigs {
		for _, db := range cfg.Databases {
			if db.Type == "mysql" {
				return true
			}
		}
	}
	return false
}

// applyContainerNamingFromConfig overrides the suite's containerNaming with the first discovered
// service config that has ContainerNaming set, so user-configured values (e.g. network_alias)
// take effect. networkName is kept in sync since it is cached separately on the suite.
func (s *TestSuite) applyContainerNamingFromConfig() {
	for _, cfg := range s.serviceConfigs {
		if cfg.ContainerNaming != nil {
			s.containerNaming = cfg.ContainerNaming
			s.networkName = cfg.ContainerNaming.NetworkName
			return
		}
	}
}

// getSchemaDiscoveryConfig returns the SchemaDiscoveryConfig from the first service that has one,
// defaulting to auto mode.
func (s *TestSuite) getSchemaDiscoveryConfig() *config.SchemaDiscoveryConfig {
	for _, cfg := range s.serviceConfigs {
		if cfg.SchemaDiscovery != nil {
			return cfg.SchemaDiscovery
		}
	}
	return &config.SchemaDiscoveryConfig{Mode: "auto"}
}

// discoverTables returns the list of tables to fetch schema for, respecting SchemaDiscoveryConfig.
func (s *TestSuite) discoverTables(ctx context.Context, dbConfig *config.DatabaseConfig, schemaDiscovery *config.SchemaDiscoveryConfig) ([]string, error) {
	if schemaDiscovery == nil || schemaDiscovery.Mode == "none" {
		return []string{}, nil
	}
	if schemaDiscovery.Mode == "static" {
		return schema.FilterExcluded(schemaDiscovery.Tables, schemaDiscovery.ExcludeTables), nil
	}
	// auto mode — query the live MySQL database
	if dbConfig == nil || s.dbHostPort == "" {
		return []string{}, nil
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
		dbConfig.Username, dbConfig.Password, "localhost", s.dbHostPort, dbConfig.Database)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database for table discovery: %w", err)
	}
	defer db.Close()

	discoverer := schema.NewAutoDiscoverer(db, "mysql", schemaDiscovery.ExcludeTables)
	tables, err := discoverer.DiscoverTables()
	if err != nil {
		logger.Debug("Failed to auto-discover tables: %v", err)
		return []string{}, nil
	}
	return tables, nil
}

type schemaCacheFile struct {
	Hash   string      `json:"hash"`
	Schema SchemaCache `json:"schema"`
}

// schemaConfigHash returns a hex hash of the db config and table list, used to key the schema cache.
func (s *TestSuite) schemaConfigHash(dbConfig *config.DatabaseConfig, tables []string) string {
	h := sha256.New()
	h.Write([]byte(dbConfig.Host + dbConfig.Username + dbConfig.Password + dbConfig.Database))
	for _, t := range tables {
		h.Write([]byte(t))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// loadSchemaCache reads the schema cache file and returns the cached schema if the hash matches.
func (s *TestSuite) loadSchemaCache(path, hash string) (SchemaCache, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var cf schemaCacheFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil, false
	}
	if cf.Hash != hash {
		return nil, false
	}
	return cf.Schema, true
}

// saveSchemaCache writes the schema and its config hash to the cache file.
func (s *TestSuite) saveSchemaCache(path, hash string, sc SchemaCache) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		logger.Debug("Failed to create schema cache dir: %v", err)
		return
	}
	cf := schemaCacheFile{Hash: hash, Schema: sc}
	data, err := json.Marshal(cf)
	if err != nil {
		logger.Debug("Failed to marshal schema cache: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		logger.Debug("Failed to write schema cache: %v", err)
	}
	logger.Debug("Schema cache written to %s", path)
}

// persistenceKey returns a stable key for a service config used to look up persistent containers.
func persistenceKey(serviceConfig *config.LineSpecConfig) string {
	return serviceConfig.BaseDir
}

// commonAncestor returns the deepest directory that is a prefix of both a and b.
// Both paths should be absolute. If one is not under the other, walks up until a
// shared ancestor is found (worst case: filesystem root).
func commonAncestor(a, b string) string {
	// Normalize
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	for {
		rel, err := filepath.Rel(a, b)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return a
		}
		parent := filepath.Dir(a)
		if parent == a {
			// Reached root
			return a
		}
		a = parent
	}
}

// canUsePersistentContainers returns true when the suite can keep containers alive across
// tests for this spec. Kafka consumer and Job tests are excluded because their trigger
// mechanism (seeding + polling) requires a fresh interceptor per test.
func canUsePersistentContainers(spec *types.TestSpec) bool {
	return spec.Receive.Channel != types.Event && spec.Receive.Channel != types.Job
}

// reloadProxy POSTs registry bytes to a proxy sidecar's /reload-registry endpoint.
func (r *testRunner) reloadProxy(ctx context.Context, sidecarAddr string, regBytes []byte) error {
	url := "http://" + sidecarAddr + "/reload-registry"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(regBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("reload-registry request failed: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("reload-registry returned %d", resp.StatusCode)
	}
	return nil
}

// truncateMySQLTables truncates all user tables in the shared MySQL database,
// excluding schema_migrations, to reset data state between tests.
func (s *TestSuite) truncateMySQLTables(ctx context.Context, dbConfig *config.DatabaseConfig, hostPort string) error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?parseTime=true",
		dbConfig.Username, dbConfig.Password, hostPort, dbConfig.Database)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("failed to open MySQL for truncation: %w", err)
	}
	defer db.Close()

	schemaDiscovery := &config.SchemaDiscoveryConfig{
		Mode:          "auto",
		ExcludeTables: []string{"schema_migrations"},
	}
	tables, err := s.discoverTables(ctx, dbConfig, schemaDiscovery)
	if err != nil {
		return fmt.Errorf("failed to discover tables for truncation: %w", err)
	}
	if len(tables) == 0 {
		return nil
	}

	if _, err := db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0"); err != nil {
		return err
	}
	for _, table := range tables {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE `%s`", table)); err != nil {
			logger.Debug("Failed to truncate table %s: %v", table, err)
		}
	}
	_, err = db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=1")
	return err
}

// truncatePostgreSQLTables truncates all user tables in the given PostgreSQL database,
// excluding schema_migrations, using CASCADE to handle foreign keys.
func (s *TestSuite) truncatePostgreSQLTables(ctx context.Context, dbConfig *config.DatabaseConfig, hostPort string) error {
	dsn := fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		dbConfig.Username, dbConfig.Password, hostPort, dbConfig.Database)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("failed to open PostgreSQL for truncation: %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx,
		"SELECT tablename FROM pg_tables WHERE schemaname='public' AND tablename NOT IN ('schema_migrations')")
	if err != nil {
		return fmt.Errorf("failed to list PostgreSQL tables: %w", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err == nil {
			tables = append(tables, t)
		}
	}
	if len(tables) == 0 {
		return nil
	}
	for _, table := range tables {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table)); err != nil {
			logger.Debug("Failed to truncate PostgreSQL table %s: %v", table, err)
		}
	}
	return nil
}

// prepareForReuse reloads registries on all running proxy sidecars and truncates
// the database, resetting state so the persistent containers are clean for the next test.
func (r *testRunner) prepareForReuse(ctx context.Context, pc *persistentServiceContainers, serviceConfig *config.LineSpecConfig) error {
	regBytes, err := r.registry.ToBytesForContainer(r.projectRoot, r.suite.containerNaming.GetProjectMountPath())
	if err != nil {
		return fmt.Errorf("failed to serialise registry: %w", err)
	}

	// Reload proxy registries in parallel
	type reloadTarget struct {
		addr string
		name string
	}
	var targets []reloadTarget
	for host, vp := range pc.dbVerifyPorts {
		targets = append(targets, reloadTarget{"localhost:" + vp, "db proxy (" + host + ")"})
	}
	if pc.httpVerifyPort != "" {
		targets = append(targets, reloadTarget{"localhost:" + pc.httpVerifyPort, "HTTP proxy"})
	}
	if pc.grpcVerifyPort != "" {
		targets = append(targets, reloadTarget{"localhost:" + pc.grpcVerifyPort, "gRPC proxy"})
	}
	if pc.redisVerifyPort != "" {
		targets = append(targets, reloadTarget{"localhost:" + pc.redisVerifyPort, "Redis proxy"})
	}

	reloadErrs := make([]error, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(idx int, tgt reloadTarget) {
			defer wg.Done()
			reloadErrs[idx] = r.reloadProxy(ctx, tgt.addr, regBytes)
		}(i, t)
	}
	wg.Wait()
	for i, err := range reloadErrs {
		if err != nil {
			return fmt.Errorf("failed to reload %s: %w", targets[i].name, err)
		}
	}

	// Truncate all configured databases
	for host, dbType := range pc.dbTypes {
		hostPort := pc.dbHostPorts[host]
		// Find the matching DatabaseConfig by host alias.
		var dbCfg *config.DatabaseConfig
		for i := range serviceConfig.Databases {
			if serviceConfig.Databases[i].Host == host {
				dbCfg = &serviceConfig.Databases[i]
				break
			}
		}
		if dbCfg == nil {
			continue
		}
		switch dbType {
		case "mysql":
			if err := r.suite.truncateMySQLTables(ctx, dbCfg, hostPort); err != nil {
				logger.Debug("Failed to truncate MySQL tables (%s): %v", host, err)
			}
		case "postgresql":
			if err := r.suite.truncatePostgreSQLTables(ctx, dbCfg, hostPort); err != nil {
				logger.Debug("Failed to truncate PostgreSQL tables (%s): %v", host, err)
			}
		case "mongodb":
			if err := r.suite.truncateMongoDBCollections(ctx, dbCfg, hostPort); err != nil {
				logger.Debug("Failed to truncate MongoDB collections (%s): %v", host, err)
			}
		}
	}
	return nil
}

// isContainerHealthy returns true if the app container responds to its health endpoint.
func (s *TestSuite) isContainerHealthy(healthURL string) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(healthURL)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

// runMigrationsForConfig runs migrations for a service based on its framework config
func (s *TestSuite) runMigrationsForConfig(ctx context.Context, cfg *config.LineSpecConfig, serviceName, serviceDir string) error {
	framework := cfg.Service.Framework
	if framework == "" {
		logger.Debug("No framework specified for %s, skipping migrations", serviceName)
		return nil
	}

	// Check if migrations are enabled for this service
	if !cfg.Infrastructure.Database {
		logger.Debug("Database not enabled for %s, skipping migrations", serviceName)
		return nil
	}

	// Get framework configuration
	fwConfig := config.GetFrameworkConfig(
		framework,
		cfg.Service.StartCommand,
		cfg.Service.MigrationCommand,
		cfg.Service.NeedsWarmup,
		cfg.Service.WarmupEndpoint,
		cfg.Service.WarmupDelayMs,
	)

	// Get migration command
	migrationCmd := fwConfig.GetMigrationCommand()
	if migrationCmd == nil {
		logger.Debug("No migration command defined for framework %s, service %s", framework, serviceName)
		return nil
	}

	return s.runMigrations(ctx, serviceName, serviceDir, migrationCmd, cfg)
}

func (s *TestSuite) waitForDBInit(ctx context.Context) error {
	// Poll until we can make an actual MySQL connection
	// This confirms init.sql has completed and handles restart period
	deadline := time.Now().Add(30 * time.Second)

	// Get database configuration
	dbConfig := s.getSharedDatabaseConfig()

	// Suppress MySQL driver internal logging during polling
	mysql.SetLogger(log.New(io.Discard, "", 0))
	defer mysql.SetLogger(log.New(os.Stderr, "[mysql] ", log.Ldate|log.Ltime|log.Lshortfile))

	var attempt int
	for time.Now().Before(deadline) {
		if s.dbHostPort != "" {
			// Try to make an actual MySQL connection
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
				dbConfig.Username, dbConfig.Password, "localhost", s.dbHostPort, dbConfig.Database)
			db, err := sql.Open("mysql", dsn)
			if err == nil {
				ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
				err = db.PingContext(ctx2)
				cancel()
				db.Close()
				if err == nil {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(expBackoff(attempt, 50*time.Millisecond, 2*time.Second)):
		}
		attempt++
	}
	return fmt.Errorf("timeout waiting for DB initialization")
}

func (s *TestSuite) ResetDatabase(ctx context.Context) error {
	if s.dbHostPort == "" {
		return nil
	}

	dbConfig := s.getSharedDatabaseConfig()
	if dbConfig == nil {
		return nil
	}

	// For now, we'll just re-run init.sql by executing it via mysql client in the container
	resetSQL := fmt.Sprintf(`
SET FOREIGN_KEY_CHECKS = 0;
SELECT CONCAT('TRUNCATE TABLE ', table_name, ';') 
FROM information_schema.tables 
WHERE table_schema = '%s' AND table_type = 'BASE TABLE';
SET FOREIGN_KEY_CHECKS = 1;
`, dbConfig.Database)

	_ = resetSQL // We'll implement this if needed, for now rely on clean test data

	return nil
}

func (s *TestSuite) runMigrations(ctx context.Context, serviceName string, serviceDir string, migrationCmd []string, cfg *config.LineSpecConfig) error {
	containerName := s.containerNaming.GetMigrateContainer(config.ContainerNameParams{ServiceName: serviceName})

	// Clean up any existing migration container
	_ = s.orch.StopAndRemoveContainer(context.Background(), containerName)

	// Build environment variables from config
	appEnv := []string{}

	// Inject connection env vars for each configured database.
	// The migration container connects to the real DB (bypasses proxy) via the network alias.
	if cfg.Infrastructure.Database {
		for i, db := range cfg.Databases {
			isFirst := i == 0
			// During migrations there is no proxy; connect directly via the real-db alias.
			realAlias := "real-" + db.Host
			namePrefix := strings.ToUpper(db.Name) + "_"

			switch db.Type {
			case "mysql":
				appEnv = append(appEnv,
					fmt.Sprintf("%sDB_HOST=%s", namePrefix, s.containerNaming.NetworkAlias),
					fmt.Sprintf("%sDB_PORT=%d", namePrefix, db.Port),
					fmt.Sprintf("%sDB_USERNAME=%s", namePrefix, db.Username),
					fmt.Sprintf("%sDB_PASSWORD=%s", namePrefix, db.Password),
				)
				if isFirst {
					appEnv = append(appEnv,
						fmt.Sprintf("DB_HOST=%s", s.containerNaming.NetworkAlias),
						fmt.Sprintf("DB_PORT=%d", db.Port),
						fmt.Sprintf("DB_USERNAME=%s", db.Username),
						fmt.Sprintf("DB_PASSWORD=%s", db.Password),
						"RAILS_ENV=development",
					)
				}
			case "postgresql":
				dbURL := fmt.Sprintf("postgresql://%s:%s@%s:%d/%s", db.Username, db.Password, realAlias, db.Port, db.Database)
				appEnv = append(appEnv, fmt.Sprintf("%sDATABASE_URL=%s", namePrefix, dbURL))
				if isFirst {
					appEnv = append(appEnv, fmt.Sprintf("DATABASE_URL=%s", dbURL))
				}
			case "mongodb":
				var mongoURI string
				if db.Username != "" && db.Password != "" {
					mongoURI = fmt.Sprintf("mongodb://%s:%s@%s:%d/%s?authSource=admin", db.Username, db.Password, realAlias, db.Port, db.Database)
				} else {
					mongoURI = fmt.Sprintf("mongodb://%s:%d/%s", realAlias, db.Port, db.Database)
				}
				appEnv = append(appEnv, fmt.Sprintf("%sMONGODB_URI=%s", namePrefix, mongoURI))
				if isFirst {
					appEnv = append(appEnv, fmt.Sprintf("MONGODB_URI=%s", mongoURI))
				}
			}
		}
	}

	// Add Kafka environment variables if enabled
	if cfg.Infrastructure.Kafka {
		appEnv = append(appEnv,
			"KAFKA_BROKERS=kafka:29092",
			"KAFKA_TOPIC=todo-events",
		)
	}

	// Add user-defined environment variables
	for k, v := range cfg.Service.Environment {
		appEnv = append(appEnv, fmt.Sprintf("%s=%s", k, v))
	}

	// Use cfg.Service.Name for the Docker image — matches the app container pattern in RunSpec.
	// The serviceName key is the directory name (e.g. "user-linespecs"), which is not the image name.
	imageName := cfg.Service.Name
	if imageName == "" {
		imageName = serviceName
	}

	_, err := s.orch.StartContainer(ctx, &container.Config{
		Image: imageName + ":latest",
		Env:   appEnv,
		Cmd:   migrationCmd,
	}, &container.HostConfig{
		AutoRemove: true,
	}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{s.networkName: {}},
	}, containerName)
	if err != nil {
		return fmt.Errorf("failed to start migration container: %w", err)
	}

	// Wait for container to complete
	statusCh, errCh := s.orch.WaitForContainer(ctx, containerName)
	select {
	case status := <-statusCh:
		if status.StatusCode != 0 {
			logger.Debug("Migrations failed with exit code %d. Fetching logs...", status.StatusCode)
			if logger.IsDebug() {
				// Stream logs to see what went wrong
				logCtx, logCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer logCancel()
				_ = s.orch.StreamLogs(logCtx, containerName, os.Stdout, os.Stderr)
			}
			return fmt.Errorf("migrations failed with exit code %d", status.StatusCode)
		}
		return nil
	case err := <-errCh:
		return fmt.Errorf("error waiting for migrations: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *TestSuite) CleanupSharedInfrastructure(ctx context.Context) {
	_ = s.orch.StopAndRemoveContainer(ctx, s.containerNaming.GetKafkaContainer(config.ContainerNameParams{}))
	_ = s.orch.StopAndRemoveContainer(ctx, s.containerNaming.GetDatabaseContainer(config.ContainerNameParams{}))

	// Stop all persistent containers (app + proxies kept alive across tests)
	s.persistentMu.Lock()
	var persistentNames []string
	for _, pc := range s.persistentContainers {
		persistentNames = append(persistentNames, pc.appName, pc.httpProxyName, pc.grpcProxyName, pc.redisProxyName)
		for _, name := range pc.dbProxies {
			persistentNames = append(persistentNames, name)
		}
		for _, name := range pc.dbContainers {
			persistentNames = append(persistentNames, name)
		}
	}
	s.persistentContainers = make(map[string]*persistentServiceContainers)
	s.persistentMu.Unlock()
	var stopWg sync.WaitGroup
	for _, name := range persistentNames {
		if name == "" {
			continue
		}
		stopWg.Add(1)
		go func(n string) {
			defer stopWg.Done()
			_ = s.orch.StopAndRemoveContainer(ctx, n)
		}(name)
	}
	stopWg.Wait()

	_ = s.orch.RemoveNetwork(ctx, s.networkName)

	// Note: We don't clean up tempDir here - it's needed for shared schema file
	// The OS will automatically clean up /tmp directories
}

func (s *TestSuite) RunTest(ctx context.Context, specPath string) error {
	r := &testRunner{
		suite:    s,
		registry: registry.NewMockRegistry(),
	}
	return r.run(ctx, specPath)
}

type testRunner struct {
	suite       *TestSuite
	registry    *registry.MockRegistry
	config      *config.LineSpecConfig
	tempDir     string                // Temp directory for registry and other test artifacts
	resolver    *interpolate.Resolver // Resolver for environment variable substitution
	projectRoot string                // Common ancestor of cwd and spec BaseDir; used as the proxy volume mount source
}

func (r *testRunner) run(ctx context.Context, specPath string) error {
	// Create resolver for environment variable substitution
	r.resolver = interpolate.NewResolver()

	// Ensure specPath is absolute so that spec.BaseDir (filepath.Dir(specPath)) is also
	// absolute. This is required for rebaseDir to correctly remap paths when the spec
	// directory is outside r.suite.cwd (e.g. ../user-linespecs/ relative to user-service/).
	if !filepath.IsAbs(specPath) {
		if abs, err := filepath.Abs(specPath); err == nil {
			specPath = abs
		}
	}

	// Load Service Configuration FIRST (before parsing, so we can populate resolver)
	specDir := filepath.Dir(specPath)
	serviceConfig, err := config.LoadConfig(specDir)
	if err != nil {
		return fmt.Errorf("failed to load service config from %s: %w", specDir, err)
	}
	r.config = serviceConfig

	// Resolve proxy image — configurable so teams can point at a private registry or
	// a pinned version without rebuilding from source on every machine.
	proxyImage := serviceConfig.Infrastructure.ProxyImage
	if proxyImage == "" {
		proxyImage = "linespec:latest"
	}

	// Populate resolver with service environment variables
	// This allows ${VAR_NAME} in .linespec files to reference values from .linespec.yml
	for k, v := range serviceConfig.Service.Environment {
		r.resolver.Variables[k] = v
	}

	// 1. Load Spec (with resolver now populated)
	tokens, err := dsl.LexFile(specPath)
	if err != nil {
		return err
	}
	parser := dsl.NewParserWithResolver(tokens, r.resolver)
	spec, err := parser.Parse(specPath)
	if err != nil {
		return err
	}
	r.registry.Register(spec)

	// Pre-scan all payload files for ${VAR} tokens and generate values now, before
	// serializing the variable map into the registry. Without this, variables that
	// appear only in RETURNS or RESPOND payload files (never in DSL fields like
	// USING_SQL or HEADERS) would be absent from the registry, causing proxy
	// containers and the runner to independently generate different random values for
	// the same variable.
	var payloadFilesToScan []string
	if spec.Receive.WithFile != "" {
		payloadFilesToScan = append(payloadFilesToScan, filepath.Join(spec.BaseDir, spec.Receive.WithFile))
	}
	for _, expect := range append(spec.Expects, spec.ExpectsNot...) {
		if expect.ReturnsFile != "" {
			payloadFilesToScan = append(payloadFilesToScan, filepath.Join(spec.BaseDir, expect.ReturnsFile))
		}
		if expect.WithFile != "" {
			payloadFilesToScan = append(payloadFilesToScan, filepath.Join(spec.BaseDir, expect.WithFile))
		}
	}
	if spec.Respond.WithFile != "" {
		payloadFilesToScan = append(payloadFilesToScan, filepath.Join(spec.BaseDir, spec.Respond.WithFile))
	}
	for _, path := range payloadFilesToScan {
		if data, err := os.ReadFile(path); err == nil {
			for _, varName := range interpolate.ExtractVariables(string(data)) {
				r.resolver.Resolve("${" + varName + "}")
			}
		}
	}

	r.registry.SetVariables(r.resolver.Variables)
	r.registry.SetVarTypes(r.resolver.VarTypes)

	// Compute the common ancestor of cwd and spec BaseDir so proxy containers can
	// access payload files that live outside the service directory (e.g. a shared
	// linespecs directory at the same level as the service).
	r.projectRoot = commonAncestor(r.suite.cwd, spec.BaseDir)

	// For Kafka consumer tests, seed the trigger payload into the registry so the
	// interceptor can serve it as a Fetch response.
	if spec.Receive.Channel == types.Event && spec.Receive.WithFile != "" {
		loader := dsl.NewPayloadLoaderWithResolver(spec.BaseDir, r.resolver)
		seedPayload, err := loader.Load(spec.Receive.WithFile)
		if err != nil {
			return fmt.Errorf("failed to load Kafka seed payload: %w", err)
		}
		seedBytes, _ := json.Marshal(seedPayload)
		r.registry.SeedTopic(spec.Receive.Topic, seedBytes)
	}

	// For Job-triggered tests, seed the backing queue based on job_backend config.
	if spec.Receive.Channel == types.Job {
		if serviceConfig.JobBackend == nil {
			return fmt.Errorf("RECEIVE JOB requires job_backend to be configured in .linespec.yml")
		}
		if spec.Receive.WithFile != "" {
			loader := dsl.NewPayloadLoaderWithResolver(spec.BaseDir, r.resolver)
			seedPayload, err := loader.Load(spec.Receive.WithFile)
			if err != nil {
				return fmt.Errorf("failed to load job seed payload: %w", err)
			}
			seedBytes, _ := json.Marshal(seedPayload)
			switch serviceConfig.JobBackend.Type {
			case "redis":
				r.registry.SeedRedisQueue(serviceConfig.JobBackend.Queue, seedBytes)
			case "kafka":
				r.registry.SeedTopic(serviceConfig.JobBackend.Queue, seedBytes)
			case "scheduled":
				// Observe-only: no seed needed.
			default:
				return fmt.Errorf("unsupported job_backend type %q (use redis, kafka, or scheduled)", serviceConfig.JobBackend.Type)
			}
		}
	}

	// Container persistence: if eligible and we have healthy persistent containers, skip setup.
	persist := canUsePersistentContainers(spec)
	serviceKey := persistenceKey(serviceConfig)
	if persist {
		r.suite.persistentMu.Lock()
		pc := r.suite.persistentContainers[serviceKey]
		r.suite.persistentMu.Unlock()
		if pc != nil {
			healthURL := fmt.Sprintf("http://localhost:%s%s", pc.appHostPort, serviceConfig.Service.HealthEndpoint)
			if r.suite.isContainerHealthy(healthURL) {
				logger.Debug("Reusing persistent containers for %s", serviceKey)
				if err := r.prepareForReuse(ctx, pc, serviceConfig); err != nil {
					logger.Debug("prepareForReuse failed, falling back to fresh start: %v", err)
					r.suite.persistentMu.Lock()
					delete(r.suite.persistentContainers, serviceKey)
					r.suite.persistentMu.Unlock()
				} else {
					// Collect all db verify ports (ordered) for runTestPhase.
					var persistedDBVerifyPorts []string
					for _, vp := range pc.dbVerifyPorts {
						if vp != "" {
							persistedDBVerifyPorts = append(persistedDBVerifyPorts, vp)
						}
					}
					return r.runTestPhase(ctx, spec, pc.appHostPort, persistedDBVerifyPorts, pc.httpVerifyPort, pc.grpcVerifyPort, pc.redisVerifyPort)
				}
			} else {
				logger.Debug("Persistent app container unhealthy, falling back to fresh start")
				r.suite.persistentMu.Lock()
				delete(r.suite.persistentContainers, serviceKey)
				r.suite.persistentMu.Unlock()
			}
		}
	} else {
		// Non-reusable test (e.g. Kafka consumer): stop any persistent containers for this
		// service that are still alive. They hold the same network aliases ("db", "grpc-proxy",
		// etc.) and Docker would round-robin connections between them and the fresh containers
		// we're about to start, causing the new app to talk to the wrong proxy.
		r.suite.persistentMu.Lock()
		pc := r.suite.persistentContainers[serviceKey]
		if pc != nil {
			delete(r.suite.persistentContainers, serviceKey)
		}
		r.suite.persistentMu.Unlock()
		if pc != nil {
			logger.Debug("Stopping persistent containers before non-reusable test for %s", serviceKey)
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Second)
			var stopWg sync.WaitGroup
			var stopNames []string
			for _, name := range pc.dbProxies {
				stopNames = append(stopNames, name)
			}
			for _, name := range pc.dbContainers {
				stopNames = append(stopNames, name)
			}
			stopNames = append(stopNames, pc.appName, pc.httpProxyName, pc.grpcProxyName, pc.redisProxyName)
			for _, name := range stopNames {
				if name == "" {
					continue
				}
				stopWg.Add(1)
				go func(n string) {
					defer stopWg.Done()
					_ = r.suite.orch.StopAndRemoveContainer(stopCtx, n)
				}(name)
			}
			stopWg.Wait()
			stopCancel()
		}
	}

	// Create temp directory for this test run
	tempDir, err := os.MkdirTemp("", "linespec-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	r.tempDir = tempDir
	logger.Debug("Created temp directory: %s", tempDir)
	defer os.RemoveAll(tempDir) // Clean up temp directory after test

	// Pre-cleanup test-specific containers in parallel
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
	params := config.ContainerNameParams{SpecName: spec.Name}
	cleanupNames := []string{
		r.suite.containerNaming.GetAppContainer(params),
		r.suite.containerNaming.GetProxyContainer(config.ContainerNameParams{SpecName: spec.Name, Type: "http"}),
		r.suite.containerNaming.GetProxyContainer(config.ContainerNameParams{SpecName: spec.Name, Type: "kafka"}),
		r.suite.containerNaming.GetProxyContainer(config.ContainerNameParams{SpecName: spec.Name, Type: "grpc"}),
		r.suite.containerNaming.GetProxyContainer(config.ContainerNameParams{SpecName: spec.Name, Type: "redis"}),
	}
	// Add per-database proxy container names (proxy alias = db.Host, used as Type in naming template).
	for _, db := range serviceConfig.Databases {
		cleanupNames = append(cleanupNames,
			r.suite.containerNaming.GetProxyContainer(config.ContainerNameParams{SpecName: spec.Name, Type: db.Host}),
		)
	}
	var cleanupWg sync.WaitGroup
	for _, name := range cleanupNames {
		cleanupWg.Add(1)
		go func(n string) {
			defer cleanupWg.Done()
			_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, n)
		}(name)
	}
	cleanupWg.Wait()
	cleanupCancel()

	// Cleanup guards: flipped to false at end of run() to keep containers alive for reuse.
	cleanupHTTPProxy := true
	cleanupGRPCProxy := true
	cleanupRedisProxy := true
	cleanupApp := true
	persistSetupComplete := false

	// Per-database cleanup guards collected during the Databases loop (flipped on persistence).
	var dbCleanupGuards []*bool

	// Per-database state accumulated during the Databases loop.
	dbProxies := make(map[string]string)    // db.Host → proxy container name
	dbContainers := make(map[string]string) // db.Host → real DB container name
	dbVerifyPortsByHost := make(map[string]string) // db.Host → verify sidecar port
	dbHostPortsMap := make(map[string]string)      // db.Host → "localhost:port" of real DB
	dbTypesMap := make(map[string]string)           // db.Host → "mysql"|"postgresql"|"mongodb"
	var dbVerifyPortsList []string                  // ordered list for runTestPhase + proxy wait

	serviceDir := filepath.Base(serviceConfig.BaseDir)
	if serviceConfig.Service.ServiceDir != "" {
		serviceDir = serviceConfig.Service.ServiceDir
	}

	// Use service.Name for Docker image name if available, otherwise fall back to serviceDir
	serviceName := serviceConfig.Service.Name
	if serviceName == "" {
		serviceName = serviceDir
	}

	appPort := fmt.Sprintf("%d", serviceConfig.Service.Port)

	// 2. Save Registry to File for Proxy Containers
	regFile := filepath.Join(r.tempDir, "registry-"+spec.Name+".json")
	_ = r.registry.SaveToFileForContainer(regFile, r.projectRoot, r.suite.containerNaming.GetProjectMountPath())

	// 3. Start Database and Proxy Containers — one per entry in serviceConfig.Databases.
	// Each database gets a unique network alias derived from db.Host.  The proxy occupies
	// alias=db.Host; the real DB occupies alias="real-"+db.Host.  For the common single-
	// database case (host defaults to "db") this reproduces the original "db"/"real-db"
	// aliases unchanged, preserving backward compatibility.
	logger.Debug("DEBUG: Infrastructure.Database=%v, len(Databases)=%d", serviceConfig.Infrastructure.Database, len(serviceConfig.Databases))
	if serviceConfig.Infrastructure.Database && len(serviceConfig.Databases) > 0 {
		for _, dbCfg := range serviceConfig.Databases {
			db := dbCfg // local copy so defers capture the right value
			dbType := db.Type
			dbPort := fmt.Sprintf("%d", db.Port)
			realAlias := "real-" + db.Host
			proxyAlias := db.Host
			proxyContainerName := r.suite.containerNaming.GetProxyContainer(config.ContainerNameParams{SpecName: spec.Name, Type: db.Host})
			dbProxies[db.Host] = proxyContainerName
			dbTypesMap[db.Host] = dbType

			logger.Debug("Setting up database: type=%s host=%s proxy=%v", dbType, db.Host, db.Proxy)

			switch dbType {
			case "postgresql":
				logger.Debug("Starting PostgreSQL database (host=%s)", db.Host)
				pgContainerName := "linespec-postgresql-" + db.Host + "-" + config.SanitizeContainerName(spec.Name)

				var pgBinds []string
				if db.InitScript != "" {
					initScriptPath := filepath.Join(serviceConfig.BaseDir, db.InitScript)
					if _, err := os.Stat(initScriptPath); err == nil {
						initScriptName := filepath.Base(initScriptPath)
						pgBinds = []string{fmt.Sprintf("%s:/docker-entrypoint-initdb.d/%s", initScriptPath, initScriptName)}
						logger.Debug("Mounting PostgreSQL init script: %s", initScriptPath)
					}
				}

				if err = r.suite.orch.EnsureImage(ctx, db.Image); err != nil {
					return fmt.Errorf("failed to pull PostgreSQL image %s: %w", db.Image, err)
				}
				_, err = r.suite.orch.StartContainer(ctx, &container.Config{
					Image: db.Image,
					Env: []string{
						fmt.Sprintf("POSTGRES_DB=%s", db.Database),
						fmt.Sprintf("POSTGRES_USER=%s", db.Username),
						fmt.Sprintf("POSTGRES_PASSWORD=%s", db.Password),
						"POSTGRES_HOST_AUTH_METHOD=trust",
					},
				}, &container.HostConfig{
					Binds: pgBinds,
					PortBindings: map[nat.Port][]nat.PortBinding{
						nat.Port(dbPort + "/tcp"): {{HostIP: "0.0.0.0", HostPort: "0"}},
					},
				}, &network.NetworkingConfig{
					EndpointsConfig: map[string]*network.EndpointSettings{r.suite.networkName: {Aliases: []string{realAlias}}},
				}, pgContainerName)
				if err != nil {
					return fmt.Errorf("failed to start PostgreSQL container (%s): %w", db.Host, err)
				}
				dbContainers[db.Host] = pgContainerName

				containerGuard := new(bool)
				*containerGuard = true
				dbCleanupGuards = append(dbCleanupGuards, containerGuard)
				localPGContainer := pgContainerName
				localContainerGuard := containerGuard
				defer func() {
					if !*localContainerGuard {
						return
					}
					cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, localPGContainer)
				}()

				logger.Debug("Waiting for PostgreSQL to be ready (host=%s)", db.Host)
				postgresHostPort, err := r.suite.waitForContainerPort(ctx, pgContainerName, dbPort+"/tcp", 30*time.Second)
				if err != nil {
					return fmt.Errorf("failed to get PostgreSQL host port (%s): %w", db.Host, err)
				}
				if err := r.suite.waitForPostgreSQL(ctx, "localhost", postgresHostPort, db.Username, db.Password, db.Database, 30*time.Second); err != nil {
					return fmt.Errorf("PostgreSQL not accepting connections (%s): %w", db.Host, err)
				}
				logger.Debug("PostgreSQL is ready (host=%s)", db.Host)
				dbHostPortsMap[db.Host] = "localhost:" + postgresHostPort

				if db.Proxy != nil && *db.Proxy {
					pgProxyCmd := []string{"proxy", "postgresql", "0.0.0.0:" + dbPort, realAlias + ":" + dbPort, r.suite.containerNaming.GetRegistryMountPath() + "/registry-" + spec.Name + ".json"}
					if logger.IsDebug() {
						pgProxyCmd = append(pgProxyCmd, "--debug")
					}
					_, err = r.suite.orch.StartContainer(ctx, &container.Config{
						Image: proxyImage,
						Cmd:   pgProxyCmd,
						ExposedPorts: map[nat.Port]struct{}{
							nat.Port(dbPort + "/tcp"): {},
							nat.Port("8081/tcp"):      {},
						},
					}, &container.HostConfig{
						Binds: []string{
							r.projectRoot + ":" + r.suite.containerNaming.GetProjectMountPath(),
							r.tempDir + ":" + r.suite.containerNaming.GetRegistryMountPath(),
						},
						PortBindings: map[nat.Port][]nat.PortBinding{
							nat.Port(dbPort + "/tcp"): {{HostIP: "0.0.0.0", HostPort: "0"}},
							nat.Port("8081/tcp"):      {{HostIP: "0.0.0.0", HostPort: "0"}},
						},
					}, &network.NetworkingConfig{
						EndpointsConfig: map[string]*network.EndpointSettings{r.suite.networkName: {Aliases: []string{proxyAlias}}},
					}, proxyContainerName)
					if err != nil {
						return fmt.Errorf("failed to start PostgreSQL proxy (%s): %w", db.Host, err)
					}
					logger.Debug("PostgreSQL proxy started (alias=%s)", proxyAlias)

					proxyGuard := new(bool)
					*proxyGuard = true
					dbCleanupGuards = append(dbCleanupGuards, proxyGuard)
					localProxyContainer := proxyContainerName
					localProxyGuard := proxyGuard
					defer func() {
						if !*localProxyGuard {
							return
						}
						cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, localProxyContainer)
					}()
					if logger.IsDebug() {
						go func() {
							logCtx, logCancel := context.WithTimeout(context.Background(), 60*time.Second)
							defer logCancel()
							_ = r.suite.orch.StreamLogs(logCtx, proxyContainerName, os.Stdout, os.Stderr)
						}()
					}
				} else {
					logger.Info("PostgreSQL proxy disabled for host=%s, mock matching will not work", db.Host)
				}

			case "mysql":
				// MySQL uses the shared persistent container started in SetupSharedInfrastructure.
				// The proxy connects to the shared container via the suite-level NetworkAlias.
				if db.Proxy != nil && *db.Proxy {
					logger.Debug("Starting MySQL proxy (host=%s)", db.Host)
					mysqlProxyCmd := []string{"proxy", "mysql", "0.0.0.0:" + dbPort, r.suite.containerNaming.NetworkAlias + ":" + dbPort, r.suite.containerNaming.GetRegistryMountPath() + "/registry-" + spec.Name + ".json", "--db-name", db.Database}
					if len(r.suite.sharedSchemaJSON) > 0 {
						schemaFile := filepath.Join(r.tempDir, "schema.json")
						if err := os.WriteFile(schemaFile, r.suite.sharedSchemaJSON, 0600); err != nil {
							logger.Debug("Failed to write schema file: %v", err)
						} else {
							mysqlProxyCmd = append(mysqlProxyCmd, "--schema-file", r.suite.containerNaming.GetRegistryMountPath()+"/schema.json")
						}
					}
					if logger.IsDebug() {
						mysqlProxyCmd = append(mysqlProxyCmd, "--debug")
					}
					_, err = r.suite.orch.StartContainer(ctx, &container.Config{
						Image: proxyImage,
						Cmd:   mysqlProxyCmd,
						ExposedPorts: map[nat.Port]struct{}{
							nat.Port("8081/tcp"): {},
						},
					}, &container.HostConfig{
						Binds: []string{
							r.projectRoot + ":" + r.suite.containerNaming.GetProjectMountPath(),
							r.tempDir + ":" + r.suite.containerNaming.GetRegistryMountPath(),
						},
						PortBindings: map[nat.Port][]nat.PortBinding{
							nat.Port("8081/tcp"): {{HostIP: "0.0.0.0", HostPort: "0"}},
						},
					}, &network.NetworkingConfig{
						EndpointsConfig: map[string]*network.EndpointSettings{r.suite.networkName: {Aliases: []string{proxyAlias}}},
					}, proxyContainerName)
					if err != nil {
						return fmt.Errorf("failed to start MySQL proxy (%s): %w", db.Host, err)
					}
					logger.Debug("MySQL proxy started (alias=%s)", proxyAlias)
					dbHostPortsMap[db.Host] = "localhost:" + r.suite.dbHostPort

					proxyGuard := new(bool)
					*proxyGuard = true
					dbCleanupGuards = append(dbCleanupGuards, proxyGuard)
					localProxyContainer := proxyContainerName
					localProxyGuard := proxyGuard
					defer func() {
						if !*localProxyGuard {
							return
						}
						cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, localProxyContainer)
					}()
					if logger.IsDebug() {
						go func() {
							logCtx, logCancel := context.WithTimeout(context.Background(), 60*time.Second)
							defer logCancel()
							_ = r.suite.orch.StreamLogs(logCtx, proxyContainerName, os.Stdout, os.Stderr)
						}()
					}
				} else {
					logger.Info("MySQL proxy disabled for host=%s, mock matching will not work", db.Host)
				}

			case "mongodb":
				logger.Debug("Starting MongoDB database (host=%s)", db.Host)
				mongoContainerName := "linespec-mongodb-" + db.Host + "-" + config.SanitizeContainerName(spec.Name)

				var mongoBinds []string
				if db.InitScript != "" {
					initScriptPath := filepath.Join(serviceConfig.BaseDir, db.InitScript)
					if _, err := os.Stat(initScriptPath); err == nil {
						initScriptName := filepath.Base(initScriptPath)
						mongoBinds = []string{fmt.Sprintf("%s:/docker-entrypoint-initdb.d/%s", initScriptPath, initScriptName)}
						logger.Debug("Mounting MongoDB init script: %s", initScriptPath)
					}
				}
				mongoEnv := []string{}
				if db.Username != "" && db.Password != "" {
					mongoEnv = append(mongoEnv,
						fmt.Sprintf("MONGO_INITDB_ROOT_USERNAME=%s", db.Username),
						fmt.Sprintf("MONGO_INITDB_ROOT_PASSWORD=%s", db.Password),
					)
				}
				if db.Database != "" {
					mongoEnv = append(mongoEnv, fmt.Sprintf("MONGO_INITDB_DATABASE=%s", db.Database))
				}

				if err = r.suite.orch.EnsureImage(ctx, db.Image); err != nil {
					return fmt.Errorf("failed to pull MongoDB image %s: %w", db.Image, err)
				}
				_, err = r.suite.orch.StartContainer(ctx, &container.Config{
					Image: db.Image,
					Env:   mongoEnv,
				}, &container.HostConfig{
					Binds: mongoBinds,
					PortBindings: map[nat.Port][]nat.PortBinding{
						nat.Port(dbPort + "/tcp"): {{HostIP: "0.0.0.0", HostPort: "0"}},
					},
				}, &network.NetworkingConfig{
					EndpointsConfig: map[string]*network.EndpointSettings{r.suite.networkName: {Aliases: []string{realAlias}}},
				}, mongoContainerName)
				if err != nil {
					return fmt.Errorf("failed to start MongoDB container (%s): %w", db.Host, err)
				}
				dbContainers[db.Host] = mongoContainerName

				containerGuard := new(bool)
				*containerGuard = true
				dbCleanupGuards = append(dbCleanupGuards, containerGuard)
				localMongoContainer := mongoContainerName
				localContainerGuard := containerGuard
				defer func() {
					if !*localContainerGuard {
						return
					}
					cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, localMongoContainer)
				}()

				logger.Debug("Waiting for MongoDB to be ready (host=%s)", db.Host)
				mongoHostPort, err := r.suite.waitForContainerPort(ctx, mongoContainerName, dbPort+"/tcp", 30*time.Second)
				if err != nil {
					return fmt.Errorf("failed to get MongoDB host port (%s): %w", db.Host, err)
				}
				if err := r.suite.waitForMongoDB(ctx, "localhost", mongoHostPort, db.Username, db.Password, db.Database, 45*time.Second); err != nil {
					return fmt.Errorf("MongoDB not accepting connections (%s): %w", db.Host, err)
				}
				logger.Debug("MongoDB is ready (host=%s)", db.Host)
				dbHostPortsMap[db.Host] = "localhost:" + mongoHostPort

				if db.Proxy != nil && *db.Proxy {
					mongoProxyCmd := []string{"proxy", "mongodb", "0.0.0.0:" + dbPort, realAlias + ":" + dbPort, r.suite.containerNaming.GetRegistryMountPath() + "/registry-" + spec.Name + ".json"}
					if logger.IsDebug() {
						mongoProxyCmd = append(mongoProxyCmd, "--debug")
					}
					_, err = r.suite.orch.StartContainer(ctx, &container.Config{
						Image: proxyImage,
						Cmd:   mongoProxyCmd,
						ExposedPorts: map[nat.Port]struct{}{
							nat.Port(dbPort + "/tcp"): {},
							nat.Port("8081/tcp"):      {},
						},
					}, &container.HostConfig{
						Binds: []string{
							r.projectRoot + ":" + r.suite.containerNaming.GetProjectMountPath(),
							r.tempDir + ":" + r.suite.containerNaming.GetRegistryMountPath(),
						},
						PortBindings: map[nat.Port][]nat.PortBinding{
							nat.Port(dbPort + "/tcp"): {{HostIP: "0.0.0.0", HostPort: "0"}},
							nat.Port("8081/tcp"):      {{HostIP: "0.0.0.0", HostPort: "0"}},
						},
					}, &network.NetworkingConfig{
						EndpointsConfig: map[string]*network.EndpointSettings{r.suite.networkName: {Aliases: []string{proxyAlias}}},
					}, proxyContainerName)
					if err != nil {
						return fmt.Errorf("failed to start MongoDB proxy (%s): %w", db.Host, err)
					}
					logger.Debug("MongoDB proxy started (alias=%s)", proxyAlias)

					proxyGuard := new(bool)
					*proxyGuard = true
					dbCleanupGuards = append(dbCleanupGuards, proxyGuard)
					localProxyContainer := proxyContainerName
					localProxyGuard := proxyGuard
					defer func() {
						if !*localProxyGuard {
							return
						}
						cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, localProxyContainer)
					}()
					if logger.IsDebug() {
						go func() {
							logCtx, logCancel := context.WithTimeout(context.Background(), 60*time.Second)
							defer logCancel()
							_ = r.suite.orch.StreamLogs(logCtx, proxyContainerName, os.Stdout, os.Stderr)
						}()
					}
				} else {
					logger.Info("MongoDB proxy disabled for host=%s, mock matching will not work", db.Host)
				}

			default:
				logger.Debug("Unknown dbType '%s' for host=%s, no proxy started", dbType, db.Host)
			}
		}
	} else {
		logger.Debug("DEBUG: Database infrastructure disabled or no database config")
	}

	// HTTP Proxy - only start when there are proxied HTTP dependencies
	httpDeps := r.getHTTPProxyDependencies(serviceConfig)
	var httpProxyContainerName string
	var httpProxyAliases []string
	// The HTTP sidecar always binds this port inside the proxy container. Using a high port
	// (19081) avoids any conflict with common service dependency ports (80, 443, 3000-3001,
	// 8080-8082, etc.), regardless of what proxyPort the dep is configured with.
	const httpSidecarContainerPort = 19081
	httpSidecarNatPort := nat.Port(fmt.Sprintf("%d/tcp", httpSidecarContainerPort))

	if len(httpDeps) > 0 {
		logger.Debug("Starting HTTP proxy for %d HTTP dependencies", len(httpDeps))

		// Build list of host aliases from dependencies
		for _, dep := range httpDeps {
			alias := dep.Name
			if dep.HostAlias != "" {
				alias = dep.HostAlias
			}
			httpProxyAliases = append(httpProxyAliases, alias)
			logger.Debug("HTTP proxy alias: %s -> %s:%d", alias, dep.Host, dep.Port)
		}

		// Determine proxy bind port from the first dep with a non-zero port, default to 80.
		// Note: one proxy container services all HTTP deps via Docker network aliases; if deps
		// have different ports, only the first declared non-zero port will be served.
		proxyPort := 80
		for _, dep := range httpDeps {
			if dep.Port != 0 {
				proxyPort = dep.Port
				break
			}
		}
		proxyAddr := fmt.Sprintf("0.0.0.0:%d", proxyPort)

		// Build HTTP proxy command with debug flag if enabled
		httpProxyCmd := []string{"proxy", "http", proxyAddr, "unused", r.suite.containerNaming.GetRegistryMountPath() + "/registry-" + spec.Name + ".json",
			fmt.Sprintf("--sidecar-port=%d", httpSidecarContainerPort)}
		if logger.IsDebug() {
			httpProxyCmd = append(httpProxyCmd, "--debug")
		}
		httpProxyContainerName = r.suite.containerNaming.GetProxyContainer(config.ContainerNameParams{SpecName: spec.Name, Type: "http"})
		_, err = r.suite.orch.StartContainer(ctx, &container.Config{
			Image: proxyImage,
			Cmd:   httpProxyCmd,
			ExposedPorts: map[nat.Port]struct{}{
				httpSidecarNatPort: {},
			},
		}, &container.HostConfig{
			Binds: []string{
				r.projectRoot + ":" + r.suite.containerNaming.GetProjectMountPath(),
				r.tempDir + ":" + r.suite.containerNaming.GetRegistryMountPath(),
			},
			PortBindings: map[nat.Port][]nat.PortBinding{
				httpSidecarNatPort: {{HostIP: "0.0.0.0", HostPort: "0"}},
			},
		}, &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{r.suite.networkName: {Aliases: httpProxyAliases}},
		}, httpProxyContainerName)
		if err != nil {
			return fmt.Errorf("failed to start HTTP proxy: %w", err)
		}
		logger.Debug("HTTP proxy container started: %s", httpProxyContainerName)
		defer func() {
			if !cleanupHTTPProxy {
				return
			}
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if logger.IsDebug() {
				logger.Debug("Fetching HTTP proxy logs before cleanup")
				_ = r.suite.orch.StreamLogs(cleanupCtx, httpProxyContainerName, os.Stdout, os.Stderr)
			}
			_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, httpProxyContainerName)
		}()
		logger.Debug("HTTP proxy started with aliases: %v", httpProxyAliases)

		// Stream HTTP proxy logs for debugging (only in debug mode)
		if logger.IsDebug() {
			go func() {
				logCtx, logCancel := context.WithTimeout(context.Background(), 120*time.Second)
				defer logCancel()
				_ = r.suite.orch.StreamLogs(logCtx, httpProxyContainerName, os.Stdout, os.Stderr)
			}()
		}
	} else {
		logger.Debug("No HTTP dependencies with proxy enabled, skipping HTTP proxy")
	}

	// Inspect all proxies to get ports and IPs
	var httpVerifyPort, proxyHttpIP string

	// Inspect each database proxy to get its verify sidecar port.
	for host, proxyName := range dbProxies {
		inspectDb, inspectDbErr := r.suite.orch.GetContainerInspect(ctx, proxyName)
		if inspectDbErr == nil && inspectDb.NetworkSettings != nil {
			if p, ok := inspectDb.NetworkSettings.Ports["8081/tcp"]; ok && len(p) > 0 {
				vp := p[0].HostPort
				dbVerifyPortsByHost[host] = vp
				dbVerifyPortsList = append(dbVerifyPortsList, vp)
			}
		}
	}

	if httpProxyContainerName != "" {
		inspectHttp, inspectHttpErr := r.suite.orch.GetContainerInspect(ctx, httpProxyContainerName)
		if inspectHttpErr == nil && inspectHttp.NetworkSettings != nil {
			if p, ok := inspectHttp.NetworkSettings.Ports[httpSidecarNatPort]; ok && len(p) > 0 {
				httpVerifyPort = p[0].HostPort
			}
			if n, ok := inspectHttp.NetworkSettings.Networks[r.suite.networkName]; ok {
				proxyHttpIP = n.IPAddress
			}
		}
	}

	// Start Kafka interceptor for consumer-triggered tests.
	// Uses a distinct alias ("kafka-proxy") so it doesn't conflict with the real Kafka
	// container on the network. The app's KAFKA_BROKERS will point to this interceptor.
	// Requires infrastructure.kafka: true, consistent with all other proxy types.
	const kafkaProxyAlias = "kafka-proxy"
	kafkaProxyContainerName := r.suite.containerNaming.GetProxyContainer(config.ContainerNameParams{SpecName: spec.Name, Type: "kafka"})
	if serviceConfig.Infrastructure.Kafka && spec.Receive.Channel == types.Event {
		kafkaProxyCmd := []string{
			"proxy", "kafka", "0.0.0.0:9092", "unused",
			r.suite.containerNaming.GetRegistryMountPath() + "/registry-" + spec.Name + ".json",
			"--host", kafkaProxyAlias,
		}
		if logger.IsDebug() {
			kafkaProxyCmd = append(kafkaProxyCmd, "--debug")
		}
		_, err = r.suite.orch.StartContainer(ctx, &container.Config{
			Image: proxyImage,
			Cmd:   kafkaProxyCmd,
		}, &container.HostConfig{
			Binds: []string{
				r.projectRoot + ":" + r.suite.containerNaming.GetProjectMountPath(),
				r.tempDir + ":" + r.suite.containerNaming.GetRegistryMountPath(),
			},
		}, &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				r.suite.networkName: {Aliases: []string{kafkaProxyAlias}},
			},
		}, kafkaProxyContainerName)
		if err != nil {
			return fmt.Errorf("failed to start Kafka interceptor: %w", err)
		}
		logger.Debug("Kafka interceptor started with alias %s", kafkaProxyAlias)
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, kafkaProxyContainerName)
		}()
		if logger.IsDebug() {
			go func() {
				logCtx, logCancel := context.WithTimeout(context.Background(), 120*time.Second)
				defer logCancel()
				_ = r.suite.orch.StreamLogs(logCtx, kafkaProxyContainerName, os.Stdout, os.Stderr)
			}()
		}
	}

	// Start gRPC interceptor when the spec has any GRPC channel expectations.
	hasGRPC := false
	for _, e := range spec.Expects {
		if e.Channel == types.GRPC {
			hasGRPC = true
			break
		}
	}
	if !hasGRPC {
		for _, e := range spec.ExpectsNot {
			if e.Channel == types.GRPC {
				hasGRPC = true
				break
			}
		}
	}
	grpcDeps := r.getGRPCProxyDependencies(serviceConfig)
	grpcProxyContainerName := r.suite.containerNaming.GetProxyContainer(config.ContainerNameParams{SpecName: spec.Name, Type: "grpc"})
	var grpcHostPort, grpcVerifyPort string
	grpcPort := 50051
	if hasGRPC || serviceConfig.Infrastructure.GRPC || len(grpcDeps) > 0 {
		var grpcProxyAliases []string
		grpcProxyAlias := "grpc-proxy"
		grpcProxyAliases = append(grpcProxyAliases, grpcProxyAlias)

		grpcUpstream := "unused"
		for _, dep := range grpcDeps {
			alias := dep.Name
			if dep.HostAlias != "" {
				alias = dep.HostAlias
			}
			grpcProxyAliases = append(grpcProxyAliases, alias)
			if dep.Port != 0 {
				grpcPort = dep.Port
			}
			if dep.Host != "" && grpcUpstream == "unused" {
				grpcUpstream = fmt.Sprintf("%s:%d", dep.Host, dep.Port)
				if dep.Port == 0 {
					grpcUpstream = dep.Host
				}
			}
		}

		grpcAddr := fmt.Sprintf("0.0.0.0:%d", grpcPort)
		grpcProxyCmd := []string{
			"proxy", "grpc", grpcAddr, grpcUpstream,
			r.suite.containerNaming.GetRegistryMountPath() + "/registry-" + spec.Name + ".json",
		}

		mergedDescriptorPath, descriptorErr := r.mergeGRPCDescriptorSets(serviceConfig, r.projectRoot)
		if descriptorErr != nil {
			return fmt.Errorf("failed to merge gRPC descriptor sets: %w", descriptorErr)
		}
		if mergedDescriptorPath != "" {
			containerDescriptorPath := r.suite.containerNaming.GetProjectMountPath() + "/" + mergedDescriptorPath
			if strings.HasPrefix(mergedDescriptorPath, r.tempDir) {
				containerDescriptorPath = r.suite.containerNaming.GetRegistryMountPath() + "/" + filepath.Base(mergedDescriptorPath)
			} else if strings.HasPrefix(mergedDescriptorPath, r.projectRoot) {
				rel, _ := filepath.Rel(r.projectRoot, mergedDescriptorPath)
				containerDescriptorPath = r.suite.containerNaming.GetProjectMountPath() + "/" + rel
			}
			grpcProxyCmd = append(grpcProxyCmd, "--grpc-descriptor-set="+containerDescriptorPath)
		}

		if logger.IsDebug() {
			grpcProxyCmd = append(grpcProxyCmd, "--debug")
		}
		_, err = r.suite.orch.StartContainer(ctx, &container.Config{
			Image: proxyImage,
			Cmd: grpcProxyCmd,
			ExposedPorts: map[nat.Port]struct{}{
				nat.Port(fmt.Sprintf("%d/tcp", grpcPort)): {},
				nat.Port("8081/tcp"):                      {},
			},
		}, &container.HostConfig{
			Binds: []string{
				r.projectRoot + ":" + r.suite.containerNaming.GetProjectMountPath(),
				r.tempDir + ":" + r.suite.containerNaming.GetRegistryMountPath(),
			},
			PortBindings: map[nat.Port][]nat.PortBinding{
				nat.Port(fmt.Sprintf("%d/tcp", grpcPort)): {{HostIP: "0.0.0.0", HostPort: "0"}},
				nat.Port("8081/tcp"):                      {{HostIP: "0.0.0.0", HostPort: "0"}},
			},
		}, &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				r.suite.networkName: {Aliases: grpcProxyAliases},
			},
		}, grpcProxyContainerName)
		if err != nil {
			return fmt.Errorf("failed to start gRPC interceptor: %w", err)
		}
		logger.Debug("gRPC interceptor started with aliases %v (upstream: %s)", grpcProxyAliases, grpcUpstream)
		if logger.IsDebug() {
			go func() {
				logCtx, logCancel := context.WithTimeout(context.Background(), 120*time.Second)
				defer logCancel()
				_ = r.suite.orch.StreamLogs(logCtx, grpcProxyContainerName, os.Stdout, os.Stderr)
			}()
		}
		defer func() {
			if !cleanupGRPCProxy {
				return
			}
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if logger.IsDebug() {
				logger.Debug("Fetching gRPC proxy logs before cleanup")
				_ = r.suite.orch.StreamLogs(cleanupCtx, grpcProxyContainerName, os.Stdout, os.Stderr)
			}
			_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, grpcProxyContainerName)
		}()
		grpcNatPort := nat.Port(fmt.Sprintf("%d/tcp", grpcPort))
		if inspectGRPC, err := r.suite.orch.GetContainerInspect(ctx, grpcProxyContainerName); err == nil && inspectGRPC.NetworkSettings != nil {
			if p, ok := inspectGRPC.NetworkSettings.Ports[grpcNatPort]; ok && len(p) > 0 {
				grpcHostPort = p[0].HostPort
			}
		if p, ok := inspectGRPC.NetworkSettings.Ports["8081/tcp"]; ok && len(p) > 0 {
			grpcVerifyPort = p[0].HostPort
		}
	}
	}

	// Start Redis interceptor when infrastructure.redis is enabled.
	const redisProxyAlias = "redis-proxy"
	redisProxyContainerName := r.suite.containerNaming.GetProxyContainer(config.ContainerNameParams{SpecName: spec.Name, Type: "redis"})
	var redisHostPort, redisVerifyPort string
	if serviceConfig.Infrastructure.Redis {
		redisProxyCmd := []string{
			"proxy", "redis", "0.0.0.0:6379", "unused",
			r.suite.containerNaming.GetRegistryMountPath() + "/registry-" + spec.Name + ".json",
		}
		if logger.IsDebug() {
			redisProxyCmd = append(redisProxyCmd, "--debug")
		}
		_, err = r.suite.orch.StartContainer(ctx, &container.Config{
			Image: proxyImage,
			Cmd:   redisProxyCmd,
			ExposedPorts: map[nat.Port]struct{}{
				nat.Port("6379/tcp"): {},
				nat.Port("8081/tcp"): {},
			},
		}, &container.HostConfig{
			Binds: []string{
				r.projectRoot + ":" + r.suite.containerNaming.GetProjectMountPath(),
				r.tempDir + ":" + r.suite.containerNaming.GetRegistryMountPath(),
			},
			PortBindings: map[nat.Port][]nat.PortBinding{
				nat.Port("6379/tcp"): {{HostIP: "0.0.0.0", HostPort: "0"}},
				nat.Port("8081/tcp"): {{HostIP: "0.0.0.0", HostPort: "0"}},
			},
		}, &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				r.suite.networkName: {Aliases: []string{redisProxyAlias}},
			},
		}, redisProxyContainerName)
		if err != nil {
			return fmt.Errorf("failed to start Redis interceptor: %w", err)
		}
		logger.Debug("Redis interceptor started with alias %s", redisProxyAlias)
		if logger.IsDebug() {
			go func() {
				logCtx, logCancel := context.WithTimeout(context.Background(), 120*time.Second)
				defer logCancel()
				_ = r.suite.orch.StreamLogs(logCtx, redisProxyContainerName, os.Stdout, os.Stderr)
			}()
		}
		defer func() {
			if !cleanupRedisProxy {
				return
			}
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if logger.IsDebug() {
				logger.Debug("Fetching Redis proxy logs before cleanup")
				_ = r.suite.orch.StreamLogs(cleanupCtx, redisProxyContainerName, os.Stdout, os.Stderr)
			}
			_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, redisProxyContainerName)
		}()
		if inspectRedis, err := r.suite.orch.GetContainerInspect(ctx, redisProxyContainerName); err == nil && inspectRedis.NetworkSettings != nil {
			if p, ok := inspectRedis.NetworkSettings.Ports["6379/tcp"]; ok && len(p) > 0 {
				redisHostPort = p[0].HostPort
			}
			if p, ok := inspectRedis.NetworkSettings.Ports["8081/tcp"]; ok && len(p) > 0 {
				redisVerifyPort = p[0].HostPort
			}
		}
	}

	// Wait for services to be ready on the network (in parallel)
	logger.Debug("Waiting for proxies to be ready")
	type proxyWait struct {
		addr string
		name string
	}
	var proxyWaits []proxyWait
	for host, vp := range dbVerifyPortsByHost {
		proxyWaits = append(proxyWaits, proxyWait{"localhost:" + vp, "database proxy (" + host + ")"})
	}
	if httpVerifyPort != "" {
		proxyWaits = append(proxyWaits, proxyWait{"localhost:" + httpVerifyPort, "HTTP proxy"})
	}
	if grpcHostPort != "" {
		proxyWaits = append(proxyWaits, proxyWait{"localhost:" + grpcHostPort, "gRPC proxy"})
	}
	if redisHostPort != "" {
		proxyWaits = append(proxyWaits, proxyWait{"localhost:" + redisHostPort, "Redis proxy"})
	}
	if len(proxyWaits) > 0 {
		proxyErrs := make([]error, len(proxyWaits))
		var proxyWg sync.WaitGroup
		for i, pw := range proxyWaits {
			proxyWg.Add(1)
			go func(idx int, w proxyWait) {
				defer proxyWg.Done()
				proxyErrs[idx] = r.suite.orch.WaitTCPInternal(ctx, r.suite.networkName, w.addr, 30*time.Second)
			}(i, pw)
		}
		proxyWg.Wait()
		for i, err := range proxyErrs {
			if err != nil {
				return fmt.Errorf("%s not ready: %w", proxyWaits[i].name, err)
			}
		}
	}

	// 4. Start SUT
	// Build environment variables based on config with proper precedence handling

	// Add database environment variables if enabled
	// Build environment variables using a map for proper precedence handling
	// Precedence (highest to lowest):
	// 1. Generated vars (from resolver - must match test expectations)
	// 2. User-defined config vars
	// 3. Auto-generated dependency URLs (if not overridden by user)
	// 4. Infrastructure defaults (database, kafka)
	envMap := make(map[string]string)

	// 4. Add infrastructure defaults first (lowest priority).
	// For each database, inject connection env vars.  The first database in the list also
	// receives the legacy unprefixed names (DB_HOST, DATABASE_URL, MONGODB_URI) so that
	// single-database configs continue to work without any changes.
	if serviceConfig.Infrastructure.Database {
		for i, db := range serviceConfig.Databases {
			isFirst := i == 0
			// Determine the hostname the app should use (proxy alias when proxied).
			dbHost := "real-" + db.Host // direct connection to real container
			if db.Proxy != nil && *db.Proxy {
				dbHost = db.Host // connect to proxy sidecar
			}
			namePrefix := strings.ToUpper(db.Name) + "_"

			switch db.Type {
			case "mysql":
				if db.Proxy != nil && *db.Proxy {
					logger.Debug("MySQL proxy enabled: app will connect to '%s' (proxy)", dbHost)
				} else {
					logger.Debug("MySQL proxy disabled: app will connect directly to '%s'", dbHost)
				}
				envMap[namePrefix+"DB_HOST"] = dbHost
				envMap[namePrefix+"DB_PORT"] = fmt.Sprintf("%d", db.Port)
				envMap[namePrefix+"DB_USERNAME"] = db.Username
				envMap[namePrefix+"DB_PASSWORD"] = db.Password
				if isFirst {
					envMap["DB_HOST"] = dbHost
					envMap["DB_PORT"] = fmt.Sprintf("%d", db.Port)
					envMap["DB_USERNAME"] = db.Username
					envMap["DB_PASSWORD"] = db.Password
					envMap["RAILS_ENV"] = "development"
				}
			case "postgresql":
				if db.Proxy != nil && *db.Proxy {
					logger.Debug("PostgreSQL proxy enabled: app will connect to '%s' (proxy)", dbHost)
				} else {
					logger.Debug("PostgreSQL proxy disabled: app will connect directly to '%s'", dbHost)
				}
				dbURL := fmt.Sprintf("postgresql://%s:%s@%s:%d/%s", db.Username, db.Password, dbHost, db.Port, db.Database)
				envMap[namePrefix+"DATABASE_URL"] = dbURL
				if isFirst {
					envMap["DATABASE_URL"] = dbURL
				}
			case "mongodb":
				if db.Proxy != nil && *db.Proxy {
					logger.Debug("MongoDB proxy enabled: app will connect to '%s' (proxy)", dbHost)
				} else {
					logger.Debug("MongoDB proxy disabled: app will connect directly to '%s'", dbHost)
				}
				var mongoURI string
				if db.Username != "" && db.Password != "" {
					mongoURI = fmt.Sprintf("mongodb://%s:%s@%s:%d/%s?authSource=admin", db.Username, db.Password, dbHost, db.Port, db.Database)
				} else {
					mongoURI = fmt.Sprintf("mongodb://%s:%d/%s", dbHost, db.Port, db.Database)
				}
				envMap[namePrefix+"MONGODB_URI"] = mongoURI
				if isFirst {
					envMap["MONGODB_URI"] = mongoURI
				}
			}
		}
	}

	if serviceConfig.Infrastructure.Kafka {
		envMap["KAFKA_BROKERS"] = "kafka:29092"
	}

	if hasGRPC || serviceConfig.Infrastructure.GRPC || len(grpcDeps) > 0 {
		envMap["GRPC_HOST"] = "grpc-proxy"
		envMap["GRPC_PORT"] = fmt.Sprintf("%d", grpcPort)
		for _, dep := range grpcDeps {
			alias := dep.Name
			if dep.HostAlias != "" {
				alias = dep.HostAlias
			}
			envVarPrefix := strings.ToUpper(strings.ReplaceAll(dep.Name, "-", "_"))
			depPort := dep.Port
			if depPort == 0 {
				depPort = 50051
			}
			if _, exists := serviceConfig.Service.Environment[envVarPrefix+"_HOST"]; !exists {
				envMap[envVarPrefix+"_HOST"] = alias
			}
			if _, exists := serviceConfig.Service.Environment[envVarPrefix+"_PORT"]; !exists {
				envMap[envVarPrefix+"_PORT"] = fmt.Sprintf("%d", depPort)
			}
			logger.Debug("Added gRPC dependency env: %s_HOST=%s, %s_PORT=%d", envVarPrefix, alias, envVarPrefix, depPort)
		}
	}

	if serviceConfig.Infrastructure.Redis {
		envMap["REDIS_URL"] = "redis://" + redisProxyAlias + ":6379"
		envMap["REDIS_HOST"] = redisProxyAlias
		envMap["REDIS_PORT"] = "6379"
	}

	// 3. Add auto-generated HTTP dependency URLs (if not in user config)
	for _, dep := range httpDeps {
		alias := dep.Name
		if dep.HostAlias != "" {
			alias = dep.HostAlias
		}
		envVarName := strings.ToUpper(strings.ReplaceAll(dep.Name, "-", "_")) + "_URL"
		if _, exists := serviceConfig.Service.Environment[envVarName]; !exists {
			depPort := dep.Port
			if depPort == 0 {
				depPort = 80
			}
			envMap[envVarName] = fmt.Sprintf("http://%s:%d", alias, depPort)
			logger.Debug("Added dependency URL: %s=http://%s:%d", envVarName, alias, depPort)
		}
	}

	// 2. Add user-defined environment variables (override defaults)
	for k, v := range serviceConfig.Service.Environment {
		if strings.Contains(v, "{{proxy_http_ip}}") {
			v = strings.ReplaceAll(v, "{{proxy_http_ip}}", proxyHttpIP)
		}
		envMap[k] = v
	}

	// 1.5. Override KAFKA_BROKERS for consumer tests — must beat user-defined config since
	// the interceptor must receive Fetch requests regardless of what the service config sets.
	if serviceConfig.Infrastructure.Kafka && spec.Receive.Channel == types.Event {
		envMap["KAFKA_BROKERS"] = kafkaProxyAlias + ":9092"
	}

	// 1. Add generated environment variables from resolver (highest priority)
	generatedEnv := r.resolver.GetGeneratedEnv()
	for _, env := range generatedEnv {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	// Convert map to slice
	appEnv := make([]string, 0, len(envMap))
	for k, v := range envMap {
		appEnv = append(appEnv, fmt.Sprintf("%s=%s", k, v))
	}

	if len(generatedEnv) > 0 {
		logger.Debug("Injecting %d generated environment variables", len(generatedEnv))
	}

	// Build extra hosts from HTTP proxy dependencies
	extraHosts := []string{}
	if proxyHttpIP != "" {
		for _, dep := range httpDeps {
			alias := dep.Name
			if dep.HostAlias != "" {
				alias = dep.HostAlias
			}
			extraHosts = append(extraHosts, alias+":"+proxyHttpIP)
		}
	}

	// Determine start command based on framework and config
	var startCmd []string
	if serviceConfig.Service.StartCommand != "" {
		// Use custom start command from config
		startCmd = []string{"sh", "-c", serviceConfig.Service.StartCommand}
	} else {
		// Get framework configuration and start command
		fwConfig := config.GetFrameworkConfig(
			serviceConfig.Service.Framework,
			serviceConfig.Service.StartCommand,
			serviceConfig.Service.MigrationCommand,
			serviceConfig.Service.NeedsWarmup,
			serviceConfig.Service.WarmupEndpoint,
			serviceConfig.Service.WarmupDelayMs,
		)
		startCmd = fwConfig.GetStartCommand(appPort)
	}

	appContainerName := r.suite.containerNaming.GetAppContainer(config.ContainerNameParams{SpecName: spec.Name})
	logger.Debug("Starting app container %s with env vars: %v", appContainerName, appEnv)
	_, err = r.suite.orch.StartContainer(ctx, &container.Config{
		Image: serviceName + ":latest",
		Env:   appEnv,
		Cmd:   startCmd,
	}, &container.HostConfig{
		ExtraHosts: extraHosts,
		PortBindings: map[nat.Port][]nat.PortBinding{
			nat.Port(appPort + "/tcp"): {{HostIP: "0.0.0.0", HostPort: "0"}},
		},
	}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{r.suite.networkName: {}},
	}, appContainerName)
	if err != nil {
		return err
	}
	defer func() {
		if !cleanupApp {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, appContainerName)
	}()

	inspectApp, inspectErr := r.suite.orch.GetContainerInspect(ctx, appContainerName)
	hostPort := ""
	if inspectErr == nil && inspectApp.NetworkSettings != nil {
		if p, ok := inspectApp.NetworkSettings.Ports[nat.Port(appPort+"/tcp")]; ok && len(p) > 0 {
			hostPort = p[0].HostPort
		}
	}
	logger.Debug("App started on host port: %s", hostPort)

	// 5. Wait for App
	logger.Debug("Waiting for App to be healthy")
	healthURL := fmt.Sprintf("http://localhost:%s%s", hostPort, serviceConfig.Service.HealthEndpoint)
	if err := r.suite.orch.WaitHTTP(ctx, healthURL, 120*time.Second); err != nil {
		logger.Debug("App failed to become healthy")
		if logger.IsDebug() {
			logCtx, logCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer logCancel()
			_ = r.suite.orch.StreamLogs(logCtx, "app-"+spec.Name, os.Stdout, os.Stderr)
		}
		return err
	}
	logger.Debug("App is healthy")
	persistSetupComplete = true

	// Warmup for apps that need it
	fwConfig := config.GetFrameworkConfig(
		serviceConfig.Service.Framework,
		serviceConfig.Service.StartCommand,
		serviceConfig.Service.MigrationCommand,
		serviceConfig.Service.NeedsWarmup,
		serviceConfig.Service.WarmupEndpoint,
		serviceConfig.Service.WarmupDelayMs,
	)

	if fwConfig.NeedsWarmup() {
		logger.Debug("Warming up %s app", serviceConfig.Service.Framework)
		warmupURL := fmt.Sprintf("http://localhost:%s%s", hostPort, fwConfig.GetWarmupEndpoint())
		resp, err := http.Get(warmupURL)
		if err != nil {
			logger.Debug("Warmup request failed: %v", err)
		} else {
			resp.Body.Close()
		}
	}

	testErr := r.runTestPhase(ctx, spec, hostPort, dbVerifyPortsList, httpVerifyPort, grpcVerifyPort, redisVerifyPort)

	// On success, register containers for reuse by subsequent tests in this suite.
	if testErr == nil && persist && persistSetupComplete {
		r.suite.persistentMu.Lock()
		r.suite.persistentContainers[serviceKey] = &persistentServiceContainers{
			dbProxies:       dbProxies,
			dbContainers:    dbContainers,
			dbVerifyPorts:   dbVerifyPortsByHost,
			dbHostPorts:     dbHostPortsMap,
			dbTypes:         dbTypesMap,
			httpProxyName:   httpProxyContainerName,
			grpcProxyName:   grpcProxyContainerName,
			redisProxyName:  redisProxyContainerName,
			appName:         appContainerName,
			appHostPort:     hostPort,
			httpVerifyPort:  httpVerifyPort,
			grpcVerifyPort:  grpcVerifyPort,
			redisVerifyPort: redisVerifyPort,
		}
		r.suite.persistentMu.Unlock()
		// Flip all DB cleanup guards and other guards so defers skip container teardown.
		for _, g := range dbCleanupGuards {
			*g = false
		}
		cleanupHTTPProxy = false
		cleanupGRPCProxy = false
		cleanupRedisProxy = false
		cleanupApp = false
		logger.Debug("Registered persistent containers for %s", serviceKey)
	}

	return testErr
}

// runTestPhase executes the trigger + response verify + mock verify steps.
// It is called both from the fresh-start path in run() and the reuse path.
func (r *testRunner) runTestPhase(
	ctx context.Context,
	spec *types.TestSpec,
	hostPort string,
	dbVerifyPorts []string,
	httpVerifyPort string,
	grpcVerifyPort string,
	redisVerifyPort string,
) error {
	// 6. Trigger (HTTP) or observe (Kafka consumer / Job)
	if spec.Receive.Channel == types.Event || spec.Receive.Channel == types.Job {
		// The trigger is already in place (seeded queue or internal scheduler).
		// Poll the verify endpoints every 500ms until all expected mocks are satisfied
		// or the test context deadline is reached.
		logger.Debug("Async test: polling for expected mock interactions...")
		pollTicker := time.NewTicker(500 * time.Millisecond)
		defer pollTicker.Stop()
	asyncPollLoop:
		for {
			for _, vp := range dbVerifyPorts {
				r.collectHits("localhost:" + vp)
			}
			if httpVerifyPort != "" {
				r.collectHits("localhost:" + httpVerifyPort)
			}
			if grpcVerifyPort != "" {
				r.collectHits("localhost:" + grpcVerifyPort)
			}
			if redisVerifyPort != "" {
				r.collectHits("localhost:" + redisVerifyPort)
			}
			if r.registry.VerifyAll() == nil {
				logger.Debug("Async test: all expected interactions satisfied")
				break asyncPollLoop
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("job test timed out waiting for expected interactions: %w", ctx.Err())
			case <-pollTicker.C:
				// continue polling
			}
		}
	} else {
		// HTTP-triggered test: send the request and verify the response.
		logger.Debug("Triggering request: %s %s", spec.Receive.Method, spec.Receive.Path)
		resp, err := r.sendRequest(spec.Receive, spec.BaseDir, hostPort)
		if err != nil {
			logger.Debug("Trigger request failed: %v", err)
			return err
		}
		defer resp.Body.Close()
		logger.Debug("Received response: %d", resp.StatusCode)

		// 7. Verify Response
		if resp.StatusCode != spec.Respond.StatusCode {
			logger.Debug("Test failed with status %d. Fetching app logs...", resp.StatusCode)
			if logger.IsDebug() {
				logCtx, logCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer logCancel()
				_ = r.suite.orch.StreamLogs(logCtx, r.suite.containerNaming.GetAppContainer(config.ContainerNameParams{SpecName: spec.Name}), os.Stdout, os.Stderr)
			}
			// Collect verify errors from sidecars before reporting the status mismatch —
			// a VERIFY failure causes the service to receive a proxy error and return a
			// non-expected status code, so surfacing the VERIFY message is more useful.
			for _, vp := range dbVerifyPorts {
				r.collectHits("localhost:" + vp)
			}
			if httpVerifyPort != "" {
				r.collectHits("localhost:" + httpVerifyPort)
			}
			if grpcVerifyPort != "" {
				r.collectHits("localhost:" + grpcVerifyPort)
			}
			if redisVerifyPort != "" {
				r.collectHits("localhost:" + redisVerifyPort)
			}
			if errs := r.registry.GetVerifyErrors(); len(errs) > 0 {
				return r.withVarContext(fmt.Errorf("%s", errs[0]))
			}
			return r.withVarContext(fmt.Errorf("expected status %d, got %d", spec.Respond.StatusCode, resp.StatusCode))
		}

		if spec.Respond.WithFile != "" {
			loader := dsl.NewPayloadLoaderWithResolver(spec.BaseDir, r.resolver)
			expected, err := loader.Load(spec.Respond.WithFile)
			if err != nil {
				return fmt.Errorf("failed to load expected response payload: %v", err)
			}

			actualRaw, _ := io.ReadAll(resp.Body)
			var actual interface{}
			_ = json.Unmarshal(actualRaw, &actual)

			if err := r.comparePayloads(expected, actual, spec.Respond.Noise); err != nil {
				logger.Debug("Response body mismatch: %v", err)
				return r.withVarContext(err)
			}
		}
	}

	// 8. Final Registry Verification
	for _, vp := range dbVerifyPorts {
		r.collectHits("localhost:" + vp)
	}
	if httpVerifyPort != "" {
		r.collectHits("localhost:" + httpVerifyPort)
	}
	if grpcVerifyPort != "" {
		r.collectHits("localhost:" + grpcVerifyPort)
	}
	if redisVerifyPort != "" {
		r.collectHits("localhost:" + redisVerifyPort)
	}
	// collectHits already waits for proxy responses with retry logic

	if err := r.registry.VerifyAll(); err != nil {
		logger.Debug("Mock verification failed: %v", err)
		if logger.IsDebug() {
			logCtx, logCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer logCancel()
			logger.Debug("Fetching app logs for debugging")
			_ = r.suite.orch.StreamLogs(logCtx, "app-"+spec.Name, os.Stdout, os.Stderr)
		}
		return r.withVarContext(err)
	}

	// Check for passthrough requests (requests that bypassed the mock layer).
	// Warn by default; fail if strict_passthrough: true is set in .linespec.yml.
	strict := r.config != nil && r.config.StrictPassthrough
	if err := r.registry.VerifyPassthroughs(strict); err != nil {
		return err
	}

	logger.Debug("Test passed")
	return nil
}

// withVarContext appends the resolved variable map to a test failure error so that
// users can immediately see what values were generated during the failing run.
func (r *testRunner) withVarContext(err error) error {
	if err == nil {
		return nil
	}
	if r.resolver == nil {
		return err
	}
	varMap := r.resolver.FormatVarMap()
	if varMap == "" {
		return err
	}
	return fmt.Errorf("%w\n\n%s", err, varMap)
}

func (r *testRunner) collectHits(addr string) {
	logger.Debug("Collecting hits from %s", addr)
	// Exponential backoff: 50ms, 100ms, 200ms, 400ms, 800ms
	delays := []time.Duration{50, 100, 200, 400, 800}
	for i := 0; i < len(delays); i++ {
		resp, err := http.Get("http://" + addr + "/verify")
		if err != nil {
			time.Sleep(delays[i] * time.Millisecond)
			continue
		}
		defer resp.Body.Close()

		// Parse response: supports both new format {"hits":{...},"passthroughs":[...]}
		// and legacy format {key: count, ...} for backward compatibility with older proxy images.
		rawBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return
		}
		var verifyResp struct {
			Hits         map[string]int `json:"hits"`
			Passthroughs []string       `json:"passthroughs"`
			VerifyErrors []string       `json:"verify_errors"`
		}
		if err := json.Unmarshal(rawBytes, &verifyResp); err != nil {
			return
		}
		if verifyResp.Hits != nil {
			r.registry.SetHits(verifyResp.Hits)
		} else {
			// Legacy format: flat map[string]int
			var legacyHits map[string]int
			if err := json.Unmarshal(rawBytes, &legacyHits); err == nil {
				r.registry.SetHits(legacyHits)
			}
		}
		if len(verifyResp.Passthroughs) > 0 {
			r.registry.AddPassthroughs(verifyResp.Passthroughs)
		}
		if len(verifyResp.VerifyErrors) > 0 {
			r.registry.AddVerifyErrors(verifyResp.VerifyErrors)
		}
		return
	}
}

func (r *testRunner) sendRequest(receive types.ReceiveStatement, baseDir string, port string) (*http.Response, error) {
	url := "http://localhost:" + port + receive.Path
	var body io.Reader
	if receive.WithFile != "" {
		loader := dsl.NewPayloadLoaderWithResolver(baseDir, r.resolver)
		payload, err := loader.Load(receive.WithFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load trigger payload: %v", err)
		}
		data, _ := json.Marshal(payload)
		body = strings.NewReader(string(data))
	}

	req, _ := http.NewRequest(receive.Method, url, body)
	if receive.WithFile != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range receive.Headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{}
	return client.Do(req)
}

func (r *testRunner) comparePayloads(expected, actual interface{}, noise []string) error {
	noiseMap := make(map[string]bool)
	for _, n := range noise {
		noiseMap[n] = true
	}
	return r.compareRecursive(expected, actual, "body", noiseMap)
}

func (r *testRunner) compareRecursive(exp, act interface{}, path string, noise map[string]bool) error {
	if noise[path] {
		return nil
	}

	switch e := exp.(type) {
	case map[string]interface{}:
		a, ok := act.(map[string]interface{})
		if !ok {
			return fmt.Errorf("at %s: expected object, got %T", path, act)
		}
		for k, v := range e {
			newPath := path + "." + k
			if err := r.compareRecursive(v, a[k], newPath, noise); err != nil {
				return err
			}
		}
	case []interface{}:
		a, ok := act.([]interface{})
		if !ok {
			return fmt.Errorf("at %s: expected array, got %T", path, act)
		}
		if len(e) != len(a) {
			return fmt.Errorf("at %s: expected array length %d, got %d", path, len(e), len(a))
		}
		for i := range e {
			newPath := fmt.Sprintf("%s[%d]", path, i)
			if err := r.compareRecursive(e[i], a[i], newPath, noise); err != nil {
				return err
			}
		}
	default:
		expStr := fmt.Sprintf("%v", exp)
		actStr := fmt.Sprintf("%v", act)
		if expStr != actStr {
			return fmt.Errorf("at %s: expected %v, got %v", path, exp, act)
		}
	}
	return nil
}

// getHTTPProxyDependencies returns HTTP dependencies that have Proxy enabled
func (r *testRunner) getHTTPProxyDependencies(cfg *config.LineSpecConfig) []config.DependencyConfig {
	var result []config.DependencyConfig
	for _, dep := range cfg.Dependencies {
		if dep.Type == "http" && dep.Proxy {
			result = append(result, dep)
		}
	}
	return result
}

func (r *testRunner) getGRPCProxyDependencies(cfg *config.LineSpecConfig) []config.DependencyConfig {
	var result []config.DependencyConfig
	for _, dep := range cfg.Dependencies {
		if dep.Type == "grpc" && dep.Proxy {
			result = append(result, dep)
		}
	}
	return result
}

func (r *testRunner) mergeGRPCDescriptorSets(cfg *config.LineSpecConfig, projectRoot string) (string, error) {
	var paths []string
	if cfg.GRPCDescriptorSet != "" {
		paths = append(paths, filepath.Join(projectRoot, cfg.GRPCDescriptorSet))
	}
	for _, dep := range cfg.Dependencies {
		if dep.Type == "grpc" && dep.GRPCDescriptorSet != "" {
			paths = append(paths, filepath.Join(projectRoot, dep.GRPCDescriptorSet))
		}
	}
	if len(paths) == 0 {
		return "", nil
	}
	if len(paths) == 1 {
		return paths[0], nil
	}
	var combinedData []byte
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("failed to read descriptor set %s: %w", p, err)
		}
		combinedData = append(combinedData, data...)
	}
	mergedPath := filepath.Join(r.tempDir, "merged-descriptor-set.pb")
	if err := os.WriteFile(mergedPath, combinedData, 0644); err != nil {
		return "", fmt.Errorf("failed to write merged descriptor set: %w", err)
	}
	return mergedPath, nil
}

// SchemaCache represents the cached schema for tables
type SchemaCache map[string][]ColumnInfo

// ColumnInfo represents a single column from SHOW FULL FIELDS
type ColumnInfo struct {
	Field      string         `json:"Field"`
	Type       string         `json:"Type"`
	Collation  sql.NullString `json:"Collation"`
	Null       string         `json:"Null"`
	Key        string         `json:"Key"`
	Default    sql.NullString `json:"Default"`
	Extra      string         `json:"Extra"`
	Privileges string         `json:"Privileges"`
	Comment    string         `json:"Comment"`
}


// fetchSchemaFromDatabase queries the real database for schema of specified tables
func (s *TestSuite) fetchSchemaFromDatabase(ctx context.Context, tables []string, dbHost, dbPort, dbUser, dbPass, dbName string) (SchemaCache, error) {
	if len(tables) == 0 {
		return make(SchemaCache), nil
	}

	// Build DSN for MySQL connection
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
		dbUser, dbPass, dbHost, dbPort, dbName)

	// Connect to database
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	// Test connection
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	schemaCache := make(SchemaCache)

	for _, table := range tables {
		query := fmt.Sprintf("SHOW FULL FIELDS FROM `%s`", table)
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			logger.Debug("Failed to fetch schema for table %s: %v", table, err)
			continue
		}
		defer rows.Close()

		var columns []ColumnInfo
		for rows.Next() {
			var col ColumnInfo
			err := rows.Scan(
				&col.Field,
				&col.Type,
				&col.Collation,
				&col.Null,
				&col.Key,
				&col.Default,
				&col.Extra,
				&col.Privileges,
				&col.Comment,
			)
			if err != nil {
				logger.Debug("Failed to scan column for table %s: %v", table, err)
				continue
			}
			columns = append(columns, col)
		}

		if err := rows.Err(); err != nil {
			logger.Debug("Error iterating rows for table %s: %v", table, err)
			continue
		}

		if len(columns) > 0 {
			schemaCache[table] = columns
			logger.Debug("Cached schema for table %s (%d columns)", table, len(columns))
		}
	}

	return schemaCache, nil
}

// expBackoff returns the next retry delay using exponential backoff, capped at max.
// attempt starts at 0 and increments each retry.
func expBackoff(attempt int, base, max time.Duration) time.Duration {
	d := base
	for i := 0; i < attempt; i++ {
		d *= 2
		if d >= max {
			return max
		}
	}
	return d
}

// kafkaClusterID generates a valid Kafka KRaft cluster ID: a 22-character base64url
// string (URL-safe, no padding) encoding 16 random bytes. cp-kafka 7.x+ rejects plain
// strings as cluster IDs; the format must match what kafka-storage random-uuid produces.
func kafkaClusterID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// fallback: fixed known-good value generated offline
		return "4L6g3nShT-eMCtK--X86sw"
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// waitForContainerPort polls until a container's port binding is available.
// If the container exits before the port binding appears, it fails immediately
// and captures the container logs to surface the actual crash reason.
func (s *TestSuite) waitForContainerPort(ctx context.Context, containerName, port string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var attempt int
	for time.Now().Before(deadline) {
		inspect, err := s.orch.GetContainerInspect(ctx, containerName)
		if err != nil {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(expBackoff(attempt, 50*time.Millisecond, 2*time.Second)):
			}
			attempt++
			continue
		}
		// Fail fast if the container already exited — the port binding will never appear
		if inspect.State.Status == "exited" || inspect.State.Status == "dead" {
			logs := s.captureContainerLogs(containerName)
			return "", fmt.Errorf("container %s exited (code %d) before port %s was bound\n%s",
				containerName, inspect.State.ExitCode, port, logs)
		}
		if p, ok := inspect.NetworkSettings.Ports[nat.Port(port)]; ok && len(p) > 0 && p[0].HostPort != "" {
			return p[0].HostPort, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(expBackoff(attempt, 50*time.Millisecond, 2*time.Second)):
		}
		attempt++
	}
	return "", fmt.Errorf("timeout waiting for container %s port %s binding", containerName, port)
}

// captureContainerLogs fetches the last lines of a container's combined stdout/stderr.
// Used to surface crash reasons when a container exits unexpectedly.
func (s *TestSuite) captureContainerLogs(containerName string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var buf bytes.Buffer
	if err := s.orch.StreamLogs(ctx, containerName, &buf, &buf); err != nil {
		return fmt.Sprintf("(could not capture logs: %v)", err)
	}
	out := buf.String()
	// Trim to last 50 lines to keep error messages readable
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 50 {
		lines = lines[len(lines)-50:]
	}
	return strings.Join(lines, "\n")
}

// waitForMySQL polls until MySQL is accepting connections using actual MySQL driver
// Handles MySQL restart during initialization by continuing to retry on any error
func (s *TestSuite) waitForMySQL(ctx context.Context, host, port, user, password, database string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
		user, password, host, port, database)

	// Suppress MySQL driver internal logging during polling
	mysql.SetLogger(log.New(io.Discard, "", 0))
	defer mysql.SetLogger(log.New(os.Stderr, "[mysql] ", log.Ldate|log.Ltime|log.Lshortfile))

	var attempt int
	for time.Now().Before(deadline) {
		db, err := sql.Open("mysql", dsn)
		if err == nil {
			ctx2, cancel := context.WithTimeout(ctx, 1*time.Second)
			err = db.PingContext(ctx2)
			cancel()
			db.Close()
			if err == nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(expBackoff(attempt, 50*time.Millisecond, 2*time.Second)):
		}
		attempt++
	}
	return fmt.Errorf("timeout waiting for MySQL at %s:%s", host, port)
}

// waitForPostgreSQL polls until PostgreSQL is accepting connections
func (s *TestSuite) waitForPostgreSQL(ctx context.Context, host, port, user, password, database string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		user, password, host, port, database)

	var attempt int
	for time.Now().Before(deadline) {
		db, err := sql.Open("postgres", dsn)
		if err == nil {
			ctx2, cancel := context.WithTimeout(ctx, 1*time.Second)
			err = db.PingContext(ctx2)
			cancel()
			db.Close()
			if err == nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(expBackoff(attempt, 50*time.Millisecond, 2*time.Second)):
		}
		attempt++
	}
	return fmt.Errorf("timeout waiting for PostgreSQL at %s:%s", host, port)
}

// buildMongoURI constructs a MongoDB connection URI from components.
func buildMongoURI(host, port, user, password, database string) string {
	uri := "mongodb://"
	if user != "" && password != "" {
		uri += user + ":" + password + "@"
	}
	uri += host + ":" + port + "/" + database
	if user != "" {
		uri += "?authSource=admin"
	}
	return uri
}

// waitForMongoDB polls until MongoDB is accepting connections using a TCP dial.
func (s *TestSuite) waitForMongoDB(ctx context.Context, host, port, user, password, database string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := host + ":" + port
	var attempt int
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(expBackoff(attempt, 200*time.Millisecond, 3*time.Second)):
		}
		attempt++
	}
	return fmt.Errorf("timeout waiting for MongoDB at %s:%s", host, port)
}

// truncateMongoDBCollections drops all non-system collections in the service database.
func (s *TestSuite) truncateMongoDBCollections(ctx context.Context, dbConfig *config.DatabaseConfig, hostPort string) error {
	// Split hostPort into host and port components for URI construction
	host := hostPort
	port := ""
	if h, p, err := net.SplitHostPort(hostPort); err == nil {
		host = h
		port = p
	}
	uri := buildMongoURI(host, port, dbConfig.Username, dbConfig.Password, dbConfig.Database)

	mongoCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(mongooptions.Client().ApplyURI(uri))
	if err != nil {
		return fmt.Errorf("failed to connect to MongoDB for truncation: %w", err)
	}
	defer client.Disconnect(context.Background()) //nolint:errcheck

	db := client.Database(dbConfig.Database)
	collections, err := db.ListCollectionNames(mongoCtx, bson.D{})
	if err != nil {
		return fmt.Errorf("failed to list MongoDB collections: %w", err)
	}
	for _, coll := range collections {
		if len(coll) > 7 && coll[:7] == "system." {
			continue
		}
		if err := db.Collection(coll).Drop(mongoCtx); err != nil {
			logger.Debug("Failed to drop MongoDB collection %s: %v", coll, err)
		}
	}
	return nil
}

// Deprecated: Use NewTestSuite instead
func NewRunner() (*Runner, error) {
	return nil, fmt.Errorf("NewRunner is deprecated, use NewTestSuite instead")
}

// Deprecated: Use TestSuite.RunTest instead
type Runner struct{}

func (r *Runner) RunTest(ctx context.Context, specPath string) error {
	return fmt.Errorf("Runner.RunTest is deprecated, use TestSuite.RunTest instead")
}
