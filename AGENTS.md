# Agent Guidelines for LineSpec

This file provides guidance for agents working on the LineSpec codebase.

## For AI Agents Helping End Users

When a user has installed LineSpec globally via npm and asks you to help write LineSpec files:

1. **Access documentation via CLI:**
   ```bash
   linespec docs              # Shows all documentation paths
   linespec docs --linespec   # Path to LINESPEC.md (language reference)
   linespec docs --agents     # Path to this file
   linespec docs --readme     # Path to README.md
   ```

2. **Read files in this order:**
   - First: `AGENTS.md` (this file) - for project context
   - Second: `LINESPEC.md` - for complete syntax reference
   - Third: `README.md` - for usage examples

3. **Documentation location:** After installation, docs are in:
   - `<npm_global_prefix>/lib/node_modules/linespec/docs/`
   - Or use the `linespec docs` command to get the exact path

---

## Project Overview

LineSpec is a DSL compiler for generating Keploy-compatible KTests and KMocks from human-readable service behavior specifications. It's a TypeScript CLI tool designed for TDD workflows.

---

## Build, Lint, and Test Commands

### Installation
```bash
npm install
```

### Build
```bash
npm run build
```
Compiles TypeScript to JavaScript using `tsc`. Output goes to `./dist`.

### Development
```bash
npm run dev
```
Runs the CLI directly using ts-node on `src/cli.ts`.

### Test
```bash
npm run test
```
Runs all tests using `vitest run`.

### Run a Single Test
```bash
vitest run --testNamePattern "test-1"
```
Or use vitest's watch mode:
```bash
vitest
```
Then press `f` to filter tests interactively.

---

## Code Style Guidelines

### TypeScript Configuration
- Target: ES2020
- Module: CommonJS
- Strict mode is enabled (`strict: true`)
- No ESLint or Prettier configured

### Imports
- Use explicit named imports: `import { tokenize } from '../src/lexer'`
- Group external imports first, then internal
- Use `import * as fs from 'fs'` for Node.js built-ins
- Use `import * as yaml from 'js-yaml'` for external packages

### Naming Conventions
- **Files**: kebab-case (e.g., `lexer.ts`, `parser.ts`)
- **Types/Interfaces**: PascalCase (e.g., `TestSpec`, `ExpectStatement`)
- **Classes**: PascalCase (e.g., `LineSpecError`)
- **Functions/Variables**: camelCase (e.g., `tokenize`, `specFile`)
- **Constants**: camelCase (e.g., `EXAMPLES_DIR`)

### Type Definitions
- Use interfaces for object shapes
- Use type unions for variant types
- Export types from `src/types.ts`
- Example:
  ```typescript
  export interface ReceiveStatement {
    channel: 'HTTP';
    method: string;
    path: string;
    withFile?: string;
  }
  ```

### Error Handling
- Create custom error classes extending `Error`
- Include line numbers for parsing errors
- Example:
  ```typescript
  export class LineSpecError extends Error {
    line?: number;
    constructor(message: string, line?: number) {
      super(message);
      this.name = 'LineSpecError';
      this.line = line;
    }
  }
  ```

### Code Structure
- One export per file for utility functions
- Group related exports in a single file (e.g., `types.ts`)
- Separate concerns: lexer, parser, validator, compiler
- Tests go in `tests/` directory
- Test files should be named `*.test.ts`

### Formatting
- Use 2 spaces for indentation
- No trailing whitespace
- One blank line between imports and code
- Use semicolons
- No comments (per existing code style)

### Best Practices
- Always specify return types for functions
- Use `as` type assertions when types are guaranteed
- Validate inputs early and throw descriptive errors
- Use `Record<string, unknown>` for dynamic object types
- Handle file I/O with proper error messages
- Clean up temporary resources in tests (use `afterEach`)

---

## Project Structure

```
src/
  cli.ts          # CLI entry point (compile + test commands)
  lexer.ts        # Tokenizer for .linespec files
  parser.ts       # AST generation
  validator.ts    # Semantic validation
  compiler.ts     # YAML output generation (KTests + KMocks)
  types.ts        # TypeScript type definitions
  test-loader.ts  # Loads KTest YAML files and mocks.yaml from a test-set directory
  mysql-proxy.ts  # TCP proxy that intercepts MySQL packets and serves mock responses
  runner.ts       # Orchestrates Docker Compose, the proxy, HTTP test execution, and reporting
examples/
  test-set-0/          # .linespec input fixtures
    *.linespec
    payloads/          # YAML payload files for test data
keploy-examples/
  test-set-0/          # Pre-compiled KTest + mocks fixtures
    tests/
    mocks.yaml
tests/
  integration.test.ts  # Main test suite
```

---

## Key Design Patterns

### Pipeline Architecture
The compiler follows a pipeline: `source → tokenize → parse → validate → compile`

