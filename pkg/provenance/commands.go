package provenance

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/livecodelife/linespec/v3/pkg/config"
	"github.com/livecodelife/linespec/v3/pkg/embeddings"
	plugin "github.com/livecodelife/linespec/v3/plugins/provenance"
)

// Commands provides all provenance CLI commands
type Commands struct {
	Loader    *Loader
	Linter    *Linter
	Git       *Git
	Checker   *CommitChecker
	Formatter *Formatter
	Config    *ProvenanceConfig
	Cache     *CacheManager
	RepoRoot  string
	Embedder  *embeddings.Client
}

// ProvenanceConfig holds provenance-related configuration
type ProvenanceConfig struct {
	Enforcement                  string
	Dir                          string
	ExcludePaths                 []string // glob/regex/path patterns for files exempt from provenance rules
	SharedRepos                  []config.SharedRepoConfig
	CacheTTLMinutes              int
	CommitTagRequired            bool
	AutoAffectedScope            bool
	RunAssociatedSpecsOnComplete bool
	OverlapSpecsOnComplete       string // block (default) | warn | off — severity of the cross-record overlap teeth at completion
	CommitOnStatusChange         bool
	Embedding                    *config.EmbeddingConfig
	ManifestURL                  string // source manifest URL, set by linespec clone
	WriteRestriction             *bool  // whether the fswrite permission projection is enforced; nil/true (default) restricts, false disables it entirely
	// ConfigFileDir is the repo-relative directory containing the .linespec.yml
	// this config was loaded from ("." for the repo root). It bounds fswrite
	// reconcile passes to the closest parent directory of a linespec config
	// file (prov-2026-bde50f4d) — a directory-specific config's write
	// restriction must never reach outside its own directory nor into a more
	// deeply nested config's directory. Empty is treated as "." (repo root),
	// which is also what a Commands built directly in Go (not through the
	// .linespec.yml loader) gets by default, preserving legacy unrestricted
	// behavior when no nested config exists.
	ConfigFileDir string
}

// Overlap-teeth severity modes for overlap_specs_on_complete. block is the default
// and matches the original Phase 3/4 behavior (prov-2026-bc57fbdc).
const (
	OverlapSpecsBlock = "block" // run touched sealed records' specs; roll back on failure
	OverlapSpecsWarn  = "warn"  // run them; a failure becomes a non-blocking FYI, completion proceeds
	OverlapSpecsOff   = "off"   // skip the cross-record teeth entirely (own specs still run)
)

// normalizeOverlapMode maps a configured overlap_specs_on_complete value to a known
// mode. Empty defaults to block (backward compatible). Any unrecognized value also
// falls back to block — never silently off — with a one-line stderr notice so a typo
// cannot quietly disable the teeth.
func normalizeOverlapMode(mode string) string {
	switch mode {
	case "":
		return OverlapSpecsBlock
	case OverlapSpecsBlock, OverlapSpecsWarn, OverlapSpecsOff:
		return mode
	default:
		fmt.Fprintf(os.Stderr,
			"Warning: unknown overlap_specs_on_complete %q — defaulting to %q. Valid values: %s, %s, %s.\n",
			mode, OverlapSpecsBlock, OverlapSpecsBlock, OverlapSpecsWarn, OverlapSpecsOff)
		return OverlapSpecsBlock
	}
}

// NewCommands creates a new commands instance
func NewCommands(config *ProvenanceConfig, repoRoot string, output *os.File, color bool) (*Commands, error) {
	return NewCommandsWithEmbedder(config, repoRoot, output, color, nil)
}

// NewCommandsWithEmbedder creates a new commands instance with optional embedding client
func NewCommandsWithEmbedder(config *ProvenanceConfig, repoRoot string, output *os.File, color bool, embedder *embeddings.Client) (*Commands, error) {
	// Default values
	if config.Dir == "" {
		config.Dir = "provenance"
	}
	if config.Enforcement == "" {
		config.Enforcement = "warn"
	}

	// Ensure directory is absolute
	if !filepath.IsAbs(config.Dir) && repoRoot != "" {
		config.Dir = filepath.Join(repoRoot, config.Dir)
	}

	// Build cache and collect any populated cache directories to load alongside local records.
	cache := NewCacheManager(config.SharedRepos, config.CacheTTLMinutes)
	sharedDirs := cache.LoadedDirs()

	loader := NewLoader(config.Dir, sharedDirs)
	if err := loader.LoadAll(); err != nil {
		return nil, fmt.Errorf("failed to load provenance records: %w", err)
	}

	// Keep the scope cache warm for the `next` fast path (prov-2026-007c8893).
	// Best-effort and only rewrites when stale, so the already-fresh case is cheap.
	refreshScopeIndexCache(repoRoot, loaderDirs(config.Dir, sharedDirs), loader.Records)

	// Create linter with hash-based integrity checker. The manifest is scoped
	// to the directory containing the resolved provenance config (the parent
	// of config.Dir), not unconditionally to repoRoot — otherwise `compile -c
	// packages/foo/.linespec.yml` would write to the same
	// <repoRoot>/.linespec/hash_manifest.json as a root-level compile and
	// clobber it with only that package's records (issue #163). For the
	// default single-package layout (config.Dir == "<repoRoot>/provenance")
	// this resolves to repoRoot exactly as before, so compile stays a no-op
	// when the manifest is already up to date.
	linter := NewLinter(loader, config.Enforcement)
	linter.Hasher = NewHasher(filepath.Dir(config.Dir))
	linter.ExcludePaths = config.ExcludePaths
	// Record-internal paths (affected_scope, forbidden_scope, associated_specs)
	// resolve against the git repository root, not the process cwd
	// (prov-2026-2f2bf9c3), so lint/open/complete are invocation-independent.
	linter.RepoRoot = repoRoot

	// Create git helper
	git := NewGit(repoRoot)

	// Create commit checker
	checker := NewCommitChecker(git, loader)
	checker.ExcludePaths = config.ExcludePaths

	// Create formatter
	formatter := NewFormatter(output, color)

	return &Commands{
		Loader:    loader,
		Linter:    linter,
		Git:       git,
		Checker:   checker,
		Formatter: formatter,
		Config:    config,
		Cache:     cache,
		RepoRoot:  repoRoot,
		Embedder:  embedder,
	}, nil
}

// CreateOptions holds options for the create command
type CreateOptions struct {
	Title      string
	Supersedes string
	Tags       []string
	NoEdit     bool
	IDSuffix   string     // Service suffix for ID (e.g., "user-service" creates prov-YYYY-NNN-user-service)
	Type       RecordType // Tier type: brief | blueprint | imprint
	ConfigFile string     // Path to custom .linespec.yml file
}

// Create creates a new provenance record
func (c *Commands) Create(opts CreateOptions) error {
	record, err := c.createRecord(opts)
	if err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to create record: %v", err))
		return err
	}

	superseded := ""
	if opts.Supersedes != "" && opts.Supersedes != "null" {
		target, _ := c.Loader.GetRecord(opts.Supersedes)
		if c.isRemoteRecord(target) {
			c.Formatter.FormatError(fmt.Sprintf(
				"Cannot supersede %s: this record is owned by a remote repository and is read-only from this repo.",
				opts.Supersedes,
			))
			return fmt.Errorf("record is read-only")
		}
		target.SupersededBy = record.ID
		target.Status = StatusSuperseded

		if err := c.Loader.SaveRecord(target); err != nil {
			c.Formatter.FormatError(fmt.Sprintf("Failed to update superseded record: %v", err))
			return err
		}
		superseded = opts.Supersedes
	}

	if !opts.NoEdit {
		if err := c.openInEditor(record.FilePath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Could not open editor: %v\n", err)
		}
	}

	c.Formatter.FormatCreateSuccess(record, superseded)
	return nil
}

// createRecord creates and saves a new provenance record, returning the record
func (c *Commands) createRecord(opts CreateOptions) (*Record, error) {
	existingIDs := c.Loader.GetAllIDs()
	year := CurrentYear()
	id, err := NextID(year, existingIDs)
	if err != nil {
		return nil, err
	}
	if opts.IDSuffix != "" {
		id = fmt.Sprintf("%s-%s", id, opts.IDSuffix)
	}
	author, err := c.Git.GetGitEmail()
	if err != nil {
		author = "unknown@example.com"
	}

	record := &Record{
		ID:               id,
		Title:            opts.Title,
		Status:           StatusDraft,
		Type:             opts.Type,
		CreatedAt:        CurrentDate(),
		Author:           author,
		Intent:           "",
		Constraints:      []string{},
		AffectedScope:    []string{},
		ForbiddenScope:   []string{},
		Supersedes:       opts.Supersedes,
		SupersededBy:     "",
		Related:          []string{},
		AssociatedSpecs:  []AssociatedSpec{},
		AssociatedTraces: []string{},
		Monitors:         []string{},
		Tags:             opts.Tags,
		FilePath:         filepath.Join(c.Config.Dir, id+".yml"),
	}

	if opts.Supersedes != "" && opts.Supersedes != "null" {
		target, exists := c.Loader.GetRecord(opts.Supersedes)
		if !exists {
			return nil, fmt.Errorf("supersedes target does not exist")
		}
		if target.SupersededBy != "" && target.SupersededBy != "null" {
			return nil, fmt.Errorf("target already superseded")
		}
	}

	if err := c.Loader.SaveRecord(record); err != nil {
		return nil, err
	}

	return record, nil
}

// openInEditor opens a file in the user's preferred editor
func (c *Commands) openInEditor(path string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi" // default fallback
	}

	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// LintOptions holds options for the lint command
type LintOptions struct {
	RecordID    string
	Enforcement string
	Format      string // human | json
	ConfigFile  string // Path to custom .linespec.yml file
	ShowWarn    bool   // show only warnings
	ShowInfo    bool   // show only hints/info
	ShowAll     bool   // show all severities
}

// Lint runs the linter
func (c *Commands) Lint(opts LintOptions) error {
	// Override enforcement if specified
	enforcement := c.Config.Enforcement
	if opts.Enforcement != "" {
		enforcement = opts.Enforcement
	}

	c.Linter.Enforcement = enforcement

	// Run lint
	var result *LintResult
	if opts.RecordID != "" {
		result = c.Linter.LintRecord(opts.RecordID)
	} else {
		result = c.Linter.LintAll()
	}

	// Warn about stale shared repo caches (populated but older than TTL).
	if c.Cache != nil {
		ttl := c.Config.CacheTTLMinutes
		if ttl <= 0 {
			ttl = 60
		}
		for _, repo := range c.Config.SharedRepos {
			if _, err := os.Stat(c.Cache.ProvenanceDir(repo)); err != nil {
				continue // never synced — not a warning, just not configured yet
			}
			if !c.Cache.IsFresh(repo) {
				result.Add(Issue{
					RecordID: "",
					Field:    "cache",
					Message: fmt.Sprintf(
						"Cache for shared repo %q is stale (older than %d minutes). Run 'linespec provenance sync' to refresh.",
						repo.Name, ttl,
					),
					Severity: SeverityWarning,
				})
			}
		}
	}

	// Output
	switch opts.Format {
	case "json":
		return c.Formatter.FormatJSON(result.ToJSON())
	case "sarif":
		// Get list of analyzed files
		analyzedFiles := GetAnalyzedFiles(result, c.Loader)

		// Generate SARIF document
		sarifDoc := result.ToSARIF(c.Loader, c.RepoRoot, analyzedFiles)

		// Convert to JSON
		jsonBytes, err := sarifDoc.ToJSON()
		if err != nil {
			return fmt.Errorf("failed to generate SARIF: %w", err)
		}

		// Write to stdout (no extra output for SARIF format)
		fmt.Fprintln(c.Formatter.Output, string(jsonBytes))

		// Exit with error if there are errors (same behavior as other formats)
		if result.HasErrors() {
			return fmt.Errorf("lint failed")
		}
		return nil
	default:
		c.Formatter.FormatLint(result, opts)
	}

	if result.HasErrors() {
		return fmt.Errorf("lint failed")
	}

	return nil
}

// StatusOptions holds options for the status command
type StatusOptions struct {
	RecordID   string
	Filter     string // open | implemented | superseded | deprecated | tag:xxx
	Format     string // human | json
	SaveScope  bool   // persist auto-populated scope to file
	ConfigFile string // Path to custom .linespec.yml file
}

// Status shows record status
func (c *Commands) Status(opts StatusOptions) error {
	// Track which records were auto-populated (for UX message)
	var autoPopulatedRecords []*Record

	// Auto-populate scope if configured
	if c.Config.AutoAffectedScope {
		for _, record := range c.Loader.Records {
			if record.Status == StatusOpen && record.ScopeMode() == "observed" {
				// Store original scope length to detect if it changed
				originalLen := len(record.AffectedScope)
				if err := c.Checker.AutoPopulateScope(record); err != nil {
					// Non-fatal, just log
					fmt.Fprintf(os.Stderr, "Warning: Could not auto-populate scope for %s: %v\n", record.ID, err)
				} else if len(record.AffectedScope) > originalLen && !c.isRemoteRecord(record) {
					// Scope was actually populated with new files (remote records are read-only)
					autoPopulatedRecords = append(autoPopulatedRecords, record)
				}
			}
		}
	}

	// Persist scope if --save-scope flag is used
	if opts.SaveScope && len(autoPopulatedRecords) > 0 {
		for _, record := range autoPopulatedRecords {
			if err := c.Loader.SaveRecord(record); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Could not save scope for %s: %v\n", record.ID, err)
			} else {
				fmt.Fprintf(c.Formatter.Output, "✓ Saved auto-populated scope for %s (%d files)\n", record.ID, len(record.AffectedScope))
			}
		}
	}

	// Output
	if opts.Format == "json" {
		if opts.RecordID != "" {
			record, exists := c.Loader.GetRecord(opts.RecordID)
			if !exists {
				return fmt.Errorf("record not found: %s", opts.RecordID)
			}
			// Include UX message in JSON if scope was auto-populated but not saved
			result := map[string]interface{}{
				"record": record,
			}
			if len(autoPopulatedRecords) > 0 && !opts.SaveScope {
				result["_notice"] = "Scope auto-populated (not saved). Use --save-scope flag or run 'linespec provenance lock-scope' to persist"
				result["_auto_populated_records"] = getRecordIDs(autoPopulatedRecords)
			}
			return c.Formatter.FormatJSON(result)
		}
		// For all records, include notice if applicable
		result := map[string]interface{}{
			"records": c.Loader.Records,
		}
		if len(autoPopulatedRecords) > 0 && !opts.SaveScope {
			result["_notice"] = "Scope auto-populated (not saved). Use --save-scope flag or run 'linespec provenance lock-scope' to persist"
			result["_auto_populated_records"] = getRecordIDs(autoPopulatedRecords)
		}
		return c.Formatter.FormatJSON(result)
	}

	// Human format output
	if opts.RecordID != "" {
		record, exists := c.Loader.GetRecord(opts.RecordID)
		if !exists {
			c.Formatter.FormatError(fmt.Sprintf("Record not found: %s", opts.RecordID))
			return fmt.Errorf("record not found")
		}
		c.Formatter.FormatStatusDetailed(record, c.Loader)
	} else {
		c.Formatter.FormatStatus(c.Loader, c.Config.Enforcement, opts.Filter)
	}

	// Show UX message for ephemeral mode (auto-populated but not saved)
	if len(autoPopulatedRecords) > 0 && !opts.SaveScope {
		fmt.Fprintln(c.Formatter.Output)
		fmt.Fprintln(c.Formatter.Output, "⚠ Scope auto-populated (not saved)")
		fmt.Fprintln(c.Formatter.Output, "  To persist these changes, use either:")
		fmt.Fprintln(c.Formatter.Output, "    --save-scope flag: linespec provenance status --save-scope")
		fmt.Fprintln(c.Formatter.Output, "    lock-scope command: linespec provenance lock-scope --record <id>")
		fmt.Fprintln(c.Formatter.Output)
		fmt.Fprintln(c.Formatter.Output, "  Auto-populated records:")
		for _, record := range autoPopulatedRecords {
			fmt.Fprintf(c.Formatter.Output, "    - %s (%d files)\n", record.ID, len(record.AffectedScope))
		}
	}

	return nil
}

// getRecordIDs extracts IDs from a slice of records
func getRecordIDs(records []*Record) []string {
	ids := make([]string, len(records))
	for i, r := range records {
		ids[i] = r.ID
	}
	return ids
}

// GraphOptions holds options for the graph command
type GraphOptions struct {
	Root       string // Start from specific record
	Filter     string // open | implemented | superseded | deprecated
	Format     string // human | json | dot
	ConfigFile string // Path to custom .linespec.yml file
}

// Graph shows the provenance graph
func (c *Commands) Graph(opts GraphOptions) error {
	// Output
	switch opts.Format {
	case "json":
		return c.Formatter.FormatJSON(BuildJSONGraph(c.Loader))
	case "dot":
		return c.outputDotGraph(opts)
	default:
		c.Formatter.FormatGraph(c.Loader, opts.Filter, opts.Root)
	}

	return nil
}

