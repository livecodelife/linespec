package sqlanalysis

import "testing"

// Proof tests for prov-2026-468bbfbe: INSERT VALUES lists must be split on
// top-level commas only, honouring quoted literals and parenthesis depth.
// Each case asserts the full WrittenValues map so that both a wrongly-split
// value and a column left misaligned are caught.

type writtenCase struct {
	name    string
	query   string
	binds   Binds
	want    map[string]string
	wantAny map[string][]string // column -> acceptable values (escape handling is unspecified)
}

func runWrittenCases(t *testing.T, dialects map[string]Dialect, cases []writtenCase) {
	t.Helper()
	for dname, d := range dialects {
		for _, tc := range cases {
			t.Run(dname+"/"+tc.name, func(t *testing.T) {
				got := Analyze(d, tc.query, tc.binds).WrittenValues
				want := len(tc.want) + len(tc.wantAny)
				if len(got) != want {
					t.Errorf("WrittenValues = %q, want %d columns", got, want)
				}
				for col, w := range tc.want {
					if got[col] != w {
						t.Errorf("%s = %q, want %q (all: %q)", col, got[col], w, got)
					}
				}
				for col, ws := range tc.wantAny {
					ok := false
					for _, w := range ws {
						if got[col] == w {
							ok = true
						}
					}
					if !ok {
						t.Errorf("%s = %q, want one of %q (all: %q)", col, got[col], ws, got)
					}
				}
			})
		}
	}
}

func TestWrittenValuesQuotedCommas(t *testing.T) {
	dialects := map[string]Dialect{"mysql": MySQL, "postgresql": PostgreSQL}
	runWrittenCases(t, dialects, []writtenCase{
		{
			name:  "control_no_commas_in_literals",
			query: `INSERT INTO todos (title, description, completed, user_id) VALUES ('Groceries', 'Milk and bread', false, 42)`,
			want: map[string]string{
				"title": "Groceries", "description": "Milk and bread", "completed": "false", "user_id": "42",
			},
		},
		{
			name:  "literal_with_commas_is_one_value",
			query: `INSERT INTO todos (title, description, completed, user_id) VALUES ('Groceries', 'Milk, eggs, and bread', false, 42)`,
			want: map[string]string{
				"title": "Groceries", "description": "Milk, eggs, and bread", "completed": "false", "user_id": "42",
			},
		},
		{
			name:  "comma_literal_first_and_last",
			query: `INSERT INTO todos (title, user_id, note) VALUES ('a, b', 7, 'x, y')`,
			want:  map[string]string{"title": "a, b", "user_id": "7", "note": "x, y"},
		},
		{
			name:  "literal_that_is_only_a_comma",
			query: `INSERT INTO todos (title, user_id) VALUES (',', 7)`,
			want:  map[string]string{"title": ",", "user_id": "7"},
		},
	})

	// Placeholders: each dialect spells them differently.
	runWrittenCases(t, map[string]Dialect{"mysql": MySQL}, []writtenCase{{
		name:  "bind_alongside_comma_literal_unresolved",
		query: `INSERT INTO todos (title, description, user_id) VALUES (?, 'Milk, eggs, and bread', 42)`,
		want:  map[string]string{"title": PresentSentinel, "description": "Milk, eggs, and bread", "user_id": "42"},
	}, {
		name:  "bind_alongside_comma_literal_resolved",
		query: `INSERT INTO todos (title, description, user_id) VALUES (?, 'Milk, eggs, and bread', ?)`,
		binds: Binds{Positional: []string{"T", "9"}},
		want:  map[string]string{"title": "T", "description": "Milk, eggs, and bread", "user_id": "9"},
	}})
	runWrittenCases(t, map[string]Dialect{"postgresql": PostgreSQL}, []writtenCase{{
		name:  "bind_alongside_comma_literal_unresolved",
		query: `INSERT INTO todos (title, description, user_id) VALUES ($1, 'Milk, eggs, and bread', 42)`,
		want:  map[string]string{"title": PresentSentinel, "description": "Milk, eggs, and bread", "user_id": "42"},
	}, {
		name:  "bind_alongside_comma_literal_resolved",
		query: `INSERT INTO todos (title, description, user_id) VALUES ($1, 'Milk, eggs, and bread', $2)`,
		binds: Binds{Positional: []string{"T", "9"}},
		want:  map[string]string{"title": "T", "description": "Milk, eggs, and bread", "user_id": "9"},
	}})
}

func TestWrittenValuesEscapedQuotesAndParens(t *testing.T) {
	both := map[string]Dialect{"mysql": MySQL, "postgresql": PostgreSQL}

	runWrittenCases(t, both, []writtenCase{
		{
			// Constraint 4: nothing special in the values, behaviour unchanged.
			name:  "control_plain_values",
			query: `INSERT INTO users (name, age, active) VALUES ('Ada', 36, true)`,
			want:  map[string]string{"name": "Ada", "age": "36", "active": "true"},
		},
		{
			// Whether the doubled quote is collapsed to one is not specified by
			// the record, so either representation is accepted; what is pinned
			// is that the literal is one value and its neighbours stay aligned.
			name:  "doubled_quote_escape",
			query: `INSERT INTO notes (body, author, n) VALUES ('it''s, ok', 'Ada', 3)`,
			wantAny: map[string][]string{
				"body": {"it's, ok", "it''s, ok"},
			},
			want: map[string]string{"author": "Ada", "n": "3"},
		},
		{
			name:  "function_call_without_args_kept_whole",
			query: `INSERT INTO events (name, created_at, n) VALUES ('x', NOW(), 5)`,
			want:  map[string]string{"name": "x", "created_at": "NOW()", "n": "5"},
		},
		{
			name:  "function_call_with_commas_kept_whole",
			query: `INSERT INTO events (name, label, n) VALUES ('x', COALESCE(a, b), 5)`,
			want:  map[string]string{"name": "x", "label": "COALESCE(a, b)", "n": "5"},
		},
		{
			name:  "close_paren_inside_literal",
			query: `INSERT INTO notes (body, author, n) VALUES ('smile :)', 'Ada', 3)`,
			want:  map[string]string{"body": "smile :)", "author": "Ada", "n": "3"},
		},
		{
			name:  "open_and_close_paren_and_comma_inside_literal",
			query: `INSERT INTO notes (body, author) VALUES ('f(a, b) :)', 'Ada')`,
			want:  map[string]string{"body": "f(a, b) :)", "author": "Ada"},
		},
	})

	runWrittenCases(t, map[string]Dialect{"mysql": MySQL}, []writtenCase{{
		// MySQL treats \' as an escaped quote, so this is a single literal
		// whose content is a\', b (or a', b once unescaped).
		name:  "backslash_escape",
		query: `INSERT INTO notes (body, author, n) VALUES ('a\', b', 'Ada', 3)`,
		wantAny: map[string][]string{
			"body": {`a\', b`, `a', b`},
		},
		want: map[string]string{"author": "Ada", "n": "3"},
	}})
}
