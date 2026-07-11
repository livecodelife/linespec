package plugin

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hooks_test.go verifies two things for the fswrite blueprint (prov-2026-8d2f5f2a):
//   - the embedded plugin tree (Files) actually ships hooks/session-start.sh as
//     a thin renderer of `linespec provenance next --json`, so what gets
//     installed matches source;
//   - the session-start.sh hook, run as a real subprocess, faithfully surfaces
//     whatever `next --json` reports. Enforcement must not depend on this hook
//     being installed at all — reconcile runs unconditionally inside `next`
//     itself (see pkg/provenance/commands.go's Next), not gated behind
//     anything this script sets. A stub `linespec` on PATH always answers with
//     a reason, so these tests exercise relaying, not gating.

// --- embedded plugin tree ------------------------------------------------------

func TestFiles_EmbedsSessionStartNextWiring(t *testing.T) {
	data, err := Files.ReadFile("hooks/session-start.sh")
	if err != nil {
		t.Fatalf("ReadFile hooks/session-start.sh: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "linespec provenance next --json") {
		t.Errorf("embedded session-start.sh should call `linespec provenance next --json`, got:\n%s", content)
	}
	if strings.Contains(content, "LINESPEC_PROVENANCE_RECONCILE") {
		t.Errorf("embedded session-start.sh must not depend on an env var to trigger reconcile — reconcile runs "+
			"unconditionally inside `next` itself, since a user may not have this hook installed at all, got:\n%s", content)
	}
}

func TestFiles_EmbedsHooksJSONWiringSessionStart(t *testing.T) {
	data, err := Files.ReadFile("hooks/hooks.json")
	if err != nil {
		t.Fatalf("ReadFile hooks/hooks.json: %v", err)
	}
	var manifest struct {
		Hooks struct {
			SessionStart []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"SessionStart"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("Unmarshal hooks.json: %v", err)
	}
	if len(manifest.Hooks.SessionStart) == 0 || len(manifest.Hooks.SessionStart[0].Hooks) == 0 {
		t.Fatalf("hooks.json should declare a SessionStart hook, got %+v", manifest)
	}
	cmd := manifest.Hooks.SessionStart[0].Hooks[0].Command
	if !strings.Contains(cmd, "session-start.sh") {
		t.Errorf("SessionStart hook command = %q, want it to invoke session-start.sh", cmd)
	}
}

// --- session-start.sh subprocess behavior --------------------------------------

// linespecStub is a shell script that answers `linespec provenance next
// --json` unconditionally — session-start.sh no longer needs to set any env
// var for a real `next` call to reconcile (that now happens inside `next`
// itself), so this stub only needs to prove the hook relays the command's
// output faithfully.
const linespecStub = `#!/usr/bin/env bash
if [ "$1" = "provenance" ] && [ "$2" = "next" ] && [ "$3" = "--json" ]; then
  echo '{"primary":{"reason":"stub next reason","command":"linespec provenance open --record prov-2026-stub"}}'
  exit 0
fi
exit 1
`

// runSessionStart runs the real hooks/session-start.sh as a subprocess with cwd
// as the hook's reported .cwd, a stub `linespec` on PATH (jq is the real one —
// it must already be on the ambient PATH), and returns stdout.
func runSessionStart(t *testing.T, cwd string) string {
	t.Helper()
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "linespec"), []byte(linespecStub), 0o755); err != nil {
		t.Fatalf("write linespec stub: %v", err)
	}

	scriptPath, err := filepath.Abs("hooks/session-start.sh")
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}

	cmd := exec.Command("bash", scriptPath)
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdin = strings.NewReader(`{"cwd": "` + cwd + `"}`)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("session-start.sh failed: %v, stderr: %s", err, exitErr.Stderr)
		}
		t.Fatalf("session-start.sh failed: %v", err)
	}
	return string(out)
}

func TestSessionStartHook_SurfacesNextReason(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".linespec.yml"), []byte("service:\n  name: test\n"), 0o644); err != nil {
		t.Fatalf("write .linespec.yml: %v", err)
	}

	out := runSessionStart(t, repo)
	if !strings.Contains(out, "stub next reason") {
		t.Errorf("session-start.sh should surface the stub's `next` reason, got: %q", out)
	}

	var payload struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("session-start.sh output is not valid JSON: %v\noutput: %s", err, out)
	}
	if !strings.Contains(payload.HookSpecificOutput.AdditionalContext, "stub next reason") {
		t.Errorf("additionalContext = %q, want it to include the stub's `next` reason", payload.HookSpecificOutput.AdditionalContext)
	}
}

func TestSessionStartHook_SilentOutsideProvenanceRepo(t *testing.T) {
	// No .linespec.yml here — the hook must degrade silently (prov-2026-8d2f5f2a
	// doesn't change this contract, only what happens once inside a real repo).
	repo := t.TempDir()
	out := runSessionStart(t, repo)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no output outside a provenance repo, got: %q", out)
	}
}
