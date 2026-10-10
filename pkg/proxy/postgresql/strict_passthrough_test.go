package postgresql

// Tests for prov-2026-545e4690: the PostgreSQL proxy records a passthrough
// (registry.RecordPassthrough) when a statement is resolved against the mocks
// and none matches, so strict_passthrough is enforced.
//
// Recording points, and only these:
//   - simple query (MsgQuery) when findMock(query, nil) misses
//   - extended query at MsgBind, after bind params are decoded, when
//     findMock(query, bindParams) misses AND the Bind is forwarded upstream
//
// Never at Parse. A Bind miss on a locally-parsed (mock-only) statement is
// answered with an ErrorResponse and is NOT a passthrough.
//
// Interface these tests define (does not exist yet):
//
//	func isHousekeepingQuery(query string) bool
//	    pure. True for SET, SHOW, BEGIN, START TRANSACTION, COMMIT, ROLLBACK,
//	    SAVEPOINT, RELEASE, DISCARD, DEALLOCATE, RESET (leading keyword,
//	    case-insensitive, after trimming whitespace/NUL), for SELECT 1 and
//	    SELECT version(), and for any query containing (substring) pg_catalog,
//	    pg_type, pg_namespace, pg_class, pg_attribute, pg_settings or
//	    information_schema. DDL (CREATE/ALTER/DROP) is NOT housekeeping.
//
// Everything else is driven end to end through the proxy loop with byte
// transcripts and asserted on via p.registry.GetPassthroughs(), the
// registry's exported getter. Helpers from parse_ordering_test.go (pgoProxy,
// pgoRun, pgoParse, pgoBindInt8, ...) are reused.

import (
	"strings"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/registry"
	"github.com/livecodelife/linespec/v3/pkg/types"
)

const (
	sptUndeclaredSQL     = "SELECT count(*) FROM sponsors"
	sptUndeclaredBindSQL = "SELECT count(*) FROM sponsors WHERE sponsorid = $1"
)

// sptDonorMock declares a mock for an unrelated table so the registry is
// non-empty but nothing matches the sponsors queries below.
func sptDonorMock() types.ExpectStatement {
	return types.ExpectStatement{
		Channel:         types.WritePostgreSQL,
		AccessingTables: []string{"donors"},
		VerifyOperation: "INSERT",
	}
}

// sptSimple builds a simple-protocol Query frame (NUL-terminated like drivers send).
func sptSimple(query string) []byte {
	return pgoMsg('Q', append([]byte(query), 0))
}

// sptBindNoParams builds a Bind for the unnamed portal/statement with zero params.
func sptBindNoParams() []byte {
	return pgoMsg('B', bpBindFrame(nil, nil))
}

// sptDescribeStmt builds Describe for the unnamed prepared statement.
func sptDescribeStmt() []byte { return pgoMsg('D', []byte{'S', 0}) }

// sptExtended runs Parse(query)/Bind/Execute/Sync, with one int8 param when the
// query has a $1 placeholder and no params otherwise.
func sptExtended(t *testing.T, p *Proxy, query string) {
	t.Helper()
	if strings.Contains(query, "$1") {
		pgoRun(t, p, pgoCat(pgoParse(query, pgoOIDInt8), pgoBindInt8(7), pgoExecute(), pgoSync()))
		return
	}
	pgoRun(t, p, pgoCat(pgoParse(query), sptBindNoParams(), pgoExecute(), pgoSync()))
}

func sptRecorded(p *Proxy) []string { return p.registry.GetPassthroughs() }

func sptWantNone(t *testing.T, p *Proxy, what string) {
	t.Helper()
	if got := sptRecorded(p); len(got) != 0 {
		t.Errorf("%s: recorded passthroughs %q, want none", what, got)
	}
}

func sptWantOne(t *testing.T, p *Proxy, what, contains string) {
	t.Helper()
	got := sptRecorded(p)
	if len(got) != 1 {
		t.Fatalf("%s: recorded %d passthroughs %q, want exactly 1", what, len(got), got)
	}
	if !strings.Contains(got[0], contains) {
		t.Errorf("%s: passthrough description %q does not contain %q", what, got[0], contains)
	}
}

// --- recorded: undeclared statements -------------------------------------

func TestStrictPassthrough_SimpleUndeclaredSelectIsRecorded(t *testing.T) {
	p := pgoProxy(t, sptDonorMock())
	_, upstream := pgoRun(t, p, sptSimple(sptUndeclaredSQL))
	if got := pgoTypes(upstream); got != "Q" {
		t.Fatalf("precondition: upstream frames = %q, want the query forwarded", got)
	}
	sptWantOne(t, p, "simple undeclared SELECT", sptUndeclaredSQL)
}

