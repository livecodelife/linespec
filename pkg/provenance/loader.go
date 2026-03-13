package provenance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loader handles loading and managing Provenance Records
type Loader struct {
	Records     []*Record
	RecordsByID map[string]*Record
	Dir         string
	SharedRepos []string
}

// NewLoader creates a new provenance loader for the given directory
func NewLoader(dir string, sharedRepos []string) *Loader {
	return &Loader{
		Records:     make([]*Record, 0),
		RecordsByID: make(map[string]*Record),
		Dir:         dir,
		SharedRepos: sharedRepos,
	}
}

// LoadAll loads all provenance records from the configured directories
func (l *Loader) LoadAll() error {
	// Load from main directory
	if err := l.LoadFromDir(l.Dir); err != nil {
		return fmt.Errorf("failed to load from main directory: %w", err)
	}

	// Load from shared repositories
	for _, repo := range l.SharedRepos {
		if err := l.LoadFromDir(repo); err != nil {
			return fmt.Errorf("failed to load from shared repo %s: %w", repo, err)
		}
	}

	// Build ID index and validate relationships
	if err := l.BuildGraph(); err != nil {
		return fmt.Errorf("failed to build graph: %w", err)
	}

	return nil
}

// LoadFromDir loads all .yml files from the given directory
func (l *Loader) LoadFromDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// Directory doesn't exist, that's ok
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}

		// Check if it's a provenance file (prov-YYYY-NNN.yml)
		if !strings.HasPrefix(name, "prov-") {
			continue
		}

		path := filepath.Join(dir, name)
		record, err := l.LoadFile(path)
		if err != nil {
			return fmt.Errorf("failed to load %s: %w", path, err)
		}

		l.Records = append(l.Records, record)
	}

	return nil
}

// LoadFile loads a single provenance record from a YAML file
func (l *Loader) LoadFile(path string) (*Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Check for deprecated field before unmarshaling
	if strings.Contains(string(data), "associated_linespecs:") {
		return nil, fmt.Errorf("deprecated field 'associated_linespecs' found, use 'associated_specs' instead (path: %s)", path)
	}

	var record Record
	if err := yaml.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}

	record.FilePath = path

	return &record, nil
}

// BuildGraph builds the ID index and validates relationships
func (l *Loader) BuildGraph() error {
	l.RecordsByID = make(map[string]*Record)

	// Build ID index
	for _, record := range l.Records {
		if _, exists := l.RecordsByID[record.ID]; exists {
			return fmt.Errorf("duplicate record ID: %s", record.ID)
		}
		l.RecordsByID[record.ID] = record
	}

	// Validate supersedes references
	for _, record := range l.Records {
		if record.Supersedes != "" && record.Supersedes != "null" {
			if _, exists := l.RecordsByID[record.Supersedes]; !exists {
				return fmt.Errorf("record %s references unknown supersedes target: %s",
					record.ID, record.Supersedes)
			}

			// Check for circular references
			if err := l.checkCircularSupersedes(record.ID, record.Supersedes); err != nil {
				return err
			}
		}

		// Validate related references
		for _, relatedID := range record.Related {
			if _, exists := l.RecordsByID[relatedID]; !exists {
				// Don't fail for missing related records, just log a warning later
				// Related is informational only
			}
		}
	}

	return nil
}

// checkCircularSupersedes checks if adding a supersedes relationship would create a cycle
func (l *Loader) checkCircularSupersedes(startID, targetID string) error {
	visited := make(map[string]bool)
	current := targetID

	for current != "" && current != "null" {
		if visited[current] {
			return fmt.Errorf("circular supersedes chain detected involving %s", startID)
		}
		visited[current] = true

		record, exists := l.RecordsByID[current]
		if !exists {
			// Unknown record, can't verify
			break
		}

		if record.Supersedes == startID {
			return fmt.Errorf("circular supersedes chain: %s would create a cycle", startID)
		}

		current = record.Supersedes
	}

	return nil
}

// GetRecord returns a record by ID
func (l *Loader) GetRecord(id string) (*Record, bool) {
	record, exists := l.RecordsByID[id]
	return record, exists
}

