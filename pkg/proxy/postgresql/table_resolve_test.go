package postgresql

import "testing"

// Assumed pure resolver (to be added in proxy.go by the implementation stage):
//
//	func resolveTable(query string, knownTables []string, schemaCache map[string][]ColumnInfo) string
//
// It takes the query text, the registry's known table names, and the schemaCache
// (only its keys are consulted) and returns the schemaCache lookup key. Keys are
// lower-cased, matching extractTable. Discovery keys tables schema-qualified
// ("schema.table") and bare ("table"; first listed schema wins).

func cols(names ...string) []ColumnInfo {
	out := make([]ColumnInfo, len(names))
	for i, n := range names {
		out[i] = ColumnInfo{Field: n, Type: "text"}
	}
	return out
}

// multiSchemaCache mirrors DiscoverSchema for schemas [cnp_global, cnp_ops_ca]:
// errors exists in both, bare key belongs to the first listed schema.
func multiSchemaCache() map[string][]ColumnInfo {
	return map[string][]ColumnInfo{
		"cnp_global.errors":   cols("id", "global_code"),
		"cnp_ops_ca.errors":   cols("id", "ops_code", "state"),
		"errors":              cols("id", "global_code"),
		"cnp_ops_ca.sessions": cols("id", "user_id"),
		"sessions":            cols("id", "user_id"),
	}
}

func TestResolveTable_QualifiedHit(t *testing.T) {
	got := resolveTable("SELECT * FROM cnp_global.errors WHERE id = $1", []string{"errors"}, multiSchemaCache())
	if got != "cnp_global.errors" {
		t.Errorf("got %q, want cnp_global.errors", got)
	}
}

func TestResolveTable_QualifiedHitUnregisteredTable(t *testing.T) {
	// Registry knows nothing: the keyword fallback path must also keep the schema.
	got := resolveTable("SELECT * FROM cnp_ops_ca.errors", nil, multiSchemaCache())
	if got != "cnp_ops_ca.errors" {
		t.Errorf("got %q, want cnp_ops_ca.errors", got)
	}
}

func TestResolveTable_QualifiedInsertUpdate(t *testing.T) {
	cache := multiSchemaCache()
	for _, q := range []string{
		"INSERT INTO cnp_ops_ca.errors (id) VALUES ($1)",
		"UPDATE cnp_ops_ca.errors SET ops_code = $1 WHERE id = $2",
	} {
		if got := resolveTable(q, []string{"errors"}, cache); got != "cnp_ops_ca.errors" {
			t.Errorf("%q: got %q, want cnp_ops_ca.errors", q, got)
		}
	}
}

func TestResolveTable_QualifiedMissFallsBackToBare(t *testing.T) {
	// other.errors has no qualified key in the cache: fall back to bare errors.
	got := resolveTable("SELECT * FROM other.errors", []string{"errors"}, multiSchemaCache())
	if got != "errors" {
		t.Errorf("got %q, want errors", got)
	}
}

func TestResolveTable_BareStaysBareFirstSchema(t *testing.T) {
	cache := multiSchemaCache()
	got := resolveTable("SELECT * FROM errors", []string{"errors"}, cache)
	if got != "errors" {
		t.Fatalf("got %q, want bare errors", got)
	}
	// Bare key is the first-listed schema's columns.
	if f := cache[got][1].Field; f != "global_code" {
		t.Errorf("bare key should carry first schema columns, got %q", f)
	}
}

func TestResolveTable_BareUnregistered(t *testing.T) {
	got := resolveTable("SELECT * FROM sessions", nil, multiSchemaCache())
	if got != "sessions" {
		t.Errorf("got %q, want sessions", got)
	}
}

func TestResolveTable_QuotedIdentifiers(t *testing.T) {
	cache := multiSchemaCache()
	for _, q := range []string{
		`SELECT * FROM "cnp_ops_ca"."errors"`,
		`SELECT * FROM "cnp_ops_ca".errors`,
		`SELECT * FROM cnp_ops_ca."errors"`,
	} {
		for _, known := range [][]string{{"errors"}, nil} {
			if got := resolveTable(q, known, cache); got != "cnp_ops_ca.errors" {
				t.Errorf("%q known=%v: got %q, want cnp_ops_ca.errors", q, known, got)
			}
		}
	}
}

func TestResolveTable_MixedCase(t *testing.T) {
	cache := multiSchemaCache()
	for _, q := range []string{
		"SELECT * FROM CNP_Ops_CA.Errors",
		"select * From Cnp_Ops_Ca.ERRORS",
		`SELECT * FROM "CNP_OPS_CA"."ERRORS"`,
	} {
		if got := resolveTable(q, []string{"errors"}, cache); got != "cnp_ops_ca.errors" {
			t.Errorf("%q: got %q, want cnp_ops_ca.errors", q, got)
		}
	}
	if got := resolveTable("SELECT * FROM ERRORS", []string{"errors"}, cache); got != "errors" {
		t.Errorf("bare mixed case: got %q, want errors", got)
	}
}

// Same table name in two schemas: a query against the later schema must get that
// schema's columns, not the first schema's.
func TestResolveTable_SameNameTwoSchemasDistinctColumns(t *testing.T) {
	cache := multiSchemaCache()
	known := []string{"errors"}

	later := cache[resolveTable("SELECT * FROM cnp_ops_ca.errors", known, cache)]
	first := cache[resolveTable("SELECT * FROM cnp_global.errors", known, cache)]

	if len(later) != 3 || later[1].Field != "ops_code" {
		t.Errorf("later schema columns wrong: %+v", later)
	}
	if len(first) != 2 || first[1].Field != "global_code" {
		t.Errorf("first schema columns wrong: %+v", first)
	}
}

// Public-only users: discovery emits bare keys only, so resolution is unchanged.
func TestResolveTable_PublicOnlyUnchanged(t *testing.T) {
	cache := map[string][]ColumnInfo{
		"orders": cols("id", "amount"),
		"users":  cols("id", "name"),
	}
	cases := []struct {
		query string
		known []string
		want  string
	}{
		{"SELECT * FROM orders", []string{"orders"}, "orders"},
		{"SELECT * FROM public.orders", []string{"orders"}, "orders"},
		{`SELECT * FROM "public"."users"`, nil, "users"},
		{"INSERT INTO users (id) VALUES ($1)", nil, "users"},
	}
	for _, c := range cases {
		if got := resolveTable(c.query, c.known, cache); got != c.want {
			t.Errorf("%q: got %q, want %q", c.query, got, c.want)
		}
	}
}

func TestResolveTable_NilCacheUnchanged(t *testing.T) {
	if got := resolveTable("SELECT * FROM cnp_global.errors", []string{"errors"}, nil); got != "errors" {
		t.Errorf("got %q, want errors", got)
	}
}
