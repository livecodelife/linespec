# LineSpec Improvement Plan

This document outlines all identified bugs, hardcoded values, and coupling issues in the LineSpec codebase, organized by theme with implementation plans.

---

## Summary of Issues Found

**Total Issues:** 50+
**Estimated Sessions:** 8-10 provenance records
**Priority Order:** Critical → High → Medium → Low

---

## Session 1: Database Configuration & Hardcoded Names [CRITICAL]

### Issues:
1. **Hardcoded database name**: `todo_api_development` scattered across proxy.go, runner.go
2. **Hardcoded credentials**: `todo_user`/`todo_password` in MySQL proxy and runner
3. **Database schema name hardcoded** in MySQL proxy responses
4. **Init script naming convention** only supports `init.sql`
5. **Database environment variables** hardcoded in runner (DB_HOST=real-db, DB_PORT=3306)

### Files to Modify:
- `pkg/proxy/mysql/proxy.go` (lines 321, 383, 515)
- `pkg/proxy/postgresql/proxy.go` (lines 791-793, 840-841)
- `pkg/runner/runner.go` (lines 204-231, 391-398, 778-782)
- `pkg/config/types.go` (add new config fields)
- `pkg/config/parser.go` (add defaults parsing)

### Implementation Plan:
1. Extend `DatabaseConfig` struct with `Database`, `Username`, `Password`, `Host` fields
2. Make MySQL/PostgreSQL proxies read schema name from configuration
3. Update runner to use configured database name instead of hardcoded values
4. Support custom init script filenames beyond `init.sql`
5. Update all SQL queries to use parameterized schema names

### Dependencies:
- None - can start immediately

### Testing:
- Update proxy tests to use configurable database names
- Test with different database names to verify queries work correctly

---

## Session 2: Framework Abstraction & Startup Commands [CRITICAL]

### Issues:
1. **Rails-only migration support**: Only `bundle exec rails db:migrate` supported
2. **Hardcoded Rails start command**: `bundle exec rails server -b 0.0.0.0`
3. **Rails-specific PID file handling**: `rm -f tmp/pids/server.pid`
4. **Rails warmup logic**: Hardcoded Rails-specific warmup
5. **Limited framework defaults**: Only `rails` and `fastapi` have defaults
6. **Framework detection**: `Framework` field required but only 2 frameworks supported

### Files to Modify:
- `pkg/config/types.go` (lines 80-124)
- `pkg/runner/runner.go` (lines 246-257, 403, 838-842, 888-898)
- `pkg/config/parser.go` (applyDefaults function)

### Implementation Plan:
1. Create `FrameworkConfig` interface with methods:
   - `GetStartCommand(port string) []string`
   - `GetMigrationCommand() []string`
   - `NeedsWarmup() bool`
   - `GetHealthEndpoint() string`
2. Implement framework adapters for: Rails, FastAPI, Django, Express, Spring Boot, Gin, etc.
3. Allow custom frameworks via `.linespec.yml` with explicit start_command
4. Remove hardcoded Rails PID file cleanup from runner
5. Make warmup generic (configurable delay and endpoint)

### Dependencies:
- Session 1 (Database configuration) - for migration commands that need DB config

### Testing:
- Add test framework configs for different languages
- Test Django, Express, and generic frameworks

---

## Session 3: Container & Network Name Configuration [HIGH]

### Issues:
1. **Hardcoded container names**: `linespec-shared-db`, `linespec-shared-kafka`, `linespec-migrate-*`
2. **Hardcoded network name**: `linespec-shared-net`
3. **Hardcoded proxy container names**: `proxy-db-*`, `proxy-http-*`, `app-*`
4. **Hardcoded project mount path**: `/app/project`
5. **Hardcoded registry mount path**: `/app/registry`

### Files to Modify:
- `pkg/runner/runner.go` (lines 55, 213, 220, 279-305, 385, 518, 606, 724)
- `pkg/docker/orchestrator.go` (container management methods)

