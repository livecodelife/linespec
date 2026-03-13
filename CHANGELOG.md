# Changelog

All notable changes to LineSpec will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
