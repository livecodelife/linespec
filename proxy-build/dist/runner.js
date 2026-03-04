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
async function reloadMocksViaControlApi(proxyHost, controlPort, mocksYaml) {
    return new Promise((resolve, reject) => {
        const options = {
            hostname: proxyHost,
            port: controlPort,
            path: '/reload',
            method: 'POST',
            headers: {
                'Content-Type': 'text/yaml',
                'Content-Length': Buffer.byteLength(mocksYaml),
            },
            timeout: 10000,
        };
        const req = http_1.default.request(options, (res) => {
            let data = '';
            res.on('data', chunk => { data += chunk; });
            res.on('end', () => {
                if (res.statusCode === 200) {
                    resolve();
                }
                else {
                    reject(new Error(`Control API returned ${res.statusCode}: ${data}`));
                }
            });
        });
        req.on('error', (err) => {
            reject(err);
        });
        req.on('timeout', () => {
            req.destroy();
            reject(new Error('Control API request timeout'));
        });
        req.write(mocksYaml);
        req.end();
    });
}
async function clearProxyErrorsViaControlApi(proxyHost, controlPort) {
    return new Promise((resolve, reject) => {
        const options = {
            hostname: proxyHost,
            port: controlPort,
            path: '/clear-errors',
            method: 'POST',
            timeout: 5000,
        };
        const req = http_1.default.request(options, (res) => {
            if (res.statusCode === 200) {
                resolve();
            }
            else {
                reject(new Error(`Control API returned ${res.statusCode}`));
            }
        });
        req.on('error', reject);
        req.on('timeout', () => {
            req.destroy();
            reject(new Error('Control API request timeout'));
        });
        req.end();
    });
}
// Optimization 5: Mock Aggregation - activate mocks for specific test
async function activateMocksViaControlApi(proxyHost, controlPort, testName) {
    return new Promise((resolve, reject) => {
        const postData = JSON.stringify({ testName });
        const options = {
            hostname: proxyHost,
            port: controlPort,
            path: '/activate',
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Content-Length': Buffer.byteLength(postData),
            },
            timeout: 10000,
        };
        const req = http_1.default.request(options, (res) => {
            let data = '';
            res.on('data', chunk => { data += chunk; });
            res.on('end', () => {
                if (res.statusCode === 200) {
                    const result = JSON.parse(data);
                    resolve({ count: result.count });
                }
                else {
                    reject(new Error(`Control API returned ${res.statusCode}: ${data}`));
                }
            });
        });
        req.on('error', (err) => {
            reject(err);
        });
        req.on('timeout', () => {
            req.destroy();
            reject(new Error('Control API request timeout'));
        });
        req.write(postData);
        req.end();
    });
}
// Check HTTP mock usage - returns unused mock names
async function checkHttpMockUsage(proxyHost, controlPort) {
    return new Promise((resolve, reject) => {
        const options = {
            hostname: proxyHost,
            port: controlPort,
            path: '/check-http-mocks',
            method: 'GET',
            timeout: 5000,
        };
        const req = http_1.default.request(options, (res) => {
            let data = '';
            res.on('data', chunk => { data += chunk; });
            res.on('end', () => {
                if (res.statusCode === 200) {
                    const result = JSON.parse(data);
                    resolve({ unused: result.unused || [] });
                }
                else {
                    reject(new Error(`Control API returned ${res.statusCode}: ${data}`));
                }
            });
        });
        req.on('error', reject);
        req.on('timeout', () => {
            req.destroy();
            reject(new Error('Control API request timeout'));
        });
        req.end();
    });
}
// Global Kafka message tracking
let kafkaMessages = new Map();
let kafkaConsumerProcess = null;
// Start Kafka consumer using docker exec
async function startKafkaConsumerDocker(composePath) {
    // Don't start a persistent consumer - we'll read messages directly after each test
    console.error('[runner] Kafka consumer ready (will consume after each test)');
}
// Read messages from Kafka topic using docker exec
async function readKafkaMessages() {
    return new Promise((resolve) => {
        const messages = [];
        // Run kafka-console-consumer to read messages
        const proc = (0, child_process_1.spawn)('docker', [
            'run', '--rm',
            '--network', 'todo-api_default',
            'confluentinc/cp-kafka:latest',
            'kafka-console-consumer',
            '--bootstrap-server', 'kafka:29092',
            '--topic', 'todo-events',
            '--from-beginning',
            '--timeout-ms', '5000'
        ], { stdio: ['ignore', 'pipe', 'pipe'] });
        let output = '';
        proc.stdout.on('data', (data) => {
            output += data.toString();
        });
        proc.on('close', () => {
            // Parse each line as a JSON message
            const lines = output.split('\n').filter(line => line.trim());
            for (const line of lines) {
                try {
                    const event = JSON.parse(line);
                    messages.push(event);
                }
                catch {
                    // Ignore non-JSON lines
                }
            }
            resolve(messages);
        });
        // Timeout after 10 seconds
        setTimeout(() => {
            proc.kill();
            resolve(messages);
        }, 10000);
    });
}
// Stop Kafka consumer
function stopKafkaConsumer() {
    // Nothing to stop with the new approach
}
// Initialize Kafka consumer
async function initKafkaConsumer(brokers) {
    // Use docker-based consumer instead of KafkaJS
    return;
}
// Check Kafka events by reading from the topic
async function checkKafkaEvents(testName, expectedEvents) {
    // Wait for messages to be produced and committed
    console.error('[runner] Waiting for Kafka messages...');
    await new Promise(resolve => setTimeout(resolve, 2000));
    // Read messages from Kafka
    console.error('[runner] Reading messages from Kafka...');
    const messages = await readKafkaMessages();
    console.error(`[runner] Read ${messages.length} messages from Kafka`);
    // Group messages by event type
    const messagesByType = new Map();
    for (const msg of messages) {
        const eventType = msg.event_type;
        if (!messagesByType.has(eventType)) {
            messagesByType.set(eventType, []);
        }
        messagesByType.get(eventType)?.push(msg);
    }
    const unused = [];
    for (const expected of expectedEvents) {
        const events = messagesByType.get(expected.eventType) || [];
        // Check if any event of this type was consumed
        if (events.length === 0) {
            unused.push(expected.mockName);
        }
    }
    return { unused };
}
// Reset Kafka messages for a new test
function resetKafkaMessages() {
    kafkaMessages.clear();
}
function parseComposeFile(composePath) {
    if (!fs.existsSync(composePath)) {
        throw new Error(`Compose file not found: ${composePath}`);
    }
    const content = fs.readFileSync(composePath, 'utf8');
    return yaml.load(content);
}
function getDbServiceName(composeParsed) {
    const services = composeParsed.services;
    if (!services)
        return null;
    // Look for common database service names
    const commonDbNames = ['db', 'database', 'mysql', 'postgres', 'postgresql'];
    for (const name of commonDbNames) {
        if (services[name]) {
            return name;
        }
    }
    // Try to detect by image or ports
    for (const [name, service] of Object.entries(services)) {
        const svc = service;
        // Check image name
        const image = svc.image || '';
        if (image.includes('mysql') || image.includes('postgres') || image.includes('mariadb')) {
            return name;
        }
        // Check for common DB ports
        const ports = svc.ports;
        if (ports) {
            for (const port of ports) {
                if (port.includes('3306') || port.includes('5432')) {
                    return name;
                }
            }
        }
    }
    return null;
}
function getDbUpstreamHost(composeParsed) {
    const dbServiceName = getDbServiceName(composeParsed);
    return dbServiceName || 'db';
}
function getDbUpstreamPort(composeParsed) {
    const dbServiceName = getDbServiceName(composeParsed);
    if (!dbServiceName)
        return 3306;
    try {
        const services = composeParsed.services;
        const db = services[dbServiceName];
        const ports = db.ports;
        if (ports && ports.length > 0) {
            // Get the internal container port (after the colon)
            const portMapping = ports[0];
            const parts = portMapping.split(':');
            if (parts.length >= 2) {
                return parseInt(parts[parts.length - 1], 10);
            }
        }
        // Try to infer from image
        const image = db.image || '';
        if (image.includes('postgres')) {
            return 5432;
        }
    }
    catch {
        // ignore and return default
    }
    return 3306;
}
function getNetworkName(composePath, composeParsed) {
    // First, check if there's an explicit network configuration
    const networks = composeParsed.networks;
    if (networks && Object.keys(networks).length > 0) {
        const firstNetwork = Object.keys(networks)[0];
        if (firstNetwork !== 'default') {
            return firstNetwork;
        }
    }
    // Otherwise, use Docker Compose's default naming convention
    // The network name is based on the project name (directory name) + _default
    // Resolve to absolute path first to handle relative paths correctly
    const absolutePath = path.resolve(composePath);
    const composeDir = path.basename(path.dirname(absolutePath));
    return `${composeDir}_default`;
}
function extractDbName(composeParsed) {
    const services = composeParsed.services;
    if (!services)
        return null;
    // Find the web/app service (not the DB service)
    const appServiceNames = ['web', 'app', 'api', 'server'];
    let appService = null;
    for (const name of appServiceNames) {
        if (services[name]) {
            appService = services[name];
            break;
        }
    }
    // If no known app service, use the first non-DB service
    if (!appService) {
        const dbServiceName = getDbServiceName(composeParsed);
        for (const [name, service] of Object.entries(services)) {
            if (name !== dbServiceName) {
                appService = service;
                break;
            }
        }
    }
    if (!appService)
        return null;
    const env = normalizeEnv(appService.environment);
    // Try DATABASE_URL first
    const databaseUrl = env.DATABASE_URL;
    if (databaseUrl) {
        const match = databaseUrl.match(/\/([^/?]+)(\?|$)/);
        if (match) {
            return match[1];
        }
    }
    // Try Rails-style DATABASE_NAME
    if (env.DATABASE_NAME) {
        return env.DATABASE_NAME;
    }
    // Try DB_NAME
    if (env.DB_NAME) {
        return env.DB_NAME;
    }
    // Try MYSQL_DATABASE (from DB service env)
    const dbServiceName = getDbServiceName(composeParsed);
    if (dbServiceName && services[dbServiceName]) {
        const dbService = services[dbServiceName];
        const dbEnv = normalizeEnv(dbService.environment);
        if (dbEnv.MYSQL_DATABASE) {
            return dbEnv.MYSQL_DATABASE;
        }
        if (dbEnv.POSTGRES_DB) {
            return dbEnv.POSTGRES_DB;
        }
    }
    return null;
}
async function createDatabase(host, port, dbName) {
    const net = await Promise.resolve().then(() => __importStar(require('net')));
    return new Promise((resolve, reject) => {
        const socket = new net.Socket();
        socket.setTimeout(10000);
        socket.on('connect', () => {
            const createDb = `CREATE DATABASE IF NOT EXISTS \`${dbName}\`;\n`;
            const packet = Buffer.alloc(1 + createDb.length);
            packet[0] = 0x03;
            packet.write(createDb, 1);
            const header = Buffer.alloc(4);
            const len = createDb.length;
            header[0] = len & 0xff;
            header[1] = (len >> 8) & 0xff;
            header[2] = (len >> 16) & 0xff;
            header[3] = 0;
            socket.write(Buffer.concat([header, packet]));
            let responseBuf = Buffer.alloc(0);
            socket.on('data', (data) => {
                responseBuf = Buffer.concat([responseBuf, data]);
                if (responseBuf.length >= 5) {
                    const okPacket = responseBuf[4];
                    if (okPacket === 0x00 || okPacket === 0xff) {
                        socket.destroy();
                        resolve();
                    }
                }
            });
            socket.on('error', (err) => {
                reject(err);
            });
            socket.on('timeout', () => {
                socket.destroy();
                reject(new Error('timeout'));
            });
        });
        socket.on('error', reject);
        socket.connect(port, host);
    });
}
function normalizeEnv(env) {
    if (!env) {
        return {};
    }
    // Handle object format: { KEY: 'value' }
    if (typeof env === 'object' && !Array.isArray(env)) {
        return env;
    }
    // Handle array format: ['KEY=value', 'KEY2=value2']
    if (Array.isArray(env)) {
        const result = {};
        for (const item of env) {
            if (typeof item === 'string') {
                const eqIndex = item.indexOf('=');
                if (eqIndex > 0) {
                    const key = item.substring(0, eqIndex);
                    const value = item.substring(eqIndex + 1);
                    result[key] = value;
                }
            }
        }
        return result;
    }
    return {};
}
function buildOverrideContent(composeParsed, proxyPort) {
    const services = composeParsed.services;
    if (!services || !services.web) {
        throw new Error('Compose file must have a "web" service defined');
    }
    const web = services.web;
    const env = normalizeEnv(web.environment);
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
        if (!env.DB_PORT) {
            overrideEnv.DB_PORT = String(proxyPort);
        }
    }
    if (env.DB_PORT && !overrideEnv.DB_PORT) {
        overrideEnv.DB_PORT = String(proxyPort);
    }
    if (env.DB_NAME) {
        overrideEnv.DB_NAME = env.DB_NAME;
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
function buildOverrideContentForContainer(composeParsed, proxyHost = 'linespec-proxy') {
    const services = composeParsed.services;
    if (!services || !services.web) {
        throw new Error('Compose file must have a "web" service defined');
    }
    const web = services.web;
    const env = normalizeEnv(web.environment);
    // For inter-container communication, use the standard MySQL port 3306
    const internalProxyPort = 3306;
    let databaseUrl = env.DATABASE_URL;
    if (databaseUrl) {
        databaseUrl = databaseUrl.replace(/@[^@/:]+:\d+\//, `@${proxyHost}:${internalProxyPort}/`);
    }
    const overrideEnv = {};
    if (databaseUrl) {
        overrideEnv.DATABASE_URL = databaseUrl;
    }
    // Handle various DB host environment variable patterns
    if (env.DB_HOST) {
        overrideEnv.DB_HOST = proxyHost;
        if (!env.DB_PORT) {
            overrideEnv.DB_PORT = String(internalProxyPort);
        }
    }
    if (env.DATABASE_HOST) {
        overrideEnv.DATABASE_HOST = proxyHost;
        if (!env.DATABASE_PORT) {
            overrideEnv.DATABASE_PORT = String(internalProxyPort);
        }
    }
    if (env.MYSQL_HOST) {
        overrideEnv.MYSQL_HOST = proxyHost;
        if (!env.MYSQL_PORT) {
            overrideEnv.MYSQL_PORT = String(internalProxyPort);
        }
    }
    if (env.POSTGRES_HOST) {
        overrideEnv.POSTGRES_HOST = proxyHost;
        if (!env.POSTGRES_PORT) {
            overrideEnv.POSTGRES_PORT = String(internalProxyPort);
        }
    }
    // Handle various DB port environment variable patterns (only if host wasn't already handled above)
    if (env.DB_PORT && !overrideEnv.DB_PORT) {
        overrideEnv.DB_PORT = String(internalProxyPort);
    }
    if (env.DATABASE_PORT && !overrideEnv.DATABASE_PORT) {
        overrideEnv.DATABASE_PORT = String(internalProxyPort);
    }
    if (env.MYSQL_PORT && !overrideEnv.MYSQL_PORT) {
        overrideEnv.MYSQL_PORT = String(internalProxyPort);
    }
    if (env.POSTGRES_PORT && !overrideEnv.POSTGRES_PORT) {
        overrideEnv.POSTGRES_PORT = String(internalProxyPort);
    }
    // Preserve other DB-related env vars
    if (env.DB_NAME) {
        overrideEnv.DB_NAME = env.DB_NAME;
    }
    if (env.DATABASE_NAME) {
        overrideEnv.DATABASE_NAME = env.DATABASE_NAME;
    }
    if (env.MYSQL_DATABASE) {
        overrideEnv.MYSQL_DATABASE = env.MYSQL_DATABASE;
    }
    if (env.POSTGRES_DB) {
        overrideEnv.POSTGRES_DB = env.POSTGRES_DB;
    }
    if (env.DB_USERNAME) {
        overrideEnv.DB_USERNAME = env.DB_USERNAME;
    }
    if (env.DATABASE_USERNAME) {
        overrideEnv.DATABASE_USERNAME = env.DATABASE_USERNAME;
    }
    if (env.MYSQL_USER) {
        overrideEnv.MYSQL_USER = env.MYSQL_USER;
    }
    if (env.DB_PASSWORD) {
        overrideEnv.DB_PASSWORD = env.DB_PASSWORD;
    }
    if (env.DATABASE_PASSWORD) {
        overrideEnv.DATABASE_PASSWORD = env.DATABASE_PASSWORD;
    }
    if (env.MYSQL_PASSWORD) {
        overrideEnv.MYSQL_PASSWORD = env.MYSQL_PASSWORD;
    }
    if (env.POSTGRES_PASSWORD) {
        overrideEnv.POSTGRES_PASSWORD = env.POSTGRES_PASSWORD;
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
function spawnProcess(cmd, args, quiet = false) {
    return new Promise((resolve, reject) => {
        const proc = (0, child_process_1.spawn)(cmd, args, { stdio: quiet ? 'pipe' : 'inherit' });
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
            await new Promise((r) => setTimeout(r, 200));
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
                        actualBody: body, // Include body for debugging
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
    // Define error file path for Docker proxy mode
    const errorDirPath = useCompose
        ? path.resolve(testSet.dir, '.linespec-errors')
        : '';
    const errorFilePath = useCompose
        ? path.join(errorDirPath, 'verification-errors.json')
        : '';
    let proxyServer = null;
    const dbPort = options.dbPort ?? 3306;
    // Variables for Docker proxy mode (declared here for scope access in test loop)
    let proxyImageName = '';
    let proxyBuildDir = '';
    let proxyContainerName = '';
    let networkName = '';
    let upstreamHost = '';
    let upstreamPort = 3306;
    let internalProxyPort = 3306;
    let controlPort = 3308;
    try {
        const proxyPort = options.proxyPort ?? await findFreePort();
        const controlPort = await findFreePort();
        const mocks = testSet.mocks.map((m) => m.mock);
        if (useCompose) {
            const composeParsed = parseComposeFile(options.composePath);
            const dbUpstreamPort = getDbUpstreamPort(composeParsed);
            process.stdout.write('→ Starting services...\n');
            // Clean up previous database state to ensure fresh start
            try {
                await spawnProcess('docker', [
                    'compose',
                    '-f', options.composePath,
                    'down', '-v'
                ], true);
            }
            catch {
                // Ignore errors - containers might not exist
            }
            await spawnProcess('docker', [
                'compose',
                '-f', options.composePath,
                'up', '-d', 'db'
            ], true);
            const networkName = getNetworkName(options.composePath, composeParsed);
            const upstreamHost = getDbUpstreamHost(composeParsed);
            const upstreamPort = getDbUpstreamPort(composeParsed);
            const maxRetries = 30;
            let dbReady = false;
            // Wait for database to be ready from within the Docker network
            // This ensures the proxy (which also runs in the network) can connect
            for (let i = 0; i < maxRetries; i++) {
                try {
                    await new Promise((resolve, reject) => {
                        const proc = (0, child_process_1.spawn)('docker', [
                            'run', '--rm',
                            '--network', networkName,
                            'alpine:latest',
                            'sh', '-c',
                            `timeout 2 nc -z ${upstreamHost} ${upstreamPort} && echo 'ready' || echo 'notready'`
                        ], { stdio: ['inherit', 'pipe', 'pipe'] });
                        let output = '';
                        proc.stdout?.on('data', (data) => { output += data.toString(); });
                        proc.on('close', (code) => {
                            if (output.includes('ready')) {
                                resolve();
                            }
                            else {
                                reject(new Error('database not ready'));
                            }
                        });
                        proc.on('error', reject);
                    });
                    dbReady = true;
                    break;
                }
                catch {
                    await new Promise((resolve) => setTimeout(resolve, 200));
                }
            }
            if (!dbReady) {
                throw new Error('Database did not become ready within timeout (checked from Docker network)');
            }
            process.stdout.write('✓ Database ready (verified from Docker network)\n');
            proxyImageName = 'linespec-mysql-proxy:latest';
            proxyBuildDir = path.join(__dirname, '..', 'proxy-build');
            const proxyBuildDockerfile = path.join(proxyBuildDir, 'Dockerfile');
            const proxyBuildDockerfileContent = `FROM node:20-alpine

WORKDIR /app

COPY package.json ./
COPY dist ./dist
COPY mocks.yaml ./

RUN npm install --omit=dev

EXPOSE 3306 3308 9092

CMD ["node", "dist/proxy-server.js", "--mocks", "mocks.yaml", "--port", "3306", "--control-port", "3308"]
`;
            if (!fs.existsSync(proxyBuildDir)) {
                fs.mkdirSync(proxyBuildDir, { recursive: true });
            }
            const distDir = path.join(proxyBuildDir, 'dist');
            if (!fs.existsSync(distDir)) {
                fs.mkdirSync(distDir, { recursive: true });
            }
            fs.writeFileSync(proxyBuildDockerfile, proxyBuildDockerfileContent);
            const pkgJsonPath = path.join(__dirname, '..', 'package.json');
            const pkgJson = fs.readFileSync(pkgJsonPath);
            fs.writeFileSync(path.join(proxyBuildDir, 'package.json'), pkgJson);
            try {
                const pkgLockPath = path.join(__dirname, '..', 'package-lock.json');
                const pkgLock = fs.readFileSync(pkgLockPath);
                fs.writeFileSync(path.join(proxyBuildDir, 'package-lock.json'), pkgLock);
            }
            catch {
                // ignore
            }
            const mocksContent = testSet.mocks.map((m) => yaml.dump(m.mock)).join('---\n');
            fs.writeFileSync(path.join(proxyBuildDir, 'mocks.yaml'), mocksContent);
            const srcDistDir = path.join(__dirname, '..', 'dist');
            const distFiles = fs.readdirSync(srcDistDir);
            for (const file of distFiles) {
                const srcPath = path.join(srcDistDir, file);
                const stat = fs.statSync(srcPath);
                if (stat.isFile()) {
                    const content = fs.readFileSync(srcPath);
                    fs.writeFileSync(path.join(distDir, file), content);
                }
            }
            process.stdout.write('→ Building proxy image...\n');
            await spawnProcess('docker', [
                'build', '--no-cache', '-t', proxyImageName, '-f', proxyBuildDockerfile, proxyBuildDir
            ]);
            process.stdout.write('✓ Proxy image built\n');
            // Pre-calculate these values for use in the test loop
            proxyContainerName = 'linespec-proxy';
            // networkName, upstreamHost, upstreamPort already set during DB readiness check
            internalProxyPort = 3306;
            // Ensure error directory exists
            if (!fs.existsSync(errorDirPath)) {
                fs.mkdirSync(errorDirPath, { recursive: true });
            }
            // Clean up any existing proxy
            try {
                await spawnProcess('docker', ['rm', '-f', proxyContainerName], true);
            }
            catch {
                // Ignore errors if container doesn't exist
            }
            // Extract unique hostnames from HTTP mocks for DNS aliases
            const httpMocks = testSet.mocks.filter(m => m.mock.kind === 'Http');
            const hostnames = [...new Set(httpMocks.map(m => {
                    try {
                        const spec = m.mock.spec;
                        const url = new URL(spec.req.url);
                        return url.hostname;
                    }
                    catch {
                        return null;
                    }
                }).filter((h) => h !== null))];
            // Build docker args with network aliases for each HTTP mock hostname
            const dockerArgs = [
                'run', '-d',
                '--name', proxyContainerName,
                '--network', networkName,
            ];
            // Add network aliases for each unique hostname
            for (const hostname of hostnames) {
                dockerArgs.push('--network-alias', hostname);
                process.stdout.write(`→ Adding DNS alias: ${hostname}\n`);
            }
            dockerArgs.push('-p', `${proxyPort}:${internalProxyPort}`, '-p', `${controlPort}:3308`, '-v', `${proxyBuildDir}:/app/mocks:ro`, '-v', `${errorDirPath}:/app/errors`, proxyImageName, 'node', 'dist/proxy-server.js', '--mocks', '/app/mocks/mocks.yaml', '--upstream-host', upstreamHost, '--upstream-port', String(upstreamPort), '--port', String(internalProxyPort), '--control-port', '3308', '--error-file', '/app/errors/verification-errors.json', '--passthrough-file', '/app/errors/passthrough-queries.json', '--query-log-file', '/app/errors/query-log.json');
            await spawnProcess('docker', dockerArgs, true);
            const maxProxyRetries = 15;
            let proxyReady = false;
            for (let i = 0; i < maxProxyRetries; i++) {
                try {
                    const conn = require('net');
                    await new Promise((resolve, reject) => {
                        const socket = new conn.Socket();
                        socket.setTimeout(1000);
                        socket.on('connect', () => {
                            socket.destroy();
                            resolve();
                        });
                        socket.on('timeout', () => {
                            socket.destroy();
                            reject(new Error('timeout'));
                        });
                        socket.on('error', reject);
                        socket.connect(proxyPort, 'localhost');
                    });
                    proxyReady = true;
                    break;
                }
                catch {
                    await new Promise((resolve) => setTimeout(resolve, 100));
                }
            }
            if (!proxyReady) {
                throw new Error('Proxy did not become ready within timeout');
            }
            // Wait for control API to be ready
            let controlReady = false;
            for (let i = 0; i < maxProxyRetries; i++) {
                try {
                    const conn = require('net');
                    await new Promise((resolve, reject) => {
                        const socket = new conn.Socket();
                        socket.setTimeout(1000);
                        socket.on('connect', () => {
                            socket.destroy();
                            resolve();
                        });
                        socket.on('timeout', () => {
                            socket.destroy();
                            reject(new Error('timeout'));
                        });
                        socket.on('error', reject);
                        socket.connect(controlPort, 'localhost');
                    });
                    controlReady = true;
                    break;
                }
                catch {
                    await new Promise((resolve) => setTimeout(resolve, 100));
                }
            }
            if (!controlReady) {
                throw new Error('Proxy control API did not become ready within timeout');
            }
            let networkVerified = false;
            const maxNetworkRetries = 10;
            for (let i = 0; i < maxNetworkRetries; i++) {
                try {
                    await new Promise((resolve, reject) => {
                        const proc = (0, child_process_1.spawn)('docker', [
                            'run', '--rm',
                            '--network', networkName,
                            'alpine:latest',
                            'sh', '-c',
                            `timeout 2 nc -z ${proxyContainerName} ${internalProxyPort} && echo 'connected' || echo 'failed'`
                        ], { stdio: ['inherit', 'pipe', 'pipe'] });
                        let output = '';
                        proc.stdout?.on('data', (data) => { output += data.toString(); });
                        proc.stderr?.on('data', (data) => { });
                        proc.on('close', (code) => {
                            if (output.includes('connected')) {
                                resolve();
                            }
                            else {
                                reject(new Error('connection failed'));
                            }
                        });
                        proc.on('error', reject);
                    });
                    networkVerified = true;
                    break;
                }
                catch {
                    await new Promise((resolve) => setTimeout(resolve, 100));
                }
            }
            if (!networkVerified) {
                throw new Error('Proxy not accessible from Docker network within timeout');
            }
            process.stdout.write(`✓ MySQL proxy ready (${proxyContainerName}:${internalProxyPort})\n`);
            // Removed 3000ms fixed delay - proxy readiness is already confirmed above
            const overrideContent = buildOverrideContentForContainer(composeParsed);
            const absoluteOverridePath = path.resolve(overridePath);
            try {
                // Ensure directory exists
                const overrideDir = path.dirname(absoluteOverridePath);
                if (!fs.existsSync(overrideDir)) {
                    fs.mkdirSync(overrideDir, { recursive: true });
                }
                fs.writeFileSync(absoluteOverridePath, overrideContent);
                // Verify file was written
                if (fs.existsSync(absoluteOverridePath)) {
                    const stats = fs.statSync(absoluteOverridePath);
                    const content = fs.readFileSync(absoluteOverridePath, 'utf-8');
                    console.error(`[runner] Override file written successfully: ${absoluteOverridePath} (${stats.size} bytes)`);
                    console.error(`[runner] Override file content:\n${content}`);
                }
                else {
                    console.error(`[runner] ERROR: Override file was not created at ${absoluteOverridePath}!`);
                }
            }
            catch (err) {
                console.error(`[runner] ERROR writing override file: ${err}`);
                throw err;
            }
            process.stdout.write(`→ Generated override: ${absoluteOverridePath}\n`);
            // Small delay to ensure file is written
            await new Promise(resolve => setTimeout(resolve, 100));
            process.stdout.write(`→ Generated override: ${overridePath}\n`);
            // Small delay to ensure file is written
            await new Promise(resolve => setTimeout(resolve, 100));
            process.stdout.write('→ Starting web service...\n');
            // Check file exists before docker compose
            if (!fs.existsSync(absoluteOverridePath)) {
                console.error(`[runner] CRITICAL: Override file ${absoluteOverridePath} does not exist before docker compose!`);
                throw new Error('Override file missing');
            }
            console.error(`[runner] Override file confirmed existing before docker compose`);
            try {
                // Use spawn without quiet mode to see docker output
                const proc = (0, child_process_1.spawn)('docker', [
                    'compose',
                    '-f', path.resolve(options.composePath),
                    '-f', absoluteOverridePath,
                    'up', '-d', '--no-recreate', '--build', 'web'
                ], { stdio: ['ignore', 'pipe', 'pipe'] });
                let stdout = '';
                let stderr = '';
                proc.stdout.on('data', (data) => { stdout += data.toString(); });
                proc.stderr.on('data', (data) => { stderr += data.toString(); });
                await new Promise((resolve, reject) => {
                    proc.on('close', (code) => {
                        if (code === 0) {
                            resolve(undefined);
                        }
                        else {
                            reject(new Error(`docker compose exited with code ${code}. stdout: ${stdout}, stderr: ${stderr}`));
                        }
                    });
                    proc.on('error', reject);
                });
            }
            catch (err) {
                console.error(`[runner] Docker compose failed: ${err}`);
                throw err;
            }
            // Check file still exists after docker compose
            if (!fs.existsSync(absoluteOverridePath)) {
                console.error(`[runner] WARNING: Override file ${absoluteOverridePath} was deleted during docker compose!`);
            }
        }
        else {
            proxyServer = await (0, mysql_proxy_1.startProxy)(mocks, 'localhost', dbPort, proxyPort);
            process.stdout.write(`✓ MySQL proxy listening on port ${proxyPort}\n`);
        }
        await pollUntilHealthy(options.serviceUrl, 120000);
        process.stdout.write(`✓ Service ready at ${options.serviceUrl}\n`);
        // Initialize Kafka consumer if there are any Kafka mocks
        // We do this after the web service is ready because Kafka is a dependency
        const hasKafkaMocks = testSet.mocks.some(m => m.mock.kind === 'Kafka');
        if (hasKafkaMocks && useCompose) {
            // Wait longer for Kafka to be fully ready (it takes time to start)
            process.stdout.write('→ Waiting for Kafka to be ready...\n');
            await new Promise(resolve => setTimeout(resolve, 8000));
            // Create the topic if it doesn't exist
            try {
                await new Promise((resolve, reject) => {
                    const proc = (0, child_process_1.spawn)('docker', [
                        'run', '--rm',
                        '--network', 'todo-api_default',
                        'confluentinc/cp-kafka:latest',
                        'kafka-topics',
                        '--bootstrap-server', 'kafka:29092',
                        '--create',
                        '--topic', 'todo-events',
                        '--partitions', '1',
                        '--replication-factor', '1',
                        '--if-not-exists'
                    ], { stdio: 'pipe' });
                    proc.on('close', (code) => {
                        if (code === 0 || code === null) {
                            console.error('[runner] Created Kafka topic: todo-events');
                            resolve();
                        }
                        else {
                            console.error(`[runner] Topic creation exited with code ${code}`);
                            resolve(); // Continue anyway, topic might already exist
                        }
                    });
                    proc.on('error', (err) => {
                        console.error(`[runner] Error creating topic: ${err}`);
                        resolve(); // Continue anyway
                    });
                });
            }
            catch (err) {
                console.error(`[runner] Could not create Kafka topic: ${err}`);
            }
            try {
                await startKafkaConsumerDocker(options.composePath);
                process.stdout.write('✓ Kafka consumer initialized\n');
            }
            catch (err) {
                console.error(`[runner] Failed to initialize Kafka consumer: ${err}`);
            }
        }
        let passed = 0;
        let failed = 0;
        const results = [];
        // Track verification errors per test
        const verificationErrors = new Map();
        // Track queries that passed through to real database
        const passthroughQueries = new Map();
        const allPassthroughQueries = [];
        // Track all queries executed (matched or passthrough)
        const testQueries = new Map();
        const allQueries = [];
        // Listen for verification errors from the proxy
        const onVerificationError = (error) => {
            // Store the error for the current test
            const currentTest = testSet.tests[results.length]?.name;
            if (currentTest) {
                verificationErrors.set(currentTest, error);
            }
        };
        mysql_proxy_1.proxyEvents.on('verificationError', onVerificationError);
        // Listen for passthrough queries from the proxy
        const onQueryPassthrough = (data) => {
            const currentTest = testSet.tests[results.length]?.name;
            if (currentTest) {
                const queries = passthroughQueries.get(currentTest) || [];
                queries.push(data.query);
                passthroughQueries.set(currentTest, queries);
            }
            allPassthroughQueries.push(data.query);
        };
        mysql_proxy_1.proxyEvents.on('queryPassthrough', onQueryPassthrough);
        // Listen for all query executions from the proxy
        const onQueryExecuted = (data) => {
            const currentTest = testSet.tests[results.length]?.name;
            if (currentTest) {
                const queries = testQueries.get(currentTest) || [];
                queries.push(data);
                testQueries.set(currentTest, queries);
            }
            allQueries.push(data);
        };
        mysql_proxy_1.proxyEvents.on('queryExecuted', onQueryExecuted);
        for (const { name, ktest } of testSet.tests) {
            // Clear any previous verification error for this test
            verificationErrors.delete(name);
            // Get mocks for this specific test
            const testMocks = testSet.mocksByTest.get(name) || [];
            if (testMocks.length === 0) {
                process.stdout.write(`⚠ Warning: No mocks found for test "${name}"\n`);
            }
            // Optimization 5: Mock Aggregation - only write test-specific mocks file for debugging
            const testMocksYaml = testMocks.map(m => yaml.dump(m.mock)).join('---\n');
            const testMocksPath = path.join(testSet.dir, `mocks-${name}.yaml`);
            fs.writeFileSync(testMocksPath, testMocksYaml);
            if (useCompose) {
                // Optimization 5: Use mock aggregation instead of full reload
                process.stdout.write(`→ ${name}: `);
                // Clear error files via control API
                try {
                    await clearProxyErrorsViaControlApi('localhost', controlPort);
                }
                catch {
                    // Ignore errors - files might not exist
                }
                // Activate mocks for this test via control API (much faster than full reload)
                try {
                    const result = await activateMocksViaControlApi('localhost', controlPort, name);
                    console.error(`[runner] Activated ${result.count} mocks for test "${name}"`);
                }
                catch (err) {
                    throw new Error(`Failed to activate mocks for test "${name}": ${err}`);
                }
            }
            const result = await runHttpTest(ktest, options.serviceUrl, name);
            // Check if there was a verification error for this test
            // First check the event listener (for local proxy mode)
            let verificationError = verificationErrors.get(name);
            // If using Docker proxy, also check the error file
            if (useCompose && !verificationError && fs.existsSync(errorFilePath)) {
                try {
                    const errorContent = fs.readFileSync(errorFilePath, 'utf-8');
                    if (errorContent) {
                        const errorData = JSON.parse(errorContent);
                        if (errorData.error) {
                            verificationError = errorData.error;
                            verificationErrors.set(name, errorData.error);
                            // Clear the file after reading
                            fs.unlinkSync(errorFilePath);
                        }
                    }
                }
                catch {
                    // Ignore file read errors
                }
            }
            // Check HTTP mock usage verification
            let httpMockError;
            if (useCompose) {
                try {
                    const httpMockUsage = await checkHttpMockUsage('localhost', controlPort);
                    if (httpMockUsage.unused.length > 0) {
                        httpMockError = `HTTP Mock(s) not invoked: ${httpMockUsage.unused.join(', ')}`;
                    }
                }
                catch (err) {
                    // If check fails, continue without failing (might be no HTTP mocks)
                    console.error(`[runner] Could not check HTTP mock usage: ${err}`);
                }
            }
            // Check Kafka mock usage verification
            let kafkaMockError;
            if (useCompose) {
                try {
                    // Get expected Kafka events for this test
                    const expectedEvents = [];
                    const testMocks = testSet.mocksByTest.get(name) || [];
                    for (const mock of testMocks) {
                        if (mock.mock.kind === 'Kafka') {
                            const eventSpec = mock.mock.spec;
                            const eventType = eventSpec.message?.event_type;
                            if (eventType) {
                                expectedEvents.push({ eventType, mockName: mock.name });
                            }
                        }
                    }
                    if (expectedEvents.length > 0) {
                        // Check which events were consumed
                        const kafkaResult = await checkKafkaEvents(name, expectedEvents);
                        if (kafkaResult.unused.length > 0) {
                            kafkaMockError = `Kafka Event(s) not produced: ${kafkaResult.unused.join(', ')}`;
                        }
                    }
                }
                catch (err) {
                    // If check fails, continue without failing (might be no Kafka mocks)
                    console.error(`[runner] Could not check Kafka events: ${err}`);
                }
            }
            // Don't reset Kafka messages between tests - we want to track all events
            // resetKafkaMessages();
            // In Docker mode, read passthrough queries from file
            if (useCompose) {
                const passthroughFilePath = path.join(errorDirPath, 'passthrough-queries.json');
                if (fs.existsSync(passthroughFilePath)) {
                    try {
                        const content = fs.readFileSync(passthroughFilePath, 'utf-8');
                        if (content) {
                            const data = JSON.parse(content);
                            if (data.queries && Array.isArray(data.queries)) {
                                for (const query of data.queries) {
                                    allPassthroughQueries.push(query);
                                    const queries = passthroughQueries.get(name) || [];
                                    queries.push(query);
                                    passthroughQueries.set(name, queries);
                                }
                                // Clear the file after reading
                                fs.unlinkSync(passthroughFilePath);
                            }
                        }
                    }
                    catch {
                        // Ignore file read errors
                    }
                }
                // Read query log to see all executed queries
                const queryLogPath = path.join(errorDirPath, 'query-log.json');
                if (fs.existsSync(queryLogPath)) {
                    try {
                        const content = fs.readFileSync(queryLogPath, 'utf-8');
                        if (content) {
                            const data = JSON.parse(content);
                            if (data.queries && Array.isArray(data.queries)) {
                                for (const q of data.queries) {
                                    allQueries.push(q);
                                    const queries = testQueries.get(name) || [];
                                    queries.push(q);
                                    testQueries.set(name, queries);
                                }
                                // Clear the file after reading
                                fs.unlinkSync(queryLogPath);
                            }
                        }
                    }
                    catch {
                        // Ignore file read errors
                    }
                }
            }
            if (verificationError && result.pass) {
                // If verification failed but HTTP test passed, mark as failed
                result.pass = false;
                result.reason = `SQL Verification Failed: ${verificationError}`;
            }
            else if (verificationError && !result.pass) {
                // If both failed, append verification error to reason
                result.reason = `${result.reason}\nSQL Verification Failed: ${verificationError}`;
            }
            // Check HTTP mock verification
            if (httpMockError) {
                if (result.pass) {
                    // If HTTP mocks weren't used but test passed, mark as failed
                    result.pass = false;
                    result.reason = httpMockError;
                }
                else {
                    // If test already failed, append HTTP mock error
                    result.reason = `${result.reason}\n${httpMockError}`;
                }
            }
            // Check Kafka mock verification
            if (kafkaMockError) {
                if (result.pass) {
                    // If Kafka events weren't produced but test passed, mark as failed
                    result.pass = false;
                    result.reason = kafkaMockError;
                }
                else {
                    // If test already failed, append Kafka mock error
                    result.reason = `${result.reason}\n${kafkaMockError}`;
                }
            }
            results.push(result);
            if (result.pass) {
                passed++;
                process.stdout.write(`✓ ${name} PASS\n`);
            }
            else {
                failed++;
                process.stdout.write(`✗ ${name} FAIL\n`);
                // Print detailed failure information
                if (verificationError) {
                    process.stdout.write(`\n  🔒 SQL Verification Error:\n`);
                    const lines = verificationError.split('\n');
                    for (const line of lines) {
                        process.stdout.write(`    ${line}\n`);
                    }
                    process.stdout.write(`\n`);
                }
                // Print HTTP mock verification error
                if (httpMockError) {
                    process.stdout.write(`\n  🔌 HTTP Mock Verification Error:\n`);
                    process.stdout.write(`    ${httpMockError}\n`);
                    process.stdout.write(`\n`);
                }
                // Print Kafka mock verification error
                if (kafkaMockError) {
                    process.stdout.write(`\n  📨 Kafka Event Verification Error:\n`);
                    process.stdout.write(`    ${kafkaMockError}\n`);
                    process.stdout.write(`\n`);
                }
                if (result.expectedStatus !== undefined && result.actualStatus !== undefined) {
                    process.stdout.write(`  Expected status : ${result.expectedStatus}\n`);
                    process.stdout.write(`  Actual status   : ${result.actualStatus}\n`);
                }
                if (result.diff) {
                    process.stdout.write(`  Body diff:\n`);
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
                                process.stdout.write(`    ${line}\n`);
                            }
                        }
                        else {
                            process.stdout.write(`    ${line}\n`);
                        }
                    }
                }
            }
        }
        // Remove the event listeners
        mysql_proxy_1.proxyEvents.off('verificationError', onVerificationError);
        mysql_proxy_1.proxyEvents.off('queryPassthrough', onQueryPassthrough);
        mysql_proxy_1.proxyEvents.off('queryExecuted', onQueryExecuted);
        // Stop Kafka consumer
        stopKafkaConsumer();
        if (options.reportDir) {
            fs.mkdirSync(options.reportDir, { recursive: true });
            const summary = {
                passed,
                failed,
                total: passed + failed,
                passthroughQueries: Array.from(new Set(allPassthroughQueries)),
                tests: results.map((r) => ({
                    name: r.name,
                    pass: r.pass,
                    reason: r.reason,
                    verificationError: verificationErrors.get(r.name),
                    passthroughQueries: passthroughQueries.get(r.name),
                })),
            };
            fs.writeFileSync(path.join(options.reportDir, 'summary.json'), JSON.stringify(summary, null, 2));
            for (const result of results) {
                const safeName = result.name.replace(/[^a-zA-Z0-9_-]/g, '_');
                const reportData = {
                    ...result,
                    verificationError: verificationErrors.get(result.name),
                    passthroughQueries: passthroughQueries.get(result.name),
                };
                fs.writeFileSync(path.join(options.reportDir, `${safeName}.json`), JSON.stringify(reportData, null, 2));
            }
            process.stdout.write(`→ Report written to ${options.reportDir}/\n`);
        }
        // Print diagnostic information if tests failed with passthrough queries
        if (failed > 0 && allPassthroughQueries.length > 0) {
            process.stdout.write(`\n⚠️  DIAGNOSTIC: ${allPassthroughQueries.length} SQL queries passed through to the real database\n`);
            process.stdout.write(`   This indicates the tests may not match the mocks properly.\n`);
            process.stdout.write(`   Check that your SQL queries in the test expectations match what the application actually executes.\n\n`);
            const uniqueQueries = Array.from(new Set(allPassthroughQueries.slice(0, 5)));
            process.stdout.write(`   Sample queries that passed through:\n`);
            for (const query of uniqueQueries) {
                process.stdout.write(`     - ${query.substring(0, 80)}${query.length > 80 ? '...' : ''}\n`);
            }
            if (allPassthroughQueries.length > 5) {
                process.stdout.write(`     ... and ${allPassthroughQueries.length - 5} more\n`);
            }
            process.stdout.write(`\n`);
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
            // Clean up proxy container
            try {
                await spawnProcess('docker', ['rm', '-f', proxyContainerName], true);
            }
            catch {
                // ignore cleanup errors
            }
            try {
                await spawnProcess('docker', [
                    'compose',
                    '-f', options.composePath,
                    'rm', '-fs', 'web'
                ], true);
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
