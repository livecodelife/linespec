# Changelog

All notable changes to LineSpec will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [2.0.0] - 2026-04-16

### Changed

- **LineSpec Testing is now stable** ([prov-2026-7cc26544](./provenance/prov-2026-7cc26544.yml)) - LineSpec Testing graduates from beta to stable. The `linespec` binary now includes the `test` and `proxy` commands by default. The `-tags beta` build flag is no longer required to access integration testing features. The `linespec-beta` Homebrew formula continues to be published as a backward-compatible alias pointing at the same binary.

### Related Provenance Records

- [prov-2026-7cc26544](./provenance/prov-2026-7cc26544.yml) - Graduate LineSpec Testing from beta to stable (v2.0.0 release)

## [1.5.0-beta] - 2026-04-15

### Added (Beta)

- **Multiple databases per service** ([prov-2026-ad64db54](./provenance/prov-2026-ad64db54.yml)) - `.linespec.yml` now accepts a `databases:` list so a service that uses more than one database type simultaneously (e.g. MySQL for relational data and MongoDB for an event log) can be tested end-to-end. Each entry in the list gets its own real-DB container and protocol-level proxy sidecar running in parallel during the test. The existing `database:` singular form is preserved as a backward-compatible alias; all existing `.linespec.yml` files work without modification.

  Key behaviours:
  - Each entry requires a `name:` field. The `host:` field defaults to the entry name, giving each proxy a unique network alias (e.g. `mysql`, `mongo`).
  - A single spec can assert `EXPECT WRITE:MYSQL` and `EXPECT WRITE:MONGODB` in the same test, with both proxies intercepting concurrently.
  - Environment variables are injected with a name prefix for every database (`MYSQL_DB_HOST`, `MONGO_MONGODB_URI`, etc.) plus the legacy unprefixed names for the first database so single-database services need no changes.
  - The validator rejects configs where two entries share the same `host:` alias.

- **order-events-service example** ([prov-2026-91ed882c](./provenance/prov-2026-91ed882c.yml)) - New example service and linespec suite demonstrating MySQL + MongoDB simultaneously (`examples/multi-db-service/`, `examples/multi-db-linespecs/`).

### Related Provenance Records

- [prov-2026-ad64db54](./provenance/prov-2026-ad64db54.yml) - Support multiple databases per service in .linespec.yml
- [prov-2026-91ed882c](./provenance/prov-2026-91ed882c.yml) - Add order-events-service example for multi-database testing

## [1.5.0] - 2026-04-15

### Added

- **Run associated specs on commit** ([prov-2026-2155a00e](./provenance/prov-2026-2155a00e.yml)) - The pre-commit hook can now run a record's `associated_specs` before allowing the commit that marks it as implemented. Opt in with `run_associated_specs_on_complete: true` in `.linespec.yml`. Supported spec types: `linespec`, `rspec`, `pytest`, `jest`. A new optional `run_command` field on each `associated_specs` entry overrides the default command for that type (supports a `{{path}}` placeholder; otherwise the path is appended). Specs with no `type` and no `run_command` are skipped with a warning rather than failing the commit. A new `linespec provenance run-specs --record <id>` subcommand is also exposed for scripts that need to invoke this step manually.

### Related Provenance Records

- [prov-2026-2155a00e](./provenance/prov-2026-2155a00e.yml) - Run associated specs on pre-commit during implementation transition

## [1.4.3-beta] - 2026-04-14

### Added (Beta)

- **Configurable proxy Docker image** ([prov-2026-557f393c](./provenance/prov-2026-557f393c.yml)) - New `proxy_image` field under `infrastructure` in `.linespec.yml` lets teams point proxies at a custom image instead of the default `linespec:latest`. Useful for CI/CD pipelines, private registries, and machines that don't have the image built locally.

### Fixed (Beta)

