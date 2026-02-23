#!/usr/bin/env node

import { startProxy } from './mysql-proxy';
import * as fs from 'fs';
import * as path from 'path';
import * as yaml from 'js-yaml';
import type { KMock } from './types';

interface ProxyOptions {
  mocksFile: string;
  upstreamHost: string;
  upstreamPort: number;
  listenPort: number;
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

  const server = await startProxy(docs, options.upstreamHost, options.upstreamPort, options.listenPort);

  console.error(`MySQL proxy listening on port ${options.listenPort} -> ${options.upstreamHost}:${options.upstreamPort}`);
  console.error('Press Ctrl+C to stop');
}

main().catch((err) => {
  console.error('Error:', err);
  process.exit(1);
});
