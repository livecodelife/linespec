package provenance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Commands provides all provenance CLI commands
type Commands struct {
	Loader    *Loader
	Linter    *Linter
	Git       *Git
	Checker   *CommitChecker
	Formatter *Formatter
	Config    *ProvenanceConfig
	RepoRoot  string
}

// ProvenanceConfig holds provenance-related configuration
type ProvenanceConfig struct {
	Enforcement       string
	Dir               string
	SharedRepos       []string
	CommitTagRequired bool
	AutoAffectedScope bool
}

// NewCommands creates a new commands instance
func NewCommands(config *ProvenanceConfig, repoRoot string, output *os.File, color bool) (*Commands, error) {
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

	// Create loader
	loader := NewLoader(config.Dir, config.SharedRepos)
	if err := loader.LoadAll(); err != nil {
		return nil, fmt.Errorf("failed to load provenance records: %w", err)
	}

	// Create linter
	linter := NewLinter(loader, config.Enforcement)

	// Create git helper
	git := NewGit(repoRoot)

	// Create commit checker
	checker := NewCommitChecker(git, loader)

	// Create formatter
	formatter := NewFormatter(output, color)

	return &Commands{
		Loader:    loader,
		Linter:    linter,
		Git:       git,
		Checker:   checker,
		Formatter: formatter,
		Config:    config,
		RepoRoot:  repoRoot,
	}, nil
}

// CreateOptions holds options for the create command
type CreateOptions struct {
	Title      string
	Supersedes string
	Tags       []string
	NoEdit     bool
	IDSuffix   string // Service suffix for ID (e.g., "user-service" creates prov-YYYY-NNN-user-service)
	ConfigFile string // Path to custom .linespec.yml file
}

