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
function parseArgs() {
    const args = process.argv.slice(2);
    const options = {
        mocksFile: 'mocks.yaml',
        upstreamHost: 'localhost',
        upstreamPort: 3306,
        listenPort: 3307,
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
        else if (args[i] === '--error-file' && i + 1 < args.length) {
            options.errorFile = args[i + 1];
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
    const server = await (0, mysql_proxy_1.startProxy)(docs, options.upstreamHost, options.upstreamPort, options.listenPort);
    console.error(`MySQL proxy listening on port ${options.listenPort} -> ${options.upstreamHost}:${options.upstreamPort}`);
    console.error('Press Ctrl+C to stop');
}
main().catch((err) => {
    console.error('Error:', err);
    process.exit(1);
});
