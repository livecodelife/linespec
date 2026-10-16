#!/usr/bin/env bash
# PreToolUse on Edit|Write: on the FIRST edit of a file per session, tell the agent
# what provenance wants done before the edit lands (create/open a record, widen a
# scope, revise an implemented record, or pick one of several open records).
# Advisory only: it injects context and NEVER blocks the edit. Silent when the
# file is already covered by an open record (the common case), and when jq or
# provenance is absent.
# The reason and command can contain record titles (repo content), so each is
# stripped of control characters and capped at 300 characters.

input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
PROV="${PROVENANCE:-provenance}"
command -v "$PROV" >/dev/null 2>&1 || exit 0

file=$(printf '%s' "$input" | jq -r '.tool_input.file_path // empty')
[ -n "$file" ] || exit 0
cwd=$(printf '%s' "$input" | jq -r '.cwd // empty')
[ -n "$cwd" ] && cd "$cwd" 2>/dev/null
[ -f ".provenance.yml" ] || [ -f ".linespec.yml" ] || exit 0

# Dedup per file per session via a marker under the per-session data dir (not the
# immutable plugin root), so the hook speaks at most once per file.
session=$(printf '%s' "$input" | jq -r '.session_id // "nosession"')
data_dir="${CLAUDE_PLUGIN_DATA:-${TMPDIR:-/tmp}}/provenance-plugin/${session}"
mkdir -p "$data_dir" 2>/dev/null || exit 0
marker="${data_dir}/$(printf '%s' "$file" | tr '/' '_')"
[ -e "$marker" ] && exit 0
: > "$marker" 2>/dev/null || true

rel="${file#"$cwd"/}"
next=$("$PROV" next --files "$rel" --json 2>/dev/null) || exit 0
kind=$(printf '%s' "$next" | jq -r '.actions[0].kind // empty')
case "$kind" in
  create|open|add_scope|revise|choose_record) ;;
  *) exit 0 ;;
esac
reason=$(printf '%s' "$next" | jq -r '.actions[0].reason // "" | explode | map(select(. >= 32 and (. < 127 or . > 159))) | implode | .[0:300]')
cmd=$(printf '%s' "$next" | jq -r '.actions[0].command // "" | explode | map(select(. >= 32 and (. < 127 or . > 159))) | implode | .[0:300]')
[ -n "$reason" ] || exit 0

msg="Provenance - before editing ${rel}: ${reason}"
[ -n "$cmd" ] && msg="${msg}"$'\n'"  ${cmd}"

# Advisory context only: no permissionDecision, so the normal permission flow proceeds.
jq -n --arg ctx "$msg" \
  '{hookSpecificOutput: {hookEventName: "PreToolUse", additionalContext: $ctx}}'
exit 0
