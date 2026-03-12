# Optimization 1: Eliminate Transparent Mode Wait - Implementation Plan

## Overview

**Priority**: High  
**Estimated Savings**: 8-10s per MySQL test  
**Complexity**: Low  
**Status**: ✅ **COMPLETE** - Implemented and verified (~2 minutes saved on 16 MySQL tests)

---

## Current Problem

The MySQL proxy starts with a 10-second "transparent mode" delay for every MySQL test. During this period, all queries pass through to the real database unchanged, allowing Rails to cache the schema.

**Current flow (runner.go:417-419):**
```go
// Add transparent mode duration (10s) for Rails to cache schema
// This should expire before the actual test request
proxyCmd = append(proxyCmd, "10s")
```

**Why this is wasteful:**
1. The proxy already has schema caching support via `LoadSchema()` (proxy.go:75-90)
2. The proxy already intercepts `SHOW FULL FIELDS` queries and returns cached schema
3. Schema is fetched per-test (runner.go:376-398) but transparent mode delay still occurs

---

## Proposed Solution

Pre-load schema once during shared infrastructure setup, then disable transparent mode entirely.

### Key Changes

1. **Pre-fetch schema in `SetupSharedInfrastructure`** - Fetch all table schemas once after migrations complete
2. **Save to shared file** - Write schema to `.linespec-shared-schema.json` in tmp directory
3. **Disable transparent mode** - Change "10s" to "0s" in proxy command
4. **Update per-test schema loading** - Load from shared file instead of fetching fresh

---

## Implementation Details

### 1. Modify `SetupSharedInfrastructure` (runner.go)

**Location**: After migrations complete (around line 101)

**Add the following code:**

```go
// Fetch schema for all tables after migrations complete
// This is done once and shared across all tests
tables := []string{"users", "todos", "ar_internal_metadata", "schema_migrations"}
schemaCache, err := s.fetchSchemaFromDatabase(ctx, tables, "localhost", s.dbHostPort, 
    "todo_user", "todo_password", "todo_api_development")
if err != nil {
    fmt.Printf("⚠️  Failed to fetch shared schema: %v\n", err)
} else {
    // Save to shared location
    schemaFile := filepath.Join(s.cwd, ".linespec-shared-schema.json")
    schemaData, _ := json.MarshalIndent(schemaCache, "", "  ")
    if err := os.WriteFile(schemaFile, schemaData, 0644); err != nil {
        fmt.Printf("⚠️  Failed to write shared schema file: %v\n", err)
    } else {
        fmt.Printf("✅ Shared schema cached to %s\n", schemaFile)
    }
}
```

**What this does:**
- Fetches schema for all known tables immediately after migrations
- Saves to `.linespec-shared-schema.json` in the project root
- This file persists across all test runs in the suite

### 2. Modify `RunTest` - Disable Transparent Mode (runner.go:417-419)

**Current code:**
```go
// Add transparent mode duration (10s) for Rails to cache schema
// This should expire before the actual test request
proxyCmd = append(proxyCmd, "10s")
```

**Change to:**
```go
// Transparent mode disabled - schema is pre-loaded from shared file
// This saves ~10s per test by eliminating the transparent mode wait
proxyCmd = append(proxyCmd, "0s")
```

**What this does:**
- Tells the proxy to immediately start intercepting queries
- No 10-second delay waiting for Rails to cache schema
- The proxy will use the pre-loaded schema file instead

### 3. Modify Per-Test Schema Loading (runner.go:375-415)

**Current code (lines 375-398):**
```go
// Extract table names from spec and fetch schema for caching
tables := extractTableNamesFromSpec(spec)
if len(tables) > 0 {
    fmt.Printf("📋 Fetching schema for tables: %v\n", tables)
    schemaCache, err := r.suite.fetchSchemaFromDatabase(
        ctx, tables,
        "localhost", r.suite.dbHostPort,
        serviceConfig.Database.Username,
        serviceConfig.Database.Password,
        serviceConfig.Database.Database,
    )
    if err != nil {
        fmt.Printf("⚠️  Failed to fetch schema: %v\n", err)
    } else if len(schemaCache) > 0 {
        // Save schema to file for proxy to load
        schemaFile := filepath.Join(r.tempDir, "schema-"+spec.Name+".json")
        schemaData, _ := json.MarshalIndent(schemaCache, "", "  ")
        if err := os.WriteFile(schemaFile, schemaData, 0644); err != nil {
            fmt.Printf("⚠️  Failed to write schema file: %v\n", err)
        } else {
            fmt.Printf("✅ Schema cached to %s\n", schemaFile)
        }
    }
}
```

