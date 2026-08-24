package postgresql

import "testing"

// A RETURNS payload whose keys don't intersect the column set inferred from
// the table named in the channel position (EXPECT READ:POSTGRESQL <table>)
// must fail the spec with both column sets named, not silently send rows
// with every field NULL. See prov-2026-14e58ba9.
func TestMockResultColumnMismatch_FailsInsteadOfSendingNullRows(t *testing.T) {
	t.Skip("pending implementation - see prov-2026-14e58ba9")
}
