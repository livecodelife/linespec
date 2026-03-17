# Agent Guidelines for LineSpec v1.0.0

This file provides guidance for agents working with LineSpec, organized by feature status.

---

## For AI Agents Helping End Users

### Reading Order (Updated for v1.0.0)

When a user has installed LineSpec v1.0.0:

1. **First:** `AGENTS.md` (this file) - for project context
2. **Second:** `PROVENANCE_RECORDS.md` - for the stable Provenance Records feature
3. **Third:** `README.md` - for installation and overview
4. **Fourth:** `LINESPEC.md` - for beta LineSpec Testing features (only if requested)

### Feature Status

| Feature | Status | Documentation |
|---------|--------|---------------|
| **Provenance Records** | ✅ **Stable (v1.0.0)** | PROVENANCE_RECORDS.md |
| **LineSpec Testing** | 🚧 **Beta** | LINESPEC.md |

### Documentation Location

After installation, docs are in the repository root:
- `PROVENANCE_RECORDS.md`
- `README.md`
- `AGENTS.md`
- `LINESPEC.md` (Beta)

---

## Project Overview (v1.0.0)

LineSpec v1.0.0 makes **Provenance Records** the primary, stable feature - structured YAML artifacts that capture architectural decisions. **LineSpec Testing** remains in beta as an opt-in feature.

### What's New in v1.0.0

**Stable:**
- Provenance Records CLI subsystem (`provenance` command)
- Git integration with pre-commit hooks
- Graph visualization of decisions
- Scope enforcement (affected_scope, forbidden_scope)
- Monorepo support with ID suffixes (e.g., `prov-2026-a1b2c3d4-user-service`)

**Beta (build with `-tags beta`):**
- LineSpec Testing (`test` command)
- Protocol proxies (`proxy` command)
- DSL-based integration testing

### Installation

```bash
# Stable (Provenance Records only)
brew tap livecodelife/linespec
brew install linespec
# OR
go install github.com/livecodelife/linespec/cmd/linespec@v1.1.0

# Beta (all features)
brew install linespec-beta
# OR
go install -tags beta github.com/livecodelife/linespec/cmd/linespec@v1.1.0
```

---

## Part 1: Provenance Records (Stable)

### Core Concepts

Provenance Records are structured YAML files that document:

- **Intent** - What we want to achieve and why
- **Constraints** - Rules that must be followed  
- **Scope** - What files are affected (or explicitly forbidden)
- **Status** - Where in the lifecycle (open → implemented → superseded/deprecated)
- **Relationships** - How decisions connect via supersedes/superseded_by

They provide a **queryable history** of architectural decisions that can be linted, graphed, and enforced at commit time.

### Quick Start

```bash
# Create a record
linespec provenance create --title "Add user authentication"

# Validate
linespec provenance lint

# View graph
linespec provenance graph

# Mark as implemented
linespec provenance complete --record prov-2026-001
```

### Schema Reference

```yaml
id: prov-YYYY-XXXXXXXX               # or prov-YYYY-XXXXXXXX-service-name (8 hex chars)
title: "Decision title"
status: open|implemented|superseded|deprecated
created_at: "YYYY-MM-DD"
author: "email@example.com"

intent: >
  What we want to achieve and why.

constraints:
  - Rule 1
  - Rule 2

affected_scope:
  - pkg/feature/**
  - "re:pattern"

forbidden_scope:
  - "re:.*_test\\.go$"

supersedes: "prov-2026-001"          # or null
superseded_by: "prov-2026-003"      # or null
related:
  - prov-2026-002

associated_specs:
  - path: tests/user_creation_spec.rb
    type: rspec
  - path: tests/create_user.linespec
    type: linespec

tags:
  - architecture
  - feature
```

### Pattern Matching for Scope

- **Exact:** `pkg/proxy/mysql/proxy.go`
- **Glob:** `pkg/proxy/**/*.go`
- **Regex:** `"re:.*_test\\.go$"`

### Scope Modes

**Observed Mode** (empty affected_scope):
- Tracks all changed files automatically from git
- Good for early development
- No enforcement until locked

**Allowlist Mode** (non-empty affected_scope):
- Only allows changes to listed files
- Good for mature, locked-in decisions
- Use `lock-scope` to transition from observed

```bash
linespec provenance lock-scope --record prov-2026-001
```

### Git Integration

