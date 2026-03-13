//go:build !beta

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/livecodelife/linespec/pkg/config"
	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/provenance"
	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "provenance", "-p":
		runProvenance()
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	logger.Info(`LineSpec v1.0.0 - Provenance Records

Usage: linespec <command> [options]

Commands:
  provenance <subcommand>    Manage provenance records (alias: -p)

Use "linespec provenance <subcommand> --help" for more information.

Beta features (LineSpec Testing) are available by building with -tags beta:
  go build -tags beta -o linespec ./cmd/linespec`)
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
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.Create(opts); err != nil {
			os.Exit(1)
		}
	case "lint":
		opts := parseLintOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.Lint(opts); err != nil {
			os.Exit(1)
		}
	case "status":
		opts := parseStatusOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.Status(opts); err != nil {
			os.Exit(1)
		}
	case "graph":
		opts := parseGraphOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.Graph(opts); err != nil {
			os.Exit(1)
		}
	case "check":
		opts := parseCheckOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.Check(opts); err != nil {
			os.Exit(1)
		}
	case "lock-scope":
		opts := parseLockScopeOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.LockScope(opts); err != nil {
			os.Exit(1)
		}
	case "complete":
		opts := parseCompleteOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.Complete(opts); err != nil {
			os.Exit(1)
		}
	case "deprecate":
		opts := parseDeprecateOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
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
	return loadProvenanceConfigFromFile(".linespec.yml")
}

func loadProvenanceConfigFromFile(filePath string) *provenance.ProvenanceConfig {
	cfg := &provenance.ProvenanceConfig{
		Dir:               "provenance",
		Enforcement:       "warn",
		CommitTagRequired: false,
		AutoAffectedScope: true,
	}

	// Try to load from specified file if it exists
	if data, err := os.ReadFile(filePath); err == nil {
		var fullConfig config.LineSpecConfig
		if err := yaml.Unmarshal(data, &fullConfig); err == nil && fullConfig.Provenance != nil {
			// Get the directory containing the config file
			configDir := filepath.Dir(filePath)

			if fullConfig.Provenance.Dir != "" {
				// Make provenance dir relative to config file location
				cfg.Dir = filepath.Join(configDir, fullConfig.Provenance.Dir)
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

// reloadConfigIfNeeded reloads the config and commands if a custom config file is specified
func reloadConfigIfNeeded(cfg **provenance.ProvenanceConfig, cmds **provenance.Commands, configFile string, repoRoot string) error {
	if configFile != "" {
		*cfg = loadProvenanceConfigFromFile(configFile)
		newCmds, err := provenance.NewCommands(*cfg, repoRoot, os.Stdout, true)
		if err != nil {
			return fmt.Errorf("failed to initialize provenance with custom config: %w", err)
		}
		*cmds = newCmds
	}
	return nil
}

func printProvenanceUsage() {
	logger.Info(`Usage: linespec provenance <subcommand> [options]

Subcommands:
  create [options]           Create a new provenance record
  lint [options]             Validate provenance records
  status [options]           Show record status
  graph [options]            Render provenance graph
  check [options]            Check commits for violations
  lock-scope [options]       Lock scope to allowlist mode
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
		case "-i", "--id-suffix":
			if i+1 < len(args) {
				opts.IDSuffix = args[i+1]
				i++
			}
		case "-c", "--config":
			if i+1 < len(args) {
				opts.ConfigFile = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance create [options]

Options:
  --title "..."              Pre-populate the title field
  --supersedes prov-YYYY-NNN Pre-populate the supersedes field
  --tag tag1,tag2            Pre-populate tags
  --no-edit                  Write without opening editor
  -i, --id-suffix name       Append service suffix to ID (e.g., user-service)
  -c, --config path          Path to custom .linespec.yml file
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
		case "-c", "--config":
			if i+1 < len(args) {
				opts.ConfigFile = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance lint [options]

Options:
  --record prov-YYYY-NNN     Lint a single record
  --enforcement level        Override enforcement (none|warn|strict)
  --format format            Output format (human|json)
  -c, --config path          Path to custom .linespec.yml file
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
		case "--save-scope":
			opts.SaveScope = true
		case "-c", "--config":
			if i+1 < len(args) {
				opts.ConfigFile = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance status [options]

Options:
  --record prov-YYYY-NNN     Show detailed status for a record
  --filter status            Filter by status (open|implemented|superseded|deprecated)
  --filter tag:name          Filter by tag
  --format format            Output format (human|json)
  --save-scope               Persist auto-populated scope to file (only affects observed-mode records)
  -c, --config path          Path to custom .linespec.yml file
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
		case "-c", "--config":
			if i+1 < len(args) {
				opts.ConfigFile = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance graph [options]

Options:
  --root prov-YYYY-NNN       Start graph from a specific record
  --filter status            Show only records with given status
  --format format            Output format (human|json|dot)
  -c, --config path          Path to custom .linespec.yml file
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
		case "-c", "--config":
			if i+1 < len(args) {
				opts.ConfigFile = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance check [options]

Options:
  --commit SHA               Check a specific commit (default: HEAD)
  --range SHA..SHA           Check a range of commits
  --record prov-YYYY-NNN     Check only against a specific record
  -c, --config path          Path to custom .linespec.yml file
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
		case "-c", "--config":
			if i+1 < len(args) {
				opts.ConfigFile = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance lock-scope [options]

Options:
  --record prov-YYYY-NNN     Required. The record to lock
  --dry-run                  Print scope without writing
  -c, --config path          Path to custom .linespec.yml file
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
		case "-c", "--config":
			if i+1 < len(args) {
				opts.ConfigFile = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance complete [options]

Options:
  --record prov-YYYY-NNN     Required. The record to mark as implemented
  --force                    Skip LineSpec existence check
  -c, --config path          Path to custom .linespec.yml file
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
		case "-c", "--config":
			if i+1 < len(args) {
				opts.ConfigFile = args[i+1]
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
  --reason "..."             Reason for deprecation
  -c, --config path          Path to custom .linespec.yml file
  --help                     Show this help message`)
}
