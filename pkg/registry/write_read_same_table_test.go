package registry

import (
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/types"
)

// TestIssue216_SameTableWriteAndRead_BothDirectionsVerify reproduces GitHub
// issue #216: a spec that declares both an EXPECT WRITE:POSTGRESQL and an
// EXPECT READ:POSTGRESQL for the same table (e.g. an INSERT immediately
// followed by a SELECT against that table in the same turn) must be able to
// verify both directions independently.
//
// Root cause: FindMockByTables only evaluates mocks that declare
// ACCESSING_TABLES; when just one of the two same-table EXPECT lines declares
// it, an actual query of the *other* direction gets evaluated only against
// the semantically-eligible mock, is rejected for a direction mismatch, and
// that rejection is unconditionally appended to the registry's global
// verifyErrors — even though the legacy fallback path (FindMock) then goes on
// to correctly match the actual EXPECT line for that direction. The
// pre-recorded rejection still poisons VerifyAll.
//
// Skipped as a placeholder pending the fix under prov-2026-00cb9b88.
func TestIssue216_SameTableWriteAndRead_BothDirectionsVerify(t *testing.T) {
	t.Skip("TODO(prov-2026-00cb9b88): WRITE and READ EXPECT lines for the same table must verify independently")

	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Name: "insert_then_select_same_table",
		Expects: []types.ExpectStatement{
			{
				Channel:         types.WritePostgreSQL,
				Table:           "chat_usage",
				AccessingTables: []string{"chat_usage"},
				VerifyOperation: "INSERT",
				CallN:           1,
			},
			{
				Channel:         types.ReadPostgreSQL,
				Table:           "chat_usage",
				AccessingTables: []string{"chat_usage"},
				VerifyOperation: "SELECT",
				CallN:           2,
			},
		},
	})

	insertQuery := "insert into chat_usage (conversation_id, total_tokens) values ($1, $2)"
	if _, ok := reg.FindMockByTables("", []string{"chat_usage"}, "INSERT", nil, nil, map[string]string{
		"conversation_id": "c1",
		"total_tokens":    "500",
	}); !ok {
		t.Fatalf("expected WRITE mock to match INSERT query %q", insertQuery)
	}

	selectQuery := "select sum(total_tokens) as total from chat_usage where conversation_id = $1"
	if _, ok := reg.FindMockByTables("", []string{"chat_usage"}, "SELECT", []string{"conversation_id"}, map[string]string{
		"conversation_id": "c1",
	}, nil); !ok {
		t.Fatalf("expected READ mock to match SELECT query %q", selectQuery)
	}

	if err := reg.VerifyAll(); err != nil {
		t.Errorf("VerifyAll() = %v, want no error (both WRITE and READ EXPECT lines were satisfied by their matching queries)", err)
	}
}
