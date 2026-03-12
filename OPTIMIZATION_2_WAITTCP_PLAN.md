# Optimization 2: Fix WaitTCPInternal - Implementation Plan

## Overview

**Status**: Pending  
**Priority**: High  
**Estimated Savings**: 2-5s per container start  
**Complexity**: Low  
**Effort**: ~1-2 hours

---

## Problem Statement

The `WaitTCPInternal` function in `pkg/docker/orchestrator.go` (lines 110-169) is a significant performance bottleneck. It spawns a new Alpine Linux container for every TCP connectivity check, then executes `nc` (netcat) commands inside that container to verify network connectivity.

### Current Implementation Issues

1. **Container spawn overhead**: Each call creates a new Alpine container (~1-2s startup time)
2. **Exec overhead**: Running `nc` via Docker exec is slow and unreliable
3. **Resource waste**: Creates and destroys containers repeatedly during test runs
4. **Polling inefficiency**: Sleeps 200ms between attempts with container overhead

### Impact

With 6-8 `WaitTCPInternal` calls per test (database, Kafka, proxies, application), this adds **2-5 seconds** to every test execution. For a test suite with 20 tests, this amounts to **40-100 seconds** of wasted time.

---

## Root Cause Analysis

### Current Flow

```
1. WaitTCPInternal called with "real-db:3306"
2. Spawns Alpine container with "sleep 300"
3. Creates exec config for: nc -z -w 1 real-db 3306
4. Executes nc command in container
5. Polls exec status every 100ms
6. Removes container on completion
```

### Why This Is Unnecessary

All services using `WaitTCPInternal` have their ports published to the host:

| Service | Internal Address | Host Port | Published? |
|---------|-----------------|-----------|------------|
| MySQL | real-db:3306 | Random (via PortBindings) | ✅ Yes |
| Kafka | kafka:29092 | Random (via PortBindings) | ✅ Yes |
| DB Proxy | db:3306 | Random (via PortBindings) | ✅ Yes |
| HTTP Proxy | user-service.local:80 | Random (via PortBindings) | ✅ Yes |

Since Docker publishes these ports to the host, we can connect directly via `localhost:<port>` instead of spawning containers to check from within the Docker network.

---

## Proposed Solution

Replace the Alpine container + nc approach with direct TCP dial from the host machine using Go's standard `net.DialTimeout`.

### Key Changes

#### 1. Simplified `WaitTCPInternal` Function

**File**: `pkg/docker/orchestrator.go`  
**Lines**: 110-169 (replace entire function)

**New Implementation**:

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

**Benefits**:
- Reduces ~60 lines to ~15 lines
- No container spawning overhead
- Direct TCP connection from host
- 10-20x faster execution

---

#### 2. Update Callers to Use Host Addresses

Since we're connecting from the host, we need to pass `localhost:<hostPort>` instead of internal Docker network addresses like `real-db:3306`.

**Current Pattern**:
```go
// Lines 86-89 in runner.go
if err := s.orch.WaitTCPInternal(ctx, s.networkName, "real-db:3306", 60*time.Second); err != nil {
    return err
}
```

**New Pattern**:
```go
// Get host port first
inspect, _ := s.orch.GetContainerInspect(ctx, "linespec-shared-db")
if p, ok := inspect.NetworkSettings.Ports["3306/tcp"]; ok && len(p) > 0 {
    hostPort := p[0].HostPort
    // Use host port instead of internal address
    if err := s.orch.WaitTCPInternal(ctx, s.networkName, "localhost:"+hostPort, 60*time.Second); err != nil {
        return err
    }
}
```

---

## Implementation Steps

### Step 1: Modify `WaitTCPInternal` Function

**Location**: `pkg/docker/orchestrator.go:110-169`

**Action**: Replace the entire function with the simplified direct TCP dial version.

**Verification**: Ensure the function signature remains unchanged to maintain backward compatibility:
```go
func (d *DockerOrchestrator) WaitTCPInternal(ctx context.Context, networkName, address string, timeout time.Duration) error
```

### Step 2: Update All Call Sites

There are **6 call sites** that need updating:

1. **Line 87** - MySQL shared DB wait
2. **Line 163** - Kafka wait  
3. **Line 364** - PostgreSQL wait
4. **Line 396** - PostgreSQL proxy wait
5. **Line 547** - Database proxy wait (generic)
6. **Line 551** - HTTP proxy wait

**Pattern for each call site**:
1. Get container inspect to find host port binding
2. Extract the host port from `NetworkSettings.Ports`
3. Pass `localhost:<hostPort>` to `WaitTCPInternal`

### Step 3: Test the Changes

**Test Command**:
```bash
cd /Users/calebcowen/workspace/linespec
go build -o linespec ./cmd/linespec
./linespec test ./examples/test-set-0
```

