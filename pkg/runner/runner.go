package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	"github.com/livecodelife/linespec/pkg/types"

	"github.com/go-sql-driver/mysql"
)

type TestSuite struct {
	orch            *docker.DockerOrchestrator
	networkName     string
	dbHostPort      string
	kafkaReady      bool
	cwd             string
	tempDir         string                            // Temp directory for shared files like schema cache
	serviceConfigs  map[string]*config.LineSpecConfig // Discovered service configurations
	defaultDBConfig *config.DatabaseConfig            // Default database configuration for shared infrastructure
	containerNaming *config.ContainerNaming           // Container naming configuration
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
		NetworkAlias:      "real-db",
		MigrateContainer:  "linespec-migrate-",
	}

	return &TestSuite{
		orch:            orch,
		networkName:     containerNaming.NetworkName,
		cwd:             cwd,
		tempDir:         tempDir,
		serviceConfigs:  make(map[string]*config.LineSpecConfig),
		containerNaming: containerNaming,
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

// FindInitScript looks for init.sql in discovered MySQL services
func (s *TestSuite) FindInitScript() string {
	// First, look for init.sql in services configured to use MySQL
	for serviceName, cfg := range s.serviceConfigs {
		// Skip PostgreSQL services
		if cfg.Database != nil && cfg.Database.Type == "postgresql" {
			logger.Debug("Skipping PostgreSQL service %s for init.sql", serviceName)
			continue
		}

		serviceDir := cfg.BaseDir
		if serviceDir == "" {
			// Construct from service name relative to cwd
			serviceDir = filepath.Join(s.cwd, serviceName)
		}

		// Check for init.sql in service directory
		initSqlPath := filepath.Join(serviceDir, "init.sql")
		if _, err := os.Stat(initSqlPath); err == nil {
			// Validate it's a MySQL-compatible script (basic check)
			content, err := os.ReadFile(initSqlPath)
			if err == nil && !containsPostgresSyntax(string(content)) {
				logger.Debug("Found MySQL-compatible init.sql in service %s: %s", serviceName, initSqlPath)
				return initSqlPath
			}
		}
	}

	// Fallback: look for init.sql in common locations with MySQL services
	fallbackPaths := []string{
		filepath.Join(s.cwd, "init.sql"),
		filepath.Join(s.cwd, "db", "init.sql"),
		filepath.Join(s.cwd, "examples", "user-service", "init.sql"),
	}

	for _, path := range fallbackPaths {
		if _, err := os.Stat(path); err == nil {
			// Validate it's not PostgreSQL
			content, err := os.ReadFile(path)
			if err == nil && !containsPostgresSyntax(string(content)) {
				logger.Debug("Found MySQL-compatible init.sql at fallback location: %s", path)
				return path
			}
		}
	}

	logger.Debug("No MySQL-compatible init.sql found, database will start empty")
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

	// Create shared network
	_, err := s.orch.CreateNetwork(ctx, s.networkName)
	if err != nil {
		return fmt.Errorf("failed to create network: %w", err)
	}

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

	_, err = s.orch.StartContainer(ctx, &container.Config{
		Image: "mysql:8.4",
		Env: []string{
			fmt.Sprintf("MYSQL_ROOT_PASSWORD=rootpassword"),
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

	// Fetch schema for all tables after migrations complete
	// This is done once and shared across all tests
	tables := s.discoverTables(ctx, dbConfig)
	schemaCache, err := s.fetchSchemaFromDatabase(ctx, tables, "localhost", s.dbHostPort,
		dbConfig.Username, dbConfig.Password, dbConfig.Database)
	if err != nil {
		logger.Debug("Failed to fetch shared schema: %v", err)
	} else {
		// Save to shared location in temp directory
		schemaFile := filepath.Join(s.tempDir, ".linespec-shared-schema.json")
		schemaData, _ := json.MarshalIndent(schemaCache, "", "  ")
		if err := os.WriteFile(schemaFile, schemaData, 0644); err != nil {
			logger.Debug("Failed to write shared schema file: %v", err)
		} else {
			logger.Debug("Shared schema cached to %s", schemaFile)
		}
	}

	// Start shared Kafka
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
			"CLUSTER_ID=linespec-cluster",
		},
	}, &container.HostConfig{
		PortBindings: map[nat.Port][]nat.PortBinding{
			"9092/tcp":  {{HostIP: "0.0.0.0", HostPort: "0"}},
			"29092/tcp": {{HostIP: "0.0.0.0", HostPort: "0"}},
		},
	}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{s.networkName: {Aliases: []string{"kafka"}}},
	}, "linespec-shared-kafka")
	if err != nil {
		return fmt.Errorf("failed to start Kafka: %w", err)
	}

	// Get Kafka host port for direct connection from host (with retry)
	kafkaHostPort, err := s.waitForContainerPort(ctx, "linespec-shared-kafka", "29092/tcp", 30*time.Second)
	if err != nil {
		return fmt.Errorf("failed to get Kafka host port: %w", err)
	}
	if err := s.orch.WaitTCPInternal(ctx, s.networkName, "localhost:"+kafkaHostPort, 60*time.Second); err != nil {
		return fmt.Errorf("Kafka not ready: %w", err)
	}

	// Wait for Kafka to be ready (actual TCP connection check)
	logger.Debug("Waiting for Kafka to be ready")
	if err := s.orch.WaitTCPInternal(ctx, s.networkName, "localhost:"+kafkaHostPort, 60*time.Second); err != nil {
		return fmt.Errorf("Kafka not ready: %w", err)
	}
	s.kafkaReady = true

	logger.Debug("Shared infrastructure ready")
	return nil
}

