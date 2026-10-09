package schema

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// This file specifies prov-2026-059ba152 (configurable non-public schemas).
//
// Interface under test (does not exist yet):
//
//	func NewAutoDiscovererWithSchemas(db *sql.DB, dbType string, excludeTables, schemas []string) *AutoDiscoverer
//	func (a *AutoDiscoverer) DiscoverSchema() (map[string][]ColumnInfo, error)
//
// Fake-database contract (a stand-in for PostgreSQL, no sqlmock in this repo):
//   - table listing: one query per schema, mentioning pg_tables, with the
//     schema name as a bind arg or a quoted literal; scans ONE column (table name).
//   - column listing: mentioning information_schema.columns, with schema and
//     table name as bind args or quoted literals; scans THREE columns
//     (column_name, data_type, is_nullable), as the existing postgresColumns does.

type fakeTable struct {
	name string
	cols []string // column names; every column is typed "text", nullable
}

type fakePG struct {
	order   []string // schema names, deterministic
	schemas map[string][]fakeTable
}

type fakePGConnector struct{ pg *fakePG }

func (c fakePGConnector) Connect(context.Context) (driver.Conn, error) { return fakePGConn{c.pg}, nil }
func (c fakePGConnector) Driver() driver.Driver                        { return fakePGDriver{} }

type fakePGDriver struct{}

func (fakePGDriver) Open(string) (driver.Conn, error) { return nil, io.EOF }

type fakePGConn struct{ pg *fakePG }

func (fakePGConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (fakePGConn) Close() error                        { return nil }
func (fakePGConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

var quotedLiteral = regexp.MustCompile(`'([^']*)'`)

func (c fakePGConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	var tokens []string
	for _, a := range args {
		if s, ok := a.Value.(string); ok {
			tokens = append(tokens, s)
		}
	}
	for _, m := range quotedLiteral.FindAllStringSubmatch(q, -1) {
		tokens = append(tokens, m[1])
	}
	schemaName := ""
	for _, tok := range tokens {
		if _, ok := c.pg.schemas[tok]; ok {
			schemaName = tok
			break
		}
	}
	tables := c.pg.schemas[schemaName]
	switch {
	case strings.Contains(q, "pg_tables"):
		rows := &fakeRows{cols: []string{"tablename"}}
		for _, t := range tables {
			rows.data = append(rows.data, []driver.Value{t.name})
		}
		return rows, nil
	case strings.Contains(q, "information_schema.columns"):
		rows := &fakeRows{cols: []string{"column_name", "data_type", "is_nullable"}}
		for _, t := range tables {
			for _, tok := range tokens {
				if tok == t.name {
					for _, col := range t.cols {
						rows.data = append(rows.data, []driver.Value{col, "text", "YES"})
					}
					return rows, nil
				}
			}
		}
		return rows, nil
	}
	return &fakeRows{}, nil
}

type fakeRows struct {
	cols []string
	data [][]driver.Value
	i    int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.i])
	r.i++
	return nil
}

func openFake(t *testing.T, pg *fakePG) *sql.DB {
	t.Helper()
	db := sql.OpenDB(fakePGConnector{pg})
	t.Cleanup(func() { db.Close() })
	return db
}

