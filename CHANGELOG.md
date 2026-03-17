# Changelog

All notable changes to LineSpec will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
