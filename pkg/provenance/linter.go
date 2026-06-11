package provenance

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Severity represents the severity level of a validation issue
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityHint    Severity = "hint"
)

// Issue represents a validation issue found in a record
type Issue struct {
	RecordID string
	Field    string
	Message  string
	Severity Severity
}

// LintResult contains the results of linting a record or set of records
type LintResult struct {
	Issues       []Issue
	PassedCount  int
	WarningCount int
	ErrorCount   int
	HintCount    int
	Enforcement  string
}

// Add adds an issue to the result
func (r *LintResult) Add(issue Issue) {
	r.Issues = append(r.Issues, issue)
	switch issue.Severity {
	case SeverityError:
		r.ErrorCount++
	case SeverityWarning:
		r.WarningCount++
	case SeverityHint:
		r.HintCount++
	}
}

// HasErrors returns true if there are any error-level issues
func (r *LintResult) HasErrors() bool {
	return r.ErrorCount > 0
}

// isTerminalStatus returns true for statuses where records are sealed and file paths
// may no longer exist as the codebase evolves.
func isTerminalStatus(s Status) bool {
	return s == StatusImplemented || s == StatusSuperseded || s == StatusDeprecated
}

// Linter validates Provenance Records according to the schema and enforcement rules
type Linter struct {
	Loader            *Loader
	Enforcement       string // none | warn | strict
	CommitTagRequired bool
	Hasher            *Hasher // nil when hash manifest is not configured
}

// NewLinter creates a new linter
func NewLinter(loader *Loader, enforcement string) *Linter {
	return &Linter{
		Loader:      loader,
		Enforcement: enforcement,
	}
}

// LintAll validates all loaded records
func (l *Linter) LintAll() *LintResult {
	result := &LintResult{
		Enforcement: l.Enforcement,
	}

	for _, record := range l.Loader.Records {
		l.lintRecord(record, result)
	}

	// Scope overlap between open records is no longer surfaced as a lint warning —
	// it was noise on every lint. Overlap is now computed at the open/complete
	// lifecycle transitions as an internal selector (see prov-2026-bc57fbdc).

	// Check for dead records
	l.checkDeadRecords(result)

	// Check for locked scope violations
	l.checkLockedScope(result)

	result.PassedCount = len(l.Loader.Records) - result.ErrorCount

	return result
}

// LintRecord validates a single record
func (l *Linter) LintRecord(recordID string) *LintResult {
	result := &LintResult{
		Enforcement: l.Enforcement,
	}

	record, exists := l.Loader.GetRecord(recordID)
	if !exists {
		result.Add(Issue{
			RecordID: recordID,
			Field:    "",
			Message:  "Record not found",
			Severity: SeverityError,
		})
		return result
	}

	l.lintRecord(record, result)
	result.PassedCount = 1 - result.ErrorCount

	return result
}

// lintRecord validates a single record
func (l *Linter) lintRecord(record *Record, result *LintResult) {
	// Validate required fields
	l.validateRequiredFields(record, result)

	// Validate ID format
	l.validateID(record, result)

	// Validate status
	l.validateStatus(record, result)

	// Validate type field (hint for missing, error for invalid)
	l.validateType(record, result)

	// Validate date
	l.validateDate(record, result)

	// Validate supersedes
	l.validateSupersedes(record, result)

	// Validate extends field format
	l.validateExtends(record, result)

	// Validate supersession type agreement (always-on, graph integrity)
	l.validateSupersessionType(record, result)

	// Validate superseded_by agreement
	l.validateSupersededBy(record, result)

	// Validate related
	l.validateRelated(record, result)

	// Validate implements field (resolution + type correctness; always-on, graph integrity)
	l.validateImplements(record, result)

	// Validate not-applicable fields per type (always-on)
	l.validateNotApplicableFields(record, result)

	// Validate Bug-specific conditional rules (always-on, graph integrity)
	l.validateBugConditionals(record, result)

	// Draft records skip enforcement-sensitive checks
	if record.Status == StatusDraft {
		// Still validate structural fields even for draft records
		l.validateTitleLength(record, result)
		l.validateSealedAtSHA(record, result)
		return
	}

	// Remote records (loaded from shared repo cache) are read-only and governed
	// by their origin repo. Skip enforcement-level checks that require local
	// artifacts or local conventions (scope paths, associated specs, etc.).
	if l.isRemoteRecord(record) {
		l.validateTitleLength(record, result)
		l.validateConstraintsHint(record, result)
		return
	}

	// Validate scope patterns
	l.validateScopePatterns(record, result)

	// Validate scope overlap
	l.validateScopeSelfOverlap(record, result)

	// Validate scope paths exist (only for open records)
	l.validateScopePaths(record, result)

	// Validate imprint scope is contained within its blueprint's scope
	l.validateImprintScopeContainment(record, result)

	// Validate associated_specs
	l.validateAssociatedSpecs(record, result)

	// Validate title length
	l.validateTitleLength(record, result)

	// Check for constraints hint
	l.validateConstraintsHint(record, result)

	// Validate immutability for implemented records
	l.validateImmutability(record, result)

	// Validate sealed_at_sha field
	l.validateSealedAtSHA(record, result)
}

