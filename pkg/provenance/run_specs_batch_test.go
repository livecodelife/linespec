package provenance

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newFakeRunner writes a shell script to tmpDir that logs each invocation as one
// line to logPath (its first argument), then exits non-zero if any remaining
// argument contains the substring "bad". It stands in for a real test runner
// (rspec/pytest/jest/bundle) so these tests never need those toolchains or Docker
// installed — only `sh`, which every environment running `go test` already has.
func newFakeRunner(t *testing.T) (scriptPath, logPath string) {
	t.Helper()
	dir := t.TempDir()
	scriptPath = filepath.Join(dir, "runner.sh")
	logPath = filepath.Join(dir, "invocations.log")
	script := `#!/bin/sh
LOG="$1"
shift
echo "RUN $*" >> "$LOG"
for a in "$@"; do
  case "$a" in
    *bad*) exit 1 ;;
  esac
done
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write fake runner: %v", err)
	}
	return scriptPath, logPath
}

// readLogLines reads path and returns its non-empty lines, or nil if the file
// does not exist (no invocation happened).
func readLogLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("failed to read log %s: %v", path, err)
	}
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// captureStdout redirects os.Stdout for the duration of fn and returns what was
// written, since runRecordSpecs streams its ✓/✗ reporting directly to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = w
	fn()
	if cerr := w.Close(); cerr != nil {
		t.Fatalf("failed to close pipe writer: %v", cerr)
	}
	os.Stdout = orig

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("failed to read captured stdout: %v", err)
	}
	return buf.String()
}

// TestRunRecordSpecs_BatchesConsecutiveSameRunnerEntries is the primary proof for
// prov-2026-3ee1f3c3: three consecutive associated_specs entries that share an
// effective run_command must execute as ONE process with all three paths appended,
// not three separate processes.
func TestRunRecordSpecs_BatchesConsecutiveSameRunnerEntries(t *testing.T) {
	script, log := newFakeRunner(t)
	runCmd := "sh " + script + " " + log

	record := &Record{
		ID: "prov-2026-a1b2c3d4",
		AssociatedSpecs: []AssociatedSpec{
			{Path: "spec/one_spec.rb", RunCommand: runCmd},
			{Path: "spec/two_spec.rb", RunCommand: runCmd},
			{Path: "spec/three_spec.rb", RunCommand: runCmd},
		},
	}

	cmds := &Commands{}
	var ran, failed bool
	captureStdout(t, func() {
		ran, failed = cmds.runRecordSpecs(record, nil)
	})

	if !ran {
		t.Error("expected ran=true")
	}
	if failed {
		t.Error("expected failed=false")
	}

	lines := readLogLines(t, log)
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 batched invocation, got %d: %v", len(lines), lines)
	}
	for _, path := range []string{"spec/one_spec.rb", "spec/two_spec.rb", "spec/three_spec.rb"} {
		if !strings.Contains(lines[0], path) {
			t.Errorf("batched invocation %q does not contain %q", lines[0], path)
		}
	}
}

// TestRunRecordSpecs_DoesNotMergeAcrossDifferentCommand verifies that a
// same-command entry separated from another same-command entry by a
// different-command entry is NOT pulled into that group — no reordering, and the
// two same-command entries still run as two separate invocations, in order.
func TestRunRecordSpecs_DoesNotMergeAcrossDifferentCommand(t *testing.T) {
	scriptX, logX := newFakeRunner(t)
	scriptY, logY := newFakeRunner(t)
	cmdX := "sh " + scriptX + " " + logX
	cmdY := "sh " + scriptY + " " + logY

	record := &Record{
		ID: "prov-2026-b2c3d4e5",
		AssociatedSpecs: []AssociatedSpec{
			{Path: "a.py", RunCommand: cmdX},
			{Path: "b.py", RunCommand: cmdY},
			{Path: "c.py", RunCommand: cmdX},
		},
	}

	cmds := &Commands{}
	captureStdout(t, func() {
		cmds.runRecordSpecs(record, nil)
	})

	linesX := readLogLines(t, logX)
	if len(linesX) != 2 {
		t.Fatalf("expected 2 separate invocations for the interrupted command, got %d: %v", len(linesX), linesX)
	}
	if !strings.Contains(linesX[0], "a.py") || strings.Contains(linesX[0], "c.py") {
		t.Errorf("first X invocation should contain only a.py, got %q", linesX[0])
	}
	if !strings.Contains(linesX[1], "c.py") || strings.Contains(linesX[1], "a.py") {
		t.Errorf("second X invocation should contain only c.py, got %q", linesX[1])
	}

	linesY := readLogLines(t, logY)
	if len(linesY) != 1 {
		t.Fatalf("expected 1 invocation for the interrupting command, got %d: %v", len(linesY), linesY)
	}
	if !strings.Contains(linesY[0], "b.py") {
		t.Errorf("Y invocation should contain b.py, got %q", linesY[0])
	}
}

// TestRunRecordSpecs_TemplatedRunCommandNeverMerges verifies that entries whose
// run_command contains a literal {{path}} placeholder always execute standalone,
// even when two such entries have identical un-substituted run_command text.
func TestRunRecordSpecs_TemplatedRunCommandNeverMerges(t *testing.T) {
	script, log := newFakeRunner(t)
	runCmd := "sh " + script + " " + log + " {{path}}"

	record := &Record{
		ID: "prov-2026-c3d4e5f6",
		AssociatedSpecs: []AssociatedSpec{
			{Path: "one.py", RunCommand: runCmd},
			{Path: "two.py", RunCommand: runCmd},
		},
	}

	cmds := &Commands{}
	captureStdout(t, func() {
		cmds.runRecordSpecs(record, nil)
	})

	lines := readLogLines(t, log)
	if len(lines) != 2 {
		t.Fatalf("expected 2 standalone invocations for templated entries, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "one.py") || strings.Contains(lines[0], "two.py") {
		t.Errorf("first invocation should contain only one.py, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "two.py") || strings.Contains(lines[1], "one.py") {
		t.Errorf("second invocation should contain only two.py, got %q", lines[1])
	}
}

// TestRunRecordSpecs_SingleSpecRecordUnchanged verifies a record with a single spec
// still produces exactly the same command buildSpecCommand would (no batching
// machinery changes the single-entry path).
func TestRunRecordSpecs_SingleSpecRecordUnchanged(t *testing.T) {
	script, log := newFakeRunner(t)
	runCmd := "sh " + script + " " + log

	spec := AssociatedSpec{Path: "only_spec.rb", RunCommand: runCmd}
	record := &Record{
		ID:              "prov-2026-d4e5f6a7",
		AssociatedSpecs: []AssociatedSpec{spec},
	}

	wantCmd, wantSkip, wantErr := buildSpecCommand(spec)
	if wantErr != nil || wantSkip {
		t.Fatalf("unexpected buildSpecCommand result: skip=%v err=%v", wantSkip, wantErr)
	}

	cmds := &Commands{}
	stdout := captureStdout(t, func() {
		cmds.runRecordSpecs(record, nil)
	})

	if !strings.Contains(stdout, wantCmd) {
		t.Errorf("stdout %q does not contain expected unbatched command %q", stdout, wantCmd)
	}

	lines := readLogLines(t, log)
	if len(lines) != 1 || !strings.Contains(lines[0], "only_spec.rb") {
		t.Fatalf("expected 1 invocation with only_spec.rb, got %v", lines)
	}
}

// TestRunRecordSpecs_BatchFailureLocalizesPerPath verifies that when a batched
// invocation fails, runRecordSpecs re-runs each path individually to attribute the
// failure to the specific path(s) that actually failed, rather than reporting every
// path in the batch as failed.
func TestRunRecordSpecs_BatchFailureLocalizesPerPath(t *testing.T) {
	script, log := newFakeRunner(t)
	runCmd := "sh " + script + " " + log

	record := &Record{
		ID: "prov-2026-e5f6a7b8",
		AssociatedSpecs: []AssociatedSpec{
			{Path: "good.py", RunCommand: runCmd},
			{Path: "bad.py", RunCommand: runCmd},
		},
	}

	cmds := &Commands{}
	var failed bool
	stdout := captureStdout(t, func() {
		_, failed = cmds.runRecordSpecs(record, nil)
	})

	if !failed {
		t.Error("expected failed=true when a path in the batch fails")
	}

	if !strings.Contains(stdout, "✓ good.py passed") {
		t.Errorf("expected good.py to be reported as passed, got stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, "✗ bad.py failed") {
		t.Errorf("expected bad.py to be reported as failed, got stdout:\n%s", stdout)
	}

	// One batched invocation (both paths, fails) + two localization re-runs (one
	// per path) = 3 total invocations logged.
	lines := readLogLines(t, log)
	if len(lines) != 3 {
		t.Fatalf("expected 3 invocations (1 batch + 2 localization re-runs), got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "good.py") || !strings.Contains(lines[0], "bad.py") {
		t.Errorf("first (batched) invocation should contain both paths, got %q", lines[0])
	}
}

// newFakeRunnerFailsOnlyWhenBatched writes a shell script that exits non-zero
// only when invoked with 2+ paths at once, and exits 0 when invoked with a
// single path — modeling an order-dependent or shared-state failure that only
// reproduces when specs run together (rspec/pytest/jest all have this failure
// mode), so it never shows up when a path is re-run standalone.
func newFakeRunnerFailsOnlyWhenBatched(t *testing.T) (scriptPath, logPath string) {
	t.Helper()
	dir := t.TempDir()
	scriptPath = filepath.Join(dir, "runner.sh")
	logPath = filepath.Join(dir, "invocations.log")
	script := `#!/bin/sh
LOG="$1"
shift
echo "RUN $*" >> "$LOG"
if [ "$#" -ge 2 ]; then
  exit 1
fi
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write fake runner: %v", err)
	}
	return scriptPath, logPath
}

