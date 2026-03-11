# Agent Guidelines for LineSpec

This file provides guidance for agents working on the LineSpec codebase.

## For AI Agents Helping End Users

When a user has installed LineSpec and asks you to help write LineSpec files:

1. **Read files in this order:**
   - First: `AGENTS.md` (this file) - for project context
   - Second: `LINESPEC.md` - for complete syntax reference
   - Third: `README.md` - for usage examples

2. **Documentation location:** After installation, docs are in:
   - `<go-install-path>/pkg/mod/github.com/anomalyco/linespec@<version>/docs/`

---

## Project Overview

LineSpec is a DSL-based integration testing tool that executes service behavior specifications directly. It uses a custom domain-specific language (.linespec files) to define test cases, then runs them against containerized services with database and HTTP mocking capabilities.

Unlike traditional integration testing tools, LineSpec:
- Parses .linespec files directly (no compile step)
- Manages Docker containers automatically
- Provides MySQL/PostgreSQL proxies for query interception
- Mocks external HTTP services with automatic DNS resolution
- Validates SQL queries at runtime with VERIFY clauses

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
go test -run TestName ./pkg/dsl
```

Or use verbose mode:
```bash
go test -v ./...
```

---

## Code Style Guidelines

### Go Configuration
- Go version: 1.21+
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
      
      "github.com/anomalyco/linespec/pkg/dsl"
  )
  ```

### Naming Conventions
- **Files**: snake_case (e.g., `lexer.go`, `parser.go`)
- **Types/Interfaces**: PascalCase (e.g., `TestSpec`, `ExpectStatement`)
- **Structs**: PascalCase (e.g., `LineSpecError`)
- **Functions/Variables**: camelCase (e.g., `tokenize`, `specFile`)
- **Constants**: PascalCase or camelCase (e.g., `ExamplesDir`)
- **Package names**: lowercase, single word (e.g., `dsl`, `runner`, `proxy`)

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
- Separate concerns: `cmd/`, `pkg/dsl/`, `pkg/runner/`, `pkg/proxy/`
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
  dsl/               # DSL lexer and parser
    lexer.go
    parser.go
    payload.go
  types/             # Core data structures
    types.go
  runner/            # Test execution engine
    runner.go
  registry/          # Mock registration & matching
    registry.go
  proxy/             # Proxy implementations
    mysql/           # MySQL proxy with query matching
    postgresql/      # PostgreSQL proxy
    http/            # HTTP interceptor
    kafka/           # Kafka interceptor
  config/            # Configuration system
    types.go
    parser.go
  docker/            # Docker orchestration
    orchestrator.go
examples/
  test-set-0/        # .linespec input fixtures
    *.linespec
    payloads/        # YAML payload files for test data
```

---

## Key Design Patterns

### Pipeline Architecture
The test runner follows a pipeline: `parse → setup → execute → verify → teardown`

1. **Parse** — Load and parse .linespec files
2. **Setup** — Start shared infrastructure (MySQL, Kafka)
3. **Execute** — For each test:
   - Start database proxy
   - Start HTTP proxy with DNS aliases
   - Start application container
   - Send HTTP trigger request
   - Collect hits from `/verify` endpoint
4. **Verify** — Validate response, SQL queries, HTTP mock usage
5. **Teardown** — Stop test-specific containers

### DSL Syntax

LineSpec uses a declarative syntax to define service behavior:

```
TEST <name>
RECEIVE HTTP:<METHOD> <URL>
[WITH {{payloads/request.yaml}}]
[HEADERS
  <header>: <value>]

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

EXPECT_NOT <CHANNEL> <resource>
[USING_SQL """
<raw-sql>
"""]

RESPOND HTTP:<STATUS_CODE>
[WITH {{payloads/response.yaml}}]
[NOISE
  body.<field>]
```

### Statement Types

- `TEST` — Test name declaration (optional, defaults to filename)
- `RECEIVE` — Trigger request (exactly one required, must be first)
- `EXPECT` — External dependencies that MUST occur (zero or more)
  - `HTTP:<METHOD> <URL>` — External HTTP calls
  - `READ_MYSQL <table>` — Database reads
  - `WRITE_MYSQL <table>` — Database writes
  - `READ_POSTGRESQL <table>` — PostgreSQL reads
  - `WRITE_POSTGRESQL <table>` — PostgreSQL writes
  - `EVENT:<topic>` / `MESSAGE:<topic>` — Message queue events (both aliases work)
- `EXPECT_NOT` — Negative assertions (zero or more)
  - `WRITE_MYSQL <table>` — Assert that a write does NOT occur
  - `READ_MYSQL <table>` — Assert that a read does NOT occur
- `RESPOND` — Response (exactly one required, must be last)
- `NOISE` — Response noise filter (optional, follows RESPOND)
- `NO TRANSACTION` — Disable auto-transaction for WRITE_MYSQL (parsed but proxy behavior is same)

### Configuration System

LineSpec uses `.linespec.yml` files for service configuration:

```yaml
service:
  name: todo-api
  service_dir: todo-api
  type: web
  framework: rails
  port: 3000
  health_endpoint: /up
  docker_compose: docker-compose.yml
  build_context: .
  start_command: bundle exec rails server -b 0.0.0.0 -p 3000
  environment:
    KAFKA_BROKERS: kafka:29092

database:
  type: mysql|postgresql
  image: mysql:8.4
  port: 3306|5432
  database: todo_api_development
  username: todo_user
  password: todo_password

infrastructure:
  database: true
  kafka: true
  external_db: false

dependencies:
  - name: user-service
    type: http
    host: user-service.local
    port: 3001
    proxy: true