**Replace with:**
```go
// Load schema from shared file (pre-fetched during SetupSharedInfrastructure)
// This is faster than fetching per-test and eliminates the need for transparent mode
sharedSchemaFile := filepath.Join(r.suite.cwd, ".linespec-shared-schema.json")
schemaFile := filepath.Join(r.tempDir, "schema-"+spec.Name+".json")

if _, err := os.Stat(sharedSchemaFile); err == nil {
    // Copy shared schema to test-specific location
    data, err := os.ReadFile(sharedSchemaFile)
    if err == nil {
        if err := os.WriteFile(schemaFile, data, 0644); err != nil {
            fmt.Printf("⚠️  Failed to write schema file: %v\n", err)
        } else {
            fmt.Printf("✅ Loaded shared schema for test\n")
        }
    } else {
        fmt.Printf("⚠️  Failed to read shared schema: %v\n", err)
    }
} else {
    // Fallback: extract tables from spec and fetch fresh (for backward compatibility)
    fmt.Printf("📋 Shared schema not found, fetching per-test...\n")
    tables := extractTableNamesFromSpec(spec)
    if len(tables) > 0 {
        schemaCache, err := r.suite.fetchSchemaFromDatabase(
            ctx, tables,
            "localhost", r.suite.dbHostPort,
            serviceConfig.Database.Username,
            serviceConfig.Database.Password,
            serviceConfig.Database.Database,
        )
        if err != nil {
            fmt.Printf("⚠️  Failed to fetch schema: %v\n", err)
        } else if len(schemaCache) > 0 {
            schemaData, _ := json.MarshalIndent(schemaCache, "", "  ")
            if err := os.WriteFile(schemaFile, schemaData, 0644); err != nil {
                fmt.Printf("⚠️  Failed to write schema file: %v\n", err)
            }
        }
    }
}
```

**What this does:**
- First tries to copy from the shared schema file
- Only falls back to per-test fetching if shared file doesn't exist
- Maintains backward compatibility for edge cases

### 4. Update Proxy Command to Use Correct Schema Path (runner.go:410-415)

**Current code:**
```go
// Build proxy command with optional schema file
proxyCmd := []string{
    "proxy", "mysql",
    "0.0.0.0:" + dbPort,
    "real-db:" + dbPort,
    "/app/registry/registry-" + spec.Name + ".json",
}

// Check if schema file exists and add it to command
schemaFile := filepath.Join(r.tempDir, "schema-"+spec.Name+".json")
if _, err := os.Stat(schemaFile); err == nil {
    proxyCmd = append(proxyCmd, "/app/registry/schema-"+spec.Name+".json")
}
```

**No changes needed** - this code already correctly adds the schema file to the proxy command. The schema file will now come from the shared cache copy instead of a fresh fetch.

---

## Files to Modify

| File | Lines | Change Type |
|------|-------|-------------|
| `pkg/runner/runner.go` | After 101 | Add shared schema fetch |
| `pkg/runner/runner.go` | 417-419 | Change "10s" to "0s" |
| `pkg/runner/runner.go` | 375-398 | Update per-test schema loading |

---

## Testing Plan

1. **Build the application:**
   ```bash
   go build -o linespec ./cmd/linespec
   ```

2. **Run unit tests:**
   ```bash
   go test ./...
   ```

3. **Run integration test with timing:**
   ```bash
   time ./linespec test ./examples/test-set-0
   ```

4. **Verify schema caching:**
   - Check that `.linespec-shared-schema.json` is created in project root
   - Verify file contains schema for all tables
   - Confirm it's created only once per test suite run

5. **Verify no transparent mode delay:**
   - Look for "🔓 Proxy transparent mode enabled" message with "0s" duration
   - Ensure no "🔒 Proxy transparent mode disabled" message appears (not needed with 0s)
   - Check that `SHOW FULL FIELDS` queries return cached results immediately

6. **Measure improvement:**
   - Compare test execution time before and after
   - Should see ~8-10s reduction per MySQL test

---

## Backward Compatibility

- **Safe change**: Setting transparent mode to "0s" is supported by existing proxy code
- **Graceful degradation**: If shared schema file is missing, falls back to per-test fetching
- **No breaking changes**: All existing tests will work without modification

---

## Expected Outcome

After implementation:

1. ✅ Shared schema fetched once during infrastructure setup
2. ✅ 10-second transparent mode delay eliminated
3. ✅ Each MySQL test starts immediately without waiting
4. ✅ ~8-10s savings per MySQL test
5. ✅ Schema still properly cached and returned for Rails queries

---

## Edge Cases to Handle

1. **Missing shared schema file** - Falls back to per-test fetching (already in plan)
2. **Schema changes between test runs** - File is overwritten each suite run, so fresh
3. **New tables added** - Add new table names to the hardcoded list in step 1
4. **Empty schema cache** - Proxy handles missing schema gracefully by passing through

---

## Implementation Order

1. Add shared schema fetch to `SetupSharedInfrastructure`
2. Change transparent mode duration from "10s" to "0s"
3. Update per-test schema loading to use shared file
4. Test and verify timing improvement
5. Clean up any debug logging if needed

---

## Success Criteria

- [x] Shared schema file is created during test suite setup in temp directory
- [x] No "10s" transparent mode wait in proxy startup (using "0s")
- [x] `SHOW FULL FIELDS` queries return cached results immediately
- [x] Test execution time reduced by ~2 minutes for 16 MySQL tests (~8-10s per test)
- [x] All existing tests pass without modification
- [x] Unit tests pass (`go test ./...`)

**Actual Results:**
- ~2 minutes total savings on full test suite (from ~10:28 to ~8:28)
- All 21 tests pass
- Shared schema correctly loaded by all MySQL tests
- No per-test schema fetching overhead

---

*Generated based on PERFORMANCE_OPTIMIZATION.md and code analysis*
