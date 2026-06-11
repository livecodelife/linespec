---
name: linespec-testing
description: How to run, write, and debug LineSpec integration tests. Covers the test runner CLI, DSL structure, payload files, variable interpolation, channel types, semantic SQL matching, and common failure patterns.
when_to_use: "When running linespec tests, writing or modifying .linespec files, debugging test failures, setting up a new test suite, or understanding how the test infrastructure works."
---

# LineSpec Testing

LineSpec is a protocol-level integration testing DSL. Tests run the real service inside a Docker container and intercept its external calls (database queries, HTTP requests, Redis commands, Kafka messages, gRPC calls) at the wire protocol level — no mocks baked into the service code, no changes to the service under test.

## Mental Model

A linespec test defines one trigger/response cycle:

1. **RECEIVE** — the trigger: an HTTP request, a Kafka/event message, a gRPC call, or a background job
2. **EXPECT** — every external interaction the service must make, in order, and what to return for each
3. **RESPOND** — the HTTP response the service must produce (for HTTP triggers)

The runner fires the trigger, proxy sidecars intercept the service's outbound traffic, and at the end it verifies every EXPECT was hit and the response matched.

## Running Tests

```bash
# Run every spec in a directory
linespec test path/to/linespecs/

# Run a single spec file
linespec test path/to/linespecs/create_user_success.linespec
```

The runner builds and starts the service container (plus its database/Redis/Kafka/gRPC dependencies), and for each spec clears the mock registry → fires the trigger → verifies interactions → tears down.

## Configuration: where `.linespec.yml` lives

The runner finds config by **walking UP the directory tree** from the spec path to the repo root, using the **nearest** `.linespec.yml` (or `.linespec.yaml`). You can also point at one explicitly with the `LINESPEC_CONFIG` environment variable.

**You do NOT need a `.linespec.yml` in every directory.** One at the root of your project (or spec tree) covers specs in all subdirectories. Put it wherever it logically governs your specs; nested configs are only for multi-service / multi-pack setups.

```yaml
service:
  name: my-service
  service_dir: ../my-service   # source code path, relative to this .linespec.yml
  framework: rails             # rails | fastapi | django | express | chi | custom
  port: 3000
  health_endpoint: /up         # framework default if omitted

database:
  type: mysql                  # mysql | postgresql | mongodb
  image: mysql:8.4
  database: mydb
  username: myuser
  password: mypassword
  init_script: ../my-service/init.sql

infrastructure:
  database: true
  kafka: true     # enable for EVENT/MESSAGE expectations
  redis: true     # enable for READ/WRITE:REDIS expectations
  grpc: true      # enable for GRPC expectations

dependencies:
  - name: user-service
    type: http
    host: user-service.local   # hostname the SUT dials
    proxy: true                # intercept calls to this host
```

Multiple databases: use a `databases:` list (each entry gets its own container + proxy). `job_backend:` configures background-job workers (see below).

## DSL Structure

Every `.linespec` file follows this exact order:

```
TEST <name>          (optional — defaults to filename)
VARS                 (optional — typed variables)
RECEIVE              (exactly one)
EXPECT               (zero or more)
EXPECT_NOT           (zero or more)
RESPOND              (exactly one, last)
```

### RECEIVE — the trigger

```
# HTTP
RECEIVE HTTP:POST /api/v1/users
WITH {{payloads/create_user_request.yaml}}
HEADERS
  Authorization: Bearer ${AUTH_TOKEN}

# Kafka / event consumer (KAFKA: and EVENT: are equivalent)
RECEIVE KAFKA:user-events
WITH {{payloads/user_created_event.json}}

# gRPC
RECEIVE GRPC:user.UserService/CreateUser
WITH {{payloads/create_user.json}}

# Background job (no HTTP response; see job_backend config)
RECEIVE JOB
```

`TIMEOUT <duration>` (e.g. `TIMEOUT 30s`) may follow RECEIVE to override the per-test timeout.

### EXPECT channels

