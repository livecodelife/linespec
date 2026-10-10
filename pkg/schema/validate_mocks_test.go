package schema

import (
	"fmt"
	"strings"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/types"
)

// Specifies prov-2026-ad21b28e. Interface under test (does not exist yet),
// in package schema (validate_mocks.go):
//
//	type MockSpec struct {
//		File    string                  // spec file path, named in every error
//		Expects []types.ExpectStatement // parsed expects of that spec
//	}
//	type RowLoader func(e types.ExpectStatement) ([]map[string]interface{}, error)
//	func ValidateMocks(specs []MockSpec, schema map[string][]ColumnInfo, load RowLoader) []error
//
// Pure: no runner/proxy/config state. load returns the rows of e.ReturnsFile
// (each row a column->value map); nil load or a load error means "cannot
// check rows", never a validation error. RETURNS rows are checked against the
// table only when e.AccessingTables names exactly one table. Only
// READ_POSTGRESQL / WRITE_POSTGRESQL expects are examined.

func vmCols(names ...string) []ColumnInfo {
	out := make([]ColumnInfo, len(names))
	for i, n := range names {
		out[i] = ColumnInfo{Name: n, Type: "text"}
	}
	return out
}

func vmSchema() map[string][]ColumnInfo {
	return map[string][]ColumnInfo{
		"users":        vmCols("id", "email", "created_at"),
		"cnp.orders":   vmCols("id", "total"),
		"orders":       vmCols("id", "total"),
		"nocols":       nil,
		"Mixed_Case_T": vmCols("Id", "Full_Name"),
	}
}

func vmRows(rows ...map[string]interface{}) RowLoader {
	return func(types.ExpectStatement) ([]map[string]interface{}, error) { return rows, nil }
}

func vmSpec(file string, e ...types.ExpectStatement) []MockSpec {
	return []MockSpec{{File: file, Expects: e}}
}

func vmJoin(errs []error) string {
	var parts []string
	for _, e := range errs {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, "\n")
}

func vmWantOne(t *testing.T, errs []error, subs ...string) {
	t.Helper()
	if len(errs) != 1 {
		t.Fatalf("want exactly 1 error, got %d: %v", len(errs), errs)
	}
	for _, s := range subs {
		if !strings.Contains(errs[0].Error(), s) {
			t.Errorf("error %q missing %q", errs[0], s)
		}
	}
}

func vmNone(t *testing.T, errs []error) {
	t.Helper()
	if len(errs) != 0 {
		t.Fatalf("want no errors, got %v", errs)
	}
}

func TestValidateMocksUnknownColumnInReturnsRow(t *testing.T) {
	e := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"users"}, ReturnsFile: "p.yaml"}
	errs := ValidateMocks(vmSpec("specs/bad.linespec", e), vmSchema(),
		vmRows(map[string]interface{}{"id": 1, "nosuchcolumn": "x"}))
	vmWantOne(t, errs, "specs/bad.linespec", "users", "nosuchcolumn", "id", "email", "created_at")
}

func TestValidateMocksReturnsSubsetOfColumnsAccepted(t *testing.T) {
	e := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"users"}, ReturnsFile: "p.yaml"}
	errs := ValidateMocks(vmSpec("a.linespec", e), vmSchema(), vmRows(map[string]interface{}{"email": "a@b.c"}))
	vmNone(t, errs)
}

func TestValidateMocksReturnsReportsEveryBadRowKey(t *testing.T) {
	e := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"users"}, ReturnsFile: "p.yaml"}
	errs := ValidateMocks(vmSpec("a.linespec", e), vmSchema(), vmRows(
		map[string]interface{}{"id": 1, "typo_one": 1},
		map[string]interface{}{"id": 2, "typo_two": 2},
	))
	all := vmJoin(errs)
	if !strings.Contains(all, "typo_one") || !strings.Contains(all, "typo_two") {
		t.Fatalf("want both bad keys reported, got %v", errs)
	}
}

func TestValidateMocksUnknownTableInAccessingTables(t *testing.T) {
	e := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"users", "usrs"}}
	errs := ValidateMocks(vmSpec("t.linespec", e), vmSchema(), nil)
	vmWantOne(t, errs, "t.linespec", "usrs")
}

func TestValidateMocksUnknownTableErrorListsNothingForKnownTables(t *testing.T) {
	e := types.ExpectStatement{Channel: types.WritePostgreSQL, AccessingTables: []string{"users"}}
	vmNone(t, ValidateMocks(vmSpec("t.linespec", e), vmSchema(), nil))
}

