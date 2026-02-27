#!/usr/bin/env node

import { startProxy, proxyEvents } from './mysql-proxy';
import * as fs from 'fs';
import * as path from 'path';
import * as yaml from 'js-yaml';
import type { KMock } from './types';

interface ProxyOptions {
  mocksFile: string;
  upstreamHost: string;
  upstreamPort: number;
  listenPort: number;
  errorFile?: string;
  passthroughFile?: string;
  queryLogFile?: string;
}

function parseArgs(): ProxyOptions {
  const args = process.argv.slice(2);
  const options: ProxyOptions = {
    mocksFile: 'mocks.yaml',
    upstreamHost: 'localhost',
    upstreamPort: 3306,
    listenPort: 3307,
  };

  for (let i = 0; i < args.length; i++) {
    if (args[i] === '--mocks' && i + 1 < args.length) {
      options.mocksFile = args[i + 1];
      i++;
    } else if (args[i] === '--upstream-host' && i + 1 < args.length) {
      options.upstreamHost = args[i + 1];
      i++;
    } else if (args[i] === '--upstream-port' && i + 1 < args.length) {
      options.upstreamPort = parseInt(args[i + 1], 10);
      i++;
    } else if (args[i] === '--port' && i + 1 < args.length) {
      options.listenPort = parseInt(args[i + 1], 10);
      i++;
    } else if (args[i] === '--error-file' && i + 1 < args.length) {
      options.errorFile = args[i + 1];
      i++;
    } else if (args[i] === '--passthrough-file' && i + 1 < args.length) {
      options.passthroughFile = args[i + 1];
      i++;
    } else if (args[i] === '--query-log-file' && i + 1 < args.length) {
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
  const docs = yaml.loadAll(raw) as KMock[];

  console.error(`Loaded ${docs.length} mock documents from ${options.mocksFile}`);

  // Set up error file if specified
  if (options.errorFile) {
    // Clear any previous errors
    if (fs.existsSync(options.errorFile)) {
      fs.unlinkSync(options.errorFile);
    }
    
    // Listen for verification errors and write them to file
    proxyEvents.on('verificationError', (error: string) => {
      try {
        fs.writeFileSync(options.errorFile!, JSON.stringify({ error, timestamp: Date.now() }));
        console.error(`[proxy-server] Verification error written to ${options.errorFile}`);
      } catch (err) {
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
    proxyEvents.on('queryPassthrough', (data: { query: string; timestamp: number }) => {
      try {
        let existing: { queries: string[] } = { queries: [] };
        if (fs.existsSync(options.passthroughFile!)) {
          try {
            existing = JSON.parse(fs.readFileSync(options.passthroughFile!, 'utf-8'));
          } catch {
            // If file is corrupted, start fresh
          }
        }
        existing.queries.push(data.query);
        fs.writeFileSync(options.passthroughFile!, JSON.stringify(existing));
        console.error(`[proxy-server] Query passed through to database: ${data.query.substring(0, 80)}...`);
      } catch (err) {
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
    proxyEvents.on('queryExecuted', (data: { query: string; matched: boolean; timestamp: number }) => {
      try {
        let existing: { queries: Array<{ query: string; matched: boolean; timestamp: number }> } = { queries: [] };
        if (fs.existsSync(options.queryLogFile!)) {
          try {
            existing = JSON.parse(fs.readFileSync(options.queryLogFile!, 'utf-8'));
          } catch {
            // If file is corrupted, start fresh
          }
        }
        existing.queries.push(data);
        fs.writeFileSync(options.queryLogFile!, JSON.stringify(existing, null, 2));
      } catch (err) {
        console.error(`[proxy-server] Failed to write query log: ${err}`);
      }
    });
  }

  const server = await startProxy(docs, options.upstreamHost, options.upstreamPort, options.listenPort);

  console.error(`MySQL proxy listening on port ${options.listenPort} -> ${options.upstreamHost}:${options.upstreamPort}`);
  console.error('Press Ctrl+C to stop');
}

main().catch((err) => {
  console.error('Error:', err);
  process.exit(1);
});
