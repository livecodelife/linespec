#!/usr/bin/env bash
# SessionStart (5b): inject the ambient next provenance action once per session.
# Thin renderer — all guidance comes from `linespec provenance next`. Degrades
# silently (exit 0, no output) when jq/linespec are absent or this is not a
# provenance repo, so it can never disrupt a session.
#
# LINESPEC_PROVENANCE_RECONCILE=1 also makes that `next` call re-derive the
# fswrite write-bit projection (prov-2026-8d2f5f2a) from the current set of open
# records' scopes before computing advice — this is how "reconcile MUST run at
# agent session start" is satisfied without a dedicated CLI verb: `next` gates
# the reconcile pass on this env var so it only fires here, never on an ordinary
# `next` call elsewhere (manual CLI use, the per-edit hook, or a test).

input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
command -v linespec >/dev/null 2>&1 || exit 0

cwd=$(printf '%s' "$input" | jq -r '.cwd // empty')
[ -n "$cwd" ] || exit 0
cd "$cwd" 2>/dev/null || exit 0
[ -f ".linespec.yml" ] || exit 0

next=$(LINESPEC_PROVENANCE_RECONCILE=1 linespec provenance next --json 2>/dev/null) || exit 0
reason=$(printf '%s' "$next" | jq -r '.primary.reason // empty')
[ -n "$reason" ] || exit 0
cmd=$(printf '%s' "$next" | jq -r '.primary.command // empty')

ctx="Provenance — next action: ${reason}"
[ -n "$cmd" ] && ctx="${ctx}"$'\n'"  ${cmd}"

jq -n --arg ctx "$ctx" \
  '{hookSpecificOutput: {hookEventName: "SessionStart", additionalContext: $ctx}}'
exit 0