**Expected Results**:
- All tests should pass
- Test execution should be 2-5s faster per test
- No Alpine containers should be spawned during execution (verify with `docker ps`)

---

## Code Change Details

### Change 1: orchestrator.go - New WaitTCPInternal

```go
// WaitTCPInternal waits for a TCP service to be available.
// Uses direct TCP dial from host instead of spawning Alpine containers.
// The address parameter should be in "host:port" format (e.g., "localhost:3306").
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

### Change 2: runner.go - Example Updated Call Site (Line 87)

**Current**:
```go
fmt.Println("Waiting for shared DB to be ready...")
if err := s.orch.WaitTCPInternal(ctx, s.networkName, "real-db:3306", 60*time.Second); err != nil {
    return err
}
```

**New**:
```go
fmt.Println("Waiting for shared DB to be ready...")
// Get host port for direct connection
inspect, err := s.orch.GetContainerInspect(ctx, "linespec-shared-db")
if err != nil {
    return fmt.Errorf("failed to inspect shared DB container: %w", err)
}
if p, ok := inspect.NetworkSettings.Ports["3306/tcp"]; ok && len(p) > 0 {
    hostPort := p[0].HostPort
    if err := s.orch.WaitTCPInternal(ctx, s.networkName, "localhost:"+hostPort, 60*time.Second); err != nil {
        return fmt.Errorf("shared DB not ready: %w", err)
    }
} else {
    return fmt.Errorf("shared DB port not found")
}
```

### Change 3: runner.go - Kafka Call Site (Line 163)

**Current**:
```go
if err := s.orch.WaitTCPInternal(ctx, s.networkName, "kafka:29092", 60*time.Second); err != nil {
    return err
}
```

**New**:
```go
// Get Kafka host port for direct connection
inspect, err := s.orch.GetContainerInspect(ctx, "linespec-shared-kafka")
if err != nil {
    return fmt.Errorf("failed to inspect Kafka container: %w", err)
}
if p, ok := inspect.NetworkSettings.Ports["29092/tcp"]; ok && len(p) > 0 {
    hostPort := p[0].HostPort
    if err := s.orch.WaitTCPInternal(ctx, s.networkName, "localhost:"+hostPort, 60*time.Second); err != nil {
        return fmt.Errorf("Kafka not ready: %w", err)
    }
} else {
    return fmt.Errorf("Kafka port not found")
}
```

### Change 4-6: runner.go - Per-Test Call Sites

For per-test calls (lines 364, 396, 547, 551), the pattern is similar:
1. Container is started with a name (e.g., `"proxy-db-"+spec.Name`)
2. Get container inspect by name
3. Extract host port
4. Call WaitTCPInternal with `localhost:<port>`

---

## Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Port not yet bound when inspect called | Low | High | Retry inspection 3 times with 100ms delay |
| Container name collision | Low | Medium | Use unique naming scheme already in place |
| Host port binding fails | Low | High | Check inspect error, fall back to internal method if needed |
| Network timing changes | Low | Low | Longer timeout (already 30-60s) |

---

## Rollback Plan

If issues arise:

1. **Revert orchestrator.go**: Restore the original `WaitTCPInternal` implementation
2. **Revert runner.go**: Restore original call sites with internal addresses
3. **Test**: Run `go test ./...` and `./linespec test ./examples/test-set-0`

---

## Success Criteria

- [ ] `WaitTCPInternal` uses direct TCP dial (no Alpine containers)
- [ ] All 6 call sites updated to use host ports
- [ ] All existing tests pass
- [ ] No Alpine containers spawned during test runs
- [ ] 2-5s improvement in test execution time per test
- [ ] Code review approved
- [ ] No regression in test reliability

---

## Related Files

- `pkg/docker/orchestrator.go` - Contains WaitTCPInternal function
- `pkg/runner/runner.go` - Contains 6 call sites
- `PERFORMANCE_OPTIMIZATION.md` - Overall optimization context

---

## Notes

1. **WaitTCP vs WaitTCPInternal**: The existing `WaitTCP` function (lines 172-187) already does direct TCP dial from host. After this optimization, both functions will have similar implementations, but `WaitTCPInternal` keeps the Docker network parameter for backward compatibility and future flexibility.

2. **Network Parameter**: The `networkName` parameter in `WaitTCPInternal` is kept for API compatibility but not used in the new implementation. It's still relevant for context and potential future use.

3. **Testing**: Verify that tests work on both Docker Desktop (Mac/Windows) and native Linux Docker, as network behavior can differ.

---

## Time Estimate

- Code changes: 30-45 minutes
- Testing and debugging: 30-45 minutes
- Code review and refinement: 15-30 minutes
- **Total: 1.5-2 hours**