// getSharedDatabaseConfig returns database config for shared infrastructure
// Uses the first discovered MySQL service's config, or defaults
func (s *TestSuite) getSharedDatabaseConfig() *config.DatabaseConfig {
	// Look for first MySQL service configuration
	for _, cfg := range s.serviceConfigs {
		if cfg.Database != nil && cfg.Database.Type == "mysql" {
			return cfg.Database
		}
	}
	// Return defaults if no MySQL service found
	return &config.DatabaseConfig{
		Database: "todo_api_development",
		Username: "todo_user",
		Password: "todo_password",
		Host:     "real-db",
		Port:     3306,
	}
}

// discoverTables returns list of tables to fetch schema for
// TODO: In Session 6, this will be replaced with auto-discovery
func (s *TestSuite) discoverTables(ctx context.Context, dbConfig *config.DatabaseConfig) []string {
	// Use hardcoded tables for backward compatibility
	// Future enhancement: auto-discover from database
	return []string{"users", "todos", "ar_internal_metadata", "schema_migrations"}
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
	needsWarmup := false
	if cfg.Service.NeedsWarmup != nil {
		needsWarmup = *cfg.Service.NeedsWarmup
	}

	fwConfig := config.GetFrameworkConfig(
		framework,
		cfg.Service.MigrationCommand,
		cfg.Service.MigrationCommand,
		needsWarmup,
		cfg.Service.WarmupEndpoint,
		cfg.Service.WarmupDelayMs,
	)

	// Get migration command
	migrationCmd := fwConfig.GetMigrationCommand()
	if migrationCmd == nil {
		logger.Debug("No migration command defined for framework %s, service %s", framework, serviceName)
		return nil
	}

	// Use custom migration command if specified
	if cfg.Service.MigrationCommand != "" {
		migrationCmd = []string{"sh", "-c", cfg.Service.MigrationCommand}
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
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout waiting for DB initialization")
}

func (s *TestSuite) ResetDatabase(ctx context.Context) error {
	if s.dbHostPort == "" {
		return nil
	}

	dbConfig := s.getSharedDatabaseConfig()

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
	containerName := s.containerNaming.MigrateContainer + serviceName

	// Clean up any existing migration container
	_ = s.orch.StopAndRemoveContainer(context.Background(), containerName)

	// Get database configuration
	dbConfig := cfg.Database
	if dbConfig == nil {
		dbConfig = s.getSharedDatabaseConfig()
	}

	// Build environment variables from config
	appEnv := []string{}

	if cfg.Infrastructure.Database && dbConfig != nil {
		appEnv = append(appEnv,
			fmt.Sprintf("DB_HOST=%s", s.containerNaming.NetworkAlias),
			fmt.Sprintf("DB_PORT=%d", dbConfig.Port),
			fmt.Sprintf("DB_USERNAME=%s", dbConfig.Username),
			fmt.Sprintf("DB_PASSWORD=%s", dbConfig.Password),
			fmt.Sprintf("RAILS_ENV=development"),
		)
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

	_, err := s.orch.StartContainer(ctx, &container.Config{
		Image: serviceName + ":latest",
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
	_ = s.orch.StopAndRemoveContainer(ctx, "linespec-shared-kafka")
	_ = s.orch.StopAndRemoveContainer(ctx, s.containerNaming.DatabaseContainer)
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
	suite    *TestSuite
	registry *registry.MockRegistry
	config   *config.LineSpecConfig
	tempDir  string                // Temp directory for registry and other test artifacts
	resolver *interpolate.Resolver // Resolver for environment variable substitution
}

func (r *testRunner) run(ctx context.Context, specPath string) error {
	// Create resolver for environment variable substitution
	r.resolver = interpolate.NewResolver()

	// 1. Load Spec
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

	// 1.5 Load Service Configuration
	specDir := spec.BaseDir
	serviceConfig, err := config.LoadConfig(specDir)
	if err != nil {
		return fmt.Errorf("failed to load service config from %s: %w", specDir, err)
	}
	r.config = serviceConfig

	// Create temp directory for this test run
	tempDir, err := os.MkdirTemp("", "linespec-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	r.tempDir = tempDir
	defer os.RemoveAll(tempDir) // Clean up temp directory after test

	// Pre-cleanup test-specific containers only
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
	_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, "app-"+spec.Name)
	_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, "proxy-db-"+spec.Name)
	_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, "proxy-http-"+spec.Name)
	cleanupCancel()

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
	_ = r.registry.SaveToFile(regFile)

	// 3. Start Database and Proxy Containers (if database is enabled)
	var dbContainerName string
	if serviceConfig.Infrastructure.Database && serviceConfig.Database != nil {
		dbType := serviceConfig.Database.Type
		dbPort := fmt.Sprintf("%d", serviceConfig.Database.Port)

		switch dbType {
		case "postgresql":
			// Start PostgreSQL container for this service
			dbContainerName = "linespec-db-" + spec.Name
			db := serviceConfig.Database

			_, err = r.suite.orch.StartContainer(ctx, &container.Config{
				Image: db.Image,
				Env: []string{
					fmt.Sprintf("POSTGRES_DB=%s", db.Database),
					fmt.Sprintf("POSTGRES_USER=%s", db.Username),
					fmt.Sprintf("POSTGRES_PASSWORD=%s", db.Password),
					"POSTGRES_HOST_AUTH_METHOD=trust", // Enable trust authentication
				},
			}, &container.HostConfig{
				PortBindings: map[nat.Port][]nat.PortBinding{
					nat.Port(dbPort + "/tcp"): {{HostIP: "0.0.0.0", HostPort: "0"}},
				},
			}, &network.NetworkingConfig{
				EndpointsConfig: map[string]*network.EndpointSettings{r.suite.networkName: {Aliases: []string{"real-db"}}},
			}, dbContainerName)
			if err != nil {
				return fmt.Errorf("failed to start PostgreSQL container: %w", err)
			}
			defer func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, dbContainerName)
			}()

			// Wait for PostgreSQL to be ready
			logger.Debug("Waiting for PostgreSQL to be ready")
			// Get host port for direct connection from host (with retry)
			postgresHostPort, err := r.suite.waitForContainerPort(ctx, dbContainerName, dbPort+"/tcp", 30*time.Second)
			if err != nil {
				return fmt.Errorf("failed to get PostgreSQL host port: %w", err)
			}
			if err := r.suite.orch.WaitTCPInternal(ctx, r.suite.networkName, "localhost:"+postgresHostPort, 30*time.Second); err != nil {
				return fmt.Errorf("PostgreSQL not ready: %w", err)
			}

			// Build PostgreSQL proxy command with debug flag if enabled
			pgProxyCmd := []string{"proxy", "postgresql", "0.0.0.0:" + dbPort, "real-db:" + dbPort, "/app/registry/registry-" + spec.Name + ".json"}
			if logger.IsDebug() {
				pgProxyCmd = append(pgProxyCmd, "--debug")
			}

			_, err = r.suite.orch.StartContainer(ctx, &container.Config{
				Image: "linespec:latest",
				Cmd:   pgProxyCmd,
			}, &container.HostConfig{
				Binds: []string{
					r.suite.cwd + ":/app/project",
					r.tempDir + ":/app/registry",
				},
				PortBindings: map[nat.Port][]nat.PortBinding{
					"8081/tcp": {{HostIP: "0.0.0.0", HostPort: "0"}},
				},
			}, &network.NetworkingConfig{
				EndpointsConfig: map[string]*network.EndpointSettings{r.suite.networkName: {Aliases: []string{"db"}}},
			}, "proxy-db-"+spec.Name)
			if err != nil {
				return fmt.Errorf("failed to start PostgreSQL proxy: %w", err)
			}
			defer func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, "proxy-db-"+spec.Name)
			}()

			logger.Debug("PostgreSQL proxy started")

			// Wait for proxy to be ready
			logger.Debug("Waiting for PostgreSQL proxy to be ready")
			// Get proxy verify endpoint host port for direct connection from host (with retry)
			// Note: The proxy listens on dbPort internally, but only exposes 8081 to host
			proxyVerifyPort, err := r.suite.waitForContainerPort(ctx, "proxy-db-"+spec.Name, "8081/tcp", 30*time.Second)
			if err != nil {
				return fmt.Errorf("failed to get PostgreSQL proxy verify port: %w", err)
			}
			if err := r.suite.orch.WaitTCPInternal(ctx, r.suite.networkName, "localhost:"+proxyVerifyPort, 30*time.Second); err != nil {
				return fmt.Errorf("PostgreSQL proxy not ready: %w", err)
			}
			logger.Debug("PostgreSQL proxy is ready")

		case "mysql":
			// MySQL: use shared database with proxy
			dbContainerName = "linespec-shared-db"

			// Load schema from shared file (pre-fetched during SetupSharedInfrastructure)
			// This is faster than fetching per-test and eliminates the need for transparent mode
			sharedSchemaFile := filepath.Join(r.suite.tempDir, ".linespec-shared-schema.json")
			schemaFile := filepath.Join(r.tempDir, "schema-"+spec.Name+".json")

			if _, err := os.Stat(sharedSchemaFile); err == nil {
				// Copy shared schema to test-specific location
				data, err := os.ReadFile(sharedSchemaFile)
				if err == nil {
					if err := os.WriteFile(schemaFile, data, 0644); err != nil {
						logger.Debug("Failed to write schema file: %v", err)
					} else {
						logger.Debug("Loaded shared schema for test")
					}
				} else {
					logger.Debug("Failed to read shared schema: %v", err)
				}
			} else {
				// Fallback: extract tables from spec and fetch fresh (for backward compatibility)
				logger.Debug("Shared schema not found, fetching per-test")
				tables := extractTableNamesFromSpec(spec)
				if len(tables) > 0 {
					schemaCache, err := r.suite.fetchSchemaFromDatabase(
						ctx, tables,
						"localhost", r.suite.dbHostPort,
						serviceConfig.Database.Username,
						serviceConfig.Database.Password,
						serviceConfig.Database.Database,
					)
					if err != nil {
						logger.Debug("Failed to fetch schema: %v", err)
					} else if len(schemaCache) > 0 {
						schemaData, _ := json.MarshalIndent(schemaCache, "", "  ")
						if err := os.WriteFile(schemaFile, schemaData, 0644); err != nil {
							logger.Debug("Failed to write schema file: %v", err)
						}
					}
				}
			}

			// Start database proxy
			logger.Debug("Starting MySQL proxy")

			// Build proxy command with optional schema file and debug flag
			proxyCmd := []string{
				"proxy", "mysql",
				"0.0.0.0:" + dbPort,
				"real-db:" + dbPort,
				"/app/registry/registry-" + spec.Name + ".json",
			}

			// Add schema file if it exists
			if _, err := os.Stat(schemaFile); err == nil {
				proxyCmd = append(proxyCmd, "/app/registry/schema-"+spec.Name+".json")
			}

			// Add transparent mode duration (0s) - schema is pre-loaded from shared file
			proxyCmd = append(proxyCmd, "0s")

			// Add debug flag if enabled
			if logger.IsDebug() {
				proxyCmd = append(proxyCmd, "--debug")
			}

			_, err = r.suite.orch.StartContainer(ctx, &container.Config{
				Image: "linespec:latest",
				Cmd:   proxyCmd,
			}, &container.HostConfig{
				Binds: []string{
					r.suite.cwd + ":/app/project",
					r.tempDir + ":/app/registry",
				},
				PortBindings: map[nat.Port][]nat.PortBinding{
					"8081/tcp": {{HostIP: "0.0.0.0", HostPort: "0"}},
				},
			}, &network.NetworkingConfig{
				EndpointsConfig: map[string]*network.EndpointSettings{r.suite.networkName: {Aliases: []string{"db"}}},
			}, "proxy-db-"+spec.Name)
			if err != nil {
				return fmt.Errorf("failed to start MySQL proxy: %w", err)
			}
			defer func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, "proxy-db-"+spec.Name)
			}()
			logger.Debug("MySQL proxy started")

			// Stream proxy logs for debugging (only in debug mode)
			if logger.IsDebug() {
				go func() {
					logCtx, logCancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer logCancel()
					_ = r.suite.orch.StreamLogs(logCtx, "proxy-db-"+spec.Name, os.Stdout, os.Stderr)
				}()
			}
		}
	}

	// HTTP Proxy - always start for backward compatibility with user-service.local
	logger.Debug("Starting HTTP proxy")

	// Build HTTP proxy command with debug flag if enabled
	httpProxyCmd := []string{"proxy", "http", "0.0.0.0:80", "unused", "/app/registry/registry-" + spec.Name + ".json"}
	if logger.IsDebug() {
		httpProxyCmd = append(httpProxyCmd, "--debug")
	}

	_, err = r.suite.orch.StartContainer(ctx, &container.Config{
		Image: "linespec:latest",
		Cmd:   httpProxyCmd,
	}, &container.HostConfig{
		Binds: []string{
			r.suite.cwd + ":/app/project",
			r.tempDir + ":/app/registry",
		},
		PortBindings: map[nat.Port][]nat.PortBinding{
			"8081/tcp": {{HostIP: "0.0.0.0", HostPort: "0"}},
		},
	}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{r.suite.networkName: {Aliases: []string{"user-service.local"}}},
	}, "proxy-http-"+spec.Name)
	if err != nil {
		return fmt.Errorf("failed to start HTTP proxy: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, "proxy-http-"+spec.Name)
	}()
	logger.Debug("HTTP proxy started")

	// Inspect all proxies to get ports and IPs
	var dbVerifyPort, httpVerifyPort, proxyHttpIP string

	if serviceConfig.Infrastructure.Database && serviceConfig.Database != nil {
		// Both MySQL and PostgreSQL now have proxies we can inspect
		inspectDb, _ := r.suite.orch.GetContainerInspect(ctx, "proxy-db-"+spec.Name)
		if p, ok := inspectDb.NetworkSettings.Ports["8081/tcp"]; ok && len(p) > 0 {
			dbVerifyPort = p[0].HostPort
		}
	}

	inspectHttp, _ := r.suite.orch.GetContainerInspect(ctx, "proxy-http-"+spec.Name)
	if p, ok := inspectHttp.NetworkSettings.Ports["8081/tcp"]; ok && len(p) > 0 {
		httpVerifyPort = p[0].HostPort
	}
	if n, ok := inspectHttp.NetworkSettings.Networks[r.suite.networkName]; ok {
		proxyHttpIP = n.IPAddress
	}

	// Wait for services to be ready on the network
	logger.Debug("Waiting for proxies to be ready")
	if serviceConfig.Infrastructure.Database && serviceConfig.Database != nil && dbVerifyPort != "" {
		if err := r.suite.orch.WaitTCPInternal(ctx, r.suite.networkName, "localhost:"+dbVerifyPort, 30*time.Second); err != nil {
			return fmt.Errorf("database proxy not ready: %w", err)
		}
	}
	if httpVerifyPort != "" {
		if err := r.suite.orch.WaitTCPInternal(ctx, r.suite.networkName, "localhost:"+httpVerifyPort, 30*time.Second); err != nil {
			return fmt.Errorf("HTTP proxy not ready: %w", err)
		}
	}

	// 4. Start SUT
	// Build environment variables based on config
	appEnv := []string{}

	// Add database environment variables if enabled
	if serviceConfig.Infrastructure.Database && serviceConfig.Database != nil {
		db := serviceConfig.Database
		switch db.Type {
		case "mysql":
			appEnv = append(appEnv,
				"DB_HOST=db",
				fmt.Sprintf("DB_PORT=%d", db.Port),
				fmt.Sprintf("DB_USERNAME=%s", db.Username),
				fmt.Sprintf("DB_PASSWORD=%s", db.Password),
				"RAILS_ENV=development",
			)
		case "postgresql":
			appEnv = append(appEnv,
				fmt.Sprintf("DATABASE_URL=postgresql://%s:%s@db:%d/%s", db.Username, db.Password, db.Port, db.Database),
			)
		}
	}

	// Add Kafka environment variables if enabled
	if serviceConfig.Infrastructure.Kafka {
		appEnv = append(appEnv,
			"KAFKA_BROKERS=kafka:29092",
		)
	}

	// Add user-defined environment variables
	for k, v := range serviceConfig.Service.Environment {
		// Interpolate proxy IP if needed
		if strings.Contains(v, "{{proxy_http_ip}}") {
			v = strings.ReplaceAll(v, "{{proxy_http_ip}}", proxyHttpIP)
		}
		appEnv = append(appEnv, fmt.Sprintf("%s=%s", k, v))
	}

	// Add generated environment variables for ${VAR_NAME} substitutions
	// These are generated at test time to prevent hardcoded value matching
	generatedEnv := r.resolver.GetGeneratedEnv()
	if len(generatedEnv) > 0 {
		logger.Debug("Injecting %d generated environment variables", len(generatedEnv))
		appEnv = append(appEnv, generatedEnv...)
	}

	// Add USER_SERVICE_URL for services that depend on user-service
	for _, dep := range serviceConfig.Dependencies {
		if dep.Name == "user-service" && dep.Type == "http" {
			// HTTP proxy listens on port 80
			appEnv = append(appEnv, fmt.Sprintf("USER_SERVICE_URL=http://%s:80/api/v1/users/auth", dep.Host))
		}
	}

	extraHosts := []string{}
	if proxyHttpIP != "" {
		extraHosts = append(extraHosts, "user-service.local:"+proxyHttpIP)
	} else {
		extraHosts = append(extraHosts, "user-service.local:host-gateway")
	}

	// Determine start command based on framework and config
	var startCmd []string
	if serviceConfig.Service.StartCommand != "" {
		// Use custom start command from config
		startCmd = []string{"sh", "-c", serviceConfig.Service.StartCommand}
	} else {
		// Get framework configuration and start command
		needsWarmup := false
		if serviceConfig.Service.NeedsWarmup != nil {
			needsWarmup = *serviceConfig.Service.NeedsWarmup
		}

		fwConfig := config.GetFrameworkConfig(
			serviceConfig.Service.Framework,
			serviceConfig.Service.StartCommand,
			serviceConfig.Service.MigrationCommand,
			needsWarmup,
			serviceConfig.Service.WarmupEndpoint,
			serviceConfig.Service.WarmupDelayMs,
		)
		startCmd = fwConfig.GetStartCommand(appPort)
	}

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
	}, "app-"+spec.Name)
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = r.suite.orch.StopAndRemoveContainer(cleanupCtx, "app-"+spec.Name)
	}()

	inspectApp, _ := r.suite.orch.GetContainerInspect(ctx, "app-"+spec.Name)
	hostPort := ""
	if p, ok := inspectApp.NetworkSettings.Ports[nat.Port(appPort+"/tcp")]; ok && len(p) > 0 {
		hostPort = p[0].HostPort
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

	// Warmup for apps that need it
	needsWarmup := false
	if serviceConfig.Service.NeedsWarmup != nil {
		needsWarmup = *serviceConfig.Service.NeedsWarmup
	}

	fwConfig := config.GetFrameworkConfig(
		serviceConfig.Service.Framework,
		serviceConfig.Service.StartCommand,
		serviceConfig.Service.MigrationCommand,
		needsWarmup,
		serviceConfig.Service.WarmupEndpoint,
		serviceConfig.Service.WarmupDelayMs,
	)

	if fwConfig.NeedsWarmup() {
		logger.Debug("Warming up %s app", serviceConfig.Service.Framework)
		warmupEndpoint := fwConfig.GetWarmupEndpoint()
		if serviceConfig.Service.WarmupEndpoint != "" {
			warmupEndpoint = serviceConfig.Service.WarmupEndpoint
		}

		warmupURL := fmt.Sprintf("http://localhost:%s%s", hostPort, warmupEndpoint)
		resp, err := http.Get(warmupURL)
		if err != nil {
			logger.Debug("Warmup request failed: %v", err)
		} else {
			resp.Body.Close()
		}

		warmupDelay := fwConfig.GetWarmupDelay()
		if serviceConfig.Service.WarmupDelayMs > 0 {
			warmupDelay = time.Duration(serviceConfig.Service.WarmupDelayMs) * time.Millisecond
		}
		if warmupDelay > 0 {
			time.Sleep(warmupDelay)
		}
	}

	// 6. Trigger Request
	logger.Debug(fmt.Sprintf("Triggering request: %s %s", spec.Receive.Method, spec.Receive.Path))
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
			_ = r.suite.orch.StreamLogs(logCtx, "app-"+spec.Name, os.Stdout, os.Stderr)
		}
		return fmt.Errorf("expected status %d, got %d", spec.Respond.StatusCode, resp.StatusCode)
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
			return err
		}
	}

	// 8. Final Registry Verification
	if dbVerifyPort != "" {
		r.collectHits("localhost:" + dbVerifyPort)
	}
	if httpVerifyPort != "" {
		r.collectHits("localhost:" + httpVerifyPort)
	}
	// REMOVED: time.Sleep(500 * time.Millisecond)
	// collectHits already waits for proxy responses with retry logic

	if err := r.registry.VerifyAll(); err != nil {
		logger.Debug("Mock verification failed: %v", err)
		if logger.IsDebug() {
			logCtx, logCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer logCancel()
			logger.Debug("Fetching app logs for debugging")
			_ = r.suite.orch.StreamLogs(logCtx, "app-"+spec.Name, os.Stdout, os.Stderr)
		}
		return err
	}

	logger.Debug("Test passed")
	return nil
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

		var hits map[string]int
		if err := json.NewDecoder(resp.Body).Decode(&hits); err != nil {
			return
		}
		r.registry.SetHits(hits)
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

// extractTableNamesFromSpec extracts table names from EXPECT statements in the spec
func extractTableNamesFromSpec(spec *types.TestSpec) []string {
	tableMap := make(map[string]bool)

	for _, expect := range spec.Expects {
		switch expect.Channel {
		case types.ReadMySQL, types.WriteMySQL:
			if expect.Table != "" {
				tableMap[expect.Table] = true
			}
		}
	}

	// Convert map to slice
	tables := make([]string, 0, len(tableMap))
	for table := range tableMap {
		tables = append(tables, table)
	}

	return tables
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

// waitForContainerPort polls until a container's port binding is available
func (s *TestSuite) waitForContainerPort(ctx context.Context, containerName, port string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		inspect, err := s.orch.GetContainerInspect(ctx, containerName)
		if err != nil {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}
		if p, ok := inspect.NetworkSettings.Ports[nat.Port(port)]; ok && len(p) > 0 && p[0].HostPort != "" {
			return p[0].HostPort, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("timeout waiting for container %s port %s binding", containerName, port)
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
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout waiting for MySQL at %s:%s", host, port)
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
