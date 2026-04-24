# Provenance Hierarchy Update — Agent Work Chunks

---

## Chunk 1 — Schema: New Fields and Draft Status ✓ COMPLETE

**Goal:** Extend the Provenance Record struct and YAML schema to support `type`, `implements`, and `draft` status. This is the foundation everything else depends on.

**What to build:**

Extend the Go struct for a Provenance Record with the following additions:

- A `type` field as a string enum with allowed values `brief`, `blueprint`, and `imprint`. Required on all new records. The CLI should set this at creation time via a `--type` flag on `linespec provenance create`. Records without a `type` field should be treated as `blueprint` for backward compatibility and surfaced with a lint warning.

- An `implements` field as a nullable string holding a single Provenance Record ID in the standard `prov-YYYY-NNN` format. Optional. Represents an upward parent reference — a Blueprint implementing a Brief, or an Imprint implementing a Blueprint.

- A `draft` status value added to the existing `status` enum alongside `open`, `implemented`, `superseded`, and `deprecated`. `draft` is the new default status set by `linespec provenance create`. No enforcement rules (scope, linespecs) apply while a record is in `draft` status. A new CLI command `linespec provenance open --record prov-YYYY-NNN` transitions a record from `draft` to `open`, which is when enforcement begins.

**Acceptance criteria:**

The existing YAML parsing must continue to work for all current records. A record with no `type` field parses successfully with a backward-compat default and a lint hint. A record with `type: brief`, `type: blueprint`, or `type: imprint` parses cleanly. The `implements` field parses as a nullable string and is omitted from YAML output when null. The `draft` status value is accepted by the status enum without error. The `linespec provenance create` command accepts `--type brief|blueprint|imprint` and writes the value to the scaffolded file. The `linespec provenance open` command transitions `draft` to `open` and rejects any other starting status with a clear error message.

**Does not include:** Lint enforcement of cross-tier rules, graph traversal changes, cross-repo resolution. Those are subsequent chunks.

---

## Chunk 2 — Lint: Cross-Tier and Supersession Type Enforcement ✓ COMPLETE

**Goal:** Add lint rules that enforce the structural constraints introduced by the tier hierarchy. Depends on Chunk 1.

**What to build:**

Three new lint rules, each with a clear error message and resolution hint:

First, **supersession type enforcement.** When a record's `supersedes` field is set, the CLI must resolve the referenced record and verify that its `type` field matches the type of the superseding record. A Blueprint attempting to supersede a Brief is a lint error. The error message should say which types were involved and direct the author to use `related` or `implements` for cross-tier relationships instead.

Second, **implements type enforcement.** When a record's `implements` field is set, the CLI must resolve the referenced record and verify that the referenced record's type is exactly one tier above. An Imprint implementing a Brief (skipping the Blueprint tier) is a lint error. A Blueprint implementing another Blueprint is a lint error. The allowed relationships are: `blueprint` implements `brief`, and `imprint` implements `blueprint`. The error message should name the invalid relationship and the expected one.

Third, **implements resolution.** The referenced record in `implements` must exist in the local provenance directory (cross-repo resolution comes in a later chunk). A missing reference is a lint error regardless of enforcement level, since it represents a broken graph edge. Surface this the same way `supersedes` resolution failures are surfaced today.

**Acceptance criteria:**

`linespec provenance lint` reports a type-mismatch error when `supersedes` crosses tiers. It reports a type-mismatch error when `implements` skips a tier or points sideways. It reports a missing-record error when `implements` cannot be resolved locally. All three errors include a plain-language hint. All three are always-on regardless of configured enforcement level, since they represent graph integrity violations.