```

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
- `BEGIN`, `COMMIT`, `ROLLBACK` (transaction statements)

### Verification Types

LineSpec supports two types of verification:

**1. SQL Verification (`VERIFY query ...`)**
Validates actual SQL queries executed by the application:
- `VERIFY query CONTAINS 'string'` — Query must include the string
- `VERIFY query NOT_CONTAINS 'string'` — Query must NOT include the string
- `VERIFY query MATCHES /regex/` — Query must match the regex pattern

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

  Expected status : 201
  Actual status   : 500
```

### Negative Assertions (EXPECT_NOT)

Use `EXPECT_NOT` to assert that certain operations do NOT occur:

```linespec
TEST create-user-no-email-check
RECEIVE HTTP:POST http://localhost:3000/users
WITH {{payloads/user_create.yaml}}

# Assert that we DON'T check for email uniqueness
EXPECT_NOT READ:MYSQL users
USING_SQL """
SELECT * FROM `users` WHERE `users`.`email` = 'john@example.com' LIMIT 1
"""

# The actual write
EXPECT WRITE:MYSQL users
WITH {{payloads/user_create.yaml}}

RESPOND HTTP:201
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

### Event Payloads
YAML with message body fields:
```yaml
topic: todo-events
key: todo-123
value:
  id: 1
  title: Buy milk
  completed: false
```

---

## Common Patterns

### Creating a Test for a POST Endpoint

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

### Creating a Test for a GET Endpoint

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

Use VERIFY clauses to validate the actual SQL executed by the application at runtime:

```linespec
TEST create-user-secure
RECEIVE HTTP:POST http://localhost:3000/users
WITH {{payloads/user_create_req.yaml}}

# Example: Ensure password is hashed
EXPECT WRITE:MYSQL users
WITH {{payloads/user_with_password_digest.yaml}}
VERIFY query CONTAINS 'password_digest'
VERIFY query NOT_CONTAINS 'password'

RESPOND HTTP:201
WITH {{payloads/user_create_resp.yaml}}
NOISE
  body.id
  body.created_at
  body.updated_at
```

### Testing with External HTTP Dependencies

```linespec
TEST microservice-call
RECEIVE HTTP:GET http://localhost:3000/api/data
HEADERS
  Authorization: Bearer token123

# Expect a call to external auth service
EXPECT HTTP:GET http://user-service.local/api/v1/users/auth
HEADERS
  Authorization: Bearer token123
RETURNS {{payloads/authenticated_user.yaml}}

RESPOND HTTP:200
WITH {{payloads/combined_response.yaml}}
```

### Using EXPECT_NOT (Negative Assertions)

Assert that certain operations don't happen:

```linespec
TEST efficient-user-lookup
RECEIVE HTTP:GET http://localhost:3000/users/123

# Should NOT query all users
EXPECT_NOT READ:MYSQL users
USING_SQL """
SELECT * FROM `users`
"""

# Should use indexed lookup
EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM `users` WHERE `users`.`id` = 123 LIMIT 1
"""
RETURNS {{payloads/user.yaml}}

RESPOND HTTP:200
WITH {{payloads/user.yaml}}
```

### Testing PostgreSQL Operations

```linespec
TEST create-postgresql-record
RECEIVE HTTP:POST http://localhost:3000/items
WITH {{payloads/item_create.yaml}}

EXPECT WRITE:POSTGRESQL items
WITH {{payloads/item_create.yaml}}

RESPOND HTTP:201
```

---

## CLI Commands Reference

### `linespec test <path>`
Execute .linespec test files against a containerized service.

**Usage:**
```bash
linespec test <path-to-linespec-or-directory>
```

**Arguments:**
- `<path>` — Path to a .linespec file or directory containing .linespec files

**Prerequisites:**
- Docker must be installed and running
- A `.linespec.yml` configuration file must exist in the service directory
- Required payload YAML files must exist

**What it does:**
1. Parses the .linespec file(s)
2. Reads `.linespec.yml` for service configuration
3. Creates a shared Docker network
4. Starts shared infrastructure (MySQL, Kafka if configured)
5. For each test:
   - Starts database proxy with per-test mock registry
   - Starts HTTP proxy with DNS aliases for dependencies
   - Starts the application container
   - Waits for health check
   - Sends the HTTP trigger request
   - Collects hits from `/verify` endpoint
   - Verifies all mocks were called
   - Stops test-specific containers

### `linespec proxy <type> <listen> <upstream> [registry]`
Start a protocol proxy for development/debugging.

**Usage:**
```bash
linespec proxy mysql <listen-addr> <upstream-addr> [registry-file]
linespec proxy postgresql <listen-addr> <upstream-addr> [registry-file]
linespec proxy http <listen-addr> <upstream-addr> [registry-file]
linespec proxy kafka <listen-addr> <upstream-addr> [registry-file]
```

---

## Important Notes for AI Agents

### What LineSpec Actually Does (vs. Documentation)

**Misconception:** LineSpec "compiles" .linespec files to Keploy artifacts.
**Reality:** LineSpec parses .linespec files and executes them directly against containerized services.

**Misconception:** There's a `linespec compile` command.
**Reality:** No compile command exists. Use `linespec test` directly.

**Misconception:** LineSpec generates KTest and KMock YAML files.
**Reality:** LineSpec uses an internal registry system and executes tests directly.

### Common User Questions

**Q: Where are my compiled YAML files?**
A: LineSpec doesn't generate YAML files. It executes tests directly from .linespec files.

**Q: How do I run my tests?**
A: Use `linespec test <path>` not `linespec compile`.

**Q: Why is my test failing with "mock not called"?**
A: HTTP mocks defined with EXPECT must be invoked. If your application catches errors and uses fallback behavior, the test will fail. This is intentional to catch silent failures.

**Q: How do I configure my service?**
A: Create a `.linespec.yml` file in your service directory with service, database, infrastructure, and dependencies sections.
