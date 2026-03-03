import * as net from 'net';
import { EventEmitter } from 'events';
import { KMock, KMockMysqlSpec, VerifyRule } from './types';

// Global event emitter for proxy events
export const proxyEvents = new EventEmitter();

// Global mock queue - shared across all connections
let globalMockQueue: KMockMysqlSpec[] = [];

// Store all mocks for aggregation (Optimization 5)
let allMockSpecs: KMockMysqlSpec[] = [];
let currentTestName: string | null = null;

// Debug logging helper
function logPacketBytes(label: string, data: Buffer, maxBytes: number = 200): void {
  const hex = data.slice(0, Math.min(data.length, maxBytes)).toString('hex').match(/.{1,2}/g)?.join(' ') || '';
  const truncated = data.length > maxBytes ? `... (${data.length} bytes total)` : '';
  console.error(`[mysql-proxy] ${label}: ${hex}${truncated}`);
}

interface Packet {
  seqId: number;
  payload: Buffer;
}

interface ConnectionState {
  phase: 'handshake' | 'command';
  clientBuf: Buffer;
  serverBuf: Buffer;
  verificationError?: string;
}

interface ProxyState {
  verificationErrors: Map<string, string>;
}

// Global state to track verification errors
const proxyState: ProxyState = {
  verificationErrors: new Map(),
};

// Track negative assertion violations
const negativeAssertionViolations: Map<string, string> = new Map();

export function getNegativeAssertionViolation(testName: string): string | undefined {
  return negativeAssertionViolations.get(testName);
}

export function clearNegativeAssertionViolations(): void {
  negativeAssertionViolations.clear();
}

export function setNegativeAssertionViolation(testName: string, violation: string): void {
  negativeAssertionViolations.set(testName, violation);
}

export function getLastVerificationError(testName: string): string | undefined {
  return proxyState.verificationErrors.get(testName);
}

export function clearVerificationErrors(): void {
  proxyState.verificationErrors.clear();
}

export function setVerificationError(testName: string, error: string): void {
  proxyState.verificationErrors.set(testName, error);
}

interface PassthroughState {
  active: boolean;
  responsePhase: 'first' | 'resultset-columns' | 'expect-eof-after-columns' | 'resultset-rows';
  columnsRemaining: number;
  expectEof: boolean;
}

function readPackets(stateBuf: Buffer): { packets: Packet[]; remainder: Buffer } {
  const packets: Packet[] = [];
  let offset = 0;

  while (offset + 4 <= stateBuf.length) {
    const payloadLength = stateBuf[offset] | (stateBuf[offset + 1] << 8) | (stateBuf[offset + 2] << 16);
    const seqId = stateBuf[offset + 3];
    const packetLength = 4 + payloadLength;

    if (offset + packetLength > stateBuf.length) {
      break;
    }

    const payload = stateBuf.slice(offset + 4, offset + packetLength);
    packets.push({ seqId, payload });
    offset += packetLength;
  }

  return { packets, remainder: stateBuf.slice(offset) };
}

function writePacket(seqId: number, payload: Buffer): Buffer {
  const header = Buffer.alloc(4);
  header[0] = payload.length & 0xff;
  header[1] = (payload.length >> 8) & 0xff;
  header[2] = (payload.length >> 16) & 0xff;
  header[3] = seqId;
  return Buffer.concat([header, payload]);
}

function encodeLenenc(n: number): Buffer {
  if (n <= 250) {
    return Buffer.from([n]);
  } else if (n <= 0xffff) {
    const buf = Buffer.alloc(3);
    buf[0] = 0xfc;
    buf[1] = n & 0xff;
    buf[2] = (n >> 8) & 0xff;
    return buf;
  } else if (n <= 0xffffff) {
    const buf = Buffer.alloc(4);
    buf[0] = 0xfd;
    buf[1] = n & 0xff;
    buf[2] = (n >> 8) & 0xff;
    buf[3] = (n >> 16) & 0xff;
    return buf;
  } else {
    const buf = Buffer.alloc(9);
    buf[0] = 0xfe;
    buf[1] = n & 0xff;
    buf[2] = (n >> 8) & 0xff;
    buf[3] = (n >> 16) & 0xff;
    buf[4] = (n >> 24) & 0xff;
    buf[5] = (n >> 32) & 0xff;
    buf[6] = (n >> 40) & 0xff;
    buf[7] = (n >> 48) & 0xff;
    buf[8] = (n >> 56) & 0xff;
    return buf;
  }
}

function encodeLenencStr(s: string): Buffer {
  const strBuf = Buffer.from(s, 'utf8');
  return Buffer.concat([encodeLenenc(strBuf.length), strBuf]);
}

