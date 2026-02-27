import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import * as fs from 'fs';
import * as path from 'path';
import * as yaml from 'js-yaml';
import { tokenize } from '../src/lexer';
import { parse } from '../src/parser';
import { validate } from '../src/validator';
import { compile } from '../src/compiler';

const TODO_EXAMPLES_DIR = path.join(__dirname, '..', 'examples', 'todo-linespecs');
const USER_EXAMPLES_DIR = path.join(__dirname, '..', 'examples', 'user-linespecs');

function compileSpec(specFile: string, outDir: string): void {
  const source = fs.readFileSync(specFile, 'utf-8');
  const tokens = tokenize(source);
  const spec = parse(tokens, specFile);
  validate(spec, path.dirname(specFile));
  compile(spec, { outDir, baseDir: path.dirname(specFile) });
}

function loadKTest(outDir: string, name: string): Record<string, unknown> {
  const content = fs.readFileSync(path.join(outDir, 'tests', `${name}.yaml`), 'utf-8');
  return yaml.load(content) as Record<string, unknown>;
}

function loadKMocks(outDir: string): Record<string, unknown>[] {
  const content = fs.readFileSync(path.join(outDir, 'mocks.yaml'), 'utf-8');
  return yaml.loadAll(content) as Record<string, unknown>[];
}

interface TestCase {
  name: string;
  file: string;
  examplesDir: string;
  method: string;
  url: string;
  statusCode: number;
  statusMessage: string;
  mockCount: number;
  hasRequestBody: boolean;
  hasResponseBody: boolean;
  noiseFields?: string[];
  expectedHttpMocks?: number;
  expectedMysqlMocks?: number;
}

