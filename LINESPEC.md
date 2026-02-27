# LineSpec CLI

LineSpec is a deterministic domain-specific language (DSL) and CLI tool for generating Keploy-compatible KTests and KMocks from human-readable service behavior specifications.

The goal of LineSpec is to:

* Provide a concise, readable way to describe service behavior
* Enforce strict structural rules to keep parsing simple
* Deterministically compile to Keploy KTest and KMock YAML artifacts
* Avoid inference, heuristics, or natural language ambiguity

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
3. Exactly one RESPOND statement

Statements MUST appear in this order:

```
RECEIVE
EXPECT (0..n)
RESPOND
```

No statements may appear after RESPOND.

---

# Top-Level Structure

Optional test name declaration:

```
TEST <test_name>
```

If omitted, the filename is used as the test name.

---

# Statement Definitions

## 1. RECEIVE

Defines the trigger request into the System Under Test (SUT).

Syntax:

```
RECEIVE HTTP:<METHOD> <PATH>
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
* MUST appear before any EXPECT
* HTTP method is required
* Path is required
* WITH is required for HTTP requests with a body
* Body must reference an external YAML or JSON file
* HEADERS is optional and supports multiple header lines with indentation
* Headers are added to the HTTP request (Authorization, X-Custom-Header, etc.)
* WITH must come before HEADERS if both are present

Compiled To:

* KTest.spec.req

---

## 2. EXPECT

Defines an external dependency interaction that MUST occur during execution.

Each EXPECT becomes a separate KMock artifact within a mocks.yaml file.

General Syntax:

```
EXPECT <CHANNEL>
WITH {{<request_file>}}
RETURNS {{<response_file>}}
```

The exact format depends on the channel type.

---

### EXPECT HTTP

```
EXPECT HTTP:<METHOD> <URL>
WITH {{<request_body>}}
RETURNS {{<response_body>}}
```

Example:

```
EXPECT HTTP:GET http://user-service.local/users/42
WITH {{user_request.yaml}}
RETURNS {{user_info.yaml}}
```

Compiled To:

* KMock of kind: Http

---

### EXPECT WRITE:MYSQL

Auto-detect operation type from payload:

```
EXPECT WRITE:MYSQL <table_name>
WITH {{<input_payload>}}
```

Explicit operation type:

```
EXPECT WRITE:MYSQL INSERT <table_name>
WITH {{<input_payload>}}

EXPECT WRITE:MYSQL UPDATE <table_name>
WITH {{<input_payload>}}

EXPECT WRITE:MYSQL DELETE <table_name>
WITH {{<input_payload>}}
```

Optional explicit SQL:

```
EXPECT WRITE:MYSQL <table_name>
USING_SQL """
<SQL statement>
"""
WITH {{<input_payload>}}
```

Compiled To:

* KMock of kind: MySQL

**Auto-Detection Rules (INSERT vs UPDATE only):**
* INSERT: Generated when payload has no `id` field OR only has `id` field with no other fields
* UPDATE: Generated when payload has `id` field AND other fields to update
* DELETE: Never auto-detected - must specify explicitly

**Explicit Operations:**
* **INSERT**: Add new records. WITH file provides field values.
* **UPDATE**: Modify existing records. WITH file must include `id` field AND fields to update.
* **DELETE**: Remove records. WITH file must include `id` field to identify which record.

**Requirements:**
* DELETE always requires a WITH file with an `id` field
* Without explicit operation AND without WITH file: compiler error
* Without explicit operation but WITH file present: auto-detects INSERT or UPDATE only

---

### VERIFY (SQL Validation)

The `VERIFY` clause validates the actual SQL query executed by the application at runtime. It can be attached to any MySQL EXPECT statement to enforce query structure, security policies, or correctness constraints.

Use cases include:
- Security: Ensuring passwords are hashed before storage
- Compliance: Verifying sensitive data is not logged in plain text
- Correctness: Confirming proper SQL structure (e.g., required columns, proper table names)
- Injection prevention: Validating query patterns match expected templates

Syntax:

```
EXPECT <CHANNEL> <resource>
[USING_SQL """<SQL>"""]
WITH {{<input_payload>}}
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

Compiled To:

* KMock with `spec.metadata.verify` array containing validation rules

Runtime Behavior:

* When the proxy matches a query to the mock, it checks all VERIFY rules
* If any rule fails, the test fails with 🔒 SQL Verification Error
* The actual query is shown in the error message for debugging

---

### EXPECT READ:MYSQL with Empty Results

For queries that return no rows (e.g., "not found" scenarios), use `RETURNS EMPTY` instead of a payload file:

```
EXPECT READ:MYSQL <table_name>
USING_SQL """
<SQL statement that returns no rows>
"""
RETURNS EMPTY
```

The compiler automatically generates proper MySQL TextResultSet column definitions based on other expectations in the same test, or uses sensible defaults for common Rails conventions.

**Example — User Not Found:**

```
TEST get-user-not-found
RECEIVE HTTP:GET /api/v1/users/999
HEADERS
  Authorization: Bearer token_abc123xyz

# First query finds the authenticated user
EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM `users` WHERE `users`.`token` = 'token_abc123xyz' LIMIT 1
"""
RETURNS {{user_response.yaml}}

# Second query finds no user with id=999
EXPECT READ:MYSQL users
USING_SQL """
SELECT * FROM `users` WHERE `users`.`id` = 999 LIMIT 1
"""
RETURNS EMPTY

RESPOND HTTP:404
WITH {{user_not_found_error.yaml}}
```

**Benefits:**
- No need to write complex MySQL protocol payload files
- Column definitions are inferred automatically
- Human-readable and maintainable

---

### EXPECT EVENT

```
EXPECT EVENT:<topic_name>
WITH {{<message_payload>}}
```

Compiled To:

* KMock of kind: Kafka (or configured event broker)

---

## 3. RESPOND

Defines the final response of the System Under Test.

Syntax:

```
RESPOND HTTP:<numeric_status_code>
WITH {{<response_body>}}
```

Example:

```
RESPOND HTTP:201
WITH {{saved_todo.yaml}}
```

Rules:

* Exactly one RESPOND per file
* MUST be the final statement
* Status MUST be numeric (e.g., 200, 201, 400, 500)
* WITH is required for JSON responses

Compiled To:

* KTest.spec.resp

RESPOND does NOT generate a KMock.

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

Compiled To:

- `KTest.spec.assertions.noise` — each field becomes a key with an empty array value

---

# Template Referencing

LineSpec supports deterministic template interpolation using double braces.

Example:

```
EXPECT HTTP:GET http://user-service.local/users/{{trigger.body.owner_id}}
```

Available context objects:

* trigger.body
* steps.<step_id>.returns

Step IDs may be auto-generated or explicitly defined in future versions.

---

# Enforcement Rules

The CLI MUST enforce:

* Exactly one RECEIVE
* Exactly one RESPOND
* RESPOND must be last
* EXPECT cannot appear before RECEIVE
* WITH files must exist
* RETURNS required for HTTP and WRITE
* No duplicate step identifiers

Compilation MUST fail if rules are violated.

---

# Complete Example

```
TEST create_todo_success

RECEIVE HTTP:POST /api/v1/todos
WITH {{todo.yaml}}

EXPECT HTTP:GET http://user-service.local/users/42
WITH {{user_request.yaml}}
RETURNS {{user_info.yaml}}

EXPECT WRITE:POSTGRESQL todos
WITH {{todo_insert.yaml}}
RETURNS {{saved_todo.yaml}}

EXPECT MESSAGE:user_notification_topic
WITH {{create_todo_message.yaml}}

RESPOND HTTP:201
WITH {{saved_todo.yaml}}
NOISE
  body.created_at
  body.updated_at
```

---

# CLI Usage

Compile a spec:

```
linespec compile create_todo_success.linespec
```

Output structure:

```
out/
  tests/
    create_todo_success.yaml
  mocks/
    create_todo_success__step1.yaml
    create_todo_success__step2.yaml
    create_todo_success__step3.yaml
```

---

# Future Extensions (Planned)

* MATCH and IGNORE rules
* gRPC support
* JSON Schema validation
* Snapshot diffing
* Spec linting mode
* Multi-test suites

---

# Philosophy

LineSpec is not a natural language tool.
It is a strict behavioral specification language designed to:

* Be readable by humans
* Be trivial to parse
* Compile deterministically
* Mirror Keploy's runtime model

No inference. No heuristics. No ambiguity.
