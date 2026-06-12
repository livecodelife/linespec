package provenance

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// scope_index_test.go is the proof artifact for the Phase 5a cached scope index
// (prov-2026-007c8893): cached-vs-uncached `next` equivalence, stat-fingerprint
// invalidation, and fallback-to-LoadAll on a corrupt/missing cache.

func entry(id string, status Status, typ RecordType, scope []string, implements string, hasSpecs bool) ScopeEntry {
	return ScopeEntry{ID: id, Status: status, Type: typ, AffectedScope: scope, Implements: implements, HasSpecs: hasSpecs}
}

func TestScopeEntry_RoundTripDrivesAdviseLikeFullRecord(t *testing.T) {
	// A lightweight record reconstructed from a cache entry must produce the same
	// advice as the full record it was projected from.
	full := bp("prov-2026-0000a11c", StatusOpen, []string{"pkg/core/**"}) // open, no specs
	cached := scopeEntryFromRecord(full).toRecord()

	files := []string{"pkg/core/auth.go"}
	gotFull := Advise(NextState{Records: []*Record{full}, IntendedFiles: files})
	gotCached := Advise(NextState{Records: []*Record{cached}, IntendedFiles: files})
	if gotFull[0].Kind != gotCached[0].Kind {
		t.Fatalf("cached record advice %s != full record advice %s", gotCached[0].Kind, gotFull[0].Kind)
	}
	if gotCached[0].Kind != ActionAddSpec {
		t.Fatalf("expected ActionAddSpec for open+no-specs, got %s", gotCached[0].Kind)
	}
}

func TestScopeEntry_HasSpecsPreservedAcrossProjection(t *testing.T) {
	withSpecs := bp("prov-2026-0000c0de", StatusOpen, []string{"pkg/core/**"}, spec("pkg/core/auth_test.go"))
	e := scopeEntryFromRecord(withSpecs)
	if !e.HasSpecs {
		t.Fatal("HasSpecs must be true when the record has associated_specs")
	}
	if len(e.toRecord().AssociatedSpecs) == 0 {
		t.Fatal("reconstructed record must report having specs so Advise advances past add_spec")
	}
}

func TestComputeScopeFingerprint_ChangesWhenRecordChanges(t *testing.T) {
	dir := t.TempDir()
	writeYML := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeYML("prov-2026-00000001.yml", "id: prov-2026-00000001\n")
	fp1, err := computeScopeFingerprint([]string{dir})
	if err != nil || fp1 == "" {
		t.Fatalf("fingerprint: %v / %q", err, fp1)
	}
	// Adding a record changes the fingerprint.
	writeYML("prov-2026-00000002.yml", "id: prov-2026-00000002\n")
	fp2, _ := computeScopeFingerprint([]string{dir})
	if fp1 == fp2 {
		t.Fatal("fingerprint must change when a record file is added")
	}
	// A non-.yml file does not affect it.
	writeYML("notes.txt", "ignore me")
	fp3, _ := computeScopeFingerprint([]string{dir})
	if fp3 != fp2 {
		t.Fatal("non-.yml files must not affect the fingerprint")
	}
}

