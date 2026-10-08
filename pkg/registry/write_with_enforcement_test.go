package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/sqlanalysis"
	"github.com/livecodelife/linespec/v3/pkg/types"
)

// prov-2026-be9b4aab: EXPECT WRITE:<db> ... WITH <payload> was parsed into
// ExpectStatement.WithFile and never read for a SQL write, so a write carrying
// the wrong values passed. These tests mirror the exact call sequence the
// MySQL and PostgreSQL proxies perform (sqlanalysis.Analyze, then
// FindMockByTables, then the legacy FindMock fallback) and assert that the
// payload's fields are compared to the written values.
//
// Both mock shapes are covered: with ACCESSING_TABLES (semantic path) and
// without (the legacy path; examples/todo-linespecs/update_todo_success.linespec
// is of this shape).

type writeCase struct {
	name    string
	channel types.ExpectChannel
	dialect sqlanalysis.Dialect
	table   string
	query   string
	binds   []string // PostgreSQL positional binds, if the query uses $n
	payload string   // YAML payload file contents
}

// runWrite registers one WRITE mock with WITH payload, replays query the way
// the proxy would, and returns the registry's final verdict.
func runWrite(t *testing.T, c writeCase, semantic bool) (*MockRegistry, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "payloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payloads", "write.yaml"), []byte(c.payload), 0o644); err != nil {
		t.Fatal(err)
	}
	mock := types.ExpectStatement{
		Channel:  c.channel,
		Table:    c.table,
		WithFile: "payloads/write.yaml",
		BaseDir:  dir,
	}
	if semantic {
		mock.AccessingTables = []string{c.table}
	}
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{Name: c.name, Expects: []types.ExpectStatement{mock}})

	r := sqlanalysis.Analyze(c.dialect, c.query, sqlanalysis.Binds{Positional: c.binds})
	if _, ok := reg.FindMockByTables("", []string{c.table}, r.Operation, r.WhereColumns, r.WhereValues, r.WrittenValues); !ok {
		reg.FindMock(c.table, c.query)
	}
	return reg, reg.VerifyAll()
}

func mismatchCases() []writeCase {
	return []writeCase{
		{
			name: "mysql_update_value_differs", channel: types.WriteMySQL, dialect: sqlanalysis.MySQL, table: "todos",
			query:   "UPDATE `todos` SET `description` = 'Milk, eggs, and bread - organic only' WHERE `todos`.`id` = 1",
			payload: "description: Milk, eggs, and bread\n",
		},
		{
			name: "mysql_insert_value_differs", channel: types.WriteMySQL, dialect: sqlanalysis.MySQL, table: "todos",
			query:   "INSERT INTO `todos` (`title`, `user_id`) VALUES ('Buy groceries', 42)",
			payload: "title: Walk the dog\nuser_id: 42\n",
		},
		{
			name: "mysql_update_no_matching_column", channel: types.WriteMySQL, dialect: sqlanalysis.MySQL, table: "todos",
			query:   "UPDATE `todos` SET `description` = 'x' WHERE `todos`.`id` = 1",
			payload: "nonexistent_column: whatever\n",
		},
		{
			name: "postgres_update_value_differs", channel: types.WritePostgreSQL, dialect: sqlanalysis.PostgreSQL, table: "todos",
			query:   "UPDATE todos SET description = 'Milk, eggs, and bread - organic only' WHERE id = 1",
			payload: "description: Milk, eggs, and bread\n",
		},
		{
			name: "postgres_update_value_differs_bound", channel: types.WritePostgreSQL, dialect: sqlanalysis.PostgreSQL, table: "todos",
			query:   "UPDATE todos SET description = $1 WHERE id = $2",
			binds:   []string{"Milk, eggs, and bread - organic only", "1"},
			payload: "description: Milk, eggs, and bread\n",
		},
		{
			name: "postgres_insert_value_differs", channel: types.WritePostgreSQL, dialect: sqlanalysis.PostgreSQL, table: "todos",
			query:   "INSERT INTO todos (title, user_id) VALUES ($1, $2)",
			binds:   []string{"Buy groceries", "42"},
			payload: "title: Walk the dog\nuser_id: 42\n",
		},
		{
			name: "postgres_insert_no_matching_column", channel: types.WritePostgreSQL, dialect: sqlanalysis.PostgreSQL, table: "todos",
			query:   "INSERT INTO todos (title, user_id) VALUES ($1, $2)",
			binds:   []string{"Buy groceries", "42"},
			payload: "nonexistent_column: whatever\n",
		},
	}
}

