# LineSpec v3.27.0

[![Version](https://img.shields.io/badge/version-3.27.0-blue.svg)](https://github.com/livecodelife/linespec/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/livecodelife/linespec)](https://goreportcard.com/report/github.com/livecodelife/linespec)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

**Provenance Records** - Structured YAML artifacts for documenting architectural decisions  
**LineSpec Testing** - DSL-based integration testing for containerized services

---

## Overview

LineSpec is a tool for managing **Provenance Records** - structured decision documents that capture the intent, constraints, and reasoning behind architectural changes - and for **LineSpec Testing**. Provenance Records are managed by the standalone [`provenance`](https://github.com/livecodelife/provenance) CLI; `linespec provenance <command>` forwards to it and is deprecated.

As of v3.27.0, **LineSpec Testing** is stable and included in the default installation alongside Provenance Records.

---

## Installation

### Homebrew (Recommended)

```bash
brew tap livecodelife/linespec
brew install linespec
```

No extra step is needed for LineSpec Testing. Protocol proxy sidecars run
`ghcr.io/livecodelife/linespec`, pinned to your binary's version and pulled
automatically the first time you run `linespec test`.

### Go Install

```bash
go install github.com/livecodelife/linespec/v3/cmd/linespec@v3.27.0
```

### GitHub Releases

Download pre-built binaries from the [releases page](https://github.com/livecodelife/linespec/releases).

- `linespec_3.27.0_*` - Full release (Provenance Records + LineSpec Testing)

---

## Quick Start - Provenance Records (Stable)

```bash
# 1. Install the provenance CLI (pin to a full commit SHA; there is no release tag yet)
go install github.com/livecodelife/provenance/cmd/provenance@<commit-sha>

# 2. Set up your repo: config, records directory, git hooks
provenance init
provenance hooks install

# 3. Ask what to do next, then create your first provenance record
provenance next
provenance create --title "Add user authentication" --type blueprint --no-edit

# 4. View the record graph
provenance graph

# 5. Validate all records
provenance lint
```

Upgrading a repo that has v1 records (`.linespec.yml` with a `provenance:` block)? Run `provenance migrate --plan`, then `provenance migrate`; see [Migrating from `linespec provenance`](./PROVENANCE_RECORDS.md#migrating-from-linespec-provenance).

### Example Provenance Record

```yaml
id: prov-2026-1a2b3c4d
title: "Use PostgreSQL for primary data store"
status: open
version: 1
created_at: "2026-03-15"
author: "dev@example.com"

intent: >
  After evaluating options, we choose PostgreSQL for our primary
  data store due to better JSON support and concurrent write handling.

constraints:
  - All new tables must use PostgreSQL
  - Use connection pooling (min 10, max 100)

affected_scope:
  - pkg/db/**
  - migrations/**

associated_specs:
  - path: tests/db/postgres_integration_spec.rb
    type: rspec

tags:
  - architecture
  - database
```

**[Complete Provenance Records Reference →](./PROVENANCE_RECORDS.md)**

---

## Feature Overview

| Feature | Commands | Status |
|---------|----------|--------|
| **Provenance Records** | `provenance` | ✅ Stable |
| **LineSpec Testing** | `test`, `proxy` | ✅ Stable |

---

## Provenance Records - Full Feature Set

### CLI Commands

```bash
# Record management
provenance create            # Create new record (always pass --no-edit in scripts)
provenance open              # draft -> open: start enforcement
provenance edit / revise     # Change a record: a new version with a reason
provenance lint              # Validate records
provenance status            # View status
provenance graph             # Render the record graph
provenance history / diff    # Read a record's versions

# Agent guidance
provenance next              # Compute the correct next provenance action
provenance govern            # List the active records governing given files
provenance context           # Show which records govern given files
provenance search            # Offline lexical search over records

# Git integration
provenance check             # Check commits for violations
provenance hooks install     # Install git hooks
provenance install-skills    # Install the Claude Code provenance skill
provenance install-plugin    # Install the Claude Code provenance plugin (hooks + slash command)

# Lifecycle management
provenance complete          # Mark as implemented (runs specs, records evidence)
provenance stale / repin     # Find and confirm records behind their parent
provenance deprecate         # Mark as deprecated

# Shared repos and upkeep
provenance sync              # Fetch records of shared repos
provenance migrate           # Convert v1 records to v2

# Output formats
provenance lint --format json     # JSON output for CI
provenance lint --format sarif    # SARIF output for GitHub Code Scanning
```

`linespec provenance <command>` still works for each command above: it forwards to the `provenance` binary with a deprecation notice. `audit`, `discover`, `generate`, `index`, `lock-layer`, `lock-scope`, `publish` and `install-hooks` have no v2 counterpart yet; they stay in `linespec` and do not work on a migrated repository. `linespec clone` and `linespec import` are unchanged.

### Key Features

- **Structured YAML format** - Clear, version-controlled decision records
- **Versioned records** - a change is a new version with a reason; imprints pin the parent version they implement, and `provenance stale` flags the ones that fell behind ([details](./PROVENANCE_RECORDS.md#core-concepts))
- **Path exemptions** - `exclude_paths` exempts generated/vendored files from governance ([details](./PROVENANCE_RECORDS.md#configuration))
- **Scope enforcement** - Automatic validation of what files can be modified
- **Git integration** - Pre-commit hooks and commit message validation
- **Graph visualization** - Query and visualize decision relationships
- **Monorepo support** - Service-specific records with ID suffixes
- **CI/CD ready** - JSON output and strict enforcement modes
- **SARIF output** - GitHub Code Scanning integration

### SARIF Output for GitHub Code Scanning

`provenance` can emit lint results in SARIF (Static Analysis Results Interchange Format) 2.1.0 format for integration with GitHub Code Scanning:

```bash
# Generate SARIF output
provenance lint --format sarif > provenance-results.sarif

# Upload to GitHub Code Scanning (in your CI workflow)
- name: Lint provenance records
  run: provenance lint --format sarif > provenance-results.sarif
  continue-on-error: true

- name: Upload to Code Scanning
  uses: github/codeql-action/upload-sarif@v3
  with:
    sarif_file: provenance-results.sarif
    category: linespec-provenance
```

The SARIF output includes:
- **Stable rule IDs** (PROV001 onward) for consistent alerting
- **File hashes** for GitHub's deduplication
- **%SRCROOT% base** for proper path resolution
- **Severity mapping** (error→error, warning→warning, hint→note)

### Documentation

- **[PROVENANCE_RECORDS.md](./PROVENANCE_RECORDS.md)** - Complete reference guide
- **[CLAUDE.md](./CLAUDE.md)** - Guidelines for AI agents using LineSpec
- **[github.com/livecodelife/provenance](https://github.com/livecodelife/provenance)** - The `provenance` CLI

---

## LineSpec Testing

LineSpec Testing is a DSL-based integration testing framework for containerized services. It intercepts database and HTTP traffic at the protocol level, making tests language-agnostic and framework-independent.

### Commands

```bash
# Run integration tests (the proxy image is pulled automatically on first use)
linespec test <path-to-linespec-files>

# Start protocol proxies
linespec proxy mysql <listen> <upstream>
linespec proxy postgresql <listen> <upstream>
linespec proxy http <listen> <upstream>
linespec proxy kafka <listen> <upstream>
linespec proxy grpc <listen> <upstream>
```

### Example LineSpec Test

```linespec
TEST create-user
RECEIVE HTTP:POST http://localhost:3000/users
WITH {{payloads/user_create_req.yaml}}

EXPECT WRITE:MYSQL users
WITH {{payloads/user_db_write_record.yaml}}
VERIFY query MATCHES /\bpassword_digest\b/
VERIFY query NOT_CONTAINS '`password`'

RESPOND HTTP:201
WITH {{payloads/user_create_resp.yaml}}
NOISE
  body.id
  body.created_at
```

### Documentation

- **[LINESPEC.md](./LINESPEC.md)** - DSL syntax reference
- **[CLAUDE.md](./CLAUDE.md)** - Contributor & AI-agent guidelines

---

## Configuration

Create a `.linespec.yml` in your repository root for LineSpec Testing. Provenance Records are configured separately, in `.provenance.yml` (see [Configuration](./PROVENANCE_RECORDS.md#configuration)):

```yaml
# LineSpec Testing configuration
service:
  name: my-service
  type: web
  port: 3000
  framework: rails                    # rails, fastapi, django, express, chi, or custom
  start_command: bundle exec rails server  # Optional: override framework default
  migration_command: bundle exec rake db:migrate  # Optional: custom migration command
  warmup_endpoint: /health            # Optional: custom warmup endpoint
  warmup_delay_ms: 100                # Optional: warmup delay in milliseconds
  
database:
  type: mysql
  port: 3306
  database: my-service_development    # Database name (default: {service}_development)
  username: my-service_user           # Username (default: {service}_user)
  password: my-service_password       # Password (default: {service}_password)
  host: db                            # Host for external DBs

infrastructure:
  database: true
  kafka: false
  grpc: false # Start a gRPC proxy sidecar
  redis: false # Start a Redis proxy sidecar
  proxy_image: "ghcr.io/livecodelife/linespec:3.27.0" # Override the proxy image (default: local linespec:latest if present, else the published image pinned to your version)

# Protobuf descriptor set for gRPC binary protobuf mocks (optional)
grpc_descriptor_set: proto/workflow.pb

# Container naming configuration (optional)
# Defaults are isolated per run (see below). The values here are explicit
# overrides, used verbatim; only set the roles you need.
container_naming:
  database_container: linespec-shared-db
  network_name: linespec-shared-net
  network_alias: real-db
  migrate_container: linespec-migrate-
```

Default container and network names are isolated per run. Each default carries a
suffix of 6 hex characters of the sha256 of the project root
plus a 6 hex per-process token, so concurrent runs on one Docker host are isolated and never
share names. Any `container_naming` value you set is an explicit override and is used verbatim
for that role; roles you leave unset still get the isolated default. Cleanup removes only
what the run created, so a crashed run's orphaned containers and network are not swept and
must be removed by hand with `docker rm -f <name>` and `docker network rm <name>`.

**Framework Support:**

| Framework | Start Command | Migration Command | Needs Warmup |
|-----------|---------------|-------------------|--------------|
| `rails` | `bundle exec rails server -b 0.0.0.0 -p ${PORT}` | `bundle exec rails db:migrate` | Yes (100ms) |
| `fastapi` | `python -m uvicorn main:app --host 0.0.0.0 --port ${PORT}` | None | No |
| `django` | `python manage.py runserver 0.0.0.0:${PORT}` | `python manage.py migrate` | Yes (100ms) |
| `express` | `npm start` | None | No |
| `chi` | `PORT=${PORT} go run .` | None | No |
| Custom | Via `start_command` | Via `migration_command` | Via `needs_warmup` |

The **Needs Warmup** column shows the framework default for `needs_warmup` (and the default `warmup_delay_ms` where applicable); override any value per-service under `service:`.

---

## Development

```bash
# Clone repository
git clone https://github.com/livecodelife/linespec.git
cd linespec

# Build (Provenance Records + LineSpec Testing — both included by default)
go build -o linespec ./cmd/linespec

# `make build-beta` / `-tags beta` remain only as backward-compatible aliases;
# they produce the same binary as the default build (identical since v2.0.0).

# Run unit tests
go test ./...

# Run integration tests (requires Docker)
make test-integration

# Run the example test suites
./linespec test ./examples/
```

---

## Updating the Docs Site

The site at [linespec.dev](https://linespec.dev) is built **from source** on every
push to `main` (`scripts/build-docs.sh` + `.github/workflows/pages.yml`). Markdown
is the single source of truth; the rendered `docs/*.html` and `docs/llms*.txt` are
gitignored build artifacts — **don't edit them by hand**, they're regenerated on
every push.

| To change… | Edit… |
|------------|-------|
| Provenance reference content | `PROVENANCE_RECORDS.md` |
| Testing / DSL reference content | `LINESPEC.md` |
| Overview content | `README.md` |
| Landing page / docs hub / curated `llms.txt` | the templates in `docs/templates/` (use the `__VERSION__` token for the version) |
| Page shell or design | `docs/templates/content.html`, `docs/templates/site-filter.lua`, `docs/assets/` |
| The version everywhere | `./scripts/bump-version.sh <new-version>` (touches `VERSION` + the `.md` sources only) |

Preview locally before pushing — the output is identical to CI:

```bash
brew install pandoc        # one-time; CI installs it via apt
./scripts/build-docs.sh    # renders docs/*.html and docs/llms*.txt
open docs/index.html
```

---

## Documentation Index

| Document | Status | Description |
|----------|--------|-------------|
| **[PROVENANCE_RECORDS.md](./PROVENANCE_RECORDS.md)** | ✅ Stable | Complete provenance reference |
| **[README.md](./README.md)** | ✅ Stable | This file - overview and installation |
| **[CLAUDE.md](./CLAUDE.md)** | ✅ Stable | Contributor & AI-agent guidelines |
| **[LINESPEC.md](./LINESPEC.md)** | ✅ Stable | DSL syntax for integration testing |
| **[CHANGELOG.md](./CHANGELOG.md)** | ✅ Stable | Version history and breaking changes |

### Reading Order

1. **Start here** (README.md) - Installation and overview
2. **PROVENANCE_RECORDS.md** - Complete reference for Provenance Records
3. **LINESPEC.md** - If using LineSpec Testing
4. **CLAUDE.md** - If contributing or using AI agents with LineSpec

---

## FAQ

### How do I migrate from the old repository?

The module path changed from `github.com/calebcowen/linespec` to `github.com/livecodelife/linespec`. Update your imports and use the new installation path.

### Where are my compiled YAML files?

LineSpec doesn't generate YAML files. Provenance Records are the authoritative source and are executed directly.

---

## Contributing

1. Check for an existing provenance record covering your work
2. If none exists, create a new record describing your decision
3. Make your changes
4. Run the linter: `make lint` (`provenance lint`)
5. Submit a pull request

See [CLAUDE.md](./CLAUDE.md) for detailed guidelines.

---

## License

MIT License - See [LICENSE](./LICENSE) for details.

---

## Support

- **Issues:** https://github.com/livecodelife/linespec/issues
- **Discussions:** https://github.com/livecodelife/linespec/discussions
- **Releases:** https://github.com/livecodelife/linespec/releases

---

**LineSpec v3.27.0** - Built with Provenance Records
