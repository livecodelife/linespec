package mysql

import (
	"testing"

	"github.com/livecodelife/linespec/pkg/registry"
)

func TestProxyDatabaseName(t *testing.T) {
	proxy := NewProxy("localhost:3306", "localhost:3307", registry.NewMockRegistry())

	// Default database name is empty — must be set explicitly
	if proxy.GetDatabaseName() != "" {
		t.Errorf("Default database name = %q, expected empty", proxy.GetDatabaseName())
	}

	// SetDatabaseName sets it
	proxy.SetDatabaseName("custom_db")
	if proxy.GetDatabaseName() != "custom_db" {
		t.Errorf("Database name after SetDatabaseName = %q, expected custom_db", proxy.GetDatabaseName())
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

func TestProxyNewProxyHasEmptyDatabaseName(t *testing.T) {
	proxy := NewProxy("localhost:3306", "localhost:3307", registry.NewMockRegistry())

	// NewProxy must NOT set any hardcoded default — database name must come from config
	if proxy.GetDatabaseName() != "" {
		t.Errorf("NewProxy should not set a default database name, got %q", proxy.GetDatabaseName())
	}
}

func TestProxyUsesDatabaseProxyConfig(t *testing.T) {
	proxy := NewProxy("localhost:3306", "localhost:3307", registry.NewMockRegistry())

	// Should start empty
	if proxy.GetDatabaseName() != "" {
		t.Errorf("Initial database name = %q, expected empty", proxy.GetDatabaseName())
	}

	proxy.SetDatabaseName("postgres")
	if proxy.GetDatabaseName() != "postgres" {
		t.Errorf("After SetDatabaseName('postgres') = %q", proxy.GetDatabaseName())
	}

	proxy.SetDatabaseName("order_service")
	if proxy.GetDatabaseName() != "order_service" {
		t.Errorf("After SetDatabaseName('order_service') = %q", proxy.GetDatabaseName())
	}
}