function decodeLenenc(buf: Buffer, offset: number): { value: number; newOffset: number } {
  if (offset >= buf.length) {
    return { value: 0, newOffset: offset };
  }
  const firstByte = buf[offset];
  if (firstByte <= 250) {
    return { value: firstByte, newOffset: offset + 1 };
  } else if (firstByte === 0xfc) {
    if (offset + 3 > buf.length) {
      return { value: 0, newOffset: offset };
    }
    const value = buf[offset + 1] | (buf[offset + 2] << 8);
    return { value, newOffset: offset + 3 };
  } else if (firstByte === 0xfd) {
    if (offset + 4 > buf.length) {
      return { value: 0, newOffset: offset };
    }
    const value = buf[offset + 1] | (buf[offset + 2] << 8) | (buf[offset + 3] << 16);
    return { value, newOffset: offset + 4 };
  } else if (firstByte === 0xfe) {
    if (offset + 9 > buf.length) {
      return { value: 0, newOffset: offset };
    }
    const value =
      buf[offset + 1] |
      (buf[offset + 2] << 8) |
      (buf[offset + 3] << 16) |
      (buf[offset + 4] << 24) |
      (buf[offset + 5] << 32) |
      (buf[offset + 6] << 40) |
      (buf[offset + 7] << 48) |
      (buf[offset + 8] << 56);
    return { value, newOffset: offset + 9 };
  }
  return { value: 0, newOffset: offset };
}

function encodeOkPayload(msg: Record<string, unknown>): Buffer {
  const buffers: Buffer[] = [Buffer.from([0x00])];
  const affectedRows = (msg.affectedRows as number) ?? (msg.affected_rows as number) ?? 0;
  buffers.push(encodeLenenc(affectedRows));
  const lastInsertId = (msg.lastInsertId as number) ?? (msg.last_insert_id as number) ?? 0;
  buffers.push(encodeLenenc(lastInsertId));
  const statusFlags = (msg.statusFlags as number) ?? (msg.status_flags as number) ?? 2;
  buffers.push(Buffer.alloc(2));
  buffers[buffers.length - 1][0] = statusFlags & 0xff;
  buffers[buffers.length - 1][1] = (statusFlags >> 8) & 0xff;
  buffers.push(Buffer.alloc(2));
  const warningCount = (msg.warningCount as number) ?? (msg.warnings as number) ?? 0;
  if (warningCount !== 0) {
    buffers[buffers.length - 1][0] = warningCount & 0xff;
    buffers[buffers.length - 1][1] = (warningCount >> 8) & 0xff;
  }
  if ('info' in msg && msg.info !== undefined) {
    const infoStr = String(msg.info ?? '');
    buffers.push(Buffer.from(infoStr, 'utf8'));
  }
  return Buffer.concat(buffers);
}

function encodeColumnDefPayload(col: Record<string, unknown>): Buffer {
  const buffers: Buffer[] = [];
  buffers.push(encodeLenencStr((col.catalog as string) || 'def'));
  buffers.push(encodeLenencStr((col.schema as string) || ''));
  buffers.push(encodeLenencStr((col.table as string) || ''));
  buffers.push(encodeLenencStr((col.orgTable as string) || (col.org_table as string) || ''));
  buffers.push(encodeLenencStr((col.name as string) || ''));
  buffers.push(encodeLenencStr((col.orgName as string) || (col.org_name as string) || ''));
  buffers.push(Buffer.from([0x0c]));
  const charsetBuf = Buffer.alloc(2);
  const charset = (col.charset as number) ?? (col.character_set as number) ?? 33;
  charsetBuf.writeUInt16LE(charset, 0);
  buffers.push(charsetBuf);
  const colLenBuf = Buffer.alloc(4);
  const columnLength = (col.columnLength as number) ?? (col.column_length as number) ?? 0;
  colLenBuf.writeUInt32LE(columnLength, 0);
  buffers.push(colLenBuf);
  const columnType = (col.columnType as number) ?? (col.type as number) ?? 0x00;
  buffers.push(Buffer.from([columnType]));
  buffers.push(Buffer.alloc(2));
  const columnFlags = (col.columnFlags as number) ?? (col.flags as number);
  if (columnFlags !== undefined) {
    buffers[buffers.length - 1][0] = columnFlags & 0xff;
    buffers[buffers.length - 1][1] = (columnFlags >> 8) & 0xff;
  }
  // Decimals (1 byte)
  const decimals = (col.decimals as number) ?? 0;
  buffers.push(Buffer.from([decimals & 0xff]));
  // Filler (2 bytes)
  buffers.push(Buffer.from([0x00, 0x00]));
  return Buffer.concat(buffers);
}