**Two-Hook Strategy:**
The provenance system uses two git hooks that work together:

1. **pre-commit hook**: Runs first, lints modified provenance records for syntax/validity
2. **commit-msg hook**: Runs after you write your message, checks staged files against provenance scope

**Commit message format:**
```bash
git commit -m "Add feature [prov-2026-042]"
git commit -m "Fix auth [prov-2026-001] [prov-2026-002]"
```

**Install git hooks:**
```bash
linespec provenance install-hooks  # Installs both pre-commit and commit-msg hooks
```

**Note:** The hooks respect the local `./linespec` binary when available (for development), otherwise fall back to the system `linespec`.

### Configuration (.linespec.yml)

```yaml
provenance:
  dir: provenance                    # Records directory (default: provenance)
  enforcement: warn                  # none|warn|strict
  commit_tag_required: false       # Require IDs in commits
  auto_affected_scope: true        # Auto-populate from git history
  shared_repos: []                 # Additional directories (monorepos)
```

### CLI Commands

| Command | Description |
|---------|-------------|
| `create` | Create new record with optional --title, --supersedes, --tag |
| `lint` | Validate records; use --record, --enforcement, --format |
| `status` | View status; use --record, --filter, --save-scope |
| `graph` | Render graph; use --root, --filter, --format |
| `search` | Semantic search; use --query, --limit (requires embedding config) |
| `audit` | Audit changes; use --description (requires embedding config) |
| `index` | Index records; use --dry-run, --force (requires embedding config) |
| `check` | Check commits; use --commit, --range, --record, --staged, --message-file |
| `lock-scope` | Lock to allowlist; use --record, --dry-run |
| `complete` | Mark record as implemented; use --record, --force |
| `deprecate` | Mark record as deprecated; use --record, --reason |
| `install-hooks` | Install git hooks |

### Semantic Search

LineSpec supports semantic search over provenance records using Voyage AI embeddings. This allows natural language queries to find relevant historical decisions.

**Configuration:**
Add to `.linespec.yml`:
```yaml
provenance:
  embedding:
    provider: voyage
    index_model: voyage-4-large     # High-quality model for indexing
    query_model: voyage-4-lite      # Efficient model for queries
    api_key: ${VOYAGE_API_KEY}
    similarity_threshold: 0.50
    index_on_complete: true
```

**Commands:**
```bash
# Search for relevant records
linespec provenance search --query "git hooks" --limit 5

# Audit recent changes against history
linespec provenance audit --description "Added authentication middleware"

# Bulk index all implemented records
linespec provenance index

# Re-index with force (even if already indexed)
linespec provenance index --force

# Preview what would be indexed
linespec provenance index --dry-run
```

**How it works:**
- Records are embedded using `voyage-4-large` with `input_type: "document"` at complete time
- Queries are embedded using `voyage-4-lite` with `input_type: "query"`
- Both models output 2048-dimensional vectors in a shared embedding space
- Similarity scores typically range 0.50-0.70 for relevant matches
- Threshold of 0.50 effectively captures semantically related records

**Embedding input format:**
```
Decision: [full title]
Intent: [complete intent field]
Constraints: [constraint 1]. [constraint 2]. [constraint 3].
```

**[Complete reference → PROVENANCE_RECORDS.md](./PROVENANCE_RECORDS.md)**

---

## Part 2: LineSpec Testing (Beta)

> 🚧 **Beta Feature** - Build with `-tags beta` to enable

LineSpec Testing is a DSL-based integration testing framework for containerized services. It intercepts database and HTTP traffic at the protocol level, making tests language-agnostic.

### Installation (Beta)

```bash
go build -tags beta -o linespec ./cmd/linespec
# OR
go install -tags beta github.com/livecodelife/linespec/cmd/linespec@v1.1.0
```

### Beta Commands

```bash
# Run integration tests
linespec test <path-to-linespec-files>

# Start protocol proxies
linespec proxy mysql <listen-addr> <upstream-addr>
linespec proxy postgresql <listen-addr> <upstream-addr>
linespec proxy http <listen-addr> <upstream-addr>
linespec proxy kafka <listen-addr> <upstream-addr>
```

### DSL Quick Reference

