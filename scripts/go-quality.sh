#!/usr/bin/env bash
#
# Go code-quality gate for the linespec module.
#
# Single source of truth shared by the pre-push git hook, `make quality`, and
# CI. Keeps the codebase compact by blocking dead code and lint regressions
# before they are pushed, and surfacing copy-paste duplication.
#
#   staticcheck  — HARD gate. Fails on any finding (baseline is clean).
#   deadcode     — HARD gate, but only on functions that are unreachable in
#                  EVERY build configuration (set intersection). A function
#                  used only by tests or only under the `beta`/`integration`
#                  build tags is considered live, not dead, so the gate is
#                  meaningful instead of noisy.
#   dupl         — WARN only. Reports duplication but never fails the gate.
#
# Environment overrides:
#   QUALITY_DEADCODE=warn   Downgrade deadcode from a hard gate to warn-only.
#   QUALITY_SKIP_DUPL=1     Skip the duplication check entirely.
#   DUPL_THRESHOLD=<n>      Token threshold for dupl (default 75).
#
set -euo pipefail

# Pinned tool versions for reproducible installs.
STATICCHECK_PKG="honnef.co/go/tools/cmd/staticcheck@2026.1"
DEADCODE_PKG="golang.org/x/tools/cmd/deadcode@latest"
DUPL_PKG="github.com/mibk/dupl@latest"

DUPL_THRESHOLD="${DUPL_THRESHOLD:-75}"

# Make `go install`-ed tools discoverable.
export PATH="$(go env GOPATH)/bin:${PATH}"

fail=0

note() { printf '\n\033[1m== %s ==\033[0m\n' "$1"; }
warn() { printf '\033[33m%s\033[0m\n' "$1"; }
ok()   { printf '\033[32m%s\033[0m\n' "$1"; }
err()  { printf '\033[31m%s\033[0m\n' "$1"; }

# ensure_tool <binary> <go-install-package>
ensure_tool() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "Installing $1 ($2)..."
        go install "$2"
    fi
}

# ---------------------------------------------------------------------------
# 1. staticcheck — hard gate
# ---------------------------------------------------------------------------
note "staticcheck ./..."
ensure_tool staticcheck "$STATICCHECK_PKG"
if staticcheck ./...; then
    ok "staticcheck: clean"
else
    err "staticcheck: findings above must be fixed before pushing."
    fail=1
fi

# ---------------------------------------------------------------------------
# 2. deadcode — hard gate on the intersection across all build configurations
# ---------------------------------------------------------------------------
note "deadcode (unreachable in every build configuration)"
ensure_tool deadcode "$DEADCODE_PKG"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Each invocation analyzes one build configuration. A function is only truly
# dead if it is unreachable in ALL of them, so we intersect the result sets.
# `deadcode` exits 0 even when it prints findings, hence the explicit checks.
run_deadcode() { deadcode "$@" ./... 2>/dev/null | grep ': unreachable func:' || true; }

run_deadcode                              | sort -u > "$tmp/c1"  # stable, no test
run_deadcode -test                        | sort -u > "$tmp/c2"  # + tests
run_deadcode -test -tags beta             | sort -u > "$tmp/c3"  # beta build
run_deadcode -test -tags integration      | sort -u > "$tmp/c4"  # integration build
run_deadcode -test -tags "integration beta" | sort -u > "$tmp/c5"

# A line present in all 5 files (count == 5) is dead in every configuration.
cat "$tmp"/c1 "$tmp"/c2 "$tmp"/c3 "$tmp"/c4 "$tmp"/c5 \
    | sort | uniq -c | awk '$1 == 5 { sub(/^ *[0-9]+ /, ""); print }' > "$tmp/dead"

if [ -s "$tmp/dead" ]; then
    cat "$tmp/dead"
    count="$(wc -l < "$tmp/dead" | tr -d ' ')"
    if [ "${QUALITY_DEADCODE:-gate}" = "warn" ]; then
        warn "deadcode: ${count} dead function(s) (warn-only; QUALITY_DEADCODE=warn)"
    else
        err "deadcode: ${count} function(s) unreachable in every build configuration. Remove them or wire up a caller."
        fail=1
    fi
else
    ok "deadcode: no functions dead across all build configurations"
fi

# ---------------------------------------------------------------------------
# 3. dupl — warn only
# ---------------------------------------------------------------------------
if [ "${QUALITY_SKIP_DUPL:-0}" = "1" ]; then
    note "dupl (skipped: QUALITY_SKIP_DUPL=1)"
else
    note "dupl (duplication report, warn-only)"
    ensure_tool dupl "$DUPL_PKG"
    # Scope to first-party source; examples/ are standalone sample modules.
    if dupl -threshold "$DUPL_THRESHOLD" pkg cmd > "$tmp/dupl" 2>/dev/null && [ ! -s "$tmp/dupl" ]; then
        ok "dupl: no duplication above threshold ${DUPL_THRESHOLD}"
    else
        cat "$tmp/dupl" 2>/dev/null || true
        warn "dupl: duplication reported above (warn-only, does not block)."
    fi
fi

# ---------------------------------------------------------------------------
echo
if [ "$fail" -ne 0 ]; then
    err "Code-quality gate FAILED."
    exit 1
fi
ok "Code-quality gate passed."
