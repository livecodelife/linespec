// Package provenance implements Provenance Records functionality for LineSpec.
// Provenance Records are structured YAML artifacts that capture organizational
// intent, constraints, and reasoning behind system changes.
package provenance

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"time"
)

// Status represents the lifecycle state of a Provenance Record
type Status string

const (
	StatusOpen        Status = "open"
	StatusImplemented Status = "implemented"
	StatusSuperseded  Status = "superseded"
	StatusDeprecated  Status = "deprecated"
)

// ValidStatuses contains all valid status values
var ValidStatuses = []Status{StatusOpen, StatusImplemented, StatusSuperseded, StatusDeprecated}

// IsValid returns true if the status is a known value
func (s Status) IsValid() bool {
	switch s {
	case StatusOpen, StatusImplemented, StatusSuperseded, StatusDeprecated:
		return true
	}
	return false
}

// AssociatedSpec represents a proof artifact with optional type annotation
type AssociatedSpec struct {
	Path string `yaml:"path"`
	Type string `yaml:"type,omitempty"`
}

// Record represents a single Provenance Record
// See PROVENANCE_RECORD_SCHEMA.md for full documentation
type Record struct {
	// Required fields
	ID        string `yaml:"id"`
	Title     string `yaml:"title"`
	Status    Status `yaml:"status"`
	CreatedAt string `yaml:"created_at"`
	Author    string `yaml:"author"`

	// Intent and reasoning
	Intent      string   `yaml:"intent"`
	Constraints []string `yaml:"constraints"`

	// Scope
	AffectedScope  []string `yaml:"affected_scope"`
	ForbiddenScope []string `yaml:"forbidden_scope"`

	// Graph relationships
	Supersedes   string   `yaml:"supersedes"`
	SupersededBy string   `yaml:"superseded_by"`
	Related      []string `yaml:"related"`

	// Proof of completion
	SealedAtSHA      string           `yaml:"sealed_at_sha"`
	AssociatedSpecs  []AssociatedSpec `yaml:"associated_specs"`
	AssociatedTraces []string         `yaml:"associated_traces"`
	Monitors         []string         `yaml:"monitors"`

	// Tags
	Tags []string `yaml:"tags"`

	// File path (not stored in YAML, set during loading)
	FilePath string `yaml:"-"`
}

// IDPattern is the regex for valid provenance record IDs: prov-YYYY-NNN, prov-YYYY-XXXXXXXX, or with service suffix
// Supports both legacy sequential format (prov-YYYY-NNN) and new crypto random format (prov-YYYY-XXXXXXXX)
var IDPattern = regexp.MustCompile(`^prov-(\d{4})-(\d{3}|[a-f0-9]{8})(?:-[a-z0-9-]+)?$`)

// IsValidID returns true if the ID matches the prov-YYYY-NNN format (with optional service suffix)
func IsValidID(id string) bool {
	return IDPattern.MatchString(id)
}

// ParseID extracts year and sequence number from a valid ID
func ParseID(id string) (year int, seq int, err error) {
	matches := IDPattern.FindStringSubmatch(id)
	if matches == nil {
		return 0, 0, fmt.Errorf("invalid ID format: %s", id)
	}

	year, err = fmt.Sscanf(matches[1], "%d", &year)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid year in ID: %s", matches[1])
	}

	seq, err = fmt.Sscanf(matches[2], "%d", &seq)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid sequence in ID: %s", matches[2])
	}

	return year, seq, nil
}

// IsMutableAfterImplemented returns true if the field can be modified after status is 'implemented'
func (r *Record) IsMutableAfterImplemented(fieldName string) bool {
	switch fieldName {
	case "monitors", "associated_traces":
		return true
	default:
		return false
	}
}

// ScopeMode returns the scope mode based on affected_scope
// allowlist mode: affected_scope is non-empty
// observed mode: affected_scope is empty
func (r *Record) ScopeMode() string {
	if len(r.AffectedScope) > 0 {
		return "allowlist"
	}
	return "observed"
}

