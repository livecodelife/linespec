import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import * as fs from 'fs';
import * as path from 'path';
import * as yaml from 'js-yaml';
import { tokenize } from '../src/lexer';
import { parse } from '../src/parser';
import { validate } from '../src/validator';
import { compile } from '../src/compiler';

const EXAMPLES_DIR = path.join(__dirname, '..', 'examples', 'test-set-0');

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
  method: string;
  url: string;
  statusCode: number;
  statusMessage: string;
  mockCount: number;
  hasRequestBody: boolean;
  hasResponseBody: boolean;
  hasTimestamps: boolean;
  tableName: string;
  payloadFiles?: string[];
}

function loadPayload(payloadPath: string): Record<string, unknown> {
  const content = fs.readFileSync(payloadPath, 'utf-8');
  try {
    return yaml.load(content) as Record<string, unknown>;
  } catch {
    return {};
  }
}

function deepMatch(actual: unknown, expected: Record<string, unknown>): boolean {
  if (typeof actual !== 'object' || actual === null) {
    return actual === expected;
  }
  const actualObj = actual as Record<string, unknown>;
  for (const key of Object.keys(expected)) {
    if (!(key in actualObj)) {
      return false;
    }
    if (typeof expected[key] === 'object' && expected[key] !== null && !Array.isArray(expected[key])) {
      if (!deepMatch(actualObj[key], expected[key] as Record<string, unknown>)) {
        return false;
      }
    } else if (Array.isArray(expected[key])) {
      const actualArr = actualObj[key];
      if (!Array.isArray(actualArr)) return false;
      const expectedArr = expected[key] as unknown[];
      if (actualArr.length !== expectedArr.length) return false;
      for (let i = 0; i < expectedArr.length; i++) {
        if (typeof expectedArr[i] === 'object' && expectedArr[i] !== null) {
          if (!deepMatch(actualArr[i], expectedArr[i] as Record<string, unknown>)) {
            return false;
          }
        } else if (actualArr[i] !== expectedArr[i]) {
          return false;
        }
      }
    } else if (actualObj[key] !== expected[key]) {
      return false;
    }
  }
  return true;
}

const testCases: TestCase[] = [
  {
    name: 'test-1',
    method: 'POST',
    url: 'http://localhost:3000/users',
    statusCode: 201,
    statusMessage: 'Created',
    mockCount: 1,
    hasRequestBody: true,
    hasResponseBody: true,
    hasTimestamps: true,
    tableName: 'users',
    payloadFiles: ['mysql_user_write_result.yaml'],
  },
  {
    name: 'test-2',
    method: 'GET',
    url: 'http://localhost:3000/users',
    statusCode: 200,
    statusMessage: 'OK',
    mockCount: 1,
    hasRequestBody: false,
    hasResponseBody: true,
    hasTimestamps: true,
    tableName: 'users',
    payloadFiles: ['mysql_users_read_result.yaml'],
  },
  {
    name: 'test-3',
    method: 'GET',
    url: 'http://localhost:3000/users/3',
    statusCode: 200,
    statusMessage: 'OK',
    mockCount: 1,
    hasRequestBody: false,
    hasResponseBody: true,
    hasTimestamps: true,
    tableName: 'users',
    payloadFiles: ['mysql_user_single_read_result.yaml'],
  },
  {
    name: 'test-4',
    method: 'PATCH',
    url: 'http://localhost:3000/users/3',
    statusCode: 200,
    statusMessage: 'OK',
    mockCount: 2,
    hasRequestBody: true,
    hasResponseBody: true,
    hasTimestamps: true,
    tableName: 'users',
    payloadFiles: ['mysql_user_single_read_result.yaml', 'mysql_user_update_result.yaml'],
  },
  {
    name: 'test-5',
    method: 'POST',
    url: 'http://localhost:3000/todos',
    statusCode: 201,
    statusMessage: 'Created',
    mockCount: 1,
    hasRequestBody: true,
    hasResponseBody: true,
    hasTimestamps: true,
    tableName: 'todos',
    payloadFiles: ['mysql_todo_write_result.yaml'],
  },
  {
    name: 'test-6',
    method: 'GET',
    url: 'http://localhost:3000/todos',
    statusCode: 200,
    statusMessage: 'OK',
    mockCount: 1,
    hasRequestBody: false,
    hasResponseBody: true,
    hasTimestamps: true,
    tableName: 'todos',
    payloadFiles: ['mysql_todos_read_result.yaml'],
  },
  {
    name: 'test-7',
    method: 'GET',
    url: 'http://localhost:3000/todos/user/3',
    statusCode: 200,
    statusMessage: 'OK',
    mockCount: 1,
    hasRequestBody: false,
    hasResponseBody: true,
    hasTimestamps: true,
    tableName: 'todos',
    payloadFiles: ['mysql_todos_read_result.yaml'],
  },
  {
    name: 'test-8',
    method: 'GET',
    url: 'http://localhost:3000/todos/3',
    statusCode: 200,
    statusMessage: 'OK',
    mockCount: 1,
    hasRequestBody: false,
    hasResponseBody: true,
    hasTimestamps: true,
    tableName: 'todos',
    payloadFiles: ['mysql_todo_single_read_result.yaml'],
  },
  {
    name: 'test-9',
    method: 'PATCH',
    url: 'http://localhost:3000/todos/3',
    statusCode: 200,
    statusMessage: 'OK',
    mockCount: 2,
    hasRequestBody: true,
    hasResponseBody: true,
    hasTimestamps: true,
    tableName: 'todos',
    payloadFiles: ['mysql_todo_single_read_result.yaml', 'mysql_todo_update_result.yaml'],
  },
  {
    name: 'test-10',
    method: 'DELETE',
    url: 'http://localhost:3000/todos/3',
    statusCode: 204,
    statusMessage: 'No Content',
    mockCount: 1,
    hasRequestBody: false,
    hasResponseBody: false,
    hasTimestamps: false,
    tableName: 'todos',
    payloadFiles: ['mysql_delete_result.yaml'],
  },
  {
    name: 'test-11',
    method: 'DELETE',
    url: 'http://localhost:3000/users/3',
    statusCode: 204,
    statusMessage: 'No Content',
    mockCount: 1,
    hasRequestBody: false,
    hasResponseBody: false,
    hasTimestamps: false,
    tableName: 'users',
    payloadFiles: ['mysql_delete_result.yaml'],
  },
];