func TestValidateMocksUnknownVerifyWrittenValuesColumn(t *testing.T) {
	e := types.ExpectStatement{Channel: types.WritePostgreSQL, AccessingTables: []string{"users"},
		VerifyWrittenValues: map[string]string{"email": "a", "emial": "b"}}
	errs := ValidateMocks(vmSpec("w.linespec", e), vmSchema(), nil)
	vmWantOne(t, errs, "w.linespec", "users", "emial", "id", "email", "created_at")
}

func TestValidateMocksUnknownVerifyWhereColumn(t *testing.T) {
	e := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"users"},
		VerifyWhere: map[string]string{"id": "1", "ghost": "2"}}
	errs := ValidateMocks(vmSpec("w.linespec", e), vmSchema(), nil)
	vmWantOne(t, errs, "w.linespec", "users", "ghost")
}

func TestValidateMocksUnknownVerifyWhereColumnsEntry(t *testing.T) {
	e := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"users"},
		VerifyWhereColumns: []string{"id", "ghost"}}
	errs := ValidateMocks(vmSpec("w.linespec", e), vmSchema(), nil)
	vmWantOne(t, errs, "w.linespec", "users", "ghost")
}

func TestValidateMocksValidExpectYieldsNoErrors(t *testing.T) {
	e := types.ExpectStatement{Channel: types.WritePostgreSQL, AccessingTables: []string{"users"},
		VerifyOperation: "INSERT", VerifyWrittenValues: map[string]string{"email": "a"},
		VerifyWhere: map[string]string{"id": "1"}, VerifyWhereColumns: []string{"id"}}
	vmNone(t, ValidateMocks(vmSpec("ok.linespec", e), vmSchema(), nil))
}

func TestValidateMocksSchemaQualifiedTable(t *testing.T) {
	// Qualified key present: resolved by the name as written.
	e := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"cnp.orders"},
		VerifyWhere: map[string]string{"nope": "1"}}
	errs := ValidateMocks(vmSpec("q.linespec", e), vmSchema(), nil)
	vmWantOne(t, errs, "q.linespec", "nope", "total")

	good := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"cnp.orders"},
		VerifyWhere: map[string]string{"total": "1"}}
	vmNone(t, ValidateMocks(vmSpec("q.linespec", good), vmSchema(), nil))
}

func TestValidateMocksSchemaQualifiedFallsBackToBareKey(t *testing.T) {
	// Only the bare key "users" exists; "public.users" must resolve via bare name.
	good := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"public.users"},
		VerifyWhere: map[string]string{"email": "x"}}
	vmNone(t, ValidateMocks(vmSpec("q.linespec", good), vmSchema(), nil))

	bad := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"public.users"},
		VerifyWhere: map[string]string{"nope": "x"}}
	vmWantOne(t, ValidateMocks(vmSpec("q.linespec", bad), vmSchema(), nil), "nope", "email")
}

func TestValidateMocksUnresolvableQualifiedTableIsSkippedNotError(t *testing.T) {
	// Neither "other.ghosts" nor bare "ghosts" in schema: cannot resolve. The
	// table itself may be an error (unknown table) only for unqualified names;
	// for a qualified name in a schema we did not discover it must be skipped.
	e := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"other.ghosts"},
		VerifyWhere: map[string]string{"anything": "1"}, ReturnsFile: "p.yaml"}
	errs := ValidateMocks(vmSpec("q.linespec", e), vmSchema(), vmRows(map[string]interface{}{"zzz": 1}))
	vmNone(t, errs)
}

func TestValidateMocksCaseInsensitiveTableAndColumn(t *testing.T) {
	e := types.ExpectStatement{Channel: types.WritePostgreSQL, AccessingTables: []string{"USERS"},
		VerifyWrittenValues: map[string]string{"EMAIL": "a"},
		VerifyWhere:         map[string]string{"Id": "1"},
		VerifyWhereColumns:  []string{"CREATED_AT"}, ReturnsFile: "p.yaml"}
	vmNone(t, ValidateMocks(vmSpec("c.linespec", e), vmSchema(), vmRows(map[string]interface{}{"ID": 1, "Email": "x"})))

	// Schema side is mixed-case; spec side lower-case.
	m := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"mixed_case_t"},
		VerifyWhere: map[string]string{"id": "1", "full_name": "x"}}
	vmNone(t, ValidateMocks(vmSpec("c.linespec", m), vmSchema(), nil))

	// Case-insensitivity must not hide a genuine typo.
	bad := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"USERS"},
		VerifyWhere: map[string]string{"EMIAL": "1"}}
	vmWantOne(t, ValidateMocks(vmSpec("c.linespec", bad), vmSchema(), nil), "EMIAL")
}