func TestStrictPassthrough_ExtendedUndeclaredSelectIsRecordedAtBind(t *testing.T) {
	p := pgoProxy(t, sptDonorMock())
	_, upstream := pgoRun(t, p, pgoCat(pgoParse(sptUndeclaredBindSQL, pgoOIDInt8), pgoBindInt8(7), pgoExecute(), pgoSync()))
	if got := pgoTypes(upstream); !strings.HasPrefix(got, "PB") {
		t.Fatalf("precondition: upstream frames = %q, want Parse and Bind forwarded", got)
	}
	sptWantOne(t, p, "extended undeclared SELECT", "SELECT count(*) FROM sponsors")
}

func TestStrictPassthrough_ExtendedUndeclaredSelectNoParams(t *testing.T) {
	p := pgoProxy(t, sptDonorMock())
	sptExtended(t, p, sptUndeclaredSQL)
	sptWantOne(t, p, "extended undeclared SELECT, no params", sptUndeclaredSQL)
}

// DECIDED: DDL is not exempt.
func TestStrictPassthrough_DDLIsRecorded(t *testing.T) {
	for _, q := range []string{
		"CREATE TABLE sponsors (id bigint)",
		"ALTER TABLE sponsors ADD COLUMN name text",
		"DROP TABLE sponsors",
		"create index idx_sponsors on sponsors (id)",
	} {
		t.Run("simple/"+q, func(t *testing.T) {
			p := pgoProxy(t, sptDonorMock())
			pgoRun(t, p, sptSimple(q))
			sptWantOne(t, p, q, q)
		})
		t.Run("extended/"+q, func(t *testing.T) {
			p := pgoProxy(t, sptDonorMock())
			sptExtended(t, p, q)
			sptWantOne(t, p, q, q)
		})
	}
}

// --- not recorded: declared / mock-resolved ------------------------------

func TestStrictPassthrough_DeclaredSimpleQueryNotRecorded(t *testing.T) {
	p := pgoProxy(t, types.ExpectStatement{
		Channel:         types.ReadPostgreSQL,
		AccessingTables: []string{"sponsors"},
		VerifyOperation: "SELECT",
		ReturnsEmpty:    true,
	})
	if _, ok := p.peekMock(sptUndeclaredSQL, nil); !ok {
		t.Fatal("precondition: mock matches the simple query")
	}
	pgoRun(t, p, sptSimple(sptUndeclaredSQL))
	sptWantNone(t, p, "declared simple query")
}

func TestStrictPassthrough_DeclaredExtendedQueryNotRecorded(t *testing.T) {
	p := pgoProxy(t, types.ExpectStatement{
		Channel:         types.WritePostgreSQL,
		AccessingTables: []string{"sponsors"},
		VerifyOperation: "INSERT",
	})
	_, upstream := pgoRun(t, p, pgoCat(
		pgoParse(pgoInsertSQL, pgoOIDInt8), pgoBindInt8(7), pgoExecute(), pgoSync()))
	for _, f := range upstream {
		if f.typ == 'P' || f.typ == 'B' {
			t.Fatalf("precondition: mocked statement reached upstream as %q", string(f.typ))
		}
	}
	sptWantNone(t, p, "declared extended query")
}

// A value-level VERIFY mock is not matched at Parse-with-nil-params; the Parse
// is answered locally and the Bind resolves against the mock. Not undeclared.
func TestStrictPassthrough_ValueLevelVerifyResolvedAtBindNotRecorded(t *testing.T) {
	p := pgoProxy(t, pgoWriteMock(map[string]string{"sponsorid": "7"}))
	client, upstream := pgoRun(t, p, pgoCat(
		pgoParse(pgoInsertSQL, pgoOIDInt8), pgoBindInt8(7), pgoDescribePortal(), pgoExecute(), pgoSync()))
	if strings.Contains(pgoTypes(client), "E") {
		t.Fatalf("precondition: client saw an ErrorResponse %q", pgoTypes(client))
	}
	for _, f := range upstream {
		if f.typ == 'P' || f.typ == 'B' {
			t.Fatalf("precondition: upstream saw %q", string(f.typ))
		}
	}
	sptWantNone(t, p, "value-level VERIFY mock resolved at Bind")
}

