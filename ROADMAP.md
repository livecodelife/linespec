# LineSpec Roadmap

This document outlines the best next steps and improvements for LineSpec, organized by priority and impact.

## Priority Levels
- **P0 (Critical)** - Core functionality gaps, blocking issues
- **P1 (High)** - Important features that significantly improve DX
- **P2 (Medium)** - Nice-to-have improvements
- **P3 (Low)** - Future enhancements, exploration

---

## P0: Critical Gaps

### 1. PostgreSQL Read Support
**Status:** WRITE_POSTGRESQL exists, READ_POSTGRESQL missing

Currently only `WRITE_POSTGRESQL` is supported. Add `READ_POSTGRESQL` channel with:
- Same `USING_SQL` and auto-generated SQL support as MySQL
- Proper mock generation for PostgreSQL wire protocol
- Connection handshake mocks for PostgreSQL

**Implementation notes:**
- Extend `ExpectReadPostgresqlStatement` in types.ts
- Add parser support in lexer.ts (new token)
- Add compiler logic in compiler.ts (similar to READ_MYSQL)
- Add proxy support for PostgreSQL protocol in mysql-proxy.ts (rename to db-proxy.ts)

### 2. HTTP Expectations Completion ✅
**Status:** DONE - HTTP expectations fully tested with examples

Tests added for:
- HTTP mock generation for external service calls
- Request body inclusion from WITH payloads
- Response body and status code extraction from RETURNS payloads
- Proper mock naming convention (`{test-name}-mock-{index}`)
- Mixed scenarios (HTTP + MySQL expectations in same test)
- Tests without HTTP expectations (no HTTP mocks generated)

**Examples available in:**
- `examples/todo-linespecs/` - All 5 todo operations with auth HTTP expectation
- `examples/user-linespecs/` - 4 out of 5 operations use HTTP auth

**Test file:** `tests/http-expectations.test.ts`

### 3. Event/Messaging Channel Validation
**Status:** EVENT channel exists but untested

Kafka/Event expectations are defined but:
- No test coverage
- No examples
- Unclear if format matches Keploy's Kafka mock format

**Action items:**
- Add working example with Kafka/ messaging
- Verify mock format against Keploy spec
- Add integration test

---

## P1: High Impact Improvements

### 4. IDE/Editor Support
**Impact:** Huge DX improvement for users

#### 4a. VS Code Extension
- Syntax highlighting for `.linespec` files
- Auto-completion for:
  - Channel types (HTTP, READ_MYSQL, WRITE_MYSQL, etc.)
  - Statement keywords
  - Payload file paths (relative to current file)
- Error squiggles for syntax errors
- Go-to-definition for payload references (`{{path}}`)
- Snippets for common test patterns

#### 4b. LSP Server
- Real-time validation
- Hover information for keywords
- Type checking for payload files

**Effort:** Medium (2-3 days for basic extension)

### 5. Better Error Messages
**Current state:** Basic line numbers, some context

**Improvements:**
- Show surrounding context lines (like Rust errors)
- Suggest fixes ("Did you mean WRITE_MYSQL?")
- Colorized output in terminal
- JSON error format for CI/CD integration
- Validation errors for:
  - Missing payload files
  - Invalid SQL syntax in USING_SQL
  - Payload structure mismatches

**Example improved error:**
```
error[linespec::invalid_channel]: unrecognized channel type
  ┌─ examples/test-set-0/test-1.linespec:6:8
  │
6 │ EXPECT READ_MSSQL users
  │        ^^^^^^^^^^
  │        
  │        Did you mean READ_MYSQL or READ_POSTGRESQL?
  │
  = Available channels: HTTP, READ_MYSQL, WRITE_MYSQL, READ_POSTGRESQL, WRITE_POSTGRESQL, EVENT
```

### 6. Payload Schema Validation
**Problem:** Payload files aren't validated against expected structure

**Solution:**
Add `EXPECTING` clause for response payload schema validation:

```linespec
TEST create-user
RECEIVE HTTP:POST http://localhost:3000/users
WITH {{payloads/user_create_req.yaml}}

EXPECT WRITE:MYSQL users
WITH {{payloads/user_create_req.yaml}}

RESPOND HTTP:201
WITH {{payloads/user_create_resp.yaml}}
EXPECTING
  body.id: number
  body.name: string
  body.email: string
  body.created_at: datetime
NOISE
  body.id
  body.created_at
```

