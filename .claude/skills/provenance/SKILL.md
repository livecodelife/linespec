---
name: provenance
description: Provenance record workflow rules for LineSpec. Governs how to create, complete, and supersede provenance records, and how to include record IDs in commits.
when_to_use: "When starting any new work, modifying files covered by provenance records, completing a feature, superseding or deprecating records, or when asked about the provenance workflow."
---

# Provenance Record Workflow

Follow these rules precisely whenever working with provenance records or making code changes in this repo.

## Step 1 — Investigate Before Creating

**Always investigate existing provenance context before writing a single line of code or creating a new record.** Records capture design decisions, scope constraints, and rationale that aren't in the code.

If embeddings are configured (`.linespec/embeddings.bin` exists), search semantically:

```bash
linespec provenance search "<description of the work or feature area>" [-c <config>]
```

For every file you plan to touch, check which records govern it:

```bash
linespec provenance context -f <file> [-c <config>]
```

Look up specific records by ID using `status --record` (this also finds remote records from shared_repos cache):

```bash
linespec provenance status --record prov-YYYY-XXXXXXXX [-c <config>]
```

If an open record already covers the work, prefer working within that record.

## Step 2 — Create a Blueprint Record (Draft)

Create a `blueprint` record to capture the scope and success criteria **before writing any code**:

```bash
linespec provenance create --title "..." --type blueprint --no-edit [-c <config>]
```

Valid types: `brief`, `blueprint`, `bug`, `imprint`.

**Always pass `--no-edit`.** Omitting it opens an interactive editor that hangs in non-TTY environments.

Fill in `intent`, `constraints`, and `affected_scope` as needed. Draft mode is flexible — add, remove, and adjust fields freely while planning with the user.

Commit the draft in a standalone commit:
```
Create provenance record [prov-YYYY-XXXXXXXX]
```

**Then present the draft record to the user for review.** Show the intent and constraints. Do not write any implementation code until the user confirms the blueprint is correct.

## Step 3 — Open the Blueprint (After User Confirmation)

Once the user confirms the blueprint is correct, transition it to open:

```bash
linespec provenance open --record prov-YYYY-XXXXXXXX [-c <config>]
```

Add `affected_scope` and `associated_specs` to the record at this point. Commit the open transition as a standalone commit:

```
Open provenance record prov-YYYY-XXXXXXXX [prov-YYYY-XXXXXXXX]
```

Scope and spec enforcement are now active for this record.

## Step 4 — Implement with Imprint Records

As you work, create `imprint` records to log micro-decisions, trade-offs, considerations, pivots, and learnings. An imprint must set `implements` pointing at the parent blueprint:

```yaml
type: imprint
implements: prov-YYYY-XXXXXXXX   # the blueprint ID
```

Imprints can be freely opened, implemented, superseded, and deprecated as the implementation evolves. **All imprints must be implemented before the blueprint can be completed.**

Imprint lifecycle is lightweight — create and complete them in quick succession as decisions are made. Tag every implementation commit with the relevant record ID (blueprint or imprint).

## Step 5 — Show Proof and Complete the Blueprint

Before completing the blueprint, verify all imprints are implemented. Then **show the user the proof** — demonstrate that the blueprint's constraints are met (test output, lint output, working commands, etc.).

**Ask the user for explicit permission before completing the blueprint.** Do not complete it automatically.

Once the user confirms, complete in a standalone commit:

```bash
linespec provenance complete --record prov-YYYY-XXXXXXXX [-c <config>]
```

Commit message:
```
Complete provenance record [prov-YYYY-XXXXXXXX]
```

## Commit Message Format

Every commit (except standalone provenance management commits) must include the governing record ID in square brackets:

```
Short description of what changed [prov-YYYY-XXXXXXXX]
```

The pre-commit hook enforces this when `commit_tag_required: true` is set in `.linespec.yml`.

## Pre-Commit Checks

Before any create, open, or complete commit:
```bash
linespec provenance lint [-c <config>]
linespec provenance check [-c <config>]
```

Before each implementation commit:
```bash
linespec provenance check --staged [-c <config>]
```

Always include `-c <path>` when the relevant `.linespec.yml` is not at the repo root.

## Superseding Records

When a record supersedes an existing one:

1. Set `supersedes: prov-YYYY-XXXXXXXX` on the **new** record
2. Update the **old** record's `superseded_by` field to the new record ID
3. Commit both records together in the standalone create commit