function encodeTextRowPayload(row: Record<string, unknown>, columnNames?: string[]): Buffer {
  const buffers: Buffer[] = [];
  
  // If columnNames is provided, encode in that order
  if (columnNames && columnNames.length > 0) {
    for (const colName of columnNames) {
      const val = row[colName];
      if (val === null) {
        buffers.push(Buffer.from([0xfb]));
      } else if (typeof val === 'boolean') {
        // MySQL booleans are TINYINT(1), encode as "0" or "1"
        console.error('[mysql-proxy] encodeTextRowPayload: encoding boolean', colName, '=', val, 'as', val ? '1' : '0');
        buffers.push(encodeLenencStr(val ? '1' : '0'));
      } else {
        buffers.push(encodeLenencStr(String(val)));
      }
    }
  } else {
    // Fallback to object values order
    for (const val of Object.values(row)) {
      if (val === null) {
        buffers.push(Buffer.from([0xfb]));
      } else if (typeof val === 'boolean') {
        // MySQL booleans are TINYINT(1), encode as "0" or "1"
        buffers.push(encodeLenencStr(val ? '1' : '0'));
      } else {
        buffers.push(encodeLenencStr(String(val)));
      }
    }
  }
  return Buffer.concat(buffers);
}

function serializeResponses(
  responses: KMockMysqlSpec['responses'],
  startSequenceId: number
): Buffer {
  const buffers: Buffer[] = [];
  let seqId = startSequenceId;

  for (const resp of responses) {
    const packetType = resp.header.packet_type;
    console.error('[mysql-proxy] Processing response packet_type:', packetType);
    const useSequenceId = (resp.header.header as Record<string, unknown>).sequence_id as number ?? seqId;
    let payload: Buffer;

    if (packetType === 'OK') {
      const msg = resp.message as Record<string, unknown>;
      payload = encodeOkPayload(msg);
      const packet = writePacket(useSequenceId, payload);
      logPacketBytes(`OK packet seq=${useSequenceId}`, packet);
      buffers.push(packet);
      seqId = useSequenceId + 1;
      continue;
    } else if (packetType === 'TextResultSet') {
      const msg = resp.message as Record<string, unknown>;
      const columnCount = (msg.columnCount as number) ?? (msg.column_count as number) ?? 0;
      console.error('[mysql-proxy] TextResultSet: columnCount=', columnCount);
      payload = encodeLenenc(columnCount);
      const countPacket = writePacket(useSequenceId, payload);
      logPacketBytes(`Column count packet seq=${useSequenceId}`, countPacket);
      buffers.push(countPacket);
      seqId = useSequenceId + 1;
      if (msg.columns && Array.isArray(msg.columns)) {
        console.error('[mysql-proxy] TextResultSet: encoding', (msg.columns as Record<string, unknown>[]).length, 'columns');
        for (const col of msg.columns as Record<string, unknown>[]) {
          console.error('[mysql-proxy] TextResultSet: encoding column', col.name, 'type=', col.type);
          const colPayload = encodeColumnDefPayload(col);
          const colPacket = writePacket(seqId, colPayload);
          logPacketBytes(`Column def ${col.name} seq=${seqId}`, colPacket, 100);
          buffers.push(colPacket);
          seqId++;
        }
      }
      const eofAfterColumns = msg.eofAfterColumns ?? msg.eof_after_columns;
      if (eofAfterColumns) {
        // eofAfterColumns already includes the full packet with header, don't wrap in writePacket
        let eofBuf: Buffer;
        if (Array.isArray(eofAfterColumns)) {
          eofBuf = Buffer.from(eofAfterColumns as number[]);
        } else {
          eofBuf = Buffer.from((eofAfterColumns as string).split(',').map(b => parseInt(b, 16)));
        }
        logPacketBytes(`EOF after columns (raw)`, eofBuf);
        buffers.push(eofBuf);
        seqId++;
      }
      if (msg.rows && Array.isArray(msg.rows)) {
        console.error('[mysql-proxy] TextResultSet: encoding', msg.rows.length, 'rows');
        // Get column names in order for proper row encoding
        const columnNames = msg.columns && Array.isArray(msg.columns) 
          ? (msg.columns as Record<string, unknown>[]).map((c: Record<string, unknown>) => c.name as string)
          : undefined;
        for (const row of msg.rows as Record<string, unknown>[]) {
          if ('values' in row && Array.isArray(row.values)) {
            const values = row.values as Record<string, unknown>[];
            const rowData: Record<string, unknown> = {};
            for (const v of values) {
              if ('name' in v && 'value' in v) {
                rowData[v.name as string] = v.value;
              }
            }
            const rowPayload = encodeTextRowPayload(rowData, columnNames);
            const rowPacket = writePacket(seqId, rowPayload);
            logPacketBytes(`Row packet seq=${seqId}`, rowPacket, 100);
            buffers.push(rowPacket);
            seqId++;
          }
        }
      }
      // Final EOF
      const finalEofBuf = Buffer.from([0xfe, 0x00, 0x00, 0x00, 0x00]);
      const finalEofPacket = writePacket(seqId, finalEofBuf);
      logPacketBytes(`Final EOF packet seq=${seqId}`, finalEofPacket);
      buffers.push(finalEofPacket);
      seqId++;
      continue;
    } else if (packetType === 'ERR') {
      const msg = resp.message as Record<string, unknown>;
      const errBuf = Buffer.alloc(9);
      errBuf[0] = 0xff;
      const errCode = (msg.errorCode as number) ?? (msg.error_code as number) ?? 1;
      errBuf[1] = errCode & 0xff;
      errBuf[2] = (errCode >> 8) & 0xff;
      errBuf.write('#', 3);
      const sqlState = (msg.sqlState as string) ?? (msg.sql_state as string) ?? 'HY000';
      const sqlStateBuf = Buffer.from(sqlState, 'utf8');
      sqlStateBuf.copy(errBuf, 4);
      const errorMessage = (msg.errorMessage as string) ?? (msg.error_message as string) ?? 'Unknown error';
      const errorBuf = Buffer.from(errorMessage, 'utf8');
      errorBuf.copy(errBuf, 9);
      payload = errBuf;
    } else {
      if (typeof resp.message === 'string') {
        const parts = resp.message.split(',').map(b => parseInt(b, 16));
        payload = Buffer.from(parts);
      } else {
        payload = Buffer.from(JSON.stringify(resp.message), 'utf8');
      }
    }

    buffers.push(writePacket(useSequenceId, payload));
    seqId = useSequenceId + 1;
  }

  return Buffer.concat(buffers);
}