const testCases: TestCase[] = [
  // Todo specs
  {
    name: 'create_todo_success',
    file: 'create_todo_success.linespec',
    examplesDir: TODO_EXAMPLES_DIR,
    method: 'POST',
    url: 'http://localhost:3000/api/v1/todos',
    statusCode: 201,
    statusMessage: 'Created',
    mockCount: 8, // 1 HTTP + 7 MySQL
    hasRequestBody: true,
    hasResponseBody: true,
    noiseFields: ['body.id', 'body.created_at', 'body.updated_at'],
    expectedHttpMocks: 1,
    expectedMysqlMocks: 7,
  },
  {
    name: 'get_todo_success',
    file: 'get_todo_success.linespec',
    examplesDir: TODO_EXAMPLES_DIR,
    method: 'GET',
    url: 'http://localhost:3000/api/v1/todos/1',
    statusCode: 200,
    statusMessage: 'OK',
    mockCount: 6, // 1 HTTP + 5 MySQL
    hasRequestBody: false,
    hasResponseBody: true,
    expectedHttpMocks: 1,
    expectedMysqlMocks: 5,
  },
  {
    name: 'list_todos_success',
    file: 'list_todos_success.linespec',
    examplesDir: TODO_EXAMPLES_DIR,
    method: 'GET',
    url: 'http://localhost:3000/api/v1/todos',
    statusCode: 200,
    statusMessage: 'OK',
    mockCount: 6, // 1 HTTP + 5 MySQL
    hasRequestBody: false,
    hasResponseBody: true,
    expectedHttpMocks: 1,
    expectedMysqlMocks: 5,
  },
  {
    name: 'update_todo_success',
    file: 'update_todo_success.linespec',
    examplesDir: TODO_EXAMPLES_DIR,
    method: 'PATCH',
    url: 'http://localhost:3000/api/v1/todos/1',
    statusCode: 200,
    statusMessage: 'OK',
    mockCount: 9, // 1 HTTP + 8 MySQL
    hasRequestBody: true,
    hasResponseBody: true,
    noiseFields: ['body.updated_at'],
    expectedHttpMocks: 1,
    expectedMysqlMocks: 8,
  },
  {
    name: 'delete_todo_success',
    file: 'delete_todo_success.linespec',
    examplesDir: TODO_EXAMPLES_DIR,
    method: 'DELETE',
    url: 'http://localhost:3000/api/v1/todos/1',
    statusCode: 204,
    statusMessage: 'No Content',
    mockCount: 9, // 1 HTTP + 8 MySQL
    hasRequestBody: false,
    hasResponseBody: false,
    expectedHttpMocks: 1,
    expectedMysqlMocks: 8,
  },
  // User specs
  {
    name: 'authenticate_user_success',
    file: 'authenticate_user_success.linespec',
    examplesDir: USER_EXAMPLES_DIR,
    method: 'GET',
    url: 'http://localhost:3000/api/v1/users/auth',
    statusCode: 200,
    statusMessage: 'OK',
    mockCount: 5, // 0 HTTP + 5 MySQL
    hasRequestBody: false,
    hasResponseBody: true,
    expectedHttpMocks: 0,
    expectedMysqlMocks: 5,
  },
  {
    name: 'authenticate_user_invalid_token',
    file: 'authenticate_user_invalid_token.linespec',
    examplesDir: USER_EXAMPLES_DIR,
    method: 'GET',
    url: 'http://localhost:3000/api/v1/users/auth',
    statusCode: 401,
    statusMessage: 'Unauthorized',
    mockCount: 5, // 0 HTTP + 5 MySQL
    hasRequestBody: false,
    hasResponseBody: true,
    expectedHttpMocks: 0,
    expectedMysqlMocks: 5,
  },
  {
    name: 'create_user_success',
    file: 'create_user_success.linespec',
    examplesDir: USER_EXAMPLES_DIR,
    method: 'POST',
    url: 'http://localhost:3000/api/v1/users',
    statusCode: 201,
    statusMessage: 'Created',
    mockCount: 7, // 0 HTTP + 7 MySQL
    hasRequestBody: true,
    hasResponseBody: true,
    noiseFields: ['body.id', 'body.created_at'],
    expectedHttpMocks: 0,
    expectedMysqlMocks: 7,
  },
  {
    name: 'create_user_already_exists',
    file: 'create_user_already_exists.linespec',
    examplesDir: USER_EXAMPLES_DIR,
    method: 'POST',
    url: 'http://localhost:3000/api/v1/users',
    statusCode: 409,
    statusMessage: 'Conflict',
    mockCount: 5, // 0 HTTP + 5 MySQL
    hasRequestBody: true,
    hasResponseBody: true,
    expectedHttpMocks: 0,
    expectedMysqlMocks: 5,
  },
  {
    name: 'get_user_success',
    file: 'get_user_success.linespec',
    examplesDir: USER_EXAMPLES_DIR,
    method: 'GET',
    url: 'http://localhost:3000/api/v1/users/42',
    statusCode: 200,
    statusMessage: 'OK',
    mockCount: 6, // 1 HTTP + 5 MySQL
    hasRequestBody: false,
    hasResponseBody: true,
    expectedHttpMocks: 1,
    expectedMysqlMocks: 5,
  },
  {
    name: 'get_user_not_found',
    file: 'get_user_not_found.linespec',
    examplesDir: USER_EXAMPLES_DIR,
    method: 'GET',
    url: 'http://localhost:3000/api/v1/users/999',
    statusCode: 404,
    statusMessage: 'Not Found',
    mockCount: 6, // 1 HTTP + 5 MySQL
    hasRequestBody: false,
    hasResponseBody: true,
    expectedHttpMocks: 1,
    expectedMysqlMocks: 5,
  },
  {
    name: 'login_success',
    file: 'login_success.linespec',
    examplesDir: USER_EXAMPLES_DIR,
    method: 'POST',
    url: 'http://localhost:3000/api/v1/users/login',
    statusCode: 200,
    statusMessage: 'OK',
    mockCount: 8, // 0 HTTP + 8 MySQL
    hasRequestBody: true,
    hasResponseBody: true,
    expectedHttpMocks: 0,
    expectedMysqlMocks: 8,
  },
  {
    name: 'update_user_success',
    file: 'update_user_success.linespec',
    examplesDir: USER_EXAMPLES_DIR,
    method: 'PUT',
    url: 'http://localhost:3000/api/v1/users/42',
    statusCode: 200,
    statusMessage: 'OK',
    mockCount: 9, // 1 HTTP + 8 MySQL
    hasRequestBody: true,
    hasResponseBody: true,
    expectedHttpMocks: 1,
    expectedMysqlMocks: 8,
  },
  {
    name: 'delete_user_success',
    file: 'delete_user_success.linespec',
    examplesDir: USER_EXAMPLES_DIR,
    method: 'DELETE',
    url: 'http://localhost:3000/api/v1/users/42',
    statusCode: 204,
    statusMessage: 'No Content',
    mockCount: 9, // 1 HTTP + 8 MySQL
    hasRequestBody: false,
    hasResponseBody: false,
    expectedHttpMocks: 1,
    expectedMysqlMocks: 8,
  },
];