### DSL Syntax

LineSpec uses a declarative syntax to define service behavior:

```
TEST <name>
RECEIVE HTTP:<METHOD> <URL>
[WITH {{payloads/request.yaml}}]

EXPECT <CHANNEL> <resource>
[USING_SQL """
<raw-sql>
"""]
[WITH {{payloads/input.yaml}}]
[RETURNS {{payloads/output.yaml}}]
[VERIFY query CONTAINS 'string']
[VERIFY query NOT_CONTAINS 'string']
[VERIFY query MATCHES /regex/]

RESPOND HTTP:<STATUS_CODE>
[WITH {{payloads/response.yaml}}]
[NOISE
  body.<field>]
```

### MySQL Auto-Transaction System

**Automatic Transaction Wrapping:**
By default, `WRITE_MYSQL` expectations are automatically wrapped in `BEGIN...COMMIT` transaction mocks. This supports transactional ORMs like Rails ActiveRecord, Django, Hibernate, etc.

```yaml
# Input
EXPECT WRITE:MYSQL users
WITH {{payloads/user_create_req.yaml}}

# Generates:
# 1. BEGIN mock (auto-generated)
# 2. INSERT INTO users (...) mock (from WITH payload)
# 3. COMMIT mock (auto-generated)
```

**Disable with `NO TRANSACTION`:**
For non-transactional ORMs, add the `NO TRANSACTION` keyword:

```yaml
EXPECT WRITE:MYSQL users
NO TRANSACTION
WITH {{payloads/user_create_req.yaml}}
```

### SQL Generation from Payloads

The compiler automatically generates SQL from `WITH` payload files for MySQL expectations:

**For WRITE_MYSQL (default generates INSERT):**
```yaml
# Payload (payloads/user_create_req.yaml):
# name: John
# email: john@example.com
# password: secret123

# Generated SQL:
# INSERT INTO users (name, email, password) VALUES ('John', 'john@example.com', 'secret123')
```

**For READ_MYSQL (generates SELECT):**
```yaml
# Input without USING_SQL:
EXPECT READ:MYSQL users
RETURNS {{payloads/users_list.yaml}}

# Generated SQL:
# SELECT * FROM users
```

### Generic OK Responses for Writes

Write operations (`WRITE_MYSQL`, `WRITE_POSTGRESQL`) don't require `RETURNS`. The compiler auto-generates a generic OK response:

```yaml
# Response mock (auto-generated):
message:
  header: 0
  affected_rows: 1
  last_insert_id: 0
  status_flags: 2
  warnings: 0
  info: ''
```

This eliminates the need for write result payload files (`mysql_*_write_result.yaml`, etc.).

### Compiler — MySQL packet_type
The compiler sets the `packet_type` dynamically:
- `WRITE_MYSQL` statements → `packet_type: OK` (proxy routes to `encodeOkPayload`)
- `READ_MYSQL` statements → `packet_type: TextResultSet` (proxy routes to column/row serialisation)

### Test Runner Pipeline
`linespec test [dir]` follows the sequence:
1. `loadTestSet(dir)` — reads all `tests/*.yaml` and `mocks.yaml`
2. `startProxy(mocks, host, dbPort, proxyPort)` — starts TCP proxy on a free port
   - Infrastructure queries pass through to real DB
   - App-level queries matched against mocks
3. Docker Compose with `DATABASE_URL` override to route through proxy
4. `pollUntilHealthy(serviceUrl)` — waits for HTTP 200/404
5. Each `KTest` replayed via `http` module
6. Status/body compared (noise fields ignored)
7. Side-by-side diff for failures
8. Report written to `linespec-report/`
9. `docker compose down` and proxy teardown

### Docker Compose Testing Infrastructure

When running tests with `--compose`, LineSpec manages Docker containers automatically:

**Startup Sequence:**

1. **Clean existing containers** (`docker compose down -v`)
   - Ensures fresh database state by removing old containers and volumes
   - Prevents conflicts with previously running services

2. **Start database service** (`docker compose up -d db`)
   - Only the database container is started initially
   - Waits for database to be ready (TCP connection check)

3. **Build MySQL proxy container**
   - Creates a custom Docker image with the proxy server
   - Copies mocks.yaml and compiled code into the image
   - Proxy intercepts all database traffic between app and real DB

4. **Start proxy container** (`docker run -d linespec-proxy`)
   - Runs on the same Docker network as the compose services
   - Listens on standard MySQL port 3306
   - Routes queries to real DB or returns mock responses

5. **Generate compose override** (`.linespec-compose-override.yml`)
   - Rewrites `DATABASE_URL` to point to proxy container
   - Updates all DB host variables: `DB_HOST`, `DATABASE_HOST`, `MYSQL_HOST`, `POSTGRES_HOST`
   - Updates all DB port variables to 3306
   - Preserves other DB-related env vars (DB_NAME, credentials, etc.)

