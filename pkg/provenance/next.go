package provenance

import (
	"fmt"
	"sort"
	"strings"
)

// next.go hosts the Phase 2 advice engine (prov-2026-54aaf9cf): a single pure
// function that maps the current provenance state — records, statuses, staged
// files, branch diff, and optional intended files — to the correct next action,
// with concrete record IDs filled in. Every guidance surface (the `next`
// command, CLI error hints, the skill, and the future plugin) renders from this
// one engine so guidance only has to be correct in one place.
//
// Design is recorded in imprint prov-2026-a053a867.

// ActionKind enumerates the kinds of next action the advice engine can recommend.
type ActionKind string

const (
	// ActionCreate — no record governs the relevant files; create a blueprint.
	ActionCreate ActionKind = "create"
	// ActionOpen — a draft record governs the work; open it to enable enforcement.
	ActionOpen ActionKind = "open"
	// ActionAddSpec — an open record has no associated_specs; add a proof artifact.
	ActionAddSpec ActionKind = "add_spec"
	// ActionAddScope — a file is blocked by fswrite enforcement (prov-2026-8d2f5f2a):
	// the active open record does not yet declare it in affected_scope, so it stays
	// non-writable on disk.
	ActionAddScope ActionKind = "add_scope"
	// ActionCommit — there are staged changes; commit them tagged with the record.
	ActionCommit ActionKind = "commit"
	// ActionImplementImprint — an imprint of the active blueprint is not implemented.
	ActionImplementImprint ActionKind = "implement_imprint"
	// ActionComplete — the active record is ready to be completed.
	ActionComplete ActionKind = "complete"
	// ActionChooseRecord — more than one open record governs the files; pick one.
	ActionChooseRecord ActionKind = "choose_record"
	// ActionNone — nothing pending; the tree is clean and no work is in flight.
	ActionNone ActionKind = "none"
)

// NextAction is a single recommended action: a ready-to-run command (with record
// IDs already substituted) plus a one-line human reason.
type NextAction struct {
	Kind     ActionKind `json:"kind"`
	Command  string     `json:"command,omitempty"`
	Reason   string     `json:"reason"`
	RecordID string     `json:"record_id,omitempty"`
	// Governing lists record IDs that govern the relevant files. It is advisory:
	// these records do NOT need to be superseded just because they govern the
	// files (Phase 1 framing).
	Governing []string `json:"governing,omitempty"`
	// Detail carries an optional multi-line elaboration (e.g. an associated_specs
	// YAML shape). Renderers may show or omit it.
	Detail string `json:"detail,omitempty"`
}

// NextState is the already-gathered, side-effect-free snapshot the engine reasons
// over. The caller (the `next` command, an error-hint site, or a hook) gathers
// this; Advise itself performs no I/O. This is what makes the decision logic
// unit-testable and reusable from every surface.
type NextState struct {
	// Records is every loaded record (local + shared cache).
	Records []*Record
	// StagedFiles is `git diff --cached --name-only`.
	StagedFiles []string
	// ChangedFiles is the broader working/branch diff (unstaged + committed-on-branch).
	ChangedFiles []string
	// IntendedFiles is the set of files the agent plans to change (from
	// `next --files`/`--plan`). When non-empty it takes precedence over the git
	// diffs so governance surfaces during planning, before any edit.
	IntendedFiles []string
	// CommitTagRequired mirrors config.commit_tag_required; affects commit wording.
	CommitTagRequired bool
}

// HintCreateNotSupersede returns the canonical guidance shown whenever a change
// touches files already governed by existing records: create ONE new record
// covering your files and tag your commits with it — supersede only when you are
// deliberately revising the decision a record captured. This is the single source
// of that wording. The `next` create advice, the commit-violation hint
// (formatter.go), and any other surface all render from it so the framing that
// caused the original ~$400 incident can never drift between surfaces again.
func HintCreateNotSupersede() string {
	return "create a new record that governs this file and tag your commit with it. " +
		"Supersede an existing record only when you are deliberately revising the decision it captured."
}

// HintStaleScope returns the canonical, non-blocking message shown when a change
// touches a file governed by an already-implemented (sealed) record. It is
// informational only — no action is required, and it is NOT a reason to supersede
// anything. The wording lives here so the commit-time warning and any other
// surface stay single-sourced.
func HintStaleScope(file, recordID, shortSHA string) string {
	return fmt.Sprintf(
		"Note: '%s' is governed by implemented record %s (sealed at %s). "+
			"This is informational and non-blocking — no action is required. "+
			"Make your change under your own new record and tag your commit with it. "+
			"Supersede %s only if you are intentionally revising that record's decision:\n"+
			"  linespec provenance create --title \"Your change description\" --supersedes %s",
		file, recordID, shortSHA, recordID, recordID)
}