// GetAllIDs returns all record IDs
func (l *Loader) GetAllIDs() []string {
	ids := make([]string, 0, len(l.RecordsByID))
	for id := range l.RecordsByID {
		ids = append(ids, id)
	}
	return ids
}

// SaveRecord saves a record to its file path, preserving original YAML formatting
func (l *Loader) SaveRecord(record *Record) error {
	if record.FilePath == "" {
		return fmt.Errorf("record has no file path")
	}

	// Ensure directory exists
	dir := filepath.Dir(record.FilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Read existing file to preserve formatting
	existingData, err := os.ReadFile(record.FilePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read existing file: %w", err)
	}

	var output []byte
	if os.IsNotExist(err) {
		// New file - marshal normally
		output, err = yaml.Marshal(record)
		if err != nil {
			return fmt.Errorf("failed to marshal record: %w", err)
		}
	} else {
		// Existing file - preserve formatting by updating specific fields
		output = l.updateRecordYAMLText(existingData, record)
	}

	// Write to file
	if err := os.WriteFile(record.FilePath, output, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// updateRecordYAMLText updates specific fields in the YAML text while preserving formatting
func (l *Loader) updateRecordYAMLText(existingData []byte, record *Record) []byte {
	content := string(existingData)
	lines := strings.Split(content, "\n")

	// Track if we're inside a multiline scalar (after | or >)
	inMultilineScalar := false
	multilineIndent := ""

	// Track which fields we've updated
	sealedAtShaUpdated := false

	// Scan through lines and update specific fields
	for i, line := range lines {
		// Check if we're entering a multiline scalar
		if !inMultilineScalar {
			trimmed := strings.TrimSpace(line)

			// Check for multiline scalar indicators at end of line
			if strings.HasSuffix(trimmed, " |") || strings.HasSuffix(trimmed, " >") ||
				strings.Contains(trimmed, " | ") || strings.Contains(trimmed, " > ") {
				inMultilineScalar = true
				// Get the indentation of this line for tracking
				multilineIndent = l.getIndentation(line)
				continue
			}

			// Only update top-level fields (not inside multiline content)
			// Check if this is a top-level field by looking at indentation
			indent := l.getIndentation(line)
			if indent == "" || indent == "  " || indent == "    " {
				// Update status field (top-level only)
				if strings.HasPrefix(trimmed, "status:") {
					lines[i] = l.updateFieldValue(line, "status", string(record.Status))
				}

				// Update sealed_at_sha field (top-level only)
				if strings.HasPrefix(trimmed, "sealed_at_sha:") {
					lines[i] = l.updateFieldValue(line, "sealed_at_sha", record.SealedAtSHA)
					sealedAtShaUpdated = true
				}

				// Update superseded_by field (top-level only)
				if strings.HasPrefix(trimmed, "superseded_by:") {
					lines[i] = l.updateFieldValue(line, "superseded_by", record.SupersededBy)
				}
			}
		} else {
			// We're inside a multiline scalar
			// Check if this line ends the scalar (empty line or less/equal indent)
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				// Empty line - scalar might end here or continue
				continue
			}

			currentIndent := l.getIndentation(line)
			// If we hit a line with less or equal indentation to the field that started the scalar,
			// we're back to regular fields
			if len(currentIndent) <= len(multilineIndent) && !strings.HasPrefix(line, " ") {
				inMultilineScalar = false
				multilineIndent = ""

				// Process this line as a regular field
				if strings.HasPrefix(trimmed, "status:") {
					lines[i] = l.updateFieldValue(line, "status", string(record.Status))
				}
				if strings.HasPrefix(trimmed, "sealed_at_sha:") {
					lines[i] = l.updateFieldValue(line, "sealed_at_sha", record.SealedAtSHA)
					sealedAtShaUpdated = true
				}
				if strings.HasPrefix(trimmed, "superseded_by:") {
					lines[i] = l.updateFieldValue(line, "superseded_by", record.SupersededBy)
				}
			}
		}
	}

	// Add sealed_at_sha field if it wasn't found and needs to be added
	if record.SealedAtSHA != "" && !sealedAtShaUpdated {
		// Find the best place to insert (after monitors or before tags)
		insertIndex := -1
		lastFieldIndex := -1

		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			// Track the last known field (not a list item or blank line, and not inside multiline)
			if trimmed != "" && !strings.HasPrefix(trimmed, "-") &&
				!strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, ":") &&
				!l.isInsideMultiline(lines, i) {
				// Skip if this is part of a multiline scalar
				if !strings.HasSuffix(trimmed, "|") && !strings.HasSuffix(trimmed, ">") {
					lastFieldIndex = i
				}
			}
		}

		if lastFieldIndex >= 0 {
			insertIndex = lastFieldIndex + 1
			// Get indentation from the previous field
			indent := l.getIndentation(lines[lastFieldIndex])
			newLine := indent + "sealed_at_sha: " + record.SealedAtSHA

			// Insert the new line
			lines = append(lines[:insertIndex], append([]string{newLine}, lines[insertIndex:]...)...)
		}
	}

	return []byte(strings.Join(lines, "\n"))
}

