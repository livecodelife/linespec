package mysql

import (
	"testing"

	"github.com/livecodelife/linespec/pkg/registry"
)

func TestProxyDatabaseName(t *testing.T) {
	// Create a new proxy
	proxy := NewProxy("localhost:3306", "localhost:3307", registry.NewMockRegistry())

	// Check default database name (backward compatibility)
	if proxy.GetDatabaseName() != "todo_api_development" {
		t.Errorf("Default database name = %q, expected todo_api_development", proxy.GetDatabaseName())
	}

	// Set a custom database name
	proxy.SetDatabaseName("custom_db")
	if proxy.GetDatabaseName() != "custom_db" {
		t.Errorf("Database name after SetDatabaseName = %q, expected custom_db", proxy.GetDatabaseName())
	}

	// Test with empty string (should still set it)
	proxy.SetDatabaseName("")
	if proxy.GetDatabaseName() != "" {
		t.Errorf("Database name after SetDatabaseName('') = %q, expected empty", proxy.GetDatabaseName())
	}

	// Test with different database names
	testNames := []string{
		"myapp_production",
		"test_db_123",
		"my-service_development",
		"CamelCaseDB",
	}

	for _, name := range testNames {
		proxy.SetDatabaseName(name)
		if proxy.GetDatabaseName() != name {
			t.Errorf("Database name after SetDatabaseName(%q) = %q, expected %q", name, proxy.GetDatabaseName(), name)
		}
	}
}

func TestProxyNewProxyWithDefaultDatabase(t *testing.T) {
	// Ensure NewProxy always sets the default database name
	proxy := NewProxy("localhost:3306", "localhost:3307", registry.NewMockRegistry())

	// The proxy should have the default database name set via GetDatabaseName()
	if proxy.GetDatabaseName() == "" {
		t.Error("NewProxy should set a default database name")
	}

	// Verify it's the expected default
	if proxy.GetDatabaseName() != "todo_api_development" {
		t.Errorf("NewProxy default database = %q, expected todo_api_development", proxy.GetDatabaseName())
	}
}

func TestProxyUsesDatabaseProxyConfig(t *testing.T) {
	// Test that the proxy properly delegates to DatabaseProxyConfig
	proxy := NewProxy("localhost:3306", "localhost:3307", registry.NewMockRegistry())

	// Should start with default
	if proxy.GetDatabaseName() != "todo_api_development" {
		t.Errorf("Initial database name = %q", proxy.GetDatabaseName())
	}

	// Change to PostgreSQL-style name
	proxy.SetDatabaseName("postgres")
	if proxy.GetDatabaseName() != "postgres" {
		t.Errorf("After SetDatabaseName('postgres') = %q", proxy.GetDatabaseName())
	}

	// Change to custom service name
	proxy.SetDatabaseName("order_service")
	if proxy.GetDatabaseName() != "order_service" {
		t.Errorf("After SetDatabaseName('order_service') = %q", proxy.GetDatabaseName())
	}
}
