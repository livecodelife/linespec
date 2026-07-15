package provenance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// fswrite.go implements the filesystem write-permission projection described by
// blueprint prov-2026-8d2f5f2a: the governed tree defaults to non-writable, and a
// source path becomes writable only when it is covered by the affected_scope of
// an open, allowlist-mode record. Writability is a pure projection of the current
// set of open records' scopes — it is re-derived, never separately tracked state.
//
// Two side effects are exposed:
//   - MaterializeScope unlocks exactly the paths a single transition (open, or
//     widening an open record's scope) newly declares, so declaration and
//     permission move together atomically.
//   - Reconcile re-derives the entire write-bit state for a candidate file list
//     from current record state, unlocking covered paths and re-locking
//     uncovered ones. It is idempotent and never invents candidate paths — the
//     caller supplies them (e.g. from `git ls-files`), so reconcile never
//     reaches into VCS internals or untracked build output.

const (
	// filePermLockMask strips write permission from every class (owner/group/
	// other) when locking a file, leaving other bits (e.g. execute) untouched.
	filePermLockMask os.FileMode = 0o222
	// filePermUnlockAdd adds owner write permission when unlocking a file.
	filePermUnlockAdd os.FileMode = 0o200
	// dirPermUnlockAdd adds owner write+execute permission when unlocking a
	// directory so a declared-but-not-yet-existing file can be created inside
	// it. It never touches any file already inside the directory.
	dirPermUnlockAdd os.FileMode = 0o300
)

// managedStatePaths returns the directory of LineSpec's own machine-regenerated
// state under .linespec/ (e.g. embeddings.bin, hash_manifest.json, and any
// future managed file placed there) that must never be locked by the
// write-permission projection: it is not human-authored source the
// provenance-before-code invariant is meant to gate, and reconcile locking it
// breaks the very tools (embeddings index, hash manifest) that regenerate it
// (prov-2026-26efc162). The whole directory is exempted, rather than naming
// individual files, so newly added managed state never needs a matching
// AlwaysWritablePaths update.
func managedStatePaths() []string {
	return []string{
		".linespec",
	}
}

// AlwaysWritablePaths returns the derived always-writable pattern set: the
// existing provenance.exclude_paths whitelist plus the authoring coop — the
// configured provenance directory, .linespec.yml, LineSpec's own managed
// .linespec/ state directory (managedStatePaths), and every associated_spec path
// across all loaded records (regardless of status), so the
// Brief-to-Blueprint-to-Imprint authoring chain can never block its own
// creation. This set is derived, never separately hand-maintained. repoRoot
// normalizes an absolute config.Dir to repo-relative, since every path this
// pattern set is matched against (from `git ls-files`) is repo-relative too.
func AlwaysWritablePaths(config *ProvenanceConfig, records []*Record, repoRoot string) []string {
	var out []string
	dir := "provenance"
	if config != nil {
		out = append(out, config.ExcludePaths...)
		if config.Dir != "" {
			dir = config.Dir
		}
	}
	if repoRoot != "" && filepath.IsAbs(dir) {
		if rel, err := filepath.Rel(repoRoot, dir); err == nil && !strings.HasPrefix(rel, "..") {
			dir = rel
		}
	}
	out = append(out, dir, ".linespec.yml")
	out = append(out, managedStatePaths()...)

	seen := map[string]bool{}
	for _, r := range records {
		for _, spec := range r.AssociatedSpecs {
			if spec.Path == "" || seen[spec.Path] {
				continue
			}
			seen[spec.Path] = true
			out = append(out, spec.Path)
		}
	}
	return out
}

// IsAlwaysWritable reports whether path matches the always-writable pattern set.
// It reuses exclude.go's pattern matching (regex/glob/directory-prefix/exact) so
// the always-writable set behaves identically to provenance.exclude_paths.
func IsAlwaysWritable(path string, alwaysWritable []string) bool {
	return IsPathExcluded(path, alwaysWritable)
}

