# Changelog

All notable changes to LineSpec will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [3.7.0] - 2026-05-15

### Changed

- **Provenance skill** ([prov-2026-f25d8526](./provenance/prov-2026-f25d8526.yml)) — The `provenance` Claude Code skill (both the embedded constant installed via `linespec provenance install-skills` and the `skills/provenance/SKILL.md` file) now documents five previously undocumented commands in its Useful Commands section: `linespec init`, `linespec provenance compile`, `linespec provenance publish`, `linespec import`, and `linespec clone`.

### Related Provenance Records

- [prov-2026-f25d8526](./provenance/prov-2026-f25d8526.yml) - Blueprint: Document compile, init, publish, import, and clone commands in provenance skill
- [prov-2026-7efa1f1b](./provenance/prov-2026-7efa1f1b.yml) - Imprint: Add compile, init, publish, import, clone to skill Useful Commands sections

## [3.6.0] - 2026-05-15

### Added

- **`linespec provenance publish` command** ([prov-2026-dac81a68](./provenance/prov-2026-dac81a68.yml)) — Packages a repository's provenance records into a versioned, content-addressed `linespec.manifest.json` artifact. Applies a deterministic transformation pipeline (strip imprints, filter superseded, promote bugs, clean dangling refs, reset status) before packaging. The provenance layer is a tar of individual record YAML files; specs and code directory layers are also tars with repo-relative paths preserved so `linespec clone` can extract them directly. SHA-256 hashes are computed per layer and combined into a `root_hash`. Versions are immutable — publish refuses to overwrite an existing version key. Supports `--specs`, `--code`, and `--prompt` optional layers, explicit `--version` override, and a custom `--manifest` path.

- **`linespec clone` command** ([prov-2026-587563e4](./provenance/prov-2026-587563e4.yml)) — Bootstraps a new project directory from a published manifest. Fetches the manifest, verifies `root_hash` before downloading any artifact, runs `git init`, writes `.linespec.yml` with `provenance.manifest_url` set to the source URL, installs git hooks, then downloads and hash-verifies each layer before extracting it. The provenance layer is extracted into `<dest>/provenance/`; specs, code, and prompt layers are extracted preserving repo-relative paths. Supports `@version` URL suffix and `--version` flag for pinning; `--dir` sets the destination directory name.

- **`linespec import` command** ([prov-2026-587563e4](./provenance/prov-2026-587563e4.yml)) — Imports provenance records from a published manifest into an existing repository. Checks all record IDs for conflicts with the local `provenance/` directory and aborts atomically if any exist. After extraction runs `linespec provenance lint` to surface reference issues. Supports `@version` URL suffix and `--version` flag for version pinning.

- **`pkg/manifest` package** ([prov-2026-94a4a406](./provenance/prov-2026-94a4a406.yml)) — New package providing `Fetch` (download and hash-verify a manifest and all its layer artifacts), `ExtractProvenance` (untar a provenance layer into a local directory), `ExtractSpecs` (untar a specs/code layer preserving repo-relative paths), and `ProvenanceRecordIDs` (enumerate record IDs from a provenance layer without extracting). Used internally by both `clone` and `import`.

### Related Provenance Records

- [prov-2026-dac81a68](./provenance/prov-2026-dac81a68.yml) - Blueprint: linespec publish — manifest packaging, transformation, and versioning
- [prov-2026-587563e4](./provenance/prov-2026-587563e4.yml) - Blueprint: Implement linespec clone and import commands
- [prov-2026-94a4a406](./provenance/prov-2026-94a4a406.yml) - Imprint: pkg/manifest — fetch, verify, and extract manifest layers
- [prov-2026-ccde6162](./provenance/prov-2026-ccde6162.yml) - Imprint: linespec clone command and manifest_url config field
- [prov-2026-81b5d5ac](./provenance/prov-2026-81b5d5ac.yml) - Imprint: linespec import command

## [3.5.0] - 2026-05-15

### Added

- **`RECEIVE JOB` channel** ([prov-2026-bfa97730](./provenance/prov-2026-bfa97730.yml)) — New test trigger channel for background job workers. Tests seed the job payload into the service's backing queue at the wire protocol level — Redis BRPOP/LPOP/BLPOP interception for Redis-backed frameworks (Asynq, Sidekiq, BullMQ, RQ, Dramatiq), existing mock rows for DB-backed frameworks (Oban, GoodJob, River), or Kafka seed for Kafka-backed workers. Scheduled/cron jobs work in observe-only mode: `RECEIVE JOB` with a `TIMEOUT` waits for the service's internal scheduler to fire naturally while EXPECT mocks supply job input data and verify outputs. Completion is detected purely through the proxy polling loop — no service status endpoint required. Configure `job_backend` once in `.linespec.yml`; individual test files stay clean. Three new examples cover Go + Asynq (Redis-backed), Node.js + BullMQ (Redis-backed), and a Go scheduled worker (observe-only with DB reads and outbound HTTP).

- **`linespec provenance compile` command** ([prov-2026-37bea46b](./provenance/prov-2026-37bea46b.yml)) — New command that rebuilds the hash manifest from scratch. Runs idempotently: if every record's hash already matches the stored manifest, no file is written. Useful for recovering from a deleted or corrupted `.linespec/hash_manifest.json` without needing to trigger an unrelated provenance write. After compile, `linespec provenance lint` passes with no integrity errors.

### Fixed

- **PostgreSQL proxy OID resolution for tokio-postgres and asyncpg** ([prov-2026-cbc8213e](./provenance/prov-2026-cbc8213e.yml)) — The proxy now checks whether OIDs can be resolved locally before intercepting Parse. If the client supplied explicit OIDs in Parse, or the query uses `$N::TYPE` casts, or the query has no parameters, the proxy handles Parse locally (mock-only mode). Otherwise Parse is forwarded to upstream so the real server's `ParameterDescription` (with correct OIDs) is relayed — fixing tokio-postgres (Rust) clients that send `num_params=0` and rely on the server to describe parameter types. Bind/Execute are still intercepted and mocked as before.