// Advise is THE advice engine: a pure function mapping provenance state to the
// ordered list of correct next actions, with record IDs filled in. It performs
// no I/O and has no side effects. Element 0 of the result is the single primary
// recommendation; later elements are optional follow-ups. The slice is never
// empty — a clean, idle state returns a single ActionNone.
func Advise(state NextState) []NextAction {
	files := relevantFiles(state)

	open := recordsByStatus(state.Records, StatusOpen)
	openGoverning := topLevelGoverning(open, files)

	// Resolve the active open record (the one the agent is working under).
	var active *Record
	switch {
	case len(openGoverning) == 1:
		active = openGoverning[0]
	case len(openGoverning) > 1:
		return []NextAction{chooseRecordAction(openGoverning, files)}
	case len(files) == 0:
		// Ambient, clean tree: a lone open blueprint/bug is the active work.
		bps := topLevelRecords(open)
		switch len(bps) {
		case 1:
			active = bps[0]
		default:
			if len(bps) > 1 {
				return []NextAction{chooseRecordAction(bps, nil)}
			}
		}
	}

	if active != nil {
		return adviseOnActive(state, active)
	}

	// No active open record. Is there a draft to open?
	drafts := recordsByStatus(state.Records, StatusDraft)
	draftGoverning := topLevelGoverning(drafts, files)
	var draft *Record
	switch {
	case len(draftGoverning) >= 1:
		draft = draftGoverning[0]
	case len(files) == 0 && len(topLevelRecords(drafts)) == 1:
		draft = topLevelRecords(drafts)[0]
	}
	if draft != nil {
		return []NextAction{openAction(draft)}
	}

	// Nothing governs the files and there is no draft.
	if len(files) == 0 {
		return []NextAction{{
			Kind:    ActionNone,
			Reason:  "No work in flight and no staged changes. Start a blueprint when you begin a change.",
			Command: `linespec provenance create --title "..." --type blueprint --no-edit`,
		}}
	}
	return []NextAction{createAction(governingRecords(state.Records, files))}
}

// adviseOnActive returns the next action for an open record the agent is working
// under. Precedence: widen scope for any blocked file -> add missing spec ->
// commit staged work -> close pending imprints -> complete. Widening scope comes
// first because a file fswrite is blocking (prov-2026-8d2f5f2a) cannot even be
// written to create the missing spec.
func adviseOnActive(state NextState, active *Record) []NextAction {
	if missing := uncoveredFiles(active, relevantFiles(state)); len(missing) > 0 {
		return []NextAction{addScopeAction(active, missing)}
	}
	if len(active.AssociatedSpecs) == 0 {
		return []NextAction{addSpecAction(active)}
	}
	if len(state.StagedFiles) > 0 {
		return []NextAction{commitAction(active, state.CommitTagRequired)}
	}
	if pending := unimplementedImprints(state.Records, active.ID); len(pending) > 0 {
		return []NextAction{implementImprintAction(pending[0], active)}
	}
	return []NextAction{completeAction(active)}
}

// --- action constructors -------------------------------------------------

func createAction(governing []string) NextAction {
	reason := "No record governs these files — create one blueprint covering them."
	if len(governing) > 0 {
		reason = fmt.Sprintf(
			"%d record(s) already govern these files, but you do NOT need to supersede them — %s",
			len(governing), HintCreateNotSupersede())
	}
	return NextAction{
		Kind:      ActionCreate,
		Command:   `linespec provenance create --title "..." --type blueprint --no-edit`,
		Reason:    reason,
		Governing: governing,
	}
}

func openAction(r *Record) NextAction {
	return NextAction{
		Kind:     ActionOpen,
		Command:  "linespec provenance open --record " + r.ID,
		Reason:   "Draft " + r.ID + " covers this work — open it to enable scope/spec enforcement.",
		RecordID: r.ID,
	}
}

// addScopeAction recommends widening active's affected_scope to cover missing —
// files that stay non-writable on disk until they are declared (fswrite
// enforcement, prov-2026-8d2f5f2a). affected_scope itself lives in the always-
// writable provenance directory, so it can be hand-edited directly; `lock-scope`
// materializes permission for already-committed files in one atomic step.
func addScopeAction(r *Record, missing []string) NextAction {
	return NextAction{
		Kind: ActionAddScope,
		Reason: fmt.Sprintf(
			"Blocked by filesystem enforcement: %s not yet in open record %s's affected_scope, so they stay "+
				"read-only on disk. Add them to affected_scope in %s's YAML (the provenance directory is always "+
				"writable), then run 'linespec provenance lock-scope --record %s' to materialize write access "+
				"for already-committed files, or wait for the next reconcile at session start.",
			strings.Join(missing, ", "), r.ID, r.ID, r.ID),
		RecordID: r.ID,
	}
}

func addSpecAction(r *Record) NextAction {
	detail := strings.Join([]string{
		"# 1. create the proof file first (it must exist before you reference it)",
		"# 2. add it to the record's associated_specs:",
		"associated_specs:",
		"  - path: path/to/proof_test.go",
		"    type: go_test",
		"    run_command: make test # {{path}}",
	}, "\n")
	return NextAction{
		Kind:     ActionAddSpec,
		Reason:   "Open record " + r.ID + " has no associated_specs — add a proof artifact before completing.",
		RecordID: r.ID,
		Detail:   detail,
	}
}