**Does not include:** Cross-repo resolution of `implements` references (that's Chunk 5-6). For now, `implements` references that use a repo prefix (if any) should be skipped with a warning noting cross-repo resolution is not yet supported.

---

## Chunk 3 — CLI: Status and Graph Output for Implements

**Goal:** Update `linespec provenance status` and `linespec provenance graph` to display and traverse `implements` relationships. Depends on Chunk 1.

**What to build:**

In `linespec provenance status --record prov-YYYY-NNN`, add two new display sections:

- **Implements:** shows the parent record ID and title if `implements` is set, or a dash if not.
- **Implemented by:** shows a list of any records in the local provenance directory whose `implements` field points to this record's ID, along with their type and title. This is derived by scanning all records at load time, not stored explicitly.

In `linespec provenance graph`, the existing supersession chain rendering should be extended to show the implements hierarchy as a separate structural dimension. When traversing the graph, records connected by `implements` should render as a parent-child indentation, visually distinct from the supersession chain (which shows horizontal evolution within a tier). A brief with two blueprints implementing it, each with imprints below them, should render as a tree. Supersession chains within a tier should be shown inline at that tier's level.

The `--root` flag on `linespec provenance graph` should work correctly when the root is any tier — passing a Brief ID should show the full downward tree of Blueprints and Imprints that implement it. Passing a Blueprint ID should show its parent Brief (one level up) and its child Imprints (downward).

The `--format json` output should include an `implements` edge type distinct from `supersedes` and `related` edge types, so the commercial visualization layer can render them with different visual treatments.

**Acceptance criteria:**

`status --record` displays parent and children cleanly, with dashes when relationships are absent. `graph` renders a readable tree when implements relationships exist. `graph --root` on a Brief traverses the full downward tree. JSON output distinguishes edge types. All output degrades gracefully when implements relationships are absent, matching current behavior exactly.

---

## Chunk 4 — Cross-Repo Config and Cache Infrastructure

**Goal:** Build the configuration schema for named remote repositories and the local cache layer that fetches and stores remote provenance directories. This chunk contains no user-facing CLI changes — it is pure infrastructure consumed by Chunk 5.

**What to build:**

Extend `.linespec.yml` to support a `shared_repos` array under the `provenance` key:

```yaml
provenance:
  shared_repos:
    - name: product
      url: https://gitlab.com/org/product-decisions
      ref: main
    - name: platform
      url: https://gitlab.com/org/platform-decisions
      ref: main
```

The `name` value is a short alias used to identify the repo in log output and future reference disambiguation. The `url` is a git-cloneable remote URL. The `ref` defaults to `main` if omitted.

Build a cache layer that stores fetched provenance directories at `~/.linespec/cache/<sha256-of-url>/provenance/`. Each cached file stores a `fetched_at` timestamp alongside it (a simple sidecar `.meta` file or a single cache manifest JSON is fine). The TTL for cache freshness is configurable in `.linespec.yml` under `provenance.cache_ttl_minutes`, defaulting to 60.

The fetch mechanism must use `git archive --remote=<url> <ref> provenance/` to retrieve only the provenance directory from the remote, not a full clone. This keeps the operation fast even for large repositories. The fetched archive is unpacked into the cache directory, replacing any previous contents for that repo.

Build a `linespec provenance sync` command that explicitly refreshes the cache for all configured shared repos, regardless of TTL. This command is intended for use in CI and as a `SessionStart` hook entry for agents. Output should report success or failure per repo clearly:

```
✓ Synced product (gitlab.com/org/product-decisions) — 14 records
✓ Synced platform (gitlab.com/org/platform-decisions) — 6 records
```

**Acceptance criteria:**

`.linespec.yml` parses `shared_repos` without error. The cache directory is created at the correct path. `linespec provenance sync` fetches and caches provenance directories from all configured repos using `git archive`. A second run within the TTL window skips the fetch and reports the cache as fresh. A run after the TTL expires re-fetches. Cache contents survive across CLI invocations. The `git archive` command failure (unreachable remote, bad ref) is caught and reported as a clear error without crashing the CLI.

**Does not include:** Any resolution of cross-repo references in lint or graph commands. That wires the cache into the rest of the system and is Chunk 5.

---

## Chunk 5 — Cross-Repo Reference Resolution

**Goal:** Wire the cache infrastructure from Chunk 4 into the lint and graph commands so that `implements` and `related` references pointing to records in configured shared repos resolve correctly. Depends on Chunks 2, 3, and 4.

**What to build:**

Extend the record resolver — the function the CLI uses to look up a record by ID — to check the local cache for each configured shared repo when a record is not found locally. The resolution order should be: local provenance directory first, then each configured shared repo's cache in the order they are declared in `.linespec.yml`. The first match wins. If no match is found anywhere, the existing missing-record error is surfaced.

When a record is resolved from the cache, it should be treated identically to a local record for read purposes — its fields are available, its type can be checked, its title can be displayed. It must not be writable — any command that would modify a record (complete, deprecate, open) must reject a cache-sourced record with an error explaining that the record is owned by a remote repository.

Update the lint warning from Chunk 2 that skipped cross-repo `implements` references. Those references should now resolve correctly if the referenced repo is configured. If the repo is not configured, the warning should be more precise: "implements references prov-YYYY-NNN which was not found locally or in any configured shared_repo. If this record lives in a remote repository, add it to shared_repos in .linespec.yml."

Update the graph traversal from Chunk 3 to follow `implements` edges across repo boundaries using the resolver. A Brief in the product repo with two Blueprints implementing it in a service repo should render as a single connected tree in `linespec provenance graph`.

Add a stale cache warning surfaced by `linespec provenance lint` when the cache for any configured shared repo is older than the configured TTL. The warning should suggest running `linespec provenance sync`.

**Acceptance criteria:**

An `implements` reference to a record in a configured shared repo resolves correctly after `linespec provenance sync` has been run. A lint error is not raised for a valid cross-repo `implements` reference. The graph traversal shows the full tree across repo boundaries. Write commands reject cache-sourced records with a clear error. A stale cache produces a warning in lint output. An unconfigured repo produces a precise, actionable error message.

---

## Sequencing Notes

Chunks 1 through 3 form a self-contained deliverable that improves the local single-repo experience and can ship independently. Chunks 4 and 5 build the cross-repo layer on top and depend on the type system from Chunk 1 being in place, but are otherwise independent of Chunks 2 and 3. If you have capacity to run two agents in parallel, Chunks 2-3 and Chunks 4-5 can proceed concurrently after Chunk 1 is complete.

Each chunk's acceptance criteria is written to be verifiable by the agent itself through a combination of `linespec provenance lint`, `linespec provenance status`, and `linespec provenance graph` against a small fixture set of YAML records covering the relevant cases.
