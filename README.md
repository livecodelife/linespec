# LineSpec

LineSpec is a DSL compiler that generates Keploy-compatible KTests and KMocks from human-readable service behavior specifications. It enables TDD by allowing you to define service behavior and database interactions in a declarative, human-readable format.

## Installation

### From source (Go)

```bash
git clone https://github.com/anomalyco/linespec.git
cd linespec
go install ./cmd/linespec
```

The `linespec` binary will be installed to `$GOPATH/bin` (or `$HOME/go/bin` by default).

## Usage

```bash
linespec compile <file> [-o <output-dir>]
```

### Options

- `<file>` - Path to a `.linespec` file or directory containing `.linespec` files (required)
- `-o, --out <dir>` - Output directory (default: `out`)

### Example

```bash
linespec compile examples/test-set-0/test-1.linespec -o out
```

## Running Tests

### `linespec test`

```bash
linespec test [dir] [--compose <file>] [--service-url <url>] [--report <dir>]
```

**Options**

| Flag | Description | Default |
|---|---|---|
| `[dir]` | Path to a compiled test-set directory containing `tests/` and `mocks.yaml` | `keploy-examples/test-set-0` |
| `--compose <file>` | Path to the `docker-compose.yml` for the service under test | (required) |
| `--service-url <url>` | Base URL of the service once it is running | `http://localhost:3000` |
| `--report <dir>` | Report output directory, relative to the test-set dir | `linespec-report` |

**Prerequisites** — Docker and Docker Compose must be installed and the Docker daemon must be running.

**How it works** — A MySQL TCP proxy is started on a free local port. Infrastructure-level queries (such as `SET NAMES`, `COM_PING`, `information_schema` introspection, and `schema_migrations` checks) are always forwarded transparently to the real database and are never matched against `mocks.yaml`. For all other queries, the proxy checks `mocks.yaml` (which contains only app-level mocks compiled from `EXPECT READ_MYSQL` / `EXPECT WRITE_MYSQL` statements) and returns a mock response on a match; otherwise it forwards the query to the real database. Docker Compose is launched with an override that rewrites the service's `DATABASE_URL` to route through the proxy via `host.docker.internal`. Once the service is healthy, each test in `tests/` is replayed as an HTTP request. Status code and body are compared against the recorded response (noise fields listed in `assertions.noise` are ignored). A pass/fail summary is printed and Docker Compose is torn down.

**Example**

```bash
linespec test keploy-examples/test-set-0 \
  --compose /path/to/docker-compose.yml \
  --service-url http://localhost:3000
```

Expected output:

```
✓ Loaded 11 tests and 38 mocks from keploy-examples/test-set-0
→ Starting services...
✓ Database ready
→ Building proxy image...
✓ Proxy image built
✓ MySQL proxy ready (linespec-proxy:3306)
✓ Service ready at http://localhost:3000
→ test-1: ✓ test-1 PASS
→ test-2: ✗ test-2 FAIL

  Expected status : 200
  Actual status   : 200
  Body diff:
    {                                           {
      "id": 1,                                    "id": 1,
      "title": "Buy milk"              ~          "title": "Buy bread"
    }                                           }
→ test-3: ✓ test-3 PASS
…
→ Report written to keploy-examples/test-set-0/linespec-report/

summary: 10 passed, 1 failed
```

> **Note:** Differing lines are marked with ` ~ ` and are coloured red (expected) / green (actual) in a terminal.

### Report Folder

The report is written to `<test-set-dir>/linespec-report/` by default. Override with `--report <dir>`.

- `summary.json` — top-level object with `passed`, `failed`, `total`, and a `tests` array (each entry: `name`, `pass`, `reason?`).
- `{test-name}.json` — full `TestResult` for that test: `name`, `pass`, `reason?`, `expectedStatus`, `actualStatus?`, `expectedBody?`, `actualBody?`, `diff?`, `req`.

Example `summary.json`:

