import * as net from 'net';
import * as fs from 'fs';
import * as path from 'path';
import * as yaml from 'js-yaml';
import { spawn } from 'child_process';
import http from 'http';
import https from 'https';
import { startProxy } from './mysql-proxy';
import type { KMock, KTest, LoadedTestSet, TestResult } from './types';

export interface RunnerOptions {
  composePath?: string;
  serviceUrl: string;
  dbPort?: number;
  reportDir?: string;
}

async function findFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.listen(0, () => {
      const address = server.address() as net.AddressInfo;
      const port = address.port;
      server.close(() => resolve(port));
    });
    server.on('error', reject);
  });
}

function parseComposeFile(composePath: string): Record<string, unknown> {
  if (!fs.existsSync(composePath)) {
    throw new Error(`Compose file not found: ${composePath}`);
  }
  const content = fs.readFileSync(composePath, 'utf8');
  return yaml.load(content) as Record<string, unknown>;
}

function getDbUpstreamPort(composeParsed: Record<string, unknown>): number {
  try {
    const services = composeParsed.services as Record<string, unknown>;
    const db = services.db as Record<string, unknown>;
    const ports = db.ports as string[];
    if (ports && ports.length > 0) {
      const portMapping = ports[0];
      const parts = portMapping.split(':');
      return parseInt(parts[0], 10);
    }
  } catch {
    // ignore and return default
  }
  return 3306;
}

function extractDbName(composeParsed: Record<string, unknown>): string | null {
  try {
    const services = composeParsed.services as Record<string, unknown>;
    const web = services.web as Record<string, unknown>;
    const env = (web.environment as Record<string, string>) ?? {};
    const databaseUrl = env.DATABASE_URL;
    if (databaseUrl) {
      const match = databaseUrl.match(/\/([^/?]+)(\?|$)/);
      if (match) {
        return match[1];
      }
    }
  } catch {
    // ignore
  }
  return null;
}

