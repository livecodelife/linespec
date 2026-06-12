package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// scope_index.go hosts the Phase 5a cached scope index (prov-2026-007c8893): a
// compact, persisted view of every record's governance fields plus a cheap
// stat-based fingerprint of the provenance directory. The `next` dispatch fast
// path serves from this cache on a fingerprint match, avoiding a full
// Loader.LoadAll(), so the command is fast enough to sit inside a per-edit Claude
// Code hook. On any fingerprint mismatch, malformed cache, or read error the
// caller falls back to the authoritative LoadAll path — the cache is an
// optimization, never a governance source of truth.

// ScopeEntry is the compact cached projection of a record, carrying only the
// fields the advice engine (Advise) reads. Heavy prose (intent, constraints,
// title) is intentionally omitted.
type ScopeEntry struct {
	ID             string     `json:"id"`
	Status         Status     `json:"status"`
	Type           RecordType `json:"type,omitempty"`
	AffectedScope  []string   `json:"affected_scope,omitempty"`
	ForbiddenScope []string   `json:"forbidden_scope,omitempty"`
	Implements     string     `json:"implements,omitempty"`
	HasSpecs       bool       `json:"has_specs"`
}

// ScopeIndex is the persisted scope cache: a fingerprint of the provenance-dir
// state plus the compact per-record entries.
type ScopeIndex struct {
	Fingerprint string       `json:"fingerprint"`
	Entries     []ScopeEntry `json:"entries"`
}

// scopeIndexPath returns the on-disk location of the scope cache, alongside the
// existing .linespec cache artifacts (schema-cache.json, hash_manifest.json).
func scopeIndexPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".linespec", "scope-index.json")
}

// scopeEntryFromRecord projects a full record to a compact cache entry.
func scopeEntryFromRecord(r *Record) ScopeEntry {
	return ScopeEntry{
		ID:             r.ID,
		Status:         r.Status,
		Type:           r.Type,
		AffectedScope:  r.AffectedScope,
		ForbiddenScope: r.ForbiddenScope,
		Implements:     r.Implements,
		HasSpecs:       len(r.AssociatedSpecs) > 0,
	}
}

// toRecord reconstructs a lightweight *Record from a cache entry, carrying exactly
// the fields Advise reads. AssociatedSpecs is synthesized to length 1 when HasSpecs
// because Advise only checks len(AssociatedSpecs), never the contents.
func (e ScopeEntry) toRecord() *Record {
	r := &Record{
		ID:             e.ID,
		Status:         e.Status,
		Type:           e.Type,
		AffectedScope:  e.AffectedScope,
		ForbiddenScope: e.ForbiddenScope,
		Implements:     e.Implements,
	}
	if e.HasSpecs {
		r.AssociatedSpecs = []AssociatedSpec{{}}
	}
	return r
}

