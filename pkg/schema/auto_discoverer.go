package schema

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// AutoDiscoverer implements Discoverer by querying the live database
type AutoDiscoverer struct {
	db            *sql.DB
	dbType        string
	excludeTables []string
}

// NewAutoDiscoverer creates an AutoDiscoverer backed by an open database connection.
// The caller is responsible for closing the db when done.
func NewAutoDiscoverer(db *sql.DB, dbType string, excludeTables []string) *AutoDiscoverer {
	return &AutoDiscoverer{db: db, dbType: dbType, excludeTables: excludeTables}
}

// DiscoverTables queries the database for all user tables and returns their names,
// excluding any in the ExcludeTables list.
func (a *AutoDiscoverer) DiscoverTables() ([]string, error) {
	var query string
	switch strings.ToLower(a.dbType) {
	case "mysql":
		query = "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE'"
	case "postgresql", "postgres":
		query = "SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = 'public'"
	default:
		return nil, fmt.Errorf("unsupported database type for auto-discovery: %s", a.dbType)
	}

	rows, err := a.db.QueryContext(context.Background(), query)
	if err != nil {
		return nil, fmt.Errorf("failed to query tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to scan table name: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tables: %w", err)
	}

	return FilterExcluded(tables, a.excludeTables), nil
}

// GetTableColumns returns column metadata for the given table.
func (a *AutoDiscoverer) GetTableColumns(table string) ([]ColumnInfo, error) {
	switch strings.ToLower(a.dbType) {
	case "mysql":
		return a.mysqlColumns(table)
	case "postgresql", "postgres":
		return a.postgresColumns(table)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", a.dbType)
	}
}

func (a *AutoDiscoverer) mysqlColumns(table string) ([]ColumnInfo, error) {
	rows, err := a.db.QueryContext(context.Background(),
		fmt.Sprintf("SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_KEY FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '%s' ORDER BY ORDINAL_POSITION", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []ColumnInfo
	for rows.Next() {
		var col ColumnInfo
		var isNullable, colKey string
		if err := rows.Scan(&col.Name, &col.Type, &isNullable, &colKey); err != nil {
			return nil, err
		}
		col.Nullable = isNullable == "YES"
		col.IsPrimary = colKey == "PRI"
		cols = append(cols, col)
	}
	return cols, rows.Err()
}

func (a *AutoDiscoverer) postgresColumns(table string) ([]ColumnInfo, error) {
	rows, err := a.db.QueryContext(context.Background(),
		`SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_schema = 'public' AND table_name = $1 ORDER BY ordinal_position`,
		table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []ColumnInfo
	for rows.Next() {
		var col ColumnInfo
		var isNullable string
		if err := rows.Scan(&col.Name, &col.Type, &isNullable); err != nil {
			return nil, err
		}
		col.Nullable = isNullable == "YES"
		cols = append(cols, col)
	}
	return cols, rows.Err()
}

// SaveCache and LoadCache delegate to the shared SchemaCache serialization.

func (a *AutoDiscoverer) SaveCache(cacheFile string) error {
	tables, err := a.DiscoverTables()
	if err != nil {
		return err
	}
	cache := SchemaCache{Tables: make(map[string][]ColumnInfo)}
	for _, t := range tables {
		cols, err := a.GetTableColumns(t)
		if err == nil {
			cache.Tables[t] = cols
		}
	}
	s := &StaticDiscoverer{Tables: tables, ColumnInfo: cache.Tables}
	return s.SaveCache(cacheFile)
}

func (a *AutoDiscoverer) LoadCache(cacheFile string) error {
	return nil // AutoDiscoverer always queries live; cache loading is a no-op
}

// FilterExcluded returns tables with any in the exclude list removed.
func FilterExcluded(tables, exclude []string) []string {
	if len(exclude) == 0 {
		return tables
	}
	excludeSet := make(map[string]bool, len(exclude))
	for _, t := range exclude {
		excludeSet[t] = true
	}
	result := make([]string, 0, len(tables))
	for _, t := range tables {
		if !excludeSet[t] {
			result = append(result, t)
		}
	}
	return result
}
