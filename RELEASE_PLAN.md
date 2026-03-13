# LineSpec v1.0.0 Release Plan

This document outlines the complete plan for releasing LineSpec v1.0.0 with Provenance Records as the stable feature and LineSpec Integration Testing as beta.

---

## Release Overview

**Version:** v1.0.0  
**Primary Feature:** Provenance Records (Stable)  
**Beta Feature:** LineSpec Integration Testing  
**Repository:** https://github.com/livecodelife/linespec

---

## Distribution Strategy

### 1. GitHub Releases (via GoReleaser)

**Stable Releases:**
- Tag format: `v1.0.0`, `v1.1.0`, etc.
- Includes: Provenance Records functionality only
- Platforms: macOS (arm64, amd64), Linux (amd64, arm64), Windows (amd64)

**Beta Releases:**
- Tag format: `v1.0.0-beta.1`, `v1.1.0-beta.1`, etc.
- Includes: Provenance Records + LineSpec Testing + Proxy commands
- Marked as pre-release on GitHub

**Installation:**
```bash
# Download from GitHub Releases page
# Or use go install
go install github.com/livecodelife/linespec/cmd/linespec@v1.0.0

# Beta
go install -tags beta github.com/livecodelife/linespec/cmd/linespec@v1.0.0-beta.1
```

### 2. Homebrew (Personal Tap)

**Repository:** `github.com/livecodelife/homebrew-linespec`

**Formulas:**
- `linespec` - Stable version (provenance only)
- `linespec-beta` - Beta version (all features)

**Installation:**
```bash
# Stable
brew tap livecodelife/linespec
brew install linespec

# Beta
brew install linespec-beta
```

---

## Technical Implementation

### Phase 1: Build Tag Infrastructure

**Files to Create:**

1. **cmd/linespec/main_stable.go**
   - Build tag: `//go:build !beta`
   - Contains: Provenance command only
   - Help text: Shows only provenance commands

2. **cmd/linespec/main_beta.go**
   - Build tag: `//go:build beta`
   - Contains: Provenance + test + proxy commands
   - Help text: Shows all commands