func TestWriteWithPayloadMismatchFails(t *testing.T) {
	for _, c := range mismatchCases() {
		for _, semantic := range []bool{true, false} {
			path := "legacy"
			if semantic {
				path = "semantic"
			}
			// Bound PostgreSQL values only reach the registry on the semantic path.
			if !semantic && len(c.binds) > 0 {
				continue
			}
			t.Run(c.name+"/"+path, func(t *testing.T) {
				reg, err := runWrite(t, c, semantic)
				if err == nil {
					t.Fatal("VerifyAll reported success for a write whose values contradict the WITH payload; " +
						"the WITH clause on a WRITE was silently ignored")
				}
				// The failure must be a diff, not merely "never called": it names
				// the payload field so the author can see what disagreed.
				var field string
				for _, f := range []string{"description", "title", "nonexistent_column"} {
					if strings.Contains(c.payload, f+":") {
						field = f
					}
				}
				if !strings.Contains(err.Error(), field) {
					t.Fatalf("failure should name the payload field %q in its diff, got: %v", field, err)
				}
				if !strings.Contains(err.Error(), "WITH") {
					t.Fatalf("failure should identify the WITH payload as the source, got: %v", err)
				}
				_ = reg
			})
		}
	}
}

func TestWriteWithPayloadMismatchDiffShowsExpectedAndActual(t *testing.T) {
	c := mismatchCases()[0]
	for _, semantic := range []bool{true, false} {
		_, err := runWrite(t, c, semantic)
		if err == nil {
			t.Fatalf("semantic=%v: expected a failure for a mismatching UPDATE", semantic)
		}
		msg := err.Error()
		if !strings.Contains(msg, "Milk, eggs, and bread - organic only") || !strings.Contains(msg, "Milk, eggs, and bread") {
			t.Fatalf("semantic=%v: diff must show both the payload value and the written value, got: %v", semantic, err)
		}
	}
}

func TestWriteWithPayloadMatchPasses(t *testing.T) {
	cases := []writeCase{
		{
			name: "mysql_update_matches", channel: types.WriteMySQL, dialect: sqlanalysis.MySQL, table: "todos",
			query:   "UPDATE `todos` SET `description` = 'Milk, eggs, and bread' WHERE `todos`.`id` = 1",
			payload: "description: Milk, eggs, and bread\n",
		},
		{
			// Payload is a subset of the written columns, with non-string YAML scalars.
			name: "mysql_insert_subset_with_scalars", channel: types.WriteMySQL, dialect: sqlanalysis.MySQL, table: "todos",
			query:   "INSERT INTO `todos` (`title`, `completed`, `user_id`) VALUES ('Buy groceries', 0, 42)",
			payload: "title: Buy groceries\nuser_id: 42\n",
		},
		{
			name: "postgres_update_matches", channel: types.WritePostgreSQL, dialect: sqlanalysis.PostgreSQL, table: "todos",
			query:   "UPDATE todos SET description = 'Milk, eggs, and bread' WHERE id = 1",
			payload: "description: Milk, eggs, and bread\n",
		},
		{
			name: "postgres_update_matches_bound", channel: types.WritePostgreSQL, dialect: sqlanalysis.PostgreSQL, table: "todos",
			query:   "UPDATE todos SET description = $1 WHERE id = $2",
			binds:   []string{"Milk, eggs, and bread", "1"},
			payload: "description: Milk, eggs, and bread\n",
		},
		{
			name: "postgres_insert_matches", channel: types.WritePostgreSQL, dialect: sqlanalysis.PostgreSQL, table: "todos",
			query:   "INSERT INTO todos (title, user_id) VALUES ($1, $2)",
			binds:   []string{"Buy groceries", "42"},
			payload: "title: Buy groceries\nuser_id: 42\n",
		},
	}
	for _, c := range cases {
		for _, semantic := range []bool{true, false} {
			if !semantic && len(c.binds) > 0 {
				continue
			}
			path := "legacy"
			if semantic {
				path = "semantic"
			}
			t.Run(c.name+"/"+path, func(t *testing.T) {
				if _, err := runWrite(t, c, semantic); err != nil {
					t.Fatalf("a write matching its WITH payload must pass, got: %v", err)
				}
			})
		}
	}
}

