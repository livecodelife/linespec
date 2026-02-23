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
linespec test [dir] [--compose <file>] [--service-url <url>] [--report <dir>]
```

**Options**

| Flag | Description | Default |
|---|---|---|
| `[dir]` | Path to a compiled test-set directory containing `tests/` and `mocks.yaml` | `keploy-examples/test-set-0` |
| `--compose <file>` | Path to the `docker-compose.yml` for the service under test | (required) |
| `--service-url <url>` | Base URL of the service once it is running | `http://localhost:3000` |
| `--report <dir>` | Report output directory, relative to the test-set dir | `linespec-report` |

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
✗ test-2 FAIL (body mismatch)
  Expected status : 200
  Actual status   : 200
  {                                           {
    "id": 1,                                    "id": 1,
    "title": "Buy milk"              ~          "title": "Buy bread"
  }                                           }
✓ test-3 PASS
…
→ Report written to keploy-examples/test-set-0/linespec-report/

summary: 10 passed, 1 failed
```

> **Note:** Differing lines are marked with ` ~ ` and are coloured red (expected) / green (actual) in a terminal.

### Report Folder

The report is written to `<test-set-dir>/linespec-report/` by default. Override with `--report <dir>`.

- `summary.json` — top-level object with `passed`, `failed`, `total`, and a `tests` array (each entry: `name`, `pass`, `reason?`).
- `{test-name}.json` — full `TestResult` for that test: `name`, `pass`, `reason?`, `expectedStatus`, `actualStatus?`, `expectedBody?`, `actualBody?`, `diff?`, `req`.

Example `summary.json`:

```json
{
  "passed": 10,
  "failed": 1,
  "total": 11,
  "tests": [
    { "name": "test-1", "pass": true },
    { "name": "test-2", "pass": false, "reason": "body mismatch" }
  ]
}
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
NOISE
  body.<field>
  body.<field>
```

- `<STATUS_CODE>` - HTTP status code (200, 201, 400, 500, etc.)
- `WITH` (optional) - Path to a YAML file containing the response body
- `NOISE` (optional) - Lists response body fields to ignore during comparison; each indented line is one dot-notation field path

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
NOISE
  body.created_at
  body.updated_at
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
