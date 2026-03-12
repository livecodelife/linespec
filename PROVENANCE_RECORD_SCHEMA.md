# Provenance Record Schema Reference

---

## Field Summary

| Field | Type | Required | Mutable After Implemented | Set By |
|---|---|---|---|---|
| `id` | string | yes | no | CLI |
| `title` | string | yes | no | author |
| `status` | enum | yes | no (transitions only) | CLI |
| `created_at` | date | yes | no | CLI |
| `author` | string | yes | no | CLI |
| `intent` | string | yes | no | author |
| `constraints` | []string | no | no | author |
| `affected_scope` | []pattern | no | no (lock via CLI only) | author or CLI |
| `forbidden_scope` | []pattern | no | no | author |
| `supersedes` | string | no | no | author |
| `superseded_by` | string | no | no | CLI |
| `related` | []string | no | no | author |
| `associated_linespecs` | []string | no | no | author |
| `associated_traces` | []string | no | **yes** | author |
| `monitors` | []string | no | **yes** | author |
| `tags` | []string | no | no | author |

---

## Field Definitions

---

### `id`
**Type:** string  
**Required:** yes  
**Format:** `prov-YYYY-NNN` where `YYYY` is the four-digit year and `NNN` is a zero-padded sequence number scoped to the repository, incrementing from `001` each year.  
**Examples:** `prov-2025-001`, `prov-2025-031`, `prov-2026-001`  
**Set by:** CLI at creation time. Never authored manually.  
**Constraints:** Immutable after creation. Globally unique within the repository. Used as the reference target in `supersedes` and `related` fields of other records.

---

### `title`
**Type:** string  
**Required:** yes  
**Format:** Plain language, single line, no markup. Should complete the sentence "We decided to…"  
**Example:** `"Auth required on all todo mutation endpoints"`  
**Set by:** Author, optionally pre-populated via `--title` flag.  
**Constraints:** Immutable after `implemented`. No length limit enforced by schema, but lint warns if over 120 characters.

---

### `status`
**Type:** enum  
**Required:** yes  
**Default:** `open` (set by CLI at creation)  
**Allowed values:**

| Value | Meaning |
|---|---|
| `open` | Authorized but not yet fully implemented or proven |
| `implemented` | All associated LineSpecs exist on disk and the record has been explicitly completed via CLI |
| `superseded` | Replaced by a newer record. `superseded_by` is set. |
| `deprecated` | Closed without replacement, e.g. the governed feature was removed |

**Set by:** CLI only. Authors do not write to this field directly. Status transitions are:
- `open` → `implemented` via `linespec provenance complete`
- `open` → `superseded` automatically when another record's `supersedes` references this ID
- `open` → `deprecated` via `linespec provenance deprecate`
- `implemented` → `superseded` automatically when another record's `supersedes` references this ID

**Constraints:** `implemented` records are immutable except for `monitors` and `associated_traces`. No transition back from `superseded` or `deprecated`.

---

### `created_at`
**Type:** string  
**Required:** yes  
**Format:** ISO 8601 date: `YYYY-MM-DD`  
**Example:** `"2025-03-10"`  
**Set by:** CLI at creation time using the local system date. Never authored manually.  
**Constraints:** Immutable after creation.

---

### `author`
**Type:** string  
**Required:** yes  
**Format:** Git user email of the creating engineer, taken from `git config user.email` at creation time.  
**Example:** `"caleb@anomalyco.com"`  
**Set by:** CLI at creation time. Never authored manually.  
**Constraints:** Immutable after creation. Represents the engineer who authorized the decision, not necessarily the one who implements it.

---

### `intent`
**Type:** string  
**Required:** yes  
**Format:** Plain language block scalar. One or more sentences describing the purpose and goal of this decision. Written as if explaining to a future engineer why this change was authorized and what it is meant to achieve. Should be self-contained — readable without context from surrounding records.  
**Example:**
```yaml
intent: >
  All POST, PATCH, and DELETE operations on the todos resource must
  verify a valid bearer token against the user service before
  proceeding. Unauthenticated requests must return 401.
```
**Set by:** Author.  
**Constraints:** Immutable after `implemented`. Lint error if empty or whitespace-only.

---

### `constraints`
**Type:** []string  
**Required:** no  
**Format:** List of plain language statements. Each constraint describes a specific behavioral or architectural requirement that any implementation fulfilling this record must respect. These are the source of truth for what associated LineSpecs should prove.  
**Example:**
```yaml
constraints:
  - The user service must be called synchronously before any
    database write is attempted
  - Service degradation in user-service must result in 503,
    not silent failure or default behavior
```
**Set by:** Author.  
**Constraints:** Immutable after `implemented`. Optional but recommended — records with `intent` but no `constraints` are valid but produce a lint hint suggesting constraints be added.

