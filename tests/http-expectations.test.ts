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

interface HttpExpectationTestCase {
  name: string;
  file: string;
  examplesDir: string;
  expectedMethod: string;
  expectedUrl: string;
  httpMockCount: number;
}

const httpExpectationTestCases: HttpExpectationTestCase[] = [
  {
    name: 'create_todo_success',
    file: 'create_todo_success.linespec',
    examplesDir: TODO_EXAMPLES_DIR,
    expectedMethod: 'GET',
    expectedUrl: 'http://user-service.local/api/v1/users/auth',
    httpMockCount: 1,
  },
  {
    name: 'get_todo_success',
    file: 'get_todo_success.linespec',
    examplesDir: TODO_EXAMPLES_DIR,
    expectedMethod: 'GET',
    expectedUrl: 'http://user-service.local/api/v1/users/auth',
    httpMockCount: 1,
  },
  {
    name: 'list_todos_success',
    file: 'list_todos_success.linespec',
    examplesDir: TODO_EXAMPLES_DIR,
    expectedMethod: 'GET',
    expectedUrl: 'http://user-service.local/api/v1/users/auth',
    httpMockCount: 1,
  },
  {
    name: 'update_todo_success',
    file: 'update_todo_success.linespec',
    examplesDir: TODO_EXAMPLES_DIR,
    expectedMethod: 'GET',
    expectedUrl: 'http://user-service.local/api/v1/users/auth',
    httpMockCount: 1,
  },
  {
    name: 'delete_todo_success',
    file: 'delete_todo_success.linespec',
    examplesDir: TODO_EXAMPLES_DIR,
    expectedMethod: 'GET',
    expectedUrl: 'http://user-service.local/api/v1/users/auth',
    httpMockCount: 1,
  },
  {
    name: 'get_user_success',
    file: 'get_user_success.linespec',
    examplesDir: USER_EXAMPLES_DIR,
    expectedMethod: 'GET',
    expectedUrl: 'http://user-service.local/api/v1/users/auth',
    httpMockCount: 1,
  },
  {
    name: 'create_user_success',
    file: 'create_user_success.linespec',
    examplesDir: USER_EXAMPLES_DIR,
    expectedMethod: null as any,
    expectedUrl: null as any,
    httpMockCount: 0,
  },
];

describe('HTTP Expectations', () => {
  let tempDir: string;

  beforeEach(() => {
    tempDir = fs.mkdtempSync(path.join(require('os').tmpdir(), 'linespec-http-'));
  });

  afterEach(() => {
    fs.rmSync(tempDir, { recursive: true, force: true });
  });

  for (const tc of httpExpectationTestCases) {
    describe(tc.name, () => {
      it('should compile .linespec with HTTP expectations correctly', () => {
        const specFile = path.join(tc.examplesDir, tc.file);
        compileSpec(specFile, tempDir);

        const ktest = loadKTest(tempDir, tc.name);
        expect(ktest).toBeDefined();
        expect(ktest.version).toBe('api.keploy.io/v1beta1');
        expect(ktest.kind).toBe('Http');
        expect(ktest.name).toBe(tc.name);

        const mocks = loadKMocks(tempDir);
        expect(mocks.length).toBeGreaterThan(0);

        const httpMocks = mocks.filter((mock) => mock.kind === 'Http');
        expect(httpMocks).toHaveLength(tc.httpMockCount);

        if (tc.httpMockCount > 0) {
          for (const httpMock of httpMocks) {
            expect(httpMock.version).toBe('api.keploy.io/v1beta1');
            expect(httpMock.kind).toBe('Http');
            expect(httpMock.name).toMatch(new RegExp(`^${tc.name}-mock-\\d+$`));

            const spec = httpMock.spec as Record<string, unknown>;
            expect(spec.req).toBeDefined();
            expect(spec.resp).toBeDefined();

            const req = spec.req as Record<string, unknown>;
            expect(req.method).toBe(tc.expectedMethod);
            expect(req.url).toBe(tc.expectedUrl);
            expect(req.header).toBeDefined();
            expect(req.header).toHaveProperty('Content-Type');

            if (req.body) {
              expect(typeof req.body).toBe('string');
              expect((req.body as string).length).toBeGreaterThan(0);
            }

            const resp = spec.resp as Record<string, unknown>;
            expect(resp.status_code).toBeDefined();
            expect(typeof resp.status_code).toBe('number');
            expect(resp.status_code).toBeGreaterThanOrEqual(100);
            expect(resp.status_code).toBeLessThan(600);
            expect(resp.header).toBeDefined();
            expect(resp.header).toHaveProperty('Content-Type');

            if (resp.body) {
              expect(typeof resp.body).toBe('string');
              expect((resp.body as string).length).toBeGreaterThan(0);
              
              try {
                const parsedBody = JSON.parse(resp.body as string);
                expect(parsedBody).toBeDefined();
              } catch (e) {
                expect(e).toBeUndefined();
              }
            }

            expect(spec.reqTimestampMock).toBeDefined();
            expect(spec.resTimestampMock).toBeDefined();
            expect(typeof spec.reqTimestampMock).toBe('string');
            expect(typeof spec.resTimestampMock).toBe('string');
          }
        }

        const mysqlMocks = mocks.filter((mock) => mock.kind === 'MySQL');
        expect(mysqlMocks.length).toBeGreaterThan(0);
      });
    });
  }

  describe('HTTP mock with status code from payload', () => {
    it('should extract status code from payload when present', () => {
      const specFile = path.join(TODO_EXAMPLES_DIR, 'create_todo_success.linespec');
      compileSpec(specFile, tempDir);

      const mocks = loadKMocks(tempDir);
      const httpMocks = mocks.filter((mock) => mock.kind === 'Http');
      
      expect(httpMocks.length).toBeGreaterThan(0);
      
      const httpMock = httpMocks[0];
      const spec = httpMock.spec as Record<string, unknown>;
      const resp = spec.resp as Record<string, unknown>;
      
      expect(resp.status_code).toBe(200);
    });
  });

  describe('HTTP mock request body', () => {
    it('should include request body when WITH file is provided', () => {
      const specFile = path.join(TODO_EXAMPLES_DIR, 'create_todo_success.linespec');
      compileSpec(specFile, tempDir);

      const mocks = loadKMocks(tempDir);
      const httpMocks = mocks.filter((mock) => mock.kind === 'Http');
      
      expect(httpMocks.length).toBeGreaterThan(0);
      
      const httpMock = httpMocks[0];
      const spec = httpMock.spec as Record<string, unknown>;
      const req = spec.req as Record<string, unknown>;
      
      expect(req.body).toBeDefined();
      expect(req.body).not.toBe('');
      expect(typeof req.body).toBe('string');
      
      const parsedBody = JSON.parse(req.body as string);
      expect(parsedBody).toHaveProperty('authorization');
    });
  });

  describe('HTTP mock without expectations', () => {
    it('should not generate HTTP mocks when no HTTP expectations exist', () => {
      const specFile = path.join(USER_EXAMPLES_DIR, 'create_user_success.linespec');
      compileSpec(specFile, tempDir);

      const mocks = loadKMocks(tempDir);
      const httpMocks = mocks.filter((mock) => mock.kind === 'Http');
      
      expect(httpMocks).toHaveLength(0);
      
      const mysqlMocks = mocks.filter((mock) => mock.kind === 'MySQL');
      expect(mysqlMocks.length).toBeGreaterThan(0);
    });
  });
});
