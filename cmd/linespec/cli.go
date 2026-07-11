package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/livecodelife/linespec/pkg/embeddings"
	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/provenance"
	"github.com/spf13/cobra"
)

// debugFlag backs the root-level persistent -d/--debug flag.
var debugFlag bool

// newRootCmd builds the full linespec command tree. The provenance group is fully
// migrated to declarative Cobra subcommands; the remaining groups are bridged to
// their existing runX() handlers (DisableFlagParsing) until their own imprints land.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "linespec",
		Short:         "LineSpec - Provenance Records & Integration Testing",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: false,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if debugFlag {
				logger.SetLevel(logger.DebugLevel)
				logger.Debug("Debug mode enabled")
			}
		},
	}
	root.SetVersionTemplate("LineSpec v{{.Version}}\n")
	root.PersistentFlags().BoolVarP(&debugFlag, "debug", "d", false, "Enable debug logging")

	root.AddCommand(
		newInitCmd(),
		newCloneCmd(),
		newImportCmd(),
		newProxyCmd(),
		newTestCmd(),
		newBuildCmd(),
		newProvenanceCmd(),
	)
	return root
}

// newTestCmd is the declarative form of the `test` command. The positional
// <path> is bound via ExactArgs and debug logging is honored through the root
// persistent -d/--debug flag (handled in PersistentPreRun), so the per-command
// `linespec test --debug <path>` form keeps working. The Run wrapper calls the
// existing runTestWithCode handler unchanged.
func newTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test <path-to-linespec-or-dir>",
		Short: "Run .linespec test files",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			os.Exit(runTestWithCode(args[0]))
		},
	}
}

// newBuildCmd is the declarative form of the `build` command. It takes no flags
// or arguments; Cobra supplies --help/-h. The Run wrapper calls the existing
// runBuild Docker-image-build logic unchanged.
func newBuildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "build",
		Short: "Build the linespec:latest Docker image",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runBuild()
		},
	}
}

// newProxyCmd is the declarative form of the `proxy` command. The positional
// <type> <addr> <upstream> contract is bound via MinimumNArgs(3); any trailing
// positionals (registry file, mysql transparent duration) flow through to the
// handler as args[3:]. Every flag the legacy os.Args parser recognized is bound
// here with identical names/defaults so proxy sidecars and the runner see the
// same surface; pflag accepts both --flag value and --flag=value natively.
// --debug/-d is honored through the root persistent flag (PersistentPreRun), so
// `linespec proxy <type> ... --debug` keeps working. The Run wrapper calls the
// existing runProxyCore handler unchanged.
func newProxyCmd() *cobra.Command {
	var dbName, host, schemaData, schemaFile, sidecarPort, grpcDescriptorSet string
	cmd := &cobra.Command{
		Use:   "proxy <type> <listen-addr> <upstream-addr> [registry-file]",
		Short: "Start protocol proxy",
		Args:  cobra.MinimumNArgs(3),
		Run: func(cmd *cobra.Command, args []string) {
			runProxyCore(args[0], args[1], args[2], dbName, host, schemaData, schemaFile, sidecarPort, grpcDescriptorSet, args[3:])
		},
	}
	cmd.Flags().StringVar(&dbName, "db-name", "", "Database name (required for mysql proxy)")
	cmd.Flags().StringVar(&host, "host", "", "Advertised host (kafka proxy)")
	cmd.Flags().StringVar(&schemaData, "schema-data", "", "Base64-encoded schema (mysql proxy)")
	cmd.Flags().StringVar(&schemaFile, "schema-file", "", "Path to schema file (mysql proxy)")
	cmd.Flags().StringVar(&sidecarPort, "sidecar-port", "8081", "Verification sidecar HTTP port")
	cmd.Flags().StringVar(&grpcDescriptorSet, "grpc-descriptor-set", "", "Path to gRPC descriptor set (grpc proxy)")
	return cmd
}

