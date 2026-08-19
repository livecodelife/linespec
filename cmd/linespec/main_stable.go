package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	debugpkg "runtime/debug"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/livecodelife/linespec/v3/pkg/config"
	"github.com/livecodelife/linespec/v3/pkg/discover/boundaries"
	"github.com/livecodelife/linespec/v3/pkg/discover/enrich"
	"github.com/livecodelife/linespec/v3/pkg/discover/framework"
	"github.com/livecodelife/linespec/v3/pkg/discover/graph"
	"github.com/livecodelife/linespec/v3/pkg/discover/lang"
	discoverrecords "github.com/livecodelife/linespec/v3/pkg/discover/records"
	discoverroutes "github.com/livecodelife/linespec/v3/pkg/discover/routes"
	"github.com/livecodelife/linespec/v3/pkg/discover/stubs"
	"github.com/livecodelife/linespec/v3/pkg/discover/symbols"
	"github.com/livecodelife/linespec/v3/pkg/dsl"
	"github.com/livecodelife/linespec/v3/pkg/embeddings"
	"github.com/livecodelife/linespec/v3/pkg/initcmd"
	"github.com/livecodelife/linespec/v3/pkg/interpolate"
	"github.com/livecodelife/linespec/v3/pkg/logger"
	"github.com/livecodelife/linespec/v3/pkg/manifest"
	"github.com/livecodelife/linespec/v3/pkg/provenance"
	grpcproxy "github.com/livecodelife/linespec/v3/pkg/proxy/grpc"
	httpproxy "github.com/livecodelife/linespec/v3/pkg/proxy/http"
	"github.com/livecodelife/linespec/v3/pkg/proxy/kafka"
	mongodbproxy "github.com/livecodelife/linespec/v3/pkg/proxy/mongodb"
	"github.com/livecodelife/linespec/v3/pkg/proxy/mysql"
	"github.com/livecodelife/linespec/v3/pkg/proxy/postgresql"
	redisproxy "github.com/livecodelife/linespec/v3/pkg/proxy/redis"
	"github.com/livecodelife/linespec/v3/pkg/registry"
	"github.com/livecodelife/linespec/v3/pkg/runner"
	"gopkg.in/yaml.v3"
)

// version is injected at build time via -X main.version (goreleaser and
// `make build`). commit and date are likewise injected by goreleaser; they are
// declared here so those -X ldflags are effective rather than silent no-ops.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

// displayVersion is the version string rendered by `--version`. It is resolved
// from the ldflag when set, otherwise from the module version embedded in the
// binary's build info — so a plain `go install` (which applies no ldflags)
// still reports the release it was built from instead of "dev".
func displayVersion() string {
	info, ok := debugpkg.ReadBuildInfo()
	return resolveDisplayVersion(version, info, ok)
}

// resolveDisplayVersion picks the best available version string. Preference:
// an explicit ldflag value, then the build-info module version (with any
// +dirty/local suffix stripped), then the raw ldflag, then "dev". The result is
// returned without a leading "v" so the "LineSpec v{{.Version}}" template never
// double-prints it (build info reports "v3.15.0" while the ldflag is bare).
// Kept pure so it can be unit-tested without a real build info.
func resolveDisplayVersion(ldflagVersion string, info *debugpkg.BuildInfo, infoOK bool) string {
	if ldflagVersion != "" && ldflagVersion != "dev" {
		return strings.TrimPrefix(ldflagVersion, "v")
	}
	if infoOK && info != nil {
		v := info.Main.Version
		if idx := strings.Index(v, "+"); idx >= 0 {
			v = v[:idx]
		}
		if v != "" && v != "(devel)" {
			return strings.TrimPrefix(v, "v")
		}
	}
	if ldflagVersion != "" {
		return strings.TrimPrefix(ldflagVersion, "v")
	}
	return "dev"
}

// buildMetadata returns the injected commit/date suffix for the version line,
// empty when neither was injected (local dev / go install).
func buildMetadata() string {
	switch {
	case commit != "" && date != "":
		return fmt.Sprintf(" (%s, %s)", commit, date)
	case commit != "":
		return fmt.Sprintf(" (%s)", commit)
	default:
		return ""
	}
}

func main() {
	// Preserve the legacy `-p` alias for the provenance command. Cobra cannot
	// register a flag-like command alias, so rewrite it before dispatch.
	if len(os.Args) > 1 && os.Args[1] == "-p" {
		os.Args[1] = "provenance"
	}
	root := newRootCmd()
	// gen-docs is a hidden, build-time-only command: it generates the CLI
	// reference for the docs site from the command tree and is registered here
	// (not in newRootCmd) so it never becomes part of the normal command set
	// users see. It does not alter any shipped command's behavior.
	root.AddCommand(newGenDocsCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// newGenDocsCmd builds the hidden `gen-docs` command. It renders the full CLI
// reference as a single Markdown page from the live Cobra command tree via
// spf13/cobra/doc, so the published reference is regenerated from the code on
// every docs build and can no longer drift. The command is hidden and only used
// by scripts/build-docs.sh; it changes no shipped command's behavior.
func newGenDocsCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:    "gen-docs",
		Short:  "Generate the CLI reference (Markdown) from the command tree",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return generateCLIReference(cmd.Root(), out)
		},
	}
	cmd.Flags().StringVar(&out, "out", "docs/CLI_REFERENCE.md",
		"output Markdown file for the generated CLI reference")
	return cmd
}

// generateCLIReference renders every command in root into one Markdown file.
// Output is deterministic/byte-stable: DisableAutoGenTag strips cobra's
// timestamped footer, and the per-command files are concatenated in a stable
// (lexical) order. Cross-command links are rewritten to in-page gfm anchors so
// the single rendered page stays navigable.
func generateCLIReference(root *cobra.Command, out string) error {
	// Strip the "Auto generated by spf13/cobra on <date>" footer. Set on root;
	// cobra/doc propagates it to every descendant it renders.
	root.DisableAutoGenTag = true

	tmp, err := os.MkdirTemp("", "linespec-cli-docs-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// e.g. "linespec_provenance_create.md" -> "#linespec-provenance-create",
	// matching the gfm heading id pandoc derives from "## linespec provenance create".
	linkHandler := func(name string) string {
		anchor := strings.TrimSuffix(name, ".md")
		anchor = strings.ReplaceAll(anchor, "_", "-")
		return "#" + anchor
	}
	noPrepend := func(string) string { return "" }
	if err := doc.GenMarkdownTreeCustom(root, tmp, noPrepend, linkHandler); err != nil {
		return err
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var buf bytes.Buffer
	buf.WriteString("# LineSpec CLI Reference\n\n")
	buf.WriteString("> Generated from the Cobra command tree by `linespec gen-docs`. Do not edit by hand.\n\n")
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(tmp, n))
		if err != nil {
			return err
		}
		buf.Write(b)
		if !bytes.HasSuffix(b, []byte("\n")) {
			buf.WriteByte('\n')
		}
		buf.WriteByte('\n')
	}

	if dir := filepath.Dir(out); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(out, buf.Bytes(), 0o644)
}

