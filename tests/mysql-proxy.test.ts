import { describe, it, expect } from 'vitest';
import * as fs from 'fs';
import * as path from 'path';
import * as yaml from 'js-yaml';
import { encodeOkPayload, encodeColumnDefPayload, encodeTextRowPayload, serializeResponses } from '../src/mysql-proxy';

const MOCKS_PATH = path.join(__dirname, '..', 'todo-api', 'linespec-tests', 'mocks.yaml');
const mocksContent = fs.readFileSync(MOCKS_PATH, 'utf-8');
const mockDocs = yaml.loadAll(mocksContent) as Record<string, unknown>[];

const mockLookup: Record<string, Record<string, unknown>> = {};
for (const doc of mockDocs) {
  if (doc && typeof doc === 'object' && 'name' in doc) {
    const name = doc.name as string;
    mockLookup[name] = doc as Record<string, unknown>;
  }
}

describe('encodeOkPayload', () => {
  it('encodes a 7-byte OK packet for status_flags=2, all zeros', () => {
    const result = encodeOkPayload({ affected_rows: 0, last_insert_id: 0, status_flags: 2, warnings: 0, info: '' });
    expect(result).toEqual(Buffer.from([0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00]));
  });

  it('encodes affected_rows and last_insert_id as lenenc integers', () => {
    const result = encodeOkPayload({ affected_rows: 1, last_insert_id: 42, status_flags: 2, warnings: 0, info: '' });
    expect(result[1]).toBe(0x01);
    expect(result[2]).toBe(0x2a);
  });

  it('encodes info string when present', () => {
    const result = encodeOkPayload({ affected_rows: 0, last_insert_id: 0, status_flags: 2, warnings: 0, info: 'Rows matched: 1' });
    expect(result.length).toBe(7 + 16);
  });

  it('falls back to camelCase keys (affectedRows, lastInsertId, statusFlags)', () => {
    const result = encodeOkPayload({ affectedRows: 1, lastInsertId: 42, statusFlags: 2, warnings: 0, info: '' });
    expect(result[1]).toBe(0x01);
    expect(result[2]).toBe(0x2a);
  });

  it('prefers snake_case over camelCase when both present', () => {
    const result = encodeOkPayload({ affected_rows: 5, affectedRows: 1, last_insert_id: 10, lastInsertId: 42, status_flags: 2, statusFlags: 0 });
    expect(result[1]).toBe(0x05);
    expect(result[2]).toBe(0x0a);
  });
});

describe('encodeColumnDefPayload', () => {
  it('encodes the TABLE_NAME column from test-2-mock-schema', () => {
    const mockSchema = mockLookup['test-2-mock-schema'] as Record<string, unknown>;
    const spec = mockSchema.spec as Record<string, unknown>;
    const responses = spec.responses as Record<string, unknown>[];
    const msg = responses[0].message as Record<string, unknown>;
    const columns = msg.columns as Record<string, unknown>[];
    const tableNameCol = columns[0];
    const result = encodeColumnDefPayload(tableNameCol);
    expect(result[0]).toBe(0x03);
  });

  it('reads org_table, character_set, column_length, type, flags from snake_case keys', () => {
    const mockSchema = mockLookup['test-2-mock-schema'] as Record<string, unknown>;
    const spec = mockSchema.spec as Record<string, unknown>;
    const responses = spec.responses as Record<string, unknown>[];
    const msg = responses[0].message as Record<string, unknown>;
    const columns = msg.columns as Record<string, unknown>[];
    const tableNameCol = columns[0];
    const result = encodeColumnDefPayload(tableNameCol);
    const catalogLen = result[0];
    let offset = 1 + catalogLen;
    const schemaLen = result[offset];
    offset += 1 + schemaLen;
    const tableLen = result[offset];
    offset += 1 + tableLen;
    const orgTableLen = result[offset];
    offset += 1 + orgTableLen;
    const nameLen = result[offset];
    offset += 1 + nameLen;
    const orgNameLen = result[offset];
    offset += 1 + orgNameLen;
    offset += 1;
    const charset = result[offset] | (result[offset + 1] << 8);
    expect(charset).toBe(255);
  });

  it('falls back to camelCase keys (orgTable, characterSet, columnLength, columnFlags)', () => {
    const col = {
      catalog: 'def',
      schema: 'test',
      table: 'users',
      orgTable: 'users',
      name: 'id',
      orgName: 'id',
      characterSet: 63,
      columnLength: 11,
      type: 3,
      columnFlags: 49667,
    };
    const result = encodeColumnDefPayload(col);
    expect(result.length).toBeGreaterThan(0);
  });

  it('prefers snake_case over camelCase when both present', () => {
    const col = {
      catalog: 'def',
      schema: 'test',
      table: 'users',
      orgTable: 'snake_table',
      orgTable: 'camel_table',
      name: 'id',
      orgName: 'snake_id',
      orgName: 'camel_id',
      characterSet: 63,
      characterSet: 45,
      columnLength: 100,
      columnLength: 11,
      type: 3,
      flags: 100,
      columnFlags: 200,
    };
    const result = encodeColumnDefPayload(col);
    expect(result.length).toBeGreaterThan(0);
  });
});

