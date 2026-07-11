package provenance

import "testing"

// next_test.go is the proof artifact for the Phase 2 advice engine
// (prov-2026-54aaf9cf). It asserts the correct primary action for each state
// branch the engine is responsible for.

func bp(id string, status Status, scope []string, specs ...AssociatedSpec) *Record {
	return &Record{ID: id, Type: RecordTypeBlueprint, Status: status, AffectedScope: scope, AssociatedSpecs: specs}
}

func imprint(id, implements string, status Status) *Record {
	return &Record{ID: id, Type: RecordTypeImprint, Status: status, Implements: implements}
}

func spec(path string) AssociatedSpec { return AssociatedSpec{Path: path, Type: "go_test"} }

// primary returns the engine's single primary recommendation for a state.
func primary(t *testing.T, s NextState) NextAction {
	t.Helper()
	actions := Advise(s)
	if len(actions) == 0 {
		t.Fatal("Advise returned no actions; the slice must never be empty")
	}
	return actions[0]
}

func TestAdvise_NoGoverningRecord_Create(t *testing.T) {
	s := NextState{
		Records:       []*Record{bp("prov-2026-00000001", StatusImplemented, []string{"other/**"})},
		IntendedFiles: []string{"pkg/new/thing.go"},
	}
	got := primary(t, s)
	if got.Kind != ActionCreate {
		t.Fatalf("want ActionCreate, got %s (%q)", got.Kind, got.Reason)
	}
}

func TestAdvise_GovernedBySealed_StillCreate_NoSupersede(t *testing.T) {
	// A sealed record governs the intended files. Engine must say create, and must
	// NOT recommend supersede (Phase 1 framing). The sealed record is surfaced as
	// advisory governing context only.
	sealed := bp("prov-2026-0000beef", StatusImplemented, []string{"pkg/core/**"})
	s := NextState{Records: []*Record{sealed}, IntendedFiles: []string{"pkg/core/auth.go"}}
	got := primary(t, s)
	if got.Kind != ActionCreate {
		t.Fatalf("want ActionCreate, got %s", got.Kind)
	}
	if len(got.Governing) != 1 || got.Governing[0] != sealed.ID {
		t.Fatalf("want sealed record surfaced as governing, got %v", got.Governing)
	}
	if containsAny(got.Reason, "supersede --", "--supersedes") {
		t.Fatalf("create advice must not push supersede as the remedy: %q", got.Reason)
	}
}

func TestAdvise_DraftGoverning_Open(t *testing.T) {
	draft := bp("prov-2026-0000d4af", StatusDraft, []string{"pkg/core/**"})
	s := NextState{Records: []*Record{draft}, IntendedFiles: []string{"pkg/core/auth.go"}}
	got := primary(t, s)
	if got.Kind != ActionOpen || got.RecordID != draft.ID {
		t.Fatalf("want ActionOpen on %s, got %s/%s", draft.ID, got.Kind, got.RecordID)
	}
}

func TestAdvise_OpenNoSpecs_AddSpec(t *testing.T) {
	open := bp("prov-2026-0000a11c", StatusOpen, []string{"pkg/core/**"})
	s := NextState{Records: []*Record{open}, IntendedFiles: []string{"pkg/core/auth.go"}}
	got := primary(t, s)
	if got.Kind != ActionAddSpec || got.RecordID != open.ID {
		t.Fatalf("want ActionAddSpec on %s, got %s/%s", open.ID, got.Kind, got.RecordID)
	}
}

func TestAdvise_OpenWithSpecsAndStaged_Commit(t *testing.T) {
	open := bp("prov-2026-0000c0de", StatusOpen, []string{"pkg/core/**"}, spec("pkg/core/auth_test.go"))
	s := NextState{
		Records:           []*Record{open},
		IntendedFiles:     []string{"pkg/core/auth.go"},
		StagedFiles:       []string{"pkg/core/auth.go"},
		CommitTagRequired: true,
	}
	got := primary(t, s)
	if got.Kind != ActionCommit || got.RecordID != open.ID {
		t.Fatalf("want ActionCommit on %s, got %s/%s", open.ID, got.Kind, got.RecordID)
	}
	if !containsAny(got.Command, open.ID) {
		t.Fatalf("commit command must embed the record tag: %q", got.Command)
	}
}