// OpenAllowlistScope returns the de-duplicated, sorted union of affected_scope
// patterns across every open, allowlist-mode record — the set of source-path
// patterns writability projects from. Non-open records and observed-mode
// records (empty affected_scope) contribute nothing: writability is a pure
// projection of open allowlist scope only.
func OpenAllowlistScope(records []*Record) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range records {
		if r.Status != StatusOpen || r.ScopeMode() != "allowlist" {
			continue
		}
		for _, p := range r.AffectedScope {
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// IsWritablePath reports whether path should be writable under the current
// projection: always-writable, or matching some open allowlist record's
// affected_scope pattern. An invalid pattern in a record cannot block
// enforcement of other, valid patterns, so it is skipped rather than erroring.
func IsWritablePath(path string, alwaysWritable, openScope []string) (bool, error) {
	if IsAlwaysWritable(path, alwaysWritable) {
		return true, nil
	}
	for _, pattern := range openScope {
		matched, err := MatchPattern(path, pattern)
		if err != nil {
			continue
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

// containsGlobMeta reports whether s contains glob metacharacters, meaning it is
// a pattern rather than a concrete path that can be chmod'd directly.
func containsGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?")
}

// patternParentDir computes the directory that must be unlocked so a
// declared-but-not-yet-existing glob or regex affected_scope pattern's file
// can be created inside it (prov-2026-b4006eda): the literal path segment
// before the pattern's first wildcard/regex metacharacter. For a "re:"
// pattern, the leading "^" anchor (if present) is stripped first, since scope
// regexes are conventionally anchored literal paths with only a suffix (e.g.
// the extension) expressed as a regex. Returns "." when no literal directory
// segment can be determined (e.g. the pattern's first path segment is itself
// a wildcard), matching Reconcile's existing skip of "." as already-writable.
func patternParentDir(pattern string) string {
	body := strings.TrimPrefix(pattern, "re:")
	body = strings.TrimPrefix(body, "^")

	metaIdx := strings.IndexAny(body, "*?.+()[]{}|\\^$")
	if metaIdx == -1 {
		return filepath.Dir(body)
	}

	prefix := body[:metaIdx]
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		return prefix[:i]
	}
	return "."
}

// LockFile strips write permission from every permission class of the file at
// path, leaving other bits (e.g. execute) untouched. Missing paths and
// directories are a no-op — reconcile only ever locks files it actually finds.
func LockFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return nil
	}
	return os.Chmod(path, info.Mode().Perm()&^filePermLockMask)
}

// UnlockFile adds owner write permission to the file at path, leaving other
// bits untouched. Missing paths and directories are a no-op.
func UnlockFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return nil
	}
	return os.Chmod(path, info.Mode().Perm()|filePermUnlockAdd)
}

// UnlockDir adds owner write+execute permission to the directory at path so a
// declared-but-not-yet-existing file can be created inside it. It does NOT
// alter the permission bits of any file already present in the directory —
// already-locked siblings remain locked. Missing paths and non-directories are
// a no-op.
func UnlockDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}
	return os.Chmod(path, info.Mode().Perm()|dirPermUnlockAdd)
}

// materializeTargets resolves each repo-relative scope path to the absolute
// filesystem location a materialize pass will chmod: the file itself when it
// already exists, otherwise its parent directory (so the not-yet-existing file
// can be created). Glob/regex patterns are skipped — they are not concrete
// paths to chmod; reconcile (which walks real files) is what makes a pattern's
// matches writable.
func materializeTargets(repoRoot string, scopePaths []string) []string {
	var out []string
	for _, rel := range scopePaths {
		if rel == "" || containsGlobMeta(rel) {
			continue
		}
		abs := filepath.Join(repoRoot, rel)
		if _, err := os.Lstat(abs); err != nil {
			out = append(out, filepath.Dir(abs))
			continue
		}
		out = append(out, abs)
	}
	return out
}

// MaterializeScope unlocks exactly the given repo-relative scope paths: an
// existing file is unlocked directly; a not-yet-existing declared path has its
// parent directory unlocked instead, so the file can be created without
// disturbing sibling files' own permission bits. This is the atomic permission
// side effect of `open` and of widening an already-open record's scope — call
// it with exactly the newly-covered paths so declaration and permission move
// together.
func MaterializeScope(repoRoot string, scopePaths []string) error {
	for _, rel := range scopePaths {
		if rel == "" || containsGlobMeta(rel) {
			continue
		}
		abs := filepath.Join(repoRoot, rel)
		if _, err := os.Lstat(abs); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
			if err := UnlockDir(filepath.Dir(abs)); err != nil {
				return err
			}
			continue
		}
		if err := UnlockFile(abs); err != nil {
			return err
		}
	}
	return nil
}

// permSnapshot captures a path's permission mode before a transition changes
// it, mirroring fileSnapshot's role for file contents — restorePerms can put
// the bits back exactly as they were if a later step (lint, commit) rejects the
// transition, so a rejected transition leaves permissions untouched.
type permSnapshot struct {
	path    string
	mode    os.FileMode
	existed bool
}

// snapshotPerms records the current permission mode of each path.
func snapshotPerms(paths ...string) []permSnapshot {
	snaps := make([]permSnapshot, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		info, err := os.Lstat(p)
		if err != nil {
			snaps = append(snaps, permSnapshot{path: p, existed: false})
			continue
		}
		snaps = append(snaps, permSnapshot{path: p, mode: info.Mode().Perm(), existed: true})
	}
	return snaps
}

