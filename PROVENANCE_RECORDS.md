# Provenance Records

**Version:** 1.0.0 (Stable)  
**LineSpec CLI Documentation**

Provenance Records are structured YAML artifacts that capture the organizational intent, constraints, and reasoning behind system changes. They live in a `provenance/` directory at the repository root and form a queryable graph of architectural decisions over time.

---

## Table of Contents

1. [Quick Start](#quick-start)
2. [Installation](#installation)
3. [Core Concepts](#core-concepts)
4. [Schema Reference](#schema-reference)
5. [CLI Commands](#cli-commands)
6. [Configuration](#configuration)
7. [Git Integration](#git-integration)
8. [Best Practices](#best-practices)
9. [Examples](#examples)

---

## Quick Start

```bash
# Install LineSpec
brew tap livecodelife/linespec
brew install linespec

# Or use go install
go install github.com/livecodelife/linespec/cmd/linespec@v1.2.0

# Create your first provenance record
linespec provenance create --title "Add user authentication"

# Validate all records
linespec provenance lint

# View the decision graph
linespec provenance graph
```

---

## Installation

### Homebrew (Recommended)

```bash
brew tap livecodelife/linespec
brew install linespec
```

### Go Install

```bash
go install github.com/livecodelife/linespec/cmd/linespec@v1.2.0
```

### GitHub Releases

Download pre-built binaries from the [releases page](https://github.com/livecodelife/linespec/releases).

---

## Core Concepts

### What are Provenance Records?

Provenance Records are **structured decision documents** that capture:

- **Intent** - What we want to achieve and why
- **Constraints** - Rules that must be followed
- **Scope** - What files are affected (or explicitly forbidden)
- **Relationships** - How decisions connect (supersedes, related)
- **Status** - Where the decision is in its lifecycle

They provide a **queryable history** of architectural decisions that can be linted, graphed, and enforced at commit time.

### ID Format

Records use the format: `prov-YYYY-NNN` or `prov-YYYY-NNN-service-name`

Examples:
- `prov-2026-001` - Root-level decision
- `prov-2026-001-user-service` - Service-specific decision

### Status Lifecycle

```
┌─────────┐    ┌─────────────┐    ┌────────────┐
│  open   │───▶│ implemented │───▶│ superseded│
└─────────┘    └─────────────┘    └────────────┘
                                    │
                                    ▼
                              ┌────────────┐
                              │ deprecated │
                              └────────────┘
```

- **open** - Decision is being discussed/developed
- **implemented** - Decision is complete and immutable
- **superseded** - Replaced by a newer record (use `superseded_by`)
- **deprecated** - No longer relevant

---

## Schema Reference

### Complete Example

```yaml
id: prov-2026-001
title: "Protocol-level proxy interception for language-agnostic test evaluation"
status: implemented
created_at: "2026-03-12"
author: "caleb.cowen@gmail.com"

sealed_at_sha: "a3f92c1d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b"

intent: >
  LineSpec evaluates service behavior by intercepting traffic at the TCP/protocol
  layer rather than through application-level mocking...

constraints:
  - Interception must occur at the network/protocol layer
  - The system under test must not require modification
  - Proxy implementations must be protocol-correct

affected_scope:
  - pkg/proxy/**
  - pkg/registry/**
  - cmd/linespec/main.go

forbidden_scope:
  - "re:.*_test\\.go$"

supersedes: null
superseded_by: null
related:
  - prov-2026-002

associated_specs: []
associated_traces: []
monitors: []

tags:
  - architecture
  - proxy
  - core
```

### Field Descriptions

#### Required Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique identifier (prov-YYYY-NNN or prov-YYYY-NNN-service) |
| `title` | string | Human-readable title |
| `status` | string | One of: open, implemented, superseded, deprecated |
| `created_at` | string | ISO 8601 date (YYYY-MM-DD) |
| `author` | string | Email of the author |

#### Intent and Constraints

| Field | Type | Description |
|-------|------|-------------|
| `intent` | string | What we want to achieve and why (folded scalar `>`) |
| `constraints` | array | List of rules that must be followed |

#### Scope

| Field | Type | Description |
|-------|------|-------------|
| `affected_scope` | array | Files/paths this decision affects |
| `forbidden_scope` | array | Files/paths explicitly excluded |

**Pattern Matching:**
- Exact paths: `pkg/proxy/mysql/proxy.go`
- Glob patterns: `pkg/proxy/**/*.go`
- Regex patterns: `"re:.*_test\\.go$"`

#### Graph Relationships

| Field | Type | Description |
|-------|------|-------------|
| `supersedes` | string | ID of older record this replaces |
| `superseded_by` | string | ID of newer record that replaces this |
| `related` | array | Related record IDs (no directional relationship) |

#### Proof of Completion

| Field | Type | Description |
|-------|------|-------------|
| `sealed_at_sha` | string | Git SHA captured when record marked implemented (CLI-only, immutable) |
| `associated_specs` | array | Proof artifacts validating this decision. Each entry has `path` (required) and optional `type` (e.g., `linespec`, `rspec`, `pytest`) |
| `associated_traces` | array | Trace files or test output |
| `monitors` | array | URLs or alerts for runtime monitoring |

#### Metadata

| Field | Type | Description |
|-------|------|-------------|
| `tags` | array | Arbitrary tags for filtering and organization |

---

## CLI Commands

### Create

Create a new provenance record:

```bash
# Interactive (opens editor)
linespec provenance create

# With pre-populated fields
linespec provenance create --title "Add caching layer" --tag architecture,performance

# For a specific service
linespec provenance create -i user-service --title "Add user auth"

# Skip editor
linespec provenance create --title "Quick fix" --no-edit
```

**Options:**
- `--title "..."` - Pre-populate title
- `--supersedes prov-YYYY-NNN` - Link to older record
- `--tag tag1,tag2` - Add tags
- `--no-edit` - Write without opening editor
- `-i, --id-suffix name` - Append service suffix
- `-c, --config path` - Use custom .linespec.yml

### Lint

Validate provenance records:

```bash
# Lint all records
linespec provenance lint

# Lint specific record
linespec provenance lint --record prov-2026-001

# JSON output for CI
linespec provenance lint --format json

# Override enforcement level
linespec provenance lint --enforcement strict
```

**Options:**
- `--record prov-YYYY-NNN` - Lint single record
- `--enforcement level` - none|warn|strict (default from config)
- `--format format` - human|json
- `-c, --config path` - Use custom config

**Enforcement Levels:**
- **none** - Don't enforce at all
- **warn** - Show warnings but allow (default)
- **strict** - Fail on any violation

### Status

View record status:

```bash
# Overview of all records
linespec provenance status

# Detailed view of one record
linespec provenance status --record prov-2026-001

# Filter by status
linespec provenance status --filter open
linespec provenance status --filter implemented

# Filter by tag
linespec provenance status --filter tag:architecture

# Save auto-populated scope
linespec provenance status --record prov-2026-001 --save-scope
```

**Options:**
- `--record prov-YYYY-NNN` - Show detailed status
- `--filter status|tag:name` - Filter results
- `--format format` - human|json
- `--save-scope` - Persist auto-populated scope
- `-c, --config path` - Use custom config

### Graph

Render provenance graph:

```bash
# Full graph
linespec provenance graph

# Graph from specific record
linespec provenance graph --root prov-2026-001

# Filter by status
linespec provenance graph --filter implemented

# Export as DOT for Graphviz
linespec provenance graph --format dot > graph.dot
```

**Options:**
- `--root prov-YYYY-NNN` - Start from specific record
- `--filter status` - Show only records with given status
- `--format format` - human|json|dot
- `-c, --config path` - Use custom config

### Check

Check commits for violations:

```bash
# Check current HEAD
linespec provenance check

# Check specific commit
linespec provenance check --commit abc123

# Check commit range
linespec provenance check --range HEAD~5..HEAD

# Check against specific record only
linespec provenance check --record prov-2026-001

# Check staged files (used by commit-msg hook)
linespec provenance check --staged --message-file .git/COMMIT_EDITMSG
```

**Options:**
- `--commit SHA` - Check specific commit (default: HEAD)
- `--range SHA..SHA` - Check commit range
- `--record prov-YYYY-NNN` - Check only against specific record
- `--staged` - Check staged files instead of committed files
- `--message-file path` - Path to commit message file (for use with --staged)
- `-c, --config path` - Use custom config

**Use with git hooks:**

```bash
# commit-msg hook usage
linespec provenance check --staged --message-file "$1"
```

This is used by the commit-msg hook to validate staged files against the commit message being written.

### Lock Scope

Lock scope to allowlist mode:

```bash
# Dry run first
linespec provenance lock-scope --record prov-2026-001 --dry-run

# Lock scope (saves to file)
linespec provenance lock-scope --record prov-2026-001
```

**Options:**
- `--record prov-YYYY-NNN` - Required. The record to lock
- `--dry-run` - Print scope without writing
- `-c, --config path` - Use custom config

### Complete

Mark record as implemented:

```bash
# Normal completion
linespec provenance complete --record prov-2026-001

# Force complete (skip LineSpec check)
linespec provenance complete --record prov-2026-001 --force
```

**Options:**
- `--record prov-YYYY-NNN` - Required. The record to mark as implemented
- `--force` - Skip LineSpec existence check
- `-c, --config path` - Use custom config

### Deprecate

Mark record as deprecated:

```bash
linespec provenance deprecate --record prov-2026-001 --reason "Replaced by new auth system"
```

**Options:**
- `--record prov-YYYY-NNN` - Required. The record to deprecate
- `--reason "..."` - Deprecation reason
- `-c, --config path` - Use custom config

### Install Hooks

Install git hooks for automatic validation:

```bash
linespec provenance install-hooks
```

---

## Configuration

Create a `.linespec.yml` file in your repository root:

```yaml
provenance:
  # Directory containing provenance records (default: provenance)
  dir: provenance
  
  # Enforcement level: none|warn|strict (default: warn)
  enforcement: warn
  
  # Require provenance IDs in commit messages (default: false)
  commit_tag_required: false
  
  # Auto-populate affected_scope from git commits (default: true)
  auto_affected_scope: true
  
  # Additional directories to load records from (for monorepos)
  shared_repos:
    - examples/user-service/provenance
    - examples/todo-api/provenance
```

### Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `dir` | string | `provenance` | Directory containing records |
| `enforcement` | string | `warn` | Global enforcement level |
| `commit_tag_required` | bool | `false` | Require tags in commits |
| `auto_affected_scope` | bool | `true` | Auto-populate scope |
| `shared_repos` | array | `[]` | Additional directories |

---

## Git Integration

### Two-Hook Strategy

The provenance system uses two git hooks that work together:

1. **pre-commit hook**: Runs first, lints modified provenance records for syntax/validity
2. **commit-msg hook**: Runs after you write your message, checks staged files against provenance scope

### Commit Message Format

Reference provenance records in commit messages:

```bash
# Single record
git commit -m "Add user authentication [prov-2026-001]"

# Multiple records
git commit -m "Fix auth and caching [prov-2026-001] [prov-2026-002]"

# Service-specific
git commit -m "Update user service [prov-2026-001-user-service]"
```

### Pre-commit Hook

The pre-commit hook validates that modified provenance records are well-formed:

- **Linting**: Checks YAML syntax, required fields, and valid values
- **Quick validation**: Ensures records can be parsed and loaded

### Commit-msg Hook

The commit-msg hook validates scope constraints:

- **Extracts IDs**: Parses provenance IDs from the commit message
- **Checks staged files**: Validates that staged files are in scope of referenced records
- **Enforces commit_tag_required**: Blocks commits without provenance IDs when configured
- **Self-modification exception**: Allows open records to modify their own YAML files
- **Implemented record enforcement**: Rejects commits tagged with already-implemented records (they are immutable)

**Implemented Record Enforcement:**

Once a provenance record is marked as `implemented`, it becomes immutable. The commit-msg hook will reject any commits tagged with an implemented record ID:

```bash
# This will FAIL - prov-2026-001 is already implemented
git commit -m "Fix typo [prov-2026-001]"
# Error: prov-2026-001 is already implemented - cannot commit with this ID. 
#        Create a new record or supersede this one.

# Instead, create a new record or supersede:
linespec provenance create --title "Fix typo in auth" --supersedes prov-2026-001
git commit -m "Fix typo [prov-2026-042]"
```

The only exception is the completion transition (when a record's own file changes from `status: open` to `status: implemented`), which is allowed.

### Git Hook Installation

```bash
# Install both hooks automatically
linespec provenance install-hooks

# This creates:
#   .git/hooks/pre-commit  - Lints modified provenance records
#   .git/hooks/commit-msg  - Checks staged files against scope
```

**Note:** Both hooks respect the local `./linespec` binary when available (for development), otherwise fall back to the system `linespec`.

### Manual Hook Setup

If you prefer manual installation:

```bash
# pre-commit hook
#!/bin/sh
# Use local binary if available
if [ -f "./linespec" ]; then
    LINESPEC="./linespec"
else
    LINESPEC="linespec"
fi

# Lint modified provenance records
modified_records=$(git diff --cached --name-only | grep "^provenance/prov-")
for record in $modified_records; do
    $LINESPEC provenance lint --record "$record"
    if [ $? -ne 0 ]; then
        exit 1
    fi
done

# commit-msg hook
#!/bin/sh
COMMIT_MSG_FILE="$1"

if [ -f "./linespec" ]; then
    LINESPEC="./linespec"
else
    LINESPEC="linespec"
fi

# Check staged files against scope
$LINESPEC provenance check --staged --message-file "$COMMIT_MSG_FILE"
if [ $? -ne 0 ]; then
    echo "Commit blocked due to provenance scope violations"
    exit 1
fi
```

### CI Integration

Add to your CI pipeline:

```yaml
# GitHub Actions example
- name: Check Provenance
  run: |
    go install github.com/livecodelife/linespec/cmd/linespec@latest
    linespec provenance lint --enforcement strict
    linespec provenance check --range HEAD~10..HEAD
```

---

## Sealed at SHA and Stale Scope Warnings

### What is `sealed_at_sha`?

When a provenance record is marked as `implemented`, the CLI automatically captures the current HEAD git SHA and stores it in the `sealed_at_sha` field. This field is:

- **Immutable** - Set once by the CLI, never modified after
- **CLI-only** - Never set manually
- **Timestamp** - Captures the exact moment a decision was "locked in"
- **Only on implemented records** - Open/superseded/deprecated records don't have this field

Example:
```yaml
id: prov-2026-001
title: "Add user authentication"
status: implemented
created_at: "2026-03-12"
author: "user@example.com"

sealed_at_sha: "a3f92c1d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b"
#          ↑ Captured when `linespec provenance complete` was run
```

### Stale Scope Warnings

The `sealed_at_sha` field enables a feature called **stale scope warnings**. When checking commits against provenance records:

1. If a commit touches files in a record's `affected_scope`
2. The CLI runs `git diff <sealed_at_sha> HEAD` on those files
3. If files **haven't actually changed** since the record was sealed, a warning is shown

**Why this matters:**
- Reduces false positives from files that were incidentally in scope
- Distinguishes between meaningful changes and safe refactors
- Gives engineers context about whether a change needs review

**Example warning:**
```
⚠ Stale scope warnings in staged (non-blocking):

  • prov-2026-001 lists pkg/utils/helpers.go in affected_scope, but 
    file unchanged since record sealed at a3f92c1
    (File listed in affected_scope but unchanged since record sealed)
```

**Key characteristics:**
- **Non-blocking** - These are warnings, not errors
- **Informational** - Helps engineers make informed decisions
- **Configurable** - Controlled by the `enforcement` setting

### Viewing the Sealed SHA

The sealed SHA is displayed in the status output for implemented records:

```bash
linespec provenance status --record prov-2026-001
```

Output:
```
prov-2026-001  ·  implemented
────────────────────────────────────────────────────────────

Title:        Add user authentication
Author:       user@example.com
Created:      2026-03-12
Sealed at:    a3f92c1        ← Shows the short SHA
```

---

## Best Practices

### Writing Good Provenance Records

1. **Start with Intent** - Clearly state what you want to achieve and why
2. **Be Specific with Constraints** - Write verifiable rules
3. **Use Appropriate Scope** - Start in observed mode (empty affected_scope), then lock to allowlist
4. **Link Related Decisions** - Use `supersedes`, `superseded_by`, and `related`
5. **Tag Thoughtfully** - Use consistent tags for filtering

### Scope Management

**Observed Mode** (empty affected_scope):
- Allows any file changes
- Good for early development
- Auto-populated from git history

**Allowlist Mode** (non-empty affected_scope):
- Only allows changes to listed files
- Good for mature decisions
- Prevents scope creep

**Transition:**
```bash
# After some commits, lock the scope
linespec provenance lock-scope --record prov-2026-001
```

### Monorepo Strategy

For multiple services in one repo:

1. **Root provenance/** - Shared architectural decisions
2. **Service directories** - Service-specific decisions
3. **Use ID suffixes** - `prov-2026-001-user-service`

```yaml
# .linespec.yml
provenance:
  dir: provenance
  shared_repos:
    - services/user-service/provenance
    - services/todo-api/provenance
```

### Record Lifecycle

```bash
# 1. Create (status: open)
linespec provenance create --title "New feature"

# 2. Develop (make commits, scope auto-populates)
git commit -m "Implement feature [prov-2026-042]"

# 3. Lock scope (when feature is complete)
linespec provenance lock-scope --record prov-2026-042

# 4. Complete (status: implemented)
linespec provenance complete --record prov-2026-042

# 5. (Optional) Supersede later
linespec provenance create --title "Better approach" --supersedes prov-2026-042
```

---

## Examples

### Example 1: Simple Architecture Decision

```yaml
id: prov-2026-015
title: "Use PostgreSQL for primary data store"
status: implemented
created_at: "2026-03-15"
author: "dev@example.com"

intent: >
  After evaluating MySQL, PostgreSQL, and SQLite, we choose PostgreSQL
  for our primary data store. It provides better JSON support, more
  advanced indexing, and better handling of concurrent writes.

constraints:
  - All new tables must use PostgreSQL
  - Existing MySQL tables will be migrated gradually
  - Use connection pooling with minimum 10, maximum 100 connections

affected_scope:
  - pkg/db/**
  - migrations/**
  - config/database.yml

forbidden_scope:
  - "re:.*_test\\.go$"
  - vendor/**

tags:
  - architecture
  - database
  - postgresql
```

### Example 2: Service-Specific Decision

```yaml
id: prov-2026-016-user-service
title: "Implement JWT-based authentication"
status: open
created_at: "2026-03-16"
author: "auth-team@example.com"

intent: >
  The user service will implement JWT-based authentication to support
  stateless API access and microservice communication.

constraints:
  - JWT tokens must expire after 24 hours
  - Refresh tokens must expire after 30 days
  - Use RS256 algorithm with 2048-bit keys

affected_scope:
  - services/user-service/pkg/auth/**
  - services/user-service/handlers/auth.go

associated_specs:
  - path: services/user-service/specs/auth/login_success.linespec
    type: linespec
  - path: services/user-service/specs/auth/login_failure.linespec
    type: linespec

tags:
  - user-service
  - authentication
  - jwt
```

### Example 3: Superseding an Old Decision

```yaml
id: prov-2026-017
title: "Replace Redis caching with in-memory LRU"
status: implemented
created_at: "2026-03-17"
author: "perf-team@example.com"

intent: >
  After load testing, we found Redis adds unnecessary latency for our
  use case. An in-memory LRU cache provides better performance with
  simpler operations.

constraints:
  - Maximum cache size: 10,000 entries
  - Eviction policy: LRU
  - TTL: 5 minutes maximum

affected_scope:
  - pkg/cache/**
  - cmd/api/main.go

supersedes: prov-2026-008
tags:
  - performance
  - caching
  - lru
```

---

## Troubleshooting

### Common Issues

**"Record not found"**
- Check that the record file exists in the provenance directory
- Verify the ID matches the filename
- Use `-c, --config` to specify the correct config file

**"File outside scope"**
- The file you're modifying isn't in affected_scope
- Add it to affected_scope or create a new provenance record
- Use `--force` to bypass (not recommended)

**"Cycle detected in graph"**
- You have circular supersedes relationships
- Check that record A doesn't supersede B while B supersedes A
- Fix the relationships in the YAML files

**Linter is slow**
- Reduce the number of shared_repos
- Use `--record` to lint only specific records
- Consider splitting large monorepos

### Getting Help

- Open an issue: https://github.com/livecodelife/linespec/issues
- Check existing provenance records in the examples/
- Review the release plan: RELEASE_PLAN.md

---

## License

MIT License - See LICENSE file for details.