func TestAdvise_OpenWithUnimplementedImprint_ImplementImprint(t *testing.T) {
	open := bp("prov-2026-0000b1ue", StatusOpen, []string{"pkg/core/**"}, spec("pkg/core/auth_test.go"))
	imp := imprint("prov-2026-0000119r", open.ID, StatusOpen)
	s := NextState{Records: []*Record{open, imp}, IntendedFiles: []string{"pkg/core/auth.go"}}
	got := primary(t, s)
	if got.Kind != ActionImplementImprint || got.RecordID != imp.ID {
		t.Fatalf("want ActionImplementImprint on %s, got %s/%s", imp.ID, got.Kind, got.RecordID)
	}
}

func TestAdvise_OpenReadyAllImprintsImplemented_Complete(t *testing.T) {
	open := bp("prov-2026-0000d011", StatusOpen, []string{"pkg/core/**"}, spec("pkg/core/auth_test.go"))
	imp := imprint("prov-2026-0000face", open.ID, StatusImplemented)
	s := NextState{Records: []*Record{open, imp}, IntendedFiles: []string{"pkg/core/auth.go"}}
	got := primary(t, s)
	if got.Kind != ActionComplete || got.RecordID != open.ID {
		t.Fatalf("want ActionComplete on %s, got %s/%s", open.ID, got.Kind, got.RecordID)
	}
}

// --- fswrite (prov-2026-8d2f5f2a): ActionAddScope precedence and content ----

func TestAdvise_ActiveRecordPartialScope_AddScopeForUncoveredFiles(t *testing.T) {
	// active governs pkg/core/** (and is thus selected as active via one of the
	// intended files matching), but a second intended file falls outside its
	// affected_scope — fswrite enforcement leaves it non-writable, so the engine
	// must recommend widening scope rather than jump straight to add_spec.
	active := bp("prov-2026-0000fed0", StatusOpen, []string{"pkg/core/**"})
	s := NextState{
		Records:       []*Record{active},
		IntendedFiles: []string{"pkg/core/auth.go", "pkg/other/thing.go"},
	}
	got := primary(t, s)
	if got.Kind != ActionAddScope || got.RecordID != active.ID {
		t.Fatalf("want ActionAddScope on %s, got %s/%s", active.ID, got.Kind, got.RecordID)
	}
	if !containsAny(got.Reason, "pkg/other/thing.go") {
		t.Fatalf("reason should name the uncovered file: %q", got.Reason)
	}
	if containsAny(got.Reason, "pkg/core/auth.go") {
		t.Fatalf("reason should not list the already-covered file: %q", got.Reason)
	}
}

func TestAdvise_AddScopeTakesPrecedenceOverAddSpec(t *testing.T) {
	// active has no associated_specs (which would normally trigger ActionAddSpec)
	// AND an uncovered file. Widening scope must win: a spec file the agent
	// cannot yet write (fswrite enforcement) cannot be added first.
	active := bp("prov-2026-0000fed1", StatusOpen, []string{"pkg/core/**"}) // no specs
	s := NextState{
		Records:       []*Record{active},
		IntendedFiles: []string{"pkg/core/auth.go", "pkg/other/thing.go"},
	}
	got := primary(t, s)
	if got.Kind != ActionAddScope {
		t.Fatalf("want ActionAddScope to take precedence over ActionAddSpec, got %s", got.Kind)
	}
}