```linespec
TEST create-user
RECEIVE HTTP:POST http://localhost:3000/users
WITH {{payloads/request.yaml}}
HEADERS
  Authorization: Bearer token

EXPECT WRITE:MYSQL users
WITH {{payloads/data.yaml}}
VERIFY query CONTAINS 'password_digest'

RESPOND HTTP:201
WITH {{payloads/response.yaml}}
NOISE
  body.id
  body.created_at
```

### Statement Types

| Statement | Description |
|-----------|-------------|
| `TEST` | Test name (optional, defaults to filename) |
| `RECEIVE` | Incoming HTTP request (required, first) |
| `EXPECT` | External dependencies (HTTP, DB reads/writes, events) |
| `EXPECT_NOT` | Negative assertions (what should NOT happen) |
| `RESPOND` | System response (required, last) |
| `NOISE` | Fields to ignore in comparison |

### VERIFY Operators

```linespec
VERIFY query CONTAINS 'string'        # Substring match
VERIFY query NOT_CONTAINS 'string'    # Negative substring match  
VERIFY query MATCHES /regex/          # Regex match (Go regexp)
```

**Best Practices:**

Use `MATCHES` with word boundaries (`\b`) for precise column name matching:
```linespec
VERIFY query MATCHES /\bpassword_digest\b/  # Matches exact column name
```

Use backticks with `NOT_CONTAINS` to check for exact column references:
```linespec
VERIFY query NOT_CONTAINS '`password`'     # Avoids false positives
```

### Configuration (.linespec.yml)

```yaml
service:
  name: todo-api
  type: web
  port: 3000
  docker_compose: docker-compose.yml

database:
  type: mysql|postgresql
  image: mysql:8.4
  port: 3306

infrastructure:
  database: true
  kafka: false
```

**[Complete DSL reference → LINESPEC.md](./LINESPEC.md)**

---

## Part 3: Build, Test, and Development

### Build Commands

```bash
# Stable build (Provenance only)
go build -o linespec ./cmd/linespec

# Beta build (all features)
go build -tags beta -o linespec ./cmd/linespec

# Development
go run ./cmd/linespec
go run -tags beta ./cmd/linespec

# Cross-compile
GOOS=darwin GOARCH=amd64 go build -o linespec-darwin-amd64 ./cmd/linespec
GOOS=linux GOARCH=amd64 go build -o linespec-linux-amd64 ./cmd/linespec
```

### Test Commands

```bash
# Run all tests
go test ./...

# Run specific package tests
go test ./pkg/provenance/...
go test -v ./pkg/provenance/...

# Run single test
go test -run TestName ./pkg/provenance
```

### Code Style Guidelines

**Go Version:** 1.21+

**Imports:**
```go
import (
    "fmt"
    "os"
    
    "gopkg.in/yaml.v3"
    
    "github.com/livecodelife/linespec/pkg/dsl"
)
```

**Naming Conventions:**
- Files: `snake_case.go` (e.g., `lexer.go`, `parser.go`)
- Types: `PascalCase` (e.g., `TestSpec`, `ExpectStatement`)
- Functions/Variables: `camelCase` (e.g., `tokenize`, `specFile`)
- Packages: `lowercase` (e.g., `dsl`, `runner`, `proxy`)

**Error Handling:**
```go
type LineSpecError struct {
    Message string
    Line    int
}

func (e *LineSpecError) Error() string {
    if e.Line > 0 {
        return fmt.Sprintf("line %d: %s", e.Line, e.Message)
    }
    return e.Message
}
```

### Project Structure

```
cmd/
  linespec/                    # CLI entry point
    main_stable.go              # Provenance only (build !beta)
    main_beta.go                # All features (build beta)

pkg/
  provenance/                   # ✅ Stable - Provenance Records
    commands.go
    types.go
    loader.go
    linter.go
    git.go
    
  proxy/                        # 🚧 Beta - Protocol proxies
    mysql/
    postgresql/
    http/
    kafka/
    
  runner/                       # 🚧 Beta - Test execution
  registry/                     # 🚧 Beta - Mock registry
  dsl/                          # 🚧 Beta - DSL parser
  config/                       # Shared - Configuration
  logger/                       # Shared - Logging
  types/                        # Shared - Core data structures

docs/                           # Documentation
  PROVENANCE_RECORDS.md         # ✅ Stable reference
  LINESPEC.md                   # 🚧 Beta reference
  AGENTS.md                     # This file
  README.md
  RELEASE_PLAN.md
  
provenance/                     # Your provenance records
  prov-2026-001.yml
  prov-2026-002.yml
  ...
```

