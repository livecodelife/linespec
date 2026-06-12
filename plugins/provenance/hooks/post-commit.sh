#!/usr/bin/env bash
# PostToolUse on Bash (5d): the commit-boundary gate. The git commit-msg/pre-commit
# hook is the REAL gate; this fires after a `git commit` ran and, only when that
# commit FAILED on a provenance violation, surfaces the engine's exact remediation
# as advisory feedback (hand the agent the fix, never a bare door). Quiet on success,
# on non-commit bash, and on non-provenance failures. Never blocks.

input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
command -v linespec >/dev/null 2>&1 || exit 0

cmd=$(printf '%s' "$input" | jq -r '.tool_input.command // empty')
# Content-level scope to git commit — tool matchers cannot see command text, so this
# one filter is necessarily in-script.
printf '%s' "$cmd" | grep -q 'git commit' || exit 0

exit_code=$(printf '%s' "$input" | jq -r '.tool_output.exit_code // .tool_response.exit_code // 0')
[ "$exit_code" != "0" ] || exit 0
stderr=$(printf '%s' "$input" | jq -r '.tool_output.stderr // .tool_response.stderr // ""')
# Only react to provenance-related rejections, not arbitrary commit failures.
printf '%s' "$stderr" | grep -qiE 'provenance|prov-[0-9]|scope violation|commit tag' || exit 0

cwd=$(printf '%s' "$input" | jq -r '.cwd // empty')
[ -n "$cwd" ] && cd "$cwd" 2>/dev/null
next=$(linespec provenance next --json 2>/dev/null) || exit 0
reason=$(printf '%s' "$next" | jq -r '.primary.reason // empty')
[ -n "$reason" ] || exit 0
ncmd=$(printf '%s' "$next" | jq -r '.primary.command // empty')

msg="Provenance blocked this commit. Next: ${reason}"
[ -n "$ncmd" ] && msg="${msg}"$'\n'"  ${ncmd}"

jq -n --arg ctx "$msg" \
  '{systemMessage: $ctx, hookSpecificOutput: {hookEventName: "PostToolUse", additionalContext: $ctx}}'
exit 0
