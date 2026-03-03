#!/usr/bin/env node
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
const mysql_proxy_1 = require("./mysql-proxy");
const fs = __importStar(require("fs"));
const yaml = __importStar(require("js-yaml"));
const http = __importStar(require("http"));
function parseArgs() {
    const args = process.argv.slice(2);
    const options = {
        mocksFile: 'mocks.yaml',
        upstreamHost: 'localhost',
        upstreamPort: 3306,
        listenPort: 3307,
        controlPort: 3308,
    };
    for (let i = 0; i < args.length; i++) {
        if (args[i] === '--mocks' && i + 1 < args.length) {
            options.mocksFile = args[i + 1];
            i++;
        }
        else if (args[i] === '--upstream-host' && i + 1 < args.length) {
            options.upstreamHost = args[i + 1];
            i++;
        }
        else if (args[i] === '--upstream-port' && i + 1 < args.length) {
            options.upstreamPort = parseInt(args[i + 1], 10);
            i++;
        }
        else if (args[i] === '--port' && i + 1 < args.length) {
            options.listenPort = parseInt(args[i + 1], 10);
            i++;
        }
        else if (args[i] === '--control-port' && i + 1 < args.length) {
            options.controlPort = parseInt(args[i + 1], 10);
            i++;
        }
        else if (args[i] === '--error-file' && i + 1 < args.length) {
            options.errorFile = args[i + 1];
            i++;
        }
        else if (args[i] === '--passthrough-file' && i + 1 < args.length) {
            options.passthroughFile = args[i + 1];
            i++;
        }
        else if (args[i] === '--query-log-file' && i + 1 < args.length) {
            options.queryLogFile = args[i + 1];
            i++;
        }
    }
    return options;
}
async function main() {
    const options = parseArgs();
    if (!fs.existsSync(options.mocksFile)) {
        console.error(`Mocks file not found: ${options.mocksFile}`);
        process.exit(1);
    }
    const raw = fs.readFileSync(options.mocksFile, 'utf-8');
    const docs = yaml.loadAll(raw);
    console.error(`Loaded ${docs.length} mock documents from ${options.mocksFile}`);
    // Set up error file if specified
    if (options.errorFile) {
        // Clear any previous errors
        if (fs.existsSync(options.errorFile)) {
            fs.unlinkSync(options.errorFile);
        }
        // Listen for verification errors and write them to file
        mysql_proxy_1.proxyEvents.on('verificationError', (error) => {
            try {
                fs.writeFileSync(options.errorFile, JSON.stringify({ error, timestamp: Date.now() }));
                console.error(`[proxy-server] Verification error written to ${options.errorFile}`);
            }
            catch (err) {
                console.error(`[proxy-server] Failed to write error file: ${err}`);
            }
        });
    }
    // Set up passthrough tracking if specified
    if (options.passthroughFile) {
        // Clear any previous passthrough data
        if (fs.existsSync(options.passthroughFile)) {
            fs.unlinkSync(options.passthroughFile);
        }
        // Listen for passthrough queries and append them to file
        mysql_proxy_1.proxyEvents.on('queryPassthrough', (data) => {
            try {
                let existing = { queries: [] };
                if (fs.existsSync(options.passthroughFile)) {
                    try {
                        existing = JSON.parse(fs.readFileSync(options.passthroughFile, 'utf-8'));
                    }
                    catch {
                        // If file is corrupted, start fresh
                    }
                }
                existing.queries.push(data.query);
                fs.writeFileSync(options.passthroughFile, JSON.stringify(existing));
                console.error(`[proxy-server] Query passed through to database: ${data.query.substring(0, 80)}...`);
            }
            catch (err) {
                console.error(`[proxy-server] Failed to write passthrough file: ${err}`);
            }
        });
    }
    // Set up query logging if specified
    if (options.queryLogFile) {
        // Clear any previous query log
        if (fs.existsSync(options.queryLogFile)) {
            fs.unlinkSync(options.queryLogFile);
        }
        // Listen for all queries and log them
        mysql_proxy_1.proxyEvents.on('queryExecuted', (data) => {
            try {
                let existing = { queries: [] };
                if (fs.existsSync(options.queryLogFile)) {
                    try {
                        existing = JSON.parse(fs.readFileSync(options.queryLogFile, 'utf-8'));
                    }
                    catch {
                        // If file is corrupted, start fresh
                    }
                }
                existing.queries.push(data);
                fs.writeFileSync(options.queryLogFile, JSON.stringify(existing, null, 2));
            }
            catch (err) {
                console.error(`[proxy-server] Failed to write query log: ${err}`);
            }
        });
    }
    // Start the MySQL proxy
    const proxyServer = await (0, mysql_proxy_1.startProxy)(docs, options.upstreamHost, options.upstreamPort, options.listenPort);
    // Start HTTP mock server to intercept calls to user-service.local
    const httpMocks = docs.filter((m) => m.kind === 'Http');
    // Track which HTTP mocks are invoked for verification
    const httpMockUsage = new Map();
    // Track current test name to filter HTTP mocks
    let currentHttpTestName = null;
    if (httpMocks.length > 0) {
        console.error(`[proxy-server] Starting HTTP mock server with ${httpMocks.length} HTTP mocks`);
        // Initialize usage tracking
        httpMocks.forEach(m => httpMockUsage.set(m.name, false));
        const httpServer = http.createServer((req, res) => {
            // Enable CORS
            res.setHeader('Access-Control-Allow-Origin', '*');
            res.setHeader('Access-Control-Allow-Methods', 'GET, POST, PUT, DELETE, OPTIONS');
            res.setHeader('Access-Control-Allow-Headers', 'Content-Type, Authorization');
            if (req.method === 'OPTIONS') {
                res.writeHead(200);
                res.end();
                return;
            }
            let body = '';
            req.on('data', chunk => { body += chunk; });
            req.on('end', () => {
                // Find matching HTTP mock - filter by current test name
                const mock = httpMocks.find((m) => {
                    // Only consider mocks for the current test
                    if (currentHttpTestName && !m.name.startsWith(`${currentHttpTestName}-mock-`)) {
                        return false;
                    }
                    const spec = m.spec;
                    const mockMethod = spec.req?.method?.toUpperCase();
                    const mockUrl = spec.req?.url;
                    const requestMethod = req.method?.toUpperCase();
                    const requestUrl = `http://${req.headers.host}${req.url}`;
                    console.error(`[proxy-server] HTTP Mock check: ${requestMethod} ${requestUrl} vs ${mockMethod} ${mockUrl}`);
                    return mockMethod === requestMethod && mockUrl === requestUrl;
                });
                if (mock) {
                    const spec = mock.spec;
                    console.error(`[proxy-server] HTTP Mock matched: ${mock.name}`);
                    // Check if this is a negative mock - if so, fail immediately
                    if (spec.metadata?.negative) {
                        const errorMsg = `NEGATIVE ASSERTION VIOLATED: HTTP call was made but should NOT have been.\nMethod: ${req.method}\nURL: http://${req.headers.host}${req.url}`;
                        console.error(`[proxy-server] ${errorMsg}`);
                        res.writeHead(500);
                        res.end(JSON.stringify({ error: errorMsg }));
                        return;
                    }
                    // Mark this mock as used
                    httpMockUsage.set(mock.name, true);
                    // Set response headers
                    if (spec.resp?.header) {
                        for (const [key, value] of Object.entries(spec.resp.header)) {
                            res.setHeader(key, value);
                        }
                    }
                    res.writeHead(spec.resp?.status_code || 200);
                    res.end(spec.resp?.body || '{}');
                }
                else {
                    console.error(`[proxy-server] No HTTP mock found for: ${req.method} http://${req.headers.host}${req.url} (current test: ${currentHttpTestName})`);
                    res.writeHead(404);
                    res.end(JSON.stringify({ error: 'No mock found' }));
                }
            });
        });
        await new Promise((resolve, reject) => {
            httpServer.listen(80, '0.0.0.0', () => {
                console.error('[proxy-server] HTTP mock server listening on port 80');
                resolve();
            });
            httpServer.on('error', reject);
        });
    }
    // Start the HTTP control server for hot mock reloading
    let controlServer = null;
    if (options.controlPort) {
        controlServer = http.createServer((req, res) => {
            // Enable CORS
            res.setHeader('Access-Control-Allow-Origin', '*');
            res.setHeader('Access-Control-Allow-Methods', 'POST, OPTIONS');
            res.setHeader('Access-Control-Allow-Headers', 'Content-Type');
            res.setHeader('Connection', 'keep-alive');
            if (req.method === 'OPTIONS') {
                res.writeHead(200);
                res.end();
                return;
            }
            if (req.method === 'POST' && req.url === '/reload') {
                let body = '';
                req.on('data', chunk => {
                    body += chunk;
                });
                req.on('end', () => {
                    try {
                        const mocks = yaml.loadAll(body);
                        (0, mysql_proxy_1.reloadMocks)(mocks);
                        console.error(`[proxy-server] Hot reloaded ${mocks.length} mocks`);
                        res.writeHead(200, { 'Content-Type': 'application/json' });
                        res.end(JSON.stringify({ success: true, count: mocks.length }));
                    }
                    catch (err) {
                        console.error(`[proxy-server] Failed to reload mocks: ${err}`);
                        res.writeHead(400, { 'Content-Type': 'application/json' });
                        res.end(JSON.stringify({ success: false, error: String(err) }));
                    }
                });
                req.on('error', (err) => {
                    console.error(`[proxy-server] Request error on /reload: ${err}`);
                    res.writeHead(500, { 'Content-Type': 'application/json' });
                    res.end(JSON.stringify({ success: false, error: String(err) }));
                });
            }
            else if (req.method === 'POST' && req.url === '/activate') {
                // Optimization 5: Mock Aggregation - activate mocks for specific test
                let body = '';
                req.on('data', chunk => { body += chunk; });
                req.on('end', () => {
                    try {
                        const data = JSON.parse(body);
                        const testName = data.testName;
                        if (!testName) {
                            res.writeHead(400, { 'Content-Type': 'application/json' });
                            res.end(JSON.stringify({ success: false, error: 'Missing testName' }));
                            return;
                        }
                        const count = (0, mysql_proxy_1.activateMocksForTest)(testName);
                        // Track current test name for HTTP mock filtering
                        currentHttpTestName = testName;
                        // Reset HTTP mock usage tracking for new test
                        httpMockUsage.forEach((_, key) => httpMockUsage.set(key, false));
                        console.error(`[proxy-server] Activated mocks for test "${testName}": ${count} mocks`);
                        res.writeHead(200, { 'Content-Type': 'application/json' });
                        res.end(JSON.stringify({ success: true, testName, count }));
                    }
                    catch (err) {
                        console.error(`[proxy-server] Failed to activate mocks: ${err}`);
                        res.writeHead(400, { 'Content-Type': 'application/json' });
                        res.end(JSON.stringify({ success: false, error: String(err) }));
                    }
                });
                req.on('error', (err) => {
                    console.error(`[proxy-server] Request error on /activate: ${err}`);
                    res.writeHead(500, { 'Content-Type': 'application/json' });
                    res.end(JSON.stringify({ success: false, error: String(err) }));
                });
            }
            else if (req.method === 'GET' && req.url === '/check-http-mocks') {
                // Check which HTTP mocks were invoked - filter by current test name
                const unusedMocks = [];
                httpMockUsage.forEach((used, name) => {
                    // Only check mocks that belong to the current test
                    if (currentHttpTestName && name.startsWith(`${currentHttpTestName}-mock-`)) {
                        if (!used)
                            unusedMocks.push(name);
                    }
                });
                res.writeHead(200, { 'Content-Type': 'application/json' });
                res.end(JSON.stringify({
                    success: true,
                    total: httpMockUsage.size,
                    used: httpMockUsage.size - unusedMocks.length,
                    unused: unusedMocks
                }));
            }
            else if (req.method === 'POST' && req.url === '/clear-errors') {
                // Clear error and passthrough files
                try {
                    if (options.errorFile && fs.existsSync(options.errorFile)) {
                        fs.unlinkSync(options.errorFile);
                    }
                    if (options.passthroughFile && fs.existsSync(options.passthroughFile)) {
                        fs.unlinkSync(options.passthroughFile);
                    }
                    if (options.queryLogFile && fs.existsSync(options.queryLogFile)) {
                        fs.unlinkSync(options.queryLogFile);
                    }
                    res.writeHead(200, { 'Content-Type': 'application/json' });
                    res.end(JSON.stringify({ success: true }));
                }
                catch (err) {
                    console.error(`[proxy-server] Failed to clear errors: ${err}`);
                    res.writeHead(500, { 'Content-Type': 'application/json' });
                    res.end(JSON.stringify({ success: false, error: String(err) }));
                }
            }
            else {
                res.writeHead(404);
                res.end('Not found');
            }
        });
        await new Promise((resolve, reject) => {
            controlServer.listen(options.controlPort, '0.0.0.0', () => {
                console.error(`Control API listening on port ${options.controlPort}`);
                resolve();
            });
            controlServer.on('error', reject);
            controlServer.on('clientError', (err, socket) => {
                console.error(`[proxy-server] Control server client error: ${err}`);
                socket.destroy();
            });
        });
    }
    console.error(`MySQL proxy listening on port ${options.listenPort} -> ${options.upstreamHost}:${options.upstreamPort}`);
    console.error('Press Ctrl+C to stop');
    // Graceful shutdown
    process.on('SIGINT', () => {
        console.error('\nShutting down...');
        proxyServer.close(() => {
            if (controlServer) {
                controlServer.close(() => {
                    process.exit(0);
                });
            }
            else {
                process.exit(0);
            }
        });
    });
}
main().catch((err) => {
    console.error('Error:', err);
    process.exit(1);
});
