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
