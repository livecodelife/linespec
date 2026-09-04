//go:build integration

package oracle

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/livecodelife/linespec/v3/pkg/registry"
	"github.com/livecodelife/linespec/v3/pkg/types"
)

// The unit tests read packets captured earlier; this one puts the proxy in the
// path of a live Oracle and a real client, so the framing, the relay and the
// registry match are exercised together against traffic nobody prepared.
//
//	make test-integration-oracle
//
// The client is sqlplus inside the database container, dialled back out at the
// proxy, which keeps the test free of an Oracle driver dependency - linespec
// would otherwise carry one for the sake of a test.

const (
	proxyAddr    = "127.0.0.1:15221"
	upstreamAddr = "127.0.0.1:1521"
	container    = "linespec-test-oracle"
)

func requireOracle(t *testing.T) {
	t.Helper()
	if os.Getenv("ORACLE_TEST") == "" {
		t.Skip("set ORACLE_TEST=1 and start the Oracle container (make test-integration-oracle)")
	}
}

// sqlplus runs statements through the proxy from inside the database container.
func sqlplus(t *testing.T, statements ...string) {
	t.Helper()
	script := "set feedback off\n" + strings.Join(statements, "\n") + "\nEXIT\n"
	cmd := exec.Command("docker", "exec", "-i", container, "bash", "-lc",
		"sqlplus -s system/linespec@//host.docker.internal:15221/FREEPDB1")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sqlplus: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "ORA-") && !strings.Contains(string(out), "ORA-00942") {
		t.Logf("sqlplus output: %s", out)
	}
}

func startProxy(t *testing.T, reg *registry.MockRegistry) {
	t.Helper()
	p := NewProxy(proxyAddr, upstreamAddr, reg)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		if err := p.Start(ctx); err != nil {
			t.Logf("proxy stopped: %v", err)
		}
	}()
	// The listener is up before Start returns from Listen; give the goroutine a
	// moment to reach Accept.
	time.Sleep(200 * time.Millisecond)
}

// A statement really sent by a real client, through the proxy, satisfies an
// expectation written against its shape.
func TestLiveStatementSatisfiesAnExpectation(t *testing.T) {
	requireOracle(t)

	reg := registry.NewMockRegistry()
	reg.Register(&types.TestSpec{Expects: []types.ExpectStatement{{
		Channel:            types.ReadOracle,
		AccessingTables:    []string{"hr_employees"},
		VerifyOperation:    "SELECT",
		VerifyWhereColumns: []string{"employee_id"},
	}}})
	startProxy(t, reg)

	sqlplus(t, "SELECT surname FROM hr_employees WHERE employee_id = '4471';")

	if err := reg.VerifyAll(); err != nil {
		t.Fatalf("expectation was not satisfied by a live query: %v", err)
	}
}

// The case the channel exists for, on a live wire. A table keyed on two columns
// updated by a statement naming one of them rewrites every row sharing that
// column - and the difference exists only in the SQL, so nothing but reading
// the wire can tell the two apart.
//
// Each direction gets its own registry because verify errors accumulate: a
// registry that has already recorded a failure reports it forever, which is
// right for a spec run and wrong for two cases in one test.
func TestLiveUnderScopedUpdateIsCaught(t *testing.T) {
	requireOracle(t)

	reg := registry.NewMockRegistry()
	reg.Register(&types.TestSpec{Expects: []types.ExpectStatement{{
		Channel:            types.WriteOracle,
		AccessingTables:    []string{"hr_employees"},
		VerifyOperation:    "UPDATE",
		VerifyWhereColumns: []string{"employee_id", "department"},
	}}})
	startProxy(t, reg)

	sqlplus(t, "UPDATE hr_employees SET surname = 'Byron' WHERE employee_id = '4471';")

	err := reg.VerifyAll()
	if err == nil {
		t.Fatal("an UPDATE missing a key column satisfied an expectation requiring both")
	}
	// The message has to name the missing column, or it tells whoever reads it
	// that something is wrong without saying what.
	for _, want := range []string{"VERIFY_WHERE_COLUMNS", "department", "employee_id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("failure does not mention %q:\n%v", want, err)
		}
	}
}

// The other direction, so the failure above is about the WHERE clause rather
// than the proxy failing to see an UPDATE at all.
func TestLiveCorrectlyScopedUpdateSatisfies(t *testing.T) {
	requireOracle(t)

	reg := registry.NewMockRegistry()
	reg.Register(&types.TestSpec{Expects: []types.ExpectStatement{{
		Channel:            types.WriteOracle,
		AccessingTables:    []string{"hr_employees"},
		VerifyOperation:    "UPDATE",
		VerifyWhereColumns: []string{"employee_id", "department"},
	}}})
	startProxy(t, reg)

	sqlplus(t, "UPDATE hr_employees SET surname = 'Byron' WHERE employee_id = '4471' AND department = 'RES';")

	if err := reg.VerifyAll(); err != nil {
		t.Fatalf("the correctly scoped UPDATE did not satisfy the expectation: %v", err)
	}
}

// Nothing the client sends is altered: the service must see exactly what Oracle
// said. A SELECT through the proxy returns the row a SELECT direct to the
// database returns.
func TestLiveResponsesAreRelayedUntouched(t *testing.T) {
	requireOracle(t)

	reg := registry.NewMockRegistry()
	startProxy(t, reg)

	sqlplus(t,
		"DELETE FROM hr_employees;",
		"INSERT INTO hr_employees (employee_id, surname, department) VALUES ('9902', 'Hopper', 'NAV');",
		"COMMIT;",
	)

	cmd := exec.Command("docker", "exec", "-i", container, "bash", "-lc",
		"sqlplus -s system/linespec@//host.docker.internal:15221/FREEPDB1")
	cmd.Stdin = strings.NewReader("set feedback off\nSELECT surname FROM hr_employees WHERE employee_id = '9902';\nEXIT\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sqlplus: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Hopper") {
		t.Fatalf("the row did not come back through the proxy:\n%s", out)
	}
}