describe('Integration Tests', () => {
  let tempDir: string;

  beforeEach(() => {
    tempDir = fs.mkdtempSync(path.join(require('os').tmpdir(), 'linespec-'));
  });

  afterEach(() => {
    fs.rmSync(tempDir, { recursive: true, force: true });
  });

  for (const tc of testCases) {
    describe(tc.name, () => {
      it('should compile .linespec to KTest and KMock YAML', () => {
        const specFile = path.join(EXAMPLES_DIR, `${tc.name}.linespec`);
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

        if (tc.hasTimestamps) {
          expect(spec.assertions).toBeDefined();
          const assertions = spec.assertions as Record<string, unknown>;
          expect(assertions.noise).toBeDefined();
          const noise = assertions.noise as Record<string, unknown>;
          expect(noise['body.created_at']).toBeDefined();
          expect(noise['body.updated_at']).toBeDefined();
        }

        const mocks = loadKMocks(tempDir);
        expect(mocks).toHaveLength(tc.mockCount);

        for (let i = 0; i < mocks.length; i++) {
          const mock = mocks[i];
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
          expect((message.query as string).includes(tc.tableName)).toBe(true);

          const header = request.header as Record<string, unknown>;
          expect(header.packet_type).toBe('COM_QUERY');

          expect(mockSpec.responses).toBeDefined();
          const responses = mockSpec.responses as Array<Record<string, unknown>>;
          expect(responses.length).toBeGreaterThan(0);
          expect(responses[0].message).toBeDefined();
          expect(typeof responses[0].message).toBe('object');

          if (tc.payloadFiles && tc.payloadFiles[i]) {
            const payloadFile = tc.payloadFiles[i];
            const expectedPayload = loadPayload(path.join(EXAMPLES_DIR, 'payloads', payloadFile));
            const actualMessage = responses[0].message as Record<string, unknown>;
            const keysToCheck = Object.keys(expectedPayload);
            for (const key of keysToCheck) {
              expect(actualMessage).toHaveProperty(key);
              if (typeof expectedPayload[key] === 'object' && expectedPayload[key] !== null) {
                expect(deepMatch(actualMessage[key], expectedPayload[key] as Record<string, unknown>)).toBe(true);
              } else {
                expect(actualMessage[key]).toEqual(expectedPayload[key]);
              }
            }
          }
        }
      });
    });
  }
});