// validateType checks the type field and emits a lint hint when absent (backward compat)
func (l *Linter) validateType(record *Record, result *LintResult) {
	if record.Type == "" {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "type",
			Message:  "No type field set; defaulting to 'blueprint' for backward compatibility. Set type: brief|blueprint|bug|imprint to suppress this hint.",
			Severity: SeverityHint,
		})
		return
	}

	if !record.Type.IsValid() {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "type",
			Message:  fmt.Sprintf("Invalid type %q: must be one of brief, blueprint, bug, imprint", record.Type),
			Severity: SeverityError,
		})
	}
}

// validateRequiredFields checks that all required fields are present and non-empty.
// Fields required on all types: id, title, status, created_at, author, intent.
// Additional per-type requirements (always-on, graph integrity):
//   - Brief: constraints required
//   - Imprint: implements required
func (l *Linter) validateRequiredFields(record *Record, result *LintResult) {
	required := map[string]string{
		"id":         record.ID,
		"title":      record.Title,
		"status":     string(record.Status),
		"created_at": record.CreatedAt,
		"author":     record.Author,
		"intent":     record.Intent,
	}

	for field, value := range required {
		if strings.TrimSpace(value) == "" || value == "null" {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    field,
				Message:  fmt.Sprintf("Missing required field: %s", field),
				Severity: SeverityError,
			})
		}
	}

	// Resolve effective type for per-type rules
	effectiveType := record.Type
	if effectiveType == "" {
		effectiveType = RecordTypeBlueprint
	}

	// Brief records must have constraints (graph integrity rule, always-on)
	if effectiveType == RecordTypeBrief && len(record.Constraints) == 0 {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "constraints",
			Message:  "Brief records must have constraints. Briefs define the business rationale and must carry machine-verifiable constraints.",
			Severity: SeverityError,
		})
	}

	// Imprint records must have implements (graph integrity rule, always-on)
	if effectiveType == RecordTypeImprint && strings.TrimSpace(record.Implements) == "" {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "implements",
			Message:  "Imprint records must set implements pointing at the parent Blueprint.",
			Severity: SeverityError,
		})
	}
}

// validateID checks that the ID matches the prov-YYYY-NNN format
func (l *Linter) validateID(record *Record, result *LintResult) {
	if record.ID == "" {
		return // Already reported as missing required field
	}

	if !IsValidID(record.ID) {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "id",
			Message:  fmt.Sprintf("Invalid ID format %q: must be prov-YYYY-NNN", record.ID),
			Severity: SeverityError,
		})
	}
}

// validateStatus checks that the status is a known value
func (l *Linter) validateStatus(record *Record, result *LintResult) {
	if record.Status == "" {
		return // Already reported as missing required field
	}

	if !record.Status.IsValid() {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "status",
			Message:  fmt.Sprintf("Invalid status %q: must be one of draft, open, implemented, superseded, deprecated", record.Status),
			Severity: SeverityError,
		})
	}
}

// validateDate checks that the date is in ISO 8601 format
func (l *Linter) validateDate(record *Record, result *LintResult) {
	if record.CreatedAt == "" {
		return // Already reported as missing required field
	}

	datePattern := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	if !datePattern.MatchString(record.CreatedAt) {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "created_at",
			Message:  fmt.Sprintf("Invalid date format %q: must be YYYY-MM-DD", record.CreatedAt),
			Severity: SeverityError,
		})
	}
}

// validateSupersedes checks that supersedes references a real record
func (l *Linter) validateSupersedes(record *Record, result *LintResult) {
	if record.Supersedes == "" || record.Supersedes == "null" {
		return
	}

	if _, exists := l.Loader.GetRecord(record.Supersedes); !exists {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "supersedes",
			Message:  fmt.Sprintf("supersedes references unknown record: %s", record.Supersedes),
			Severity: SeverityError,
		})
	}
}

// validateSupersededBy checks that superseded_by agrees with the graph
func (l *Linter) validateSupersededBy(record *Record, result *LintResult) {
	// Reconstruct what superseded_by should be from the graph
	var expectedSupersededBy string
	for _, r := range l.Loader.Records {
		if r.Supersedes == record.ID {
			expectedSupersededBy = r.ID
			break
		}
	}

	if record.SupersededBy != expectedSupersededBy &&
		!(record.SupersededBy == "" && expectedSupersededBy == "") &&
		!(record.SupersededBy == "null" && expectedSupersededBy == "") {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "superseded_by",
			Message:  fmt.Sprintf("superseded_by (%s) does not agree with graph (should be %s)", record.SupersededBy, expectedSupersededBy),
			Severity: SeverityWarning,
		})
	}
}