3. **pkg/proxy/***_beta.go files**
   - Stub files with beta build tag
   - Each proxy package needs conditional compilation

4. **pkg/runner/runner_beta.go**
   - Build tag for test runner

5. **pkg/registry/registry_beta.go**
   - Build tag for registry

### Phase 2: Release Infrastructure

**Files to Create:**

1. **.goreleaser.yml**
   - Two build configurations (stable and beta)
   - Archive formats for all platforms
   - Checksum generation
   - Pre-release handling for beta tags

2. **.github/workflows/release.yml**
   - Trigger on version tags
   - Run tests
   - Execute GoReleaser
   - Create GitHub Release

3. **homebrew-linespec/** (separate repository)
   - `linespec.rb` - Stable formula
   - `linespec-beta.rb` - Beta formula
   - `README.md` - Installation instructions

### Phase 3: Documentation

**Files to Update:**

1. **PROVENANCE_RECORDS.md** (NEW)
   - Complete reference for provenance functionality
   - Command reference with examples
   - Schema documentation
   - Workflow guide
   - Configuration options
   - Git integration details

2. **README.md** (UPDATE)
   - New intro emphasizing Provenance Records
   - Clear installation instructions
   - Two-tier feature presentation
   - Version badges and status indicators

3. **AGENTS.md** (UPDATE)
   - New reading order
   - Clear status indicators
   - Separate sections for stable vs beta

4. **LINESPEC.md** (UPDATE)
   - Beta warning header
   - Build tag instructions

5. **go.mod** (UPDATE)
   - Change module path to `github.com/livecodelife/linespec`

---

## Feature Status

### Provenance Records (STABLE - v1.0.0)

**Commands:**
- `linespec provenance create` - Create new record
- `linespec provenance lint` - Validate records
- `linespec provenance status` - Show status
- `linespec provenance graph` - Render graph
- `linespec provenance check` - Check commit compliance
- `linespec provenance lock-scope` - Lock record scope
- `linespec provenance complete` - Mark as implemented
- `linespec provenance deprecate` - Deprecate record

**Features:**
- YAML-based structured format (prov-YYYY-NNN)
- Git integration for commit validation
- Scope enforcement (affected_scope, forbidden_scope)
- Graph relationships (supersedes, superseded_by)
- Configurable enforcement levels

### LineSpec Integration Testing (BETA)

**Commands:**
- `linespec test <path>` - Run .linespec tests
- `linespec proxy <type> ...` - Start protocol proxies

**Features:**
- DSL-based integration testing
- MySQL/PostgreSQL proxy interception
- HTTP mocking with DNS resolution
- Kafka message queue testing
- SQL verification clauses

**Beta Access:**
```bash
# Build from source
go build -tags beta -o linespec ./cmd/linespec

# Or install
go install -tags beta github.com/livecodelife/linespec/cmd/linespec@latest
```

---

## Module Path Changes

**Before:**
```
github.com/calebcowen/linespec
```

**After:**
```
github.com/livecodelife/linespec
```

**Impact:**
- All internal imports need updating
- Documentation needs new paths
- Homebrew formula uses new path
- GoReleaser config uses new path

---

## Timeline

1. **Phase 1: Build Infrastructure** (2-3 hours)
   - Create build tag files
   - Test both build configurations
   - Update go.mod paths

2. **Phase 2: Release Infrastructure** (2-3 hours)
   - Create .goreleaser.yml
   - Create GitHub Actions workflow
   - Set up homebrew tap repository

3. **Phase 3: Documentation** (3-4 hours)
   - Create PROVENANCE_RECORDS.md
   - Update README.md
   - Update AGENTS.md
   - Update LINESPEC.md

4. **Phase 4: Testing & Release** (1-2 hours)
   - Cross-platform builds
   - Homebrew formula testing
   - Create v1.0.0 tag
   - Create GitHub Release

**Total Estimated Time:** 8-12 hours

---

## Testing Checklist

### Build Tests
- [ ] `go build ./cmd/linespec` creates binary with provenance only
- [ ] `go build -tags beta ./cmd/linespec` creates binary with all features
- [ ] Help text matches build configuration
- [ ] Beta commands don't exist in stable build

### Cross-Platform Tests
- [ ] Darwin amd64 build succeeds
- [ ] Darwin arm64 build succeeds
- [ ] Linux amd64 build succeeds
- [ ] Linux arm64 build succeeds
- [ ] Windows amd64 build succeeds

### Release Tests
- [ ] GoReleaser creates all artifacts
- [ ] Checksums generated correctly
- [ ] GitHub Release created with notes
- [ ] Pre-release flag set for beta tags

### Homebrew Tests
- [ ] Stable formula installs correctly
- [ ] Beta formula installs correctly
- [ ] Both formulas point to correct binaries
- [ ] Uninstall works cleanly

---

## Release Notes Template (v1.0.0)

```markdown
## LineSpec v1.0.0 🎉

We're excited to announce the first stable release of LineSpec!

### What's New

**Provenance Records** is now stable and ready for production use:
- Structured YAML artifacts for documenting architectural decisions
- Git integration for commit validation
- Scope enforcement with glob/regex patterns
- Graph visualization of decision history
- CLI tooling for creation, linting, and management

### Installation

**Homebrew:**
\`\`\`bash
brew tap livecodelife/linespec
brew install linespec
\`\`\`

**Go:**
\`\`\`bash
go install github.com/livecodelife/linespec/cmd/linespec@v1.0.0
\`\`\`

### Beta Feature

LineSpec Integration Testing is available as a beta feature:
\`\`\`bash
go install -tags beta github.com/livecodelife/linespec/cmd/linespec@v1.0.0
\`\`\`

### Documentation

- [PROVENANCE_RECORDS.md](./PROVENANCE_RECORDS.md) - Complete provenance reference
- [README.md](./README.md) - Installation and quick start
- [LINESPEC.md](./LINESPEC.md) - Integration testing reference (beta)

### Assets

* linespec_1.0.0_darwin_amd64.tar.gz
* linespec_1.0.0_darwin_arm64.tar.gz
* linespec_1.0.0_linux_amd64.tar.gz
* linespec_1.0.0_linux_arm64.tar.gz
* linespec_1.0.0_windows_amd64.zip
* checksums.txt
```

---

## Provenance Record

See `provenance/prov-2026-010.yml` for the architectural decision record documenting this release strategy.

---

## Questions?

- Review the provenance record at `provenance/prov-2026-010.yml`
- Check documentation in `PROVENANCE_RECORDS.md` (after creation)
- Open an issue at https://github.com/livecodelife/linespec/issues
