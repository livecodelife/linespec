package dsl

import (
	"strings"
	"testing"
)

// prov-2026-be9b4aab: a WITH payload on a WRITE is asserted against the
// INSERT/UPDATE's written values. A DELETE writes no values, so there is
// nothing to compare; accepting the clause would leave it inert, which is the
// silent-pass this record removes. It must be rejected at parse time.
//
// parseSpecText is shared with expect_not_clauses_test.go.

func TestWriteWithOnDeleteRejected(t *testing.T) {
	cases := map[string]string{
		"mysql USING_SQL then WITH": `TEST del
RECEIVE HTTP:DELETE http://localhost:3000/todos/1

EXPECT WRITE:MYSQL todos
USING_SQL """
DELETE FROM todos WHERE id = 1
"""
WITH {{payloads/todo.yaml}}

RESPOND HTTP:204`,
		"mysql WITH then USING_SQL": `TEST del
RECEIVE HTTP:DELETE http://localhost:3000/todos/1

EXPECT WRITE:MYSQL todos
WITH {{payloads/todo.yaml}}
USING_SQL """
DELETE FROM todos WHERE id = 1
"""

RESPOND HTTP:204`,
		"mysql USING_SQL_CONTAINS": `TEST del
RECEIVE HTTP:DELETE http://localhost:3000/todos/1

EXPECT WRITE:MYSQL todos
USING_SQL_CONTAINS """
DELETE FROM todos
"""
WITH {{payloads/todo.yaml}}

RESPOND HTTP:204`,
		"mysql DELETE on the channel line": `TEST del
RECEIVE HTTP:DELETE http://localhost:3000/todos/1

EXPECT WRITE:MYSQL DELETE todos
WITH {{payloads/todo.yaml}}

RESPOND HTTP:204`,
		"mysql VERIFY_OPERATION DELETE": `TEST del
RECEIVE HTTP:DELETE http://localhost:3000/todos/1

EXPECT WRITE:MYSQL todos
ACCESSING_TABLES todos
VERIFY_OPERATION DELETE
WITH {{payloads/todo.yaml}}

RESPOND HTTP:204`,
		"postgresql USING_SQL": `TEST del
RECEIVE HTTP:DELETE http://localhost:3000/todos/1

EXPECT WRITE:POSTGRESQL todos
USING_SQL """
DELETE FROM todos WHERE id = 1
"""
WITH {{payloads/todo.yaml}}

RESPOND HTTP:204`,
		"postgresql VERIFY_OPERATION DELETE": `TEST del
RECEIVE HTTP:DELETE http://localhost:3000/todos/1

EXPECT WRITE:POSTGRESQL todos
ACCESSING_TABLES todos
VERIFY_OPERATION DELETE
WITH {{payloads/todo.yaml}}

RESPOND HTTP:204`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseSpecText(t, src)
			if err == nil {
				t.Fatal("parser accepted WITH on a DELETE write; the clause would be inert and the spec would pass asserting nothing")
			}
			msg := err.Error()
			if !strings.Contains(msg, "WITH") || !strings.Contains(strings.ToUpper(msg), "DELETE") {
				t.Fatalf("error should name both WITH and DELETE so the author knows what to remove, got: %v", err)
			}
		})
	}
}

// Control: the same clause on an UPDATE or INSERT is still accepted at parse
// time (it is asserted later, at match time).
func TestWriteWithOnUpdateOrInsertStillParses(t *testing.T) {
	for _, sql := range []string{"UPDATE todos SET title = 'x' WHERE id = 1", "INSERT INTO todos (title) VALUES ('x')"} {
		for _, ch := range []string{"WRITE:MYSQL", "WRITE:POSTGRESQL"} {
			src := "TEST ok\nRECEIVE HTTP:PATCH http://localhost:3000/todos/1\n\nEXPECT " + ch + " todos\nUSING_SQL \"\"\"\n" + sql + "\n\"\"\"\nWITH {{payloads/todo.yaml}}\n\nRESPOND HTTP:200"
			spec, err := parseSpecText(t, src)
			if err != nil {
				t.Fatalf("%s %q: WITH must remain valid on INSERT/UPDATE, got: %v", ch, sql, err)
			}
			if len(spec.Expects) != 1 || spec.Expects[0].WithFile != "payloads/todo.yaml" {
				t.Fatalf("%s %q: WithFile not retained: %+v", ch, sql, spec.Expects)
			}
		}
	}
}
