# Changelog

All notable changes to LineSpec will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