- **Extended-query protocol for multi-language clients** ([prov-2026-806c453e](./provenance/prov-2026-806c453e.yml)) — Fixed a race condition where the relay goroutine could deliver a stale `ParseComplete` to an idle connection after mock Execute completed. Additionally: `DescribeStatement` for `SELECT` now replies with `RowDescription` (not `NoData`) to match lib/pq expectations; `DescribePortal` for mocked portals is handled locally to avoid "portal does not exist" upstream errors; `Close` for mock-only statements/portals is handled locally. Redis `PopRedisSeed` now reads from the live registry so seeds survive hot-reload via `/reload-registry`.

- **Suppress passthrough warnings for idle pop commands** ([prov-2026-a02e898c](./provenance/prov-2026-a02e898c.yml)) — After a seeded job is consumed, the worker naturally re-issues BRPOP/BLPOP/LPOP to wait for more work. This idle-polling behavior no longer records a passthrough warning. All other unmatched commands continue to produce passthrough warnings as before.

### Changed

- **Lint: downgrade file-path issues to warnings for terminal-state records** ([prov-2026-9ea7c935](./provenance/prov-2026-9ea7c935.yml)) — For records in a terminal state (implemented, superseded, deprecated), missing or inaccessible file paths in `associated_specs`, `affected_scope`, and `forbidden_scope` are now reported as warnings rather than errors. Open and draft records retain the current error behavior. This prevents sealed records from generating lint failures as files are legitimately renamed or deleted after a feature ships.

### Related Provenance Records

- [prov-2026-bfa97730](./provenance/prov-2026-bfa97730.yml) - Blueprint: RECEIVE JOB background job trigger channel
- [prov-2026-fc2c3aef](./provenance/prov-2026-fc2c3aef.yml) - Imprint: Job channel type, RECEIVE JOB parser branch, and JobBackendConfig
- [prov-2026-2a95bd72](./provenance/prov-2026-2a95bd72.yml) - Imprint: Redis job seed mechanism — SeedRedisQueue, BRPOP/BLPOP/LPOP intercept
- [prov-2026-d5afa7b6](./provenance/prov-2026-d5afa7b6.yml) - Imprint: Wire RECEIVE JOB into runner — seed setup, polling loop, container persistence
- [prov-2026-6b014521](./provenance/prov-2026-6b014521.yml) - Imprint: asynq-worker Go example with RECEIVE JOB linespecs
- [prov-2026-bab878c6](./provenance/prov-2026-bab878c6.yml) - Imprint: bullmq-worker Node.js example with RECEIVE JOB linespecs
- [prov-2026-7095694c](./provenance/prov-2026-7095694c.yml) - Imprint: scheduled-worker Go example with observe-only RECEIVE JOB test
- [prov-2026-806c453e](./provenance/prov-2026-806c453e.yml) - Bug: Fix proxy extended-query protocol for multi-language clients
- [prov-2026-a02e898c](./provenance/prov-2026-a02e898c.yml) - Imprint: Suppress passthrough warnings for idle pop commands
- [prov-2026-cbc8213e](./provenance/prov-2026-cbc8213e.yml) - Bug: Fix PostgreSQL proxy OID resolution for tokio-postgres and asyncpg
- [prov-2026-9ea7c935](./provenance/prov-2026-9ea7c935.yml) - Blueprint: Downgrade missing-file errors to warnings for terminal-state records
- [prov-2026-2189e586](./provenance/prov-2026-2189e586.yml) - Imprint: Downgrade file-path lint issues to warnings for terminal-state records
- [prov-2026-37bea46b](./provenance/prov-2026-37bea46b.yml) - Blueprint: Add provenance compile command to regenerate hash manifest
- [prov-2026-f534cda1](./provenance/prov-2026-f534cda1.yml) - Imprint: Implement compile command — CompileManifest on Hasher + CLI wiring

## [3.4.0] - 2026-05-14

### Added

- **Lint output filtering flags** ([prov-2026-4358fe61](./provenance/prov-2026-4358fe61.yml)) — The `linespec provenance lint` command now shows only error-severity issues by default. Three new flags control what is displayed: `--warn` shows only warnings, `--info` shows only informational hints, and `--all` shows everything. The summary line (passed / warnings / errors counts) is always printed regardless of which flag is used.

### Related Provenance Records