---

## Part 4: Provenance Records Rules

### Constraints Field Guidelines

**Constraints should describe behavioral rules** — what the system must or must not do — rather than listing implementation actions like "add X to file Y".

**Constraints should describe the expected behavior AFTER the changes**, not explain how to make the changes. For example, say "The system MUST do X" not "We MUST change Y to make X happen."

**Good constraints (behavioral):**
```yaml
constraints:
  - MUST validate email format before saving user records
  - MUST NOT allow password reuse from last 5 iterations
  - MUST encrypt sensitive fields at rest using AES-256
  - MUST log all authentication attempts with timestamp and IP
```

**Avoid (implementation details):**
```yaml
constraints:
  - MUST add ValidateEmail() function to pkg/users/validator.go  # Too specific
  - MUST update database schema to add encrypted_password column  # Implementation detail
  - MUST write unit tests for all new functions  # Process, not behavior
```

**Why behavioral constraints matter:**
- They describe the contract the system must uphold
- They're testable (can be verified against behavior, not file contents)
- They remain valid even as implementation changes
- They focus on user-visible outcomes rather than internal mechanics

**When to list implementation:**
Use `affected_scope` to specify which files/modules will be modified. Keep constraints focused on what those changes must accomplish.

### Never Implement Without User Confirmation

**CRITICAL RULE:** Never change a provenance record's status to `implemented` without explicitly asking the user first.

Provenance records represent architectural decisions with meaningful lifecycles:
- **Open** — Decision is being discussed/developed
- **Implemented** — Decision is final and immutable
- **Superseded** — Replaced by a newer record
- **Deprecated** — No longer relevant

**When marked `implemented`:**
- All fields become immutable (except `monitors` and `associated_traces`)
- No more scope changes allowed
- The decision is "locked in"

**Correct workflow:**
```
1. Create record → status: open
2. Make changes described in the record
3. Run tests to verify
4. ASK USER: "Should I mark prov-YYYY-XXXXXXXX as implemented?"
5. Only after confirmation: linespec provenance complete --record prov-YYYY-XXXXXXXX
```

### Provenance Required for All Decisions

**CRITICAL RULE:** Create a provenance record for every architectural or behavioral decision that affects the codebase, unless covered by an existing open record.

