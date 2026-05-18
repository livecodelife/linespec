//go:build !beta

package main

import (
	"bufio"
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
	"strings"
	"syscall"
	"time"

	"github.com/livecodelife/linespec/pkg/config"
	"github.com/livecodelife/linespec/pkg/manifest"
	"github.com/livecodelife/linespec/pkg/dsl"
	"github.com/livecodelife/linespec/pkg/embeddings"
	"github.com/livecodelife/linespec/pkg/initcmd"
	"github.com/livecodelife/linespec/pkg/interpolate"
	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/provenance"
	grpcproxy "github.com/livecodelife/linespec/pkg/proxy/grpc"
	httpproxy "github.com/livecodelife/linespec/pkg/proxy/http"
	"github.com/livecodelife/linespec/pkg/proxy/kafka"
	mongodbproxy "github.com/livecodelife/linespec/pkg/proxy/mongodb"
	"github.com/livecodelife/linespec/pkg/proxy/mysql"
	"github.com/livecodelife/linespec/pkg/proxy/postgresql"
	redisproxy "github.com/livecodelife/linespec/pkg/proxy/redis"
	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/runner"
	"gopkg.in/yaml.v3"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "init":
		runInit()
	case "clone":
		runClone()
	case "import":
		runImport()
	case "proxy":
		runProxy()
	case "test":
		runTest()
	case "build":
		runBuild()
	case "provenance", "-p":
		runProvenance()
	default:
		printUsage()
		os.Exit(1)
	}
}

func runTest() {
	// Parse test command arguments — argument errors can exit immediately, no cleanup needed yet.
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

	if debug {
		logger.SetLevel(logger.DebugLevel)
		logger.Debug("Debug mode enabled")
	}

	// Delegate to a helper that returns an exit code so that deferred cleanup
	// always runs before os.Exit is called (os.Exit skips defers).
	os.Exit(runTestWithCode(path))
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
	args := os.Args[2:]
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			logger.Info(`Usage: linespec build

Builds the linespec:latest Docker image using the currently installed binary.
This image is required by protocol proxy sidecars when running linespec tests
against services with database or HTTP dependencies.

Run this once after installation, or any time the Docker image is missing:

  linespec build

Docker must be running. If Docker is not available, start it and re-run.`)
			os.Exit(0)
		}
	}

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

	tmpDir, err := os.MkdirTemp("", "linespec-build-*")
	if err != nil {
		logger.Error("Failed to create temp directory: %v", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	// The Docker image runs Linux. On non-Linux hosts the host binary is the
	// wrong format (Mach-O on macOS, PE on Windows) and will fail with
	// "exec format error" inside the Alpine container. Cross-compile a Linux
	// binary before building the image.
	linuxBinaryPath := execPath
	if runtime.GOOS != "linux" {
		compiled, err := crossCompileLinuxBinary(execPath, tmpDir)
		if err != nil {
			logger.Error("Failed to build a Linux binary for the Docker image: %v", err)
			logger.Error("Ensure `go` is in PATH and one of the following is true:")
			logger.Error("  • Run `linespec build` from within the linespec source directory")
			logger.Error("  • Or install via: go install github.com/livecodelife/linespec/cmd/linespec@VERSION")
			os.Exit(1)
		}
		linuxBinaryPath = compiled
	}

	data, err := os.ReadFile(linuxBinaryPath)
	if err != nil {
		logger.Error("Failed to read linespec binary: %v", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "linespec"), data, 0755); err != nil {
		logger.Error("Failed to stage linespec binary: %v", err)
		os.Exit(1)
	}

	dockerfile := "FROM alpine:latest\n" +
		"RUN apk --no-cache add ca-certificates\n" +
		"WORKDIR /app\n" +
		"COPY linespec /app/linespec\n" +
		"ENTRYPOINT [\"/app/linespec\"]\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		logger.Error("Failed to write Dockerfile: %v", err)
		os.Exit(1)
	}

	logger.Info("Building linespec:latest Docker image...")
	cmd := exec.Command("docker", "build", "-t", "linespec:latest", tmpDir)
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
	if err := cmd.Run(); err != nil {
		logger.Error("Docker build failed: %v", err)
		logger.Error("Make sure Docker is running and try again: linespec build")
		os.Exit(1)
	}
	logger.Info("Successfully built linespec:latest")
}