- [prov-2026-66902c47](https://github.com/livecodelife/linespec-provenance) - Brief: Add flags for controlling lint output
- [prov-2026-4358fe61](./provenance/prov-2026-4358fe61.yml) - Blueprint: Add output filtering flags to lint command
- [prov-2026-08b36e49](./provenance/prov-2026-08b36e49.yml) - Imprint: Implement --warn/--info/--all output filter flags for lint

## [3.3.1] - 2026-05-08

### Fixed

- **`EXPECT_NOT` lexer regex** ([prov-2026-f5c528ac](./provenance/prov-2026-f5c528ac.yml)) — The lexer now accepts both `EXPECT_NOT` (underscore, documented form) and `EXPECT NOT` (space, legacy form). Previously only the space form worked; specs using the underscore form produced a parse error.
- **Container name sanitization** ([prov-2026-f5c528ac](./provenance/prov-2026-f5c528ac.yml)) — Test spec names containing spaces (e.g. `TEST create record success`) are now sanitized before being used in Docker container names. Previously Docker rejected container names with embedded spaces.

### Related Provenance Records

- [prov-2026-f5c528ac](./provenance/prov-2026-f5c528ac.yml) - Bug: Fix EXPECT_NOT lexer regex and container name sanitization

## [3.3.0] - 2026-05-08

### Added

- **`linespec init` command** ([prov-2026-85259a41](./provenance/prov-2026-85259a41.yml)) — New interactive setup wizard that guides users through creating a `.linespec.yml` configuration file from scratch. The command scans the project directory to infer sensible defaults for framework (`rails`, `express`, `chi`, `django`, `fastapi`), database type, service name, and port, then prompts for confirmation at each step. Pass `--force` to overwrite an existing config, `--project` to point at a different directory, and `--output` to write the file to a custom location. Works in both stable and beta builds. Framework detection priority: `manage.py` (Django) takes precedence over `requirements.txt` (FastAPI) so multi-file Python projects are classified correctly.

### Related Provenance Records

- [prov-2026-85259a41](./provenance/prov-2026-85259a41.yml) - Brief: Add init command to LineSpec CLI
- [prov-2026-d7f475dd](./provenance/prov-2026-d7f475dd.yml) - Blueprint: init command for interactive LineSpec project setup
- [prov-2026-486ad8d2](./provenance/prov-2026-486ad8d2.yml) - Imprint: Use pkg/initcmd to avoid Go reserved identifier conflict
- [prov-2026-55c45eda](./provenance/prov-2026-55c45eda.yml) - Imprint: Framework detection priority (manage.py before requirements.txt)
- [prov-2026-9bb99d81](./provenance/prov-2026-9bb99d81.yml) - Imprint: Use bufio.Scanner for interactive prompts with no external deps
- [prov-2026-d5bcb98f](./provenance/prov-2026-d5bcb98f.yml) - Imprint: Combined open transition with implementation due to strict spec enforcement

## [3.2.0] - 2026-05-06

### Added

- **Hash-based provenance record integrity** ([prov-2026-60ef0cfa](./provenance/prov-2026-60ef0cfa.yml)) — Implemented records are now protected by cryptographic content hashes stored in `.linespec/hash_manifest.json`. `linespec provenance complete` seals a SHA-256 hash of each record's canonical YAML representation into the manifest at transition time. `linespec provenance lint` compares live record content against stored hashes and reports a **PROV-IMM** error on any mismatch — no git access required, fully runnable in any CI or hook environment. The manifest also tracks a full-graph hash (all records sorted by ID) and an active-subset hash (excludes superseded and deprecated records), both recomputed on every seal.

- **`linespec provenance generate` command** ([prov-2026-c83990dc](./provenance/prov-2026-c83990dc.yml)) — New command that compiles a behavioral specification document from provenance records. By default produces Markdown; pass `--format yaml` for structured output. Target a specific brief or blueprint with `--record <id>` to scope the output, or run without arguments to compile from the full active provenance graph. Imprint records are always excluded. Bug records that `extends` a blueprint have their constraints merged in; Bug records that `supersede` a blueprint replace its content. Output can be written to a file with `--output <path>`.

### Related Provenance Records

- [prov-2026-60ef0cfa](./provenance/prov-2026-60ef0cfa.yml) - Blueprint: Hash-based integrity for implemented provenance records
- [prov-2026-618113ec](./provenance/prov-2026-618113ec.yml) - Hash manifest: JSON file in .linespec/, keyed by record ID
- [prov-2026-9a992763](./provenance/prov-2026-9a992763.yml) - Content hash computed from canonical YAML field serialization via SHA-256
- [prov-2026-3ee43780](./provenance/prov-2026-3ee43780.yml) - Linter: validateImmutability reads hash manifest, no git required
- [prov-2026-c83990dc](./provenance/prov-2026-c83990dc.yml) - Blueprint: generate command for behavioral specification documents
- [prov-2026-70ed0528](./provenance/prov-2026-70ed0528.yml) - Imprint: generate command implementation

## [3.1.1] - 2026-04-27

### Fixed

- **gRPC WITH body matching for binary protobuf requests** ([prov-2026-ad235109](./provenance/prov-2026-ad235109.yml)) — `EXPECT GRPC:Service/Method WITH {{file}}` was silently failing for services that use standard binary protobuf encoding (`Content-Type: application/grpc`). The interceptor now decodes the protobuf request body to JSON using the registered descriptor before running `CompareJSON`. If no descriptor is registered and the request is binary protobuf, a clear diagnostic error is surfaced instead of a silent false match.

### Internal

- **Reduced binary size ~29%** ([prov-2026-e0762917](./provenance/prov-2026-e0762917.yml)) — Added `-ldflags="-s -w"` and `-trimpath` to all `go build` and `go install` targets. Strips debug symbols and local filesystem paths from distributed binaries with no impact on runtime behavior.

### Related Provenance Records

- [prov-2026-ad235109](./provenance/prov-2026-ad235109.yml) - Fix gRPC WITH body matching for binary protobuf requests
- [prov-2026-e0762917](./provenance/prov-2026-e0762917.yml) - Add build optimization flags to reduce binary size

## [3.1.0] - 2026-04-27

### Added

- **Request body comparison via `WITH` statements** ([prov-2026-9e052576](./provenance/prov-2026-9e052576.yml)) — HTTP, gRPC, and Kafka `EXPECT` blocks now support `WITH {{payload.json}}` to assert the exact request body sent by the service. The proxy intercepts the inbound request, compares it against the mock payload using semantic JSON equality (key order-independent), and fails the test if the bodies do not match. New `CompareJSON` utility in `pkg/verify` handles the comparison with detailed diff output on mismatch.

### Fixed

- **gRPC descriptor load failure is now non-fatal** ([prov-2026-a9d51966](./provenance/prov-2026-a9d51966.yml)) — A missing or unreadable proto descriptor file previously crashed the runner at startup. The error is now logged as a warning and execution continues, allowing tests that do not rely on gRPC reflection to proceed normally.

- **Kafka resolver wiring in stable build** ([prov-2026-7c084083](./provenance/prov-2026-7c084083.yml)) — The Kafka mock resolver was not wired into the stable binary. Kafka `EXPECT` blocks now resolve correctly in both stable and beta builds. Dynamic timestamps removed from Kafka event payloads in example tests to prevent spurious match failures.

### Related Provenance Records

- [prov-2026-9e052576](./provenance/prov-2026-9e052576.yml) - Add request body comparison via WITH statements
- [prov-2026-a9d51966](./provenance/prov-2026-a9d51966.yml) - Fix gRPC descriptor load failure to be non-fatal
- [prov-2026-7c084083](./provenance/prov-2026-7c084083.yml) - Fix stable Kafka resolver wiring

## [3.0.1] - 2026-04-26

### Fixed

- **`associated_specs` enforcement incorrectly fires on open `brief` records** ([prov-2026-7b402f2f](./provenance/prov-2026-7b402f2f.yml)) — The `validateAssociatedSpecs` enforcement block (strict/warn/none) now skips `brief` records entirely. Brief records are structurally forbidden from carrying `associated_specs`, so the enforcement check previously always fired on open briefs with no valid resolution path.

## [3.0.0] - 2026-04-24

### Added

- **`bug` record type** ([prov-2026-9ffe68bb](./provenance/prov-2026-9ffe68bb.yml)) — New `type: bug` for correction and gap-fill records. A Bug must have exactly one of `supersedes` (when existing constraints are incorrect) or `extends` (when constraints are missing). Bug records may supersede a Blueprint or another Bug; all other cross-type supersessions remain errors.

- **`extends` field** ([prov-2026-9ffe68bb](./provenance/prov-2026-9ffe68bb.yml)) — New `extends` field on Bug records, pointing at the Blueprint or Bug whose constraint coverage this record supplements. Unlike `supersedes`, the target record is not replaced. `extends` and `supersedes` are mutually exclusive on Bug records.

- **Per-type field enforcement matrix** ([prov-2026-9ffe68bb](./provenance/prov-2026-9ffe68bb.yml)) — The linter now enforces type-specific field rules as always-on graph integrity checks: Brief records require `constraints`; Imprint records require `implements`; Brief and Bug records may not use `implements`; `extends` is not applicable on Brief, Blueprint, or Imprint records; Brief records may not carry `affected_scope`, `forbidden_scope`, or `associated_specs`; Imprint records may not carry `associated_traces` or `monitors`.

- **Imprint supersession same-parent constraint** ([prov-2026-9ffe68bb](./provenance/prov-2026-9ffe68bb.yml)) — When an Imprint supersedes another Imprint, both must share the same `implements` value (i.e., they must be in service of the same Blueprint). Mismatched parents are a lint error.

- **`RETURNS EMPTY` for gRPC expectations** ([prov-2026-8abe0e04](./provenance/prov-2026-8abe0e04.yml)) — gRPC `EXPECT` blocks now support `RETURNS EMPTY` to simulate methods that return `google.protobuf.Empty` (e.g. `SignalWorkflow`). The proxy sends the required 5-byte Length-Prefixed Message frame with a zero-length body, which gRPC/h2 clients require for a successful empty response.

### Fixed

- **gRPC empty-body response framing** ([prov-2026-8abe0e04](./provenance/prov-2026-8abe0e04.yml)) — The gRPC proxy skipped the DATA frame entirely when the protobuf response body was empty (0 bytes), violating the gRPC spec. Tonic, grpc-go, and other h2 clients classified this as a retryable transport error instead of a successful response. The proxy now always sends `encodeGRPCFrame(nil)` — a 5-byte frame with compression-flag 0 and length 0 — for every unary response including empty ones.

- **gRPC error trailers sent in initial HEADERS frame** ([prov-2026-8abe0e04](./provenance/prov-2026-8abe0e04.yml)) — `writeGRPCError` was setting `Grpc-Status` and `Grpc-Message` in the initial HEADERS frame rather than in a separate trailing HEADERS frame. The gRPC HTTP/2 spec requires these to appear as trailers. The proxy now declares trailer keys before `WriteHeader` and writes the values after, matching the success-path pattern.

- **gRPC proxy port mismatch in runner** ([prov-2026-8abe0e04](./provenance/prov-2026-8abe0e04.yml)) — The runner was passing `GRPC_PORT=50051` as a fixed default to service containers instead of the actual port the gRPC interceptor bound to. Services that resolved their upstream gRPC address from the environment variable were connecting to the wrong port.

### Related Provenance Records

- [prov-2026-9ffe68bb](./provenance/prov-2026-9ffe68bb.yml) - Per-type field enforcement matrix for provenance record tiers
- [prov-2026-8abe0e04](./provenance/prov-2026-8abe0e04.yml) - gRPC empty-body framing fix, error trailer fix, RETURNS EMPTY support

## [2.9.1] - 2026-04-23

### Added

- **Semantic SQL matching** ([prov-2026-ae2a4514](./provenance/prov-2026-ae2a4514.yml)) — New ORM-agnostic SQL routing system for MySQL and PostgreSQL. Instead of brittle literal SQL matching, write `ACCESSING_TABLES [table1, table2]` to route a mock by which tables a query touches. Optional verification clauses (`VERIFY_OPERATION`, `VERIFY_WHERE_COLUMNS`, `VERIFY_WHERE`, `VERIFY_WRITTEN_VALUES`) assert the DML type and specific column values without caring about query shape, column order, aliases, or ORM dialect. `CALL N` provides an ordered tiebreaker when two mocks on the same table set are otherwise indistinguishable. A specificity-wins algorithm ensures the most constrained mock is always preferred. For PostgreSQL, the proxy resolves actual Bind message parameter values (`$1` → `42`) so `VERIFY_WHERE id: 42` matches parameterized and inline queries identically. The legacy `USING_SQL` / `USING_SQL_CONTAINS` keywords remain functional but are deprecated.

- **`RETURNS ERROR` / `RETURNS HTTP:NNN` for HTTP expectations** ([prov-2026-6165b458](./provenance/prov-2026-6165b458.yml)) — HTTP `EXPECT` blocks can now simulate dependency failures without adding mock payloads. `RETURNS ERROR` causes the HTTP proxy to close the TCP connection immediately so the service sees an `io.EOF`. `RETURNS ERROR <code>` does the same and labels the failure for test readability. `RETURNS HTTP:NNN` instructs the proxy to respond with an explicit HTTP status code (useful for rate-limit, auth, gateway timeout scenarios); combine with `WITH {{file}}` to include a response body.

### Fixed

- **PostgreSQL type OID heuristics caused schema-mismatch errors** ([prov-2026-6165b458](./provenance/prov-2026-6165b458.yml)) — The PostgreSQL proxy inferred column types from name patterns (`id` → `INT4`, `*_at` → `TIMESTAMPTZ`). When the actual schema used `UUID` for `*_id` columns or `TIMESTAMP WITHOUT TIME ZONE` for `*_at` columns the driver received binary-encoded data with the wrong OID and raised a type-mismatch error. The proxy now always sends `TEXT` (OID 25) in text format for all mock result columns, which every PostgreSQL driver can decode regardless of the schema's actual type.

### Related Provenance Records

- [prov-2026-ae2a4514](./provenance/prov-2026-ae2a4514.yml) - Semantic SQL matching (ACCESSING_TABLES, VERIFY_ clauses, CALL N, specificity-wins)
- [prov-2026-6165b458](./provenance/prov-2026-6165b458.yml) - RETURNS ERROR / RETURNS HTTP:NNN + PostgreSQL OID fix

## [2.8.7] - 2026-04-23

### Added

- **gRPC upstream passthrough** ([prov-2026-ce3ba028](./provenance/prov-2026-ce3ba028.yml)) — Unmocked gRPC calls are now forwarded to a real upstream backend via HTTP/2 reverse proxy when a `type: grpc` dependency is configured with a `host` and `port`. This lets you mix mocked and real gRPC backends in a single test — methods you `EXPECT` are intercepted; all others are forwarded transparently. When no upstream is configured, unmocked calls return `UNIMPLEMENTED` as before.

- **gRPC content-type echo** ([prov-2026-ce3ba028](./provenance/prov-2026-ce3ba028.yml)) — The gRPC proxy now echoes the request's `Content-Type` in its response instead of hardcoding `application/grpc+json`. Requests with `application/grpc` content-type receive binary protobuf responses; `application/grpc+json` (the default) receives JSON. Falls back to `application/grpc+json` when no content-type is specified.

- **Protobuf descriptor mocks for gRPC** ([prov-2026-ce3ba028](./provenance/prov-2026-ce3ba028.yml)) — New `grpc_descriptor_set` field in `.linespec.yml` (at both service-level and per-dependency scope) allows you to write `RETURNS` payloads as JSON and have the proxy convert them to binary protobuf on the wire. Requires a compiled `FileDescriptorSet` (`.pb` file) produced by `protoc --descriptor_set_out`. The runner merges all descriptor sets before passing them to the proxy container.

- **`type: grpc` dependency support** ([prov-2026-ce3ba028](./provenance/prov-2026-ce3ba028.yml)) — The `dependencies` section in `.linespec.yml` now accepts `type: grpc` entries alongside `type: http`. Each gRPC dependency gets its own network alias, upstream address, and optional descriptor set override.

### Related Provenance Records

- [prov-2026-ce3ba028](./provenance/prov-2026-ce3ba028.yml) - gRPC upstream passthrough, content-type echo, protobuf descriptor mocks

## [2.8.6] - 2026-04-21

### Fixed

- **MySQL proxy container fails with "argument list too long" on large schemas** ([prov-2026-f0384a57](./provenance/prov-2026-f0384a57.yml)) — The MySQL proxy was receiving the full base64-encoded database schema as a `--schema-data` CLI argument. On large schemas (many tables/columns), the combined argument size exceeds the Linux kernel's `ARG_MAX` limit (~2MB), causing `exec /app/linespec: argument list too long`. The schema JSON is now written to a file in the per-test temp directory (already bind-mounted into the proxy container) and passed via `--schema-file` instead. The `--schema-data` flag is retained as a fallback for backward compatibility.

## [2.8.5] - 2026-04-20

### Fixed

- **Homebrew post_install always shows warning even on success** ([prov-2026-1687368b](./provenance/prov-2026-1687368b.yml)) — Homebrew's `Formula#system` raises `BuildError` on failure and returns `nil` on success. The formula used `unless system(...)` which evaluates to `unless nil` — always truthy — so the "Could not build" warning fired on every install regardless of outcome. The formula now uses `rescue BuildError` which is the correct Homebrew idiom: the warning only appears when `linespec build` actually fails.

## [2.8.4] - 2026-04-20

### Fixed

- **`linespec build` fails silently during Homebrew post_install on macOS** ([prov-2026-c8585133](./provenance/prov-2026-c8585133.yml)) — Homebrew's `post_install` hook does not source the user's shell dotfiles, so `DOCKER_HOST` (set by Docker Desktop's shell integration) is absent. With the legacy builder (`DOCKER_BUILDKIT=0`), the Docker CLI only falls back to `/var/run/docker.sock` and does not probe `~/.docker/run/docker.sock` — the default socket path for Docker Desktop on macOS. `linespec build` now probes both locations on macOS and injects `DOCKER_HOST` into the docker subprocess environment when it is not already set.

## [2.8.3] - 2026-04-20

### Fixed

- **`linespec build` fails during Homebrew post_install on macOS** ([prov-2026-336ba2a8](./provenance/prov-2026-336ba2a8.yml)) — Docker BuildKit writes an activity timestamp to `~/.docker/buildx/activity/` on every build. Homebrew's post_install hook runs in a restricted environment where this write is blocked with "operation not permitted". `linespec build` now sets `DOCKER_BUILDKIT=0` for the `docker build` subprocess, using the classic builder which does not touch that path.

## [2.8.2] - 2026-04-20

### Fixed

- **`linespec build` go install cross-compile on Homebrew installs** ([prov-2026-04141669](./provenance/prov-2026-04141669.yml)) — When the source repo is not present (e.g. Homebrew install), `linespec build` falls back to `go install` to cross-compile a Linux binary. Setting `GOBIN` caused Go to reject cross-compiled installs with "cannot install cross-compiled binaries when GOBIN is set". The fix uses a temporary `GOPATH` instead and reads the binary from `$GOPATH/bin/linux_<GOARCH>/`.

## [2.8.1] - 2026-04-20

### Fixed

- **`linespec build` cross-compile on non-Linux hosts** ([prov-2026-a6456485](./provenance/prov-2026-a6456485.yml)) — On macOS and Windows, `linespec build` was copying the host binary (Mach-O/PE format) directly into the Alpine Docker image, causing `exec format error` when proxy sidecar containers tried to start. MySQL, MongoDB, and Redis proxies silently failed as a result. `linespec build` now detects non-Linux hosts and cross-compiles a Linux ELF binary using `go build GOOS=linux` before building the image, finding the source root by walking up from the current directory.

## [2.8.0] - 2026-04-20

### Added

- **`linespec build` command** ([prov-2026-0e5c82e4](./provenance/prov-2026-0e5c82e4.yml)) — Builds the `linespec:latest` Docker image from the installed binary. Protocol proxy sidecars require this image to run. The Homebrew formula now calls `linespec build` automatically via `post_install`; if Docker is not running at install time, a warning is shown and the image can be built manually by running `linespec build`.

## [2.7.0] - 2026-04-18

### Added

- **Auto-pull Docker images when not found locally** ([prov-2026-6c7c0ab9](./provenance/prov-2026-6c7c0ab9.yml)) — `StartContainer` automatically pulls the image before retrying when "No such image" error is detected, so test runs in fresh CI environments succeed without requiring pre-pulled images.

- **CI Docker image build step** ([prov-2026-a7b8e1c2](./provenance/prov-2026-a7b8e1c2.yml)) — The `linespec:latest` Docker image is now built in CI before running any linespec test commands, matching the expected environment for proxy containers.

- **CI example service image builds** ([prov-2026-ff23779d](./provenance/prov-2026-ff23779d.yml)) — Each example service image (user-service:latest, todo-api:latest, etc.) is built immediately before its corresponding test suite step in CI, so tests use locally-built images instead of expecting pre-pulled ones.

- **Spinner suppression in non-TTY environments** ([prov-2026-778d2362](./provenance/prov-2026-778d2362.yml)) — The spinner animation is suppressed in non-TTY environments (CI, piped output), printing the message once as a plain line to avoid log bloat.

### Fixed

- **HTTP proxy ExposedPorts missing** ([prov-2026-1a379777](./provenance/prov-2026-1a379777.yml)) — HTTP proxy container config now sets ExposedPorts for the sidecar port (19081/tcp), matching the pattern used by all other proxy types. Without this, Docker Engine on Linux does not populate NetworkSettings.Ports, causing HTTP mock verification to fail with "was never called" even when the proxy correctly served the request.

- **Kafka readiness check uses wrong port** ([prov-2026-baaf7cef](./provenance/prov-2026-baaf7cef.yml)) — The Kafka readiness check was polling for port 29092/tcp, but the cp-kafka Dockerfile only EXPOSEs port 9092. Additionally, 29092 binds to the container hostname, not 0.0.0.0, so Docker's NAT cannot proxy it. Now correctly uses port 9092/tcp (PLAINTEXT_HOST) which IS exposed and binds on all interfaces.

- **Infra container exit detection and log capture** ([prov-2026-1e5cf179](./provenance/prov-2026-1e5cf179.yml)) — `waitForContainerPort` now detects container exit inside the poll loop and fails immediately with the container's stdout/stderr logs captured, so CI output shows the actual crash reason instead of a 30-second timeout.

- **CI image pull and Kafka cluster ID fixes** ([prov-2026-86f3243a](./provenance/prov-2026-86f3243a.yml)) — Two CI fixes: (1) Explicit image pull is now done only for known infrastructure images (MySQL, Kafka, PostgreSQL, MongoDB), not app/migration containers which should fail fast if not built. (2) Kafka CLUSTER_ID now uses a valid base64url UUID format required by cp-kafka 7.x+ in KRaft mode.

### Related Provenance Records

- [prov-2026-6c7c0ab9](./provenance/prov-2026-6c7c0ab9.yml) - Auto-pull Docker images when not found locally
- [prov-2026-a7b8e1c2](./provenance/prov-2026-a7b8e1c2.yml) - Build linespec Docker image in CI before example tests
- [prov-2026-ff23779d](./provenance/prov-2026-ff23779d.yml) - Build example service images in CI before each test suite
- [prov-2026-778d2362](./provenance/prov-2026-778d2362.yml) - Suppress spinner animation in non-TTY environments
- [prov-2026-1a379777](./provenance/prov-2026-1a379777.yml) - Fix HTTP proxy ExposedPorts missing — sidecar port unreachable in CI
- [prov-2026-baaf7cef](./provenance/prov-2026-baaf7cef.yml) - Fix Kafka readiness check to use exposed port 9092
- [prov-2026-1e5cf179](./provenance/prov-2026-1e5cf179.yml) - Diagnose infra container failures: exit detection and log capture in port wait
- [prov-2026-86f3243a](./provenance/prov-2026-86f3243a.yml) - Fix CI: explicit infrastructure image pull, valid Kafka cluster ID

---

## [2.6.0] - 2026-04-18

### Added

- **VARS block typed constraints** ([prov-2026-a8304063](./provenance/prov-2026-a8304063.yml)) — The `VARS` block now accepts inline `key=value` constraints after the type token. `integer` supports `min=N max=N` for range-bounded generation. `string` supports `length=N`, `charset=alpha|alphanumeric|numeric|hex|uppercase`, and `pattern=[A-Z]{3}[0-9]{4}`-style character-class patterns (stdlib only, no new dependencies). A new `enum` type accepts `values=a,b,c` and picks randomly at test time. All existing VARS lines with no constraints continue to work exactly as before. Unknown constraint keys for a type produce a parse-time error.

- **CI workflow** ([prov-2026-d019b9ee](./provenance/prov-2026-d019b9ee.yml)) — A new `.github/workflows/ci.yml` runs on every push to `main` and every pull request. Two parallel jobs: `unit-tests` runs `go test ./...` across all packages; `example-tests` builds the binary and runs all four proven example suites (user, todo, order, multi-db) as separate named steps against live Docker containers.

### Fixed

- **SARIF lint output produced invalid JSON when example services are present** ([prov-2026-d3b0abe0](./provenance/prov-2026-d3b0abe0.yml)) — `linespec provenance lint --format sarif` without `--config` discovered all `.linespec.yml` files in the repo tree and wrote one complete SARIF JSON document per file to stdout, producing concatenated JSON that GitHub Code Scanning rejected. The multi-config loop is now skipped when format is `sarif`; only the root provenance records are linted. Human and JSON formats are unaffected. Also bumped `codeql-action` from v3 to v4.

### Related Provenance Records

- [prov-2026-a8304063](./provenance/prov-2026-a8304063.yml) - Extend VARS block with typed constraints
- [prov-2026-d019b9ee](./provenance/prov-2026-d019b9ee.yml) - Add CI workflow for unit tests and example linespec suites
- [prov-2026-d3b0abe0](./provenance/prov-2026-d3b0abe0.yml) - Fix SARIF lint output multi-config bug

---

## [2.5.0] - 2026-04-17

### Added

- **`install-skills` command installs both Claude Code skills** ([prov-2026-5a3f28a5](./provenance/prov-2026-5a3f28a5.yml)) — `linespec provenance install-skill` has been renamed to `install-skills` (plural) and now installs both the `provenance` skill and the new `linespec-testing` skill in one command. Existing skill directories are overwritten silently. The `--name` flag is removed since both skills are always installed together.

- **`linespec-testing` Claude Code skill** ([prov-2026-5a3f28a5](./provenance/prov-2026-5a3f28a5.yml)) — A new embedded skill covering how to run, write, and debug LineSpec integration tests. Includes the mental model, CLI usage, DSL structure, all channel types, variable interpolation, payload files, and a debugging guide for common failure patterns. Invoke with `/linespec-testing` in Claude Code after running `install-skills`.

### Related Provenance Records

- [prov-2026-5a3f28a5](./provenance/prov-2026-5a3f28a5.yml) - Rename install-skill to install-skills, add linespec-testing skill, overwrite existing

---

## [2.4.1] - 2026-04-17

### Fixed

- **Linter false positives for `sealed_at_sha` on superseded/deprecated records** ([prov-2026-755704af](./provenance/prov-2026-755704af.yml)) — The linter incorrectly warned when `sealed_at_sha` was present on `superseded` or `deprecated` records. Those terminal states can only be reached after a record has been implemented, so carrying a `sealed_at_sha` is correct and expected. Only `open` records are now flagged.

### Related Provenance Records

- [prov-2026-755704af](./provenance/prov-2026-755704af.yml) - Fix linter to allow sealed_at_sha on superseded and deprecated records

---

## [2.4.0] - 2026-04-17

### Added

- **VARS block for typed variable declarations** ([prov-2026-935c1716](./provenance/prov-2026-935c1716.yml)) — Linespecs can now declare an explicit `VARS` block before `RECEIVE` to pre-generate typed variables before any payload is loaded. Supported types: `uuid` (RFC 4122 v4), `integer` (1–99999), `string` (random alphanumeric). Variables without a VARS declaration continue to use name-based inference (UUID suffix → uuid, otherwise string).

- **Failure output includes resolved variable map** ([prov-2026-935c1716](./provenance/prov-2026-935c1716.yml)) — When a test fails, the error message now appends the full resolved variable map showing each variable name, type, and generated value. Makes it easy to reproduce failures by knowing exactly what values were used.

- **Channel-aware integer type correction** ([prov-2026-935c1716](./provenance/prov-2026-935c1716.yml)) — Variables declared with `integer` type in the VARS block are rendered as JSON numbers (not quoted strings) in HTTP response payloads. VarTypes flow through the registry JSON so proxy containers receive them alongside variable values.

### Related Provenance Records

- [prov-2026-935c1716](./provenance/prov-2026-935c1716.yml) - Add VARS block, typed variable generation, failure variable output, and channel-aware formatting

## [2.3.4] - 2026-04-17

### Added

- **Multi-pack aware `lint` and `check` CLI commands** ([prov-2026-423a3a9a](./provenance/prov-2026-423a3a9a.yml)) — `linespec provenance lint` and `linespec provenance check` now auto-discover all `.linespec.yml` files under the working directory and run for each config, matching the behavior of the git hooks added in v2.3.3. Running from a packwerk monorepo root without `-c` no longer silently uses only the root config. For `check --staged`, non-root configs are skipped when no staged files exist under their directory, preventing spurious `commit_tag_required` failures from unrelated packs.

### Related Provenance Records

- [prov-2026-423a3a9a](./provenance/prov-2026-423a3a9a.yml) - Multi-pack aware lint and check CLI commands

## [2.3.3] - 2026-04-17

### Fixed

- **PostgreSQL extended query protocol: binary UUID encoding** ([prov-2026-1aac1dc7](./provenance/prov-2026-1aac1dc7.yml)) — lib/pq requests binary format (code=1) for UUID columns in its Bind message. The proxy now encodes UUID values as 16 raw bytes when the client requests binary format, and generates proper RFC 4122 v4 UUIDs for interpolation variables named `*_UUID`.

- **PostgreSQL text-mode timestamp format** ([prov-2026-1aac1dc7](./provenance/prov-2026-1aac1dc7.yml)) — lib/pq's internal timestamp parser expects PostgreSQL wire format (`2006-01-02 15:04:05`, space separator). Payload timestamps in ISO 8601 format (`T` separator) would fail with `expected '32' at position 10; got '84'`. Payload files should use space-separated timestamps.

- **JSONB/JSON columns not JSON-encoded in DataRow** ([prov-2026-1aac1dc7](./provenance/prov-2026-1aac1dc7.yml)) — Slice and map values from YAML payloads were formatted with `%v`, producing non-JSON output that `json.Unmarshal` in the service handler would silently reject. They are now properly JSON-marshaled.

### Related Provenance Records

- [prov-2026-1aac1dc7](./provenance/prov-2026-1aac1dc7.yml) - Fix PostgreSQL proxy binary encoding and extended query protocol for lib/pq

## [2.3.2] - 2026-04-17

### Fixed

- **Payload variable consistency across hot-reload** ([prov-2026-59110ab7](./provenance/prov-2026-59110ab7.yml)) — Variables declared in payload files were not being pre-scanned before the resolver was built, causing inconsistent values between the first and subsequent test runs when hot-reload was active.

### Related Provenance Records

- [prov-2026-59110ab7](./provenance/prov-2026-59110ab7.yml) - Fix variable consistency: pre-scan payloads and rebuild resolver on hot-reload

## [2.3.1] - 2026-04-16

### Added

- **`RETURNS` payload interpolation in proxy containers** ([prov-2026-4e4db58e](./provenance/prov-2026-4e4db58e.yml)) — `RETURNS {{payload.yaml}}` now supports `${VAR}` interpolation inside payload files, consistent with how HTTP response payloads work.

### Related Provenance Records

- [prov-2026-4e4db58e](./provenance/prov-2026-4e4db58e.yml) - Implement RETURNS payload interpolation in proxy containers

## [2.3.0] - 2026-04-16

### Added

- **`USING_SQL_CONTAINS` DSL keyword** ([prov-2026-556ce2a7](./provenance/prov-2026-556ce2a7.yml)) - New SQL matching mode for `EXPECT READ/WRITE:MYSQL` and `EXPECT READ/WRITE:POSTGRESQL` that performs a substring match against the normalized query instead of requiring an exact match. Use `USING_SQL_CONTAINS` when the ORM or driver may add clauses (e.g. `ORDER BY`, `LIMIT`, prepared-statement placeholders) that make exact matching fragile. The fragment is normalized with the same rules as `USING_SQL` (backticks stripped, whitespace collapsed, `table.*` → `*`). Both keywords can appear in the same spec file.

### Changed

- **`USING_SQL` is now exact-match only** ([prov-2026-556ce2a7](./provenance/prov-2026-556ce2a7.yml)) - `USING_SQL` performs an exact equality check after normalization. The previous fuzzy fallback (where any SELECT matched a `READ:MYSQL` mock regardless of SQL) has been removed. Tests that relied on the fallback may now fail — migrate those mocks to `USING_SQL_CONTAINS` with an appropriate stable fragment.

### Fixed

- **`USING_SQL_CONTAINS` hit tracking across Docker boundary** ([prov-2026-fe2348ba](./provenance/prov-2026-fe2348ba.yml)) - Proxies were overwriting `mock.SQL` with the actual query text for SQLContains mocks, causing the verification sidecar to emit a wrong key for `/verify` hit counts. Fixed by guarding the assignment: `if mock.SQL == "" && mock.SQLContains == "" { mock.SQL = query }`.

- **Example linespecs updated to reflect real ORM/driver SQL** - Several example linespecs that relied on the old fuzzy fallback were updated to use `USING_SQL_CONTAINS` with accurate stable fragments:
  - `examples/user-linespecs/get_user_success.linespec` — Rails `find` generates `WHERE users.id = ?`
  - `examples/user-linespecs/create_user_already_exists.linespec` — Rails `exists?` generates `SELECT 1 AS one FROM users WHERE...`
  - `examples/todo-linespecs/get_todo_success.linespec`, `delete_todo_success.linespec`, `update_todo_success.linespec` — Rails 7 `.where(...).first` adds `ORDER BY id ASC`
  - `examples/multi-db-linespecs/create_order_success_missing_expect_test.linespec` — Go MySQL driver sends `COM_STMT_PREPARE` with `?` placeholders

### Related Provenance Records

- [prov-2026-556ce2a7](./provenance/prov-2026-556ce2a7.yml) - Add USING_SQL_CONTAINS DSL keyword and release v2.3.0
- [prov-2026-782ae9dd](./provenance/prov-2026-782ae9dd.yml) - Fix multi-db-linespecs USING_SQL for prepared statement SQL
- [prov-2026-b5cbfedf](./provenance/prov-2026-b5cbfedf.yml) - Fix todo-linespecs SQL mocks to use USING_SQL_CONTAINS
- [prov-2026-543305e3](./provenance/prov-2026-543305e3.yml) - Fix create_user_already_exists linespec
- [prov-2026-fe2348ba](./provenance/prov-2026-fe2348ba.yml) - Fix USING_SQL_CONTAINS hit tracking

## [2.2.0] - 2026-04-16

### Added

- **`install-skill` CLI command** ([prov-2026-940803d3](./provenance/prov-2026-940803d3.yml)) - `linespec provenance install-skill` installs the provenance skill directly into a Claude Code skills directory without requiring the repo to be cloned. Supports `--name` to override the slash command name and `--path` to override the target directory (default: `.claude/skills`). The skill content is embedded in the binary, so it works out of the box for users who only have `linespec` installed.

### Related Provenance Records

- [prov-2026-940803d3](./provenance/prov-2026-940803d3.yml) - Add install-skill CLI command for provenance skill

## [2.1.0] - 2026-04-16

### Added

- **Claude Code provenance skill** ([prov-2026-5f02e318](./provenance/prov-2026-5f02e318.yml)) - Introduces `skills/provenance/SKILL.md`, a reusable Claude Code skill that encodes the full provenance record workflow. Install it into any repo with the new `scripts/install-skill` script: `install-skill <target-path> [name]`.

### Changed

- **Pre-commit validation workflow** ([prov-2026-5f02e318](./provenance/prov-2026-5f02e318.yml)) - The provenance workflow now requires explicit pre-commit validation instead of an informal user-review gate. Run `linespec provenance lint` and `linespec provenance check` before every create or complete commit; run `linespec provenance check --staged` before each intra-lifecycle implementation commit. Updated in CLAUDE.md, PROVENANCE_RECORDS.md, and the documentation site.

### Related Provenance Records

- [prov-2026-5f02e318](./provenance/prov-2026-5f02e318.yml) - Add Claude Code provenance skill and pre-commit validation workflow

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
