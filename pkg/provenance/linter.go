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

// Linter validates Provenance Records according to the schema and enforcement rules
type Linter struct {
	Loader            *Loader
	Enforcement       string // none | warn | strict
	CommitTagRequired bool
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

	// Check for scope overlaps
	l.checkScopeOverlaps(result)

	// Check for dead records
	l.checkDeadRecords(result)

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

	// Validate date
	l.validateDate(record, result)

	// Validate supersedes
	l.validateSupersedes(record, result)

	// Validate superseded_by agreement
	l.validateSupersededBy(record, result)

	// Validate related
	l.validateRelated(record, result)

	// Validate scope patterns
	l.validateScopePatterns(record, result)

	// Validate scope overlap
	l.validateScopeSelfOverlap(record, result)

	// Validate scope paths exist (only for open records)
	l.validateScopePaths(record, result)

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

// validateRequiredFields checks that all required fields are present and non-empty
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
			Message:  fmt.Sprintf("Invalid status %q: must be one of open, implemented, superseded, deprecated", record.Status),
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

// validateScopePaths checks that scope patterns match actual files (only for open records)
func (l *Linter) validateScopePaths(record *Record, result *LintResult) {
	// Only validate scope paths for open records
	// This preserves dead records functionality
	if record.Status != StatusOpen {
		return
	}

	allPatterns := append(record.AffectedScope, record.ForbiddenScope...)

	for _, pattern := range allPatterns {
		// Skip empty patterns
		if strings.TrimSpace(pattern) == "" {
			continue
		}

		// Check for regex prefix
		if len(pattern) > 3 && pattern[:3] == "re:" {
			l.validateRegexPattern(record, pattern, result)
			continue
		}

		// Check for glob pattern
		if strings.Contains(pattern, "*") || strings.Contains(pattern, "?") {
			l.validateGlobPattern(record, pattern, result)
			continue
		}

		// Exact path validation
		l.validateExactPath(record, pattern, result)
	}
}

// validateExactPath checks that an exact path exists and is a file
func (l *Linter) validateExactPath(record *Record, path string, result *LintResult) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "scope",
				Message:  fmt.Sprintf("Scope path does not exist: %s", path),
				Severity: SeverityError,
			})
		} else {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "scope",
				Message:  fmt.Sprintf("Cannot access scope path %s: %v", path, err),
				Severity: SeverityError,
			})
		}
		return
	}

	if info.IsDir() {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "scope",
			Message:  fmt.Sprintf("Scope path is a directory, not a file (use glob pattern for directories): %s", path),
			Severity: SeverityError,
		})
	}
}

// validateGlobPattern checks that a glob pattern matches at least one file
func (l *Linter) validateGlobPattern(record *Record, pattern string, result *LintResult) {
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
			Severity: SeverityError,
		})
	}
}

// validateRegexPattern checks that a regex pattern matches at least one file
func (l *Linter) validateRegexPattern(record *Record, pattern string, result *LintResult) {
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
			Severity: SeverityError,
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

	// For now, we'll assume non-exact patterns might overlap
	// A more sophisticated check would be needed for complex cases
	return false
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
	// Check file existence and accessibility
	for _, spec := range record.AssociatedSpecs {
		info, err := os.Stat(spec.Path)
		if err != nil {
			if os.IsNotExist(err) {
				result.Add(Issue{
					RecordID: record.ID,
					Field:    "associated_specs",
					Message:  fmt.Sprintf("Proof artifact does not exist: %s", spec.Path),
					Severity: SeverityError,
				})
			} else {
				result.Add(Issue{
					RecordID: record.ID,
					Field:    "associated_specs",
					Message:  fmt.Sprintf("Cannot access proof artifact %s: %v", spec.Path, err),
					Severity: SeverityError,
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
				Severity: SeverityError,
			})
		}
	}

	// Check for enforcement level issues
	hasSpecs := len(record.AssociatedSpecs) > 0
	isOpen := record.Status == StatusOpen

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

// validateImmutability checks if an implemented record has been modified
func (l *Linter) validateImmutability(record *Record, result *LintResult) {
	if record.Status != StatusImplemented {
		return
	}

	// This would require comparing against git history
	// For now, we'll skip this check as it requires git integration
	// The check would compare current values against values at the time
	// the record was marked implemented
}

// checkScopeOverlaps checks for overlapping scope between open records
func (l *Linter) checkScopeOverlaps(result *LintResult) {
	openRecords := l.Loader.FilterByStatus(StatusOpen)

	for i := 0; i < len(openRecords); i++ {
		for j := i + 1; j < len(openRecords); j++ {
			a, b := openRecords[i], openRecords[j]
			overlap := l.findScopeOverlap(a, b)
			if len(overlap) > 0 {
				result.Add(Issue{
					RecordID: a.ID,
					Field:    "scope",
					Message:  fmt.Sprintf("Scope overlap with %s: %v", b.ID, overlap),
					Severity: SeverityWarning,
				})
			}
		}
	}
}

// findScopeOverlap returns files that appear in the scope of both records
func (l *Linter) findScopeOverlap(a, b *Record) []string {
	var overlap []string

	// Get all scope patterns from both records
	allA := append(a.AffectedScope, a.ForbiddenScope...)
	allB := append(b.AffectedScope, b.ForbiddenScope...)

	// Check if any pattern from A overlaps with any pattern from B
	for _, patternA := range allA {
		for _, patternB := range allB {
			if patternsOverlap(patternA, patternB) {
				overlap = append(overlap, patternA)
				break
			}
		}
	}

	return overlap
}

// checkDeadRecords checks if any governed files have been deleted
func (l *Linter) checkDeadRecords(result *LintResult) {
	for _, record := range l.Loader.Records {
		allFiles := append(record.AffectedScope, record.ForbiddenScope...)

		allDeleted := true
		anyFiles := false

		for _, pattern := range allFiles {
			// Skip patterns that are globs or regex
			if isPattern(pattern) {
				continue
			}

			anyFiles = true
			if _, err := os.Stat(pattern); !os.IsNotExist(err) {
				allDeleted = false
				break
			}
		}

		if anyFiles && allDeleted {
			result.Add(Issue{
				RecordID: record.ID,
				Field:    "scope",
				Message:  "Dead record: all governed files have been deleted. Consider deprecating this record.",
				Severity: SeverityWarning,
			})
		}
	}
}

// validateSealedAtSHA checks that sealed_at_sha is only present on implemented records and has valid format
func (l *Linter) validateSealedAtSHA(record *Record, result *LintResult) {
	if record.SealedAtSHA == "" {
		return // Not set, that's fine
	}

	// sealed_at_sha should only be set for implemented records
	if record.Status != StatusImplemented {
		result.Add(Issue{
			RecordID: record.ID,
			Field:    "sealed_at_sha",
			Message:  fmt.Sprintf("sealed_at_sha is set but record status is %s (should only be set for implemented records)", record.Status),
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