// validateRelated checks that related references exist
func (l *Linter) validateRelated(record *Record, result *LintResult) {
	for _, relatedID := range record.Related {
		if _, exists := l.Loader.GetRecord(relatedID); !exists {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "related",
				Message:  fmt.Sprintf("related references unknown record: %s", relatedID),
				Severity: SeverityWarning,
			})
		}
	}
}

// validateScopePatterns checks that all scope patterns are valid
func (l *Linter) validateScopePatterns(record *Record, result *LintResult) {
	allPatterns := append(record.AffectedScope, record.ForbiddenScope...)

	for _, pattern := range allPatterns {
		// Check for regex prefix
		if len(pattern) > 3 && pattern[:3] == "re:" {
			regex := pattern[3:]
			if _, err := regexp.Compile(regex); err != nil {
				result.Add(Issue{
					RecordID: record.ID,
					Field:    "scope",
					Message:  fmt.Sprintf("Invalid regex pattern %q: %v", pattern, err),
					Severity: SeverityError,
				})
			}
		}
	}
}

// validateScopePaths checks that scope patterns match actual files.
// Open records produce errors for missing paths; terminal-state records produce warnings.
// Draft records are skipped entirely.
func (l *Linter) validateScopePaths(record *Record, result *LintResult) {
	if record.Status == StatusDraft {
		return
	}

	missingSev := SeverityError
	if isTerminalStatus(record.Status) {
		missingSev = SeverityWarning
	}

	allPatterns := append(record.AffectedScope, record.ForbiddenScope...)

	for _, pattern := range allPatterns {
		// Skip empty patterns
		if strings.TrimSpace(pattern) == "" {
			continue
		}

		// Check for regex prefix
		if len(pattern) > 3 && pattern[:3] == "re:" {
			l.validateRegexPattern(record, pattern, missingSev, result)
			continue
		}

		// Check for glob pattern
		if strings.Contains(pattern, "*") || strings.Contains(pattern, "?") {
			l.validateGlobPattern(record, pattern, missingSev, result)
			continue
		}

		// Exact path validation
		l.validateExactPath(record, pattern, missingSev, result)
	}
}

// validateExactPath checks that an exact path exists and is a file
func (l *Linter) validateExactPath(record *Record, path string, missingSev Severity, result *LintResult) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "scope",
				Message:  fmt.Sprintf("Scope path does not exist: %s", path),
				Severity: missingSev,
			})
		} else {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "scope",
				Message:  fmt.Sprintf("Cannot access scope path %s: %v", path, err),
				Severity: missingSev,
			})
		}
		return
	}

	if info.IsDir() {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "scope",
			Message:  fmt.Sprintf("Scope path is a directory, not a file (use glob pattern for directories): %s", path),
			Severity: missingSev,
		})
	}
}

// validateGlobPattern checks that a glob pattern matches at least one file
func (l *Linter) validateGlobPattern(record *Record, pattern string, missingSev Severity, result *LintResult) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "scope",
			Message:  fmt.Sprintf("Invalid glob pattern %q: %v", pattern, err),
			Severity: SeverityError,
		})
		return
	}

	if len(matches) == 0 {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "scope",
			Message:  fmt.Sprintf("Glob pattern matches no files: %s", pattern),
			Severity: missingSev,
		})
	}
}

// validateRegexPattern checks that a regex pattern matches at least one file
func (l *Linter) validateRegexPattern(record *Record, pattern string, missingSev Severity, result *LintResult) {
	regex := pattern[3:] // Strip "re:" prefix
	re, err := regexp.Compile(regex)
	if err != nil {
		// This should already be caught by validateScopePatterns, but double-check
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "scope",
			Message:  fmt.Sprintf("Invalid regex pattern %q: %v", pattern, err),
			Severity: SeverityError,
		})
		return
	}

	// Walk the filesystem to find matching files
	foundMatch := false
	walkErr := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Continue walking even if we can't access some paths
		}
		if info.IsDir() {
			return nil
		}
		if re.MatchString(path) {
			foundMatch = true
			return filepath.SkipDir // Stop walking once we find a match
		}
		return nil
	})

	if walkErr != nil {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "scope",
			Message:  fmt.Sprintf("Error walking filesystem for regex pattern %s: %v", pattern, walkErr),
			Severity: SeverityError,
		})
		return
	}

	if !foundMatch {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "scope",
			Message:  fmt.Sprintf("Regex pattern matches no files: %s", pattern),
			Severity: missingSev,
		})
	}
}

// validateScopeSelfOverlap checks if a pattern appears in both affected_scope and forbidden_scope
func (l *Linter) validateScopeSelfOverlap(record *Record, result *LintResult) {
	for _, affected := range record.AffectedScope {
		for _, forbidden := range record.ForbiddenScope {
			// Check if they could match the same file
			if patternsOverlap(affected, forbidden) {
				result.Add(Issue{
					RecordID: record.ID,
					Field:    "scope",
					Message:  fmt.Sprintf("Pattern %q appears in both affected_scope and forbidden_scope", affected),
					Severity: SeverityError,
				})
			}
		}
	}
}