// TestRunRecordSpecs_BatchOnlyFailureIsNotMasked verifies the blueprint constraint
// that a failure of the batched invocation itself must still mark the record
// failed=true, even when every path happens to pass on its own during the
// per-path localization re-run — e.g. an order-dependent or cross-file
// shared-state failure that only reproduces when paths run together.
func TestRunRecordSpecs_BatchOnlyFailureIsNotMasked(t *testing.T) {
	script, log := newFakeRunnerFailsOnlyWhenBatched(t)
	runCmd := "sh " + script + " " + log

	record := &Record{
		ID: "prov-2026-b1a7c4e2",
		AssociatedSpecs: []AssociatedSpec{
			{Path: "one.py", RunCommand: runCmd},
			{Path: "two.py", RunCommand: runCmd},
		},
	}

	cmds := &Commands{}
	var ran, failed bool
	stdout := captureStdout(t, func() {
		ran, failed = cmds.runRecordSpecs(record, nil)
	})

	if !ran {
		t.Error("expected ran=true")
	}
	if !failed {
		t.Error("expected failed=true when the batch itself fails, even though every path passes standalone during localization")
	}
	if !strings.Contains(stdout, "✓ one.py passed") || !strings.Contains(stdout, "✓ two.py passed") {
		t.Errorf("expected both paths to localize as passed, got stdout:\n%s", stdout)
	}
}