// newInitCmd is the declarative form of the `init` command. It takes no
// positionals; the --force/-f, --project/-p, and --output/-o flags are bound with
// identical names/shorthands/defaults to the legacy os.Args parser. The -p
// shorthand is a command-local flag and does not collide with the root's -p
// (provenance) alias, which is rewritten in main() before Cobra parses. The Run
// wrapper calls the existing runInitCore handler unchanged.
func newInitCmd() *cobra.Command {
	var force bool
	var projectPath, outputPath string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Interactively create a .linespec.yml for your project",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runInitCore(force, projectPath, outputPath)
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Overwrite existing .linespec.yml without prompting")
	cmd.Flags().StringVarP(&projectPath, "project", "p", "", "Path to the project to set up (default: prompted)")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Directory where .linespec.yml is written (default: project path)")
	return cmd
}

// newCloneCmd is the declarative form of the `clone` command. The positional
// <manifest-url> is bound via ExactArgs(1); --version pins the manifest (the
// @version URL-suffix behavior is preserved inside manifest.Fetch) and --dir sets
// the destination directory. --version is a command-local flag and does not
// collide with the root's binary --version, which is a non-persistent flag on the
// root command only. The Run wrapper calls the existing runCloneCore handler
// unchanged.
func newCloneCmd() *cobra.Command {
	var version, dir string
	cmd := &cobra.Command{
		Use:   "clone <manifest-url>",
		Short: "Bootstrap a project from a published manifest",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runCloneCore(args[0], version, dir)
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "Pin to a specific manifest version (overrides @version suffix)")
	cmd.Flags().StringVar(&dir, "dir", "", "Destination directory name (default: derived from manifest URL)")
	return cmd
}

// newImportCmd is the declarative form of the `import` command. The positional
// <manifest-url> is bound via ExactArgs(1) and --version pins the manifest (the
// @version URL-suffix behavior is preserved inside manifest.Fetch). --version is a
// command-local flag and does not collide with the root's binary --version. The
// Run wrapper calls the existing runImportCore handler unchanged.
func newImportCmd() *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "import <manifest-url>",
		Short: "Import provenance records from a published manifest",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runImportCore(args[0], version)
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "Pin to a specific manifest version (overrides @version suffix)")
	return cmd
}

// provSetup mirrors the per-invocation setup that runProvenance performed before
// its switch: load the default config, build the embedder, and construct Commands.
func provSetup() (*provenance.ProvenanceConfig, *provenance.Commands, string, *embeddings.Client) {
	cfg := loadProvenanceConfig()
	repoRoot, _ := os.Getwd()

	var embedder *embeddings.Client
	if cfg.Embedding != nil {
		embedder, _ = embeddings.NewClient(*cfg.Embedding)
	}

	cmds, err := provenance.NewCommandsWithEmbedder(cfg, repoRoot, os.Stdout, true, embedder)
	if err != nil {
		logger.Error("Failed to initialize provenance: %v", err)
		os.Exit(1)
	}
	return cfg, cmds, repoRoot, embedder
}

// provCmds returns a Commands ready for the given --config override, replicating
// the setup + reloadConfigIfNeeded pair used by most runProvenance switch cases.
func provCmds(configFile string) *provenance.Commands {
	cfg, cmds, repoRoot, _ := provSetup()
	if err := reloadConfigIfNeeded(&cfg, &cmds, configFile, repoRoot); err != nil {
		logger.Error("Failed to reload config: %v", err)
		os.Exit(1)
	}
	return cmds
}

