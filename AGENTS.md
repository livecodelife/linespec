# Agent Guidelines for LineSpec

This file provides guidance for agents working on the LineSpec codebase.

## For AI Agents Helping End Users

When a user has installed LineSpec and asks you to help write LineSpec files:

1. **Access documentation via CLI:**
   ```bash
   linespec docs              # Shows all documentation paths
   linespec docs --linespec   # Path to LINESPEC.md (language reference)
   linespec docs --agents     # Path to this file
   linespec docs --readme     # Path to README.md
   ```

2. **Read files in this order:**
   - First: `AGENTS.md` (this file) - for project context
   - Second: `LINESPEC.md` - for complete syntax reference
   - Third: `README.md` - for usage examples

3. **Documentation location:** After installation, docs are in:
   - `<go-install-path>/pkg/mod/github.com/anomalyco/linespec@<version>/docs/`
   - Or use the `linespec docs` command to get the exact path

---

## Project Overview

LineSpec is a DSL compiler for generating Keploy-compatible KTests and KMocks from human-readable service behavior specifications. It's a Go CLI tool designed for TDD workflows.

---

## Build, Lint, and Test Commands

### Installation
```bash
go install ./cmd/linespec
```

### Build
```bash
go build -o linespec ./cmd/linespec
```

### Development
```bash
go run ./cmd/linespec
```

### Test
```bash
go test ./...
```

### Run a Single Test
```bash
go test -run TestName ./pkg/compiler
```

Or use verbose mode:
```bash
go test -v ./...
```

---

## Code Style Guidelines

### Go Configuration
- Go version: 1.25.7
- Standard Go formatting (use `gofmt` or `go fmt`)

### Imports
- Use standard Go import style
- Group imports: standard library, then third-party, then internal
- Example:
  ```go
  import (
      "fmt"
      "os"
      
      "gopkg.in/yaml.v3"
      
      "github.com/anomalyco/linespec/pkg/parser"
  )
  ```

### Naming Conventions
- **Files**: snake_case (e.g., `lexer.go`, `parser.go`)
- **Types/Interfaces**: PascalCase (e.g., `TestSpec`, `ExpectStatement`)
- **Structs**: PascalCase (e.g., `LineSpecError`)
- **Functions/Variables**: camelCase (e.g., `tokenize`, `specFile`)
- **Constants**: PascalCase or camelCase (e.g., `ExamplesDir`)
- **Package names**: lowercase, single word (e.g., `parser`, `compiler`)

### Error Handling
- Use idiomatic Go error handling with `error` interface
- Create custom error types when needed
- Include context in error messages
- Example:
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

### Code Structure
- One package per directory
- Separate concerns: `cmd/`, `pkg/lexer`, `pkg/parser`, `pkg/compiler`
- Tests go in same package with `_test.go` suffix
- Test files: `*_test.go`

### Formatting
- Use `gofmt` or `go fmt` for consistent formatting
- Use tabs for indentation (standard Go)
- No trailing whitespace
- One blank line between import groups

### Best Practices
- Always handle errors explicitly
- Use `defer` for resource cleanup
- Prefer composition over inheritance
- Use interfaces to define contracts
- Keep functions small and focused
- Write table-driven tests

---

## Project Structure

```
cmd/
  linespec/          # CLI entry point
    main.go
pkg/
  lexer/             # Tokenizer for .linespec files
  parser/            # AST generation
  compiler/          # YAML output generation (KTests + KMocks)
examples/
  test-set-0/        # .linespec input fixtures
    *.linespec
    payloads/        # YAML payload files for test data
keploy-examples/
  test-set-0/        # Pre-compiled KTest + mocks fixtures
    tests/
    mocks.yaml
```

---

## Key Design Patterns

### Pipeline Architecture
The compiler follows a pipeline: `source → tokenize → parse → validate → compile`

### DSL Syntax

LineSpec uses a declarative syntax to define service behavior:

```
TEST <name>
RECEIVE HTTP:<METHOD> <URL>
[WITH {{payloads/request.yaml}}]

EXPECT <CHANNEL> <resource>
[USING_SQL """
<raw-sql>
"""]
[WITH {{payloads/input.yaml}}]
[RETURNS {{payloads/output.yaml}}]
[RETURNS EMPTY]
[VERIFY query CONTAINS 'string']
[VERIFY query NOT_CONTAINS 'string']
[VERIFY query MATCHES /regex/]

RESPOND HTTP:<STATUS_CODE>
[WITH {{payloads/response.yaml}}]
[NOISE
  body.<field>]
```