// outputDotGraph outputs the graph in Graphviz DOT format
func (c *Commands) outputDotGraph(opts GraphOptions) error {
	fmt.Println("digraph ProvenanceGraph {")
	fmt.Println("  rankdir=TB;")
	fmt.Println("  node [shape=box];")

	for _, record := range c.Loader.Records {
		label := fmt.Sprintf("%s\\n%s", record.ID, strings.ReplaceAll(record.Title, "\"", "\\\""))
		color := "black"
		switch record.Status {
		case StatusOpen:
			color = "orange"
		case StatusImplemented:
			color = "green"
		case StatusSuperseded:
			color = "gray"
		case StatusDeprecated:
			color = "red"
		}

		fmt.Printf("  \"%s\" [label=\"%s\", color=%s];\n", record.ID, label, color)

		if record.Supersedes != "" && record.Supersedes != "null" {
			fmt.Printf("  \"%s\" -> \"%s\";\n", record.Supersedes, record.ID)
		}
	}

	fmt.Println("}")
	return nil
}

// CheckOptions holds options for the check command
type CheckOptions struct {
	Commit      string // Single commit to check (default: HEAD)
	Range       string // Range to check (e.g., SHA..SHA)
	Record      string // Check only against a specific record
	Staged      bool   // Check staged files instead of committed
	MessageFile string // Path to commit message file (for staged mode)
	ConfigFile  string // Path to custom .linespec.yml file
}

// Check checks commits for violations
func (c *Commands) Check(opts CheckOptions) error {
	var violations []Violation
	var err error

	// Governance-overlap / stale-scope signals are intentionally NOT computed here.
	// They were noisy on every commit and every lint; the signal now lives on the
	// open/complete lifecycle transitions (see prov-2026-bc57fbdc). `check` is
	// limited to forbidden-scope enforcement.
	if opts.Staged {
		// Check staged files
		violations, err = c.Checker.CheckStaged(opts.MessageFile, c.Config.CommitTagRequired)
		if err != nil {
			c.Formatter.FormatError(fmt.Sprintf("Check failed: %v", err))
			return err
		}
	} else if opts.Range != "" {
		// Check range
		parts := strings.Split(opts.Range, "..")
		if len(parts) != 2 {
			c.Formatter.FormatError("Invalid range format. Use SHA..SHA")
			return fmt.Errorf("invalid range format")
		}
		violations, err = c.Checker.CheckRange(parts[0], parts[1])
	} else {
		// Check single commit (default HEAD)
		commit := opts.Commit
		if commit == "" {
			commit = "HEAD"
		}
		violations, err = c.Checker.CheckCommit(commit)
	}

	if err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Check failed: %v", err))
		return err
	}

	// Filter by record if specified
	if opts.Record != "" {
		var filtered []Violation
		for _, v := range violations {
			if v.RecordID == opts.Record {
				filtered = append(filtered, v)
			}
		}
		violations = filtered
	}

	// Use appropriate label for output
	label := opts.Commit
	if label == "" {
		label = "HEAD"
	}
	if opts.Staged {
		label = "staged"
	}
	c.Formatter.FormatCheckResult(violations, nil, label)

	if len(violations) > 0 {
		return fmt.Errorf("forbidden scope violations found")
	}

	return nil
}

// LockScopeOptions holds options for the lock-scope command
type LockScopeOptions struct {
	RecordID   string
	DryRun     bool
	ConfigFile string // Path to custom .linespec.yml file
}

// lockableRecord loads opts.RecordID and validates it can have its scope
// changed at all (exists, not remote, not implemented) — the precondition
// shared by LockScope's initial populate and AddScope's widening.
func (c *Commands) lockableRecord(recordID string) (*Record, error) {
	record, exists := c.Loader.GetRecord(recordID)
	if !exists {
		c.Formatter.FormatError(fmt.Sprintf("Record not found: %s", recordID))
		return nil, fmt.Errorf("record not found")
	}

	if c.isRemoteRecord(record) {
		c.Formatter.FormatError(fmt.Sprintf(
			"Cannot modify %s: this record is owned by a remote repository and is read-only from this repo.",
			recordID,
		))
		return nil, fmt.Errorf("record is read-only")
	}

	if record.Status == StatusImplemented {
		c.Formatter.FormatError(fmt.Sprintf("Cannot modify %s: record is implemented\n\n  Implemented records are immutable. To change scope, create a new\n  Provenance Record that supersedes %s.", recordID, recordID))
		return nil, fmt.Errorf("record is implemented")
	}

	return record, nil
}

// LockScope populates a record's affected_scope from its git history the
// first time (observed -> allowlist). Widening an already-allowlist record's
// scope is a distinct operation — see AddScope — so it errors here rather
// than silently doing something different from what --dry-run just printed.
func (c *Commands) LockScope(opts LockScopeOptions) error {
	record, err := c.lockableRecord(opts.RecordID)
	if err != nil {
		return err
	}

	if record.ScopeMode() == "allowlist" {
		c.Formatter.FormatError(fmt.Sprintf("%s is already in allowlist mode; use 'linespec provenance add-scope --record %s' to widen it", opts.RecordID, opts.RecordID))
		return fmt.Errorf("already in allowlist mode")
	}

	origScope := append([]string(nil), record.AffectedScope...)

	// Auto-populate scope from git history
	if err := c.Checker.AutoPopulateScope(record); err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to auto-populate scope: %v", err))
		return err
	}
	added := append([]string(nil), record.AffectedScope...)

	if opts.DryRun {
		c.Formatter.FormatLockScopeSuccess(record, record.AffectedScope)
		record.AffectedScope = origScope
		return nil
	}

	if err := c.Loader.SaveRecord(record); err != nil {
		record.AffectedScope = origScope
		c.Formatter.FormatError(fmt.Sprintf("Failed to save record: %v", err))
		return err
	}

	// Materialize write permission for the newly-declared paths in the same
	// atomic operation that locks scope (prov-2026-8d2f5f2a). Permissions only
	// apply to OPEN records (constraint 1) — a draft record's scope is not
	// enforced yet, so there is nothing to materialize.
	if record.Status == StatusOpen {
		if err := MaterializeScope(c.RepoRoot, added); err != nil {
			record.AffectedScope = origScope
			if serr := c.Loader.SaveRecord(record); serr != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to roll back %s after materialize failure: %v\n", opts.RecordID, serr)
			}
			c.Formatter.FormatError(fmt.Sprintf("Failed to materialize write permission for %s's scope: %v", opts.RecordID, err))
			return err
		}
	}

	c.Formatter.FormatLockScopeSuccess(record, record.AffectedScope)
	return nil
}

// AddScopeOptions holds options for the add-scope command.
type AddScopeOptions struct {
	RecordID   string
	DryRun     bool
	ConfigFile string // Path to custom .linespec.yml file
}

// AddScope widens an already-allowlist record's affected_scope (prov-2026-8d2f5f2a,
// "Adding a path to an already-open record's affected_scope MUST go through a
// dedicated `linespec provenance add-scope` CLI verb"): it pulls files from
// commits already tagged with the record that are not yet declared, adds them
// to affected_scope, and — if the record is open — materializes write
// permission for exactly those newly-added paths in the same atomic
// operation. It errors if the record is not yet in allowlist mode (use
// LockScope first) or if there is nothing new to add.
func (c *Commands) AddScope(opts AddScopeOptions) error {
	record, err := c.lockableRecord(opts.RecordID)
	if err != nil {
		return err
	}

	if record.ScopeMode() != "allowlist" {
		c.Formatter.FormatError(fmt.Sprintf("%s is not yet in allowlist mode; use 'linespec provenance lock-scope --record %s' to populate its initial scope", opts.RecordID, opts.RecordID))
		return fmt.Errorf("not in allowlist mode")
	}

	origScope := append([]string(nil), record.AffectedScope...)

	// Pull files from commits already tagged with this record that are not yet
	// declared — AutoPopulateScope itself is a no-op once allowlist, so the
	// merge is done inline here instead.
	commits, err := c.Git.GetCommitsForRecord(record.ID)
	if err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to look up commits for %s: %v", opts.RecordID, err))
		return err
	}
	files, err := c.Git.GetFilesChangedInCommits(commits)
	if err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to list files changed for %s: %v", opts.RecordID, err))
		return err
	}
	// The commit tagging this record as it was first authored (e.g. "add
	// record [id]") also matches GetCommitsForRecord, so its changed files
	// include the record's own YAML and any always-writable coop path — never
	// meaningful additions to affected_scope, and always writable regardless.
	always := AlwaysWritablePaths(c.Config, c.Loader.Records, c.RepoRoot)
	ownFile, _ := filepath.Rel(c.RepoRoot, record.FilePath)
	existing := map[string]bool{}
	for _, f := range record.AffectedScope {
		existing[f] = true
	}
	var added []string
	for _, f := range files {
		if existing[f] || f == ownFile || IsAlwaysWritable(f, always) {
			continue
		}
		record.AffectedScope = append(record.AffectedScope, f)
		existing[f] = true
		added = append(added, f)
	}
	if len(added) == 0 {
		c.Formatter.FormatError(fmt.Sprintf("%s has no newly committed files to add to affected_scope", opts.RecordID))
		return fmt.Errorf("nothing new to add")
	}
	sort.Strings(added)

	if opts.DryRun {
		c.Formatter.FormatLockScopeSuccess(record, record.AffectedScope)
		record.AffectedScope = origScope
		return nil
	}

	if err := c.Loader.SaveRecord(record); err != nil {
		record.AffectedScope = origScope
		c.Formatter.FormatError(fmt.Sprintf("Failed to save record: %v", err))
		return err
	}

	// Materialize write permission for the newly-declared paths in the same
	// atomic operation that widens scope. Permissions only apply to OPEN
	// records (constraint 1) — a draft record's scope is not enforced yet.
	if record.Status == StatusOpen {
		if err := MaterializeScope(c.RepoRoot, added); err != nil {
			record.AffectedScope = origScope
			if serr := c.Loader.SaveRecord(record); serr != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to roll back %s after materialize failure: %v\n", opts.RecordID, serr)
			}
			c.Formatter.FormatError(fmt.Sprintf("Failed to materialize write permission for %s's widened scope: %v", opts.RecordID, err))
			return err
		}
	}

	c.Formatter.FormatLockScopeSuccess(record, record.AffectedScope)
	return nil
}

// LockLayerOptions holds options for the lock-layer command
type LockLayerOptions struct {
	Title      string
	NoEdit     bool
	ConfigFile string // Path to custom .linespec.yml file
}

// LockLayer creates a locked layer record — an architectural declaration
// that is immediately implemented and locked
func (c *Commands) LockLayer(opts LockLayerOptions) error {
	if opts.Title == "" {
		c.Formatter.FormatError("--title is required")
		return fmt.Errorf("--title is required")
	}

	// Step 1: Create record (reuses createRecord, no editor)
	record, err := c.createRecord(CreateOptions{
		Title:  opts.Title,
		NoEdit: true,
	})
	if err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to create record: %v", err))
		return err
	}

	// Step 2: Complete it (reuses Complete — seals SHA, sets implemented)
	if err := c.Complete(CompleteOptions{RecordID: record.ID, Force: true}); err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to complete record: %v", err))
		return err
	}

	// Step 3: Set locked and save
	record, _ = c.Loader.GetRecord(record.ID)
	record.Locked = true
	if err := c.Loader.SaveRecord(record); err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to save record: %v", err))
		return err
	}

	// Step 4: Open editor if user didn't pass --no-edit
	if !opts.NoEdit {
		if err := c.openInEditor(record.FilePath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Could not open editor: %v\n", err)
		}
	}

	c.Formatter.FormatLockLayerSuccess(record)
	return nil
}

// CompleteOptions holds options for the complete command
type CompleteOptions struct {
	RecordID   string
	Force      bool
	ConfigFile string // Path to custom .linespec.yml file
}

// Complete marks a record as implemented
// isRemoteRecord reports whether a record was loaded from a shared repo cache
// rather than from the local provenance directory. Remote records are read-only.
func (c *Commands) isRemoteRecord(record *Record) bool {
	if record.FilePath == "" || c.Config.Dir == "" {
		return false
	}
	dir := c.Config.Dir
	if !strings.HasSuffix(dir, string(os.PathSeparator)) {
		dir += string(os.PathSeparator)
	}
	return !strings.HasPrefix(record.FilePath, dir)
}

// fileSnapshot captures a file's contents (or its absence) before a status
// transition mutates it, so the transition can be rolled back to leave the file
// byte-for-byte as it was.
type fileSnapshot struct {
	path    string
	data    []byte
	existed bool
}

// snapshotFiles records the current on-disk state of the given paths. A path that
// does not exist yet is captured as absent so that rollback removes it if the
// transition created it (e.g. a hash manifest written for the first time by seal).
func snapshotFiles(paths ...string) ([]fileSnapshot, error) {
	snaps := make([]fileSnapshot, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				snaps = append(snaps, fileSnapshot{path: p, existed: false})
				continue
			}
			return nil, fmt.Errorf("failed to snapshot %s: %w", p, err)
		}
		snaps = append(snaps, fileSnapshot{path: p, data: data, existed: true})
	}
	return snaps, nil
}

// restoreFiles returns the snapshotted files to their captured state, recreating
// removed files, rewriting modified ones, and deleting any that did not exist
// before. Errors are reported but do not stop the remaining restores.
func restoreFiles(snaps []fileSnapshot) {
	for _, s := range snaps {
		if s.existed {
			if err := os.WriteFile(s.path, s.data, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to restore %s during rollback: %v\n", s.path, err)
			}
			continue
		}
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: failed to remove %s during rollback: %v\n", s.path, err)
		}
	}
}

// reconcile re-derives the entire fswrite write-bit state (prov-2026-8d2f5f2a)
// for the git-tracked files under RepoRoot from the current set of open
// records' scopes, sourced from c.Loader.Records — which may already be the
// cache-backed projection TryNextFromCache built, satisfying "sourced from the
// existing scope-index cache" without a second load.
func (c *Commands) reconcile() (*ReconcileResult, error) {
	if WriteRestrictionEnabled(c.Config) && ColdStartSkip(c.Loader.Records, c.Config) {
		return &ReconcileResult{}, nil
	}
	files, err := listTrackedFiles(c.RepoRoot)
	if err != nil {
		return nil, err
	}
	ownDir := "."
	if c.Config != nil && c.Config.ConfigFileDir != "" {
		ownDir = c.Config.ConfigFileDir
	}
	files = boundToOwnConfigDir(ownDir, files)
	return Reconcile(c.RepoRoot, files, c.Loader.Records, c.Config)
}

// ReconcileOptions holds options for the reconcile command.
type ReconcileOptions struct {
	Format     string // human (default) | json
	ConfigFile string // path to custom .linespec.yml file
}

// Reconcile is the dedicated `linespec provenance reconcile` CLI verb
// (prov-2026-8d2f5f2a): it re-derives the entire fswrite write-bit projection
// from the current set of open records' scopes and reports what changed. It
// exists so reconcile can be invoked explicitly by any harness or script —
// enforcement must not depend on the Claude Code plugin hooks being
// installed. `next` (see Commands.Next) already calls this unconditionally on
// every invocation; this verb is for direct/scripted use.
func (c *Commands) Reconcile(opts ReconcileOptions) error {
	result, err := c.reconcile()
	if err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Reconcile failed: %v", err))
		return err
	}

	if opts.Format == "json" {
		enc := json.NewEncoder(c.Formatter.Output)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	if len(result.Unlocked) == 0 && len(result.Locked) == 0 {
		fmt.Fprintf(c.Formatter.Output, "\nWrite-bit projection already up to date — nothing to reconcile.\n\n")
		return nil
	}
	fmt.Fprintf(c.Formatter.Output, "\nReconciled write-bit projection\n\n")
	if len(result.Unlocked) > 0 {
		fmt.Fprintf(c.Formatter.Output, "  Unlocked %d paths:\n", len(result.Unlocked))
		for _, path := range result.Unlocked {
			fmt.Fprintf(c.Formatter.Output, "    · %s\n", path)
		}
	}
	if len(result.Locked) > 0 {
		fmt.Fprintf(c.Formatter.Output, "  Locked %d paths:\n", len(result.Locked))
		for _, path := range result.Locked {
			fmt.Fprintf(c.Formatter.Output, "    · %s\n", path)
		}
	}
	fmt.Fprintln(c.Formatter.Output)
	return nil
}

// relockClosedScope re-locks a record's own affected_scope paths after it
// stops being open (complete or deprecate), unless another still-open
// allowlist record also covers them — writability is a pure projection of the
// CURRENT set of open records, so a record leaving the open set gives up its
// exclusive claim on the paths it granted. Best-effort: a failure here must
// not undo an otherwise-successful status transition, so it only warns.
func (c *Commands) relockClosedScope(closed *Record) {
	if !WriteRestrictionEnabled(c.Config) {
		return // restriction disabled entirely (prov-2026-c1515def): nothing to re-lock
	}
	if closed.ScopeMode() != "allowlist" {
		return // observed-mode records never granted writability (constraint 1)
	}
	always := AlwaysWritablePaths(c.Config, c.Loader.Records, c.RepoRoot)
	stillOpen := OpenAllowlistScope(c.Loader.Records) // closed.Status is already updated in memory, so it's excluded here
	for _, rel := range closed.AffectedScope {
		if rel == "" || containsGlobMeta(rel) {
			continue
		}
		writable, err := IsWritablePath(rel, always, stillOpen)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to re-lock %s after closing %s: %v\n", rel, closed.ID, err)
			continue
		}
		if writable {
			continue
		}
		if err := LockFile(filepath.Join(c.RepoRoot, rel)); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to re-lock %s after closing %s: %v\n", rel, closed.ID, err)
		}
	}
}

