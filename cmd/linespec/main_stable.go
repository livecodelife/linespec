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
	"github.com/livecodelife/linespec/pkg/initcmd"
	"github.com/livecodelife/linespec/pkg/interpolate"
	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/provenance"
	"github.com/livecodelife/linespec/pkg/discover/boundaries"
	"github.com/livecodelife/linespec/pkg/discover/enrich"
	"github.com/livecodelife/linespec/pkg/discover/framework"
	discoverrecords "github.com/livecodelife/linespec/pkg/discover/records"
	discoverroutes "github.com/livecodelife/linespec/pkg/discover/routes"
	"github.com/livecodelife/linespec/pkg/discover/stubs"
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
)

func main() {
	// Preserve the legacy `-p` alias for the provenance command. Cobra cannot
	// register a flag-like command alias, so rewrite it before dispatch.
	if len(os.Args) > 1 && os.Args[1] == "-p" {
		os.Args[1] = "provenance"
	}
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
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
			cfg.OverlapSpecsOnComplete = fullConfig.Provenance.OverlapSpecsOnComplete
			cfg.CommitOnStatusChange = fullConfig.Provenance.CommitOnStatusChange
			cfg.SharedRepos = fullConfig.Provenance.SharedRepos
			cfg.CacheTTLMinutes = fullConfig.Provenance.CacheTTLMinutes
			cfg.ManifestURL = fullConfig.Provenance.ManifestURL
			cfg.ExcludePaths = fullConfig.Provenance.ExcludePaths

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

// discoverOptions holds parsed flags for the discover subcommand.
type discoverOptions struct {
	Dir        string
	Lang       string
	Framework  string
	Enrich     bool
	LLMBaseURL string
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
			logger.Error("Could not detect framework in %q. Use --lang and --framework to specify.", scanDir)
			os.Exit(1)
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
		printDiscoverDryRun(desc, scanDir, stubResults, recordResults, sum, opts.Format)
		return
	}

	recordResults, sum, err := discoverrecords.Write(recordInput, existingIDs)
	if err != nil {
		logger.Error("Record generation failed: %v", err)
		os.Exit(1)
	}

	printDiscoverSummary(desc, scanDir, recordResults, sum)

	if opts.Enrich {
		enrichRoot := scanDir
		if out, err := exec.Command("git", "-C", scanDir, "rev-parse", "--show-toplevel").Output(); err == nil {
			enrichRoot = strings.TrimSpace(string(out))
		}
		runDiscoverEnrich(recordResults, enrichRoot, opts.LLMBaseURL)
	}
}

// runDiscoverEnrich runs Phase 5: git history analysis + LLM intent synthesis.
// Errors are non-fatal: each failure is logged as a warning and the pipeline continues.
func runDiscoverEnrich(results []discoverrecords.Result, repoRoot, llmBaseURL string) {
	recordFiles := make([]string, 0, len(results))
	for _, r := range results {
		recordFiles = append(recordFiles, r.FilePath)
	}

	fmt.Fprintln(os.Stdout, "\n  Phase 5: git history enrichment")
	enrichResults, err := enrich.Enrich(enrich.Input{
		RepoDir:     repoRoot,
		RecordFiles: recordFiles,
		LLMBaseURL:  llmBaseURL,
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
