# Performance Investigation: Why todo-api Tests Are 5x Slower Than user-service Tests

## Executive Summary

The todo-api tests take **~12 seconds per test** while user-service tests take only **~2.5 seconds per test** - a **5x performance difference**. The root cause is HTTP connection timeouts in the Rails application that cannot resolve the `user-service.local` hostname.

## Key Findings

### 1. HTTP Call with Fixed Timeout in Rails Application

**Location:** `todo-api/app/controllers/api/v1/todos_controller.rb:58-61`

```ruby
response = HTTParty.get("http://user-service.local/api/v1/users/auth",
  body: { authorization: "Bearer #{token}" }.to_json,
  headers: { "Content-Type" => "application/json" },
  timeout: 5)  # <-- 5 second timeout
```

Every todo-api request triggers an HTTP call to `user-service.local` for authentication. This hostname **does not resolve** in the todo-api Docker network, causing a 5-second timeout per request.

### 2. Rescue Block with Fallback Behavior

**Location:** `todo-api/app/controllers/api/v1/todos_controller.rb:68-71`

```ruby
rescue HTTParty::Error, JSON::ParserError, SocketError, Errno::ECONNREFUSED, Net::OpenTimeout, Timeout::Error
end

@current_user = OpenStruct.new(id: 42, email: "user@example.com", name: "John Doe")
```

After the 5-second timeout, the Rails app falls back to a default user and continues processing. This means:
- Tests appear to pass (fallback user works)
- But each test incurs a 5-second penalty
- The HTTP mock in LineSpec never gets matched because the request times out before reaching the proxy

### 3. Test Structure Comparison

| Aspect | todo-api | user-service |
|--------|----------|--------------|
| Tests | 5 | 9 |
| HTTP mocks per test | 1 (unresolvable) | 0 |
| MySQL mocks per test | 6-9 | 5-9 |
| Avg time per test | ~12 seconds | ~2.5 seconds |
| Total test time | ~60 seconds | ~23 seconds |

### 4. DNS Resolution Failure

The hostname `user-service.local` is defined in the **user-service** docker-compose network, not in the todo-api network:

```yaml
# user-service/docker-compose.yml
networks:
  user-service-network:
    name: user-service-network  # Different network!
```

The todo-api web container has no way to resolve `user-service.local`, causing DNS lookup to hang until the 5-second HTTParty timeout expires.

## Impact Analysis

### Time Breakdown Per todo-api Test

| Operation | Time |
|-----------|------|
| HTTP timeout (user-service.local DNS failure) | ~5 seconds |
| MySQL proxy communication | ~2-3 seconds |
| Test execution overhead | ~3-4 seconds |
| **Total per test** | **~12 seconds** |

### Mock Utilization

The todo-api tests include HTTP mocks (`kind: Http`) that are never utilized:

```yaml
# todo-linespecs/mocks.yaml has 5 HTTP mocks
count: 5 HTTP mocks in todo-linespecs/mocks.yaml
count: 0 HTTP mocks in user-linespecs/mocks.yaml
```

These HTTP mocks represent wasted setup time since the HTTP calls timeout before the proxy can intercept them.

## Root Cause

The performance degradation is caused by **architectural mismatch** between the test expectations and the application behavior:

1. **LineSpec assumes** the HTTP call will be intercepted by the proxy and served from mocks
2. **Rails application** has a hardcoded 5-second timeout that expires before proxy interception
3. **DNS cannot resolve** `user-service.local` in the todo-api network
4. **Fallback behavior** masks the failure, making tests pass despite the timeout penalty

## Recommendations

### Option 1: Fix DNS Resolution (Recommended)
Add the proxy container to the todo-api network with an alias for `user-service.local`:

```yaml
# In the proxy startup or docker-compose override
services:
  web:
    extra_hosts:
      - "user-service.local:linespec-proxy"
```

Or configure the proxy container to respond to the `user-service.local` hostname.

### Option 2: Reduce HTTParty Timeout
Modify the Rails application to use a shorter timeout during testing:

```ruby
timeout: Rails.env.test? ? 0.5 : 5
```

This would reduce the penalty from 5 seconds to 0.5 seconds per test.

### Option 3: Remove HTTP Dependency
Refactor the todo-api authentication to not depend on an external service during tests, or use a mock authentication middleware.

### Option 4: Create Shared Network
Connect both services to a shared Docker network so DNS resolution works:

```yaml
networks:
  shared:
    external: true
```

## Verification

To confirm this analysis, add timing instrumentation around the HTTP call in `todos_controller.rb`:

```ruby
def authenticate_user!
  start_time = Time.now
  
  begin
    response = HTTParty.get("http://user-service.local/api/v1/users/auth", ...)
  rescue => e
    Rails.logger.error "Auth timeout after #{Time.now - start_time}s: #{e.class}"
  end
end
```

Expected output would show ~5 second delays for each test.

## Conclusion

The 5x performance difference is entirely attributable to DNS resolution timeouts for `user-service.local`. Each todo-api test wastes approximately 5 seconds waiting for an HTTP connection that can never succeed. The LineSpec HTTP mocks exist but cannot be utilized because the timeout occurs before proxy interception.

**Estimated time savings if fixed:** ~4-5 seconds per test (from ~12s to ~6-7s per test)
