package provenance

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Formatter handles output formatting for provenance commands
type Formatter struct {
	Output io.Writer
	Color  bool
}

// NewFormatter creates a new formatter
func NewFormatter(output io.Writer, color bool) *Formatter {
	if output == nil {
		output = os.Stdout
	}
	return &Formatter{
		Output: output,
		Color:  color && isTerminal(output),
	}
}

// isTerminal returns true if the output is a terminal (supports color)
func isTerminal(w io.Writer) bool {
	// Simple check: if it's stdout and not piped
	if f, ok := w.(*os.File); ok {
		stat, err := f.Stat()
		if err == nil {
			// Check if it's a character device (terminal)
			return (stat.Mode() & os.ModeCharDevice) != 0
		}
	}
	return false
}

// Color codes
const (
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorCyan   = "\033[36m"
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
)

// colored returns the string with color if enabled
func (f *Formatter) colored(s, color string) string {
	if f.Color {
		return color + s + colorReset
	}
	return s
}

// FormatStatus formats the status output
func (f *Formatter) FormatStatus(loader *Loader, enforcement string, filter string) {
	// Header
	fmt.Fprintf(f.Output, "\n%s\n\n", f.colored("PROVENANCE RECORDS", colorBold))
	fmt.Fprintf(f.Output, "  Enforcement: %s\n\n", enforcement)

	// Filter records if needed
	var records []*Record
	switch {
	case filter == "":
		records = loader.Records
	case filter == "open" || filter == "implemented" || filter == "superseded" || filter == "deprecated":
		records = loader.FilterByStatus(Status(filter))
	case strings.HasPrefix(filter, "tag:"):
		tag := strings.TrimPrefix(filter, "tag:")
		records = loader.FilterByTag(tag)
	default:
		records = loader.Records
	}

	// Table header
	fmt.Fprintf(f.Output, "  %-15s %-14s %-45s %s\n",
		f.colored("ID", colorBold),
		f.colored("STATUS", colorBold),
		f.colored("TITLE", colorBold),
		f.colored("LINESPECS", colorBold))
	fmt.Fprintf(f.Output, "  %s\n", strings.Repeat("-", 90))

	// Rows
	openWithoutSpecs := 0
	for _, record := range records {
		status := string(record.Status)
		if record.Status == StatusOpen && len(record.AssociatedLineSpecs) == 0 {
			status = "open ⚠"
			openWithoutSpecs++
		}

		// Truncate title if too long
		title := record.Title
		if len(title) > 42 {
			title = title[:39] + "..."
		}

		// Color status
		statusColored := status
		switch record.Status {
		case StatusOpen:
			if len(record.AssociatedLineSpecs) == 0 {
				statusColored = f.colored(status, colorYellow)
			} else {
				statusColored = f.colored(status, colorCyan)
			}
		case StatusImplemented:
			statusColored = f.colored(status, colorGreen)
		case StatusSuperseded, StatusDeprecated:
			statusColored = f.colored(status, colorYellow)
		}

		linespecCount := fmt.Sprintf("%d", len(record.AssociatedLineSpecs))

		fmt.Fprintf(f.Output, "  %-15s %-20s %-45s %s\n",
			record.ID,
			statusColored,
			title,
			linespecCount)
	}

	// Summary warning
	if openWithoutSpecs > 0 {
		fmt.Fprintf(f.Output, "\n%s %d open records have no associated LineSpecs\n",
			f.colored("⚠", colorYellow), openWithoutSpecs)
	}

	fmt.Fprintln(f.Output)
}

