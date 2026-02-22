import * as fs from 'fs';
import * as path from 'path';
import * as yaml from 'js-yaml';
import { KTest, KMock, LoadedKTest, LoadedMock, LoadedTestSet } from './types';

export function loadTestSet(dir: string): LoadedTestSet {
  const testsDir = path.join(dir, 'tests');

  if (!fs.existsSync(testsDir)) {
    throw new Error(`Tests directory not found: ${testsDir}`);
  }

  const testFiles = fs.readdirSync(testsDir).filter((file) => file.endsWith('.yaml'));

  const tests: LoadedKTest[] = [];
  for (const file of testFiles) {
    const name = path.basename(file, '.yaml');
    const filePath = path.join(testsDir, file);

    try {
      const raw = fs.readFileSync(filePath, 'utf-8');
      const ktest = yaml.load(raw) as KTest;
      tests.push({ name, ktest });
    } catch (err) {
      if (err instanceof Error) {
        throw new Error(`Failed to parse test file ${file}: ${err.message}`);
      }
      throw err;
    }
  }

  const mocksPath = path.join(dir, 'mocks.yaml');
  const mocks: LoadedMock[] = [];

  if (fs.existsSync(mocksPath)) {
    try {
      const raw = fs.readFileSync(mocksPath, 'utf-8');
      const docs = yaml.loadAll(raw) as KMock[];

      for (const mock of docs) {
        if (mock && typeof mock.name === 'string') {
          mocks.push({ name: mock.name, mock });
        }
      }
    } catch (err) {
      if (err instanceof Error) {
        throw new Error(`Failed to parse mocks file: ${err.message}`);
      }
      throw err;
    }
  }

  return { tests, mocks };
}
