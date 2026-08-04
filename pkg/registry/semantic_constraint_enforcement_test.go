package registry

import (
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/types"
)

// These tests reproduce GitHub issue #159: VERIFY_OPERATION / VERIFY_WHERE /
// VERIFY_WHERE_COLUMNS / VERIFY_WRITTEN_VALUES and the READ/WRITE direction on
// an EXPECT line are parsed into ExpectStatement but never actually cause a
// spec to fail when the app's real behavior contradicts them.
//
// Both proxy.matchMock implementations (pkg/proxy/postgresql/proxy.go and
// pkg/proxy/mysql/proxy.go) call FindMockByTables first; when it returns
// not-found (because a VERIFY_* constraint failed) they silently fall back to
// the legacy FindMock, which matches purely on table key + channel-vs-SQL-verb
// and knows nothing about VerifyOperation/VerifyWhere/VerifyWrittenValues, so
// it records a hit anyway. matchesSemanticConstraints also never checks
// mock.Channel at all, so READ/WRITE direction is not enforced even on the
// semantic path. These tests reproduce that end-to-end via the registry's
// public API, mirroring the exact call sequence proxy.matchMock performs.

func TestIssue159_VerifyOperationMismatch_MustFailVerification(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Name: "n3_wrong_operation",
		Expects: []types.ExpectStatement{
			{
				Channel:         types.WritePostgreSQL,
				Table:           "applications",
				AccessingTables: []string{"applications"},
				VerifyOperation: "UPDATE",
			},
		},
	})

	query := "insert into applications (household_size, email) values ($1, $2)"

	// Mirrors proxy.matchMock: try the semantic path first, then the legacy
	// fallback if it doesn't find a match.
	if _, ok := reg.FindMockByTables([]string{"applications"}, "INSERT", nil, nil, nil); ok {
		t.Fatal("FindMockByTables unexpectedly matched an INSERT against a mock declaring VERIFY_OPERATION UPDATE")
	}
	reg.FindMock("applications", query)

	if err := reg.VerifyAll(); err == nil {
		t.Fatal("bug (issue #159): VerifyAll reported all expectations satisfied for an INSERT " +
			"against a mock declaring VERIFY_OPERATION UPDATE — the constraint was silently bypassed " +
			"by the legacy fallback matcher instead of failing the spec")
	}
}

func TestIssue159_ReadWriteDirectionMismatch_MustFailVerification(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Name: "read_expected_but_app_writes",
		Expects: []types.ExpectStatement{
			{
				Channel:         types.ReadPostgreSQL, // EXPECT READ:POSTGRESQL
				Table:           "applications",
				AccessingTables: []string{"applications"},
			},
		},
	})

	query := "insert into applications (household_size, email) values ($1, $2)"

	if _, ok := reg.FindMockByTables([]string{"applications"}, "INSERT", nil, nil, nil); ok {
		t.Fatal("bug (issue #159): a READ mock matched an INSERT (write) — " +
			"matchesSemanticConstraints never checks mock.Channel against the actual operation direction")
	}
	reg.FindMock("applications", query)

	if err := reg.VerifyAll(); err == nil {
		t.Fatal("bug (issue #159): VerifyAll reported the READ expectation satisfied even though the " +
			"app only ever performed a WRITE against the table")
	}
}
