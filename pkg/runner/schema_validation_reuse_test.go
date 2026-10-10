package runner

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/config"
	"github.com/livecodelife/linespec/v3/pkg/schema"
	"github.com/livecodelife/linespec/v3/pkg/types"
)

// Specifies prov-2026-e0174ee3. Seams under test (do not exist yet), in
// package runner, on a zero-value-usable TestSuite (no Docker needed; the
// cache and its mutex must not require NewTestSuite):
//
//	// Records the result of the existing fetch for a database host. A nil or
//	// empty schema is cached as "attempted, none". Safe for concurrent use.
//	func (s *TestSuite) cacheDiscoveredSchema(host string, discovered map[string][]schema.ColumnInfo)
//
//	// Validates spec against the cached schema for host, using the suite's
//	// getSchemaDiscoveryConfig() mode. Never fetches. Behaviour:
//	//   - validate off/"": nil, no logf, no load, nothing recorded.
//	//   - nothing cached for host yet (no fetch attempted): nil, silent
//	//     (the spec that populates the cache is validated by the call made
//	//     right after the fetch); the spec is NOT marked validated.
//	//   - cached but empty: the "no schema discovered; skipping" note is
//	//     logged ONCE per suite (not per spec); returns nil.
//	//   - warn: logs this spec's own errors, returns nil.
//	//   - error: returns an error carrying this spec's errors (only this spec).
//	//   - a spec (identified by spec.FilePath) already validated by a prior
//	//     call that had a cache to validate against is never validated again:
//	//     returns nil, no logf, no load.
//	func (s *TestSuite) validateSpecFromCache(spec types.TestSpec, host string,
//		load schema.RowLoader, logf func(format string, args ...interface{})) error

type svrLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *svrLog) logf(format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *svrLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

func (l *svrLog) all() string { return strings.Join(l.snapshot(), "\n") }

func svrSuite(mode string) *TestSuite {
	return &TestSuite{serviceConfigs: map[string]*config.LineSpecConfig{
		"svc": {SchemaDiscovery: &config.SchemaDiscoveryConfig{Mode: "auto", Validate: mode}},
	}}
}

func svrSchema() map[string][]schema.ColumnInfo {
	return map[string][]schema.ColumnInfo{"users": {{Name: "id"}, {Name: "email"}}}
}

func svrBadSpec(file, col, table string) types.TestSpec {
	return types.TestSpec{Name: file, FilePath: file, Expects: []types.ExpectStatement{
		{Channel: types.ReadPostgreSQL, AccessingTables: []string{"users"}, VerifyWhere: map[string]string{col: "1"}},
		{Channel: types.ReadPostgreSQL, AccessingTables: []string{table}},
	}}
}

func svrGoodSpec(file string) types.TestSpec {
	return types.TestSpec{Name: file, FilePath: file, Expects: []types.ExpectStatement{
		{Channel: types.ReadPostgreSQL, AccessingTables: []string{"users"}, VerifyWhere: map[string]string{"id": "1"}},
	}}
}

func svrCountingLoader(calls *int32Counter) schema.RowLoader {
	return func(types.ExpectStatement) ([]map[string]interface{}, error) {
		calls.inc()
		return nil, nil
	}
}

type int32Counter struct {
	mu sync.Mutex
	n  int
}

func (c *int32Counter) inc() { c.mu.Lock(); c.n++; c.mu.Unlock() }
func (c *int32Counter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func TestSchemaValidationReuseLaterSpecsValidatedFromCacheWithoutFetch(t *testing.T) {
	s := svrSuite("error")
	var l svrLog
	// The first spec starts the containers: one fetch fills the cache, then it is validated.
	s.cacheDiscoveredSchema("db1", svrSchema())
	if err := s.validateSpecFromCache(svrGoodSpec("first.linespec"), "db1", nil, l.logf); err != nil {
		t.Fatalf("first (good) spec: %v", err)
	}
	// 2nd and later specs reuse the setup: no further cacheDiscoveredSchema call (no fetch).
	for _, f := range []string{"second.linespec", "third.linespec"} {
		err := s.validateSpecFromCache(svrBadSpec(f, "nosuchcolumn", "userz"), "db1", nil, l.logf)
		if err == nil {
			t.Fatalf("%s: bad spec reusing setup must be validated and fail", f)
		}
		for _, want := range []string{f, "nosuchcolumn", "userz"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error missing %q: %v", f, want, err)
			}
		}
	}
}

func TestSchemaValidationReuseNoCacheYetIsSilentAndDoesNotMarkValidated(t *testing.T) {
	s := svrSuite("error")
	var l svrLog
	calls := &int32Counter{}
	spec := svrBadSpec("first.linespec", "nosuchcolumn", "userz")
	if err := s.validateSpecFromCache(spec, "db1", svrCountingLoader(calls), l.logf); err != nil {
		t.Fatalf("no cache yet: want nil, got %v", err)
	}
	if len(l.snapshot()) != 0 || calls.get() != 0 {
		t.Fatalf("no cache yet: must be silent and not load; log=%v loads=%d", l.snapshot(), calls.get())
	}
	// After the fetch populates the cache, the same spec must still be validated.
	s.cacheDiscoveredSchema("db1", svrSchema())
	if err := s.validateSpecFromCache(spec, "db1", nil, l.logf); err == nil {
		t.Fatal("spec must be validated once the cache exists (it was not validated earlier)")
	}
}

func TestSchemaValidationReuseWarnLogsEachSpecsOwnErrors(t *testing.T) {
	s := svrSuite("warn")
	s.cacheDiscoveredSchema("db1", svrSchema())
	var l1, l2 svrLog
	if err := s.validateSpecFromCache(svrBadSpec("a.linespec", "colA", "tableA"), "db1", nil, l1.logf); err != nil {
		t.Fatalf("warn must not fail: %v", err)
	}
	if err := s.validateSpecFromCache(svrBadSpec("b.linespec", "colB", "tableB"), "db1", nil, l2.logf); err != nil {
		t.Fatalf("warn must not fail: %v", err)
	}
	for _, want := range []string{"a.linespec", "colA", "tableA"} {
		if !strings.Contains(l1.all(), want) {
			t.Errorf("spec a output missing %q:\n%s", want, l1.all())
		}
	}
	for _, want := range []string{"b.linespec", "colB", "tableB"} {
		if !strings.Contains(l2.all(), want) {
			t.Errorf("spec b output missing %q:\n%s", want, l2.all())
		}
	}
	if strings.Contains(l2.all(), "a.linespec") || strings.Contains(l1.all(), "b.linespec") {
		t.Errorf("each spec must log only its own errors:\na=%s\nb=%s", l1.all(), l2.all())
	}
}

func TestSchemaValidationReuseErrorFailsOnlyTheBadSpec(t *testing.T) {
	s := svrSuite("error")
	s.cacheDiscoveredSchema("db1", svrSchema())
	var l svrLog
	if err := s.validateSpecFromCache(svrGoodSpec("good1.linespec"), "db1", nil, l.logf); err != nil {
		t.Fatalf("good1: %v", err)
	}
	err := s.validateSpecFromCache(svrBadSpec("bad.linespec", "nosuchcolumn", "userz"), "db1", nil, l.logf)
	if err == nil {
		t.Fatal("bad spec must fail in error mode")
	}
	if !strings.Contains(err.Error(), "bad.linespec") || strings.Contains(err.Error(), "good1.linespec") {
		t.Errorf("error must name only the bad spec: %v", err)
	}
	// The run continues: a later good spec still passes.
	if err := s.validateSpecFromCache(svrGoodSpec("good2.linespec"), "db1", nil, l.logf); err != nil {
		t.Fatalf("later good spec must pass after a failed one: %v", err)
	}
}

func TestSchemaValidationReuseSkipNoteOncePerRun(t *testing.T) {
	for _, mode := range []string{"warn", "error"} {
		for name, sch := range map[string]map[string][]schema.ColumnInfo{"nil(failed discovery)": nil, "empty": {}} {
			s := svrSuite(mode)
			s.cacheDiscoveredSchema("db1", sch) // "attempted, none"
			var l svrLog
			for i := 0; i < 4; i++ {
				spec := svrBadSpec(fmt.Sprintf("s%d.linespec", i), "nosuchcolumn", "userz")
				if err := s.validateSpecFromCache(spec, "db1", nil, l.logf); err != nil {
					t.Fatalf("%s/%s spec %d: no schema must not be an error: %v", mode, name, i, err)
				}
			}
			lines := l.snapshot()
			if len(lines) != 1 {
				t.Fatalf("%s/%s: want exactly one skip note across 4 specs, got %d: %v", mode, name, len(lines), lines)
			}
			if !strings.Contains(strings.ToLower(lines[0]), "skip") {
				t.Errorf("%s/%s: note should say skipped: %q", mode, name, lines[0])
			}
		}
	}
}

func TestSchemaValidationReuseSpecNeverValidatedTwice(t *testing.T) {
	// Reuse falls back to a fresh start: the spec was validated before the reuse
	// branch, then the post-fetch call runs for the same spec.
	for _, mode := range []string{"warn", "error"} {
		s := svrSuite(mode)
		s.cacheDiscoveredSchema("db1", svrSchema())
		var l svrLog
		calls := &int32Counter{}
		spec := svrBadSpec("dup.linespec", "nosuchcolumn", "userz")
		spec.Expects = append(spec.Expects, types.ExpectStatement{
			Channel: types.ReadPostgreSQL, AccessingTables: []string{"users"}, ReturnsFile: "rows.yaml"})
		_ = s.validateSpecFromCache(spec, "db1", svrCountingLoader(calls), l.logf)
		linesAfterFirst := len(l.snapshot())
		loadsAfterFirst := calls.get()
		if err := s.validateSpecFromCache(spec, "db1", svrCountingLoader(calls), l.logf); err != nil {
			t.Fatalf("%s: second validation of the same spec must be a no-op nil, got %v", mode, err)
		}
		if len(l.snapshot()) != linesAfterFirst || calls.get() != loadsAfterFirst {
			t.Fatalf("%s: spec validated twice (log %d->%d, loads %d->%d)", mode,
				linesAfterFirst, len(l.snapshot()), loadsAfterFirst, calls.get())
		}
	}
}

func TestSchemaValidationReuseOffIsNoOp(t *testing.T) {
	for _, mode := range []string{"off", ""} {
		s := svrSuite(mode)
		var l svrLog
		calls := &int32Counter{}
		s.cacheDiscoveredSchema("db1", svrSchema())
		s.cacheDiscoveredSchema("db2", nil)
		for _, host := range []string{"db1", "db2", "never-fetched"} {
			if err := s.validateSpecFromCache(svrBadSpec("x.linespec", "nosuchcolumn", "userz"), host, svrCountingLoader(calls), l.logf); err != nil {
				t.Fatalf("mode %q host %s: want nil, got %v", mode, host, err)
			}
		}
		if len(l.snapshot()) != 0 || calls.get() != 0 {
			t.Fatalf("mode %q: off must log nothing and never call the loader; log=%v loads=%d", mode, l.snapshot(), calls.get())
		}
	}
}

func TestSchemaValidationReuseDefaultConfigIsOff(t *testing.T) {
	// A suite with no service config at all (default auto mode, no validate) is off.
	s := &TestSuite{}
	var l svrLog
	s.cacheDiscoveredSchema("db1", svrSchema())
	if err := s.validateSpecFromCache(svrBadSpec("x.linespec", "nosuchcolumn", "userz"), "db1", nil, l.logf); err != nil || len(l.snapshot()) != 0 {
		t.Fatalf("default config must be a silent no-op: err=%v log=%v", err, l.snapshot())
	}
}

func TestSchemaValidationReuseDatabasesKeepSeparateCaches(t *testing.T) {
	s := svrSuite("error")
	s.cacheDiscoveredSchema("dbA", map[string][]schema.ColumnInfo{"users": {{Name: "id"}}})
	s.cacheDiscoveredSchema("dbB", map[string][]schema.ColumnInfo{"orders": {{Name: "total"}}})
	var l svrLog

	usersSpec := func(f string) types.TestSpec {
		return types.TestSpec{FilePath: f, Expects: []types.ExpectStatement{
			{Channel: types.ReadPostgreSQL, AccessingTables: []string{"users"}, VerifyWhere: map[string]string{"id": "1"}}}}
	}
	if err := s.validateSpecFromCache(usersSpec("onA.linespec"), "dbA", nil, l.logf); err != nil {
		t.Fatalf("users exists in dbA: %v", err)
	}
	if err := s.validateSpecFromCache(usersSpec("onB.linespec"), "dbB", nil, l.logf); err == nil {
		t.Fatal("users does not exist in dbB: must fail against dbB's own cache")
	}
}

func TestSchemaValidationReuseConcurrentSafe(t *testing.T) {
	s := svrSuite("warn")
	var l svrLog
	calls := &int32Counter{}

	// Baseline: number of messages one bad spec yields.
	var base svrLog
	if err := validateSpecMocks(&config.SchemaDiscoveryConfig{Validate: "warn"},
		[]types.TestSpec{svrBadSpec("same.linespec", "nosuchcolumn", "userz")}, svrSchema(), nil, base.logf); err != nil {
		t.Fatal(err)
	}
	perSpec := len(base.snapshot())
	if perSpec == 0 {
		t.Fatal("test setup: bad spec should yield errors")
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			host := fmt.Sprintf("db%d", i%4)
			s.cacheDiscoveredSchema(host, svrSchema())
			// The same spec from many goroutines must be validated once.
			_ = s.validateSpecFromCache(svrBadSpec("same.linespec", "nosuchcolumn", "userz"), host, svrCountingLoader(calls), l.logf)
			// Distinct specs each validated once.
			_ = s.validateSpecFromCache(svrBadSpec(fmt.Sprintf("own%d.linespec", i), "nosuchcolumn", "userz"), host, nil, l.logf)
		}(i)
	}
	wg.Wait()

	if got, want := len(l.snapshot()), perSpec*(1+32); got != want {
		t.Fatalf("want %d messages (each of 33 specs validated exactly once), got %d", want, got)
	}
}

func TestSchemaValidationReuseConcurrentSkipNoteOnce(t *testing.T) {
	s := svrSuite("warn")
	s.cacheDiscoveredSchema("db1", nil)
	var l svrLog
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = s.validateSpecFromCache(svrBadSpec(fmt.Sprintf("s%d.linespec", i), "c", "t"), "db1", nil, l.logf)
		}(i)
	}
	wg.Wait()
	if n := len(l.snapshot()); n != 1 {
		t.Fatalf("skip note must be logged once across concurrent specs, got %d: %v", n, l.snapshot())
	}
}
