import * as net from 'net';
import * as fs from 'fs';
import * as path from 'path';
import * as yaml from 'js-yaml';
import { spawn } from 'child_process';
import http from 'http';
import https from 'https';
import { startProxy, proxyEvents } from './mysql-proxy';
import type { KMock, KTest, LoadedTestSet, TestResult } from './types';

export interface RunnerOptions {
  composePath?: string;
  serviceUrl: string;
  dbPort?: number;
  reportDir?: string;
  proxyPort?: number;
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

async function reloadMocksViaControlApi(
  proxyHost: string,
  controlPort: number,
  mocksYaml: string
): Promise<void> {
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

    const req = http.request(options, (res) => {
      let data = '';
      res.on('data', chunk => { data += chunk; });
      res.on('end', () => {
        if (res.statusCode === 200) {
          resolve();
        } else {
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

async function clearProxyErrorsViaControlApi(
  proxyHost: string,
  controlPort: number
): Promise<void> {
  return new Promise((resolve, reject) => {
    const options = {
      hostname: proxyHost,
      port: controlPort,
      path: '/clear-errors',
      method: 'POST',
      timeout: 5000,
    };

    const req = http.request(options, (res) => {
      if (res.statusCode === 200) {
        resolve();
      } else {
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
async function activateMocksViaControlApi(
  proxyHost: string,
  controlPort: number,
  testName: string
): Promise<{ count: number }> {
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

    const req = http.request(options, (res) => {
      let data = '';
      res.on('data', chunk => { data += chunk; });
      res.on('end', () => {
        if (res.statusCode === 200) {
          const result = JSON.parse(data);
          resolve({ count: result.count });
        } else {
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

function parseComposeFile(composePath: string): Record<string, unknown> {
  if (!fs.existsSync(composePath)) {
    throw new Error(`Compose file not found: ${composePath}`);
  }
  const content = fs.readFileSync(composePath, 'utf8');
  return yaml.load(content) as Record<string, unknown>;
}

function getDbServiceName(composeParsed: Record<string, unknown>): string | null {
  const services = composeParsed.services as Record<string, unknown>;
  if (!services) return null;

  // Look for common database service names
  const commonDbNames = ['db', 'database', 'mysql', 'postgres', 'postgresql'];
  
  for (const name of commonDbNames) {
    if (services[name]) {
      return name;
    }
  }

  // Try to detect by image or ports
  for (const [name, service] of Object.entries(services)) {
    const svc = service as Record<string, unknown>;
    
    // Check image name
    const image = (svc.image as string) || '';
    if (image.includes('mysql') || image.includes('postgres') || image.includes('mariadb')) {
      return name;
    }
    
    // Check for common DB ports
    const ports = svc.ports as string[];
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

function getDbUpstreamHost(composeParsed: Record<string, unknown>): string {
  const dbServiceName = getDbServiceName(composeParsed);
  return dbServiceName || 'db';
}

function getDbUpstreamPort(composeParsed: Record<string, unknown>): number {
  const dbServiceName = getDbServiceName(composeParsed);
  if (!dbServiceName) return 3306;
  
  try {
    const services = composeParsed.services as Record<string, unknown>;
    const db = services[dbServiceName] as Record<string, unknown>;
    const ports = db.ports as string[];
    if (ports && ports.length > 0) {
      // Get the internal container port (after the colon)
      const portMapping = ports[0];
      const parts = portMapping.split(':');
      if (parts.length >= 2) {
        return parseInt(parts[parts.length - 1], 10);
      }
    }
    
    // Try to infer from image
    const image = (db.image as string) || '';
    if (image.includes('postgres')) {
      return 5432;
    }
  } catch {
    // ignore and return default
  }
  return 3306;
}

function getNetworkName(composePath: string, composeParsed: Record<string, unknown>): string {
  // First, check if there's an explicit network configuration
  const networks = composeParsed.networks as Record<string, unknown>;
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

function extractDbName(composeParsed: Record<string, unknown>): string | null {
  const services = composeParsed.services as Record<string, unknown>;
  if (!services) return null;
  
  // Find the web/app service (not the DB service)
  const appServiceNames = ['web', 'app', 'api', 'server'];
  let appService: Record<string, unknown> | null = null;
  
  for (const name of appServiceNames) {
    if (services[name]) {
      appService = services[name] as Record<string, unknown>;
      break;
    }
  }
  
  // If no known app service, use the first non-DB service
  if (!appService) {
    const dbServiceName = getDbServiceName(composeParsed);
    for (const [name, service] of Object.entries(services)) {
      if (name !== dbServiceName) {
        appService = service as Record<string, unknown>;
        break;
      }
    }
  }
  
  if (!appService) return null;
  
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
    const dbService = services[dbServiceName] as Record<string, unknown>;
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

function normalizeEnv(env: unknown): Record<string, string> {
  if (!env) {
    return {};
  }
  
  // Handle object format: { KEY: 'value' }
  if (typeof env === 'object' && !Array.isArray(env)) {
    return env as Record<string, string>;
  }
  
  // Handle array format: ['KEY=value', 'KEY2=value2']
  if (Array.isArray(env)) {
    const result: Record<string, string> = {};
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

function buildOverrideContent(composeParsed: Record<string, unknown>, proxyPort: number): string {
  const services = composeParsed.services as Record<string, unknown>;
  if (!services || !services.web) {
    throw new Error('Compose file must have a "web" service defined');
  }
  const web = services.web as Record<string, unknown>;
  const env = normalizeEnv(web.environment);
  
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

function buildOverrideContentForContainer(composeParsed: Record<string, unknown>, proxyHost: string = 'linespec-proxy'): string {
  const services = composeParsed.services as Record<string, unknown>;
  if (!services || !services.web) {
    throw new Error('Compose file must have a "web" service defined');
  }
  const web = services.web as Record<string, unknown>;
  const env = normalizeEnv(web.environment);
  
  // For inter-container communication, use the standard MySQL port 3306
  const internalProxyPort = 3306;
  
  let databaseUrl = env.DATABASE_URL;
  if (databaseUrl) {
    databaseUrl = databaseUrl.replace(/@[^@/:]+:\d+\//, `@${proxyHost}:${internalProxyPort}/`);
  }

  const overrideEnv: Record<string, string> = {};

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

function spawnProcess(cmd: string, args: string[], quiet: boolean = false): Promise<void> {
  return new Promise((resolve, reject) => {
    const proc = spawn(cmd, args, { stdio: quiet ? 'pipe' : 'inherit' });
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
      await new Promise((r) => setTimeout(r, 200));
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
  
  // Define error file path for Docker proxy mode
  const errorDirPath = useCompose 
    ? path.resolve(testSet.dir, '.linespec-errors')
    : '';
  const errorFilePath = useCompose
    ? path.join(errorDirPath, 'verification-errors.json')
    : '';

  let proxyServer: net.Server | null = null;
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
      const composeParsed = parseComposeFile(options.composePath!);
      const dbUpstreamPort = getDbUpstreamPort(composeParsed);

      process.stdout.write('→ Starting services...\n');
      
      // Clean up previous database state to ensure fresh start
      try {
        await spawnProcess('docker', [
          'compose',
          '-f', options.composePath!,
          'down', '-v'
        ], true);
      } catch {
        // Ignore errors - containers might not exist
      }
      
      await spawnProcess('docker', [
        'compose',
        '-f', options.composePath!,
        'up', '-d', 'db'
      ], true);

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
          await new Promise((resolve) => setTimeout(resolve, 200));
        }
      }
      
      if (!dbReady) {
        throw new Error('Database did not become ready within timeout');
      }
      
      process.stdout.write('✓ Database ready\n');

      proxyImageName = 'linespec-mysql-proxy:latest';
      
      proxyBuildDir = path.join(__dirname, '..', 'proxy-build');
      const proxyBuildDockerfile = path.join(proxyBuildDir, 'Dockerfile');
      
      const proxyBuildDockerfileContent = `FROM node:20-alpine

WORKDIR /app

COPY package.json ./
COPY dist ./dist
COPY mocks.yaml ./

RUN npm install --omit=dev

EXPOSE 3306 3308

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
      } catch {
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
      networkName = getNetworkName(options.composePath!, composeParsed);
      upstreamHost = getDbUpstreamHost(composeParsed);
      upstreamPort = getDbUpstreamPort(composeParsed);
      internalProxyPort = 3306;
      
      // Ensure error directory exists
      if (!fs.existsSync(errorDirPath)) {
        fs.mkdirSync(errorDirPath, { recursive: true });
      }
      
      // Clean up any existing proxy
      try {
        await spawnProcess('docker', ['rm', '-f', proxyContainerName], true);
      } catch {
        // Ignore errors if container doesn't exist
      }
      
      // Start initial proxy with ALL mocks (Optimization 5: Mock Aggregation)
      // Mocks are already written to mocks.yaml above and will be filtered per-test via /activate
      // fs.writeFileSync(path.join(proxyBuildDir, 'mocks.yaml'), '');  // REMOVED - was clearing mocks
      
      await spawnProcess('docker', [
        'run', '-d',
        '--name', proxyContainerName,
        '--network', networkName,
        '-p', `${proxyPort}:${internalProxyPort}`,
        '-p', `${controlPort}:3308`,
        '-v', `${proxyBuildDir}:/app/mocks:ro`,
        '-v', `${errorDirPath}:/app/errors`,
        proxyImageName,
        'node', 'dist/proxy-server.js',
        '--mocks', '/app/mocks/mocks.yaml',
        '--upstream-host', upstreamHost,
        '--upstream-port', String(upstreamPort),
        '--port', String(internalProxyPort),
        '--control-port', '3308',
        '--error-file', '/app/errors/verification-errors.json',
        '--passthrough-file', '/app/errors/passthrough-queries.json',
        '--query-log-file', '/app/errors/query-log.json'
      ], true);

      const maxProxyRetries = 15;
      let proxyReady = false;
      
      for (let i = 0; i < maxProxyRetries; i++) {
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
            socket.connect(proxyPort, 'localhost');
          });
          proxyReady = true;
          break;
        } catch {
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
            socket.connect(controlPort, 'localhost');
          });
          controlReady = true;
          break;
        } catch {
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
          await new Promise<void>((resolve, reject) => {
            const proc = spawn('docker', [
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
              } else {
                reject(new Error('connection failed'));
              }
            });
            
            proc.on('error', reject);
          });
          networkVerified = true;
          break;
        } catch {
          await new Promise((resolve) => setTimeout(resolve, 100));
        }
      }
      
      if (!networkVerified) {
        throw new Error('Proxy not accessible from Docker network within timeout');
      }

      process.stdout.write(`✓ MySQL proxy ready (${proxyContainerName}:${internalProxyPort})\n`);
      // Removed 3000ms fixed delay - proxy readiness is already confirmed above

      const overrideContent = buildOverrideContentForContainer(composeParsed);
      fs.writeFileSync(overridePath, overrideContent);
      process.stdout.write(`→ Generated override: ${overridePath}\n`);

      process.stdout.write('→ Starting web service...\n');
      await spawnProcess('docker', [
        'compose',
        '-f', options.composePath!,
        '-f', overridePath,
        'up', '-d', '--no-deps', '--no-recreate', '--build', 'web'
      ], true);
    } else {
      proxyServer = await startProxy(mocks, 'localhost', dbPort, proxyPort);
      process.stdout.write(`✓ MySQL proxy listening on port ${proxyPort}\n`);
    }

    await pollUntilHealthy(options.serviceUrl, 120000);
    process.stdout.write(`✓ Service ready at ${options.serviceUrl}\n`);

    let passed = 0;
    let failed = 0;
    const results: TestResult[] = [];
    
    // Track verification errors per test
    const verificationErrors = new Map<string, string>();
    
    // Track queries that passed through to real database
    const passthroughQueries = new Map<string, string[]>();
    const allPassthroughQueries: string[] = [];
    
    // Track all queries executed (matched or passthrough)
    const testQueries = new Map<string, Array<{ query: string; matched: boolean; timestamp: number }>>();
    const allQueries: Array<{ query: string; matched: boolean; timestamp: number }> = [];
    
    // Listen for verification errors from the proxy
    const onVerificationError = (error: string) => {
      // Store the error for the current test
      const currentTest = testSet.tests[results.length]?.name;
      if (currentTest) {
        verificationErrors.set(currentTest, error);
      }
    };
    proxyEvents.on('verificationError', onVerificationError);
    
    // Listen for passthrough queries from the proxy
    const onQueryPassthrough = (data: { query: string; timestamp: number }) => {
      const currentTest = testSet.tests[results.length]?.name;
      if (currentTest) {
        const queries = passthroughQueries.get(currentTest) || [];
        queries.push(data.query);
        passthroughQueries.set(currentTest, queries);
      }
      allPassthroughQueries.push(data.query);
    };
    proxyEvents.on('queryPassthrough', onQueryPassthrough);
    
    // Listen for all query executions from the proxy
    const onQueryExecuted = (data: { query: string; matched: boolean; timestamp: number }) => {
      const currentTest = testSet.tests[results.length]?.name;
      if (currentTest) {
        const queries = testQueries.get(currentTest) || [];
        queries.push(data);
        testQueries.set(currentTest, queries);
      }
      allQueries.push(data);
    };
    proxyEvents.on('queryExecuted', onQueryExecuted);

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
        } catch {
          // Ignore errors - files might not exist
        }
        
        // Activate mocks for this test via control API (much faster than full reload)
        try {
          const result = await activateMocksViaControlApi('localhost', controlPort, name);
          console.error(`[runner] Activated ${result.count} mocks for test "${name}"`);
        } catch (err) {
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
        } catch {
          // Ignore file read errors
        }
      }
      
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
          } catch {
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
          } catch {
            // Ignore file read errors
          }
        }
      }
      
      if (verificationError && result.pass) {
        // If verification failed but HTTP test passed, mark as failed
        result.pass = false;
        result.reason = `SQL Verification Failed: ${verificationError}`;
      } else if (verificationError && !result.pass) {
        // If both failed, append verification error to reason
        result.reason = `${result.reason}\nSQL Verification Failed: ${verificationError}`;
      }
      
      results.push(result);

      if (result.pass) {
        passed++;
        process.stdout.write(`✓ ${name} PASS\n`);
      } else {
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
              } else {
                process.stdout.write(`    ${line}\n`);
              }
            } else {
              process.stdout.write(`    ${line}\n`);
            }
          }
        }
      }
    }
    
    // Remove the event listeners
    proxyEvents.off('verificationError', onVerificationError);
    proxyEvents.off('queryPassthrough', onQueryPassthrough);
    proxyEvents.off('queryExecuted', onQueryExecuted);

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
        fs.writeFileSync(
          path.join(options.reportDir, `${safeName}.json`),
          JSON.stringify(reportData, null, 2)
        );
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
  } finally {
    if (proxyServer) {
      proxyServer.close();
    }

    if (useCompose) {
      // Clean up proxy container
      try {
        await spawnProcess('docker', ['rm', '-f', proxyContainerName], true);
      } catch {
        // ignore cleanup errors
      }
      
      try {
        await spawnProcess('docker', [
          'compose',
          '-f', options.composePath!,
          'rm', '-fs', 'web'
        ], true);
      } catch {
        // ignore cleanup errors
      }

      if (fs.existsSync(overridePath)) {
        fs.unlinkSync(overridePath);
      }
    }
  }
}