**CRITICAL RULE:** Always use the CLI to create provenance records:
```bash
linespec provenance create --title "Your decision title"
```
**Important:** Add `--no-edit` flag when running in non-interactive environments (like automated scripts or when you don't want to open an editor):
```bash
linespec provenance create --title "Your decision title" --no-edit
```
Never write provenance record YAML files manually. The CLI ensures proper ID generation, validation, and metadata.

**CRITICAL RULE:** Always validate provenance records before proceeding:
```bash
# After creating a record, lint it to ensure no errors
linespec provenance lint --record prov-YYYY-XXXXXXXX

# For open records with associated_specs, run checks too
linespec provenance check --record prov-YYYY-XXXXXXXX
```
Any lint errors or check failures must be fixed before proceeding with implementation.

**When to create a new record:**
- Adding new functionality or features
- Changing existing behavior or APIs
- Modifying configuration systems or patterns
- Introducing new dependencies or tools
- Refactoring with behavioral impact

**When to update an existing record:**
- The work falls within the scope of an **open** record (status: open)
- The record is in **observed mode** (empty affected_scope)
- The changes are consistent with the record's intent

**Correct workflow:**
```
1. Identify the work to be done
2. Check for existing open provenance records covering this scope
3. IF no open record exists:
   → Create new record with status: open
   → Get user confirmation
   → Make the changes
4. IF open record exists in observed mode:
   → Update affected_scope as you work
   → Changes automatically tracked
5. IF open record exists in allowlist mode:
   → Verify changes match affected_scope
   → Discuss scope expansion with user if needed
```

### Self-Modification Exception

**Open records can modify their own YAML files.** When a commit is tagged with a provenance record ID and modifies that record's own file, the commit is allowed even if the file is not explicitly listed in `affected_scope`. This enables the natural workflow:

```
1. Create record (status: open)
2. Make code changes, commit with [prov-YYYY-XXXXXXXX]
3. Complete the record: linespec provenance complete --record prov-YYYY-XXXXXXXX
4. Commit the completion: git commit -m "Complete [prov-YYYY-XXXXXXXX]"
   ↑ This commit is allowed because it's the completion transition!
```

**Important:**
- The exception only applies when `status: open`
- If the record file is in `forbidden_scope`, changes are blocked regardless
- Once `status: implemented`, the record becomes immutable
- The commit-msg hook enforces this validation

### Never Add Record to Its Own Scope

**CRITICAL RULE:** Never add a provenance record's own YAML file to its `affected_scope`.

The self-modification exception already handles this case - open records can always modify their own YAML files when tagged with that record's ID. Adding the record file to `affected_scope` is unnecessary and causes stale scope warnings after the record is completed.

**Correct approach:**
```yaml
affected_scope:
    - CHANGELOG.md           # Files that need to change for this release
    # provenance/prov-2026-XXX.yml - DO NOT add this!
```

**Why this matters:**
- The self-modification exception already allows modifications to the record file
- Adding it to scope creates a false positive stale scope warning after completion
- The record file changes are tracked automatically by the commit-msg hook
- Keeps `affected_scope` focused on actual implementation files

### Complete Workflow Example

```bash
# 1. Create a new record
linespec provenance create --title "Add user authentication"
# Creates prov-2026-XXXXXXXX with status: open

# 2. Make implementation changes
git add src/auth/
git commit -m "Add auth module [prov-2026-XXXXXXXX]"
# → pre-commit hook: lints the record
# → commit-msg hook: checks staged files are in scope (or auto-tracked in observed mode)

# 3. Complete the implementation work
# ... more commits as needed ...

# 4. Mark record as implemented
linespec provenance complete --record prov-2026-XXXXXXXX
# Updates status: implemented

git add provenance/prov-2026-XXXXXXXX.yml
git commit -m "Complete user auth [prov-2026-XXXXXXXX]"
# → commit-msg hook: allows this because it's the completion commit!
#   (open → implemented transition with only the status field changing)
```

---

### Never Bypass Git Hooks

**CRITICAL RULE:** Never use `--no-verify` or `--no-hooks` flags when committing changes.

The git hooks are there for a reason - they enforce provenance scope rules and ensure code quality. Bypassing them:
- Allows commits that violate scope constraints
- Breaks the provenance tracking workflow
- Can introduce bugs or unapproved changes

**If a commit is blocked by hooks:**
1. Read the error message carefully
2. Fix the underlying issue (wrong files staged, scope violations, etc.)
3. Re-stage and commit normally
4. Ask the user for guidance if you're unsure

**Never do this:**
```bash
git commit --no-verify -m "message"  # WRONG - bypasses hooks
git commit --no-hooks -m "message"     # WRONG - bypasses hooks
```

**Always do this:**
```bash
git commit -m "message [prov-YYYY-XXXXXXXX]"  # CORRECT - runs hooks normally
```

---

## Part 5: Important Notes for AI Agents

### What LineSpec Actually Does

**Misconception:** LineSpec "compiles" .linespec files to Keploy artifacts.
**Reality:** LineSpec parses and executes .linespec files directly. No compilation step.

**Misconception:** There's a `linespec compile` command.
**Reality:** No compile command. Use `linespec test` directly (requires beta build).

**Misconception:** LineSpec generates YAML artifact files.
**Reality:** Uses internal registry system. Executes tests directly.

### Common User Questions

**Q: Where are my compiled YAML files?**
A: LineSpec doesn't generate YAML files. Provenance Records are the authoritative source.

**Q: How do I run my tests?**
A: Build with `-tags beta`, then use `linespec test <path>`.

**Q: Why is my test failing with "mock not called"?**
A: HTTP mocks defined with EXPECT must be invoked. Tests fail if mocks are defined but not used to catch silent failures.

**Q: How do I configure my service?**
A: Create a `.linespec.yml` file with service, database, infrastructure sections.

**Q: What's the difference between stable and beta?**
A: Stable includes Provenance Records only. Beta adds LineSpec Testing features.

### Testing Guidelines

- Use Go's standard `testing` package
- Use table-driven tests for multiple cases
- Use `t.Run()` for subtests
- Use `t.Cleanup()` for cleanup
- Test both success and failure cases

**Example:**
```go
func TestTokenize(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected []Token
    }{
        {"simple", "TEST foo", []Token{...}},
        {"complex", "EXPECT HTTP:GET...", []Token{...}},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := tokenize(tt.input)
            if !reflect.DeepEqual(got, tt.expected) {
                t.Errorf("tokenize() = %v, want %v", got, tt.expected)
            }
        })
    }
}
```

### Payload File Conventions

**HTTP Request/Response:**
```yaml
name: User One
email: user_one@example.com
```

**MySQL Read Results:**
```yaml
rows:
  - id: 1
    name: User One
    email: user_one@example.com
```

**Empty Results:**
Use `RETURNS EMPTY` instead of payload files:
```linespec
EXPECT READ:MYSQL users
USING_SQL """SELECT * FROM users WHERE id = 999"""
RETURNS EMPTY
```

---

## Part 6: Common Testing Patterns (Beta)

### Creating a POST Endpoint Test

```linespec
TEST create-resource
RECEIVE HTTP:POST http://localhost:3000/resources
WITH {{payloads/resource_create_req.yaml}}
HEADERS
  Authorization: Bearer token123

EXPECT WRITE:MYSQL resources
WITH {{payloads/resource_create_req.yaml}}

RESPOND HTTP:201
WITH {{payloads/resource_create_resp.yaml}}
NOISE
  body.id
  body.created_at
```

### Creating a GET Endpoint Test

```linespec
TEST get-resource
RECEIVE HTTP:GET http://localhost:3000/resources/1
HEADERS
  Authorization: Bearer token123

EXPECT READ:MYSQL resources
RETURNS {{payloads/resource_single.yaml}}

RESPOND HTTP:200
WITH {{payloads/resource_single.yaml}}
```

### Handling Validation Queries

```linespec
TEST create-user-with-validation
RECEIVE HTTP:POST http://localhost:3000/users
WITH {{payloads/user_create_req.yaml}}

# Validation query - returns empty (no duplicate)
EXPECT READ:MYSQL users
USING_SQL """SELECT 1 AS one FROM users WHERE email = 'test@example.com' LIMIT 1"""
RETURNS EMPTY

# The actual INSERT
EXPECT WRITE:MYSQL users
WITH {{payloads/user_create_req.yaml}}

RESPOND HTTP:201
```

### Verifying SQL Structure

```linespec
TEST create-user-secure
RECEIVE HTTP:POST http://localhost:3000/users
WITH {{payloads/user_create_req.yaml}}

EXPECT WRITE:MYSQL users
WITH {{payloads/user_with_password_digest.yaml}}
VERIFY query MATCHES /\bpassword_digest\b/
VERIFY query NOT_CONTAINS '`password`'

RESPOND HTTP:201
WITH {{payloads/user_create_resp.yaml}}
NOISE
  body.id
  body.created_at
  body.updated_at
```

### Using EXPECT_NOT

```linespec
TEST efficient-user-lookup
RECEIVE HTTP:GET http://localhost:3000/users/123

# Assert that we DON'T do a full table scan
EXPECT_NOT READ:MYSQL users
USING_SQL """SELECT * FROM users"""

# Should use indexed lookup instead
EXPECT READ:MYSQL users
USING_SQL """SELECT * FROM users WHERE id = 123 LIMIT 1"""
RETURNS {{payloads/user.yaml}}

RESPOND HTTP:200
WITH {{payloads/user.yaml}}
```

### Testing PostgreSQL

```linespec
TEST create-item
RECEIVE HTTP:POST http://localhost:3000/items
WITH {{payloads/item_create.yaml}}

EXPECT WRITE:POSTGRESQL items
WITH {{payloads/item_create.yaml}}

RESPOND HTTP:201
```

---

## Summary

**For Provenance Records (Stable v1.0.0):**
- Focus on PROVENANCE_RECORDS.md
- Use stable installation commands
- Follow the provenance workflow
- Never mark as implemented without asking

**For LineSpec Testing (Beta):**
- Build with `-tags beta`
- See LINESPEC.md for DSL reference
- Report issues on GitHub
- Features may change before stable release

**Quick links:**
- [PROVENANCE_RECORDS.md](./PROVENANCE_RECORDS.md) - Stable reference
- [LINESPEC.md](./LINESPEC.md) - Beta reference
- [README.md](./README.md) - Overview and installation
- [RELEASE_PLAN.md](./RELEASE_PLAN.md) - v1.0.0 release details
