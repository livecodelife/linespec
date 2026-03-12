# Optimization 3: Reduce Hardcoded Sleeps - Implementation Plan

## Summary

**Status**: ✅ **COMPLETE**  
**Priority**: Medium  
**Estimated Savings**: 2-3s per test  
**Complexity**: Low  
**Files Modified**: `pkg/runner/runner.go`

---

## Current State Analysis

After previous optimizations, 3 hardcoded `time.Sleep` calls remain in the runner:

| Location | Duration | Purpose | Impact |
|----------|----------|---------|--------|
| `runner.go:720` | 2s | Rails framework warmup after health check | ~32s total (2s × 16 MySQL tests) |
| `runner.go:766` | 500ms | Pre-verification delay after collectHits | ~8s total (500ms × 16 tests) |
| `runner.go:786` | 500ms × 5 | collectHits retry loop delay | ~4s worst case (500ms × 5 retries × ~1-2 tests) |

**Total potential savings**: ~40-45 seconds on a 16-test MySQL suite

---

## Sleep #1: Rails Warmup (Line 720)

### Current Code

```go
// Lines 714-721
// Warmup for Rails apps to force schema/model loading
if serviceConfig.Service.Framework == "rails" {
    fmt.Println("Warming up Rails app...")
    // Send a simple request to force Rails to load models
    warmupURL := fmt.Sprintf("http://localhost:%s%s", hostPort, serviceConfig.Service.HealthEndpoint)
    http.Get(warmupURL)
    time.Sleep(2 * time.Second) // Give Rails time to load models
}
```

### Problem

- 2 seconds is excessively conservative
- The health check already waited for the app to be ready (with HTTP 200 response)
- After health check returns, Rails is already initialized and responding to requests
- The subsequent warmup HTTP request provides additional confirmation

### Proposed Solution

**Option A: Reduce to 100ms (Recommended)**
- The health endpoint already returns 200 when Rails is ready
- A single HTTP request to health endpoint takes ~50-100ms
- 100ms additional buffer is sufficient

**Option B: Remove entirely (Aggressive)**
- If warmup HTTP request completes, Rails is ready
- May cause flakiness on slower machines
- Not recommended without extensive testing

### Implementation

```go
// Lines 714-721 - REDUCE from 2s to 100ms
// Warmup for Rails apps to force schema/model loading
if serviceConfig.Service.Framework == "rails" {
    fmt.Println("Warming up Rails app...")
    warmupURL := fmt.Sprintf("http://localhost:%s%s", hostPort, serviceConfig.Service.HealthEndpoint)
    resp, err := http.Get(warmupURL)
    if err != nil {
        fmt.Printf("⚠️ Warmup request failed: %v\n", err)
    } else {
        resp.Body.Close()
        // Reduced from 2s to 100ms - health check already confirms Rails is ready
        time.Sleep(100 * time.Millisecond)
    }
}
```

**Savings**: 1.9s per Rails test = ~30s total on MySQL suite

---

## Sleep #2: Pre-Verification Delay (Line 766)

### Current Code

```go
// Lines 759-766
// 8. Final Registry Verification
if dbVerifyPort != "" {
    r.collectHits("localhost:" + dbVerifyPort)
}
if httpVerifyPort != "" {
    r.collectHits("localhost:" + httpVerifyPort)
}
time.Sleep(500 * time.Millisecond)  // Line 766

if err := r.registry.VerifyAll(); err != nil {
```

### Problem

- 500ms arbitrary delay after collecting hits from proxies
- collectHits() already waits for proxy responses with its own retry logic
- This delay is redundant and not based on any actual need

### Analysis

The `collectHits` function:
1. Calls `/verify` endpoint on the proxy (lines 781-798)
2. Retries up to 5 times with 500ms delays
3. Sets hits in registry when successful
4. Returns immediately on success

If collectHits succeeds, the proxy has already processed and recorded all hits. An additional 500ms delay is unnecessary. If collectHits fails after 5 retries, the additional 500ms won't help anyway.

### Proposed Solution

**Remove entirely**
- The collectHits function's retry loop already handles timing
- If collectHits succeeds, verification can proceed immediately
- If it fails, the delay doesn't help

### Implementation

```go
// Lines 759-768 - REMOVE the 500ms sleep
// 8. Final Registry Verification
if dbVerifyPort != "" {
    r.collectHits("localhost:" + dbVerifyPort)
}
if httpVerifyPort != "" {
    r.collectHits("localhost:" + httpVerifyPort)
}
// REMOVED: time.Sleep(500 * time.Millisecond)
// collectHits already waits for proxy responses with retry logic

if err := r.registry.VerifyAll(); err != nil {
```

**Savings**: 500ms per test = ~8s total on 16-test suite

---

## Sleep #3: collectHits Retry Delay (Line 786)

### Current Code

```go
// Lines 781-798
func (r *testRunner) collectHits(addr string) {
    fmt.Printf("Proxy: Collecting hits from %s...\n", addr)
    for i := 0; i < 5; i++ {
        resp, err := http.Get("http://" + addr + "/verify")
        if err != nil {
            time.Sleep(500 * time.Millisecond)  // Line 786
            continue
        }
        defer resp.Body.Close()

        var hits map[string]int
        if err := json.NewDecoder(resp.Body).Decode(&hits); err != nil {
            return
        }
        r.registry.SetHits(hits)
        return
    }
}
```

