# LineSpec

LineSpec is a DSL-based integration testing tool that executes service behavior specifications directly against containerized services. It uses a custom domain-specific language (.linespec files) to define test cases, then runs them with automatic database and HTTP mocking.

## Installation

### From source (Go)

```bash
git clone https://github.com/anomalyco/linespec.git
cd linespec
go install ./cmd/linespec
```

The `linespec` binary will be installed to `$GOPATH/bin` (or `$HOME/go/bin` by default).

## Usage

### Running Tests

```bash
linespec test <path>
```

**Arguments**

- `<path>` - Path to a `.linespec` file or directory containing `.linespec` files (required)

**Example**

```bash
linespec test examples/test-set-0/
linespec test examples/test-set-0/create-user.linespec
```

**Prerequisites** — Docker must be installed and running.

**How it works:**

1. **Parse** — Loads and parses .linespec files
2. **Setup** — Starts shared infrastructure (MySQL, Kafka if configured)
3. **Execute** — For each test:
   - Starts database proxy with per-test mock registry
   - Starts HTTP proxy with DNS aliases for dependencies
   - Starts the application container
   - Waits for health check
   - Sends the HTTP trigger request
   - Collects hits from `/verify` endpoint
4. **Verify** — Validates response, SQL queries, HTTP mock usage
5. **Teardown** — Stops test-specific containers

**Expected output:**

```
✓ Loaded 3 tests
→ Starting infrastructure...
✓ MySQL ready (mysql:3306)
✓ Kafka ready (kafka:29092)
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

summary: 2 passed, 1 failed
```

> **Note:** Differing lines are marked with ` ~ ` and are coloured red (expected) / green (actual) in a terminal.

## Configuration

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

## Specification Format

A `.linespec` file defines a test case with statement types:

### TEST

Defines the test name:

```
TEST test-name
```

If omitted, the filename (without extension) is used as the test name.

### RECEIVE

Specifies the incoming HTTP request (required, must be first):

```
RECEIVE HTTP:<METHOD> <URL>
[WITH {{payload/request.yaml}}]
[HEADERS
  <header_name>: <header_value>
  ...]
```

- `<METHOD>` - HTTP method (GET, POST, PUT, DELETE, etc.)
- `<URL>` - Full URL including protocol and host
- `WITH` (optional) - Path to a YAML file containing the request body
- `HEADERS` (optional) - Additional HTTP headers with indented key: value pairs

Example:
```linespec
RECEIVE HTTP:POST http://localhost:3000/api/todos
WITH {{payloads/create_todo.yaml}}
HEADERS
  Authorization: Bearer token_abc123xyz
```

### EXPECT

Defines external dependencies that MUST occur (zero or more allowed):

```
EXPECT <CHANNEL> <resource>
[USING_SQL """
<raw-sql-query>
"""]
[WITH {{payload/input.yaml}}]
[RETURNS {{payload/output.yaml}}]
[RETURNS EMPTY]
[VERIFY query CONTAINS 'string']
[VERIFY query NOT_CONTAINS 'string']
[VERIFY query MATCHES /regex/]
```

Supported channels:
- `HTTP:<METHOD> <URL>` - External HTTP calls
- `READ_MYSQL <table>` - Database reads (requires `RETURNS` or `RETURNS EMPTY`)
- `WRITE_MYSQL <table>` - Database writes
- `READ_POSTGRESQL <table>` - PostgreSQL reads
- `WRITE_POSTGRESQL <table>` - PostgreSQL writes
- `EVENT:<topic>` / `MESSAGE:<topic>` - Message queue events (both aliases work)

**HTTP Expectations:**

```linespec
EXPECT HTTP:GET http://user-service.local/api/v1/users/auth
HEADERS
  Authorization: Bearer token_abc123xyz
RETURNS {{payloads/auth_response.yaml}}
```

LineSpec extracts hostnames from HTTP expectations and sets up DNS aliases. Tests fail if HTTP mocks are defined but not invoked.

**MySQL/PostgreSQL Operations:**

```linespec
# Read operation (requires RETURNS)
EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM users WHERE id = 123
"""
RETURNS {{payloads/user.yaml}}

# Write operation
EXPECT WRITE:MYSQL users
WITH {{payloads/user_create.yaml}}

# Write with SQL verification
EXPECT WRITE:MYSQL users
WITH {{payloads/user_create.yaml}}
VERIFY query CONTAINS 'password_digest'
```

**Empty Results:**

```linespec
EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM users WHERE id = 999
"""
RETURNS EMPTY
```

### EXPECT_NOT

Negative assertions — assert that certain operations do NOT occur:

```linespec
EXPECT_NOT READ:MYSQL users
USING_SQL """
SELECT * FROM users
"""
```

Useful for testing query optimization (e.g., ensuring the application uses indexed lookups instead of full table scans).

### RESPOND

Specifies the HTTP response (required, must be last):

```
RESPOND HTTP:<STATUS_CODE>
[WITH {{payload/response.yaml}}]
[NOISE
  body.<field>
  body.<field>]
```

- `<STATUS_CODE>` - HTTP status code (200, 201, 400, 500, etc.)
- `WITH` (optional) - Path to a YAML file containing the response body
- `NOISE` (optional) - Lists response body fields to ignore during comparison

Example:
```linespec
RESPOND HTTP:201
WITH {{payloads/created_todo.yaml}}
NOISE
  body.id
  body.created_at
  body.updated_at
```

## Examples

### Simple Write Operation

```linespec
TEST create-user
RECEIVE HTTP:POST http://localhost:3000/users
WITH {{payloads/user_create_req.yaml}}

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

```linespec
TEST create-user-secure
RECEIVE HTTP:POST http://localhost:3000/users
WITH {{payloads/user_create_req.yaml}}