// patternsOverlap returns true if two patterns could match the same file
func patternsOverlap(a, b string) bool {
	// If they're identical, they overlap
	if a == b {
		return true
	}

	// If one is an exact path and the other matches it, they overlap
	if !isPattern(a) && matchesPattern(a, b) {
		return true
	}
	if !isPattern(b) && matchesPattern(b, a) {
		return true
	}

	// If both are patterns, check if they could match the same files
	if isPattern(a) && isPattern(b) {
		return globPatternsOverlap(a, b)
	}

	return false
}

// globPatternsOverlap checks if two glob/regex patterns could match the same file
func globPatternsOverlap(a, b string) bool {
	aRegex := patternToRegex(a)
	bRegex := patternToRegex(b)

	if aRegex == nil || bRegex == nil {
		return false
	}

	// Generate sample paths from each pattern and check if the other matches
	samples := generateSamplePaths(a)
	for _, sample := range samples {
		if bRegex.MatchString(sample) {
			return true
		}
	}
	samples = generateSamplePaths(b)
	for _, sample := range samples {
		if aRegex.MatchString(sample) {
			return true
		}
	}

	return false
}

// patternToRegex converts a pattern to a compiled regex, or nil if invalid
func patternToRegex(pattern string) *regexp.Regexp {
	if len(pattern) > 3 && pattern[:3] == "re:" {
		re, err := regexp.Compile(pattern[3:])
		if err != nil {
			return nil
		}
		return re
	}
	regex := GlobToRegex(pattern)
	re, err := regexp.Compile(regex)
	if err != nil {
		return nil
	}
	return re
}

// generateSamplePaths generates sample file paths that match a pattern
func generateSamplePaths(pattern string) []string {
	var samples []string

	if len(pattern) > 3 && pattern[:3] == "re:" {
		// For regex patterns, generate a few plausible paths
		samples = append(samples, "test/file.go", "pkg/module/file.go", "src/test.go")
		return samples
	}

	// For glob patterns, replace wildcards with plausible values
	path := pattern
	path = strings.Replace(path, "**", "pkg/module", 1)
	path = strings.Replace(path, "*", "file", 1)
	path = strings.Replace(path, "?", "f", 1)
	samples = append(samples, path)

	// Also try with different replacements
	path2 := pattern
	path2 = strings.Replace(path2, "**", "src/sub", 1)
	path2 = strings.Replace(path2, "*", "test", 1)
	path2 = strings.Replace(path2, "?", "t", 1)
	samples = append(samples, path2)

	return samples
}

// isPattern returns true if the string is a glob or regex pattern
func isPattern(s string) bool {
	return strings.Contains(s, "*") ||
		strings.Contains(s, "?") ||
		(len(s) > 3 && s[:3] == "re:")
}

// matchesPattern checks if a file path matches a pattern
func matchesPattern(filePath, pattern string) bool {
	matches, _ := MatchPattern(filePath, pattern)
	return matches
}

// validateAssociatedSpecs checks that associated spec files exist
func (l *Linter) validateAssociatedSpecs(record *Record, result *LintResult) {
	missingSev := SeverityError
	if isTerminalStatus(record.Status) {
		missingSev = SeverityWarning
	}

	// Check file existence and accessibility
	for _, spec := range record.AssociatedSpecs {
		info, err := os.Stat(spec.Path)
		if err != nil {
			if os.IsNotExist(err) {
				result.Add(Issue{
					RecordID: record.ID,
					Field:    "associated_specs",
					Message:  fmt.Sprintf("Proof artifact does not exist: %s", spec.Path),
					Severity: missingSev,
				})
			} else {
				result.Add(Issue{
					RecordID: record.ID,
					Field:    "associated_specs",
					Message:  fmt.Sprintf("Cannot access proof artifact %s: %v", spec.Path, err),
					Severity: missingSev,
				})
			}
			continue
		}
		// Check if it's a directory
		if info.IsDir() {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "associated_specs",
				Message:  fmt.Sprintf("Proof artifact path is a directory, not a file: %s", spec.Path),
				Severity: missingSev,
			})
		}
	}

	// Check for enforcement level issues
	hasSpecs := len(record.AssociatedSpecs) > 0
	isOpen := record.Status == StatusOpen

	// Brief records cannot carry associated_specs (enforced by validateNotApplicableFields),
	// so the enforcement check would always fire on open briefs with no valid resolution.
	effectiveType := record.Type
	if effectiveType == "" {
		effectiveType = RecordTypeBlueprint
	}
	if effectiveType == RecordTypeBrief {
		return
	}

	if isOpen && !hasSpecs {
		switch l.Enforcement {
		case "strict":
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "associated_specs",
				Message:  "No associated specs (open) [strict]",
				Severity: SeverityError,
			})
		case "warn":
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "associated_specs",
				Message:  "No associated specs (open)",
				Severity: SeverityWarning,
			})
		case "none":
			// At none level, give a hint if there are constraints but no specs
			if len(record.Constraints) > 0 {
				result.Add(Issue{
					RecordID: record.ID,
					Field:    "associated_specs",
					Message:  "Record has constraints but no associated specs",
					Severity: SeverityHint,
				})
			}
		}
	}
}