```json
{
  "passed": 10,
  "failed": 1,
  "total": 11,
  "tests": [
    { "name": "test-1", "pass": true },
    { "name": "test-2", "pass": false, "reason": "body mismatch" }
  ]
}
```

## Specification Format

A `.linespec` file defines a test case with three statement types:

### TEST

Defines the test name:

```
TEST test-name
```

### RECEIVE

Specifies the incoming HTTP request (required, must be first):

```
RECEIVE HTTP:<METHOD> <URL>
WITH {{payload/request.yaml}}
```

- `<METHOD>` - HTTP method (GET, POST, PUT, DELETE, etc.)
- `<URL>` - Full URL including protocol and host
- `WITH` (optional) - Path to a YAML file containing the request body

### EXPECT

Defines external dependencies (zero or more allowed):

```
EXPECT <CHANNEL> <resource>
[USING_SQL """
<raw-sql-query>
"""]
[WITH {{payload/input.yaml}}]
[RETURNS {{payload/output.yaml}}]
```

Supported channels:
- `HTTP:<METHOD> <URL>` - External HTTP calls (see HTTP Mock section below)
- `READ_MYSQL <table>` - Database reads (requires `RETURNS`)
- `WRITE_MYSQL <table>` - Database writes (auto-transactional by default)
- `WRITE_POSTGRESQL <table>` - PostgreSQL writes
- `EVENT <topic>` - Message queue events

**HTTP Expectations:**

The `EXPECT HTTP` statement mocks external HTTP service calls. This is useful for testing microservices that call other services:

```linespec
EXPECT HTTP:GET http://auth-service.local/api/v1/validate
WITH {{payloads/auth_request.yaml}}
RETURNS {{payloads/auth_response.yaml}}
```

**How it works:**
1. LineSpec extracts hostnames from HTTP expectations (e.g., `auth-service.local`)
2. The proxy container gets DNS aliases for these hostnames
3. When your application calls the external service, it resolves to the proxy
4. The proxy matches the request and returns the mocked response
5. Tests fail if HTTP mocks are defined but not invoked (catches fallback behavior)

**Important:** HTTP mocks are scoped by test name. Each test only sees its own HTTP mocks, preventing cross-test contamination when multiple tests use the same URL.

**MySQL Write Operations:**

For `WRITE_MYSQL`, the system provides powerful conveniences:

1. **Auto-transactions** - By default, writes are wrapped in `BEGIN...COMMIT`. The compiler automatically generates these mocks.
2. **SQL generation** - If `USING_SQL` is not provided, SQL is automatically generated from the `WITH` payload:
   - `INSERT INTO <table> (columns...) VALUES (values...)` from the payload data
3. **Generic responses** - Write operations don't need `RETURNS`; a generic OK response is auto-generated.
4. **SQL Verification** - Use `VERIFY` clauses to validate actual SQL queries at runtime for security, correctness, or compliance:

   ```linespec
   EXPECT WRITE:MYSQL users
   WITH {{payloads/user_data.yaml}}
   VERIFY query CONTAINS 'password_digest'
   VERIFY query NOT_CONTAINS 'password'
   ```

   Common use cases include password hashing verification, audit trail enforcement, and SQL injection prevention.

Use `NO TRANSACTION` to disable auto-transaction wrapping for non-transactional ORMs:

```linespec
EXPECT WRITE:MYSQL users
NO TRANSACTION
WITH {{payloads/user_data.yaml}}
```

**MySQL Read Operations:**

For `READ_MYSQL`, `RETURNS` is required to verify the data format:

```linespec
EXPECT READ:MYSQL users
RETURNS {{payloads/users_list.yaml}}
```

If `USING_SQL` is not provided, generates: `SELECT * FROM <table>`

**Empty Results:**

For queries that return no rows (e.g., "user not found"), use `RETURNS EMPTY` instead of a payload file:

```linespec
EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM `users` WHERE `users`.`id` = 999 LIMIT 1
"""
RETURNS EMPTY
```

The compiler automatically generates proper MySQL TextResultSet column definitions.

---

### RESPOND

Specifies the HTTP response (required, must be last):

