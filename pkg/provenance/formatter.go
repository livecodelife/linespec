package provenance

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// isRemoteFilePath reports whether filePath lives outside the local provenance
// directory, i.e. it was resolved from a shared-repo cache.
func isRemoteFilePath(filePath, localDir string) bool {
	if filePath == "" || localDir == "" {
		return false
	}
	dir := localDir
	if !strings.HasSuffix(dir, string(os.PathSeparator)) {
		dir += string(os.PathSeparator)
	}
	return !strings.HasPrefix(filePath, dir)
}

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
		if record.Status == StatusOpen && len(record.AssociatedSpecs) == 0 {
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
			if len(record.AssociatedSpecs) == 0 {
				statusColored = f.colored(status, colorYellow)
			} else {
				statusColored = f.colored(status, colorCyan)
			}
		case StatusImplemented:
			statusColored = f.colored(status, colorGreen)
		case StatusSuperseded, StatusDeprecated:
			statusColored = f.colored(status, colorYellow)
		}

		specCount := fmt.Sprintf("%d", len(record.AssociatedSpecs))

		fmt.Fprintf(f.Output, "  %-15s %-20s %-45s %s\n",
			record.ID,
			statusColored,
			title,
			specCount)
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
	if record.Status == StatusOpen && len(record.AssociatedSpecs) == 0 {
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

	// Implements (parent reference)
	if record.Implements != "" {
		parent, exists := loader.GetRecord(record.Implements)
		if exists {
			fmt.Fprintf(f.Output, "Implements:   %s  %s\n", record.Implements, parent.Title)
		} else {
			fmt.Fprintf(f.Output, "Implements:   %s\n", record.Implements)
		}
	} else {
		fmt.Fprintf(f.Output, "Implements:   —\n")
	}

	// Implemented by (child references — derived by scanning all records)
	var implementors []*Record
	for _, r := range loader.Records {
		if r.Implements == record.ID {
			implementors = append(implementors, r)
		}
	}
	if len(implementors) == 0 {
		fmt.Fprintf(f.Output, "Implemented by: —\n")
	} else {
		fmt.Fprintf(f.Output, "Implemented by:\n")
		for _, r := range implementors {
			typeStr := string(r.Type)
			if typeStr == "" {
				typeStr = "blueprint"
			}
			fmt.Fprintf(f.Output, "  · %s  [%s]  %s\n", r.ID, typeStr, r.Title)
		}
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

	// Associated Specs
	fmt.Fprintf(f.Output, "%s\n", f.colored("Associated Specs:", colorBold))
	if len(record.AssociatedSpecs) == 0 {
		fmt.Fprintf(f.Output, "  (none)\n")
	} else {
		for _, spec := range record.AssociatedSpecs {
			exists := "✓ exists"
			if _, err := os.Stat(spec.Path); os.IsNotExist(err) {
				exists = f.colored("✗ not found", colorRed)
			}
			fmt.Fprintf(f.Output, "  · %-50s %s\n", spec.Path, exists)
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

// FormatLint formats lint results, filtering issue output by severity based on opts.
// The summary line is always printed. With no filter flag set, only errors are shown.
func (f *Formatter) FormatLint(result *LintResult, opts LintOptions) {
	// Header
	total := result.PassedCount + result.WarningCount + result.ErrorCount
	fmt.Fprintf(f.Output, "\n%s Linting %d provenance records (enforcement: %s)\n\n",
		f.colored("✓", colorGreen), total, result.Enforcement)

	// Determine which severities to show.
	showSeverity := func(s Severity) bool {
		switch {
		case opts.ShowAll:
			return true
		case opts.ShowWarn:
			return s == SeverityWarning
		case opts.ShowInfo:
			return s == SeverityHint
		default:
			return s == SeverityError
		}
	}

	// Issues by record
	issuesByRecord := make(map[string][]Issue)
	for _, issue := range result.Issues {
		issuesByRecord[issue.RecordID] = append(issuesByRecord[issue.RecordID], issue)
	}

	// Print issues
	for recordID, issues := range issuesByRecord {
		for _, issue := range issues {
			if !showSeverity(issue.Severity) {
				continue
			}

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

	// Summary — always printed regardless of filter
	fmt.Fprintln(f.Output)
	fmt.Fprintf(f.Output, "%s %d passed  ", f.colored("✓", colorGreen), result.PassedCount)
	fmt.Fprintf(f.Output, "%s %d warnings  ", f.colored("⚠", colorYellow), result.WarningCount)
	fmt.Fprintf(f.Output, "%s %d errors\n", f.colored("✗", colorRed), result.ErrorCount)

	if result.ErrorCount > 0 && result.Enforcement == "strict" {
		fmt.Fprintln(f.Output)
		fmt.Fprintf(f.Output, "  %s Associate LineSpec files with open records or set status to\n", f.colored("Hint:", colorCyan))
		fmt.Fprintf(f.Output, "       'implemented' if proof already exists outside of LineSpec.\n")
	}

	fmt.Fprintln(f.Output)
}

// FormatGraph formats the provenance graph.
// When root is non-empty, only the subgraph centred on that record is shown:
// one level of implements parent (if any) plus all downstream implements and
// supersession children.
func (f *Formatter) FormatGraph(loader *Loader, filter string, root string) {
	fmt.Fprintf(f.Output, "\n%s\n\n", f.colored("PROVENANCE GRAPH", colorBold))

	if root != "" {
		f.printRootedGraph(loader, root, filter)
		fmt.Fprintln(f.Output)
		return
	}

	// Build set of IDs that are superseded (so we can skip them as top-level roots)
	supersededIDs := make(map[string]bool)
	for _, record := range loader.Records {
		if record.Supersedes != "" && record.Supersedes != "null" {
			supersededIDs[record.Supersedes] = true
		}
	}

	// Build set of IDs that implement something else (they appear as children in the
	// implements hierarchy, not as top-level roots)
	hasImplementsParent := make(map[string]bool)
	for _, record := range loader.Records {
		if record.Implements != "" {
			hasImplementsParent[record.ID] = true
		}
	}

	// Top-level roots: not superseded AND not a child in any implements chain
	visited := make(map[string]bool)
	for _, record := range loader.Records {
		if supersededIDs[record.ID] || hasImplementsParent[record.ID] {
			continue
		}
		if filter != "" && string(record.Status) != filter {
			continue
		}
		f.printGraphNode(loader, record.ID, 0, visited, filter)
	}

	// Orphaned implements children (parent missing) not yet printed
	fmt.Fprintln(f.Output)
	for _, record := range loader.Records {
		if visited[record.ID] {
			continue
		}
		if filter != "" && string(record.Status) != filter {
			continue
		}
		f.printGraphNodeSimple(record, loader.Dir)
	}

	fmt.Fprintln(f.Output)
}

// printRootedGraph renders the subgraph centred on rootID:
// shows the parent record (one level up via implements, if any) then the root
// and all its implements+supersession descendants.
func (f *Formatter) printRootedGraph(loader *Loader, rootID string, filter string) {
	root, exists := loader.GetRecord(rootID)
	if !exists {
		fmt.Fprintf(f.Output, "  record not found: %s\n", rootID)
		return
	}

	visited := make(map[string]bool)

	// Show parent (one level up via implements) if present
	if root.Implements != "" {
		parent, parentExists := loader.GetRecord(root.Implements)
		if parentExists {
			f.printGraphNodeLine(parent, 0, false, loader.Dir)
			visited[parent.ID] = true
			// Print root as an implements child of parent
			f.printImplementsNode(loader, rootID, 1, visited, filter)
			return
		}
	}

	// No parent — print from root down
	f.printGraphNode(loader, rootID, 0, visited, filter)
}

// printGraphNode prints a node and its children (both supersession and implements)
// recursively. Supersession children use └─ connectors. Implements children use
// ↳ [type] connectors to distinguish the two relationship types.
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
		// Still descend into superseded nodes so the chain is visible
		if record.Status != StatusSuperseded {
			return
		}
	}

	f.printGraphNodeLine(record, depth, false, loader.Dir)

	// Supersession children (records that supersede this one) — same dimension
	for _, r := range loader.Records {
		if r.Supersedes == id {
			f.printGraphNode(loader, r.ID, depth+1, visited, filter)
		}
	}

	// Implements children — visually distinct with ↳ [type] connector
	for _, r := range loader.Records {
		if r.Implements == id && !visited[r.ID] {
			f.printImplementsNode(loader, r.ID, depth+1, visited, filter)
		}
	}
}

// printImplementsNode prints a record reached via an implements edge, using a
// visually distinct connector (↳ [type]) to separate it from supersession chains.
func (f *Formatter) printImplementsNode(loader *Loader, id string, depth int, visited map[string]bool, filter string) {
	if visited[id] {
		return
	}
	visited[id] = true

	record, exists := loader.GetRecord(id)
	if !exists {
		return
	}

	if filter != "" && string(record.Status) != filter && record.Status != StatusSuperseded {
		return
	}

	indent := strings.Repeat("  ", depth)
	typeLabel := string(record.Type)
	if typeLabel == "" {
		typeLabel = "blueprint"
	}

	statusStr := f.statusLabel(record)

	remoteLabel := ""
	if isRemoteFilePath(record.FilePath, loader.Dir) {
		remoteLabel = " [remote]"
	}

	fmt.Fprintf(f.Output, "%s↳ [%s] %s  %s  %s%s\n",
		indent,
		typeLabel,
		record.ID,
		statusStr,
		record.Title,
		remoteLabel)

	// Supersession children within this implements tier
	for _, r := range loader.Records {
		if r.Supersedes == id {
			f.printGraphNode(loader, r.ID, depth+1, visited, filter)
		}
	}

	// Further implements children (next tier down)
	for _, r := range loader.Records {
		if r.Implements == id && !visited[r.ID] {
			f.printImplementsNode(loader, r.ID, depth+1, visited, filter)
		}
	}
}

// printGraphNodeLine prints a single record line with the appropriate indent and connector.
func (f *Formatter) printGraphNodeLine(record *Record, depth int, implementsChild bool, localDir string) {
	indent := strings.Repeat("  ", depth)
	statusStr := f.statusLabel(record)

	connector := ""
	if depth > 0 && !implementsChild {
		connector = "└─ "
	}

	remoteLabel := ""
	if isRemoteFilePath(record.FilePath, localDir) {
		remoteLabel = " [remote]"
	}

	fmt.Fprintf(f.Output, "%s%s%s  %s  %s%s\n",
		indent,
		connector,
		record.ID,
		statusStr,
		record.Title,
		remoteLabel)
}

// statusLabel returns a coloured status string for a record.
func (f *Formatter) statusLabel(record *Record) string {
	switch record.Status {
	case StatusOpen:
		if len(record.AssociatedSpecs) == 0 {
			return f.colored("open ⚠", colorYellow)
		}
		return f.colored("open", colorCyan)
	case StatusImplemented:
		return f.colored("implemented", colorGreen)
	case StatusSuperseded:
		return f.colored("superseded", colorYellow)
	case StatusDeprecated:
		return f.colored("deprecated", colorYellow)
	case StatusDraft:
		return f.colored("draft", colorCyan)
	default:
		return string(record.Status)
	}
}

// printGraphNodeSimple prints a simple node line
func (f *Formatter) printGraphNodeSimple(record *Record, localDir string) {
	remoteLabel := ""
	if isRemoteFilePath(record.FilePath, localDir) {
		remoteLabel = " [remote]"
	}
	fmt.Fprintf(f.Output, "  %s  %s  %s%s\n",
		record.ID,
		f.statusLabel(record),
		record.Title,
		remoteLabel)
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
					if v.File == "" {
						fmt.Fprintf(f.Output, "    %s\n", v.Message)
					} else {
						fmt.Fprintf(f.Output, "    · %s\n", v.File)
					}
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

// FormatLockLayerSuccess formats the lock-layer command success output
func (f *Formatter) FormatLockLayerSuccess(record *Record) {
	fmt.Fprintf(f.Output, "\n%s Created locked layer %s\n",
		f.colored("✓", colorGreen),
		record.FilePath)
	fmt.Fprintln(f.Output)
	fmt.Fprintf(f.Output, "  Record created as implemented with locked: true.\n")
	fmt.Fprintf(f.Output, "  Edit affected_scope and associated_specs to define the protected surface.\n\n")
}

// FormatCompleteSuccess formats the complete command success output
func (f *Formatter) FormatCompleteSuccess(record *Record) {
	fmt.Fprintf(f.Output, "\n%s %s marked as implemented\n\n",
		f.colored("✓", colorGreen),
		record.ID)

	if len(record.AssociatedSpecs) > 0 {
		fmt.Fprintf(f.Output, "  Associated specs verified:\n")
		for _, spec := range record.AssociatedSpecs {
			exists := "✓"
			if _, err := os.Stat(spec.Path); os.IsNotExist(err) {
				exists = f.colored("✗ not found", colorRed)
			}
			fmt.Fprintf(f.Output, "    · %-50s %s\n", spec.Path, exists)
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

// JSONGraphEdge represents a directed edge in the JSON graph output.
// EdgeType distinguishes supersedes, implements, and related relationships.
type JSONGraphEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	EdgeType string `json:"edge_type"` // "supersedes", "implements", "related"
}

// JSONGraphNode represents a node in the graph for JSON output
type JSONGraphNode struct {
	ID              string          `json:"id"`
	Title           string          `json:"title"`
	Status          string          `json:"status"`
	Type            string          `json:"type,omitempty"`
	Remote          bool            `json:"remote,omitempty"`
	Supersedes      string          `json:"supersedes,omitempty"`
	SupersededBy    string          `json:"superseded_by,omitempty"`
	Implements      string          `json:"implements,omitempty"`
	ImplementedBy   []string        `json:"implemented_by,omitempty"`
	Children        []JSONGraphNode `json:"children,omitempty"`
	ImplementsNodes []JSONGraphNode `json:"implements_nodes,omitempty"`
}

// JSONGraph is the top-level structure for --format json graph output.
type JSONGraph struct {
	Nodes []JSONGraphNode `json:"nodes"`
	Edges []JSONGraphEdge `json:"edges"`
}

// BuildJSONGraph builds the graph for JSON output with typed edges.
func BuildJSONGraph(loader *Loader) JSONGraph {
	// Collect all edges
	var edges []JSONGraphEdge
	for _, r := range loader.Records {
		if r.Supersedes != "" && r.Supersedes != "null" {
			edges = append(edges, JSONGraphEdge{From: r.ID, To: r.Supersedes, EdgeType: "supersedes"})
		}
		if r.Implements != "" {
			edges = append(edges, JSONGraphEdge{From: r.ID, To: r.Implements, EdgeType: "implements"})
		}
		for _, rel := range r.Related {
			edges = append(edges, JSONGraphEdge{From: r.ID, To: rel, EdgeType: "related"})
		}
	}

	// Build implemented-by index
	implementedBy := make(map[string][]string)
	for _, r := range loader.Records {
		if r.Implements != "" {
			implementedBy[r.Implements] = append(implementedBy[r.Implements], r.ID)
		}
	}

	// Build nodes (top-level: not superseded and no implements parent)
	supersededIDs := make(map[string]bool)
	for _, r := range loader.Records {
		if r.Supersedes != "" && r.Supersedes != "null" {
			supersededIDs[r.Supersedes] = true
		}
	}

	// Top-level JSON nodes: not superseded AND not implementing something else
	visited := make(map[string]bool)
	var nodes []JSONGraphNode
	for _, r := range loader.Records {
		if supersededIDs[r.ID] || r.Implements != "" {
			continue
		}
		node := buildJSONNode(loader, r.ID, visited, implementedBy)
		if node != nil {
			nodes = append(nodes, *node)
		}
	}

	return JSONGraph{Nodes: nodes, Edges: edges}
}

func buildJSONNode(loader *Loader, id string, visited map[string]bool, implementedBy map[string][]string) *JSONGraphNode {
	if visited[id] {
		return nil
	}
	visited[id] = true

	record, exists := loader.GetRecord(id)
	if !exists {
		return nil
	}

	typeStr := string(record.Type)
	if typeStr == "" {
		typeStr = "blueprint"
	}

	node := &JSONGraphNode{
		ID:            record.ID,
		Title:         record.Title,
		Status:        string(record.Status),
		Type:          typeStr,
		Remote:        isRemoteFilePath(record.FilePath, loader.Dir),
		Supersedes:    record.Supersedes,
		SupersededBy:  record.SupersededBy,
		Implements:    record.Implements,
		ImplementedBy: implementedBy[record.ID],
	}

	// Supersession children
	for _, r := range loader.Records {
		if r.Supersedes == id {
			child := buildJSONNode(loader, r.ID, visited, implementedBy)
			if child != nil {
				node.Children = append(node.Children, *child)
			}
		}
	}

	// Implements children (next tier down)
	for _, r := range loader.Records {
		if r.Implements == id && !visited[r.ID] {
			child := buildJSONNode(loader, r.ID, visited, implementedBy)
			if child != nil {
				node.ImplementsNodes = append(node.ImplementsNodes, *child)
			}
		}
	}

	return node
}

// FormatContext formats the context command output in human-readable format
func (f *Formatter) FormatContext(result *ContextResult) {
	// Header
	fmt.Fprintf(f.Output, "\n%s\n\n", f.colored("PROVENANCE CONTEXT", colorBold))
	fmt.Fprintf(f.Output, "Files: %s\n\n", strings.Join(result.Files, ", "))

	// Group records by status
	var open, implemented, others []*ContextRecord
	for _, ctx := range result.DirectMatches {
		switch ctx.Record.Status {
		case StatusOpen:
			open = append(open, ctx)
		case StatusImplemented:
			implemented = append(implemented, ctx)
		default:
			others = append(others, ctx)
		}
	}

	// Output open records first
	if len(open) > 0 {
		fmt.Fprintf(f.Output, "%s\n\n", f.colored("OPEN RECORDS", colorCyan))
		for _, ctx := range open {
			f.formatContextRecord(ctx, false)
		}
	}

	// Output implemented records
	if len(implemented) > 0 {
		fmt.Fprintf(f.Output, "%s\n\n", f.colored("IMPLEMENTED RECORDS", colorGreen))
		for _, ctx := range implemented {
			f.formatContextRecord(ctx, false)
		}
	}

	// Output other records (superseded, deprecated)
	if len(others) > 0 {
		fmt.Fprintf(f.Output, "%s\n\n", f.colored("OTHER RECORDS", colorYellow))
		for _, ctx := range others {
			f.formatContextRecord(ctx, false)
		}
	}

	// Output conflicts
	if len(result.Conflicts) > 0 {
		fmt.Fprintf(f.Output, "%s\n\n", f.colored("SCOPE CONFLICTS", colorRed))
		for _, conflict := range result.Conflicts {
			fmt.Fprintf(f.Output, "  %s\n", f.colored("⚠", colorYellow))
			fmt.Fprintf(f.Output, "    File: %s\n", conflict.File)
			fmt.Fprintf(f.Output, "    Conflicting records: %s\n", strings.Join(conflict.RecordIDs, ", "))
			fmt.Fprintln(f.Output)
		}
	}

	// Summary
	if len(result.DirectMatches) == 0 {
		fmt.Fprintf(f.Output, "%s No provenance records govern these files.\n\n", f.colored("→", colorCyan))
	} else {
		fmt.Fprintf(f.Output, "%s %d record(s) govern these files\n\n", f.colored("✓", colorGreen), len(result.DirectMatches))
	}
}

// formatContextRecord formats a single context record
func (f *Formatter) formatContextRecord(ctx *ContextRecord, compact bool) {
	record := ctx.Record

	// Record header with status color
	var statusColor string
	switch record.Status {
	case StatusOpen:
		statusColor = colorCyan
	case StatusImplemented:
		statusColor = colorGreen
	case StatusSuperseded, StatusDeprecated:
		statusColor = colorYellow
	}

	ancestorLabel := ""
	if ctx.IsAncestor {
		ancestorLabel = f.colored(" [ANCESTOR]", colorYellow)
	}

	fmt.Fprintf(f.Output, "  %s%s\n", f.colored(record.ID, colorBold), ancestorLabel)
	fmt.Fprintf(f.Output, "  Status: %s\n", f.colored(string(record.Status), statusColor))
	fmt.Fprintf(f.Output, "  Title: %s\n", record.Title)

	// Intent
	if record.Intent != "" {
		fmt.Fprintf(f.Output, "  %s\n", f.colored("Intent:", colorBold))
		lines := strings.Split(record.Intent, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				fmt.Fprintf(f.Output, "    %s\n", trimmed)
			}
		}
	}

	// Constraints
	if len(record.Constraints) > 0 {
		fmt.Fprintf(f.Output, "  %s\n", f.colored("Constraints:", colorBold))
		for _, c := range record.Constraints {
			fmt.Fprintf(f.Output, "    · %s\n", c)
		}
	}

	// Ancestry
	if len(ctx.Ancestors) > 0 {
		fmt.Fprintf(f.Output, "  %s\n", f.colored("Ancestry:", colorBold))
		fmt.Fprintf(f.Output, "    Supersedes: %s\n", strings.Join(ctx.Ancestors, " → "))
	}

	// Scope (only for direct matches, not ancestors)
	if !ctx.IsAncestor {
		if len(record.AffectedScope) > 0 {
			fmt.Fprintf(f.Output, "  %s\n", f.colored("Affected Scope:", colorBold))
			for _, s := range record.AffectedScope {
				fmt.Fprintf(f.Output, "    · %s\n", s)
			}
		}
		if len(record.ForbiddenScope) > 0 {
			fmt.Fprintf(f.Output, "  %s\n", f.colored("Forbidden Scope:", colorBold))
			for _, s := range record.ForbiddenScope {
				fmt.Fprintf(f.Output, "    · %s\n", s)
			}
		}
	}

	fmt.Fprintln(f.Output)
}

// FormatContextCompact formats the context in a compact, token-efficient format
func (f *Formatter) FormatContextCompact(result *ContextResult) {
	// Minimal header
	fmt.Fprintf(f.Output, "CONTEXT: %s\n\n", strings.Join(result.Files, ","))

	// Group by status
	var open, implemented, others []*ContextRecord
	for _, ctx := range result.DirectMatches {
		switch ctx.Record.Status {
		case StatusOpen:
			open = append(open, ctx)
		case StatusImplemented:
			implemented = append(implemented, ctx)
		default:
			others = append(others, ctx)
		}
	}

	// Open records
	if len(open) > 0 {
		fmt.Fprintln(f.Output, "OPEN:")
		for _, ctx := range open {
			f.formatCompactRecord(ctx)
		}
	}

	// Implemented records
	if len(implemented) > 0 {
		fmt.Fprintln(f.Output, "IMPLEMENTED:")
		for _, ctx := range implemented {
			f.formatCompactRecord(ctx)
		}
	}

	// Other records
	if len(others) > 0 {
		fmt.Fprintln(f.Output, "OTHER:")
		for _, ctx := range others {
			f.formatCompactRecord(ctx)
		}
	}

	// Conflicts
	if len(result.Conflicts) > 0 {
		fmt.Fprintln(f.Output, "CONFLICTS:")
		for _, conflict := range result.Conflicts {
			fmt.Fprintf(f.Output, "FILE:%s RECORDS:%s\n", conflict.File, strings.Join(conflict.RecordIDs, ","))
		}
		fmt.Fprintln(f.Output)
	}

	// Summary count
	fmt.Fprintf(f.Output, "TOTAL:%d\n", len(result.DirectMatches))
}

// formatCompactRecord formats a record in compact style
func (f *Formatter) formatCompactRecord(ctx *ContextRecord) {
	record := ctx.Record

	// Single line header
	ancestorNote := ""
	if ctx.IsAncestor {
		ancestorNote = " [A]"
	}
	fmt.Fprintf(f.Output, "%s%s %s\n", record.ID, ancestorNote, record.Title)

	// Intent (first line only, truncated if needed)
	if record.Intent != "" {
		intent := strings.TrimSpace(record.Intent)
		lines := strings.Split(intent, "\n")
		firstLine := strings.TrimSpace(lines[0])
		if len(firstLine) > 100 {
			firstLine = firstLine[:97] + "..."
		}
		fmt.Fprintf(f.Output, "INTENT:%s\n", firstLine)
	}

	// Constraints (if any)
	if len(record.Constraints) > 0 {
		fmt.Fprintf(f.Output, "CONSTRAINTS:\n")
		for _, c := range record.Constraints {
			// Truncate long constraints
			constraint := c
			if len(constraint) > 80 {
				constraint = constraint[:77] + "..."
			}
			fmt.Fprintf(f.Output, "-%s\n", constraint)
		}
	}

	// Ancestry (compact format)
	if len(ctx.Ancestors) > 0 {
		fmt.Fprintf(f.Output, "ANCESTRY:%s\n", strings.Join(ctx.Ancestors, ">"))
	}

	// Empty line between records
	fmt.Fprintln(f.Output)
}

// FormatContextJSON formats the context as JSON
func (f *Formatter) FormatContextJSON(result *ContextResult) error {
	type JSONContextRecord struct {
		ID             string   `json:"id"`
		Title          string   `json:"title"`
		Status         string   `json:"status"`
		Intent         string   `json:"intent,omitempty"`
		Constraints    []string `json:"constraints,omitempty"`
		IsAncestor     bool     `json:"is_ancestor"`
		Ancestors      []string `json:"ancestors,omitempty"`
		AffectedScope  []string `json:"affected_scope,omitempty"`
		ForbiddenScope []string `json:"forbidden_scope,omitempty"`
	}

	type JSONScopeConflict struct {
		File      string   `json:"file"`
		RecordIDs []string `json:"record_ids"`
	}

	type JSONContextResult struct {
		Files        []string            `json:"files"`
		Records      []JSONContextRecord `json:"records"`
		Conflicts    []JSONScopeConflict `json:"conflicts,omitempty"`
		TotalRecords int                 `json:"total_records"`
	}

	jsonResult := JSONContextResult{
		Files:        result.Files,
		Records:      make([]JSONContextRecord, 0, len(result.DirectMatches)),
		Conflicts:    make([]JSONScopeConflict, 0, len(result.Conflicts)),
		TotalRecords: len(result.DirectMatches),
	}

	// Convert records
	for _, ctx := range result.DirectMatches {
		jsonRecord := JSONContextRecord{
			ID:          ctx.Record.ID,
			Title:       ctx.Record.Title,
			Status:      string(ctx.Record.Status),
			Intent:      ctx.Record.Intent,
			Constraints: ctx.Record.Constraints,
			IsAncestor:  ctx.IsAncestor,
			Ancestors:   ctx.Ancestors,
		}

		// Only include scope for direct matches
		if !ctx.IsAncestor {
			jsonRecord.AffectedScope = ctx.Record.AffectedScope
			jsonRecord.ForbiddenScope = ctx.Record.ForbiddenScope
		}

		jsonResult.Records = append(jsonResult.Records, jsonRecord)
	}

	// Convert conflicts
	for _, conflict := range result.Conflicts {
		jsonResult.Conflicts = append(jsonResult.Conflicts, JSONScopeConflict{
			File:      conflict.File,
			RecordIDs: conflict.RecordIDs,
		})
	}

	encoder := json.NewEncoder(f.Output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(jsonResult)
}
