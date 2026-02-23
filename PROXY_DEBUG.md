# MySQL Proxy - Current Status and Fixes Needed

## Overview

The MySQL proxy is designed to intercept database queries from the application and return mock responses based on recorded mocks from `mocks.yaml`. This document outlines the current implementation status and what needs to be fixed.

## Current Status

### What's Working ✅

1. **Proxy runs in Docker container** - The proxy is built as a Docker image and runs in the same Docker network as the application
2. **Network connectivity** - The web container can reach the proxy, and the proxy can reach the database
3. **Mock matching** - The proxy correctly identifies queries that should be mocked (SET NAMES, PING, schema_migrations query)
4. **Connection handling** - Basic MySQL handshake is being forwarded correctly

### What's Not Working ❌

1. **Mock response serialization** - The serialized responses from mocks don't match what the MySQL client expects
2. **Rails fails with "Unknown or undefined error code"** - After receiving mock responses, Rails cannot parse them correctly

## Architecture

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────┐
│   Web Container │────▶│  Proxy Container │────▶│ DB Container│
│  (Rails App)   │     │ (linespec-proxy) │     │  (MySQL)   │
└─────────────────┘     └──────────────────┘     └─────────────┘
       │                        │
       │ DATABASE_URL           │ upstream-host: db
       │ host: linespec-proxy  │
       │ port: 3307           │
       └──────────────────────┘
```

## Files Involved

1. **`src/mysql-proxy.ts`** - Main proxy implementation
   - `serializeResponses()` - Converts mock YAML to MySQL protocol packets
   - `encodeOkPayload()` - Encodes OK packet responses
   - `encodeColumnDefPayload()` - Encodes column definitions
   - `encodeTextRowPayload()` - Encodes result set rows

2. **`src/runner.ts`** - Test runner that orchestrates:
   - Starting the database container
   - Building and running the proxy container
   - Generating docker-compose override

3. **`proxy-build/`** - Directory for building proxy Docker image

## Testing the Fix

### Prerequisites

1. Clean Docker environment:
```bash
docker rm -f todo-db todo-api-web linespec-proxy 2>/dev/null
```

2. Build the project:
```bash
npm run build
```

### Run Test with Debug Output

```bash
cd /Users/calebcowen/workspace/linespec
npx linespec test linespec-tests --compose todo-api/docker-compose.yml --service-url http://localhost:3000 2>&1 &
sleep 60
kill $! 2>/dev/null
```

### Check Logs

```bash
# Check proxy logs
docker logs linespec-proxy

# Check web container logs  
docker logs todo-api-web

# Check running containers
docker ps -a | grep -E "todo|linespec"
```

### Expected Debug Output

When working correctly, you should see logs like:
```
[mysql-proxy] Client packet: cmd=COM_QUERY (0x3), seqId=0
[mysql-proxy] COM_QUERY: "SET NAMES utf8mb4..."
[mysql-proxy] Mock found: yes
[mysql-proxy] Serializing mock response...
[mysql-proxy] Response bytes: 0a000000...
```

## What Needs to be Fixed

### 1. Response Packet Encoding

The `serializeResponses()` and `encodeOkPayload()` functions in `src/mysql-proxy.ts` need to properly encode MySQL protocol packets.

**Current issue**: The OK packet format may not match exactly what MySQL client expects.

**Location**: `src/mysql-proxy.ts` lines 125-151 (`encodeOkPayload` function)

**MySQL OK Packet Format**:
```
Bytes 0-2: Packet length (3 bytes, little-endian)
Byte 3: Sequence ID
Byte 4: Header (0x00 for OK)
Bytes 5-6: Affected rows (lenenc)
Bytes 7-8: Last insert ID (lenenc)
Bytes 9-10: Status flags (2 bytes)
Bytes 11-12: Warnings (2 bytes)
[Optional]: Info string
```

### 2. Mock Queue Matching

The `findAndConsumeMock()` function needs to correctly match queries to mocks. Currently the matching logic checks if the query string contains or is contained by the mock query.

**Location**: `src/mysql-proxy.ts` lines 291-319 (`findAndConsumeMock` function)

### 3. Packet Sequence IDs

The proxy must maintain proper MySQL packet sequence IDs. Each packet should increment the sequence ID.

**Current issue**: The `serializeResponses` function uses the starting sequence ID but may not handle multi-packet responses correctly.

**Location**: `src/mysql-proxy.ts` lines 191-275 (`serializeResponses` function)

## Debugging Steps

1. **Add hex logging** to see exact bytes being sent:
```typescript
// In serializeResponses, after building response
console.error(`[proxy] Response hex: ${response.toString('hex')}`);
```

2. **Compare with real MySQL** - Run the app without mocks and capture actual packets:
```bash
# Run against real DB to see working packets
tcpdump -i lo -w /tmp/mysql.pcap port 3306
```

3. **Test with mysql CLI** - See what the real server returns:
```bash
docker exec -it todo-db mysql -u todo_user -p
SHOW VARIABLES LIKE 'character_set%';
```

## Current Mocks Format

The mocks in `linespec-tests/mocks.yaml` have this structure:

```yaml
version: api.keploy.io/v1beta1
kind: MySQL
name: test-2-mock-set-names
spec:
  metadata:
    connID: "0"
    requestOperation: COM_QUERY
    responseOperation: OK
    type: mocks
  requests:
    - header:
        header:
          payload_length: 151
          sequence_id: 0
        packet_type: COM_QUERY
      message:
        query: SET NAMES utf8mb4...
  responses:
    - header:
        header:
          payload_length: 7
          sequence_id: 1
        packet_type: OK
      message:
        header: 0
        affected_rows: 0
        last_insert_id: 0
        status_flags: 2
        warnings: 0
        info: ''
```

## Key Takeaways

1. The MySQL protocol is strict about packet formats
2. Small differences in encoding cause "Unknown or undefined error code"
3. The mock responses need to exactly match what a real MySQL server would return
4. Debug by comparing hex output with real MySQL responses
