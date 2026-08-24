# LineSpec v3.20.0

[![Version](https://img.shields.io/badge/version-3.20.0-blue.svg)](https://github.com/livecodelife/linespec/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/livecodelife/linespec)](https://goreportcard.com/report/github.com/livecodelife/linespec)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

**Provenance Records** - Structured YAML artifacts for documenting architectural decisions  
**LineSpec Testing** - DSL-based integration testing for containerized services

---

## Overview

LineSpec is a tool for managing **Provenance Records** - structured decision documents that capture the intent, constraints, and reasoning behind architectural changes. It includes a powerful CLI for creating, validating, and enforcing these records.

As of v3.20.0, **LineSpec Testing** is stable and included in the default installation alongside Provenance Records.

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
go install github.com/livecodelife/linespec/v3/cmd/linespec@v3.20.0
```

### GitHub Releases

Download pre-built binaries from the [releases page](https://github.com/livecodelife/linespec/releases).

- `linespec_3.20.0_*` - Full release (Provenance Records + LineSpec Testing)

---

## Quick Start - Provenance Records (Stable)

```bash
# 1. Install LineSpec
brew tap livecodelife/linespec
brew install linespec

# 2. Create your first provenance record
linespec provenance create --title "Add user authentication"

# 3. View the decision graph
linespec provenance graph

# 4. Validate all records
linespec provenance lint
```

### Example Provenance Record

```yaml
id: prov-2026-001
title: "Use PostgreSQL for primary data store"
status: open
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
linespec provenance create          # Create new record
linespec provenance discover        # Bootstrap draft records + spec stubs from existing source
linespec provenance lint            # Validate records
linespec provenance status          # View status
linespec provenance graph           # Render decision graph

# Agent guidance
linespec provenance next            # Compute the correct next provenance action
linespec provenance govern          # List the active records governing given files
linespec provenance context         # Show which records govern given files

# Semantic search (requires embedding configuration)
linespec provenance search          # Search by semantic similarity
linespec provenance audit           # Audit changes against history
linespec provenance index           # Index records for search

# Git integration
linespec provenance check           # Check commits for violations
linespec provenance install-hooks   # Install git hooks
linespec provenance install-skills  # Install all Claude Code skills (provenance + linespec-testing)
linespec provenance install-plugin  # Install the Claude Code provenance plugin (hooks + slash command)

# Lifecycle management
linespec provenance lock-scope      # Lock scope to allowlist
linespec provenance complete        # Mark as implemented
linespec provenance deprecate       # Mark as deprecated

# Manifest distribution
linespec provenance publish         # Package records into a versioned manifest artifact
linespec clone <manifest-url>       # Bootstrap a project from a published manifest
linespec import <manifest-url>      # Import provenance records from a published manifest

# Output formats
linespec provenance lint --format json     # JSON output for CI
linespec provenance lint --format sarif    # SARIF output for GitHub Code Scanning
```

### Key Features

- **Structured YAML format** - Clear, version-controlled decision records
- **Codebase discovery** - `provenance discover` bootstraps draft records and `.linespec` stubs from an existing Chi/Rails/Sinatra project ([details](./PROVENANCE_RECORDS.md#discover))
- **Path exemptions** - `exclude_paths` exempts generated/vendored files from governance ([details](./PROVENANCE_RECORDS.md#exclude_paths))
- **Scope enforcement** - Automatic validation of what files can be modified
- **Git integration** - Pre-commit hooks and commit message validation
- **Graph visualization** - Query and visualize decision relationships
- **Monorepo support** - Service-specific records with ID suffixes
- **CI/CD ready** - JSON output and strict enforcement modes
- **SARIF output** - GitHub Code Scanning integration

### SARIF Output for GitHub Code Scanning

LineSpec can emit lint results in SARIF (Static Analysis Results Interchange Format) 2.1.0 format for integration with GitHub Code Scanning:

```bash
# Generate SARIF output
linespec provenance lint --format sarif > provenance-results.sarif

# Upload to GitHub Code Scanning (in your CI workflow)
- name: Lint provenance records
  run: linespec provenance lint --format sarif > provenance-results.sarif
  continue-on-error: true

- name: Upload to Code Scanning
  uses: github/codeql-action/upload-sarif@v3
  with:
    sarif_file: provenance-results.sarif
    category: linespec-provenance
```

The SARIF output includes:
- **19 stable rule IDs** (PROV001-PROV019) for consistent alerting
- **File hashes** for GitHub's deduplication
- **%SRCROOT% base** for proper path resolution
- **Severity mapping** (error→error, warning→warning, hint→note)

### Documentation

- **[PROVENANCE_RECORDS.md](./PROVENANCE_RECORDS.md)** - Complete reference guide
- **[CLAUDE.md](./CLAUDE.md)** - Guidelines for AI agents using LineSpec

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

Create a `.linespec.yml` in your repository root:

```yaml
# Provenance Records configuration
provenance:
  dir: provenance                    # Records directory
  enforcement: warn                  # none|warn|strict
  commit_tag_required: false         # Require IDs in commits
  auto_affected_scope: true           # Auto-populate from git
  run_associated_specs_on_complete: false  # Run associated_specs on completion
  overlap_specs_on_complete: block   # Overlap teeth severity: block|warn|off
  shared_repos: []                    # Additional directories (monorepos)
  
  # Semantic search configuration (optional - supports voyage or openai)
  embedding:
    provider: voyage                 # Embedding provider: voyage or openai
    index_model: voyage-4-large     # Model for indexing (2048 dims for voyage)
    query_model: voyage-4-lite      # Model for queries (2048 dims for voyage)
    api_key: ${VOYAGE_API_KEY}      # API key from environment
    similarity_threshold: 0.50        # Minimum similarity for results
    index_on_complete: true         # Auto-index on complete

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
  proxy_image: "ghcr.io/livecodelife/linespec:3.20.0" # Override the proxy image (default: local linespec:latest if present, else the published image pinned to your version)

# Protobuf descriptor set for gRPC binary protobuf mocks (optional)
grpc_descriptor_set: proto/workflow.pb

# Container naming configuration (optional)
container_naming:
  database_container: linespec-shared-db
  network_name: linespec-shared-net
  network_alias: real-db
  migrate_container: linespec-migrate-
```

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
4. Run the linter: `linespec provenance lint`
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

**LineSpec v3.20.0** - Built with Provenance Records