// On the legacy path (FindMock(key, query)) no bind values reach
// sqlanalysis.Analyze, so a prepared statement's written value is an
// unresolved placeholder ($n / ?). A payload field whose written value is a
// placeholder cannot be judged and is skipped; literals are still compared; if
// nothing could be compared the write does not fail.
func TestWriteWithPayloadSkipsUnresolvedPlaceholdersOnLegacyPath(t *testing.T) {
	pass := []writeCase{
		{
			name: "postgres_legacy_all_placeholders", channel: types.WritePostgreSQL, dialect: sqlanalysis.PostgreSQL, table: "todos",
			query:   "UPDATE todos SET description = $1 WHERE id = $2",
			payload: "description: Milk, eggs, and bread\n",
		},
		{
			name: "mysql_legacy_all_placeholders", channel: types.WriteMySQL, dialect: sqlanalysis.MySQL, table: "todos",
			query:   "UPDATE `todos` SET `description` = ? WHERE `todos`.`id` = ?",
			payload: "description: Milk, eggs, and bread\n",
		},
		{
			name: "postgres_legacy_literal_matches_other_placeholder", channel: types.WritePostgreSQL, dialect: sqlanalysis.PostgreSQL, table: "todos",
			query:   "UPDATE todos SET description = $1, title = 'Buy groceries' WHERE id = $2",
			payload: "description: anything\ntitle: Buy groceries\n",
		},
		{
			name: "mysql_legacy_literal_matches_other_placeholder", channel: types.WriteMySQL, dialect: sqlanalysis.MySQL, table: "todos",
			query:   "INSERT INTO `todos` (`title`, `user_id`) VALUES (?, 42)",
			payload: "title: Buy groceries\nuser_id: 42\n",
		},
	}
	for _, c := range pass {
		t.Run(c.name, func(t *testing.T) {
			if _, err := runWrite(t, c, false); err != nil {
				t.Fatalf("an unresolved placeholder must be skipped, not reported as a mismatch; got: %v", err)
			}
		})
	}

	fail := []writeCase{
		{
			name: "postgres_legacy_literal_differs_other_placeholder", channel: types.WritePostgreSQL, dialect: sqlanalysis.PostgreSQL, table: "todos",
			query:   "UPDATE todos SET description = $1, title = 'Buy groceries' WHERE id = $2",
			payload: "description: anything\ntitle: Walk the dog\n",
		},
		{
			name: "mysql_legacy_literal_differs_other_placeholder", channel: types.WriteMySQL, dialect: sqlanalysis.MySQL, table: "todos",
			query:   "UPDATE `todos` SET `description` = ?, `title` = 'Buy groceries' WHERE `todos`.`id` = ?",
			payload: "description: anything\ntitle: Walk the dog\n",
		},
	}
	for _, c := range fail {
		t.Run(c.name, func(t *testing.T) {
			_, err := runWrite(t, c, false)
			if err == nil {
				t.Fatal("a literal that differs from the payload must still fail even when another field is a placeholder")
			}
			if !strings.Contains(err.Error(), "title") || !strings.Contains(err.Error(), "WITH") {
				t.Fatalf("failure should name the literal field %q and the WITH payload, got: %v", "title", err)
			}
			if strings.Contains(err.Error(), "description") {
				t.Fatalf("the placeholder field %q must not be reported, got: %v", "description", err)
			}
		})
	}

	// A payload matching no written column still fails on the legacy path.
	t.Run("no_column_match_still_fails", func(t *testing.T) {
		c := writeCase{
			name: "legacy_no_match", channel: types.WritePostgreSQL, dialect: sqlanalysis.PostgreSQL, table: "todos",
			query:   "UPDATE todos SET description = $1 WHERE id = $2",
			payload: "nonexistent_column: whatever\n",
		}
		if _, err := runWrite(t, c, false); err == nil {
			t.Fatal("a payload with no column matching the write must still fail")
		}
	})
}
