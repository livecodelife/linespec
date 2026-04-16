---
name: provenance
description: Provenance record workflow rules for LineSpec. Governs how to create, complete, and supersede provenance records, and how to include record IDs in commits.
when_to_use: "When starting any new work, modifying files covered by provenance records, completing a feature, superseding or deprecating records, or when asked about the provenance workflow."
---

# Provenance Record Workflow

Follow these rules precisely whenever working with provenance records or making code changes in this repo.

## Before Starting Any Work

Check whether existing records govern the files you plan to change:

```bash
linespec provenance context -f <file>
```

Then create a new record for your work:

```bash
linespec provenance create --title "..." --no-edit
```

**Always pass `--no-edit`.** Omitting it opens an interactive editor that hangs in non-TTY environments.

The record ID printed by the command looks like `prov-YYYY-XXXXXXXX`. Note it — every subsequent commit must reference it.

## Commit Message Format

Every commit (except the standalone provenance create/complete commits themselves) must include the record ID in square brackets at the end of the subject line:

```
Short description of what changed [prov-YYYY-XXXXXXXX]
```

The pre-commit hook enforces this when `commit_tag_required: true` is set in `.linespec.yml`.

## Record Lifecycle Rules

Records move through states: `open` → `implemented` → `superseded` or `deprecated`.

**Do not mark a record implemented until all its work is merged.**

When work is complete, mark the record implemented in a **standalone commit**:

```bash
linespec provenance complete <id>
```

Then commit only that change with a message like:
```
Complete provenance record [prov-YYYY-XXXXXXXX]
```

## Standalone Commits for Record Operations

Creating and completing provenance records must each be their own standalone commit — do not mix them with code changes. Commit order for a typical feature:

1. **Standalone commit**: create the provenance record
2. **Implementation commits**: code changes, each tagged `[prov-YYYY-XXXXXXXX]`
3. **Standalone commit**: complete the provenance record

Before attempting a create or complete commit, always run:

```bash
linespec provenance lint
linespec provenance check
```

Fix any issues before committing.

Before making an intra-lifecycle implementation commit, always run:

```bash
linespec provenance check --staged
```

## Superseding Records

When creating a record that supersedes an existing one:

1. Set `supersedes: prov-YYYY-XXXXXXXX` on the **new** record
2. Immediately update the **old** record's `superseded_by` field to the new record ID
3. Commit both records together in the standalone provenance creation commit

Both directions of the relationship must be present before committing. The user caught a case where `superseded_by` was left empty — don't let that happen.

## Hard Rules

- **Never use `--no-verify`** to skip git hooks. If a hook fails, fix the underlying issue.
- Records are named `prov-YYYY-XXXXXXXX.yml` using a crypto-random hex suffix (not sequential).

## Useful Commands

```bash
# Check which records govern a file
linespec provenance context -f <file>

# Semantic search across records
linespec provenance search "<query>"

# View record status
linespec provenance status

# Lint all records
linespec provenance lint

# Complete a record
linespec provenance complete <id>
```
