# LineSpec

LineSpec is a DSL compiler that generates Keploy-compatible KTests and KMocks from human-readable service behavior specifications. It enables TDD by allowing you to define service behavior and database interactions in a declarative, human-readable format.

## Installation

### From npm (recommended)

```bash
npm install -g linespec
```

### From source (development)

```bash
git clone https://github.com/anomalyco/linespec.git
cd linespec
npm install
npm run build
npm link
```

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
✓ MySQL proxy listening on port 54321 → localhost:3306
✓ Service healthy at http://localhost:3000
✓ test-1 PASS
✗ test-2 FAIL (body mismatch)
  Expected status : 200
  Actual status   : 200
  {                                           {
    "id": 1,                                    "id": 1,
    "title": "Buy milk"              ~          "title": "Buy bread"
  }                                           }
✓ test-3 PASS
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
- `HTTP:<METHOD> <URL>` - External HTTP calls
  - `READ_MYSQL <table>` - Database reads (requires `RETURNS`)
  - `WRITE_MYSQL <table>` - Database writes (auto-transactional by default)
  - `WRITE_POSTGRESQL <table>` - PostgreSQL writes
  - `EVENT <topic>` - Message queue events

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
npm run dev        # Run CLI in development mode
npm run build      # Compile TypeScript
npm run test       # Run tests
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
