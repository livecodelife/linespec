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

	"github.com/calebcowen/linespec/pkg/config"
	"github.com/calebcowen/linespec/pkg/logger"
	"github.com/calebcowen/linespec/pkg/provenance"
	httpproxy "github.com/calebcowen/linespec/pkg/proxy/http"
	"github.com/calebcowen/linespec/pkg/proxy/kafka"
	"github.com/calebcowen/linespec/pkg/proxy/mysql"
	"github.com/calebcowen/linespec/pkg/proxy/postgresql"
	"github.com/calebcowen/linespec/pkg/registry"
	"github.com/calebcowen/linespec/pkg/runner"
	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "proxy":
		runProxy()
	case "test":
		runTest()
	case "provenance", "-p":
		runProvenance()
	default:
		printUsage()
		os.Exit(1)
	}
}

func runTest() {
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
  test [--debug] <path>      Run .linespec test files
  proxy <type> ...           Start protocol proxy
  provenance <subcommand>    Manage provenance records (alias: -p)

Use "linespec <command> --help" for more information about a command.`)
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

func runProvenance() {
	if len(os.Args) < 3 {
		printProvenanceUsage()
		os.Exit(1)
	}

	subcommand := os.Args[2]
	args := os.Args[3:]

	// Load configuration
	cfg := loadProvenanceConfig()

	// Get repo root
	repoRoot, _ := os.Getwd()

	// Create commands
	cmds, err := provenance.NewCommands(cfg, repoRoot, os.Stdout, true)
	if err != nil {
		logger.Error("Failed to initialize provenance: %v", err)
		os.Exit(1)
	}

	switch subcommand {
	case "create":
		opts := parseCreateOptions(args)
		if err := cmds.Create(opts); err != nil {
			os.Exit(1)
		}
	case "lint":
		opts := parseLintOptions(args)
		if err := cmds.Lint(opts); err != nil {
			os.Exit(1)
		}
	case "status":
		opts := parseStatusOptions(args)
		if err := cmds.Status(opts); err != nil {
			os.Exit(1)
		}
	case "graph":
		opts := parseGraphOptions(args)
		if err := cmds.Graph(opts); err != nil {
			os.Exit(1)
		}
	case "check":
		opts := parseCheckOptions(args)
		if err := cmds.Check(opts); err != nil {
			os.Exit(1)
		}
	case "lock-scope":
		opts := parseLockScopeOptions(args)
		if err := cmds.LockScope(opts); err != nil {
			os.Exit(1)
		}
	case "complete":
		opts := parseCompleteOptions(args)
		if err := cmds.Complete(opts); err != nil {
			os.Exit(1)
		}
	case "deprecate":
		opts := parseDeprecateOptions(args)
		if err := cmds.Deprecate(opts); err != nil {
			os.Exit(1)
		}
	case "install-hooks":
		if err := cmds.InstallHooks(); err != nil {
			logger.Error("Failed to install hooks: %v", err)
			os.Exit(1)
		}
	case "--help", "-h":
		printProvenanceUsage()
	default:
		logger.Error("Unknown provenance subcommand: %s", subcommand)
		printProvenanceUsage()
		os.Exit(1)
	}
}

func loadProvenanceConfig() *provenance.ProvenanceConfig {
	cfg := &provenance.ProvenanceConfig{
		Dir:               "provenance",
		Enforcement:       "warn",
		CommitTagRequired: false,
		AutoAffectedScope: true,
	}

	// Try to load from .linespec.yml if it exists
	if data, err := os.ReadFile(".linespec.yml"); err == nil {
		var fullConfig config.LineSpecConfig
		if err := yaml.Unmarshal(data, &fullConfig); err == nil && fullConfig.Provenance != nil {
			if fullConfig.Provenance.Dir != "" {
				cfg.Dir = fullConfig.Provenance.Dir
			}
			if fullConfig.Provenance.Enforcement != "" {
				cfg.Enforcement = fullConfig.Provenance.Enforcement
			}
			cfg.CommitTagRequired = fullConfig.Provenance.CommitTagRequired
			cfg.AutoAffectedScope = fullConfig.Provenance.AutoAffectedScope
			cfg.SharedRepos = fullConfig.Provenance.SharedRepos
		}
	}

	return cfg
}

func printProvenanceUsage() {
	logger.Info(`Usage: linespec provenance <subcommand> [options]

Subcommands:
  create [options]           Create a new provenance record
  lint [options]             Validate provenance records
  status [options]           Show record status
  graph [options]            Render provenance graph
  check [options]            Check commits for violations
  lock-scope [options]     Lock scope to allowlist mode
  complete [options]         Mark record as implemented
  deprecate [options]        Mark record as deprecated
  install-hooks              Install git hooks

Use "linespec provenance <subcommand> --help" for more information.`)
}

func parseCreateOptions(args []string) provenance.CreateOptions {
	opts := provenance.CreateOptions{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--title":
			if i+1 < len(args) {
				opts.Title = args[i+1]
				i++
			}
		case "--supersedes":
			if i+1 < len(args) {
				opts.Supersedes = args[i+1]
				i++
			}
		case "--tag":
			if i+1 < len(args) {
				opts.Tags = append(opts.Tags, strings.Split(args[i+1], ",")...)
				i++
			}
		case "--no-edit":
			opts.NoEdit = true
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance create [options]

Options:
  --title "..."              Pre-populate the title field
  --supersedes prov-YYYY-NNN Pre-populate the supersedes field
  --tag tag1,tag2            Pre-populate tags
  --no-edit                  Write without opening editor
  --help                     Show this help message`)
			os.Exit(0)
		}
	}

	return opts
}