// crossCompileLinuxBinary produces a linux/GOARCH binary at outPath.
// It tries two strategies in order:
//  1. Find the linespec source root (go.mod) by walking up from CWD or the
//     executable directory, then run `go build GOOS=linux`.
//  2. Run `go install github.com/livecodelife/linespec/cmd/linespec@VERSION`
//     with GOOS=linux, using the version embedded in the current binary.
func crossCompileLinuxBinary(execPath, tmpDir string) (string, error) {
	goarch := runtime.GOARCH
	outPath := filepath.Join(tmpDir, "linespec-linux")
	const modulePrefix = "module github.com/livecodelife/linespec"

	// Strategy 1a: walk up from the current working directory.
	if cwd, err := os.Getwd(); err == nil {
		if srcRoot := findGoModRoot(cwd, modulePrefix); srcRoot != "" {
			logger.Info("Cross-compiling Linux/%s binary from source: %s", goarch, srcRoot)
			if err := goBuildForLinux(srcRoot, "./cmd/linespec", outPath, goarch); err == nil {
				return outPath, nil
			}
		}
	}

	// Strategy 1b: walk up from the executable's directory.
	if srcRoot := findGoModRoot(filepath.Dir(execPath), modulePrefix); srcRoot != "" {
		logger.Info("Cross-compiling Linux/%s binary from source: %s", goarch, srcRoot)
		if err := goBuildForLinux(srcRoot, "./cmd/linespec", outPath, goarch); err == nil {
			return outPath, nil
		}
	}

	// Strategy 2: go install from the module proxy using the embedded version.
	moduleVersion := embeddedModuleVersion()
	logger.Info("Cross-compiling Linux/%s binary via go install @%s", goarch, moduleVersion)
	return outPath, goInstallForLinux("github.com/livecodelife/linespec/cmd/linespec", moduleVersion, outPath, goarch)
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

// goBuildForLinux runs `go build` with GOOS=linux from srcRoot and writes the
// binary to outPath.
func goBuildForLinux(srcRoot, pkg, outPath, goarch string) error {
	cmd := exec.Command("go", "build", "-o", outPath, "-ldflags", "-s -w", pkg)
	cmd.Dir = srcRoot
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goarch, "CGO_ENABLED=0")
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// goInstallForLinux runs `go install MOD@VERSION` with GOOS=linux and moves
// the result to outPath.
func goInstallForLinux(modulePath, version, outPath, goarch string) error {
	// go install refuses to install cross-compiled binaries when GOBIN is set.
	// Use a temp GOPATH instead; cross-compiled output lands in
	// $GOPATH/bin/linux_<GOARCH>/ rather than $GOPATH/bin/.
	tmpGopath, err := os.MkdirTemp("", "linespec-gopath-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpGopath)

	// Strip any existing GOBIN or GOPATH so they don't interfere.
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "GOBIN=") && !strings.HasPrefix(e, "GOPATH=") {
			filtered = append(filtered, e)
		}
	}
	filtered = append(filtered, "GOOS=linux", "GOARCH="+goarch, "CGO_ENABLED=0", "GOPATH="+tmpGopath)

	cmd := exec.Command("go", "install", modulePath+"@"+version)
	cmd.Env = filtered
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	// Cross-compiled binaries land in $GOPATH/bin/<GOOS>_<GOARCH>/.
	installed := filepath.Join(tmpGopath, "bin", "linux_"+goarch, "linespec")
	return os.Rename(installed, outPath)
}