// snapshotPaths extracts the file paths from a set of snapshots.
func snapshotPaths(snaps []fileSnapshot) []string {
	paths := make([]string, 0, len(snaps))
	for _, s := range snaps {
		paths = append(paths, s.path)
	}
	return paths
}

// formatLintErrors renders the error-level issues of a lint result as an indented
// bullet list for inclusion in an aborted-transition message.
func formatLintErrors(result *LintResult) string {
	var b strings.Builder
	for _, issue := range result.Issues {
		if issue.Severity != SeverityError {
			continue
		}
		if issue.Field != "" {
			fmt.Fprintf(&b, "    · %s: %s\n", issue.Field, issue.Message)
		} else {
			fmt.Fprintf(&b, "    · %s\n", issue.Message)
		}
	}
	return b.String()
}

func (c *Commands) Complete(opts CompleteOptions) error {
	record, exists := c.Loader.GetRecord(opts.RecordID)
	if !exists {
		c.Formatter.FormatError(fmt.Sprintf("Record not found: %s", opts.RecordID))
		return fmt.Errorf("record not found")
	}

	if c.isRemoteRecord(record) {
		c.Formatter.FormatError(fmt.Sprintf(
			"Cannot modify %s: this record is owned by a remote repository and is read-only from this repo.",
			opts.RecordID,
		))
		return fmt.Errorf("record is read-only")
	}

	// Check if already implemented
	if record.Status == StatusImplemented {
		c.Formatter.FormatError(fmt.Sprintf("Record %s is already implemented", opts.RecordID))
		return fmt.Errorf("already implemented")
	}

	// Verify associated specs exist
	if !opts.Force && len(record.AssociatedSpecs) > 0 {
		var missing []string
		for _, spec := range record.AssociatedSpecs {
			if _, err := os.Stat(resolveRecordPath(c.RepoRoot, spec.Path)); os.IsNotExist(err) {
				missing = append(missing, spec.Path)
			}
		}

		if len(missing) > 0 {
			c.Formatter.FormatError(fmt.Sprintf("Cannot mark %s as implemented\n\n  The following associated specs do not exist on disk:\n", opts.RecordID))
			for _, path := range missing {
				fmt.Fprintf(os.Stdout, "    · %s  ✗ not found\n", path)
			}
			fmt.Fprintln(os.Stdout)
			fmt.Fprintln(os.Stdout, "  Create the missing spec files or remove them from")
			fmt.Fprintln(os.Stdout, "  associated_specs before completing this record.")
			fmt.Fprintln(os.Stdout)
			return fmt.Errorf("missing specs")
		}
	}

	// Capture HEAD SHA for sealing
	headSHA, err := c.Git.GetHeadSHA()
	if err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to get HEAD SHA: %v", err))
		return err
	}

	// Snapshot the on-disk state of every file this transition will touch — the
	// record YAML and the hash manifest — BEFORE any mutation. Completing seals the
	// record (sealed_at_sha + manifest hash) and makes it immutable, so if anything
	// downstream fails we must be able to restore these byte-for-byte and leave the
	// record exactly as it was. The manifest is included even when it does not exist
	// yet, so a rollback removes a manifest the seal created.
	manifestTracked := c.Linter != nil && c.Linter.Hasher != nil
	tracked := []string{record.FilePath}
	if manifestTracked {
		tracked = append(tracked, c.Linter.Hasher.ManifestPath())
	}
	snaps, err := snapshotFiles(tracked...)
	if err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to snapshot record state: %v", err))
		return err
	}

	// Preserve the in-memory fields we are about to overwrite so a rollback can
	// restore the record struct as well as the files on disk.
	origStatus := record.Status
	origSealedAtSHA := record.SealedAtSHA

	rollback := func(unstage bool) {
		record.Status = origStatus
		record.SealedAtSHA = origSealedAtSHA
		restoreFiles(snaps)
		if unstage {
			if err := c.Git.Unstage(snapshotPaths(snaps)...); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to unstage during rollback: %v\n", err)
			}
		}
	}

	// Update status and seal
	record.Status = StatusImplemented
	record.SealedAtSHA = headSHA

	// Save record
	if err := c.Loader.SaveRecord(record); err != nil {
		rollback(false)
		c.Formatter.FormatError(fmt.Sprintf("Failed to save record: %v", err))
		return err
	}

	// Seal content hash into the manifest so the linter can verify integrity
	// without git traversal.
	if manifestTracked {
		if err := c.Linter.Hasher.SealRecord(record, c.Loader.Records); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to seal content hash for %s: %v\n", record.ID, err)
		}
	}

	// Validate the sealed record BEFORE committing it to disk, unconditionally —
	// regardless of commit_on_status_change. Sealing makes the record immutable, so
	// a record that does not pass lint would be locked into an invalid, unfixable
	// state. If validation fails, roll the seal back and tell the user why.
	if c.Linter != nil {
		if result := c.Linter.LintRecord(record.ID); result.HasErrors() {
			rollback(false)
			c.Formatter.FormatError(fmt.Sprintf(
				"Cannot complete %s: sealing it would make it immutable, but it does not pass validation:\n\n%s\n"+
					"  The transition was rolled back — %s is unchanged (still %s).\n"+
					"  Fix the errors above and run 'linespec provenance complete' again.",
				opts.RecordID, formatLintErrors(result), opts.RecordID, origStatus,
			))
			return fmt.Errorf("record failed validation")
		}
	}

	// Run the record's OWN associated_specs — the actual completion-time gate.
	// This uses the same mechanism as `run-specs` (runRecordSpecs), so a spec whose
	// command exits non-zero blocks completion exactly like `run-specs` would report
	// it: ✗ failed, exit nonzero. Only active when run_associated_specs_on_complete
	// is enabled and the record has specs; opts.Force bypasses it the same way it
	// bypasses the path-existence check above. A failure rolls back the seal —
	// mirroring the lint-failure rollback just above — so the record is never left
	// implemented with a failing proof.
	var specOutcomes []SpecOutcome
	if !opts.Force && c.Config.RunAssociatedSpecsOnComplete && len(record.AssociatedSpecs) > 0 {
		fmt.Fprintf(c.Formatter.Output, "\nRunning %d associated spec(s) for %s...\n\n", len(record.AssociatedSpecs), record.ID)
		_, failed, outcomes := c.runRecordSpecs(record, nil)
		specOutcomes = outcomes
		if failed {
			rollback(false)
			c.Formatter.FormatError(fmt.Sprintf(
				"Cannot complete %s: one or more of its own associated specs failed.\n\n"+
					"  The transition was rolled back — %s is unchanged (still %s).\n"+
					"  Fix the failing spec(s) above and run 'linespec provenance complete' again.",
				opts.RecordID, opts.RecordID, origStatus,
			))
			return fmt.Errorf("associated spec failed")
		}
	}

	// Governance-overlap verification (completion-time teeth). Find the sealed
	// records this change actually touched — computed from the files across this
	// record's commits, not glob intersection — and run their associated specs to
	// confirm their proven behavior still holds. The overlap_specs_on_complete mode
	// controls severity: block rolls back on a real spec failure; warn turns a
	// failure into a non-blocking FYI and proceeds; off skips the teeth entirely
	// (selection included, so it costs nothing). Remote records are excluded by the
	// selector. A blocked completion rolls back atomically.
	overlapMode := normalizeOverlapMode(c.Config.OverlapSpecsOnComplete)
	if overlapMode != OverlapSpecsOff {
		if touched := c.completionOverlapSelector(record); len(touched) > 0 {
			var unverified []*Record
			var failedRecords []*Record
			runTeeth := c.Config.RunAssociatedSpecsOnComplete
			// seen de-duplicates identical spec commands across the touched records so a
			// shared proof (e.g. `make test`) runs once, not once per record.
			seen := map[string]bool{}
			for _, r := range touched {
				if !runTeeth || len(r.AssociatedSpecs) == 0 {
					unverified = append(unverified, r)
					continue
				}
				fmt.Fprintf(c.Formatter.Output, "\nVerifying sealed record %s touched by this change (%s)...\n", r.ID, r.Title)
				ran, failed, _ := c.runRecordSpecs(r, seen)
				if failed {
					failedRecords = append(failedRecords, r)
				}
				if !ran {
					unverified = append(unverified, r)
				}
			}

			if len(failedRecords) > 0 && overlapMode == OverlapSpecsBlock {
				rollback(false)
				c.Formatter.FormatError(fmt.Sprintf(
					"Cannot complete %s: it changes files governed by a sealed record whose specs now fail.\n\n"+
						"  A sealed record's proven behavior appears broken by this change, so completion is blocked.\n"+
						"  The transition was rolled back — %s is unchanged (still %s).\n"+
						"  Fix the failing spec(s) above, or supersede the affected record, then run 'linespec provenance complete' again.\n"+
						"  (To downgrade this gate, set overlap_specs_on_complete: warn|off in .linespec.yml.)",
					opts.RecordID, opts.RecordID, origStatus,
				))
				return fmt.Errorf("overlapping sealed record spec failed")
			}

			if len(failedRecords) > 0 { // warn mode — surface but do not block
				fmt.Fprintf(c.Formatter.Output,
					"\nℹ Governance overlap (non-blocking, warn mode): specs of sealed record(s) you touched failed:\n\n%s\n"+
						"  Completion proceeds because overlap_specs_on_complete is 'warn'. If the failure is real,\n"+
						"  fix it or supersede the affected record; if it is flaky/unrelated, no action is needed.\n\n",
					formatRecordList(failedRecords))
			}

			if len(unverified) > 0 {
				fmt.Fprintf(c.Formatter.Output,
					"\nℹ Governance overlap (non-blocking): this change also touches files governed by sealed record(s):\n\n%s\n"+
						"  No runnable specs were available to auto-verify them. If your change conflicts with\n"+
						"  their original intent, create a record that supersedes them.\n\n",
					formatRecordList(unverified))
			}
		}
	}

	if c.Config.CommitOnStatusChange {
		msg := fmt.Sprintf("Complete provenance record [%s]", opts.RecordID)
		files := []string{record.FilePath}
		if manifestTracked && c.Linter.Hasher.ManifestExists() {
			files = append(files, c.Linter.Hasher.ManifestPath())
		}
		if err := c.Git.CommitRecord(msg, files...); err != nil {
			rollback(true)
			c.Formatter.FormatError(fmt.Sprintf(
				"Cannot complete %s: the auto-commit was rejected:\n\n    %v\n\n"+
					"  The transition was rolled back — %s is unchanged (still %s) and nothing was staged.\n"+
					"  Resolve the issue above (e.g. a failing pre-commit hook) and run 'linespec provenance complete' again.",
				opts.RecordID, err, opts.RecordID, origStatus,
			))
			return err
		}
	}

	c.Formatter.FormatCompleteSuccess(record, specOutcomes)

	// Completing gives up the record's exclusive claim on the write access its
	// scope granted while open (prov-2026-8d2f5f2a) — re-lock any of its scope
	// paths no other still-open allowlist record covers.
	c.relockClosedScope(record)

	// Generate and store embedding for the implemented record
	if c.Embedder != nil && c.Embedder.IsConfigured() && c.Embedder.IndexOnComplete() {
		text := embeddings.ExtractTextFromRecord(record.Title, record.Intent, record.Constraints)
		vector, err := c.Embedder.GenerateDocument(text)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to generate embedding for %s: %v\n", record.ID, err)
		} else {
			store := embeddings.NewStore(c.RepoRoot)
			err := store.Write(embeddings.RecordEmbedding{
				RecordID: record.ID,
				Vector:   vector,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to store embedding for %s: %v\n", record.ID, err)
			}
		}
	}

	return nil
}

// DeprecateOptions holds options for the deprecate command
type DeprecateOptions struct {
	RecordID   string
	Reason     string
	ConfigFile string // Path to custom .linespec.yml file
}

// Deprecate marks a record as deprecated
func (c *Commands) Deprecate(opts DeprecateOptions) error {
	record, exists := c.Loader.GetRecord(opts.RecordID)
	if !exists {
		c.Formatter.FormatError(fmt.Sprintf("Record not found: %s", opts.RecordID))
		return fmt.Errorf("record not found")
	}

	if c.isRemoteRecord(record) {
		c.Formatter.FormatError(fmt.Sprintf(
			"Cannot modify %s: this record is owned by a remote repository and is read-only from this repo.",
			opts.RecordID,
		))
		return fmt.Errorf("record is read-only")
	}

	// Check if already deprecated or superseded
	if record.Status == StatusDeprecated {
		c.Formatter.FormatError(fmt.Sprintf("Record %s is already deprecated", opts.RecordID))
		return fmt.Errorf("already deprecated")
	}

	if record.Status == StatusSuperseded {
		c.Formatter.FormatError(fmt.Sprintf("Record %s is superseded and cannot be deprecated", opts.RecordID))
		return fmt.Errorf("already superseded")
	}

	// Snapshot the record file before mutating so a rejected auto-commit can be
	// rolled back, keeping the status change atomic with its commit.
	snaps, err := snapshotFiles(record.FilePath)
	if err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to snapshot record state: %v", err))
		return err
	}
	origStatus := record.Status

	// Update status
	record.Status = StatusDeprecated

	// TODO: Add deprecation_reason field to Record struct if reason is provided

	// Save record
	if err := c.Loader.SaveRecord(record); err != nil {
		record.Status = origStatus
		restoreFiles(snaps)
		c.Formatter.FormatError(fmt.Sprintf("Failed to save record: %v", err))
		return err
	}

	if c.Config.CommitOnStatusChange {
		msg := fmt.Sprintf("Deprecate provenance record [%s]", opts.RecordID)
		if err := c.Git.CommitRecord(msg, record.FilePath); err != nil {
			record.Status = origStatus
			restoreFiles(snaps)
			if uerr := c.Git.Unstage(snapshotPaths(snaps)...); uerr != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to unstage during rollback: %v\n", uerr)
			}
			c.Formatter.FormatError(fmt.Sprintf(
				"Cannot deprecate %s: the auto-commit was rejected:\n\n    %v\n\n"+
					"  The transition was rolled back — %s is unchanged (still %s) and nothing was staged.\n"+
					"  Resolve the issue above (e.g. a failing pre-commit hook) and run 'linespec provenance deprecate' again.",
				opts.RecordID, err, opts.RecordID, origStatus,
			))
			return err
		}
	}

	fmt.Fprintf(os.Stdout, "\n✓ %s marked as deprecated\n\n", opts.RecordID)

	// Deprecating gives up the record's exclusive claim on the write access its
	// scope granted while open (prov-2026-8d2f5f2a) — re-lock any of its scope
	// paths no other still-open allowlist record covers.
	c.relockClosedScope(record)

	return nil
}

// OpenOptions holds options for the open command
type OpenOptions struct {
	RecordID   string
	ConfigFile string // Path to custom .linespec.yml file
}

