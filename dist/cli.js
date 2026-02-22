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
const commander_1 = require("commander");
const lexer_1 = require("./lexer");
const parser_1 = require("./parser");
const validator_1 = require("./validator");
const compiler_1 = require("./compiler");
const test_loader_1 = require("./test-loader");
const runner_1 = require("./runner");
const fs = __importStar(require("fs"));
const path = __importStar(require("path"));
const program = new commander_1.Command();
program
    .name('linespec')
    .description('LineSpec DSL compiler for Keploy KTests and KMocks')
    .version('0.1.0');
program
    .command('compile <file>')
    .description('Compile a .linespec file into KTest and KMock YAML')
    .option('-o, --out <dir>', 'Output directory', 'out')
    .action((file, options) => {
    let source;
    try {
        source = fs.readFileSync(file, 'utf-8');
    }
    catch (err) {
        if (err.code === 'ENOENT') {
            console.error(`Error: File not found: ${file}`);
            process.exit(1);
        }
        throw err;
    }
    const baseDir = path.dirname(path.resolve(file));
    try {
        const tokens = (0, lexer_1.tokenize)(source);
        const spec = (0, parser_1.parse)(tokens, file);
        (0, validator_1.validate)(spec, baseDir);
        (0, compiler_1.compile)(spec, { outDir: options.out, baseDir });
        console.log(`✓ Compiled ${spec.name} → ${options.out}/tests/${spec.name}.yaml`);
        const mocksPath = path.join(options.out, 'mocks.yaml');
        if (fs.existsSync(mocksPath)) {
            console.log(`✓ Mocks → ${options.out}/mocks.yaml`);
        }
    }
    catch (err) {
        if (err instanceof parser_1.LineSpecError) {
            const lineInfo = err.line ? `:${err.line}` : '';
            console.error(`Error${lineInfo}: ${err.message}`);
            process.exit(1);
        }
        if (err instanceof Error) {
            console.error(`Error: ${err.message}`);
            process.exit(1);
        }
        throw err;
    }
});
program
    .command('test [dir]')
    .description('Run compiled KTests against a proxied service')
    .option('--compose <file>', 'Path to docker-compose file (optional, for manual service management)')
    .option('--service-url <url>', 'Base URL of the service to test', 'http://localhost:3000')
    .option('--db-port <port>', 'MySQL port to proxy (default: 3306)', '3306')
    .action((dir, options) => {
    const testDir = dir || 'keploy-examples/test-set-0';
    try {
        const testSet = (0, test_loader_1.loadTestSet)(testDir);
        console.log(`✓ Loaded ${testSet.tests.length} tests and ${testSet.mocks.length} mocks from ${testDir}`);
        (0, runner_1.runTests)(testSet, {
            composePath: options.compose,
            serviceUrl: options.serviceUrl,
            dbPort: parseInt(options.dbPort, 10),
        })
            .catch((err) => {
            console.error(`Error: ${err.message}`);
            process.exit(1);
        });
    }
    catch (err) {
        if (err instanceof Error) {
            console.error(`Error: ${err.message}`);
            process.exit(1);
        }
        throw err;
    }
});
program.parse(process.argv);
