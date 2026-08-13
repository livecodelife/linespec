package dsl

import "testing"

// TestIssue185_VerifyBeforeReturnsOrderIndependent is a placeholder for the
// regression coverage tracked by prov-2026-4ea65be4 (GitHub issue #185).
//
// parseExpect (pkg/dsl/parser.go) checks trailing EXPECT clauses in a fixed
// sequence, ending with a `for p.peek().Type == TokenVerify` loop after the
// RETURNS/RESPONSE_HEADERS checks. When a legacy VERIFY line appears before
// RETURNS in the source, RETURNS is never consumed (peek() sees TokenVerify,
// not TokenReturns, when the RETURNS check runs), which orphans the RETURNS
// token at the head of the remaining stream and causes the parser to report
// "RESPOND block is required for HTTP-triggered tests" even when RESPOND is
// present later in the file.
//
// This test currently just documents the expected contract; it will be
// fleshed out into a real regression during implementation of the fix.
func TestIssue185_VerifyBeforeReturnsOrderIndependent(t *testing.T) {
	t.Skip("placeholder proof artifact for prov-2026-4ea65be4 (issue #185); fleshed out during implementation")
}
