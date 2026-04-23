# LineSpec DSL Reference

LineSpec is a deterministic domain-specific language (DSL) for describing service behavior and defining integration tests that execute directly against containerized services.

The goal of LineSpec is to:

* Provide a concise, readable way to describe service behavior
* Enforce strict structural rules to keep parsing simple
* Execute deterministically without inference or heuristics
* Support database mocking, HTTP interception, and message queue testing

---

# Setup

Before running `linespec test`, the `linespec:latest` Docker image must exist in your local Docker daemon. This image is used by all protocol proxy sidecars (PostgreSQL, MySQL, HTTP, Kafka, etc.).

**Homebrew installs** build the image automatically during `brew install linespec`. If Docker was not running at install time, run:

```bash
linespec build
```

**Go install / manual binary** installs must always run `linespec build` once after installation.

If you see `Error response from daemon: No such image: linespec:latest`, run `linespec build` to fix it.

---

# Core Design Principles

1. Deterministic parsing — no NLP, no guessing.
2. Single entrypoint and single exit per spec.
3. Clear separation between:
   * Trigger (RECEIVE)
   * External dependencies (EXPECT)
   * System response (RESPOND)
4. All payload shapes are defined externally in YAML or JSON files.

---

# File Extension

Recommended extension:

```
.linespec
```

Example:

```
create_todo_success.linespec
```

---

# DSL Grammar Overview

A LineSpec file MUST follow this structure:

1. Exactly one RECEIVE statement
2. Zero or more EXPECT statements
3. Zero or more EXPECT_NOT statements
4. Exactly one RESPOND statement

Statements MUST appear in this order:

```
TEST <name>        (optional)
VARS               (optional — declare typed variables)
RECEIVE
EXPECT (0..n)
EXPECT_NOT (0..n)
RESPOND
```

No statements may appear after RESPOND.

---

# Top-Level Structure

Optional test name declaration:

```
TEST <test_name>
```

If omitted, the filename (without extension) is used as the test name.

## VARS Block (optional)

Declare typed variables before `RECEIVE` to pre-generate values with an explicit type and optional constraints:

```
VARS
  VAR_NAME: <type> [constraint=value ...]
  VAR_NAME: <type> [constraint=value ...]
  ...
```

The `VARS` block must appear after `TEST` (if present) and before `RECEIVE`. Each line declares one variable using the format `VAR_NAME: type` followed by zero or more `key=value` constraint pairs.

### Supported types

| Type | Default generated value | Supported constraints |
|------|------------------------|-----------------------|
| `uuid` | RFC 4122 v4 UUID, e.g. `550e8400-e29b-41d4-a716-446655440000` | _(none)_ |
| `integer` | Random integer between 1 and 99999 | `min=N`, `max=N` |
| `string` | `lowercase_varname_` + 8 random hex chars | `length=N`, `charset=<set>`, `pattern=<regex-like>` |
| `enum` | _(required: must provide `values`)_ | `values=a,b,c` |

### Constraints reference

**`integer`**

- `min=N` — lower bound (inclusive). Default: 1.
- `max=N` — upper bound (inclusive). Default: 99999.

**`string`**

- `length=N` — exact character count of the generated string.
- `charset=<set>` — character pool. Supported values: `alphanumeric`, `alpha`, `numeric`, `hex`, `lowercase`, `uppercase`. Default: `hex`.
- `pattern=<regex-like>` — generate a string matching a simplified regex. Supports character classes (`[a-z]`, `[A-Z0-9]`), repetition counts (`{N}`), and literal text. For example, `pattern=prov-[0-9]{4}-[a-f0-9]{8}` generates strings like `prov-2026-dab46dda`.

**`enum`**

- `values=a,b,c` — comma-separated list of allowed values. One is chosen at random each run.

### Why use VARS?

Without `VARS`, variable types are inferred from the variable name (a variable ending in `_UUID` gets a UUID; everything else gets a string). `VARS` lets you be explicit — in particular, it is the only way to generate an integer-typed variable that encodes as a JSON number (not a quoted string) in payload files and HTTP responses.

### Resolution order

1. If the variable is already set in the environment, that value is used
2. Otherwise a random value of the declared type is generated and injected into the test container

### Examples

**Integer with bounds, string with charset:**

```linespec
TEST get_user_with_vars

VARS
  AUTH_TOKEN: string length=32 charset=alphanumeric
  USER_ID: integer min=1 max=9999

RECEIVE HTTP:GET /api/v1/users/${USER_ID}
HEADERS
  Authorization: Bearer ${AUTH_TOKEN}

EXPECT READ:MYSQL users
USING_SQL """
SELECT users.* FROM `users` WHERE `users`.`token` = '${AUTH_TOKEN}' LIMIT 1
"""
RETURNS {{payloads/user_response.json}}

EXPECT READ:MYSQL users
USING_SQL_CONTAINS """
WHERE users.id =
"""
RETURNS {{payloads/user_response.json}}

RESPOND HTTP:200
WITH {{payloads/user_public_response.json}}
```

`USER_ID` is declared as `integer`, so `${USER_ID}` is replaced by a number (e.g. `4271`) in the URL and in any payload file that references it. The mock registry receives it as a JSON number, so the service's response body encodes `user_id` as `4271`, not `"4271"`.