- **Kafka proxy startup now gated on `infrastructure.kafka`** ([prov-2026-557f393c](./provenance/prov-2026-557f393c.yml)) - The Kafka proxy container was previously started whenever a test used a `RECEIVE EVENT:` trigger, regardless of the `infrastructure.kafka` flag. It now respects the flag, consistent with all other proxy types (PostgreSQL, MySQL, HTTP, Redis, gRPC).

### Related Provenance Records

- [prov-2026-557f393c](./provenance/prov-2026-557f393c.yml) - Configurable proxy image name and Kafka proxy startup guard

## [1.4.1-beta] - 2026-04-13

### Added (Beta)

- **MySQL prepared statement interception** ([prov-2026-edbd5218](./provenance/prov-2026-edbd5218.yml)) - MySQL proxy now intercepts `COM_STMT_PREPARE` and `COM_STMT_EXECUTE` commands in addition to plain `COM_QUERY`, enabling test coverage of services that use prepared statements.

- **Kafka consumer/fetch protocol interception** ([prov-2026-abc98f0d](./provenance/prov-2026-abc98f0d.yml)) - Kafka proxy now intercepts consumer-side traffic: Fetch, JoinGroup, SyncGroup, Heartbeat, LeaveGroup, OffsetCommit, and OffsetFetch requests. Enables `.linespec` tests to assert on messages consumed by the service under test.

- **gRPC and Redis proxy support** ([prov-2026-9f50db3c](./provenance/prov-2026-9f50db3c.yml)) - New gRPC (HTTP/2 + JSON) and Redis (RESP2) protocol interceptors. Adds DSL channel types `GRPC:service/Method`, `READ:REDIS`, and `WRITE:REDIS` so services using gRPC or Redis dependencies can be tested with LineSpec.

- **Configurable test timeout** ([prov-2026-e9e990bb](./provenance/prov-2026-e9e990bb.yml)) - Added `timeout_seconds` field to `.linespec.yml` service config and a per-test `TIMEOUT` DSL directive. Per-test value takes precedence over the service default; both fall back to 180s when unset.

- **Passthrough visibility for unmatched mocks** ([prov-2026-6edd93f0](./provenance/prov-2026-6edd93f0.yml)) - Proxies now log a warning when a request passes through without matching any registered mock, making it easier to diagnose missing mock registrations during test development.

- **notification-service Redis/gRPC example** ([prov-2026-f9eb4e06](./provenance/prov-2026-f9eb4e06.yml)) - Extended the notification-service example to demonstrate Redis auth-token caching and a gRPC+JSON client for user lookup in the Kafka consumer path.

### Fixed (Beta)

- **Kafka consumer protocol bugs** ([prov-2026-63f06580](./provenance/prov-2026-63f06580.yml)) - Fixed JoinGroup follower handling, round-robin assignor, `ListOffsets` offset 0 behavior, and YAML→JSON conversion for HTTP responses in Kafka consumer tests.

- **Kafka produce message parsing** ([prov-2026-fe6a08be](./provenance/prov-2026-fe6a08be.yml)) - Fixed `extractProduceData` to use proper Kafka wire protocol parsing instead of magic positional offsets. Adds RecordBatch v2 varint/zigzag decoding, header extraction, MessageSet v0/v1 fallback, and compressed batch detection.

- **HTTP proxy port now configurable** ([prov-2026-c99fbd8e](./provenance/prov-2026-c99fbd8e.yml)) - HTTP dependency proxies now bind on the port declared in `.linespec.yml` (`dep.Port`) instead of always defaulting to port 80, enabling interception of services on non-standard ports.

- **Dockerfile.linespec exclusion false positives** ([prov-2026-0736d6d6](./provenance/prov-2026-0736d6d6.yml)) - Fixed test file exclusion logic to match only the exact filename `Dockerfile.linespec` instead of any path containing the substring "dockerfile", preventing valid test files from being silently skipped.

- **Network alias sourced from containerNaming config** ([prov-2026-08169255](./provenance/prov-2026-08169255.yml)) - Container network aliases are now sourced exclusively from the `containerNaming` config template instead of being partially derived from hardcoded logic.