### Implementation Plan:
1. Add `ContainerNaming` struct to config with templates for:
   - Database container: `{{ .ServiceName }}-db`
   - Kafka container: `{{ .ServiceName }}-kafka`
   - Proxy container: `{{ .ServiceName }}-proxy-{{ .Type }}`
   - App container: `{{ .ServiceName }}-app`
   - Network: `{{ .ServiceName }}-net`
2. Make container name generation configurable via `.linespec.yml`
3. Support custom prefixes/namespaces for multi-tenant environments
4. Make mount paths configurable (not hardcoded `/app/project`)

### Dependencies:
- None

### Testing:
- Test with custom container name templates
- Verify multiple services can run in parallel with different names

---

## Session 4: Service Discovery & HTTP Proxy Decoupling [HIGH]

### Issues:
1. **Hardcoded `user-service.local`**: Alias hardcoded for HTTP proxy (3+ locations)
2. **Hardcoded service dependency handling**: Only checks for `user-service` name
3. **Hardcoded `USER_SERVICE_URL` env var**: With hardcoded path `/api/v1/users/auth`
4. **HTTP proxy always starts**: Even when not needed, for "backward compatibility"
5. **Hardcoded HTTP proxy port**: Always binds to port 80

### Files to Modify:
- `pkg/proxy/http/interceptor.go` (lines 91-95)
- `pkg/runner/runner.go` (lines 703-735, 815-827)
- `pkg/config/types.go` (DependencyConfig section)

### Implementation Plan:
1. Make HTTP proxy conditional - only start when service has HTTP dependencies
2. Add `HostAlias` field to `DependencyConfig` to specify custom hostnames
3. Remove hardcoded `user-service.local` - use configured dependency names
4. Remove hardcoded `USER_SERVICE_URL` generation
5. Make HTTP proxy port configurable (not always 80)
6. Support multiple HTTP service dependencies with different aliases

### Dependencies:
- Session 3 (Container naming) - to properly name proxy containers

### Testing:
- Test with custom service dependencies
- Verify multiple HTTP proxies can coexist

---

## Session 5: Protocol & Port Configuration [HIGH]

### Issues:
1. **Hardcoded MySQL port**: `3306/tcp` in multiple locations
2. **Hardcoded PostgreSQL port**: `5432` in defaults
3. **Hardcoded Kafka ports**: `9092/tcp`, `29092/tcp`, `29093` in runner
4. **Hardcoded proxy verification port**: `8081/tcp` for all proxies
5. **Port conflicts**: No detection or dynamic port allocation
6. **Host address hardcoding**: `localhost` and `0.0.0.0` used throughout

### Files to Modify:
- `pkg/runner/runner.go` (lines 209, 220, 300-301, 311, 392, 557, 742-750)
- `pkg/proxy/mysql/proxy.go` (proxy initialization)
- `pkg/proxy/postgresql/proxy.go` (proxy initialization)
- `pkg/proxy/http/interceptor.go` (port configuration)

