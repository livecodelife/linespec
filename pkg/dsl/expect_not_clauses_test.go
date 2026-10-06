package dsl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/types"
)

// prov-2026-a71a796a: parseExpectNot consumed only WITH, so the documented
// negative clauses (USING_SQL, ACCESSING_TABLES, VERIFY_*, DATABASE) were
// dropped, and with a Kafka or Job trigger every later EXPECT went with them
// without any error. These tests parse real DSL text and assert on the result.

func parseSpecText(t *testing.T, content string) (*types.TestSpec, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "probe.linespec")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	tokens, err := LexFile(path)
	if err != nil {
		t.Fatalf("LexFile: %v", err)
	}
	return NewParser(tokens).Parse(path)
}

func TestExpectNotTrailingClauses_UsingSQLRetained(t *testing.T) {
	spec, err := parseSpecText(t, `TEST not-with-sql
RECEIVE HTTP:GET http://localhost:3000/users/1

EXPECT_NOT READ:MYSQL users
USING_SQL """
SELECT * FROM users
"""

RESPOND HTTP:200`)
	if err != nil {
		t.Fatalf("bug: documented EXPECT_NOT ... USING_SQL form failed to parse: %v", err)
	}
	if len(spec.ExpectsNot) != 1 {
		t.Fatalf("expected 1 EXPECT_NOT, got %d", len(spec.ExpectsNot))
	}
	if got := strings.TrimSpace(spec.ExpectsNot[0].SQL); got != "SELECT * FROM users" {
		t.Errorf("bug: USING_SQL dropped from EXPECT_NOT; SQL = %q", got)
	}
}

func TestExpectNotTrailingClauses_SemanticClausesRetained(t *testing.T) {
	spec, err := parseSpecText(t, `TEST not-with-semantic
RECEIVE HTTP:GET http://localhost:3000/users/1

EXPECT_NOT WRITE:MYSQL users
ACCESSING_TABLES [users]
VERIFY_OPERATION DELETE
DATABASE primary

RESPOND HTTP:200`)
	if err != nil {
		t.Fatalf("bug: EXPECT_NOT with ACCESSING_TABLES/VERIFY_OPERATION/DATABASE failed to parse: %v", err)
	}
	if len(spec.ExpectsNot) != 1 {
		t.Fatalf("expected 1 EXPECT_NOT, got %d", len(spec.ExpectsNot))
	}
	n := spec.ExpectsNot[0]
	if len(n.AccessingTables) != 1 || n.AccessingTables[0] != "users" {
		t.Errorf("bug: ACCESSING_TABLES dropped from EXPECT_NOT: %v", n.AccessingTables)
	}
	if n.VerifyOperation != "DELETE" {
		t.Errorf("bug: VERIFY_OPERATION dropped from EXPECT_NOT: %q", n.VerifyOperation)
	}
	if n.Database != "primary" {
		t.Errorf("bug: DATABASE dropped from EXPECT_NOT: %q", n.Database)
	}
}

func TestExpectNotTrailingClauses_KafkaTriggerKeepsLaterExpects(t *testing.T) {
	spec, err := parseSpecText(t, `TEST kafka-not-then-expects
RECEIVE EVENT:orders

EXPECT_NOT READ:MYSQL users
USING_SQL """
SELECT * FROM users
"""

EXPECT WRITE:MYSQL orders
USING_SQL """
INSERT INTO orders (id) VALUES (1)
"""

EXPECT WRITE:MYSQL audit
USING_SQL """
INSERT INTO audit (id) VALUES (1)
"""`)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(spec.ExpectsNot) != 1 {
		t.Errorf("expected 1 EXPECT_NOT, got %d", len(spec.ExpectsNot))
	}
	if len(spec.Expects) != 2 {
		t.Fatalf("bug: the spec declares two EXPECTs after EXPECT_NOT; parsed %d (the rest were dropped without error)", len(spec.Expects))
	}
}

func TestExpectNotTrailingClauses_JobTriggerKeepsLaterExpects(t *testing.T) {
	spec, err := parseSpecText(t, `TEST job-not-then-expect
RECEIVE JOB

EXPECT_NOT READ:MYSQL users
USING_SQL_CONTAINS """
FROM users
"""

EXPECT WRITE:MYSQL orders
USING_SQL_CONTAINS """
INSERT INTO orders
"""`)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(spec.Expects) != 1 {
		t.Fatalf("bug: EXPECT following EXPECT_NOT was dropped silently; parsed %d expects", len(spec.Expects))
	}
}

// Whatever the clause, a token the parser did not consume must be an error
// that names the token's line — never silence.
func TestExpectNotTrailingClauses_UnconsumedTokensRejectedWithLine(t *testing.T) {
	// HEADERS after RESPOND's own HEADERS slot is consumed; a second WITH after
	// the RESPOND block has no slot anywhere, so it is left over at end of input.
	content := `TEST leftover
RECEIVE EVENT:orders

EXPECT WRITE:MYSQL orders
USING_SQL """
INSERT INTO orders (id) VALUES (1)
"""

RESPOND HTTP:200
WITH {{a.yaml}}
WITH {{leftover.yaml}}`
	_, err := parseSpecText(t, content)
	if err == nil {
		t.Fatal("bug: a token left unconsumed at end of input was silently ignored")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "line") {
		t.Errorf("error must carry line information, got: %v", err)
	}
}

func TestExpectNotTrailingClauses_UnconsumedTokensAfterJobRejected(t *testing.T) {
	// A trailing RESPOND-less Job spec: the stray HEADERS block after the
	// EXPECTs belongs to nothing and must be reported, not dropped.
	content := `TEST job-leftover
RECEIVE JOB

EXPECT WRITE:MYSQL orders
USING_SQL """
INSERT INTO orders (id) VALUES (1)
"""

EXPECT_NOT READ:MYSQL users

EXPECT WRITE:MYSQL audit
USING_SQL """
INSERT INTO audit (id) VALUES (1)
"""`
	spec, err := parseSpecText(t, content)
	// Either the later EXPECT is retained, or parsing fails with line info.
	// Dropping it silently is the one outcome ruled out.
	if err == nil && len(spec.Expects)+len(spec.ExpectsNot) != 3 {
		t.Fatalf("bug: 3 statements declared, %d retained, no error", len(spec.Expects)+len(spec.ExpectsNot))
	}
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "line") {
		t.Errorf("parse error must carry line information, got: %v", err)
	}
}
