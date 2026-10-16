#!/usr/bin/env bash
# PostToolUse on Bash: after a `git commit` that FAILED on a provenance violation,
# surface the engine's exact next action as advisory feedback. The git
# commit-msg/pre-commit hooks are the real gate; this only hands the agent the
# fix. Quiet on success, on non-commit commands, and on non-provenance failures.
# Never blocks.
# The injected reason and command can contain record titles (repo content), so each
# is stripped of control characters and capped at 300 characters.

input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
PROV="${PROVENANCE:-provenance}"
command -v "$PROV" >/dev/null 2>&1 || exit 0

cmd=$(printf '%s' "$input" | jq -r '.tool_input.command // empty')
printf '%s' "$cmd" | grep -q 'git commit' || exit 0

exit_code=$(printf '%s' "$input" | jq -r '.tool_output.exit_code // .tool_response.exit_code // 0')
[ "$exit_code" != "0" ] || exit 0
stderr=$(printf '%s' "$input" | jq -r '.tool_output.stderr // .tool_response.stderr // ""')
printf '%s' "$stderr" | grep -qiE 'provenance|prov-[0-9]|scope violation|commit tag' || exit 0

cwd=$(printf '%s' "$input" | jq -r '.cwd // empty')
[ -n "$cwd" ] && cd "$cwd" 2>/dev/null
next=$("$PROV" next --json 2>/dev/null) || exit 0
reason=$(printf '%s' "$next" | jq -r '.actions[0].reason // "" | explode | map(select(. >= 32 and (. < 127 or . > 159))) | implode | .[0:300]')
[ -n "$reason" ] || exit 0
ncmd=$(printf '%s' "$next" | jq -r '.actions[0].command // "" | explode | map(select(. >= 32 and (. < 127 or . > 159))) | implode | .[0:300]')

msg="Provenance blocked this commit. Next: ${reason}"
[ -n "$ncmd" ] && msg="${msg}"$'\n'"  ${ncmd}"

jq -n --arg ctx "$msg" \
  '{systemMessage: $ctx, hookSpecificOutput: {hookEventName: "PostToolUse", additionalContext: $ctx}}'
exit 0
