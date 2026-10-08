package postgresql

import "testing"

// A statement forwarded upstream because no mock matched (e.g. against a
// missing relation) must propagate the upstream ErrorResponse to the client,
// or at minimum log it unconditionally — not resolve to an empty result set
// that reads as a passing read. See prov-2026-14e58ba9.
func TestForwardedQuery_UpstreamErrorResponseNotSwallowed(t *testing.T) {
	t.Skip("pending implementation - see prov-2026-14e58ba9")
}