func keysOf(m map[string][]ColumnInfo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func colNames(cols []ColumnInfo) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

func TestDiscoverSchemaMultiSchemaKeysBareAndQualified(t *testing.T) {
	pg := &fakePG{schemas: map[string][]fakeTable{
		"cnp_global": {{"errors", []string{"id", "msg"}}, {"tenants", []string{"id"}}},
		"cnp_ops_ca": {{"orders", []string{"id", "total"}}},
	}}
	d := NewAutoDiscovererWithSchemas(openFake(t, pg), "postgresql", nil, []string{"cnp_global", "cnp_ops_ca"})
	got, err := d.DiscoverSchema()
	if err != nil {
		t.Fatalf("DiscoverSchema: %v", err)
	}
	want := []string{
		"cnp_global.errors", "cnp_global.tenants", "cnp_ops_ca.orders",
		"errors", "orders", "tenants",
	}
	if !reflect.DeepEqual(keysOf(got), want) {
		t.Fatalf("keys = %v, want %v", keysOf(got), want)
	}
	if !reflect.DeepEqual(colNames(got["cnp_global.errors"]), []string{"id", "msg"}) {
		t.Errorf("qualified columns wrong: %v", colNames(got["cnp_global.errors"]))
	}
	if !reflect.DeepEqual(got["errors"], got["cnp_global.errors"]) {
		t.Errorf("bare and qualified keys must carry identical columns")
	}
	if !reflect.DeepEqual(colNames(got["orders"]), []string{"id", "total"}) {
		t.Errorf("bare orders columns wrong: %v", colNames(got["orders"]))
	}
}

func TestDiscoverSchemaCollisionFirstListedSchemaWins(t *testing.T) {
	pg := &fakePG{schemas: map[string][]fakeTable{
		"a": {{"users", []string{"a_id"}}},
		"b": {{"users", []string{"b_id"}}},
	}}
	for _, tc := range []struct {
		schemas  []string
		wantBare []string
	}{
		{[]string{"a", "b"}, []string{"a_id"}},
		{[]string{"b", "a"}, []string{"b_id"}},
	} {
		d := NewAutoDiscovererWithSchemas(openFake(t, pg), "postgresql", nil, tc.schemas)
		got, err := d.DiscoverSchema()
		if err != nil {
			t.Fatalf("DiscoverSchema: %v", err)
		}
		if !reflect.DeepEqual(colNames(got["users"]), tc.wantBare) {
			t.Errorf("schemas %v: bare users = %v, want %v", tc.schemas, colNames(got["users"]), tc.wantBare)
		}
		// Qualified keys are always unambiguous, whichever schema won the bare name.
		if !reflect.DeepEqual(colNames(got["a.users"]), []string{"a_id"}) {
			t.Errorf("schemas %v: a.users = %v", tc.schemas, colNames(got["a.users"]))
		}
		if !reflect.DeepEqual(colNames(got["b.users"]), []string{"b_id"}) {
			t.Errorf("schemas %v: b.users = %v", tc.schemas, colNames(got["b.users"]))
		}
	}
}

// Public-only users must see byte-identical keys: bare names only, no
// "public."-qualified duplicates, whether schemas is unset or [public].
func TestDiscoverSchemaDefaultPublicKeysUnchanged(t *testing.T) {
	pg := &fakePG{schemas: map[string][]fakeTable{
		"public": {{"users", []string{"id"}}, {"todos", []string{"id", "title"}}},
		"other":  {{"hidden", []string{"id"}}},
	}}
	for name, schemas := range map[string][]string{"nil": nil, "empty": {}, "explicit public": {"public"}} {
		t.Run(name, func(t *testing.T) {
			d := NewAutoDiscovererWithSchemas(openFake(t, pg), "postgresql", nil, schemas)
			got, err := d.DiscoverSchema()
			if err != nil {
				t.Fatalf("DiscoverSchema: %v", err)
			}
			if want := []string{"todos", "users"}; !reflect.DeepEqual(keysOf(got), want) {
				t.Fatalf("keys = %v, want %v", keysOf(got), want)
			}
		})
	}
}

// Existing constructor keeps working and keeps its public-only behaviour.
func TestNewAutoDiscovererStillPublicOnly(t *testing.T) {
	pg := &fakePG{schemas: map[string][]fakeTable{
		"public": {{"users", []string{"id"}}},
		"other":  {{"hidden", []string{"id"}}},
	}}
	got, err := NewAutoDiscoverer(openFake(t, pg), "postgresql", nil).DiscoverTables()
	if err != nil {
		t.Fatalf("DiscoverTables: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"users"}) {
		t.Fatalf("tables = %v, want [users]", got)
	}
}

func TestDiscoverSchemaExcludeTablesApplied(t *testing.T) {
	pg := &fakePG{schemas: map[string][]fakeTable{
		"s1": {{"keep", []string{"id"}}, {"schema_migrations", []string{"version"}}},
	}}
	d := NewAutoDiscovererWithSchemas(openFake(t, pg), "postgresql", []string{"schema_migrations"}, []string{"s1"})
	got, err := d.DiscoverSchema()
	if err != nil {
		t.Fatalf("DiscoverSchema: %v", err)
	}
	if want := []string{"keep", "s1.keep"}; !reflect.DeepEqual(keysOf(got), want) {
		t.Fatalf("keys = %v, want %v", keysOf(got), want)
	}
}
