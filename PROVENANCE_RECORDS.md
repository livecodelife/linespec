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
go install github.com/livecodelife/linespec/cmd/linespec@v3.11.7

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
go install github.com/livecodelife/linespec/cmd/linespec@v3.11.7
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

Records use the format: `prov-YYYY-XXXXXXXX` or `prov-YYYY-XXXXXXXX-service-name`

Where:
- `YYYY` is the four-digit year
- `XXXXXXXX` is 8 cryptographically random hex characters (lowercase)

The year prefix limits the collision probability per year. With 8 hex characters, there are 2^32 (4,294,967,296) possible combinations per year, making collisions extremely unlikely even with concurrent record creation.

Examples:
- `prov-2026-a1b2c3d4` - Root-level decision  
- `prov-2026-deadbeef-user-service` - Service-specific decision

**Backward Compatibility:** The system still accepts the legacy sequential format `prov-YYYY-NNN` for existing records, but all new records use the crypto random format.

### Status Lifecycle

```
┌─────────┐    ┌─────────┐    ┌─────────────┐    ┌────────────┐
│  draft  │───▶│  open   │───▶│ implemented │───▶│ superseded│
└─────────┘    └─────────┘    └─────────────┘    └────────────┘
                                                    │
                                                    ▼
                                              ┌────────────┐
                                              │ deprecated │
                                              └────────────┘
```