func TestAdvise_FullyCoveredFiles_NoAddScope_FallsThroughToAddSpec(t *testing.T) {
	// Regression: when every intended file is already in affected_scope, the new
	// addScope check must not fire, and existing behavior (fall through to
	// add_spec) must be preserved.
	active := bp("prov-2026-0000fed2", StatusOpen, []string{"pkg/core/**"})
	s := NextState{
		Records:       []*Record{active},
		IntendedFiles: []string{"pkg/core/auth.go", "pkg/core/other.go"},
	}
	got := primary(t, s)
	if got.Kind != ActionAddSpec || got.RecordID != active.ID {
		t.Fatalf("want ActionAddSpec (fully covered scope), got %s/%s", got.Kind, got.RecordID)
	}
}

func TestAdvise_AmbientNoFiles_NeverTriggersAddScope(t *testing.T) {
	// Ambient path (no intended/staged files): relevantFiles is empty, so
	// uncoveredFiles must be empty too, even for an observed-mode (empty scope)
	// active record. Regression guard for the ambient branch in Advise.
	open := bp("prov-2026-0000fed3", StatusOpen, nil, spec("pkg/core/auth_test.go")) // observed mode
	s := NextState{Records: []*Record{open}}
	got := primary(t, s)
	if got.Kind == ActionAddScope {
		t.Fatalf("ambient (no files) state must never produce ActionAddScope, got reason %q", got.Reason)
	}
	if got.Kind != ActionComplete {
		t.Fatalf("want ActionComplete for the ambient ready blueprint, got %s", got.Kind)
	}
}

func TestUncoveredFiles_AllowlistRecord_ReturnsOnlyFilesOutsideScope(t *testing.T) {
	active := bp("prov-2026-0000fed4", StatusOpen, []string{"pkg/core/**"})
	got := uncoveredFiles(active, []string{"pkg/core/a.go", "pkg/other/b.go", "pkg/core/c.go"})
	want := []string{"pkg/other/b.go"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("uncoveredFiles = %v, want %v", got, want)
	}
}

func TestUncoveredFiles_ObservedModeRecord_TreatsAllFilesAsUncovered(t *testing.T) {
	// An observed-mode record (empty affected_scope) never explicitly governs any
	// file (see explicitlyGoverns), so every file is reported as uncovered. In
	// practice Advise never calls this with a non-empty file list for an
	// observed-mode active record (it can only become active via the files==0
	// ambient path), but the helper's own contract must hold regardless.
	observed := bp("prov-2026-0000fed5", StatusOpen, nil)
	got := uncoveredFiles(observed, []string{"pkg/a.go", "pkg/b.go"})
	if len(got) != 2 {
		t.Fatalf("uncoveredFiles = %v, want both files reported uncovered", got)
	}
}

func TestUncoveredFiles_EmptyFileList_ReturnsNil(t *testing.T) {
	active := bp("prov-2026-0000fed6", StatusOpen, []string{"pkg/core/**"})
	if got := uncoveredFiles(active, nil); len(got) != 0 {
		t.Fatalf("uncoveredFiles(nil) = %v, want empty", got)
	}
}

func TestAddScopeAction_RecommendsLockScopeAndNamesRecord(t *testing.T) {
	r := bp("prov-2026-0000fed7", StatusOpen, []string{"pkg/core/**"})
	got := addScopeAction(r, []string{"pkg/other/a.go", "pkg/other/b.go"})
	if got.Kind != ActionAddScope {
		t.Fatalf("Kind = %s, want ActionAddScope", got.Kind)
	}
	if got.RecordID != r.ID {
		t.Fatalf("RecordID = %s, want %s", got.RecordID, r.ID)
	}
	for _, want := range []string{"pkg/other/a.go, pkg/other/b.go", "affected_scope", r.ID, "lock-scope --record " + r.ID} {
		if !containsAny(got.Reason, want) {
			t.Fatalf("addScopeAction reason missing %q: %q", want, got.Reason)
		}
	}
}

