package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/dsl"
	"github.com/livecodelife/linespec/v3/pkg/types"
)

// prov-2026-a3105d31: the legacy table-keyed FindMock path never applied
// VERIFY_*, the Mongo interceptor only calls FindMock (so ACCESSING_TABLES and
// VERIFY_* were dead there), and NO_TRANSACTION was parsed then ignored.
// "Fails the spec" means VerifyAll reports an error after the call sequence the
// proxy performs; whether FindMock itself reports not-found is an
// implementation choice and is not asserted.

func TestLegacyFindMockEnforcesVerify_OperationMismatchFailsSpec(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Name: "table_form_wrong_operation",
		Expects: []types.ExpectStatement{
			{Channel: types.WriteMySQL, Table: "users", VerifyOperation: "UPDATE"},
		},
	})
	reg.FindMock("users", "INSERT INTO users (name) VALUES ('a')")
	if err := reg.VerifyAll(); err == nil {
		t.Fatal("bug: table-form EXPECT with VERIFY_OPERATION UPDATE was satisfied by an INSERT; FindMock never applies VERIFY_*")
	}
}

func TestLegacyFindMockEnforcesVerify_CorrectOperationStillPasses(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Name: "table_form_right_operation",
		Expects: []types.ExpectStatement{
			{Channel: types.WriteMySQL, Table: "users", VerifyOperation: "INSERT"},
		},
	})
	if _, ok := reg.FindMock("users", "INSERT INTO users (name) VALUES ('a')"); !ok {
		t.Fatal("a table-form EXPECT whose VERIFY_OPERATION holds must still match")
	}
	if err := reg.VerifyAll(); err != nil {
		t.Fatalf("correct VERIFY_OPERATION must keep passing: %v", err)
	}
}

func TestLegacyFindMockEnforcesVerify_WhereColumnsMismatchFailsSpec(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Name: "table_form_missing_where_column",
		Expects: []types.ExpectStatement{
			{Channel: types.ReadMySQL, Table: "users", VerifyWhereColumns: []string{"id"}},
		},
	})
	reg.FindMock("users", "SELECT * FROM users WHERE email = 'a@b.c'")
	if err := reg.VerifyAll(); err == nil {
		t.Fatal("bug: table-form EXPECT with VERIFY_WHERE_COLUMNS id was satisfied by a query filtering on email only")
	}
}

// The Mongo interceptor calls FindMock(collection, COMMAND, database).
func TestLegacyFindMockEnforcesVerify_MongoVerifyOperationCanFailSpec(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Name: "mongo_wrong_operation",
		Expects: []types.ExpectStatement{
			{Channel: types.WriteMongoDB, Table: "orders", VerifyOperation: "DELETE"},
		},
	})
	reg.FindMock("orders", "INSERT", "")
	if err := reg.VerifyAll(); err == nil {
		t.Fatal("bug: Mongo EXPECT with VERIFY_OPERATION DELETE was satisfied by an insert command")
	}
}

func TestLegacyFindMockEnforcesVerify_MongoAccessingTablesCanMatch(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Name: "mongo_accessing_tables",
		Expects: []types.ExpectStatement{
			{Channel: types.ReadMongoDB, Table: "orders", AccessingTables: []string{"orders"}},
		},
	})
	if _, ok := reg.FindMock("orders", "FIND", ""); !ok {
		t.Fatal("bug: a Mongo EXPECT declaring ACCESSING_TABLES can never match through FindMock (it is skipped), so the clause is dead")
	}
	if err := reg.VerifyAll(); err != nil {
		t.Fatalf("matched Mongo expectation must verify: %v", err)
	}
}

// NO TRANSACTION (the lexer keyword; the record calls it NO_TRANSACTION) is either a parse error or enforced; accepted-and-inert is
// the one outcome ruled out. If the parser accepts it, the registry must fail
// a spec whose statement ran inside a transaction (BEGIN seen before it).
func TestLegacyFindMockEnforcesVerify_NoTransactionNotInert(t *testing.T) {
	content := `TEST no-tx
RECEIVE HTTP:POST http://localhost:3000/users

EXPECT WRITE:MYSQL users
NO TRANSACTION
USING_SQL """
INSERT INTO users (name) VALUES ('a')
"""

RESPOND HTTP:201`
	path := filepath.Join(t.TempDir(), "no_tx.linespec")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	tokens, err := dsl.LexFile(path)
	if err != nil {
		t.Fatalf("LexFile: %v", err)
	}
	spec, err := dsl.NewParser(tokens).Parse(path)
	if err != nil {
		return // rejected at parse time as unsupported: acceptable
	}
	if len(spec.Expects) != 1 || !spec.Expects[0].NoTransaction {
		t.Fatalf("parser accepted NO_TRANSACTION but did not retain it: %+v", spec.Expects)
	}

	reg := NewMockRegistry()
	reg.Register(spec)
	// The wrapped-in-a-transaction call sequence a proxy would see.
	reg.FindMock("users", "BEGIN")
	reg.FindMock("users", "INSERT INTO users (name) VALUES ('a')")
	if err := reg.VerifyAll(); err == nil {
		t.Fatal("bug: NO_TRANSACTION is accepted by the parser yet a transactional write passes; the clause is inert")
	}
}