---

### `affected_scope`
**Type:** []pattern  
**Required:** no  
**Format:** List of file path patterns, each of which may be one of three kinds:

| Kind | Prefix | Example |
|---|---|---|
| Exact | (none) | `src/controllers/todos_controller.rb` |
| Glob | (none, contains `*` or `?`) | `src/middleware/**` |
| Regex | `re:` | `re:src/tests/todos_.*\.rb` |

All paths are relative to the repository root. Glob patterns support `**` to match any number of path segments. Regex patterns use RE2 syntax (no backreferences).

**Scope mode** is derived from this field:
- If empty: **observed mode** — the CLI auto-populates this field from git diffs on tagged commits. No implicit forbidden scope is applied.
- If non-empty: **allowlist mode** — any file not matching a pattern in this list is implicitly forbidden for commits tagged to this record, as if it were listed in `forbidden_scope`.

**Example (allowlist mode):**
```yaml
affected_scope:
  - src/controllers/todos_controller.rb
  - src/middleware/**
  - re:src/tests/todos_.*\.rb
```

**Example (observed mode):**
```yaml
affected_scope: []
```

**Set by:** Author (allowlist mode) or CLI (observed mode auto-population and `lock-scope` command).  
**Constraints:** Immutable after `implemented`. In observed mode, the CLI appends to this field as commits are scanned — it never removes entries. Transition from observed to allowlist mode is only available while status is `open`, via `linespec provenance lock-scope`.

---

### `forbidden_scope`
**Type:** []pattern  
**Required:** no  
**Format:** Same pattern format as `affected_scope` — exact paths, globs, and `re:`-prefixed regex.  
**Example:**
```yaml
forbidden_scope:
  - src/billing/**
  - src/models/user.rb
  - re:.*_migration\.rb$
```
**Set by:** Author.  
**Behavior:** Always enforced regardless of scope mode. In allowlist mode, `forbidden_scope` entries are additive — they call out specific files for explicit human-readable emphasis even when those files are already implicitly forbidden by the allowlist. In observed mode, they are the primary enforcement mechanism — "I don't know everything I'll touch, but I know these files must not be touched."  
**Constraints:** Immutable after `implemented`. A file may not appear in both `affected_scope` and `forbidden_scope` — the linter raises an error if a pattern in one set could match a pattern in the other.

---

### `supersedes`
**Type:** string  
**Required:** no  
**Format:** A single valid Provenance Record ID: `prov-YYYY-NNN`  
**Example:** `supersedes: prov-2025-019`  
**Set by:** Author at creation time, optionally pre-populated via `--supersedes` flag.  
**Behavior:** When this field is set, the CLI automatically sets `superseded_by` on the referenced record and transitions its status to `superseded`. The referenced record must exist and must not already have a `superseded_by` value pointing to a different record.  
**Constraints:** Immutable after creation. The referenced record must exist in the same repository or a configured shared repository. A record may supersede at most one other record. Circular supersedes chains are a lint error.

---

### `superseded_by`
**Type:** string  
**Required:** no  
**Format:** A single valid Provenance Record ID: `prov-YYYY-NNN`  
**Set by:** CLI only, automatically, when another record's `supersedes` references this record's ID. Authors never write to this field.  
**Constraints:** Immutable once set. The CLI reconstructs this value from the graph at load time and will surface a warning if the file's value disagrees with what the graph implies, indicating a manual edit.

---

### `related`
**Type:** []string  
**Required:** no  
**Format:** List of valid Provenance Record IDs.  
**Example:**
```yaml
related:
  - prov-2025-028
  - prov-2025-019
```
**Set by:** Author.  
**Behavior:** Informational only. Expresses a contextual relationship between records that is not a supersedes relationship — for example, two concurrent records that govern different parts of the same feature, or a record that provides background context for this one. No enforcement behavior. Rendered in `graph` output as dashed edges distinct from supersedes edges.  
**Constraints:** Immutable after `implemented`. Referenced records must exist (lint warning if not). A record should not list its own `supersedes` target in `related` — that relationship is already expressed structurally.

---

### `associated_linespecs`
**Type:** []string  
**Required:** no (enforcement depends on configured lint level)  
**Format:** List of file paths to `.linespec` files, relative to the repository root.  
**Example:**
```yaml
associated_linespecs:
  - todo-linespecs/create_todo_success.linespec
  - todo-linespecs/create_todo_unauthenticated.linespec
  - todo-linespecs/create_todo_unavailable_user_service.linespec
```
**Set by:** Author.  
**Behavior:** The linter checks that each listed path exists on disk. At `strict` enforcement, an `open` record with no entries here is a lint error. At `warn`, it is a warning. At `none`, no check is performed beyond verifying that listed paths exist if any are provided.  
**Constraints:** Immutable after `implemented`. The linter does not run the referenced LineSpecs — use `linespec test` for that. The linter only verifies file existence.