func parseLintOptions(args []string) provenance.LintOptions {
	opts := provenance.LintOptions{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--record":
			if i+1 < len(args) {
				opts.RecordID = args[i+1]
				i++
			}
		case "--enforcement":
			if i+1 < len(args) {
				opts.Enforcement = args[i+1]
				i++
			}
		case "--format":
			if i+1 < len(args) {
				opts.Format = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance lint [options]

Options:
  --record prov-YYYY-NNN     Lint a single record
  --enforcement level        Override enforcement (none|warn|strict)
  --format format            Output format (human|json)
  --help                     Show this help message`)
			os.Exit(0)
		}
	}

	return opts
}

func parseStatusOptions(args []string) provenance.StatusOptions {
	opts := provenance.StatusOptions{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--record":
			if i+1 < len(args) {
				opts.RecordID = args[i+1]
				i++
			}
		case "--filter":
			if i+1 < len(args) {
				opts.Filter = args[i+1]
				i++
			}
		case "--format":
			if i+1 < len(args) {
				opts.Format = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance status [options]

Options:
  --record prov-YYYY-NNN     Show detailed status for a record
  --filter status            Filter by status (open|implemented|superseded|deprecated)
  --filter tag:name          Filter by tag
  --format format            Output format (human|json)
  --help                     Show this help message`)
			os.Exit(0)
		}
	}

	return opts
}

func parseGraphOptions(args []string) provenance.GraphOptions {
	opts := provenance.GraphOptions{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--root":
			if i+1 < len(args) {
				opts.Root = args[i+1]
				i++
			}
		case "--filter":
			if i+1 < len(args) {
				opts.Filter = args[i+1]
				i++
			}
		case "--format":
			if i+1 < len(args) {
				opts.Format = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance graph [options]

Options:
  --root prov-YYYY-NNN       Start graph from a specific record
  --filter status            Show only records with given status
  --format format            Output format (human|json|dot)
  --help                     Show this help message`)
			os.Exit(0)
		}
	}

	return opts
}

func parseCheckOptions(args []string) provenance.CheckOptions {
	opts := provenance.CheckOptions{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--commit":
			if i+1 < len(args) {
				opts.Commit = args[i+1]
				i++
			}
		case "--range":
			if i+1 < len(args) {
				opts.Range = args[i+1]
				i++
			}
		case "--record":
			if i+1 < len(args) {
				opts.Record = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance check [options]

Options:
  --commit SHA               Check a specific commit (default: HEAD)
  --range SHA..SHA           Check a range of commits
  --record prov-YYYY-NNN     Check only against a specific record
  --help                     Show this help message`)
			os.Exit(0)
		}
	}

	return opts
}

func parseLockScopeOptions(args []string) provenance.LockScopeOptions {
	opts := provenance.LockScopeOptions{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--record":
			if i+1 < len(args) {
				opts.RecordID = args[i+1]
				i++
			}
		case "--dry-run":
			opts.DryRun = true
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance lock-scope [options]

Options:
  --record prov-YYYY-NNN     Required. The record to lock
  --dry-run                  Print scope without writing
  --help                     Show this help message`)
			os.Exit(0)
		}
	}

	if opts.RecordID == "" {
		logger.Error("--record is required")
		printLockScopeUsage()
		os.Exit(1)
	}

	return opts
}

func parseCompleteOptions(args []string) provenance.CompleteOptions {
	opts := provenance.CompleteOptions{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--record":
			if i+1 < len(args) {
				opts.RecordID = args[i+1]
				i++
			}
		case "--force":
			opts.Force = true
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance complete [options]

Options:
  --record prov-YYYY-NNN     Required. The record to mark as implemented
  --force                    Skip LineSpec existence check
  --help                     Show this help message`)
			os.Exit(0)
		}
	}

	if opts.RecordID == "" {
		logger.Error("--record is required")
		printCompleteUsage()
		os.Exit(1)
	}

	return opts
}

func parseDeprecateOptions(args []string) provenance.DeprecateOptions {
	opts := provenance.DeprecateOptions{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--record":
			if i+1 < len(args) {
				opts.RecordID = args[i+1]
				i++
			}
		case "--reason":
			if i+1 < len(args) {
				opts.Reason = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance deprecate [options]

Options:
  --record prov-YYYY-NNN     Required. The record to deprecate
  --reason "..."             Deprecation reason
  --help                     Show this help message`)
			os.Exit(0)
		}
	}

	if opts.RecordID == "" {
		logger.Error("--record is required")
		printDeprecateUsage()
		os.Exit(1)
	}

	return opts
}

func printLockScopeUsage() {
	logger.Info(`Usage: linespec provenance lock-scope --record prov-YYYY-NNN [options]

Options:
  --record prov-YYYY-NNN     Required. The record to lock
  --dry-run                  Print scope without writing
  --help                     Show this help message`)
}

func printCompleteUsage() {
	logger.Info(`Usage: linespec provenance complete --record prov-YYYY-NNN [options]

Options:
  --record prov-YYYY-NNN     Required. The record to mark as implemented
  --force                    Skip LineSpec existence check
  --help                     Show this help message`)
}

func printDeprecateUsage() {
	logger.Info(`Usage: linespec provenance deprecate --record prov-YYYY-NNN [options]

Options:
  --record prov-YYYY-NNN     Required. The record to deprecate
  --reason "..."             Deprecation reason
  --help                     Show this help message`)
}