export function buildMockQueue(mocks: KMock[]): KMockMysqlSpec[] {
  const queue: KMockMysqlSpec[] = [];
  for (const mock of mocks) {
    if (mock.kind === 'MySQL') {
      const spec = mock.spec as KMockMysqlSpec;
      // Add mock name to metadata for test filtering (Optimization 5)
      spec.metadata.name = mock.name;
      // Only operational mocks (not config/handshake) go into active queue
      if (spec.metadata && spec.metadata.type === 'mocks') {
        queue.push(spec);
      }
    }
  }
  queue.sort((a, b) => a.created - b.created);
  return queue;
}

export function reloadMocks(mocks: KMock[]): void {
  console.error(`[mysql-proxy] Reloading mocks: clearing ${globalMockQueue.length} mocks, loading ${mocks.length} new mocks`);
  globalMockQueue.length = 0;
  const newQueue = buildMockQueue(mocks);
  globalMockQueue.push(...newQueue);
  // Also store ALL MySQL specs for aggregation mode (Optimization 5)
  allMockSpecs.length = 0;
  for (const mock of mocks) {
    if (mock.kind === 'MySQL') {
      const spec = mock.spec as KMockMysqlSpec;
      spec.metadata.name = mock.name;
      allMockSpecs.push(spec);
    }
  }
  console.error(`[mysql-proxy] Reloaded ${globalMockQueue.length} mocks into global queue (${allMockSpecs.length} total for aggregation)`);
  for (let i = 0; i < globalMockQueue.length; i++) {
    const q = globalMockQueue[i];
    const req = q.requests[0];
    if (req && typeof req.message === 'object' && 'query' in req.message) {
      console.error(`[mysql-proxy] Queue[${i}]: ${(req.message as any).query}`);
    }
  }
}

// Optimization 5: Mock Aggregation - activate mocks for a specific test
export function activateMocksForTest(testName: string): number {
  currentTestName = testName;
  
  // Filter mocks by test name (format: {test-name}-mock-{number})
  const filteredSpecs = allMockSpecs.filter(spec => {
    // Check if spec has metadata with name
    if (spec.metadata && spec.metadata.name) {
      const mockName = spec.metadata.name as string;
      // Match exact test name or test name prefix followed by -mock-
      return mockName === testName || mockName.startsWith(`${testName}-mock-`);
    }
    return false;
  });
  
  // Replace global queue with filtered specs
  globalMockQueue.length = 0;
  globalMockQueue.push(...filteredSpecs);
  
  console.error(`[mysql-proxy] Activated ${filteredSpecs.length} mocks for test "${testName}" (out of ${allMockSpecs.length} total)`);
  for (let i = 0; i < globalMockQueue.length; i++) {
    const q = globalMockQueue[i];
    const req = q.requests[0];
    if (req && typeof req.message === 'object' && 'query' in req.message) {
      console.error(`[mysql-proxy] Queue[${i}]: ${(req.message as any).query}`);
    }
  }
  
  return filteredSpecs.length;
}

