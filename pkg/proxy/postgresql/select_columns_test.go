package postgresql

import (
	"reflect"
	"testing"
)

// Assumed pure column selector (to be added in proxy.go by the implementation
// stage, replacing extractSelectColumns / inferColumnsForTable on the mock-only
// Describe paths):
//
//	func selectColumns(query string, schemaCache map[string][]ColumnInfo) []<column>
//
// where <column> is any struct with string fields Name and Type. Name is the
// RowDescription column name (alias if present, implicit function name for
// aggregates and calls, bare column name otherwise). Type is the discovered
// information_schema-style type from schemaCache ("integer", "uuid", ...) when
// the column maps to a table column, and "" when it cannot be typed (the caller
// then uses the existing name/value-based OID heuristics). The element type is
// read by reflection here so the test does not depend on its Go name.
//
// The table key is resolved from the query alone (resolveTable with no
// registry), so a schema-qualified FROM picks the "schema.table" cache key.
// No proxy, connection or registry state is involved.

// scTable builds a schemaCache entry from alternating name, type pairs.
func scTable(pairs ...string) []ColumnInfo {
	out := make([]ColumnInfo, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, ColumnInfo{Field: pairs[i], Type: pairs[i+1]})
	}
	return out
}

// scApplications is a 12-column table, as in the reported Npgsql failure.
func scApplications() []ColumnInfo {
	return scTable(
		"applicationid", "integer",
		"applicationname", "character varying",
		"description", "text",
		"owner", "character varying",
		"isactive", "boolean",
		"createdby", "character varying",
		"createdon", "timestamp without time zone",
		"modifiedby", "character varying",
		"modifiedon", "timestamp without time zone",
		"version", "integer",
		"tenantid", "uuid",
		"notes", "text",
	)
}

func scCache() map[string][]ColumnInfo {
	return map[string][]ColumnInfo{
		"applications": scApplications(),
		"users": scTable(
			"id", "uuid",
			"email", "character varying",
			"age", "integer",
			"created_at", "timestamp without time zone",
		),
		// 7-column table under a schema-qualified key; errorcode is integer here.
		"cnp_global.errors": scTable(
			"errorid", "integer",
			"errorcode", "integer",
			"errormessage", "text",
			"severity", "character varying",
			"isretryable", "boolean",
			"createdon", "timestamp without time zone",
			"traceid", "uuid",
		),
		// Decoy bare key (first-listed-schema alias) with a different type for
		// errorcode: a qualified query must not be typed from this one.
		"errors": scTable(
			"errorid", "integer",
			"errorcode", "text",
		),
	}
}

// scRead projects the Name and Type fields of whatever column struct
// selectColumns returns.
func scRead(t *testing.T, got interface{}) (names, types []string) {
	t.Helper()
	v := reflect.ValueOf(got)
	if v.Kind() != reflect.Slice {
		t.Fatalf("selectColumns must return a slice, got %T", got)
	}
	names = []string{}
	types = []string{}
	for i := 0; i < v.Len(); i++ {
		e := v.Index(i)
		n, ty := e.FieldByName("Name"), e.FieldByName("Type")
		if !n.IsValid() || !ty.IsValid() {
			t.Fatalf("column element %T needs string fields Name and Type", e.Interface())
		}
		names = append(names, n.String())
		types = append(types, ty.String())
	}
	return names, types
}

// scWant asserts the exact ordered column names. wantTypes may be nil to leave
// types unasserted; otherwise it must be the same length as wantNames.
func scWant(t *testing.T, query string, cache map[string][]ColumnInfo, wantNames, wantTypes []string) {
	t.Helper()
	names, types := scRead(t, selectColumns(query, cache))
	if !reflect.DeepEqual(names, wantNames) {
		t.Errorf("query %q\n  names = %q\n  want    %q", query, names, wantNames)
		return
	}
	if wantTypes != nil && !reflect.DeepEqual(types, wantTypes) {
		t.Errorf("query %q\n  types = %q\n  want    %q", query, types, wantTypes)
	}
}

