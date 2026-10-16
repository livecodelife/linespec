package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// DESIGN, "Migrating from linespec provenance", step 2: `linespec provenance <cmd>` forwards
// to the `provenance` CLI for every command that CLI implements. Dispatch happens in main(),
// before cobra parses, so flags only the new CLI knows reach it untouched.

// forwardedProvenanceCommands is the static list of `provenance` subcommands that forward:
// the commands registered by the provenance CLI. Anything else under `linespec provenance`
// keeps running in-process until it is ported. TestForwardListMatchesCLI compares this set
// with the CLI's own command list, so a port is a reviewed, one-line change here.
var forwardedProvenanceCommands = map[string]bool{
	"add-scope":      true,
	"check":          true,
	"compile":        true,
	"complete":       true,
	"context":        true,
	"create":         true,
	"deprecate":      true,
	"diff":           true,
	"edit":           true,
	"evidence":       true,
	"govern":         true,
	"graph":          true,
	"history":        true,
	"hooks":          true,
	"init":           true,
	"install-plugin": true,
	"install-skills": true,
	"lint":           true,
	"migrate":        true,
	"next":           true,
	"open":           true,
	"reconcile":      true,
	"repin":          true,
	"revise":         true,
	"run-specs":      true,
	"search":         true,
	"stale":          true,
	"status":         true,
	"sync":           true,
}

const (
	// envProvenanceBin names the binary to forward to, a path or a bare name.
	envProvenanceBin = "PROVENANCE"
	// envNoDeprecationWarning silences the deprecation warning for scripts that cannot change yet.
	envNoDeprecationWarning = "LINESPEC_NO_DEPRECATION_WARNING"

	exitShimError      = 1
	exitBinaryMissing  = 127 // the shell's "command not found"
	exitSignalBase     = 128
	provenanceInstall  = "go install github.com/livecodelife/provenance/cmd/provenance@6f8827d045bab45e5afa3bc2d32f4c98d27b2b81"
	provenanceReleases = "https://github.com/livecodelife/provenance/releases"
)

// forwardEnv is everything the forwarder reads from the process, so tests can supply their own.
type forwardEnv struct {
	Getenv     func(string) string
	LookPath   func(string) (string, error)
	Executable func() (string, error)
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

func defaultForwardEnv() forwardEnv {
	return forwardEnv{
		Getenv:     os.Getenv,
		LookPath:   exec.LookPath,
		Executable: os.Executable,
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
	}
}

// forwardedList returns the forwarded command names, sorted, for help text.
func forwardedList() string {
	names := make([]string, 0, len(forwardedProvenanceCommands))
	for n := range forwardedProvenanceCommands {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// shouldForwardProvenance reports whether args (os.Args[1:], after the -p rewrite) name a
// forwarded subcommand.
func shouldForwardProvenance(args []string) bool {
	return len(args) >= 2 && args[0] == "provenance" && forwardedProvenanceCommands[args[1]]
}

// forwardProvenance runs the provenance binary for args (os.Args[1:]) and returns the exit
// status linespec should exit with. Only call it when shouldForwardProvenance(args) is true.
func forwardProvenance(args []string, e forwardEnv) int {
	cmdName := args[1]
	bin, err := locateProvenance(e)
	switch {
	case errors.Is(err, errProvenanceMissing):
		fmt.Fprint(e.Stderr, missingProvenanceMessage(cmdName))
		return exitBinaryMissing
	case err != nil:
		fmt.Fprintf(e.Stderr, "linespec: %v\n", err)
		return exitShimError
	}

	if e.Getenv(envNoDeprecationWarning) == "" {
		fmt.Fprintf(e.Stderr,
			"linespec: `linespec provenance %[1]s` is deprecated and will be removed after two linespec minor releases; run `provenance %[1]s` instead (set %[2]s=1 to silence this).\n",
			cmdName, envNoDeprecationWarning)
	}

	cmd := exec.Command(bin, args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = e.Stdin, e.Stdout, e.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(e.Stderr, "linespec: cannot run %s: %v\n", bin, err)
		return exitShimError
	}

	// A terminal's Ctrl-C reaches the whole foreground group, child included, so SIGINT is
	// swallowed here rather than allowed to end the shim first. SIGTERM, which targets only
	// this process, is forwarded.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigs:
				if s == syscall.SIGTERM {
					_ = cmd.Process.Signal(s)
				}
			case <-done:
				return
			}
		}
	}()
	err = cmd.Wait()
	close(done)
	return exitStatus(err)
}

// exitStatus maps the child's wait result to linespec's exit status: the child's own code,
// 128+signal when a signal ended it, and 1 for anything unexpected.
func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return exitShimError
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return exitSignalBase + int(ws.Signal())
	}
	if c := ee.ExitCode(); c >= 0 {
		return c
	}
	return exitShimError
}

var errProvenanceMissing = errors.New("provenance binary not found")

// locateProvenance finds the binary to forward to: $PROVENANCE if set (a path, or a bare name
// resolved on PATH), else `provenance` on PATH. A candidate that is the running linespec
// itself counts as missing, so a symlink cannot make the shim exec itself. A set but unusable
// $PROVENANCE is an error naming the variable; it never falls back to PATH.
func locateProvenance(e forwardEnv) (string, error) {
	var path string
	if v := e.Getenv(envProvenanceBin); v != "" {
		p, err := resolveCandidate(v, e)
		if err != nil {
			return "", fmt.Errorf("$%s=%q is not usable: %w", envProvenanceBin, v, err)
		}
		path = p
	} else {
		p, err := e.LookPath("provenance")
		if err != nil {
			return "", errProvenanceMissing
		}
		path = p
	}
	if isSelf(path, e) {
		return "", errProvenanceMissing
	}
	return path, nil
}

func resolveCandidate(v string, e forwardEnv) (string, error) {
	if !strings.ContainsRune(v, filepath.Separator) && !strings.ContainsRune(v, '/') {
		return e.LookPath(v)
	}
	fi, err := os.Stat(v)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		return "", errors.New("is a directory")
	}
	if fi.Mode().Perm()&0o111 == 0 {
		return "", errors.New("not executable")
	}
	return v, nil
}

// isSelf reports whether path is the running linespec, after resolving symlinks.
func isSelf(path string, e forwardEnv) bool {
	self, err := e.Executable()
	if err != nil {
		return false
	}
	a, errA := os.Stat(path)
	b, errB := os.Stat(self)
	return errA == nil && errB == nil && os.SameFile(a, b)
}

func missingProvenanceMessage(cmd string) string {
	return fmt.Sprintf(`linespec: `+"`linespec provenance %[1]s`"+` now runs in the provenance CLI, which was not found.
  Install it:  %[2]s
  or download: %[3]s
  Then run:    provenance %[1]s
  (Set $%[4]s to the binary's path if it is installed somewhere else.)
`, cmd, provenanceInstall, provenanceReleases, envProvenanceBin)
}