// Open transitions a record from draft to open, enabling enforcement
func (c *Commands) Open(opts OpenOptions) error {
	record, exists := c.Loader.GetRecord(opts.RecordID)
	if !exists {
		c.Formatter.FormatError(fmt.Sprintf("Record not found: %s", opts.RecordID))
		return fmt.Errorf("record not found")
	}

	if c.isRemoteRecord(record) {
		c.Formatter.FormatError(fmt.Sprintf(
			"Cannot modify %s: this record is owned by a remote repository and is read-only from this repo.",
			opts.RecordID,
		))
		return fmt.Errorf("record is read-only")
	}

	if record.Status != StatusDraft {
		c.Formatter.FormatError(fmt.Sprintf(
			"Cannot open %s: record status is %q (open only transitions from draft).\n\n  Use 'linespec provenance complete' to mark an open record as implemented.",
			opts.RecordID, record.Status,
		))
		return fmt.Errorf("record is not in draft status")
	}

	// Snapshot the record file before mutating so a failed validation or rejected
	// commit can leave it exactly as it was.
	snaps, err := snapshotFiles(record.FilePath)
	if err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to snapshot record state: %v", err))
		return err
	}
	origStatus := record.Status

	// Snapshot the permission bits MaterializeScope is about to flip (the fswrite
	// blueprint, prov-2026-8d2f5f2a) so a rejected transition leaves permissions
	// untouched, exactly like the file-content snapshot above.
	permTargets := materializeTargets(c.RepoRoot, record.AffectedScope)
	permSnaps := snapshotPerms(permTargets...)

	rollback := func(unstage bool) {
		record.Status = origStatus
		restoreFiles(snaps)
		restorePerms(permSnaps)
		if unstage {
			if err := c.Git.Unstage(snapshotPaths(snaps)...); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to unstage during rollback: %v\n", err)
			}
		}
	}

	record.Status = StatusOpen

	if err := c.Loader.SaveRecord(record); err != nil {
		rollback(false)
		c.Formatter.FormatError(fmt.Sprintf("Failed to save record: %v", err))
		return err
	}

	// Validate the opened record before persisting the change, unconditionally —
	// regardless of commit_on_status_change. Opening enables scope and spec
	// enforcement; refuse to transition into a state that does not pass lint.
	if c.Linter != nil {
		if result := c.Linter.LintRecord(record.ID); result.HasErrors() {
			rollback(false)
			c.Formatter.FormatError(fmt.Sprintf(
				"Cannot open %s: the record does not pass validation:\n\n%s\n"+
					"  The transition was rolled back — %s is unchanged (still %s).\n"+
					"  Fix the errors above and run 'linespec provenance open' again.",
				opts.RecordID, formatLintErrors(result), opts.RecordID, origStatus,
			))
			return fmt.Errorf("record failed validation")
		}
	}

	// Materialize write permission for the record's declared scope as part of the
	// same atomic operation that opens it (prov-2026-8d2f5f2a, constraint 3):
	// declaration and permission move together, and a rejected transition below
	// rolls the chmod back via permSnaps.
	if err := MaterializeScope(c.RepoRoot, record.AffectedScope); err != nil {
		rollback(false)
		c.Formatter.FormatError(fmt.Sprintf(
			"Cannot open %s: failed to materialize write permission for its scope: %v\n\n"+
				"  The transition was rolled back — %s is unchanged (still %s).",
			opts.RecordID, err, opts.RecordID, origStatus,
		))
		return err
	}

	if c.Config.CommitOnStatusChange {
		msg := fmt.Sprintf("Open provenance record %s [%s]", opts.RecordID, opts.RecordID)
		if err := c.Git.CommitRecord(msg, record.FilePath); err != nil {
			rollback(true)
			c.Formatter.FormatError(fmt.Sprintf(
				"Cannot open %s: the auto-commit was rejected:\n\n    %v\n\n"+
					"  The transition was rolled back — %s is unchanged (still %s) and nothing was staged.\n"+
					"  Resolve the issue above (e.g. a failing pre-commit hook) and run 'linespec provenance open' again.",
				opts.RecordID, err, opts.RecordID, origStatus,
			))
			return err
		}
	}

	fmt.Fprintf(os.Stdout, "\n✓ %s transitioned from draft → open\n\n", opts.RecordID)
	fmt.Fprintln(os.Stdout, "  Scope and spec enforcement now apply to this record.")
	fmt.Fprintln(os.Stdout)

	// Lifecycle-time governance-overlap heads-up (non-blocking). Before any files
	// have changed, surface sealed records whose scope this record's declared scope
	// overlaps, so the author knows their specs will be verified at completion.
	if overlaps := c.declaredOverlapSealedRecords(record); len(overlaps) > 0 {
		fmt.Fprintf(c.Formatter.Output,
			"ℹ Governance overlap (non-blocking): this record's declared scope overlaps sealed record(s):\n\n%s\n"+
				"  At completion, their specs will be run against your actual changes to confirm nothing broke.\n\n",
			formatRecordList(overlaps))
	}

	return nil
}

// RunSpecsOptions holds options for the run-specs command
type RunSpecsOptions struct {
	RecordID   string
	ConfigFile string
}

// RunSpecs runs the associated_specs for a provenance record.
// It is called by the pre-commit hook when a record transitions from open to implemented.
// If run_associated_specs_on_complete is not enabled in config, it exits silently.
func (c *Commands) RunSpecs(opts RunSpecsOptions) error {
	if !c.Config.RunAssociatedSpecsOnComplete {
		return nil
	}

	record, exists := c.Loader.GetRecord(opts.RecordID)
	if !exists {
		return fmt.Errorf("record not found: %s", opts.RecordID)
	}

	if len(record.AssociatedSpecs) == 0 {
		return nil
	}

	fmt.Fprintf(os.Stdout, "\nRunning %d associated spec(s) for %s...\n\n", len(record.AssociatedSpecs), record.ID)

	_, failed, _ := c.runRecordSpecs(record, nil)
	if failed {
		return fmt.Errorf("one or more associated specs failed — commit blocked")
	}

	fmt.Fprintf(os.Stdout, "All associated specs passed.\n\n")
	return nil
}

// SpecOutcome records the actual executed result of one associated_spec — used by
// FormatCompleteSuccess to report a real pass/fail/skip outcome instead of merely
// checking that the spec's path exists on disk.
type SpecOutcome struct {
	Path   string
	Status string // "passed", "failed", or "skipped"
}

// specGroupEntry pairs an AssociatedSpec with its resolved (non-batched) single-path
// command, so the group it lands in can be executed together while each entry still
// carries the exact command it would have run standalone.
type specGroupEntry struct {
	spec   AssociatedSpec
	cmdStr string
}

// runRecordSpecs executes the associated_specs of a single record, streaming each
// command's output. It returns ran (whether the record has any executable spec),
// failed (whether any spec it ran exited non-zero), and the per-spec outcomes. It
// deliberately does NOT consult run_associated_specs_on_complete or look at record
// status — the caller decides when to invoke it, so it can verify an arbitrary
// record's specs on demand.
//
// Consecutive entries whose effective command is identical (prov-2026-3ee1f3c3) are
// batched into a single process with all their paths appended, instead of one process
// per entry — this is what avoids paying a full framework boot (e.g. Rails/rspec) once
// per spec when several specs share a runner. Grouping never reorders entries and never
// merges a {{path}}-templated run_command, which has no multi-path form.
//
// When seen is non-nil it is used to de-duplicate identical spec commands across
// calls: a command already present in seen is treated as already-verified and not
// re-run (so completing a change to a widely-governed file does not run `make test`
// dozens of times). The seen key is always each entry's own single-path command, never
// the batched command string, so whether a path was already verified does not depend
// on which neighbors it happened to batch with in this or another record. A record
// still counts as ran if it has a runnable spec, even when that spec's command was
// already executed for another record.
func (c *Commands) runRecordSpecs(record *Record, seen map[string]bool) (ran bool, failed bool, outcomes []SpecOutcome) {
	specs := record.AssociatedSpecs
	for i := 0; i < len(specs); {
		spec := specs[i]
		cmdStr, skip, err := buildSpecCommand(spec)
		if err != nil {
			fmt.Fprintf(os.Stdout, "  · %s  ✗ could not build command: %v\n", spec.Path, err)
			failed = true
			outcomes = append(outcomes, SpecOutcome{Path: spec.Path, Status: "failed"})
			i++
			continue
		}
		if skip {
			fmt.Fprintf(os.Stdout, "  · %s  (skipped — no type or run_command)\n", spec.Path)
			outcomes = append(outcomes, SpecOutcome{Path: spec.Path, Status: "skipped"})
			i++
			continue
		}

		base, templated, _ := specCommandBase(spec)
		group := []specGroupEntry{{spec, cmdStr}}
		j := i + 1
		if !templated {
			for j < len(specs) {
				nextCmd, nextSkip, nextErr := buildSpecCommand(specs[j])
				if nextErr != nil || nextSkip {
					break
				}
				nextBase, nextTemplated, _ := specCommandBase(specs[j])
				if nextTemplated || nextBase != base {
					break
				}
				group = append(group, specGroupEntry{specs[j], nextCmd})
				j++
			}
		}

		ran = true

		var toRun []specGroupEntry
		for _, g := range group {
			if seen != nil && seen[g.cmdStr] {
				fmt.Fprintf(os.Stdout, "  · %s  (already verified)\n", g.cmdStr)
				outcomes = append(outcomes, SpecOutcome{Path: g.spec.Path, Status: "passed"})
				continue
			}
			toRun = append(toRun, g)
		}

		if len(toRun) == 0 {
			i = j
			continue
		}
		if seen != nil {
			for _, g := range toRun {
				seen[g.cmdStr] = true
			}
		}

		if len(toRun) == 1 {
			g := toRun[0]
			fmt.Fprintf(os.Stdout, "  · %s\n    %s\n", g.spec.Path, g.cmdStr)
			if runErr := runShellCommand(g.cmdStr); runErr != nil {
				fmt.Fprintf(os.Stdout, "    ✗ failed\n\n")
				failed = true
				outcomes = append(outcomes, SpecOutcome{Path: g.spec.Path, Status: "failed"})
			} else {
				fmt.Fprintf(os.Stdout, "    ✓ passed\n\n")
				outcomes = append(outcomes, SpecOutcome{Path: g.spec.Path, Status: "passed"})
			}
			i = j
			continue
		}

		// Batched: run every path in this group as one process. Per-entry ✓/✗
		// reporting is still one line per path — on a batch failure, re-run each
		// path's own single-path command to localize which one(s) actually failed,
		// rather than reporting the whole group as failed undifferentiated.
		paths := make([]string, len(toRun))
		for k, g := range toRun {
			paths[k] = g.spec.Path
		}
		batchCmd := base + " " + strings.Join(paths, " ")
		fmt.Fprintf(os.Stdout, "  · %s\n    %s\n", strings.Join(paths, ", "), batchCmd)

		if runErr := runShellCommand(batchCmd); runErr == nil {
			for _, g := range toRun {
				fmt.Fprintf(os.Stdout, "    ✓ %s passed\n", g.spec.Path)
				outcomes = append(outcomes, SpecOutcome{Path: g.spec.Path, Status: "passed"})
			}
			fmt.Fprintln(os.Stdout)
		} else {
			// The batch itself failed: mark the record failed regardless of what the
			// per-path localization below finds, so an order-dependent or shared-state
			// failure that only reproduces when paths run together can never be masked
			// by every path passing when re-run standalone (prov-2026-3ee1f3c3).
			failed = true
			fmt.Fprintf(os.Stdout, "    ✗ batch failed — localizing per spec:\n")
			for _, g := range toRun {
				if singleErr := runShellCommand(g.cmdStr); singleErr != nil {
					fmt.Fprintf(os.Stdout, "    ✗ %s failed\n", g.spec.Path)
					outcomes = append(outcomes, SpecOutcome{Path: g.spec.Path, Status: "failed"})
				} else {
					fmt.Fprintf(os.Stdout, "    ✓ %s passed\n", g.spec.Path)
					outcomes = append(outcomes, SpecOutcome{Path: g.spec.Path, Status: "passed"})
				}
			}
			fmt.Fprintln(os.Stdout)
		}

		i = j
	}
	return ran, failed, outcomes
}