// computeScopeFingerprint hashes the (path, size, modtime) of every *.yml across
// the given directories. It stats only — no file contents are read — so it is a
// cheap freshness check that changes whenever any record file is added, removed,
// or modified.
func computeScopeFingerprint(dirs []string) (string, error) {
	var tuples []string
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".yml") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			tuples = append(tuples, fmt.Sprintf("%s\x00%d\x00%d", path, info.Size(), info.ModTime().UnixNano()))
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	sort.Strings(tuples)
	h := sha256.New()
	for _, t := range tuples {
		h.Write([]byte(t))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// buildScopeIndex projects loaded records into a cache stamped with fingerprint.
func buildScopeIndex(records []*Record, fingerprint string) *ScopeIndex {
	entries := make([]ScopeEntry, 0, len(records))
	for _, r := range records {
		entries = append(entries, scopeEntryFromRecord(r))
	}
	return &ScopeIndex{Fingerprint: fingerprint, Entries: entries}
}

// save persists the scope index to path, creating the parent dir if needed.
func (idx *ScopeIndex) save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// loadScopeIndex reads a persisted scope index from path.
func loadScopeIndex(path string) (*ScopeIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var idx ScopeIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

// loadFreshScopeIndex loads the cached scope index and returns it only if its
// fingerprint still matches the current provenance-dir state. ok is false on any
// miss, staleness, or error, so the caller must fall back to LoadAll.
func loadFreshScopeIndex(repoRoot string, dirs []string) (*ScopeIndex, bool) {
	idx, err := loadScopeIndex(scopeIndexPath(repoRoot))
	if err != nil {
		return nil, false
	}
	fp, err := computeScopeFingerprint(dirs)
	if err != nil || fp == "" || fp != idx.Fingerprint {
		return nil, false
	}
	return idx, true
}

// refreshScopeIndexCache rewrites the scope cache from the freshly-loaded records,
// but only when the on-disk cache is missing or stale — so the common already-fresh
// case costs one stat-walk and no write. Best-effort: the cache is advisory, so all
// errors are ignored.
func refreshScopeIndexCache(repoRoot string, dirs []string, records []*Record) {
	if repoRoot == "" {
		return
	}
	fp, err := computeScopeFingerprint(dirs)
	if err != nil || fp == "" {
		return
	}
	path := scopeIndexPath(repoRoot)
	if existing, err := loadScopeIndex(path); err == nil && existing.Fingerprint == fp {
		return // already fresh
	}
	_ = buildScopeIndex(records, fp).save(path)
}

// loaderDirs returns the directories LoadAll reads: the (absolute) local provenance
// dir followed by any shared-repo cache dirs.
func loaderDirs(absDir string, sharedDirs []string) []string {
	return append([]string{absDir}, sharedDirs...)
}

// newCommandsFromScopeIndex builds a Commands whose Loader is populated from the
// cached scope entries WITHOUT a full LoadAll. It carries only the lightweight
// records the advice engine needs — enough to serve `next`, nothing more.
func newCommandsFromScopeIndex(config *ProvenanceConfig, repoRoot, absDir string, output *os.File, color bool, idx *ScopeIndex) *Commands {
	if config.Enforcement == "" {
		config.Enforcement = "warn"
	}
	loader := NewLoader(absDir, nil)
	for _, e := range idx.Entries {
		r := e.toRecord()
		loader.Records = append(loader.Records, r)
		loader.RecordsByID[r.ID] = r
	}
	return &Commands{
		Loader:    loader,
		Git:       NewGit(repoRoot),
		Formatter: NewFormatter(output, color),
		Config:    config,
		RepoRoot:  repoRoot,
	}
}

// TryNextFromCache serves `linespec provenance next` from the cached scope index
// without a full LoadAll, when the cache is fresh. It returns true if it handled
// the command (output already rendered); false if the caller must fall back to the
// heavy, authoritative path — on a custom -c config, a cache miss/staleness, or any
// error. This is the per-edit-hook fast path.
func TryNextFromCache(config *ProvenanceConfig, repoRoot string, output *os.File, color bool, opts NextOptions) bool {
	// A custom config path changes which records and directories apply; let the
	// heavy path reason about it rather than duplicate that logic here.
	if opts.ConfigFile != "" {
		return false
	}
	if config.Dir == "" {
		config.Dir = "provenance"
	}
	absDir := config.Dir
	if !filepath.IsAbs(absDir) && repoRoot != "" {
		absDir = filepath.Join(repoRoot, absDir)
	}
	sharedDirs := NewCacheManager(config.SharedRepos, config.CacheTTLMinutes).LoadedDirs()
	dirs := loaderDirs(absDir, sharedDirs)

	idx, ok := loadFreshScopeIndex(repoRoot, dirs)
	if !ok {
		return false
	}
	cmds := newCommandsFromScopeIndex(config, repoRoot, absDir, output, color, idx)
	if err := cmds.Next(opts); err != nil {
		return false
	}
	return true
}
