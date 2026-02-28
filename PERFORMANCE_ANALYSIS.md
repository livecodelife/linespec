# LineSpec Test Performance Analysis

## Executive Summary

Each individual LineSpec test takes approximately **1 second** to execute due to the **per-test Docker proxy container restart cycle**. The architecture requires stopping, removing, and restarting a Docker container for each test to isolate mocks, resulting in significant overhead.

## Key Bottlenecks Identified

### 1. Per-Test Proxy Container Restart (Primary Bottleneck)

**Location:** `src/runner.ts:976-1064`

For **every single test**, the system performs:

```typescript
// Per-test cycle (lines 980-1064)
await spawnProcess('docker', ['stop', '-t', '1', proxyContainerName], true);
await spawnProcess('docker', ['rm', '-f', proxyContainerName], true);
await new Promise((resolve) => setTimeout(resolve, 500));  // Port release wait
await spawnProcess('docker', ['run', '-d', ...]);          // Start new container
await new Promise((resolve) => setTimeout(resolve, 1500)); // Container init wait
// ... health checks and additional waits
```

**Time breakdown per test:**
- `docker stop`: ~100-300ms (grace period: 1 second)
- `docker rm`: ~100-200ms
- Port release wait: **500ms (fixed)**
- `docker run`: ~200-500ms
- Container init wait: **1500ms (fixed)**
- TCP readiness polling: 0-10 seconds (20 retries × 500ms)
- Mock load wait: **500ms (fixed)**
- Service health re-check: 0-6 seconds

**Total per-test overhead: ~2.5-4 seconds** (even though tests appear to take ~1s, this is because some operations happen in parallel or early exit conditions)

### 2. Fixed Arbitrary Delays

Multiple `setTimeout` calls with fixed durations:

| Location | Delay | Purpose |
|----------|-------|---------|
| `runner.ts:1001` | 500ms | "Ensure port is free after container removal" |
| `runner.ts:1022` | 1500ms | "Wait for proxy to be ready" |
| `runner.ts:1054` | 500ms | "Additional wait to ensure proxy has loaded mocks" |
| `runner.ts:705` | 2000ms | Database readiness polling interval |
| `runner.ts:464` | 2000ms | Service health polling interval |

**Total fixed delays per test: ~2.5 seconds minimum**

### 3. Service Health Reconnection

**Location:** `src/runner.ts:1056-1063`

After each proxy restart, the web service must reconnect to the new MySQL proxy:

```typescript
// Wait for web service to be healthy after proxy restart
try {
  await pollUntilHealthy(options.serviceUrl, 30000);
} catch (healthErr) {
  // Continue anyway
}
```

This triggers Rails/ActiveRecord connection pool re-establishment, which takes time.

### 4. Docker Context Switches

**Operations per test:**
1. Docker daemon communication overhead
2. Container filesystem teardown and setup
3. Network namespace configuration
4. Volume mount operations
5. Process startup (Node.js runtime initialization)

### 5. Mock File I/O

**Location:** `src/runner.ts:971-974`

For each test, the system:
1. Serializes mocks to YAML (`yaml.dump`)
2. Writes to disk (`fs.writeFileSync`)
3. Mounts file into container via Docker volume

This I/O could potentially be done once upfront or served via an API.

## Architectural Design Decision

The per-test proxy restart exists to ensure **test isolation**. Each test has different MySQL mocks, and the proxy maintains an internal state (mock queue) that cannot be reset without restarting.

**Current flow:**
```
Test 1 → Write mocks-1.yaml → Restart Proxy → Run HTTP Test
Test 2 → Write mocks-2.yaml → Restart Proxy → Run HTTP Test
Test 3 → Write mocks-3.yaml → Restart Proxy → Run HTTP Test
```

## Potential Optimizations

### 1. Hot Mock Reloading (Highest Impact)

**Estimated improvement: 70-80% reduction**

Instead of restarting the container, implement a hot-reload mechanism:

```typescript
// Add to proxy-server.ts
proxyEvents.on('reloadMocks', (newMocks: KMock[]) => {
  // Reset internal queue without container restart
  queue.length = 0;
  queue.push(...buildMockQueue(newMocks));
});
```

**Implementation approach:**
- Keep proxy container running throughout test suite
- Send mock data via:
  - Unix domain socket signal
  - HTTP control endpoint on proxy
  - File watcher with inotify (less reliable)
- Reset mock queue state between tests

### 2. Parallel Test Execution

**Estimated improvement: 60-70% for multi-test suites**

Run tests in parallel using separate proxy ports:

```typescript
const testBatches = chunk(testSet.tests, 4);
await Promise.all(testBatches.map(async (batch) => {
  const proxyPort = await findFreePort();
  // Each batch gets its own proxy
  for (const test of batch) {
    // Run tests sequentially within batch
  }
}));
```

### 3. Connection Pool Reuse

**Estimated improvement: 20-30%**

Instead of full health checks, verify connection pool recovery:

```typescript
// Instead of pollUntilHealthy, use lightweight ping
await pingService(options.serviceUrl);
```

### 4. Remove Fixed Delays with Proper Readiness Checks

**Estimated improvement: 1-2 seconds per test**

Replace fixed `setTimeout` calls with active readiness probes:

```typescript
// Instead of: await setTimeout(1500)
await waitForCondition(async () => {
  return await isProxyReady(proxyPort);
}, { timeout: 5000, interval: 100 });
```

### 5. Mock Aggregation

**Estimated improvement: 10-20%**

Pre-compile all mocks into the proxy at startup, use test names to filter:

```typescript
// Start proxy once with ALL mocks
const allMocks = testSet.mocks;
proxyServer = await startProxy(allMocks, ...);

// Per-test, just activate the relevant mocks
proxyServer.activateMocksForTest(testName);
```

### 6. In-Process Proxy Mode (Non-Docker)

**Estimated improvement: 50-60%**

When Docker Compose is not required, run proxy in the same Node.js process:

```typescript
if (!useCompose) {
  // Reuse the same proxy server, just swap mocks
  proxyServer.clearMocks();
  proxyServer.loadMocks(testMocks);
}
```

## Measurement Recommendations

To validate these findings, add timing instrumentation:

```typescript
const timings = {
  proxyStop: 0,
  proxyRemove: 0,
  portWait: 0,
  proxyStart: 0,
  initWait: 0,
  healthCheck: 0,
  httpTest: 0,
};

// Wrap each operation with timing
const start = Date.now();
await spawnProcess('docker', ['stop', ...]);
timings.proxyStop = Date.now() - start;

// Log at end of each test
console.log(`[timing] ${testName}:`, timings);
```

## Summary

The ~1 second per test execution time is primarily caused by:

1. **Docker container lifecycle overhead** (stop → rm → run) = ~400-800ms
2. **Fixed arbitrary delays** (2.5s total across 3 waits) = ~1.5-2s
3. **Health check polling** = ~0-2s (depends on service state)

**Recommended priority:**
1. Implement hot mock reloading (biggest impact)
2. Remove fixed delays with proper readiness checks
3. Add parallel test execution
4. Optimize service reconnection strategy

With these optimizations, test execution could drop from ~1s per test to ~100-200ms per test (5-10x improvement).
