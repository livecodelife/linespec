# LineSpec v1.0.4

[![Version](https://img.shields.io/badge/version-1.0.4-blue.svg)](https://github.com/livecodelife/linespec/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/livecodelife/linespec)](https://goreportcard.com/report/github.com/livecodelife/linespec)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

**Provenance Records** - Structured YAML artifacts for documenting architectural decisions  
**LineSpec Testing** - DSL-based integration testing for containerized services (Beta)

---

## Overview

LineSpec is a tool for managing **Provenance Records** - structured decision documents that capture the intent, constraints, and reasoning behind architectural changes. It includes a powerful CLI for creating, validating, and enforcing these records.

The default installation includes only the stable **Provenance Records** functionality. **LineSpec Testing** features are available as a beta opt-in.

---

## Installation

### Homebrew (Recommended)

```bash
# Install stable version (Provenance Records only)
brew tap livecodelife/linespec
brew install linespec

# Or install beta version (includes LineSpec Testing)
brew install linespec-beta
```

### Go Install

```bash
# Stable version (Provenance Records only)
go install github.com/livecodelife/linespec/cmd/linespec@v1.0.4

# Beta version (includes LineSpec Testing)
go install -tags beta github.com/livecodelife/linespec/cmd/linespec@v1.0.4
```

### GitHub Releases

Download pre-built binaries from the [releases page](https://github.com/livecodelife/linespec/releases).

- `linespec_1.0.4_*` - Stable version (Provenance only)
- `linespec-beta_1.0.4_*` - Beta version (All features)

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

## Feature Comparison

| Feature | Provenance Records (v1.0.0) | LineSpec Testing (Beta) |
|---------|------------------------------|-------------------------|
| Status | ✅ **Stable** | 🚧 **Beta** |
| Commands | `provenance` | `test`, `proxy` |
| Maturity | Production-ready | Active development |
| Installation | Default | Requires `-tags beta` |
| Documentation | Complete | In progress |

---

## Provenance Records - Full Feature Set

### CLI Commands

```bash
# Record management
linespec provenance create          # Create new record
linespec provenance lint            # Validate records
linespec provenance status          # View status
linespec provenance graph           # Render decision graph

# Git integration
linespec provenance check           # Check commits for violations
linespec provenance install-hooks   # Install git hooks

# Lifecycle management
linespec provenance lock-scope      # Lock scope to allowlist
linespec provenance complete        # Mark as implemented
linespec provenance deprecate       # Mark as deprecated
```

### Key Features

- **Structured YAML format** - Clear, version-controlled decision records
- **Scope enforcement** - Automatic validation of what files can be modified
- **Git integration** - Pre-commit hooks and commit message validation
- **Graph visualization** - Query and visualize decision relationships
- **Monorepo support** - Service-specific records with ID suffixes
- **CI/CD ready** - JSON output and strict enforcement modes

### Documentation

- **[PROVENANCE_RECORDS.md](./PROVENANCE_RECORDS.md)** - Complete reference guide
- **[AGENTS.md](./AGENTS.md)** - Guidelines for AI agents using LineSpec

---

## LineSpec Testing (Beta)

> 🚧 **Beta Feature**: LineSpec Testing is in active development. Build with `-tags beta` to enable.

LineSpec Testing is a DSL-based integration testing framework for containerized services. It intercepts database and HTTP traffic at the protocol level, making tests language-agnostic and framework-independent.

### Installation (Beta)

```bash
# Build from source with beta tag
go build -tags beta -o linespec ./cmd/linespec

# Or install via go install
go install -tags beta github.com/livecodelife/linespec/cmd/linespec@v1.1.0
```

### Beta Commands

```bash
# Run integration tests
linespec test <path-to-linespec-files>

# Start protocol proxies
linespec proxy mysql <listen> <upstream>
linespec proxy postgresql <listen> <upstream>
linespec proxy http <listen> <upstream>
linespec proxy kafka <listen> <upstream>
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

### Beta Documentation

- **[LINESPEC.md](./LINESPEC.md)** - DSL syntax reference (Beta)
- **[AGENTS.md](./AGENTS.md)** - Testing guidelines (Beta section)

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
  shared_repos: []                    # Additional directories (monorepos)

# LineSpec Testing configuration (Beta)
service:
  name: my-service
  type: web
  port: 3000
  
database:
  type: mysql
  port: 3306

infrastructure:
  database: true
  kafka: false
```

---

## Development

```bash
# Clone repository
git clone https://github.com/livecodelife/linespec.git
cd linespec

# Build stable version (Provenance only)
go build -o linespec ./cmd/linespec

# Build beta version (all features)
go build -tags beta -o linespec ./cmd/linespec

# Run tests
go test ./pkg/provenance/...

# Run with beta features
./linespec test ./examples/
```

---

## Documentation Index

| Document | Status | Description |
|----------|--------|-------------|
| **[PROVENANCE_RECORDS.md](./PROVENANCE_RECORDS.md)** | ✅ Stable | Complete provenance reference |
| **[README.md](./README.md)** | ✅ Stable | This file - overview and installation |
| **[AGENTS.md](./AGENTS.md)** | ✅ Stable | Guidelines for AI agents |
| **[LINESPEC.md](./LINESPEC.md)** | 🚧 Beta | DSL syntax for integration testing |
| **[RELEASE_PLAN.md](./RELEASE_PLAN.md)** | ✅ Stable | v1.0.0 release strategy |

### Reading Order

1. **Start here** (README.md) - Installation and overview
2. **PROVENANCE_RECORDS.md** - Complete reference for stable features
3. **AGENTS.md** - If using AI agents with LineSpec
4. **LINESPEC.md** - If using beta testing features

---

## FAQ

### What's the difference between stable and beta?

**Stable (default):** Includes only Provenance Records - fully tested and production-ready.

**Beta:** Includes Provenance Records + LineSpec Testing - active development, may have bugs.

### Can I install both versions?

Yes! Using Homebrew:
```bash
brew install linespec        # Stable
brew install linespec-beta   # Beta (installed as 'linespec-beta')
```

### When will LineSpec Testing be stable?

LineSpec Testing will reach v1.0.0 in a future release. The beta is available now for early adopters who want to test and provide feedback.

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

See [AGENTS.md](./AGENTS.md) for detailed guidelines.

---

## License

MIT License - See [LICENSE](./LICENSE) for details.

---

## Support

- **Issues:** https://github.com/livecodelife/linespec/issues
- **Discussions:** https://github.com/livecodelife/linespec/discussions
- **Releases:** https://github.com/livecodelife/linespec/releases

---

**LineSpec v1.0.4** - Built with Provenance Records
