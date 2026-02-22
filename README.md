# LineSpec

LineSpec is a DSL compiler that generates Keploy-compatible KTests and KMocks from human-readable service behavior specifications.

## Installation

```bash
npm install
npm run build
```

## Usage

```bash
linespec compile <file> [-o <output-dir>]
```

### Options

- `<file>` - Path to a `.linespec` file (required)
- `-o, --out <dir>` - Output directory (default: `out`)

### Example

```bash
linespec compile examples/test-set-0/test-1.linespec -o out
```

## Running Tests

### `linespec test`

```bash
linespec test [dir] [--compose <file>] [--service-url <url>]
```

**Options**

| Flag | Description | Default |
|---|---|---|
| `[dir]` | Path to a compiled test-set directory containing `tests/` and `mocks.yaml` | `keploy-examples/test-set-0` |
| `--compose <file>` | Path to the `docker-compose.yml` for the service under test | (required) |
| `--service-url <url>` | Base URL of the service once it is running | `http://localhost:3000` |

**Prerequisites** — Docker and Docker Compose must be installed and the Docker daemon must be running.

**How it works** — A MySQL TCP proxy is started on a free local port. It intercepts queries and returns mock responses from `mocks.yaml` when a matching query is found; otherwise it forwards the query to the real database. Docker Compose is launched with an override that rewrites the service's `DATABASE_URL` to route through the proxy via `host.docker.internal`. Once the service is healthy, each test in `tests/` is replayed as an HTTP request. Status code and body are compared against the recorded response (noise fields listed in `assertions.noise` are ignored). A pass/fail summary is printed and Docker Compose is torn down.

**Example**

```bash
linespec test keploy-examples/test-set-0 \
  --compose /path/to/docker-compose.yml \
  --service-url http://localhost:3000
```

Expected output:

```
✓ Loaded 11 tests and 42 mocks from keploy-examples/test-set-0
✓ MySQL proxy listening on port 54321 → localhost:3306
✓ Service healthy at http://localhost:3000
✓ test-1 PASS
✓ test-2 PASS
…
summary: 11 passed, 0 failed
```

## Specification Format

A `.linespec` file defines a test case with three statement types:

### TEST

Defines the test name:

```
TEST test-name
```

### RECEIVE

Specifies the incoming HTTP request (required, must be first):

```
RECEIVE HTTP:<METHOD> <URL>
WITH {{payload/request.yaml}}
```

- `<METHOD>` - HTTP method (GET, POST, PUT, DELETE, etc.)
- `<URL>` - Full URL including protocol and host
- `WITH` (optional) - Path to a YAML file containing the request body

### EXPECT

Defines external dependencies (zero or more allowed):

```
EXPECT <CHANNEL> <resource>
WITH {{payload/input.yaml}}
RETURNS {{payload/output.yaml}}
```

Supported channels:
- `HTTP:<METHOD> <URL>` - External HTTP calls
- `READ_MYSQL <table>` - Database reads
- `WRITE_MYSQL <table>` - Database writes
- `WRITE_POSTGRESQL <table>` - PostgreSQL writes
- `EVENT <topic>` - Message queue events

### RESPOND

Specifies the HTTP response (required, must be last):

```
RESPOND HTTP:<STATUS_CODE>
WITH {{payload/response.yaml}}
```

- `<STATUS_CODE>` - HTTP status code (200, 201, 400, 500, etc.)
- `WITH` (optional) - Path to a YAML file containing the response body

## Example

```linespec
TEST test-1
RECEIVE HTTP:POST http://localhost:3000/users
WITH {{payloads/user_create_req.yaml}}
EXPECT WRITE:MYSQL users
WITH {{payloads/mysql_user_write_input.yaml}}
RETURNS {{payloads/mysql_user_write_result.yaml}}
RESPOND HTTP:201
WITH {{payloads/user_create_resp.yaml}}
```

This generates:
- `out/tests/test-1.yaml` - KTest specification
- `out/mocks.yaml` - KMock definitions for external dependencies

## Development

```bash
npm run dev        # Run CLI in development mode
npm run build      # Compile TypeScript
npm run test       # Run tests
linespec compile examples/test-set-0/test-1.linespec -o out
linespec test keploy-examples/test-set-0 --compose /path/to/docker-compose.yml
```