- **MySQL integration tests broken by P2-E** ([prov-2026-e00e9f98](./provenance/prov-2026-e00e9f98.yml)) - Fixed MySQL integration tests that were broken by the database name enforcement added in P2-E.

- **Remaining incomplete plan items** ([prov-2026-de632306](./provenance/prov-2026-de632306.yml)) - Fixed four items marked complete in the improvement plan but not fully implemented: remaining inline regex compilations (P4-B), a fixed 12-second sleep in the Kafka consumer wait path (P4-A), a hardcoded network alias constant (P5-E), and a mock registration gap (P1-A).

### Changed (Beta)

- **Exponential-backoff readiness polling** ([prov-2026-238350b1](./provenance/prov-2026-238350b1.yml)) - Replaced fixed `time.Sleep` calls in container startup with exponential-backoff polling, reducing total test suite time and eliminating arbitrary waits.

- **PostgreSQL proxy debug logging opt-in** ([prov-2026-8719e54b](./provenance/prov-2026-8719e54b.yml)) - PostgreSQL proxy debug logging is now disabled by default with a no-op fast path. Enable via config to restore previous behavior.

- **MySQL schema passed inline via `--schema-data`** ([prov-2026-a4a1063d](./provenance/prov-2026-a4a1063d.yml)) - MySQL schema is now passed to the proxy container as an inline flag argument (`--schema-data`) instead of a mounted JSON file, simplifying container setup.

- **DSL verify-rule regex compiled once at init** ([prov-2026-00cfa45c](./provenance/prov-2026-00cfa45c.yml)) - Regex patterns used in DSL verify-rule parsing are now compiled once at package init rather than on every parse call, eliminating redundant compilation overhead.

### Related Provenance Records

- [prov-2026-edbd5218](./provenance/prov-2026-edbd5218.yml) - MySQL prepared statement interception
- [prov-2026-abc98f0d](./provenance/prov-2026-abc98f0d.yml) - Kafka consumer/fetch protocol interception
- [prov-2026-63f06580](./provenance/prov-2026-63f06580.yml) - Kafka consumer protocol bug fixes
- [prov-2026-fe6a08be](./provenance/prov-2026-fe6a08be.yml) - Kafka produce message parsing fix
- [prov-2026-9f50db3c](./provenance/prov-2026-9f50db3c.yml) - gRPC and Redis proxy support
- [prov-2026-f9eb4e06](./provenance/prov-2026-f9eb4e06.yml) - notification-service Redis/gRPC example
- [prov-2026-238350b1](./provenance/prov-2026-238350b1.yml) - Exponential-backoff readiness polling
- [prov-2026-e00e9f98](./provenance/prov-2026-e00e9f98.yml) - MySQL integration test fix
- [prov-2026-00cfa45c](./provenance/prov-2026-00cfa45c.yml) - DSL verify-rule regex compiled at init
- [prov-2026-8719e54b](./provenance/prov-2026-8719e54b.yml) - PostgreSQL proxy debug logging opt-in
- [prov-2026-a4a1063d](./provenance/prov-2026-a4a1063d.yml) - MySQL schema via --schema-data flag
- [prov-2026-e9e990bb](./provenance/prov-2026-e9e990bb.yml) - Configurable test timeout
- [prov-2026-c99fbd8e](./provenance/prov-2026-c99fbd8e.yml) - HTTP proxy port configurable
- [prov-2026-6edd93f0](./provenance/prov-2026-6edd93f0.yml) - Passthrough visibility for unmatched mocks
- [prov-2026-0736d6d6](./provenance/prov-2026-0736d6d6.yml) - Dockerfile.linespec exclusion fix
- [prov-2026-08169255](./provenance/prov-2026-08169255.yml) - Network alias from containerNaming config
- [prov-2026-de632306](./provenance/prov-2026-de632306.yml) - Fix four incomplete improvement-plan items
- [prov-2026-f22371a0](./provenance/prov-2026-f22371a0.yml) - This release