// isInsideMultiline checks if the given line is inside a multiline scalar
func (l *Loader) isInsideMultiline(lines []string, targetIndex int) bool {
	for i := 0; i < targetIndex; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Check if we started a multiline scalar
		if strings.HasSuffix(trimmed, " |") || strings.HasSuffix(trimmed, " >") ||
			strings.Contains(trimmed, " | ") || strings.Contains(trimmed, " > ") {
			// Check if target line is after this and indented more
			baseIndent := l.getIndentation(line)
			for j := i + 1; j < targetIndex; j++ {
				if strings.TrimSpace(lines[j]) == "" {
					continue
				}
				currentIndent := l.getIndentation(lines[j])
				if len(currentIndent) > len(baseIndent) {
					return true
				}
				// If we hit a line at same or less indent, multiline ended
				if len(currentIndent) <= len(baseIndent) {
					break
				}
			}
		}
	}
	return false
}

// updateFieldValue updates a field's value while preserving indentation
func (l *Loader) updateFieldValue(line, fieldName, newValue string) string {
	// Get the indentation from the original line
	indent := l.getIndentation(line)

	// If newValue is empty, keep the original format (field: "" or field: null)
	if newValue == "" {
		// Check if original had empty quotes
		if strings.Contains(line, `""`) || strings.Contains(line, `" "`) {
			return indent + fieldName + `: ""`
		}
		// Check if original had null
		if strings.Contains(line, "null") {
			return indent + fieldName + ": null"
		}
		return line // Keep original if we can't determine
	}

	return indent + fieldName + ": " + newValue
}

// getIndentation returns the leading whitespace of a line
func (l *Loader) getIndentation(line string) string {
	for i, c := range line {
		if c != ' ' && c != '\t' {
			return line[:i]
		}
	}
	return line
}

// FilterByStatus returns records filtered by status
func (l *Loader) FilterByStatus(status Status) []*Record {
	var result []*Record
	for _, record := range l.Records {
		if record.Status == status {
			result = append(result, record)
		}
	}
	return result
}

// FilterByTag returns records filtered by tag
func (l *Loader) FilterByTag(tag string) []*Record {
	var result []*Record
	for _, record := range l.Records {
		for _, t := range record.Tags {
			if t == tag {
				result = append(result, record)
				break
			}
		}
	}
	return result
}

// GetSupersededChain returns the chain of records that supersede the given one
func (l *Loader) GetSupersededChain(id string) []string {
	var chain []string
	current := id

	for current != "" && current != "null" {
		record, exists := l.RecordsByID[current]
		if !exists {
			break
		}

		chain = append(chain, current)

		if record.SupersededBy == "" || record.SupersededBy == "null" {
			break
		}

		current = record.SupersededBy
	}

	return chain
}

// UpdateSupersededBy updates the superseded_by field and status for all records
// that are superseded by other records
func (l *Loader) UpdateSupersededBy() {
	// Reset all superseded_by fields
	for _, record := range l.Records {
		record.SupersededBy = ""
	}

	// Set superseded_by based on supersedes relationships
	for _, record := range l.Records {
		if record.Supersedes != "" && record.Supersedes != "null" {
			target, exists := l.RecordsByID[record.Supersedes]
			if exists {
				target.SupersededBy = record.ID
				target.Status = StatusSuperseded
			}
		}
	}
}
