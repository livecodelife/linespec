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
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
exports.runTests = runTests;
const net = __importStar(require("net"));
const fs = __importStar(require("fs"));
const path = __importStar(require("path"));
const yaml = __importStar(require("js-yaml"));
const child_process_1 = require("child_process");
const http_1 = __importDefault(require("http"));
const https_1 = __importDefault(require("https"));
const mysql_proxy_1 = require("./mysql-proxy");
async function findFreePort() {
    return new Promise((resolve, reject) => {
        const server = net.createServer();
        server.listen(0, () => {
            const address = server.address();
            const port = address.port;
            server.close(() => resolve(port));
        });
        server.on('error', reject);
    });
}
function parseComposeFile(composePath) {
    if (!fs.existsSync(composePath)) {
        throw new Error(`Compose file not found: ${composePath}`);
    }
    const content = fs.readFileSync(composePath, 'utf8');
    return yaml.load(content);
}
function getDbUpstreamPort(composeParsed) {
    try {
        const services = composeParsed.services;
        const db = services.db;
        const ports = db.ports;
        if (ports && ports.length > 0) {
            const portMapping = ports[0];
            const parts = portMapping.split(':');
            return parseInt(parts[0], 10);
        }
    }
    catch {
        // ignore and return default
    }
    return 3306;
}
function buildOverrideContent(composeParsed, proxyPort) {
    const services = composeParsed.services;
    if (!services || !services.web) {
        throw new Error('Compose file must have a "web" service defined');
    }
    const web = services.web;
    const env = web.environment ?? {};
    let databaseUrl = env.DATABASE_URL;
    if (databaseUrl) {
        databaseUrl = databaseUrl.replace(/@[^@/:]+:\d+\//, `@host.docker.internal:${proxyPort}/`);
    }
    const overrideEnv = {};
    if (databaseUrl) {
        overrideEnv.DATABASE_URL = databaseUrl;
    }
    if (env.DB_HOST) {
        overrideEnv.DB_HOST = 'host.docker.internal';
    }
    const overrideObject = {
        services: {
            web: {
                environment: overrideEnv,
            },
        },
    };
    return yaml.dump(overrideObject);
}
function spawnProcess(cmd, args) {
    return new Promise((resolve, reject) => {
        const proc = (0, child_process_1.spawn)(cmd, args, { stdio: 'inherit' });
        proc.on('error', reject);
        proc.on('close', (code) => {
            if (code === 0) {
                resolve();
            }
            else {
                reject(new Error(`${cmd} ${args.join(' ')} exited with code ${code}`));
            }
        });
    });
}
async function pollUntilHealthy(serviceUrl, timeoutMs = 120000) {
    const url = new URL(serviceUrl);
    const startTime = Date.now();
    while (Date.now() - startTime < timeoutMs) {
        try {
            await new Promise((resolve, reject) => {
                const lib = url.protocol === 'https:' ? https_1.default : http_1.default;
                const req = lib.get(serviceUrl, (res) => {
                    res.on('data', () => { });
                    res.on('end', () => {
                        if (url.pathname === '/' && (res.statusCode === 200 || res.statusCode === 404)) {
                            resolve();
                        }
                        else if (url.pathname !== '/' && res.statusCode === 200) {
                            resolve();
                        }
                        else {
                            reject(new Error(`unexpected status ${res.statusCode}`));
                        }
                    });
                });
                req.on('error', reject);
                req.setTimeout(5000, () => {
                    req.destroy();
                    reject(new Error('timeout'));
                });
            });
            return;
        }
        catch {
            await new Promise((r) => setTimeout(r, 2000));
            process.stdout.write('…waiting for service\n');
        }
    }
    throw new Error('Service did not become healthy within timeout');
}
function deleteAtPath(obj, pathParts) {
    if (Array.isArray(obj)) {
        for (const item of obj) {
            deleteAtPath(item, pathParts);
        }
        return;
    }
    if (typeof obj === 'object' && obj !== null) {
        const record = obj;
        if (pathParts.length === 1) {
            delete record[pathParts[0]];
        }
        else {
            deleteAtPath(record[pathParts[0]], pathParts.slice(1));
        }
    }
}
function stripNoise(body, noiseKeys) {
    const cloned = JSON.parse(JSON.stringify(body));
    for (const key of Object.keys(noiseKeys)) {
        const dotPath = key.replace(/^body\./, '');
        const pathParts = dotPath.split('.');
        deleteAtPath(cloned, pathParts);
    }
    return cloned;
}
function prettyPrint(str) {
    try {
        return JSON.stringify(JSON.parse(str), null, 2);
    }
    catch {
        return str;
    }
}
function buildSideBySideDiff(expected, actual) {
    const expectedLines = prettyPrint(expected).split('\n');
    const actualLines = prettyPrint(actual).split('\n');
    const maxExpectedLen = Math.max(...expectedLines.map((l) => l.length));
    const colWidth = Math.min(Math.max(maxExpectedLen, 40), 60);
    const maxLines = Math.max(expectedLines.length, actualLines.length);
    const rows = [];
    for (let i = 0; i < maxLines; i++) {
        const expLine = expectedLines[i] ?? '';
        const actLine = actualLines[i] ?? '';
        const paddedExp = expLine.padEnd(colWidth);
        const separator = expLine === actLine ? '  ' : ' ~ ';
        rows.push(`${paddedExp}${separator}${actLine}`);
    }
    return rows.join('\n');
}
async function runHttpTest(ktest, serviceUrl, name) {
    const url = new URL(ktest.spec.req.url);
    const targetUrl = `${serviceUrl}${url.pathname}${url.search}`;
    const lib = serviceUrl.startsWith('https') ? https_1.default : http_1.default;
    return new Promise((resolve) => {
        const options = {
            method: ktest.spec.req.method,
            headers: { ...ktest.spec.req.header },
        };
        delete options.headers.Host;
        delete options.headers['Content-Length'];
        const expectedStatus = ktest.spec.resp.status_code;
        const req = lib.request(targetUrl, options, (res) => {
            let body = '';
            res.on('data', (chunk) => { body += chunk; });
            res.on('end', () => {
                const actualStatus = res.statusCode ?? 0;
                if (actualStatus !== expectedStatus) {
                    resolve({
                        name,
                        pass: false,
                        reason: `status ${actualStatus} ≠ ${expectedStatus}`,
                        expectedStatus,
                        actualStatus,
                        req: ktest.spec.req,
                    });
                    return;
                }
                let actualBody = body;
                let expectedBody = ktest.spec.resp.body;
                try {
                    const actualJson = JSON.parse(body);
                    const expectedJson = JSON.parse(ktest.spec.resp.body);
                    const noise = ktest.spec.assertions?.noise ?? {};
                    const actualStripped = stripNoise(actualJson, noise);
                    const expectedStripped = stripNoise(expectedJson, noise);
                    actualBody = JSON.stringify(actualStripped);
                    expectedBody = JSON.stringify(expectedStripped);
                }
                catch {
                    // not JSON, use raw comparison
                }
                if (actualBody !== expectedBody) {
                    const diff = buildSideBySideDiff(expectedBody, actualBody);
                    resolve({
                        name,
                        pass: false,
                        reason: 'body mismatch',
                        expectedStatus,
                        actualStatus,
                        expectedBody,
                        actualBody,
                        diff,
                        req: ktest.spec.req,
                    });
                    return;
                }
                resolve({
                    name,
                    pass: true,
                    expectedStatus,
                    actualStatus,
                    expectedBody,
                    actualBody,
                    req: ktest.spec.req,
                });
            });
        });
        req.on('error', (err) => {
            resolve({
                name,
                pass: false,
                reason: err.message,
                expectedStatus: ktest.spec.resp.status_code,
                req: ktest.spec.req,
            });
        });
        const method = ktest.spec.req.method.toUpperCase();
        if (method !== 'GET' && method !== 'HEAD' && ktest.spec.req.body) {
            req.write(ktest.spec.req.body);
        }
        req.end();
    });
}
async function runTests(testSet, options) {
    const useCompose = options.composePath && fs.existsSync(options.composePath);
    const composePath = options.composePath ?? 'todo-api/docker-compose.yml';
    const overridePath = useCompose
        ? path.join(path.dirname(composePath), '.linespec-compose-override.yml')
        : '';
    let proxyServer = null;
    const dbPort = options.dbPort ?? 3306;
    try {
        const proxyPort = await findFreePort();
        const mocks = testSet.mocks.map((m) => m.mock);
        if (useCompose) {
            const composeParsed = parseComposeFile(options.composePath);
            const overrideContent = buildOverrideContent(composeParsed, proxyPort);
            fs.writeFileSync(overridePath, overrideContent);
            process.stdout.write('→ Starting db service...\n');
            await spawnProcess('docker', [
                'compose',
                '-f', options.composePath,
                'up', '-d', 'db'
            ]);
            await new Promise((resolve) => setTimeout(resolve, 5000));
            proxyServer = await (0, mysql_proxy_1.startProxy)(mocks, 'localhost', dbPort, proxyPort);
            process.stdout.write(`✓ MySQL proxy listening on port ${proxyPort} → localhost:${dbPort}\n`);
            process.stdout.write('→ Starting web service...\n');
            await spawnProcess('docker', [
                'compose',
                '-f', options.composePath,
                '-f', overridePath,
                'up', '-d', '--build', 'web'
            ]);
        }
        else {
            proxyServer = await (0, mysql_proxy_1.startProxy)(mocks, 'localhost', dbPort, proxyPort);
            process.stdout.write(`✓ MySQL proxy listening on port ${proxyPort} → localhost:${dbPort}\n`);
            process.stdout.write('→ Waiting for service (ensure it is already running)...\n');
        }
        await pollUntilHealthy(options.serviceUrl, 120000);
        process.stdout.write(`✓ Service healthy at ${options.serviceUrl}\n`);
        let passed = 0;
        let failed = 0;
        const results = [];
        for (const { name, ktest } of testSet.tests) {
            const result = await runHttpTest(ktest, options.serviceUrl, name);
            results.push(result);
            if (result.pass) {
                passed++;
                process.stdout.write(`✓ ${name} PASS\n`);
            }
            else {
                failed++;
                process.stdout.write(`✗ ${name} FAIL (${result.reason})\n`);
                if (result.expectedStatus !== undefined && result.actualStatus !== undefined) {
                    process.stdout.write(`  Expected status : ${result.expectedStatus}\n`);
                    process.stdout.write(`  Actual status   : ${result.actualStatus}\n`);
                }
                if (result.diff) {
                    const diffLines = result.diff.split('\n');
                    for (const line of diffLines) {
                        if (line.includes(' ~ ')) {
                            const parts = line.split(' ~ ');
                            const leftCol = parts[0] ?? '';
                            const rightCol = parts[1] ?? '';
                            const leftMatches = leftCol === rightCol;
                            if (!leftMatches) {
                                process.stdout.write(`\x1b[31m${leftCol}\x1b[0m  ~  \x1b[32m${rightCol}\x1b[0m\n`);
                            }
                            else {
                                process.stdout.write(`${line}\n`);
                            }
                        }
                        else {
                            process.stdout.write(`${line}\n`);
                        }
                    }
                }
            }
        }
        if (options.reportDir) {
            fs.mkdirSync(options.reportDir, { recursive: true });
            const summary = {
                passed,
                failed,
                total: passed + failed,
                tests: results.map((r) => ({
                    name: r.name,
                    pass: r.pass,
                    reason: r.reason,
                })),
            };
            fs.writeFileSync(path.join(options.reportDir, 'summary.json'), JSON.stringify(summary, null, 2));
            for (const result of results) {
                const safeName = result.name.replace(/[^a-zA-Z0-9_-]/g, '_');
                fs.writeFileSync(path.join(options.reportDir, `${safeName}.json`), JSON.stringify(result, null, 2));
            }
            process.stdout.write(`→ Report written to ${options.reportDir}/\n`);
        }
        process.stdout.write(`\nsummary: ${passed} passed, ${failed} failed\n`);
        if (failed > 0) {
            process.exitCode = 1;
        }
    }
    finally {
        if (proxyServer) {
            proxyServer.close();
        }
        if (useCompose) {
            try {
                await spawnProcess('docker', [
                    'compose',
                    '-f', options.composePath,
                    'rm', '-fs', 'web'
                ]);
            }
            catch {
                // ignore cleanup errors
            }
            if (fs.existsSync(overridePath)) {
                fs.unlinkSync(overridePath);
            }
        }
    }
}