**Benefits:**
- Catch payload mismatches at compile time
- Self-documenting expected response structure
- Enables better IDE autocomplete

### 7. SQL Syntax Highlighting & Validation
**Current:** Raw SQL in USING_SQL is just a string

**Improvements:**
- Syntax highlighting for SQL blocks in editors
- Basic SQL validation (parse check)
- Warn on SQL injection risks (string concatenation patterns)
- Auto-format SQL on compile

### 8. Watch Mode for Development
**Feature:** `linespec compile --watch`

**Behavior:**
- Watch `.linespec` files and payload YAML files
- Recompile on change
- Show compilation status in terminal
- Integration with test runner for auto-test-on-change

---

## P2: Medium Impact

### 9. Advanced SQL Generation
**Current:** Basic INSERT/UPDATE/DELETE generation

**Missing:**
- JOIN queries for reads
- WHERE clause generation
- ORDER BY / LIMIT support
- Aggregations (COUNT, SUM, etc.)
- Batch operations

**Proposal - Extended syntax:**
```linespec
EXPECT READ:MYSQL users
USING_SQL """
SELECT u.*, COUNT(t.id) as todo_count
FROM users u
LEFT JOIN todos t ON t.user_id = u.id
WHERE u.active = 1
GROUP BY u.id
ORDER BY u.created_at DESC
LIMIT 10
"""
RETURNS {{payloads/users_with_counts.yaml}}
```

### 10. Test Organization & Tagging
**Feature:** Tags and organization

```linespec
TEST create-user
TAGS auth, users, critical
RECEIVE HTTP:POST http://localhost:3000/users
...
```

**CLI additions:**
```bash
linespec test --tag critical    # Run only critical tests
linespec test --exclude-tag wip  # Skip WIP tests
linespec list --tag users        # List all user tests
```

### 11. Test Dependencies & Ordering
**Feature:** Test dependencies

```linespec
TEST delete-user
DEPENDS_ON create-user  # This test creates user#3
RECEIVE HTTP:DELETE http://localhost:3000/users/3
...
```

**Benefits:**
- Run tests in correct order
- Parallelize independent tests
- Better test organization

### 12. Data Fixtures & Seeding
**Feature:** Pre-test data setup

```linespec
TEST get-user-with-todos
FIXTURES
  - users: {{fixtures/users.yaml}}
  - todos: {{fixtures/todos.yaml}}
RECEIVE HTTP:GET http://localhost:3000/users/1/todos
...
```

**Use case:** Tests that need existing data without depending on other tests

### 13. Enhanced Assertions
**Current:** Basic noise filtering

**Add:**
- `ASSERT body.field > 0` - Numeric comparisons
- `ASSERT body.status IN ['active', 'pending']` - Enum validation
- `ASSERT body.email MATCHES /^[^@]+@[^@]+$/` - Regex validation
- `ASSERT header.content-type CONTAINS 'json'` - Header checks
- `ASSERT response.time < 500ms` - Performance assertions

**Example:**
```linespec
RESPOND HTTP:200
WITH {{payloads/user.yaml}}
ASSERT
  body.id > 0
  body.email CONTAINS '@'
  header.content-type == 'application/json'
  response.time < 500
NOISE
  body.created_at
```

### 14. Conditional Expectations
**Feature:** Handle conditional logic

```linespec
TEST conditional-example
RECEIVE HTTP:POST http://localhost:3000/api/process
WITH {{payloads/request.yaml}}

EXPECT READ:MYSQL status_check
USING_SQL """
SELECT * FROM statuses WHERE id = {{request.status_id}}
"""
RETURNS {{payloads/status.yaml}}

# Only if status is 'pending'
EXPECT WRITE:MYSQL statuses
WHEN status == 'pending'
WITH {{payloads/status_update.yaml}}

RESPOND HTTP:200
WITH {{payloads/response.yaml}}
```

**Use case:** Different code paths based on data state

### 15. Request/Response Hooks
**Feature:** Pre/post processing

