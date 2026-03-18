package base

// DatabaseProxyConfig holds database name configuration for any database proxy
// This provides a common base for all database types (MySQL, PostgreSQL, MongoDB, etc.)
type DatabaseProxyConfig struct {
	DatabaseName string // Configurable database name
}

// NewDatabaseProxyConfig creates a new config with the given database name
// If databaseName is empty, it uses a sensible default
func NewDatabaseProxyConfig(databaseName string) *DatabaseProxyConfig {
	if databaseName == "" {
		databaseName = "default_database"
	}
	return &DatabaseProxyConfig{
		DatabaseName: databaseName,
	}
}

// SetDatabaseName sets the database name
func (c *DatabaseProxyConfig) SetDatabaseName(name string) {
	c.DatabaseName = name
}

// GetDatabaseName returns the current database name
func (c *DatabaseProxyConfig) GetDatabaseName() string {
	return c.DatabaseName
}