### Implementation Plan:
1. Create `PortAllocator` service that finds available ports dynamically
2. Make all proxy ports dynamically allocated (not hardcoded 8081)
3. Add `PortRange` config to specify available port ranges
4. Store allocated ports in test metadata
5. Make database ports configurable per-service (not always 3306/5432)
6. Support external databases (don't bind to random port if using external)

### Dependencies:
- Session 3 (Container naming)

### Testing:
- Test port allocation with multiple parallel test runs
- Verify no port conflicts occur

---

## Session 6: Table & Schema Discovery Abstraction [MEDIUM]

### Issues:
1. **Hardcoded table names**: `users`, `todos`, `ar_internal_metadata`, `schema_migrations`
2. **Rails-specific tables**: `ar_internal_metadata` and `schema_migrations` assumed
3. **Schema cache hardcoded tables**: Runner fetches only known tables
4. **Table extraction logic**: Uses regex with hardcoded known tables
5. **PostgreSQL proxy**: Has its own hardcoded table lists

### Files to Modify:
- `pkg/proxy/mysql/proxy.go` (lines 445-466)
- `pkg/proxy/postgresql/proxy.go` (lines 791-841)
- `pkg/runner/runner.go` (lines 261-275)
- `pkg/config/types.go` (add schema discovery config)

### Implementation Plan:
1. Add `SchemaDiscovery` config section with:
   - `Mode`: `auto` (discover from DB), `static` (from config), `none`
   - `Tables`: explicit list when mode is `static`
   - `ExcludeTables`: tables to ignore in auto mode
2. Create `SchemaDiscoverer` interface with implementations:
   - `MySQLSchemaDiscoverer`
   - `PostgreSQLSchemaDiscoverer`
   - `StaticSchemaDiscoverer`
3. Auto-discover tables from database at startup
4. Cache discovered schema to temp file
5. Remove hardcoded knownTables slices from proxies

### Dependencies:
- Session 1 (Database configuration) - need DB connection details

### Testing:
- Test auto-discovery with various database schemas
- Test static table list configuration

---

## Session 7: Configuration File Flexibility [MEDIUM]

### Issues:
1. **Hardcoded config filename**: Only `.linespec.yml` supported
2. **Hardcoded config search**: Only walks up to `.git` directory
3. **No support for custom config paths**: No `--config` flag in CLI
4. **Hardcoded provenance directory**: `provenance/` only
5. **Hardcoded file extensions**: Only `.yml` and `.yaml` for provenance

### Files to Modify:
- `pkg/config/parser.go` (lines 11-39)
- `pkg/provenance/loader.go` (lines 52-88)
- `pkg/provenance/commands.go` (add --config flag support)
- `cmd/linespec/main_beta.go` (add --config flag)
- `cmd/linespec/main_stable.go` (add --config flag)

### Implementation Plan:
1. Add `--config` / `-c` flag to all CLI commands
2. Support multiple config formats: `.linespec.yml`, `.linespec.yaml`, `linespec.json`
3. Allow config path via environment variable `LINESPEC_CONFIG`
4. Make provenance directory name configurable
5. Support both `.yml` and `.yaml` extensions in loader
6. Allow custom config search depth (not just until `.git`)

### Dependencies:
- None

### Testing:
- Test with different config file names and locations
- Test environment variable override

---

## Session 8: Payload & Protocol Flexibility [MEDIUM]

### Issues:
1. **Limited payload formats**: Only JSON and YAML supported
2. **Status code extraction**: Assumes specific JSON structure
3. **Rails-specific auth extraction**: From request body in HTTP interceptor
4. **No custom payload parsers**: Can't add new formats without code changes
5. **Payload directory naming**: Hardcoded `payloads/` subdirectory

### Files to Modify:
- `pkg/dsl/payload.go` (lines 27-66)
- `pkg/proxy/http/interceptor.go` (lines 150-166)
- `pkg/config/types.go` (add payload config)

### Implementation Plan:
1. Create `PayloadParser` interface with methods:
   - `CanParse(extension string) bool`
   - `Parse(data []byte) (interface{}, error)`
2. Implement parsers for: JSON, YAML, XML, MessagePack, Protobuf (basic)
3. Make payload directory name configurable
4. Add custom response field extraction config:
   - `response.status_field`: field path for status code (default: "status")
5. Remove Rails-specific auth extraction from interceptor (move to config)
6. Allow custom auth extraction rules via config

### Dependencies:
- None

### Testing:
- Add XML and MessagePack payload tests
- Test custom status code extraction

---

## Session 9: Test Coupling & Examples Decoupling [LOW]

### Issues:
1. **Tests depend on examples directory**: Tests use `../../examples/` paths
2. **Hardcoded test emails**: `test@example.com`, `john@example.com` in production code paths
3. **Hardcoded API paths in tests**: `/users/123`, `/api/v1/users/auth`
4. **Test data in examples**: Examples directory contains test-specific data

### Files to Modify:
- `pkg/dsl/dsl_test.go` (lines 13, 48, 81)
- `pkg/dsl/payload_test.go` (lines 8, 25)
- `pkg/dsl/integration_test.go` (lines 10)
- `pkg/proxy/mysql/proxy_test.go` (lines 17-134)
- `pkg/verify/*_test.go` (various test data)

### Implementation Plan:
1. Create `testfixtures/` directory for test-specific data
2. Move test fixtures out of `examples/` into `testfixtures/`
3. Update all test file paths to use `testfixtures/` or create fixtures dynamically
4. Remove hardcoded test emails from production code
5. Make test database connection configurable (not always localhost:3307)
6. Add test markers to skip tests when services unavailable (better than current skip logic)

### Dependencies:
- None

### Testing:
- All tests should pass with new fixture locations
- Tests should skip gracefully when dependencies unavailable

---

## Session 10: Bug Fixes & Resource Management [CRITICAL]

### Issues:
1. **MySQL proxy tests fail**: Require running MySQL container on localhost:3307
2. **TODO: Deprecation reason not implemented**: Flag accepted but not stored
3. **Context.Background() misuse**: Cleanup operations use wrong context type
4. **Resource leaks**: File.Close() errors ignored, connections not closed properly
5. **No retry logic**: Database connections lack transient failure handling
6. **Silent error ignoring**: Many `err` variables assigned to `_`
7. **Regex compilation at runtime**: Patterns compiled on every call

### Files to Modify:
- `pkg/proxy/mysql/proxy_test.go` (container startup checks)
- `pkg/provenance/commands.go` (line 648 TODO)
- `pkg/runner/runner.go` (context usage lines 181, 388, 421, etc.)
- `pkg/embeddings/store.go` (file close errors)
- `pkg/proxy/mysql/proxy.go` (regex compilation, error handling)
- `pkg/proxy/postgresql/proxy.go` (error handling)
- `pkg/registry/registry.go` (hit tracking race condition)

### Implementation Plan:
1. **Fix proxy tests**: Add container startup check with better skip logic
2. **Implement deprecation reason**: Add field to Record struct, store in YAML
3. **Fix contexts**: Use `context.WithTimeout()` for all cleanup operations
4. **Add retry logic**: Implement exponential backoff for DB connections
5. **Fix resource leaks**: Check all `defer` statements, handle close errors
6. **Optimize regex**: Move pattern compilation to package-level vars
7. **Fix race condition**: Use atomic operations for hit tracking in registry
8. **Error handling**: Log all ignored errors at debug level

### Dependencies:
- Can be done in parallel with Sessions 1-9
- Should be completed before v1.0.0 stable release

### Testing:
- Run full test suite
- Verify no resource leaks with `go test -race`

---

## Quick Wins (Can be done in single session)

### Issue Q1: Configurable Health Endpoints
**Current**: Rails uses `/up`, FastAPI uses `/health`
**Fix**: Add `HealthEndpoint` to config (already partially there)
**Files**: `pkg/config/types.go`, `pkg/runner/runner.go`
**Lines**: 5-10 lines changed

### Issue Q2: Remove Rails-Specific Body Auth Extraction
**Current**: `extractAuthFromBody()` assumes Rails auth format
**Fix**: Make auth extraction configurable or remove to HTTP mock level
**Files**: `pkg/proxy/http/interceptor.go`
**Lines**: Remove lines 168-205

### Issue Q3: Fix Silent Error Handling
**Current**: Many `_, _ = io.Copy()` patterns
**Fix**: Add debug logging for all ignored errors
**Files**: `pkg/proxy/mysql/proxy.go`, `pkg/proxy/postgresql/proxy.go`, `pkg/docker/orchestrator.go`
**Lines**: ~20 lines added

### Issue Q4: Optimize Regex Compilation
**Current**: `regexp.MustCompile()` inside functions
**Fix**: Move to package-level `var` declarations
**Files**: `pkg/proxy/mysql/proxy.go`, `pkg/dsl/lexer.go`
**Lines**: ~15 lines changed

---

## Implementation Order Recommendation

### Phase 1: Foundation (Sessions 1-2)
1. **Session 1**: Database Configuration
2. **Session 2**: Framework Abstraction

*These are foundational and unblock most other work.*

### Phase 2: Infrastructure (Sessions 3-5)
3. **Session 3**: Container Naming
4. **Session 4**: Service Discovery
5. **Session 5**: Port Configuration

*These handle the infrastructure coupling issues.*

### Phase 3: Flexibility (Sessions 6-8)
6. **Session 6**: Schema Discovery
7. **Session 7**: Config File Flexibility
8. **Session 8**: Payload Formats

*These add flexibility for different use cases.*

### Phase 4: Polish (Sessions 9-10)
9. **Session 9**: Test Decoupling
10. **Session 10**: Bug Fixes & Resource Management

*These improve quality and maintainability.*

### Phase 5: Quick Wins (Parallel)
11. **Quick Wins Q1-Q4**: Can be done anytime, good for filler work

---

## Dependencies Graph

```
Session 1 (Database Config)
    ↓
Session 2 (Frameworks) ← depends on Session 1 for DB migrations
    ↓
Session 3 (Container Naming)
    ↓
Session 4 (Service Discovery) ← depends on Session 3 for container names
    ↓
Session 5 (Port Config) ← depends on Session 3 for container coordination
    ↓
Session 6 (Schema Discovery) ← depends on Session 1 for DB connection

Independent:
- Session 7 (Config Files)
- Session 8 (Payload Formats)
- Session 9 (Test Decoupling)
- Session 10 (Bug Fixes)
- Quick Wins Q1-Q4
```

---

## Testing Strategy

### For Each Session:
1. Create provenance record describing the changes
2. Add/update unit tests for new functionality
3. Add integration tests with different configurations
4. Update existing tests to use new configurable options
5. Run full test suite: `go test ./...`
6. Run race detector: `go test -race ./...`

### Integration Test Matrix:
- **Databases**: MySQL, PostgreSQL, External (no DB)
- **Frameworks**: Rails, FastAPI, Django, Express, Generic
- **Languages**: Ruby, Python, Node.js, Go, Java
- **Payload Formats**: JSON, YAML, XML, MessagePack
- **Service Types**: Web, Worker, Consumer, Mixed

---

## Acceptance Criteria

LineSpec should be able to test:

1. ✅ A Django app with PostgreSQL
2. ✅ A Node.js/Express API with MongoDB
3. ✅ A Spring Boot service with external database
4. ✅ A Go microservice with Redis
5. ✅ Multiple services in parallel without port conflicts
6. ✅ Services with custom container names and networks
7. ✅ Non-Rails frameworks without code changes
8. ✅ Custom payload formats (XML, Protobuf)
9. ✅ Custom database initialization scripts
10. ✅ Services with custom health check endpoints

---

## Notes for Agents

### When Working on Sessions:
1. **Always create a provenance record** before starting
2. **Use the CLI**: `linespec provenance create --title "Description"`
3. **Don't use --no-verify** when committing (unless testing hook bypass)
4. **Ask user before marking implemented** - these are architectural changes
5. **Update tests** as part of each session
6. **Run the test suite** before and after changes
7. **Follow AGENTS.md guidelines** for constraints and scope

### Grouping Rules:
- Sessions 1-2 should be done sequentially (dependencies)
- Sessions 3-5 should be done sequentially (dependencies)
- Sessions 6-10 can be done in parallel or in any order
- Quick wins can be done anytime
- Bug fixes (Session 10) should be prioritized before stable release

### Breaking Changes:
Sessions 1-5 may introduce breaking changes to `.linespec.yml` format:
- New required fields may be added
- Default behaviors may change
- Document all breaking changes in CHANGELOG.md

---

## Related Documentation

- **AGENTS.md**: Guidelines for working with LineSpec
- **PROVENANCE_RECORDS.md**: How to create and manage records
- **LINESPEC.md**: Beta features and DSL documentation
- **Current code**: See grep results in each session for exact line numbers

---

*Last Updated: March 18, 2026*
*Total Estimated Effort: 8-10 provenance records*
*Priority: Critical issues first, then high, medium, low*