# Provenance Record Schema Reference

Schema 2 (versioned records), as read and written by the `provenance` CLI. For the workflow, see [PROVENANCE_RECORDS.md](./PROVENANCE_RECORDS.md).

A record is one YAML file at `provenance/<id>.yml`. An append-only snapshot store (`provenance/.versions/<id>/<version>-<hash>.yml`) is the version log, and `.provenance/versions.jsonl` is the manifest of `{id, version, content_hash}`. Records written by `linespec provenance` (schema 1) still decode, with lint hint PROV028, until `provenance migrate` converts them.

---

## Field Summary

| Field | Type | Required | Set by | Changing it |
|---|---|---|---|---|
| `id` | string | yes | CLI | never |
| `title` | string | yes | author | in place (does not bump the version) |
| `status` | enum | yes | CLI | transitions only |
| `type` | enum | no | author | no |
| `version` | integer | yes | CLI | bumped by `revise` / `edit` |
| `reason` | string | on every bump | author | with the version |
| `reason_kind` | `fix` | no | author (`--fix`) | with the version |
| `created_at` | ISO 8601 date | yes | CLI | no |
| `author` | string | yes | CLI | no |
| `intent` | string | yes | author | new version |
| `constraints` | list of strings | no | author | new version |
| `affected_scope` | list of strings | no | author / CLI | new version |
| `forbidden_scope` | list of strings | no | author | new version |
| `implements` | string | imprints | author (`--implements`) | new version |
| `implements_repo` | string | no | migration | new version |
| `implements_version` | integer | with `implements` | CLI | `repin` (no new version) |
| `aliases` | list of strings | no | migration | no |
| `related` | list of strings | no | author | new version |
| `sealed_at_sha` | string | no | CLI (`complete`) | never by hand |
| `associated_specs` | list of objects | to open | author | new version |
| `associated_traces`, `monitors` | list of strings | no | author | in place |
| `tags` | list of strings | no | author | in place |
| `locked` | boolean | no | author (`create --locked`) | no |

Removed from schema 1: `supersedes`, `superseded_by`, `extends`, the `superseded` status and the `bug` type.

---

## Field Details

### `id`

`prov-YYYY-XXXXXXXX`, random hex. Created by `provenance create`. With `--id-suffix <service>` a service suffix is appended for monorepos. Immutable, and unique within the repository.

### `status`

| Value | Meaning |
|---|---|
| `draft` | Planning. Nothing is enforced and every field is editable. The default for `create`. |
| `open` | Enforcement is on: commits tagged with the record are checked against its scope. |
| `implemented` | Sealed by `complete`: `sealed_at_sha` is set and verification evidence is recorded. |
| `deprecated` | Terminal. The record takes no new versions. |

Transitions: `draft → open` (`open`), `open → implemented` (`complete`), any live status `→ deprecated` (`deprecate`). Editing an implemented record's content creates a new version and returns it to `open`.

### `type`

| Value | Meaning | `implements` |
|---|---|---|
| `brief` | Business rationale. `affected_scope`, `forbidden_scope` and `associated_specs` do not apply (PROV031). | not allowed |
| `blueprint` | A design decision (default when `type` is absent). | optional: a brief |
| `imprint` | One unit of implementation or one micro-decision. `associated_traces` and `monitors` do not apply. | required: a blueprint |

### `version`, `reason`, `reason_kind`

`version` starts at 1 and increases by one each time `content_hash` changes. Every version after the first needs a `reason`. `reason_kind: fix` marks a version that fixes a defect. Hand-editing content without a bump is lint error PROV024; `provenance revise` or `edit` do it correctly.

Commit tags can name a version: `[prov-2026-1a2b3c4d@3]`.

### `content_hash`

Not stored in the record. The SHA-256 of a canonical encoding of the content fields (intent, constraints, scopes, `implements`, specs, tags, …) plus the content hash of each associated spec file. It excludes `status`, `sealed_at_sha`, `version` and `reason`. See the provenance repository's `docs/HASHING.md`. Avoid pointing an associated spec at a record file: migration and revision rewrite those files and change the hash.

### `intent`, `constraints`

`intent` is why the change exists. `constraints` are what must stay true; each should be provable by an associated spec (lint hints when constraints have no specs).

### `affected_scope`, `forbidden_scope`

File paths, glob patterns (`pkg/db/**`) or regexes (`/…/`).

- Empty `affected_scope` is **observed** mode: any file is allowed except `forbidden_scope`.
- Non-empty is **allowlist** mode: only matching files.
- A pattern in both lists is an error (PROV009). A path or pattern that matches nothing is flagged (PROV029); one declared for creation is a note.
- An imprint's scope must be inside its blueprint's (PROV023).

Widen a scope with `provenance edit <id> --add-scope … --reason …`, or `add-scope --record <id>` for files already committed under the record.

### `implements`, `implements_repo`, `implements_version`

