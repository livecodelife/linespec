package oracle

import (
	"strings"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/registry"
	"github.com/livecodelife/linespec/v3/pkg/types"
)

// A matched Oracle expectation's VERIFY rules have to actually run. They did not:
// the proxy matched and stopped, never calling verify.VerifySQL the way the
// PostgreSQL proxy does, so a spec could assert the shape of a query, pass, and be
// asserting nothing. The packet below is one a real ODP.NET client sent.

func vendorTypesMock(verify []types.VerifyRule) *types.TestSpec {
	return &types.TestSpec{Expects: []types.ExpectStatement{{
		Channel:         types.ReadOracle,
		AccessingTables: []string{"fas.vendor_types"},
		VerifyOperation: "SELECT",
		Verify:          verify,
	}}}
}

func TestVerifyRuleFailureIsRecorded(t *testing.T) {
	reg := registry.NewMockRegistry()
	reg.Register(vendorTypesMock([]types.VerifyRule{
		{Target: "query", Type: "MATCHES", Pattern: `ORDER BY UPPER\(NoSuchColumn\)`},
	}))

	p := NewProxy("", "", reg)
	p.observe(load(t, "odp-select-long-489.bin"))

	errs := reg.GetVerifyErrors()
	if len(errs) == 0 {
		t.Fatal("a failing VERIFY rule recorded nothing; the rule was never evaluated")
	}
	// The message has to name the rule, not merely report a failure. VerifyAll
	// reports a recorded verify error ahead of any never-called message, so this
	// text is what a spec author reads when their assertion breaks.
	if !strings.Contains(errs[0], "READ:ORACLE") || !strings.Contains(errs[0], "NoSuchColumn") {
		t.Errorf("recorded error does not name the channel and the rule: %q", errs[0])
	}
}

func TestVerifyRuleThatHoldsRecordsNothing(t *testing.T) {
	reg := registry.NewMockRegistry()
	reg.Register(vendorTypesMock([]types.VerifyRule{
		{Target: "query", Type: "MATCHES", Pattern: `ORDER BY UPPER\(VendorCategory\)`},
	}))

	p := NewProxy("", "", reg)
	p.observe(load(t, "odp-select-long-489.bin"))

	if errs := reg.GetVerifyErrors(); len(errs) != 0 {
		t.Errorf("a rule that holds recorded %v", errs)
	}
}

// An unmatched statement is not a VERIFY failure. The rules belong to a mock, and
// a mock that did not match has not been asked anything.
func TestVerifyRulesAreNotEvaluatedOnAnUnmatchedStatement(t *testing.T) {
	reg := registry.NewMockRegistry()
	reg.Register(&types.TestSpec{Expects: []types.ExpectStatement{{
		Channel:         types.ReadOracle,
		AccessingTables: []string{"fas.vendor_types"},
		VerifyOperation: "DELETE", // the captured statement is a SELECT
		Verify: []types.VerifyRule{
			{Target: "query", Type: "MATCHES", Pattern: `ORDER BY UPPER\(NoSuchColumn\)`},
		},
	}}})

	p := NewProxy("", "", reg)
	p.observe(load(t, "odp-select-long-489.bin"))

	for _, e := range reg.GetVerifyErrors() {
		if strings.Contains(e, "NoSuchColumn") {
			t.Errorf("a VERIFY rule was evaluated on a mock that did not match: %q", e)
		}
	}
}
