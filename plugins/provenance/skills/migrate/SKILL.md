---
name: migrate
description: Migrate a repository from v1 (linespec) provenance records to v2 with `provenance migrate`. Reads the plan, explains version chains and suggestions, asks the user what to fold, applies, then verifies. Stops before any commit, push or CI change.
disable-model-invocation: true
---

# Migrate to provenance v2

Run these steps in order. Never commit, push, or edit CI files. Stop after step 7.

1. **Check the start state.** Run `git status --short` and `git branch --show-current`. The tree must be clean. If it is not, stop and tell the user. If the branch is `main`, suggest `git switch -c provenance-v2` first. Run `provenance --help` to confirm the CLI is installed; if it is not, point the user to the install one-liner in the provenance README.
2. **Read the plan.** Run `provenance migrate --plan`. It writes nothing.
3. **Explain it.** Say how many records become how many. For each version chain, say which v1 records fold into one v2 record (oldest version first). Name every keep-separate suggestion and every `dangling_ref` warning. A dangling `implements` target means the parent lives in another repo: ask whether it belongs in `shared_repos`.
4. **Ask before choosing.** For each suggestion, ask the user: fold it, or keep it separate? Default to folding everything. Map the answers to `--keep-separate <id>` and `--fold <id>`, or to `--accept-suggestions` when the user keeps all of them.
5. **Apply.** If the plan named shared repos, run `provenance sync` first. Run `provenance migrate` with the flags from step 4. Read the report: records written, ids folded, children re-pinned.
6. **Verify.** Run `provenance lint`, `provenance stale` and `provenance check --evidence`. Fix leftovers:
   - A pin behind its parent: `provenance repin <id> --reason "<why it is still correct>"`. Confirm each reason with the user.
   - An unknown parent: add the shared repo to `.provenance.yml` and run `provenance sync`.
   - Anything else: show the output and ask.
7. **Hand back.** Tell the user what is left for them, and do none of it:
   - Review `git status` and commit the migration as one commit.
   - Run `provenance hooks install` to replace the linespec hooks (old ones are kept as `.bak`).
   - Run `provenance check --range <base>..HEAD` after committing.
   - Switch CI and Makefile targets from linespec's `provenance` subcommand to the `provenance` binary, with the install one-liner pinned to a full commit SHA.
   - To roll back, `git revert` the migration commit. Never mix v1 tooling with v2 records.