// IsInScope returns true if the given file path matches the record's scope
func (r *Record) IsInScope(filePath string) (bool, error) {
	// Check forbidden_scope first (always forbidden regardless of mode)
	for _, pattern := range r.ForbiddenScope {
		matches, err := MatchPattern(filePath, pattern)
		if err != nil {
			return false, err
		}
		if matches {
			return false, nil
		}
	}

	// If in allowlist mode, check affected_scope
	if r.ScopeMode() == "allowlist" {
		for _, pattern := range r.AffectedScope {
			matches, err := MatchPattern(filePath, pattern)
			if err != nil {
				return false, err
			}
			if matches {
				return true, nil
			}
		}
		// In allowlist mode, not matching affected_scope means forbidden
		return false, nil
	}

	// In observed mode, no implicit forbidden scope (except explicit forbidden_scope already checked)
	return true, nil
}

// MatchPattern checks if a file path matches a pattern (exact, glob, or regex)
func MatchPattern(filePath, pattern string) (bool, error) {
	// Check for regex prefix
	if len(pattern) > 3 && pattern[:3] == "re:" {
		regex := pattern[3:]
		re, err := regexp.Compile(regex)
		if err != nil {
			return false, fmt.Errorf("invalid regex pattern %q: %w", pattern, err)
		}
		return re.MatchString(filePath), nil
	}

	// Check for glob pattern
	if contains(pattern, '*') || contains(pattern, '?') {
		// Convert glob to regex
		regex := GlobToRegex(pattern)
		re, err := regexp.Compile(regex)
		if err != nil {
			return false, fmt.Errorf("invalid glob pattern %q: %w", pattern, err)
		}
		return re.MatchString(filePath), nil
	}

	// Exact match
	return filePath == pattern, nil
}

// contains returns true if the string contains the given rune
func contains(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

// GlobToRegex converts a glob pattern to a regex pattern
func GlobToRegex(glob string) string {
	var result string
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				result += ".*"
				i++ // skip the second *
			} else {
				result += "[^/]*"
			}
		case '?':
			result += "[^/]"
		case '.', '+', '(', ')', '|', '^', '$':
			result += "\\" + string(c)
		case '\\':
			if i+1 < len(glob) {
				result += "\\" + string(glob[i+1])
				i++
			}
		default:
			result += string(c)
		}
	}
	return "^" + result + "$"
}

// generateRandomHex generates 8 random hex characters using crypto/rand
func generateRandomHex() (string, error) {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("%08x", bytes), nil
}

// NextID generates the next available ID for the given year using crypto random hex
// Format: prov-YYYY-XXXXXXXX where XXXXXXXX is 8 hex characters
// Retries up to 10 times if collision occurs, then returns error
func NextID(year int, existingIDs []string) (string, error) {
	existingSet := make(map[string]bool)
	for _, id := range existingIDs {
		existingSet[id] = true
	}

	prefix := fmt.Sprintf("prov-%d-", year)

	// Try up to 10 times to generate a unique ID
	for i := 0; i < 10; i++ {
		hex, err := generateRandomHex()
		if err != nil {
			return "", fmt.Errorf("failed to generate random hex: %w", err)
		}

		newID := prefix + hex
		if !existingSet[newID] {
			return newID, nil
		}
		// Collision occurred, try again
	}

	return "", fmt.Errorf("failed to generate unique ID after 10 attempts")
}

// CurrentYear returns the current year as an integer
func CurrentYear() int {
	return time.Now().Year()
}

// CurrentDate returns the current date in ISO 8601 format (YYYY-MM-DD)
func CurrentDate() string {
	return time.Now().Format("2006-01-02")
}

// ContextRecord represents a record in the context output
type ContextRecord struct {
	Record     *Record  // The record itself
	IsAncestor bool     // True if this record is only in ancestry, not directly matched
	Ancestors  []string // Chain of supersedes relationships (oldest first, i.e., the chain from current to oldest ancestor)
}

// ContextResult holds the complete context output
type ContextResult struct {
	Files         []string         // Input files
	DirectMatches []*ContextRecord // Records that directly match the files
	Conflicts     []ScopeConflict  // Overlapping open records
}

// ScopeConflict represents two or more open records with overlapping scope
type ScopeConflict struct {
	File      string   // The file with conflicting governance
	RecordIDs []string // The IDs of conflicting records
}