// embeddedModuleVersion returns a module version suitable for `go install`.
// It reads the version from the current binary's build info, stripping any
// +dirty or local-only suffixes that the module proxy would reject.
func embeddedModuleVersion() string {
	info, ok := debugpkg.ReadBuildInfo()
	if !ok {
		return "latest"
	}
	v := info.Main.Version
	if v == "" || v == "(devel)" {
		return "latest"
	}
	// Strip +dirty or other local suffixes.
	if idx := strings.Index(v, "+"); idx >= 0 {
		v = v[:idx]
	}
	if v == "" {
		return "latest"
	}
	return v
}

func runImport() {
	args := os.Args[2:]
	var manifestURL string
	var version string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--version":
			i++
			if i < len(args) {
				version = args[i]
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec import <manifest-url> [options]

Options:
  --version <ver>   Pin to a specific manifest version (overrides @version suffix)
  --help, -h        Show this help message

Examples:
  linespec import https://example.com/linespec.manifest.json
  linespec import https://example.com/linespec.manifest.json@v3
  linespec import https://example.com/linespec.manifest.json --version v2`)
			return
		default:
			if manifestURL == "" && !strings.HasPrefix(args[i], "-") {
				manifestURL = args[i]
			}
		}
	}

	if manifestURL == "" {
		logger.Error("Usage: linespec import <manifest-url> [--version <ver>]")
		os.Exit(1)
	}

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

func runClone() {
	args := os.Args[2:]
	var manifestURL string
	var version string
	var destDir string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--version":
			i++
			if i < len(args) {
				version = args[i]
			}
		case "--dir":
			i++
			if i < len(args) {
				destDir = args[i]
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec clone <manifest-url> [options]

Options:
  --version <ver>   Pin to a specific manifest version (overrides @version suffix)
  --dir <path>      Destination directory name (default: derived from manifest URL)
  --help, -h        Show this help message

Examples:
  linespec clone https://example.com/linespec.manifest.json
  linespec clone https://example.com/linespec.manifest.json@v3
  linespec clone https://example.com/linespec.manifest.json --version v2 --dir myproject`)
			return
		default:
			if manifestURL == "" && !strings.HasPrefix(args[i], "-") {
				manifestURL = args[i]
			}
		}
	}

	if manifestURL == "" {
		logger.Error("Usage: linespec clone <manifest-url> [--version <ver>] [--dir <path>]")
		os.Exit(1)
	}

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

	// Extract layers
	if provData, ok := fetched.Layers["provenance"]; ok {
		provDir := filepath.Join(destDir, "provenance")
		if err := os.MkdirAll(provDir, 0755); err != nil {
			logger.Error("Failed to create provenance directory: %v", err)
			os.Exit(1)
		}
		if err := manifest.ExtractProvenance(provData, provDir); err != nil {
			logger.Error("Failed to extract provenance layer: %v", err)
			os.Exit(1)
		}
		fmt.Println("  ✓ extracted provenance layer")
	}
	for _, layerName := range []string{"specs", "code", "prompt"} {
		data, ok := fetched.Layers[layerName]
		if !ok {
			continue
		}
		if err := manifest.ExtractSpecs(data, destDir); err != nil {
			logger.Error("Failed to extract %s layer: %v", layerName, err)
			os.Exit(1)
		}
		fmt.Printf("  ✓ extracted %s layer\n", layerName)
	}

	fmt.Printf("\n✓ Project ready in ./%s\n", destDir)
}