### MySQL Auto-Transaction System

**Automatic Transaction Wrapping:**
By default, `WRITE_MYSQL` expectations are automatically wrapped in `BEGIN...COMMIT` transaction mocks. This supports transactional ORMs like Rails ActiveRecord, Django, Hibernate, etc.

```yaml
# Input
EXPECT WRITE:MYSQL users
WITH {{payloads/user_create_req.yaml}}

# Generates:
# 1. BEGIN mock (auto-generated)
# 2. INSERT INTO users (...) mock (from WITH payload)
# 3. COMMIT mock (auto-generated)
```

**Disable with `NO TRANSACTION`:**
For non-transactional ORMs, add the `NO TRANSACTION` keyword:

```yaml
EXPECT WRITE:MYSQL users
NO TRANSACTION
WITH {{payloads/user_create_req.yaml}}
```

### SQL Generation from Payloads

The compiler automatically generates SQL from `WITH` payload files for MySQL expectations:

**For WRITE_MYSQL (default generates INSERT):**
```yaml
# Payload (payloads/user_create_req.yaml):
# name: John
# email: john@example.com
# password: secret123

# Generated SQL:
# INSERT INTO users (name, email, password) VALUES ('John', 'john@example.com', 'secret123')
```

**For READ_MYSQL (generates SELECT):**
```yaml
# Input without USING_SQL:
EXPECT READ:MYSQL users
RETURNS {{payloads/users_list.yaml}}

# Generated SQL:
# SELECT * FROM users
```

### Generic OK Responses for Writes

Write operations (`WRITE_MYSQL`, `WRITE_POSTGRESQL`) don't require `RETURNS`. The compiler auto-generates a generic OK response:

```yaml
# Response mock (auto-generated):
message:
  header: 0
  affected_rows: 1
  last_insert_id: 0
  status_flags: 2
  warnings: 0
  info: ''
```

This eliminates the need for write result payload files (`mysql_*_write_result.yaml`, etc.).

### HTTP Mock Interception

The proxy includes an HTTP server that intercepts external service calls:

```go
// HTTP server listens on port 80
httpServer := &http.Server{
    Addr: ":80",
    Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Match against HTTP mocks filtered by current test name
        mock := findMatchingMock(r, currentTestName)
        if mock != nil {
            httpMockUsage[mock.Name] = true // Track usage
            w.WriteHeader(mock.Spec.Response.StatusCode)
            w.Write([]byte(mock.Spec.Response.Body))
        }
    }),
}
```

**Key features:**
1. **Dynamic DNS aliases** - Hostnames from HTTP mocks are added as `--network-alias` when starting the proxy container
2. **Test-scoped matching** - Each test only sees its own HTTP mocks (prevents cross-test contamination)
3. **Usage tracking** - Mocks are tracked and verified after each test

### Test Runner Pipeline
`linespec test [dir]` follows the sequence:
1. `loadTestSet(dir)` — reads all `tests/*.yaml` and `mocks.yaml`
2. **Extract HTTP hostnames** - Parse HTTP mocks to get unique hostnames for DNS aliases
3. **Build proxy with all mocks** - Start proxy container with all mocks loaded (mock aggregation)
4. **Add DNS aliases** - Add `--network-alias` for each HTTP mock hostname
5. **Per-test activation** - For each test:
   - Call `/activate` endpoint with test name
   - Proxy filters mocks by test name prefix
   - Reset HTTP mock usage tracking
6. **Run HTTP test** - Replay KTest via HTTP module
7. **Verify mocks** - Check SQL verification errors and HTTP mock usage
8. **Report results** - Side-by-side diff for failures
9. **Write report** - JSON files to `linespec-report/`
10. **Cleanup** - `docker compose down` and proxy teardown

### Docker Compose Testing Infrastructure

When running tests with `--compose`, LineSpec manages Docker containers automatically:

**Startup Sequence:**

1. **Clean existing containers** (`docker compose down -v`)
   - Ensures fresh database state by removing old containers and volumes
   - Prevents conflicts with previously running services

2. **Start database service** (`docker compose up -d db`)
   - Only the database container is started initially
   - Waits for database to be ready (TCP connection check)

