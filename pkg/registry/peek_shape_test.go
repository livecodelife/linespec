package registry

import (
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/types"
)

// Tests for prov-2026-becc5d88: PeekMockByTablesShape is a read-only variant of
// PeekMockByTables that skips the value-level blocks (VerifyWhere,
// VerifyWrittenValues) and consumes no hit. Assumed signature:
//
//	func (r *MockRegistry) PeekMockByTablesShape(database string, tables []string,
//	    operation string, whereColumns []string) (*types.ExpectStatement, bool)

func shapeReg(expects ...types.ExpectStatement) *MockRegistry {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{Name: "peek-shape", Expects: expects})
	return reg
}

func shapeWriteMock() types.ExpectStatement {
	return types.ExpectStatement{
		Channel:             types.WritePostgreSQL,
		AccessingTables:     []string{"sponsors"},
		VerifyOperation:     "INSERT",
		VerifyWrittenValues: map[string]string{"sponsorid": "7"},
	}
}

func TestPeekMockByTablesShapeValueLevelOnlyMockHits(t *testing.T) {
	reg := shapeReg(shapeWriteMock())
	if _, ok := reg.PeekMockByTables("", []string{"sponsors"}, "INSERT", nil, nil, nil); ok {
		t.Fatal("precondition: PeekMockByTables must still miss a VERIFY_WRITTEN_VALUES mock when no written values are supplied")
	}
	if _, ok := reg.PeekMockByTablesShape("", []string{"sponsors"}, "INSERT", nil); !ok {
		t.Error("shape peek should hit: only value-level predicates are unmet")
	}
}

func TestPeekMockByTablesShapeSkipsWhereAndPresentValues(t *testing.T) {
	reg := shapeReg(
		types.ExpectStatement{
			Channel:         types.ReadPostgreSQL,
			AccessingTables: []string{"sponsors"},
			VerifyOperation: "SELECT",
			VerifyWhere:     map[string]string{"sponsorid": "7", "id": "PRESENT"},
		},
		types.ExpectStatement{
			Channel:             types.WritePostgreSQL,
			AccessingTables:     []string{"donors"},
			VerifyOperation:     "INSERT",
			VerifyWrittenValues: map[string]string{"donorid": "PRESENT"},
		},
	)
	if _, ok := reg.PeekMockByTablesShape("", []string{"sponsors"}, "SELECT", nil); !ok {
		t.Error("shape peek should skip VERIFY_WHERE (literal and PRESENT)")
	}
	if _, ok := reg.PeekMockByTablesShape("", []string{"donors"}, "INSERT", nil); !ok {
		t.Error("shape peek should skip VERIFY_WRITTEN_VALUES PRESENT")
	}
}

func TestPeekMockByTablesShapeConsumesNoHit(t *testing.T) {
	reg := shapeReg(shapeWriteMock())
	for i := 0; i < 3; i++ {
		if _, ok := reg.PeekMockByTablesShape("", []string{"sponsors"}, "INSERT", nil); !ok {
			t.Fatalf("peek %d should still match (no hit consumed)", i)
		}
	}
	if hits := reg.GetHits(); len(hits) != 0 {
		for k, v := range hits {
			if v > 0 {
				t.Errorf("hit recorded for %q = %d, want none", k, v)
			}
		}
	}
}

func TestPeekMockByTablesShapeWrongTableMisses(t *testing.T) {
	reg := shapeReg(shapeWriteMock())
	if _, ok := reg.PeekMockByTablesShape("", []string{"donors"}, "INSERT", nil); ok {
		t.Error("different table set must miss")
	}
}

func TestPeekMockByTablesShapeWrongOperationMisses(t *testing.T) {
	reg := shapeReg(shapeWriteMock())
	if _, ok := reg.PeekMockByTablesShape("", []string{"sponsors"}, "UPDATE", nil); ok {
		t.Error("VERIFY_OPERATION INSERT must not match UPDATE")
	}
}

func TestPeekMockByTablesShapeWrongDirectionMisses(t *testing.T) {
	reg := shapeReg(types.ExpectStatement{
		Channel:         types.ReadPostgreSQL,
		AccessingTables: []string{"sponsors"},
		VerifyWhere:     map[string]string{"sponsorid": "7"},
	})
	if _, ok := reg.PeekMockByTablesShape("", []string{"sponsors"}, "INSERT", nil); ok {
		t.Error("READ mock must not match a write operation")
	}
	if _, ok := reg.PeekMockByTablesShape("", []string{"sponsors"}, "SELECT", nil); !ok {
		t.Error("READ mock should match a SELECT (control)")
	}
}

func TestPeekMockByTablesShapeMissingWhereColumnMisses(t *testing.T) {
	reg := shapeReg(types.ExpectStatement{
		Channel:            types.ReadPostgreSQL,
		AccessingTables:    []string{"sponsors"},
		VerifyOperation:    "SELECT",
		VerifyWhereColumns: []string{"sponsorid"},
		VerifyWhere:        map[string]string{"sponsorid": "7"},
	})
	if _, ok := reg.PeekMockByTablesShape("", []string{"sponsors"}, "SELECT", []string{"id"}); ok {
		t.Error("VERIFY_WHERE_COLUMNS sponsorid absent from where columns must miss")
	}
	if _, ok := reg.PeekMockByTablesShape("", []string{"sponsors"}, "SELECT", []string{"SponsorID"}); !ok {
		t.Error("where column present (case-insensitive) should hit")
	}
}

func TestPeekMockByTablesShapeWrongDatabaseMisses(t *testing.T) {
	m := shapeWriteMock()
	m.Database = "db1"
	reg := shapeReg(m)
	if _, ok := reg.PeekMockByTablesShape("db2", []string{"sponsors"}, "INSERT", nil); ok {
		t.Error("mock scoped to db1 must not match a db2 proxy")
	}
	if _, ok := reg.PeekMockByTablesShape("db1", []string{"sponsors"}, "INSERT", nil); !ok {
		t.Error("mock scoped to db1 should match a db1 proxy (control)")
	}
}

func TestPeekMockByTablesShapeAlreadyHitMockMisses(t *testing.T) {
	m := shapeWriteMock()
	m.VerifyWrittenValues = nil
	reg := shapeReg(m)
	if _, ok := reg.FindMockByTables("", []string{"sponsors"}, "INSERT", nil, nil, nil); !ok {
		t.Fatal("precondition: FindMockByTables should consume the mock")
	}
	if _, ok := reg.PeekMockByTablesShape("", []string{"sponsors"}, "INSERT", nil); ok {
		t.Error("a mock that has already been hit must be skipped")
	}
}

func TestPeekMockByTablesShapeNoMocksMisses(t *testing.T) {
	reg := NewMockRegistry()
	if _, ok := reg.PeekMockByTablesShape("", []string{"sponsors"}, "INSERT", nil); ok {
		t.Error("no mocks registered: must miss")
	}
}
