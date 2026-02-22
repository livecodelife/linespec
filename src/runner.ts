import * as net from 'net';
import * as fs from 'fs';
import * as path from 'path';
import * as yaml from 'js-yaml';
import { spawn } from 'child_process';
import http from 'http';
import https from 'https';
import { startProxy } from './mysql-proxy';
import type { KMock, KTest, LoadedTestSet } from './types';

export interface RunnerOptions {
  composePath?: string;
  serviceUrl: string;
  dbPort?: number;
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

async function runHttpTest(
  ktest: KTest,
  serviceUrl: string
): Promise<{ pass: boolean; reason?: string }> {
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

    const req = lib.request(targetUrl, options, (res) => {
      let body = '';
      res.on('data', (chunk) => { body += chunk; });
      res.on('end', () => {
        if (res.statusCode !== ktest.spec.resp.status_code) {
          resolve({ pass: false, reason: `status ${res.statusCode} ≠ ${ktest.spec.resp.status_code}` });
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
          resolve({ pass: false, reason: 'body mismatch' });
          return;
        }

        resolve({ pass: true });
      });
    });

    req.on('error', (err) => {
      resolve({ pass: false, reason: err.message });
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
      const overrideContent = buildOverrideContent(composeParsed, proxyPort);
      fs.writeFileSync(overridePath, overrideContent);

      process.stdout.write('→ Starting db service...\n');
      await spawnProcess('docker', [
        'compose',
        '-f', options.composePath!,
        'up', '-d', 'db'
      ]);

      await new Promise((resolve) => setTimeout(resolve, 5000));

      proxyServer = await startProxy(mocks, 'localhost', dbPort, proxyPort);
      process.stdout.write(`✓ MySQL proxy listening on port ${proxyPort} → localhost:${dbPort}\n`);

      process.stdout.write('→ Starting web service...\n');
      await spawnProcess('docker', [
        'compose',
        '-f', options.composePath!,
        '-f', overridePath,
        'up', '-d', '--build', 'web'
      ]);
    } else {
      proxyServer = await startProxy(mocks, 'localhost', dbPort, proxyPort);
      process.stdout.write(`✓ MySQL proxy listening on port ${proxyPort} → localhost:${dbPort}\n`);
      process.stdout.write('→ Waiting for service (ensure it is already running)...\n');
    }

    await pollUntilHealthy(options.serviceUrl, 120000);
    process.stdout.write(`✓ Service healthy at ${options.serviceUrl}\n`);

    let passed = 0;
    let failed = 0;

    for (const { name, ktest } of testSet.tests) {
      const result = await runHttpTest(ktest, options.serviceUrl);
      if (result.pass) {
        passed++;
        process.stdout.write(`✓ ${name} PASS\n`);
      } else {
        failed++;
        process.stdout.write(`✗ ${name} FAIL (${result.reason})\n`);
      }
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
