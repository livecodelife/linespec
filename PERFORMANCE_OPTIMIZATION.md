# LineSpec Performance Optimization Implementation Plan

This document outlines the performance optimizations to speed up LineSpec test execution.

---

## Overview

Current bottlenecks identified:

| Issue | Location | Impact |
|-------|----------|--------|
| 10s transparent mode wait per MySQL test | runner.go:419 | High |
| WaitTCPInternal spawns Alpine container | orchestrator.go:110-169 | Medium |
| Hardcoded 2s Rails warmup sleep | runner.go:626 | Medium |
| 500ms sleep in verification | runner.go:672, 692 | Low |
| Sequential test execution | main.go:97-120 | High for suites |
| Per-test schema fetching | runner.go:376-398 | Low |

**Total potential savings**: 13-18s per test + 2-4x suite speedup with parallelism

---

## Optimization 1: Eliminate Transparent Mode Wait

**Priority**: High | **Estimated Savings**: 8-10s per MySQL test | **Complexity**: Low

### Problem

The MySQL proxy starts in "transparent mode" for 10 seconds to let Rails cache schema. This is wasteful because:
- The proxy already supports loading schema from file (`LoadSchema` at proxy.go:75-90)
- Schema is fetched per-test but not utilized properly before the transparent mode expires

### Solution

Pre-load schema in shared setup, eliminate transparent mode.

### Code Changes

1. **pkg/runner/runner.go** - Modify `SetupSharedInfrastructure` (around line 48)

   Add schema fetching after DB is ready:

   ```go
   // After DB is ready (around line 91), add:
   
   // Fetch schema for all tables used by tests
   tables := []string{"users", "todos", "ar_internal_metadata", "schema_migrations"}
   schemaCache, err := s.fetchSchemaFromDatabase(ctx, tables, "localhost", s.dbHostPort, "todo_user", "todo_password", "todo_api_development")
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

2. **pkg/runner/runner.go** - Modify `RunTest` (line 417-419)

   Change from:
   ```go
   // Add transparent mode duration (10s) for Rails to cache schema
   // This should expire before the actual test request
   proxyCmd = append(proxyCmd, "10s")
   ```

   To:
   ```go
   // Transparent mode disabled - schema is pre-loaded
   proxyCmd = append(proxyCmd, "0s")
   ```

   Or simply remove transparent mode entirely (pass "0s" or omit the argument).

3. **pkg/runner/runner.go** - Modify `RunTest` (line 390-415)

   Replace per-test schema fetch with loading from shared file:

   ```go
   // OLD: Per-test schema fetch (lines 376-398)
   // NEW: Load from shared schema file
   schemaFile := filepath.Join(s.cwd, ".linespec-shared-schema.json")
   if _, err := os.Stat(schemaFile); err == nil {
       proxyCmd = append(proxyCmd, "/app/project/.linespec-shared-schema.json")
   }
   ```

   Remove the per-test schema fetch code block (lines 376-398).

---

## Optimization 2: Fix WaitTCPInternal

**Priority**: High | **Estimated Savings**: 2-5s per container start | **Complexity**: Low

### Problem

`WaitTCPInternal` (orchestrator.go:110-169) spawns a new Alpine container for every TCP check, then runs `nc` via exec. This is extremely slow.

### Solution

Use direct TCP dial from host machine. Docker published ports are accessible from host.

### Code Changes

**pkg/docker/orchestrator.go** - Replace `WaitTCPInternal` (lines 110-169)

```go
func (d *DockerOrchestrator) WaitTCPInternal(ctx context.Context, networkName, address string, timeout time.Duration) error {
    deadline := time.Now().Add(timeout)
    for time.Now().Before(deadline) {
        conn, err := net.DialTimeout("tcp", address, 1*time.Second)
        if err == nil {
            conn.Close()
            return nil
        }
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(200 * time.Millisecond):
        }
    }
    return fmt.Errorf("timeout waiting for TCP %s", address)
}
```

**Note**: This simplifies from ~60 lines to ~15 lines and removes container spawning overhead.

---

## Optimization 3: Reduce Hardcoded Sleeps

**Priority**: Medium | **Estimated Savings**: 2-3s per test | **Complexity**: Very Low

### Problem

Multiple `time.Sleep` calls add up:

| Location | Current | Purpose | Proposed |
|----------|---------|---------|----------|
| runner.go:626 | 2s | Rails warmup | 500ms or remove |
| runner.go:672 | 500ms | Pre-verification delay | 100ms |
| runner.go:692 | 500ms x5 | collectHits retry | Exponential backoff |

### Code Changes

**pkg/runner/runner.go** - Line 620-627

```go
// OLD:
if serviceConfig.Service.Framework == "rails" {
    fmt.Println("Warming up Rails app...")
    warmupURL := fmt.Sprintf("http://localhost:%s%s", hostPort, serviceConfig.Service.HealthEndpoint)
    http.Get(warmupURL)
    time.Sleep(2 * time.Second)
}

// NEW:
if serviceConfig.Service.Framework == "rails" {
    fmt.Println("Warming up Rails app...")
    warmupURL := fmt.Sprintf("http://localhost:%s%s", hostPort, serviceConfig.Service.HealthEndpoint)
    http.Get(warmupURL)
    time.Sleep(500 * time.Millisecond)
}
```

**pkg/runner/runner.go** - Line 672

```go
// OLD:
time.Sleep(500 * time.Millisecond)

