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
exports.loadTestSet = loadTestSet;
const fs = __importStar(require("fs"));
const path = __importStar(require("path"));
const yaml = __importStar(require("js-yaml"));
function loadTestSet(dir) {
    const testsDir = path.join(dir, 'tests');
    if (!fs.existsSync(testsDir)) {
        throw new Error(`Tests directory not found: ${testsDir}`);
    }
    const testFiles = fs.readdirSync(testsDir).filter((file) => file.endsWith('.yaml'));
    const tests = [];
    for (const file of testFiles) {
        const name = path.basename(file, '.yaml');
        const filePath = path.join(testsDir, file);
        try {
            const raw = fs.readFileSync(filePath, 'utf-8');
            const ktest = yaml.load(raw);
            tests.push({ name, ktest });
        }
        catch (err) {
            if (err instanceof Error) {
                throw new Error(`Failed to parse test file ${file}: ${err.message}`);
            }
            throw err;
        }
    }
    const mocksPath = path.join(dir, 'mocks.yaml');
    const mocks = [];
    const mocksByTest = new Map();
    if (fs.existsSync(mocksPath)) {
        try {
            const raw = fs.readFileSync(mocksPath, 'utf-8');
            const docs = yaml.loadAll(raw);
            for (const mock of docs) {
                if (mock && typeof mock.name === 'string') {
                    mocks.push({ name: mock.name, mock });
                    // Parse test name from mock name (format: {test-name}-mock-{number} or {test-name}-mock-{type}-{number})
                    const match = mock.name.match(/^(.+)-mock-/);
                    if (match) {
                        const testName = match[1];
                        if (!mocksByTest.has(testName)) {
                            mocksByTest.set(testName, []);
                        }
                        mocksByTest.get(testName).push({ name: mock.name, mock });
                    }
                }
            }
        }
        catch (err) {
            if (err instanceof Error) {
                throw new Error(`Failed to parse mocks file: ${err.message}`);
            }
            throw err;
        }
    }
    return { dir, tests, mocks, mocksByTest };
}