### Problem

- Fixed 500ms delay between retries
- 5 retries × 500ms = 2.5s worst case per proxy
- Most failures are immediate (proxy not ready yet), not timing-related
- First retry should be fast, subsequent retries can wait longer

### Proposed Solution

**Implement exponential backoff**
- Start with 50ms for immediate retry
- Double each time: 50ms, 100ms, 200ms, 400ms, 800ms
- Total worst case: 1.55s (vs 2.5s currently)
- Average case: Much faster (usually succeeds on 1st or 2nd try)

### Implementation

```go
// Lines 781-798 - EXPONENTIAL BACKOFF for retries
func (r *testRunner) collectHits(addr string) {
    fmt.Printf("Proxy: Collecting hits from %s...\n", addr)
    // Exponential backoff: 50ms, 100ms, 200ms, 400ms, 800ms
    delays := []time.Duration{50, 100, 200, 400, 800}
    for i := 0; i < len(delays); i++ {
        resp, err := http.Get("http://" + addr + "/verify")
        if err != nil {
            time.Sleep(delays[i] * time.Millisecond)
            continue
        }
        defer resp.Body.Close()

        var hits map[string]int
        if err := json.NewDecoder(resp.Body).Decode(&hits); err != nil {
            return
        }
        r.registry.SetHits(hits)
        return
    }
}
```

**Savings**: 
- Best case: ~450ms faster (if succeeds on 2nd try)
- Worst case: ~950ms faster (2.5s → 1.55s)
- Average case: ~1s per test × 16 tests = ~16s total

---

## Implementation Order

1. **Sleep #2 (Line 766)**: Remove 500ms pre-verification delay - ✅ COMPLETE
2. **Sleep #3 (Line 786)**: Exponential backoff in collectHits - ✅ COMPLETE
3. **Sleep #1 (Line 720)**: Reduce Rails warmup to 100ms - ✅ COMPLETE

---

## Testing Strategy

### Unit Tests
- Run `go test ./pkg/runner/...` to ensure no regressions
- Verify collectHits logic with mock HTTP server

### Integration Tests
Run full test suite:
```bash
cd examples/test-set-0
go run ../../cmd/linespec test .
```

### Validation Criteria
- All tests pass consistently (run 3 times to check for flakiness)
- No increase in test failures
- Measure total execution time before/after

### Benchmark

Before optimization (baseline):
```bash
time go run ../../cmd/linespec test .
# Expected: ~60-70s for 16 MySQL tests
```

After optimization:
```bash
time go run ../../cmd/linespec test .
# Expected: ~45-55s (10-15s improvement)
```

---

## Rollback Plan

If flakiness increases:

1. **Immediate**: Increase Rails warmup from 100ms to 250ms
2. **If still flaky**: Re-add 250ms pre-verification delay
3. **Last resort**: Revert all changes

---

## Code Changes Summary

### File: `pkg/runner/runner.go`

**Change 1 - Line 714-721 (Rails warmup)**
```diff
  // Warmup for Rails apps to force schema/model loading
  if serviceConfig.Service.Framework == "rails" {
      fmt.Println("Warming up Rails app...")
      // Send a simple request to force Rails to load models
      warmupURL := fmt.Sprintf("http://localhost:%s%s", hostPort, serviceConfig.Service.HealthEndpoint)
-     http.Get(warmupURL)
-     time.Sleep(2 * time.Second) // Give Rails time to load models
+     resp, err := http.Get(warmupURL)
+     if err != nil {
+         fmt.Printf("⚠️ Warmup request failed: %v\n", err)
+     } else {
+         resp.Body.Close()
+         // Reduced from 2s to 100ms - health check already confirms Rails is ready
+         time.Sleep(100 * time.Millisecond)
+     }
  }
```

**Change 2 - Line 766 (Pre-verification delay)**
```diff
  if httpVerifyPort != "" {
      r.collectHits("localhost:" + httpVerifyPort)
  }
- time.Sleep(500 * time.Millisecond)
+ // REMOVED: collectHits already waits for proxy responses
```

**Change 3 - Lines 781-798 (collectHits exponential backoff)**
```diff
  func (r *testRunner) collectHits(addr string) {
      fmt.Printf("Proxy: Collecting hits from %s...\n", addr)
-     for i := 0; i < 5; i++ {
+     // Exponential backoff: 50ms, 100ms, 200ms, 400ms, 800ms
+     delays := []time.Duration{50, 100, 200, 400, 800}
+     for i := 0; i < len(delays); i++ {
          resp, err := http.Get("http://" + addr + "/verify")
          if err != nil {
-             time.Sleep(500 * time.Millisecond)
+             time.Sleep(delays[i] * time.Millisecond)
              continue
          }
          defer resp.Body.Close()
```

---

## Expected Results

| Metric | Before | After | Savings |
|--------|--------|-------|---------|
| Per-test overhead | ~3s | ~0.2s | ~2.8s |
| 16-test suite | ~48s sleeps | ~3s sleeps | ~45s |
| Total suite time | ~70s | ~50s | ~20s (28%) |

---

## Notes

- The existing `time.After` calls in polling loops (lines 219, 988, 998, 1029) use appropriate 100-200ms intervals and should remain unchanged
- These changes are additive to previous optimizations (1 & 2)
- Combined with optimizations 1 & 2, total suite time should drop from ~180s to ~50s (72% improvement)