// FormatStatusDetailed formats detailed status for a single record
func (f *Formatter) FormatStatusDetailed(record *Record, loader *Loader) {
	// Header
	status := string(record.Status)
	if record.Status == StatusOpen && len(record.AssociatedLineSpecs) == 0 {
		status = "open ⚠"
	}

	fmt.Fprintf(f.Output, "\n%s  ·  %s\n", record.ID, f.colored(status, colorCyan))
	fmt.Fprintf(f.Output, "%s\n\n", strings.Repeat("─", 60))

	// Metadata
	fmt.Fprintf(f.Output, "Title:        %s\n", record.Title)
	fmt.Fprintf(f.Output, "Author:       %s\n", record.Author)
	fmt.Fprintf(f.Output, "Created:      %s\n", record.CreatedAt)

	if record.Status == StatusImplemented && record.SealedAtSHA != "" {
		fmt.Fprintf(f.Output, "Sealed at:    %s\n", record.SealedAtSHA[:7])
	}

	if record.Supersedes != "" && record.Supersedes != "null" {
		fmt.Fprintf(f.Output, "Supersedes:   %s\n", record.Supersedes)
	} else {
		fmt.Fprintf(f.Output, "Supersedes:   —\n")
	}

	if len(record.Tags) > 0 {
		fmt.Fprintf(f.Output, "Tags:         %s\n", strings.Join(record.Tags, ", "))
	}

	scopeMode := record.ScopeMode()
	if scopeMode == "allowlist" {
		fmt.Fprintf(f.Output, "Scope Mode:   %s (%d files)\n", scopeMode, len(record.AffectedScope))
	} else {
		fmt.Fprintf(f.Output, "Scope Mode:   %s\n", scopeMode)
	}

	fmt.Fprintln(f.Output)

	// Intent
	if record.Intent != "" {
		fmt.Fprintf(f.Output, "%s\n", f.colored("Intent:", colorBold))
		f.printIndented(record.Intent)
		fmt.Fprintln(f.Output)
	}

	// Constraints
	if len(record.Constraints) > 0 {
		fmt.Fprintf(f.Output, "%s\n", f.colored("Constraints:", colorBold))
		for _, c := range record.Constraints {
			fmt.Fprintf(f.Output, "  · %s\n", c)
		}
		fmt.Fprintln(f.Output)
	}

	// Scope
	if len(record.AffectedScope) > 0 {
		fmt.Fprintf(f.Output, "%s\n", f.colored("Allowed Scope:", colorBold))
		for _, s := range record.AffectedScope {
			fmt.Fprintf(f.Output, "  · %s\n", s)
		}
		fmt.Fprintln(f.Output)
	}

	if len(record.ForbiddenScope) > 0 {
		fmt.Fprintf(f.Output, "%s\n", f.colored("Forbidden Scope (explicit):", colorBold))
		for _, s := range record.ForbiddenScope {
			fmt.Fprintf(f.Output, "  · %s\n", s)
		}
		fmt.Fprintln(f.Output)
	}

	// Associated LineSpecs
	fmt.Fprintf(f.Output, "%s\n", f.colored("Associated LineSpecs:", colorBold))
	if len(record.AssociatedLineSpecs) == 0 {
		fmt.Fprintf(f.Output, "  (none)\n")
	} else {
		for _, path := range record.AssociatedLineSpecs {
			exists := "✓ exists"
			if _, err := os.Stat(path); os.IsNotExist(err) {
				exists = f.colored("✗ not found", colorRed)
			}
			fmt.Fprintf(f.Output, "  · %-50s %s\n", path, exists)
		}
	}
	fmt.Fprintln(f.Output)

	// Monitors
	fmt.Fprintf(f.Output, "%s\n", f.colored("Monitors:", colorBold))
	if len(record.Monitors) == 0 {
		fmt.Fprintf(f.Output, "  (none)\n")
	} else {
		for _, m := range record.Monitors {
			fmt.Fprintf(f.Output, "  · %s\n", m)
		}
	}
	fmt.Fprintln(f.Output)
}

// printIndented prints text with indentation
func (f *Formatter) printIndented(text string) {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		fmt.Fprintf(f.Output, "  %s\n", strings.TrimSpace(line))
	}
}

