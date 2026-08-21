package provenance

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLintCwdIndependent reproduces prov-2026-2f2bf9c3: a record's
// affected_scope/associated_specs paths are stored relative to the git
// repository root (the auto_affected_scope convention documented in
// PROVENANCE_RECORDS.md), matching what auto_affected_scope writes. Before
// the fix, the linter validated those paths with os.Stat/filepath.Glob/
// filepath.Walk(".") directly (validateExactPath, checkDeadRecords,
// validateAssociatedSpecs), which the OS resolves against the process's
// current working directory rather than against Commands.RepoRoot. Linting
// (or opening) from inside a nested package directory therefore doubled the
// path — "pkg_a/app/a.rb" became "pkg_a/pkg_a/app/a.rb" when cwd was already
// pkg_a — and reported phantom failures for scope/spec paths that exist on
// disk. Lint must report identical pass/warning/error/hint counts, and the
// same per-path status, regardless of the invoking cwd, as long as RepoRoot
// resolves to the same git repository root both times — the CLI's job
// (cmd/linespec's gitRepoRoot), simulated here by holding repoRoot fixed
// while only the process cwd changes.
func TestLintCwdIndependent(t *testing.T) {
	repoRoot := t.TempDir()

	// A nested package with its own provenance dir, mirroring a multi-pack
	// repo: config.Dir resolves relative to the directory holding the
	// declaring .linespec.yml (pkg_a/provenance here), independent of the
	// RepoRoot base used to resolve this record's own affected_scope /
	// associated_specs paths below.
	pkgDir := filepath.Join(repoRoot, "pkg_a")
	appDir := filepath.Join(pkgDir, "app")
	provDir := filepath.Join(pkgDir, "provenance")
	for _, d := range []string{appDir, provDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("MkdirAll %s: %v", d, err)
		}
	}

	// The governed file and its proof artifact both exist on disk, and the
	// record below stores both paths relative to repoRoot — exactly the
	// auto_affected_scope convention this repo's own records use.
	if err := os.WriteFile(filepath.Join(appDir, "a.rb"), []byte("# app\n"), 0644); err != nil {
		t.Fatalf("write a.rb: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "a_spec.rb"), []byte("# spec\n"), 0644); err != nil {
		t.Fatalf("write a_spec.rb: %v", err)
	}

	recordYAML := `id: prov-2026-abcd1234
title: cwd-independence repro record
status: open
created_at: '2026-08-21'
author: test@example.com
intent: repro for prov-2026-2f2bf9c3
constraints:
  - repro
affected_scope:
  - pkg_a/app/a.rb
type: blueprint
associated_specs:
  - path: pkg_a/app/a_spec.rb
    run_command: "true"
`
	if err := os.WriteFile(filepath.Join(provDir, "prov-2026-abcd1234.yml"), []byte(recordYAML), 0644); err != nil {
		t.Fatalf("write record: %v", err)
	}

	// lintFrom builds Commands with RepoRoot fixed at the true repo root
	// (what the CLI's git-root resolution produces regardless of cwd) but
	// invokes LintAll with the process cwd set to the given directory —
	// isolating exactly the variable the bug depended on.
	lintFrom := func(cwd string) *LintResult {
		orig, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd: %v", err)
		}
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("Chdir %s: %v", cwd, err)
		}
		t.Cleanup(func() {
			if err := os.Chdir(orig); err != nil {
				t.Fatalf("restore Chdir: %v", err)
			}
		})

		cfg := &ProvenanceConfig{Dir: provDir, Enforcement: "warn"}
		cmds, err := NewCommandsWithEmbedder(cfg, repoRoot, os.Stdout, false, nil)
		if err != nil {
			t.Fatalf("NewCommandsWithEmbedder: %v", err)
		}
		result := cmds.Linter.LintAll()

		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore Chdir: %v", err)
		}
		return result
	}

	fromRoot := lintFrom(repoRoot)
	fromNested := lintFrom(appDir)

	if fromRoot.ErrorCount != fromNested.ErrorCount ||
		fromRoot.WarningCount != fromNested.WarningCount ||
		fromRoot.HintCount != fromNested.HintCount ||
		fromRoot.PassedCount != fromNested.PassedCount {
		t.Fatalf("lint result differs by invoking cwd:\n  from repo root:  errors=%d warnings=%d hints=%d passed=%d issues=%+v\n  from nested pkg: errors=%d warnings=%d hints=%d passed=%d issues=%+v",
			fromRoot.ErrorCount, fromRoot.WarningCount, fromRoot.HintCount, fromRoot.PassedCount, fromRoot.Issues,
			fromNested.ErrorCount, fromNested.WarningCount, fromNested.HintCount, fromNested.PassedCount, fromNested.Issues)
	}

	// Not just "equally wrong" on both sides: the scope/spec paths must
	// actually resolve and be found when linting from the nested directory,
	// so a regression that breaks both invocations identically still fails
	// this test.
	for _, issue := range fromNested.Issues {
		if issue.RecordID == "prov-2026-abcd1234" && (issue.Field == "scope" || issue.Field == "associated_specs") {
			t.Fatalf("unexpected scope/associated_specs issue when linting from a nested package directory: %+v", issue)
		}
	}
}

// TestCheckDeadRecordsCwdIndependent reproduces the "Dead record" half of
// prov-2026-2f2bf9c3: checkDeadRecords used os.Stat/filepath.Glob/
// filepath.Walk(".") directly on affected_scope patterns, so a live record
// governing files that still exist was misreported as dead purely because
// of the invoking cwd — and following that advice would deprecate a record
// whose files were never deleted.
func TestCheckDeadRecordsCwdIndependent(t *testing.T) {
	repoRoot := t.TempDir()
	appDir := filepath.Join(repoRoot, "pkg_a", "app")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "a.rb"), []byte("# app\n"), 0644); err != nil {
		t.Fatalf("write a.rb: %v", err)
	}

	record := makeTestRecord("prov-2026-deadbeef", "open")
	record.AffectedScope = []string{"pkg_a/app/a.rb"}

	loader := &Loader{Records: []*Record{record}}
	linter := NewLinter(loader, "warn")
	linter.RepoRoot = repoRoot

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(appDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore Chdir: %v", err)
		}
	}()

	result := &LintResult{}
	linter.checkDeadRecords(result)

	for _, issue := range result.Issues {
		if issue.RecordID == record.ID {
			t.Fatalf("record governing an existing file was misreported as dead when linted from a nested package directory: %+v", issue)
		}
	}
}
