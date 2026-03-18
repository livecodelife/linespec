package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Discoverer defines the interface for schema discovery
type Discoverer interface {
	DiscoverTables() ([]string, error)
	GetTableColumns(table string) ([]ColumnInfo, error)
	SaveCache(cacheFile string) error
	LoadCache(cacheFile string) error
}

// ColumnInfo represents column metadata
type ColumnInfo struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Nullable  bool   `json:"nullable"`
	IsPrimary bool   `json:"is_primary"`
	Default   string `json:"default,omitempty"`
}

// SchemaCache represents cached schema information
type SchemaCache struct {
	Tables  map[string][]ColumnInfo `json:"tables"`
	Version string                  `json:"version"`
}

// StaticDiscoverer implements Discoverer for static table lists
type StaticDiscoverer struct {
	Tables     []string
	ColumnInfo map[string][]ColumnInfo
}

func NewStaticDiscoverer(tables []string) *StaticDiscoverer {
	return &StaticDiscoverer{
		Tables:     tables,
		ColumnInfo: make(map[string][]ColumnInfo),
	}
}

func (s *StaticDiscoverer) DiscoverTables() ([]string, error) {
	return s.Tables, nil
}

func (s *StaticDiscoverer) GetTableColumns(table string) ([]ColumnInfo, error) {
	if columns, ok := s.ColumnInfo[table]; ok {
		return columns, nil
	}
	return nil, fmt.Errorf("table %s not found", table)
}

func (s *StaticDiscoverer) SetTableColumns(table string, columns []ColumnInfo) {
	s.ColumnInfo[table] = columns
}

func (s *StaticDiscoverer) SaveCache(cacheFile string) error {
	cache := SchemaCache{
		Tables:  s.ColumnInfo,
		Version: "1.0",
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return os.WriteFile(cacheFile, data, 0644)
}

func (s *StaticDiscoverer) LoadCache(cacheFile string) error {
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return err
	}
	var cache SchemaCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return err
	}
	s.ColumnInfo = cache.Tables
	return nil
}

// NoOpDiscoverer implements Discoverer for when discovery is disabled
type NoOpDiscoverer struct{}

func NewNoOpDiscoverer() *NoOpDiscoverer {
	return &NoOpDiscoverer{}
}

func (n *NoOpDiscoverer) DiscoverTables() ([]string, error) {
	return []string{}, nil
}

func (n *NoOpDiscoverer) GetTableColumns(table string) ([]ColumnInfo, error) {
	return nil, fmt.Errorf("schema discovery disabled")
}

func (n *NoOpDiscoverer) SaveCache(cacheFile string) error {
	return nil
}

func (n *NoOpDiscoverer) LoadCache(cacheFile string) error {
	return nil
}

// CreateDiscoverer creates a discoverer based on mode and configuration
func CreateDiscoverer(mode string, staticTables []string, dbType string, dbConfig map[string]string) (Discoverer, error) {
	switch strings.ToLower(mode) {
	case "static":
		return NewStaticDiscoverer(staticTables), nil
	case "none":
		return NewNoOpDiscoverer(), nil
	case "auto":
		// For auto mode, we would need database connection
		// This will be implemented in database-specific packages
		return NewStaticDiscoverer([]string{}), nil
	default:
		return nil, fmt.Errorf("unknown schema discovery mode: %s", mode)
	}
}

// GetDefaultCachePath returns the default path for schema cache
func GetDefaultCachePath(baseDir string) string {
	return filepath.Join(baseDir, ".linespec", "schema_cache.json")
}