// TestRunRecordSpecs_SeenDedupKeyIndependentOfBatching verifies the tricky
// completion-time overlap-teeth case: a path already verified as part of a batch in
// one record's run must still be recognized as already-verified in a later record's
// run, even though that later run never batches it with the same neighbor (so its
// batched command string differs from how it ran the first time).
func TestRunRecordSpecs_SeenDedupKeyIndependentOfBatching(t *testing.T) {
	script, log := newFakeRunner(t)
	runCmd := "sh " + script + " " + log

	recordA := &Record{
		ID: "prov-2026-f6a7b8c9",
		AssociatedSpecs: []AssociatedSpec{
			{Path: "shared.py", RunCommand: runCmd},
			{Path: "only_in_a.py", RunCommand: runCmd},
		},
	}
	recordB := &Record{
		ID: "prov-2026-a7b8c9d0",
		AssociatedSpecs: []AssociatedSpec{
			{Path: "shared.py", RunCommand: runCmd},
		},
	}

	cmds := &Commands{}
	seen := map[string]bool{}

	captureStdout(t, func() {
		ran, failed := cmds.runRecordSpecs(recordA, seen)
		if !ran || failed {
			t.Errorf("record A: ran=%v failed=%v, want ran=true failed=false", ran, failed)
		}
	})

	lines := readLogLines(t, log)
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 batched invocation after record A, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "shared.py") || !strings.Contains(lines[0], "only_in_a.py") {
		t.Fatalf("record A's batch should contain both paths, got %q", lines[0])
	}

	stdoutB := captureStdout(t, func() {
		ran, failed := cmds.runRecordSpecs(recordB, seen)
		if !ran {
			t.Error("record B: expected ran=true even though its only spec was already verified")
		}
		if failed {
			t.Error("record B: expected failed=false")
		}
	})

	if !strings.Contains(stdoutB, "already verified") {
		t.Errorf("expected record B to report shared.py as already verified, got stdout:\n%s", stdoutB)
	}

	// No new invocation should have been logged for record B — shared.py's
	// identity as "already verified" must not depend on which neighbor it batched
	// with when it actually ran.
	linesAfterB := readLogLines(t, log)
	if len(linesAfterB) != 1 {
		t.Fatalf("expected still exactly 1 invocation after record B (no re-run), got %d: %v", len(linesAfterB), linesAfterB)
	}
}
