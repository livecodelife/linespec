# LineSpec DSL Reference

> 🚧 **Beta Feature** - LineSpec Testing is in active development. Build with `-tags beta` to enable these features.
> 
> **Installation:**
> ```bash
> go build -tags beta -o linespec ./cmd/linespec
> # or
> go install -tags beta github.com/livecodelife/linespec/cmd/linespec@v1.1.0
> ```
> 
> See [PROVENANCE_RECORDS.md](./PROVENANCE_RECORDS.md) for the stable Provenance Records feature.

LineSpec is a deterministic domain-specific language (DSL) for describing service behavior and defining integration tests that execute directly against containerized services.

The goal of LineSpec is to:

* Provide a concise, readable way to describe service behavior
* Enforce strict structural rules to keep parsing simple
* Execute deterministically without inference or heuristics
* Support database mocking, HTTP interception, and message queue testing

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
[WITH {{<request_file>}}]
[RETURNS {{<response_file>}}]
[RETURNS EMPTY]
[VERIFY query CONTAINS '<string>']
[VERIFY query NOT_CONTAINS '<string>']
[VERIFY query MATCHES /<regex>/]
```

The exact format depends on the channel type.

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
RETURNS {{<response_file>}}
```

Or for empty results:

```
EXPECT READ:MYSQL <table_name>
[USING_SQL """
<SQL SELECT statement that returns no rows>
"""]
RETURNS EMPTY
```

Example:

```
EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM users WHERE id = 42
"""
RETURNS {{user_response.yaml}}
```

Rules:

* RETURNS is required (either a file or EMPTY)
* USING_SQL is optional; if omitted, the proxy matches by table name
* The proxy matches SELECT queries by table name in the FROM clause
* RETURNS EMPTY generates proper MySQL protocol response for zero rows

---

### EXPECT WRITE:MYSQL

```
EXPECT WRITE:MYSQL <table_name>
[USING_SQL """
<SQL INSERT/UPDATE/DELETE statement>
"""]
[WITH {{<input_payload>}}]
[NO TRANSACTION]
[VERIFY query CONTAINS '<string>']
[VERIFY query NOT_CONTAINS '<string>']
[VERIFY query MATCHES /<regex>/]
```

Example:

```
EXPECT WRITE:MYSQL users
WITH {{user_create.yaml}}
VERIFY query CONTAINS 'password_digest'
```

Rules:

* WITH is optional for write operations
* USING_SQL is optional; if omitted, the proxy matches by table name and operation type
* NO TRANSACTION is parsed but has no effect (transactions always pass through)
* VERIFY clauses validate the actual SQL executed at runtime
* The proxy sends a generic OK response for matched write operations

---

### EXPECT READ:POSTGRESQL

Same syntax as READ:MYSQL:

```
EXPECT READ:POSTGRESQL <table_name>
[USING_SQL """
<SQL SELECT statement>
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
[WITH {{<input_payload>}}]
[VERIFY query CONTAINS '<string>']
[VERIFY query NOT_CONTAINS '<string>']
[VERIFY query MATCHES /<regex>/]
```

---

### VERIFY (SQL Validation)

The `VERIFY` clause validates the actual SQL query executed by the application at runtime. It can be attached to any MySQL or PostgreSQL EXPECT statement.

Use cases include:
- Security: Ensuring passwords are hashed before storage
- Compliance: Verifying sensitive data is not logged in plain text
- Correctness: Confirming proper SQL structure
- Injection prevention: Validating query patterns match expected templates

Syntax:

```
EXPECT <CHANNEL> <resource>
[USING_SQL """<SQL>"""]
[WITH {{<input_payload>}}]
VERIFY query CONTAINS '<string>'
VERIFY query NOT_CONTAINS '<string>'
VERIFY query MATCHES /<regex>/
```

Operators:

* `CONTAINS` — Query must include the specified string
* `NOT_CONTAINS` — Query must NOT include the specified string
* `MATCHES` — Query must match the specified regex pattern

Example — Password Hashing (Security):

```
TEST create-user-with-hashing
RECEIVE HTTP:POST /api/v1/users
WITH {{user_create_request.yaml}}

# Ensure password is hashed before storage
EXPECT WRITE:MYSQL users
WITH {{user_with_hashed_password.yaml}}
VERIFY query CONTAINS 'password_digest'
VERIFY query NOT_CONTAINS 'password'

RESPOND HTTP:201
```

Example — Query Structure Validation:

```
TEST create-order-audit
RECEIVE HTTP:POST /api/v1/orders
WITH {{order_request.yaml}}

# Ensure all inserts include created_at for audit trails
EXPECT WRITE:MYSQL orders
WITH {{order_data.yaml}}
VERIFY query CONTAINS 'created_at'
VERIFY query MATCHES /INSERT INTO orders \([^)]+\) VALUES \([^)]+\)/

RESPOND HTTP:201
```

Runtime Behavior:

* When the proxy matches a query to the mock, it checks all VERIFY rules
* If any rule fails, the test fails with 🔒 SQL Verification Error
* The actual query is shown in the error message for debugging

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

# CLI Usage

Execute a spec:

```
linespec test create_todo_success.linespec
linespec test /path/to/linespecs/
```

---

# Future Extensions (Planned)

* MATCH and IGNORE rules for fuzzy matching
* gRPC support
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