Both directions must be set before committing.

## Tier Hierarchy Rules (Enforced by Linter)

- `brief` → top-level intent, cannot use `implements`
- `blueprint` → design decision, may `implements` a brief
- `bug` → defect or regression record, uses `extends` or `supersedes` (not `implements`)
- `imprint` → implementation record, must `implements` a blueprint
- `supersedes` must stay within the same tier (exception: `bug` may supersede a `blueprint`) — PROV020
- `implements` must point exactly one tier up — PROV021
- `implements` reference must resolve locally or via configured shared_repos cache — PROV022
- `extends` is only valid on `bug` records; target must be a `blueprint` or `bug`
- `bug` must have exactly one of `supersedes` or `extends` (not both, not neither)

## Cross-Repo Provenance (shared_repos)

When `.linespec.yml` configures `shared_repos`, records in those remote repositories are available for cross-repo relationships:

- A `blueprint` in a service repo may `implements` a `brief` in a shared product repo
- Resolution order: local `provenance/` directory first, then each configured shared repo cache in declaration order; first match wins
- Remote records are **read-only** — write commands (`complete`, `deprecate`, `open`) reject cache-sourced records; to modify, work in the origin repository
- Before working, sync the cache: `linespec provenance sync`
- The linter warns if the cache is older than `cache_ttl_minutes` (default 60)

## Multi-Pack Projects and the `-c` Flag

In monorepos or multi-pack setups where multiple `.linespec.yml` files exist (e.g., a root config plus per-service configs in subdirectories), **always use `-c <path>` to target the correct config file** for the service you are working on. Without it, commands default to the repo-root `.linespec.yml`, which may report records, scope, and enforcement from the wrong service.

Apply `-c` to every provenance command when working outside the root config:

```bash
linespec provenance status -c path/to/.linespec.yml
linespec provenance create --title "..." --type blueprint --no-edit -c path/to/.linespec.yml
linespec provenance open --record <id> -c path/to/.linespec.yml
linespec provenance lint -c path/to/.linespec.yml
linespec provenance check --staged -c path/to/.linespec.yml
linespec provenance complete --record <id> -c path/to/.linespec.yml
```

If working on the root service, `-c` is optional (it is the default). When in doubt, check which `.linespec.yml` governs the files you are changing and use `-c` accordingly.

## Finding Records

When you need to look up a specific record by ID, use `status --record` rather than searching the provenance directory directly. This ensures remote records (from shared_repos cache) are also discoverable:

```bash
linespec provenance status --record prov-YYYY-XXXXXXXX [-c <config>]
```

For semantic discovery, use search:

```bash
linespec provenance search --query "<description>" [-c <config>]
```

## Hard Rules

- **Never use `--no-verify`** to skip git hooks. If a hook fails, fix the issue.
- **Never complete the blueprint without user confirmation.**
- **Always use `-c <path>` when working on a service that has its own `.linespec.yml`.** Without it, provenance commands may read the wrong config and produce misleading output.
- Records are named `prov-YYYY-XXXXXXXX.yml` using crypto-random hex (not sequential).
- Draft records are for planning — freely edit all fields including `affected_scope`.

## Useful Commands

```bash
# Investigation
linespec provenance status [-c <config>]                 # list all records and status
linespec provenance status --record <id> [-c <config>]   # detailed view of one record
linespec provenance search --query "<query>" [-c <config>] # semantic search (requires embeddings)
linespec provenance context -f <file> [-c <config>]      # which records govern a file
linespec provenance audit [-c <config>]                  # audit recent changes against provenance
linespec provenance graph [--root <id>] [-c <config>]    # render provenance graph

# Record lifecycle
linespec provenance create --title "..." --type <tier> --no-edit [-c <config>]
linespec provenance open --record <id> [-c <config>]     # draft → open
linespec provenance complete --record <id> [-c <config>] # open → implemented
linespec provenance deprecate --record <id> --reason "..." [-c <config>]

# Validation and enforcement
linespec provenance lint [-c <config>]                   # validate all records
linespec provenance check [--staged] [-c <config>]       # check commits for violations
linespec provenance lock-scope --record <id> [-c <config>] # lock scope to allowlist

# Cross-repo
linespec provenance sync                                # refresh shared_repos cache
linespec provenance index [-c <config>]                 # index records for semantic search
```
