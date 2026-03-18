package postgresql

import (
	"testing"

	"github.com/livecodelife/linespec/pkg/registry"
)

func TestProxyDatabaseName(t *testing.T) {
	// Create a new proxy
	proxy := NewProxy("localhost:5432", "localhost:5433", registry.NewMockRegistry())

	// Check default database name (should be "postgres" for PostgreSQL)
	if proxy.GetDatabaseName() != "postgres" {
		t.Errorf("Default database name = %q, expected postgres", proxy.GetDatabaseName())
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
		"order_service",
		"myapp_production",
		"test_db_123",
		"user_service_development",
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
	proxy := NewProxy("localhost:5432", "localhost:5433", registry.NewMockRegistry())

	// The proxy should have the default database name set via GetDatabaseName()
	if proxy.GetDatabaseName() == "" {
		t.Error("NewProxy should set a default database name")
	}

	// Verify it's the expected default for PostgreSQL
	if proxy.GetDatabaseName() != "postgres" {
		t.Errorf("NewProxy default database = %q, expected postgres", proxy.GetDatabaseName())
	}
}

func TestProxyUsesDatabaseProxyConfig(t *testing.T) {
	// Test that the proxy properly delegates to DatabaseProxyConfig
	proxy := NewProxy("localhost:5432", "localhost:5433", registry.NewMockRegistry())

	// Should start with PostgreSQL default
	if proxy.GetDatabaseName() != "postgres" {
		t.Errorf("Initial database name = %q", proxy.GetDatabaseName())
	}

	// Change to MySQL-style name
	proxy.SetDatabaseName("todo_api_development")
	if proxy.GetDatabaseName() != "todo_api_development" {
		t.Errorf("After SetDatabaseName('todo_api_development') = %q", proxy.GetDatabaseName())
	}

	// Change to custom service name
	proxy.SetDatabaseName("notification_service")
	if proxy.GetDatabaseName() != "notification_service" {
		t.Errorf("After SetDatabaseName('notification_service') = %q", proxy.GetDatabaseName())
	}
}

func TestProxyMultipleInstances(t *testing.T) {
	// Each proxy instance should have its own database name
	proxy1 := NewProxy("localhost:5432", "localhost:5433", registry.NewMockRegistry())
	proxy2 := NewProxy("localhost:5434", "localhost:5435", registry.NewMockRegistry())

	// Set different names
	proxy1.SetDatabaseName("service_a")
	proxy2.SetDatabaseName("service_b")

	// Verify isolation
	if proxy1.GetDatabaseName() != "service_a" {
		t.Errorf("proxy1 database = %q, expected service_a", proxy1.GetDatabaseName())
	}

	if proxy2.GetDatabaseName() != "service_b" {
		t.Errorf("proxy2 database = %q, expected service_b", proxy2.GetDatabaseName())
	}
}
