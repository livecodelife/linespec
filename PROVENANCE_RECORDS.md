# Provenance Records

**Record model:** v2 (versioned records)  
**CLI:** `provenance` (github.com/livecodelife/provenance)

Provenance Records are structured YAML artifacts that capture the intent, constraints, and reasoning behind system changes. They live in a `provenance/` directory at the repository root and form a queryable graph of decisions over time. Since LineSpec v3.28.0 they are managed by the standalone `provenance` CLI; `linespec provenance <command>` forwards to it (see [Migrating from `linespec provenance`](#migrating-from-linespec-provenance)).

---

## Table of Contents

1. [Quick Start](#quick-start)
2. [Installation](#installation)
3. [Core Concepts](#core-concepts)
4. [Schema Reference](#schema-reference)
5. [CLI Commands](#cli-commands)
6. [Configuration](#configuration)
7. [Git Integration](#git-integration)
8. [Scope Enforcement & When You're Blocked](#scope-enforcement--when-youre-blocked)
9. [Migrating from `linespec provenance`](#migrating-from-linespec-provenance)
10. [Best Practices](#best-practices)
11. [Examples](#examples)
12. [Troubleshooting](#troubleshooting)

---

## Quick Start

```bash
# 1. Install the provenance CLI (see Installation for pinning)
go install github.com/livecodelife/provenance/cmd/provenance@<commit-sha>

# 2. Set up your repo (run these from the repo root)
provenance init                 # writes .provenance.yml and the provenance/ directory
provenance hooks install        # pre-commit, commit-msg and opt-in pre-push git hooks

# 3. Onboard Claude Code (optional but recommended — Claude Code only)
provenance install-skills       # the /provenance skill
provenance install-plugin       # in-loop next-action advice + /provenance-next

# 4. Start a change: ask what to do, then create a blueprint
provenance next
provenance create --title "Add user authentication" --type blueprint --no-edit

# 5. Validate all records
provenance lint

# 6. View the record graph
provenance graph
```

> **Always pass `--no-edit` to `create`.** Without it the command opens `$EDITOR`, which hangs in scripts and agent sessions.
>
> **Claude Code users:** `install-skills` and `install-plugin` are specific to [Claude Code](https://claude.ai/code). They write the provenance skill and the provenance plugin into your repo's `.claude/` directory. The plugin's hooks only call `provenance next`, so enforcement never depends on them being installed.

---

## Installation

The CLI is a single Go binary.

### Go Install

```bash
go install github.com/livecodelife/provenance/cmd/provenance@<commit-sha>
```

There is no release tag yet. **Pin to a full commit SHA** (CI in this repo does) and bump it deliberately. `provenance` needs Go 1.25 or newer to build.

### LineSpec forwarder

If you already have `linespec` installed, `linespec provenance <command>` runs the `provenance` binary found on `PATH` (or at `$PROVENANCE`) and prints a deprecation notice. It is a transition aid, not a second implementation.

---

## Core Concepts

### What are Provenance Records?

A record is one YAML file (`provenance/prov-YYYY-XXXXXXXX.yml`) that states **why** a change is being made (`intent`), **what must hold** (`constraints`), **which files it governs** (`affected_scope`) and **how it is proven** (`associated_specs`). Git hooks and a filesystem write lock enforce that code changes happen under an open record.

### Tiers

| Tier | Purpose | `implements` |
|------|---------|--------------|
| `brief` | Top-level intent | cannot implement anything |
| `blueprint` | A design decision | may implement a brief |
| `imprint` | One unit of implementation or one micro-decision | implements exactly one tier up (a blueprint) |

Lint enforces the tiers. The v1 `bug` type is gone: a fix is a new **version** of the record it fixes, marked `--fix`.

### ID Format

`prov-YYYY-XXXXXXXX`, where `XXXXXXXX` is random hex (never sequential). Old ids folded into a record by `provenance migrate` stay resolvable as that record's `aliases`, so existing `[prov-…]` commit tags keep working.

### Status Lifecycle

```
draft ──open──▶ open ──complete──▶ implemented ──deprecate──▶ deprecated
                  ▲                     │
                  └──── revise/edit ────┘   (a new version reopens it)
```

- **draft** — free to edit; nothing is enforced.
- **open** — enforcement is on: commits are checked against the record's scope.
- **implemented** — sealed at a commit (`sealed_at_sha`) with evidence recorded.
- **deprecated** — terminal. It takes no new versions.

`superseded` no longer exists. Changing a decision is a new version of the same record.

### Versions, pins and staleness

Every content change is a **version**. A version bump needs a `reason`, and writes an immutable snapshot under `provenance/.versions/<id>/<version>-<hash>.yml`. A manifest of `{id, version, content_hash}` lines lives in `.provenance/versions.jsonl`.

- `content_hash` covers the content fields (intent, constraints, scopes, specs, tags, …) and the content of each associated spec file. It excludes `status`, `sealed_at_sha`, `version` and `reason`, so a status change never looks like a content change.
- An imprint stores `implements_version`, the parent version it was written against. When the parent gets a newer version the imprint is **stale**: `provenance stale` lists the **direct** cases (the pin is behind the parent) and the **transitive** ones (an ancestor is stale).
- Resolve a stale record either by changing it (a new version), or by confirming it is still correct: `provenance repin <id> --reason "..."`. A repin moves only the pin; it creates no version.
- Commit tags may carry the version: `[prov-2026-1a2b3c4d@3]`.

---

## Schema Reference

### Complete Example

```yaml
id: prov-2026-1a2b3c4d
title: Add rate limiting to the public API
status: implemented
version: 2
reason: Tighten the per-key limit after the load test
created_at: "2026-10-01"
author: you@example.com
intent: |
  Public endpoints must be rate limited so one key cannot starve the others.
constraints:
  - Limits are per API key, not per IP
  - A throttled request returns 429 with a Retry-After header
affected_scope:
  - pkg/api/ratelimit/**
  - cmd/server/routes.go
forbidden_scope:
  - pkg/api/auth/**
type: blueprint
implements: prov-2026-0f0f0f0f
implements_version: 3
aliases:
  - prov-2026-9e8d7c6b
related: []
sealed_at_sha: 4f3c2b1a...
associated_specs:
  - path: pkg/api/ratelimit/ratelimit_test.go
    type: go_test
    run_command: go test ./pkg/api/ratelimit/... -run TestRateLimit
tags:
  - api
```

### Field Descriptions

| Field | Required | Description |
|-------|----------|-------------|
| `id` | yes | `prov-YYYY-XXXXXXXX`. Never changes. |
| `title` | yes | Short name (lint warns on long titles). |
| `status` | yes | `draft`, `open`, `implemented` or `deprecated`. |
| `version` | yes | Integer starting at 1; bumped whenever the content changes. |
| `reason` | on every bump | Why this version exists. `reason_kind: fix` marks a fix. |
| `type` | no | `brief`, `blueprint` (default) or `imprint`. |
| `created_at`, `author` | yes | Date and email. |
| `intent` | yes | Why this change exists. |
| `constraints` | no | What must remain true. Constraints should be provable by `associated_specs`. |
| `affected_scope` | no | Globs of files the record governs. Empty means *observed*: any file except `forbidden_scope`. Non-empty means *allowlist*. |
| `forbidden_scope` | no | Globs the record must not touch. |
| `implements` | imprints | Parent record id. May name a record in a configured shared repo via `implements_repo`. |
| `implements_version` | with `implements` | The parent version the record was last confirmed against. |
| `aliases` | no | Old ids folded into this record by migration. |
| `related` | no | Non-hierarchical links. |
| `sealed_at_sha` | set by `complete` | The commit the record was sealed at. Do not edit. |
| `associated_specs` | to open | Proof artifacts (`path`, optional `type`, optional `run_command`). |
| `associated_traces`, `monitors`, `tags` | no | Free-form metadata. |
| `locked` | no | A locked layer: governance gate over its scope. |

Removed from v1: `supersedes`, `superseded_by`, `extends` and the `bug` type.

Never hand-edit `status`, `version`, `sealed_at_sha`, the snapshot files, or `.provenance/versions.jsonl`. Use the commands. `provenance compile` rebuilds the manifest from the snapshots.

---

## CLI Commands

Run `provenance <command> --help` for every flag. Pass `-c <path>` to use a config other than `.provenance.yml`.

### Records and versions

| Command | What it does |
|---------|--------------|
| `create --title … --type brief\|blueprint\|imprint --no-edit [--implements <id>] [--scope …]` | New draft record. `--implements` pins the parent's current version. |
| `open --record <id>` | draft → open. Validates the record (it needs `associated_specs`) and starts enforcement. |
| `edit <id> --reason "…" [--intent … --add-scope … --remove-scope … --add-constraint … --add-spec path[:cmd] --set-spec old=new --refresh-specs]` | Field-level change without an editor. A new version needs `--reason`. |
| `revise <id> --reason "…" [--fix]` | Same, in `$EDITOR`. |
| `add-scope --record <id> [--dry-run]` | Widen an open record's scope with the files committed under it. |
| `complete --record <id> [--force]` | open → implemented. Runs the specs, records evidence and seals the record at HEAD. |
| `deprecate --record <id> --reason "…"` | Terminal status. |
| `repin <id> --reason "…" [--to N]` | Confirm a child against a newer parent version; no new version. |
| `history <id>`, `diff <id> [vA vB]`, `status [--record <id>]` | Read versions, changes and status. |

Editing an implemented record's content reopens it as a new version; completing it again seals that version.

### Checking

| Command | What it does |
|---------|--------------|
| `lint [--record <id>] [--format human\|json\|sarif] [--enforcement none\|warn\|strict]` | Validate every record, including the version and snapshot rules. |
| `check --staged \| --commit <sha> \| --range a..b \| --message-file <f>` | Check changes against the record named in the commit tag. |
| `check --evidence` | Per implemented record: is its evidence fresh, stale (content, specs, code or parent), unknown or missing. |
| `evidence refresh <id>` | Re-run the specs and append fresh evidence after a code-only change; no new version. |
| `run-specs --record <id>` | Run a record's associated specs (the pre-commit hook uses it). |
| `stale [--json] [--exit-code]` | Records behind their parent. |

### Understanding

| Command | What it does |
|---------|--------------|
| `next [files…] [--json]` | The single correct next action, with ids filled in. |
| `govern [files…]` | Which active (open or implemented) records govern the files. |
| `context [paths or ids…]` | What an agent needs to know about a path or a record. |
| `graph [--root <id>] [--format human\|json\|dot]` | Records, parents, pins and versions. |
| `search "<words>"` | Offline lexical (BM25) search over records. |

### Repo upkeep

| Command | What it does |
|---------|--------------|
| `init` | Scaffold `.provenance.yml` and `provenance/`. Installs nothing. |
| `migrate [--plan]` | Convert v1 records to v2 (below). |
| `sync [--force]` | Fetch the records of the configured `shared_repos` into the local cache. |
| `reconcile [--dry-run]` | Re-derive the filesystem write lock from the open records' scopes. |
| `compile` | Rebuild `.provenance/versions.jsonl` from the snapshots. |
| `hooks install` | Write the git hooks (existing ones are kept as `.bak`). |
| `install-skills`, `install-plugin` | Claude Code skill and plugin. |

### Not in the `provenance` CLI

These `linespec provenance` commands have no v2 counterpart yet. They still run in-process in `linespec`, but they read v1 records and **do not work on a migrated repository**: `audit`, `discover`, `generate`, `index`, `lock-layer`, `lock-scope`, `publish` and `install-hooks` (use `provenance hooks install`). `linespec clone` and `linespec import` are not provenance commands and are unchanged.

---

## Configuration

`provenance init` writes a fully commented `.provenance.yml`. The keys:

```yaml
enforcement: strict            # none | warn | strict
dir: provenance                # where records live, relative to this file
exclude_paths: ['.gitignore', 'README.md', '.provenance.yml']   # paths, dirs, globs or /regex/
shared_repos:                  # records of other repos you may implement (read-only)
  - name: livecodelife-cli
    url: https://github.com/livecodelife/linespec-provenance
    ref: main
    dir: linespec-cli/provenance
cache_ttl_minutes: 0           # shared-repo cache freshness; 0 means the default, 60
commit_tag_required: true      # commits that change governed files must carry [prov-…] or [prov-…@N]
auto_affected_scope: true      # fill affected_scope from the diff
run_associated_specs_on_complete: true   # complete runs the specs and records evidence
overlap_specs_on_complete: warn          # block | warn | off
commit_on_status_change: true  # open, complete and deprecate commit the record change themselves
write_restriction: true        # omit for the default (true); false disables the write lock
pre_push_command: go build ./... # optional: run in a clean checkout of every pushed commit
```

A `spec`'s `run_command` is run as `<run_command> <path>`, or with `{{path}}` substituted wherever it appears.

**Shared repos.** `implements_repo` lets a record implement a parent that lives in another repository. Run `provenance sync` to fetch those records into `~/.linespec/cache/`; lint, `graph`, `context`, `search` and `check` then read them (read-only).

**This repo's `overlap_specs_on_complete`** is `warn`: completing a record that touches files like `commands.go` or `main_*.go` pulls in the whole `examples/*-linespecs` Docker matrix, which is environmentally fragile.

---

## Git Integration

### Hooks

`provenance hooks install` writes three hooks and keeps existing ones as `.bak`:

- **pre-commit** — lints staged records and runs a record's specs on a completion.
- **commit-msg** — `provenance check --staged` against the record named in the commit tag.
- **pre-push** — opt-in: does nothing unless `pre_push_command` is set; then it runs in a clean checkout of every pushed commit and a failing commit blocks the push.

Never use `--no-verify`.

### Commit Message Format

```
Short description of the change [prov-2026-1a2b3c4d]
Short description of the change [prov-2026-1a2b3c4d@2]
```

Every commit except standalone provenance bookkeeping carries the governing record. The `@N` form pins the version and survives rebases and squashes. Create, open and complete operations are standalone commits.

### CI

This repository's CI installs `provenance` at a full commit SHA, then runs `provenance sync`, `provenance lint --enforcement strict`, `provenance stale`, and on pull requests `provenance check --range <base>..<head>`. A separate workflow uploads `provenance lint --format sarif` to GitHub Code Scanning.

```yaml
- run: go install github.com/livecodelife/provenance/cmd/provenance@<full-commit-sha>
- run: provenance lint --enforcement strict
```

### Claude Code plugin

`provenance install-plugin` and `provenance install-skills` copy the plugin and skill into `.claude/`. The plugin renders `provenance next` at session start, `govern` before edits and remediation after a rejected commit.

---

## Scope Enforcement & When You're Blocked

The commit check validates changed files **only against the record you tag**. Other records that happen to govern the files do not block you, and touching an implemented record's files does not require a new record.

### Happy path — do this before touching anything

1. `provenance next [files…]` — the correct next action, with ids.
2. `provenance create --type blueprint --no-edit --title "…"`; fill `intent`, `constraints`, `affected_scope`; create the proof file; reference it in `associated_specs`.
3. Commit the draft on its own, get the user's confirmation, then `provenance open --record <id>`.
4. Implement with `imprint` records (`--implements <blueprint-id>`), committing each tagged `[prov-…]`.
5. `provenance check --staged` before each implementation commit.
6. Show proof, then `provenance complete --record <id>`.

### Scope modes

Empty `affected_scope` is *observed*: any file is allowed except `forbidden_scope`. A non-empty list is an *allowlist*.

### When you're blocked — decision tree

| Message | Do this |
|---------|---------|
| `Commit tag required but no provenance ID found` | Tag the commit with your record, `[prov-YYYY-XXXXXXXX]`. |
| `<id> is implemented: run 'provenance revise <id>'` | Revise the record (a new version), then tag the commit with that version. |
| `forbids changes to <file>` / scope violation | Add `<file>` to **your tagged record's** scope: `provenance edit <id> --add-scope <file> --reason "…"`, or `add-scope`. |
| `No associated specs (open)` | Create the proof file, add it with `edit --add-spec`, then open. |
| `Content changed since version N without a version bump` (PROV024) | A spec file changed: `provenance edit <id> --refresh-specs --reason "…"`. |
| `Version manifest says version N …` (PROV026) | `provenance compile`. |
| `permission denied` writing a file | The write lock: declare the file in an open record. Never `chmod`. |

**Hard rules:** never `--no-verify`, never relax `enforcement` to get unblocked, and stop and ask when a wall is a governance decision.

### Filesystem write lock

Tracked files are read-only unless an **open** record's `affected_scope` covers them. The records directory, `.provenance.yml`, `.provenance/`, `exclude_paths` and every record's `associated_specs` paths are always writable; snapshot files under `provenance/.versions/` are always read-only. `provenance reconcile` re-derives the permissions; `open`, `complete`, `deprecate`, `revise` and `edit` run it for you.

### Use commands, not manual YAML edits

| Instead of editing… | Use |
|---------------------|-----|
| `status: open` | `open --record <id>` |
| `status: implemented` and `sealed_at_sha` | `complete --record <id>` |
| `status: deprecated` | `deprecate --record <id> --reason "…"` |
| intent, constraints, scope, specs | `edit` or `revise`, which write a new version |
| files already committed, into scope | `add-scope --record <id>` |
| the version manifest | `compile` |

### associated_specs — proof artifacts

Each entry has a required `path` (the file must exist), an optional `type` and an optional `run_command`. Create the proof file first, reference it, then open. Pick a stable proof file: a mutable config file drifts the record every time it changes, and a spec file shared by several records drifts all of them. Always give `type: code` and `type: go_test` specs a `run_command`, or `complete` skips them silently.

---

## Migrating from `linespec provenance`

Records written by `linespec provenance` (v1) convert once, in place:

```bash
provenance migrate --plan                   # show what would happen, write nothing
provenance migrate --accept-suggestions     # apply, keeping suggested chains separate
```

What `migrate` does:

1. A chain of records joined by `supersedes` becomes **versions of the newest record**; the old ids become `aliases`, so old commit tags resolve. Imprints, blueprints and bugs are folded by the migration rules in the design (a bug that extends or supersedes a record becomes a fixing version of it).
2. It writes a snapshot for every version, `.provenance/versions.jsonl`, and moves the config to `.provenance.yml` (`.linespec.yml` is removed when it held only the provenance block).
3. It removes the v1 hash manifest `.linespec/hash_manifest.json`.
4. The plan marks chains it suggests keeping separate (low scope overlap); `--accept-suggestions` keeps every suggestion, or choose per record with `--keep-separate <id>` and `--fold <id>`.
5. It does not install hooks. Run `provenance hooks install` afterwards.

After migrating, run `provenance lint`, `provenance sync` (for shared repos) and `provenance stale`.

### The `linespec provenance` forwarder

`linespec provenance <command>` forwards every command the `provenance` CLI implements to that binary and prints a deprecation notice on stderr. The forwarded commands are `add-scope check compile complete context create deprecate diff edit evidence govern graph history hooks init install-plugin install-skills lint migrate next open reconcile repin revise run-specs search stale status sync`.

- `PROVENANCE=/path/to/provenance` selects the binary; `LINESPEC_NO_DEPRECATION_WARNING=1` silences the notice.
- If the binary is not installed the command exits 127 with the install command.
- The forwarder is removed after two linespec minor releases. Switch scripts to `provenance <command>`.
- `-p` still works as an alias for `provenance` in `linespec`.

### Rollback

Migration is a normal git change: revert the migration commit(s) to return to v1 records, `.linespec.yml` and the hash manifest, and use a linespec release older than v3.28.0 (or keep the in-process commands). Nothing outside the repository changes. The `provenance/.versions/` snapshots and `.provenance/` directory disappear with the revert. Hooks are the exception: `provenance hooks install` keeps the previous hooks as `.bak`, so restore those files.

---

## Best Practices

### Writing Good Provenance Records

- State the intent as a decision and its reason, not a task list.
- Write constraints that a spec can prove.
- Keep imprints small: one unit of work or one decision, written **before** the code it documents.
- Keep intents complete; a revision is a full statement, not a delta.

### Scope Management

- Scope exactly the files you will change. Widen with `edit --add-scope` or `add-scope`, not by deleting `affected_scope`.
- `exclude_paths` is for generated code, vendored files and tool configs, not for avoiding records.

### Record Lifecycle

- Draft → commit alone → get confirmation → open (alone) → implement → show proof → ask → complete (alone).
- Complete imprints before their blueprint; `complete` warns about unfinished imprints.
- Prefer a new version over a new record when the decision changes. Use `deprecate` when the decision is dead.
- After a parent changes, `provenance stale` then `repin` or revise each child.

---

## Examples

### Example 1: A small change

```bash
provenance next pkg/api/ratelimit/limiter.go
provenance create --title "Rate limit public API" --type blueprint --no-edit
# fill in intent, constraints and affected_scope in the record, create the proof test
provenance edit prov-2026-1a2b3c4d --add-spec pkg/api/ratelimit/ratelimit_test.go:"go test ./pkg/api/ratelimit/..." --reason "Add proof"
provenance open --record prov-2026-1a2b3c4d
git commit -m "Add limiter [prov-2026-1a2b3c4d]"
provenance complete --record prov-2026-1a2b3c4d
```

### Example 2: Changing a decision

```bash
provenance revise prov-2026-1a2b3c4d --reason "Limit per key, not per IP"
git commit -m "Per-key limits [prov-2026-1a2b3c4d@2]"
provenance stale                       # which imprints are now behind?
provenance repin prov-2026-5e6f7a8b --reason "Still correct against v2"
```

### Example 3: Moving a file

```bash
provenance edit prov-2026-1a2b3c4d --remove-scope old/path.go --add-scope new/path.go --reason "Move limiter"
# one commit, tagged @N, that deletes old/path.go and adds new/path.go
```

---

## Troubleshooting

### Common Issues

| Symptom | Cause and fix |
|---------|---------------|
| `provenance: command not found` from `linespec provenance …` | Install the CLI (see Installation) or set `$PROVENANCE`. |
| `open .linespec.yml: no such file` | A migrated repo uses `.provenance.yml`. Drop the old `-c .linespec.yml`. |
| `cannot be judged: parent is in shared repo` in `stale` | Run `provenance sync` to fetch the shared repo's records. |
| `pinned to X@1, parent is at N` in `stale` | The parent has a newer version: revise the child, or `repin` it. |
| `check --evidence` shows `unknown` | The code hash needs a current `sedum`; set `SEDUM_BIN`, then `provenance evidence refresh <id>`. |
| `make lint` reports `provenance CLI not found` | Install the pinned CLI (the message prints the command). |
| `linespec provenance index` or `discover` fails on a migrated repo | Those commands read v1 records and are not part of the v2 CLI (see Not in the `provenance` CLI). |

### Getting Help

- `provenance <command> --help` and `provenance next`
- Issues: https://github.com/livecodelife/linespec/issues (this site) and https://github.com/livecodelife/provenance/issues (the CLI)

## License

MIT License - See LICENSE file for details.