// runShellCommand runs cmdStr through sh -c, streaming its output to the process's
// own stdout/stderr, and reports whether it exited non-zero.
func runShellCommand(cmdStr string) error {
	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// overlappingSealedRecords returns implemented (sealed) records — other than self —
// whose scope contains at least one of changedFiles. This is the internal overlap
// selector: membership is computed from the files a change ACTUALLY touched, not
// from glob-pattern-vs-glob-pattern intersection.
func (c *Commands) overlappingSealedRecords(self *Record, changedFiles []string) []*Record {
	var out []*Record
	for _, r := range c.Loader.Records {
		// Remote (shared_repos cache) records are skipped: their associated_specs
		// paths are relative to the origin repo and may not resolve here, and they
		// cannot be superseded locally — so they must not gate local completion.
		if r.ID == self.ID || r.Status != StatusImplemented || r.SealedAtSHA == "" || c.isRemoteRecord(r) {
			continue
		}
		for _, f := range changedFiles {
			if inScope, err := r.IsInScope(f); err == nil && inScope {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// completionOverlapSelector returns the sealed records the completing record
// actually touched, computed from the union of files across its tagged commits.
// Returns nil when git history is unavailable or the record changed nothing.
func (c *Commands) completionOverlapSelector(self *Record) []*Record {
	if c.Git == nil {
		return nil
	}
	commits, err := c.Git.GetCommitsForRecord(self.ID)
	if err != nil || len(commits) == 0 {
		return nil
	}
	changed, err := c.Git.GetFilesChangedInCommits(commits)
	if err != nil || len(changed) == 0 {
		return nil
	}
	return c.overlappingSealedRecords(self, changed)
}

// declaredOverlapSealedRecords returns sealed records whose scope overlaps the
// given record's DECLARED affected_scope (glob intersection). Used at open time —
// before any files have changed — for an early non-blocking heads-up.
func (c *Commands) declaredOverlapSealedRecords(self *Record) []*Record {
	var out []*Record
	for _, r := range c.Loader.Records {
		// Skip remote (shared_repos cache) records — see overlappingSealedRecords.
		if r.ID == self.ID || r.Status != StatusImplemented || r.SealedAtSHA == "" || c.isRemoteRecord(r) {
			continue
		}
		if scopesOverlap(self, r) {
			out = append(out, r)
		}
	}
	return out
}

// scopesOverlap reports whether two records' affected_scope patterns could match a
// common file. This is the glob-overlap computation, retained as an internal
// selector rather than a surfaced lint warning.
func scopesOverlap(a, b *Record) bool {
	for _, pa := range a.AffectedScope {
		for _, pb := range b.AffectedScope {
			if patternsOverlap(pa, pb) {
				return true
			}
		}
	}
	return false
}

// formatRecordList renders records as an indented "· ID — Title" bullet list.
func formatRecordList(records []*Record) string {
	var b strings.Builder
	for _, r := range records {
		fmt.Fprintf(&b, "    · %s — %s\n", r.ID, r.Title)
	}
	return b.String()
}

// buildSpecCommand returns the shell command string to run for a given AssociatedSpec.
// If the spec has no type and no run_command, skip is true and cmdStr is empty.
func buildSpecCommand(spec AssociatedSpec) (cmdStr string, skip bool, err error) {
	base, templated, skip := specCommandBase(spec)
	if skip {
		return "", true, nil
	}
	if templated {
		return strings.ReplaceAll(spec.RunCommand, "{{path}}", spec.Path), false, nil
	}
	return base + " " + spec.Path, false, nil
}

// specCommandBase returns the effective command for spec with its path omitted (base),
// so callers can compare two specs' commands for equality without the path baked in —
// this is what lets runRecordSpecs batch consecutive same-command entries. templated
// reports whether spec's run_command contains a literal {{path}} placeholder, which has
// no multi-path form and so is never grouped with neighbors (base is "" in that case).
// skip mirrors buildSpecCommand's skip: true when spec has neither run_command nor a
// known type.
func specCommandBase(spec AssociatedSpec) (base string, templated bool, skip bool) {
	if spec.RunCommand != "" {
		if strings.Contains(spec.RunCommand, "{{path}}") {
			return "", true, false
		}
		return spec.RunCommand, false, false
	}

	switch spec.Type {
	case "linespec":
		if _, statErr := os.Stat("./linespec"); statErr == nil {
			return "./linespec test", false, false
		}
		return "linespec test", false, false
	case "rspec":
		return "bundle exec rspec", false, false
	case "pytest":
		return "pytest", false, false
	case "jest":
		return "npx jest", false, false
	default:
		// Unknown type or no type — skip with a warning rather than hard-fail,
		// since the user may have non-executable proof artifacts (e.g. type: config).
		return "", false, true
	}
}

// InstallHooks installs git hooks
func (c *Commands) InstallHooks() error {
	hooksDir := filepath.Join(c.RepoRoot, ".git", "hooks")

	// Create pre-commit hook (lints records; runs associated specs on completion transitions)
	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	preCommitContent := `#!/bin/sh
# LineSpec provenance pre-commit hook
# Supports multi-pack repos (e.g. packwerk): for each staged provenance record,
# walks up the directory tree to find the nearest .linespec.yml and lints using it.
# When a record transitions open → implemented, runs its associated_specs if
# run_associated_specs_on_complete is enabled in the nearest .linespec.yml.

# Use the local linespec binary if it exists, otherwise fall back to system
if [ -f "./linespec" ]; then
    LINESPEC="./linespec"
else
    LINESPEC="linespec"
fi

# Get staged files that look like provenance records (prov-YYYY-* pattern)
staged_prov=$(git diff --cached --name-only | grep -E "prov-[0-9]{4}-" | grep -E "\.ya?ml$")

if [ -z "$staged_prov" ]; then
    exit 0
fi

exit_code=0

# Walk up the directory tree from a file path to find the nearest .linespec.yml.
# Echoes the config path if found; returns 1 if not found.
find_nearest_config() {
    dir=$(dirname "$1")
    while true; do
        if [ -f "$dir/.linespec.yml" ]; then
            echo "$dir/.linespec.yml"
            return 0
        fi
        if [ "$dir" = "." ] || [ "$dir" = "/" ]; then
            return 1
        fi
        dir=$(dirname "$dir")
    done
}

for file in $staged_prov; do
    record=$(basename "$file" | sed -E 's/\.ya?ml$//')
    config=$(find_nearest_config "$file")

    if [ -n "$config" ]; then
        $LINESPEC provenance lint -c "$config" --record "$record"
    else
        $LINESPEC provenance lint --record "$record"
    fi

    if [ $? -ne 0 ]; then
        echo "Commit blocked due to lint errors in $record"
        exit_code=1
    fi

    # Detect open → implemented completion transition.
    # git show returns non-zero if the file didn't exist at HEAD (new record), so we
    # default head_status to empty string in that case — the transition won't trigger.
    staged_status=$(git show ":$file" 2>/dev/null | grep "^status:" | head -1 | awk '{print $2}')
    head_status=$(git show "HEAD:$file" 2>/dev/null | grep "^status:" | head -1 | awk '{print $2}')

    if [ "$head_status" = "open" ] && [ "$staged_status" = "implemented" ]; then
        if [ -n "$config" ]; then
            $LINESPEC provenance run-specs -c "$config" --record "$record"
        else
            $LINESPEC provenance run-specs --record "$record"
        fi

        if [ $? -ne 0 ]; then
            exit_code=1
        fi
    fi
done

exit $exit_code
`

	if err := os.WriteFile(preCommitPath, []byte(preCommitContent), 0755); err != nil {
		return fmt.Errorf("failed to write pre-commit hook: %w", err)
	}

	// Create commit-msg hook (checks scope)
	commitMsgPath := filepath.Join(hooksDir, "commit-msg")
	commitMsgContent := `#!/bin/sh
# LineSpec provenance commit-msg hook
# Supports multi-pack repos (e.g. packwerk): scans for all .linespec.yml files and
# runs scope-check for each one that is relevant to the staged changes.
# A non-root config is considered relevant only when staged files exist under its directory.

# Use the local linespec binary if it exists, otherwise fall back to system
if [ -f "./linespec" ]; then
    LINESPEC="./linespec"
else
    LINESPEC="linespec"
fi

COMMIT_MSG_FILE="$1"

# Find all .linespec.yml files in the repo (excluding .git)
configs=$(find . -name ".linespec.yml" -not -path "./.git/*" 2>/dev/null | sort)

exit_code=0

if [ -z "$configs" ]; then
    # No config files found; use default (no -c flag)
    $LINESPEC provenance check --staged --message-file "$COMMIT_MSG_FILE"
    if [ $? -ne 0 ]; then
        echo ""
        echo "Commit blocked due to provenance scope violations"
        exit 1
    fi
    exit 0
fi

for config in $configs; do
    config_dir=$(dirname "$config" | sed 's|^\./||')

    # Non-root configs only apply when staged files exist under their directory.
    # This prevents spurious commit_tag_required failures from unrelated pack configs.
    if [ "$config_dir" != "." ]; then
        has_staged=$(git diff --cached --name-only | grep "^$config_dir/" | head -1)
        if [ -z "$has_staged" ]; then
            continue
        fi
    fi

    $LINESPEC provenance check --staged --message-file "$COMMIT_MSG_FILE" -c "$config"
    if [ $? -ne 0 ]; then
        exit_code=1
    fi
done

if [ $exit_code -ne 0 ]; then
    echo ""
    echo "Commit blocked due to provenance scope violations"
fi

exit $exit_code
`

	if err := os.WriteFile(commitMsgPath, []byte(commitMsgContent), 0755); err != nil {
		return fmt.Errorf("failed to write commit-msg hook: %w", err)
	}

	fmt.Fprintf(os.Stdout, "\n✓ Installed git hooks to %s\n\n", hooksDir)
	fmt.Fprintln(os.Stdout, "  pre-commit hook:")
	fmt.Fprintln(os.Stdout, "    · Lints modified provenance records")
	fmt.Fprintln(os.Stdout, "    · Uses nearest .linespec.yml for each record (multi-pack aware)")
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "  commit-msg hook:")
	fmt.Fprintln(os.Stdout, "    · Checks staged files against provenance scope")
	fmt.Fprintln(os.Stdout, "    · Validates provenance IDs in commit message")
	fmt.Fprintln(os.Stdout, "    · Runs per-pack config for relevant staged changes (multi-pack aware)")
	fmt.Fprintln(os.Stdout)

	return nil
}

// InstallPluginOptions holds options for the install-plugin command.
type InstallPluginOptions struct {
	Path string // target plugins directory relative to repo root (default: .claude/plugins)
}

// InstallPlugin extracts the embedded Claude Code provenance plugin into the target
// config directory (default <repo>/.claude/plugins/linespec-provenance), preserving
// directory structure and re-applying the executable bit to *.sh hook scripts (which
// embed.FS drops). It writes ONLY the plugin's own files — it never edits or clobbers
// unrelated user Claude Code config.
func (c *Commands) InstallPlugin(opts InstallPluginOptions) error {
	base := opts.Path
	if base == "" {
		base = ".claude/plugins"
	}
	// A relative --path is resolved against the repo root; an absolute --path is used
	// as-is (so callers can install into an arbitrary config dir).
	dest := filepath.Join(base, "linespec-provenance")
	if !filepath.IsAbs(base) {
		dest = filepath.Join(c.RepoRoot, base, "linespec-provenance")
	}

	err := fs.WalkDir(plugin.Files, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		target := filepath.Join(dest, path)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := plugin.Files.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if strings.HasSuffix(path, ".sh") {
			mode = 0755
		}
		return os.WriteFile(target, data, mode)
	})
	if err != nil {
		return fmt.Errorf("failed to install plugin: %w", err)
	}

	fmt.Fprintf(os.Stdout, "✓ Installed plugin 'linespec-provenance' to %s\n", dest)
	fmt.Fprintf(os.Stdout, "\nIt adds three hooks (session-start guidance, per-edit governance, commit remediation),\n")
	fmt.Fprintf(os.Stdout, "all rendered from `linespec provenance`. Enable it in your Claude Code settings,\n")
	fmt.Fprintf(os.Stdout, "or install via the marketplace instead:\n")
	fmt.Fprintf(os.Stdout, "  claude plugin marketplace add <repo>/plugins/provenance\n")
	fmt.Fprintf(os.Stdout, "  claude plugin install linespec-provenance@linespec\n\n")
	return nil
}

// InstallSkillsOptions holds options for the install-skills command
type InstallSkillsOptions struct {
	Path string // Target directory relative to repo root (default: .claude/skills)
}

// InstallSkills copies all LineSpec Claude Code skills into a skills directory.
// Existing skill directories are overwritten silently.
func (c *Commands) InstallSkills(opts InstallSkillsOptions) error {
	targetDir := opts.Path
	if targetDir == "" {
		targetDir = ".claude/skills"
	}

	skills := []struct {
		name    string
		content string
	}{
		{"provenance", provenanceSkillMD},
		{"linespec-testing", linespecTestingSkillMD},
	}

	for _, skill := range skills {
		destDir := filepath.Join(c.RepoRoot, targetDir, skill.name)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", destDir, err)
		}
		skillPath := filepath.Join(destDir, "SKILL.md")
		if err := os.WriteFile(skillPath, []byte(skill.content), 0644); err != nil {
			return fmt.Errorf("failed to write %s/SKILL.md: %w", skill.name, err)
		}
		fmt.Fprintf(os.Stdout, "✓ Installed skill '%s' to %s\n", skill.name, destDir)
	}

	fmt.Fprintf(os.Stdout, "\nInvoke in Claude Code with: /provenance  or  /linespec-testing\n\n")
	return nil
}

const provenanceSkillMD = `---
name: provenance
description: Provenance record workflow rules for LineSpec. Governs how to create, complete, and supersede provenance records, and how to include record IDs in commits.
when_to_use: "When starting any new work, modifying files covered by provenance records, completing a feature, superseding or deprecating records, or when asked about the provenance workflow."
---

# Provenance Record Workflow

Follow these rules precisely whenever working with provenance records or making code changes in this repo.

## TL;DR — the happy path

Before touching anything, follow this sequence and you will not get stuck on enforcement:

1. **Investigate.** Run ` + "`" + `linespec provenance next --plan <file>...` + "`" + ` for every file you plan to change. It computes the single correct next action — with record IDs already filled in — so start here, not with a manual read of the graph. (Governing records ` + "`" + `next` + "`" + ` surfaces do **not** need to be superseded — see below.)
2. **Create one record:** ` + "`" + `linespec provenance create --type blueprint --no-edit --title "…"` + "`" + `.
3. **Set its ` + "`" + `affected_scope` + "`" + `** to exactly the files you will change.
4. **Create your proof artifact, then ` + "`" + `open` + "`" + `** the record with that spec referenced in ` + "`" + `associated_specs` + "`" + `.
5. **Make changes; commit** tagged with the record ID ` + "`" + `[prov-YYYY-XXXXXXXX]` + "`" + `.
6. **Show proof, then ` + "`" + `complete` + "`" + `.**

## Step 1 — Investigate Before Creating

**Always investigate existing provenance context before writing a single line of code or creating a new record.** Records capture design decisions, scope constraints, and rationale that aren't in the code — and grep/find/cat cannot see any of it: they can't tell you which records govern a file, whether a file is exempted, what the ancestry of a decision is, or whether two open records conflict over the same file. **Always use ` + "`" + `linespec provenance` + "`" + ` commands for provenance investigation; never fall back to raw bash grep/find/cat to infer governance.**

**Start with ` + "`" + `next` + "`" + `** — it computes the single correct next provenance action for the files you're about to touch, with record IDs already filled in, so you don't have to manually reason through the state machine:

` + "`" + `` + "`" + `` + "`" + `bash
linespec provenance next --plan <file>... [-c <config>]   # or: next [files...]
` + "`" + `` + "`" + `` + "`" + `

It tells you, precisely: create a new record, open an existing draft, add specs before opening, commit under an existing ID, or complete — with the exact command to run next. Run this before you write a line of code or create a record.

For a lighter-weight lookup — just the active records currently governing a set of files, without the full graph context — use ` + "`" + `govern` + "`" + `:

` + "`" + `` + "`" + `` + "`" + `bash
linespec provenance govern --files <file>... [-c <config>]   # or: govern [files...]
` + "`" + `` + "`" + `` + "`" + `

` + "`" + `govern` + "`" + ` returns only **open + implemented** records (cache-backed, like ` + "`" + `next` + "`" + `). Reach for it when you already know what you're doing and just need to confirm what currently governs the files you're about to change.

For the full picture — all statuses, ancestry, and any cross-record conflicts on your files — use ` + "`" + `context` + "`" + `:

` + "`" + `` + "`" + `` + "`" + `bash
linespec provenance context <file>... [-c <config>]
` + "`" + `` + "`" + `` + "`" + `

If embeddings are configured, search semantically:

` + "`" + `` + "`" + `` + "`" + `bash
linespec provenance search --query "<description of the work or feature area>" [-c <config>]
` + "`" + `` + "`" + `` + "`" + `

Look up a record by ID with ` + "`" + `status --record` + "`" + ` (this also finds remote records from the shared_repos cache):

` + "`" + `` + "`" + `` + "`" + `bash
linespec provenance status --record prov-YYYY-XXXXXXXX [-c <config>]
` + "`" + `` + "`" + `` + "`" + `

**Important:** discovering that several records govern your files does NOT mean you must supersede them. The scope check only validates the record you tag. You will create one new record covering your files (Step 2). See "Scope Enforcement & When You're Blocked" below.

### Bootstrapping provenance on an existing codebase — ` + "`" + `discover` + "`" + `

` + "`" + `discover` + "`" + ` scans a codebase with tree-sitter and generates **draft** blueprint records plus ` + "`" + `.linespec` + "`" + ` stubs, so you don't hand-author provenance from a blank page for code that predates it:

` + "`" + `` + "`" + `` + "`" + `bash
linespec provenance discover [--dir <path>] [--lang <lang>] [--framework <name>] [--dry-run] [--enrich] [--format table|json] [-c <config>]
` + "`" + `` + "`" + `` + "`" + `

- ` + "`" + `--dir` + "`" + ` — scope the scan to a subdirectory instead of the repo root (useful in monorepos).
- ` + "`" + `--lang` + "`" + ` / ` + "`" + `--framework` + "`" + ` — override auto-detection when the codebase's dependency manifests don't make it obvious.
- ` + "`" + `--dry-run` + "`" + ` — print what would be generated (routes, boundaries, records) without writing any files; pair with ` + "`" + `--format json` + "`" + ` for machine-readable output.
- ` + "`" + `--enrich` + "`" + ` — populate ` + "`" + `intent` + "`" + ` fields from git history instead of leaving them as placeholders.
- Supported frameworks: Chi (Go), and Rails/Sinatra (Ruby).

It emits **draft** blueprint records under ` + "`" + `provenance/` + "`" + ` and skeleton ` + "`" + `.linespec` + "`" + ` files under ` + "`" + `linespecs/` + "`" + ` — a starting point, not finished output. Review and refine every generated record and spec (fill in real ` + "`" + `intent` + "`" + `/` + "`" + `constraints` + "`" + `, correct any misdetected routes) before opening them; ` + "`" + `discover` + "`" + ` never overwrites existing records or specs.

## Step 2 — Create a Blueprint Record (Draft)

Create a ` + "`" + `blueprint` + "`" + ` record to capture the scope and success criteria **before writing any code**:

` + "`" + `` + "`" + `` + "`" + `bash
linespec provenance create --title "..." --type blueprint --no-edit [-c <config>]
` + "`" + `` + "`" + `` + "`" + `

Valid types: ` + "`" + `brief` + "`" + `, ` + "`" + `blueprint` + "`" + `, ` + "`" + `bug` + "`" + `, ` + "`" + `imprint` + "`" + `.

**Always pass ` + "`" + `--no-edit` + "`" + `.** Omitting it opens an interactive editor that hangs in non-TTY environments.

Fill in ` + "`" + `intent` + "`" + `, ` + "`" + `constraints` + "`" + `, and ` + "`" + `affected_scope` + "`" + ` as needed. Draft mode is flexible — add, remove, and adjust fields freely while planning with the user. Commit the draft in a standalone commit, then **present it to the user for review.** Do not write implementation code until the user confirms.

## Step 3 — Open the Blueprint (After User Confirmation)

` + "`" + `` + "`" + `` + "`" + `bash
linespec provenance open --record prov-YYYY-XXXXXXXX [-c <config>]
` + "`" + `` + "`" + `` + "`" + `

Add ` + "`" + `affected_scope` + "`" + ` and ` + "`" + `associated_specs` + "`" + ` at this point (create the proof files first — see associated_specs below). Commit the open transition as a standalone commit.

## Step 4 — Implement with Imprint Records

As you work, create ` + "`" + `imprint` + "`" + ` records to log micro-decisions, trade-offs, pivots, and learnings. An imprint must set ` + "`" + `implements` + "`" + ` pointing at the parent blueprint:

` + "`" + `` + "`" + `` + "`" + `yaml
type: imprint
implements: prov-YYYY-XXXXXXXX   # the blueprint ID
` + "`" + `` + "`" + `` + "`" + `

**Write and commit the imprint BEFORE writing the code it documents, not after.** An imprint captures the decision you're about to make — the trade-off, the pivot, the reasoning — so it must exist before the commit that acts on it, the same way the blueprint must exist before any implementation code. Retroactively writing an imprint after the fact turns it into a summary instead of a record of the decision, and defeats the purpose of provenance as a decision log.

**All imprints must be implemented before the blueprint can be completed.** Tag every implementation commit with the relevant record ID.

## Step 5 — Show Proof and Complete the Blueprint

Verify all imprints are implemented, **show the user the proof** (test/lint output, working commands), and **ask for explicit permission before completing.** Then:

` + "`" + `` + "`" + `` + "`" + `bash
linespec provenance complete --record prov-YYYY-XXXXXXXX [-c <config>]
` + "`" + `` + "`" + `` + "`" + `

## Commit Message Format

Every commit (except standalone provenance management commits) must include the governing record ID in square brackets:

` + "`" + `` + "`" + `` + "`" + `
Short description of what changed [prov-YYYY-XXXXXXXX]
` + "`" + `` + "`" + `` + "`" + `

The pre-commit hook enforces this when ` + "`" + `commit_tag_required: true` + "`" + ` is set in ` + "`" + `.linespec.yml` + "`" + `.

## Pre-Commit Checks

Before any create, open, or complete commit: ` + "`" + `linespec provenance lint` + "`" + ` and ` + "`" + `linespec provenance check` + "`" + `. Before each implementation commit: ` + "`" + `linespec provenance check --staged` + "`" + `. Always include ` + "`" + `-c <path>` + "`" + ` when the relevant ` + "`" + `.linespec.yml` + "`" + ` is not at the repo root.

## Scope Enforcement & When You're Blocked

**The single most important rule:** the pre-commit scope check validates your changed files **only against the record you tag** in the commit message. It does **not** consult other records that happen to govern those files. So implemented/sealed records whose ` + "`" + `affected_scope` + "`" + ` overlaps your files do **not** block your commit and do **not** need to be superseded. Create **one** new record covering exactly your files, open it, and tag your commits with it. Supersede a record *only* when you are deliberately revising the decision it captured.

### Scope modes

- A record with an **empty ` + "`" + `affected_scope` + "`" + `** is **observed** — its check permits any file (except ` + "`" + `forbidden_scope` + "`" + `).
- A record with a **non-empty ` + "`" + `affected_scope` + "`" + `** is **allowlist** — it permits only files matching that scope. So a "scope violation" on *your tagged record* means its ` + "`" + `affected_scope` + "`" + ` is missing one of your changed files → widen that record's scope (free to edit while ` + "`" + `draft` + "`" + `), don't touch other records.
- ` + "`" + `lock-scope` + "`" + ` auto-populates a record's ` + "`" + `affected_scope` + "`" + ` from the files it changed in git. ` + "`" + `lock-layer` + "`" + ` creates a ` + "`" + `locked` + "`" + ` governance record — advanced and uncommon; only when locked records exist can an overlapping open record hard-fail lint.

### When you're blocked — decision tree

| Message | Do this |
|---------|---------|
| ` + "`" + `Commit tag required but no provenance ID found` + "`" + ` | Tag the commit with your record ID ` + "`" + `[prov-YYYY-XXXXXXXX]` + "`" + `. |
| ` + "`" + `X is already implemented - cannot commit with this ID` + "`" + ` | Implemented records are immutable. Create **one new record** covering your files and tag that — do **not** supersede the records that govern the files. |
| ` + "`" + `forbids changes to <file>` + "`" + ` / scope violation | Add ` + "`" + `<file>` + "`" + ` to **your tagged record's** ` + "`" + `affected_scope` + "`" + ` (editable in draft). |
| ` + "`" + `No associated specs (open) [strict]` + "`" + ` | Add ` + "`" + `associated_specs` + "`" + ` (proof) to the open record before committing/completing. |
| ` + "`" + `overlaps with locked record Y` + "`" + ` | A deliberate governance gate (only if locked layers exist). **Stop and ask the maintainer** — do not blindly supersede multiple records. |
| ` + "`" + `permission denied` + "`" + ` writing/creating a source file | Not a bug — the filesystem write-access lock (see below) keeps ungoverned files read-only on disk. Declare the file in an ` + "`" + `open` + "`" + ` record's ` + "`" + `affected_scope` + "`" + ` (open a draft that covers it, or ` + "`" + `add-scope` + "`" + ` an already-open record) and it unlocks. Never ` + "`" + `chmod` + "`" + ` to force it. |

> **Stale-scope warnings are non-blocking.** When you edit a file governed by an *implemented* record you may see a warning that the file "is governed by implemented record … create a superseding record." This is informational only — the commit still succeeds, no action is required, and it is **not** a reason to supersede anything. Proceed under your own new record.

### Filesystem write-access lock — why a file is read-only

Enforcement also runs at the **filesystem boundary**, so it holds even when git hooks are absent and regardless of which agent (or none) is driving. The governed tree defaults to **non-writable**: a source file is writable **only while an ` + "`" + `open` + "`" + `, allowlist-mode record declares it** in ` + "`" + `affected_scope` + "`" + `. That is why an edit can fail with ` + "`" + `permission denied` + "`" + ` *before* you ever commit — the path simply isn't covered by open work yet. It is the system telling you to declare your scope, not a bug to route around with ` + "`" + `chmod` + "`" + `.

- **Unlock by declaring the path.** ` + "`" + `open` + "`" + ` a draft whose ` + "`" + `affected_scope` + "`" + ` covers the file, or ` + "`" + `add-scope --record <id>` + "`" + ` to widen an already-open record. Declaration and write permission are granted in the same atomic step.
- **Always writable, never locked:** the provenance directory, ` + "`" + `.linespec.yml` + "`" + `, LineSpec's own ` + "`" + `.linespec/` + "`" + ` state dir, and every record's ` + "`" + `associated_specs` + "`" + ` paths — so authoring records and their proof files is never blocked.
- **Re-locked on ` + "`" + `complete` + "`" + `/` + "`" + `deprecate` + "`" + `,** unless another still-open record also covers the path.
- **Self-healing.** ` + "`" + `linespec provenance next` + "`" + ` re-derives the write bits on every run, and ` + "`" + `linespec provenance reconcile` + "`" + ` does it on demand (a fresh clone, or bits that drifted). Running ` + "`" + `next --plan <file>` + "`" + ` first (Step 1) both tells you the correct action and unlocks what your declared work needs — you rarely call ` + "`" + `reconcile` + "`" + ` by hand.

The loop never changes: **investigate with ` + "`" + `next` + "`" + `, declare your scope, and the files you may touch become writable.** A blocked write means "declare this file in an open record," never "bypass enforcement."

**Hard rules:** Never use ` + "`" + `--no-verify` + "`" + `. Never relax enforcement (` + "`" + `strict` + "`" + ` → ` + "`" + `warn` + "`" + `/` + "`" + `none` + "`" + `) to get unblocked — that is a maintainer + settings-level decision, not yours. When a wall is genuinely a governance call, stop and ask rather than brute-forcing.

## Use Commands, Not Manual YAML Edits

Let the CLI update records — hand-editing managed fields corrupts the graph.

| Instead of manually editing… | Use |
|------------------------------|-----|
| ` + "`" + `supersedes` + "`" + ` + the old record's ` + "`" + `superseded_by` + "`" + ` + ` + "`" + `status: superseded` + "`" + ` | ` + "`" + `create --supersedes <old-id>` + "`" + ` (sets all of it and stages both files) |
| ` + "`" + `status: open` + "`" + ` | ` + "`" + `open --record <id>` + "`" + ` |
| ` + "`" + `status: implemented` + "`" + ` + ` + "`" + `sealed_at_sha` + "`" + ` | ` + "`" + `complete --record <id>` + "`" + ` |
| ` + "`" + `status: deprecated` + "`" + ` | ` + "`" + `deprecate --record <id> --reason "…"` + "`" + ` |
| listing changed files into ` + "`" + `affected_scope` + "`" + ` | ` + "`" + `lock-scope --record <id>` + "`" + ` (auto-populates from git) |
| the hash manifest | ` + "`" + `compile` + "`" + ` |

Never hand-edit ` + "`" + `status` + "`" + `, ` + "`" + `superseded_by` + "`" + `, ` + "`" + `sealed_at_sha` + "`" + `, or the hash manifest.

### Superseding records

To supersede an existing record, run ` + "`" + `create --supersedes` + "`" + `:

` + "`" + `` + "`" + `` + "`" + `bash
linespec provenance create --title "Better approach" --supersedes prov-YYYY-XXXXXXXX --no-edit
` + "`" + `` + "`" + `` + "`" + `

This sets ` + "`" + `supersedes` + "`" + ` on the new record AND automatically updates the old record's ` + "`" + `superseded_by` + "`" + ` and ` + "`" + `status: superseded` + "`" + `, staging both files together. Do **not** edit those fields by hand.

## associated_specs — Proof Artifacts

` + "`" + `associated_specs` + "`" + ` attach proof that a record's constraints are met. Each entry:

- **` + "`" + `path` + "`" + `** — required. Must point to a file that *exists* (lint fails otherwise). Any file type: a test, a ` + "`" + `.linespec` + "`" + `, a config, a doc, a screenshot, a log.
- **` + "`" + `type` + "`" + `** — optional. These auto-run with no ` + "`" + `run_command` + "`" + `: ` + "`" + `linespec` + "`" + ` → ` + "`" + `linespec test <path>` + "`" + `, ` + "`" + `rspec` + "`" + ` → ` + "`" + `bundle exec rspec <path>` + "`" + `, ` + "`" + `pytest` + "`" + ` → ` + "`" + `pytest <path>` + "`" + `, ` + "`" + `jest` + "`" + ` → ` + "`" + `npx jest <path>` + "`" + `. Any other type with no ` + "`" + `run_command` + "`" + ` is recorded as proof but **skipped** (not executed).
- **` + "`" + `run_command` + "`" + `** — optional; overrides ` + "`" + `type` + "`" + `. Runs as ` + "`" + `<run_command> <path>` + "`" + ` (path appended) **unless** the command contains ` + "`" + `{{path}}` + "`" + `, which is substituted instead.

**Strict order of operations:** under strict enforcement an open record with no ` + "`" + `associated_specs` + "`" + ` is a hard error, and a referenced spec path that does not exist also fails lint — so create the proof file *first*, then reference it, then ` + "`" + `open` + "`" + `:

` + "`" + `` + "`" + `` + "`" + `yaml
# 1. write the proof file first (e.g. spec/models/user_spec.rb)
# 2. reference it:
associated_specs:
  - path: spec/models/user_spec.rb   # must already exist
    type: rspec                       # auto-runs ` + "`" + `bundle exec rspec <path>` + "`" + `
  - path: linespecs/create_user.linespec
    type: linespec                    # auto-runs ` + "`" + `linespec test <path>` + "`" + `
  - path: docs/architecture.md
    run_command: test -f {{path}}     # non-test proof: just assert it exists
# 3. then ` + "`" + `linespec provenance open --record <id>` + "`" + `
` + "`" + `` + "`" + `` + "`" + `

To author the ` + "`" + `.linespec` + "`" + ` files that back ` + "`" + `type: linespec` + "`" + ` specs, use the **linespec-testing** skill (` + "`" + `/linespec-testing` + "`" + `).

## Tier Hierarchy Rules (Enforced by Linter)

- ` + "`" + `brief` + "`" + ` → top-level intent, cannot use ` + "`" + `implements` + "`" + `
- ` + "`" + `blueprint` + "`" + ` → design decision, may ` + "`" + `implements` + "`" + ` a brief
- ` + "`" + `bug` + "`" + ` → defect/regression record, uses ` + "`" + `extends` + "`" + ` or ` + "`" + `supersedes` + "`" + ` (not ` + "`" + `implements` + "`" + `)
- ` + "`" + `imprint` + "`" + ` → implementation record, must ` + "`" + `implements` + "`" + ` a blueprint
- ` + "`" + `supersedes` + "`" + ` must stay within the same tier (exception: ` + "`" + `bug` + "`" + ` may supersede a ` + "`" + `blueprint` + "`" + `) — PROV020
- ` + "`" + `implements` + "`" + ` must point exactly one tier up — PROV021
- ` + "`" + `implements` + "`" + ` must resolve locally or via configured shared_repos cache — PROV022
- ` + "`" + `extends` + "`" + ` is only valid on ` + "`" + `bug` + "`" + ` records; target must be a ` + "`" + `blueprint` + "`" + ` or ` + "`" + `bug` + "`" + `
- ` + "`" + `bug` + "`" + ` must have exactly one of ` + "`" + `supersedes` + "`" + ` or ` + "`" + `extends` + "`" + `

## Cross-Repo Provenance (shared_repos)

When ` + "`" + `.linespec.yml` + "`" + ` configures ` + "`" + `shared_repos` + "`" + `, records in those remote repositories are available for cross-repo relationships. Resolution: local ` + "`" + `provenance/` + "`" + ` first, then each shared repo cache in order; first match wins. Remote records are **read-only**. Sync the cache before working: ` + "`" + `linespec provenance sync` + "`" + `. The linter warns if the cache is older than ` + "`" + `cache_ttl_minutes` + "`" + ` (default 60).

## Multi-Pack Projects and the ` + "`" + `-c` + "`" + ` Flag

In monorepos with multiple ` + "`" + `.linespec.yml` + "`" + ` files, **always use ` + "`" + `-c <path>` + "`" + `** to target the correct config for the service you are working on. Without it, commands default to the repo-root config and may report records, scope, and enforcement from the wrong service.

## Hard Rules

- **Never use ` + "`" + `--no-verify` + "`" + `** to skip git hooks. If a hook fails, fix the issue.
- **Never relax enforcement** (` + "`" + `strict` + "`" + ` → ` + "`" + `warn` + "`" + `/` + "`" + `none` + "`" + `) to get unblocked — that is a maintainer + settings-level decision.
- **Never complete the blueprint without user confirmation.**
- **Never hand-edit** ` + "`" + `status` + "`" + `, ` + "`" + `superseded_by` + "`" + `, ` + "`" + `sealed_at_sha` + "`" + `, or the hash manifest — use the command that manages them.
- Records are named ` + "`" + `prov-YYYY-XXXXXXXX.yml` + "`" + ` using crypto-random hex (not sequential).
- Draft records are for planning — freely edit all fields including ` + "`" + `affected_scope` + "`" + `.

## Useful Commands

` + "`" + `` + "`" + `` + "`" + `bash
# Investigation
linespec provenance next --plan <file>... [-c <config>]    # the single correct next action (start here)
linespec provenance govern --files <file>... [-c <config>] # active (open+implemented) records governing files
linespec provenance status [--record <id>] [-c <config>]   # list records / detail
linespec provenance search --query "<query>" [-c <config>] # semantic search
linespec provenance context <file>... [-c <config>]        # full context: which records govern a file
linespec provenance discover [--dir <path>] [--dry-run]    # bootstrap draft records + .linespec stubs
linespec provenance audit [-c <config>]                    # audit recent changes
linespec provenance graph [--root <id>] [-c <config>]      # render the graph

# Record lifecycle (these update state for you — don't hand-edit YAML)
linespec provenance create --title "..." --type <tier> --no-edit [-c <config>]
linespec provenance create --supersedes <old-id> --title "..." --no-edit  # supersede correctly
linespec provenance open --record <id> [-c <config>]       # draft → open
linespec provenance complete --record <id> [-c <config>]   # open → implemented (+ seals SHA)
linespec provenance deprecate --record <id> --reason "..." [-c <config>]

# Validation and enforcement
linespec provenance lint [--warn|--info|--all] [-c <config>]  # validate; filter output
linespec provenance check [--staged] [-c <config>]            # check commits for violations
linespec provenance run-specs --record <id> [-c <config>]     # run a record's associated_specs
linespec provenance lock-scope --record <id> [-c <config>]    # freeze affected_scope from git
linespec provenance add-scope --record <id> [--dry-run]       # widen an open record's scope (unlocks those paths on disk)
linespec provenance reconcile [--json] [-c <config>]          # re-derive filesystem write-locks from open records
linespec provenance lock-layer --title "..." --no-edit        # create a locked governance layer

# Cross-repo and maintenance
linespec provenance sync [--force]                         # refresh shared_repos cache
linespec provenance index [-c <config>]                    # index records for semantic search
linespec provenance compile [-c <config>]                  # rebuild the hash manifest
linespec provenance generate [--record <id>]               # generate a behavioral spec doc

# Setup, distribution, and tooling
linespec init                                              # bootstrap a .linespec.yml
linespec provenance install-hooks                          # install pre-commit + commit-msg hooks
linespec provenance install-skills                         # install the Claude Code skills
linespec provenance publish [-c <config>]                  # package records into a manifest
linespec import <manifest-url>                             # import records from a manifest
linespec clone <manifest-url>                              # bootstrap a project from a manifest
` + "`" + `` + "`" + `` + "`" + `
`

const linespecTestingSkillMD = `---
name: linespec-testing
description: How to run, write, and debug LineSpec integration tests. Covers the test runner CLI, DSL structure, payload files, variable interpolation, channel types, semantic SQL matching, and common failure patterns.
when_to_use: "When running linespec tests, writing or modifying .linespec files, debugging test failures, setting up a new test suite, or understanding how the test infrastructure works."
---

# LineSpec Testing

LineSpec is a protocol-level integration testing DSL. Tests run the real service inside a Docker container and intercept its external calls (database queries, HTTP requests, Redis commands, Kafka messages, gRPC calls) at the wire protocol level — no mocks baked into the service code, no changes to the service under test.

## Mental Model

A linespec test defines one trigger/response cycle:

1. **RECEIVE** — the trigger: an HTTP request, a Kafka/event message, a gRPC call, or a background job
2. **EXPECT** — every external interaction the service must make, in order, and what to return for each
3. **RESPOND** — the HTTP response the service must produce (for HTTP triggers)

The runner fires the trigger, proxy sidecars intercept the service's outbound traffic, and at the end it verifies every EXPECT was hit and the response matched.

## Running Tests

` + "`" + `` + "`" + `` + "`" + `bash
# Run every spec in a directory
linespec test path/to/linespecs/

# Run a single spec file
linespec test path/to/linespecs/create_user_success.linespec
` + "`" + `` + "`" + `` + "`" + `

The runner builds and starts the service container (plus its database/Redis/Kafka/gRPC dependencies), and for each spec clears the mock registry → fires the trigger → verifies interactions → tears down.

## Configuration: where ` + "`" + `.linespec.yml` + "`" + ` lives

The runner finds config by **walking UP the directory tree** from the spec path to the repo root, using the **nearest** ` + "`" + `.linespec.yml` + "`" + ` (or ` + "`" + `.linespec.yaml` + "`" + `). You can also point at one explicitly with the ` + "`" + `LINESPEC_CONFIG` + "`" + ` environment variable.

**You do NOT need a ` + "`" + `.linespec.yml` + "`" + ` in every directory.** One at the root of your project (or spec tree) covers specs in all subdirectories. Put it wherever it logically governs your specs; nested configs are only for multi-service / multi-pack setups.

` + "`" + `` + "`" + `` + "`" + `yaml
service:
  name: my-service
  service_dir: ../my-service   # source code path, relative to this .linespec.yml
  framework: rails             # rails | fastapi | django | express | chi | custom
  port: 3000
  health_endpoint: /up         # framework default if omitted

database:
  type: mysql                  # mysql | postgresql | mongodb
  image: mysql:8.4
  database: mydb
  username: myuser
  password: mypassword
  init_script: ../my-service/init.sql

infrastructure:
  database: true
  kafka: true     # enable for EVENT/MESSAGE expectations
  redis: true     # enable for READ/WRITE:REDIS expectations
  grpc: true      # enable for GRPC expectations

dependencies:
  - name: user-service
    type: http
    host: user-service.local   # hostname the SUT dials
    proxy: true                # intercept calls to this host
` + "`" + `` + "`" + `` + "`" + `

Multiple databases: use a ` + "`" + `databases:` + "`" + ` list (each entry gets its own container + proxy). ` + "`" + `job_backend:` + "`" + ` configures background-job workers (see below).

## DSL Structure

Every ` + "`" + `.linespec` + "`" + ` file follows this exact order:

` + "`" + `` + "`" + `` + "`" + `
TEST <name>          (optional — defaults to filename)
VARS                 (optional — typed variables)
RECEIVE              (exactly one)
EXPECT               (zero or more)
EXPECT_NOT           (zero or more)
RESPOND              (exactly one, last)
` + "`" + `` + "`" + `` + "`" + `

### RECEIVE — the trigger

` + "`" + `` + "`" + `` + "`" + `
# HTTP
RECEIVE HTTP:POST /api/v1/users
WITH {{payloads/create_user_request.yaml}}
HEADERS
  Authorization: Bearer ${AUTH_TOKEN}

# Kafka / event consumer (KAFKA: and EVENT: are equivalent)
RECEIVE KAFKA:user-events
WITH {{payloads/user_created_event.json}}

# gRPC
RECEIVE GRPC:user.UserService/CreateUser
WITH {{payloads/create_user.json}}

# Background job (no HTTP response; see job_backend config)
RECEIVE JOB
` + "`" + `` + "`" + `` + "`" + `

` + "`" + `TIMEOUT <duration>` + "`" + ` (e.g. ` + "`" + `TIMEOUT 30s` + "`" + `) may follow RECEIVE to override the per-test timeout.

### EXPECT channels

| Channel | Intercepts |
|---|---|
| ` + "`" + `HTTP:<METHOD> <url>` + "`" + ` | outbound HTTP calls to a declared dependency |
| ` + "`" + `READ:MYSQL <table>` + "`" + ` / ` + "`" + `WRITE:MYSQL <table>` + "`" + ` | SELECT / INSERT·UPDATE·DELETE |
| ` + "`" + `READ:POSTGRESQL <table>` + "`" + ` / ` + "`" + `WRITE:POSTGRESQL <table>` + "`" + ` | SELECT / write |
| ` + "`" + `READ:REDIS <CMD> <key>` + "`" + ` / ` + "`" + `WRITE:REDIS <CMD> <key>` + "`" + ` | GET/HGET/… / SET/DEL/… |
| ` + "`" + `READ:MONGODB <coll>` + "`" + ` / ` + "`" + `WRITE:MONGODB <coll>` + "`" + ` | find / insert·update·delete |
| ` + "`" + `GRPC:<package.Service/Method>` + "`" + ` | gRPC calls |
| ` + "`" + `EVENT:<topic>` + "`" + ` / ` + "`" + `MESSAGE:<topic>` + "`" + ` | Kafka messages produced to a topic |

Multiple EXPECTs on the same table match in declaration order. Use ` + "`" + `CALL N` + "`" + ` to disambiguate repeated identical queries (e.g. ` + "`" + `EXPECT READ:MYSQL users CALL 2` + "`" + `).

### EXPECT options

` + "`" + `` + "`" + `` + "`" + `
EXPECT HTTP:POST http://payment-service.local/charge
WITH {{payloads/charge_request.json}}      # assert the outbound REQUEST body
HEADERS
  Idempotency-Key: ${KEY}                  # match request headers
RETURNS {{payloads/charge_response.json}}  # mocked RESPONSE body
RESPONSE_HEADERS
  Content-Type: application/json           # set response headers explicitly
` + "`" + `` + "`" + `` + "`" + `

- **` + "`" + `WITH {{file}}` + "`" + `** — matches the **outbound request body** the service sends. Omit it to match any body. (It is *not* the response.)
- **` + "`" + `RETURNS` + "`" + `** — the mocked response. Forms: ` + "`" + `{{file}}` + "`" + `, ` + "`" + `EMPTY` + "`" + `, ` + "`" + `ERROR` + "`" + ` (close the connection — service sees ` + "`" + `io.EOF` + "`" + `), ` + "`" + `ERROR <label>` + "`" + `, ` + "`" + `HTTP:NNN` + "`" + ` (status code). For a non-200 HTTP response with a body, use ` + "`" + `RETURNS {{file}}` + "`" + ` and include a ` + "`" + `status:` + "`" + ` field in that payload.
- **` + "`" + `RESPONSE_HEADERS` + "`" + `** — explicit response headers. Without it, ` + "`" + `Content-Type` + "`" + ` is **inferred from the payload file extension** (` + "`" + `.json` + "`" + `→` + "`" + `application/json` + "`" + `, ` + "`" + `.yaml` + "`" + `/` + "`" + `.yml` + "`" + `→` + "`" + `application/yaml` + "`" + `, ` + "`" + `.xml` + "`" + `→` + "`" + `application/xml` + "`" + `).

### SQL matching — semantic (recommended)

Match queries by structure, not brittle text. This is stable against ORM-added ` + "`" + `ORDER BY` + "`" + `/` + "`" + `LIMIT` + "`" + `, column reordering, and ` + "`" + `$1` + "`" + `/` + "`" + `?` + "`" + ` placeholder styles:

` + "`" + `` + "`" + `` + "`" + `
EXPECT READ:POSTGRESQL users
ACCESSING_TABLES users
VERIFY_OPERATION SELECT
VERIFY_WHERE_COLUMNS id
VERIFY_WHERE
  id: 42
RETURNS {{payloads/user.yaml}}

EXPECT WRITE:MYSQL users
VERIFY_OPERATION INSERT
VERIFY_WRITTEN_VALUES
  email: ${EMAIL}
RETURNS {{payloads/insert_result.yaml}}
` + "`" + `` + "`" + `` + "`" + `

- ` + "`" + `ACCESSING_TABLES` + "`" + ` — tables the query must touch (list two for a JOIN)
- ` + "`" + `VERIFY_OPERATION` + "`" + ` — ` + "`" + `SELECT` + "`" + ` | ` + "`" + `INSERT` + "`" + ` | ` + "`" + `UPDATE` + "`" + ` | ` + "`" + `DELETE` + "`" + `
- ` + "`" + `VERIFY_WHERE_COLUMNS` + "`" + ` — columns that must appear in the WHERE clause
- ` + "`" + `VERIFY_WHERE` + "`" + ` — specific WHERE column = value pairs
- ` + "`" + `VERIFY_WRITTEN_VALUES` + "`" + ` — column = value pairs for INSERT/UPDATE

**Legacy (deprecated):** ` + "`" + `USING_SQL """…"""` + "`" + ` (exact match after normalization) and ` + "`" + `USING_SQL_CONTAINS """…"""` + "`" + ` (substring). Prefer semantic matching; reach for these only for queries semantic matching can't express.

### VERIFY — validate intercepted data

` + "`" + `` + "`" + `` + "`" + `
EXPECT WRITE:MYSQL users
VERIFY query MATCHES /\bpassword_digest\b/
VERIFY query NOT_CONTAINS ` + "`" + `password` + "`" + `
` + "`" + `` + "`" + `` + "`" + `

Operators: ` + "`" + `CONTAINS` + "`" + `, ` + "`" + `NOT_CONTAINS` + "`" + `, ` + "`" + `MATCHES` + "`" + ` (Go regexp). Targets by channel:

| Channel | VERIFY targets |
|---|---|
| MySQL / PostgreSQL | ` + "`" + `query` + "`" + ` |
| HTTP | ` + "`" + `headers.<name>` + "`" + `, ` + "`" + `body` + "`" + `, ` + "`" + `url` + "`" + `, ` + "`" + `path` + "`" + ` |
| Kafka / EVENT | ` + "`" + `key` + "`" + `, ` + "`" + `value` + "`" + `, ` + "`" + `headers.<name>` + "`" + ` |
| gRPC | ` + "`" + `request_body` + "`" + `, ` + "`" + `metadata.<name>` + "`" + ` |
| Redis | ` + "`" + `command` + "`" + `, ` + "`" + `key` + "`" + `, ` + "`" + `value` + "`" + ` |

### EXPECT_NOT — assert an interaction did NOT happen

` + "`" + `` + "`" + `` + "`" + `
EXPECT_NOT WRITE:MYSQL users          # underscore or "EXPECT NOT" both work
EXPECT_NOT READ:POSTGRESQL audit_log
EXPECT_NOT WRITE:MONGODB sessions
EXPECT_NOT HTTP:GET ${AUTH_URL}
` + "`" + `` + "`" + `` + "`" + `

Negative expectations are enforced for the SQL and MongoDB stores (MySQL, PostgreSQL, MongoDB read/write) and HTTP.

### RESPOND

` + "`" + `` + "`" + `` + "`" + `
RESPOND HTTP:201
WITH {{payloads/created_user.yaml}}
NOISE
  body.id
  body.created_at
` + "`" + `` + "`" + `` + "`" + `

` + "`" + `NOISE` + "`" + ` lists response fields to exclude from comparison (runtime-generated IDs, timestamps).

## Background Jobs

For worker services, set ` + "`" + `RECEIVE JOB` + "`" + ` and configure ` + "`" + `job_backend` + "`" + `:

` + "`" + `` + "`" + `` + "`" + `yaml
job_backend:
  type: redis        # redis | kafka | scheduled
  queue: queue:default   # Redis queue key or Kafka topic (omit for scheduled)
` + "`" + `` + "`" + `` + "`" + `

The runner enqueues the job (Redis BRPOP/BLPOP/LPOP-based workers, Kafka consumers) or, for ` + "`" + `scheduled` + "`" + `, observes a cron-triggered run without seeding. EXPECT blocks then assert the work the job performs.

## gRPC

Set ` + "`" + `infrastructure.grpc: true` + "`" + `. JSON ` + "`" + `RETURNS` + "`" + ` payloads are sent as ` + "`" + `application/grpc+json` + "`" + ` by default; to emit binary protobuf (` + "`" + `application/grpc` + "`" + `), configure a compiled descriptor set via ` + "`" + `grpc_descriptor_set` + "`" + ` (service-level or per-dependency). Unmocked calls pass through to the real upstream when configured. Use ` + "`" + `RETURNS EMPTY` + "`" + ` for a method that returns an empty message.

## Variable Interpolation

` + "`" + `${VAR_NAME}` + "`" + ` is substituted everywhere — URLs, headers, SQL, payload files. If the variable is set in the environment, that value is used; otherwise a random value is generated and injected into the service container (forcing the service to read from the environment rather than hardcoding secrets). Declare types explicitly when needed:

` + "`" + `` + "`" + `` + "`" + `
VARS
  AUTH_TOKEN: string
  USER_ID: integer            # renders as a JSON number, not a quoted string
  ITEM_UUID: uuid
  STATUS: enum(active,banned) # constrained set
` + "`" + `` + "`" + `` + "`" + `

Typed constraints are supported: integer ` + "`" + `min` + "`" + `/` + "`" + `max` + "`" + `, string ` + "`" + `length` + "`" + `/` + "`" + `charset` + "`" + `/` + "`" + `pattern` + "`" + `, and ` + "`" + `enum` + "`" + `.

## Payload Files

Referenced as ` + "`" + `{{payloads/file.yaml}}` + "`" + ` (JSON, YAML, or XML). They may contain ` + "`" + `${VAR}` + "`" + ` interpolation. Files must exist at parse time. Write-result payloads:

` + "`" + `` + "`" + `` + "`" + `yaml
# mysql_write_result.yaml
affected_rows: 1
last_insert_id: 42
` + "`" + `` + "`" + `` + "`" + `

## Debugging Failures

- **` + "`" + `[WRITE:MYSQL] on [users] was never called` + "`" + `** — the service returned early, hit a different table/op, or a prior EXPECT didn't match (so its mock was never consumed). Check proxy logs: ` + "`" + `docker logs <proxy-container>` + "`" + `.
- **` + "`" + `[HTTP:GET] on [host] was never called` + "`" + `** — the dependency hostname in ` + "`" + `dependencies:` + "`" + ` doesn't match what the service dials, or the service's URL env var is wrong.
- **` + "`" + `negative expectation failed: [WRITE:MYSQL] … was called` + "`" + `** — an ` + "`" + `EXPECT_NOT` + "`" + ` was violated; usually a service logic bug.
- **` + "`" + `VERIFY failed` + "`" + `** — the intercepted query/body didn't satisfy a ` + "`" + `VERIFY` + "`" + ` rule; the error includes the actual value.
- **Response body mismatch** — add ` + "`" + `NOISE` + "`" + ` for varying fields, or fix the payload to match the service's real response shape.
- **SQL didn't match** — prefer semantic matching (` + "`" + `ACCESSING_TABLES` + "`" + ` + ` + "`" + `VERIFY_*` + "`" + `); if using legacy text matching, use ` + "`" + `USING_SQL_CONTAINS` + "`" + ` with a stable fragment.

## Example Suites

| Suite | Demonstrates |
|---|---|
| ` + "`" + `examples/todo-linespecs/` + "`" + ` | Rails + MySQL + HTTP dep + Kafka events |
| ` + "`" + `examples/user-linespecs/` + "`" + ` | Rails + MySQL, CRUD, auth, VARS |
| ` + "`" + `examples/notification-linespecs/` + "`" + ` | FastAPI + PostgreSQL + Redis cache hit/miss |
| ` + "`" + `examples/multi-db-linespecs/` + "`" + ` | Go + MySQL + MongoDB simultaneously |

` + "`" + `` + "`" + `` + "`" + `bash
linespec test examples/todo-linespecs/
` + "`" + `` + "`" + `` + "`" + `
`

// ContextOptions holds options for the context command
type ContextOptions struct {
	Files      []string // File paths to check (positional args or --files)
	Format     string   // Output format: human (default), compact, json
	ConfigFile string   // Path to custom .linespec.yml file
}

// Context retrieves provenance context for the given files
// NextOptions configures the `next` command.
type NextOptions struct {
	Files      []string // intended files (--files / --plan / positional args)
	Format     string   // human (default) | json
	ConfigFile string   // path to custom .linespec.yml file
}

// Next computes and renders the correct next provenance action for the current
// state. It is the I/O boundary for the advice engine: it gathers records,
// staged files, and working-tree changes, then hands a fully-populated NextState
// to the pure Advise function. Both the `next` command and the routed error
// hints (Phase 2c) render from that one engine.
//
// Every call also re-derives the fswrite write-bit projection (prov-2026-8d2f5f2a)
// unconditionally, best-effort. Enforcement MUST NOT depend on the Claude Code
// plugin hooks being installed — a user may not have them set up — so this
// cannot be gated behind an env var only a hook script sets. `next` is the one
// command every workflow (manual CLI use, any agent harness, the plugin hooks)
// already calls before touching files, which makes it the natural place for
// "reconcile MUST run at agent session start" to actually hold in practice. A
// dedicated `linespec provenance reconcile` verb (see Reconcile) also exists for
// explicit/scripted invocation.
func (c *Commands) Next(opts NextOptions) error {
	if _, err := c.reconcile(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: provenance reconcile failed: %v\n", err)
	}

	state := NextState{
		Records:           c.Loader.Records,
		IntendedFiles:     opts.Files,
		CommitTagRequired: c.Config != nil && c.Config.CommitTagRequired,
	}

	if c.Git != nil {
		if staged, err := c.Git.GetStagedFiles(); err == nil {
			state.StagedFiles = staged
		}
		if changed, err := c.Git.GetWorkingTreeChanges(); err == nil {
			state.ChangedFiles = changed
		}
	}

	actions := Advise(state)

	if opts.Format == "json" {
		return c.Formatter.FormatNextJSON(actions)
	}
	c.Formatter.FormatNext(actions)
	return nil
}

// GovernOptions configures the `govern` command — the active-only per-file
// governance lookup the 5c plugin hook consumes.
type GovernOptions struct {
	Files      []string // files to look up governance for
	Format     string   // human (default) | json
	ConfigFile string   // path to custom .linespec.yml file
}

// governingJSON is the stable hook-facing shape of one active governing record.
type governingJSON struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Govern reports the ACTIVE records (open + implemented) that govern the given
// files, plus the engine's primary next action so a caller can end with a `next`
// suggestion. Superseded/deprecated records are excluded — see
// activeGoverningRecords. This is the hook-facing lookup; the full `context`
// command still shows history.
func (c *Commands) Govern(opts GovernOptions) error {
	active := activeGoverningRecords(c.Loader.Records, opts.Files)

	state := NextState{
		Records:           c.Loader.Records,
		IntendedFiles:     opts.Files,
		CommitTagRequired: c.Config != nil && c.Config.CommitTagRequired,
	}
	if c.Git != nil {
		if staged, err := c.Git.GetStagedFiles(); err == nil {
			state.StagedFiles = staged
		}
		if changed, err := c.Git.GetWorkingTreeChanges(); err == nil {
			state.ChangedFiles = changed
		}
	}
	actions := Advise(state)
	var primary *NextAction
	if len(actions) > 0 {
		primary = &actions[0]
	}

	if opts.Format == "json" {
		governing := make([]governingJSON, 0, len(active))
		for _, r := range active {
			governing = append(governing, governingJSON{ID: r.ID, Status: string(r.Status)})
		}
		out := struct {
			Files     []string        `json:"files"`
			Governing []governingJSON `json:"governing"`
			Next      *NextAction     `json:"next,omitempty"`
		}{Files: opts.Files, Governing: governing, Next: primary}
		enc := json.NewEncoder(c.Formatter.Output)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if len(active) == 0 {
		fmt.Fprintf(c.Formatter.Output, "No active records govern %s.\n", strings.Join(opts.Files, ", "))
	} else {
		fmt.Fprintf(c.Formatter.Output, "Active records governing %s:\n", strings.Join(opts.Files, ", "))
		for _, r := range active {
			fmt.Fprintf(c.Formatter.Output, "  · %s [%s]\n", r.ID, r.Status)
		}
	}
	if primary != nil && primary.Command != "" {
		fmt.Fprintf(c.Formatter.Output, "Next: %s\n  %s\n", primary.Reason, primary.Command)
	}
	return nil
}

func (c *Commands) Context(opts ContextOptions) error {
	if len(opts.Files) == 0 {
		c.Formatter.FormatError("No files specified. Provide file paths as arguments or use --files flag.")
		return fmt.Errorf("no files specified")
	}

	// Build context result
	result := c.buildContextResult(opts.Files)

	// Output based on format
	switch opts.Format {
	case "json":
		return c.Formatter.FormatContextJSON(result)
	case "compact":
		c.Formatter.FormatContextCompact(result)
	default:
		c.Formatter.FormatContext(result)
	}

	return nil
}

// buildContextResult builds the context result for the given files
func (c *Commands) buildContextResult(files []string) *ContextResult {
	result := &ContextResult{
		Files:         files,
		DirectMatches: make([]*ContextRecord, 0),
		Conflicts:     make([]ScopeConflict, 0),
		ExemptFiles:   make([]string, 0),
	}

	// Collect files that are exempt from provenance rules via exclude_paths.
	var governedFiles []string
	for _, file := range files {
		if IsPathExcluded(file, c.Config.ExcludePaths) {
			result.ExemptFiles = append(result.ExemptFiles, file)
		} else {
			governedFiles = append(governedFiles, file)
		}
	}

	// Track which records directly match files
	directMatches := make(map[string]bool)

	// Track open record conflicts per file
	fileToOpenRecords := make(map[string][]string)

	// Find matching records for each non-exempt file
	for _, file := range governedFiles {
		matchingOpenRecords := make([]string, 0)

		for _, record := range c.Loader.Records {
			inScope, err := record.IsInScope(file)
			if err != nil {
				// Skip records with invalid scope patterns
				continue
			}

			if inScope {
				directMatches[record.ID] = true

				// Track open records for conflict detection
				if record.Status == StatusOpen {
					matchingOpenRecords = append(matchingOpenRecords, record.ID)
				}
			}
		}

		// Check for conflicts (>1 open records matching same file)
		if len(matchingOpenRecords) > 1 {
			result.Conflicts = append(result.Conflicts, ScopeConflict{
				File:      file,
				RecordIDs: matchingOpenRecords,
			})
		}

		fileToOpenRecords[file] = matchingOpenRecords
	}

	// Build ContextRecords for direct matches with ancestry
	contextRecords := make(map[string]*ContextRecord)

	for recordID := range directMatches {
		record, exists := c.Loader.GetRecord(recordID)
		if !exists {
			continue
		}

		ctxRecord := &ContextRecord{
			Record:     record,
			IsAncestor: false,
			Ancestors:  make([]string, 0),
		}

		// Follow supersedes chain to build ancestry
		visited := make(map[string]bool)
		current := record.Supersedes

		for current != "" && current != "null" {
			if visited[current] {
				// Circular reference detected, stop
				break
			}
			visited[current] = true

			ancestor, exists := c.Loader.GetRecord(current)
			if !exists {
				break
			}

			ctxRecord.Ancestors = append(ctxRecord.Ancestors, current)

			// If this ancestor isn't already a direct match, add it as an ancestor-only record
			if !directMatches[current] {
				if _, alreadyAdded := contextRecords[current]; !alreadyAdded {
					ancestorCtx := &ContextRecord{
						Record:     ancestor,
						IsAncestor: true,
						Ancestors:  make([]string, 0),
					}
					contextRecords[current] = ancestorCtx
				}
			}

			current = ancestor.Supersedes
		}

		contextRecords[recordID] = ctxRecord
	}

	// Convert map to slice and sort
	result.DirectMatches = c.sortContextRecords(contextRecords)

	return result
}

// sortContextRecords sorts context records: open first, then implemented, then others
// Within each group, sort by ID chronologically
func (c *Commands) sortContextRecords(records map[string]*ContextRecord) []*ContextRecord {
	var open, implemented, others []*ContextRecord

	for _, ctxRecord := range records {
		switch ctxRecord.Record.Status {
		case StatusOpen:
			open = append(open, ctxRecord)
		case StatusImplemented:
			implemented = append(implemented, ctxRecord)
		default:
			others = append(others, ctxRecord)
		}
	}

	// Sort each group by ID
	sortByID := func(records []*ContextRecord) {
		for i := 0; i < len(records); i++ {
			for j := i + 1; j < len(records); j++ {
				if records[i].Record.ID > records[j].Record.ID {
					records[i], records[j] = records[j], records[i]
				}
			}
		}
	}

	sortByID(open)
	sortByID(implemented)
	sortByID(others)

	// Combine: open first, then implemented, then others
	return append(append(open, implemented...), others...)
}

// SearchOptions holds options for the search command
type SearchOptions struct {
	Query      string // Natural language query
	Limit      int    // Maximum number of results
	ConfigFile string // Path to custom .linespec.yml file
}

// ProvenanceSearchResult represents a single search result with record details
type ProvenanceSearchResult struct {
	Record     *Record
	Similarity float64
}

// errEmbeddingNotConfigured prints the .linespec.yml embedding setup guidance to
// stderr and returns the standard "embedding not configured" error. Shared by the
// Search and Index commands.
func errEmbeddingNotConfigured() error {
	fmt.Fprintln(os.Stderr, "Embedding API not configured. Add to .linespec.yml:")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "provenance:")
	fmt.Fprintln(os.Stderr, "  embedding:")
	fmt.Fprintln(os.Stderr, "    provider: voyage")
	fmt.Fprintln(os.Stderr, "    index_model: voyage-4-large")
	fmt.Fprintln(os.Stderr, "    query_model: voyage-4-lite")
	fmt.Fprintln(os.Stderr, "    api_key: ${VOYAGE_API_KEY}")
	fmt.Fprintln(os.Stderr, "")
	return fmt.Errorf("embedding not configured")
}

// Search performs semantic search over provenance records
func (c *Commands) Search(opts SearchOptions) error {
	// Check if embedder is configured
	if c.Embedder == nil || !c.Embedder.IsConfigured() {
		return errEmbeddingNotConfigured()
	}

	// Generate embedding for query
	queryVector, err := c.Embedder.GenerateQuery(opts.Query)
	if err != nil {
		return fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Search the store
	store := embeddings.NewStore(c.RepoRoot)

	results, err := store.Find(queryVector, opts.Limit)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	// Filter results by similarity threshold
	threshold := c.Embedder.SimilarityThreshold()
	var filteredResults []embeddings.SearchResult
	for _, r := range results {
		if r.Similarity >= threshold {
			filteredResults = append(filteredResults, r)
		}
	}

	if len(filteredResults) == 0 {
		fmt.Fprintln(os.Stdout, "No semantically similar records found.")
		fmt.Fprintf(os.Stdout, "(Similarity threshold: %.2f)\n", threshold)
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, "Note: Search results are based on semantic similarity to implemented")
		fmt.Fprintln(os.Stdout, "records. This is an advisory result, not a scope constraint check.")
		return nil
	}

	// Build search results with record details
	var searchResults []ProvenanceSearchResult
	for _, r := range filteredResults {
		if record, exists := c.Loader.GetRecord(r.RecordID); exists {
			searchResults = append(searchResults, ProvenanceSearchResult{
				Record:     record,
				Similarity: r.Similarity,
			})
		}
	}

	// Display results
	fmt.Fprintf(os.Stdout, "\n[ADVISORY] Semantic Search Results for: %q\n", opts.Query)
	fmt.Fprintf(os.Stdout, "(Similarity threshold: %.2f)\n", threshold)
	fmt.Fprintln(os.Stdout, strings.Repeat("=", 60))
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "Found records with semantic similarity to your query.")
	fmt.Fprintln(os.Stdout, "These are advisory results based on meaning, not scope constraints.")
	fmt.Fprintln(os.Stdout, "")

	for i, r := range searchResults {
		similarity := r.Similarity * 100
		fmt.Fprintf(os.Stdout, "%d. %s (%.1f%% similar)\n", i+1, r.Record.ID, similarity)
		fmt.Fprintf(os.Stdout, "   Title: %s\n", r.Record.Title)
		fmt.Fprintf(os.Stdout, "   Status: %s\n", r.Record.Status)
		fmt.Fprintln(os.Stdout, "")
	}

	fmt.Fprintln(os.Stdout, strings.Repeat("-", 60))
	fmt.Fprintln(os.Stdout, "Use 'linespec provenance context <files>' for scope-based lookup.")
	fmt.Fprintln(os.Stdout, "")

	return nil
}

// AuditOptions holds options for the audit command
type AuditOptions struct {
	Description string // Description of recent changes
	ConfigFile  string // Path to custom .linespec.yml file
}

// Audit performs semantic audit comparing changes against provenance history
func (c *Commands) Audit(opts AuditOptions) error {
	// Check if embedder is configured
	if c.Embedder == nil || !c.Embedder.IsConfigured() {
		fmt.Fprintln(os.Stderr, "Embedding API not configured. Add to .linespec.yml:")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "provenance:")
		fmt.Fprintln(os.Stderr, "  embedding:")
		fmt.Fprintln(os.Stderr, "    provider: voyage")
		fmt.Fprintln(os.Stderr, "    index_model: voyage-4-large")
		fmt.Fprintln(os.Stderr, "    query_model: voyage-4-lite")
		fmt.Fprintln(os.Stderr, "    api_key: ${VOYAGE_API_KEY}")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stdout, "✓ Audit advisory (no embedding configured)")
		return nil // Exit 0 as per constraint
	}

	// Generate embedding for description
	descVector, err := c.Embedder.GenerateQuery(opts.Description)
	if err != nil {
		// Graceful degradation - print advisory and exit 0
		fmt.Fprintf(os.Stderr, "Warning: Failed to generate embedding: %v\n", err)
		fmt.Fprintln(os.Stdout, "✓ Audit advisory completed with warnings")
		return nil
	}

	// Search for similar records
	store := embeddings.NewStore(c.RepoRoot)

	results, err := store.Find(descVector, 5)
	if err != nil {
		// Graceful degradation
		fmt.Fprintf(os.Stderr, "Warning: Search failed: %v\n", err)
		fmt.Fprintln(os.Stdout, "✓ Audit advisory completed with warnings")
		return nil
	}

	// Filter results by similarity threshold
	threshold := c.Embedder.SimilarityThreshold()
	var filteredResults []embeddings.SearchResult
	for _, r := range results {
		if r.Similarity >= threshold {
			filteredResults = append(filteredResults, r)
		}
	}

	if len(filteredResults) == 0 {
		fmt.Fprintln(os.Stdout, "✓ Audit advisory: No similar records found in provenance history.")
		fmt.Fprintf(os.Stdout, "(Similarity threshold: %.2f)\n", threshold)
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, "Your changes do not appear to conflict with any prior decisions.")
		return nil
	}

	// Display results
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "[ADVISORY] Semantic Audit Results")
	fmt.Fprintf(os.Stdout, "(Similarity threshold: %.2f)\n", threshold)
	fmt.Fprintln(os.Stdout, strings.Repeat("=", 60))
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "Recent changes compared against provenance history.")
	fmt.Fprintln(os.Stdout, "These are advisory findings based on semantic similarity.")
	fmt.Fprintln(os.Stdout, "They do not represent scope violations or blocking issues.")
	fmt.Fprintln(os.Stdout, "")

	foundRelevant := false
	for _, r := range filteredResults {
		foundRelevant = true
		if record, exists := c.Loader.GetRecord(r.RecordID); exists {
			similarity := r.Similarity * 100
			fmt.Fprintf(os.Stdout, "• %s (%.1f%% similar)\n", record.ID, similarity)
			fmt.Fprintf(os.Stdout, "  Title: %s\n", record.Title)
			fmt.Fprintf(os.Stdout, "  Status: %s\n", record.Status)
			if len(record.Constraints) > 0 {
				fmt.Fprintln(os.Stdout, "  Key constraints:")
				for _, c := range record.Constraints[:minInt(3, len(record.Constraints))] {
					fmt.Fprintf(os.Stdout, "    - %s\n", c)
				}
			}
			fmt.Fprintln(os.Stdout, "")
		}
	}

	if !foundRelevant {
		fmt.Fprintln(os.Stdout, "No records above similarity threshold found.")
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, "Your changes do not appear to conflict with prior decisions.")
	}

	fmt.Fprintln(os.Stdout, strings.Repeat("-", 60))
	fmt.Fprintln(os.Stdout, "✓ Audit advisory completed (exit 0)")
	fmt.Fprintln(os.Stdout, "")

	return nil
}