## [1.4.0-beta] - 2026-04-13

### Added (Beta)

- **EXPECT_NOT enforcement** ([prov-2026-758af159](./provenance/prov-2026-758af159.yml)) - Negative expectations in `.linespec` test files are now properly enforced. Interactions matching an `EXPECT_NOT` block now cause the test to fail, closing a gap where negative assertions were silently ignored.

- **Auto table discovery** ([prov-2026-9a74b241](./provenance/prov-2026-9a74b241.yml)) - PostgreSQL proxy now automatically discovers tables from query traffic instead of relying on a hardcoded Rails table list. Removes framework-specific assumptions from the proxy core.

- **Data-driven framework config** ([prov-2026-83fdfdd9](./provenance/prov-2026-83fdfdd9.yml)) - Framework defaults (health endpoint, port, start command) are now driven by a config map rather than hardcoded structs. Chi framework defaults added.

- **Chi framework defaults** ([prov-2026-eb5f2ecb](./provenance/prov-2026-eb5f2ecb.yml)) - Added Chi as a supported framework with sensible defaults in the framework config map.

- **HTTP proxy Content-Type inference** ([prov-2026-ebfa7835](./provenance/prov-2026-ebfa7835.yml)) - HTTP proxy now infers `Content-Type` from the payload file extension (`.json` → `application/json`, `.xml` → `application/xml`, etc.) instead of always defaulting to `application/json`. Added `RESPONSE_HEADERS` support on `EXPECT` blocks and `HEADERS` support on `RESPOND` blocks for explicit overrides.

### Fixed (Beta)

- **Non-deterministic registry matching** ([prov-2026-cd962cbc](./provenance/prov-2026-cd962cbc.yml)) - Fixed fuzzy registry matching that produced inconsistent results across test runs due to map iteration order. Matching is now deterministic.

- **PostgreSQL startup enforcement** ([prov-2026-0a7ec7ff](./provenance/prov-2026-0a7ec7ff.yml)) - PostgreSQL proxy now fails fast on startup timeout rather than silently hanging, improving error visibility in CI.

- **MySQL CLIENT_QUERY_ATTRIBUTES parsing** ([prov-2026-976c7d8a](./provenance/prov-2026-976c7d8a.yml)) - Fixed MySQL proxy to correctly parse the `CLIENT_QUERY_ATTRIBUTES` capability flag, resolving handshake failures with MySQL 8.x clients.

- **PostgreSQL mid-read deadlock** ([prov-2026-24c7e7aa](./provenance/prov-2026-24c7e7aa.yml)) - Fixed a deadlock in the PostgreSQL proxy that occurred when a read was in progress during config decoupling. Also decoupled proxy behavior config from the global `.linespec.yml` config.

- **Proxy behavior config unification** ([prov-2026-aa64b036](./provenance/prov-2026-aa64b036.yml)) - Unified proxy behavior configuration across all proxy types (PostgreSQL, MySQL, HTTP, Kafka), replacing per-proxy ad-hoc config with a shared structure.

### Related Provenance Records

- [prov-2026-758af159](./provenance/prov-2026-758af159.yml) - Enforce EXPECT_NOT negative expectations
- [prov-2026-cd962cbc](./provenance/prov-2026-cd962cbc.yml) - Fix non-deterministic fuzzy registry matching
- [prov-2026-0a7ec7ff](./provenance/prov-2026-0a7ec7ff.yml) - Enforce PostgreSQL startup failure on timeout
- [prov-2026-976c7d8a](./provenance/prov-2026-976c7d8a.yml) - Fix MySQL CLIENT_QUERY_ATTRIBUTES parsing
- [prov-2026-9a74b241](./provenance/prov-2026-9a74b241.yml) - Auto table discovery replacing hardcoded Rails tables
- [prov-2026-83fdfdd9](./provenance/prov-2026-83fdfdd9.yml) - Data-driven framework config replacing hardcoded structs
- [prov-2026-24c7e7aa](./provenance/prov-2026-24c7e7aa.yml) - Config decoupling and PostgreSQL deadlock fix
- [prov-2026-eb5f2ecb](./provenance/prov-2026-eb5f2ecb.yml) - Chi framework defaults
- [prov-2026-aa64b036](./provenance/prov-2026-aa64b036.yml) - Proxy behavior config unification
- [prov-2026-ebfa7835](./provenance/prov-2026-ebfa7835.yml) - HTTP proxy Content-Type inference from file extension
- [prov-2026-70cbf556](./provenance/prov-2026-70cbf556.yml) - This release