- **draft** - Record created but enforcement not yet active. Scope and spec checks are suppressed. Transition to open with `linespec provenance open --record <id>`.
- **open** - Decision is active and under enforcement
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
| `id` | string | Unique identifier (prov-YYYY-XXXXXXXX or prov-YYYY-XXXXXXXX-service) |
| `title` | string | Human-readable title |
| `status` | string | One of: draft, open, implemented, superseded, deprecated |
| `type` | string | Record tier: `brief`, `blueprint` (default), `bug`, or `imprint`. See [Tier Hierarchy](#tier-hierarchy). |
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
| `supersedes` | string | ID of older record this replaces (same tier; Bug may also supersede Blueprint) |
| `superseded_by` | string | ID of newer record that replaces this |
| `extends` | string | Bug only: ID of Blueprint or Bug whose constraint coverage this record supplements |
| `implements` | string | ID of the parent record one tier above (blueprint→brief, imprint→blueprint) |
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

## Tier Hierarchy

Records can be organized into tiers using the `type` field:

| Type | Role | Implements | Required fields |
|------|------|------------|-----------------|
| `brief` | High-level intent — the "why" | Nothing (top of hierarchy) | `constraints` |
| `blueprint` | Design decision — the "what" | `brief` (optional) | — |
| `bug` | Correction or gap-fill — the "fix" | Nothing (cannot use `implements`) | exactly one of `supersedes` or `extends` |
| `imprint` | Implementation record — the "how" | `blueprint` (required) | `implements` |

Records without a `type` field default to `blueprint` for backward compatibility (a lint hint is emitted).

**Hierarchy rules enforced by the linter (always-on, graph integrity):**

- **Supersession must stay within the same tier.** A `blueprint` cannot supersede a `brief`. Use `related` or `implements` for cross-tier connections. (PROV020) _Exception: `bug` may supersede `blueprint` or another `bug`._
- **`implements` must point exactly one tier up.** `blueprint` implements `brief`, `imprint` implements `blueprint`. Skipping a tier or pointing sideways is a lint error. `brief` and `bug` records may not use `implements`. (PROV021)
- **`implements` must resolve.** The referenced record must exist locally or in a configured shared repo cache. A missing reference is always an error. (PROV022)
- **Imprint supersession requires same parent.** When an Imprint supersedes another Imprint, both must share the same `implements` value.
- **Bug records require exactly one of `supersedes` or `extends`.** Use `supersedes` when existing constraints are incorrect; use `extends` when constraints are missing. Neither or both is an error.
- **`extends` target must be a Blueprint or Bug.** A Bug extending a Brief or Imprint is an error.
- **`brief` records must carry `constraints`.** A Brief with no constraints is a lint error.

**Lifecycle note:** Draft records skip all scope and spec enforcement. Use `linespec provenance open` to begin enforcement when the record is ready.

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
- `--type brief|blueprint|bug|imprint` - Set record tier (default: blueprint)
- `--supersedes prov-YYYY-XXXXXXXX` - Link to older record
- `--tag tag1,tag2` - Add tags
- `--no-edit` - Write without opening editor
- `-i, --id-suffix name` - Append service suffix
- `-c, --config path` - Use custom .linespec.yml

**Note:** New records start in `draft` status. Scope and spec enforcement are suppressed until you run `linespec provenance open`.

### Open

Transition a record from `draft` to `open`, activating scope and spec enforcement:

```bash
linespec provenance open --record prov-2026-XXXXXXXX
```

Only records in `draft` status can be opened. Attempting to open a record in any other status returns an error.

**Options:**
- `--record prov-YYYY-XXXXXXXX` - Record ID to open (required)
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

# SARIF output for GitHub Code Scanning
linespec provenance lint --format sarif > provenance-results.sarif

# Override enforcement level
linespec provenance lint --enforcement strict
```

**Options:**
- `--record prov-YYYY-XXXXXXXX` - Lint single record
- `--enforcement level` - none|warn|strict (default from config)
- `--format format` - human|json|sarif
- `-c, --config path` - Use custom config

**Enforcement Levels:**
- **none** - Don't enforce at all
- **warn** - Show warnings but allow (default)
- **strict** - Fail on any violation

**SARIF Output:**
When using `--format sarif`, the command produces a valid SARIF 2.1.0 document that can be uploaded to GitHub Code Scanning:

- Includes all 19 lint rules in the tool descriptor
- Maps severity to SARIF levels (error→error, warning→warning, hint→note)
- Uses %SRCROOT% as uriBaseId for path resolution
- Includes SHA-256 hashes for file deduplication
- Supports enforcement level variation (e.g., PROV010 is error under strict, warning under warn)

### Status

View record status:

```bash
# Overview of all records
linespec provenance status

# Detailed view of one record
linespec provenance status --record prov-2026-a1b2c3d4

# Filter by status
linespec provenance status --filter open
linespec provenance status --filter implemented

# Filter by tag
linespec provenance status --filter tag:architecture

# Save auto-populated scope
linespec provenance status --record prov-2026-a1b2c3d4 --save-scope
```

**Options:**
- `--record prov-YYYY-XXXXXXXX` - Show detailed status
- `--filter status|tag:name` - Filter results
- `--format format` - human|json
- `--save-scope` - Persist auto-populated scope
- `-c, --config path` - Use custom config

The detailed `--record` view includes two hierarchy sections derived at render time:

- **Implements** — the parent record ID and title if this record's `implements` field is set, or `—` if not.
- **Implemented by** — any records in the local provenance directory whose `implements` field points to this record's ID, shown with their type and title. `—` when none exist.

### Graph

Render provenance graph:

```bash
# Full graph
linespec provenance graph

# Graph from specific record (shows parent + record + all downstream children)
linespec provenance graph --root prov-2026-a1b2c3d4

# Filter to open records only
linespec provenance graph --filter open

# JSON output with typed edges
linespec provenance graph --format json
```

**Options:**
- `--root prov-YYYY-XXXXXXXX` - Show subgraph centred on a record: one implements parent (if any) plus all downstream implements and supersession children
- `--filter status` - Show only records with given status
- `--format format` - human|json|dot
- `-c, --config path` - Use custom config

The graph renders two relationship dimensions:

- **Supersession chains** (`└─` connector) — records that supersede one another, showing evolution within a tier
- **Implements hierarchy** (`↳ [type]` connector) — brief → blueprint → imprint parent-child relationships, visually distinct from supersession. Bug records appear in supersession chains alongside blueprints.

In `--format json`, edges carry an `edge_type` field with values `supersedes`, `implements`, or `related`, so visualization tooling can apply different visual treatments. Each node also includes an `implements_nodes` array for direct implements children.

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
linespec provenance check --record prov-2026-a1b2c3d4

# Check staged files (used by commit-msg hook)
linespec provenance check --staged --message-file .git/COMMIT_EDITMSG
```

**Options:**
- `--commit SHA` - Check specific commit (default: HEAD)
- `--range SHA..SHA` - Check commit range
- `--record prov-YYYY-XXXXXXXX` - Check only against specific record
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
linespec provenance lock-scope --record prov-2026-a1b2c3d4 --dry-run

# Lock scope (saves to file)
linespec provenance lock-scope --record prov-2026-a1b2c3d4
```

**Options:**
- `--record prov-YYYY-XXXXXXXX` - Required. The record to lock
- `--dry-run` - Print scope without writing
- `-c, --config path` - Use custom config

### Lock Layer

Create a locked layer record — an architectural declaration that crystallizes
a portion of the system. The record is created immediately in `implemented`
status with `locked: true` and a `sealed_at_sha` captured from HEAD.

```bash
# Create a locked layer (opens editor)
linespec provenance lock-layer --title "Core proxy layer"

# Create without opening editor
linespec provenance lock-layer --title "Core proxy layer" --no-edit
```

**Options:**
- `--title "..."` - Required. Title for the locked layer record
- `--no-edit` - Write without opening editor
- `-c, --config path` - Use custom config

**How it works:**
The command creates a new provenance record that is immediately `implemented` and `locked: true`. After creation, edit the record to define `affected_scope` and `associated_specs` — these together define the protected surface.

The linter checks both `affected_scope` and `associated_specs` from locked records against both fields of any `open` record. If any surface overlaps, it's a lint error unless the open record declares `supersedes` pointing at the locked record. This forces explicit acknowledgment when reopening a crystallized layer.

**Unlocking a layer:**
To modify files governed by a locked record, create a new record that supersedes it:
```bash
linespec provenance create --title "Update proxy layer" --supersedes prov-2026-a1b2c3d4
```

### Complete

Mark record as implemented:

```bash
# Normal completion
linespec provenance complete --record prov-2026-a1b2c3d4

# Force complete (skip LineSpec check)
linespec provenance complete --record prov-2026-a1b2c3d4 --force
```

**Options:**
- `--record prov-YYYY-XXXXXXXX` - Required. The record to mark as implemented
- `--force` - Skip LineSpec existence check
- `-c, --config path` - Use custom config

**Hash sealing:** After marking a record as implemented, `complete` computes a SHA-256 hash of the record's canonical YAML content and writes it to `.linespec/hash_manifest.json`. The manifest is created automatically on first use. The full-graph hash and active-subset hash in the manifest are also recomputed at this time. Once sealed, any modification to the record's immutable fields will be detected by `linespec provenance lint` as a **PROV-IMM** error.

### Compile

Rebuild the hash manifest from scratch:

```bash
# Recompute hashes for all records and update the manifest
linespec provenance compile

# Use a custom config
linespec provenance compile -c /path/to/.linespec.yml
```

**Options:**
- `-c, --config path` - Use custom config

**Idempotent:** If every record's hash already matches the stored manifest, no file is written and the command exits 0. Running `compile` when the manifest is missing or stale writes the full manifest covering every record and recomputes `FullGraphHash` and `ActiveSubsetHash`.

**When to use:** After accidentally deleting or corrupting `.linespec/hash_manifest.json`, after a failed `complete` operation left the manifest incomplete, or as a recovery step when `linespec provenance lint` reports unexpected **PROV-IMM** errors on records you haven't edited.

### Publish

Package a repository's provenance records into a versioned, content-addressed `linespec.manifest.json` artifact for distribution:

```bash
# Publish provenance records only (minimum viable publish)
linespec provenance publish

# Include optional layers
linespec provenance publish --specs linespecs/ --code pkg/ --prompt PROMPT.md

# Pin to an explicit version instead of auto-incrementing
linespec provenance publish --version v2

# Write the manifest to a custom path
linespec provenance publish --manifest dist/linespec.manifest.json
```

**Options:**
- `--manifest path` — Path to `linespec.manifest.json` (default: `./linespec.manifest.json`)
- `--version label` — Explicit version label; default is auto-increment (`v1` → `v2` → `v3`)
- `--specs path` — Path to a specs artifact file or directory
- `--code path` — Path to a code artifact file or directory
- `--prompt path` — Path to a prompt artifact file
- `--help` — Show help

**What it does:**

1. Loads the existing `linespec.manifest.json` (creates one if it doesn't exist)
2. Resolves the next version label (or uses `--version`)
3. Applies a deterministic transformation pipeline to the loaded records, making them consumer-appropriate:
   - Strips imprint records
   - Filters superseded records (retains the superseding record)
   - Promotes bug records to blueprint type
   - Cleans dangling references left by filtered records
   - Resets all retained records to `status: open` and removes `sealed_at_sha`
4. Packages each layer as a local artifact file:
   - **provenance** (always included) — tar archive of individual `<id>.yml` files
   - **specs**, **code** — tar archive preserving repo-relative paths (for directories) or raw bytes with original extension (for single files)
   - **prompt** — raw bytes with original file extension
5. Computes SHA-256 for each layer artifact and a `root_hash` across all present layers in declaration order (`provenance`, `specs`, `code`, `prompt`)
6. Appends an immutable version entry to the manifest and updates the `latest` pointer

**Artifact files** are written next to the manifest: `linespec-provenance-v1.tar`, `linespec-specs-v1.tar`, `linespec-prompt-v1.md`, etc.

**URL fields** are always present in the manifest but left as empty strings — fill them in after uploading the artifacts to your hosting location:

```json
{
  "latest": "v1",
  "versions": {
    "v1": {
      "created_at": "2026-05-15T12:00:00Z",
      "root_hash": "a3f2...",
      "layers": {
        "provenance": {
          "sha256": "7b1c...",
          "url": ""
        }
      }
    }
  }
}
```

**Immutability:** `linespec publish` refuses to overwrite an existing version key and exits non-zero. To publish a new version, run `publish` again — it will auto-increment.

**When to use:** When sharing a governed project with another team or agent. The recipient runs `linespec clone <manifest-url>` to bootstrap a fully-governed local copy with provenance records, hooks, and config already in place.

### Clone

Bootstrap a new project directory from a published manifest:

```bash
# Clone from a manifest URL (destination dir defaults to manifest filename stem)
linespec clone https://example.com/linespec.manifest.json

# Pin to a specific version
linespec clone https://example.com/linespec.manifest.json@v2
linespec clone https://example.com/linespec.manifest.json --version v2

# Specify the destination directory name
linespec clone https://example.com/linespec.manifest.json --dir myproject
```

**Options:**
- `<manifest-url>` — Required. URL to a `linespec.manifest.json` file. Append `@version` to pin to a specific version.
- `--version label` — Pin to a specific version (overrides `@version` suffix)
- `--dir path` — Destination directory name (default: derived from the manifest URL's filename stem)
- `--help` — Show help

**What it does:**

1. Fetches the manifest JSON and resolves the target version (pinned or `latest`)
2. Verifies `root_hash` against the concatenation of all layer hashes before fetching any artifact — aborts on mismatch
3. Creates the destination directory and runs `git init`
4. Writes `.linespec.yml` with `provenance.manifest_url` set to the source URL (without `@version` suffix)
5. Installs git hooks via `linespec provenance install-hooks`
6. Downloads each layer artifact, verifies its SHA-256 hash, and extracts it:
   - **provenance** — extracted into `<dest>/provenance/`
   - **specs**, **code**, **prompt** — extracted into `<dest>/` preserving repo-relative paths

**When to use:** When a colleague or automated agent has published a governed project and you want a fully development-ready local copy with provenance records, hooks, and `.linespec.yml` already configured.

### Import

Import provenance records from a published manifest into an existing repository:

```bash
# Import all records from the latest manifest version
linespec import https://example.com/linespec.manifest.json

# Pin to a specific version
linespec import https://example.com/linespec.manifest.json@v3
linespec import https://example.com/linespec.manifest.json --version v2
```

**Options:**
- `<manifest-url>` — Required. URL to a `linespec.manifest.json` file. Append `@version` to pin.
- `--version label` — Pin to a specific version (overrides `@version` suffix)
- `--help` — Show help

**What it does:**

1. Fetches the manifest and verifies all layer hashes
2. Reads the record IDs from the provenance layer and checks for conflicts with the local `provenance/` directory — aborts without writing anything if any ID already exists
3. Extracts all records from the provenance layer into the local `provenance/` directory
4. Runs `linespec provenance lint` to surface any reference issues introduced by the import

**When to use:** When you want to pull in a set of published records to extend an existing repository's provenance graph. Use `clone` instead when starting from scratch.

### Deprecate

Mark record as deprecated:

```bash
linespec provenance deprecate --record prov-2026-a1b2c3d4 --reason "Replaced by new auth system"
```

**Options:**
- `--record prov-YYYY-XXXXXXXX` - Required. The record to deprecate
- `--reason "..."` - Deprecation reason
- `-c, --config path` - Use custom config

### Context

Show provenance context for specific files — which records govern them:

```bash
# Check which records govern specific files
linespec provenance context pkg/proxy/postgresql/proxy.go

# Check multiple files
linespec provenance context pkg/proxy/postgresql/proxy.go pkg/registry/registry.go

# Compact output
linespec provenance context --format compact pkg/proxy/**/*.go

# JSON output for tooling
linespec provenance context --format json pkg/proxy/postgresql/proxy.go
```

**Options:**
- `<files...>` - File paths to check (positional arguments)
- `--files f1 f2 f3` - Explicit file list (alternative to positional args)
- `--format format` - human|compact|json
- `-c, --config path` - Use custom config

**Output:**
- Shows all records whose scope matches the given files
- Follows `supersedes` chains to show ancestry
- Detects conflicts when multiple open records govern the same file

### Search (Semantic)

Search provenance records by semantic similarity:

```bash
# Search with natural language query
linespec provenance search --query "authentication system"

# Limit results
linespec provenance search --query "database schema" --limit 10

# Use custom config
linespec provenance search --query "API changes" -c /path/to/.linespec.yml
```

**Options:**
- `--query "text"` - Required. Natural language search query
- `--limit N` - Maximum results to return (default: 5)
- `-c, --config path` - Use custom config

**Requirements:**
- Requires embedding configuration in `.linespec.yml`
- Records must be indexed using `linespec provenance index`

### Audit (Semantic)

Audit recent changes against provenance history:

```bash
# Audit with description
linespec provenance audit --description "Added password validation"

# Use custom config
linespec provenance audit --description "Refactored user service" -c /path/to/.linespec.yml
```

**Options:**
- `--description "text"` - Required. Description of changes to audit
- `-c, --config path` - Use custom config

**Output:**
- Shows records with semantic similarity to your changes
- Advisory only - always exits 0
- Helps identify potential conflicts with prior decisions

### Index

Index provenance records for semantic search:

```bash
# Index all unindexed implemented records
linespec provenance index

# Dry run - show what would be indexed
linespec provenance index --dry-run

# Re-index all records (even if already indexed)
linespec provenance index --force

# Use custom config
linespec provenance index -c /path/to/.linespec.yml
```

**Options:**
- `--dry-run` - Show what would be indexed without making API calls
- `--force` - Re-index even if embedding already exists
- `-c, --config path` - Use custom config

**When to use:**
- After enabling embedding configuration for the first time
- To backfill historical records
- After changing embedding models or formats

### Install Hooks

Install git hooks for automatic validation:

```bash
linespec provenance install-hooks
```

### Install Skills (Claude Code / AI Agents)

Install all LineSpec Claude Code skills into a skills directory:

```bash
# Install to .claude/skills/ (default)
linespec provenance install-skills

# Override the target directory
linespec provenance install-skills --path path/to/skills
```

This installs two skills:

- **`provenance`** — encodes the full provenance record workflow so AI agents automatically follow the correct create → implement → complete lifecycle. Invoke with `/provenance` in Claude Code.
- **`linespec-testing`** — covers running, writing, and debugging LineSpec integration tests. Invoke with `/linespec-testing` in Claude Code.

Existing skill directories are overwritten silently. Both skills are always installed together.

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
  
  # Run associated_specs before allowing a completion-transition commit (default: false)
  run_associated_specs_on_complete: false
  
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
| `run_associated_specs_on_complete` | bool | `false` | Run specs on completion transition |
| `shared_repos` | array | `[]` | Additional directories |

### Semantic Search Configuration

Enable semantic search over provenance records using your choice of embedding provider:

**Voyage AI (default):**
```yaml
provenance:
  embedding:
    provider: voyage                 # Embedding provider
    index_model: voyage-4-large     # Model for indexing (2048 dims)
    query_model: voyage-4-lite      # Model for queries (2048 dims)
    api_key: ${VOYAGE_API_KEY}      # API key from environment
    similarity_threshold: 0.50        # Minimum similarity (0.50-0.70 range)
    index_on_complete: true         # Auto-index on complete
```

**OpenAI:**
```yaml
provenance:
  embedding:
    provider: openai                 # Embedding provider
    index_model: text-embedding-3-small   # Model for indexing
    query_model: text-embedding-3-small   # Model for queries
    api_key: ${OPENAI_API_KEY}      # API key from environment
    similarity_threshold: 0.50        # Minimum similarity
    index_on_complete: true         # Auto-index on complete
```

**Configuration Options:**

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `provider` | string | - | Embedding provider: `voyage` or `openai` |
| `index_model` | string | Provider-specific | Model for document indexing |
| `query_model` | string | Provider-specific | Model for query embeddings |
| `api_key` | string | - | API key (use `${ENV_VAR}` format) |
| `similarity_threshold` | float | `0.50` | Minimum similarity for results |
| `index_on_complete` | bool | `true` | Auto-generate embeddings on complete |

**Voyage AI Models:**
- `voyage-4-large` - High-quality model for document embeddings (input_type: "document")
- `voyage-4-lite` - Efficient model for query embeddings (input_type: "query")
- Both output 2048-dimensional vectors in a shared embedding space
- Cross-model similarity is valid due to Voyage's training process

**OpenAI Models:**
- `text-embedding-3-small` - Fast, cost-effective embeddings (1536 dims)
- `text-embedding-3-large` - Best performance embeddings (3072 dims)
- `text-embedding-ada-002` - Legacy model (1536 dims)

**Note:** When using OpenAI, both document and query embeddings use the same model (no separate input_type handling).

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
git commit -m "Add user authentication [prov-2026-a1b2c3d4]"

# Multiple records
git commit -m "Fix auth and caching [prov-2026-a1b2c3d4] [prov-2026-deadbeef]"

# Service-specific
git commit -m "Update user service [prov-2026-a1b2c3d4-user-service]"
```

### Pre-commit Hook

The pre-commit hook validates that modified provenance records are well-formed, and optionally runs specs when a record is completed:

- **Linting**: Checks YAML syntax, required fields, and valid values
- **Quick validation**: Ensures records can be parsed and loaded
- **Spec execution** (opt-in): When `run_associated_specs_on_complete: true` is set in `.linespec.yml`, detects `open` → `implemented` status transitions and runs the record's `associated_specs` before allowing the commit. Supported types: `linespec`, `rspec`, `pytest`, `jest`. Use `run_command` on any spec entry to override the default command for that type.

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
# This will FAIL - prov-2026-a1b2c3d4 is already implemented
git commit -m "Fix typo [prov-2026-a1b2c3d4]"
# Error: prov-2026-a1b2c3d4 is already implemented - cannot commit with this ID. 
#        Create a new record or supersede this one.

# Instead, create a new record or supersede:
linespec provenance create --title "Fix typo in auth" --supersedes prov-2026-a1b2c3d4
git commit -m "Fix typo [prov-2026-deadbeef]"
```

The only exception is the completion transition (when a record's own file changes from `status: open` to `status: implemented`), which is allowed.

### Git Hook Installation

```bash
# Install both hooks automatically
linespec provenance install-hooks

# This creates:
#   .git/hooks/pre-commit  - Lints records; runs associated_specs on completion transitions
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

### GitHub Code Scanning Integration (SARIF)

Upload provenance lint results to GitHub Code Scanning for inline PR annotations and repository-level alerts:

```yaml
# GitHub Actions workflow
- name: Lint provenance records
  run: linespec provenance lint --format sarif > provenance-results.sarif
  continue-on-error: true

- name: Upload to Code Scanning
  uses: github/codeql-action/upload-sarif@v3
  with:
    sarif_file: provenance-results.sarif
    category: linespec-provenance
```

**Benefits:**
- **Inline PR annotations** - Violations appear directly in the Files Changed view
- **Repository dashboard** - Track all provenance alerts in one place
- **Alert deduplication** - GitHub uses file hashes to avoid duplicate alerts
- **Suppression** - Use GitHub's alert management UI to dismiss false positives

**Rule IDs:**
The SARIF output uses stable rule IDs (PROV001-PROV022, PROV-IMM) that persist across LineSpec versions:
- PROV001: InvalidYaml
- PROV002: MissingRequiredField
- PROV003: UnknownStatus
- PROV006: UnresolvedSupersedes
- PROV020: SupersessionTypeMismatch (supersedes crosses tier boundaries)
- PROV021: ImplementsTypeMismatch (implements skips a tier or points sideways)
- PROV022: UnresolvedImplements (implements references a non-existent record)
- PROV-IMM: ContentHashMismatch (implemented record content differs from sealed hash in `.linespec/hash_manifest.json`)
- ... (see full catalog in SARIF output)

**Example PR annotation:**
When a commit references an implemented record, GitHub will show an inline annotation:
```
⚠ PROV010: MissingAssociatedSpecs
Record prov-2026-001 is open with no associated specs.
```

### Automated Embedding Indexing

When semantic search is enabled, a GitHub Action workflow automatically indexes newly completed provenance records when they are merged to the main branch. This ensures the embedding store stays up-to-date without manual intervention.

**How it works:**
1. When a record is marked `implemented` via `linespec provenance complete`, the embedding is generated locally (if `index_on_complete: true`)
2. When the record is merged to main, the GitHub Action runs and indexes any unindexed records
3. The action respects Voyage AI rate limits and handles batching automatically

**Configuration for CI/CD:**

For CI/CD environments where you want to skip local embedding generation (to avoid rate limits), set `index_on_complete: false` in `.linespec.yml`:

```yaml
provenance:
  embedding:
    provider: voyage
    index_model: voyage-4-large
    query_model: voyage-4-lite
    api_key: ${VOYAGE_API_KEY}
    similarity_threshold: 0.50
    index_on_complete: false  # Skip local embedding, let GitHub Action handle it
```

**Required Secret:**

Add `VOYAGE_API_KEY` as a repository secret in GitHub:
1. Go to Settings → Secrets and variables → Actions
2. Click "New repository secret"
3. Name: `VOYAGE_API_KEY`
4. Value: Your Voyage AI API key

**Workflow file:** `.github/workflows/index-provenance.yml`

The workflow triggers on pushes to main that modify provenance records and automatically indexes them.

---

## Sealed at SHA and Stale Scope Warnings

### Hash Manifest (`.linespec/hash_manifest.json`)

When a record is completed, `linespec provenance complete` seals a **SHA-256 content hash** of the record into `.linespec/hash_manifest.json`. This file is created automatically on first use and is committed alongside the completion commit.

**Structure:**

```json
{
  "records": {
    "prov-2026-a1b2c3d4": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
    "prov-2026-deadbeef": "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9820"
  },
  "full_graph_hash": "abc123...",
  "active_subset_hash": "def456..."
}
```

- **`records`** — Map of record ID to SHA-256 hex digest. The hash is computed over `yaml.Marshal` of the record with `FilePath` cleared, making it deterministic and path-independent.
- **`full_graph_hash`** — SHA-256 of the sorted concatenation of all per-record hashes (all statuses).
- **`active_subset_hash`** — Same, but only over records not in `superseded` or `deprecated` status.

Both graph hashes are recomputed on every `complete` invocation.

**Immutability enforcement:**

`linespec provenance lint` includes a **PROV-IMM** check that computes the current hash of each implemented record and compares it against the manifest. A mismatch is reported as an error. This check:
- Requires no git access — fully runnable in any CI environment or git hook
- Is silent for records that predate the hash system (no manifest entry)
- Is silent if `.linespec/hash_manifest.json` does not exist (graceful bootstrap)

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
3. **Use ID suffixes** - `prov-2026-a1b2c3d4-user-service`

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
# 1. Validate, then create (status: open) — standalone commit
linespec provenance lint
linespec provenance check
linespec provenance create --title "New feature" --no-edit
git commit -m "Create provenance record [prov-2026-deadbeef]"

# 2. Develop (make commits, scope auto-populates)
#    Run check --staged before each implementation commit
linespec provenance check --staged
git commit -m "Implement feature [prov-2026-deadbeef]"

# 3. Lock scope (when feature is complete)
linespec provenance lock-scope --record prov-2026-deadbeef

# 4. Validate, then complete (status: implemented) — standalone commit
linespec provenance lint
linespec provenance check
linespec provenance complete --record prov-2026-deadbeef
git commit -m "Complete provenance record [prov-2026-deadbeef]"

# 5. (Optional) Supersede later
linespec provenance create --title "Better approach" --supersedes prov-2026-deadbeef
```

---

## Examples

### Example 1: Simple Architecture Decision

```yaml
id: prov-2026-a1b2c3d4
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
id: prov-2026-deadbeef-user-service
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
id: prov-2026-01234567
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

supersedes: prov-2026-a1b2c3d4
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
- Review the changelog: CHANGELOG.md

---

## License

MIT License - See LICENSE file for details.