// Create creates a new provenance record
func (c *Commands) Create(opts CreateOptions) error {
	// Get next available ID
	existingIDs := c.Loader.GetAllIDs()
	year := CurrentYear()
	id := NextID(year, existingIDs)

	// Append service suffix if provided
	if opts.IDSuffix != "" {
		id = fmt.Sprintf("%s-%s", id, opts.IDSuffix)
	}

	// Get author
	author, err := c.Git.GetGitEmail()
	if err != nil {
		author = "unknown@example.com"
	}

	// Create record
	record := &Record{
		ID:                  id,
		Title:               opts.Title,
		Status:              StatusOpen,
		CreatedAt:           CurrentDate(),
		Author:              author,
		Intent:              "",
		Constraints:         []string{},
		AffectedScope:       []string{},
		ForbiddenScope:      []string{},
		Supersedes:          opts.Supersedes,
		SupersededBy:        "",
		Related:             []string{},
		AssociatedLineSpecs: []string{},
		AssociatedTraces:    []string{},
		Monitors:            []string{},
		Tags:                opts.Tags,
		FilePath:            filepath.Join(c.Config.Dir, id+".yml"),
	}

	// Validate supersedes if provided
	if opts.Supersedes != "" && opts.Supersedes != "null" {
		target, exists := c.Loader.GetRecord(opts.Supersedes)
		if !exists {
			c.Formatter.FormatError(fmt.Sprintf("Supersedes target %s does not exist", opts.Supersedes))
			return fmt.Errorf("supersedes target does not exist")
		}

		// Check if target is already superseded
		if target.SupersededBy != "" && target.SupersededBy != "null" {
			c.Formatter.FormatError(fmt.Sprintf("Record %s is already superseded by %s", opts.Supersedes, target.SupersededBy))
			return fmt.Errorf("target already superseded")
		}
	}

	// Save record
	if err := c.Loader.SaveRecord(record); err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to save record: %v", err))
		return err
	}

	// Update superseded record if applicable
	superseded := ""
	if opts.Supersedes != "" && opts.Supersedes != "null" {
		target, _ := c.Loader.GetRecord(opts.Supersedes)
		target.SupersededBy = record.ID
		target.Status = StatusSuperseded

		if err := c.Loader.SaveRecord(target); err != nil {
			c.Formatter.FormatError(fmt.Sprintf("Failed to update superseded record: %v", err))
			return err
		}
		superseded = opts.Supersedes
	}

	// Open in editor if not --no-edit
	if !opts.NoEdit {
		if err := c.openInEditor(record.FilePath); err != nil {
			// Don't fail if editor fails, just warn
			fmt.Fprintf(os.Stderr, "Warning: Could not open editor: %v\n", err)
		}
	}

	c.Formatter.FormatCreateSuccess(record, superseded)
	return nil
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

	// Output
	if opts.Format == "json" {
		return c.Formatter.FormatJSON(result.ToJSON())
	}

	c.Formatter.FormatLint(result)

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
				} else if len(record.AffectedScope) > originalLen {
					// Scope was actually populated with new files
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
		c.Formatter.FormatGraph(c.Loader, opts.Filter)
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
	var staleWarnings []StaleScopeWarning
	var err error

	if opts.Staged {
		// Check staged files
		violations, err = c.Checker.CheckStaged(opts.MessageFile, c.Config.CommitTagRequired)
		if err != nil {
			c.Formatter.FormatError(fmt.Sprintf("Check failed: %v", err))
			return err
		}

		// Check for stale scope warnings on staged files
		stagedFiles, err := c.Git.GetStagedFiles()
		if err == nil {
			for _, record := range c.Loader.Records {
				if record.Status == StatusImplemented && record.SealedAtSHA != "" {
					warnings := c.Checker.CheckForStaleScopeWarnings(record, stagedFiles)
					staleWarnings = append(staleWarnings, warnings...)
				}
			}
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
	c.Formatter.FormatCheckResult(violations, staleWarnings, label)

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

// LockScope locks the scope of a record
func (c *Commands) LockScope(opts LockScopeOptions) error {
	record, exists := c.Loader.GetRecord(opts.RecordID)
	if !exists {
		c.Formatter.FormatError(fmt.Sprintf("Record not found: %s", opts.RecordID))
		return fmt.Errorf("record not found")
	}

	// Check status
	if record.Status == StatusImplemented {
		c.Formatter.FormatError(fmt.Sprintf("Cannot modify %s: record is implemented\n\n  Implemented records are immutable. To change scope, create a new\n  Provenance Record that supersedes %s.", opts.RecordID, opts.RecordID))
		return fmt.Errorf("record is implemented")
	}

	// Check if already in allowlist mode
	if record.ScopeMode() == "allowlist" {
		c.Formatter.FormatError(fmt.Sprintf("%s is already in allowlist mode", opts.RecordID))
		return fmt.Errorf("already in allowlist mode")
	}

	// Auto-populate scope from git history
	if err := c.Checker.AutoPopulateScope(record); err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to auto-populate scope: %v", err))
		return err
	}

	if opts.DryRun {
		c.Formatter.FormatLockScopeSuccess(record, record.AffectedScope)
		return nil
	}

	// Save record
	if err := c.Loader.SaveRecord(record); err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to save record: %v", err))
		return err
	}

	c.Formatter.FormatLockScopeSuccess(record, record.AffectedScope)
	return nil
}

// CompleteOptions holds options for the complete command
type CompleteOptions struct {
	RecordID   string
	Force      bool
	ConfigFile string // Path to custom .linespec.yml file
}

// Complete marks a record as implemented
func (c *Commands) Complete(opts CompleteOptions) error {
	record, exists := c.Loader.GetRecord(opts.RecordID)
	if !exists {
		c.Formatter.FormatError(fmt.Sprintf("Record not found: %s", opts.RecordID))
		return fmt.Errorf("record not found")
	}

	// Check if already implemented
	if record.Status == StatusImplemented {
		c.Formatter.FormatError(fmt.Sprintf("Record %s is already implemented", opts.RecordID))
		return fmt.Errorf("already implemented")
	}

	// Verify associated LineSpecs exist
	if !opts.Force && len(record.AssociatedLineSpecs) > 0 {
		var missing []string
		for _, path := range record.AssociatedLineSpecs {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				missing = append(missing, path)
			}
		}

		if len(missing) > 0 {
			c.Formatter.FormatError(fmt.Sprintf("Cannot mark %s as implemented\n\n  The following associated LineSpecs do not exist on disk:\n", opts.RecordID))
			for _, path := range missing {
				fmt.Fprintf(os.Stdout, "    · %s  ✗ not found\n", path)
			}
			fmt.Fprintln(os.Stdout)
			fmt.Fprintln(os.Stdout, "  Create the missing LineSpec files or remove them from")
			fmt.Fprintln(os.Stdout, "  associated_linespecs before completing this record.")
			fmt.Fprintln(os.Stdout)
			return fmt.Errorf("missing linespecs")
		}
	}

	// Capture HEAD SHA for sealing
	headSHA, err := c.Git.GetHeadSHA()
	if err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to get HEAD SHA: %v", err))
		return err
	}

	// Update status and seal
	record.Status = StatusImplemented
	record.SealedAtSHA = headSHA

	// Save record
	if err := c.Loader.SaveRecord(record); err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to save record: %v", err))
		return err
	}

	c.Formatter.FormatCompleteSuccess(record)
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

	// Check if already deprecated or superseded
	if record.Status == StatusDeprecated {
		c.Formatter.FormatError(fmt.Sprintf("Record %s is already deprecated", opts.RecordID))
		return fmt.Errorf("already deprecated")
	}

	if record.Status == StatusSuperseded {
		c.Formatter.FormatError(fmt.Sprintf("Record %s is superseded and cannot be deprecated", opts.RecordID))
		return fmt.Errorf("already superseded")
	}

	// Update status
	record.Status = StatusDeprecated

	// TODO: Add deprecation_reason field to Record struct if reason is provided

	// Save record
	if err := c.Loader.SaveRecord(record); err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to save record: %v", err))
		return err
	}

	fmt.Fprintf(os.Stdout, "\n✓ %s marked as deprecated\n\n", opts.RecordID)

	return nil
}

