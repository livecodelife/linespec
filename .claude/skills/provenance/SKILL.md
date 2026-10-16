---
name: provenance
description: Provenance record workflow rules. Governs how to create, open, revise, complete and repin versioned provenance records, and how to tag commits with record IDs and versions.
when_to_use: "When starting any new work, modifying files covered by provenance records, completing a feature, revising or deprecating records, or when asked about the provenance workflow."
---

# Provenance Record Workflow

Follow these rules precisely whenever you work with provenance records or change code in a repo that has a `.provenance.yml`.

## TL;DR: the happy path

1. **Ask `next`.** `provenance next [files...]` computes the single correct next action, with record IDs filled in. Start here, not with a manual read of the records.
2. **Create one record:** `provenance create --type blueprint --no-edit --title "..."` (an imprint for one unit of work: `--type imprint --implements <blueprint-id>`).
3. **Set `affected_scope`** to exactly the files you will change, and create your proof file.
4. **Reference the proof** in `associated_specs`, then `provenance open --record <id>`.
5. **Make changes and commit**, tagged `[prov-YYYY-XXXXXXXX]` or `[prov-YYYY-XXXXXXXX@N]` (N is the record's current version).
6. **Show proof, then `provenance complete --record <id>`.**

## Records are versioned

A record is one file with a version. A change to what a record says (intent, constraints, scope, specs) is a **new version**, never a new "superseding" record:

- **Use `provenance edit <id> --reason "..." [--intent ... --add-constraint ... --add-scope ... --remove-scope ... --add-spec path[:cmd] --remove-spec path --set-spec old=new ...]`.** It changes fields without an editor. `provenance revise` opens `$EDITOR` and hangs without a TTY, so leave it to humans. A reason is required for a new version.
- Revising an `implemented` record reopens it. Tag the commit with the new version: `[prov-YYYY-XXXXXXXX@2]`.
- A spec file whose content changed on disk moves the record's content hash. Record that with `provenance edit <id> --refresh-specs --reason "..."` (a new version).
- Title and tag edits do not change behaviour, so they save in place without a version.
- Never hand-edit `status`, `version`, `sealed_at_sha`, snapshot files under `provenance/.versions/`, or `.provenance/versions.jsonl`. `provenance compile` rebuilds the manifest if it is wrong.

## Step 1: investigate with `next`

```bash
provenance next [files...] [--json] [-c <config>]
```

It says: create a record, open a draft, widen scope (`add_scope`), add a proof (`add_spec`), commit under an ID, complete, finish a pending imprint, or **revise** an implemented record that governs your files. Other lookups:

```bash
provenance status [--record <id>]      # list records, or one record in detail
provenance history <id>                # versions with reasons and commits
provenance diff <id> <v1> <v2>         # what a version changed
provenance stale                       # records behind their parent
```

Governing records that `next` surfaces do not each need a new version. Create one record for your change, or revise the one record you are deliberately changing.

## Step 2: create a draft

```bash
provenance create --title "..." --type blueprint --no-edit
```

**Always pass `--no-edit`**: omitting it opens an interactive editor that hangs without a TTY. Valid types: `brief`, `blueprint`, `imprint`. Fill in `intent`, `constraints` and `affected_scope`; a draft is free to edit. Commit the draft on its own, then present it to the user. Do not write implementation code until the user confirms.

## Step 3: open (after the user confirms)

```bash
provenance open --record <id>
```

Create the proof files first, reference them in `associated_specs`, then open. Opening writes version 1 and starts enforcement.

## Step 4: implement with imprints

Create `imprint` records (`--implements <blueprint-id>`) for units of work and decisions. Write and commit the imprint **before** the code it documents. Imprints must be implemented before their blueprint is completed. An imprint's `implements_version` pins the parent version it was written against.

## Step 5: show proof, then complete

Show the user the proof (test output, working commands) and ask before completing:

```bash
provenance complete --record <id>
```

With `run_associated_specs_on_complete: true` this runs the associated specs and records **evidence** (hashes of the content, specs and code, plus the result). Without it, complete warns that it recorded none.

## Commit messages

Every commit except standalone provenance bookkeeping carries the governing record in square brackets: `Short description [prov-YYYY-XXXXXXXX]` or with the version `[prov-YYYY-XXXXXXXX@N]`. The commit-msg hook enforces it when `commit_tag_required: true`. A commit that changes an implemented record's own file must be a new version (revise first). Merge commits are checked only for paths that differ from both parents.

## Hooks

Install them once with `provenance hooks install`: `pre-commit` (lints staged records), `commit-msg` (`check --staged`) and an opt-in `pre-push` (runs `pre_push_command` in a clean checkout of every pushed commit). Run `provenance lint` or `provenance check --staged` yourself to see a failure before committing.

## Scope enforcement and when you are blocked

The commit check validates your changed files **only against the record you tag**. Other records that happen to govern the files do not block you.

- Empty `affected_scope` = **observed**: any file is allowed except `forbidden_scope`. Non-empty = **allowlist**: only matching files.
- A "scope violation" on your tagged record means its `affected_scope` misses one of your files. Widen that record: `provenance edit <id> --add-scope <file> --reason "..."`, or `provenance add-scope --record <id>` for files already committed under it.

| Message | Do this |
|---------|---------|
| `Commit tag required but no provenance ID found` | Tag the commit with your record `[prov-YYYY-XXXXXXXX]`. |
| `<id> is implemented: run 'provenance revise <id>'` | Start a new version with `provenance edit <id> --reason "..."`, then tag the commit with that version. |
| `forbids changes to <file>` | Add `<file>` to your tagged record's `affected_scope`. |
| `No associated specs (open)` | Create the proof file, add it to `associated_specs`, then `provenance edit <id> --refresh-specs --reason "..."` if the record is already open. |
| `Version manifest says version N ...` (PROV026) | Run `provenance compile`. |
| `Content changed since version N without a version bump` (PROV024) | A spec file changed: `provenance edit <id> --refresh-specs --reason "..."`. |
| `permission denied` writing a source file | Not a bug: see the write lock below. Never `chmod`. |

### Filesystem write lock

Enforcement also runs at the filesystem. Tracked files are read-only unless an **open** record's `affected_scope` covers them. The provenance directory, `.provenance.yml`, `.provenance/`, `exclude_paths` and every record's `associated_specs` paths are always writable; snapshot files under `provenance/.versions/<id>/*.yml` are always read-only. A blocked write means "declare this file in an open record". `provenance reconcile [--dry-run]` re-derives the permissions on demand; `open`, `complete`, `deprecate`, `revise` and `edit` run it for you. `write_restriction: false` in `.provenance.yml` turns it off.

## Staleness and evidence

- A child record is **stale** when its `implements_version` is behind its parent's version. `provenance stale` lists them; `provenance repin <id> --reason "..."` confirms a child against the newer parent version without a new version.
- `provenance check --evidence` reports, per implemented record, whether its evidence is fresh, stale (and why: content, specs, code, parent), unknown (code hash not available) or missing.
- If only the **code** moved (a regeneration), `provenance evidence refresh <id>` re-runs the specs and appends fresh evidence with no new version. It refuses when the record's own text or a spec changed: revise those.

## associated_specs: proof artifacts

Each entry has a required `path` (must exist), an optional `type`, and an optional `run_command` (run as `<run_command> <path>`, or with `{{path}}` substituted). Create the proof file first, reference it, then open. Pick a **stable** proof file: a spec that is a mutable config file (`.gitignore`, `.provenance.yml`) drifts the record every time it is edited. Give each record its own spec file where practical; a spec file shared by several records drifts all of them when it changes. Change a spec path with `provenance edit <id> --set-spec old=new[:run_command] --reason "..."`.

## Tier rules (enforced by lint)

- `brief` is top-level intent and cannot use `implements`.
- `blueprint` is a design decision and may implement a brief.
- `imprint` is an implementation record and implements exactly one tier up.
- `implements` resolves locally or through the cached `shared_repos` (`provenance sync` refreshes the cache; remote records are read-only).

## Hard rules

- Never use `--no-verify`. If a hook fails, fix the cause.
- Never relax `enforcement` (`strict` to `warn`/`none`) to get unblocked: that is a maintainer decision.
- Never complete the blueprint without user confirmation.
- Never hand-edit managed fields (see "Records are versioned").
- With several `.provenance.yml` files, pass `-c <path>` to target the right one.
- To move a v1 (linespec) repository to v2, use the `/provenance:migrate` skill (`provenance migrate --plan` first); never run `provenance migrate` unasked.

## Useful commands

```bash
provenance next [files...] [--json]
provenance status [--record <id>] | history <id> | diff <id> <v1> <v2> | stale
provenance create --title "..." --type <tier> --no-edit [--implements <id>]
provenance open --record <id>
provenance edit <id> --reason "..." [flags]      # revise <id> opens $EDITOR: humans only
provenance add-scope --record <id> [--dry-run]
provenance complete --record <id> [--force]
provenance deprecate --record <id> --reason "..."
provenance repin <id> --reason "..." [--to N]
provenance lint [--record <id>] [--warn|--info|--all] | check --staged | check --commit <sha> | check --range a..b | check --evidence
provenance evidence refresh <id> | --all [--reason "..."]
provenance run-specs --record <id>
provenance reconcile [--dry-run] | compile | sync [--force]
provenance hooks install | install-skills | install-plugin | init | migrate --plan
```
