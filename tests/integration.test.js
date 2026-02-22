"use strict";
var __createBinding = (this && this.__createBinding) || (Object.create ? (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    var desc = Object.getOwnPropertyDescriptor(m, k);
    if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) {
      desc = { enumerable: true, get: function() { return m[k]; } };
    }
    Object.defineProperty(o, k2, desc);
}) : (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    o[k2] = m[k];
}));
var __setModuleDefault = (this && this.__setModuleDefault) || (Object.create ? (function(o, v) {
    Object.defineProperty(o, "default", { enumerable: true, value: v });
}) : function(o, v) {
    o["default"] = v;
});
var __importStar = (this && this.__importStar) || (function () {
    var ownKeys = function(o) {
        ownKeys = Object.getOwnPropertyNames || function (o) {
            var ar = [];
            for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k)) ar[ar.length] = k;
            return ar;
        };
        return ownKeys(o);
    };
    return function (mod) {
        if (mod && mod.__esModule) return mod;
        var result = {};
        if (mod != null) for (var k = ownKeys(mod), i = 0; i < k.length; i++) if (k[i] !== "default") __createBinding(result, mod, k[i]);
        __setModuleDefault(result, mod);
        return result;
    };
})();
Object.defineProperty(exports, "__esModule", { value: true });
const vitest_1 = require("vitest");
const fs = __importStar(require("fs"));
const path = __importStar(require("path"));
const yaml = __importStar(require("js-yaml"));
const lexer_1 = require("../src/lexer");
const parser_1 = require("../src/parser");
const validator_1 = require("../src/validator");
const compiler_1 = require("../src/compiler");
const EXAMPLES_DIR = path.join(__dirname, '..', 'examples', 'test-set-0');
function compileSpec(specFile, outDir) {
    const source = fs.readFileSync(specFile, 'utf-8');
    const tokens = (0, lexer_1.tokenize)(source);
    const spec = (0, parser_1.parse)(tokens, specFile);
    (0, validator_1.validate)(spec, path.dirname(specFile));
    (0, compiler_1.compile)(spec, { outDir, baseDir: path.dirname(specFile) });
}
function loadKTest(outDir, name) {
    const content = fs.readFileSync(path.join(outDir, 'tests', `${name}.yaml`), 'utf-8');
    return yaml.load(content);
}
function loadKMocks(outDir) {
    const content = fs.readFileSync(path.join(outDir, 'mocks.yaml'), 'utf-8');
    return yaml.loadAll(content);
}
function loadPayload(payloadPath) {
    const content = fs.readFileSync(payloadPath, 'utf-8');
    try {
        return yaml.load(content);
    }
    catch {
        return {};
    }
}
function deepMatch(actual, expected) {
    if (typeof actual !== 'object' || actual === null) {
        return actual === expected;
    }
    const actualObj = actual;
    for (const key of Object.keys(expected)) {
        if (!(key in actualObj)) {
            return false;
        }
        if (typeof expected[key] === 'object' && expected[key] !== null && !Array.isArray(expected[key])) {
            if (!deepMatch(actualObj[key], expected[key])) {
                return false;
            }
        }
        else if (Array.isArray(expected[key])) {
            const actualArr = actualObj[key];
            if (!Array.isArray(actualArr))
                return false;
            const expectedArr = expected[key];
            if (actualArr.length !== expectedArr.length)
                return false;
            for (let i = 0; i < expectedArr.length; i++) {
                if (typeof expectedArr[i] === 'object' && expectedArr[i] !== null) {
                    if (!deepMatch(actualArr[i], expectedArr[i])) {
                        return false;
                    }
                }
                else if (actualArr[i] !== expectedArr[i]) {
                    return false;
                }
            }
        }
        else if (actualObj[key] !== expected[key]) {
            return false;
        }
    }
    return true;
}
const testCases = [
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
(0, vitest_1.describe)('Integration Tests', () => {
    let tempDir;
    (0, vitest_1.beforeEach)(() => {
        tempDir = fs.mkdtempSync(path.join(require('os').tmpdir(), 'linespec-'));
    });
    (0, vitest_1.afterEach)(() => {
        fs.rmSync(tempDir, { recursive: true, force: true });
    });
    for (const tc of testCases) {
        (0, vitest_1.describe)(tc.name, () => {
            (0, vitest_1.it)('should compile .linespec to KTest and KMock YAML', () => {
                const specFile = path.join(EXAMPLES_DIR, `${tc.name}.linespec`);
                compileSpec(specFile, tempDir);
                const ktest = loadKTest(tempDir, tc.name);
                (0, vitest_1.expect)(ktest).toBeDefined();
                (0, vitest_1.expect)(ktest.version).toBe('api.keploy.io/v1beta1');
                (0, vitest_1.expect)(ktest.kind).toBe('Http');
                (0, vitest_1.expect)(ktest.name).toBe(tc.name);
                const spec = ktest.spec;
                (0, vitest_1.expect)(spec.req).toBeDefined();
                (0, vitest_1.expect)(spec.req.method).toBe(tc.method);
                (0, vitest_1.expect)(spec.req.url).toBe(tc.url);
                (0, vitest_1.expect)(spec.req.proto_major).toBe(1);
                (0, vitest_1.expect)(spec.resp).toBeDefined();
                (0, vitest_1.expect)(spec.resp.status_code).toBe(tc.statusCode);
                (0, vitest_1.expect)(spec.resp.status_message).toBe(tc.statusMessage);
                (0, vitest_1.expect)(spec.resp.proto_major).toBe(0);
                (0, vitest_1.expect)(spec.app_port).toBe(3000);
                const reqBody = spec.req.body;
                if (tc.hasRequestBody) {
                    (0, vitest_1.expect)(reqBody).toBeTruthy();
                }
                else {
                    (0, vitest_1.expect)(reqBody).toBe('');
                }
                const respBody = spec.resp.body;
                if (tc.hasResponseBody) {
                    (0, vitest_1.expect)(respBody).toBeTruthy();
                }
                else {
                    (0, vitest_1.expect)(respBody).toBe('');
                }
                if (tc.hasTimestamps) {
                    (0, vitest_1.expect)(spec.assertions).toBeDefined();
                    const assertions = spec.assertions;
                    (0, vitest_1.expect)(assertions.noise).toBeDefined();
                    const noise = assertions.noise;
                    (0, vitest_1.expect)(noise['body.created_at']).toBeDefined();
                    (0, vitest_1.expect)(noise['body.updated_at']).toBeDefined();
                }
                const mocks = loadKMocks(tempDir);
                (0, vitest_1.expect)(mocks).toHaveLength(tc.mockCount);
                for (let i = 0; i < mocks.length; i++) {
                    const mock = mocks[i];
                    (0, vitest_1.expect)(mock.version).toBe('api.keploy.io/v1beta1');
                    (0, vitest_1.expect)(mock.kind).toBe('MySQL');
                    const mockSpec = mock.spec;
                    (0, vitest_1.expect)(mockSpec.requests).toBeDefined();
                    const requests = mockSpec.requests;
                    (0, vitest_1.expect)(requests.length).toBeGreaterThan(0);
                    const request = requests[0];
                    (0, vitest_1.expect)(request.message).toBeDefined();
                    const message = request.message;
                    (0, vitest_1.expect)(message.query).toBeTruthy();
                    (0, vitest_1.expect)(typeof message.query).toBe('string');
                    (0, vitest_1.expect)(message.query.includes(tc.tableName)).toBe(true);
                    const header = request.header;
                    (0, vitest_1.expect)(header.packet_type).toBe('COM_QUERY');
                    (0, vitest_1.expect)(mockSpec.responses).toBeDefined();
                    const responses = mockSpec.responses;
                    (0, vitest_1.expect)(responses.length).toBeGreaterThan(0);
                    (0, vitest_1.expect)(responses[0].message).toBeDefined();
                    (0, vitest_1.expect)(typeof responses[0].message).toBe('object');
                    if (tc.payloadFiles && tc.payloadFiles[i]) {
                        const payloadFile = tc.payloadFiles[i];
                        const expectedPayload = loadPayload(path.join(EXAMPLES_DIR, 'payloads', payloadFile));
                        const actualMessage = responses[0].message;
                        const keysToCheck = Object.keys(expectedPayload);
                        for (const key of keysToCheck) {
                            (0, vitest_1.expect)(actualMessage).toHaveProperty(key);
                            if (typeof expectedPayload[key] === 'object' && expectedPayload[key] !== null) {
                                (0, vitest_1.expect)(deepMatch(actualMessage[key], expectedPayload[key])).toBe(true);
                            }
                            else {
                                (0, vitest_1.expect)(actualMessage[key]).toEqual(expectedPayload[key]);
                            }
                        }
                    }
                }
            });
        });
    }
});