func commitAction(r *Record, tagRequired bool) NextAction {
	cmd := fmt.Sprintf(`git commit -m "<description> [%s]"`, r.ID)
	reason := "You have staged changes — commit them tagged with " + r.ID + "."
	if !tagRequired {
		reason += " (commit tag is optional in this repo, but recommended.)"
	}
	return NextAction{
		Kind:     ActionCommit,
		Command:  cmd,
		Reason:   reason,
		RecordID: r.ID,
	}
}

func implementImprintAction(imprint, blueprint *Record) NextAction {
	return NextAction{
		Kind:     ActionImplementImprint,
		Command:  "linespec provenance complete --record " + imprint.ID,
		Reason:   "Imprint " + imprint.ID + " of blueprint " + blueprint.ID + " is not implemented yet — finish and complete it before completing the blueprint.",
		RecordID: imprint.ID,
	}
}

func completeAction(r *Record) NextAction {
	return NextAction{
		Kind:     ActionComplete,
		Command:  "linespec provenance complete --record " + r.ID,
		Reason:   "Specs are present and no imprints are pending — show proof, then complete " + r.ID + ".",
		RecordID: r.ID,
	}
}

func chooseRecordAction(records []*Record, files []string) NextAction {
	ids := recordIDs(records)
	scope := "the files in play"
	if len(files) > 0 {
		scope = strings.Join(files, ", ")
	}
	return NextAction{
		Kind: ActionChooseRecord,
		Reason: fmt.Sprintf(
			"Multiple open records govern %s: %s. Work under ONE of them and tag your commits with it — do NOT supersede the others.",
			scope, strings.Join(ids, ", ")),
		Governing: ids,
	}
}

// --- pure helpers --------------------------------------------------------

// relevantFiles returns IntendedFiles when set, otherwise the de-duplicated union
// of staged and changed files.
func relevantFiles(state NextState) []string {
	if len(state.IntendedFiles) > 0 {
		return dedupe(state.IntendedFiles)
	}
	return dedupe(append(append([]string{}, state.StagedFiles...), state.ChangedFiles...))
}

// isTopLevel reports whether a record is a workable top-level record (blueprint or
// bug). Empty Type defaults to blueprint for backward compatibility.
func isTopLevel(r *Record) bool {
	return r.Type == RecordTypeBlueprint || r.Type == RecordTypeBug || r.Type == ""
}

func recordsByStatus(records []*Record, status Status) []*Record {
	var out []*Record
	for _, r := range records {
		if r.Status == status {
			out = append(out, r)
		}
	}
	return out
}

func topLevelRecords(records []*Record) []*Record {
	var out []*Record
	for _, r := range records {
		if isTopLevel(r) {
			out = append(out, r)
		}
	}
	return out
}

// explicitlyGoverns reports whether a record CLAIMS the given file — i.e. it is in
// allowlist mode (non-empty affected_scope) and the file matches that scope.
// Observed-mode records (empty affected_scope) permit any file but do not claim
// any specific one, so they are NOT treated as governing a particular file. This
// keeps the engine from selecting a permissive observed record as the active one
// for every file, and keeps the advisory governing list free of noise.
func explicitlyGoverns(r *Record, file string) bool {
	if r.ScopeMode() != "allowlist" {
		return false
	}
	inScope, err := r.IsInScope(file)
	return err == nil && inScope
}

// topLevelGoverning returns the top-level records (blueprint/bug) that explicitly
// govern at least one of the files, sorted by ID for determinism. Returns nil
// when files is empty.
func topLevelGoverning(records []*Record, files []string) []*Record {
	if len(files) == 0 {
		return nil
	}
	var out []*Record
	for _, r := range records {
		if !isTopLevel(r) {
			continue
		}
		for _, f := range files {
			if explicitlyGoverns(r, f) {
				out = append(out, r)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// governingRecords returns every record (any tier/status) that explicitly governs
// at least one of the files, sorted by ID.
func governingRecords(records []*Record, files []string) []string {
	seen := map[string]bool{}
	var ids []string
	for _, r := range records {
		for _, f := range files {
			if explicitlyGoverns(r, f) {
				if !seen[r.ID] {
					seen[r.ID] = true
					ids = append(ids, r.ID)
				}
				break
			}
		}
	}
	sort.Strings(ids)
	return ids
}

// uncoveredFiles returns the subset of files NOT explicitly governed by active
// — the ones fswrite enforcement (prov-2026-8d2f5f2a) leaves non-writable
// because active's affected_scope does not declare them.
func uncoveredFiles(active *Record, files []string) []string {
	var out []string
	for _, f := range files {
		if !explicitlyGoverns(active, f) {
			out = append(out, f)
		}
	}
	return out
}

// unimplementedImprints returns the imprints that implement blueprintID and are
// not yet implemented, sorted by ID.
func unimplementedImprints(records []*Record, blueprintID string) []*Record {
	var out []*Record
	for _, r := range records {
		if r.Type == RecordTypeImprint && r.Implements == blueprintID && r.Status != StatusImplemented {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func recordIDs(records []*Record) []string {
	ids := make([]string, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	return ids
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