// Helper function for min
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// IndexOptions holds options for the index command
type IndexOptions struct {
	DryRun     bool   // Show what would be indexed without doing it
	Force      bool   // Re-index even if embedding exists
	ConfigFile string // Path to custom .linespec.yml file
}

// Index generates embeddings for all implemented provenance records that don't have them
func (c *Commands) Index(opts IndexOptions) error {
	// Check if embedder is configured
	if c.Embedder == nil || !c.Embedder.IsConfigured() {
		return errEmbeddingNotConfigured()
	}

	// Initialize embedding store
	store := embeddings.NewStore(c.RepoRoot)

	// Get all implemented records
	var toIndex []*Record
	for _, record := range c.Loader.Records {
		if record.Status != StatusImplemented {
			continue
		}

		// Check if already indexed (unless force)
		if !opts.Force {
			exists, err := store.Exists(record.ID)
			if err != nil {
				// If the store file doesn't exist yet, treat as not indexed
				if !os.IsNotExist(err) {
					fmt.Fprintf(os.Stderr, "Warning: Failed to check embedding for %s: %v\n", record.ID, err)
					continue
				}
			}
			if exists {
				continue
			}
		}

		toIndex = append(toIndex, record)
	}

	if len(toIndex) == 0 {
		fmt.Fprintln(os.Stdout, "✓ All implemented records already have embeddings.")
		return nil
	}

	fmt.Fprintf(os.Stdout, "\nFound %d record(s) to index\n", len(toIndex))
	fmt.Fprintln(os.Stdout, strings.Repeat("=", 60))
	fmt.Fprintln(os.Stdout, "")

	if opts.DryRun {
		fmt.Fprintln(os.Stdout, "[DRY RUN] Would index the following records:")
		for _, record := range toIndex {
			fmt.Fprintf(os.Stdout, "  • %s - %s\n", record.ID, record.Title)
		}
		fmt.Fprintln(os.Stdout, "")
		return nil
	}

	// Index each record
	successCount := 0
	failCount := 0
	for i, record := range toIndex {
		fmt.Fprintf(os.Stdout, "[%d/%d] Indexing %s...\n", i+1, len(toIndex), record.ID)

		text := embeddings.ExtractTextFromRecord(record.Title, record.Intent, record.Constraints)
		vector, err := c.Embedder.GenerateDocument(text)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ Failed to generate embedding: %v\n", err)
			failCount++
			continue
		}

		err = store.Write(embeddings.RecordEmbedding{
			RecordID: record.ID,
			Vector:   vector,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ Failed to store embedding: %v\n", err)
			failCount++
			continue
		}

		fmt.Fprintf(os.Stdout, "  ✓ Indexed successfully (%d dimensions)\n", len(vector))
		successCount++
	}

	glyph := "✓"
	if successCount == 0 && failCount > 0 {
		glyph = "✗"
	}
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, strings.Repeat("=", 60))
	fmt.Fprintf(os.Stdout, "%s Indexing complete: %d succeeded, %d failed\n", glyph, successCount, failCount)
	fmt.Fprintln(os.Stdout, "")

	if failCount > 0 {
		return fmt.Errorf("indexing completed with %d failures", failCount)
	}

	return nil
}

