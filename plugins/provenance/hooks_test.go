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
//   - the embedded plugin tree (Files) actually ships the reconcile wiring in
//     hooks/session-start.sh, so what gets installed matches source;
//   - the session-start.sh hook, run as a real subprocess, sets
//     LINESPEC_PROVENANCE_RECONCILE=1 around its `linespec provenance next`
//     call — the one and only trigger point for "reconcile MUST run at agent
//     session start" (constraint 5). A stub `linespec` on PATH only produces a
//     usable reason when that env var is present, so a regression that drops
//     the env var manifests as the hook going silent, not a subtler diff.

// --- embedded plugin tree ------------------------------------------------------

func TestFiles_EmbedsSessionStartReconcileWiring(t *testing.T) {
	data, err := Files.ReadFile("hooks/session-start.sh")
	if err != nil {
		t.Fatalf("ReadFile hooks/session-start.sh: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "LINESPEC_PROVENANCE_RECONCILE") {
		t.Errorf("embedded session-start.sh should set LINESPEC_PROVENANCE_RECONCILE around the `next` call, got:\n%s", content)
	}
	if !strings.Contains(content, "linespec provenance next --json") {
		t.Errorf("embedded session-start.sh should still call `linespec provenance next --json`, got:\n%s", content)
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

// linespecStub is a shell script that only answers `linespec provenance next
// --json` — and only with a usable reason when LINESPEC_PROVENANCE_RECONCILE is
// set — so its output is a direct signal of whether session-start.sh propagated
// that env var, without needing to fake a real provenance repo.
const linespecStub = `#!/usr/bin/env bash
if [ "$1" = "provenance" ] && [ "$2" = "next" ] && [ "$3" = "--json" ]; then
  if [ -n "$LINESPEC_PROVENANCE_RECONCILE" ]; then
    echo '{"primary":{"reason":"reconcile ran","command":"linespec provenance open --record prov-2026-stub"}}'
  else
    echo '{}'
  fi
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

func TestSessionStartHook_PropagatesReconcileEnvVarToNext(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".linespec.yml"), []byte("service:\n  name: test\n"), 0o644); err != nil {
		t.Fatalf("write .linespec.yml: %v", err)
	}

	out := runSessionStart(t, repo)
	if !strings.Contains(out, "reconcile ran") {
		t.Errorf("session-start.sh should surface the stub's reconcile-gated reason, got: %q", out)
	}

	var payload struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("session-start.sh output is not valid JSON: %v\noutput: %s", err, out)
	}
	if !strings.Contains(payload.HookSpecificOutput.AdditionalContext, "reconcile ran") {
		t.Errorf("additionalContext = %q, want it to include the stub's reconcile-gated reason", payload.HookSpecificOutput.AdditionalContext)
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