// NEW:
time.Sleep(100 * time.Millisecond)
```

**pkg/runner/runner.go** - Line 687-703 (collectHits)

```go
// OLD:
func (r *testRunner) collectHits(addr string) {
    fmt.Printf("Proxy: Collecting hits from %s...\n", addr)
    for i := 0; i < 5; i++ {
        resp, err := http.Get("http://" + addr + "/verify")
        if err != nil {
            time.Sleep(500 * time.Millisecond)
            continue
        }
        // ...
    }
}

// NEW:
func (r *testRunner) collectHits(addr string) {
    fmt.Printf("Proxy: Collecting hits from %s...\n", addr)
    delays := []time.Duration{100, 200, 400, 800, 1600} // Exponential backoff
    for i := 0; i < len(delays); i++ {
        resp, err := http.Get("http://" + addr + "/verify")
        if err != nil {
            time.Sleep(delays[i] * time.Millisecond)
            continue
        }
        // ...
    }
}
```

---

## Optimization 4: Parallel Test Execution

**Priority**: High | **Estimated Savings**: 2-4x for test suites | **Complexity**: Medium

### Problem

Tests run sequentially in main.go:97-120, but they're independent and could run concurrently.

### Solution

Add `-parallel` flag to control concurrency.

### Code Changes

**cmd/linespec/main.go** - Add flag parsing (after line 36)

```go
// Add after existing flag parsing
parallel := 1
for i, arg := range os.Args {
    if arg == "-parallel" && i+1 < len(os.Args) {
        if p, err := strconv.Atoi(os.Args[i+1]); err == nil && p > 0 {
            parallel = p
            // Remove from args to avoid confusing test path parsing
            os.Args = append(os.Args[:i], os.Args[i+2:]...)
        }
    }
}
```

**cmd/linespec/main.go** - Replace sequential loop (lines 97-120)

```go
var mu sync.Mutex
var wg sync.WaitGroup
passed := 0
failed := 0

// Semaphore for limiting concurrency
sem := make(chan struct{}, parallel)

for i, file := range testFiles {
    sem <- struct{}{} // Acquire slot
    wg.Add(1)
    
    go func(file string, idx int) {
        defer wg.Done()
        defer func() { <-sem }()
        
        fmt.Printf("\n[%d/%d] Running Test: %s\n", idx+1, len(testFiles), file)
        fmt.Println("--------------------------------------------------")
        
        testCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
        
        func() {
            defer cancel()
            defer func() {
                if r := recover(); r != nil {
                    fmt.Printf("❌ Test %s PANICKED: %v\n", file, r)
                    mu.Lock()
                    failed++
                    mu.Unlock()
                }
            }()
            
            if err := suite.RunTest(testCtx, file); err != nil {
                fmt.Printf("\n❌ Test %s FAILED: %v\n", file, err)
                mu.Lock()
                failed++
                mu.Unlock()
            } else {
                fmt.Printf("\n✅ Test %s PASSED\n", file)
                mu.Lock()
                passed++
                mu.Unlock()
            }
        }
    }(file, i)
}

wg.Wait()
```

**Note**: Add `sync` and `strconv` to imports.

### Usage

```bash
# Run 4 tests in parallel
linespec test ./tests -parallel 4

# Run all tests in parallel (up to 10)
linespec test ./tests -parallel 10
```

---

## Optimization 5: Cache Schema Globally

**Priority**: Medium | **Estimated Savings**: 1-3s per test | **Complexity**: Low

### Problem

Schema is fetched from database for each test (runner.go:376-398).

### Solution

This is already addressed in Optimization 1 (shared schema file). The per-test schema fetch should be removed.

### Verification

After implementing Optimization 1, verify that:
1. Schema is fetched once in `SetupSharedInfrastructure`
2. Schema is loaded from shared file in each test
3. No per-test schema queries are made

---

## Implementation Order

| Step | Optimization | Estimated Savings |
|------|-------------|------------------|
| 1 | Eliminate Transparent Mode | 8-10s per test |
| 2 | Fix WaitTCPInternal | 2-5s per test |
| 3 | Reduce Hardcoded Sleeps | 2-3s per test |
| 4 | Cache Schema Globally | (covered by #1) |
| 5 | Parallel Test Execution | 2-4x for suites |

---

## Backward Compatibility

- **Transparent mode**: Setting to "0s" maintains backward compatibility - existing tests will work
- **Parallel flag**: Default to 1 (sequential) to maintain existing behavior; users opt-in to parallelism
- **WaitTCPInternal**: Drop-in replacement, no API changes

---

## Testing

After implementing changes, run:

```bash
# Build
go build -o linespec ./cmd/linespec

# Run tests
go test ./...

# Integration test (if you have sample tests)
linespec test ./examples/test-set-0
```

---

## Future Considerations (Out of Scope)

1. **Container reuse**: Keep proxy/app containers running, update registry in-place
2. **Database state reset**: Reset DB between parallel tests to avoid conflicts
3. **Incremental container updates**: Only restart changed components

---

## Summary

Implementing optimizations 1-5 should reduce test execution time from ~60s to ~40s per test (33% improvement), and enable 2-4x speedup for test suites via parallelism.