describe('Integration Tests - Todo Specs', () => {
  let tempDir: string;

  beforeEach(() => {
    tempDir = fs.mkdtempSync(path.join(require('os').tmpdir(), 'linespec-'));
  });

  afterEach(() => {
    fs.rmSync(tempDir, { recursive: true, force: true });
  });

  const todoTestCases = testCases.filter(tc => tc.examplesDir === TODO_EXAMPLES_DIR);

  for (const tc of todoTestCases) {
    describe(tc.name, () => {
      it('should compile .linespec to KTest and KMock YAML', () => {
        const specFile = path.join(tc.examplesDir, tc.file);
        compileSpec(specFile, tempDir);

        const ktest = loadKTest(tempDir, tc.name);
        expect(ktest).toBeDefined();
        expect(ktest.version).toBe('api.keploy.io/v1beta1');
        expect(ktest.kind).toBe('Http');
        expect(ktest.name).toBe(tc.name);

        const spec = ktest.spec as Record<string, unknown>;
        expect(spec.req).toBeDefined();
        expect((spec.req as Record<string, unknown>).method).toBe(tc.method);
        expect((spec.req as Record<string, unknown>).url).toBe(tc.url);
        expect((spec.req as Record<string, unknown>).proto_major).toBe(1);

        expect(spec.resp).toBeDefined();
        expect((spec.resp as Record<string, unknown>).status_code).toBe(tc.statusCode);
        expect((spec.resp as Record<string, unknown>).status_message).toBe(tc.statusMessage);
        expect((spec.resp as Record<string, unknown>).proto_major).toBe(0);

        expect(spec.app_port).toBe(3000);

        const reqBody = (spec.req as Record<string, unknown>).body as string;
        if (tc.hasRequestBody) {
          expect(reqBody).toBeTruthy();
        } else {
          expect(reqBody).toBe('');
        }

        const respBody = (spec.resp as Record<string, unknown>).body as string;
        if (tc.hasResponseBody) {
          expect(respBody).toBeTruthy();
        } else {
          expect(respBody).toBe('');
        }

        if (tc.noiseFields && tc.noiseFields.length > 0) {
          expect(spec.assertions).toBeDefined();
          const assertions = spec.assertions as Record<string, unknown>;
          expect(assertions.noise).toBeDefined();
          const noise = assertions.noise as Record<string, unknown>;
          for (const field of tc.noiseFields) {
            expect(noise[field]).toBeDefined();
          }
        } else {
          expect(spec.assertions).toBeUndefined();
        }

        const mocks = loadKMocks(tempDir);
        expect(mocks).toHaveLength(tc.mockCount);

        // Check HTTP mocks count
        if (tc.expectedHttpMocks !== undefined) {
          const httpMocks = mocks.filter(m => m.kind === 'Http');
          expect(httpMocks).toHaveLength(tc.expectedHttpMocks);
        }

        // Check MySQL mocks count
        if (tc.expectedMysqlMocks !== undefined) {
          const mysqlMocks = mocks.filter(m => m.kind === 'MySQL');
          expect(mysqlMocks).toHaveLength(tc.expectedMysqlMocks);
        }

        const queryMocks = mocks.filter((mock) => {
          const mockSpec = mock.spec as Record<string, unknown>;
          const metadata = mockSpec.metadata as Record<string, unknown>;
          return metadata?.type === 'mocks';
        });

        expect(queryMocks.length).toBeGreaterThan(0);

        for (let i = 0; i < queryMocks.length; i++) {
          const mock = queryMocks[i];
          expect(mock.version).toBe('api.keploy.io/v1beta1');
          expect(mock.kind).toBe('MySQL');

          const mockSpec = mock.spec as Record<string, unknown>;
          expect(mockSpec.requests).toBeDefined();
          const requests = mockSpec.requests as Array<Record<string, unknown>>;
          expect(requests.length).toBeGreaterThan(0);

          const request = requests[0];
          expect(request.message).toBeDefined();
          const message = request.message as Record<string, unknown>;
          expect(message.query).toBeTruthy();
          expect(typeof message.query).toBe('string');

          const header = request.header as Record<string, unknown>;
          expect(header.packet_type).toBe('COM_QUERY');

          expect(mockSpec.responses).toBeDefined();
          const responses = mockSpec.responses as Array<Record<string, unknown>>;
          expect(responses.length).toBeGreaterThan(0);
          expect(responses[0].message).toBeDefined();
          expect(typeof responses[0].message).toBe('object');
        }
      });
    });
  }
});

