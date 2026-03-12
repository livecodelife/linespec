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
}

// Create creates a new provenance record
func (c *Commands) Create(opts CreateOptions) error {
	// Get next available ID
	existingIDs := c.Loader.GetAllIDs()
	year := CurrentYear()
	id := NextID(year, existingIDs)

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
	RecordID string
	Filter   string // open | implemented | superseded | deprecated | tag:xxx
	Format   string // human | json
}

// Status shows record status
func (c *Commands) Status(opts StatusOptions) error {
	// Auto-populate scope if configured
	if c.Config.AutoAffectedScope {
		for _, record := range c.Loader.Records {
			if record.Status == StatusOpen && record.ScopeMode() == "observed" {
				if err := c.Checker.AutoPopulateScope(record); err != nil {
					// Non-fatal, just log
					fmt.Fprintf(os.Stderr, "Warning: Could not auto-populate scope for %s: %v\n", record.ID, err)
				}
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
			return c.Formatter.FormatJSON(record)
		}
		return c.Formatter.FormatJSON(c.Loader.Records)
	}

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

	return nil
}

// GraphOptions holds options for the graph command
type GraphOptions struct {
	Root   string // Start from specific record
	Filter string // open | implemented | superseded | deprecated
	Format string // human | json | dot
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
	Commit string // Single commit to check (default: HEAD)
	Range  string // Range to check (e.g., SHA..SHA)
	Record string // Check only against a specific record
}

// Check checks commits for violations
func (c *Commands) Check(opts CheckOptions) error {
	commit := opts.Commit
	if commit == "" {
		commit = "HEAD"
	}

	var violations []Violation
	var err error

	if opts.Range != "" {
		// Check range
		parts := strings.Split(opts.Range, "..")
		if len(parts) != 2 {
			c.Formatter.FormatError("Invalid range format. Use SHA..SHA")
			return fmt.Errorf("invalid range format")
		}
		violations, err = c.Checker.CheckRange(parts[0], parts[1])
	} else {
		// Check single commit
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

	c.Formatter.FormatCheckResult(violations, commit)

	if len(violations) > 0 {
		return fmt.Errorf("forbidden scope violations found")
	}

	return nil
}

// LockScopeOptions holds options for the lock-scope command
type LockScopeOptions struct {
	RecordID string
	DryRun   bool
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
	RecordID string
	Force    bool
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

	// Update status
	record.Status = StatusImplemented

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
	RecordID string
	Reason   string
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

	// Create pre-commit hook
	hookPath := filepath.Join(hooksDir, "pre-commit")
	hookContent := `#!/bin/sh
# LineSpec provenance pre-commit hook

# Check HEAD for forbidden scope violations
linespec provenance check --commit HEAD
if [ $? -ne 0 ]; then
    echo "Commit blocked due to forbidden scope violations"
    echo "Use 'git commit --no-verify' to bypass this check"
    exit 1
fi

# Get list of modified provenance records
modified_records=$(git diff --cached --name-only | grep "^provenance/prov-" | sed 's|provenance/||' | sed 's|\.yml$||')

# Lint modified records
for record in $modified_records; do
    linespec provenance lint --record "$record"
    if [ $? -ne 0 ]; then
        echo "Commit blocked due to lint errors in $record"
        exit 1
    fi
done
`

	if err := os.WriteFile(hookPath, []byte(hookContent), 0755); err != nil {
		return fmt.Errorf("failed to write pre-commit hook: %w", err)
	}

	fmt.Fprintf(os.Stdout, "\n✓ Installed pre-commit hook to %s\n\n", hookPath)
	fmt.Fprintln(os.Stdout, "  The hook will:")
	fmt.Fprintln(os.Stdout, "    · Check for forbidden scope violations")
	fmt.Fprintln(os.Stdout, "    · Lint modified provenance records")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "  Use 'git commit --no-verify' to bypass when needed.")
	fmt.Fprintln(os.Stdout)

	return nil
}