// runTestWithCode executes the full test run and returns an exit code.
// Using a return value instead of os.Exit ensures all deferred cleanups run.
func runTestWithCode(path string) int {
	// Create a cancellable root context. Signal handling below will cancel it,
	// which propagates into every in-flight container operation.
	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	// Catch SIGINT (Ctrl+C) and SIGTERM so we can clean up Docker resources
	// before the process exits.  Without this, Go's default signal handling
	// terminates the process immediately and deferred cleanups never run.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			logger.Info("\nReceived %v, cleaning up Docker resources...", sig)
			cancelCtx()
		case <-ctx.Done():
		}
	}()
	defer signal.Stop(sigCh)

	fileInfo, err := os.Stat(path)
	if err != nil {
		logger.Error("Error: %v", err)
		return 1
	}

	var testFiles []string
	if fileInfo.IsDir() {
		err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			// Only include files with .linespec extension, excluding files like Dockerfile.linespec
			if !info.IsDir() && filepath.Ext(p) == ".linespec" && filepath.Base(p) != "Dockerfile.linespec" {
				testFiles = append(testFiles, p)
			}
			return nil
		})
		if err != nil {
			logger.Error("Error walking path: %v", err)
			return 1
		}
	} else {
		testFiles = append(testFiles, path)
	}

	if len(testFiles) == 0 {
		logger.Info("No .linespec files found.")
		return 0
	}

	// Create test suite with shared infrastructure
	suite, err := runner.NewTestSuite()
	if err != nil {
		logger.Error("Failed to create test suite: %v", err)
		return 1
	}

	// Register cleanup before attempting setup so that partial infrastructure
	// (containers started before an error) is always torn down on exit.
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		suite.CleanupSharedInfrastructure(cleanupCtx)
	}()

	// Setup shared infrastructure once
	setupStop := logger.ShowSpinner("Setting up tests...")
	infraCtx, infraCancel := context.WithTimeout(ctx, 2*time.Minute)
	setupErr := suite.SetupSharedInfrastructure(infraCtx)
	infraCancel()
	logger.StopSpinner(setupStop)

	if setupErr != nil {
		logger.Error("Failed to setup infrastructure: %v", setupErr)
		return 1
	}
	logger.SetupComplete()

	passed := 0
	failed := 0

	for i, file := range testFiles {
		// Stop running more tests if the context was cancelled (e.g. Ctrl+C).
		select {
		case <-ctx.Done():
			logger.Info("Interrupted, stopping test execution")
			return 1
		default:
		}

		logger.TestRunning(i+1, len(testFiles), file)

		// Determine per-test timeout:
		// 1. Use TIMEOUT directive from the .linespec file if present.
		// 2. Fall back to timeout_seconds from .linespec.yml (default: 180s).
		var testTimeout time.Duration
		if tokens, lexErr := dsl.LexFile(file); lexErr == nil {
			parser := dsl.NewParser(tokens)
			if spec, parseErr := parser.Parse(file); parseErr == nil {
				testTimeout = spec.Timeout
			}
		}
		if testTimeout == 0 {
			if serviceConfig, cfgErr := config.LoadConfig(filepath.Dir(file)); cfgErr == nil {
				testTimeout = time.Duration(serviceConfig.TestTimeoutSeconds) * time.Second
			}
		}
		if testTimeout == 0 {
			testTimeout = 3 * time.Minute
		}

		testCtx, cancel := context.WithTimeout(ctx, testTimeout)

		func() {
			defer cancel()

			// Show spinner during test execution
			testStop := logger.TestingMessage()

			defer func() {
				if r := recover(); r != nil {
					logger.StopSpinner(testStop)
					stack := debugpkg.Stack()
					logger.TestFailed(file, fmt.Errorf("PANIC: %v\nStack trace:\n%s", r, stack))
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
		return 1
	}
	return 0
}

func runBuild() {
	execPath, err := os.Executable()
	if err != nil {
		logger.Error("Failed to locate linespec executable: %v", err)
		os.Exit(1)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		logger.Error("Failed to resolve executable path: %v", err)
		os.Exit(1)
	}

	// The Docker image runs Linux. On non-Linux hosts the host binary is the
	// wrong format (Mach-O on macOS, PE on Windows) for the Alpine container,
	// so a Linux binary has to be produced some other way. cmd/linespec pulls
	// in go-tree-sitter (via pkg/discover), a cgo package, so that binary must
	// be built with cgo enabled — the grammar packages' cgo files (which
	// define the `Node` type) are excluded by the build constraints when cgo
	// is disabled, and the remaining pure-Go files fail with "undefined:
	// Node". A host-side `go build GOOS=linux CGO_ENABLED=0` cross-compile
	// cannot produce a working binary at all now that tree-sitter is in the
	// dependency graph (prov-2026-b9339a5a), and replicating a Linux C
	// toolchain on the host isn't worth it when Dockerfile.linespec already
	// builds the Linux binary correctly (CGO_ENABLED=1, build-base installed)
	// inside its golang:1.23-alpine builder stage. So build the image
	// directly from source instead of staging a cross-compiled binary.
	if runtime.GOOS != "linux" {
		if err := buildFromSourceCheckout(execPath); err != nil {
			logger.Error("%v", err)
			os.Exit(1)
		}
		return
	}

	// Host is already Linux: the executable is a native (non-cross-compiled)
	// build, so it can be packaged as-is.
	if err := buildFromHostBinary(execPath); err != nil {
		logger.Error("%v", err)
		os.Exit(1)
	}
}

// buildFromSourceCheckout builds linespec:latest via `docker build -f
// Dockerfile.linespec`, locating the linespec source root by walking up from
// the current working directory and then from the executable's directory. If
// neither is inside a linespec checkout, it fails with an actionable message
// instead of attempting a doomed CGO_ENABLED=0 cross-compile.
func buildFromSourceCheckout(execPath string) error {
	srcRoot := findLinespecSourceRoot(execPath)
	if srcRoot == "" {
		return fmt.Errorf(
			"cannot build linespec:latest outside a linespec source checkout: "+
				"cross-compiling the Linux binary requires cgo (go-tree-sitter) "+
				"enabled, which needs a Linux C toolchain unavailable when "+
				"cross-compiling from %s.\nRun `linespec build` from within the "+
				"linespec source directory instead",
			runtime.GOOS,
		)
	}

	dockerfilePath := filepath.Join(srcRoot, "Dockerfile.linespec")
	if _, err := os.Stat(dockerfilePath); err != nil {
		return fmt.Errorf("dockerfile.linespec not found at %s: %w", dockerfilePath, err)
	}

	logger.Info("Building linespec:latest from source (%s)...", srcRoot)
	if err := runDockerBuild("-f", dockerfilePath, "-t", "linespec:latest", srcRoot); err != nil {
		return fmt.Errorf("docker build failed: %w\nMake sure Docker is running and try again: linespec build", err)
	}
	logger.Info("Successfully built linespec:latest")
	return nil
}

// buildFromHostBinary packages the given executable — a native Linux build,
// not cross-compiled — into a minimal Alpine image.
func buildFromHostBinary(execPath string) error {
	tmpDir, err := os.MkdirTemp("", "linespec-build-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	data, err := os.ReadFile(execPath)
	if err != nil {
		return fmt.Errorf("failed to read linespec binary: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "linespec"), data, 0755); err != nil {
		return fmt.Errorf("failed to stage linespec binary: %w", err)
	}

	dockerfile := "FROM alpine:latest\n" +
		"RUN apk --no-cache add ca-certificates\n" +
		"WORKDIR /app\n" +
		"COPY linespec /app/linespec\n" +
		"ENTRYPOINT [\"/app/linespec\"]\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", err)
	}

	logger.Info("Building linespec:latest Docker image...")
	if err := runDockerBuild("-t", "linespec:latest", tmpDir); err != nil {
		return fmt.Errorf("docker build failed: %w\nMake sure Docker is running and try again: linespec build", err)
	}
	logger.Info("Successfully built linespec:latest")
	return nil
}

// runDockerBuild runs `docker build` with the given arguments, applying the
// same BuildKit/socket workarounds regardless of which build path (source
// checkout or host binary) is producing the image.
func runDockerBuild(args ...string) error {
	cmd := exec.Command("docker", append([]string{"build"}, args...)...)
	// Disable BuildKit so docker build doesn't try to write to ~/.docker/buildx/activity/,
	// which fails in restricted environments such as Homebrew post_install on macOS.
	dockerEnv := append(os.Environ(), "DOCKER_BUILDKIT=0")
	// On macOS, DOCKER_HOST is set by Docker Desktop's shell integration (via ~/.zshrc
	// or similar). Homebrew post_install does not source shell dotfiles, so DOCKER_HOST
	// is absent. The legacy builder (DOCKER_BUILDKIT=0) does not fall back to
	// ~/.docker/run/docker.sock automatically — it only tries /var/run/docker.sock, which
	// may not be accessible in Homebrew's restricted environment. Probe known socket paths
	// and inject DOCKER_HOST when it is not already set.
	if runtime.GOOS == "darwin" && os.Getenv("DOCKER_HOST") == "" {
		home, _ := os.UserHomeDir()
		for _, sock := range []string{
			filepath.Join(home, ".docker/run/docker.sock"),
			"/var/run/docker.sock",
		} {
			if _, statErr := os.Stat(sock); statErr == nil {
				dockerEnv = append(dockerEnv, "DOCKER_HOST=unix://"+sock)
				logger.Info("Using Docker socket: %s", sock)
				break
			}
		}
	}
	cmd.Env = dockerEnv
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// findLinespecSourceRoot locates the linespec source root (the directory
// containing its go.mod) by walking up from the current working directory,
// then from the executable's directory. Returns "" if neither is inside a
// linespec checkout.
func findLinespecSourceRoot(execPath string) string {
	const modulePrefix = "module github.com/livecodelife/linespec/v3"
	if cwd, err := os.Getwd(); err == nil {
		if srcRoot := findGoModRoot(cwd, modulePrefix); srcRoot != "" {
			return srcRoot
		}
	}
	return findGoModRoot(filepath.Dir(execPath), modulePrefix)
}

// findGoModRoot walks up from startDir looking for a go.mod file that contains
// the given module prefix. Returns the directory containing go.mod, or "".
func findGoModRoot(startDir, modulePrefix string) string {
	dir := startDir
	for {
		gomod := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(gomod); err == nil {
			if strings.Contains(string(data), modulePrefix) {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// runImportCore imports a published manifest's provenance layer. The
// <manifest-url> positional and --version pin are bound declaratively by
// newImportCmd in provenance_cobra.go; the @version URL-suffix behavior is
// preserved inside manifest.Fetch. Everything below this front layer is unchanged.
func runImportCore(manifestURL, version string) {
	cfg := loadProvenanceConfig()
	provDir := cfg.Dir
	if provDir == "" {
		provDir = "provenance"
	}

	fmt.Printf("Fetching manifest from %s...\n", manifestURL)
	fetched, err := manifest.Fetch(manifestURL, version)
	if err != nil {
		logger.Error("Failed to fetch manifest: %v", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Fetched version %s — all layers verified\n\n", fetched.Version)

	provData, ok := fetched.Layers["provenance"]
	if !ok {
		logger.Error("Manifest version %s has no provenance layer", fetched.Version)
		os.Exit(1)
	}

	// Check for conflicts before writing anything
	ids, err := manifest.ProvenanceRecordIDs(provData)
	if err != nil {
		logger.Error("Failed to read provenance layer: %v", err)
		os.Exit(1)
	}
	var conflicts []string
	for _, id := range ids {
		dest := filepath.Join(provDir, id+".yml")
		if _, err := os.Stat(dest); err == nil {
			conflicts = append(conflicts, id)
		}
	}
	if len(conflicts) > 0 {
		logger.Error("Import aborted — the following record IDs already exist locally (resolve manually):")
		for _, id := range conflicts {
			fmt.Fprintf(os.Stderr, "  · %s\n", id)
		}
		os.Exit(1)
	}

	if err := os.MkdirAll(provDir, 0755); err != nil {
		logger.Error("Failed to create provenance directory: %v", err)
		os.Exit(1)
	}
	if err := manifest.ExtractProvenance(provData, provDir); err != nil {
		logger.Error("Failed to extract provenance layer: %v", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Imported %d record(s) into %s\n\n", len(ids), provDir)

	// Run lint to surface reference issues with the local graph
	fmt.Println("Running provenance lint...")
	repoRoot, _ := os.Getwd()
	cmds, err := provenance.NewCommands(cfg, repoRoot, os.Stdout, true)
	if err != nil {
		logger.Error("Failed to initialize provenance for lint: %v", err)
		os.Exit(1)
	}
	if err := cmds.Lint(provenance.LintOptions{}); err != nil {
		fmt.Fprintln(os.Stderr, "\nLint reported issues (see above). Records were imported — resolve reference issues manually.")
	}
}

// runCloneCore bootstraps a new project directory from a published manifest. The
// <manifest-url> positional, --version pin, and --dir destination are bound
// declaratively by newCloneCmd in provenance_cobra.go; the @version URL-suffix
// behavior is preserved inside manifest.Fetch. Everything below this front layer
// is unchanged.
func runCloneCore(manifestURL, version, destDir string) {
	fmt.Printf("Fetching manifest from %s...\n", manifestURL)
	fetched, err := manifest.Fetch(manifestURL, version)
	if err != nil {
		logger.Error("Failed to fetch manifest: %v", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Fetched version %s — all layers verified\n\n", fetched.Version)

	if destDir == "" {
		if fetched.Name != "" {
			destDir = fetched.Name
		} else {
			base := filepath.Base(strings.SplitN(manifestURL, "@", 2)[0])
			base = strings.TrimSuffix(base, filepath.Ext(base))
			if base == "" || base == "." {
				base = "linespec-project"
			}
			destDir = base
		}
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		logger.Error("Failed to create directory %s: %v", destDir, err)
		os.Exit(1)
	}

	gitCmd := exec.Command("git", "init", destDir)
	gitCmd.Stdout = os.Stdout
	gitCmd.Stderr = os.Stderr
	if err := gitCmd.Run(); err != nil {
		logger.Error("git init failed: %v", err)
		os.RemoveAll(destDir)
		os.Exit(1)
	}

	// Strip @version from the URL stored in .linespec.yml
	baseManifestURL := strings.SplitN(manifestURL, "@", 2)[0]
	linespecYML := fmt.Sprintf("provenance:\n  dir: provenance\n  manifest_url: %q\n", baseManifestURL)
	if err := os.WriteFile(filepath.Join(destDir, ".linespec.yml"), []byte(linespecYML), 0644); err != nil {
		logger.Error("Failed to write .linespec.yml: %v", err)
		os.Exit(1)
	}

	// Install git hooks into the cloned project
	cloneCfg := &provenance.ProvenanceConfig{
		Dir:         filepath.Join(destDir, "provenance"),
		Enforcement: "warn",
		ManifestURL: baseManifestURL,
	}
	cloneCmds, err := provenance.NewCommands(cloneCfg, destDir, os.Stdout, true)
	if err != nil {
		logger.Error("Failed to initialize provenance for hook installation: %v", err)
		os.Exit(1)
	}
	if err := cloneCmds.InstallHooks(); err != nil {
		logger.Error("Failed to install git hooks: %v", err)
		os.Exit(1)
	}
	fmt.Println("  ✓ installed git hooks")

	if err := cloneCmds.InstallSkills(provenance.InstallSkillsOptions{}); err != nil {
		logger.Error("Failed to install skills: %v", err)
		os.Exit(1)
	}

	// Extract layers into their correct locations under destDir. Provenance records
	// land flat in provenance/, the specs layer (.linespec test files and their
	// payloads) in a dedicated linespecs/ directory mirroring the provenance/
	// convention, and code/prompt artifacts at the project root.
	for _, layerName := range []string{"provenance", "specs", "code", "prompt"} {
		data, ok := fetched.Layers[layerName]
		if !ok {
			continue
		}
		if err := manifest.ExtractLayer(layerName, data, destDir); err != nil {
			logger.Error("Failed to extract %s layer: %v", layerName, err)
			os.Exit(1)
		}
		fmt.Printf("  ✓ extracted %s layer\n", layerName)
	}

	fmt.Printf("\n✓ Project ready in ./%s\n", destDir)
}

// runInitCore runs the interactive project setup. The --force/-f, --project/-p,
// and --output/-o flags are bound declaratively by newInitCmd in
// provenance_cobra.go; the parsed values are assembled into initcmd.Options here.
// The initcmd.Run business logic is unchanged.
func runInitCore(force bool, projectPath, outputPath string) {
	opts := initcmd.Options{Force: force, ProjectPath: projectPath, OutputPath: outputPath}
	if err := initcmd.Run(opts); err != nil {
		logger.Error("%v", err)
		os.Exit(1)
	}
}

// runProxyCore runs the protocol proxy with already-parsed arguments. The
// positional <type>/<addr>/<upstream> contract and every flag (--db-name,
// --host, --schema-data, --schema-file, --sidecar-port, --grpc-descriptor-set)
// are bound declaratively by newProxyCmd in provenance_cobra.go; --debug is
// handled by the root persistent flag. extraArgs carries the trailing
// positionals the legacy parser collected (registry file, mysql transparent
// duration). Everything below this front layer is unchanged.
func runProxyCore(pType, addr, upstream, dbName, kafkaHost, schemaDataB64, schemaFile, sidecarPort, grpcDescriptorSet string, extraArgs []string) {
	// Change working directory to /app/project if it exists (inside container)
	if _, err := os.Stat("/app/project"); err == nil {
		os.Chdir("/app/project")
		logger.Debug("Changed working directory to /app/project")
	}

	filteredArgs := extraArgs

	reg := registry.NewMockRegistry()
	if len(filteredArgs) > 0 {
		regFile := filteredArgs[0]
		if err := reg.LoadFromFile(regFile); err != nil {
			logger.Error("Failed to load registry: %v", err)
			os.Exit(1)
		}
		logger.Debug("Loaded registry from %s", regFile)

		// Debug: Print registry contents
		data, _ := os.ReadFile(regFile)
		logger.Debug("Registry file size: %d bytes", len(data))
	}

	// Build a resolver from the variables stored in the registry so that
	// ${VAR} tokens in RETURNS payload files are interpolated at runtime.
	resolver := interpolate.NewResolver()
	for k, v := range reg.GetVariables() {
		resolver.Variables[k] = v
	}

	// pgProxy is set in the switch below when pType=="postgresql".
	// The reload handler closes existing connections so the service reconnects
	// with clean per-connection state between tests.
	var pgProxy *postgresql.Proxy

	// Start a sidecar HTTP server for verification and registry hot-reload
	mux := http.NewServeMux()
	srv := &http.Server{Addr: "0.0.0.0:" + sidecarPort, Handler: mux}
	mux.HandleFunc("/verify", func(w http.ResponseWriter, r *http.Request) {
		resp := struct {
			Hits         map[string]int `json:"hits"`
			Passthroughs []string       `json:"passthroughs"`
			VerifyErrors []string       `json:"verify_errors,omitempty"`
		}{
			Hits:         reg.GetHits(),
			Passthroughs: reg.GetPassthroughs(),
			VerifyErrors: reg.GetVerifyErrors(),
		}
		json.NewEncoder(w).Encode(resp)
	})
	// /reload-registry accepts a POST with the new registry JSON in the body.
	// It replaces the proxy's in-memory registry and clears all state counters,
	// allowing the same proxy container to be reused across multiple test runs.
	mux.HandleFunc("/reload-registry", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := reg.LoadFromBytes(body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		reg.ClearState()
		if pgProxy != nil {
			pgProxy.ResetConnections()
		}
		// Rebuild the resolver from the new registry's variables so that ${VAR}
		// tokens in RETURNS payload files resolve to the current test's values.
		// Without this, persistent proxy containers would keep resolving variables
		// with values generated during the very first test they served.
		newVars := reg.GetVariables()
		resolver.Variables = make(map[string]string, len(newVars))
		resolver.Generated = make(map[string]bool)
		for k, v := range newVars {
			resolver.Variables[k] = v
		}
		logger.Debug("Registry hot-reloaded (%d bytes)", len(body))
		w.WriteHeader(http.StatusOK)
	})

	go func() {
		logger.Debug("Verification sidecar listening on 0.0.0.0:%s", sidecarPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Verification sidecar error: %v", err)
		}
	}()

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	var proxyErr error
	switch pType {
	case "mysql":
		if dbName == "" {
			logger.Error("MySQL proxy requires --db-name argument specifying the database name")
			os.Exit(1)
		}
		p := mysql.NewProxy(addr, upstream, reg)
		p.SetDatabaseName(dbName)
		// Load schema from file if provided via --schema-file flag
		if schemaFile != "" {
			if err := p.LoadSchema(schemaFile); err != nil {
				logger.Error("Failed to load schema from --schema-file: %v", err)
				// Don't exit - schema is optional
			}
		} else if schemaDataB64 != "" {
			// Fallback: load from inline base64 data via --schema-data flag
			schemaBytes, err := base64.StdEncoding.DecodeString(schemaDataB64)
			if err != nil {
				logger.Debug("Failed to decode --schema-data: %v", err)
			} else if err := p.LoadSchemaFromBytes(schemaBytes); err != nil {
				logger.Error("Failed to load schema from --schema-data: %v", err)
				// Don't exit - schema is optional
			}
		}
		// Check for transparent mode duration (filteredArgs[1])
		if len(filteredArgs) > 1 {
			transparentDuration := filteredArgs[1]
			if duration, err := time.ParseDuration(transparentDuration); err == nil {
				p.EnableTransparentMode(duration)
			} else {
				logger.Error("Invalid transparent duration: %v", err)
			}
		}
		p.SetResolver(resolver)
		proxyErr = p.Start(ctx)
	case "postgresql":
		pgProxy = postgresql.NewProxy(addr, upstream, reg)
		if dbName != "" {
			pgProxy.SetDatabaseName(dbName)
		}
		pgProxy.SetResolver(resolver)
		// Load schema from file if provided via --schema-file flag, mirroring the
		// MySQL case above. Populates schemaCache so RowDescription messages carry
		// real column OIDs instead of falling back to name-based heuristics.
		if schemaFile != "" {
			if err := pgProxy.LoadSchema(schemaFile); err != nil {
				logger.Error("Failed to load schema from --schema-file: %v", err)
				// Don't exit - schema is optional
			}
		} else if schemaDataB64 != "" {
			schemaBytes, err := base64.StdEncoding.DecodeString(schemaDataB64)
			if err != nil {
				logger.Debug("Failed to decode --schema-data: %v", err)
			} else if err := pgProxy.LoadSchemaFromBytes(schemaBytes); err != nil {
				logger.Error("Failed to load schema from --schema-data: %v", err)
				// Don't exit - schema is optional
			}
		}
		proxyErr = pgProxy.Start(ctx)
	case "http":
		p := httpproxy.NewInterceptor(addr, reg)
		p.SetResolver(resolver)
		proxyErr = p.Start(ctx)
	case "kafka":
		p := kafka.NewInterceptor(addr, reg)
		if kafkaHost != "" {
			p.SetHost(kafkaHost)
		}
		p.SetResolver(resolver)
		proxyErr = p.Start(ctx)
	case "grpc":
		p := grpcproxy.NewInterceptor(addr, upstream, reg)
		p.SetResolver(resolver)
		if grpcDescriptorSet != "" {
			desc, err := grpcproxy.LoadDescriptorSet(grpcDescriptorSet)
			if err != nil {
				logger.Error("Warning: failed to load gRPC descriptor set (protobuf decoding unavailable): %v", err)
			} else {
				p.SetDescriptor(desc)
				logger.Debug("Loaded gRPC descriptor set from %s", grpcDescriptorSet)
			}
		}
		proxyErr = p.Start(ctx)
	case "redis":
		p := redisproxy.NewInterceptor(addr, reg)
		p.SetResolver(resolver)
		proxyErr = p.Start(ctx)
	case "mongodb":
		p := mongodbproxy.NewInterceptor(addr, upstream, reg)
		p.SetResolver(resolver)
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

func loadProvenanceConfig() *provenance.ProvenanceConfig {
	return loadProvenanceConfigFromFile(".linespec.yml")
}

func loadProvenanceConfigFromFile(filePath string) *provenance.ProvenanceConfig {
	cfg := &provenance.ProvenanceConfig{
		Dir:               "provenance",
		Enforcement:       "warn",
		CommitTagRequired: false,
		AutoAffectedScope: true,
		// ConfigFileDir bounds fswrite reconcile to the closest parent directory
		// of this config file (prov-2026-bde50f4d), regardless of whether the
		// file parses or has a provenance: section below.
		ConfigFileDir: filepath.Dir(filePath),
	}

	// Try to load from specified file if it exists
	if data, err := os.ReadFile(filePath); err == nil {
		var fullConfig config.LineSpecConfig
		unmarshalErr := yaml.Unmarshal(data, &fullConfig)
		if unmarshalErr != nil {
			// yaml.Unmarshal still populates fullConfig.Provenance with whatever
			// decoded cleanly before the error (e.g. an unknown key rejected by
			// EmbeddingConfig.UnmarshalYAML) — surface the error instead of
			// silently discarding the whole provenance: section, which left
			// unknown-key typos indistinguishable from "Embedding API not
			// configured" (prov-2026-57aff9e1).
			logger.Error("Failed to parse %s: %v", filePath, unmarshalErr)
		}
		if fullConfig.Provenance != nil {
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
			cfg.RunAssociatedSpecsOnComplete = fullConfig.Provenance.RunAssociatedSpecsOnComplete
			cfg.OverlapSpecsOnComplete = fullConfig.Provenance.OverlapSpecsOnComplete
			cfg.CommitOnStatusChange = fullConfig.Provenance.CommitOnStatusChange
			cfg.SharedRepos = fullConfig.Provenance.SharedRepos
			cfg.CacheTTLMinutes = fullConfig.Provenance.CacheTTLMinutes
			cfg.ManifestURL = fullConfig.Provenance.ManifestURL
			cfg.ExcludePaths = fullConfig.Provenance.ExcludePaths
			cfg.WriteRestriction = fullConfig.Provenance.WriteRestriction

			// Only trust Embedding when the unmarshal succeeded: on an unknown-key
			// error, EmbeddingConfig.UnmarshalYAML leaves it a zero-value struct
			// rather than nil, which would otherwise look "configured" with an
			// empty provider.
			if unmarshalErr == nil && fullConfig.Provenance.Embedding != nil {
				cfg.Embedding = fullConfig.Provenance.Embedding
			}
		}
	}

	return cfg
}

// findAllLinespecConfigs returns paths to all .linespec.yml files found under root,
// excluding .git directories. filepath.Walk visits in lexicographic order.
func findAllLinespecConfigs(root string) []string {
	var configs []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Name() == ".linespec.yml" {
			configs = append(configs, path)
		}
		return nil
	})
	return configs
}

// reloadConfigIfNeeded reloads the config and commands if a custom config file is specified.
// It rebuilds the embedder from the reloaded config's provenance.embedding block, mirroring
// provSetup(), so `-c/--config` actually takes effect for embedding-backed subcommands
// (index, search) instead of always reporting "Embedding API not configured"
// (prov-2026-57aff9e1).
func reloadConfigIfNeeded(cfg **provenance.ProvenanceConfig, cmds **provenance.Commands, configFile string, repoRoot string) error {
	if configFile != "" {
		*cfg = loadProvenanceConfigFromFile(configFile)

		var embedder *embeddings.Client
		if (*cfg).Embedding != nil {
			embedder, _ = embeddings.NewClient(*(*cfg).Embedding)
		}

		newCmds, err := provenance.NewCommandsWithEmbedder(*cfg, repoRoot, os.Stdout, true, embedder)
		if err != nil {
			return fmt.Errorf("failed to initialize provenance with custom config: %w", err)
		}
		*cmds = newCmds
	}
	return nil
}

// discoverOptions holds parsed flags for the discover subcommand.
type discoverOptions struct {
	Dir        string
	Lang       string
	Framework  string
	Enrich     bool
	LLMBaseURL string
	Model      string
	MaxTokens  int
	DryRun     bool
	Format     string
	ConfigFile string
}

// runDiscover orchestrates all discover phases (1–4) and prints a summary.
func runDiscover(opts discoverOptions, cfg *provenance.ProvenanceConfig, repoRoot string) {
	if opts.ConfigFile != "" {
		cfg = loadProvenanceConfigFromFile(opts.ConfigFile)
	}

	scanDir := opts.Dir
	if scanDir == "" {
		scanDir = "."
	}

	// Load framework descriptions (built-ins + user overrides from .linespec/frameworks/).
	userFrameworksDir := filepath.Join(scanDir, ".linespec", "frameworks")
	descs, err := framework.Load(userFrameworksDir)
	if err != nil {
		logger.Error("Failed to load framework descriptions: %v", err)
		os.Exit(1)
	}

	// Resolve the framework description to use.
	var desc *framework.Description
	if opts.Framework != "" {
		d, ok := descs[opts.Framework]
		if !ok {
			names := make([]string, 0, len(descs))
			for k := range descs {
				names = append(names, k)
			}
			logger.Error("Unknown framework %q. Available: %s", opts.Framework, strings.Join(names, ", "))
			os.Exit(1)
		}
		desc = d
	} else {
		result := framework.Detect(scanDir, descs)
		if result == nil {
			logger.Info("Warning: could not detect a supported framework in %q; proceeding in framework-agnostic mode (language detection + symbol extraction only, no route or .linespec discovery).", scanDir)
			runDiscoverAgnostic(opts, cfg, scanDir)
			return
		}
		if opts.Lang != "" && result.Language != opts.Lang {
			logger.Error("Detected framework language %q does not match --lang %q", result.Language, opts.Lang)
			os.Exit(1)
		}
		desc = descs[result.Framework]
	}

	ctx := context.Background()

	// Phase 1: route discovery.
	assembler, err := discoverroutes.New(desc)
	if err != nil {
		logger.Error("Failed to create route assembler: %v", err)
		os.Exit(1)
	}
	groups, err := assembler.Assemble(ctx, scanDir)
	if err != nil {
		logger.Error("Route discovery failed: %v", err)
		os.Exit(1)
	}

	// Flatten all routes for Phase 2.
	var allRoutes []discoverroutes.Route
	for _, g := range groups {
		allRoutes = append(allRoutes, g.Routes...)
	}

	// Phase 2: protocol boundary tracing.
	tracer, err := boundaries.New(desc)
	if err != nil {
		logger.Error("Failed to create boundary tracer: %v", err)
		os.Exit(1)
	}
	hitMap, err := tracer.Trace(ctx, scanDir, allRoutes)
	if err != nil {
		logger.Error("Boundary tracing failed: %v", err)
		os.Exit(1)
	}

	// Phase 3: spec stub generation.
	// When --dir is set, resolve output paths relative to the scanned directory.
	specsDir := filepath.Join(scanDir, "linespecs")
	provenanceDir := cfg.Dir
	if opts.Dir != "" {
		provenanceDir = filepath.Join(scanDir, filepath.Base(cfg.Dir))
	}
	stubInputs := buildDiscoverStubInputs(groups, hitMap)

	var stubResults []stubs.Result
	if opts.DryRun {
		stubResults = stubs.Plan(specsDir, stubInputs)
	} else {
		stubResults, err = stubs.Write(specsDir, stubInputs)
		if err != nil {
			logger.Error("Stub generation failed: %v", err)
			os.Exit(1)
		}
	}

	// Phase 4: blueprint record generation.
	existingLoader := provenance.NewLoader(provenanceDir, nil)
	_ = existingLoader.LoadAll()
	existingIDs := existingLoader.GetAllIDs()

	author := "linespec-discover"
	if helperCmds, cerr := provenance.NewCommands(cfg, scanDir, os.Stdout, false); cerr == nil {
		if email, gerr := helperCmds.Git.GetGitEmail(); gerr == nil && email != "" {
			author = email
		}
	}

	recordInput := discoverrecords.Input{
		Groups:        groups,
		StubResults:   stubResults,
		Boundaries:    hitMap,
		ProvenanceDir: provenanceDir,
		SpecsDir:      specsDir,
		Author:        author,
	}

	if opts.DryRun {
		recordResults, sum := discoverrecords.Plan(recordInput, existingIDs)

		suppResults, suppSum, serr := runSupplementalAgnostic(ctx, scanDir, groups, recordResults, provenanceDir, author, idsAfter(existingIDs, recordResults), true)
		if serr != nil {
			logger.Error("Supplemental framework-agnostic scan failed: %v", serr)
			os.Exit(1)
		}
		recordResults = append(recordResults, agnosticResultsToDiscoverResults(suppResults)...)
		sum.RecordsCreated += suppSum.RecordsCreated
		sum.Unclassified = append(sum.Unclassified, suppSum.Unclassified...)

		printDiscoverDryRun(desc, scanDir, stubResults, recordResults, sum, opts.Format)
		return
	}

	recordResults, sum, err := discoverrecords.Write(recordInput, existingIDs)
	if err != nil {
		logger.Error("Record generation failed: %v", err)
		os.Exit(1)
	}

	// Supplemental pass: the route assembler above only sees files with HTTP route
	// registrations. Every other directory (services, models, repositories, ...)
	// gets no blueprint from it, which is the bug this pass fixes — run the same
	// framework-agnostic scan the no-framework-detected path uses, but skip any
	// directory already covered by a route group so blueprints are never doubled up.
	suppResults, suppSum, err := runSupplementalAgnostic(ctx, scanDir, groups, recordResults, provenanceDir, author, idsAfter(existingIDs, recordResults), false)
	if err != nil {
		logger.Error("Supplemental framework-agnostic scan failed: %v", err)
		os.Exit(1)
	}
	recordResults = append(recordResults, agnosticResultsToDiscoverResults(suppResults)...)
	sum.RecordsCreated += suppSum.RecordsCreated
	sum.Unclassified = append(sum.Unclassified, suppSum.Unclassified...)

	printDiscoverSummary(desc, scanDir, recordResults, sum)

	if opts.Enrich {
		enrichRoot := scanDir
		if out, err := exec.Command("git", "-C", scanDir, "rev-parse", "--show-toplevel").Output(); err == nil {
			enrichRoot = strings.TrimSpace(string(out))
		}
		runDiscoverEnrich(recordResults, enrichRoot, opts.LLMBaseURL, opts.Model, opts.MaxTokens)
	}
}

// idsAfter returns existingIDs plus the record IDs already allocated to results,
// so a subsequent record-generation pass never collides with them.
func idsAfter(existingIDs []string, results []discoverrecords.Result) []string {
	ids := append([]string(nil), existingIDs...)
	for _, r := range results {
		ids = append(ids, r.RecordID)
	}
	return ids
}

// runSupplementalAgnostic runs discover's framework-agnostic scan (extension-based
// language detection + directory grouping) restricted to directories that contain
// no framework-discovered route, so files invisible to the route assembler —
// services, repositories, models, utilities — still get blueprint coverage.
// routeResults must be the discoverrecords results for groups, in the same order,
// so that non-route files sharing a directory with a route file (e.g. helper.go
// next to router.go) can be merged into that route group's already-written
// blueprint instead of being silently dropped.
func runSupplementalAgnostic(ctx context.Context, scanDir string, groups []discoverroutes.Group, routeResults []discoverrecords.Result, provenanceDir, author string, existingIDs []string, dryRun bool) ([]agnosticResult, agnosticSummary, error) {
	files, unclassified, err := scanAgnosticFiles(ctx, scanDir)
	if err != nil {
		return nil, agnosticSummary{}, err
	}

	var routeFiles []string
	for _, g := range groups {
		for _, r := range g.Routes {
			if r.Source.File != "" {
				routeFiles = append(routeFiles, r.Source.File)
			}
		}
	}
	covered := graph.CoveredDirs(routeFiles)
	uncovered := graph.FilterUncovered(files, covered)

	if !dryRun {
		extras := graph.CoveredDirExtras(files, covered, routeFiles)
		if err := mergeExtrasIntoRouteGroups(extras, groups, routeResults, provenanceDir); err != nil {
			return nil, agnosticSummary{}, err
		}
	}

	var grouper graph.Grouper = graph.DirectoryGrouper{}
	dirGroups, err := grouper.Group(uncovered)
	if err != nil {
		return nil, agnosticSummary{}, err
	}

	sum := computeAgnosticSummary(uncovered, dirGroups, unclassified)

	if dryRun {
		results := planAgnosticRecords(dirGroups, provenanceDir, existingIDs)
		sum.RecordsCreated = len(results)
		return results, sum, nil
	}

	results, err := writeAgnosticRecords(dirGroups, provenanceDir, author, existingIDs)
	if err != nil {
		return nil, agnosticSummary{}, err
	}
	sum.RecordsCreated = len(results)
	return results, sum, nil
}

// mergeExtrasIntoRouteGroups merges each extra file's path into the
// affected_scope of the already-written blueprint record for the route group
// that owns its directory. A directory's owning group is identified by the
// directory of its route files; the corresponding entry in routeResults (same
// index as in groups) points at the record file on disk to update.
func mergeExtrasIntoRouteGroups(extras []graph.File, groups []discoverroutes.Group, routeResults []discoverrecords.Result, provenanceDir string) error {
	if len(extras) == 0 {
		return nil
	}

	dirToFilePath := make(map[string]string)
	for i, g := range groups {
		if i >= len(routeResults) {
			break
		}
		for _, r := range g.Routes {
			if r.Source.File == "" {
				continue
			}
			dir := filepath.Dir(r.Source.File)
			if _, ok := dirToFilePath[dir]; !ok {
				dirToFilePath[dir] = routeResults[i].FilePath
			}
		}
	}

	extraPaths := make(map[string][]string)
	for _, f := range extras {
		recordPath, ok := dirToFilePath[filepath.Dir(f.Path)]
		if !ok {
			continue
		}
		extraPaths[recordPath] = append(extraPaths[recordPath], f.Path)
	}

	loader := provenance.NewLoader(provenanceDir, nil)
	for recordPath, paths := range extraPaths {
		record, err := loader.LoadFile(recordPath)
		if err != nil {
			return fmt.Errorf("load record %s: %w", recordPath, err)
		}
		seen := make(map[string]bool, len(record.AffectedScope))
		for _, p := range record.AffectedScope {
			seen[p] = true
		}
		for _, p := range paths {
			if !seen[p] {
				record.AffectedScope = append(record.AffectedScope, p)
				seen[p] = true
			}
		}
		sort.Strings(record.AffectedScope)

		// SaveRecord preserves formatting for an already-existing file by
		// patching status/sealed_at_sha/superseded_by in place; it does not
		// rewrite affected_scope for a file that already exists. Since this
		// record was written moments ago by this same discover run (nothing
		// user-authored to preserve yet), remove it first so SaveRecord takes
		// its fresh-marshal path and the merged scope actually lands on disk.
		if err := os.Remove(recordPath); err != nil {
			return fmt.Errorf("remove record %s for scope merge: %w", recordPath, err)
		}
		if err := loader.SaveRecord(record); err != nil {
			return fmt.Errorf("save record %s: %w", recordPath, err)
		}
	}
	return nil
}

// agnosticResultsToDiscoverResults adapts framework-agnostic supplemental results
// to discoverrecords.Result so they print and JSON-marshal alongside route-based
// results in the same summary. RouteCount is 0 — these blueprints cover files with
// no framework-discovered route.
func agnosticResultsToDiscoverResults(results []agnosticResult) []discoverrecords.Result {
	out := make([]discoverrecords.Result, 0, len(results))
	for _, r := range results {
		out = append(out, discoverrecords.Result{
			GroupName:  r.GroupName,
			RecordID:   r.RecordID,
			FilePath:   r.FilePath,
			Title:      r.Title,
			RouteCount: 0,
		})
	}
	return out
}

// agnosticSkipDirs lists directory names skipped during a framework-agnostic scan —
// dependency and VCS directories whose contents are not the user's own code.
var agnosticSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
}

// agnosticResult is the outcome of generating one blueprint record for a directory group.
type agnosticResult struct {
	GroupName string
	RecordID  string
	FilePath  string
	Title     string
	FileCount int
}

// agnosticSummary is the framework-agnostic discover report.
type agnosticSummary struct {
	FilesScanned   int
	SymbolCount    int
	GroupCount     int
	RecordsCreated int
	Unclassified   []string
}

// runDiscoverAgnostic runs discover's framework-agnostic path: extension-based
// language detection, tree-sitter symbol/import extraction, and directory-based
// grouping. It is used when --framework is unspecified and auto-detection finds
// no supported framework. Unlike the framework-driven pipeline, it never
// generates .linespec stubs — no routes or protocol boundaries are known — it
// only writes draft blueprint records, one per group.
func runDiscoverAgnostic(opts discoverOptions, cfg *provenance.ProvenanceConfig, scanDir string) {
	ctx := context.Background()

	files, unclassified, err := scanAgnosticFiles(ctx, scanDir)
	if err != nil {
		logger.Error("Framework-agnostic scan failed: %v", err)
		os.Exit(1)
	}

	var grouper graph.Grouper = graph.DirectoryGrouper{}
	groups, err := grouper.Group(files)
	if err != nil {
		logger.Error("Grouping failed: %v", err)
		os.Exit(1)
	}

	provenanceDir := cfg.Dir
	if opts.Dir != "" {
		provenanceDir = filepath.Join(scanDir, filepath.Base(cfg.Dir))
	}

	existingLoader := provenance.NewLoader(provenanceDir, nil)
	_ = existingLoader.LoadAll()
	existingIDs := existingLoader.GetAllIDs()

	sum := computeAgnosticSummary(files, groups, unclassified)

	if opts.DryRun {
		results := planAgnosticRecords(groups, provenanceDir, existingIDs)
		sum.RecordsCreated = len(results)
		printDiscoverAgnosticDryRun(scanDir, results, sum, opts.Format)
		return
	}

	author := "linespec-discover"
	if helperCmds, cerr := provenance.NewCommands(cfg, scanDir, os.Stdout, false); cerr == nil {
		if email, gerr := helperCmds.Git.GetGitEmail(); gerr == nil && email != "" {
			author = email
		}
	}

	results, err := writeAgnosticRecords(groups, provenanceDir, author, existingIDs)
	if err != nil {
		logger.Error("Record generation failed: %v", err)
		os.Exit(1)
	}
	sum.RecordsCreated = len(results)

	printDiscoverAgnosticSummary(scanDir, results, sum)

	if opts.Enrich {
		enrichRoot := scanDir
		if out, err := exec.Command("git", "-C", scanDir, "rev-parse", "--show-toplevel").Output(); err == nil {
			enrichRoot = strings.TrimSpace(string(out))
		}
		enrichResults := make([]discoverrecords.Result, len(results))
		for i, r := range results {
			enrichResults[i] = discoverrecords.Result{FilePath: r.FilePath}
		}
		runDiscoverEnrich(enrichResults, enrichRoot, opts.LLMBaseURL, opts.Model, opts.MaxTokens)
	}
}

// scanAgnosticFiles walks dir, detecting language by extension and extracting
// symbols/imports for every recognized, tree-sitter-supported language. Files
// with a recognized extension that tree-sitter cannot yet parse are returned
// as unclassified; files with no recognized extension are skipped entirely
// (they are not source code discover can reason about).
func scanAgnosticFiles(ctx context.Context, dir string) ([]graph.File, []string, error) {
	var files []graph.File
	var unclassified []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if path != dir && agnosticSkipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		l, ok := lang.Detect(path)
		if !ok {
			return nil
		}
		if !symbols.Supported(l) {
			unclassified = append(unclassified, path)
			return nil
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		extracted, err := symbols.Extract(ctx, l, src)
		if err != nil {
			// A single unparsable file should not abort the whole scan.
			logger.Info("Warning: skipping %s: %v", path, err)
			unclassified = append(unclassified, path)
			return nil
		}

		files = append(files, graph.File{
			Path:    path,
			Lang:    l,
			Symbols: extracted.Symbols,
			Imports: extracted.Imports,
		})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Strings(unclassified)
	return files, unclassified, nil
}

func computeAgnosticSummary(files []graph.File, groups []graph.Group, unclassified []string) agnosticSummary {
	symbolCount := 0
	for _, f := range files {
		symbolCount += len(f.Symbols)
	}
	return agnosticSummary{
		FilesScanned: len(files),
		SymbolCount:  symbolCount,
		GroupCount:   len(groups),
		Unclassified: unclassified,
	}
}

// planAgnosticRecords computes what writeAgnosticRecords would produce without touching the filesystem.
func planAgnosticRecords(groups []graph.Group, provenanceDir string, existingIDs []string) []agnosticResult {
	year := provenance.CurrentYear()
	ids := append([]string(nil), existingIDs...)

	results := make([]agnosticResult, 0, len(groups))
	for _, g := range groups {
		id, err := provenance.NextID(year, ids)
		if err != nil {
			continue
		}
		ids = append(ids, id)
		results = append(results, agnosticResult{
			GroupName: g.Name,
			RecordID:  id,
			FilePath:  filepath.Join(provenanceDir, id+".yml"),
			Title:     agnosticGroupTitle(g.Name),
			FileCount: len(g.Files),
		})
	}
	return results
}

// writeAgnosticRecords generates and saves draft blueprint records to provenanceDir,
// one per group. No .linespec stubs are generated — framework-agnostic mode has no
// route or boundary information to seed them with.
func writeAgnosticRecords(groups []graph.Group, provenanceDir, author string, existingIDs []string) ([]agnosticResult, error) {
	loader := provenance.NewLoader(provenanceDir, nil)

	year := provenance.CurrentYear()
	ids := append([]string(nil), existingIDs...)

	results := make([]agnosticResult, 0, len(groups))
	for _, g := range groups {
		id, err := provenance.NextID(year, ids)
		if err != nil {
			return nil, fmt.Errorf("generate record ID: %w", err)
		}
		ids = append(ids, id)

		title := agnosticGroupTitle(g.Name)
		filePath := filepath.Join(provenanceDir, id+".yml")

		scope := make([]string, 0, len(g.Files))
		for _, f := range g.Files {
			scope = append(scope, f.Path)
		}

		record := &provenance.Record{
			ID:               id,
			Title:            title,
			Status:           provenance.StatusDraft,
			Type:             provenance.RecordTypeBlueprint,
			CreatedAt:        provenance.CurrentDate(),
			Author:           author,
			Intent:           "",
			Constraints:      []string{},
			AffectedScope:    scope,
			ForbiddenScope:   []string{},
			Supersedes:       "",
			SupersededBy:     "",
			Related:          []string{},
			AssociatedSpecs:  []provenance.AssociatedSpec{},
			AssociatedTraces: []string{},
			Monitors:         []string{},
			Tags:             []string{"discover", "framework-agnostic"},
			FilePath:         filePath,
		}

		if err := loader.SaveRecord(record); err != nil {
			return nil, fmt.Errorf("save record %s: %w", id, err)
		}

		results = append(results, agnosticResult{
			GroupName: g.Name,
			RecordID:  id,
			FilePath:  filePath,
			Title:     title,
			FileCount: len(g.Files),
		})
	}
	return results, nil
}

// agnosticGroupTitle converts a directory group name into a human-readable blueprint title.
func agnosticGroupTitle(name string) string {
	if name == "." {
		return "root module"
	}
	return name + " module"
}

// printDiscoverAgnosticSummary prints the post-run summary for a successful framework-agnostic run.
func printDiscoverAgnosticSummary(scanDir string, results []agnosticResult, sum agnosticSummary) {
	fmt.Fprintf(os.Stdout, "\nlinespec provenance discover — complete (framework-agnostic)\n\n")
	fmt.Fprintf(os.Stdout, "  Scanned:                   %s\n", scanDir)
	fmt.Fprintf(os.Stdout, "  Source files scanned:      %d\n", sum.FilesScanned)
	fmt.Fprintf(os.Stdout, "  Symbols extracted:         %d\n", sum.SymbolCount)
	fmt.Fprintf(os.Stdout, "  Groups discovered:         %d\n", sum.GroupCount)
	fmt.Fprintf(os.Stdout, "  Blueprint records created: %d\n\n", sum.RecordsCreated)
	if len(results) > 0 {
		fmt.Fprintf(os.Stdout, "  Records:\n")
		for _, r := range results {
			fmt.Fprintf(os.Stdout, "    %-20s  %s  (%d file(s))\n", r.RecordID, r.Title, r.FileCount)
		}
		fmt.Fprintln(os.Stdout)
	}
	if len(sum.Unclassified) > 0 {
		fmt.Fprintf(os.Stdout, "  Unclassified files (%d):\n", len(sum.Unclassified))
		for _, f := range sum.Unclassified {
			fmt.Fprintf(os.Stdout, "    %s\n", f)
		}
		fmt.Fprintln(os.Stdout)
	}
}

// printDiscoverAgnosticDryRun prints the dry-run output for a framework-agnostic run.
func printDiscoverAgnosticDryRun(scanDir string, results []agnosticResult, sum agnosticSummary, format string) {
	if format == "json" {
		data, err := json.MarshalIndent(struct {
			Results []agnosticResult `json:"results"`
			Summary agnosticSummary  `json:"summary"`
		}{results, sum}, "", "  ")
		if err != nil {
			logger.Error("Failed to format JSON: %v", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, string(data))
		return
	}
	fmt.Fprintf(os.Stdout, "\nlinespec provenance discover — DRY RUN, framework-agnostic (no files written)\n\n")
	fmt.Fprintf(os.Stdout, "  Scanned: %s\n\n", scanDir)
	fmt.Fprintf(os.Stdout, "Records:\n")
	if len(results) == 0 {
		fmt.Fprintln(os.Stdout, "  (none)")
		return
	}
	for _, r := range results {
		fmt.Fprintf(os.Stdout, "  %-20s  %s  (%d file(s))\n", r.RecordID, r.Title, r.FileCount)
	}
}

// runDiscoverEnrich runs Phase 5: git history analysis + LLM intent synthesis.
// Errors are non-fatal: each failure is logged as a warning and the pipeline continues.
func runDiscoverEnrich(results []discoverrecords.Result, repoRoot, llmBaseURL, model string, maxTokens int) {
	recordFiles := make([]string, 0, len(results))
	for _, r := range results {
		recordFiles = append(recordFiles, r.FilePath)
	}

	fmt.Fprintln(os.Stdout, "\n  Phase 5: git history enrichment")
	enrichResults, err := enrich.Enrich(enrich.Input{
		RepoDir:     repoRoot,
		RecordFiles: recordFiles,
		LLMBaseURL:  llmBaseURL,
		Model:       model,
		MaxTokens:   maxTokens,
		Progress:    func(msg string) { fmt.Fprintln(os.Stdout, msg) },
	})
	if err != nil {
		logger.Info("--enrich: unexpected error: %v", err)
		return
	}

	enriched := 0
	for _, r := range enrichResults {
		if r.Skipped || r.Err != nil {
			continue
		}
		enriched++
	}

	if enriched > 0 {
		fmt.Fprintf(os.Stdout, "\n  Intent fields enriched: %d/%d\n\n", enriched, len(enrichResults))
	} else {
		fmt.Fprintln(os.Stdout, "\n  No intent fields populated.")
	}
}

// buildDiscoverStubInputs converts Phase 1 and Phase 2 output into Phase 3 inputs.
func buildDiscoverStubInputs(groups []discoverroutes.Group, hitMap map[string][]boundaries.Hit) []stubs.Input {
	var inputs []stubs.Input
	for _, g := range groups {
		for _, r := range g.Routes {
			hits := hitMap[r.HandlerRef]
			boundaryHits := make([]stubs.BoundaryHit, len(hits))
			for i, h := range hits {
				boundaryHits[i] = stubs.BoundaryHit{
					Protocol:  h.Protocol,
					Direction: h.Direction,
					Target:    h.Target,
					Dynamic:   h.Dynamic,
				}
			}
			inputs = append(inputs, stubs.Input{
				Route:      r,
				Boundaries: boundaryHits,
			})
		}
	}
	return inputs
}

// printDiscoverSummary prints the post-run summary for a successful discover run.
func printDiscoverSummary(desc *framework.Description, scanDir string, results []discoverrecords.Result, sum discoverrecords.Summary) {
	fmt.Fprintf(os.Stdout, "\nlinespec provenance discover — complete\n\n")
	fmt.Fprintf(os.Stdout, "  Scanned:                   %s (%s/%s)\n", scanDir, desc.Name, desc.Language)
	fmt.Fprintf(os.Stdout, "  Routes discovered:         %d\n", sum.RouteCount)
	fmt.Fprintf(os.Stdout, "  Protocol boundaries:       %d\n", sum.BoundaryCount)
	fmt.Fprintf(os.Stdout, "  Blueprint records created: %d\n\n", sum.RecordsCreated)
	if len(results) > 0 {
		fmt.Fprintf(os.Stdout, "  Records:\n")
		for _, r := range results {
			fmt.Fprintf(os.Stdout, "    %-20s  %s  (%d route(s))\n", r.RecordID, r.Title, r.RouteCount)
		}
		fmt.Fprintln(os.Stdout)
	}
	if len(sum.Unclassified) > 0 {
		fmt.Fprintf(os.Stdout, "  Unclassified files (%d):\n", len(sum.Unclassified))
		for _, f := range sum.Unclassified {
			fmt.Fprintf(os.Stdout, "    %s\n", f)
		}
		fmt.Fprintln(os.Stdout)
	}
}

// printDiscoverDryRun prints the dry-run output showing what would be generated.
func printDiscoverDryRun(desc *framework.Description, scanDir string, stubResults []stubs.Result, recordResults []discoverrecords.Result, sum discoverrecords.Summary, format string) {
	if format == "json" {
		data, err := discoverrecords.FormatJSON(recordResults, sum)
		if err != nil {
			logger.Error("Failed to format JSON: %v", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, string(data))
		return
	}
	fmt.Fprintf(os.Stdout, "\nlinespec provenance discover — DRY RUN (no files written)\n\n")
	fmt.Fprintf(os.Stdout, "  Scanned: %s (%s/%s)\n\n", scanDir, desc.Name, desc.Language)
	fmt.Fprintf(os.Stdout, "Stubs:\n")
	fmt.Fprint(os.Stdout, stubs.FormatTable(stubResults))
	fmt.Fprintf(os.Stdout, "\nRecords:\n")
	fmt.Fprint(os.Stdout, discoverrecords.FormatTable(recordResults, sum))
}

// resolvePublishName determines the project name to write into the manifest.
// Priority: existing manifest name → service.name from .linespec.yml → directory name.
// When stdin is a terminal the user is prompted; otherwise the default is used silently.
func resolvePublishName(manifestPath string) string {
	if manifestPath == "" {
		manifestPath = "linespec.manifest.json"
	}

	// Use existing manifest name as the default if the manifest already exists.
	defaultName := ""
	if data, err := os.ReadFile(manifestPath); err == nil {
		var existing struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &existing) == nil && existing.Name != "" {
			defaultName = existing.Name
		}
	}

	// Fall back to service.name from .linespec.yml, then directory name.
	if defaultName == "" {
		cwd, _ := os.Getwd()
		if cfg, err := config.LoadConfig(cwd); err == nil && cfg.Service.Name != "" {
			defaultName = cfg.Service.Name
		} else {
			defaultName = filepath.Base(cwd)
		}
	}

	// Prompt user; falls back to default when stdin is not a terminal (CI-safe).
	fmt.Fprintf(os.Stdout, "Project name [%s]: ", defaultName)
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			return line
		}
	}
	return defaultName
}
