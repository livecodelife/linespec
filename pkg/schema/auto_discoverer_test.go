package schema

import (
	"testing"
)

func TestFilterExcluded(t *testing.T) {
	tests := []struct {
		name    string
		tables  []string
		exclude []string
		want    []string
	}{
		{
			name:    "no exclusions",
			tables:  []string{"users", "todos", "orders"},
			exclude: nil,
			want:    []string{"users", "todos", "orders"},
		},
		{
			name:    "empty exclusions",
			tables:  []string{"users", "todos"},
			exclude: []string{},
			want:    []string{"users", "todos"},
		},
		{
			name:    "excludes rails tables",
			tables:  []string{"users", "todos", "ar_internal_metadata", "schema_migrations"},
			exclude: []string{"ar_internal_metadata", "schema_migrations"},
			want:    []string{"users", "todos"},
		},
		{
			name:    "excludes all tables",
			tables:  []string{"users", "todos"},
			exclude: []string{"users", "todos"},
			want:    []string{},
		},
		{
			name:    "exclude not present",
			tables:  []string{"users", "todos"},
			exclude: []string{"orders"},
			want:    []string{"users", "todos"},
		},
		{
			name:    "empty table list",
			tables:  []string{},
			exclude: []string{"users"},
			want:    []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterExcluded(tt.tables, tt.exclude)
			if len(got) != len(tt.want) {
				t.Errorf("FilterExcluded() len = %d, want %d (got=%v, want=%v)", len(got), len(tt.want), got, tt.want)
				return
			}
			wantSet := make(map[string]bool)
			for _, w := range tt.want {
				wantSet[w] = true
			}
			for _, g := range got {
				if !wantSet[g] {
					t.Errorf("FilterExcluded() unexpected table %q in result", g)
				}
			}
		})
	}
}