func TestStrictPassthrough_ValueLevelVerifyWhereResolvedAtBindNotRecorded(t *testing.T) {
	p := pgoProxy(t, pgoReadMock(map[string]string{"sponsorid": "7"}, nil))
	pgoRun(t, p, pgoCat(pgoParse(pgoSelectSQL, pgoOIDInt8), pgoBindInt8(7), pgoExecute(), pgoSync()))
	sptWantNone(t, p, "VERIFY_WHERE mock resolved at Bind")
}

// Sibling prov-2026-becc5d88: a Bind miss on a mock-only statement is an
// ErrorResponse, not forwarded, therefore not a passthrough.
func TestStrictPassthrough_MockOnlyBindMissIsNotRecorded(t *testing.T) {
	p := pgoProxy(t, pgoWriteMock(map[string]string{"sponsorid": "7"}))
	client, upstream := pgoRun(t, p, pgoCat(
		pgoParse(pgoInsertSQL, pgoOIDInt8), pgoBindInt8(99), pgoSync()))
	if len(client) < 2 || client[1].typ != 'E' {
		t.Fatalf("precondition: client frames = %q, want 1,E", pgoTypes(client))
	}
	for _, f := range upstream {
		if f.typ == 'P' || f.typ == 'B' {
			t.Fatalf("precondition: upstream saw %q", string(f.typ))
		}
	}
	sptWantNone(t, p, "mock-only Bind miss (ErrorResponse)")
}

func TestStrictPassthrough_MockOnlyWhereBindMissIsNotRecorded(t *testing.T) {
	p := pgoProxy(t, pgoReadMock(map[string]string{"sponsorid": "7"}, nil))
	pgoRun(t, p, pgoCat(pgoParse(pgoSelectSQL, pgoOIDInt8), pgoBindInt8(99), pgoSync()))
	sptWantNone(t, p, "mock-only VERIFY_WHERE Bind miss")
}

// --- recording points: never at Parse, at most once per Bind -------------

func TestStrictPassthrough_ParseAndDescribeWithoutBindRecordNothing(t *testing.T) {
	p := pgoProxy(t, sptDonorMock())
	_, upstream := pgoRun(t, p, pgoCat(
		pgoParse(sptUndeclaredBindSQL, pgoOIDInt8), sptDescribeStmt(), pgoSync()))
	if !strings.HasPrefix(pgoTypes(upstream), "P") {
		t.Fatalf("precondition: Parse should be forwarded, upstream = %q", pgoTypes(upstream))
	}
	sptWantNone(t, p, "Parse/Describe with no Bind")
}

func TestStrictPassthrough_ParseOnlyForwardedForVerifyShapeRecordsNothing(t *testing.T) {
	// No explicit OIDs: tokio-postgres style, Parse is forwarded even though a
	// value-level mock exists. Still not recorded at Parse.
	p := pgoProxy(t, pgoWriteMock(map[string]string{"sponsorid": "7"}))
	_, upstream := pgoRun(t, p, pgoParse(pgoInsertSQL))
	if got := pgoTypes(upstream); got != "P" {
		t.Fatalf("precondition: upstream = %q, want Parse forwarded", got)
	}
	sptWantNone(t, p, "forwarded Parse alone")
}

func TestStrictPassthrough_AtMostOncePerBind(t *testing.T) {
	p := pgoProxy(t, sptDonorMock())
	pgoRun(t, p, pgoCat(
		pgoParse(sptUndeclaredBindSQL, pgoOIDInt8),
		sptDescribeStmt(),
		pgoBindInt8(7),
		pgoDescribePortal(),
		pgoExecute(),
		pgoSync(),
	))
	sptWantOne(t, p, "one Parse+Bind+Describe+Execute", "SELECT count(*) FROM sponsors")
}

func TestStrictPassthrough_OneRecordPerBind(t *testing.T) {
	p := pgoProxy(t, sptDonorMock())
	pgoRun(t, p, pgoCat(
		pgoParse(sptUndeclaredBindSQL, pgoOIDInt8), pgoBindInt8(1), pgoExecute(), pgoSync(),
		pgoParse(sptUndeclaredBindSQL, pgoOIDInt8), pgoBindInt8(2), pgoExecute(), pgoSync(),
	))
	if got := sptRecorded(p); len(got) != 2 {
		t.Errorf("two Binds recorded %d passthroughs %q, want 2", len(got), got)
	}
}

func TestStrictPassthrough_BindOnUnknownStatementNotRecorded(t *testing.T) {
	p := pgoProxy(t, sptDonorMock())
	_, upstream := pgoRun(t, p, pgoCat(pgoBindInt8(7), pgoExecute(), pgoSync())) // no Parse
	if !strings.HasPrefix(pgoTypes(upstream), "B") {
		t.Fatalf("precondition: Bind should be forwarded, upstream = %q", pgoTypes(upstream))
	}
	sptWantNone(t, p, "Bind on unknown statement")
}