func TestLoadFreshScopeIndex_StaleAndCorruptFallBack(t *testing.T) {
	repo := t.TempDir()
	provDir := filepath.Join(repo, "provenance")
	if err := os.MkdirAll(provDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(provDir, "prov-2026-00000001.yml"), []byte("id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirs := []string{provDir}

	// No cache yet -> not fresh.
	if _, ok := loadFreshScopeIndex(repo, dirs); ok {
		t.Fatal("expected miss when no cache file exists")
	}

	// Write a fresh cache -> hit.
	fp, _ := computeScopeFingerprint(dirs)
	idx := &ScopeIndex{Fingerprint: fp, Entries: []ScopeEntry{entry("prov-2026-00000001", StatusOpen, RecordTypeBlueprint, nil, "", false)}}
	if err := idx.save(scopeIndexPath(repo)); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadFreshScopeIndex(repo, dirs); !ok {
		t.Fatal("expected a fresh cache to be served")
	}

	// Change a record -> fingerprint mismatch -> not fresh.
	if err := os.WriteFile(filepath.Join(provDir, "prov-2026-00000002.yml"), []byte("id: y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadFreshScopeIndex(repo, dirs); ok {
		t.Fatal("expected staleness after a record change")
	}

	// Corrupt cache -> not fresh (fall back), never a panic.
	if err := os.WriteFile(scopeIndexPath(repo), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadFreshScopeIndex(repo, dirs); ok {
		t.Fatal("expected a corrupt cache to be treated as a miss")
	}
}

func TestActiveGoverningRecords_ExcludesSupersededAndDeprecated(t *testing.T) {
	scope := []string{"pkg/core/**"}
	open := bp("prov-2026-0000aaaa", StatusOpen, scope)
	impl := bp("prov-2026-0000bbbb", StatusImplemented, scope)
	superseded := bp("prov-2026-0000cccc", StatusSuperseded, scope)
	deprecated := bp("prov-2026-0000dddd", StatusDeprecated, scope)
	draft := bp("prov-2026-0000eeee", StatusDraft, scope)

	got := activeGoverningRecords([]*Record{open, impl, superseded, deprecated, draft}, []string{"pkg/core/auth.go"})

	gotIDs := map[string]bool{}
	for _, r := range got {
		gotIDs[r.ID] = true
	}
	if !gotIDs[open.ID] || !gotIDs[impl.ID] {
		t.Fatalf("active lookup must include open + implemented, got %v", gotIDs)
	}
	if gotIDs[superseded.ID] || gotIDs[deprecated.ID] {
		t.Fatalf("active lookup must EXCLUDE superseded/deprecated, got %v", gotIDs)
	}
	if gotIDs[draft.ID] {
		t.Fatalf("active lookup must exclude draft (not yet enforcing), got %v", gotIDs)
	}
}

func TestActiveGoverningRecords_ObservedModeNotGoverning(t *testing.T) {
	// An open observed-mode record (empty scope) permits any file but claims none —
	// it must not appear as governing a specific file.
	observed := bp("prov-2026-0000ffff", StatusImplemented, nil)
	got := activeGoverningRecords([]*Record{observed}, []string{"pkg/core/auth.go"})
	if len(got) != 0 {
		t.Fatalf("observed-mode record must not be reported as governing, got %v", got)
	}
}

func TestTryNextFromCache_MatchesHeavyPathOutput(t *testing.T) {
	cmds, repo, _ := newTransitionTestRepo(t, false)

	// Two records governing different files, in different lifecycle states.
	writeRecord(t, repo, "prov-2026-aa000001",
		customRecord("prov-2026-aa000001", "open", "", []string{"pkg/foo.go"}, nil))
	writeRecord(t, repo, "prov-2026-aa000002",
		customRecord("prov-2026-aa000002", "draft", "", []string{"pkg/bar.go"}, nil))
	reloadRecords(t, cmds)

	// Heavy path output for `next --files pkg/bar.go --json`.
	opts := NextOptions{Files: []string{"pkg/bar.go"}, Format: "json"}
	var heavy bytes.Buffer
	cmds.Formatter = NewFormatter(&heavy, false)
	if err := cmds.Next(opts); err != nil {
		t.Fatalf("heavy next: %v", err)
	}

	// Build + persist the cache from the loaded records, then serve via the fast path.
	provDir := filepath.Join(repo, "provenance")
	fp, _ := computeScopeFingerprint([]string{provDir})
	if err := buildScopeIndex(cmds.Loader.Records, fp).save(scopeIndexPath(repo)); err != nil {
		t.Fatal(err)
	}
	idx, ok := loadFreshScopeIndex(repo, []string{provDir})
	if !ok {
		t.Fatal("cache should be fresh immediately after building it")
	}
	light := newCommandsFromScopeIndex(cmds.Config, repo, provDir, nil, false, idx)
	var fast bytes.Buffer
	light.Formatter = NewFormatter(&fast, false)
	if err := light.Next(opts); err != nil {
		t.Fatalf("fast next: %v", err)
	}

	if heavy.String() != fast.String() {
		t.Fatalf("cached next output differs from uncached:\n--- heavy ---\n%s\n--- fast ---\n%s", heavy.String(), fast.String())
	}
}