// FormatLint formats lint results
func (f *Formatter) FormatLint(result *LintResult) {
	// Header
	total := result.PassedCount + result.WarningCount + result.ErrorCount
	fmt.Fprintf(f.Output, "\n%s Linting %d provenance records (enforcement: %s)\n\n",
		f.colored("✓", colorGreen), total, result.Enforcement)

	// Issues by record
	issuesByRecord := make(map[string][]Issue)
	for _, issue := range result.Issues {
		issuesByRecord[issue.RecordID] = append(issuesByRecord[issue.RecordID], issue)
	}

	// Print issues
	for recordID, issues := range issuesByRecord {
		for _, issue := range issues {
			symbol := "⚠"
			color := colorYellow

			switch issue.Severity {
			case SeverityError:
				symbol = "✗"
				color = colorRed
			case SeverityWarning:
				symbol = "⚠"
				color = colorYellow
			case SeverityHint:
				symbol = "→"
				color = colorCyan
			}

			fieldStr := ""
			if issue.Field != "" {
				fieldStr = fmt.Sprintf("(%s)", issue.Field)
			}

			fmt.Fprintf(f.Output, "  %s %-15s %s %s\n",
				f.colored(symbol, color),
				recordID,
				issue.Message,
				fieldStr)
		}
	}

	// Summary
	fmt.Fprintln(f.Output)
	if result.ErrorCount > 0 {
		fmt.Fprintf(f.Output, "%s %d passed  ", f.colored("✓", colorGreen), result.PassedCount)
	} else if result.WarningCount > 0 {
		fmt.Fprintf(f.Output, "%s %d passed  ", f.colored("✓", colorGreen), result.PassedCount)
	} else {
		fmt.Fprintf(f.Output, "%s %d passed  ", f.colored("✓", colorGreen), result.PassedCount)
	}
	fmt.Fprintf(f.Output, "%s %d warnings  ", f.colored("⚠", colorYellow), result.WarningCount)
	fmt.Fprintf(f.Output, "%s %d errors\n", f.colored("✗", colorRed), result.ErrorCount)

	if result.ErrorCount > 0 && result.Enforcement == "strict" {
		fmt.Fprintln(f.Output)
		fmt.Fprintf(f.Output, "  %s Associate LineSpec files with open records or set status to\n", f.colored("Hint:", colorCyan))
		fmt.Fprintf(f.Output, "       'implemented' if proof already exists outside of LineSpec.\n")
	}

	fmt.Fprintln(f.Output)
}

// FormatGraph formats the provenance graph
func (f *Formatter) FormatGraph(loader *Loader, filter string) {
	fmt.Fprintf(f.Output, "\n%s\n\n", f.colored("PROVENANCE GRAPH", colorBold))

	// Find root records (not superseded by anything)
	roots := make(map[string]bool)
	for _, record := range loader.Records {
		roots[record.ID] = true
	}
	for _, record := range loader.Records {
		if record.Supersedes != "" && record.Supersedes != "null" {
			delete(roots, record.Supersedes)
		}
	}

	// Filter if requested
	if filter != "" {
		// Only show records matching the filter status
		filteredRoots := make(map[string]bool)
		for id := range roots {
			record, exists := loader.GetRecord(id)
			if exists && string(record.Status) == filter {
				filteredRoots[id] = true
			}
		}
		roots = filteredRoots
	}

	// Print tree for each root
	for id := range roots {
		f.printGraphNode(loader, id, 0, make(map[string]bool), filter)
	}

	// Print unconnected records
	fmt.Fprintln(f.Output)
	for _, record := range loader.Records {
		if !roots[record.ID] && record.Supersedes == "" {
			// Not a root and doesn't supersede anything - standalone
			if filter == "" || string(record.Status) == filter {
				f.printGraphNodeSimple(record)
			}
		}
	}

	fmt.Fprintln(f.Output)
}

// printGraphNode prints a node and its children recursively
func (f *Formatter) printGraphNode(loader *Loader, id string, depth int, visited map[string]bool, filter string) {
	if visited[id] {
		fmt.Fprintf(f.Output, "%s%s (circular reference)\n", strings.Repeat("  ", depth), id)
		return
	}
	visited[id] = true

	record, exists := loader.GetRecord(id)
	if !exists {
		fmt.Fprintf(f.Output, "%s%s (not found)\n", strings.Repeat("  ", depth), id)
		return
	}

	// Filter check
	if filter != "" && string(record.Status) != filter {
		// Still show superseded children
		if record.Status != StatusSuperseded {
			return
		}
	}

	indent := strings.Repeat("  ", depth)

	// Status indicator
	statusStr := string(record.Status)
	switch record.Status {
	case StatusOpen:
		if len(record.AssociatedLineSpecs) == 0 {
			statusStr = f.colored("open ⚠", colorYellow)
		} else {
			statusStr = f.colored("open", colorCyan)
		}
	case StatusImplemented:
		statusStr = f.colored("implemented", colorGreen)
	case StatusSuperseded:
		statusStr = f.colored("superseded", colorYellow)
	case StatusDeprecated:
		statusStr = f.colored("deprecated", colorYellow)
	}

	// Tree connector
	connector := ""
	if depth > 0 {
		connector = "└─ "
	}

	fmt.Fprintf(f.Output, "%s%s%s  %s  %s\n",
		indent,
		connector,
		record.ID,
		statusStr,
		record.Title)

	// Find children (records that supersede this one)
	for _, r := range loader.Records {
		if r.Supersedes == id {
			f.printGraphNode(loader, r.ID, depth+1, visited, filter)
		}
	}
}

