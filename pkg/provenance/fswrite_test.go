package provenance

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writePerm(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), mode); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func isWritableOnDisk(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat %s: %v", path, err)
	}
	return info.Mode().Perm()&0o200 != 0
}

// --- AlwaysWritablePaths / IsAlwaysWritable ---------------------------------

func TestAlwaysWritablePaths_IsDerivedNotHandMaintained(t *testing.T) {
	config := &ProvenanceConfig{
		Dir:          "provenance",
		ExcludePaths: []string{"*.md", "vendor/"},
	}
	records := []*Record{
		{ID: "prov-2026-aaaa0001", AssociatedSpecs: []AssociatedSpec{{Path: "pkg/foo_test.go"}}},
		{ID: "prov-2026-aaaa0002", AssociatedSpecs: []AssociatedSpec{{Path: "pkg/foo_test.go"}, {Path: "specs/bar.linespec"}}},
	}

	always := AlwaysWritablePaths(config, records, "")

	for _, want := range []string{"*.md", "vendor/", "provenance", ".linespec.yml", "pkg/foo_test.go", "specs/bar.linespec"} {
		if !slices.Contains(always, want) {
			t.Errorf("AlwaysWritablePaths missing %q, got %v", want, always)
		}
	}
	// De-duplicated: pkg/foo_test.go appears in two records but once in the set.
	count := 0
	for _, p := range always {
		if p == "pkg/foo_test.go" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected pkg/foo_test.go once, got %d times in %v", count, always)
	}
}

func TestAlwaysWritablePaths_DefaultsDirToProvenance(t *testing.T) {
	always := AlwaysWritablePaths(&ProvenanceConfig{}, nil, "")
	if !slices.Contains(always, "provenance") {
		t.Errorf("expected default provenance dir in always-writable set, got %v", always)
	}
}

// TestAlwaysWritablePaths_NormalizesAbsoluteDirToRepoRelative guards against a
// regression where an absolute config.Dir (as tests elsewhere construct, or a
// user's .linespec.yml could set) never matches the repo-relative paths
// `git ls-files` produces, silently locking the entire provenance directory
// out from under reconcile.
func TestAlwaysWritablePaths_NormalizesAbsoluteDirToRepoRelative(t *testing.T) {
	repoRoot := "/repo"
	always := AlwaysWritablePaths(&ProvenanceConfig{Dir: "/repo/provenance"}, nil, repoRoot)
	if !slices.Contains(always, "provenance") {
		t.Errorf("expected absolute config.Dir normalized to repo-relative \"provenance\", got %v", always)
	}
	if !IsAlwaysWritable("provenance/prov-2026-aaaa0001.yml", always) {
		t.Errorf("a repo-relative record path should match the normalized always-writable dir")
	}
}