| Channel | Intercepts |
|---|---|
| `HTTP:<METHOD> <url>` | outbound HTTP calls to a declared dependency |
| `READ:MYSQL <table>` / `WRITE:MYSQL <table>` | SELECT / INSERT·UPDATE·DELETE |
| `READ:POSTGRESQL <table>` / `WRITE:POSTGRESQL <table>` | SELECT / write |
| `READ:REDIS <CMD> <key>` / `WRITE:REDIS <CMD> <key>` | GET/HGET/… / SET/DEL/… |
| `READ:MONGODB <coll>` / `WRITE:MONGODB <coll>` | find / insert·update·delete |
| `GRPC:<package.Service/Method>` | gRPC calls |
| `EVENT:<topic>` / `MESSAGE:<topic>` | Kafka messages produced to a topic |

Multiple EXPECTs on the same table match in declaration order. Use `CALL N` to disambiguate repeated identical queries (e.g. `EXPECT READ:MYSQL users CALL 2`).

### EXPECT options

```
EXPECT HTTP:POST http://payment-service.local/charge
WITH {{payloads/charge_request.json}}      # assert the outbound REQUEST body
HEADERS
  Idempotency-Key: ${KEY}                  # match request headers
RETURNS {{payloads/charge_response.json}}  # mocked RESPONSE body
RESPONSE_HEADERS
  Content-Type: application/json           # set response headers explicitly
```

- **`WITH {{file}}`** — matches the **outbound request body** the service sends. Omit it to match any body. (It is *not* the response.)
- **`RETURNS`** — the mocked response. Forms: `{{file}}`, `EMPTY`, `ERROR` (close the connection — service sees `io.EOF`), `ERROR <label>`, `HTTP:NNN` (status code). For a non-200 HTTP response with a body, use `RETURNS {{file}}` and include a `status:` field in that payload.
- **`RESPONSE_HEADERS`** — explicit response headers. Without it, `Content-Type` is **inferred from the payload file extension** (`.json`→`application/json`, `.yaml`/`.yml`→`application/yaml`, `.xml`→`application/xml`).

### SQL matching — semantic (recommended)

Match queries by structure, not brittle text. This is stable against ORM-added `ORDER BY`/`LIMIT`, column reordering, and `$1`/`?` placeholder styles:

```
EXPECT READ:POSTGRESQL users
ACCESSING_TABLES users
VERIFY_OPERATION SELECT
VERIFY_WHERE_COLUMNS id
VERIFY_WHERE
  id: 42
RETURNS {{payloads/user.yaml}}

EXPECT WRITE:MYSQL users
VERIFY_OPERATION INSERT
VERIFY_WRITTEN_VALUES
  email: ${EMAIL}
RETURNS {{payloads/insert_result.yaml}}
```

- `ACCESSING_TABLES` — tables the query must touch (list two for a JOIN)
- `VERIFY_OPERATION` — `SELECT` | `INSERT` | `UPDATE` | `DELETE`
- `VERIFY_WHERE_COLUMNS` — columns that must appear in the WHERE clause
- `VERIFY_WHERE` — specific WHERE column = value pairs
- `VERIFY_WRITTEN_VALUES` — column = value pairs for INSERT/UPDATE

**Legacy (deprecated):** `USING_SQL """…"""` (exact match after normalization) and `USING_SQL_CONTAINS """…"""` (substring). Prefer semantic matching; reach for these only for queries semantic matching can't express.

### VERIFY — validate intercepted data

```
EXPECT WRITE:MYSQL users
VERIFY query MATCHES /\bpassword_digest\b/
VERIFY query NOT_CONTAINS `password`
```

Operators: `CONTAINS`, `NOT_CONTAINS`, `MATCHES` (Go regexp). Targets by channel:

| Channel | VERIFY targets |
|---|---|
| MySQL / PostgreSQL | `query` |
| HTTP | `headers.<name>`, `body`, `url`, `path` |
| Kafka / EVENT | `key`, `value`, `headers.<name>` |
| gRPC | `request_body`, `metadata.<name>` |
| Redis | `command`, `key`, `value` |