// printGraphNodeSimple prints a simple node line
func (f *Formatter) printGraphNodeSimple(record *Record) {
	statusStr := string(record.Status)
	switch record.Status {
	case StatusOpen:
		if len(record.AssociatedLineSpecs) == 0 {
			statusStr = f.colored("open ⚠", colorYellow)
		} else {
			statusStr = f.colored("open", colorCyan)
		}
	case StatusImplemented:
		statusStr = f.colored("implemented", colorGreen)
	case StatusSuperseded:
		statusStr = f.colored("superseded", colorYellow)
	case StatusDeprecated:
		statusStr = f.colored("deprecated", colorYellow)
	}

	fmt.Fprintf(f.Output, "  %s  %s  %s\n",
		record.ID,
		statusStr,
		record.Title)
}

// FormatCheckResult formats the check command output
func (f *Formatter) FormatCheckResult(violations []Violation, staleWarnings []StaleScopeWarning, commit string) {
	if len(violations) == 0 && len(staleWarnings) == 0 {
		fmt.Fprintf(f.Output, "\n%s No forbidden scope violations in %s\n\n",
			f.colored("✓", colorGreen), commit)
		return
	}

	if len(violations) > 0 {
		fmt.Fprintf(f.Output, "\n%s Forbidden scope violation in %s\n\n",
			f.colored("✗", colorRed), commit)

		// Group by record
		byRecord := make(map[string][]Violation)
		for _, v := range violations {
			byRecord[v.RecordID] = append(byRecord[v.RecordID], v)
		}

		for recordID, vs := range byRecord {
			if recordID == "" {
				// Special case: no record ID means it's a general violation (e.g., missing tag)
				for _, v := range vs {
					fmt.Fprintf(f.Output, "  %s\n", v.Message)
				}
			} else {
				fmt.Fprintf(f.Output, "  %s forbids changes to:\n", recordID)
				for _, v := range vs {
					fmt.Fprintf(f.Output, "    · %s\n", v.File)
				}
			}
			fmt.Fprintln(f.Output)
		}

		fmt.Fprintf(f.Output, "  %s If this change is intentional, create a new Provenance Record\n", f.colored("Hint:", colorCyan))
		fmt.Fprintf(f.Output, "       that supersedes the governing record and governs this file.\n")
	}

	if len(staleWarnings) > 0 {
		fmt.Fprintf(f.Output, "\n%s Stale scope warnings in %s (non-blocking):\n\n",
			f.colored("⚠", colorYellow), commit)

		for _, w := range staleWarnings {
			fmt.Fprintf(f.Output, "  • %s\n", w.Message)
			fmt.Fprintln(f.Output, "    (File listed in affected_scope but unchanged since record sealed)")
			fmt.Fprintln(f.Output)
		}
	}

	fmt.Fprintln(f.Output)
}

// FormatCreateSuccess formats the create command success output
func (f *Formatter) FormatCreateSuccess(record *Record, superseded string) {
	fmt.Fprintf(f.Output, "\n%s Created %s\n",
		f.colored("✓", colorGreen),
		record.FilePath)

	if superseded != "" {
		fmt.Fprintf(f.Output, "%s Marked %s as superseded\n",
			f.colored("→", colorCyan),
			superseded)
	}

	fmt.Fprintln(f.Output)
}

// FormatLockScopeSuccess formats the lock-scope command success output
func (f *Formatter) FormatLockScopeSuccess(record *Record, lockedPaths []string) {
	fmt.Fprintf(f.Output, "\n%s %s scope locked (allowlist mode)\n\n",
		f.colored("✓", colorGreen),
		record.ID)

	fmt.Fprintf(f.Output, "  Locked %d paths from observed history:\n", len(lockedPaths))
	for _, path := range lockedPaths {
		fmt.Fprintf(f.Output, "    · %s\n", path)
	}

	fmt.Fprintln(f.Output)
	fmt.Fprintf(f.Output, "  Future commits tagged to this record must stay within this scope.\n\n")
}