func TestSelectColumns_ReportedNpgsqlApplications(t *testing.T) {
	// Was 12 columns (whole table); must be exactly the one selected.
	scWant(t, "SELECT applicationid FROM applications", scCache(),
		[]string{"applicationid"}, []string{"integer"})
}

func TestSelectColumns_ReportedNpgsqlSchemaQualifiedErrors(t *testing.T) {
	// Was 7 columns (whole table); typed from the qualified key, not the bare decoy.
	scWant(t, "SELECT errorcode FROM cnp_global.errors", scCache(),
		[]string{"errorcode"}, []string{"integer"})
}

func TestSelectColumns_ExplicitListKeepsSelectOrder(t *testing.T) {
	scWant(t, "SELECT email, id, age FROM users", scCache(),
		[]string{"email", "id", "age"},
		[]string{"character varying", "uuid", "integer"})
}

func TestSelectColumns_LowerCaseKeywords(t *testing.T) {
	scWant(t, "select email, id from users where id = $1", scCache(),
		[]string{"email", "id"}, []string{"character varying", "uuid"})
}

func TestSelectColumns_WhereAndOrderDoNotLeak(t *testing.T) {
	scWant(t, "SELECT id FROM users WHERE age > 3 ORDER BY created_at LIMIT 1", scCache(),
		[]string{"id"}, []string{"uuid"})
}

func TestSelectColumns_AliasWithAs(t *testing.T) {
	// Name is the alias; the type still comes from the underlying column.
	scWant(t, "SELECT id AS user_id, email AS mail FROM users", scCache(),
		[]string{"user_id", "mail"}, []string{"uuid", "character varying"})
}

func TestSelectColumns_BareAlias(t *testing.T) {
	scWant(t, "SELECT id user_id, email mail FROM users", scCache(),
		[]string{"user_id", "mail"}, []string{"uuid", "character varying"})
}

func TestSelectColumns_QualifiedColumnByTableName(t *testing.T) {
	scWant(t, "SELECT users.id, users.email FROM users", scCache(),
		[]string{"id", "email"}, []string{"uuid", "character varying"})
}

func TestSelectColumns_QualifiedColumnByTableAlias(t *testing.T) {
	// EF Core / Dapper style: FROM applications AS a, a.col
	scWant(t, `SELECT a.applicationid, a.applicationname FROM applications AS a`, scCache(),
		[]string{"applicationid", "applicationname"}, []string{"integer", "character varying"})
	scWant(t, `SELECT a.applicationid FROM applications a WHERE a.isactive`, scCache(),
		[]string{"applicationid"}, []string{"integer"})
}

func TestSelectColumns_QuotedIdentifiers(t *testing.T) {
	scWant(t, `SELECT "applicationid", "applicationname" FROM "applications"`, scCache(),
		[]string{"applicationid", "applicationname"}, []string{"integer", "character varying"})
}

func TestSelectColumns_StarExpandsFromSchema(t *testing.T) {
	names, types := scRead(t, selectColumns("SELECT * FROM users", scCache()))
	wantNames := []string{"id", "email", "age", "created_at"}
	wantTypes := []string{"uuid", "character varying", "integer", "timestamp without time zone"}
	if !reflect.DeepEqual(names, wantNames) || !reflect.DeepEqual(types, wantTypes) {
		t.Errorf("SELECT * = %q / %q, want %q / %q", names, types, wantNames, wantTypes)
	}
	for _, n := range names {
		if n == "*" {
			t.Errorf("literal \"*\" column leaked into %q", names)
		}
	}
}

func TestSelectColumns_StarExpandsApplicationsToAllTwelve(t *testing.T) {
	names, _ := scRead(t, selectColumns("SELECT * FROM applications", scCache()))
	if len(names) != 12 || names[0] != "applicationid" || names[11] != "notes" {
		t.Errorf("SELECT * FROM applications = %q, want the 12 schema columns in order", names)
	}
}

func TestSelectColumns_TableStarExpandsFromSchema(t *testing.T) {
	scWant(t, "SELECT users.* FROM users", scCache(),
		[]string{"id", "email", "age", "created_at"},
		[]string{"uuid", "character varying", "integer", "timestamp without time zone"})
	scWant(t, "SELECT u.* FROM users u", scCache(),
		[]string{"id", "email", "age", "created_at"},
		[]string{"uuid", "character varying", "integer", "timestamp without time zone"})
}

