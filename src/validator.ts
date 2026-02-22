import * as fs from 'fs';
import * as path from 'path';
import { LineSpecError } from './lexer';
import { TestSpec } from './types';

export { LineSpecError };

export function validate(spec: TestSpec, baseDir: string): void {
  for (const expect of spec.expects) {
    if (
      (expect.channel === 'HTTP' || expect.channel === 'WRITE_MYSQL' || expect.channel === 'WRITE_POSTGRESQL') &&
      !(expect as any).returnsFile
    ) {
      throw new LineSpecError(`RETURNS is required for EXPECT ${expect.channel}`);
    }
  }

  for (const expect of spec.expects) {
    if (
      (expect.channel === 'WRITE_MYSQL' || expect.channel === 'WRITE_POSTGRESQL' || expect.channel === 'EVENT') &&
      !(expect as any).withFile
    ) {
      throw new LineSpecError(`WITH is required for EXPECT ${expect.channel}`);
    }
  }

  if (spec.respond.statusCode < 100 || spec.respond.statusCode > 599) {
    throw new LineSpecError(`Invalid HTTP status code: ${spec.respond.statusCode}`);
  }

  const seenKeys = new Set<string>();
  for (const expect of spec.expects) {
    let key: string;
    if (expect.channel === 'HTTP') {
      key = `HTTP:${(expect as any).method}:${(expect as any).url}`;
    } else if (expect.channel === 'READ_MYSQL' || expect.channel === 'WRITE_MYSQL' || expect.channel === 'WRITE_POSTGRESQL') {
      key = `${expect.channel}:${expect.table}`;
    } else {
      key = `EVENT:${(expect as any).topic}`;
    }

    if (seenKeys.has(key)) {
      throw new LineSpecError(`Duplicate EXPECT: ${key}`);
    }
    seenKeys.add(key);
  }

  const fileRefs: string[] = [];
  if (spec.receive.withFile) {
    fileRefs.push(spec.receive.withFile);
  }
  for (const expect of spec.expects) {
    if ((expect as any).withFile) {
      fileRefs.push((expect as any).withFile);
    }
    if ((expect as any).returnsFile) {
      fileRefs.push((expect as any).returnsFile);
    }
  }
  if (spec.respond.withFile) {
    fileRefs.push(spec.respond.withFile);
  }

  for (const file of fileRefs) {
    const resolved = path.resolve(baseDir, file);
    if (!fs.existsSync(resolved)) {
      throw new LineSpecError(`Payload file not found: ${file}`);
    }
  }

  for (const expect of spec.expects) {
    if ((expect as any).sql !== undefined && (expect as any).sql.trim() === '') {
      throw new LineSpecError('USING_SQL block is empty');
    }
  }
}