```
RESPOND HTTP:<STATUS_CODE>
WITH {{payload/response.yaml}}
NOISE
  body.<field>
  body.<field>
```

- `<STATUS_CODE>` - HTTP status code (200, 201, 400, 500, etc.)
- `WITH` (optional) - Path to a YAML file containing the response body
- `NOISE` (optional) - Lists response body fields to ignore during comparison; each indented line is one dot-notation field path

## Examples

### Simple Write Operation

```linespec
TEST create-user
RECEIVE HTTP:POST http://localhost:3000/users
WITH {{payloads/user_create_req.yaml}}

# Auto-generates: INSERT INTO users (name, email, password) VALUES (...)
# Auto-wraps with BEGIN...COMMIT
EXPECT WRITE:MYSQL users
WITH {{payloads/user_create_req.yaml}}

RESPOND HTTP:201
WITH {{payloads/user_create_resp.yaml}}
NOISE
  body.id
  body.created_at
  body.updated_at
```

### Write with SQL Verification

Use `VERIFY` clauses to validate the actual SQL executed by your application:

```linespec
TEST create-user-secure
RECEIVE HTTP:POST http://localhost:3000/users
WITH {{payloads/user_create_req.yaml}}

# VERIFY validates the actual SQL query at runtime
# Example: ensure password is hashed (no plain text 'password' field)
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

When verification fails, the test output shows:
```
✗ create-user-secure FAIL

  🔒 SQL Verification Error:
    VERIFY FAILED: Query does not contain 'password_digest'.
    Actual query: INSERT INTO `users` (`name`, `email`, `password`) ...

  Expected status : 201
  Actual status   : 500
```

### HTTP Mock Verification

LineSpec verifies that HTTP mocks are actually invoked during tests. This prevents silent failures when applications use fallback behavior instead of making HTTP calls:

```linespec
TEST microservice-call
RECEIVE HTTP:GET http://localhost:3000/api/data

# Expect a call to external auth service
EXPECT HTTP:POST http://auth-service.local/validate
WITH {{payloads/auth_request.yaml}}
RETURNS {{payloads/auth_response.yaml}}

# Expect a call to external payment service  
EXPECT HTTP:POST http://payment-service.local/charge
WITH {{payloads/payment_request.yaml}}
RETURNS {{payloads/payment_response.yaml}}

RESPOND HTTP:200
WITH {{payloads/combined_response.yaml}}
```

**If HTTP mocks are not invoked, the test fails:**
```
✗ microservice-call FAIL

  🔌 HTTP Mock Verification Error:
    HTTP Mock(s) not invoked: microservice-call-mock-0, microservice-call-mock-1

  Expected status : 200
  Actual status   : 200
```

This catches common issues:
- **Fallback behavior** - Application catches HTTP errors and uses default values
- **Wrong hostnames** - Application calls `auth-service.prod` instead of `auth-service.local`
- **Test mode bypass** - Application skips external calls in test environment

### Read Operation with Custom SQL

```linespec
TEST get-user
RECEIVE HTTP:GET http://localhost:3000/users/123

# Custom SQL for complex query
EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM users WHERE id = 123 AND active = 1
"""
RETURNS {{payloads/user_single_resp.yaml}}

RESPOND HTTP:200
WITH {{payloads/user_single_resp.yaml}}
```

### Read Operation with Empty Results (Not Found)

Use `RETURNS EMPTY` for queries that return no rows:

```linespec
TEST get-user-not-found
RECEIVE HTTP:GET http://localhost:3000/users/999
HEADERS
  Authorization: Bearer token_abc123xyz

# First query finds the authenticated user
EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM `users` WHERE `users`.`token` = 'token_abc123xyz' LIMIT 1
"""
RETURNS {{payloads/user_response.yaml}}