`implements` is the parent record's id, exactly one tier up. It resolves locally or through a configured `shared_repos` cache (`implements_repo` names the shared repo; `provenance sync` fetches it; remote records are read-only). `implements_version` is the parent version the record was last confirmed against; `provenance create --implements <id>` sets it to the parent's current version. A record whose pin is behind its parent is **stale** (`provenance stale`); `provenance repin` confirms it without a new version.

### `aliases`

Old ids folded into this record by `provenance migrate`. A commit tag naming an alias resolves to this record.

### `related`

Informational links to other records. No enforcement. Unresolvable references are a warning.

### `sealed_at_sha`

The commit `complete` sealed the record at (7 to 40 hex characters). Set only for implemented or deprecated records.

### `associated_specs`

Proof artifacts. Each entry:

```yaml
associated_specs:
  - path: pkg/db/db_test.go        # required; must exist on disk
    type: go_test                  # optional: code, go_test, unit, rspec, …
    run_command: go test ./pkg/db/... -run TestPool
```

- An `open` record needs at least one entry at `strict` enforcement (error), `warn` (warning).
- `complete` runs each spec when `run_associated_specs_on_complete: true` and records evidence. `run_command` is run as `<run_command> <path>`, or with `{{path}}` substituted. A spec with a `type` but no `run_command` is skipped silently.
- Editing a spec file changes the record's content hash: record it with `provenance edit <id> --refresh-specs --reason "…"`.
- Change a spec path with `edit --set-spec old=new[:run_command]`, add with `--add-spec`, remove with `--remove-spec`.

### `associated_traces`, `monitors`, `tags`

Free-form metadata. Traces and monitors anchor at the blueprint tier. They change in place, without a version.

### `locked`

A locked record is a governance layer: lint warns when another record's scope overlaps a locked record's surface (`create --locked`).

---

## Complete Example

```yaml
id: prov-2026-1a2b3c4d
title: Add rate limiting to the public API
status: implemented
version: 2
reason: Tighten the per-key limit after the load test
reason_kind: fix
created_at: "2026-10-01"
author: you@example.com
intent: |
  Public endpoints must be rate limited so one key cannot starve the others.
constraints:
  - Limits are per API key, not per IP
  - A throttled request returns 429 with a Retry-After header
affected_scope:
  - pkg/api/ratelimit/**
forbidden_scope:
  - pkg/api/auth/**
type: blueprint
aliases:
  - prov-2026-9e8d7c6b
related: []
sealed_at_sha: 4f3c2b1a9d8e7f60
associated_specs:
  - path: pkg/api/ratelimit/ratelimit_test.go
    type: go_test
    run_command: go test ./pkg/api/ratelimit/... -run TestRateLimit
tags:
  - api
```

An imprint of it:

```yaml
id: prov-2026-5e6f7a8b
title: Token bucket for the limiter
status: open
version: 1
type: imprint
implements: prov-2026-1a2b3c4d
implements_version: 2
affected_scope:
  - pkg/api/ratelimit/bucket.go
```

---

## Validation Rules Summary

`provenance lint` (`--format human|json|sarif`) reports rule ids that are stable for alerting. The important ones:

| Rule | Severity | Meaning |
|---|---|---|
| PROV001-PROV005 | error | Valid YAML; required fields; known status; `id` format; `created_at` is a date |
| PROV008 | warning | `related` references a record that does not exist |
| PROV009 | error | A pattern is in both `affected_scope` and `forbidden_scope` |
| PROV010, PROV011, PROV012 | error / warning / hint by enforcement | An open record has no `associated_specs`; constraints without specs; intent without constraints |
| PROV013 | warning | Title is too long |
| PROV015 | warning | Two open records share scope |
| PROV016 | warning | Every path in the scope has been deleted |
| PROV018, PROV019 | error | A spec path does not exist; a scope pattern is invalid |
| PROV021, PROV022 | error | `implements` skips a tier or points sideways; the parent cannot be found |
| PROV023 | error | An imprint's scope is wider than its blueprint's |
| PROV024 | error | Content changed since the version without a bump (`provenance revise`) |
| PROV025 | error | A version after the first has no `reason` |
| PROV026 | error | Version snapshots are missing, not consecutive, do not verify, or disagree with the manifest (`provenance compile`) |
| PROV027 | error | `implements_version` is invalid |
| PROV028 | hint | A schema 1 record: run `provenance migrate` |
| PROV029 | warning / note | A scope path or pattern matches nothing |
| PROV030, PROV031 | error / warning | Unknown `type`; a field that does not apply to that tier |
| PROV032 | warning | `sealed_at_sha` is malformed or set on a record that is not implemented |
| PROV033 | warning | A record overlaps a locked layer |

Rules PROV006, PROV007, PROV014 and PROV020 (the v1 supersession and hash-immutability checks) are retired: versions replace supersession, and snapshots replace the hash manifest.