func runInit() {
	args := os.Args[2:]
	opts := initcmd.Options{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--force", "-f":
			opts.Force = true
		case "--project", "-p":
			i++
			if i < len(args) {
				opts.ProjectPath = args[i]
			}
		case "--output", "-o":
			i++
			if i < len(args) {
				opts.OutputPath = args[i]
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec init [options]

Options:
  --force, -f           Overwrite existing .linespec.yml without prompting
  --project, -p <path>  Path to the project to set up (default: prompted)
  --output, -o <path>   Directory where .linespec.yml is written (default: project path)
  --help, -h            Show this help message`)
			return
		}
	}
	if err := initcmd.Run(opts); err != nil {
		logger.Error("%v", err)
		os.Exit(1)
	}
}

func printUsage() {
	logger.Info(`LineSpec v` + version + ` - Provenance Records & Integration Testing

Usage: linespec <command> [options]

Commands:
  init                       Interactively create a .linespec.yml for your project
  clone <manifest-url>       Bootstrap a project from a published manifest
  import <manifest-url>      Import provenance records from a published manifest
  provenance <subcommand>    Manage provenance records (alias: -p)
  test [--debug] <path>      Run .linespec test files
  build                      Build the linespec:latest Docker image
  proxy <type> ...           Start protocol proxy

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
		logger.Error("Usage: linespec proxy <type> <listen-addr> <upstream-addr> [registry-file] [--schema-file <path>]")
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

	// Extract --db-name, --host, --schema-data, --schema-file, --sidecar-port, --grpc-descriptor-set, and --debug flags from remaining args
	var dbName, kafkaHost, schemaDataB64, schemaFile, grpcDescriptorSet string
	sidecarPort := "8081"
	var filteredArgs []string
	for i := 5; i < len(os.Args); i++ {
		if os.Args[i] == "--db-name" && i+1 < len(os.Args) {
			dbName = os.Args[i+1]
			i++
		} else if strings.HasPrefix(os.Args[i], "--db-name=") {
			dbName = strings.TrimPrefix(os.Args[i], "--db-name=")
		} else if os.Args[i] == "--host" && i+1 < len(os.Args) {
			kafkaHost = os.Args[i+1]
			i++
		} else if strings.HasPrefix(os.Args[i], "--host=") {
			kafkaHost = strings.TrimPrefix(os.Args[i], "--host=")
		} else if os.Args[i] == "--schema-file" && i+1 < len(os.Args) {
			schemaFile = os.Args[i+1]
			i++
		} else if strings.HasPrefix(os.Args[i], "--schema-file=") {
			schemaFile = strings.TrimPrefix(os.Args[i], "--schema-file=")
		} else if os.Args[i] == "--schema-data" && i+1 < len(os.Args) {
			schemaDataB64 = os.Args[i+1]
			i++
		} else if strings.HasPrefix(os.Args[i], "--schema-data=") {
			schemaDataB64 = strings.TrimPrefix(os.Args[i], "--schema-data=")
		} else if os.Args[i] == "--sidecar-port" && i+1 < len(os.Args) {
			sidecarPort = os.Args[i+1]
			i++
	} else if strings.HasPrefix(os.Args[i], "--sidecar-port=") {
		sidecarPort = strings.TrimPrefix(os.Args[i], "--sidecar-port=")
	} else if os.Args[i] == "--grpc-descriptor-set" && i+1 < len(os.Args) {
		grpcDescriptorSet = os.Args[i+1]
		i++
	} else if strings.HasPrefix(os.Args[i], "--grpc-descriptor-set=") {
		grpcDescriptorSet = strings.TrimPrefix(os.Args[i], "--grpc-descriptor-set=")
	} else if os.Args[i] == "--debug" || os.Args[i] == "-d" {
			logger.SetLevel(logger.DebugLevel)
		} else {
			filteredArgs = append(filteredArgs, os.Args[i])
		}
	}

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
		pgProxy.SetResolver(resolver)
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

	// Create embedder client if configured
	var embedder *embeddings.Client
	if cfg.Embedding != nil {
		embedder, _ = embeddings.NewClient(*cfg.Embedding)
	}

	// Create commands
	cmds, err := provenance.NewCommandsWithEmbedder(cfg, repoRoot, os.Stdout, true, embedder)
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
							for _, f := range stagedFiles {
								if strings.HasPrefix(f, rel+"/") {
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
	case "lock-scope":
		opts := parseLockScopeOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.LockScope(opts); err != nil {
			os.Exit(1)
		}
	case "lock-layer":
		opts := parseLockLayerOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.LockLayer(opts); err != nil {
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
	case "context":
		opts := parseContextOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.Context(opts); err != nil {
			os.Exit(1)
		}
	case "search":
		opts := parseSearchOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.Search(opts); err != nil {
			logger.Error("Search failed: %v", err)
			os.Exit(1)
		}
	case "audit":
		opts := parseAuditOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.Audit(opts); err != nil {
			os.Exit(1)
		}
	case "index":
		opts := parseIndexOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.Index(opts); err != nil {
			os.Exit(1)
		}
	case "run-specs":
		opts := parseRunSpecsOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.RunSpecs(opts); err != nil {
			os.Exit(1)
		}
	case "open":
		opts := parseOpenOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.Open(opts); err != nil {
			os.Exit(1)
		}
	case "generate":
		opts := parseGenerateOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.Generate(opts); err != nil {
			logger.Error("%v", err)
			os.Exit(1)
		}
	case "sync":
		opts := parseSyncOptions(args)
		if err := cmds.Sync(opts); err != nil {
			os.Exit(1)
		}
	case "compile":
		opts := parseCompileOptions(args)
		if err := reloadConfigIfNeeded(&cfg, &cmds, opts.ConfigFile, repoRoot); err != nil {
			logger.Error("Failed to reload config: %v", err)
			os.Exit(1)
		}
		if err := cmds.Compile(opts); err != nil {
			os.Exit(1)
		}
	case "publish":
		opts := parsePublishOptions(args)
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
	case "install-hooks":
		if err := cmds.InstallHooks(); err != nil {
			logger.Error("Failed to install hooks: %v", err)
			os.Exit(1)
		}
	case "install-skills":
		opts := parseInstallSkillsOptions(args)
		if err := cmds.InstallSkills(opts); err != nil {
			logger.Error("Failed to install skills: %v", err)
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
			cfg.RunAssociatedSpecsOnComplete = fullConfig.Provenance.RunAssociatedSpecsOnComplete
			cfg.SharedRepos = fullConfig.Provenance.SharedRepos
			cfg.CacheTTLMinutes = fullConfig.Provenance.CacheTTLMinutes
			cfg.ManifestURL = fullConfig.Provenance.ManifestURL

			if fullConfig.Provenance.Embedding != nil {
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
  create [options]           Create a new provenance record (defaults to draft status)
  open [options]             Transition a record from draft to open (enables enforcement)
  lint [options]             Validate provenance records (supports --format sarif)
  status [options]           Show record status
  graph [options]            Render provenance graph
  check [options]            Check commits for violations
  lock-scope [options]       Lock scope to allowlist mode
  lock-layer [options]       Create a locked layer record
  complete [options]         Mark record as implemented
  deprecate [options]        Mark record as deprecated
  run-specs [options]        Run associated_specs for a record (used by pre-commit hook)
  context [options]          Show provenance context for files
  search [options]           Search provenance records by semantic similarity
  audit [options]            Audit recent changes against provenance history
  index [options]            Index all implemented records for semantic search
  generate [options]         Generate a behavioral specification document
  sync [options]             Refresh cache for all configured shared_repos
  compile [options]          Rebuild the hash manifest from all provenance records
  publish [options]          Package records into a versioned linespec.manifest.json
  install-hooks              Install git hooks
  install-skills [options]   Install all LineSpec Claude Code skills

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
		case "--type":
			if i+1 < len(args) {
				opts.Type = provenance.RecordType(args[i+1])
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
  --type brief|blueprint|imprint  Set the tier type of the record
  --no-edit                  Write without opening editor
  -i, --id-suffix name       Append service suffix to ID (e.g., user-service)
  -c, --config path          Path to custom .linespec.yml file
  --help                     Show this help message`)
			os.Exit(0)
		}
	}

	return opts
}

func parseOpenOptions(args []string) provenance.OpenOptions {
	opts := provenance.OpenOptions{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--record":
			if i+1 < len(args) {
				opts.RecordID = args[i+1]
				i++
			}
		case "-c", "--config":
			if i+1 < len(args) {
				opts.ConfigFile = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance open [options]

Options:
  --record prov-YYYY-NNN     Record to transition from draft to open
  -c, --config path          Path to custom .linespec.yml file
  --help                     Show this help message`)
			os.Exit(0)
		}
	}

	if opts.RecordID == "" {
		logger.Error("--record is required")
		os.Exit(1)
	}

	return opts
}

func parseInstallSkillsOptions(args []string) provenance.InstallSkillsOptions {
	opts := provenance.InstallSkillsOptions{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--path":
			if i+1 < len(args) {
				opts.Path = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance install-skills [options]

Options:
  --path dir     Target directory relative to repo root (default: .claude/skills)
  --help         Show this help message`)
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
		case "--warn":
			opts.ShowWarn = true
		case "--info":
			opts.ShowInfo = true
		case "--all":
			opts.ShowAll = true
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance lint [options]

Options:
  --record prov-YYYY-NNN     Lint a single record
  --enforcement level        Override enforcement (none|warn|strict)
  --format format            Output format (human|json|sarif)
  --warn                     Show only warnings in output
  --info                     Show only informational hints in output
  --all                      Show all output (errors, warnings, and hints)
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
		case "--staged":
			opts.Staged = true
		case "--message-file":
			if i+1 < len(args) {
				opts.MessageFile = args[i+1]
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
  --staged                   Check staged files instead of committed
  --message-file path        Path to commit message file (for staged mode)
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
  -c, --config path          Path to custom .linespec.yml file
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

func parseContextOptions(args []string) provenance.ContextOptions {
	opts := provenance.ContextOptions{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--files":
			// Collect all remaining arguments as files
			for j := i + 1; j < len(args); j++ {
				if strings.HasPrefix(args[j], "--") {
					i = j - 1
					break
				}
				opts.Files = append(opts.Files, args[j])
				if j == len(args)-1 {
					i = j
				}
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
			logger.Info(`Usage: linespec provenance context [options] <files...>

Arguments:
  <files...>                 File paths to retrieve context for

Options:
  --files f1 f2 f3          Explicit file list (alternative to positional args)
  --format format           Output format (human|compact|json)
  -c, --config path         Path to custom .linespec.yml file
  --help                    Show this help message`)
			os.Exit(0)
		default:
			// If not a flag, treat as positional file argument
			if !strings.HasPrefix(args[i], "--") && !strings.HasPrefix(args[i], "-") {
				opts.Files = append(opts.Files, args[i])
			}
		}
	}

	return opts
}

func parseSearchOptions(args []string) provenance.SearchOptions {
	opts := provenance.SearchOptions{
		Limit: 5,
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--query":
			if i+1 < len(args) {
				opts.Query = args[i+1]
				i++
			}
		case "--limit":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &opts.Limit)
				i++
			}
		case "-c", "--config":
			if i+1 < len(args) {
				opts.ConfigFile = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance search [options]

Options:
  --query "text"            Natural language search query
  --limit N                 Maximum results to return (default: 5)
  -c, --config path         Path to custom .linespec.yml file
  --help                    Show this help message

Example:
  linespec provenance search --query "authentication system"
  linespec provenance search --query "database schema changes" --limit 10`)
			os.Exit(0)
		default:
			if !strings.HasPrefix(args[i], "--") && !strings.HasPrefix(args[i], "-") && opts.Query == "" {
				opts.Query = args[i]
			}
		}
	}
	return opts
}

func parseAuditOptions(args []string) provenance.AuditOptions {
	opts := provenance.AuditOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--description":
			if i+1 < len(args) {
				opts.Description = args[i+1]
				i++
			}
		case "-c", "--config":
			if i+1 < len(args) {
				opts.ConfigFile = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance audit [options]

Options:
  --description "text"      Description of recent changes to audit
  -c, --config path         Path to custom .linespec.yml file
  --help                    Show this help message

Example:
  linespec provenance audit --description "Added password validation middleware"
  linespec provenance audit --description "Refactored user service to use new database schema"`)
			os.Exit(0)
		default:
			if !strings.HasPrefix(args[i], "--") && !strings.HasPrefix(args[i], "-") && opts.Description == "" {
				opts.Description = args[i]
			}
		}
	}
	return opts
}

func parseIndexOptions(args []string) provenance.IndexOptions {
	opts := provenance.IndexOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			opts.DryRun = true
		case "--force":
			opts.Force = true
		case "-c", "--config":
			if i+1 < len(args) {
				opts.ConfigFile = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance index [options]

Options:
  --dry-run                 Show what would be indexed without doing it
  --force                   Re-index even if embedding already exists
  -c, --config path         Path to custom .linespec.yml file
  --help                    Show this help message

Example:
  linespec provenance index              # Index all unindexed records
  linespec provenance index --dry-run    # Preview what would be indexed
  linespec provenance index --force      # Re-index all records`)
			os.Exit(0)
		}
	}
	return opts
}

func parseGenerateOptions(args []string) provenance.GenerateOptions {
	opts := provenance.GenerateOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--record", "-r":
			if i+1 < len(args) {
				opts.RecordID = args[i+1]
				i++
			}
		case "--format", "-f":
			if i+1 < len(args) {
				opts.Format = args[i+1]
				i++
			}
		case "--output", "-o":
			if i+1 < len(args) {
				opts.OutputFile = args[i+1]
				i++
			}
		case "-c", "--config":
			if i+1 < len(args) {
				opts.ConfigFile = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance generate [options]

Generates a behavioral specification document from provenance records.
Produces Markdown by default, or structured YAML via --format yaml.

Options:
  --record <id>       Target a specific brief or blueprint record
  --format <fmt>      Output format: markdown (default) or yaml
  --output <file>     Write output to a file instead of stdout
  -c, --config <path> Path to .linespec.yml
  --help              Show this help message

Rules:
  - When --record targets a brief, all implementing blueprints are included
  - When --record targets a blueprint, only that blueprint is included
  - Bug and imprint records cannot be targeted directly
  - When run without --record, compiles from the full active provenance graph
  - Imprint records are always excluded from generated output
  - Bug records that extend a blueprint have their content merged in
  - Bug records that supersede a blueprint replace that blueprint's content

Example:
  linespec provenance generate                                # Full spec, Markdown
  linespec provenance generate --format yaml                 # Full spec, YAML
  linespec provenance generate --record prov-2026-XXXXXXXX  # Scoped to one record
  linespec provenance generate --output spec.md              # Write to file`)
			os.Exit(0)
		}
	}
	return opts
}

func parseSyncOptions(args []string) provenance.SyncOptions {
	opts := provenance.SyncOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--force":
			opts.Force = true
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance sync [options]

Fetches the provenance directory from each configured shared_repo using git archive
and stores it in ~/.linespec/cache/. Skips repos whose cache is within the TTL window
unless --force is specified.

Options:
  --force     Ignore TTL and re-fetch all repos
  --help      Show this help message

Example:
  linespec provenance sync           # Sync all configured repos
  linespec provenance sync --force   # Force re-fetch even if cache is fresh`)
			os.Exit(0)
		}
	}
	return opts
}

