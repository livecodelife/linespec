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

// SaveRecord saves a record to its file path
func (l *Loader) SaveRecord(record *Record) error {
	if record.FilePath == "" {
		return fmt.Errorf("record has no file path")
	}

	// Ensure directory exists
	dir := filepath.Dir(record.FilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Marshal to YAML
	data, err := yaml.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal record: %w", err)
	}

	// Write to file
	if err := os.WriteFile(record.FilePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
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
