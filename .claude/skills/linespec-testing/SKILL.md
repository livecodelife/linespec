---
name: linespec-testing
description: How to run, write, and debug LineSpec integration tests. Covers the test runner CLI, DSL structure, payload files, variable interpolation, channel types, and common failure patterns.
when_to_use: "When running linespec tests, writing or modifying .linespec files, debugging test failures, setting up a new test suite, or understanding how the test infrastructure works."
---

# LineSpec Testing

LineSpec is a protocol-level integration testing DSL. Tests run the real service inside a Docker container and intercept its external calls (database queries, HTTP requests, Redis commands, Kafka messages) at the wire protocol level — no mocks baked into the service code, no changes to the service under test.

## Mental Model

A linespec test defines one request/response cycle:

1. **RECEIVE** — the HTTP request (or Kafka message) that triggers the service
2. **EXPECT** — every external interaction the service must make, in order, with what to return
3. **RESPOND** — the HTTP response the service must produce

The runner fires the trigger, the proxy sidecars intercept traffic from the service, and at the end it verifies every EXPECT was hit and the response matched.

## Running Tests

```bash
# Run all specs in a directory (uses .linespec.yml in that directory)
linespec test path/to/linespecs/

# Run a single spec file
linespec test path/to/linespecs/create_user_success.linespec
```

The runner:
1. Builds and starts the service container (and its database/Redis/Kafka dependencies)
2. For each spec: clears the mock registry → fires the trigger → verifies all interactions
3. Tears down containers when done

Tests are independent and isolated: the registry is cleared between runs, and database tables are truncated.

## Project Structure

```
my-linespecs/
  .linespec.yml          # Config: service, database, infrastructure, dependencies
  create_user.linespec   # One test per file
  get_user.linespec
  payloads/
    create_user_request.yaml
    user_db_row.yaml
    user_response.json
```

The `.linespec.yml` file tells the runner how to build and wire up the service. Every linespecs directory needs one. Key sections:

```yaml
service:
  name: my-service
  service_dir: ../my-service   # Path to source code (relative to .linespec.yml)
  framework: rails             # rails | fastapi | django | express | chi | custom
  port: 3000
  health_endpoint: /up
  start_command: bundle exec rails server -b 0.0.0.0 -p 3000  # optional override

database:
  type: mysql                  # mysql | postgresql | mongodb
  image: mysql:8.4
  port: 3306
  container: db
  database: mydb
  username: myuser
  password: mypassword
  init_script: ../my-service/init.sql   # Seeds schema on first boot

infrastructure:
  database: true
  kafka: true      # Enable if tests use EVENT/MESSAGE expectations
  redis: true      # Enable if tests use READ/WRITE:REDIS expectations

dependencies:
  - name: user-service
    type: http
    host: user-service.local   # Hostname the SUT dials to reach this service
    port: 3001
    proxy: true                # Intercept calls to this host
```

## DSL Structure

Every `.linespec` file follows this exact order — no exceptions:

```
TEST <name>          (optional — defaults to filename)
VARS                 (optional — declare typed variables)
RECEIVE              (exactly one)
EXPECT               (zero or more)
EXPECT_NOT           (zero or more)
RESPOND              (exactly one, must be last)
```

### RECEIVE

```
RECEIVE HTTP:POST /api/v1/users
WITH {{payloads/create_user_request.yaml}}
HEADERS
  Authorization: Bearer ${AUTH_TOKEN}
```

### EXPECT channels

| Channel | What it intercepts |
|---------|-------------------|
| `READ:MYSQL <table>` | SELECT queries on the table |
| `WRITE:MYSQL <table>` | INSERT/UPDATE/DELETE on the table |
| `READ:POSTGRESQL <table>` | SELECT queries |
| `WRITE:POSTGRESQL <table>` | INSERT/UPDATE/DELETE |
| `READ:REDIS <CMD> <key>` | GET, HGET, LRANGE, etc. |
| `WRITE:REDIS <CMD> <key>` | SET, DEL, HSET, etc. |
| `READ:MONGODB <collection>` | Find queries |
| `WRITE:MONGODB <collection>` | Insert/update/delete |
| `HTTP:GET <url>` | Outbound HTTP calls to a declared dependency |
| `EVENT:<topic>` / `MESSAGE:<topic>` | Kafka messages produced to a topic |

Multiple EXPECT blocks on the same table are matched in declaration order (first declared, first consumed). This is how INSERT + UPDATE sequences are distinguished.

### SQL matching

Two keywords control how database queries are matched:

- **`USING_SQL`** — exact match after normalization (backticks stripped, whitespace collapsed, `table.*` → `*`)
- **`USING_SQL_CONTAINS`** — substring match after normalization

**Use `USING_SQL_CONTAINS` when:**
- ORM may append `ORDER BY`, `LIMIT`, or change column lists
- Driver uses prepared statements with `?` placeholders (Go MySQL driver)
- Rails `where(...).first` appends `ORDER BY id ASC`