func parseCompileOptions(args []string) provenance.CompileOptions {
	opts := provenance.CompileOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-c", "--config":
			if i+1 < len(args) {
				opts.ConfigFile = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance compile [options]

Rebuilds the hash manifest (.linespec/hash_manifest.json) from all provenance
records. Idempotent: if every record hash and both graph hashes already match
the stored manifest, no file is written.

Options:
  -c, --config path   Path to custom .linespec.yml file
  --help              Show this help message

Example:
  linespec provenance compile          # Rebuild manifest
  linespec provenance compile -c path/to/.linespec.yml`)
			os.Exit(0)
		}
	}
	return opts
}

func parsePublishOptions(args []string) provenance.PublishOptions {
	opts := provenance.PublishOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--manifest":
			if i+1 < len(args) {
				opts.ManifestPath = args[i+1]
				i++
			}
		case "--name":
			if i+1 < len(args) {
				opts.Name = args[i+1]
				i++
			}
		case "--version":
			if i+1 < len(args) {
				opts.Version = args[i+1]
				i++
			}
		case "--specs":
			if i+1 < len(args) {
				opts.SpecsPath = args[i+1]
				i++
			}
		case "--code":
			if i+1 < len(args) {
				opts.CodePath = args[i+1]
				i++
			}
		case "--prompt":
			if i+1 < len(args) {
				opts.PromptPath = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance publish [options]

Packages provenance records into a versioned, content-addressed linespec.manifest.json.
Applies a transformation pipeline (strip imprints, filter superseded, promote bugs,
clean dangling refs, reset status) before packaging. Hashes each layer artifact and
computes a root_hash. URL fields are left empty for the author to fill in after upload.

Options:
  --manifest path    Path to linespec.manifest.json (default: ./linespec.manifest.json)
  --name name        Project name written to the manifest (prompted if omitted)
  --version label    Explicit version label (default: auto-increment v1, v2, v3...)
  --specs path       Path to specs artifact (file or directory)
  --code path        Path to code artifact (file or directory)
  --prompt path      Path to prompt artifact file
  --help             Show this help message

Examples:
  linespec provenance publish
  linespec provenance publish --name my-service
  linespec provenance publish --manifest dist/linespec.manifest.json
  linespec provenance publish --version v2 --specs linespecs/ --prompt PROMPT.md`)
			os.Exit(0)
		}
	}
	return opts
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

