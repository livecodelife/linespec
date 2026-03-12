package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/calebcowen/linespec/pkg/logger"
	httpproxy "github.com/calebcowen/linespec/pkg/proxy/http"
	"github.com/calebcowen/linespec/pkg/proxy/kafka"
	"github.com/calebcowen/linespec/pkg/proxy/mysql"
	"github.com/calebcowen/linespec/pkg/proxy/postgresql"
	"github.com/calebcowen/linespec/pkg/registry"
	"github.com/calebcowen/linespec/pkg/runner"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	if os.Args[1] == "proxy" {
		runProxy()
		return
	}

	if os.Args[1] != "test" {
		printUsage()
		os.Exit(1)
	}

	// Parse test command arguments
	args := os.Args[2:]
	debug := false
	var path string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--debug", "-d":
			debug = true
		case "--help", "-h":
			printTestUsage()
			os.Exit(0)
		default:
			if !strings.HasPrefix(args[i], "-") {
				path = args[i]
			} else {
				logger.Error("Unknown flag: %s", args[i])
				printTestUsage()
				os.Exit(1)
			}
		}
	}

	if path == "" {
		logger.Error("Usage: linespec test [--debug] <path-to-linespec-or-dir>")
		os.Exit(1)
	}

	// Set log level based on debug flag
	if debug {
		logger.SetLevel(logger.DebugLevel)
		logger.Debug("Debug mode enabled")
	}

	ctx := context.Background()

	fileInfo, err := os.Stat(path)
	if err != nil {
		logger.Error("Error: %v", err)
		os.Exit(1)
	}

	var testFiles []string
	if fileInfo.IsDir() {
		err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			// Only include files with .linespec extension, excluding files like Dockerfile.linespec
			if !info.IsDir() && filepath.Ext(p) == ".linespec" && !strings.Contains(strings.ToLower(filepath.Base(p)), "dockerfile") {
				testFiles = append(testFiles, p)
			}
			return nil
		})
		if err != nil {
			logger.Error("Error walking path: %v", err)
			os.Exit(1)
		}
	} else {
		testFiles = append(testFiles, path)
	}

	if len(testFiles) == 0 {
		logger.Info("No .linespec files found.")
		return
	}

	// Create test suite with shared infrastructure
	suite, err := runner.NewTestSuite()
	if err != nil {
		logger.Error("Failed to create test suite: %v", err)
		os.Exit(1)
	}

	// Setup shared infrastructure once
	setupStop := logger.ShowSpinner("Setting up tests...")
	infraCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	setupErr := suite.SetupSharedInfrastructure(infraCtx)
	cancel()
	logger.StopSpinner(setupStop)

	if setupErr != nil {
		logger.Error("Failed to setup infrastructure: %v", setupErr)
		os.Exit(1)
	}
	logger.SetupComplete()

	// Cleanup shared infrastructure when done
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		suite.CleanupSharedInfrastructure(cleanupCtx)
		cancel()
	}()

	passed := 0
	failed := 0

	for i, file := range testFiles {
		logger.TestRunning(i+1, len(testFiles), file)

		testCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)

		func() {
			defer cancel()

			// Show spinner during test execution
			testStop := logger.TestingMessage()

			defer func() {
				if r := recover(); r != nil {
					logger.StopSpinner(testStop)
					logger.TestFailed(file, fmt.Errorf("PANIC: %v", r))
					failed++
				}
			}()

			if err := suite.RunTest(testCtx, file); err != nil {
				logger.StopSpinner(testStop)
				logger.TestFailed(file, err)
				failed++
			} else {
				logger.StopSpinner(testStop)
				logger.TestPassed()
				passed++
			}
		}()
	}

	logger.Summary(len(testFiles), passed, failed)

	if failed > 0 {
		os.Exit(1)
	}
}

func printUsage() {
	logger.Info(`Usage: linespec <command> [options]

Commands:
  test [--debug] <path>  Run .linespec test files
  proxy <type> ...       Start protocol proxy

Use "linespec test --help" for more information about test command.`)
}

func printTestUsage() {
	logger.Info(`Usage: linespec test [--debug] <path-to-linespec-or-dir>

Options:
  --debug, -d    Show detailed debug logs
  --help, -h     Show this help message

Examples:
  linespec test ./tests/              # Run all tests in directory
  linespec test --debug ./tests/       # Run with debug output
  linespec test ./tests/create.linespec # Run single test file`)
}

func runProxy() {
	if len(os.Args) < 5 {
		logger.Error("Usage: linespec proxy <type> <listen-addr> <upstream-addr> [registry-file] [schema-file]")
		os.Exit(1)
	}

	// Change working directory to /app/project if it exists (inside container)
	if _, err := os.Stat("/app/project"); err == nil {
		os.Chdir("/app/project")
		logger.Debug("Changed working directory to /app/project")
	}

	pType := os.Args[2]
	addr := os.Args[3]
	upstream := os.Args[4]

	reg := registry.NewMockRegistry()
	if len(os.Args) > 5 {
		regFile := os.Args[5]
		if err := reg.LoadFromFile(regFile); err != nil {
			logger.Error("Failed to load registry: %v", err)
			os.Exit(1)
		}
		logger.Debug("Loaded registry from %s", regFile)

		// Debug: Print registry contents
		data, _ := os.ReadFile(regFile)
		logger.Debug("Registry file size: %d bytes", len(data))
	}

	// Start a sidecar HTTP server for verification
	srv := &http.Server{Addr: "0.0.0.0:8081"}
	http.HandleFunc("/verify", func(w http.ResponseWriter, r *http.Request) {
		hits := reg.GetHits()
		json.NewEncoder(w).Encode(hits)
	})

	go func() {
		logger.Debug("Verification sidecar listening on 0.0.0.0:8081")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Verification sidecar error: %v", err)
		}
	}()

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	var proxyErr error
	switch pType {
	case "mysql":
		p := mysql.NewProxy(addr, upstream, reg)
		// Load schema file if provided (6th argument)
		if len(os.Args) > 6 {
			schemaFile := os.Args[6]
			logger.Debug("Loading schema file: %s", schemaFile)
			if _, err := os.Stat(schemaFile); err != nil {
				logger.Debug("Schema file does not exist: %v", err)
			} else {
				if err := p.LoadSchema(schemaFile); err != nil {
					logger.Error("Failed to load schema file: %v", err)
					// Don't exit - schema is optional
				}
			}
		} else {
			logger.Debug("No schema file provided (len(os.Args) = %d)", len(os.Args))
		}
		// Check for transparent mode duration (7th argument)
		if len(os.Args) > 7 {
			transparentDuration := os.Args[7]
			if duration, err := time.ParseDuration(transparentDuration); err == nil {
				p.EnableTransparentMode(duration)
			} else {
				logger.Error("Invalid transparent duration: %v", err)
			}
		}
		proxyErr = p.Start(ctx)
	case "postgresql":
		p := postgresql.NewProxy(addr, upstream, reg)
		proxyErr = p.Start(ctx)
	case "http":
		p := httpproxy.NewInterceptor(addr, reg)
		proxyErr = p.Start(ctx)
	case "kafka":
		p := kafka.NewInterceptor(addr, reg)
		proxyErr = p.Start(ctx)
	default:
		logger.Error("Unknown proxy type: %s", pType)
		os.Exit(1)
	}

	if proxyErr != nil {
		logger.Error("Proxy error: %v", proxyErr)
		os.Exit(1)
	}

	// Block until context is cancelled
	<-ctx.Done()
}
