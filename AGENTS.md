# Agent Guidelines for LineSpec

This file provides guidance for agents working on the LineSpec codebase.

## Project Overview

LineSpec is a DSL compiler for generating Keploy-compatible KTests and KMocks from human-readable service behavior specifications. It's a TypeScript CLI tool.

---

## Build, Lint, and Test Commands

### Installation
```bash
npm install
```

### Build
```bash
npm run build
```
Compiles TypeScript to JavaScript using `tsc`. Output goes to `./dist`.

### Development
```bash
npm run dev
```
Runs the CLI directly using ts-node on `src/cli.ts`.

### Test
```bash
npm run test
```
Runs all tests using `vitest run`.

### Run a Single Test
```bash
vitest run --testNamePattern "test-1"
```
Or use vitest's watch mode:
```bash
vitest
```
Then press `f` to filter tests interactively.

---

## Code Style Guidelines

### TypeScript Configuration
- Target: ES2020
- Module: CommonJS
- Strict mode is enabled (`strict: true`)
- No ESLint or Prettier configured

### Imports
- Use explicit named imports: `import { tokenize } from '../src/lexer'`
- Group external imports first, then internal
- Use `import * as fs from 'fs'` for Node.js built-ins
- Use `import * as yaml from 'js-yaml'` for external packages

### Naming Conventions
- **Files**: kebab-case (e.g., `lexer.ts`, `parser.ts`)
- **Types/Interfaces**: PascalCase (e.g., `TestSpec`, `ExpectStatement`)
- **Classes**: PascalCase (e.g., `LineSpecError`)
- **Functions/Variables**: camelCase (e.g., `tokenize`, `specFile`)
- **Constants**: camelCase (e.g., `EXAMPLES_DIR`)

### Type Definitions
- Use interfaces for object shapes
- Use type unions for variant types
- Export types from `src/types.ts`
- Example:
  ```typescript
  export interface ReceiveStatement {
    channel: 'HTTP';
    method: string;
    path: string;
    withFile?: string;
  }
  ```

### Error Handling
- Create custom error classes extending `Error`
- Include line numbers for parsing errors
- Example:
  ```typescript
  export class LineSpecError extends Error {
    line?: number;
    constructor(message: string, line?: number) {
      super(message);
      this.name = 'LineSpecError';
      this.line = line;
    }
  }
  ```

### Code Structure
- One export per file for utility functions
- Group related exports in a single file (e.g., `types.ts`)
- Separate concerns: lexer, parser, validator, compiler
- Tests go in `tests/` directory
- Test files should be named `*.test.ts`

### Formatting
- Use 2 spaces for indentation
- No trailing whitespace
- One blank line between imports and code
- Use semicolons
- No comments (per existing code style)

### Best Practices
- Always specify return types for functions
- Use `as` type assertions when types are guaranteed
- Validate inputs early and throw descriptive errors
- Use `Record<string, unknown>` for dynamic object types
- Handle file I/O with proper error messages
- Clean up temporary resources in tests (use `afterEach`)

---

## Project Structure

```
src/
  cli.ts          # CLI entry point (compile + test commands)
  lexer.ts        # Tokenizer for .linespec files
  parser.ts       # AST generation
  validator.ts    # Semantic validation
  compiler.ts     # YAML output generation
  types.ts        # TypeScript type definitions
  test-loader.ts  # Loads KTest YAML files and mocks.yaml from a test-set directory
  mysql-proxy.ts  # TCP proxy that intercepts MySQL packets and serves mock responses
  runner.ts       # Orchestrates Docker Compose, the proxy, HTTP test execution, and reporting
tests/
  integration.test.ts  # Main test suite
examples/
  test-set-0/          # .linespec input fixtures
    *.linespec
    payloads/
keploy-examples/
  test-set-0/          # Pre-compiled KTest + mocks fixtures
    tests/
    mocks.yaml
```

---

## Key Design Patterns

### Pipeline Architecture
The compiler follows a pipeline: `source → tokenize → parse → validate → compile`

### Test Runner Pipeline
`linespec test [dir]` follows the sequence:
1. `loadTestSet(dir)` — reads all `tests/*.yaml` and `mocks.yaml` from the given directory
2. `startProxy(mocks, host, dbPort, proxyPort)` — starts a TCP server that intercepts MySQL `COM_QUERY` packets, returns a serialised mock response on a hit, or pipes the packet to the real upstream on a miss
3. Docker Compose is started with a generated override file that rewrites `DATABASE_URL` to point at `host.docker.internal:<proxyPort>`
4. `pollUntilHealthy(serviceUrl)` — polls the service root until HTTP 200 or 404
5. Each `KTest` is replayed via Node's built-in `http` module; status code and body (minus noise keys) are compared
6. For each failed `KTest`, `buildSideBySideDiff(expectedBody, actualBody)` produces a side-by-side string where lines that differ are separated by ` ~ `. `runTests` prints each diff line to stdout; lines containing ` ~ ` are split and the left column is wrapped in ANSI red (`\x1b[31m`) and the right in ANSI green (`\x1b[32m`).
7. After all tests run, if `options.reportDir` is set (resolved by the CLI as `path.resolve(testDir, options.report)`), `runTests` calls `fs.mkdirSync(reportDir, { recursive: true })`, writes `summary.json` (aggregate counts + per-test pass/fail/reason), and writes one `{safeName}.json` per `TestResult` (full detail including `req`, `diff`, bodies). A confirmation line is printed to stdout.
8. Summary line is printed; `docker compose down` and proxy teardown run in `finally`.

### Mock-or-Passthrough
The MySQL proxy maintains an ordered queue of `KMockMysqlSpec` entries sorted by `created` timestamp. On each `COM_QUERY` it searches the queue for a spec whose `message.query` is a substring of (or contains) the incoming query. On a hit the mock is consumed and its serialised `responses` are written back to the client socket. On a miss the packet is forwarded to the real upstream and the response is streamed back transparently.

### Statement Types
- `RECEIVE` - Trigger request (exactly one required)
- `EXPECT` - External dependencies (zero or more)
- `RESPOND` - Response (exactly one required, must be last)

### Custom Errors
All parsing and validation errors extend `LineSpecError` with optional line numbers for error reporting.

---

## Testing Guidelines

- Use Vitest's `describe`/`it` syntax
- Use `beforeEach`/`afterEach` for setup/teardown
- Create temporary directories for test output
- Verify YAML output structure against expected schemas
- Test both success and failure cases