// validateTitleLength checks if title exceeds 120 characters
func (l *Linter) validateTitleLength(record *Record, result *LintResult) {
	if len(record.Title) > 120 {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "title",
			Message:  fmt.Sprintf("Title exceeds 120 characters (%d chars)", len(record.Title)),
			Severity: SeverityWarning,
		})
	}
}

// validateConstraintsHint gives a hint if intent exists but no constraints
func (l *Linter) validateConstraintsHint(record *Record, result *LintResult) {
	if record.Intent != "" && len(record.Constraints) == 0 {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "constraints",
			Message:  "Record has intent but no constraints. Consider adding specific behavioral constraints.",
			Severity: SeverityHint,
		})
	}
}

// validateNotApplicableFields emits errors/warnings for fields that must not appear on specific types.
// These are always-on rules derived from the field enforcement matrix.
func (l *Linter) validateNotApplicableFields(record *Record, result *LintResult) {
	effectiveType := record.Type
	if effectiveType == "" {
		effectiveType = RecordTypeBlueprint
	}

	// extends is only valid on Bug records
	if effectiveType != RecordTypeBug && strings.TrimSpace(record.Extends) != "" {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "extends",
			Message:  fmt.Sprintf("extends is not applicable on %s records; it is only valid on bug records", effectiveType),
			Severity: SeverityError,
		})
	}

	// Brief records must not carry affected_scope, forbidden_scope, or associated_specs
	if effectiveType == RecordTypeBrief {
		if len(record.AffectedScope) > 0 {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "affected_scope",
				Message:  "affected_scope is not applicable on brief records; briefs define business rationale, not implementation scope",
				Severity: SeverityWarning,
			})
		}
		if len(record.ForbiddenScope) > 0 {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "forbidden_scope",
				Message:  "forbidden_scope is not applicable on brief records",
				Severity: SeverityWarning,
			})
		}
		if len(record.AssociatedSpecs) > 0 {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "associated_specs",
				Message:  "associated_specs is not applicable on brief records; proof artifacts belong on blueprints and imprints",
				Severity: SeverityWarning,
			})
		}
	}

	// Imprint records must not carry associated_traces or monitors
	if effectiveType == RecordTypeImprint {
		if len(record.AssociatedTraces) > 0 {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "associated_traces",
				Message:  "associated_traces is not applicable on imprint records; trace results are operational artifacts anchored at the blueprint tier",
				Severity: SeverityWarning,
			})
		}
		if len(record.Monitors) > 0 {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "monitors",
				Message:  "monitors is not applicable on imprint records; production monitoring is anchored at the blueprint tier",
				Severity: SeverityWarning,
			})
		}
	}
}

// validateBugConditionals enforces Bug-specific rules (always-on, graph integrity):
//   - Exactly one of supersedes or extends must be present
//   - When supersedes is set, the target must be a Blueprint or Bug
//   - When extends is set, the target must be a Blueprint or Bug
func (l *Linter) validateBugConditionals(record *Record, result *LintResult) {
	if record.Type != RecordTypeBug {
		return
	}

	hasSupersedesRef := strings.TrimSpace(record.Supersedes) != "" && record.Supersedes != "null"
	hasExtendsRef := strings.TrimSpace(record.Extends) != ""

	if !hasSupersedesRef && !hasExtendsRef {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "supersedes",
			Message:  "Bug records must either supersede or extend a Blueprint. Use supersedes when existing constraints are incorrect, extends when constraints are missing.",
			Severity: SeverityError,
		})
		return
	}

	if hasSupersedesRef && hasExtendsRef {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "supersedes",
			Message:  "supersedes and extends are mutually exclusive on Bug records. A record cannot simultaneously replace and be additive to the same parent.",
			Severity: SeverityError,
		})
		return
	}

	if hasSupersedesRef {
		target, exists := l.Loader.GetRecord(record.Supersedes)
		if exists {
			targetType := target.Type
			if targetType == "" {
				targetType = RecordTypeBlueprint
			}
			if targetType != RecordTypeBlueprint && targetType != RecordTypeBug {
				result.Add(Issue{
					RecordID: record.ID,
					Field:    "supersedes",
					Message: fmt.Sprintf(
						"Bug records may only supersede a Blueprint or Bug, but %s is a %s. "+
							"Use implements for downward tier references, related for lateral informational links.",
						record.Supersedes, targetType,
					),
					Severity: SeverityError,
				})
			}
		}
	}

	if hasExtendsRef {
		target, exists := l.Loader.GetRecord(record.Extends)
		if !exists {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "extends",
				Message:  fmt.Sprintf("extends references unknown record: %s", record.Extends),
				Severity: SeverityError,
			})
			return
		}
		targetType := target.Type
		if targetType == "" {
			targetType = RecordTypeBlueprint
		}
		if targetType != RecordTypeBlueprint && targetType != RecordTypeBug {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "extends",
				Message: fmt.Sprintf(
					"Bug records may only extend a Blueprint or Bug, but %s is a %s. "+
						"Use implements for downward tier references, related for lateral informational links.",
					record.Extends, targetType,
				),
				Severity: SeverityError,
			})
		}
	}
}

