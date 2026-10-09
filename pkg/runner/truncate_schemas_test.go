package runner

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Specifies prov-2026-059ba152. Interface under test (does not exist yet):
//
//	func truncatePostgreSQLSchemas(ctx context.Context, db *sql.DB, schemas []string) error
//
// A free function, not a TestSuite method: connection + schema list in, so it
// can become Dependency.Reset unchanged. Empty/nil schemas means ["public"].
// For each schema it lists tables with one pg_tables query (schema as bind arg
// or quoted literal, ONE scanned column = table name) and executes exactly
//
//	TRUNCATE TABLE "<schema>"."<table>" CASCADE
//
// for every table except schema_migrations. Per-table failures are tolerated.

type truncFake struct {
	mu     sync.Mutex
	tables map[string][]string
	execs  []string
}

type truncConnector struct{ f *truncFake }

func (c truncConnector) Connect(context.Context) (driver.Conn, error) { return truncConn(c), nil }
func (c truncConnector) Driver() driver.Driver                        { return truncDriver{} }

type truncDriver struct{}

func (truncDriver) Open(string) (driver.Conn, error) { return nil, io.EOF }

type truncConn struct{ f *truncFake }

func (truncConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (truncConn) Close() error                        { return nil }
func (truncConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

var truncLiteral = regexp.MustCompile(`'([^']*)'`)

func (c truncConn) ExecContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	c.f.execs = append(c.f.execs, q)
	return driver.RowsAffected(0), nil
}

func (c truncConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	var tokens []string
	for _, a := range args {
		if s, ok := a.Value.(string); ok {
			tokens = append(tokens, s)
		}
	}
	for _, m := range truncLiteral.FindAllStringSubmatch(q, -1) {
		tokens = append(tokens, m[1])
	}
	rows := &truncRows{}
	if !strings.Contains(q, "pg_tables") {
		return rows, nil
	}
	for _, tok := range tokens {
		if tbls, ok := c.f.tables[tok]; ok {
			for _, tb := range tbls {
				// Honour a SQL-side exclusion if the implementation uses one.
				if tb == "schema_migrations" && strings.Contains(q, "schema_migrations") {
					continue
				}
				rows.data = append(rows.data, tb)
			}
			break
		}
	}
	return rows, nil
}

type truncRows struct {
	data []string
	i    int
}

func (r *truncRows) Columns() []string { return []string{"tablename"} }
func (r *truncRows) Close() error      { return nil }
func (r *truncRows) Next(dest []driver.Value) error {
	if r.i >= len(r.data) {
		return io.EOF
	}
	dest[0] = r.data[r.i]
	r.i++
	return nil
}

func runTruncate(t *testing.T, tables map[string][]string, schemas []string) []string {
	t.Helper()
	f := &truncFake{tables: tables}
	db := sql.OpenDB(truncConnector{f})
	defer db.Close()
	if err := truncatePostgreSQLSchemas(context.Background(), db, schemas); err != nil {
		t.Fatalf("truncatePostgreSQLSchemas: %v", err)
	}
	out := append([]string(nil), f.execs...)
	sort.Strings(out)
	return out
}

func TestTruncateSchemasQuotedQualifiedAcrossSchemas(t *testing.T) {
	got := runTruncate(t, map[string][]string{
		"cnp_global": {"errors", "tenants"},
		"cnp_ops_ca": {"orders"},
		"unlisted":   {"nope"},
	}, []string{"cnp_global", "cnp_ops_ca"})
	want := []string{
		`TRUNCATE TABLE "cnp_global"."errors" CASCADE`,
		`TRUNCATE TABLE "cnp_global"."tenants" CASCADE`,
		`TRUNCATE TABLE "cnp_ops_ca"."orders" CASCADE`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("statements:\n got %q\nwant %q", got, want)
	}
}

func TestTruncateSchemasSkipsSchemaMigrations(t *testing.T) {
	got := runTruncate(t, map[string][]string{
		"s1": {"schema_migrations", "things"},
		"s2": {"schema_migrations"},
	}, []string{"s1", "s2"})
	want := []string{`TRUNCATE TABLE "s1"."things" CASCADE`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("statements:\n got %q\nwant %q", got, want)
	}
}

func TestTruncateSchemasDefaultsToPublic(t *testing.T) {
	tables := map[string][]string{"public": {"users"}, "other": {"x"}}
	want := []string{`TRUNCATE TABLE "public"."users" CASCADE`}
	for name, schemas := range map[string][]string{"nil": nil, "empty": {}} {
		if got := runTruncate(t, tables, schemas); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %q want %q", name, got, want)
		}
	}
}

func TestTruncateSchemasQuotesHostileIdentifiers(t *testing.T) {
	got := runTruncate(t, map[string][]string{`we"ird`: {`ta"ble`}}, []string{`we"ird`})
	want := []string{`TRUNCATE TABLE "we""ird"."ta""ble" CASCADE`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}
