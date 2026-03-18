package schema

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStaticDiscoverer(t *testing.T) {
	tables := []string{"users", "todos", "products"}
	d := NewStaticDiscoverer(tables)

	// Test DiscoverTables
	discovered, err := d.DiscoverTables()
	if err != nil {
		t.Fatalf("DiscoverTables failed: %v", err)
	}
	if len(discovered) != len(tables) {
		t.Errorf("Expected %d tables, got %d", len(tables), len(discovered))
	}

	// Test GetTableColumns before setting
	_, err = d.GetTableColumns("users")
	if err == nil {
		t.Error("Expected error for unset table columns")
	}

	// Set columns for a table
	columns := []ColumnInfo{
		{Name: "id", Type: "int", IsPrimary: true},
		{Name: "name", Type: "varchar(255)"},
	}
	d.SetTableColumns("users", columns)

	// Test GetTableColumns after setting
	retrieved, err := d.GetTableColumns("users")
	if err != nil {
		t.Fatalf("GetTableColumns failed: %v", err)
	}
	if len(retrieved) != 2 {
		t.Errorf("Expected 2 columns, got %d", len(retrieved))
	}
}

func TestNoOpDiscoverer(t *testing.T) {
	d := NewNoOpDiscoverer()

	// Test DiscoverTables
	tables, err := d.DiscoverTables()
	if err != nil {
		t.Fatalf("DiscoverTables failed: %v", err)
	}
	if len(tables) != 0 {
		t.Errorf("Expected 0 tables, got %d", len(tables))
	}

	// Test GetTableColumns
	_, err = d.GetTableColumns("any_table")
	if err == nil {
		t.Error("Expected error for disabled discovery")
	}
}

func TestCreateDiscoverer(t *testing.T) {
	tests := []struct {
		name         string
		mode         string
		staticTables []string
		wantErr      bool
	}{
		{"static mode", "static", []string{"users", "todos"}, false},
		{"none mode", "none", []string{}, false},
		{"auto mode", "auto", []string{}, false},
		{"invalid mode", "invalid", []string{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := CreateDiscoverer(tt.mode, tt.staticTables, "mysql", map[string]string{})
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateDiscoverer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && d == nil {
				t.Error("Expected non-nil discoverer")
			}
		})
	}
}

func TestSchemaCache(t *testing.T) {
	d := NewStaticDiscoverer([]string{"users"})
	columns := []ColumnInfo{
		{Name: "id", Type: "int", IsPrimary: true},
		{Name: "email", Type: "varchar(255)"},
	}
	d.SetTableColumns("users", columns)

	// Create temp directory for cache
	tempDir := t.TempDir()
	cacheFile := filepath.Join(tempDir, "schema_cache.json")

	// Test SaveCache
	err := d.SaveCache(cacheFile)
	if err != nil {
		t.Fatalf("SaveCache failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
		t.Error("Cache file was not created")
	}

	// Create new discoverer and load cache
	d2 := NewStaticDiscoverer([]string{})
	err = d2.LoadCache(cacheFile)
	if err != nil {
		t.Fatalf("LoadCache failed: %v", err)
	}

	// Verify loaded data
	loaded, err := d2.GetTableColumns("users")
	if err != nil {
		t.Fatalf("GetTableColumns after load failed: %v", err)
	}
	if len(loaded) != 2 {
		t.Errorf("Expected 2 columns after load, got %d", len(loaded))
	}
}

func TestGetDefaultCachePath(t *testing.T) {
	baseDir := "/some/path"
	path := GetDefaultCachePath(baseDir)
	expected := filepath.Join(baseDir, ".linespec", "schema_cache.json")
	if path != expected {
		t.Errorf("Expected %s, got %s", expected, path)
	}
}
