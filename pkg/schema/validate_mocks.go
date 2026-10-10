package schema

import (
	"fmt"
	"strings"

	"github.com/livecodelife/linespec/v3/pkg/types"
)

// MockSpec is the parsed expects of one spec file, as seen by ValidateMocks.
type MockSpec struct {
	File    string
	Expects []types.ExpectStatement
}

// RowLoader returns the rows (column -> value maps) of an expect's RETURNS payload.
type RowLoader func(e types.ExpectStatement) ([]map[string]interface{}, error)

// ValidateMocks checks PostgreSQL expects against a discovered schema. It is
// pure: a nil loader or a loader error means rows cannot be checked, never a
// validation error. Unresolvable qualified tables and tables with no columns
// are skipped.
func ValidateMocks(specs []MockSpec, schema map[string][]ColumnInfo, load RowLoader) []error {
	if len(schema) == 0 {
		return nil
	}
	lower := make(map[string][]ColumnInfo, len(schema))
	for k, v := range schema {
		lower[strings.ToLower(k)] = v
	}
	var errs []error
	for _, sp := range specs {
		for _, e := range sp.Expects {
			if e.Channel != types.ReadPostgreSQL && e.Channel != types.WritePostgreSQL {
				continue
			}
			errs = append(errs, validateExpect(sp.File, e, lower, load)...)
		}
	}
	return errs
}

func lookupTable(lower map[string][]ColumnInfo, name string) ([]ColumnInfo, bool) {
	n := strings.ToLower(name)
	if cols, ok := lower[n]; ok {
		return cols, true
	}
	if i := strings.LastIndex(n, "."); i >= 0 {
		cols, ok := lower[n[i+1:]]
		return cols, ok
	}
	return nil, false
}

func validateExpect(file string, e types.ExpectStatement, lower map[string][]ColumnInfo, load RowLoader) []error {
	var errs []error
	for _, t := range e.AccessingTables {
		if _, ok := lookupTable(lower, t); !ok && !strings.Contains(t, ".") {
			errs = append(errs, fmt.Errorf("%s: unknown table %q (not in discovered schema)", file, t))
		}
	}
	if len(e.AccessingTables) != 1 {
		return errs
	}
	table := e.AccessingTables[0]
	cols, ok := lookupTable(lower, table)
	if !ok || len(cols) == 0 {
		return errs
	}
	known := make(map[string]bool, len(cols))
	names := make([]string, len(cols))
	for i, c := range cols {
		known[strings.ToLower(c.Name)] = true
		names[i] = c.Name
	}
	check := func(col, where string) {
		if !known[strings.ToLower(col)] {
			errs = append(errs, fmt.Errorf("%s: %s references unknown column %q on table %q (known columns: %s)",
				file, where, col, table, strings.Join(names, ", ")))
		}
	}
	for c := range e.VerifyWrittenValues {
		check(c, "VERIFY_WRITTEN_VALUES")
	}
	for c := range e.VerifyWhere {
		check(c, "VERIFY_WHERE")
	}
	for _, c := range e.VerifyWhereColumns {
		check(c, "VERIFY_WHERE_COLUMNS")
	}
	if e.ReturnsFile != "" && load != nil {
		if rows, err := load(e); err == nil {
			seen := map[string]bool{}
			for _, row := range rows {
				for c := range row {
					if !seen[strings.ToLower(c)] {
						seen[strings.ToLower(c)] = true
						check(c, "RETURNS "+e.ReturnsFile)
					}
				}
			}
		}
	}
	return errs
}