**String with pattern (provenance ID format):**

```linespec
VARS
  AUTH_TOKEN: string pattern=prov-[0-9]{4}-[a-f0-9]{8}
```

Generates values like `prov-2026-dab46dda` — useful when the service expects a token in a specific structured format.

**Enum:**

```linespec
VARS
  ORDER_STATUS: enum values=pending,active,cancelled
```

Picks one of the three values at random each run.

---

# Statement Definitions

## 1. RECEIVE

Defines the trigger request into the System Under Test (SUT).

Syntax:

```
RECEIVE HTTP:<METHOD> <URL>
[WITH {{<body_file>}}]
[HEADERS
  <header_name>: <header_value>
  ...]
```

Example:

```
RECEIVE HTTP:POST /api/v1/todos
WITH {{todo.yaml}}

RECEIVE HTTP:GET /api/v1/users/42
HEADERS
  Authorization: Bearer token_abc123xyz
```

Rules:

* Exactly one RECEIVE per file
* MUST appear before any EXPECT or EXPECT_NOT
* HTTP method is required
* URL is required (full URL including protocol and host)
* WITH is optional for HTTP requests without a body
* Body must reference an external YAML or JSON file
* HEADERS is optional and supports multiple header lines with indentation
* Headers are added to the HTTP request (Authorization, X-Custom-Header, etc.)
* WITH must come before HEADERS if both are present

---

## 2. EXPECT

Defines an external dependency interaction that MUST occur during execution.

General Syntax:

```
EXPECT <CHANNEL> <resource>
[USING_SQL """
<raw-sql-query>
"""]
[USING_SQL_CONTAINS """
<sql-fragment>
"""]
[WITH {{<request_file>}}]
[RETURNS {{<response_file>}}]
[RETURNS EMPTY]
[VERIFY query CONTAINS '<string>']
[VERIFY query NOT_CONTAINS '<string>']
[VERIFY query MATCHES /<regex>/]
```

The exact format depends on the channel type.

### SQL Matching: USING_SQL vs USING_SQL_CONTAINS

Two keywords control how the proxy matches intercepted SQL queries:

| Keyword | Match mode | When to use |
|---------|-----------|-------------|
| `USING_SQL` | Exact match after normalization | You control the exact query and want strict assertions |
| `USING_SQL_CONTAINS` | Substring match after normalization | ORM or driver may add clauses you can't predict |

**Normalization** (applied to both modes before comparison):
- Backticks are stripped
- Whitespace is collapsed to single spaces
- `table.*` column references are normalized to `*`

**When to use `USING_SQL_CONTAINS`:**
- Rails 7 `.where(...).first` appends `ORDER BY id ASC` — use a `WHERE` fragment
- Go MySQL driver uses `COM_STMT_PREPARE` with `?` placeholders — use a table/keyword fragment
- Any ORM that varies `SELECT` columns or adds `LIMIT` clauses

**Example:**

```
# Exact match — fails if ORM adds ORDER BY or changes column list
EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM users WHERE id = 42 LIMIT 1
"""
RETURNS {{user.yaml}}

# Substring match — stable even if ORM shape changes
EXPECT READ:MYSQL users
USING_SQL_CONTAINS """
WHERE users.id = 42
"""
RETURNS {{user.yaml}}
```

---

---

### EXPECT HTTP

```
EXPECT HTTP:<METHOD> <URL>
[HEADERS
  <header_name>: <header_value>
  ...]
RETURNS {{<response_body>}}
```

Example:

```
EXPECT HTTP:GET http://user-service.local/users/42
HEADERS
  Authorization: Bearer token_abc123xyz
RETURNS {{user_info.yaml}}
```

Rules:

* RETURNS is required for HTTP expectations
* HEADERS is optional; headers are matched against the actual request
* The proxy intercepts calls to the hostname and returns the mocked response
* Tests fail if the HTTP mock is defined but not invoked

---

### EXPECT READ:MYSQL

```
EXPECT READ:MYSQL <table_name>
[USING_SQL """
<SQL SELECT statement>
"""]
[USING_SQL_CONTAINS """
<sql-fragment>
"""]
RETURNS {{<response_file>}}
```

Or for empty results:

```
EXPECT READ:MYSQL <table_name>
[USING_SQL_CONTAINS """
<sql-fragment>
"""]
RETURNS EMPTY
```

Example — exact match:

```
EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM users WHERE token = 'abc' LIMIT 1
"""
RETURNS {{user_response.yaml}}
```

Example — substring match (ORM may add ORDER BY or vary columns):

```
EXPECT READ:MYSQL users
USING_SQL_CONTAINS """
WHERE users.id = 42
"""
RETURNS {{user_response.yaml}}
```

Rules:

* RETURNS is required (either a file or EMPTY)
* `USING_SQL` and `USING_SQL_CONTAINS` are both optional; if omitted, the proxy matches by table name
* `USING_SQL` performs exact equality after normalization
* `USING_SQL_CONTAINS` performs substring containment after normalization
* Only one of `USING_SQL` or `USING_SQL_CONTAINS` may appear per EXPECT block
* RETURNS EMPTY generates proper MySQL protocol response for zero rows

