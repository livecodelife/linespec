package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildDoesNotHardcodeCGODisabled guards against reintroducing a
// CGO_ENABLED=0 host-side cross-compile of the Linux binary. Once
// pkg/discover pulled in go-tree-sitter (a cgo package), any GOOS=linux build
// with cgo disabled fails to compile with "undefined: Node" because the
// tree-sitter grammar packages' cgo files (which define the Node type) are
// excluded by the build constraints. See prov-2026-b9339a5a.
func TestBuildDoesNotHardcodeCGODisabled(t *testing.T) {
	src, err := os.ReadFile("main_stable.go")
	if err != nil {
		t.Fatalf("failed to read main_stable.go: %v", err)
	}
	// Match the quoted Go string literal, as it would appear in a real
	// `cmd.Env = append(..., "CGO_ENABLED=0")` env setting, not prose in
	// comments that merely discuss the historical bug.
	if strings.Contains(string(src), `"CGO_ENABLED=0"`) {
		t.Fatal("main_stable.go must not hardcode CGO_ENABLED=0 anywhere in the " +
			"linespec:latest build path — the tree-sitter (go-tree-sitter) " +
			"dependency requires cgo to build the Linux binary; see prov-2026-b9339a5a")
	}
}

// TestFindLinespecSourceRoot exercises the source-root discovery `linespec
// build` uses to decide whether it can build the image via `docker build -f
// Dockerfile.linespec` (see buildFromSourceCheckout). From within the
// linespec checkout it must locate the module root; outside a checkout it
// must return "" so runBuild fails with an actionable message instead of
// attempting a doomed cgo cross-compile.
func TestFindLinespecSourceRoot(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd)) // cmd/linespec -> repo root
	if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err != nil {
		t.Fatalf("expected repo root at %s to contain go.mod: %v", repoRoot, err)
	}

	// Strategy 1a (cwd walk-up) finds the checkout regardless of execPath,
	// since the test process's cwd is still inside it at this point.
	if got := findLinespecSourceRoot("/nonexistent/linespec"); got != repoRoot {
		t.Fatalf("findLinespecSourceRoot from within checkout = %q, want %q", got, repoRoot)
	}

	// Move outside the checkout so neither the cwd walk-up nor the execPath
	// walk-up can find a go.mod declaring the linespec module.
	outside := t.TempDir()
	t.Chdir(outside)
	if got := findLinespecSourceRoot(filepath.Join(outside, "linespec")); got != "" {
		t.Fatalf("findLinespecSourceRoot outside checkout = %q, want \"\"", got)
	}
}
