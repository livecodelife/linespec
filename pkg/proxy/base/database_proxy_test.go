package base

import (
	"testing"
)

func TestNewDatabaseProxyConfig(t *testing.T) {
	// Test with explicit database name
	config := NewDatabaseProxyConfig("my_database")
	if config.GetDatabaseName() != "my_database" {
		t.Errorf("GetDatabaseName() = %q, expected my_database", config.GetDatabaseName())
	}

	// Test with empty string (should use default)
	config2 := NewDatabaseProxyConfig("")
	if config2.GetDatabaseName() != "default_database" {
		t.Errorf("GetDatabaseName() with empty input = %q, expected default_database", config2.GetDatabaseName())
	}
}

func TestDatabaseProxyConfigSetAndGet(t *testing.T) {
	config := NewDatabaseProxyConfig("initial_db")

	// Test setting and getting
	testCases := []string{
		"postgres",
		"myapp_development",
		"order_service",
		"test_db_123",
		"",
	}

	for _, dbName := range testCases {
		config.SetDatabaseName(dbName)
		if config.GetDatabaseName() != dbName {
			t.Errorf("After SetDatabaseName(%q), GetDatabaseName() = %q", dbName, config.GetDatabaseName())
		}
	}
}

func TestDatabaseProxyConfigImmutability(t *testing.T) {
	// Each instance should have its own state
	config1 := NewDatabaseProxyConfig("db1")
	config2 := NewDatabaseProxyConfig("db2")

	config1.SetDatabaseName("modified_db1")

	if config1.GetDatabaseName() != "modified_db1" {
		t.Error("config1 should have modified name")
	}

	if config2.GetDatabaseName() != "db2" {
		t.Error("config2 should still have original name")
	}
}

func TestDatabaseProxyConfigDifferentDefaults(t *testing.T) {
	// MySQL default
	mysqlConfig := NewDatabaseProxyConfig("todo_api_development")
	if mysqlConfig.GetDatabaseName() != "todo_api_development" {
		t.Errorf("MySQL config = %q", mysqlConfig.GetDatabaseName())
	}

	// PostgreSQL default
	pgConfig := NewDatabaseProxyConfig("postgres")
	if pgConfig.GetDatabaseName() != "postgres" {
		t.Errorf("PostgreSQL config = %q", pgConfig.GetDatabaseName())
	}

	// Custom service
	customConfig := NewDatabaseProxyConfig("order_service")
	if customConfig.GetDatabaseName() != "order_service" {
		t.Errorf("Custom config = %q", customConfig.GetDatabaseName())
	}
}