## [1.4.0] - 2026-04-03

### Added

- **Lock-layer command** ([prov-2026-7df318ca](./provenance/prov-2026-7df318ca.yml)) - New `linespec provenance lock-layer` command that creates a provenance record in `implemented` status with `locked: true`, capturing the current HEAD SHA. The linter enforces that any `open` record whose `affected_scope` or `associated_specs` overlaps with a locked record's scope is a lint error unless the open record declares `supersedes` pointing at the locked record. This forces explicit acknowledgment when reopening a crystallized layer.

- **SARIF output format** ([prov-2026-da6796d4](./provenance/prov-2026-da6796d4.yml)) - Added `--format sarif` flag to the `linespec provenance lint` command that emits lint results as a standards-compliant SARIF 2.1.0 JSON document. Enables provenance violations to appear as first-class citizens in GitHub Code Scanning, VS Code's Problems panel, and any other tooling that consumes the SARIF standard.

- **GitHub Code Scanning workflow** - Added SARIF-based provenance lint workflow that uploads results to GitHub Code Scanning on every push.

### Changed

- **Beta build parity** - Beta build now includes all stable provenance commands plus embedder client setup for search/audit/index commands.

- **Documentation site** - Added GitHub Pages documentation site with comprehensive guides for Provenance Records and LineSpec Testing.

- **PostgreSQL proxy** - Major rewrite with improved query/response cycle handling, command complete message support, and table extraction capabilities.

- **MySQL proxy** - Improved database name extraction and proxy handling.

- **Configuration** - Added container naming flexibility, framework configuration options, and payload format enhancements.

- **Runner** - Added port allocator, improved container orchestration, and schema discovery support.

### Related Provenance Records

- [prov-2026-7df318ca](./provenance/prov-2026-7df318ca.yml) - Add lock-layer command and locked scope lint enforcement
- [prov-2026-da6796d4](./provenance/prov-2026-da6796d4.yml) - Add SARIF output format to provenance linter
- [prov-2026-4bf54660](./provenance/prov-2026-4bf54660.yml) - Add linespec-beta to affected scope
- [prov-2026-5801f304](./provenance/prov-2026-5801f304.yml) - Phase 3: Schema Discovery, Config File Flexibility, and Payload Formats
- [prov-2026-3f32f886](./provenance/prov-2026-3f32f886.yml) - Phase 2: Infrastructure Decoupling
- [prov-2026-4275079c](./provenance/prov-2026-4275079c.yml) - Phase 1: Database Configuration and Framework Abstraction
- [prov-2026-8227b384](./provenance/prov-2026-8227b384.yml) - Phase 3 implementation
- [prov-2026-054aadca](./provenance/prov-2026-054aadca.yml) - Documentation site
- [prov-2026-7ade6d68](./provenance/prov-2026-7ade6d68.yml) - Bug fix
- [prov-2026-c5facf10](./provenance/prov-2026-c5facf10.yml) - This release

## [1.3.0] - 2026-03-17

### Added