3. **Build MySQL proxy container**
   - Creates a custom Docker image with the proxy server
   - Copies mocks.yaml and compiled code into the image
   - Proxy intercepts all database traffic between app and real DB

4. **Start proxy container** (`docker run -d linespec-proxy`)
   - Runs on the same Docker network as the compose services
   - Listens on standard MySQL port 3306
   - Routes queries to real DB or returns mock responses

5. **Generate compose override** (`.linespec-compose-override.yml`)
   - Rewrites `DATABASE_URL` to point to proxy container
   - Updates all DB host variables: `DB_HOST`, `DATABASE_HOST`, `MYSQL_HOST`, `POSTGRES_HOST`
   - Updates all DB port variables to 3306
   - Preserves other DB-related env vars (DB_NAME, credentials, etc.)

6. **Start web service with override**
   ```bash
   docker compose -f docker-compose.yml -f .linespec-compose-override.yml up -d web
   ```
   - Web service now connects to proxy instead of real database
   - Proxy determines which queries to mock vs. pass through

**Shutdown Sequence:**

1. **Remove web service** (`docker compose rm -fs web`)
   - Stops and removes only the web container
   - Keeps database running for next test run

2. **Clean up proxy**
   - Stops and removes the proxy container
   - Proxy server process is terminated

3. **Remove override file**
   - Deletes `.linespec-compose-override.yml`
   - Cleans up temporary files

### Proxy Pattern Matching

The proxy matches queries using a hierarchy:

1. **Exact match first** (normalized by removing backticks, lowercasing)
2. **Query type checking** — SELECT mocks only match SELECT queries, not INSERT/UPDATE/DELETE
3. **Pattern matching** — For INSERT/UPDATE/DELETE, match by table name prefix
4. **Table matching** — For SELECT, match by table name in FROM clause

Example matching:
```
Mock:    insert into users (name, email) values (...)
Rails:   INSERT INTO `users` (`name`, `email`, `created_at`) VALUES (...)
Result:  ✓ MATCH (normalized + table prefix)
```

### Infrastructure Pass-Through
These queries always pass through to the real database (never matched against mocks):
- `SET NAMES ...`
- `COM_PING`
- `information_schema` queries
- `schema_migrations` checks
- `SHOW FULL FIELDS ...`
- `SHOW ...` queries

### Statement Types
- `RECEIVE` - Trigger request (exactly one required, must be first)
- `EXPECT` - External dependencies (zero or more)
  - `HTTP` - External HTTP calls (mocked by proxy, verified after test)
  - `READ_MYSQL` - Database reads (requires `RETURNS`)
  - `WRITE_MYSQL` - Database writes (auto-transactional, optional `RETURNS`)
  - `WRITE_POSTGRESQL` - PostgreSQL writes
  - `EVENT` - Message queue events
  - `VERIFY` - SQL query validation (attached to EXPECT statements)
- `RESPOND` - Response (exactly one required, must be last)
- `NOISE` - Response noise filter (optional, follows RESPOND)
- `NO TRANSACTION` - Disable auto-transaction for WRITE_MYSQL

### Verification Types
LineSpec supports two types of verification:

**1. SQL Verification (`VERIFY query ...`)**
Validates actual SQL queries executed by the application:
- `VERIFY query CONTAINS 'string'` - Query must include the string
- `VERIFY query NOT_CONTAINS 'string'` - Query must NOT include the string
- `VERIFY query MATCHES /regex/` - Query must match the regex pattern

**2. HTTP Mock Verification (automatic)**
Tests fail if HTTP mocks are defined but not invoked:
- Catches fallback behavior (rescue blocks providing default values)
- Validates external service calls actually happen
- Prevents silent test bypasses

When verification fails, the test runner displays:
```
✗ test-1 FAIL

  🔒 SQL Verification Error:
    VERIFY FAILED: Query does not contain 'password_digest'.
    Actual query: INSERT INTO `users` (`name`, `email`, `password`) ...

  🔌 HTTP Mock Verification Error:
    HTTP Mock(s) not invoked: test-1-mock-0

  Expected status : 201
  Actual status   : 500
```

---

## Testing Guidelines

- Use Go's standard `testing` package
- Use table-driven tests for multiple test cases
- Use `t.Run()` for subtests
- Use `t.Cleanup()` for cleanup
- Verify YAML output structure against expected schemas
- Test both success and failure cases