---

### `associated_traces`
**Type:** []string  
**Required:** no  
**Format:** List of free-form strings. May be URLs to TraceTest results, test run identifiers, or human-readable references.  
**Example:**
```yaml
associated_traces:
  - https://tracetest.example.com/runs/abc123
  - tracetest-run-2025-03-10-todos-auth
```
**Set by:** Author.  
**Behavior:** Informational only in v1. No enforcement behavior. Rendered in `status` output. Reserved for future enforcement where a record cannot be marked `implemented` without at least one associated trace.  
**Constraints:** **Mutable after `implemented`.** This is one of two fields that may be updated on an implemented record, since trace results are typically attached post-deployment.

---

### `monitors`
**Type:** []string  
**Required:** no  
**Format:** List of free-form strings. May be URLs to Datadog alerts, Grafana panels, PagerDuty policies, or any other operational reference.  
**Example:**
```yaml
monitors:
  - https://app.datadoghq.com/monitors/12345678
  - https://grafana.example.com/d/abc123/todos-auth-dashboard
```
**Set by:** Author.  
**Behavior:** Informational only. No enforcement behavior. Rendered in `status` output. Provides the link between a Provenance Record and the operational signals that indicate whether the implemented behavior is healthy in production.  
**Constraints:** **Mutable after `implemented`.** This is one of two fields that may be updated on an implemented record, since monitors are typically created or identified after implementation is deployed.

---

### `tags`
**Type:** []string  
**Required:** no  
**Format:** List of lowercase strings. No spaces. Hyphens allowed.  
**Example:**
```yaml
tags:
  - security
  - auth
  - todos
```
**Set by:** Author, optionally pre-populated via `--tag` flag.  
**Behavior:** Used for filtering in `status` and `graph` commands via `--filter tag:security`. No enforcement behavior.  
**Constraints:** Immutable after `implemented`.

---

## Complete Example

```yaml
id: prov-2025-031
title: "Auth required on all todo mutation endpoints"
status: open
created_at: "2025-03-10"
author: "caleb@anomalyco.com"

intent: >
  All POST, PATCH, and DELETE operations on the todos resource must
  verify a valid bearer token against the user service before
  proceeding. Unauthenticated requests must return 401. The user
  service is the sole authority on token validity.

constraints:
  - The user service must be called synchronously before any
    database write is attempted
  - Service degradation in user-service must produce a 503 response,
    not silent failure or fallback to a default authenticated state
  - Token validation must not be cached between requests

affected_scope:
  - src/controllers/todos_controller.rb
  - src/middleware/**
  - re:src/tests/todos_.*\.rb

forbidden_scope:
  - src/billing/**
  - src/models/user.rb

supersedes: prov-2025-019
superseded_by: null

related:
  - prov-2025-028

associated_linespecs:
  - todo-linespecs/create_todo_success.linespec
  - todo-linespecs/create_todo_unauthenticated.linespec
  - todo-linespecs/create_todo_unavailable_user_service.linespec

associated_traces: []

monitors:
  - https://app.datadoghq.com/monitors/12345678

tags:
  - security
  - auth
  - todos
```

---

## Validation Rules Summary

| Rule | Severity | Enforcement Level |
|---|---|---|
| File is valid YAML | error | always |
| All required fields present and non-empty | error | always |
| `status` is a known value | error | always |
| `id` matches `prov-YYYY-NNN` format | error | always |
| `created_at` is a valid ISO 8601 date | error | always |
| `supersedes` references a real record | error | always |
| `superseded_by` agrees with graph | warning | always |
| `related` references real records | warning | always |
| A pattern appears in both `affected_scope` and `forbidden_scope` | error | always |
| `open` record has no `associated_linespecs` | error | strict |
| `open` record has no `associated_linespecs` | warning | warn |
| `open` record has `constraints` but no `associated_linespecs` | hint | none |
| `open` record has `intent` but no `constraints` | hint | always |
| `title` exceeds 120 characters | warning | always |
| Circular supersedes chain detected | error | always |
| Scope overlap between two open records | warning | always |
| All files in `affected_scope` and `forbidden_scope` have been deleted | warning | always |
| `implemented` record has modified immutable fields | warning | always |
| Listed `associated_linespecs` paths do not exist on disk | error | always |
| Regex pattern fails to compile | error | always |