// restorePerms puts every snapshotted path's permission bits back to their
// captured state. Paths that did not exist at snapshot time are left alone —
// MaterializeScope never creates files, only unlocks directories, so there is
// nothing to remove on rollback.
func restorePerms(snaps []permSnapshot) {
	for _, s := range snaps {
		if !s.existed {
			continue
		}
		if err := os.Chmod(s.path, s.mode); err != nil && !os.IsNotExist(err) {
			fmtWarnf("failed to restore permissions on %s during rollback: %v", s.path, err)
		}
	}
}

// ColdStartSkip reports whether reconcile should skip enforcement entirely
// because the project has no provenance records yet. Hand-initialized projects
// default to warn-until-first-record (skip=true) so the tree is never locked
// down before there is anything to declare scope against. Projects created via
// `linespec clone` default to strict-deny (skip=false): ManifestURL being set
// means the project arrived pre-seeded with open records to enforce against.
func ColdStartSkip(records []*Record, config *ProvenanceConfig) bool {
	if config != nil && config.ManifestURL != "" {
		return false
	}
	return len(records) == 0
}

// ReconcileResult summarizes a reconcile pass: the repo-relative paths whose
// write bit was flipped in either direction.
type ReconcileResult struct {
	Unlocked []string
	Locked   []string
}

// Reconcile re-derives the entire write-bit state of files from the current set
// of open, allowlist-mode records' scopes. It is idempotent — a path already in
// the correct state is left untouched — and it never invents candidate paths:
// it walks only the caller-supplied files (e.g. a git-tracked file list), so it
// never reaches into VCS internals or untracked build output. Cold-start
// projects with no records yet are skipped entirely (see ColdStartSkip).
func Reconcile(repoRoot string, files []string, records []*Record, config *ProvenanceConfig) (*ReconcileResult, error) {
	result := &ReconcileResult{}
	if ColdStartSkip(records, config) {
		return result, nil
	}

	always := AlwaysWritablePaths(config, records, repoRoot)
	openScope := OpenAllowlistScope(records)

	writableDirs := map[string]bool{}
	for _, rel := range files {
		if rel == "" {
			continue
		}
		writable, err := IsWritablePath(rel, always, openScope)
		if err != nil {
			return result, err
		}
		if writable {
			writableDirs[filepath.Dir(rel)] = true
		}

		abs := filepath.Join(repoRoot, rel)
		info, err := os.Lstat(abs)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return result, err
		}
		if info.IsDir() {
			continue
		}

		curWritable := info.Mode().Perm()&filePermUnlockAdd != 0
		switch {
		case writable && !curWritable:
			if err := UnlockFile(abs); err != nil {
				return result, err
			}
			result.Unlocked = append(result.Unlocked, rel)
		case !writable && curWritable:
			if err := LockFile(abs); err != nil {
				return result, err
			}
			result.Locked = append(result.Locked, rel)
		}
	}

	// Unlock parent directories of declared-but-not-yet-existing open-scope
	// paths so they can be created, without altering any sibling file's bits.
	// Glob and regex patterns are declarations too (prov-2026-b4006eda) — they
	// have no single file to Lstat, so their parent directory is derived from
	// the pattern's literal prefix (patternParentDir) unconditionally, rather
	// than gated on an existence check the way an exact path is.
	for _, r := range records {
		if r.Status != StatusOpen || r.ScopeMode() != "allowlist" {
			continue
		}
		for _, p := range r.AffectedScope {
			if p == "" {
				continue
			}
			if containsGlobMeta(p) || strings.HasPrefix(p, "re:") {
				writableDirs[patternParentDir(p)] = true
				continue
			}
			if _, err := os.Lstat(filepath.Join(repoRoot, p)); os.IsNotExist(err) {
				writableDirs[filepath.Dir(p)] = true
			}
		}
	}
	for d := range writableDirs {
		if d == "." || d == "" {
			continue
		}
		if err := UnlockDir(filepath.Join(repoRoot, d)); err != nil {
			return result, err
		}
	}

	sort.Strings(result.Unlocked)
	sort.Strings(result.Locked)
	return result, nil
}

// listTrackedFiles returns the git-tracked files under repoRoot (repo-relative
// paths), the candidate set reconcile walks. Deliberately independent of
// pkg/provenance/git.go's Git helper (out of this blueprint's affected_scope):
// this is the one place reconcile needs a file list, and shelling out directly
// keeps the change contained to fswrite.go.
func listTrackedFiles(repoRoot string) ([]string, error) {
	cmd := exec.Command("git", "ls-files")
	if repoRoot != "" {
		cmd.Dir = repoRoot
	}
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for line := range strings.SplitSeq(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// fmtWarnf writes a warning to stderr, matching the "Warning: ..." convention
// used throughout commands.go's best-effort rollback paths.
func fmtWarnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Warning: "+format+"\n", args...)
}