```
# Fragile — breaks if ORM adds ORDER BY
EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM users WHERE id = 42 LIMIT 1
"""
RETURNS {{user.yaml}}

# Stable — only checks the part you control
EXPECT READ:MYSQL users
USING_SQL_CONTAINS """
WHERE users.id = 42
"""
RETURNS {{user.yaml}}
```

### RESPOND

```
RESPOND HTTP:201
WITH {{payloads/created_user.yaml}}
NOISE
  body.id
  body.created_at
  body.updated_at
```

`NOISE` lists fields to exclude from the response comparison. Use it for IDs and timestamps that are generated at runtime.

### EXPECT_NOT

Asserts an interaction must NOT occur:

```
EXPECT_NOT WRITE:MYSQL users    # No write should happen
EXPECT_NOT HTTP:GET ${AUTH_URL} # Cached response should not re-fetch
```

### VERIFY

Validates the actual intercepted query or command at runtime:

```
EXPECT WRITE:MYSQL users
VERIFY query MATCHES /\bpassword_digest\b/
VERIFY query NOT_CONTAINS `password`
```

Operators: `CONTAINS`, `NOT_CONTAINS`, `MATCHES` (Go regexp). Targets: `query` (SQL), `command`/`key`/`value` (Redis).

## Variable Interpolation

`${VAR_NAME}` is substituted everywhere — URLs, headers, SQL, payload files. Rules:
- Variable names must be uppercase with underscores
- If the variable is set in the environment, that value is used
- Otherwise a random value is generated and injected into the service container — this forces the service to read from the environment rather than having hardcoded secrets

To declare typed variables explicitly (required for integer-typed JSON numbers):

```
VARS
  AUTH_TOKEN: string
  USER_ID: integer    # Renders as a JSON number, not a quoted string
  ITEM_UUID: uuid
```

Without `VARS`, a name ending in `_UUID` gets a UUID; everything else gets a random string.

## Payload Files

Payloads are YAML or JSON files referenced with `{{payloads/file.yaml}}` syntax. They can contain `${VAR_NAME}` interpolation. Files must exist at parse time — the runner fails immediately if a referenced file is missing.

Write result payloads for MySQL/PostgreSQL writes:

```yaml
# order_insert_result.yaml
affected_rows: 1
last_insert_id: 42
```

```yaml
# postgres_write_result.yaml
affected_rows: 3
```

## Debugging Failures

### "expectation failed: [WRITE:MYSQL] on [users] was never called"

The service ran but never made this database call. Possible causes:
- The service returned early (check auth/validation logic)
- The ORM is calling a different table or operation type
- A prior EXPECT's SQL didn't match, so the mock was never consumed and the service got an unexpected response

To see the actual SQL being sent, check the proxy container logs:
```bash
docker logs <proxy-container-name>
```
Container names follow the naming template in `.linespec.yml` (default pattern: `proxy-mysql-<spec-name>`).

### "expectation failed: [HTTP:GET] on [user-service.local] was never called"

The service didn't call the expected HTTP dependency. Check:
- The dependency hostname in `dependencies:` matches what the service actually dials
- The `service.environment` has the correct URL variable pointing to that hostname

### "negative expectation failed: [WRITE:MYSQL] on [users] was called 1 times"

An `EXPECT_NOT` block was violated — the service made a call it shouldn't have. Usually a logic bug in the service.

### "VERIFY failed: query does not contain 'password_digest'"

The actual intercepted query didn't satisfy a `VERIFY` rule. The error message includes the actual query text. Check the service's write logic.

### Response body mismatch

The service returned a different body than what `RESPOND WITH {{file}}` specified. The error shows a diff. Common fixes:
- Add `NOISE` for fields that vary (IDs, timestamps)
- Update the payload file to match the service's actual response shape
- Check that variable interpolation is working as expected

### "failed to load service config" / service won't start

Check:
- `service_dir` path is correct relative to `.linespec.yml`
- `start_command` is correct for the framework
- `health_endpoint` actually returns 2xx when the service is up
- `init_script` path is valid if specified

### SQL didn't match / proxy returned wrong data

Enable `USING_SQL_CONTAINS` with just a stable fragment of the query. To find the actual query the service sends, check proxy container logs or add a `VERIFY query CONTAINS 'something'` rule intentionally so the error output shows the real query.

### Variable values don't match across the spec

If a `${VAR}` appears in the RECEIVE and in a RETURNS payload, both must resolve to the same value. The first use in the spec defines the value for the entire test. If the service's response doesn't contain the expected variable value, check the payload file is using `${VAR_NAME}` (not a hardcoded value) and that the service is reading from the environment rather than hardcoding.

## Example Suites

Look at these example suites for reference patterns:

| Suite | What it demonstrates |
|-------|---------------------|
| `examples/todo-linespecs/` | Rails + MySQL + HTTP dep + Kafka events |
| `examples/user-linespecs/` | Rails + MySQL, CRUD operations, auth, VARS |
| `examples/notification-linespecs/` | FastAPI + PostgreSQL + Redis cache hit/miss |
| `examples/multi-db-linespecs/` | Go service + MySQL + MongoDB simultaneously |

Run an example suite (requires Docker):
```bash
linespec test examples/todo-linespecs/
```