// --- housekeeping exemptions ---------------------------------------------

var sptHousekeeping = []string{
	"SET application_name = 'x'",
	"SET search_path TO public",
	"SHOW server_version",
	"BEGIN",
	"START TRANSACTION",
	"COMMIT",
	"ROLLBACK",
	"SAVEPOINT s1",
	"RELEASE SAVEPOINT s1",
	"DISCARD ALL",
	"DEALLOCATE ALL",
	"RESET ALL",
	"SELECT 1",
	"SELECT version()",
	"SELECT * FROM pg_catalog.pg_tables",
	"SELECT oid, typname FROM pg_type WHERE oid = 25",
	"SELECT nspname FROM pg_namespace",
	"SELECT relname FROM pg_class",
	"SELECT attname FROM pg_attribute",
	"SELECT name, setting FROM pg_settings",
	"SELECT table_name FROM information_schema.tables",
}

func TestStrictPassthrough_HousekeepingPredicate(t *testing.T) {
	for _, q := range sptHousekeeping {
		for _, v := range []string{q, strings.ToLower(q), "  " + q + " ;", q + "\x00"} {
			if !isHousekeepingQuery(v) {
				t.Errorf("isHousekeepingQuery(%q) = false, want true", v)
			}
		}
	}
	for _, q := range []string{
		sptUndeclaredSQL,
		sptUndeclaredBindSQL,
		pgoInsertSQL,
		pgoSelectSQL,
		"CREATE TABLE sponsors (id bigint)",
		"ALTER TABLE sponsors ADD COLUMN name text",
		"DROP TABLE sponsors",
		"SELECT 2",
		"UPDATE sponsors SET name = 'x'", // starts with UPDATE, not SET
		"SELECT * FROM settings_log",     // not pg_settings
	} {
		if isHousekeepingQuery(q) {
			t.Errorf("isHousekeepingQuery(%q) = true, want false", q)
		}
	}
}

func TestStrictPassthrough_HousekeepingSimpleNotRecorded(t *testing.T) {
	for _, q := range sptHousekeeping {
		t.Run(q, func(t *testing.T) {
			p := pgoProxy(t, sptDonorMock())
			_, upstream := pgoRun(t, p, sptSimple(q))
			if got := pgoTypes(upstream); got != "Q" {
				t.Fatalf("precondition: upstream = %q, want the query forwarded", got)
			}
			sptWantNone(t, p, q)
		})
	}
}

func TestStrictPassthrough_HousekeepingExtendedNotRecorded(t *testing.T) {
	for _, q := range sptHousekeeping {
		t.Run(q, func(t *testing.T) {
			p := pgoProxy(t, sptDonorMock())
			_, upstream := pgoRun(t, p, pgoCat(pgoParse(q), sptBindNoParams(), pgoExecute(), pgoSync()))
			if !strings.HasPrefix(pgoTypes(upstream), "PB") {
				t.Fatalf("precondition: upstream = %q, want Parse and Bind forwarded", pgoTypes(upstream))
			}
			sptWantNone(t, p, q)
		})
	}
}

// --- strict_passthrough off: recording is unconditional, only the verdict differs

func TestStrictPassthrough_RecordingIsUnconditionalAndOnlyStrictFails(t *testing.T) {
	p := pgoProxy(t, sptDonorMock())
	pgoRun(t, p, sptSimple(sptUndeclaredSQL))
	sptWantOne(t, p, "recorded regardless of strict flag", sptUndeclaredSQL)

	if err := p.registry.VerifyPassthroughs(false); err != nil {
		t.Errorf("VerifyPassthroughs(false) = %v, want nil (warning only)", err)
	}
	if err := p.registry.VerifyPassthroughs(true); err == nil {
		t.Error("VerifyPassthroughs(true) = nil, want an error for the recorded passthrough")
	}
}

func TestStrictPassthrough_NoPassthroughsStrictPasses(t *testing.T) {
	reg := registry.NewMockRegistry()
	if err := reg.VerifyPassthroughs(true); err != nil {
		t.Fatalf("precondition: empty registry strict verify = %v", err)
	}
	p := pgoProxy(t, sptDonorMock())
	pgoRun(t, p, sptSimple("SET application_name = 'x'"))
	if err := p.registry.VerifyPassthroughs(true); err != nil {
		t.Errorf("housekeeping-only traffic failed strict verify: %v", err)
	}
}
