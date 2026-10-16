#!/usr/bin/env bash
# SessionStart: inject the ambient next provenance action once per session.
# A thin renderer: all guidance comes from `provenance next --json`. Degrades
# silently (exit 0, no output) when jq or provenance is absent or this is not a
# provenance repo, so it can never disrupt a session. It only reads: it changes
# no file and no permission. The reason and command can contain record titles (repo
# content), so each is stripped of control characters and capped at 300 characters.

input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
PROV="${PROVENANCE:-provenance}"
command -v "$PROV" >/dev/null 2>&1 || exit 0

cwd=$(printf '%s' "$input" | jq -r '.cwd // empty')
[ -n "$cwd" ] || exit 0
cd "$cwd" 2>/dev/null || exit 0
[ -f ".provenance.yml" ] || [ -f ".linespec.yml" ] || exit 0

next=$("$PROV" next --json 2>/dev/null) || exit 0
reason=$(printf '%s' "$next" | jq -r '.actions[0].reason // "" | explode | map(select(. >= 32 and (. < 127 or . > 159))) | implode | .[0:300]')
[ -n "$reason" ] || exit 0
cmd=$(printf '%s' "$next" | jq -r '.actions[0].command // "" | explode | map(select(. >= 32 and (. < 127 or . > 159))) | implode | .[0:300]')

ctx="Provenance - next action: ${reason}"
[ -n "$cmd" ] && ctx="${ctx}"$'\n'"  ${cmd}"

jq -n --arg ctx "$ctx" \
  '{hookSpecificOutput: {hookEventName: "SessionStart", additionalContext: $ctx}}'
exit 0