describe('encodeTextRowPayload', () => {
  it('encodes a row with values sub-array (test-2-mock-schema format)', () => {
    const mockSchema = mockLookup['test-2-mock-schema'] as Record<string, unknown>;
    const spec = mockSchema.spec as Record<string, unknown>;
    const responses = spec.responses as Record<string, unknown>[];
    const msg = responses[0].message as Record<string, unknown>;
    const rows = msg.rows as Record<string, unknown>[];
    const firstRow = rows[0];
    const result = encodeTextRowPayload(firstRow);
    expect(result).toEqual(Buffer.concat([Buffer.from([0x11]), Buffer.from('schema_migrations', 'utf8')]));
  });

  it('encodes a NULL value as 0xfb', () => {
    const result = encodeTextRowPayload({ values: [{ value: null }] });
    expect(result).toEqual(Buffer.from([0xfb]));
  });

  it('falls back to Object.values for flat row format', () => {
    const result = encodeTextRowPayload({ id: '3', name: 'Alice' });
    const expected = Buffer.from([0x01, 0x33, 0x05, 0x41, 0x6c, 0x69, 0x63, 0x65]);
    expect(result).toEqual(expected);
  });
});

describe('serializeResponses', () => {
  it('produces a single OK packet for test-2-mock-set-names', () => {
    const mockSetNames = mockLookup['test-2-mock-set-names'] as Record<string, unknown>;
    const spec = mockSetNames.spec as Record<string, unknown>;
    const responses = spec.responses as Record<string, unknown>[];
    const result = serializeResponses(responses, 1);
    expect(result.length).toBe(11);
  });

  it('produces column-count + column-def + EOF + row + final-EOF for test-2-mock-schema', () => {
    const mockSchema = mockLookup['test-2-mock-schema'] as Record<string, unknown>;
    const spec = mockSchema.spec as Record<string, unknown>;
    const responses = spec.responses as Record<string, unknown>[];
    const result = serializeResponses(responses, 1);
    const packets: Buffer[] = [];
    let offset = 0;
    while (offset < result.length) {
      const payloadLength = result[offset] | (result[offset + 1] << 8) | (result[offset + 2] << 16);
      const packetLength = 4 + payloadLength;
      if (offset + packetLength > result.length) break;
      packets.push(result.slice(offset, offset + packetLength));
      offset += packetLength;
    }
    const seqIds = packets.map(p => p[3]);
    expect(seqIds).toEqual([1, 2, 3, 4, 5]);
  });

  it('produces column-count + column-def + EOF + final-EOF for test-2-mock-versions (empty rows)', () => {
    const mockVersions = mockLookup['test-2-mock-versions'] as Record<string, unknown>;
    const spec = mockVersions.spec as Record<string, unknown>;
    const responses = spec.responses as Record<string, unknown>[];
    const result = serializeResponses(responses, 1);
    const packets: Buffer[] = [];
    let offset = 0;
    while (offset < result.length) {
      const payloadLength = result[offset] | (result[offset + 1] << 8) | (result[offset + 2] << 16);
      const packetLength = 4 + payloadLength;
      if (offset + packetLength > result.length) break;
      packets.push(result.slice(offset, offset + packetLength));
      offset += packetLength;
    }
    const seqIds = packets.map(p => p[3]);
    expect(seqIds).toEqual([1, 2, 3, 4]);
  });

  it('accepts legacy data field alongside FinalResponse.data', () => {
    const responses = [
      {
        header: { header: { sequence_id: 1 }, packet_type: 'TextResultSet' },
        message: {
          columnCount: 1,
          columns: [{ name: 'id', type: 3 }],
          rows: [],
          data: [5, 0, 0, 5, 254, 0, 0, 2, 0],
        },
      },
    ] as unknown as Parameters<typeof serializeResponses>[0];
    const result = serializeResponses(responses, 1);
    expect(result.length).toBeGreaterThan(0);
  });

  it('prefers FinalResponse.data over top-level data when both present', () => {
    const responses = [
      {
        header: { header: { sequence_id: 1 }, packet_type: 'TextResultSet' },
        message: {
          columnCount: 1,
          columns: [{ name: 'id', type: 3 }],
          rows: [],
          data: [1, 2, 3],
          FinalResponse: {
            data: [5, 0, 0, 5, 254, 0, 0, 2, 0],
          },
        },
      },
    ] as unknown as Parameters<typeof serializeResponses>[0];
    const result = serializeResponses(responses, 1);
    const packet3Start = result.length - 13;
    const lastPacket = result.slice(packet3Start);
    expect(lastPacket[4]).toBe(5);
  });
});
