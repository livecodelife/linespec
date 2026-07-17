package postgresql

import (
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/registry"
	"github.com/livecodelife/linespec/v3/pkg/types"
)

func TestExtractTable_DynamicFromRegistry_RequiresRegistration(t *testing.T) {
	// This test verifies that extractTable uses the registry's GetTables() method
	// and will FAIL if it only relies on hardcoded tables

	// Create a proxy with an empty registry
	reg := registry.NewMockRegistry()
	proxy := NewProxy("localhost:5432", "localhost:5433", reg)

	// Register mocks with a custom table name that is NOT in the hardcoded list
	// and uses a query format that the SQL keyword parser can't handle well
	customTable := "my_custom_table_12345"

	// Register WritePostgreSQL mock
	reg.Register(&types.TestSpec{
		Name: "test-custom-table",
		Expects: []types.ExpectStatement{
			{
				Channel: types.WritePostgreSQL,
				Table:   customTable,
				SQL:     "INSERT INTO my_custom_table_12345 (id, name) VALUES ($1, $2)",
			},
		},
	})

	// Query that the SQL keyword fallback might struggle with
	// (complex nested query or unusual format)
	query := "WITH cte AS (SELECT * FROM my_custom_table_12345) INSERT INTO my_custom_table_12345 SELECT * FROM cte"

	got := proxy.extractTable(query)
	if got != customTable {
		t.Errorf("extractTable(%q) = %q, want %q - registry.GetTables() not being used", query, got, customTable)
	}

	// Also test a simple query to ensure basic functionality works
	simpleQuery := "SELECT * FROM my_custom_table_12345 WHERE id = 1"
	got = proxy.extractTable(simpleQuery)
	if got != customTable {
		t.Errorf("extractTable(%q) = %q, want %q - registry.GetTables() not being used", simpleQuery, got, customTable)
	}
}

func TestExtractTable_FallbackForUnregisteredTables(t *testing.T) {
	// Create a proxy with empty registry
	reg := registry.NewMockRegistry()
	proxy := NewProxy("localhost:5432", "localhost:5433", reg)

	// Test that we can still extract tables using SQL keyword fallback
	// for tables not explicitly registered
	tests := []struct {
		query     string
		wantTable string
	}{
		{
			query:     "SELECT * FROM unknown_table WHERE id = 1",
			wantTable: "unknown_table",
		},
		{
			query:     "INSERT INTO fallback_table VALUES (1, 2, 3)",
			wantTable: "fallback_table",
		},
		{
			query:     "UPDATE schema.table_name SET col = 'val'",
			wantTable: "table_name", // Should strip schema prefix
		},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := proxy.extractTable(tt.query)
			if got != tt.wantTable {
				t.Errorf("extractTable(%q) = %q, want %q", tt.query, got, tt.wantTable)
			}
		})
	}
}

func TestExtractTable_MixedRegistryAndHardcoded(t *testing.T) {
	// This test verifies that both registry tables and legacy hardcoded tables work
	reg := registry.NewMockRegistry()
	proxy := NewProxy("localhost:5432", "localhost:5433", reg)

	// Register a custom table
	reg.Register(&types.TestSpec{
		Name: "test",
		Expects: []types.ExpectStatement{
			{
				Channel: types.WritePostgreSQL,
				Table:   "dynamic_table",
				SQL:     "INSERT INTO dynamic_table VALUES ($1)",
			},
		},
	})

	// Test both the registered table AND a hardcoded table (if we keep hardcoded)
	tests := []struct {
		query     string
		wantTable string
	}{
		{
			query:     "INSERT INTO dynamic_table VALUES (1)",
			wantTable: "dynamic_table",
		},
		{
			// Hardcoded table from the original list
			query:     "INSERT INTO users (name) VALUES ('John')",
			wantTable: "users",
		},
		{
			// Another hardcoded table
			query:     "INSERT INTO orders (total) VALUES (100)",
			wantTable: "orders",
		},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := proxy.extractTable(tt.query)
			if got != tt.wantTable {
				t.Errorf("extractTable(%q) = %q, want %q", tt.query, got, tt.wantTable)
			}
		})
	}
}
