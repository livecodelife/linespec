package oracle_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/dsl"
	"github.com/livecodelife/linespec/v3/pkg/types"
)

// The channel exists at the DSL level, which is a separate question from
// whether the protocol parses: a spec author meets these before any byte is
// read off a wire.

func parseSpec(t *testing.T, body string) (*types.TestSpec, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spec.linespec")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	tokens, err := dsl.LexFile(path)
	if err != nil {
		return nil, err
	}
	return dsl.NewParser(tokens).Parse(path)
}

func TestOracleChannelsParse(t *testing.T) {
	spec, err := parseSpec(t, `TEST oracle_channels
RECEIVE HTTP:GET /employees

EXPECT READ:ORACLE employees
ACCESSING_TABLES employees
VERIFY_OPERATION SELECT
VERIFY_WHERE_COLUMNS employee_id

EXPECT WRITE:ORACLE employees
VERIFY_OPERATION UPDATE
VERIFY_WHERE_COLUMNS employee_id, department

RESPOND HTTP:200
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(spec.Expects) != 2 {
		t.Fatalf("got %d expectations, want 2", len(spec.Expects))
	}
	if spec.Expects[0].Channel != types.ReadOracle {
		t.Errorf("first channel = %q, want %q", spec.Expects[0].Channel, types.ReadOracle)
	}
	if spec.Expects[1].Channel != types.WriteOracle {
		t.Errorf("second channel = %q, want %q", spec.Expects[1].Channel, types.WriteOracle)
	}
	if got := spec.Expects[1].VerifyWhereColumns; len(got) != 2 {
		t.Errorf("VERIFY_WHERE_COLUMNS = %v, want two columns", got)
	}
}

// A table name is optional on a SQL channel when ACCESSING_TABLES carries it,
// and Oracle has to be in that set or the bare form is a parse error.
func TestOracleChannelAllowsAnEmptyTableName(t *testing.T) {
	_, err := parseSpec(t, `TEST bare_channel
RECEIVE HTTP:GET /employees

EXPECT READ:ORACLE
ACCESSING_TABLES employees
VERIFY_OPERATION SELECT

RESPOND HTTP:200
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
}

// The channel's one refusal, and it is refused where the author can see it.
// Accepting RETURNS and ignoring it at run time would let a spec claim to mock
// a response that the real database actually produced.
func TestReturnsIsRefusedOnOracle(t *testing.T) {
	_, err := parseSpec(t, `TEST returns_on_oracle
RECEIVE HTTP:GET /employees

EXPECT READ:ORACLE employees
RETURNS {{payloads/employee.yaml}}

RESPOND HTTP:200
`)
	if err == nil {
		t.Fatal("RETURNS on an Oracle expectation parsed without error")
	}
	for _, want := range []string{"RETURNS is not supported", "READ:ORACLE", "VERIFY_WHERE_COLUMNS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%s", want, err)
		}
	}
}

// The refusal is specific to Oracle; the other SQL channels still take RETURNS,
// because they answer for their database and Oracle does not.
func TestReturnsStillWorksOnPostgreSQL(t *testing.T) {
	_, err := parseSpec(t, `TEST returns_on_postgres
RECEIVE HTTP:GET /employees

EXPECT READ:POSTGRESQL employees
RETURNS {{payloads/employee.yaml}}

RESPOND HTTP:200
`)
	if err != nil {
		t.Fatalf("RETURNS on PostgreSQL was refused: %v", err)
	}
}