// InstallHooks installs git hooks
func (c *Commands) InstallHooks() error {
	hooksDir := filepath.Join(c.RepoRoot, ".git", "hooks")

	// Create pre-commit hook (only lints)
	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	preCommitContent := `#!/bin/sh
# LineSpec provenance pre-commit hook
# This hook only lints modified provenance records for syntax/validity

# Use the local linespec binary if it exists, otherwise fall back to system
if [ -f "./linespec" ]; then
    LINESPEC="./linespec"
else
    LINESPEC="linespec"
fi

# Get list of modified provenance records
modified_records=$(git diff --cached --name-only | grep "^provenance/prov-" | sed 's|provenance/||' | sed -E 's|\.ya?ml$||')

# Lint modified records
for record in $modified_records; do
    $LINESPEC provenance lint --record "$record"
    if [ $? -ne 0 ]; then
        echo "Commit blocked due to lint errors in $record"
        exit 1
    fi
done
`

	if err := os.WriteFile(preCommitPath, []byte(preCommitContent), 0755); err != nil {
		return fmt.Errorf("failed to write pre-commit hook: %w", err)
	}

	// Create commit-msg hook (checks scope)
	commitMsgPath := filepath.Join(hooksDir, "commit-msg")
	commitMsgContent := `#!/bin/sh
# LineSpec provenance commit-msg hook
# This hook validates staged files against provenance record scope constraints

# Use the local linespec binary if it exists, otherwise fall back to system
if [ -f "./linespec" ]; then
    LINESPEC="./linespec"
else
    LINESPEC="linespec"
fi

# The commit message file is passed as the first argument
COMMIT_MSG_FILE="$1"

# Check staged files against scope constraints using the commit message
$LINESPEC provenance check --staged --message-file "$COMMIT_MSG_FILE"
if [ $? -ne 0 ]; then
    echo ""
    echo "Commit blocked due to provenance scope violations"
    echo "Use 'git commit --no-verify' to bypass this check"
    exit 1
fi
`

	if err := os.WriteFile(commitMsgPath, []byte(commitMsgContent), 0755); err != nil {
		return fmt.Errorf("failed to write commit-msg hook: %w", err)
	}

	fmt.Fprintf(os.Stdout, "\n✓ Installed git hooks to %s\n\n", hooksDir)
	fmt.Fprintln(os.Stdout, "  pre-commit hook:")
	fmt.Fprintln(os.Stdout, "    · Lints modified provenance records")
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "  commit-msg hook:")
	fmt.Fprintln(os.Stdout, "    · Checks staged files against provenance scope")
	fmt.Fprintln(os.Stdout, "    · Validates provenance IDs in commit message")
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "  Use 'git commit --no-verify' to bypass when needed.")
	fmt.Fprintln(os.Stdout)

	return nil
}

// ContextOptions holds options for the context command
type ContextOptions struct {
	Files      []string // File paths to check (positional args or --files)
	Format     string   // Output format: human (default), compact, json
	ConfigFile string   // Path to custom .linespec.yml file
}

// Context retrieves provenance context for the given files
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
	}

	// Track which records directly match files
	directMatches := make(map[string]bool)

	// Track open record conflicts per file
	fileToOpenRecords := make(map[string][]string)

	// Find matching records for each file
	for _, file := range files {
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