- **Semantic search and embedding layer** ([prov-2026-7136e8c4](./provenance/prov-2026-7136e8c4.yml)) - Natural language search capability for provenance records using Voyage AI embeddings. Enables engineers and AI agents to discover historically relevant records by meaning rather than by file path or record ID.
  - New `linespec provenance search` command - Accepts natural language queries and returns semantically similar records ranked by cosine similarity
  - New `linespec provenance audit` command - Compares descriptions of recent changes against embedding history to surface potential inconsistencies
  - New `linespec provenance index` command - Bulk indexes all implemented records for semantic search
  - Local embedding store at `.linespec/embeddings.bin` - No external database required
  - Dual model support - Uses `voyage-4-large` for indexing and `voyage-4-lite` for queries (both 2048-dimensional)
  - Automatic embedding generation on `linespec provenance complete`
  - Configurable via `.linespec.yml` with environment variable API key

- **Crypto random hex IDs** ([prov-2026-84ab4e56](./provenance/prov-2026-84ab4e56.yml)) - New provenance record ID format using 8 cryptographically random hex characters instead of sequential numbers.
  - Eliminates ID conflicts when multiple engineers create records concurrently
  - Format: `prov-YYYY-XXXXXXXX` (e.g., `prov-2026-a1b2c3d4`)
  - 4+ billion possible combinations per year
  - Fully backward compatible with existing sequential IDs
  - Supports monorepo suffixes: `prov-YYYY-XXXXXXXX-service-name`

### Changed

- **Documentation** - Updated all documentation to reflect new features and version

### Related Provenance Records

- [prov-2026-7136e8c4](./provenance/prov-2026-7136e8c4.yml) - Semantic search and local embedding layer for provenance history
- [prov-2026-84ab4e56](./provenance/prov-2026-84ab4e56.yml) - Switch provenance record IDs from sequential numbers to crypto random hex
- [prov-2026-d146c70d](./provenance/prov-2026-d146c70d.yml) - This release

## [1.2.0] - 2026-03-14

### Added (Beta)

- **Extended VERIFY functionality** ([prov-2026-030](./provenance/prov-2026-030.yml)) - Unified verification engine supporting SQL queries, HTTP headers/body/URLs, and Kafka message keys/values/headers. All intercepted traffic can now be validated with CONTAINS, NOT_CONTAINS, and MATCHES operators.
- **Environment variable substitution** ([prov-2026-028](./provenance/prov-2026-028.yml)) - Implicit ${VAR_NAME} syntax in HTTP headers, URLs, paths, and payload files. Random values are generated at test runtime to catch hardcoded tokens and API keys.

### Fixed (Stable)

- **Formatter empty file handling** ([prov-2026-036](./provenance/prov-2026-036.yml)) - Fixed confusing output when a violation occurs on an implemented record. Now displays the violation message explaining the record is already implemented, instead of showing an empty bullet point.

### Related Provenance Records

- [prov-2026-028](./provenance/prov-2026-028.yml) - Add implicit environment variable substitution to LineSpec DSL
- [prov-2026-029](./provenance/prov-2026-029.yml) - Implement full VERIFY functionality for SQL queries (superseded by prov-2026-030)
- [prov-2026-030](./provenance/prov-2026-030.yml) - Extend VERIFY to support HTTP, Kafka, and SQL verification with unified engine
- [prov-2026-036](./provenance/prov-2026-036.yml) - Fix formatter to display message when File is empty

## [1.1.0] - 2026-03-13

### Breaking Changes

- **Replace associated_linespecs with associated_specs field** ([prov-2026-027](./provenance/prov-2026-027.yml)) - Breaking change to the provenance record schema. The `associated_linespecs` field has been replaced with `associated_specs`, which accepts any file path as proof artifacts with an optional `type` annotation.
  - Teams can now link any proof artifacts (RSpec, pytest, Jest, etc.) to their architectural decisions
  - The old `associated_linespecs` key is rejected with a clear error message
  - Type annotations help the linter understand the kind of artifact being referenced
  - Since there are no external users yet, this is implemented as a breaking change rather than a deprecation

### Fixed

- **Path validation in linter** ([prov-2026-031](./provenance/prov-2026-031.yml)) - Fixed two critical validation bugs that allowed invalid file paths to pass validation silently:
  - Now handles ALL os.Stat errors for associated_specs paths, not just IsNotExist
  - Validates that exact paths in affected_scope and forbidden_scope exist (including untracked files)
  - Validates that exact paths are files, not directories
  - Validates that glob and regex patterns match at least one existing file (including untracked)
  - Scope path validation only applies to OPEN records (preserving dead records feature)