describe('Integration Tests - User Specs', () => {
  let tempDir: string;

  beforeEach(() => {
    tempDir = fs.mkdtempSync(path.join(require('os').tmpdir(), 'linespec-'));
  });

  afterEach(() => {
    fs.rmSync(tempDir, { recursive: true, force: true });
  });

  const userTestCases = testCases.filter(tc => tc.examplesDir === USER_EXAMPLES_DIR);

  for (const tc of userTestCases) {
    describe(tc.name, () => {
      it('should compile .linespec to KTest and KMock YAML', () => {
        const specFile = path.join(tc.examplesDir, tc.file);
        compileSpec(specFile, tempDir);

        const ktest = loadKTest(tempDir, tc.name);
        expect(ktest).toBeDefined();
        expect(ktest.version).toBe('api.keploy.io/v1beta1');
        expect(ktest.kind).toBe('Http');
        expect(ktest.name).toBe(tc.name);

        const spec = ktest.spec as Record<string, unknown>;
        expect(spec.req).toBeDefined();
        expect((spec.req as Record<string, unknown>).method).toBe(tc.method);
        expect((spec.req as Record<string, unknown>).url).toBe(tc.url);
        expect((spec.req as Record<string, unknown>).proto_major).toBe(1);

        expect(spec.resp).toBeDefined();
        expect((spec.resp as Record<string, unknown>).status_code).toBe(tc.statusCode);
        expect((spec.resp as Record<string, unknown>).status_message).toBe(tc.statusMessage);
        expect((spec.resp as Record<string, unknown>).proto_major).toBe(0);

        expect(spec.app_port).toBe(3000);

        const reqBody = (spec.req as Record<string, unknown>).body as string;
        if (tc.hasRequestBody) {
          expect(reqBody).toBeTruthy();
        } else {
          expect(reqBody).toBe('');
        }

        const respBody = (spec.resp as Record<string, unknown>).body as string;
        if (tc.hasResponseBody) {
          expect(respBody).toBeTruthy();
        } else {
          expect(respBody).toBe('');
        }

        if (tc.noiseFields && tc.noiseFields.length > 0) {
          expect(spec.assertions).toBeDefined();
          const assertions = spec.assertions as Record<string, unknown>;
          expect(assertions.noise).toBeDefined();
          const noise = assertions.noise as Record<string, unknown>;
          for (const field of tc.noiseFields) {
            expect(noise[field]).toBeDefined();
          }
        } else {
          expect(spec.assertions).toBeUndefined();
        }

        const mocks = loadKMocks(tempDir);
        expect(mocks).toHaveLength(tc.mockCount);

        // Check HTTP mocks count
        if (tc.expectedHttpMocks !== undefined) {
          const httpMocks = mocks.filter(m => m.kind === 'Http');
          expect(httpMocks).toHaveLength(tc.expectedHttpMocks);
        }

        // Check MySQL mocks count
        if (tc.expectedMysqlMocks !== undefined) {
          const mysqlMocks = mocks.filter(m => m.kind === 'MySQL');
          expect(mysqlMocks).toHaveLength(tc.expectedMysqlMocks);
        }

        const queryMocks = mocks.filter((mock) => {
          const mockSpec = mock.spec as Record<string, unknown>;
          const metadata = mockSpec.metadata as Record<string, unknown>;
          return metadata?.type === 'mocks';
        });

        expect(queryMocks.length).toBeGreaterThan(0);

        for (let i = 0; i < queryMocks.length; i++) {
          const mock = queryMocks[i];
          expect(mock.version).toBe('api.keploy.io/v1beta1');
          expect(mock.kind).toBe('MySQL');

          const mockSpec = mock.spec as Record<string, unknown>;
          expect(mockSpec.requests).toBeDefined();
          const requests = mockSpec.requests as Array<Record<string, unknown>>;
          expect(requests.length).toBeGreaterThan(0);

          const request = requests[0];
          expect(request.message).toBeDefined();
          const message = request.message as Record<string, unknown>;
          expect(message.query).toBeTruthy();
          expect(typeof message.query).toBe('string');

          const header = request.header as Record<string, unknown>;
          expect(header.packet_type).toBe('COM_QUERY');

          expect(mockSpec.responses).toBeDefined();
          const responses = mockSpec.responses as Array<Record<string, unknown>>;
          expect(responses.length).toBeGreaterThan(0);
          expect(responses[0].message).toBeDefined();
          expect(typeof responses[0].message).toBe('object');
        }
      });
    });
  }
});
