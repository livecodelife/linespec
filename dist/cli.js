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
function collectLinespecFiles(dir) {
    const results = [];
    const entries = fs.readdirSync(dir, { withFileTypes: true });
    for (const entry of entries) {
        const fullPath = path.join(dir, entry.name);
        if (entry.isDirectory()) {
            results.push(...collectLinespecFiles(fullPath));
        }
        else if (entry.isFile() && entry.name.endsWith('.linespec')) {
            results.push(fullPath);
        }
    }
    return results;
}
program
    .command('compile <file>')
    .description('Compile a .linespec file into KTest and KMock YAML')
    .option('-o, --out <dir>', 'Output directory', 'out')
    .action((file, options) => {
    let stats;
    try {
        stats = fs.statSync(file);
    }
    catch (err) {
        if (err.code === 'ENOENT') {
            console.error(`Error: File not found: ${file}`);
            process.exit(1);
        }
        throw err;
    }
    if (stats.isDirectory()) {
        const files = collectLinespecFiles(file);
        if (files.length === 0) {
            console.log(`No .linespec files found in ${file}`);
            return;
        }
        for (const filePath of files) {
            let source;
            try {
                source = fs.readFileSync(filePath, 'utf-8');
            }
            catch (err) {
                if (err.code === 'ENOENT') {
                    console.error(`Error: File not found: ${filePath}`);
                    process.exit(1);
                }
                throw err;
            }
            const baseDir = path.dirname(path.resolve(filePath));
            try {
                const tokens = (0, lexer_1.tokenize)(source);
                const spec = (0, parser_1.parse)(tokens, filePath);
                (0, validator_1.validate)(spec, baseDir);
                (0, compiler_1.compile)(spec, { outDir: options.out, baseDir });
                console.log(`✓ Compiled ${spec.name} → ${options.out}/tests/${spec.name}.yaml`);
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
        }
        const mocksPath = path.join(options.out, 'mocks.yaml');
        if (fs.existsSync(mocksPath)) {
            console.log(`✓ Mocks → ${options.out}/mocks.yaml`);
        }
        return;
    }
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
    .option('--report <dir>', 'Report output directory (relative to test-set dir)', 'linespec-report')
    .action((dir, options) => {
    const testDir = dir || 'keploy-examples/test-set-0';
    const reportDir = path.resolve(testDir, options.report);
    try {
        const testSet = (0, test_loader_1.loadTestSet)(testDir);
        console.log(`✓ Loaded ${testSet.tests.length} tests and ${testSet.mocks.length} mocks from ${testDir}`);
        (0, runner_1.runTests)(testSet, {
            composePath: options.compose,
            serviceUrl: options.serviceUrl,
            dbPort: parseInt(options.dbPort, 10),
            reportDir,
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
program
    .command('docs')
    .description('Show documentation paths and AI agent guidelines')
    .option('-l, --linespec', 'Open LineSpec language reference')
    .option('-a, --agents', 'Open AI agent guidelines')
    .option('-r, --readme', 'Open README')
    .action((options) => {
    const pkgDir = path.dirname(require.resolve('../package.json'));
    const docsDir = path.join(pkgDir, 'docs');
    if (options.linespec) {
        const linespecPath = path.join(docsDir, 'LINESPEC.md');
        if (fs.existsSync(linespecPath)) {
            console.log(linespecPath);
        }
        else {
            console.error('LINESPEC.md not found in package');
            process.exit(1);
        }
    }
    else if (options.agents) {
        const agentsPath = path.join(docsDir, 'AGENTS.md');
        if (fs.existsSync(agentsPath)) {
            console.log(agentsPath);
        }
        else {
            console.error('AGENTS.md not found in package');
            process.exit(1);
        }
    }
    else if (options.readme) {
        const readmePath = path.join(docsDir, 'README.md');
        if (fs.existsSync(readmePath)) {
            console.log(readmePath);
        }
        else {
            console.error('README.md not found in package');
            process.exit(1);
        }
    }
    else {
        console.log('Documentation files location:');
        console.log(`  ${docsDir}`);
        console.log('');
        console.log('Available documentation:');
        console.log('  LINESPEC.md   - Complete language reference');
        console.log('  AGENTS.md     - Guidelines for AI agents');
        console.log('  README.md     - General usage guide');
        console.log('');
        console.log('Usage:');
        console.log('  linespec docs --linespec   # Print path to LINESPEC.md');
        console.log('  linespec docs --agents     # Print path to AGENTS.md');
        console.log('  linespec docs --readme     # Print path to README.md');
        console.log('');
        console.log('AI Agent Note:');
        console.log('  When helping users write LineSpec files, read AGENTS.md first');
        console.log('  for project context, then refer to LINESPEC.md for syntax.');
    }
});
program.parse(process.argv);