6. **Start web service with override**
   ```bash
   docker compose -f docker-compose.yml -f .linespec-compose-override.yml up -d web
   ```
   - Web service now connects to proxy instead of real database
   - Proxy determines which queries to mock vs. pass through

**Shutdown Sequence:**

1. **Remove web service** (`docker compose rm -fs web`)
   - Stops and removes only the web container
   - Keeps database running for next test run

2. **Clean up proxy**
   - Stops and removes the proxy container
   - Proxy server process is terminated

3. **Remove override file**
   - Deletes `.linespec-compose-override.yml`
   - Cleans up temporary files

**Example Override File:**
```yaml
version: "3.8"
services:
  web:
    environment:
      DATABASE_URL: mysql2://user:pass@linespec-proxy:3306/mydb
      DB_HOST: linespec-proxy
      DB_PORT: "3306"
```

**Proxy Query Routing:**
- Infrastructure queries (SET NAMES, COM_PING, SHOW, etc.) → Real database
- App-level queries (SELECT, INSERT, UPDATE, DELETE) → Mocks.yaml matching
- If no mock matches → Pass through to real database

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

### Statement Types
- `RECEIVE` - Trigger request (exactly one required, must be first)
- `EXPECT` - External dependencies (zero or more)
  - `HTTP` - External HTTP calls
  - `READ_MYSQL` - Database reads (requires `RETURNS`)
  - `WRITE_MYSQL` - Database writes (auto-transactional, optional `RETURNS`)
  - `WRITE_POSTGRESQL` - PostgreSQL writes
  - `EVENT` - Message queue events
  - `VERIFY` - SQL query validation (attached to EXPECT statements)
- `RESPOND` - Response (exactly one required, must be last)
- `NOISE` - Response noise filter (optional, follows RESPOND)
- `NO TRANSACTION` - Disable auto-transaction for WRITE_MYSQL

### Custom Errors
All parsing and validation errors extend `LineSpecError` with optional line numbers for error reporting.

---

## Testing Guidelines

- Use Vitest's `describe`/`it` syntax
- Use `beforeEach`/`afterEach` for setup/teardown
- Create temporary directories for test output
- Verify YAML output structure against expected schemas
- Test both success and failure cases

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
For queries returning no rows:
```yaml
columnCount: 1
columns:
  - name: one
    type: 8
    # ... column definition
rows: []
```

---

## Common Patterns

### Creating a Test for a POST Endpoint

```linespec
TEST create-resource
RECEIVE HTTP:POST http://localhost:3000/resources
WITH {{payloads/resource_create_req.yaml}}

# Auto-generates SQL from payload
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

# Requires RETURNS to verify data format
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
RETURNS {{payloads/mysql_empty_result.yaml}}

# The actual INSERT
EXPECT WRITE:MYSQL users
WITH {{payloads/user_create_req.yaml}}

RESPOND HTTP:201
```

### Verifying SQL Query Structure

Use VERIFY clauses to validate the actual SQL executed by the application at runtime. This enables security checks, compliance validation, and correctness enforcement.

**Common use cases:**
- **Security:** Ensure passwords are hashed before storage
- **Compliance:** Verify audit fields (created_at, updated_by) are included
- **Correctness:** Confirm proper table names and column sets
- **Injection prevention:** Validate query patterns match expected templates

```linespec
TEST create-user-secure
RECEIVE HTTP:POST http://localhost:3000/users
WITH {{payloads/user_create_req.yaml}}

# Example: Ensure password is hashed (no plain text 'password' in SQL)
EXPECT WRITE:MYSQL users
WITH {{payloads/user_with_hashed_password.yaml}}
VERIFY query CONTAINS 'password_digest'
VERIFY query NOT_CONTAINS 'password'

RESPOND HTTP:201
WITH {{payloads/user_create_resp.yaml}}
NOISE
  body.id
  body.created_at
  body.updated_at
```

Verification operators:
- `CONTAINS 'string'` — Query must include the string
- `NOT_CONTAINS 'string'` — Query must NOT include the string  
- `MATCHES /regex/` — Query must match the regex pattern

When verification fails, the test runner displays:
```
✗ test-1 FAIL

  🔒 SQL Verification Error:
    VERIFY FAILED: Query does not contain 'password_digest'.
    Actual query: INSERT INTO `users` (`name`, `email`, `password`, ...) ...

  Expected status : 201
  Actual status   : 500
```

### Non-Transactional ORM

For ORMs that don't wrap operations in transactions:

```linespec
TEST create-item
RECEIVE HTTP:POST http://localhost:3000/items
WITH {{payloads/item_create_req.yaml}}

EXPECT WRITE:MYSQL items
NO TRANSACTION
WITH {{payloads/item_create_req.yaml}}

RESPOND HTTP:201
```
