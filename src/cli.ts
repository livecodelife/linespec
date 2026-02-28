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

function collectLinespecFiles(dir: string): string[] {
  const results: string[] = [];
  const entries = fs.readdirSync(dir, { withFileTypes: true });
  for (const entry of entries) {
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      results.push(...collectLinespecFiles(fullPath));
    } else if (entry.isFile() && entry.name.endsWith('.linespec')) {
      results.push(fullPath);
    }
  }
  return results;
}

program
  .command('compile <file>')
  .description('Compile a .linespec file into KTest and KMock YAML')
  .option('-o, --out <dir>', 'Output directory', 'out')
  .action((file: string, options: { out: string }) => {
    let stats: fs.Stats;
    try {
      stats = fs.statSync(file);
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code === 'ENOENT') {
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
        let source: string;
        try {
          source = fs.readFileSync(filePath, 'utf-8');
        } catch (err) {
          if ((err as NodeJS.ErrnoException).code === 'ENOENT') {
            console.error(`Error: File not found: ${filePath}`);
            process.exit(1);
          }
          throw err;
        }

        const baseDir = path.dirname(path.resolve(filePath));

        try {
          const tokens = tokenize(source);
          const spec = parse(tokens, filePath);
          validate(spec, baseDir);
          compile(spec, { outDir: options.out, baseDir });

          console.log(`✓ Compiled ${spec.name} → ${options.out}/tests/${spec.name}.yaml`);
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
      }

      const mocksPath = path.join(options.out, 'mocks.yaml');
      if (fs.existsSync(mocksPath)) {
        console.log(`✓ Mocks → ${options.out}/mocks.yaml`);
      }
      return;
    }

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
  .option('--proxy-port <port>', 'Proxy listen port (default: random free port)', '')
  .option('--report <dir>', 'Report output directory (relative to test-set dir)', 'linespec-report')
  .action((dir: string | undefined, options: { compose?: string; serviceUrl: string; dbPort: string; proxyPort: string; report: string }) => {
    const testDir = dir || 'keploy-examples/test-set-0';
    const reportDir = path.resolve(testDir, options.report);

    try {
      const testSet = loadTestSet(testDir);
      console.log(`✓ Loaded ${testSet.tests.length} tests and ${testSet.mocks.length} mocks from ${testDir}`);

      runTests(testSet, { 
        composePath: options.compose, 
        serviceUrl: options.serviceUrl,
        dbPort: parseInt(options.dbPort, 10),
        proxyPort: options.proxyPort ? parseInt(options.proxyPort, 10) : undefined,
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

program
  .command('docs')
  .description('Show documentation paths and AI agent guidelines')
  .option('-l, --linespec', 'Open LineSpec language reference')
  .option('-a, --agents', 'Open AI agent guidelines')
  .option('-r, --readme', 'Open README')
  .action((options: { linespec?: boolean; agents?: boolean; readme?: boolean }) => {
    const pkgDir = path.dirname(require.resolve('../package.json'));
    const docsDir = path.join(pkgDir, 'docs');
    
    if (options.linespec) {
      const linespecPath = path.join(docsDir, 'LINESPEC.md');
      if (fs.existsSync(linespecPath)) {
        console.log(linespecPath);
      } else {
        console.error('LINESPEC.md not found in package');
        process.exit(1);
      }
    } else if (options.agents) {
      const agentsPath = path.join(docsDir, 'AGENTS.md');
      if (fs.existsSync(agentsPath)) {
        console.log(agentsPath);
      } else {
        console.error('AGENTS.md not found in package');
        process.exit(1);
      }
    } else if (options.readme) {
      const readmePath = path.join(docsDir, 'README.md');
      if (fs.existsSync(readmePath)) {
        console.log(readmePath);
      } else {
        console.error('README.md not found in package');
        process.exit(1);
      }
    } else {
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
