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
WITH {{<body_file>}}
```

Example:

```
RECEIVE HTTP:POST /api/v1/todos
WITH {{todo.yaml}}
```

Rules:

* Exactly one RECEIVE per file
* MUST appear before any EXPECT
* HTTP method is required
* Path is required
* WITH is required for HTTP requests with a body
* Body must reference an external YAML or JSON file

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

```
EXPECT WRITE:MYSQL <table_name>
WITH {{<input_payload>}}
RETURNS {{<db_result>}}
```

Optional explicit SQL:

```
EXPECT WRITE:MYSQL <table_name>
USING_SQL """
<SQL statement>
"""
WITH {{<input_payload>}}
RETURNS {{<db_result>}}
```

Compiled To:

* KMock of kind: MySQL

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
* Mirror Keploy’s runtime model

No inference. No heuristics. No ambiguity.
