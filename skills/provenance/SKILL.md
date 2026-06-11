---
name: provenance
description: Provenance record workflow rules for LineSpec. Governs how to create, complete, and supersede provenance records, and how to include record IDs in commits.
when_to_use: "When starting any new work, modifying files covered by provenance records, completing a feature, superseding or deprecating records, or when asked about the provenance workflow."
---

# Provenance Record Workflow

Follow these rules precisely whenever working with provenance records or making code changes in this repo.

## TL;DR — the happy path

Before touching anything, follow this sequence and you will not get stuck on enforcement:

1. **Investigate.** Run `linespec provenance context -f <file>` for every file you plan to change. This shows which records govern them. (Those governing records do **not** need to be superseded — see below.)
2. **Create one record:** `linespec provenance create --type blueprint --no-edit --title "…"`.
3. **Set its `affected_scope`** to exactly the files you will change.
4. **Create your proof artifact, then `open`** the record with that spec referenced in `associated_specs`.
5. **Make changes; commit** tagged with the record ID `[prov-YYYY-XXXXXXXX]`.
6. **Show proof, then `complete`.**

## Step 1 — Investigate Before Creating

**Always investigate existing provenance context before writing a single line of code or creating a new record.** Records capture design decisions, scope constraints, and rationale that aren't in the code.

For every file you plan to touch, check which records govern it:

```bash
linespec provenance context -f <file> [-c <config>]
```

If embeddings are configured, search semantically:

```bash
linespec provenance search --query "<description of the work or feature area>" [-c <config>]
```

Look up a record by ID with `status --record` (this also finds remote records from the shared_repos cache):

```bash
linespec provenance status --record prov-YYYY-XXXXXXXX [-c <config>]
```

**Important:** discovering that several records govern your files does NOT mean you must supersede them. The scope check only validates the record you tag. You will create one new record covering your files (Step 2). See "Scope Enforcement & When You're Blocked" below.

## Step 2 — Create a Blueprint Record (Draft)

Create a `blueprint` record to capture the scope and success criteria **before writing any code**:

```bash
linespec provenance create --title "..." --type blueprint --no-edit [-c <config>]
```

Valid types: `brief`, `blueprint`, `bug`, `imprint`.

**Always pass `--no-edit`.** Omitting it opens an interactive editor that hangs in non-TTY environments.

Fill in `intent`, `constraints`, and `affected_scope` as needed. Draft mode is flexible — add, remove, and adjust fields freely while planning with the user. Commit the draft in a standalone commit, then **present it to the user for review.** Do not write implementation code until the user confirms.

## Step 3 — Open the Blueprint (After User Confirmation)

```bash
linespec provenance open --record prov-YYYY-XXXXXXXX [-c <config>]
```

Add `affected_scope` and `associated_specs` at this point (create the proof files first — see associated_specs below). Commit the open transition as a standalone commit.

## Step 4 — Implement with Imprint Records

As you work, create `imprint` records to log micro-decisions, trade-offs, pivots, and learnings. An imprint must set `implements` pointing at the parent blueprint:

```yaml
type: imprint
implements: prov-YYYY-XXXXXXXX   # the blueprint ID
```

**All imprints must be implemented before the blueprint can be completed.** Tag every implementation commit with the relevant record ID.

## Step 5 — Show Proof and Complete the Blueprint

Verify all imprints are implemented, **show the user the proof** (test/lint output, working commands), and **ask for explicit permission before completing.** Then:

```bash
linespec provenance complete --record prov-YYYY-XXXXXXXX [-c <config>]
```

## Commit Message Format

Every commit (except standalone provenance management commits) must include the governing record ID in square brackets:

```
Short description of what changed [prov-YYYY-XXXXXXXX]
```

The pre-commit hook enforces this when `commit_tag_required: true` is set in `.linespec.yml`.

## Pre-Commit Checks

Before any create, open, or complete commit: `linespec provenance lint` and `linespec provenance check`. Before each implementation commit: `linespec provenance check --staged`. Always include `-c <path>` when the relevant `.linespec.yml` is not at the repo root.

## Scope Enforcement & When You're Blocked