- **Dead record detection with glob patterns** ([prov-2026-033](./provenance/prov-2026-033.yml)) - Fixed false positives where records were marked as "dead" when their glob patterns (like `pkg/proxy/**`) still matched existing files. The dead records check now considers glob patterns when determining if a record is dead.

### Changed

- **Improved stale scope warning messages** ([prov-2026-032](./provenance/prov-2026-032.yml)) - Updated warning messages to be clearer and more actionable:
  - Clearly indicates the user is modifying a file in an implemented record's scope
  - Includes the record ID and sealed SHA for reference
  - Explains that implemented records should not need further changes
  - Suggests creating a superseding record as the resolution path
  - Includes the specific CLI command to create a superseding record

### Related Provenance Records

- [prov-2026-027](./provenance/prov-2026-027.yml) - Breaking change: Replace associated_linespecs with associated_specs
- [prov-2026-031](./provenance/prov-2026-031.yml) - Fix path validation in linter
- [prov-2026-032](./provenance/prov-2026-032.yml) - Improve stale scope warning message clarity
- [prov-2026-033](./provenance/prov-2026-033.yml) - Fix dead record detection to handle glob patterns
- [prov-2026-035](./provenance/prov-2026-035.yml) - This release

## [1.0.4] - 2026-03-13

### Fixed

- **Enforce immutability for implemented records** ([prov-2026-023](./provenance/prov-2026-023.yml)) - Fixed bug where the commit-msg hook allowed commits tagged with already-implemented provenance records. Once a record is marked as `implemented`, it is now truly immutable - any attempt to commit with that record ID will be rejected with a clear error message: "is already implemented - cannot commit with this ID. Create a new record or supersede this one."

### Added

- **Implemented record enforcement** ([prov-2026-023](./provenance/prov-2026-023.yml)) - The commit-msg hook now validates record status before processing scope checks. Implemented records are rejected to prevent changes to finalized architectural decisions.
- **Test coverage** ([prov-2026-023](./provenance/prov-2026-023.yml)) - Added `TestCheckStagedRejectsImplementedRecords` to verify the new enforcement behavior.

### Changed

- **Documentation** ([prov-2026-023](./provenance/prov-2026-023.yml)) - Updated `AGENTS.md` with rule about never adding provenance records to their own affected_scope.

### Related Provenance Records

- [prov-2026-023](./provenance/prov-2026-023.yml) - Enforce immutability for implemented records
- [prov-2026-024](./provenance/prov-2026-024.yml) - This release

## [1.0.3] - 2026-03-13

### Added

- **sealed_at_sha field** ([prov-2026-021](./provenance/prov-2026-021.yml)) - New field in Provenance Records that captures the HEAD git SHA when a record is marked as `implemented`. This enables smarter stale scope detection that reduces false positives by only warning on files that have actually changed since the record was sealed.
  - Automatically set by `linespec provenance complete` command
  - Validated by `linespec provenance lint` (7-40 hex characters)
  - Displayed by `linespec provenance status` for implemented records
  - Used by `linespec provenance check` to filter stale scope warnings
- **Stale scope warning filtering** ([prov-2026-021](./provenance/prov-2026-021.yml)) - The check command now uses `git diff <sealed_at_sha> HEAD` to verify files have actually changed since sealing before surfacing warnings, reducing noise for engineers making safe refactors.

### Changed

- **Documentation** ([prov-2026-021](./provenance/prov-2026-021.yml)) - Updated `PROVENANCE_RECORDS.md` and `AGENTS.md` with sealed_at_sha field documentation and schema reference.

### Related Provenance Records

- [prov-2026-021](./provenance/prov-2026-021.yml) - Add sealed_at_sha field for stale scope detection
- [prov-2026-022](./provenance/prov-2026-022.yml) - This release

