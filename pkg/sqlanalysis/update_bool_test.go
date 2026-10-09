package sqlanalysis

import (
	"strings"
	"testing"
)

// Proof tests for prov-2026-beed31fc: an UPDATE SET item whose value is a bare
// TRUE, FALSE or NULL must be extracted as written, as INSERT already does.
// Case is preserved. Controls pin behaviour that must not change.

func TestUpdateWrittenValuesBooleanKeywords(t *testing.T) {
	dialects := []struct {
		name string
		d    Dialect
		bind string
	}{
		{"mysql", MySQL, "?"},
		{"postgresql", PostgreSQL, "$1"},
	}

	type tcase struct {
		name  string
		query string // %s is replaced by the dialect's bind token
		want  map[string]string
		where map[string]string // nil: WhereValues not checked
	}
	cases := []tcase{
		{"false", "UPDATE todos SET completed = FALSE WHERE id = 1", map[string]string{"completed": "FALSE"}, nil},
		{"true", "UPDATE todos SET completed = TRUE WHERE id = 1", map[string]string{"completed": "TRUE"}, nil},
		{"lowercase false", "UPDATE todos SET completed = false WHERE id = 1", map[string]string{"completed": "false"}, nil},
		{"lowercase true", "UPDATE todos SET completed = true WHERE id = 1", map[string]string{"completed": "true"}, nil},
		{"null", "UPDATE todos SET deleted_at = NULL WHERE id = 1", map[string]string{"deleted_at": "NULL"}, nil},
		{"lowercase null", "UPDATE todos SET deleted_at = null WHERE id = 1", map[string]string{"deleted_at": "null"}, nil},
		{"mixed", "UPDATE todos SET title = 'Buy', completed = FALSE, user_id = 42, note = %s WHERE id = 1",
			map[string]string{"title": "Buy", "completed": "FALSE", "user_id": "42", "note": PresentSentinel}, nil},
		{"mixed keywords", "UPDATE todos SET a = TRUE, b = NULL, c = 'x' WHERE id = 1",
			map[string]string{"a": "TRUE", "b": "NULL", "c": "x"}, nil},
		// Word boundary: not literals, so not extracted (unchanged behaviour).
		{"control bare identifier TRUEISH", "UPDATE todos SET a = TRUEISH WHERE id = 1", map[string]string{}, nil},
		{"control column reference", "UPDATE todos SET a = other_col WHERE id = 1", map[string]string{}, nil},
		{"control identifier beginning NULL", "UPDATE todos SET a = NULLABLE WHERE id = 1", map[string]string{}, nil},
		// Controls: no keywords.
		{"control no keywords", "UPDATE todos SET a = 'x', b = 42, c = %s WHERE flag = 1 AND id = 7",
			map[string]string{"a": "x", "b": "42", "c": PresentSentinel}, map[string]string{"flag": "1", "id": "7"}},
		{"control where unchanged with keyword", "UPDATE todos SET completed = FALSE WHERE flag = 1 AND id = 7",
			map[string]string{"completed": "FALSE"}, map[string]string{"flag": "1", "id": "7"}},
		{"control insert", "INSERT INTO todos (a, b) VALUES ('a', FALSE)", map[string]string{"a": "a", "b": "FALSE"}, nil},
		{"control insert null", "INSERT INTO todos (a, b) VALUES ('a', NULL)", map[string]string{"a": "a", "b": "NULL"}, nil},
	}

	for _, dl := range dialects {
		for _, tc := range cases {
			t.Run(dl.name+"/"+tc.name, func(t *testing.T) {
				q := strings.ReplaceAll(tc.query, "%s", dl.bind)
				res := Analyze(dl.d, q, Binds{})
				got := res.WrittenValues
				if len(got) != len(tc.want) {
					t.Errorf("WrittenValues = %q, want %q", got, tc.want)
				}
				for col, w := range tc.want {
					if got[col] != w {
						t.Errorf("%s = %q, want %q (all: %q)", col, got[col], w, got)
					}
				}
				if tc.where != nil {
					if len(res.WhereValues) != len(tc.where) {
						t.Errorf("WhereValues = %q, want %q", res.WhereValues, tc.where)
					}
					for col, w := range tc.where {
						if res.WhereValues[col] != w {
							t.Errorf("where %s = %q, want %q", col, res.WhereValues[col], w)
						}
					}
				}
			})
		}
	}
}