func TestValidateMocksSchemaLessTableSkipped(t *testing.T) {
	// "nocols" is in the map with zero columns: nothing to compare against.
	e := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"nocols"},
		VerifyWhere: map[string]string{"x": "1"}, VerifyWhereColumns: []string{"y"},
		VerifyWrittenValues: map[string]string{"z": "1"}, ReturnsFile: "p.yaml"}
	vmNone(t, ValidateMocks(vmSpec("s.linespec", e), vmSchema(), vmRows(map[string]interface{}{"w": 1})))
}

func TestValidateMocksNoSingleTableSkipsColumnChecks(t *testing.T) {
	// A join (two tables) or no ACCESSING_TABLES: column ownership is
	// ambiguous, so column checks are skipped rather than guessed.
	join := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"users", "orders"},
		VerifyWhere: map[string]string{"anything": "1"}, ReturnsFile: "p.yaml"}
	vmNone(t, ValidateMocks(vmSpec("j.linespec", join), vmSchema(), vmRows(map[string]interface{}{"zzz": 1})))

	none := types.ExpectStatement{Channel: types.ReadPostgreSQL, VerifyWhere: map[string]string{"anything": "1"}}
	vmNone(t, ValidateMocks(vmSpec("j.linespec", none), vmSchema(), nil))
}

func TestValidateMocksIgnoresNonPostgresChannels(t *testing.T) {
	bad := func(ch types.ExpectChannel) types.ExpectStatement {
		return types.ExpectStatement{Channel: ch, AccessingTables: []string{"nosuchtable"},
			VerifyWhere: map[string]string{"nosuchcolumn": "1"}, VerifyWrittenValues: map[string]string{"nosuch": "1"},
			VerifyWhereColumns: []string{"nosuch"}, ReturnsFile: "p.yaml"}
	}
	specs := vmSpec("m.linespec", bad(types.ReadMySQL), bad(types.WriteMySQL), bad(types.HTTP), bad(types.ReadOracle))
	vmNone(t, ValidateMocks(specs, vmSchema(), vmRows(map[string]interface{}{"nosuch": 1})))
}

func TestValidateMocksEmptyOrNilSchemaYieldsNoErrors(t *testing.T) {
	e := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"whatever"},
		VerifyWhere: map[string]string{"x": "1"}, ReturnsFile: "p.yaml"}
	specs := vmSpec("n.linespec", e)
	vmNone(t, ValidateMocks(specs, nil, vmRows(map[string]interface{}{"q": 1})))
	vmNone(t, ValidateMocks(specs, map[string][]ColumnInfo{}, vmRows(map[string]interface{}{"q": 1})))
	vmNone(t, ValidateMocks(nil, vmSchema(), nil))
}

func TestValidateMocksLoaderFailureOrNilLoaderIsNotAnError(t *testing.T) {
	e := types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"users"}, ReturnsFile: "missing.yaml"}
	failing := func(types.ExpectStatement) ([]map[string]interface{}, error) { return nil, fmt.Errorf("boom") }
	vmNone(t, ValidateMocks(vmSpec("l.linespec", e), vmSchema(), failing))
	vmNone(t, ValidateMocks(vmSpec("l.linespec", e), vmSchema(), nil))
}

func TestValidateMocksErrorsNameTheirOwnSpecFile(t *testing.T) {
	bad := func(col string) types.ExpectStatement {
		return types.ExpectStatement{Channel: types.ReadPostgreSQL, AccessingTables: []string{"users"},
			VerifyWhere: map[string]string{col: "1"}}
	}
	specs := []MockSpec{
		{File: "one.linespec", Expects: []types.ExpectStatement{bad("colone")}},
		{File: "two.linespec", Expects: []types.ExpectStatement{bad("coltwo")}},
	}
	errs := ValidateMocks(specs, vmSchema(), nil)
	if len(errs) != 2 {
		t.Fatalf("want 2 errors (one per spec), got %v", errs)
	}
	for _, e := range errs {
		s := e.Error()
		okOne := strings.Contains(s, "one.linespec") && strings.Contains(s, "colone") && !strings.Contains(s, "two.linespec")
		okTwo := strings.Contains(s, "two.linespec") && strings.Contains(s, "coltwo") && !strings.Contains(s, "one.linespec")
		if !okOne && !okTwo {
			t.Errorf("error attributes the wrong spec/column: %q", s)
		}
	}
}
