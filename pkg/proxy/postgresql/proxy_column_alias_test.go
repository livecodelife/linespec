package postgresql

import "testing"

// TestExtractSelectColumns_RespectsAlias reproduces GitHub issue #216: for a
// SELECT whose select-list is a computed expression followed by `AS <alias>`,
// extractSelectColumns (proxy.go) must return the alias, not the leading
// function/expression name. Today the " AS " split keeps the wrong side
// (col[:asIdx] instead of col[asIdx+len(" AS "):]), and a later aggregate-name
// normalization step then further collapses the retained text down to the bare
// function name (e.g. "sum"), discarding the alias twice over.
//
// Skipped as a placeholder pending the fix under prov-2026-00cb9b88 — enable
// once extractSelectColumns is corrected to prefer the alias.
func TestExtractSelectColumns_RespectsAlias(t *testing.T) {
	t.Skip("TODO(prov-2026-00cb9b88): extractSelectColumns must return the AS alias, not the pre-alias expression")

	p := &Proxy{}

	tests := []struct {
		name string
		sql  string
		want []string
	}{
		{
			name: "coalesce+sum with alias",
			sql:  "select coalesce(sum(total_tokens), 0) as total from chat_usage where conversation_id = $1",
			want: []string{"total"},
		},
		{
			name: "sum with alias",
			sql:  "select sum(total_tokens) as total from chat_usage where conversation_id = $1",
			want: []string{"total"},
		},
		{
			name: "sum with cast and alias",
			sql:  "select sum(total_tokens)::bigint as total from chat_usage where conversation_id = $1",
			want: []string{"total"},
		},
		{
			name: "count with cast and alias",
			sql:  "select count(*)::int as n from chat_usage where conversation_id = $1",
			want: []string{"n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.extractSelectColumns(tt.sql)
			if len(got) != len(tt.want) || (len(got) > 0 && got[0] != tt.want[0]) {
				t.Errorf("extractSelectColumns(%q) = %v, want %v", tt.sql, got, tt.want)
			}
		})
	}
}