func TestSelectColumns_StarSchemaQualifiedUsesQualifiedKey(t *testing.T) {
	// 7 columns from cnp_global.errors, not the 2-column bare decoy.
	names, types := scRead(t, selectColumns("SELECT * FROM cnp_global.errors", scCache()))
	if len(names) != 7 || names[1] != "errorcode" || types[1] != "integer" {
		t.Errorf("SELECT * FROM cnp_global.errors = %q / %q, want the 7 qualified columns", names, types)
	}
}

func TestSelectColumns_StarMixedWithExplicit(t *testing.T) {
	scWant(t, "SELECT id, count(*) AS n FROM users", scCache(),
		[]string{"id", "n"}, []string{"uuid", ""})
	names, _ := scRead(t, selectColumns("SELECT users.*, 1 AS one FROM users", scCache()))
	want := []string{"id", "email", "age", "created_at", "one"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("t.* followed by an expression = %q, want %q", names, want)
	}
}

func TestSelectColumns_SchemaQualifiedTableTypedFromQualifiedKey(t *testing.T) {
	scWant(t, "SELECT errorcode, errormessage FROM cnp_global.errors", scCache(),
		[]string{"errorcode", "errormessage"}, []string{"integer", "text"})
	scWant(t, "SELECT e.errorcode FROM cnp_global.errors e", scCache(),
		[]string{"errorcode"}, []string{"integer"})
}

func TestSelectColumns_SchemaQualifiedMissFallsBackToBareKey(t *testing.T) {
	// No "other.errors" key: the bare key is the best schema available.
	scWant(t, "SELECT errorcode FROM other.errors", scCache(),
		[]string{"errorcode"}, []string{"text"})
}

func TestSelectColumns_CastKeepsColumnName(t *testing.T) {
	// Postgres names `id::text` "id". The type of a cast column is not pinned.
	scWant(t, "SELECT id::text, age::bigint FROM users", scCache(),
		[]string{"id", "age"}, nil)
	scWant(t, "SELECT CAST(age AS bigint) AS age_big FROM users", scCache(),
		[]string{"age_big"}, nil)
}

func TestSelectColumns_CountStarHasNoTableColumn(t *testing.T) {
	// Implicit name "count"; no table column, so no discovered type.
	scWant(t, "SELECT count(*) FROM users", scCache(),
		[]string{"count"}, []string{""})
	scWant(t, "SELECT COUNT(*) FROM users", scCache(),
		[]string{"count"}, []string{""})
	scWant(t, "SELECT COUNT(*) AS total FROM users", scCache(),
		[]string{"total"}, []string{""})
}

func TestSelectColumns_OtherAggregatesImplicitNames(t *testing.T) {
	scWant(t, "SELECT sum(age), max(created_at), min(age), avg(age) FROM users", scCache(),
		[]string{"sum", "max", "min", "avg"}, nil)
}

func TestSelectColumns_FunctionCallsWithCommasDoNotSplit(t *testing.T) {
	scWant(t, "SELECT coalesce(email, 'none') AS e, id FROM users", scCache(),
		[]string{"e", "id"}, []string{"", "uuid"})
	scWant(t, "SELECT concat(email, ' ', id), age FROM users", scCache(),
		[]string{"concat", "age"}, nil)
	scWant(t, "SELECT date_trunc('day', created_at) AS day, count(*) AS n FROM users", scCache(),
		[]string{"day", "n"}, nil)
	scWant(t, "SELECT COALESCE(SUM(age), 0) AS total, id FROM users", scCache(),
		[]string{"total", "id"}, nil)
}

func TestSelectColumns_CommaInStringLiteralDoesNotSplit(t *testing.T) {
	scWant(t, "SELECT 'a,b' AS lit, id FROM users", scCache(),
		[]string{"lit", "id"}, nil)
}

