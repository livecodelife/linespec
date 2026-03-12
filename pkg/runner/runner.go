package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/calebcowen/linespec/pkg/config"
	"github.com/calebcowen/linespec/pkg/docker"
	"github.com/calebcowen/linespec/pkg/dsl"
	"github.com/calebcowen/linespec/pkg/registry"
	"github.com/calebcowen/linespec/pkg/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"

	_ "github.com/go-sql-driver/mysql"
)

type TestSuite struct {
	orch        *docker.DockerOrchestrator
	networkName string
	dbHostPort  string
	kafkaReady  bool
	cwd         string
	tempDir     string // Temp directory for shared files like schema cache
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

	return &TestSuite{
		orch:        orch,
		networkName: "linespec-shared-net",
		cwd:         cwd,
		tempDir:     tempDir,
	}, nil
}

func (s *TestSuite) SetupSharedInfrastructure(ctx context.Context) error {
	// Clean up any existing infrastructure first
	s.CleanupSharedInfrastructure(context.Background())

	// Create shared network
	_, err := s.orch.CreateNetwork(ctx, s.networkName)
	if err != nil {
		return fmt.Errorf("failed to create network: %w", err)
	}

	// Start shared MySQL
	serviceDir := "user-service"
	initSqlPath := filepath.Join(s.cwd, serviceDir, "init.sql")

	_, err = s.orch.StartContainer(ctx, &container.Config{
		Image: "mysql:8.4",
		Env:   []string{"MYSQL_ROOT_PASSWORD=rootpassword", "MYSQL_DATABASE=todo_api_development", "MYSQL_USER=todo_user", "MYSQL_PASSWORD=todo_password"},
	}, &container.HostConfig{
		Binds: []string{fmt.Sprintf("%s:/docker-entrypoint-initdb.d/init.sql", initSqlPath)},
		PortBindings: map[nat.Port][]nat.PortBinding{
			"3306/tcp": {{HostIP: "0.0.0.0", HostPort: "0"}},
		},
	}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{s.networkName: {Aliases: []string{"real-db"}}},
	}, "linespec-shared-db")
	if err != nil {
		return fmt.Errorf("failed to start MySQL: %w", err)
	}

	fmt.Println("Waiting for shared DB to be ready...")
	if err := s.orch.WaitTCPInternal(ctx, s.networkName, "real-db:3306", 60*time.Second); err != nil {
		return err
	}

	// Get the host port for DB reset
	inspect, _ := s.orch.GetContainerInspect(ctx, "linespec-shared-db")
	if p, ok := inspect.NetworkSettings.Ports["3306/tcp"]; ok && len(p) > 0 {
		s.dbHostPort = p[0].HostPort
	}

	// Wait for init.sql to complete
	if err := s.waitForDBInit(ctx); err != nil {
		return fmt.Errorf("failed waiting for DB init: %w", err)
	}

	// Run Rails migrations once for all services
	fmt.Println("Running Rails migrations...")
	if err := s.runMigrations(ctx, "user-service", "3001"); err != nil {
		return fmt.Errorf("failed to run user-service migrations: %w", err)
	}
	if err := s.runMigrations(ctx, "todo-api", "3000"); err != nil {
		return fmt.Errorf("failed to run todo-api migrations: %w", err)
	}
	fmt.Println("✅ Migrations complete")

	// Fetch schema for all tables after migrations complete
	// This is done once and shared across all tests
	tables := []string{"users", "todos", "ar_internal_metadata", "schema_migrations"}
	schemaCache, err := s.fetchSchemaFromDatabase(ctx, tables, "localhost", s.dbHostPort,
		"todo_user", "todo_password", "todo_api_development")
	if err != nil {
		fmt.Printf("⚠️  Failed to fetch shared schema: %v\n", err)
	} else {
		// Save to shared location in temp directory
		schemaFile := filepath.Join(s.tempDir, ".linespec-shared-schema.json")
		schemaData, _ := json.MarshalIndent(schemaCache, "", "  ")
		if err := os.WriteFile(schemaFile, schemaData, 0644); err != nil {
			fmt.Printf("⚠️  Failed to write shared schema file: %v\n", err)
		} else {
			fmt.Printf("✅ Shared schema cached to %s\n", schemaFile)
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

	if err := s.orch.WaitTCPInternal(ctx, s.networkName, "kafka:29092", 60*time.Second); err != nil {
		return err
	}
	s.kafkaReady = true

	fmt.Println("✅ Shared infrastructure ready")
	return nil
}

func (s *TestSuite) waitForDBInit(ctx context.Context) error {
	// Poll for table creation to confirm init.sql completed
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		// Try to connect via TCP to the host-mapped port
		if s.dbHostPort != "" {
			if err := s.orch.WaitTCP(ctx, "localhost:"+s.dbHostPort, 2*time.Second); err == nil {
				// Give a moment for init.sql to fully apply
				time.Sleep(2 * time.Second)
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout waiting for DB initialization")
}

func (s *TestSuite) ResetDatabase(ctx context.Context) error {
	if s.dbHostPort == "" {
		return nil
	}

	// For now, we'll just re-run init.sql by executing it via mysql client in the container
	resetSQL := `
SET FOREIGN_KEY_CHECKS = 0;
SELECT CONCAT('TRUNCATE TABLE ', table_name, ';') 
FROM information_schema.tables 
WHERE table_schema = 'todo_api_development' AND table_type = 'BASE TABLE';
SET FOREIGN_KEY_CHECKS = 1;
`

	_ = resetSQL // We'll implement this if needed, for now rely on clean test data

	return nil
}

func (s *TestSuite) runMigrations(ctx context.Context, serviceDir, appPort string) error {
	// Start a temporary container to run migrations
	containerName := "linespec-migrate-" + serviceDir

	// Clean up any existing migration container
	_ = s.orch.StopAndRemoveContainer(context.Background(), containerName)

	appEnv := []string{
		"DB_HOST=real-db",
		"DB_PORT=3306",
		"DB_USERNAME=todo_user",
		"DB_PASSWORD=todo_password",
		"RAILS_ENV=development",
		"KAFKA_BROKERS=kafka:29092",
		"KAFKA_TOPIC=todo-events",
	}

	_, err := s.orch.StartContainer(ctx, &container.Config{
		Image: serviceDir + ":latest",
		Env:   appEnv,
		Cmd:   []string{"bundle", "exec", "rails", "db:migrate"},
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
	_ = s.orch.StopAndRemoveContainer(ctx, "linespec-shared-db")
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
	tempDir  string // Temp directory for registry and other test artifacts
}

func (r *testRunner) run(ctx context.Context, specPath string) error {
	// 1. Load Spec
	tokens, err := dsl.LexFile(specPath)
	if err != nil {
		return err
	}
	parser := dsl.NewParser(tokens)
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
	appPort := fmt.Sprintf("%d", serviceConfig.Service.Port)

	// 2. Save Registry to File for Proxy Containers
	regFile := filepath.Join(r.tempDir, "registry-"+spec.Name+".json")
	_ = r.registry.SaveToFile(regFile)

	// 3. Start Database and Proxy Containers (if database is enabled)
	var dbContainerName string
	if serviceConfig.Infrastructure.Database && serviceConfig.Database != nil {
		dbType := serviceConfig.Database.Type
		dbPort := fmt.Sprintf("%d", serviceConfig.Database.Port)

		if dbType == "postgresql" {
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
			fmt.Println("Waiting for PostgreSQL to be ready...")
			if err := r.suite.orch.WaitTCPInternal(ctx, r.suite.networkName, "real-db:"+dbPort, 30*time.Second); err != nil {
				return fmt.Errorf("PostgreSQL not ready: %w", err)
			}

			// Start PostgreSQL proxy
			_, err = r.suite.orch.StartContainer(ctx, &container.Config{
				Image: "linespec:latest",
				Cmd:   []string{"proxy", "postgresql", "0.0.0.0:" + dbPort, "real-db:" + dbPort, "/app/registry/registry-" + spec.Name + ".json"},
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

			fmt.Println("✅ PostgreSQL proxy started")

			// Wait for proxy to be ready
			fmt.Println("Waiting for PostgreSQL proxy to be ready...")
			if err := r.suite.orch.WaitTCPInternal(ctx, r.suite.networkName, "db:"+dbPort, 30*time.Second); err != nil {
				return fmt.Errorf("PostgreSQL proxy not ready: %w", err)
			}
			fmt.Println("✅ PostgreSQL proxy is ready")

		} else if dbType == "mysql" {
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
						fmt.Printf("⚠️  Failed to write schema file: %v\n", err)
					} else {
						fmt.Printf("✅ Loaded shared schema for test\n")
					}
				} else {
					fmt.Printf("⚠️  Failed to read shared schema: %v\n", err)
				}
			} else {
				// Fallback: extract tables from spec and fetch fresh (for backward compatibility)
				fmt.Printf("📋 Shared schema not found, fetching per-test...\n")
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
						fmt.Printf("⚠️  Failed to fetch schema: %v\n", err)
					} else if len(schemaCache) > 0 {
						schemaData, _ := json.MarshalIndent(schemaCache, "", "  ")
						if err := os.WriteFile(schemaFile, schemaData, 0644); err != nil {
							fmt.Printf("⚠️  Failed to write schema file: %v\n", err)
						}
					}
				}
			}

			// Start database proxy
			fmt.Println("Starting MySQL proxy...")

			// Build proxy command with optional schema file
			proxyCmd := []string{
				"proxy", "mysql",
				"0.0.0.0:" + dbPort,
				"real-db:" + dbPort,
				"/app/registry/registry-" + spec.Name + ".json",
			}

			// Check if schema file exists and add it to command
			if _, err := os.Stat(schemaFile); err == nil {
				proxyCmd = append(proxyCmd, "/app/registry/schema-"+spec.Name+".json")
			}

			// Add transparent mode duration (0s) - schema is pre-loaded from shared file
			// This saves ~10s per test by eliminating the transparent mode wait
			proxyCmd = append(proxyCmd, "0s")

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
			fmt.Println("✅ MySQL proxy started")

			// Stream proxy logs for debugging (only during development)
			go func() {
				logCtx, logCancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer logCancel()
				_ = r.suite.orch.StreamLogs(logCtx, "proxy-db-"+spec.Name, os.Stdout, os.Stderr)
			}()
		}
	}

	// HTTP Proxy - always start for backward compatibility with user-service.local
	fmt.Println("Starting HTTP proxy...")
	_, err = r.suite.orch.StartContainer(ctx, &container.Config{
		Image: "linespec:latest",
		Cmd:   []string{"proxy", "http", "0.0.0.0:80", "unused", "/app/registry/registry-" + spec.Name + ".json"},
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
	fmt.Println("✅ HTTP proxy started")

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
	fmt.Println("Waiting for proxies to be ready...")
	if serviceConfig.Infrastructure.Database && serviceConfig.Database != nil {
		dbPort := fmt.Sprintf("%d", serviceConfig.Database.Port)
		if err := r.suite.orch.WaitTCPInternal(ctx, r.suite.networkName, "db:"+dbPort, 30*time.Second); err != nil {
			return fmt.Errorf("database proxy not ready: %w", err)
		}
	}
	if err := r.suite.orch.WaitTCPInternal(ctx, r.suite.networkName, "user-service.local:80", 30*time.Second); err != nil {
		return fmt.Errorf("HTTP proxy not ready: %w", err)
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
		// Default commands based on framework
		switch serviceConfig.Service.Framework {
		case "rails":
			startCmd = []string{"bash", "-c", "rm -f tmp/pids/server.pid && bundle exec rails server -b 0.0.0.0 -p " + appPort}
		default:
			startCmd = []string{"sh", "-c", "echo 'No start command specified'"}
		}
	}

	_, err = r.suite.orch.StartContainer(ctx, &container.Config{
		Image: serviceDir + ":latest",
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
	fmt.Printf("App started on host port: %s\n", hostPort)

	// 5. Wait for App
	fmt.Println("Waiting for App to be healthy...")
	healthURL := fmt.Sprintf("http://localhost:%s%s", hostPort, serviceConfig.Service.HealthEndpoint)
	if err := r.suite.orch.WaitHTTP(ctx, healthURL, 120*time.Second); err != nil {
		fmt.Println("❌ App failed to become healthy")
		logCtx, logCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer logCancel()
		_ = r.suite.orch.StreamLogs(logCtx, "app-"+spec.Name, os.Stdout, os.Stderr)
		return err
	}
	fmt.Println("✅ App is healthy")

	// Warmup for Rails apps to force schema/model loading
	if serviceConfig.Service.Framework == "rails" {
		fmt.Println("Warming up Rails app...")
		// Send a simple request to force Rails to load models
		warmupURL := fmt.Sprintf("http://localhost:%s%s", hostPort, serviceConfig.Service.HealthEndpoint)
		http.Get(warmupURL)
		time.Sleep(2 * time.Second) // Give Rails time to load models
	}

	// 6. Trigger Request
	fmt.Printf("🚀 Triggering RECEIVE: %s %s\n", spec.Receive.Method, spec.Receive.Path)
	resp, err := r.sendRequest(spec.Receive, spec.BaseDir, hostPort)
	if err != nil {
		fmt.Printf("❌ Trigger request failed: %v\n", err)
		return err
	}
	defer resp.Body.Close()
	fmt.Printf("✅ Received response: %d\n", resp.StatusCode)

	// 7. Verify Response
	if resp.StatusCode != spec.Respond.StatusCode {
		fmt.Printf("❌ Test failed with status %d. Fetching app logs...\n", resp.StatusCode)
		logCtx, logCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer logCancel()
		_ = r.suite.orch.StreamLogs(logCtx, "app-"+spec.Name, os.Stdout, os.Stderr)
		return fmt.Errorf("expected status %d, got %d", spec.Respond.StatusCode, resp.StatusCode)
	}

	if spec.Respond.WithFile != "" {
		loader := &dsl.PayloadLoader{BaseDir: spec.BaseDir}
		expected, err := loader.Load(spec.Respond.WithFile)
		if err != nil {
			return fmt.Errorf("failed to load expected response payload: %v", err)
		}

		actualRaw, _ := io.ReadAll(resp.Body)
		var actual interface{}
		_ = json.Unmarshal(actualRaw, &actual)

		if err := r.comparePayloads(expected, actual, spec.Respond.Noise); err != nil {
			fmt.Printf("❌ Response body mismatch: %v\n", err)
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
	time.Sleep(500 * time.Millisecond)

	if err := r.registry.VerifyAll(); err != nil {
		fmt.Printf("❌ Mock verification failed: %v\n", err)
		logCtx, logCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer logCancel()
		fmt.Println("📋 Fetching app logs for debugging...")
		_ = r.suite.orch.StreamLogs(logCtx, "app-"+spec.Name, os.Stdout, os.Stderr)
		return err
	}

	fmt.Println("✨ Test passed!")
	return nil
}

func (r *testRunner) collectHits(addr string) {
	fmt.Printf("Proxy: Collecting hits from %s...\n", addr)
	for i := 0; i < 5; i++ {
		resp, err := http.Get("http://" + addr + "/verify")
		if err != nil {
			time.Sleep(500 * time.Millisecond)
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
		loader := &dsl.PayloadLoader{BaseDir: baseDir}
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
			fmt.Printf("⚠️  Failed to fetch schema for table %s: %v\n", table, err)
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
				fmt.Printf("⚠️  Failed to scan column for table %s: %v\n", table, err)
				continue
			}
			columns = append(columns, col)
		}

		if err := rows.Err(); err != nil {
			fmt.Printf("⚠️  Error iterating rows for table %s: %v\n", table, err)
			continue
		}

		if len(columns) > 0 {
			schemaCache[table] = columns
			fmt.Printf("✅ Cached schema for table %s (%d columns)\n", table, len(columns))
		}
	}

	return schemaCache, nil
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