// validateImmutability checks whether an implemented record's content matches its
// sealed hash in the manifest. No git traversal is required; the check is a direct
// cryptographic comparison against the stored hash.
func (l *Linter) validateImmutability(record *Record, result *LintResult) {
	if record.Status != StatusImplemented {
		return
	}

	if l.Hasher == nil || !l.Hasher.ManifestExists() {
		return
	}

	stored, current, ok, err := l.Hasher.VerifyRecord(record)
	if err != nil {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "integrity",
			Message:  fmt.Sprintf("Could not verify content hash: %v", err),
			Severity: SeverityWarning,
		})
		return
	}

	if !ok {
		// Record pre-dates the hash manifest — no hash to check yet. Not an error.
		return
	}

	if stored != current {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "integrity",
			Message:  fmt.Sprintf("PROV-IMM: content hash mismatch — record was modified after being sealed (stored %s, current %s)", stored[:12], current[:12]),
			Severity: SeverityError,
		})
	}
}

// checkDeadRecords checks if any governed files have been deleted
// A record is only considered dead if NO files match ANY of the patterns in affected_scope
func (l *Linter) checkDeadRecords(result *LintResult) {
	for _, record := range l.Loader.Records {
		// Only check affected_scope - forbidden_scope is ignored for dead record detection
		// A record should only be dead when no files match any affected_scope pattern
		if len(record.AffectedScope) == 0 {
			continue // No scope to check
		}

		anyMatch := false

		for _, pattern := range record.AffectedScope {
			// Skip empty patterns
			if strings.TrimSpace(pattern) == "" {
				continue
			}

			// Check regex patterns
			if len(pattern) > 3 && pattern[:3] == "re:" {
				if l.patternHasMatches(pattern, true) {
					anyMatch = true
					break
				}
				continue
			}

			// Check glob patterns
			if strings.Contains(pattern, "*") || strings.Contains(pattern, "?") {
				if l.patternHasMatches(pattern, false) {
					anyMatch = true
					break
				}
				continue
			}

			// Check exact paths
			if _, err := os.Stat(pattern); !os.IsNotExist(err) {
				anyMatch = true
				break
			}
		}

		// Only mark as dead if no patterns matched any files
		if !anyMatch {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "scope",
				Message:  "Dead record: all governed files have been deleted. Consider deprecating this record.",
				Severity: SeverityWarning,
			})
		}
	}
}

// patternHasMatches checks if a pattern matches at least one file
// If isRegex is true, treats pattern as a regex (with "re:" prefix stripped)
// Otherwise treats pattern as a glob
func (l *Linter) patternHasMatches(pattern string, isRegex bool) bool {
	if isRegex {
		regex := pattern[3:] // Strip "re:" prefix
		re, err := regexp.Compile(regex)
		if err != nil {
			return false // Invalid regex can't match anything
		}

		foundMatch := false
		filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			if re.MatchString(path) {
				foundMatch = true
				return filepath.SkipDir
			}
			return nil
		})
		return foundMatch
	}

	// Glob pattern
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return false
	}
	return len(matches) > 0
}

// validateSealedAtSHA checks that sealed_at_sha is only present on implemented/superseded/deprecated records and has valid format
func (l *Linter) validateSealedAtSHA(record *Record, result *LintResult) {
	if record.SealedAtSHA == "" {
		return // Not set, that's fine
	}

	// sealed_at_sha should only be set for records that have been implemented (including those
	// subsequently superseded or deprecated, since they must have been implemented first)
	if record.Status != StatusImplemented && record.Status != StatusSuperseded && record.Status != StatusDeprecated {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "sealed_at_sha",
			Message:  fmt.Sprintf("sealed_at_sha is set but record status is %s (should only be set for implemented, superseded, or deprecated records)", record.Status),
			Severity: SeverityWarning,
		})
	}

	// Validate SHA format (should be 7-40 hex characters)
	shaPattern := regexp.MustCompile(`^[a-f0-9]{7,40}$`)
	if !shaPattern.MatchString(record.SealedAtSHA) {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "sealed_at_sha",
			Message:  fmt.Sprintf("Invalid sealed_at_sha format: %s (expected 7-40 hex characters)", record.SealedAtSHA),
			Severity: SeverityWarning,
		})
	}
}