EXPECT WRITE:MYSQL users
WITH {{payloads/user_create_req.yaml}}
VERIFY query CONTAINS 'password_digest'
VERIFY query NOT_CONTAINS 'password'

RESPOND HTTP:201
WITH {{payloads/user_create_resp.yaml}}
NOISE
  body.id
  body.created_at
  body.updated_at
```

Output when verification fails:
```
✗ create-user-secure FAIL

  🔒 SQL Verification Error:
    VERIFY FAILED: Query does not contain 'password_digest'.
    Actual query: INSERT INTO `users` (`name`, `email`, `password`) ...

  Expected status : 201
  Actual status   : 500
```

### HTTP Mock Verification

```linespec
TEST microservice-call
RECEIVE HTTP:GET http://localhost:3000/api/data
HEADERS
  Authorization: Bearer token123

EXPECT HTTP:GET http://user-service.local/api/v1/users/auth
HEADERS
  Authorization: Bearer token123
RETURNS {{payloads/auth_response.yaml}}

RESPOND HTTP:200
WITH {{payloads/combined_response.yaml}}
```

Output when HTTP mock not invoked:
```
✗ microservice-call FAIL

  🔌 HTTP Mock Verification Error:
    HTTP Mock(s) not invoked: microservice-call-mock-0

  Expected status : 200
  Actual status   : 200
```

### Read with Empty Results (Not Found)

```linespec
TEST get-user-not-found
RECEIVE HTTP:GET http://localhost:3000/users/999
HEADERS
  Authorization: Bearer token_abc123xyz

EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM `users` WHERE `users`.`id` = 999 LIMIT 1
"""
RETURNS EMPTY

RESPOND HTTP:404
WITH {{payloads/not_found_error.yaml}}
```

### Using EXPECT_NOT

```linespec
TEST efficient-user-lookup
RECEIVE HTTP:GET http://localhost:3000/users/123

# Assert that we DON'T do a full table scan
EXPECT_NOT READ:MYSQL users
USING_SQL """
SELECT * FROM `users`
"""

# Should use indexed lookup instead
EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM `users` WHERE `users`.`id` = 123 LIMIT 1
"""
RETURNS {{payloads/user.yaml}}

RESPOND HTTP:200
WITH {{payloads/user.yaml}}
```

### PostgreSQL Operations

```linespec
TEST create-item-postgres
RECEIVE HTTP:POST http://localhost:3000/items
WITH {{payloads/item_create.yaml}}

EXPECT WRITE:POSTGRESQL items
WITH {{payloads/item_create.yaml}}

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

## Proxy Commands

Start individual protocol proxies for development:

```bash
linespec proxy mysql <listen-addr> <upstream-addr> [registry-file]
linespec proxy postgresql <listen-addr> <upstream-addr> [registry-file]
linespec proxy http <listen-addr> <upstream-addr> [registry-file]
linespec proxy kafka <listen-addr> <upstream-addr> [registry-file]
```

## Payload File Conventions

### HTTP Request/Response Payloads
```yaml
name: User One
email: user_one@example.com
```

### MySQL Read Result Payloads
```yaml
rows:
  - id: 1
    name: User One
    email: user_one@example.com
```

### Event Payloads
```yaml
topic: todo-events
key: todo-123
value:
  id: 1
  title: Buy milk
  completed: false
```

## Development

```bash
go run ./cmd/linespec          # Run CLI in development mode
go build -o linespec ./cmd/linespec  # Build binary
go test ./...                  # Run tests
```

## Key Features

- **Human-readable DSL** - Define service behavior in plain text
- **Direct execution** - No compile step, tests run immediately from .linespec files
- **Pattern matching** - SQL queries matched by operation and table, so ORM-specific variations work automatically
- **Infrastructure pass-through** - Schema queries pass through to real database
- **HTTP mock interception** - External service calls intercepted with automatic DNS resolution
- **Strict mock verification** - Tests fail if mocks are defined but not used
- **Negative assertions** - EXPECT_NOT for testing what should NOT happen
- **SQL verification** - VERIFY clauses for runtime SQL validation
- **Per-test isolation** - Each test gets its own mock registry while sharing infrastructure

### Proxy Pattern Matching

The proxy matches queries using a hierarchy:

1. **Exact match first** (normalized by removing backticks, lowercasing)
2. **Query type checking** — SELECT mocks only match SELECT queries
3. **Pattern matching** — For INSERT/UPDATE/DELETE, match by table name prefix
4. **Table matching** — For SELECT, match by table name in FROM clause

Example:
```
Mock:    insert into users (name, email) values (...)
Rails:   INSERT INTO `users` (`name`, `email`, `created_at`) VALUES (...)
Result:  ✓ MATCH (normalized + table prefix)
```

### Infrastructure Pass-Through

These queries always pass through to the real database:
- `SET NAMES ...`
- `COM_PING`
- `information_schema` queries
- `schema_migrations` checks
- `SHOW ...` queries
- `BEGIN`, `COMMIT`, `ROLLBACK` (transactions)

## How It Works

LineSpec directly parses and executes .linespec files — no compilation step is needed. The test runner:

1. Reads `.linespec.yml` for service configuration
2. Starts shared Docker infrastructure (MySQL, Kafka)
3. For each test, starts proxies and the application
4. Sends the HTTP trigger request
5. Verifies that all expected mocks were called
6. Validates SQL queries against VERIFY clauses
7. Compares responses (respecting NOISE fields)

The proxy system intercepts database and HTTP calls, matching them against expectations and returning mock responses when appropriate.