## [1.0.2] - 2026-03-13

### Fixed

- **Self-modification exception for completion transition** ([prov-2026-019](./provenance/prov-2026-019.yml)) - Fixed bug where completing a provenance record (transitioning `status: open` → `status: implemented`) was being blocked by the commit-msg hook when the record was in allowlist mode (non-empty `affected_scope`). The hook now properly detects the completion transition by comparing the HEAD version with the staged version.

### Changed

- **Documentation** ([prov-2026-019](./provenance/prov-2026-019.yml)) - Updated `AGENTS.md` with `--no-edit` flag documentation for CLI usage in non-interactive environments.

### Related Provenance Records

- [prov-2026-019](./provenance/prov-2026-019.yml) - Bug fix for self-modification exception
- [prov-2026-020](./provenance/prov-2026-020.yml) - This release

## [1.0.1] - 2026-03-12

### Added

- **Two-hook git strategy** ([prov-2026-014](./provenance/prov-2026-014.yml)) - Separates concerns between pre-commit and commit-msg hooks:
  - `pre-commit` hook: Validates that modified provenance records are well-formed (linting)
  - `commit-msg` hook: Validates that provenance IDs in the message match staged files and enforces scope constraints
- **Self-modification exception** ([prov-2026-013](./provenance/prov-2026-013.yml)) - Open provenance records can now modify their own YAML files when the commit is tagged with that record's ID, enabling natural workflow completion
- **New CLI flags** for `linespec provenance check` command:
  - `--staged` - Check staged files instead of committed files
  - `--message-file` - Path to commit message file for validation

### Fixed

- **Self-modification exception logic** ([prov-2026-015](./provenance/prov-2026-015.yml)) - Now properly checks `forbidden_scope` directly instead of using `IsInScope()`, which was incorrectly requiring files to be in `affected_scope`
- **Completion commit check** - Removed overly permissive check that was allowing arbitrary modifications to implemented records

### Changed

- **Documentation updates** ([prov-2026-016](./provenance/prov-2026-016.yml)):
  - Updated `AGENTS.md` with two-hook strategy details and CLI usage guidelines
  - Updated `PROVENANCE_RECORDS.md` with new check command flags and workflow examples
  - Added clear distinction between pre-commit and commit-msg hook responsibilities
  - Documented the self-modification exception with examples
  - Updated `install-hooks` command documentation to reflect that it installs both hooks

### Related Provenance Records

- [prov-2026-012](./provenance/prov-2026-012.yml) - v1.0.0 release strategy
- [prov-2026-013](./provenance/prov-2026-013.yml) - Self-modification exception
- [prov-2026-014](./provenance/prov-2026-014.yml) - Two-hook git strategy
- [prov-2026-015](./provenance/prov-2026-015.yml) - Fix self-modification exception logic
- [prov-2026-016](./provenance/prov-2026-016.yml) - Documentation updates

## [1.0.0] - 2026-03-12

### Added

- **Provenance Records (Stable)** - Structured YAML artifacts for documenting architectural decisions
  - Complete CLI subsystem with create, lint, status, graph, check, lock-scope, complete, and deprecate commands
  - Git integration with pre-commit hooks and commit message validation
  - Scope enforcement (affected_scope, forbidden_scope)
  - Graph visualization of decision relationships
  - Monorepo support with ID suffixes
  - CI/CD ready with JSON output and strict enforcement modes
- **LineSpec Testing (Beta)** - DSL-based integration testing for containerized services
  - Protocol proxies for MySQL, PostgreSQL, HTTP, and Kafka
  - Available via `-tags beta` build flag
- **GoReleaser configuration** - Automated releases for Linux, macOS, Windows
- **Homebrew support** - Separate formulas for stable (`linespec`) and beta (`linespec-beta`)

### Notes

- First stable release focusing on Provenance Records
- LineSpec Testing features remain in beta
- Module path: `github.com/livecodelife/linespec`