func TestIsAlwaysWritable_MatchesDirectoryPrefixAndGlob(t *testing.T) {
	always := AlwaysWritablePaths(&ProvenanceConfig{Dir: "provenance", ExcludePaths: []string{"*.md"}}, nil, "")

	cases := map[string]bool{
		"provenance/prov-2026-aaaa0001.yml": true,
		".linespec.yml":                     true,
		"README.md":                         true,
		"pkg/provenance/commands.go":        false,
	}
	for path, want := range cases {
		if got := IsAlwaysWritable(path, always); got != want {
			t.Errorf("IsAlwaysWritable(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestAlwaysWritablePaths_ExemptsNestedConfigsOwnLinespecYML reproduces
// issue #173 / prov-2026-c65450b6: PROVENANCE_RECORDS.md documents that
// ".linespec.yml remains always-writable", but AlwaysWritablePaths used to
// append the bare literal ".linespec.yml" regardless of where the reconciling
// config actually lives, so only a repo-root config's own file matched — a
// nested config (e.g. pkg_a/.linespec.yml, the very file that turns
// write_restriction on) was locked like any other undeclared file. The fix
// derives the config-file entry from config.ConfigFileDir the same way the
// provenance-dir entry is derived from config.Dir.
func TestAlwaysWritablePaths_ExemptsNestedConfigsOwnLinespecYML(t *testing.T) {
	always := AlwaysWritablePaths(&ProvenanceConfig{Dir: "pkg_a/provenance", ConfigFileDir: "pkg_a"}, nil, "")

	if !IsAlwaysWritable("pkg_a/.linespec.yml", always) {
		t.Errorf("expected nested config's own .linespec.yml to be always-writable, got %v", always)
	}
	if IsAlwaysWritable("pkg_b/.linespec.yml", always) {
		t.Errorf("a sibling config's .linespec.yml outside ConfigFileDir must not be exempted, got %v", always)
	}
	if IsAlwaysWritable(".linespec.yml", always) {
		t.Errorf("the repo-root .linespec.yml must not be exempted by a nested config's reconcile pass, got %v", always)
	}
}

// TestAlwaysWritablePaths_NilOrUnsetConfigFileDirExemptsRepoRootLinespecYML
// guards the no-regression constraint: a nil config, or one with an empty
// ConfigFileDir, must still exempt the repo-root ".linespec.yml" exactly as
// before this fix.
func TestAlwaysWritablePaths_NilOrUnsetConfigFileDirExemptsRepoRootLinespecYML(t *testing.T) {
	if !IsAlwaysWritable(".linespec.yml", AlwaysWritablePaths(nil, nil, "")) {
		t.Errorf("nil config should still exempt the repo-root .linespec.yml")
	}
	if !IsAlwaysWritable(".linespec.yml", AlwaysWritablePaths(&ProvenanceConfig{}, nil, "")) {
		t.Errorf("unset ConfigFileDir should still exempt the repo-root .linespec.yml")
	}
}

// --- OpenAllowlistScope ------------------------------------------------------

func TestOpenAllowlistScope_OnlyOpenAllowlistRecordsContribute(t *testing.T) {
	records := []*Record{
		{ID: "prov-2026-0001", Status: StatusOpen, AffectedScope: []string{"pkg/a.go"}},
		{ID: "prov-2026-0002", Status: StatusDraft, AffectedScope: []string{"pkg/b.go"}},       // draft: no writability yet
		{ID: "prov-2026-0003", Status: StatusImplemented, AffectedScope: []string{"pkg/c.go"}}, // sealed: no longer contributing
		{ID: "prov-2026-0004", Status: StatusOpen, AffectedScope: nil},                         // observed mode: never contributes
		{ID: "prov-2026-0005", Status: StatusOpen, AffectedScope: []string{"pkg/a.go", "pkg/d.go"}},
	}

	got := OpenAllowlistScope(records)
	want := []string{"pkg/a.go", "pkg/d.go"}
	if len(got) != len(want) {
		t.Fatalf("OpenAllowlistScope = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("OpenAllowlistScope[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// --- IsWritablePath ----------------------------------------------------------

func TestIsWritablePath(t *testing.T) {
	always := []string{"provenance"}
	openScope := []string{"pkg/foo.go", "pkg/bar/*.go"}

	cases := map[string]bool{
		"provenance/prov-2026-aaaa0001.yml": true,
		"pkg/foo.go":                        true,
		"pkg/bar/baz.go":                    true,
		"pkg/other.go":                      false,
	}
	for path, want := range cases {
		got, err := IsWritablePath(path, always, openScope)
		if err != nil {
			t.Fatalf("IsWritablePath(%q): %v", path, err)
		}
		if got != want {
			t.Errorf("IsWritablePath(%q) = %v, want %v", path, got, want)
		}
	}
}

// --- LockFile / UnlockFile / UnlockDir ---------------------------------------

func TestLockFile_StripsWriteLeavesOtherBitsAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	writePerm(t, path, 0o755) // executable, writable

	if err := LockFile(path); err != nil {
		t.Fatalf("LockFile: %v", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o555 {
		t.Errorf("mode = %o, want 0555 (write stripped, exec kept)", info.Mode().Perm())
	}
}

func TestUnlockFile_AddsOwnerWriteLeavesOtherBitsAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	writePerm(t, path, 0o444)

	if err := UnlockFile(path); err != nil {
		t.Fatalf("UnlockFile: %v", err)
	}
	if !isWritableOnDisk(t, path) {
		t.Errorf("expected file writable after UnlockFile")
	}
}

func TestLockFile_UnlockFile_MissingPathIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.go")
	if err := LockFile(path); err != nil {
		t.Errorf("LockFile on missing path should be a no-op, got %v", err)
	}
	if err := UnlockFile(path); err != nil {
		t.Errorf("UnlockFile on missing path should be a no-op, got %v", err)
	}
}

func TestUnlockDir_DoesNotAlterSiblingFilePermissions(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	locked := filepath.Join(sub, "locked.go")
	writePerm(t, locked, 0o444)
	if err := os.Chmod(sub, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	if err := UnlockDir(sub); err != nil {
		t.Fatalf("UnlockDir: %v", err)
	}

	info, _ := os.Stat(sub)
	if info.Mode().Perm()&0o200 == 0 {
		t.Errorf("directory should be writable after UnlockDir, mode = %o", info.Mode().Perm())
	}
	// The already-locked sibling file must remain locked — unlocking a directory
	// to permit new-file creation must not alter files already present in it.
	if isWritableOnDisk(t, locked) {
		t.Errorf("UnlockDir must not alter permission bits of files already present in the directory")
	}
}

// --- MaterializeScope ---------------------------------------------------------

func TestMaterializeScope_UnlocksExistingFile(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, "pkg", "foo.go")
	writePerm(t, target, 0o444)

	if err := MaterializeScope(repo, []string{"pkg/foo.go"}); err != nil {
		t.Fatalf("MaterializeScope: %v", err)
	}
	if !isWritableOnDisk(t, target) {
		t.Errorf("expected pkg/foo.go writable after MaterializeScope")
	}
}

func TestMaterializeScope_UnlocksParentDirForNotYetExistingFile(t *testing.T) {
	repo := t.TempDir()
	pkgDir := filepath.Join(repo, "pkg")
	if err := os.Mkdir(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(pkgDir, "sibling.go")
	writePerm(t, sibling, 0o444)
	if err := os.Chmod(pkgDir, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	if err := MaterializeScope(repo, []string{"pkg/new_file.go"}); err != nil {
		t.Fatalf("MaterializeScope: %v", err)
	}

	dirInfo, _ := os.Stat(filepath.Join(repo, "pkg"))
	if dirInfo.Mode().Perm()&0o200 == 0 {
		t.Errorf("expected pkg/ writable so pkg/new_file.go can be created, mode = %o", dirInfo.Mode().Perm())
	}
	// The sibling's own bits must be untouched by unlocking the directory.
	if isWritableOnDisk(t, sibling) {
		t.Errorf("unlocking the directory for a not-yet-existing file must not touch sibling file bits")
	}
}

func TestMaterializeScope_SkipsGlobPatterns(t *testing.T) {
	repo := t.TempDir()
	// A glob pattern is not a concrete path to chmod — MaterializeScope must not
	// error attempting to Lstat it.
	if err := MaterializeScope(repo, []string{"pkg/*.go"}); err != nil {
		t.Fatalf("MaterializeScope should skip glob patterns without error, got %v", err)
	}
}

// --- ColdStartSkip -------------------------------------------------------------

func TestColdStartSkip_HandInitWithNoRecordsDefersEnforcement(t *testing.T) {
	if !ColdStartSkip(nil, &ProvenanceConfig{}) {
		t.Errorf("expected cold-start skip=true for a hand-initialized project with no records")
	}
}

func TestColdStartSkip_ClonedProjectDefaultsStrictDenyEvenWithNoLocalRecords(t *testing.T) {
	// linespec clone stamps ManifestURL — the project arrived pre-seeded with open
	// records to enforce against, so cold-start deferral must not apply.
	if ColdStartSkip(nil, &ProvenanceConfig{ManifestURL: "https://example.com/manifest.yml"}) {
		t.Errorf("expected cold-start skip=false for a cloned project (ManifestURL set)")
	}
}

func TestColdStartSkip_FalseOnceRecordsExist(t *testing.T) {
	records := []*Record{{ID: "prov-2026-0001"}}
	if ColdStartSkip(records, &ProvenanceConfig{}) {
		t.Errorf("expected cold-start skip=false once at least one record exists")
	}
}

// --- Reconcile -----------------------------------------------------------------

func TestReconcile_LocksUncoveredUnlocksCoveredIsIdempotent(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	covered := filepath.Join(repo, "pkg", "covered.go")
	uncovered := filepath.Join(repo, "pkg", "uncovered.go")
	// Start in the opposite state of what reconcile should produce, so the test
	// actually exercises both directions.
	writePerm(t, covered, 0o444)
	writePerm(t, uncovered, 0o644)

	records := []*Record{
		{ID: "prov-2026-0001", Status: StatusOpen, AffectedScope: []string{"pkg/covered.go"}},
	}
	config := &ProvenanceConfig{Dir: "provenance"}

	result, err := Reconcile(repo, []string{"pkg/covered.go", "pkg/uncovered.go"}, records, config)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !isWritableOnDisk(t, covered) {
		t.Errorf("pkg/covered.go should be writable (in open record's affected_scope)")
	}
	if isWritableOnDisk(t, uncovered) {
		t.Errorf("pkg/uncovered.go should be locked (governed tree defaults to non-writable)")
	}
	if len(result.Unlocked) != 1 || result.Unlocked[0] != "pkg/covered.go" {
		t.Errorf("result.Unlocked = %v, want [pkg/covered.go]", result.Unlocked)
	}
	if len(result.Locked) != 1 || result.Locked[0] != "pkg/uncovered.go" {
		t.Errorf("result.Locked = %v, want [pkg/uncovered.go]", result.Locked)
	}

	// Idempotent: running again from the now-reconciled state changes nothing.
	result2, err := Reconcile(repo, []string{"pkg/covered.go", "pkg/uncovered.go"}, records, config)
	if err != nil {
		t.Fatalf("Reconcile (second pass): %v", err)
	}
	if len(result2.Unlocked) != 0 || len(result2.Locked) != 0 {
		t.Errorf("second Reconcile pass should be a no-op, got unlocked=%v locked=%v", result2.Unlocked, result2.Locked)
	}
}

func TestReconcile_AlwaysWritablePathsStayWritableEvenUncovered(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "provenance"), 0o755); err != nil {
		t.Fatal(err)
	}
	recordFile := filepath.Join(repo, "provenance", "prov-2026-0001.yml")
	writePerm(t, recordFile, 0o644)

	records := []*Record{{ID: "prov-2026-0001", Status: StatusDraft}} // draft, not open: no scope granted
	config := &ProvenanceConfig{Dir: "provenance"}

	if _, err := Reconcile(repo, []string{"provenance/prov-2026-0001.yml"}, records, config); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !isWritableOnDisk(t, recordFile) {
		t.Errorf("provenance directory must remain unconditionally writable")
	}
}

func TestReconcile_ColdStartSkipsEnforcementEntirely(t *testing.T) {
	repo := t.TempDir()
	uncovered := filepath.Join(repo, "uncovered.go")
	writePerm(t, uncovered, 0o644)

	result, err := Reconcile(repo, []string{"uncovered.go"}, nil, &ProvenanceConfig{})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(result.Locked) != 0 {
		t.Errorf("cold-start (no records yet) must defer enforcement, got locked=%v", result.Locked)
	}
	if !isWritableOnDisk(t, uncovered) {
		t.Errorf("cold-start must leave files untouched")
	}
}

func TestReconcile_UnlocksParentDirOfDeclaredNotYetExistingPath(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, "pkg"), 0o555); err != nil {
		t.Fatal(err)
	}

	records := []*Record{
		{ID: "prov-2026-0001", Status: StatusOpen, AffectedScope: []string{"pkg/new_file.go"}},
	}
	if _, err := Reconcile(repo, nil, records, &ProvenanceConfig{Dir: "provenance"}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	info, _ := os.Stat(filepath.Join(repo, "pkg"))
	if info.Mode().Perm()&0o200 == 0 {
		t.Errorf("expected pkg/ unlocked so pkg/new_file.go could be created, mode = %o", info.Mode().Perm())
	}
}

// --- boundToOwnConfigDir / nestedConfigDirs (prov-2026-bde50f4d) ------------
//
// Reproduces the bug: a monorepo with a nested pkg/foo/.linespec.yml governing
// its own provenance/ was previously invisible to the boundary logic, so the
// repo-root config's reconcile pass walked (and could lock) every file in the
// repo, including files under pkg/foo/ that belong to a more specific,
// directory-local config. The closest parent directory of a linespec config
// file must be the one that governs a path.

func TestNestedConfigDirs_FindsConfigsStrictlyBeneathOwnDir(t *testing.T) {
	files := []string{
		".linespec.yml",
		"pkg/foo/.linespec.yml",
		"pkg/foo/bar/.linespec.yml",
		"other/.linespec.yml",
		"pkg/foo/main.go",
	}

	got := nestedConfigDirs(".", files)
	want := []string{"other", "pkg/foo", "pkg/foo/bar"}
	if len(got) != len(want) {
		t.Fatalf("nestedConfigDirs(\".\") = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("nestedConfigDirs(\".\")[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}

	// From pkg/foo's own perspective, only its own deeper nested config
	// (pkg/foo/bar) counts — the root config and the sibling "other" config are
	// not beneath it.
	gotFoo := nestedConfigDirs("pkg/foo", files)
	if len(gotFoo) != 1 || gotFoo[0] != "pkg/foo/bar" {
		t.Errorf("nestedConfigDirs(\"pkg/foo\") = %v, want [pkg/foo/bar]", gotFoo)
	}
}

func TestNestedConfigDirs_EmptyWhenFilesListHasNoOtherConfig(t *testing.T) {
	// The common case exercised by every other fswrite test in this file: no
	// .linespec.yml appears in the candidate file list at all (tests construct
	// ProvenanceConfig directly, never writing an actual config file to disk).
	// Boundary logic must be a complete no-op here, preserving legacy behavior.
	files := []string{"pkg/a.go", "pkg/b.go"}
	if got := nestedConfigDirs(".", files); len(got) != 0 {
		t.Errorf("nestedConfigDirs = %v, want empty", got)
	}
}

func TestBoundToOwnConfigDir_RootExcludesNestedConfigSubtreeEntirely(t *testing.T) {
	files := []string{
		".linespec.yml",
		"pkg/root.go",
		"pkg/uncovered.go",
		"pkg/foo/.linespec.yml",
		"pkg/foo/foo.go",
		"pkg/foo/other.go",
	}

	got := boundToOwnConfigDir(".", files)
	for _, want := range []string{".linespec.yml", "pkg/root.go", "pkg/uncovered.go"} {
		if !slices.Contains(got, want) {
			t.Errorf("boundToOwnConfigDir(\".\") missing %q, got %v", want, got)
		}
	}
	for _, notWant := range []string{"pkg/foo/.linespec.yml", "pkg/foo/foo.go", "pkg/foo/other.go"} {
		if slices.Contains(got, notWant) {
			t.Errorf("boundToOwnConfigDir(\".\") must exclude %q (governed by the closer pkg/foo config), got %v", notWant, got)
		}
	}
}

func TestBoundToOwnConfigDir_NestedConfigOnlySeesItsOwnDirectory(t *testing.T) {
	files := []string{
		".linespec.yml",
		"pkg/root.go",
		"pkg/foo/.linespec.yml",
		"pkg/foo/foo.go",
		"other/other.go",
	}

	got := boundToOwnConfigDir("pkg/foo", files)
	want := []string{"pkg/foo/.linespec.yml", "pkg/foo/foo.go"}
	if len(got) != len(want) {
		t.Fatalf("boundToOwnConfigDir(\"pkg/foo\") = %v, want %v", got, want)
	}
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("boundToOwnConfigDir(\"pkg/foo\") missing %q, got %v", w, got)
		}
	}
}

func TestBoundToOwnConfigDir_UnrestrictedWhenNoNestedConfigExists(t *testing.T) {
	// Matches every pre-existing Reconcile test in this file: no nested config
	// in the candidate list means the full file list passes through unchanged.
	files := []string{"pkg/a.go", "pkg/b.go", "provenance/prov-2026-0001.yml"}
	got := boundToOwnConfigDir(".", files)
	if len(got) != len(files) {
		t.Errorf("boundToOwnConfigDir with no nested config = %v, want unchanged %v", got, files)
	}
}

// --- Managed .linespec/ state (prov-2026-26efc162) --------------------------

// TestAlwaysWritablePaths_IncludesManagedLinespecStateFiles reproduces the bug:
// LineSpec's own regenerated state files under .linespec/ (embeddings.bin,
// hash_manifest.json) were not part of the always-writable "authoring coop",
// so reconcile locked them whenever they happened to be git-tracked. The whole
// .linespec/ directory is exempted rather than individual filenames, so any
// file under it — including ones not yet named here — stays writable.
func TestAlwaysWritablePaths_IncludesManagedLinespecStateFiles(t *testing.T) {
	always := AlwaysWritablePaths(&ProvenanceConfig{Dir: "provenance"}, nil, "")
	for _, want := range []string{".linespec/embeddings.bin", ".linespec/hash_manifest.json", ".linespec/anything_else.json"} {
		if !IsAlwaysWritable(want, always) {
			t.Errorf("IsAlwaysWritable(%q) = false, want true (managed LineSpec state must never be locked)", want)
		}
	}
}

// TestReconcile_NeverLocksTrackedLinespecStateFiles reproduces the reported
// failure end to end: a repo that git-tracks .linespec/embeddings.bin and
// .linespec/hash_manifest.json (as this repo does) must come out of reconcile
// with both still writable, even though no open record's affected_scope covers
// them and they are not in provenance.exclude_paths. Before the fix these were
// locked read-only, and the next `provenance index` / `complete` failed with
// "permission denied" trying to regenerate them.
func TestReconcile_NeverLocksTrackedLinespecStateFiles(t *testing.T) {
	repo := t.TempDir()
	linespecDir := filepath.Join(repo, ".linespec")
	if err := os.MkdirAll(linespecDir, 0o755); err != nil {
		t.Fatal(err)
	}
	embeddingsPath := filepath.Join(linespecDir, "embeddings.bin")
	manifestPath := filepath.Join(linespecDir, "hash_manifest.json")
	writePerm(t, embeddingsPath, 0o644)
	writePerm(t, manifestPath, 0o644)

	// An unrelated open record whose scope does not cover .linespec/ at all —
	// mirrors the reported repro where these files fall outside every open
	// record's affected_scope.
	records := []*Record{
		{ID: "prov-2026-0001", Status: StatusOpen, AffectedScope: []string{"pkg/unrelated.go"}},
	}
	config := &ProvenanceConfig{Dir: "provenance"}

	result, err := Reconcile(repo, []string{".linespec/embeddings.bin", ".linespec/hash_manifest.json"}, records, config)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !isWritableOnDisk(t, embeddingsPath) {
		t.Errorf(".linespec/embeddings.bin must stay writable — it is LineSpec's own regenerated state, not governed source")
	}
	if !isWritableOnDisk(t, manifestPath) {
		t.Errorf(".linespec/hash_manifest.json must stay writable — it is LineSpec's own regenerated state, not governed source")
	}
	if slices.Contains(result.Locked, ".linespec/embeddings.bin") || slices.Contains(result.Locked, ".linespec/hash_manifest.json") {
		t.Errorf("reconcile must never report managed .linespec/ state as locked, got Locked=%v", result.Locked)
	}
}

// TestRefreshScopeIndexCache_WarnsOnSaveFailureInsteadOfSwallowingIt covers the
// secondary half of prov-2026-26efc162: when the scope-index cache write fails
// (e.g. its directory got locked, or is otherwise unwritable), the failure must
// be visible on stderr rather than silently discarded, which previously left a
// permanently stale cache with no signal to the user (refreshScopeIndexCache in
// pkg/provenance/scope_index.go).
func TestRefreshScopeIndexCache_WarnsOnSaveFailureInsteadOfSwallowingIt(t *testing.T) {
	repo := t.TempDir()
	provDir := filepath.Join(repo, "provenance")
	if err := os.MkdirAll(provDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(provDir, "prov-2026-00000001.yml"), []byte("id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Make .linespec/ itself unwritable so save() fails trying to MkdirAll into it.
	linespecDir := filepath.Join(repo, ".linespec")
	if err := os.MkdirAll(linespecDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(linespecDir, 0o755) })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStderr := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = origStderr })

	refreshScopeIndexCache(repo, []string{provDir}, []*Record{{ID: "prov-2026-00000001", Status: StatusDraft}})

	w.Close()
	os.Stderr = origStderr
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Warning") {
		t.Errorf("expected a warning on stderr when the scope index cache fails to save, got %q", buf.String())
	}
}

// --- WriteRestrictionEnabled / write_restriction config (prov-2026-c1515def) ----

func boolPtr(b bool) *bool { return &b }

func TestWriteRestrictionEnabled_DefaultsTrueOnNilConfigOrUnsetKey(t *testing.T) {
	if !WriteRestrictionEnabled(nil) {
		t.Errorf("expected enabled=true for a nil config")
	}
	if !WriteRestrictionEnabled(&ProvenanceConfig{}) {
		t.Errorf("expected enabled=true when write_restriction is unset, to preserve pre-existing behavior")
	}
}

func TestWriteRestrictionEnabled_HonorsExplicitValue(t *testing.T) {
	if !WriteRestrictionEnabled(&ProvenanceConfig{WriteRestriction: boolPtr(true)}) {
		t.Errorf("expected enabled=true when write_restriction: true")
	}
	if WriteRestrictionEnabled(&ProvenanceConfig{WriteRestriction: boolPtr(false)}) {
		t.Errorf("expected enabled=false when write_restriction: false")
	}
}

func TestReconcile_WriteRestrictionDisabled_LeavesUncoveredFileWritable(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	uncovered := filepath.Join(repo, "pkg", "uncovered.go")
	writePerm(t, uncovered, 0o644)

	records := []*Record{{ID: "prov-2026-0001", Status: StatusOpen, AffectedScope: []string{"pkg/covered.go"}}}
	config := &ProvenanceConfig{Dir: "provenance", WriteRestriction: boolPtr(false)}

	if _, err := Reconcile(repo, []string{"pkg/uncovered.go"}, records, config); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !isWritableOnDisk(t, uncovered) {
		t.Errorf("pkg/uncovered.go should remain writable — write_restriction: false must leave no restriction present")
	}
}

func TestReconcile_WriteRestrictionDisabled_UnlocksAlreadyLockedFile(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(repo, "pkg", "locked.go")
	writePerm(t, locked, 0o444)

	records := []*Record{{ID: "prov-2026-0001", Status: StatusOpen, AffectedScope: []string{"pkg/other.go"}}}
	config := &ProvenanceConfig{Dir: "provenance", WriteRestriction: boolPtr(false)}

	result, err := Reconcile(repo, []string{"pkg/locked.go"}, records, config)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !isWritableOnDisk(t, locked) {
		t.Errorf("a previously-locked file must be unlocked once write_restriction: false is set")
	}
	if len(result.Unlocked) != 1 || result.Unlocked[0] != "pkg/locked.go" {
		t.Errorf("result.Unlocked = %v, want [pkg/locked.go]", result.Unlocked)
	}
}

func TestReconcile_WriteRestrictionEnabled_StillLocksUncovered(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	uncovered := filepath.Join(repo, "pkg", "uncovered.go")
	writePerm(t, uncovered, 0o644)

	records := []*Record{{ID: "prov-2026-0001", Status: StatusOpen, AffectedScope: []string{"pkg/covered.go"}}}
	config := &ProvenanceConfig{Dir: "provenance", WriteRestriction: boolPtr(true)}

	if _, err := Reconcile(repo, []string{"pkg/uncovered.go"}, records, config); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if isWritableOnDisk(t, uncovered) {
		t.Errorf("write_restriction: true must keep restricting exactly as the default does")
	}
}