---

### EXPECT WRITE:MYSQL

```
EXPECT WRITE:MYSQL <table_name>
[USING_SQL """
<SQL INSERT/UPDATE/DELETE statement>
"""]
[USING_SQL_CONTAINS """
<sql-fragment>
"""]
[WITH {{<input_payload>}}]
[RETURNS {{<write_result_file>}}]
[NO TRANSACTION]
[VERIFY query CONTAINS '<string>']
[VERIFY query NOT_CONTAINS '<string>']
[VERIFY query MATCHES /<regex>/]
```

Example — simple write:

```
EXPECT WRITE:MYSQL users
WITH {{user_create.yaml}}
VERIFY query CONTAINS 'password_digest'
```

Example — INSERT followed by UPDATE using the inserted ID:

```
EXPECT WRITE:MYSQL orders
WITH {{order_insert.yaml}}
RETURNS {{order_insert_result.yaml}}

EXPECT WRITE:MYSQL orders
WITH {{order_status_update.yaml}}
RETURNS {{order_update_result.yaml}}
```

Where `order_insert_result.yaml` specifies the OK packet values the MySQL driver will read:

```yaml
affected_rows: 1
last_insert_id: 42
```

And `order_update_result.yaml`:

```yaml
affected_rows: 1
```

Rules:

* WITH is optional for write operations
* USING_SQL is optional; if omitted, the proxy matches by table name and operation type
* RETURNS is optional. When present, the payload must be a YAML object with optional `affected_rows` and `last_insert_id` fields. Omitting RETURNS defaults to `affected_rows=0, last_insert_id=0`.
* Multiple WRITE mocks on the same table are matched in declaration order (first declared, first consumed). This is how INSERT + UPDATE sequences are distinguished.
* NO TRANSACTION is parsed but has no effect (transactions always pass through)
* VERIFY clauses validate the actual SQL executed at runtime

---

### EXPECT READ:POSTGRESQL

Same syntax as READ:MYSQL:

```
EXPECT READ:POSTGRESQL <table_name>
[USING_SQL """
<SQL SELECT statement>
"""]
[USING_SQL_CONTAINS """
<sql-fragment>
"""]
RETURNS {{<response_file>}}
```

---

### EXPECT WRITE:POSTGRESQL

```
EXPECT WRITE:POSTGRESQL <table_name>
[USING_SQL """
<SQL INSERT/UPDATE/DELETE statement>
"""]
[USING_SQL_CONTAINS """
<sql-fragment>
"""]
[WITH {{<input_payload>}}]
[RETURNS {{<write_result_file>}}]
[VERIFY query CONTAINS '<string>']
[VERIFY query NOT_CONTAINS '<string>']
[VERIFY query MATCHES /<regex>/]
```

When `RETURNS` is provided for a write operation, the payload controls the `affected_rows` value sent in the CommandComplete tag (e.g. `"UPDATE 3"`). Omitting RETURNS defaults to `affected_rows=1`.

```yaml
# write_result.yaml
affected_rows: 3
```

