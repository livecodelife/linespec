#!/usr/bin/env bash
# PreToolUse on Edit|Write (5c): on the FIRST edit of a file per session, inject the
# ACTIVE records that govern it (open + implemented; superseded/deprecated excluded)
# plus the next suggestion. Advisory only — it injects context and NEVER blocks the
# edit. Silent when no active record governs the file (the common case). Degrades
# silently when jq/linespec are absent.

input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
command -v linespec >/dev/null 2>&1 || exit 0

file=$(printf '%s' "$input" | jq -r '.tool_input.file_path // empty')
[ -n "$file" ] || exit 0
cwd=$(printf '%s' "$input" | jq -r '.cwd // empty')
[ -n "$cwd" ] && cd "$cwd" 2>/dev/null
[ -f ".linespec.yml" ] || exit 0

# Dedup per file per session via a marker under the per-session data dir (NOT the
# immutable plugin root) so the hook speaks at most once per file.
session=$(printf '%s' "$input" | jq -r '.session_id // "nosession"')
data_dir="${CLAUDE_PLUGIN_DATA:-${TMPDIR:-/tmp}}/linespec-provenance/${session}"
mkdir -p "$data_dir" 2>/dev/null || exit 0
marker="${data_dir}/$(printf '%s' "$file" | tr '/' '_')"
[ -e "$marker" ] && exit 0
: > "$marker" 2>/dev/null || true

# govern matches repo-relative scope; strip the cwd prefix from the absolute path.
rel="${file#"$cwd"/}"
gov=$(linespec provenance govern --files "$rel" --json 2>/dev/null) || exit 0
count=$(printf '%s' "$gov" | jq -r '.governing | length' 2>/dev/null || echo 0)
[ "$count" -gt 0 ] 2>/dev/null || exit 0

# Keep it signal, not noise: name the OPEN records (the ones you might tag) and
# summarize sealed records as a count — a governed file can have dozens of sealed
# records, and listing them all is exactly the over-gating noise to avoid.
open_ids=$(printf '%s' "$gov" | jq -r '[.governing[] | select(.status=="open") | .id] | join(", ")')
impl_count=$(printf '%s' "$gov" | jq -r '[.governing[] | select(.status=="implemented")] | length')
if [ -n "$open_ids" ]; then
  msg="Provenance — ${rel} is governed by open record(s): ${open_ids}. Tag your commit with one (no supersede needed)."
  [ "$impl_count" -gt 0 ] 2>/dev/null && msg="${msg} (${impl_count} sealed record(s) also govern it — no action needed.)"
else
  msg="Provenance — ${rel} is governed by ${impl_count} sealed record(s). Make your change under your own record and tag your commits with it; do NOT supersede."
fi
nreason=$(printf '%s' "$gov" | jq -r '.next.reason // empty')
ncmd=$(printf '%s' "$gov" | jq -r '.next.command // empty')
[ -n "$nreason" ] && msg="${msg}"$'\n'"Next: ${nreason}"
[ -n "$ncmd" ] && msg="${msg}"$'\n'"  ${ncmd}"

# Advisory context only — no permissionDecision, so the normal permission flow proceeds.
jq -n --arg ctx "$msg" \
  '{hookSpecificOutput: {hookEventName: "PreToolUse", additionalContext: $ctx}}'
exit 0
