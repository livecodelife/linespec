package registry

import "testing"

// TestIssue185_MultiDatabaseCallScoping is a placeholder for the regression
// coverage tracked by prov-2026-4ea65be4 (GitHub issue #185).
//
// When two proxied databases (e.g. two PostgreSQL databases declared in the
// `databases:` list form) expose a table with the same name, EXPECT
// statements for that table — and their CALL N ordering — must resolve to
// the specific database/store the spec author intended. Today
// MockRegistry.getExpectKey/tableSetKey (registry.go) index mocks purely by
// table name with no database dimension, so a query against one database can
// consume (or be matched against) an EXPECT written for the other database's
// stream of the same table name.
//
// This test currently just documents the expected contract; it will be
// fleshed out into a real regression during implementation of the fix.
func TestIssue185_MultiDatabaseCallScoping(t *testing.T) {
	t.Skip("placeholder proof artifact for prov-2026-4ea65be4 (issue #185); fleshed out during implementation")
}