// SyncOptions holds options for the sync command
type SyncOptions struct {
	Force bool // Ignore TTL and re-fetch even if cache is fresh
}

// Sync refreshes the local cache for all configured shared repos using git archive.
// Within the configured TTL window, a repo is skipped as already fresh unless Force is set.
func (c *Commands) Sync(opts SyncOptions) error {
	if len(c.Config.SharedRepos) == 0 {
		fmt.Fprintln(c.Formatter.Output, "No shared_repos configured in .linespec.yml")
		return nil
	}

	results := c.Cache.SyncAll(opts.Force)
	hasError := false
	for _, r := range results {
		host := r.Repo.URL
		if r.Err != nil {
			fmt.Fprintf(c.Formatter.Output, "✗ %s (%s): %v\n", r.Repo.Name, host, r.Err)
			hasError = true
		} else if r.WasFresh {
			fmt.Fprintf(c.Formatter.Output, "✓ %s (%s) — cache is fresh, skipped\n", r.Repo.Name, host)
		} else {
			fmt.Fprintf(c.Formatter.Output, "✓ Synced %s (%s) — %d records\n", r.Repo.Name, host, r.RecordCount)
		}
	}

	if hasError {
		return fmt.Errorf("one or more repos failed to sync")
	}
	return nil
}

// CompileOptions holds options for the compile command
type CompileOptions struct {
	ConfigFile string
}

// Compile rebuilds the hash manifest from all loaded records. It is idempotent:
// if every record hash and both graph hashes already match the stored manifest,
// no file is written and the command exits 0.
func (c *Commands) Compile(opts CompileOptions) error {
	if c.Linter == nil || c.Linter.Hasher == nil {
		c.Formatter.FormatError("Hash manifest not configured")
		return fmt.Errorf("hash manifest not configured")
	}

	changed, err := c.Linter.Hasher.CompileManifest(c.Loader.Records)
	if err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to compile manifest: %v", err))
		return err
	}

	n := len(c.Loader.Records)
	if changed {
		fmt.Fprintf(os.Stdout, "\n✓ Hash manifest compiled (%d records)\n\n", n)
	} else {
		fmt.Fprintf(os.Stdout, "\n✓ Hash manifest is up to date (%d records)\n\n", n)
	}
	return nil
}
