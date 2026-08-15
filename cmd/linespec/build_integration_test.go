//go:build integration

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDockerfileLinespecBuildsWithCGOAndRealVersion is the Docker-backed half
// of the prov-2026-b9339a5a repro: it builds linespec:latest the way `linespec
// build` now does from a source checkout (`docker build -f
// Dockerfile.linespec`), and asserts the resulting binary both runs (cgo/
// tree-sitter compiled correctly) and reports the real VERSION instead of the
// ldflag-less "vdev" default. Requires Docker; run via `make test-integration`
// or `go test -tags integration ./cmd/linespec/... -v`.
func TestDockerfileLinespecBuildsWithCGOAndRealVersion(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd)) // cmd/linespec -> repo root

	wantVersion, err := os.ReadFile(filepath.Join(repoRoot, "VERSION"))
	if err != nil {
		t.Fatalf("failed to read VERSION file: %v", err)
	}
	want := strings.TrimSpace(string(wantVersion))

	const image = "linespec-build-integration-test:latest"

	buildCmd := exec.Command("docker", "build", "-f", "Dockerfile.linespec", "-t", image, ".")
	buildCmd.Dir = repoRoot
	buildCmd.Env = append(os.Environ(), "DOCKER_BUILDKIT=0")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("docker build -f Dockerfile.linespec failed: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rmi", "-f", image).Run()
	})

	runCmd := exec.Command("docker", "run", "--rm", image, "--version")
	out, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker run %s --version failed: %v\n%s", image, err, out)
	}

	got := strings.TrimSpace(string(out))
	if strings.Contains(got, "vdev") {
		t.Fatalf("linespec:latest --version reported %q, want a real version containing %q, not the ldflag-less default", got, want)
	}
	if !strings.Contains(got, want) {
		t.Fatalf("linespec:latest --version reported %q, want it to contain VERSION %q", got, want)
	}
}