**The single most important rule:** the pre-commit scope check validates your changed files **only against the record you tag** in the commit message. It does **not** consult other records that happen to govern those files. So implemented/sealed records whose `affected_scope` overlaps your files do **not** block your commit and do **not** need to be superseded. Create **one** new record covering exactly your files, open it, and tag your commits with it. Supersede a record *only* when you are deliberately revising the decision it captured.

### Scope modes

- A record with an **empty `affected_scope`** is **observed** — its check permits any file (except `forbidden_scope`).
- A record with a **non-empty `affected_scope`** is **allowlist** — it permits only files matching that scope. So a "scope violation" on *your tagged record* means its `affected_scope` is missing one of your changed files → widen that record's scope (free to edit while `draft`), don't touch other records.
- `lock-scope` auto-populates a record's `affected_scope` from the files it changed in git. `lock-layer` creates a `locked` governance record — advanced and uncommon; only when locked records exist can an overlapping open record hard-fail lint.

### When you're blocked — decision tree

| Message | Do this |
|---------|---------|
| `Commit tag required but no provenance ID found` | Tag the commit with your record ID `[prov-YYYY-XXXXXXXX]`. |
| `X is already implemented - cannot commit with this ID` | Implemented records are immutable. Create **one new record** covering your files and tag that — do **not** supersede the records that govern the files. |
| `forbids changes to <file>` / scope violation | Add `<file>` to **your tagged record's** `affected_scope` (editable in draft). |
| `No associated specs (open) [strict]` | Add `associated_specs` (proof) to the open record before committing/completing. |
| `overlaps with locked record Y` | A deliberate governance gate (only if locked layers exist). **Stop and ask the maintainer** — do not blindly supersede multiple records. |

> **Stale-scope warnings are non-blocking.** When you edit a file governed by an *implemented* record you may see a warning that the file "is governed by implemented record … create a superseding record." This is informational only — the commit still succeeds, no action is required, and it is **not** a reason to supersede anything. Proceed under your own new record.

**Hard rules:** Never use `--no-verify`. Never relax enforcement (`strict` → `warn`/`none`) to get unblocked — that is a maintainer + settings-level decision, not yours. When a wall is genuinely a governance call, stop and ask rather than brute-forcing.

## Use Commands, Not Manual YAML Edits

Let the CLI update records — hand-editing managed fields corrupts the graph.

| Instead of manually editing… | Use |
|------------------------------|-----|
| `supersedes` + the old record's `superseded_by` + `status: superseded` | `create --supersedes <old-id>` (sets all of it and stages both files) |
| `status: open` | `open --record <id>` |
| `status: implemented` + `sealed_at_sha` | `complete --record <id>` |
| `status: deprecated` | `deprecate --record <id> --reason "…"` |
| listing changed files into `affected_scope` | `lock-scope --record <id>` (auto-populates from git) |
| the hash manifest | `compile` |

Never hand-edit `status`, `superseded_by`, `sealed_at_sha`, or the hash manifest.

### Superseding records

To supersede an existing record, run `create --supersedes`:

```bash
linespec provenance create --title "Better approach" --supersedes prov-YYYY-XXXXXXXX --no-edit
```

This sets `supersedes` on the new record AND automatically updates the old record's `superseded_by` and `status: superseded`, staging both files together. Do **not** edit those fields by hand.

## associated_specs — Proof Artifacts

`associated_specs` attach proof that a record's constraints are met. Each entry:

- **`path`** — required. Must point to a file that *exists* (lint fails otherwise). Any file type: a test, a `.linespec`, a config, a doc, a screenshot, a log.
- **`type`** — optional. These auto-run with no `run_command`: `linespec` → `linespec test <path>`, `rspec` → `bundle exec rspec <path>`, `pytest` → `pytest <path>`, `jest` → `npx jest <path>`. Any other type with no `run_command` is recorded as proof but **skipped** (not executed).
- **`run_command`** — optional; overrides `type`. Runs as `<run_command> <path>` (path appended) **unless** the command contains `{{path}}`, which is substituted instead.

**Strict order of operations:** under strict enforcement an open record with no `associated_specs` is a hard error, and a referenced spec path that does not exist also fails lint — so create the proof file *first*, then reference it, then `open`:

```yaml
# 1. write the proof file first (e.g. spec/models/user_spec.rb)
# 2. reference it:
associated_specs:
  - path: spec/models/user_spec.rb   # must already exist
    type: rspec                       # auto-runs `bundle exec rspec <path>`
  - path: linespecs/create_user.linespec
    type: linespec                    # auto-runs `linespec test <path>`
  - path: docs/architecture.md
    run_command: test -f {{path}}     # non-test proof: just assert it exists
# 3. then `linespec provenance open --record <id>`
```

To author the `.linespec` files that back `type: linespec` specs, use the **linespec-testing** skill (`/linespec-testing`).

## Tier Hierarchy Rules (Enforced by Linter)

- `brief` → top-level intent, cannot use `implements`
- `blueprint` → design decision, may `implements` a brief
- `bug` → defect/regression record, uses `extends` or `supersedes` (not `implements`)
- `imprint` → implementation record, must `implements` a blueprint
- `supersedes` must stay within the same tier (exception: `bug` may supersede a `blueprint`) — PROV020
- `implements` must point exactly one tier up — PROV021
- `implements` must resolve locally or via configured shared_repos cache — PROV022
- `extends` is only valid on `bug` records; target must be a `blueprint` or `bug`
- `bug` must have exactly one of `supersedes` or `extends`

## Cross-Repo Provenance (shared_repos)

When `.linespec.yml` configures `shared_repos`, records in those remote repositories are available for cross-repo relationships. Resolution: local `provenance/` first, then each shared repo cache in order; first match wins. Remote records are **read-only**. Sync the cache before working: `linespec provenance sync`. The linter warns if the cache is older than `cache_ttl_minutes` (default 60).

## Multi-Pack Projects and the `-c` Flag

In monorepos with multiple `.linespec.yml` files, **always use `-c <path>`** to target the correct config for the service you are working on. Without it, commands default to the repo-root config and may report records, scope, and enforcement from the wrong service.

## Hard Rules

- **Never use `--no-verify`** to skip git hooks. If a hook fails, fix the issue.
- **Never relax enforcement** (`strict` → `warn`/`none`) to get unblocked — that is a maintainer + settings-level decision.
- **Never complete the blueprint without user confirmation.**
- **Never hand-edit** `status`, `superseded_by`, `sealed_at_sha`, or the hash manifest — use the command that manages them.
- Records are named `prov-YYYY-XXXXXXXX.yml` using crypto-random hex (not sequential).
- Draft records are for planning — freely edit all fields including `affected_scope`.

## Useful Commands

```bash
# Investigation
linespec provenance status [--record <id>] [-c <config>]   # list records / detail
linespec provenance search --query "<query>" [-c <config>] # semantic search
linespec provenance context -f <file> [-c <config>]        # which records govern a file
linespec provenance audit [-c <config>]                    # audit recent changes
linespec provenance graph [--root <id>] [-c <config>]      # render the graph

# Record lifecycle (these update state for you — don't hand-edit YAML)
linespec provenance create --title "..." --type <tier> --no-edit [-c <config>]
linespec provenance create --supersedes <old-id> --title "..." --no-edit  # supersede correctly
linespec provenance open --record <id> [-c <config>]       # draft → open
linespec provenance complete --record <id> [-c <config>]   # open → implemented (+ seals SHA)
linespec provenance deprecate --record <id> --reason "..." [-c <config>]

# Validation and enforcement
linespec provenance lint [--warn|--info|--all] [-c <config>]  # validate; filter output
linespec provenance check [--staged] [-c <config>]            # check commits for violations
linespec provenance run-specs --record <id> [-c <config>]     # run a record's associated_specs
linespec provenance lock-scope --record <id> [-c <config>]    # freeze affected_scope from git
linespec provenance lock-layer --title "..." --no-edit        # create a locked governance layer

# Cross-repo and maintenance
linespec provenance sync [--force]                         # refresh shared_repos cache
linespec provenance index [-c <config>]                    # index records for semantic search
linespec provenance compile [-c <config>]                  # rebuild the hash manifest
linespec provenance generate [--record <id>]               # generate a behavioral spec doc

# Setup, distribution, and tooling
linespec init                                              # bootstrap a .linespec.yml
linespec provenance install-hooks                          # install pre-commit + commit-msg hooks
linespec provenance install-skills                         # install the Claude Code skills
linespec provenance publish [-c <config>]                  # package records into a manifest
linespec import <manifest-url>                             # import records from a manifest
linespec clone <manifest-url>                              # bootstrap a project from a manifest
```