func TestSelectColumns_FromInsideFunctionDoesNotEndSelectList(t *testing.T) {
	// extract(year FROM x) contains a FROM that is not the clause keyword.
	scWant(t, "SELECT extract(year FROM created_at) AS yr, id FROM users", scCache(),
		[]string{"yr", "id"}, nil)
}

func TestSelectColumns_Distinct(t *testing.T) {
	scWant(t, "SELECT DISTINCT errorcode FROM cnp_global.errors", scCache(),
		[]string{"errorcode"}, []string{"integer"})
	scWant(t, "select distinct email, age from users", scCache(),
		[]string{"email", "age"}, nil)
}

func TestSelectColumns_DistinctOn(t *testing.T) {
	// DISTINCT ON (expr) is not a selected column.
	scWant(t, "SELECT DISTINCT ON (email) email, age FROM users", scCache(),
		[]string{"email", "age"}, nil)
}

func TestSelectColumns_UnknownColumnFallsBackNameOnly(t *testing.T) {
	// Present in the query but not in the schema: kept by name, untyped, no error.
	scWant(t, "SELECT id, mystery FROM users", scCache(),
		[]string{"id", "mystery"}, []string{"uuid", ""})
}

func TestSelectColumns_UnknownTableFallsBackNameOnly(t *testing.T) {
	scWant(t, "SELECT foo, bar FROM nosuchtable", scCache(),
		[]string{"foo", "bar"}, []string{"", ""})
}

func TestSelectColumns_NilAndEmptyCacheFallBackNameOnly(t *testing.T) {
	scWant(t, "SELECT applicationid FROM applications", nil,
		[]string{"applicationid"}, []string{""})
	scWant(t, "SELECT applicationid FROM applications", map[string][]ColumnInfo{},
		[]string{"applicationid"}, []string{""})
}

func TestSelectColumns_ReturningClause(t *testing.T) {
	scWant(t, "INSERT INTO users (id, email) VALUES ($1, $2) RETURNING id, email", scCache(),
		[]string{"id", "email"}, []string{"uuid", "character varying"})
	scWant(t, "UPDATE users SET age = $1 WHERE id = $2 RETURNING age AS a", scCache(),
		[]string{"a"}, []string{"integer"})
}

func TestSelectColumns_ReturningStarExpandsFromSchema(t *testing.T) {
	names, _ := scRead(t, selectColumns("INSERT INTO users (id) VALUES ($1) RETURNING *", scCache()))
	want := []string{"id", "email", "age", "created_at"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("RETURNING * = %q, want %q", names, want)
	}
}

// Queries whose current RowDescription is already right must not change.
func TestSelectColumns_AlreadyCorrectShapesUnchanged(t *testing.T) {
	cases := []struct {
		query string
		want  []string
	}{
		{"SELECT id, email FROM users", []string{"id", "email"}},
		{"SELECT id FROM users WHERE id = $1", []string{"id"}},
		{"SELECT users.id, users.email FROM users", []string{"id", "email"}},
		{"SELECT id AS user_id FROM users", []string{"user_id"}},
		{"SELECT id::text FROM users", []string{"id"}},
		{"SELECT count(*) FROM users", []string{"count"}},
		{"SELECT DISTINCT email FROM users", []string{"email"}},
		{"INSERT INTO users (email) VALUES ($1) RETURNING id", []string{"id"}},
	}
	for _, c := range cases {
		for label, cache := range map[string]map[string][]ColumnInfo{"typed": scCache(), "untyped": nil} {
			names, _ := scRead(t, selectColumns(c.query, cache))
			if !reflect.DeepEqual(names, c.want) {
				t.Errorf("[%s] %q = %q, want unchanged %q", label, c.query, names, c.want)
			}
		}
	}
}

func TestSelectColumns_PureNoMutationOfCache(t *testing.T) {
	cache := scCache()
	before := reflect.ValueOf(cache["users"]).Len()
	_ = selectColumns("SELECT * FROM users", cache)
	_ = selectColumns("SELECT id FROM users", cache)
	if after := len(cache["users"]); after != before {
		t.Errorf("schemaCache mutated: users columns %d -> %d", before, after)
	}
}