// checkLockedScope validates that open records don't overlap with locked records' surfaces
func (l *Linter) checkLockedScope(result *LintResult) {
	var lockedRecords []*Record
	for _, r := range l.Loader.Records {
		if r.Locked {
			lockedRecords = append(lockedRecords, r)
		}
	}
	if len(lockedRecords) == 0 {
		return
	}

	for _, openRecord := range l.Loader.FilterByStatus(StatusOpen) {
		var openSurfaces []string
		openSurfaces = append(openSurfaces, openRecord.AffectedScope...)
		for _, spec := range openRecord.AssociatedSpecs {
			openSurfaces = append(openSurfaces, spec.Path)
		}

		for _, locked := range lockedRecords {
			// Skip if open record supersedes this specific locked record
			if openRecord.Supersedes == locked.ID {
				continue
			}

			var lockedSurfaces []string
			lockedSurfaces = append(lockedSurfaces, locked.AffectedScope...)
			for _, spec := range locked.AssociatedSpecs {
				lockedSurfaces = append(lockedSurfaces, spec.Path)
			}

			for _, openSurface := range openSurfaces {
				for _, lockedSurface := range lockedSurfaces {
					if patternsOverlap(openSurface, lockedSurface) {
						field := "affected_scope"
						if !containsString(openRecord.AffectedScope, openSurface) {
							field = "associated_specs"
						}
						result.Add(Issue{
							RecordID: openRecord.ID,
							Field:    field,
							Message:  fmt.Sprintf("Surface %q overlaps with locked record %s surface %q. Resolving this is a governance decision: narrow this record's scope, or — only if you are revising the locked decision — supersede %s to reopen the layer.", openSurface, locked.ID, lockedSurface, locked.ID),
							Severity: SeverityError,
						})
					}
				}
			}
		}
	}
}

// validateSupersessionType checks that a supersedes relationship stays within the same tier,
// with the following exceptions from the enforcement matrix (always-on, graph integrity):
//   - Bug may supersede Blueprint (the core bug-fix supersession case)
//   - Bug may supersede Bug (a corrected bug fix)
//   - Imprint may supersede Imprint, but both must share the same implements value
func (l *Linter) validateSupersessionType(record *Record, result *LintResult) {
	if record.Supersedes == "" || record.Supersedes == "null" {
		return
	}

	target, exists := l.Loader.GetRecord(record.Supersedes)
	if !exists {
		return // Already reported by validateSupersedes
	}

	// Resolve effective types — records with no type field default to blueprint
	recordType := record.Type
	if recordType == "" {
		recordType = RecordTypeBlueprint
	}
	targetType := target.Type
	if targetType == "" {
		targetType = RecordTypeBlueprint
	}

	// Bug-specific supersession exceptions handled by validateBugConditionals.
	// Bug may supersede Blueprint or Bug — skip the type-equality check.
	if recordType == RecordTypeBug {
		return
	}

	// Imprint may supersede Imprint, but both must implement the same Blueprint.
	if recordType == RecordTypeImprint && targetType == RecordTypeImprint {
		if record.Implements != target.Implements {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "supersedes",
				Message: fmt.Sprintf(
					"Imprints may only supersede Imprints that implement the same Blueprint. "+
						"This record implements %s but %s implements %s.",
					record.Implements, record.Supersedes, target.Implements,
				),
				Severity: SeverityError,
			})
		}
		return
	}

	if recordType != targetType {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "supersedes",
			Message: fmt.Sprintf(
				"Type mismatch in supersedes: a %s record cannot supersede a %s record (%s). "+
					"Supersession must be within the same tier. "+
					"Use 'related' or 'implements' for cross-tier relationships.",
				recordType, targetType, record.Supersedes,
			),
			Severity: SeverityError,
		})
	}
}

// isRemoteRecord reports whether a record was loaded from a shared repo cache
// rather than from the local provenance directory. Remote records are read-only
// and governed by their origin repo, so enforcement-level checks (scope,
// associated_specs) are skipped for them.
func (l *Linter) isRemoteRecord(record *Record) bool {
	if record.FilePath == "" || l.Loader.Dir == "" {
		return false
	}
	dir := l.Loader.Dir
	if !strings.HasSuffix(dir, string(os.PathSeparator)) {
		dir += string(os.PathSeparator)
	}
	return !strings.HasPrefix(record.FilePath, dir)
}

