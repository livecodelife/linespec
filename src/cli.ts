#!/usr/bin/env node

import { Command } from 'commander';
import { tokenize } from './lexer';
import { parse, LineSpecError } from './parser';
import { validate } from './validator';
import { compile } from './compiler';
import { loadTestSet } from './test-loader';
import { runTests } from './runner';
import * as fs from 'fs';
import * as path from 'path';

const program = new Command();
program
  .name('linespec')
  .description('LineSpec DSL compiler for Keploy KTests and KMocks')
  .version('0.1.0');

program
  .command('compile <file>')
  .description('Compile a .linespec file into KTest and KMock YAML')
  .option('-o, --out <dir>', 'Output directory', 'out')
  .action((file: string, options: { out: string }) => {
    let source: string;
    try {
      source = fs.readFileSync(file, 'utf-8');
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code === 'ENOENT') {
        console.error(`Error: File not found: ${file}`);
        process.exit(1);
      }
      throw err;
    }

    const baseDir = path.dirname(path.resolve(file));

    try {
      const tokens = tokenize(source);
      const spec = parse(tokens, file);
      validate(spec, baseDir);
      compile(spec, { outDir: options.out, baseDir });

      console.log(`✓ Compiled ${spec.name} → ${options.out}/tests/${spec.name}.yaml`);

      const mocksPath = path.join(options.out, 'mocks.yaml');
      if (fs.existsSync(mocksPath)) {
        console.log(`✓ Mocks → ${options.out}/mocks.yaml`);
      }
    } catch (err) {
      if (err instanceof LineSpecError) {
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
  .action((dir: string | undefined, options: { compose?: string; serviceUrl: string; dbPort: string; report: string }) => {
    const testDir = dir || 'keploy-examples/test-set-0';
    const reportDir = path.resolve(testDir, options.report);

    try {
      const testSet = loadTestSet(testDir);
      console.log(`✓ Loaded ${testSet.tests.length} tests and ${testSet.mocks.length} mocks from ${testDir}`);

      runTests(testSet, { 
        composePath: options.compose, 
        serviceUrl: options.serviceUrl,
        dbPort: parseInt(options.dbPort, 10),
        reportDir,
      })
        .catch((err) => {
          console.error(`Error: ${err.message}`);
          process.exit(1);
        });
    } catch (err) {
      if (err instanceof Error) {
        console.error(`Error: ${err.message}`);
        process.exit(1);
      }
      throw err;
    }
  });

program.parse(process.argv);