func newProvenanceCmd() *cobra.Command {
	prov := &cobra.Command{
		Use:   "provenance",
		Short: "Manage provenance records (alias: -p)",
		Long:  "Manage provenance records (alias: -p)",
	}
	prov.AddCommand(
		newProvCreateCmd(),
		newProvOpenCmd(),
		newProvLintCmd(),
		newProvStatusCmd(),
		newProvGraphCmd(),
		newProvCheckCmd(),
		newProvLockScopeCmd(),
		newProvAddScopeCmd(),
		newProvReconcileCmd(),
		newProvLockLayerCmd(),
		newProvCompleteCmd(),
		newProvDeprecateCmd(),
		newProvRunSpecsCmd(),
		newProvContextCmd(),
		newProvNextCmd(),
		newProvGovernCmd(),
		newProvSearchCmd(),
		newProvAuditCmd(),
		newProvIndexCmd(),
		newProvGenerateCmd(),
		newProvSyncCmd(),
		newProvCompileCmd(),
		newProvPublishCmd(),
		newProvInstallHooksCmd(),
		newProvInstallSkillsCmd(),
		newProvInstallPluginCmd(),
		newProvDiscoverCmd(),
	)
	return prov
}

func newProvCreateCmd() *cobra.Command {
	var opts provenance.CreateOptions
	var typeStr string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new provenance record (defaults to draft status)",
		Run: func(cmd *cobra.Command, args []string) {
			opts.Type = provenance.RecordType(typeStr)
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.Create(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Title, "title", "", "Pre-populate the title field")
	f.StringVar(&opts.Supersedes, "supersedes", "", "Pre-populate the supersedes field")
	f.StringSliceVar(&opts.Tags, "tag", nil, "Pre-populate tags (comma-separated; repeatable)")
	f.BoolVar(&opts.NoEdit, "no-edit", false, "Write without opening editor")
	f.StringVarP(&opts.IDSuffix, "id-suffix", "i", "", "Append service suffix to ID (e.g., user-service)")
	f.StringVar(&typeStr, "type", "", "Set the tier type of the record (brief|blueprint|imprint)")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	return cmd
}

func newProvOpenCmd() *cobra.Command {
	var opts provenance.OpenOptions
	cmd := &cobra.Command{
		Use:   "open",
		Short: "Transition a record from draft to open (enables enforcement)",
		Run: func(cmd *cobra.Command, args []string) {
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.Open(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.RecordID, "record", "", "Record to transition from draft to open")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	_ = cmd.MarkFlagRequired("record")
	return cmd
}

func newProvLintCmd() *cobra.Command {
	var opts provenance.LintOptions
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Validate provenance records (supports --format sarif)",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, cmds, repoRoot, embedder := provSetup()
			// Multi-config fan-out: when no explicit config is given (and not emitting
			// SARIF), lint every .linespec.yml discovered under the working tree.
			if opts.ConfigFile == "" && opts.Format != "sarif" {
				if configs := findAllLinespecConfigs("."); len(configs) > 0 {
					exitCode := 0
					for _, cfgPath := range configs {
						localCfg := loadProvenanceConfigFromFile(cfgPath)
						localCmds, err := provenance.NewCommandsWithEmbedder(localCfg, repoRoot, os.Stdout, true, embedder)
						if err != nil {
							logger.Error("Failed to initialize provenance for %s: %v", cfgPath, err)
							exitCode = 1
							continue
						}
						if err := localCmds.Lint(opts); err != nil {
							exitCode = 1
						}
					}
					if exitCode != 0 {
						os.Exit(exitCode)
					}
					return
				}
			}
			if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
				logger.Error("Failed to reload config: %v", err)
				os.Exit(1)
			}
			if err := cmds.Lint(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.RecordID, "record", "", "Lint a single record")
	f.StringVar(&opts.Enforcement, "enforcement", "", "Override enforcement (none|warn|strict)")
	f.StringVar(&opts.Format, "format", "", "Output format (human|json|sarif)")
	f.BoolVar(&opts.ShowWarn, "warn", false, "Show only warnings in output")
	f.BoolVar(&opts.ShowInfo, "info", false, "Show only informational hints in output")
	f.BoolVar(&opts.ShowAll, "all", false, "Show all output (errors, warnings, and hints)")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	return cmd
}

func newProvStatusCmd() *cobra.Command {
	var opts provenance.StatusOptions
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show record status",
		Run: func(cmd *cobra.Command, args []string) {
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.Status(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.RecordID, "record", "", "Show detailed status for a record")
	f.StringVar(&opts.Filter, "filter", "", "Filter by status (open|implemented|superseded|deprecated) or tag:name")
	f.StringVar(&opts.Format, "format", "", "Output format (human|json)")
	f.BoolVar(&opts.SaveScope, "save-scope", false, "Persist auto-populated scope to file (observed-mode records)")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	return cmd
}

func newProvGraphCmd() *cobra.Command {
	var opts provenance.GraphOptions
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Render provenance graph",
		Run: func(cmd *cobra.Command, args []string) {
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.Graph(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Root, "root", "", "Start graph from a specific record")
	f.StringVar(&opts.Filter, "filter", "", "Show only records with given status")
	f.StringVar(&opts.Format, "format", "", "Output format (human|json|dot)")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	return cmd
}

func newProvCheckCmd() *cobra.Command {
	var opts provenance.CheckOptions
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check commits for violations",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, cmds, repoRoot, embedder := provSetup()
			// Multi-config fan-out mirroring lint, with staged-file relevance filtering.
			if opts.ConfigFile == "" {
				if configs := findAllLinespecConfigs("."); len(configs) > 0 {
					exitCode := 0
					var stagedFiles []string
					if opts.Staged {
						stagedFiles, _ = cmds.Git.GetStagedFiles()
					}
					for _, cfgPath := range configs {
						if opts.Staged {
							cfgDir := filepath.Dir(cfgPath)
							rel, _ := filepath.Rel(".", cfgDir)
							if rel != "." {
								hasRelevant := false
								for _, fl := range stagedFiles {
									if strings.HasPrefix(fl, rel+"/") {
										hasRelevant = true
										break
									}
								}
								if !hasRelevant {
									continue
								}
							}
						}
						localCfg := loadProvenanceConfigFromFile(cfgPath)
						localCmds, err := provenance.NewCommandsWithEmbedder(localCfg, repoRoot, os.Stdout, true, embedder)
						if err != nil {
							logger.Error("Failed to initialize provenance for %s: %v", cfgPath, err)
							exitCode = 1
							continue
						}
						if err := localCmds.Check(opts); err != nil {
							exitCode = 1
						}
					}
					if exitCode != 0 {
						os.Exit(exitCode)
					}
					return
				}
			}
			if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
				logger.Error("Failed to reload config: %v", err)
				os.Exit(1)
			}
			if err := cmds.Check(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Commit, "commit", "", "Check a specific commit (default: HEAD)")
	f.StringVar(&opts.Range, "range", "", "Check a range of commits (SHA..SHA)")
	f.StringVar(&opts.Record, "record", "", "Check only against a specific record")
	f.BoolVar(&opts.Staged, "staged", false, "Check staged files instead of committed")
	f.StringVar(&opts.MessageFile, "message-file", "", "Path to commit message file (for staged mode)")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	return cmd
}

func newProvLockScopeCmd() *cobra.Command {
	var opts provenance.LockScopeOptions
	cmd := &cobra.Command{
		Use:   "lock-scope",
		Short: "Populate scope from git history (observed -> allowlist)",
		Run: func(cmd *cobra.Command, args []string) {
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.LockScope(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.RecordID, "record", "", "Required. The record to lock")
	f.BoolVar(&opts.DryRun, "dry-run", false, "Print scope without writing")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	_ = cmd.MarkFlagRequired("record")
	return cmd
}

// newProvAddScopeCmd is the dedicated scope-widening verb (prov-2026-8d2f5f2a):
// distinct from lock-scope's initial observed->allowlist populate, it adds
// newly-committed files to an already-allowlist record's affected_scope and
// materializes write permission for them in the same atomic operation.
func newProvAddScopeCmd() *cobra.Command {
	var opts provenance.AddScopeOptions
	cmd := &cobra.Command{
		Use:   "add-scope",
		Short: "Widen an already-allowlist record's scope with newly-committed files",
		Run: func(cmd *cobra.Command, args []string) {
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.AddScope(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.RecordID, "record", "", "Required. The record to widen")
	f.BoolVar(&opts.DryRun, "dry-run", false, "Print widened scope without writing")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	_ = cmd.MarkFlagRequired("record")
	return cmd
}

// newProvReconcileCmd is the dedicated fswrite reconcile verb (prov-2026-8d2f5f2a):
// re-derives the entire write-bit projection from current record state.
// `next` already runs this unconditionally on every invocation; this verb
// exists so it can also be invoked explicitly, without depending on the
// Claude Code plugin hooks being installed.
func newProvReconcileCmd() *cobra.Command {
	var opts provenance.ReconcileOptions
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Re-derive filesystem write permissions from current record state",
		Run: func(cmd *cobra.Command, args []string) {
			if jsonOut {
				opts.Format = "json"
			}
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.Reconcile(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.BoolVar(&jsonOut, "json", false, "Machine-readable output")
	f.StringVar(&opts.Format, "format", "", "Output format (human|json)")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	return cmd
}

func newProvLockLayerCmd() *cobra.Command {
	var opts provenance.LockLayerOptions
	cmd := &cobra.Command{
		Use:   "lock-layer",
		Short: "Create a locked layer record",
		Run: func(cmd *cobra.Command, args []string) {
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.LockLayer(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Title, "title", "", "Required. Title for the locked layer record")
	f.BoolVar(&opts.NoEdit, "no-edit", false, "Write without opening editor")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

func newProvCompleteCmd() *cobra.Command {
	var opts provenance.CompleteOptions
	cmd := &cobra.Command{
		Use:   "complete",
		Short: "Mark record as implemented",
		Run: func(cmd *cobra.Command, args []string) {
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.Complete(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.RecordID, "record", "", "Required. The record to mark as implemented")
	f.BoolVar(&opts.Force, "force", false, "Skip LineSpec existence check")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	_ = cmd.MarkFlagRequired("record")
	return cmd
}

func newProvDeprecateCmd() *cobra.Command {
	var opts provenance.DeprecateOptions
	cmd := &cobra.Command{
		Use:   "deprecate",
		Short: "Mark record as deprecated",
		Run: func(cmd *cobra.Command, args []string) {
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.Deprecate(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.RecordID, "record", "", "Required. The record to deprecate")
	f.StringVar(&opts.Reason, "reason", "", "Deprecation reason")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	_ = cmd.MarkFlagRequired("record")
	return cmd
}

func newProvRunSpecsCmd() *cobra.Command {
	var opts provenance.RunSpecsOptions
	cmd := &cobra.Command{
		Use:   "run-specs",
		Short: "Run associated_specs for a record (used by pre-commit hook)",
		Run: func(cmd *cobra.Command, args []string) {
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.RunSpecs(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.RecordID, "record", "r", "", "Record ID whose associated_specs to run")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	return cmd
}

func newProvContextCmd() *cobra.Command {
	var opts provenance.ContextOptions
	cmd := &cobra.Command{
		Use:   "context [files...]",
		Short: "Show provenance context for files",
		Run: func(cmd *cobra.Command, args []string) {
			opts.Files = append(opts.Files, args...)
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.Context(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&opts.Files, "files", nil, "Explicit file list (alternative to positional args)")
	f.StringVar(&opts.Format, "format", "", "Output format (human|compact|json)")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	return cmd
}

func newProvNextCmd() *cobra.Command {
	var opts provenance.NextOptions
	var plan []string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "next [files...]",
		Short: "Compute the single correct next provenance action",
		Run: func(cmd *cobra.Command, args []string) {
			opts.Files = append(opts.Files, plan...)
			opts.Files = append(opts.Files, args...)
			if jsonOut {
				opts.Format = "json"
			}
			// Fast path: serve from the cached scope index without a full record load.
			cfg := loadProvenanceConfig()
			repoRoot, _ := os.Getwd()
			if provenance.TryNextFromCache(cfg, repoRoot, os.Stdout, true, opts) {
				return
			}
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.Next(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&opts.Files, "files", nil, "Files you intend to change (plan before editing)")
	f.StringSliceVar(&plan, "plan", nil, "Alias for --files")
	f.BoolVar(&jsonOut, "json", false, "Machine-readable output (for hooks/agents)")
	f.StringVar(&opts.Format, "format", "", "Output format (human|json)")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	return cmd
}

func newProvGovernCmd() *cobra.Command {
	var opts provenance.GovernOptions
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "govern [files...]",
		Short: "List the active records that govern the given files",
		Run: func(cmd *cobra.Command, args []string) {
			opts.Files = append(opts.Files, args...)
			if jsonOut {
				opts.Format = "json"
			}
			cfg := loadProvenanceConfig()
			repoRoot, _ := os.Getwd()
			if provenance.TryGovernFromCache(cfg, repoRoot, os.Stdout, true, opts) {
				return
			}
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.Govern(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&opts.Files, "files", nil, "Files to look up governance for")
	f.BoolVar(&jsonOut, "json", false, "Machine-readable output (for hooks)")
	f.StringVar(&opts.Format, "format", "", "Output format (human|json)")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	return cmd
}

func newProvSearchCmd() *cobra.Command {
	var opts provenance.SearchOptions
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search provenance records by semantic similarity",
		Run: func(cmd *cobra.Command, args []string) {
			if opts.Query == "" && len(args) > 0 {
				opts.Query = args[0]
			}
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.Search(opts); err != nil {
				logger.Error("Search failed: %v", err)
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Query, "query", "", "Natural language search query")
	f.IntVar(&opts.Limit, "limit", 5, "Maximum results to return")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	return cmd
}

func newProvAuditCmd() *cobra.Command {
	var opts provenance.AuditOptions
	cmd := &cobra.Command{
		Use:   "audit [description]",
		Short: "Audit recent changes against provenance history",
		Run: func(cmd *cobra.Command, args []string) {
			if opts.Description == "" && len(args) > 0 {
				opts.Description = args[0]
			}
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.Audit(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Description, "description", "", "Description of recent changes to audit")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	return cmd
}

func newProvIndexCmd() *cobra.Command {
	var opts provenance.IndexOptions
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Index all implemented records for semantic search",
		Run: func(cmd *cobra.Command, args []string) {
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.Index(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.DryRun, "dry-run", false, "Show what would be indexed without doing it")
	f.BoolVar(&opts.Force, "force", false, "Re-index even if embedding already exists")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	return cmd
}

func newProvGenerateCmd() *cobra.Command {
	var opts provenance.GenerateOptions
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a behavioral specification document",
		Run: func(cmd *cobra.Command, args []string) {
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.Generate(opts); err != nil {
				logger.Error("%v", err)
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.RecordID, "record", "r", "", "Target a specific brief or blueprint record")
	f.StringVarP(&opts.Format, "format", "f", "", "Output format: markdown (default) or yaml")
	f.StringVarP(&opts.OutputFile, "output", "o", "", "Write output to a file instead of stdout")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	return cmd
}

func newProvSyncCmd() *cobra.Command {
	var opts provenance.SyncOptions
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Refresh cache for all configured shared_repos",
		Run: func(cmd *cobra.Command, args []string) {
			_, cmds, _, _ := provSetup()
			if err := cmds.Sync(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	cmd.Flags().BoolVar(&opts.Force, "force", false, "Ignore TTL and re-fetch all repos")
	return cmd
}

func newProvCompileCmd() *cobra.Command {
	var opts provenance.CompileOptions
	cmd := &cobra.Command{
		Use:   "compile",
		Short: "Rebuild the hash manifest from all provenance records",
		Run: func(cmd *cobra.Command, args []string) {
			cmds := provCmds(opts.ConfigFile)
			if err := cmds.Compile(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	cmd.Flags().StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	return cmd
}

func newProvPublishCmd() *cobra.Command {
	var opts provenance.PublishOptions
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Package records into a versioned linespec.manifest.json",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, cmds, repoRoot, _ := provSetup()
			if err := reloadConfigIfNeeded(&cfg, &cmds, "", repoRoot); err != nil {
				logger.Error("Failed to reload config: %v", err)
				os.Exit(1)
			}
			if opts.Name == "" {
				opts.Name = resolvePublishName(opts.ManifestPath)
			}
			if err := cmds.Publish(opts); err != nil {
				os.Exit(1)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.ManifestPath, "manifest", "", "Path to linespec.manifest.json (default: ./linespec.manifest.json)")
	f.StringVar(&opts.Name, "name", "", "Project name written to the manifest (prompted if omitted)")
	f.StringVar(&opts.Version, "version", "", "Explicit version label (default: auto-increment v1, v2, v3...)")
	f.StringVar(&opts.SpecsPath, "specs", "", "Path to specs artifact (file or directory)")
	f.StringVar(&opts.CodePath, "code", "", "Path to code artifact (file or directory)")
	f.StringVar(&opts.PromptPath, "prompt", "", "Path to prompt artifact file")
	return cmd
}

func newProvInstallHooksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install-hooks",
		Short: "Install git hooks",
		Run: func(cmd *cobra.Command, args []string) {
			_, cmds, _, _ := provSetup()
			if err := cmds.InstallHooks(); err != nil {
				logger.Error("Failed to install hooks: %v", err)
				os.Exit(1)
			}
		},
	}
}

func newProvInstallSkillsCmd() *cobra.Command {
	var opts provenance.InstallSkillsOptions
	cmd := &cobra.Command{
		Use:   "install-skills",
		Short: "Install all LineSpec Claude Code skills",
		Run: func(cmd *cobra.Command, args []string) {
			_, cmds, _, _ := provSetup()
			if err := cmds.InstallSkills(opts); err != nil {
				logger.Error("Failed to install skills: %v", err)
				os.Exit(1)
			}
		},
	}
	cmd.Flags().StringVar(&opts.Path, "path", "", "Target directory relative to repo root (default: .claude/skills)")
	return cmd
}

func newProvInstallPluginCmd() *cobra.Command {
	var opts provenance.InstallPluginOptions
	cmd := &cobra.Command{
		Use:   "install-plugin",
		Short: "Install the Claude Code provenance plugin",
		Run: func(cmd *cobra.Command, args []string) {
			_, cmds, _, _ := provSetup()
			if err := cmds.InstallPlugin(opts); err != nil {
				logger.Error("Failed to install plugin: %v", err)
				os.Exit(1)
			}
		},
	}
	cmd.Flags().StringVar(&opts.Path, "path", "", "Target plugins directory relative to repo root (default: .claude/plugins)")
	return cmd
}

func newProvDiscoverCmd() *cobra.Command {
	var opts discoverOptions
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Scan codebase and generate draft provenance records + .linespec stubs",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := loadProvenanceConfig()
			repoRoot, _ := os.Getwd()
			runDiscover(opts, cfg, repoRoot)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Dir, "dir", "", "Scope scan to a specific directory (default: repo root)")
	f.StringVar(&opts.Lang, "lang", "", "Language override (auto-detected if omitted)")
	f.StringVar(&opts.Framework, "framework", "", "Framework override (auto-detected if omitted)")
	f.BoolVar(&opts.Enrich, "enrich", false, "Populate intent fields from git history (Phase 5)")
	f.StringVar(&opts.LLMBaseURL, "llm-url", "", "Base URL for local LLM server (provider path appended automatically)")
	f.BoolVar(&opts.DryRun, "dry-run", false, "Print what would be generated without writing files")
	f.StringVar(&opts.Format, "format", "table", "Output format for --dry-run (table|json)")
	f.StringVarP(&opts.ConfigFile, "config", "c", "", "Path to custom .linespec.yml file")
	return cmd
}