async function createDatabase(host: string, port: number, dbName: string): Promise<void> {
  const net = await import('net');
  
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

function buildOverrideContent(composeParsed: Record<string, unknown>, proxyPort: number): string {
  const services = composeParsed.services as Record<string, unknown>;
  if (!services || !services.web) {
    throw new Error('Compose file must have a "web" service defined');
  }
  const web = services.web as Record<string, unknown>;
  const env = (web.environment as Record<string, string>) ?? {};
  
  let databaseUrl = env.DATABASE_URL;
  if (databaseUrl) {
    databaseUrl = databaseUrl.replace(/@[^@/:]+:\d+\//, `@host.docker.internal:${proxyPort}/`);
  }

  const overrideEnv: Record<string, string> = {};

  if (databaseUrl) {
    overrideEnv.DATABASE_URL = databaseUrl;
  }

  if (env.DB_HOST) {
    overrideEnv.DB_HOST = 'host.docker.internal';
  }

  if (env.DB_PORT) {
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

function buildOverrideContentForContainer(composeParsed: Record<string, unknown>, proxyPort: number): string {
  const services = composeParsed.services as Record<string, unknown>;
  if (!services || !services.web) {
    throw new Error('Compose file must have a "web" service defined');
  }
  const web = services.web as Record<string, unknown>;
  const env = (web.environment as Record<string, string>) ?? {};
  
  let databaseUrl = env.DATABASE_URL;
  if (databaseUrl) {
    databaseUrl = databaseUrl.replace(/@[^@/:]+:\d+\//, `@linespec-proxy:3307/`);
  }

  const overrideEnv: Record<string, string> = {};

  if (databaseUrl) {
    overrideEnv.DATABASE_URL = databaseUrl;
  }

  if (env.DB_HOST) {
    overrideEnv.DB_HOST = 'linespec-proxy';
  }

  if (env.DB_PORT) {
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

function spawnProcess(cmd: string, args: string[]): Promise<void> {
  return new Promise((resolve, reject) => {
    const proc = spawn(cmd, args, { stdio: 'inherit' });
    proc.on('error', reject);
    proc.on('close', (code) => {
      if (code === 0) {
        resolve();
      } else {
        reject(new Error(`${cmd} ${args.join(' ')} exited with code ${code}`));
      }
    });
  });
}

async function pollUntilHealthy(serviceUrl: string, timeoutMs: number = 120000): Promise<void> {
  const url = new URL(serviceUrl);
  const startTime = Date.now();

  while (Date.now() - startTime < timeoutMs) {
    try {
      await new Promise<void>((resolve, reject) => {
        const lib = url.protocol === 'https:' ? https : http;
        const req = lib.get(serviceUrl, (res) => {
          res.on('data', () => {});
          res.on('end', () => {
            if (url.pathname === '/' && (res.statusCode === 200 || res.statusCode === 404)) {
              resolve();
            } else if (url.pathname !== '/' && res.statusCode === 200) {
              resolve();
            } else {
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
    } catch {
      await new Promise((r) => setTimeout(r, 2000));
      process.stdout.write('…waiting for service\n');
    }
  }
  throw new Error('Service did not become healthy within timeout');
}

function deleteAtPath(obj: unknown, pathParts: string[]): void {
  if (Array.isArray(obj)) {
    for (const item of obj) {
      deleteAtPath(item, pathParts);
    }
    return;
  }
  if (typeof obj === 'object' && obj !== null) {
    const record = obj as Record<string, unknown>;
    if (pathParts.length === 1) {
      delete record[pathParts[0]];
    } else {
      deleteAtPath(record[pathParts[0]], pathParts.slice(1));
    }
  }
}

function stripNoise(body: unknown, noiseKeys: Record<string, string[]>): unknown {
  const cloned = JSON.parse(JSON.stringify(body));
  for (const key of Object.keys(noiseKeys)) {
    const dotPath = key.replace(/^body\./, '');
    const pathParts = dotPath.split('.');
    deleteAtPath(cloned, pathParts);
  }
  return cloned;
}

function prettyPrint(str: string): string {
  try {
    return JSON.stringify(JSON.parse(str), null, 2);
  } catch {
    return str;
  }
}

function buildSideBySideDiff(expected: string, actual: string): string {
  const expectedLines = prettyPrint(expected).split('\n');
  const actualLines = prettyPrint(actual).split('\n');

  const maxExpectedLen = Math.max(...expectedLines.map((l) => l.length));
  const colWidth = Math.min(Math.max(maxExpectedLen, 40), 60);

  const maxLines = Math.max(expectedLines.length, actualLines.length);
  const rows: string[] = [];

  for (let i = 0; i < maxLines; i++) {
    const expLine = expectedLines[i] ?? '';
    const actLine = actualLines[i] ?? '';
    const paddedExp = expLine.padEnd(colWidth);
    const separator = expLine === actLine ? '  ' : ' ~ ';
    rows.push(`${paddedExp}${separator}${actLine}`);
  }

  return rows.join('\n');
}

async function runHttpTest(
  ktest: KTest,
  serviceUrl: string,
  name: string
): Promise<TestResult> {
  const url = new URL(ktest.spec.req.url);
  const targetUrl = `${serviceUrl}${url.pathname}${url.search}`;

  const lib = serviceUrl.startsWith('https') ? https : http;

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
        } catch {
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

export async function runTests(testSet: LoadedTestSet, options: RunnerOptions): Promise<void> {
  const useCompose = options.composePath && fs.existsSync(options.composePath);
  const composePath = options.composePath ?? 'todo-api/docker-compose.yml';
  const overridePath = useCompose 
    ? path.join(path.dirname(composePath), '.linespec-compose-override.yml')
    : '';

  let proxyServer: net.Server | null = null;
  const dbPort = options.dbPort ?? 3306;

  try {
    const proxyPort = await findFreePort();
    const mocks = testSet.mocks.map((m) => m.mock);

    if (useCompose) {
      const composeParsed = parseComposeFile(options.composePath!);
      const dbUpstreamPort = getDbUpstreamPort(composeParsed);

      process.stdout.write('→ Starting db service...\n');
      await spawnProcess('docker', [
        'compose',
        '-f', options.composePath!,
        'up', '-d', 'db'
      ]);

      process.stdout.write('→ Waiting for db to be ready...\n');
      const dbHost = 'localhost';
      const maxRetries = 30;
      let dbReady = false;
      
      for (let i = 0; i < maxRetries; i++) {
        try {
          const conn = require('net');
          await new Promise<void>((resolve, reject) => {
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
            socket.connect(dbUpstreamPort, dbHost);
          });
          dbReady = true;
          break;
        } catch {
          await new Promise((resolve) => setTimeout(resolve, 2000));
          process.stdout.write('…waiting for db\n');
        }
      }
      
      if (!dbReady) {
        throw new Error('Database did not become ready within timeout');
      }
      
      process.stdout.write('✓ Database ready\n');

      process.stdout.write('→ Building proxy Docker image...\n');
      const proxyImageName = 'linespec-mysql-proxy:latest';
      
      const testDir = path.dirname(options.composePath!);
      const mocksPath = path.join(testDir, 'linespec-tests', 'mocks.yaml');
      const proxyBuildDir = path.join(__dirname, '..', 'proxy-build');
      const proxyBuildDockerfile = path.join(proxyBuildDir, 'Dockerfile');
      
      const proxyBuildDockerfileContent = `FROM node:20-alpine

WORKDIR /app

COPY package.json ./
COPY dist ./dist
COPY mocks.yaml ./

RUN npm install --omit=dev

EXPOSE 3307

CMD ["node", "dist/proxy-server.js", "--mocks", "mocks.yaml", "--port", "3307"]
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
      } catch {
        // ignore
      }
      
      const mocksContent = fs.readFileSync(mocksPath);
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
      
      await spawnProcess('docker', [
        'build', '-t', proxyImageName, '-f', 'proxy-build/Dockerfile', 'proxy-build'
      ]);

      const proxyContainerName = 'linespec-proxy';

      process.stdout.write('→ Starting proxy container...\n');
      await spawnProcess('docker', [
        'rm', '-f', proxyContainerName
      ]);
      
      await spawnProcess('docker', [
        'run', '-d',
        '--name', proxyContainerName,
        '--network', 'todo-api_todo-network',
        '-p', `${proxyPort}:3307`,
        proxyImageName,
        'node', 'dist/proxy-server.js',
        '--mocks', 'mocks.yaml',
        '--upstream-host', 'db',
        '--upstream-port', '3306',
        '--port', '3307'
      ]);

      await new Promise((resolve) => setTimeout(resolve, 2000));

      process.stdout.write(`✓ MySQL proxy running in container ${proxyContainerName}:3307 → db:3306\n`);

      const overrideContent = buildOverrideContentForContainer(composeParsed, proxyPort);
      fs.writeFileSync(overridePath, overrideContent);
      process.stdout.write(`→ Generated override: ${overridePath}\n`);

      process.stdout.write('→ Starting web service...\n');
      await spawnProcess('docker', [
        'compose',
        '-f', options.composePath!,
        '-f', overridePath,
        'up', '-d', '--no-deps', '--no-recreate', '--build', 'web'
      ]);
    } else {
      proxyServer = await startProxy(mocks, 'localhost', dbPort, proxyPort);
      process.stdout.write(`✓ MySQL proxy listening on port ${proxyPort} → localhost:${dbPort}\n`);
      process.stdout.write(`→ Configure your app to connect to localhost:${proxyPort} instead of localhost:${dbPort}\n`);
      process.stdout.write('→ Waiting for service (ensure it is already running)...\n');
    }

    await pollUntilHealthy(options.serviceUrl, 120000);
    process.stdout.write(`✓ Service healthy at ${options.serviceUrl}\n`);

    let passed = 0;
    let failed = 0;
    const results: TestResult[] = [];

    for (const { name, ktest } of testSet.tests) {
      const result = await runHttpTest(ktest, options.serviceUrl, name);
      results.push(result);

      if (result.pass) {
        passed++;
        process.stdout.write(`✓ ${name} PASS\n`);
      } else {
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
              } else {
                process.stdout.write(`${line}\n`);
              }
            } else {
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
        fs.writeFileSync(
          path.join(options.reportDir, `${safeName}.json`),
          JSON.stringify(result, null, 2)
        );
      }

      process.stdout.write(`→ Report written to ${options.reportDir}/\n`);
    }

    process.stdout.write(`\nsummary: ${passed} passed, ${failed} failed\n`);
    if (failed > 0) {
      process.exitCode = 1;
    }
  } finally {
    if (proxyServer) {
      proxyServer.close();
    }

    if (useCompose) {
      try {
        await spawnProcess('docker', [
          'compose',
          '-f', options.composePath!,
          'rm', '-fs', 'web'
        ]);
      } catch {
        // ignore cleanup errors
      }

      if (fs.existsSync(overridePath)) {
        fs.unlinkSync(overridePath);
      }
    }
  }
}