Example test structure:
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

---

## Payload File Conventions

### HTTP Request Payloads
YAML with request body fields:
```yaml
name: User One
email: user_one@example.com
```

### HTTP Response Payloads
YAML with response body fields:
```yaml
id: 1
name: User One
email: user_one@example.com
```

### MySQL Read Result Payloads
YAML with `rows` array:
```yaml
rows:
  - id: 1
    name: User One
    email: user_one@example.com
```

The compiler infers MySQL column types from the data (e.g., `id` → BIGINT, `created_at` → DATETIME).

### Empty Result Payloads
For queries returning no rows, use `RETURNS EMPTY` instead of creating complex payload files:

```linespec
EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM `users` WHERE `users`.`id` = 999 LIMIT 1
"""
RETURNS EMPTY
```

The compiler automatically generates proper MySQL TextResultSet column definitions. Only use manual payload files if you need specific column configurations.

---

## Common Patterns

### Creating a Test for a POST Endpoint

```linespec
TEST create-resource
RECEIVE HTTP:POST http://localhost:3000/resources
WITH {{payloads/resource_create_req.yaml}}

# Auto-generates SQL from payload
EXPECT WRITE:MYSQL resources
WITH {{payloads/resource_create_req.yaml}}

RESPOND HTTP:201
WITH {{payloads/resource_create_resp.yaml}}
NOISE
  body.id
  body.created_at
```

### Creating a Test for a GET Endpoint

```linespec
TEST get-resource
RECEIVE HTTP:GET http://localhost:3000/resources/1

# Requires RETURNS to verify data format
EXPECT READ:MYSQL resources
RETURNS {{payloads/resource_single.yaml}}

RESPOND HTTP:200
WITH {{payloads/resource_single.yaml}}
```

### Handling Validation Queries

Rails often checks for existing records before creating:

```linespec
TEST create-user-with-validation
RECEIVE HTTP:POST http://localhost:3000/users
WITH {{payloads/user_create_req.yaml}}

# Validation query - returns empty (no duplicate)
EXPECT READ:MYSQL users
USING_SQL """
SELECT 1 AS one FROM `users` WHERE `users`.`email` = 'user@example.com' LIMIT 1
"""
RETURNS EMPTY

# The actual INSERT
EXPECT WRITE:MYSQL users
WITH {{payloads/user_create_req.yaml}}

RESPOND HTTP:201
```

### Verifying SQL Query Structure

Use VERIFY clauses to validate the actual SQL executed by the application at runtime. This enables security checks, compliance validation, and correctness enforcement.

**Common use cases:**
- **Security:** Ensure passwords are hashed before storage
- **Compliance:** Verify audit fields (created_at, updated_by) are included
- **Correctness:** Confirm proper table names and column sets
- **Injection prevention:** Validate query patterns match expected templates

```linespec
TEST create-user-secure
RECEIVE HTTP:POST http://localhost:3000/users
WITH {{payloads/user_create_req.yaml}}

# Example: Ensure password is hashed (no plain text 'password' in SQL)
EXPECT WRITE:MYSQL users
WITH {{payloads/user_with_hashed_password.yaml}}
VERIFY query CONTAINS 'password_digest'
VERIFY query NOT_CONTAINS 'password'

RESPOND HTTP:201
WITH {{payloads/user_create_resp.yaml}}
NOISE
  body.id
  body.created_at
  body.updated_at
```

Verification operators:
- `CONTAINS 'string'` — Query must include the string
- `NOT_CONTAINS 'string'` — Query must NOT include the string  
- `MATCHES /regex/` — Query must match the regex pattern

When verification fails, the test runner displays:
```
✗ test-1 FAIL

  🔒 SQL Verification Error:
    VERIFY FAILED: Query does not contain 'password_digest'.
    Actual query: INSERT INTO `users` (`name`, `email`, `password`, ...) ...

  Expected status : 201
  Actual status   : 500
```

### Non-Transactional ORM

For ORMs that don't wrap operations in transactions:

```linespec
TEST create-item
RECEIVE HTTP:POST http://localhost:3000/items
WITH {{payloads/item_create_req.yaml}}

EXPECT WRITE:MYSQL items
NO TRANSACTION
WITH {{payloads/item_create_req.yaml}}

RESPOND HTTP:201
```