function checkVerificationRules(query: string, rules: VerifyRule[] | undefined): { success: boolean; error?: string } {
  if (!rules || rules.length === 0) {
    return { success: true };
  }
  
  for (const rule of rules) {
    switch (rule.type) {
      case 'CONTAINS':
        if (!query.includes(rule.pattern)) {
          return { 
            success: false, 
            error: `VERIFY FAILED: Query does not contain '${rule.pattern}'.\nActual query: ${query}` 
          };
        }
        break;
      case 'NOT_CONTAINS':
        if (query.includes(rule.pattern)) {
          return { 
            success: false, 
            error: `VERIFY FAILED: Query should NOT contain '${rule.pattern}' but it does.\nActual query: ${query}` 
          };
        }
        break;
      case 'MATCHES':
        const regex = new RegExp(rule.pattern, 'i');
        if (!regex.test(query)) {
          return { 
            success: false, 
            error: `VERIFY FAILED: Query does not match pattern /${rule.pattern}/.\nActual query: ${query}` 
          };
        }
        break;
    }
  }
  
  return { success: true };
}

function findAndConsumeMock(
  requestOperation: string,
  query?: string
): { spec: KMockMysqlSpec | null; verificationError?: string } {
  console.error(`[mysql-proxy] findAndConsumeMock called with operation=${requestOperation}, query="${query}"`);
  console.error(`[mysql-proxy] Global queue length: ${globalMockQueue.length}`);
  
  // Create a snapshot of queue indices to avoid race conditions during iteration
  // when the queue is modified by concurrent connections
  const queueSnapshot = globalMockQueue.map((spec, index) => ({ spec, index }));
  
  for (const { spec, index: i } of queueSnapshot) {
    if (spec.metadata.requestOperation !== requestOperation) {
      continue;
    }
    if (requestOperation === 'COM_PING') {
      globalMockQueue.splice(i, 1);
      return { spec };
    }
    if (requestOperation === 'COM_QUERY' && query) {
      for (const req of spec.requests) {
        const msg = req.message;
        if (typeof msg === 'object' && msg !== null && 'query' in msg) {
          const mockQuery = (msg as Record<string, unknown>).query as string;
          const mockLower = mockQuery.toLowerCase();
          const queryLower = query.toLowerCase();
          
          console.error(`[mysql-proxy] Checking mock query: "${mockQuery}"`);
          
          // Normalize queries for matching (remove backticks for comparison)
          const normalizedMock = mockLower.replace(/`/g, '');
          const normalizedQuery = queryLower.replace(/`/g, '');
          
          console.error(`[mysql-proxy] Trying exact match:`);
          console.error(`[mysql-proxy]   normalizedMock: "${normalizedMock}"`);
          console.error(`[mysql-proxy]   normalizedQuery: "${normalizedQuery}"`);
          
          // Check if this is a write operation (should be consumed/removed after use)
          const isWriteOp = normalizedMock.startsWith('insert into ') || 
                            normalizedMock.startsWith('update ') || 
                            normalizedMock.startsWith('delete from ') ||
                            normalizedMock.startsWith('begin') ||
                            normalizedMock.startsWith('commit') ||
                            normalizedMock.startsWith('rollback');
          
          // Try exact match first (normalized)
          if (normalizedQuery === normalizedMock) {
            console.error(`[mysql-proxy] EXACT MATCH found for query: "${query}"`);
            
            // Check if this is a negative mock - if so, fail immediately
            if (spec.metadata.negative) {
              const errorMsg = `NEGATIVE ASSERTION VIOLATED: Query was executed but should NOT have been.\nQuery: "${query}"`;
              console.error(`[mysql-proxy] ${errorMsg}`);
              proxyEvents.emit('negativeAssertionViolation', errorMsg);
              if (currentTestName) {
                setNegativeAssertionViolation(currentTestName, errorMsg);
              }
              return { spec: null, verificationError: errorMsg };
            }
            
            // Check verification rules
            const verifyResult = checkVerificationRules(query, spec.metadata.verify);
            if (!verifyResult.success) {
              console.error(`[mysql-proxy] VERIFICATION FAILED: ${verifyResult.error}`);
              proxyEvents.emit('verificationError', verifyResult.error);
              return { spec: null, verificationError: verifyResult.error };
            }
            
            if (isWriteOp) {
              globalMockQueue.splice(i, 1);
            }
            return { spec };
          }
          
          // For SELECT queries, match by table name appearing in the query (but not schema queries)
          if (normalizedMock.startsWith('select ')) {
            // Skip schema/infrastructure queries
            if (normalizedQuery.includes('information_schema') || 
                normalizedQuery.includes('show full fields') ||
                normalizedQuery.includes('show ')) {
              continue;
            }
            
            // First check if queries have the same structure (both with WHERE or both without)
            const mockHasWhere = normalizedMock.includes(' where ');
            const queryHasWhere = normalizedQuery.includes(' where ');
            
            // If structure differs, skip this mock (let another mock match)
            if (mockHasWhere !== queryHasWhere) {
              console.error(`[mysql-proxy] WHERE clause mismatch - mockHasWhere=${mockHasWhere}, queryHasWhere=${queryHasWhere}`);
              continue;
            }
            
            // CRITICAL: SELECT mocks should only match SELECT queries, not DELETE/INSERT/UPDATE
            // Check that the incoming query is actually a SELECT
            if (!normalizedQuery.startsWith('select ')) {
              console.error(`[mysql-proxy] Query type mismatch - mock is SELECT but query is not: "${normalizedQuery.substring(0, 50)}"`);
              continue;
            }
            
            // If both have WHERE, check if they're for the same column (basic check)
            if (mockHasWhere && queryHasWhere) {
              // Extract the column after WHERE (e.g., "where users.id" or "where email")
              const mockWhereCol = normalizedMock.match(/where\s+[`\w]+\.?`?(\w+)/);
              const queryWhereCol = normalizedQuery.match(/where\s+[`\w]+\.?`?(\w+)/);
              if (mockWhereCol && queryWhereCol && mockWhereCol[1] !== queryWhereCol[1]) {
                // Different WHERE columns, skip this mock
                console.error(`[mysql-proxy] Different WHERE columns - mock: ${mockWhereCol[1]}, query: ${queryWhereCol[1]}`);
                continue;
              }
            }
            
            // Extract table name from mock query
            const tableMatch = normalizedMock.match(/from\s+(\w+)/);
            if (tableMatch) {
              const tableName = tableMatch[1];
              // Check if real query references this table
              const hasTable = normalizedQuery.includes(`from ${tableName}`) || 
                              normalizedQuery.includes(`join ${tableName}`);
              if (hasTable) {
                console.error(`[mysql-proxy] TABLE MATCH for ${requestOperation}: ${tableName}`);
                
                // Check if this is a negative mock - if so, fail immediately
                if (spec.metadata.negative) {
                  const errorMsg = `NEGATIVE ASSERTION VIOLATED: Query was executed but should NOT have been.\nQuery: "${query}"`;
                  console.error(`[mysql-proxy] ${errorMsg}`);
                  proxyEvents.emit('negativeAssertionViolation', errorMsg);
                  if (currentTestName) {
                    setNegativeAssertionViolation(currentTestName, errorMsg);
                  }
                  return { spec: null, verificationError: errorMsg };
                }
                
                // Check verification rules
                const verifyResult = checkVerificationRules(query, spec.metadata.verify);
                if (!verifyResult.success) {
                  console.error(`[mysql-proxy] VERIFICATION FAILED: ${verifyResult.error}`);
                  proxyEvents.emit('verificationError', verifyResult.error);
                  return { spec: null, verificationError: verifyResult.error };
                }
                
                return { spec };
              }
            }
          }
          
          // For INSERT/UPDATE/DELETE, match by operation + table name
          if (normalizedMock.startsWith('insert into ') || 
              normalizedMock.startsWith('update ') || 
              normalizedMock.startsWith('delete from ')) {
            let tableName = '';
            let opPrefix = '';
            if (normalizedMock.startsWith('insert into ')) {
              const match = normalizedMock.match(/^insert into\s+(\w+)/);
              if (match) {
                tableName = match[1];
                opPrefix = 'insert into';
              }
            } else if (normalizedMock.startsWith('update ')) {
              const match = normalizedMock.match(/^update\s+(\w+)/);
              if (match) {
                tableName = match[1];
                opPrefix = 'update';
              }
            } else if (normalizedMock.startsWith('delete from ')) {
              const match = normalizedMock.match(/^delete from\s+(\w+)/);
              if (match) {
                tableName = match[1];
                opPrefix = 'delete from';
              }
            }
            
            console.error(`[mysql-proxy] Pattern matching for ${opPrefix} on table ${tableName}, query starts with: "${normalizedQuery.substring(0, 50)}"`);
            
            if (tableName && normalizedQuery.startsWith(`${opPrefix} ${tableName}`)) {
              console.error(`[mysql-proxy] Pattern match for ${requestOperation}: ${opPrefix} ${tableName}*`);
              
              // Check if this is a negative mock - if so, fail immediately
              if (spec.metadata.negative) {
                const errorMsg = `NEGATIVE ASSERTION VIOLATED: Query was executed but should NOT have been.\nQuery: "${query}"`;
                console.error(`[mysql-proxy] ${errorMsg}`);
                proxyEvents.emit('negativeAssertionViolation', errorMsg);
                if (currentTestName) {
                  setNegativeAssertionViolation(currentTestName, errorMsg);
                }
                return { spec: null, verificationError: errorMsg };
              }
              
              // Check verification rules
              const verifyResult = checkVerificationRules(query, spec.metadata.verify);
              if (!verifyResult.success) {
                console.error(`[mysql-proxy] VERIFICATION FAILED: ${verifyResult.error}`);
                proxyEvents.emit('verificationError', verifyResult.error);
                return { spec: null, verificationError: verifyResult.error };
              }
              
              if (isWriteOp) {
                globalMockQueue.splice(i, 1);
              }
              return { spec };
            }
          }
        }
      }
    }
  }
  console.error(`[mysql-proxy] No match found for query: "${query}" - passing through to DB`);
  return { spec: null };
}

function isResponseComplete(payload: Buffer, passthroughState: PassthroughState): boolean {
  if (!passthroughState.active) {
    return true;
  }
  if (passthroughState.responsePhase === 'first') {
    if (payload[0] === 0x00) {
      return true;
    }
    if (payload[0] === 0xff) {
      return true;
    }
    if (payload[0] === 0xfe && payload.length < 9) {
      return true;
    }
    const { value: columnCount, newOffset } = decodeLenenc(payload, 0);
    if (columnCount > 0) {
      passthroughState.responsePhase = 'resultset-columns';
      passthroughState.columnsRemaining = columnCount;
      passthroughState.expectEof = false;
    }
    return false;
  }
  if (passthroughState.responsePhase === 'resultset-columns') {
    passthroughState.columnsRemaining--;
    if (passthroughState.columnsRemaining === 0) {
      passthroughState.responsePhase = 'expect-eof-after-columns';
      passthroughState.expectEof = true;
    }
    return false;
  }
  if (passthroughState.responsePhase === 'expect-eof-after-columns') {
    if (payload[0] === 0xfe && payload.length < 9) {
      passthroughState.responsePhase = 'resultset-rows';
      passthroughState.expectEof = false;
      return false;
    }
    return false;
  }
  if (passthroughState.responsePhase === 'resultset-rows') {
    if (payload[0] === 0xfe && payload.length < 9) {
      return true;
    }
    return false;
  }
  return true;
}

function handleConnection(
  clientSocket: net.Socket,
  upstreamHost: string,
  upstreamPort: number
): void {
  const upstreamSocket = net.createConnection(upstreamPort, upstreamHost);
  
  upstreamSocket.on('connect', () => {
    console.error(`[mysql-proxy] Connected to upstream ${upstreamHost}:${upstreamPort}`);
  });

  const connState: ConnectionState = {
    phase: 'handshake',
    clientBuf: Buffer.alloc(0),
    serverBuf: Buffer.alloc(0),
  };

  const passthroughState: PassthroughState = {
    active: false,
    responsePhase: 'first',
    columnsRemaining: 0,
    expectEof: false,
  };

  upstreamSocket.on('error', () => clientSocket.destroy());
  clientSocket.on('error', () => upstreamSocket.destroy());

  upstreamSocket.on('data', (data: Buffer) => {
    if (connState.phase === 'handshake') {
      clientSocket.write(data);
      connState.serverBuf = Buffer.concat([connState.serverBuf, data]);
      const { packets, remainder } = readPackets(connState.serverBuf);
      connState.serverBuf = remainder;

      for (const packet of packets) {
        // Looking for OK packet (0x00) to signal end of handshake
        if (packet.payload[0] === 0x00 && packet.payload.length > 5) {
          connState.phase = 'command';
          break;
        }
      }
      return;
    }

    if (passthroughState.active) {
      clientSocket.write(data);
      connState.serverBuf = Buffer.concat([connState.serverBuf, data]);
      const { packets, remainder } = readPackets(connState.serverBuf);
      connState.serverBuf = remainder;

      for (const packet of packets) {
        if (isResponseComplete(packet.payload, passthroughState)) {
          passthroughState.active = false;
          passthroughState.responsePhase = 'first';
          passthroughState.columnsRemaining = 0;
          passthroughState.expectEof = false;
        }
      }
    } else {
      clientSocket.write(data);
    }
  });

  clientSocket.on('data', (data: Buffer) => {
    if (connState.phase === 'handshake') {
      upstreamSocket.write(data);
      return;
    }

    connState.clientBuf = Buffer.concat([connState.clientBuf, data]);
    const { packets, remainder } = readPackets(connState.clientBuf);
    connState.clientBuf = remainder;

    for (const packet of packets) {
      if (packet.payload.length === 0) {
        continue;
      }

      const commandByte = packet.payload[0];

      if (commandByte === 0x0e) {
        const { spec: mock, verificationError } = findAndConsumeMock('COM_PING');
        if (verificationError) {
          console.error('[mysql-proxy] VERIFICATION FAILED for COM_PING: ' + verificationError);
          clientSocket.destroy(new Error(verificationError));
          return;
        }
        if (mock) {
          console.error('[mysql-proxy] Using mock for COM_PING');
          const response = serializeResponses(mock.responses, packet.seqId + 1);
          logPacketBytes('SENDING COM_PING MOCK RESPONSE', response);
          clientSocket.write(response);
        } else {
          console.error('[mysql-proxy] Passing through COM_PING to db');
          passthroughState.active = true;
          passthroughState.responsePhase = 'first';
          passthroughState.columnsRemaining = 0;
          passthroughState.expectEof = false;
          const fullPacket = writePacket(packet.seqId, packet.payload);
          upstreamSocket.write(fullPacket);
        }
        continue;
      }

      if (commandByte === 0x03) {
        const query = packet.payload.slice(1).toString('utf8');
        const { spec: mock, verificationError } = findAndConsumeMock('COM_QUERY', query);
        if (verificationError) {
          console.error('[mysql-proxy] VERIFICATION FAILED for query: ' + query);
          console.error('[mysql-proxy] Error: ' + verificationError);
          clientSocket.destroy(new Error(verificationError));
          return;
        }
        if (mock) {
          console.error('[mysql-proxy] Using mock for: ' + query);
          proxyEvents.emit('queryExecuted', { query, matched: true, timestamp: Date.now() });
          const response = serializeResponses(mock.responses, packet.seqId + 1);
          logPacketBytes('SENDING COM_QUERY MOCK RESPONSE', response);
          clientSocket.write(response);
        } else {
          console.error(`[mysql-proxy] Passing through to db: ` + query);
          proxyEvents.emit('queryExecuted', { query, matched: false, timestamp: Date.now() });
          proxyEvents.emit('queryPassthrough', { query, timestamp: Date.now() });
          passthroughState.active = true;
          passthroughState.responsePhase = 'first';
          passthroughState.columnsRemaining = 0;
          const fullPacket = writePacket(packet.seqId, packet.payload);
          upstreamSocket.write(fullPacket);
        }
        continue;
      }

      passthroughState.active = true;
      passthroughState.responsePhase = 'first';
      passthroughState.columnsRemaining = 0;
      const fullPacket = writePacket(packet.seqId, packet.payload);
      upstreamSocket.write(fullPacket);
    }
  });

  clientSocket.on('close', () => upstreamSocket.destroy());
  upstreamSocket.on('close', () => clientSocket.destroy());
}

export function startProxy(
  mocks: KMock[],
  upstreamHost: string,
  upstreamPort: number,
  listenPort: number
): Promise<net.Server> {
  return new Promise((resolve, reject) => {
    // Initialize global mock queue and all specs for aggregation
    globalMockQueue.length = 0;
    allMockSpecs.length = 0;
    const initialQueue = buildMockQueue(mocks);
    globalMockQueue.push(...initialQueue);
    // Store ALL MySQL specs for aggregation mode (Optimization 5)
    for (const mock of mocks) {
      if (mock.kind === 'MySQL') {
        const spec = mock.spec as KMockMysqlSpec;
        spec.metadata.name = mock.name;
        allMockSpecs.push(spec);
      }
    }
    console.error('[mysql-proxy] Loaded ' + globalMockQueue.length + ' mocks into global queue (' + allMockSpecs.length + ' total for aggregation)');
    for (let i = 0; i < globalMockQueue.length; i++) {
      const q = globalMockQueue[i];
      const req = q.requests[0];
      if (req && typeof req.message === 'object' && 'query' in req.message) {
        console.error('[mysql-proxy] Queue[' + i + ']: ' + (req.message as any).query);
      }
    }

    const server = net.createServer((clientSocket) => {
      handleConnection(clientSocket, upstreamHost, upstreamPort);
    });

    server.on('error', reject);
    server.listen(listenPort, '0.0.0.0', () => resolve(server));
  });
}