// FormatCompleteSuccess formats the complete command success output
func (f *Formatter) FormatCompleteSuccess(record *Record) {
	fmt.Fprintf(f.Output, "\n%s %s marked as implemented\n\n",
		f.colored("✓", colorGreen),
		record.ID)

	if len(record.AssociatedLineSpecs) > 0 {
		fmt.Fprintf(f.Output, "  Associated LineSpecs verified:\n")
		for _, path := range record.AssociatedLineSpecs {
			exists := "✓"
			if _, err := os.Stat(path); os.IsNotExist(err) {
				exists = f.colored("✗ not found", colorRed)
			}
			fmt.Fprintf(f.Output, "    · %-50s %s\n", path, exists)
		}
	}

	fmt.Fprintln(f.Output)
}

// FormatError formats an error message
func (f *Formatter) FormatError(message string) {
	fmt.Fprintf(f.Output, "\n%s %s\n\n", f.colored("✗", colorRed), message)
}

// FormatJSON outputs data as JSON
func (f *Formatter) FormatJSON(data interface{}) error {
	encoder := json.NewEncoder(f.Output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

// JSONLintResult represents lint results for JSON output
type JSONLintResult struct {
	Enforcement string      `json:"enforcement"`
	Total       int         `json:"total"`
	Passed      int         `json:"passed"`
	Warnings    int         `json:"warnings"`
	Errors      int         `json:"errors"`
	Issues      []JSONIssue `json:"issues"`
}

// JSONIssue represents a single issue for JSON output
type JSONIssue struct {
	RecordID string `json:"record_id"`
	Field    string `json:"field"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

// ToJSON converts LintResult to JSONLintResult
func (r *LintResult) ToJSON() *JSONLintResult {
	jsonIssues := make([]JSONIssue, len(r.Issues))
	for i, issue := range r.Issues {
		jsonIssues[i] = JSONIssue{
			RecordID: issue.RecordID,
			Field:    issue.Field,
			Message:  issue.Message,
			Severity: string(issue.Severity),
		}
	}

	return &JSONLintResult{
		Enforcement: r.Enforcement,
		Total:       r.PassedCount + r.WarningCount + r.ErrorCount,
		Passed:      r.PassedCount,
		Warnings:    r.WarningCount,
		Errors:      r.ErrorCount,
		Issues:      jsonIssues,
	}
}

// JSONGraphNode represents a node in the graph for JSON output
type JSONGraphNode struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Status       string          `json:"status"`
	Supersedes   string          `json:"supersedes,omitempty"`
	SupersededBy string          `json:"superseded_by,omitempty"`
	Children     []JSONGraphNode `json:"children,omitempty"`
}

// BuildJSONGraph builds the graph for JSON output
func BuildJSONGraph(loader *Loader) []JSONGraphNode {
	// Find roots
	roots := make(map[string]bool)
	for _, record := range loader.Records {
		roots[record.ID] = true
	}
	for _, record := range loader.Records {
		if record.Supersedes != "" && record.Supersedes != "null" {
			delete(roots, record.Supersedes)
		}
	}

	var result []JSONGraphNode
	for id := range roots {
		node := buildJSONNode(loader, id, make(map[string]bool))
		if node != nil {
			result = append(result, *node)
		}
	}

	return result
}

func buildJSONNode(loader *Loader, id string, visited map[string]bool) *JSONGraphNode {
	if visited[id] {
		return nil
	}
	visited[id] = true

	record, exists := loader.GetRecord(id)
	if !exists {
		return nil
	}

	node := &JSONGraphNode{
		ID:           record.ID,
		Title:        record.Title,
		Status:       string(record.Status),
		Supersedes:   record.Supersedes,
		SupersededBy: record.SupersededBy,
	}

	// Find children
	for _, r := range loader.Records {
		if r.Supersedes == id {
			child := buildJSONNode(loader, r.ID, visited)
			if child != nil {
				node.Children = append(node.Children, *child)
			}
		}
	}

	return node
}