func parseLockLayerOptions(args []string) provenance.LockLayerOptions {
	opts := provenance.LockLayerOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--title":
			if i+1 < len(args) {
				opts.Title = args[i+1]
				i++
			}
		case "--no-edit":
			opts.NoEdit = true
		case "-c", "--config":
			if i+1 < len(args) {
				opts.ConfigFile = args[i+1]
				i++
			}
		case "--help", "-h":
			printLockLayerUsage()
			os.Exit(0)
		}
	}
	if opts.Title == "" {
		logger.Error("--title is required")
		printLockLayerUsage()
		os.Exit(1)
	}
	return opts
}

func parseRunSpecsOptions(args []string) provenance.RunSpecsOptions {
	opts := provenance.RunSpecsOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--record", "-r":
			if i+1 < len(args) {
				opts.RecordID = args[i+1]
				i++
			}
		case "-c", "--config":
			if i+1 < len(args) {
				opts.ConfigFile = args[i+1]
				i++
			}
		case "--help", "-h":
			logger.Info(`Usage: linespec provenance run-specs --record <id> [options]

Options:
  --record, -r id            Record ID whose associated_specs to run
  -c, --config path          Path to custom .linespec.yml file
  --help                     Show this help message`)
			os.Exit(0)
		}
	}
	return opts
}

func printLockLayerUsage() {
	logger.Info(`Usage: linespec provenance lock-layer --title "..." [options]

Options:
  --title "..."              Required. Title for the locked layer record
  --no-edit                  Write without opening editor
  --help                     Show this help message`)
}
