package registry

import (
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/types"
)

// TestMultiDatabaseCallScoping is the regression coverage for prov-2026-4ea65be4
// (GitHub issue #185).
//
// When two proxied databases (e.g. two PostgreSQL databases declared in the
// `databases:` list form) expose a table with the same name, EXPECT
// statements for that table — and their CALL N ordering — must resolve to
// the specific database/store the spec author intended via the DATABASE
// clause (types.ExpectStatement.Database). Before the fix,
// MockRegistry.getExpectKey/tableSetKey indexed mocks purely by table name
// with no database dimension, so a query against one database could consume
// (or be matched against) an EXPECT written for the other database's stream
// of the same table name.
func TestMultiDatabaseCallScoping(t *testing.T) {
	reg := NewMockRegistry()
	spec := &types.TestSpec{
		Name: "multi_database_call_scoping",
		Expects: []types.ExpectStatement{
			{
				Channel:         types.WritePostgreSQL,
				Database:        "district",
				AccessingTables: []string{"orders"},
				VerifyOperation: "UPDATE",
				CallN:           1,
				WithFile:        "payloads/district_order_1.json",
			},
			{
				Channel:         types.WritePostgreSQL,
				Database:        "master",
				AccessingTables: []string{"orders"},
				VerifyOperation: "UPDATE",
				CallN:           1,
				WithFile:        "payloads/master_order_1.json",
			},
		},
	}
	reg.Register(spec)

	// A query against the "district" proxy must only ever bind to the EXPECT
	// declared DATABASE district, never the one declared DATABASE master, even
	// though both target the same table name with the same CallN.
	districtMock, ok := reg.FindMockByTables("district", []string{"orders"}, "UPDATE", nil, nil, nil)
	if !ok {
		t.Fatal("Expected a mock for the district database")
	}
	if districtMock.Database != "district" {
		t.Errorf("district proxy matched an EXPECT for database %q, want %q", districtMock.Database, "district")
	}
	if districtMock.WithFile != "payloads/district_order_1.json" {
		t.Errorf("district proxy matched the wrong EXPECT (WithFile=%q)", districtMock.WithFile)
	}

	// The "master" proxy must independently match its own EXPECT for the same
	// table/CallN, unaffected by the district proxy's match above.
	masterMock, ok := reg.FindMockByTables("master", []string{"orders"}, "UPDATE", nil, nil, nil)
	if !ok {
		t.Fatal("Expected a mock for the master database")
	}
	if masterMock.Database != "master" {
		t.Errorf("master proxy matched an EXPECT for database %q, want %q", masterMock.Database, "master")
	}
	if masterMock.WithFile != "payloads/master_order_1.json" {
		t.Errorf("master proxy matched the wrong EXPECT (WithFile=%q)", masterMock.WithFile)
	}

	// Both EXPECTs are now consumed (0 hits remaining); a third query against
	// either database must find nothing left to bind to.
	if _, ok := reg.FindMockByTables("district", []string{"orders"}, "UPDATE", nil, nil, nil); ok {
		t.Error("Expected no remaining mock for district after its single EXPECT was consumed")
	}
	if _, ok := reg.FindMockByTables("master", []string{"orders"}, "UPDATE", nil, nil, nil); ok {
		t.Error("Expected no remaining mock for master after its single EXPECT was consumed")
	}
}

// TestMultiDatabaseCallScoping_HitKeysDoNotCollide verifies that two
// database-scoped EXPECTs that are otherwise identical (same channel, table
// set, VERIFY_OPERATION, and CALL N) produce distinct mockHitKey values, so
// that hit counts reported by two independent proxy sidecar processes never
// conflate on merge (see MockRegistry.SetHits/GetHits).
func TestMultiDatabaseCallScoping_HitKeysDoNotCollide(t *testing.T) {
	districtMock := &types.ExpectStatement{
		Channel:         types.WritePostgreSQL,
		Database:        "district",
		AccessingTables: []string{"orders"},
		VerifyOperation: "UPDATE",
		CallN:           1,
	}
	masterMock := &types.ExpectStatement{
		Channel:         types.WritePostgreSQL,
		Database:        "master",
		AccessingTables: []string{"orders"},
		VerifyOperation: "UPDATE",
		CallN:           1,
	}

	if mockHitKey(districtMock) == mockHitKey(masterMock) {
		t.Errorf("expected distinct hit keys for EXPECTs on different databases, both got %q", mockHitKey(districtMock))
	}
}

// TestMultiDatabaseCallScoping_UnscopedExpectMatchesAnyDatabase pins down the
// backward-compatible behavior: an EXPECT with no DATABASE clause (the
// common single-database and non-colliding-multi-database case, e.g.
// examples/multi-db-linespecs) must continue to match a query from any
// proxy, scoped or not.
func TestMultiDatabaseCallScoping_UnscopedExpectMatchesAnyDatabase(t *testing.T) {
	reg := NewMockRegistry()
	spec := &types.TestSpec{
		Name: "unscoped_expect",
		Expects: []types.ExpectStatement{
			{
				Channel:         types.ReadMySQL,
				AccessingTables: []string{"order_events"},
				VerifyOperation: "SELECT",
			},
		},
	}
	reg.Register(spec)

	mock, ok := reg.FindMockByTables("mongo", []string{"order_events"}, "SELECT", nil, nil, nil)
	if !ok {
		t.Fatal("Expected an unscoped EXPECT to match a query from a proxy with a database identity")
	}
	if mock.Database != "" {
		t.Errorf("Expected unscoped EXPECT (Database==\"\"), got %q", mock.Database)
	}
}