Note: `RETURNING` clauses in the SQL (PostgreSQL's row-returning syntax) are handled separately — the proxy returns a full result set for those, not a RETURNS payload.

---

### EXPECT READ:REDIS

```
EXPECT READ:REDIS <COMMAND> <key>
RETURNS {{<response_file>}}
```

Or for a cache miss / empty result:

```
EXPECT READ:REDIS <COMMAND> <key>
RETURNS EMPTY
```

Example:

```
EXPECT READ:REDIS GET auth:cache:${AUTH_TOKEN}
RETURNS {{payloads/cached_user.json}}

EXPECT READ:REDIS GET session:${SESSION_ID}
RETURNS EMPTY
```

Supported read commands: `GET`, `MGET`, `HGET`, `HGETALL`, `HMGET`, `LRANGE`, `LLEN`, `SMEMBERS`, `SISMEMBER`, `ZRANGE`, `ZRANGEBYSCORE`, `EXISTS`, `TTL`, `TYPE`, `KEYS`, `STRLEN`, `LINDEX`

Rules:

* `RETURNS` is required (either a file or `EMPTY`)
* `RETURNS EMPTY` encodes as a Redis nil bulk string (`$-1\r\n`) — the correct response for a missing key
* The interceptor speaks RESP2 and handles `PING`, `AUTH`, `SELECT`, `HELLO`, and `COMMAND` transparently without registry lookups

---

### EXPECT WRITE:REDIS

```
EXPECT WRITE:REDIS <COMMAND> <key>
[WITH {{<input_payload>}}]
[VERIFY command CONTAINS '<string>']
[VERIFY command NOT_CONTAINS '<string>']
[VERIFY command MATCHES /<regex>/]
[VERIFY key CONTAINS '<string>']
[VERIFY key NOT_CONTAINS '<string>']
[VERIFY key MATCHES /<regex>/]
[VERIFY value CONTAINS '<string>']
[VERIFY value NOT_CONTAINS '<string>']
[VERIFY value MATCHES /<regex>/]
```

Example:

```
EXPECT WRITE:REDIS SET session:abc
WITH {{payloads/session-data.json}}

EXPECT WRITE:REDIS DEL user:123
VERIFY command CONTAINS 'DEL'
VERIFY key CONTAINS 'user:'
```

Rules:

* `WITH` is optional; write commands without a payload return `+OK`
* `VERIFY` clauses can validate the command name, key, and/or the value argument independently
* Unmatched write commands pass through and return `+OK`

---

### EXPECT READ:MONGODB

```
EXPECT READ:MONGODB <collection>
RETURNS {{<response_file>}}
```

Or for empty results:

```
EXPECT READ:MONGODB <collection>
RETURNS EMPTY
```

Example:

```
EXPECT READ:MONGODB products
RETURNS {{payloads/products_list.json}}

EXPECT READ:MONGODB users
RETURNS EMPTY
```

Rules:

* `RETURNS` is required (either a file or `EMPTY`)
* The proxy intercepts at the MongoDB wire protocol level (OP_MSG) — no changes needed to the service under test
* Payload files may contain a single JSON object or a `{"rows": [...]}` array for multiple documents
* JSON `"id"` fields containing a 24-character hex string are automatically mapped to `_id: ObjectID`
* Unmatched queries are forwarded transparently to the upstream MongoDB container

---

### EXPECT WRITE:MONGODB

```
EXPECT WRITE:MONGODB <collection>
[WITH {{<input_payload>}}]
```

Example:

```
EXPECT WRITE:MONGODB products
WITH {{payloads/create_product_request.json}}
```

Rules:

* `WITH` is optional; all matched write operations return `{n: 1, ok: 1}` (MongoDB write acknowledgement)
* The interceptor matches by collection name and command type (insert, update, delete, etc.)
* Unmatched write commands are forwarded to the real upstream MongoDB

---

### EXPECT GRPC

```
EXPECT GRPC:<ServiceName>/<MethodName>
[WITH {{<request_payload>}}]
RETURNS {{<response_payload>}}
```

LineSpec intercepts outbound gRPC calls using an HTTP/2 proxy. The service under test must point its gRPC client at the proxy host — no code changes to the service are required.

Enable the proxy in `.linespec.yml`:

```yaml
infrastructure:
  grpc: true

dependencies:
- name: user-grpc-service
  type: grpc
  host: user-grpc-service.local
  port: 50051
```

Example:

```
EXPECT GRPC:users.UserService/GetUser
WITH {{payloads/get_user_grpc_request.yaml}}
RETURNS {{payloads/get_user_grpc_response.json}}
```

Rules:

* `ServiceName/MethodName` matches the gRPC route (e.g. `UserService/GetUser` or `users.UserService/GetUser`)
* `WITH` is optional; omit it to match any request body for that method
* `RETURNS` is required; the proxy returns it as the gRPC response
* Test fails if the expected gRPC call is not observed

#### Content-Type handling

The gRPC proxy echoes the request's `Content-Type` in its response:

* `application/grpc+json` (default) — payloads are JSON. The 5-byte gRPC length-prefixed frame contains a JSON body. This is the original mode and remains the default when no Content-Type is specified.
* `application/grpc` — payloads are binary protobuf. When a protobuf descriptor set is configured (see below), `RETURNS` payloads written as JSON are automatically converted to binary protobuf on the wire. Without a descriptor, the raw bytes from the payload file are sent as-is.

#### Upstream passthrough

When a `type: grpc` dependency specifies a `host` and `port`, the proxy forwards any **unmocked** gRPC calls to that upstream backend via HTTP/2 reverse proxy. This lets you mix mocked and real gRPC backends in a single test — methods you `EXPECT` are intercepted; all others are forwarded transparently.

When no upstream is configured (or `infrastructure.grpc: true` is used without gRPC dependencies), unmocked calls return `UNIMPLEMENTED` — preserving backward compatibility with the original pure-mock behavior.

#### Protobuf descriptor mocks

When the service under test uses native gRPC clients (not JSON), the proxy needs a compiled protobuf descriptor set (`.pb` file) to convert JSON `RETURNS` payloads into binary protobuf on the wire.

Configure the descriptor set in `.linespec.yml`:

```yaml
# Service-level default — applies to all gRPC dependencies
grpc_descriptor_set: proto/workflow.pb

dependencies:
- name: workflow-service
  type: grpc
  host: temporal
  port: 7233

# Per-dependency override — takes precedence over the service-level default
- name: user-grpc-service
  type: grpc
  host: user-grpc-service.local
  port: 50051
  grpc_descriptor_set: proto/user.pb
```

The descriptor set is a `FileDescriptorSet` compiled with `protoc`:

```bash
protoc --include_imports --descriptor_set_out=workflow.proto workflow.proto
```

Behavior:

* When a descriptor is loaded and the request `Content-Type` is `application/grpc`, the proxy converts JSON `RETURNS` payloads to binary protobuf using the descriptor's message definitions
* When no descriptor is configured, or when the request `Content-Type` is `application/grpc+json`, payloads are served as-is (JSON or raw bytes)
* The runner merges all descriptor sets (service-level + per-dependency) into a single `FileDescriptorSet` before passing it to the proxy container

---

### VERIFY (Validation Rules)

The `VERIFY` clause validates the actual query or command intercepted at runtime. It can be attached to MySQL, PostgreSQL, and Redis EXPECT statements.

Use cases include:
- Security: Ensuring passwords are hashed before storage
- Compliance: Verifying sensitive data is not logged in plain text
- Correctness: Confirming proper SQL structure or Redis key naming conventions
- Injection prevention: Validating query patterns match expected templates

**Targets by channel:**

| Channel | Valid VERIFY targets |
|---------|----------------------|
| MySQL / PostgreSQL | `query` |
| Redis | `command`, `key`, `value` |

Operators:

* `CONTAINS` — Value must include the specified string (substring match)
* `NOT_CONTAINS` — Value must NOT include the specified string
* `MATCHES` — Value must match the specified regex pattern (full Go regexp support)

**SQL VERIFY syntax:**

```
EXPECT <CHANNEL> <resource>
[USING_SQL """<SQL>"""]
[WITH {{<input_payload>}}]
VERIFY query CONTAINS '<string>'
VERIFY query NOT_CONTAINS '<string>'
VERIFY query MATCHES /<regex>/
```

**Redis VERIFY syntax:**

```
EXPECT WRITE:REDIS <COMMAND> <key>
VERIFY command CONTAINS '<string>'
VERIFY key CONTAINS '<string>'
VERIFY value CONTAINS '<string>'
```

**Best Practices:**

Use `MATCHES` with word boundaries (`\b`) for precise column name matching to avoid false positives with compound column names:

```
# GOOD: Uses word boundaries to match exact column name
VERIFY query MATCHES /\bpassword_digest\b/

# BAD: Would also match 'password_digest' in 'old_password_digest_column'
VERIFY query CONTAINS 'password_digest'
```

Use `NOT_CONTAINS` with backtick-wrapped column names to avoid matching compound names:

```
# GOOD: Checks for exact column reference
VERIFY query NOT_CONTAINS '`password`'

# BAD: Would fail on 'password_digest' because it contains 'password'
VERIFY query NOT_CONTAINS 'password'
```

Example — Password Hashing (Security):

```
TEST create-user-with-hashing
RECEIVE HTTP:POST /api/v1/users
WITH {{user_create_request.yaml}}

# Ensure password is hashed before storage
EXPECT WRITE:MYSQL users
WITH {{user_with_hashed_password.yaml}}
VERIFY query MATCHES /\bpassword_digest\b/
VERIFY query NOT_CONTAINS '`password`'

RESPOND HTTP:201
```

Example — Redis Key Convention:

```
TEST delete-user-clears-cache
RECEIVE HTTP:DELETE /api/v1/users/123

EXPECT WRITE:REDIS DEL user:123
VERIFY command CONTAINS 'DEL'
VERIFY key MATCHES /^user:\d+$/

RESPOND HTTP:204
```

Example — Query Structure Validation:

```
TEST create-order-audit
RECEIVE HTTP:POST /api/v1/orders
WITH {{order_request.yaml}}

# Ensure all inserts include created_at for audit trails
EXPECT WRITE:MYSQL orders
WITH {{order_data.yaml}}
VERIFY query MATCHES /\bcreated_at\b/
VERIFY query MATCHES /INSERT INTO orders \([^)]+\) VALUES \([^)]+\)/

RESPOND HTTP:201
```

Runtime Behavior:

* When the proxy matches an interaction to the mock, it checks all VERIFY rules
* If any rule fails, the test fails with a verification error
* The actual query or command is shown in the error message for debugging
* Verification happens at interception time in MySQL, PostgreSQL, and Redis proxies

---

### EXPECT EVENT / EXPECT MESSAGE

Both `EVENT` and `MESSAGE` are aliases for the same functionality:

```
EXPECT EVENT:<topic_name>
WITH {{<message_payload>}}

EXPECT MESSAGE:<topic_name>
WITH {{<message_payload>}}
```

Example:

```
EXPECT EVENT:todo-events
WITH {{todo_created_event.yaml}}

# Same as:
EXPECT MESSAGE:todo-events
WITH {{todo_created_event.yaml}}
```

Rules:

* Both `EVENT:` and `MESSAGE:` prefixes work identically
* WITH file should contain the message payload
* Currently, the Kafka proxy passes through to the real broker

---

## 3. EXPECT_NOT

Defines an external dependency interaction that must NOT occur during execution. Useful for testing query optimization and ensuring certain operations are avoided.

Syntax:

```
EXPECT_NOT <CHANNEL> <resource>
[USING_SQL """
<raw-sql-query>
"""]
[USING_SQL_CONTAINS """
<sql-fragment>
"""]
```

Supported channels:
- `READ_MYSQL <table>` — Assert that a SELECT query does NOT occur
- `WRITE_MYSQL <table>` — Assert that an INSERT/UPDATE/DELETE does NOT occur

Example — Testing Efficient Queries:

```
TEST efficient-user-lookup
RECEIVE HTTP:GET /api/v1/users/123

# Assert that we DON'T do a full table scan
EXPECT_NOT READ:MYSQL users
USING_SQL """
SELECT * FROM users
"""

# Should use indexed lookup instead
EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM users WHERE id = 123 LIMIT 1
"""
RETURNS {{user_response.yaml}}

RESPOND HTTP:200
WITH {{user_response.yaml}}
```

Rules:

* Exactly one of READ_MYSQL or WRITE_MYSQL
* USING_SQL is optional; if provided, matches that specific query
* If no USING_SQL, matches any read/write on the table
* Test fails if the forbidden operation is detected

---

## 4. RESPOND

Defines the final response of the System Under Test.

Syntax:

```
RESPOND HTTP:<numeric_status_code>
[WITH {{<response_body>}}]
[NOISE
  body.<field_name>
  body.<field_name>]
```

Example:

```
RESPOND HTTP:201
WITH {{saved_todo.yaml}}
NOISE
  body.id
  body.created_at
  body.updated_at
```

Rules:

* Exactly one RESPOND per file
* MUST be the final statement
* Status MUST be numeric (e.g., 200, 201, 400, 500)
* WITH is optional for responses without a body
* NOISE must appear after WITH if both are present

### NOISE (optional)

Syntax:

```
RESPOND HTTP:<status>
WITH {{response.yaml}}
NOISE
  body.<field_name>
  body.<field_name>
```

Rules:

- `NOISE` must appear after `RESPOND` (and after `WITH` if present)
- Each indented line names one field path to exclude from comparison
- Field paths use dot notation matching the JSON response body (e.g. `body.created_at`)
- `NOISE` is optional; omit it when no fields need filtering

---

# Enforcement Rules

The parser MUST enforce:

* Exactly one RECEIVE
* Exactly one RESPOND
* RESPOND must be last
* EXPECT/EXPECT_NOT cannot appear before RECEIVE
* WITH files must exist (if specified)
* RETURNS required for READ operations and HTTP expectations
* No duplicate step identifiers

Parsing MUST fail if rules are violated.

---

# Complete Example

```
TEST create_todo_success

RECEIVE HTTP:POST /api/v1/todos
WITH {{todo.yaml}}
HEADERS
  Authorization: Bearer token_abc123xyz

EXPECT HTTP:GET http://user-service.local/api/v1/users/auth
HEADERS
  Authorization: Bearer token_abc123xyz
RETURNS {{user_info.yaml}}

EXPECT WRITE:MYSQL todos
WITH {{todo_insert.yaml}}

EXPECT EVENT:todo-events
WITH {{todo_created_event.yaml}}

RESPOND HTTP:201
WITH {{saved_todo.yaml}}
NOISE
  body.id
  body.created_at
  body.updated_at
```

---

# Environment Variable Interpolation

LineSpec supports environment variable substitution using `${VAR_NAME}` syntax. This feature catches hardcoded secrets and ensures your application reads configuration from the environment.

## Syntax

```
${VAR_NAME}
```

**Variable Name Rules:**
- Must start with an uppercase letter (`A-Z`)
- Can contain uppercase letters, digits, and underscores (`A-Z0-9_`)
- Lowercase variables are treated as literal text (not interpolated)

**Valid:** `${API_TOKEN}`, `${DB_HOST_1}`, `${API_VERSION}`  
**Invalid (treated as literal):** `${api_token}` (lowercase), `${123_VAR}` (starts with digit), `${VAR-NAME}` (hyphen not allowed)

## Where It Works

Environment variables can be used in:

| Location | Example |
|----------|---------|
| **HTTP URLs** | `http://api.${DOMAIN}.com/users` |
| **HTTP Paths** | `/api/${API_VERSION}/todos` |
| **HTTP Headers** | `Authorization: Bearer ${AUTH_TOKEN}` |
| **SQL Queries** | `WHERE api_key = '${API_KEY}'` |
| **Payload Files** | JSON/YAML files loaded via `WITH {{file.yaml}}` |

## How It Works

### Resolution Order

1. **Check Environment:** If the variable is set in the environment, use that value
2. **Generate Random:** If not set, generate a random value at test runtime
3. **Inject into Container:** Generated values are automatically injected as environment variables into your test container

### Random Value Format

When a variable is not defined in the environment, LineSpec generates:

```
{lowercase_var_name}_{16_hex_chars}
```

Example: `api_token_a1b2c3d4e5f6g7h8`

This ensures:
- Your tests never accidentally match hardcoded secrets
- The application must read from environment variables to get the correct value
- Same variable used multiple times in a test gets the same generated value

## Use Cases

### Catching Hardcoded Secrets

```linespec
TEST authenticate-user
RECEIVE HTTP:POST /api/v1/auth
WITH {{auth_request.yaml}}
HEADERS
  Authorization: Bearer ${API_TOKEN}

EXPECT HTTP:GET http://auth-service.local/validate
HEADERS
  Authorization: Bearer ${API_TOKEN}
RETURNS {{auth_response.yaml}}

RESPOND HTTP:200
```

If your application has a hardcoded API token instead of reading from `API_TOKEN`, the test will fail because the generated random value won't match.

### Dynamic Configuration

```linespec
RECEIVE HTTP:GET /api/${API_VERSION}/users

EXPECT READ:MYSQL users
USING_SQL """SELECT * FROM users WHERE env = '${DEPLOY_ENV}'"""
RETURNS {{users.yaml}}
```

### Payload File Interpolation

Variables in payload files are also interpolated:

**auth_request.yaml:**
```yaml
api_key: ${API_KEY}
user_id: 123
```

**Response expectation:**
```yaml
# The actual API key value is substituted at test time
api_key: api_key_a1b2c3d4e5f6g7h8
status: active
```

## Limitations

- **No default values:** `${VAR:-default}` syntax is not supported
- **Strict naming:** Only uppercase with underscores
- **No nested interpolation:** Cannot do `${${VAR}}`
- **First-use defines:** The first resolution of a variable determines its value for the entire test

---

# Configuration Reference (`.linespec.yml`)

Every LineSpec test directory requires a `.linespec.yml` file that tells the runner how to build, start, and wire up your service and its dependencies.

Below is a fully annotated example covering all supported fields. Only `service` is required; all other sections are optional.

```yaml
# ─────────────────────────────────────────────
# Service Under Test
# ─────────────────────────────────────────────
service:
  name: notification-service       # Logical name used in container labels
  service_dir: notification-service # Directory containing the service source code
  type: web                        # web | worker | consumer
  framework: fastapi               # rails | fastapi | django | express | chi | custom
                                   # Known frameworks get sensible defaults for start
                                   # command, migration command, and warmup endpoint.
  port: 3002                       # Port the service listens on inside the container
  health_endpoint: /health         # Path polled to confirm the service is ready

  # Docker build / run
  docker_compose: docker-compose.yml  # Path to docker-compose file (relative to service_dir)
  build_context: .                    # Docker build context (relative to .linespec.yml)

  # Override the framework default start command.
  # Use ${PORT} to inject the configured port at runtime.
  start_command: uvicorn app.main:app --host 0.0.0.0 --port 3002

  # Override the framework default migration command (optional).
  migration_command: alembic upgrade head

  # Warmup — wait for the service to accept traffic before running tests.
  needs_warmup: true          # true | false (default: per-framework)
  warmup_endpoint: /health    # Path to poll (overrides framework default)
  warmup_delay_ms: 100        # Extra delay after health check passes (ms)

  # Environment variables injected into the service container at test time.
  environment:
    DATABASE_URL: postgresql+asyncpg://user:pass@db:5432/mydb
    REDIS_URL: redis://redis-proxy:6379
    KAFKA_BROKERS: kafka:29092
    USER_SERVICE_URL: http://user-service:3001/api/v1/users/auth

# ─────────────────────────────────────────────
# Database (omit if external_db: true)
# ─────────────────────────────────────────────
# Single-database form (backward compatible):
database:
  type: postgresql     # mysql | postgresql | mongodb
  image: postgres:16-alpine
  port: 5432
  container: db        # Service name in docker-compose
  init_script: init.sql  # SQL/JS file run on first startup to seed schema
  database: mydb
  username: myuser
  password: mypassword

  # For external databases (not managed by LineSpec):
  host: db.internal    # External host (used when external_db: true)

  # Set to false to disable the protocol-level proxy for this database.
  # Default: true when infrastructure.database is true.
  proxy: true

# Multi-database form — use `databases:` when a service talks to more than
# one database type at the same time (e.g. MySQL + MongoDB).
# Each entry gets its own real-DB container and proxy sidecar.
# The `name:` field is required; `host:` defaults to the entry's name.
databases:
  - name: mysql
    type: mysql
    image: mysql:8.4
    port: 3306
    database: myapp_development
    username: myuser
    password: mypassword
    proxy: true
    # Network aliases assigned automatically:
    #   proxy  → "mysql"       (app connects here)
    #   real   → "real-mysql"  (proxy forwards here)

  - name: mongo
    type: mongodb
    image: mongo:7
    port: 27017
    database: myapp_events
    username: myuser
    password: mypassword
    proxy: true
    # Network aliases: proxy → "mongo", real → "real-mongo"

# Environment variables injected per database when using `databases:`:
#
#   First database also receives legacy unprefixed names:
#     DB_HOST, DB_PORT, DB_USERNAME, DB_PASSWORD          (mysql)
#     DATABASE_URL                                         (postgresql)
#     MONGODB_URI                                          (mongodb)
#
#   Every database receives a name-prefixed variant:
#     <NAME>_DB_HOST, <NAME>_DB_PORT, ...                 (mysql)
#     <NAME>_DATABASE_URL                                  (postgresql)
#     <NAME>_MONGODB_URI                                   (mongodb)
#
# Example: databases: [{name: mysql, ...}, {name: mongo, ...}] injects
#   DB_HOST=mysql, MYSQL_DB_HOST=mysql, MONGO_MONGODB_URI=mongodb://...

# ─────────────────────────────────────────────
# Infrastructure Flags
# ─────────────────────────────────────────────
infrastructure:
  database: true    # Start and proxy a database container
  kafka: true       # Start a Kafka container for EVENT/MESSAGE expectations
  redis: true       # Start and proxy a Redis interceptor
  grpc: false       # Start a gRPC proxy sidecar
  external_db: false  # true = don't manage the DB container; connect to host above

  # Docker image used for protocol proxy sidecars.
  # Default: linespec:latest
  proxy_image: linespec:latest

# ─────────────────────────────────────────────
# Protobuf descriptor set (optional — gRPC)
# ─────────────────────────────────────────────
# Path to a compiled FileDescriptorSet (.pb) for JSON-to-protobuf conversion.
# When set, RETURNS payloads for gRPC mocks are converted from JSON to binary
# protobuf when the request Content-Type is application/grpc.
# Per-dependency grpc_descriptor_set overrides this value.
grpc_descriptor_set: proto/workflow.pb

# ─────────────────────────────────────────────
# External HTTP / gRPC / service dependencies
# ─────────────────────────────────────────────
dependencies:
- name: user-service
  type: http
  host: user-service.local # Hostname the SUT dials
  port: 3001
  proxy: true # Intercept calls to this host
  host_alias: user-svc # Optional DNS alias inside the test network
  headers: # Default headers added to all matched requests
    X-Internal-Token: secret

- name: workflow-service
  type: grpc
  host: temporal # gRPC upstream hostname (unmocked calls are forwarded here)
  port: 7233
  grpc_descriptor_set: proto/workflow.pb # Optional: overrides service-level default

# ─────────────────────────────────────────────
# Provenance (optional — enables git hooks)
# ─────────────────────────────────────────────
provenance:
  dir: provenance/
  enforcement: warn        # none | warn | strict
  commit_tag_required: true
  auto_affected_scope: true

  # Voyage AI embeddings for semantic search
  embedding:
    provider: voyage
    index_model: voyage-4-large
    query_model: voyage-4-lite
    api_key: "${VOYAGE_API_KEY}"
    similarity_threshold: 0.50
    index_on_complete: true

# ─────────────────────────────────────────────
# Container & Network Naming (optional)
# Template variables: {{ .ServiceName }}, {{ .SpecName }}, {{ .Type }}
# ─────────────────────────────────────────────
container_naming:
  database_container: linespec-shared-db
  network_alias: real-db
  kafka_container: linespec-shared-kafka
  proxy_container: proxy-{{ .Type }}-{{ .SpecName }}
  app_container: app-{{ .SpecName }}
  migrate_container: linespec-migrate-{{ .ServiceName }}
  network_name: linespec-shared-net
  project_mount_path: /app/project    # Where the spec directory is mounted
  registry_mount_path: /app/registry  # Where mock payloads are mounted

# ─────────────────────────────────────────────
# Dynamic Port Allocation (optional)
# ─────────────────────────────────────────────
ports:
  dynamic_ports: true       # Allocate random host ports (default: true)
  min_port: 20000           # Lower bound for random port range
  max_port: 30000           # Upper bound for random port range
  fixed_proxy_port: 0       # Set to a specific port to pin the verify sidecar (0 = dynamic)

# ─────────────────────────────────────────────
# Schema Discovery (optional — MySQL/PostgreSQL)
# ─────────────────────────────────────────────
schema_discovery:
  mode: auto            # auto | static | none
  tables:               # Used when mode: static
    - users
    - orders
  exclude_tables:       # Tables to ignore in auto mode
    - schema_migrations
    - ar_internal_metadata
  cache_file: .linespec/schema-cache.json

# ─────────────────────────────────────────────
# Payload Loading (optional)
# ─────────────────────────────────────────────
payload:
  directory: payloads       # Subdirectory name for payload files (default: payloads)
  status_field: status      # JSON field path used to extract HTTP status from payload files

# ─────────────────────────────────────────────
# Misc
# ─────────────────────────────────────────────
timeout_seconds: 60     # Per-test timeout (default: 30)
strict_passthrough: false  # true = fail on any unmatched proxy interaction
```

## Framework defaults

When `framework` is set to a known value, LineSpec supplies defaults that you can selectively override:

| Framework | Default start command | Default migration command | Warmup endpoint |
|-----------|----------------------|--------------------------|-----------------|
| `rails` | `bundle exec rails server -b 0.0.0.0 -p ${PORT}` | `bundle exec rails db:migrate` | `/up` |
| `fastapi` | `python -m uvicorn main:app --host 0.0.0.0 --port ${PORT}` | — | `/health` |
| `django` | `python manage.py runserver 0.0.0.0:${PORT}` | `python manage.py migrate` | `/health` |
| `express` | `npm start` | — | `/health` |
| `chi` | `PORT=${PORT} go run .` | — | `/health` |
| `custom` | (required — must set `start_command`) | — | `/` |

## Minimal example

A minimal config for a FastAPI service with a PostgreSQL database:

```yaml
service:
  name: my-service
  framework: fastapi
  port: 8000

database:
  type: postgresql
  image: postgres:16-alpine
  port: 5432
  container: db
  database: mydb
  username: myuser
  password: mypassword

infrastructure:
  database: true
```

---

# CLI Usage

Execute a spec:

```
linespec test create_todo_success.linespec
linespec test /path/to/linespecs/
```

---

# Future Extensions (Planned)

* MATCH and IGNORE rules for fuzzy matching
* JSON Schema validation
* Snapshot diffing
* Spec linting mode
* Multi-test suites
* Template interpolation ({{variable}} support)

---

# Philosophy

LineSpec is not a natural language tool.
It is a strict behavioral specification language designed to:

* Be readable by humans
* Be trivial to parse
* Execute deterministically
* Support modern microservice testing workflows

No inference. No heuristics. No ambiguity.