func TestAdvise_MultipleOpenGoverning_ChooseNotSupersede(t *testing.T) {
	a := bp("prov-2026-0000aaaa", StatusOpen, []string{"pkg/core/**"}, spec("a_test.go"))
	b := bp("prov-2026-0000bbbb", StatusOpen, []string{"pkg/core/**"}, spec("b_test.go"))
	s := NextState{Records: []*Record{a, b}, IntendedFiles: []string{"pkg/core/auth.go"}}
	got := primary(t, s)
	if got.Kind != ActionChooseRecord {
		t.Fatalf("want ActionChooseRecord, got %s", got.Kind)
	}
	if len(got.Governing) != 2 {
		t.Fatalf("want both open records listed, got %v", got.Governing)
	}
	if containsAny(got.Reason, "--supersedes") {
		t.Fatalf("choose advice must not recommend the supersede command: %q", got.Reason)
	}
}

func TestAdvise_ObservedModeOpenRecord_NotTreatedAsGoverning(t *testing.T) {
	// An open record with empty affected_scope (observed mode) permits any file but
	// claims none. It must NOT be selected as the active record for an arbitrary
	// file, nor listed as governing it — otherwise every observed record would
	// hijack advice for every file.
	observed := bp("prov-2026-0b5e2bed", StatusOpen, nil, spec("x_test.go")) // empty scope
	s := NextState{Records: []*Record{observed}, IntendedFiles: []string{"pkg/core/auth.go"}}
	got := primary(t, s)
	if got.Kind != ActionCreate {
		t.Fatalf("observed-mode open record must not become active; want ActionCreate, got %s", got.Kind)
	}
	if len(got.Governing) != 0 {
		t.Fatalf("observed-mode record must not appear as governing, got %v", got.Governing)
	}
}

func TestAdvise_CleanAmbient_None(t *testing.T) {
	s := NextState{Records: []*Record{bp("prov-2026-00000001", StatusImplemented, []string{"pkg/core/**"})}}
	got := primary(t, s)
	if got.Kind != ActionNone {
		t.Fatalf("want ActionNone on clean ambient state, got %s", got.Kind)
	}
}

func TestAdvise_AmbientSingleOpenBlueprint_AdvancesIt(t *testing.T) {
	open := bp("prov-2026-0000aaff", StatusOpen, []string{"pkg/core/**"}, spec("pkg/core/auth_test.go"))
	s := NextState{Records: []*Record{open}} // no intended/staged files
	got := primary(t, s)
	if got.Kind != ActionComplete || got.RecordID != open.ID {
		t.Fatalf("ambient with one open ready blueprint should advise complete, got %s/%s", got.Kind, got.RecordID)
	}
}

func TestHintCreateNotSupersede_FramesSupersedeAsRevisionOnly(t *testing.T) {
	h := HintCreateNotSupersede()
	if !containsAny(h, "create a new record") {
		t.Fatalf("hint must lead with creating a new record: %q", h)
	}
	if containsAny(h, "--supersedes") {
		t.Fatalf("hint must not present the --supersedes command as the default remedy: %q", h)
	}
	if !containsAny(h, "deliberately revising") {
		t.Fatalf("hint must frame supersede as the deliberate-revision branch: %q", h)
	}
}

func TestHintStaleScope_NonBlockingAndSpecific(t *testing.T) {
	h := HintStaleScope("pkg/core/auth.go", "prov-2026-0000beef", "abc1234")
	for _, want := range []string{"non-blocking", "no action is required", "pkg/core/auth.go", "prov-2026-0000beef"} {
		if !containsAny(h, want) {
			t.Fatalf("stale-scope hint missing %q: %s", want, h)
		}
	}
}

func TestAdvise_NeverEmpty(t *testing.T) {
	if len(Advise(NextState{})) == 0 {
		t.Fatal("Advise must never return an empty slice")
	}
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if n != "" && stringsContains(haystack, n) {
			return true
		}
	}
	return false
}

// stringsContains is a tiny local helper to avoid importing strings in the test
// file just for one call.
func stringsContains(s, sub string) bool {
	return len(sub) <= len(s) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