# Second query finds no user with id=999
EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM `users` WHERE `users`.`id` = 999 LIMIT 1
"""
RETURNS EMPTY

RESPOND HTTP:404
WITH {{payloads/user_not_found_error.yaml}}
```

### Non-Transactional ORM

```linespec
TEST create-item-no-tx
RECEIVE HTTP:POST http://localhost:3000/items
WITH {{payloads/item_create_req.yaml}}

# Disable auto-transaction for non-transactional ORMs
EXPECT WRITE:MYSQL items
NO TRANSACTION
WITH {{payloads/item_create_req.yaml}}

RESPOND HTTP:201
```

## VERIFY Operators

The `VERIFY` clause supports three operators for SQL validation:

| Operator | Description |
|----------|-------------|
| `CONTAINS '<string>'` | Query must include the specified string |
| `NOT_CONTAINS '<string>'` | Query must NOT include the specified string |
| `MATCHES /regex/` | Query must match the specified regex pattern |

Multiple VERIFY clauses can be attached to a single EXPECT statement:

```linespec
EXPECT WRITE:MYSQL users
WITH {{payloads/user_data.yaml}}
VERIFY query CONTAINS 'password_digest'
VERIFY query NOT_CONTAINS 'password'
VERIFY query MATCHES /INSERT INTO users/
```

## Generated Output

Compiling a `.linespec` file generates:
- `out/tests/<name>.yaml` - KTest specification (the HTTP test to replay)
- `out/mocks.yaml` - KMock definitions for all external dependencies

### Example Generated Mocks

For the simple write operation above, the compiler auto-generates:

```yaml
---
version: api.keploy.io/v1beta1
kind: MySQL
name: create-user-mock-transaction-0
spec:
  metadata:
    requestOperation: COM_QUERY
    responseOperation: OK
  requests:
    - message:
        query: BEGIN
  responses:
    - message:
        header: 0
        affected_rows: 0
---
version: api.keploy.io/v1beta1
kind: MySQL
name: create-user-mock-0
spec:
  requests:
    - message:
        query: INSERT INTO users (name, email, password) VALUES ('John', 'john@example.com', 'secret')
  responses:
    - message:
        header: 0
        affected_rows: 1
---
version: api.keploy.io/v1beta1
kind: MySQL
name: create-user-mock-transaction-1
spec:
  requests:
    - message:
        query: COMMIT
  responses:
    - message:
        header: 0
        affected_rows: 0
```

## Development

```bash
go run ./cmd/linespec    # Run CLI in development mode
go build -o linespec ./cmd/linespec  # Build binary
go test ./...            # Run tests
linespec compile examples/test-set-0/test-1.linespec -o out
linespec test keploy-examples/test-set-0 --compose /path/to/docker-compose.yml
```

## Key Features

- **Human-readable DSL** - Define service behavior in plain text
- **Auto-transaction support** - Database writes automatically wrapped in transactions
- **SQL generation** - Payload files automatically converted to SQL queries
- **Generic responses** - No need to define write result payloads
- **Pattern matching** - SQL queries are matched by operation and table, so ORM-specific SQL variations work automatically
- **Infrastructure pass-through** - Schema queries (`information_schema`, `SHOW`, etc.) pass through to real database
- **Docker Compose integration** - Seamless testing with containerized services
- **HTTP mock interception** - External service calls are intercepted and mocked with automatic DNS resolution
- **Strict mock verification** - Tests fail if mocks are defined but not used (catches silent fallback behavior)
- **High performance** - Hot mock reloading eliminates per-test container restarts (5-10x faster than traditional approaches)

### Performance

LineSpec uses several optimizations to minimize test execution time:

| Optimization | Impact |
|-------------|--------|
| **Hot mock reloading** | Proxy container stays running; mocks filtered by test name |
| **Mock aggregation** | All mocks loaded once at startup |
| **Active readiness probes** | Eliminates fixed delays (200ms instead of 2000ms) |
| **HTTP interception** | External service calls mocked instantly (no 5s timeouts) |

**Typical performance:**
- 5 tests: ~14 seconds total (~2.8s per test)
- 9 tests: ~14 seconds total (~1.5s per test)

The proxy container starts once and serves all tests via the `/activate` control API endpoint.
