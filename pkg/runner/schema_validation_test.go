package runner

import (
	"fmt"
	"strings"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/config"
	"github.com/livecodelife/linespec/v3/pkg/schema"
	"github.com/livecodelife/linespec/v3/pkg/types"
)

// Specifies prov-2026-ad21b28e. Seam under test (does not exist yet), in
// package runner:
//
//	func validateSpecMocks(cfg *config.SchemaDiscoveryConfig, specs []types.TestSpec,
//		discovered map[string][]schema.ColumnInfo, load schema.RowLoader,
//		logf func(format string, args ...interface{})) error
//
// Called by RunSpec right after fetchPostgresSchema, before the proxy starts.
// Behaviour by cfg.Validate:
//   - "off" (or ""): returns nil, never calls logf, never calls load.
//   - discovered nil/empty (any non-off mode): calls logf exactly once with a
//     note that validation was skipped; returns nil.
//   - "warn": schema.ValidateMocks errors are each passed to logf; returns nil.
//   - "error": returns a non-nil error whose text contains every validation
//     error; logf is not required.
// Each types.TestSpec is converted to schema.MockSpec{File: FilePath, Expects: Expects}.
// MySQL channels are ignored (delegated to ValidateMocks).

type svLog struct{ lines []string }

func (l *svLog) logf(format string, args ...interface{}) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}
func (l *svLog) all() string { return strings.Join(l.lines, "\n") }

func svCfg(mode string) *config.SchemaDiscoveryConfig {
	return &config.SchemaDiscoveryConfig{Mode: "auto", Validate: mode}
}

func svSchema() map[string][]schema.ColumnInfo {
	return map[string][]schema.ColumnInfo{
		"users": {{Name: "id"}, {Name: "email"}},
	}
}

func svBadSpecs() []types.TestSpec {
	return []types.TestSpec{{
		Name: "bad", FilePath: "specs/bad.linespec",
		Expects: []types.ExpectStatement{
			{Channel: types.ReadPostgreSQL, AccessingTables: []string{"users"}, VerifyWhere: map[string]string{"nosuchcolumn": "1"}},
			{Channel: types.ReadPostgreSQL, AccessingTables: []string{"userz"}},
		},
	}}
}

func svLoader(called *int) schema.RowLoader {
	return func(types.ExpectStatement) ([]map[string]interface{}, error) {
		*called++
		return nil, nil
	}
}

func TestSchemaValidationOffRunsNothingAndIsSilent(t *testing.T) {
	for _, mode := range []string{"off", ""} {
		var l svLog
		calls := 0
		err := validateSpecMocks(svCfg(mode), svBadSpecs(), svSchema(), svLoader(&calls), l.logf)
		if err != nil {
			t.Fatalf("mode %q: err = %v, want nil", mode, err)
		}
		if len(l.lines) != 0 {
			t.Fatalf("mode %q: no output allowed, got %v", mode, l.lines)
		}
		if calls != 0 {
			t.Fatalf("mode %q: loader must not be called", mode)
		}
		// Even with no schema, off must stay silent (no skip note).
		if err := validateSpecMocks(svCfg(mode), svBadSpecs(), nil, nil, l.logf); err != nil || len(l.lines) != 0 {
			t.Fatalf("mode %q, nil schema: err=%v output=%v, want silence", mode, err, l.lines)
		}
	}
}

func TestSchemaValidationWarnLogsAndContinues(t *testing.T) {
	var l svLog
	err := validateSpecMocks(svCfg("warn"), svBadSpecs(), svSchema(), nil, l.logf)
	if err != nil {
		t.Fatalf("warn must not fail the run, got %v", err)
	}
	out := l.all()
	for _, want := range []string{"specs/bad.linespec", "nosuchcolumn", "userz"} {
		if !strings.Contains(out, want) {
			t.Errorf("warn output missing %q:\n%s", want, out)
		}
	}
}

func TestSchemaValidationWarnCleanSpecsStaySilent(t *testing.T) {
	var l svLog
	specs := []types.TestSpec{{FilePath: "ok.linespec", Expects: []types.ExpectStatement{
		{Channel: types.ReadPostgreSQL, AccessingTables: []string{"users"}, VerifyWhere: map[string]string{"id": "1"}}}}}
	if err := validateSpecMocks(svCfg("warn"), specs, svSchema(), nil, l.logf); err != nil || len(l.lines) != 0 {
		t.Fatalf("clean specs: err=%v output=%v, want nil and silence", err, l.lines)
	}
}

func TestSchemaValidationErrorFailsRunWithAllErrors(t *testing.T) {
	var l svLog
	err := validateSpecMocks(svCfg("error"), svBadSpecs(), svSchema(), nil, l.logf)
	if err == nil {
		t.Fatal("error mode must fail the run")
	}
	for _, want := range []string{"specs/bad.linespec", "nosuchcolumn", "userz"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("returned error missing %q (must carry all errors): %v", want, err)
		}
	}
}

func TestSchemaValidationErrorCleanSpecsPass(t *testing.T) {
	specs := []types.TestSpec{{FilePath: "ok.linespec", Expects: []types.ExpectStatement{
		{Channel: types.WritePostgreSQL, AccessingTables: []string{"users"}, VerifyWrittenValues: map[string]string{"email": "a"}}}}}
	if err := validateSpecMocks(svCfg("error"), specs, svSchema(), nil, (&svLog{}).logf); err != nil {
		t.Fatalf("clean specs must pass in error mode: %v", err)
	}
}

func TestSchemaValidationNoDiscoveredSchemaSkipsWithOneNote(t *testing.T) {
	for _, mode := range []string{"warn", "error"} {
		for name, sch := range map[string]map[string][]schema.ColumnInfo{"nil": nil, "empty": {}} {
			var l svLog
			err := validateSpecMocks(svCfg(mode), svBadSpecs(), sch, nil, l.logf)
			if err != nil {
				t.Fatalf("%s/%s: no schema must not be an error, got %v", mode, name, err)
			}
			if len(l.lines) != 1 {
				t.Fatalf("%s/%s: want exactly one note, got %d: %v", mode, name, len(l.lines), l.lines)
			}
			if !strings.Contains(strings.ToLower(l.lines[0]), "skip") {
				t.Errorf("%s/%s: note should say validation was skipped: %q", mode, name, l.lines[0])
			}
		}
	}
}

func TestSchemaValidationMySQLExpectsUntouched(t *testing.T) {
	specs := []types.TestSpec{{FilePath: "my.linespec", Expects: []types.ExpectStatement{
		{Channel: types.ReadMySQL, AccessingTables: []string{"nosuchtable"}, VerifyWhere: map[string]string{"nope": "1"}},
		{Channel: types.WriteMySQL, AccessingTables: []string{"nosuchtable"}, VerifyWrittenValues: map[string]string{"nope": "1"}},
	}}}
	var l svLog
	if err := validateSpecMocks(svCfg("error"), specs, svSchema(), nil, l.logf); err != nil {
		t.Fatalf("MySQL expects must not be validated: %v", err)
	}
	if len(l.lines) != 0 {
		t.Fatalf("no output expected for MySQL-only specs, got %v", l.lines)
	}
}
