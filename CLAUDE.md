# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build
make build               # Stable version (Provenance Records only)
make build-beta          # Beta version (includes LineSpec Testing)
make install             # Install stable to $GOPATH/bin
make install-beta        # Install beta to $GOPATH/bin

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
make lint                # Runs: ./linespec provenance lint
```

## Provenance Records

Every change must be documented by a provenance record. Records live in `provenance/` as YAML files using the format `prov-YYYY-XXXXXXXX.yml` (crypto-random hex, not sequential).

**Record lifecycle:** `draft` → `open` → `implemented` → `superseded|deprecated`

### Workflow

**1. Investigate first.** Before writing any code, check existing records:
```bash
linespec provenance search "<query>"        # semantic search (requires embeddings)
linespec provenance context -f <file>       # which records govern a file
```

**2. Create a blueprint record (draft).** This captures scope and success criteria:
```bash
linespec provenance create --title "..." --type blueprint --no-edit
```
Fill in `intent` and `constraints`. Leave `affected_scope` empty (observed mode) — the git hook's self-modification exception only applies to `open` records, so setting scope on a draft will block the creation commit. Commit the draft standalone, then **present it to the user and wait for confirmation** before writing any code.

**3. Open the blueprint (after user confirms).** Once the user approves:
```bash
linespec provenance open --record prov-YYYY-XXXXXXXX
```
Add `affected_scope` and `associated_specs` now. Commit the open transition standalone.

**4. Implement with imprint records.** Create `imprint` records as you work to log micro-decisions, trade-offs, pivots, and learnings:
```bash
linespec provenance create --title "..." --type imprint --no-edit
# Set in the YAML: implements: prov-YYYY-XXXXXXXX  (the blueprint ID)
```
Imprints can be freely opened, implemented, superseded, and deprecated as work evolves. **All imprints must be implemented before the blueprint can be completed.**

**5. Show proof and ask to complete.** Before completing the blueprint, verify all imprints are implemented, then show the user the proof (test output, lint, working commands). **Ask the user explicitly** before completing. Then complete in a standalone commit:
```bash
linespec provenance complete --record prov-YYYY-XXXXXXXX
```

### Commit Rules

- Every implementation commit must include the governing record ID: `[prov-YYYY-XXXXXXXX]`
- Create, open, and complete operations must each be standalone commits
- Before any provenance management commit: `linespec provenance lint && linespec provenance check`
- Before each implementation commit: `linespec provenance check --staged`
- **Never use `--no-verify`** to skip git hooks

## Architecture

LineSpec has two subsystems gated by the `beta` build tag:

**Stable** (`cmd/linespec/main_stable.go`): Provenance Records CLI only.

**Beta** (`cmd/linespec/main_beta.go`): Adds LineSpec Testing — a protocol-level integration testing DSL for containerized services. No modifications required to the service under test.

### Provenance Records (`pkg/provenance/`)

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

**Configuration** (`pkg/config/`) — loaded from `.linespec.yml`. Key sections: `service` (framework, port, start_command, health_endpoint), `database` (type, host, credentials), `infrastructure`, `container_naming` (Go template strings), `provenance`.

## Key Documentation

- `PROVENANCE_RECORDS.md` — Full schema reference and CLI usage
- `LINESPEC.md` — Beta DSL reference
- `CHANGELOG.md` — Breaking changes by version