// validateImplements checks the implements field for resolution and type correctness.
// Both checks are always-on regardless of enforcement level (graph integrity rules).
func (l *Linter) validateImplements(record *Record, result *LintResult) {
	if record.Implements == "" {
		return
	}

	// Validate the ID format before attempting resolution.
	if !IsValidID(record.Implements) {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "implements",
			Message: fmt.Sprintf(
				"implements value %q is not a valid provenance record ID (expected prov-YYYY-NNN format).",
				record.Implements,
			),
			Severity: SeverityError,
		})
		return
	}

	// Rule: implements reference must resolve (local or shared repo cache).
	target, exists := l.Loader.GetRecord(record.Implements)
	if !exists {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "implements",
			Message: fmt.Sprintf(
				"implements references unknown record: %s. "+
					"The referenced record was not found locally or in any configured shared_repo cache. "+
					"If this record lives in a remote repository, add it to shared_repos in .linespec.yml and run 'linespec provenance sync'.",
				record.Implements,
			),
			Severity: SeverityError,
		})
		return
	}

	// Rule: implements type relationship must be exactly one tier up.
	// Allowed: blueprint implements brief, imprint implements blueprint.
	// Not allowed: brief, bug.
	recordType := record.Type
	if recordType == "" {
		recordType = RecordTypeBlueprint
	}
	targetType := target.Type
	if targetType == "" {
		targetType = RecordTypeBlueprint
	}

	validParent := map[RecordType]RecordType{
		RecordTypeBlueprint: RecordTypeBrief,
		RecordTypeImprint:   RecordTypeBlueprint,
	}

	expectedParent, allowed := validParent[recordType]
	if !allowed {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "implements",
			Message: fmt.Sprintf(
				"A %s record cannot use the implements field. "+
					"Only blueprint (implements brief) and imprint (implements blueprint) records may use implements.",
				recordType,
			),
			Severity: SeverityError,
		})
		return
	}

	if targetType != expectedParent {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "implements",
			Message: fmt.Sprintf(
				"Invalid implements relationship: a %s record must implement a %s record, "+
					"but %s is a %s. "+
					"Allowed relationships: blueprint implements brief, imprint implements blueprint.",
				recordType, expectedParent, record.Implements, targetType,
			),
			Severity: SeverityError,
		})
	}
}

// validateExtends checks that the extends field references a valid record.
// Target existence is validated here; type constraints are enforced in validateBugConditionals.
func (l *Linter) validateExtends(record *Record, result *LintResult) {
	if strings.TrimSpace(record.Extends) == "" {
		return
	}

	if !IsValidID(record.Extends) {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "extends",
			Message:  fmt.Sprintf("extends value %q is not a valid provenance record ID (expected prov-YYYY-NNN format).", record.Extends),
			Severity: SeverityError,
		})
	}
}

// validateImprintScopeContainment checks that every file matched by an imprint's
// affected_scope is also matched by its blueprint's affected_scope (PROV023).
// Exempt when the imprint is draft/remote (handled by callers), the imprint has
// no affected_scope, or the blueprint has no affected_scope (unconstrained).
func (l *Linter) validateImprintScopeContainment(record *Record, result *LintResult) {
	effectiveType := record.Type
	if effectiveType == "" {
		effectiveType = RecordTypeBlueprint
	}
	if effectiveType != RecordTypeImprint {
		return
	}
	// Only enforce on open imprints — terminal records predate this rule.
	if record.Status != StatusOpen {
		return
	}
	if record.Implements == "" || len(record.AffectedScope) == 0 {
		return
	}

	blueprint, exists := l.Loader.GetRecord(record.Implements)
	if !exists || len(blueprint.AffectedScope) == 0 {
		return
	}

	for _, file := range l.filesMatchingPatterns(record.AffectedScope) {
		covered := false
		for _, bpPattern := range blueprint.AffectedScope {
			if ok, _ := MatchPattern(file, bpPattern); ok {
				covered = true
				break
			}
		}
		if !covered {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "affected_scope",
				Message: fmt.Sprintf(
					"PROV023: imprint scope exceeds blueprint scope — %q is governed by this imprint but not covered by any pattern in %s's affected_scope. Widen the blueprint's scope first.",
					file, blueprint.ID,
				),
				Severity: SeverityError,
			})
		}
	}
}

// filesMatchingPatterns returns all files on disk matched by any of the given patterns.
func (l *Linter) filesMatchingPatterns(patterns []string) []string {
	seen := make(map[string]bool)
	var files []string

	for _, pattern := range patterns {
		if strings.TrimSpace(pattern) == "" {
			continue
		}

		if len(pattern) > 3 && pattern[:3] == "re:" {
			re, err := regexp.Compile(pattern[3:])
			if err != nil {
				continue
			}
			filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				if re.MatchString(path) && !seen[path] {
					seen[path] = true
					files = append(files, path)
				}
				return nil
			})
			continue
		}

		if strings.Contains(pattern, "*") || strings.Contains(pattern, "?") {
			globRegex := GlobToRegex(pattern)
			re, err := regexp.Compile(globRegex)
			if err != nil {
				continue
			}
			filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				if re.MatchString(path) && !seen[path] {
					seen[path] = true
					files = append(files, path)
				}
				return nil
			})
			continue
		}

		// Exact path
		if _, err := os.Stat(pattern); err == nil && !seen[pattern] {
			seen[pattern] = true
			files = append(files, pattern)
		}
	}

	return files
}

// containsString returns true if the slice contains the string
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