```linespec
TEST with-hooks
BEFORE
  # Generate dynamic data
  SET $email = "user_{{random}}@example.com"
  
RECEIVE HTTP:POST http://localhost:3000/users
WITH {{payloads/user_create.yaml}}
# Override payload field
OVERRIDE body.email = $email

EXPECT WRITE:MYSQL users
WITH {{payloads/user_create.yaml}}
VERIFY query CONTAINS $email

RESPOND HTTP:201

AFTER
  # Cleanup or validation
  EXECUTE http DELETE http://localhost:3000/users/{{response.body.id}}
```

---

## P3: Future Exploration

### 16. Additional Database Support
- MongoDB (document-based expectations)
- Redis (key-value expectations)
- Elasticsearch (search expectations)

### 17. GraphQL Support
```linespec
TEST graphql-query
RECEIVE GRAPHQL http://localhost:3000/graphql
QUERY """
query GetUser($id: ID!) {
  user(id: $id) {
    name
    email
  }
}
"""
VARIABLES {{payloads/vars.yaml}}

EXPECT READ:MYSQL users
RETURNS {{payloads/user.yaml}}

RESPOND HTTP:200
WITH {{payloads/graphql_response.yaml}}
```

### 18. gRPC Support
```linespec
TEST grpc-method
RECEIVE GRPC user.UserService/GetUser
PROTO ./proto/user.proto
WITH {{payloads/user_request.yaml}}

EXPECT READ:MYSQL users
RETURNS {{payloads/user.yaml}}

RESPOND GRPC
WITH {{payloads/user_response.yaml}}
```

### 19. OpenAPI/Swagger Integration
**Feature:** Generate LineSpec from OpenAPI spec

```bash
linespec generate-from-openapi ./api.yaml --out ./tests
```

**Benefits:**
- Auto-generate test skeletons from API spec
- Ensure tests cover all documented endpoints
- Keep tests in sync with API changes

### 20. Test Recording/Playback
**Feature:** Record real traffic and convert to LineSpec

```bash
linespec record --proxy localhost:8080 --out ./recorded-tests
```

Captures:
- HTTP requests/responses
- Database queries
- External service calls

Converts to `.linespec` files with appropriate expectations.

### 21. CI/CD Integrations
- GitHub Actions helper
- GitLab CI template
- JUnit XML output format
- Coverage reporting

### 22. Team Collaboration Features
- Test organization (folders/tags)
- Test ownership (`OWNER team@company.com`)
- Review workflow integration
- Shared fixture libraries

---

## Implementation Priorities

### Phase 1: Foundation (Next 2-4 weeks)
1. ✅ Fix any critical bugs
2. Add PostgreSQL read support (P0)
3. ✅ Add HTTP expectation tests (P0)
4. Better error messages (P1)
5. VS Code extension MVP (P1)

### Phase 2: Developer Experience (Month 2-3)
6. Watch mode
7. SQL validation
8. Payload schema validation
9. Test tagging
10. Documentation improvements

### Phase 3: Advanced Features (Month 3+)
11. Test dependencies
12. Data fixtures
13. Enhanced assertions
14. Conditional expectations
15. Request/response hooks

### Phase 4: Ecosystem (Month 6+)
16. GraphQL support
17. OpenAPI integration
18. Recording/playback
19. CI/CD integrations
20. Additional databases

---

## Contributing

Priority items welcome contributions:

1. **Good first issues:**
   - Better error messages (P1 #5)
   - Additional test examples (P0 #2, #3)
   - Documentation improvements

2. **Medium complexity:**
   - PostgreSQL read support (P0 #1)
   - Watch mode (P1 #8)
   - Test tagging (P2 #10)

3. **Advanced:**
   - VS Code extension (P1 #4)
   - LSP server (P1 #4)
   - SQL validation (P1 #7)

See [CONTRIBUTING.md](./CONTRIBUTING.md) for guidelines.

---

## Changelog

### Current Version (0.1.0)
- ✅ Core DSL syntax
- ✅ MySQL read/write support
- ✅ PostgreSQL write support
- ✅ HTTP request/response
- ✅ HTTP expectations (external service mocking)
- ✅ Auto-transaction wrapping
- ✅ SQL generation from payloads
- ✅ SQL verification (VERIFY clause)
- ✅ Test runner with Docker Compose
- ✅ MySQL proxy for test isolation
- ✅ Noise filtering for dynamic fields
- ✅ HTTP expectations with full test coverage

---

*Last updated: 2026-02-25*
*HTTP expectation tests completed*
*Maintainers: See [AGENTS.md](./AGENTS.md) for contact info*
