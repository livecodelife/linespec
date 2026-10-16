# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build (Provenance Records + LineSpec Testing — both included by default)
make build               # Build ./linespec with all features
make install             # Install to $GOPATH/bin
# `make build-beta` / `make install-beta` remain only as backward-compatible
# aliases; they produce the same binary as `make build` (identical since v2.0.0).

# Test
make test                # Unit tests only (go test ./...)
make test-quick          # Short unit tests (pre-commit)
make test-integration    # Full integration suite (requires Docker)
make test-integration-mysql     # MySQL proxy tests (requires Docker, port 3307)
make test-integration-postgres  # PostgreSQL proxy tests (requires Docker, port 5433)

# Single package test
go test ./pkg/proxy/postgresql/... -v
go test -tags integration ./pkg/proxy/postgresql/... -v

# Lint
make lint                # Runs: provenance lint (the standalone CLI; install it first, see below)

# Code quality (Go)
make quality             # staticcheck (hard) + deadcode (hard) + dupl (warn)
make install-hooks-quality  # Install the pre-push code-quality hook
```

## Code Quality

A code-quality gate keeps the codebase compact by blocking dead code and lint
regressions before they are pushed. It runs as a **pre-push** git hook and as
the `code-quality` CI job; both delegate to the single script
`scripts/go-quality.sh`.

- **staticcheck** `./...` — hard gate; any finding blocks the push.
- **deadcode** — hard gate, but only on functions unreachable in *every* build
  configuration. The script runs deadcode across `default`, `-test`,
  `-test -tags beta`, `-test -tags integration`, and
  `-test -tags 'integration beta'`, then reports the intersection. A function
  used only by tests or only under a build tag is therefore treated as live,
  not dead. Remove genuinely-dead functions (or wire up a caller) to pass.
- **dupl** — warn only; reports copy-paste duplication but never blocks.

Run `make install-hooks-quality` once per clone/worktree to install the
pre-push hook (`.git/hooks` is not version-controlled). The hook is separate
from the linespec-managed pre-commit/commit-msg hooks, so `linespec
install-hooks` never overwrites it. Missing tools are installed automatically
via `go install`.

Env overrides for `scripts/go-quality.sh`: `QUALITY_DEADCODE=warn` (downgrade
deadcode to warn-only), `QUALITY_SKIP_DUPL=1` (skip duplication),
`DUPL_THRESHOLD=<n>` (dupl token threshold, default 75).

## Provenance Records

Every change must be documented by a provenance record. Records live in `provenance/` as YAML files using the format `prov-YYYY-XXXXXXXX.yml` (crypto-random hex, not sequential). They are managed by the standalone **`provenance` CLI** ([github.com/livecodelife/provenance](https://github.com/livecodelife/provenance)), configured by `.provenance.yml`. Install it pinned to the full commit SHA that CI uses (see `PROVENANCE_SHA` in `.github/workflows/ci.yml`):

```bash
go install github.com/livecodelife/provenance/cmd/provenance@<commit-sha>
provenance hooks install   # once per clone/worktree; keeps existing hooks as .bak
```

`linespec provenance <command>` still forwards to the `provenance` binary (with a deprecation notice), but use `provenance` directly. `audit`, `discover`, `generate`, `index`, `lock-layer`, `lock-scope`, `publish` and `install-hooks` have no v2 counterpart and do not work on this (migrated) repo.

**Record lifecycle:** `draft` → `open` → `implemented` → `deprecated`. There is no `superseded` status and no `bug` type: a change to a record is a new **version** of it (`provenance revise` / `edit`, with a `--reason`), and a fix is a version marked `--fix`.

### Workflow

**1. Investigate first.** Before writing any code, check existing records:
```bash
provenance search "<query>"        # offline lexical search
provenance context <file>          # which records govern a file
provenance next <file>             # the computed next action for files you'll change
```
`next` renders the correct next step (create / open / add spec / commit / complete) with record IDs filled in — touching a governed file does **not** require a new record or a new version. `govern <file>` lists just the active (open + implemented) records governing a file.

**2. Create a blueprint record (draft).** This captures scope and success criteria:
```bash
provenance create --title "..." --type blueprint --no-edit
```
Fill in `intent`, `constraints`, and `affected_scope` as needed. Draft mode is flexible — add, remove, and adjust fields freely while planning. Commit the draft standalone, then **present it to the user and wait for confirmation** before writing any code.

**3. Open the blueprint (after user confirms).** Once the user approves:
```bash
provenance open --record prov-YYYY-XXXXXXXX
```
Add `associated_specs` before opening (a record cannot open without one; give `type: code`/`go_test` specs a `run_command` such as `make test`, or they are skipped silently). Change an open record with `provenance edit <id> --reason "..." [--add-scope ... --add-spec path:cmd]`. Commit the open transition standalone.

**4. Implement with imprint records.** Create `imprint` records as you work to log micro-decisions, trade-offs, pivots, and learnings:
```bash
provenance create --title "..." --type imprint --implements prov-YYYY-XXXXXXXX --no-edit
```
`--implements` pins the blueprint's current version. An imprint's scope must stay inside the blueprint's (PROV023). **All imprints must be implemented before the blueprint can be completed.**

**5. Show proof and ask to complete.** Before completing the blueprint, verify all imprints are implemented, then show the user the proof (test output, lint, working commands). **Ask the user explicitly** before completing. Then complete in a standalone commit:
```bash
provenance complete --record prov-YYYY-XXXXXXXX
```

**When a decision changes**, revise the record instead of creating a new one, and tag the commit with the new version: `provenance revise <id> --reason "..."` → `[prov-YYYY-XXXXXXXX@2]`. After a parent changes, `provenance stale` lists children whose `implements_version` is behind; revise each or `provenance repin <id> --reason "..."`.

### Commit Rules

- Every implementation commit must include the governing record ID: `[prov-YYYY-XXXXXXXX]` (or with the version: `[prov-YYYY-XXXXXXXX@N]`)
- Create, open, and complete operations must each be standalone commits
- Before any provenance management commit: `provenance lint && provenance check`
- Before each implementation commit: `provenance check --staged`
- **Never use `--no-verify`** to skip git hooks
- Draft records are for planning — freely edit all fields including `affected_scope` in draft mode
- Never hand-edit `status`, `version`, `sealed_at_sha`, `provenance/.versions/`, or `.provenance/versions.jsonl`; `provenance compile` rebuilds the manifest

**Completion-time overlap teeth.** When a record is completed, the sealed records whose scope it actually touches have their `associated_specs` run. `overlap_specs_on_complete` in `.provenance.yml` sets severity — `block` (roll back on failure), `warn` (failure is a non-blocking FYI), or `off` (skip). This repo runs `warn` because completing files like `commands.go`/`main_*.go` pulls in the whole `examples/*-linespecs` Docker matrix, which is environmentally fragile. `complete --force` does not bypass the teeth.

**Claude Code plugin.** `provenance install-plugin` installs hooks that render this guidance in-loop (session-start `next`, per-edit `govern`, commit-rejection remediation) plus a `/provenance-next` command. The plugin and skill in `plugins/provenance/` and `skills/provenance/` are copies of the `assets/` of the pinned provenance commit.

## Architecture

LineSpec has two subsystems, both included in the default build (as of v2.0.0):

**Provenance Records** (`cmd/linespec/provenance_forward.go`): the standalone `provenance` CLI owns them; linespec only forwards `linespec provenance <command>` to it and still carries the v1 implementation (`pkg/provenance/`) for the commands that have no v2 counterpart.

**LineSpec Testing** (`cmd/linespec/main_beta.go`): a protocol-level integration testing DSL for containerized services — no modifications required to the service under test. The `beta` build tag and `main_beta.go` filename are legacy: `make build` includes LineSpec Testing, and `make build-beta` is an identical alias.

### Legacy v1 Provenance Records (`pkg/provenance/`)

Still built, but only reached for `audit`, `discover`, `generate`, `index`, `lock-layer`, `lock-scope`, `publish` and `install-hooks`, which read v1 records and so do not work on this repo.

- `commands.go` — All CLI operations (create, lint, status, graph, check, complete, deprecate, search, audit, index)
- `loader.go` — Reads `.yml` records from the configured directory
- `linter.go` — Validates records against schema
- `git.go` — Pre-commit and commit-msg hook installation and enforcement
- Semantic search via Voyage AI (`pkg/embeddings/`) — index stored in `.linespec/embeddings.bin`

### LineSpec Testing (`pkg/runner/`, `pkg/proxy/`, `pkg/dsl/`)

**Test execution flow:**

1. `linespec test <path>` parses `.linespec` DSL files
2. Runner starts Docker containers (database, Kafka, proxy sidecars)
3. Service under test is built and started inside a container
4. For each test: registry is cleared → HTTP call made to service → EXPECT interactions verified
5. Containers are cleaned up

**Proxy system** — intercepts traffic at the wire protocol level between the service and its dependencies:

- `pkg/proxy/postgresql/` — PostgreSQL wire protocol (startup handshake, query/response cycle)
- `pkg/proxy/mysql/` — MySQL wire protocol
- `pkg/proxy/http/` — HTTP request/response interception
- `pkg/proxy/kafka/` — Kafka message interception
- `pkg/proxy/base/connection.go` — Shared connection manager abstraction

The PostgreSQL proxy connects upstream **first** (before any client communication), then uses transparent `io.Copy` bidirectional relay during the startup/auth phase. After startup, it switches to intercepting individual messages to match against the mock registry.

**Registry** (`pkg/registry/registry.go`) — stores mock responses keyed by query/request. A verification sidecar HTTP server (port 8081) exposes `/verify` for checking which mocks were hit.

**DSL** (`pkg/dsl/`) — lexer/parser for `.linespec` files. Channels: `READ:POSTGRESQL`, `WRITE:POSTGRESQL`, `HTTP:GET`, `EVENT:kafka-topic`. Payloads loaded from YAML/JSON files via `{{payload.yaml}}` syntax.

**Configuration** (`pkg/config/`) — loaded from `.linespec.yml`. Key sections: `service` (framework, port, start_command, health_endpoint), `database` (type, host, credentials), `infrastructure`, `container_naming` (Go template strings). Provenance settings live in `.provenance.yml`, not here.

## Updating the Docs Site

The published site at [linespec.dev](https://linespec.dev) is built **from source**
by `scripts/build-docs.sh` and deployed by `.github/workflows/pages.yml` on every
push to `main`. Markdown is the single source of truth — the rendered HTML and
`llms*.txt` are gitignored build artifacts.

**Never hand-edit `docs/*.html` or `docs/llms*.txt`.** They are regenerated on
every push and your changes will be overwritten. Edit the source instead:

- **Content pages** (`provenance.html`, `linespec.html`) — edit the Markdown:
  `PROVENANCE_RECORDS.md` and `LINESPEC.md`. Pandoc renders them with
  `--from gfm`, so in-page anchors match the Markdown's own TOC links. If you add
  a cross-page link, target the **rendered gfm anchor** (e.g.
  `provenance.html#core-concepts`, not `#concepts`).
- **Chrome pages** (`index.html` landing, `docs.html` hub) and the curated
  `llms.txt` — edit their committed templates under `docs/templates/`. Use the
  `__VERSION__` token wherever the version appears; the build substitutes it from
  `VERSION`.
- **Page shell / design** — `docs/templates/content.html` (pandoc shell),
  `docs/templates/site-filter.lua` (code-block markup + sidebar nav), and
  `docs/assets/` (CSS/JS).
- **Version bump** — `scripts/bump-version.sh <new-version>` edits only `VERSION`
  and the source `.md` files; the version flows into the site at build time.

**Preview locally before pushing** (output is byte-identical to CI):

```bash
brew install pandoc        # one-time; CI installs it via apt
./scripts/build-docs.sh    # renders docs/*.html and docs/llms*.txt
open docs/index.html
```

As with any change in this repo, wrap docs edits in a provenance record and tag
the commit with its ID.

## Key Documentation

- `PROVENANCE_RECORDS.md` — Full schema reference and CLI usage
- `LINESPEC.md` — LineSpec Testing DSL reference
- `CHANGELOG.md` — Breaking changes by version
