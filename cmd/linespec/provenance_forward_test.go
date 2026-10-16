package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Specs for prov-2026-d3d04540 (linespec provenance forwards to the provenance CLI).
// They run a stub `provenance` shell script, never the real CLI, and touch no git state.

// syncBuffer is a bytes.Buffer that is safe to read while a child process writes to it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type forwardRun struct {
	out, err syncBuffer
	code     int
}

// stubDir writes a `provenance` stub script into a fresh dir and returns the dir.
func stubDir(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "provenance"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// run forwards args with a controlled environment. env maps variable names to values; PATH
// defaults to dir (so the stub is found) unless overridden.
func run(t *testing.T, dir string, env map[string]string, args ...string) *forwardRun {
	t.Helper()
	r := &forwardRun{}
	e := forwardEnv{
		Getenv: func(k string) string {
			if v, ok := env[k]; ok {
				return v
			}
			return ""
		},
		LookPath: func(name string) (string, error) {
			return lookIn(dir, name)
		},
		Executable: func() (string, error) { return filepath.Join(t.TempDir(), "linespec-not-a-file"), nil },
		Stdin:      strings.NewReader(""),
		Stdout:     &r.out,
		Stderr:     &r.err,
	}
	r.code = forwardProvenance(args, e)
	return r
}

// lookIn is exec.LookPath restricted to one directory.
func lookIn(dir, name string) (string, error) {
	if dir == "" {
		return "", exec.ErrNotFound
	}
	t := filepath.Join(dir, name)
	if fi, err := os.Stat(t); err != nil || fi.IsDir() || fi.Mode().Perm()&0o111 == 0 {
		return "", exec.ErrNotFound
	}
	return t, nil
}

func TestForwardListMatchesCLI(t *testing.T) {
	bin, err := exec.LookPath("provenance")
	if v := os.Getenv(envProvenanceBin); v != "" {
		bin, err = v, nil
	}
	if err != nil {
		t.Skip("no provenance CLI on PATH or $PROVENANCE; cannot compare the forward list with its commands")
	}
	out, err := exec.Command(bin, "--help").Output()
	if err != nil {
		t.Skipf("%s --help failed: %v", bin, err)
	}
	cli := map[string]bool{}
	inCommands := false
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "Available Commands:"):
			inCommands = true
		case inCommands && strings.TrimSpace(line) == "":
			inCommands = false
		case inCommands:
			if f := strings.Fields(line); len(f) > 0 && f[0] != "help" && f[0] != "completion" {
				cli[f[0]] = true
			}
		}
	}
	if len(cli) == 0 {
		t.Fatalf("parsed no commands from %s --help:\n%s", bin, out)
	}
	var missing, extra []string
	for c := range forwardedProvenanceCommands {
		if !cli[c] {
			missing = append(missing, c)
		}
	}
	for c := range cli {
		if !forwardedProvenanceCommands[c] {
			extra = append(extra, c)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("forwarded but not in the provenance CLI: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("in the provenance CLI but not forwarded: %v (add them to forwardedProvenanceCommands, which also stops the in-process copy)", extra)
	}
}

func TestShouldForwardOnlyListedCommands(t *testing.T) {
	yes := [][]string{{"provenance", "lint"}, {"provenance", "migrate", "--plan"}, {"provenance", "run-specs", "-c", "x"}, {"provenance", "next"}, {"provenance", "govern", "--json"}}
	for _, a := range yes {
		if !shouldForwardProvenance(a) {
			t.Errorf("%v should forward", a)
		}
	}
	no := [][]string{{}, {"provenance"}, {"provenance", "audit"}, {"provenance", "index", "--dry-run"}, {"provenance", "install-hooks"},
		{"lint"}, {"test", "provenance"}, {"provenance", "--help"}, {"provenance", "help"}}
	for _, a := range no {
		if shouldForwardProvenance(a) {
			t.Errorf("%v must not forward", a)
		}
	}
}

func TestForwardArgsVerbatim(t *testing.T) {
	dir := stubDir(t, `for a in "$@"; do printf '[%s]' "$a"; done`)
	r := run(t, dir, nil, "provenance", "lint", "-c", "my config.yml", "--record", "prov-2026-x", "--format=sarif", "", "--", "-x")
	want := "[lint][-c][my config.yml][--record][prov-2026-x][--format=sarif][][--][-x]"
	if r.out.String() != want {
		t.Errorf("stdout = %q, want %q", r.out.String(), want)
	}
}

func TestForwardStdoutUntouched(t *testing.T) {
	dir := stubDir(t, "printf '{\"a\":1}\\n'; printf 'child-stderr\\n' >&2")
	r := run(t, dir, nil, "provenance", "status", "--json")
	if r.out.String() != "{\"a\":1}\n" {
		t.Errorf("stdout = %q: the shim must add nothing to it", r.out.String())
	}
	if !strings.Contains(r.err.String(), "child-stderr") {
		t.Errorf("child stderr not passed through: %q", r.err.String())
	}
}

func TestForwardExitCodePassesThrough(t *testing.T) {
	for _, code := range []string{"0", "1", "2", "42"} {
		dir := stubDir(t, "exit "+code)
		r := run(t, dir, nil, "provenance", "check")
		if want := map[string]int{"0": 0, "1": 1, "2": 2, "42": 42}[code]; r.code != want {
			t.Errorf("exit %s: got %d", code, r.code)
		}
	}
}

func TestForwardSignalExitIs128Plus(t *testing.T) {
	dir := stubDir(t, "kill -TERM $$")
	r := run(t, dir, nil, "provenance", "check")
	if want := 128 + int(syscall.SIGTERM); r.code != want {
		t.Errorf("child killed by SIGTERM: exit %d, want %d", r.code, want)
	}
}

func TestForwardSigtermReachesChild(t *testing.T) {
	// The child announces itself, then waits; the shim must forward SIGTERM and wait for it.
	dir := stubDir(t, "trap 'echo got-term; exit 9' TERM\necho ready\nwhile :; do sleep 0.05; done")
	r := &forwardRun{}
	e := forwardEnv{
		Getenv:     func(string) string { return "" },
		LookPath:   func(n string) (string, error) { return lookIn(dir, n) },
		Executable: func() (string, error) { return "/nonexistent/linespec", nil },
		Stdin:      strings.NewReader(""), Stdout: &r.out, Stderr: &r.err,
	}
	done := make(chan int, 1)
	go func() { done <- forwardProvenance([]string{"provenance", "lint"}, e) }()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(r.out.String(), "ready") {
		if time.Now().After(deadline) {
			t.Fatal("child never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 9 {
			t.Errorf("exit %d, want the child's 9", code)
		}
		if !strings.Contains(r.out.String(), "got-term") {
			t.Errorf("child did not get SIGTERM: %q", r.out.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shim did not exit after SIGTERM")
	}
}

func TestForwardMissingBinaryExits127(t *testing.T) {
	r := run(t, "", nil, "provenance", "lint")
	if r.code != 127 {
		t.Errorf("exit %d, want 127", r.code)
	}
	for _, want := range []string{"provenance lint", "go install github.com/livecodelife/provenance/cmd/provenance", "releases", "$PROVENANCE"} {
		if !strings.Contains(r.err.String(), want) {
			t.Errorf("install message lacks %q:\n%s", want, r.err.String())
		}
	}
	if r.out.String() != "" {
		t.Errorf("stdout must stay empty, got %q", r.out.String())
	}
}

func TestForwardUnusableEnvIsAnError(t *testing.T) {
	good := stubDir(t, "echo from-path")
	for name, val := range map[string]string{"missing path": "/nonexistent/provenance", "bare unknown name": "no-such-binary-xyz", "a directory": t.TempDir()} {
		r := run(t, good, map[string]string{"PROVENANCE": val}, "provenance", "lint")
		if r.code != 1 {
			t.Errorf("%s: exit %d, want 1", name, r.code)
		}
		if !strings.Contains(r.err.String(), "$PROVENANCE") {
			t.Errorf("%s: error must name the variable: %q", name, r.err.String())
		}
		if strings.Contains(r.out.String(), "from-path") {
			t.Errorf("%s: fell back to PATH", name)
		}
	}
	notExec := filepath.Join(t.TempDir(), "provenance")
	if err := os.WriteFile(notExec, []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run(t, good, map[string]string{"PROVENANCE": notExec}, "provenance", "lint"); r.code != 1 || !strings.Contains(r.err.String(), "not executable") {
		t.Errorf("non-executable: exit %d, err %q", r.code, r.err.String())
	}
}

func TestForwardEnvOverridesPath(t *testing.T) {
	onPath := stubDir(t, "echo from-path")
	other := stubDir(t, "echo from-env")
	r := run(t, onPath, map[string]string{"PROVENANCE": filepath.Join(other, "provenance")}, "provenance", "lint")
	if r.code != 0 || strings.TrimSpace(r.out.String()) != "from-env" {
		t.Errorf("exit %d stdout %q: $PROVENANCE must win over PATH", r.code, r.out.String())
	}
}

func TestForwardSelfCountsAsMissing(t *testing.T) {
	dir := stubDir(t, "echo should-not-run")
	self := filepath.Join(dir, "provenance")
	link := filepath.Join(t.TempDir(), "linespec-link")
	if err := os.Symlink(self, link); err != nil {
		t.Fatal(err)
	}
	// "the running linespec" is the stub itself, reached through a symlink
	r := &forwardRun{}
	e := forwardEnv{
		Getenv:     func(string) string { return "" },
		LookPath:   func(n string) (string, error) { return lookIn(dir, n) },
		Executable: func() (string, error) { return link, nil },
		Stdin:      strings.NewReader(""), Stdout: &r.out, Stderr: &r.err,
	}
	r.code = forwardProvenance([]string{"provenance", "lint"}, e)
	if r.code != 127 || strings.Contains(r.out.String(), "should-not-run") {
		t.Errorf("exit %d stdout %q: a candidate that is the running binary must count as missing", r.code, r.out.String())
	}
}

func TestForwardWarningOncePerRunOnStderr(t *testing.T) {
	dir := stubDir(t, "echo out")
	r := run(t, dir, nil, "provenance", "lint")
	if n := strings.Count(r.err.String(), "deprecated"); n != 1 {
		t.Errorf("warning printed %d times: %q", n, r.err.String())
	}
	for _, want := range []string{"`linespec provenance lint`", "two linespec minor releases", "`provenance lint`"} {
		if !strings.Contains(r.err.String(), want) {
			t.Errorf("warning lacks %q: %q", want, r.err.String())
		}
	}
	if strings.Contains(r.out.String(), "deprecated") {
		t.Errorf("warning leaked to stdout: %q", r.out.String())
	}
}

func TestForwardOptOutSilencesWarning(t *testing.T) {
	dir := stubDir(t, "echo out")
	r := run(t, dir, map[string]string{"LINESPEC_NO_DEPRECATION_WARNING": "1"}, "provenance", "lint")
	if r.err.String() != "" {
		t.Errorf("stderr = %q, want empty", r.err.String())
	}
	if strings.TrimSpace(r.out.String()) != "out" {
		t.Errorf("stdout = %q", r.out.String())
	}
}

// TestNotForwardedRunsInProcess builds nothing: it checks the dispatch decision, which is all
// main() uses. A command outside the list reaches cobra exactly as before.
func TestNotForwardedRunsInProcess(t *testing.T) {
	for _, c := range []string{"audit", "index", "publish", "install-hooks", "discover", "generate", "lock-layer", "lock-scope"} {
		if shouldForwardProvenance([]string{"provenance", c}) {
			t.Errorf("%s must keep running in-process", c)
		}
	}
}