### EXPECT_NOT — assert an interaction did NOT happen

```
EXPECT_NOT WRITE:MYSQL users          # underscore or "EXPECT NOT" both work
EXPECT_NOT READ:POSTGRESQL audit_log
EXPECT_NOT WRITE:MONGODB sessions
EXPECT_NOT HTTP:GET ${AUTH_URL}
```

Negative expectations are enforced for the SQL and MongoDB stores (MySQL, PostgreSQL, MongoDB read/write) and HTTP.

### RESPOND

```
RESPOND HTTP:201
WITH {{payloads/created_user.yaml}}
NOISE
  body.id
  body.created_at
```

`NOISE` lists response fields to exclude from comparison (runtime-generated IDs, timestamps).

## Background Jobs

For worker services, set `RECEIVE JOB` and configure `job_backend`:

```yaml
job_backend:
  type: redis        # redis | kafka | scheduled
  queue: queue:default   # Redis queue key or Kafka topic (omit for scheduled)
```

The runner enqueues the job (Redis BRPOP/BLPOP/LPOP-based workers, Kafka consumers) or, for `scheduled`, observes a cron-triggered run without seeding. EXPECT blocks then assert the work the job performs.

## gRPC

Set `infrastructure.grpc: true`. JSON `RETURNS` payloads are sent as `application/grpc+json` by default; to emit binary protobuf (`application/grpc`), configure a compiled descriptor set via `grpc_descriptor_set` (service-level or per-dependency). Unmocked calls pass through to the real upstream when configured. Use `RETURNS EMPTY` for a method that returns an empty message.

## Variable Interpolation

`${VAR_NAME}` is substituted everywhere — URLs, headers, SQL, payload files. If the variable is set in the environment, that value is used; otherwise a random value is generated and injected into the service container (forcing the service to read from the environment rather than hardcoding secrets). Declare types explicitly when needed:

```
VARS
  AUTH_TOKEN: string
  USER_ID: integer            # renders as a JSON number, not a quoted string
  ITEM_UUID: uuid
  STATUS: enum(active,banned) # constrained set
```

Typed constraints are supported: integer `min`/`max`, string `length`/`charset`/`pattern`, and `enum`.

## Payload Files

Referenced as `{{payloads/file.yaml}}` (JSON, YAML, or XML). They may contain `${VAR}` interpolation. Files must exist at parse time. Write-result payloads:

```yaml
# mysql_write_result.yaml
affected_rows: 1
last_insert_id: 42
```

## Debugging Failures

- **`[WRITE:MYSQL] on [users] was never called`** — the service returned early, hit a different table/op, or a prior EXPECT didn't match (so its mock was never consumed). Check proxy logs: `docker logs <proxy-container>`.
- **`[HTTP:GET] on [host] was never called`** — the dependency hostname in `dependencies:` doesn't match what the service dials, or the service's URL env var is wrong.
- **`negative expectation failed: [WRITE:MYSQL] … was called`** — an `EXPECT_NOT` was violated; usually a service logic bug.
- **`VERIFY failed`** — the intercepted query/body didn't satisfy a `VERIFY` rule; the error includes the actual value.
- **Response body mismatch** — add `NOISE` for varying fields, or fix the payload to match the service's real response shape.
- **SQL didn't match** — prefer semantic matching (`ACCESSING_TABLES` + `VERIFY_*`); if using legacy text matching, use `USING_SQL_CONTAINS` with a stable fragment.

## Example Suites

| Suite | Demonstrates |
|---|---|
| `examples/todo-linespecs/` | Rails + MySQL + HTTP dep + Kafka events |
| `examples/user-linespecs/` | Rails + MySQL, CRUD, auth, VARS |
| `examples/notification-linespecs/` | FastAPI + PostgreSQL + Redis cache hit/miss |
| `examples/multi-db-linespecs/` | Go + MySQL + MongoDB simultaneously |

```bash
linespec test examples/todo-linespecs/
```
