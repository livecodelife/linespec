import * as fs from 'fs';
import * as path from 'path';
import * as yaml from 'js-yaml';
import {
  TestSpec,
  KTest,
  KMock,
  KMockMysqlSpec,
  KMockHttpSpec,
  KMockEventSpec,
  PayloadFile,
  KTestAssertions,
  ExpectStatement,
  ExpectWriteMysqlStatement,
  VerifyRule,
} from './types';

export interface CompileOptions {
  outDir: string;
  baseDir: string;
}

function loadPayload(baseDir: string, filename: string): PayloadFile {
  const resolved = path.resolve(baseDir, filename);
  const raw = fs.readFileSync(resolved, 'utf-8');
  const parsed = yaml.load(raw) as Record<string, unknown>;
  return { raw, parsed };
}

// Convert ISO 8601 timestamp to MySQL DATETIME format
function toMySqlDateTime(value: unknown): string {
  if (typeof value !== 'string') return String(value);
  
  // Check if it's an ISO 8601 timestamp (e.g., "2026-02-25T10:00:00Z" or "2026-02-25T10:00:00+00:00")
  const isoPattern = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?$/;
  const match = value.match(isoPattern);
  
  if (match) {
    // Convert to MySQL format: "YYYY-MM-DD HH:MM:SS"
    return `${match[1]}-${match[2]}-${match[3]} ${match[4]}:${match[5]}:${match[6]}`;
  }
  
  return value;
}

function generateGenericOkResponse(): Record<string, unknown> {
  return {
    header: 0,
    affected_rows: 1,
    last_insert_id: 0,
    status_flags: 2,
    warnings: 0,
    info: '',
  };
}

function createTransactionMock(
  spec: TestSpec,
  mockIndex: number,
  query: string,
  created: number
): KMock {
  const mysqlSpec: KMockMysqlSpec = {
    metadata: {
      connID: '0',
      requestOperation: 'COM_QUERY',
      responseOperation: 'OK',
      type: 'mocks',
    },
    requests: [{
      header: {
        header: {
          payload_length: query.length + 1,
          sequence_id: 0,
        },
        packet_type: 'COM_QUERY',
      },
      message: { query },
    }],
    responses: [{
      header: {
        header: {
          payload_length: 0,
          sequence_id: 1,
        },
        packet_type: 'OK',
      },
      message: generateGenericOkResponse(),
    }],
    created,
    reqtimestampmock: new Date().toISOString(),
    restimestampmock: new Date().toISOString(),
  };

  return {
    version: 'api.keploy.io/v1beta1',
    kind: 'MySQL',
    name: `${spec.name}-mock-transaction-${mockIndex}`,
    spec: mysqlSpec,
  };
}

function generateInsertSql(table: string, payload: Record<string, unknown>): string {
  const columns = Object.keys(payload);
  const values = columns.map(col => {
    const val = payload[col];
    if (typeof val === 'string') {
      return `'${val.replace(/'/g, "''")}'`;
    } else if (typeof val === 'number' || typeof val === 'boolean') {
      return String(val);
    } else if (val === null || val === undefined) {
      return 'NULL';
    } else {
      return `'${String(val).replace(/'/g, "''")}'`;
    }
  });
  return `INSERT INTO ${table} (${columns.join(', ')}) VALUES (${values.join(', ')})`;
}

function generateUpdateSql(table: string, payload: Record<string, unknown>): string {
  const setClauses = Object.keys(payload)
    .filter(col => col !== 'id')
    .map(col => {
      const val = payload[col];
      let valueStr: string;
      if (typeof val === 'string') {
        valueStr = `'${val.replace(/'/g, "''")}'`;
      } else if (typeof val === 'number' || typeof val === 'boolean') {
        valueStr = String(val);
      } else if (val === null || val === undefined) {
        valueStr = 'NULL';
      } else {
        valueStr = `'${String(val).replace(/'/g, "''")}'`;
      }
      return `${col} = ${valueStr}`;
    });
  
  const id = payload.id;
  if (id !== undefined) {
    return `UPDATE ${table} SET ${setClauses.join(', ')} WHERE id = ${id}`;
  }
  return `UPDATE ${table} SET ${setClauses.join(', ')}`;
}

function generateDeleteSql(table: string, payload: Record<string, unknown>): string {
  if (payload.id !== undefined) {
    return `DELETE FROM ${table} WHERE id = ${payload.id}`;
  }
  throw new Error(
    `Cannot generate DELETE for table "${table}" - payload must include an 'id' field to identify which record to delete.\n` +
    `Example payload: { "id": 42 }`
  );
}

function statusMessage(code: number): string {
  const messages: Record<number, string> = {
    200: 'OK',
    201: 'Created',
    204: 'No Content',
    400: 'Bad Request',
    401: 'Unauthorized',
    403: 'Forbidden',
    404: 'Not Found',
    409: 'Conflict',
    422: 'Unprocessable Entity',
    500: 'Internal Server Error',
  };
  return messages[code] || 'Unknown';
}

function extractPort(url: string): number {
  try {
    const parsed = new URL(url);
    if (parsed.port) {
      return parseInt(parsed.port, 10);
    }
    return 3000;
  } catch {
    return 3000;
  }
}

function buildCurl(
  method: string,
  url: string,
  headers: Record<string, string>,
  body: string
): string {
  let curl = `curl --request ${method} \\n`;
  for (const [key, value] of Object.entries(headers)) {
    curl += `--header '${key}: ${value}' \\n`;
  }
  if (body) {
    curl += `--data "${body}"`;
  }
  return curl;
}

// Helper function to infer column definitions from other expectations in the test
function inferColumnsFromOtherExpects(
  spec: TestSpec,
  payloads: Map<string, PayloadFile>,
  currentTable: string
): Array<Record<string, unknown>> | null {
  // Look through all READ_MYSQL expectations in the test
  for (const expect of spec.expects) {
    if (expect.channel !== 'READ_MYSQL') continue;
    if (expect.table !== currentTable) continue;
    
    const readExpect = expect as any;
    if (!readExpect.returnsFile) continue;
    
    const payload = payloads.get(readExpect.returnsFile);
    if (!payload) continue;
    
    // Check if this payload has data we can use for column inference
    const parsed = payload.parsed;
    if (typeof parsed !== 'object' || parsed === null) continue;
    
    // If it's a rows array with data, extract columns from first row
    if ('rows' in parsed && Array.isArray(parsed.rows) && parsed.rows.length > 0) {
      const firstRow = parsed.rows[0] as Record<string, unknown>;
      return generateColumnsFromRow(firstRow, currentTable);
    }
    
    // If it's a single row object, extract columns directly
    if (!('rows' in parsed)) {
      return generateColumnsFromRow(parsed as Record<string, unknown>, currentTable);
    }
  }
  
  return null;
}

// Generate column definitions from a row object
function generateColumnsFromRow(
  rowData: Record<string, unknown>,
  tableName: string
): Array<Record<string, unknown>> {
  let seqId = 2;
  
  return Object.keys(rowData).map(col => {
    const value = rowData[col];
    let colType = 253; // Default to VAR_STRING
    let colLength = 1020;
    let colFlags = 0;
    let colCharset = 255;
    let colDecimals = 0;
    
    if (col === 'id') {
      colType = 8; // MYSQL_TYPE_LONGLONG (BIGINT)
      colLength = 20;
      colFlags = 16899; // NOT_NULL + PRI_KEY + AUTO_INCREMENT
      colCharset = 63; // Binary
    } else if (col === 'user_id' || col.endsWith('_id')) {
      colType = 8; // MYSQL_TYPE_LONGLONG (BIGINT)
      colLength = 20;
      colFlags = 20489; // NOT_NULL + KEY + UNSIGNED
      colCharset = 63; // Binary
    } else if (col === 'completed') {
      colType = 1; // MYSQL_TYPE_TINY
      colLength = 1;
      colFlags = 0;
      colCharset = 63; // Binary
    } else if (col === 'created_at' || col === 'updated_at' || col.endsWith('_at')) {
      colType = 12; // MYSQL_TYPE_DATETIME
      colLength = 26;
      colFlags = 128; // BINARY only
      colCharset = 63; // Binary
      colDecimals = 6;
    } else if (col === 'description') {
      colType = 252; // MYSQL_TYPE_LONG_BLOB (TEXT)
      colLength = 262140;
      colFlags = 16; // BLOB_FLAG
      colCharset = 255;
    } else if (typeof value === 'string' && value.length > 255) {
      colType = 252; // MYSQL_TYPE_LONG_BLOB (TEXT)
      colLength = 262140;
      colFlags = 16; // BLOB_FLAG
      colCharset = 255;
    } else if (typeof value === 'string') {
      colType = 253; // MYSQL_TYPE_VAR_STRING (VARCHAR)
      colLength = 1020;
      colFlags = 0;
      colCharset = 255;
    }
    
    return {
      header: {
        payload_length: 0,
        sequence_id: seqId++,
      },
      catalog: 'def',
      schema: 'todo_api_development',
      table: tableName,
      orgTable: tableName,
      name: col,
      orgName: col,
      fixed_length: 12,
      character_set: colCharset,
      column_length: colLength,
      type: colType,
      flags: colFlags,
      decimals: colDecimals,
      filler: [0, 0],
      defaultValue: '',
    };
  });
}

// Generate a proper MySQL empty result response with column definitions
function generateEmptyResultResponse(
  tableName: string,
  columns: Array<Record<string, unknown>>
): Record<string, unknown> {
  const seqId = 2 + columns.length + 1; // After column count (1) + all columns + EOF
  const finalSeqId = seqId + 1; // After the empty rows
  
  return {
    columnCount: columns.length,
    columns: columns,
    eofAfterColumns: [5, 0, 0, seqId - 1, 254, 0, 0, 34, 0],
    rows: [],
    FinalResponse: {
      data: [5, 0, 0, finalSeqId, 254, 0, 0, 34, 0],
      type: 'EOF',
    },
  };
}

function buildKTest(spec: TestSpec, payloads: Map<string, PayloadFile>): KTest {
  const receive = spec.receive;
  const respond = spec.respond;

  let body = '';
  if (receive.withFile) {
    const payload = payloads.get(receive.withFile);
    if (payload) {
      body = JSON.stringify(payload.parsed, null, 2);
    }
  }

  let url = receive.path;
  if (!url.startsWith('http')) {
    url = `http://localhost:3000${url}`;
  }

  const parsedUrl = new URL(url);
  const host = parsedUrl.host;

  const reqHeaders: Record<string, string> = {
    'Content-Type': 'application/json',
    Accept: 'application/json',
    Host: host,
  };
  if (body) {
    reqHeaders['Content-Length'] = String(body.length);
  }

  if (receive.headers) {
    Object.assign(reqHeaders, receive.headers);
  }

  let respBody = '';
  if (respond.withFile) {
    const payload = payloads.get(respond.withFile);
    if (payload) {
      respBody = JSON.stringify(payload.parsed);
    }
  }

  const respHeaders: Record<string, string> = respBody
    ? { 'Content-Type': 'application/json; charset=utf-8' }
    : {};

  let assertions: KTestAssertions | undefined;
  if (respond.noise && respond.noise.length > 0) {
    const noiseRecord: Record<string, string[]> = {};
    for (const key of respond.noise) {
      noiseRecord[key] = [];
    }
    assertions = { noise: noiseRecord };
  }

  const ktest: KTest = {
    version: 'api.keploy.io/v1beta1',
    kind: 'Http',
    name: spec.name,
    spec: {
      metadata: {},
      req: {
        method: receive.method,
        proto_major: 1,
        proto_minor: 1,
        url: url,
        header: reqHeaders,
        body,
        timestamp: new Date().toISOString(),
      },
      resp: {
        status_code: respond.statusCode,
        header: respHeaders,
        body: respBody,
        status_message: statusMessage(respond.statusCode),
        proto_major: 0,
        proto_minor: 0,
        timestamp: new Date().toISOString(),
      },
      objects: [],
      assertions,
      created: Math.floor(Date.now() / 1000),
      app_port: extractPort(url),
    },
    curl: buildCurl(receive.method, url, reqHeaders, body),
  };

  return ktest;
}

function buildKMocks(spec: TestSpec, payloads: Map<string, PayloadFile>): KMock[] {
  const mocks: KMock[] = [];

  const hasMysqlExpect = spec.expects.some(
    (e) => e.channel === 'READ_MYSQL' || e.channel === 'WRITE_MYSQL'
  );

  if (hasMysqlExpect) {
    for (let connId = 0; connId < 8; connId += 2) {
      const handshakeMock: KMock = {
        version: 'api.keploy.io/v1beta1',
        kind: 'MySQL',
        name: `${spec.name}-mock-handshake-${connId}`,
        spec: {
          metadata: {
            connID: String(connId),
            requestOperation: 'HandshakeV10',
            responseOperation: 'OK',
            type: 'config',
          },
          requests: [
            {
              header: {
                header: {
                  payload_length: 241,
                  sequence_id: 1,
                },
                packet_type: 'HandshakeResponse41',
              },
              message: {
                capability_flags: 2159977103,
                max_packet_size: 1073741824,
                character_set: 45,
                filler: [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
                username: 'todo_user',
                auth_response: [105, 48, 238, 137, 38, 102, 183, 51, 114, 211, 197, 150, 126, 76, 108, 109, 98, 56, 45, 95, 240, 241, 240, 33, 200, 82, 196, 158, 90, 27, 145, 225],
                database: 'todo_api_development',
                auth_plugin_name: 'caching_sha2_password',
                connection_attributes: {
                  _client_name: 'libmariadb',
                  _client_version: '3.3.17',
                  _os: 'Linux',
                  _pid: '33',
                  _platform: 'aarch64',
                  _server_host: 'db',
                  program_name: './bin/rails',
                },
                zstdcompressionlevel: 0,
              },
            },
            {
              header: {
                header: {
                  payload_length: 1,
                  sequence_id: 3,
                },
                packet_type: 'RequestPublicKey',
              },
              message: 'request_public_key',
            },
            {
              header: {
                header: {
                  payload_length: 256,
                  sequence_id: 5,
                },
                packet_type: 'encrypted_password',
              },
              message: 'gg6t94LVF1DKSd/qJkdTwIaIofQdo5D5v8OD/rrB75cATAkbjp6+VkIDUTeMY+sVnlroCydyQTJ+W5SpZsrivP2pXrl/afv4uHvLb7PrrevlORNO3j690LMfL5xBDqA2DW7Q2wd7oIXkpIqzpqpGWClVG05N0FrpqpmPtLnI90w6RvgsxvX8ikArGj/ytpPz9Qa4QPPP7Mpa8/Ur2w/bOEggjPhRCDQgr+MWoqxwiJzUTk9raWUGVxpgmVEUkv8bj66IhKvJCPZascFHiETkJtd0nZ72w9aNdLOsxq52GzURuxkvY9Zmk3J73Pgp5l4MB7/eTvwLN9cllrNYQdXxqg==',
            },
          ],
          responses: [
            {
              header: {
                header: {
                  payload_length: 73,
                  sequence_id: 0,
                },
                packet_type: 'HandshakeV10',
              },
              message: {
                protocol_version: 10,
                server_version: '8.4.8',
                connection_id: connId + 1,
                auth_plugin_data: [48, 126, 12, 99, 79, 37, 14, 27, 21, 71, 32, 99, 63, 42, 63, 39, 1, 114, 119, 14, 0],
                filler: 0,
                capability_flags: 3758096383,
                character_set: 255,
                status_flags: 2,
                auth_plugin_name: 'caching_sha2_password',
              },
            },
            {
              header: {
                header: {
                  payload_length: 2,
                  sequence_id: 2,
                },
                packet_type: 'AuthMoreData',
              },
              message: {
                status_tag: 1,
                data: 'PerformFullAuthentication',
              },
            },
            {
              header: {
                header: {
                  payload_length: 452,
                  sequence_id: 4,
                },
                packet_type: 'AuthMoreData',
              },
              message: {
                status_tag: 1,
                data: '-----BEGIN PUBLIC KEY-----\nMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAu5YpAjnA8WPNgJSHXhx4\nwef99MJKkN69+y2IFlkOFkwBYZOFLnOjGhqEFqxTxQ8PhFIWa3BCR1EcJOVCMuo6\n5BUQd+dN52YxTX14W3/e2/kD2mu7G2pBAYud5uFbNdn01Llx0edtm3RubymLW2pV\n2DVRBsDZUjBXpN03bv7ih88zpUpu2KJxauRiMpaNz8zuxLYhOLSWYQkPyFnEY9T6\nR2qawOxpw7SmvoTWJtpqzJhcwINBMMlrD52CKFD4fAqAbvvo/QOcwzFcx0WY6T/F\npE0SWXkChqJQwXQbz0csEPgZofzLY8W4bKqSOQCky7NSor1ZykEOE23zwNHaJ7ks\nWQIDAQAB\n-----END PUBLIC KEY-----',
              },
            },
            {
              header: {
                header: {
                  payload_length: 32,
                  sequence_id: 6,
                },
                packet_type: 'OK',
              },
              message: {
                header: 0,
                affected_rows: 0,
                last_insert_id: 0,
                status_flags: 16386,
                warnings: 0,
                info: '',
              },
            },
          ],
          created: Math.floor(Date.now() / 1000),
          reqtimestampmock: new Date().toISOString(),
          restimestampmock: new Date().toISOString(),
        },
      };
      mocks.push(handshakeMock);
    }
  }

  let mockIndex = 0;
  let transactionMockIndex = 0;
  const writeMysqlExpects: Array<{ index: number; expect: ExpectStatement }> = [];

  // First pass: collect WRITE_MYSQL expectations for transaction wrapping
  for (let i = 0; i < spec.expects.length; i++) {
    const expect = spec.expects[i];
    if (expect.channel === 'WRITE_MYSQL') {
      writeMysqlExpects.push({ index: i, expect });
    }
  }

  // Add BEGIN mock if we have transactional WRITE_MYSQL expectations
  if (writeMysqlExpects.length > 0) {
    const firstWriteExpect = writeMysqlExpects[0].expect as ExpectWriteMysqlStatement;
    if (firstWriteExpect.transactional !== false) {
      mocks.push(createTransactionMock(spec, transactionMockIndex++, 'BEGIN', Math.floor(Date.now() / 1000)));
    }
  }

  for (let i = 0; i < spec.expects.length; i++) {
    const expect = spec.expects[i];

    if (expect.channel === 'READ_MYSQL' || expect.channel === 'WRITE_MYSQL') {
      const isWrite = expect.channel === 'WRITE_MYSQL';
      const responseOp = isWrite ? 'OK' : 'TextResultSet';
      const verifyRules = (expect as any).verify as VerifyRule[] | undefined;
      const metadata: KMockMysqlSpec['metadata'] = {
        connID: '0',
        requestOperation: 'COM_QUERY',
        responseOperation: responseOp,
        type: 'mocks',
      };
      
      if (verifyRules && verifyRules.length > 0) {
        metadata.verify = verifyRules;
      }
      
      const mysqlSpec: KMockMysqlSpec = {
        metadata,
        requests: [],
        responses: [],
        created: Math.floor(Date.now() / 1000),
        reqtimestampmock: new Date().toISOString(),
        restimestampmock: new Date().toISOString(),
      };

      let sql: string;
      if ((expect as any).sql) {
        sql = (expect as any).sql;
      } else if (expect.channel === 'READ_MYSQL') {
        sql = `SELECT * FROM ${expect.table}`;
      } else {
        // WRITE_MYSQL - generate SQL based on operation type or auto-detect
        const withFile = (expect as ExpectWriteMysqlStatement).withFile;
        const writeExpect = expect as ExpectWriteMysqlStatement;
        
        if (writeExpect.operation) {
          // Explicit operation type specified
          switch (writeExpect.operation) {
            case 'DELETE':
              if (!withFile) {
                throw new Error(
                  `WRITE:MYSQL DELETE for table "${expect.table}" requires a WITH file to identify which record to delete.\n` +
                  `Example: EXPECT WRITE:MYSQL DELETE ${expect.table} WITH {{payloads/delete_request.yaml}}`
                );
              }
              if (!payloads.has(withFile)) {
                throw new Error(`Payload file not found: ${withFile}`);
              }
              const deletePayload = payloads.get(withFile)!;
              sql = generateDeleteSql(expect.table, deletePayload.parsed);
              break;
            case 'UPDATE':
              if (withFile && payloads.has(withFile)) {
                const updatePayload = payloads.get(withFile)!;
                sql = generateUpdateSql(expect.table, updatePayload.parsed);
              } else {
                sql = `UPDATE ${expect.table}`;
              }
              break;
            case 'INSERT':
            default:
              if (withFile && payloads.has(withFile)) {
                const insertPayload = payloads.get(withFile)!;
                sql = generateInsertSql(expect.table, insertPayload.parsed);
              } else {
                sql = `INSERT INTO ${expect.table}`;
              }
          }
        } else if (withFile && payloads.has(withFile)) {
          // Auto-detect based on payload (only INSERT or UPDATE, never DELETE)
          const payload = payloads.get(withFile)!;
          if (typeof payload.parsed === 'object' && payload.parsed !== null) {
            // Check if this is an UPDATE (has id field AND other fields) or INSERT
            // DELETE is never auto-detected - must use explicit "DELETE" keyword
            if (payload.parsed.id !== undefined && Object.keys(payload.parsed).length > 1) {
              sql = generateUpdateSql(expect.table, payload.parsed);
            } else {
              sql = generateInsertSql(expect.table, payload.parsed);
            }
          } else {
            sql = `INSERT INTO ${expect.table}`;
          }
        } else {
          // No WITH file and no explicit operation - this is an error
          throw new Error(
            `WRITE:MYSQL for table "${expect.table}" requires either a WITH file or an explicit operation type (INSERT, UPDATE, or DELETE).\n` +
            `Example: EXPECT WRITE:MYSQL DELETE ${expect.table} WITH {{payloads/delete_request.yaml}}`
          );
        }
      }

      mysqlSpec.requests.push({
        header: {
          header: {
            payload_length: sql.length + 1,
            sequence_id: 0,
          },
          packet_type: 'COM_QUERY',
        },
        message: { query: sql },
      });

      const returnsFile = (expect as any).returnsFile;
      const returnsEmpty = (expect as any).returnsEmpty;
      
      if (returnsEmpty) {
        // RETURNS EMPTY - generate empty result with proper column definitions
        const tableName = expect.table;
        
        // Try to infer columns from other expectations in the test
        let columns = inferColumnsFromOtherExpects(spec, payloads, tableName);
        
        // If no columns found, generate a minimal set based on common patterns
        if (!columns) {
          // Default column set for typical Rails users table
          columns = generateColumnsFromRow({
            id: 1,
            name: 'placeholder',
            email: 'placeholder@example.com',
            password: 'placeholder',
            token: 'placeholder',
            created_at: '2026-01-01T00:00:00.000Z',
            updated_at: '2026-01-01T00:00:00.000Z',
          }, tableName);
        }
        
        const emptyResponse = generateEmptyResultResponse(tableName, columns);
        mysqlSpec.responses.push({
          header: {
            header: {
              payload_length: 0,
              sequence_id: 1,
            },
            packet_type: responseOp,
          },
          message: emptyResponse,
        });
      } else if (returnsFile && payloads.has(returnsFile)) {
        const payload = payloads.get(returnsFile)!;
        if (responseOp === 'TextResultSet' && typeof payload.parsed === 'object' && payload.parsed !== null && 'rows' in payload.parsed) {
          const rows = payload.parsed.rows as unknown[];
          const parsed = payload.parsed as Record<string, unknown>;
          
          // Check if payload already has valid column definitions (like mysql_empty_result.yaml)
          const existingColumnCount = parsed.columnCount as number | undefined;
          const existingColumns = parsed.columns as unknown[] | undefined;
          
          if (existingColumnCount !== undefined && existingColumns && Array.isArray(existingColumns) && existingColumns.length > 0) {
            // Use the custom payload format directly (preserving column definitions)
            mysqlSpec.responses.push({
              header: {
                header: {
                  payload_length: 0,
                  sequence_id: 1,
                },
                packet_type: responseOp,
              },
              message: parsed,
            });
          } else if (Array.isArray(rows) && rows.length > 0) {
            const firstRow = rows[0] as Record<string, unknown>;
            let seqId = 2; // Column definitions start at sequence_id 2 (after column count at 1)
            
            const columns = Object.keys(firstRow).map(col => {
              // Infer MySQL column type from column name and value - matching Keploy format
              const value = firstRow[col];
              let colType = 253; // Default to VAR_STRING
              let colLength = 1020;
              let colFlags = 0;
              let colCharset = 255;
              let colDecimals = 0;
              
              if (col === 'id') {
                colType = 8; // MYSQL_TYPE_LONGLONG (BIGINT)
                colLength = 20;
                colFlags = 16899; // NOT_NULL + PRI_KEY + AUTO_INCREMENT
                colCharset = 63; // Binary
              } else if (col === 'user_id' || col.endsWith('_id')) {
                colType = 8; // MYSQL_TYPE_LONGLONG (BIGINT)
                colLength = 20;
                colFlags = 20489; // NOT_NULL + KEY + UNSIGNED
                colCharset = 63; // Binary
              } else if (col === 'completed') {
                colType = 1; // MYSQL_TYPE_TINY
                colLength = 1;
                colFlags = 0;
                colCharset = 63; // Binary
              } else if (col === 'created_at' || col === 'updated_at' || col.endsWith('_at')) {
                colType = 12; // MYSQL_TYPE_DATETIME
                colLength = 26;
                colFlags = 128; // BINARY only (NOT_NULL flag confuses Rails mysql2 adapter)
                colCharset = 63; // Binary
                colDecimals = 6;
              } else if (col === 'description') {
                colType = 252; // MYSQL_TYPE_LONG_BLOB (TEXT)
                colLength = 262140;
                colFlags = 16; // BLOB_FLAG
                colCharset = 255;
              } else if (typeof value === 'string' && value.length > 255) {
                colType = 252; // MYSQL_TYPE_LONG_BLOB (TEXT)
                colLength = 262140;
                colFlags = 16; // BLOB_FLAG
                colCharset = 255;
              } else if (typeof value === 'string') {
                colType = 253; // MYSQL_TYPE_VAR_STRING (VARCHAR)
                colLength = 1020;
                colFlags = 0;
                colCharset = 255;
              }
              
              const colDef = {
                header: {
                  payload_length: 0, // Will be calculated by proxy
                  sequence_id: seqId++,
                },
                catalog: 'def',
                schema: expect.table ? `todo_api_development` : '',
                table: expect.table,
                orgTable: expect.table,
                name: col,
                orgName: col,
                fixed_length: 12,
                character_set: colCharset,
                column_length: colLength,
                type: colType,
                flags: colFlags,
                decimals: colDecimals,
                filler: [0, 0],
                defaultValue: '',
              };
              return colDef;
            });
            
            // Convert rows to Keploy format with headers and values array
            let rowSeqId = seqId + 1; // Rows start after columns (plus EOF)
            const keployRows = rows.map((row, index) => {
              const rowData = row as Record<string, unknown>;
                const values = Object.keys(rowData).map(col => {
                  const val = rowData[col];
                  const colDef = columns.find(c => c.name === col);
                  // Convert value to string, with special handling for booleans (MySQL TINYINT is "0" or "1")
                  // and DATETIME columns (convert ISO 8601 to MySQL format)
                  let strValue: string;
                  if (typeof val === 'boolean') {
                    strValue = val ? '1' : '0';
                  } else if (colDef?.type === 12) {
                    // DATETIME column - convert ISO 8601 to MySQL format
                    strValue = toMySqlDateTime(val);
                  } else {
                    strValue = String(val);
                  }
                  return {
                    type: colDef?.type ?? 253,
                    name: col,
                    value: strValue,
                    unsigned: false,
                  };
                });
              
              return {
                header: {
                  payload_length: 0, // Will be calculated
                  sequence_id: rowSeqId++,
                },
                values: values,
              };
            });
            
            mysqlSpec.responses.push({
              header: {
                header: {
                  payload_length: 0,
                  sequence_id: 1,
                },
                packet_type: responseOp,
              },
              message: {
                columnCount: columns.length,
                columns: columns,
                eofAfterColumns: [5, 0, 0, seqId, 254, 0, 0, 34, 0], // EOF after columns (seqId is now at 9 after 7 columns starting at 2)
                rows: keployRows,
                FinalResponse: {
                  data: [5, 0, 0, rowSeqId, 254, 0, 0, 34, 0], // Final EOF uses rowSeqId which is now at the next sequence after all rows
                  type: 'EOF',
                },
              },
            });
          } else {
            // Empty result set - check if payload has custom format with columns
            const parsed = payload.parsed as Record<string, unknown>;
            const existingColumnCount = parsed.columnCount as number | undefined;
            const existingColumns = parsed.columns as unknown[] | undefined;
            
            if (existingColumnCount !== undefined && existingColumns && Array.isArray(existingColumns)) {
              // Use the custom payload format (preserving column definitions for empty result)
              mysqlSpec.responses.push({
                header: {
                  header: {
                    payload_length: 0,
                    sequence_id: 1,
                  },
                  packet_type: responseOp,
                },
                message: parsed,
              });
            } else {
              // Legacy empty result format (no column definitions)
              mysqlSpec.responses.push({
                header: {
                  header: {
                    payload_length: 0,
                    sequence_id: 1,
                  },
                  packet_type: responseOp,
                },
                message: {
                  columnCount: 0,
                  rows: [],
                },
              });
            }
          }
        } else if (responseOp === 'TextResultSet' && typeof payload.parsed === 'object' && payload.parsed !== null) {
          // Payload is a single row object (not wrapped in 'rows' array)
          // Convert it to proper TextResultSet format
          const rowData = payload.parsed as Record<string, unknown>;
          let seqId = 2;
          
          const columns = Object.keys(rowData).map(col => {
            const value = rowData[col];
            let colType = 253;
            let colLength = 1020;
            let colFlags = 0;
            let colCharset = 255;
            let colDecimals = 0;
            
            if (col === 'id') {
              colType = 8;
              colLength = 20;
              colFlags = 16899;
              colCharset = 63;
            } else if (col === 'user_id' || col.endsWith('_id')) {
              colType = 8;
              colLength = 20;
              colFlags = 20489;
              colCharset = 63;
            } else if (col === 'completed') {
              colType = 1;
              colLength = 1;
              colFlags = 0;
              colCharset = 63;
            } else if (col === 'created_at' || col === 'updated_at' || col.endsWith('_at')) {
              colType = 12;
              colLength = 26;
              colFlags = 128;
              colCharset = 63;
              colDecimals = 6;
            } else if (col === 'description') {
              colType = 252;
              colLength = 262140;
              colFlags = 16;
              colCharset = 255;
            } else if (typeof value === 'string' && value.length > 255) {
              colType = 252;
              colLength = 262140;
              colFlags = 16;
              colCharset = 255;
            } else if (typeof value === 'string') {
              colType = 253;
              colLength = 1020;
              colFlags = 0;
              colCharset = 255;
            }
            
            return {
              header: {
                payload_length: 0,
                sequence_id: seqId++,
              },
              catalog: 'def',
              schema: expect.table ? `todo_api_development` : '',
              table: expect.table,
              orgTable: expect.table,
              name: col,
              orgName: col,
              fixed_length: 12,
              character_set: colCharset,
              column_length: colLength,
              type: colType,
              flags: colFlags,
              decimals: colDecimals,
              filler: [0, 0],
              defaultValue: '',
            };
          });
          
          // Convert the single row to Keploy format
          const rowSeqId = seqId + 1;
          const values = Object.keys(rowData).map(col => {
            const val = rowData[col];
            const colDef = columns.find(c => c.name === col);
            let strValue: string;
            if (typeof val === 'boolean') {
              strValue = val ? '1' : '0';
            } else if (colDef?.type === 12) {
              strValue = toMySqlDateTime(val);
            } else {
              strValue = String(val);
            }
            return {
              type: colDef?.type ?? 253,
              name: col,
              value: strValue,
              unsigned: false,
            };
          });
          
          mysqlSpec.responses.push({
            header: {
              header: {
                payload_length: 0,
                sequence_id: 1,
              },
              packet_type: responseOp,
            },
            message: {
              columnCount: columns.length,
              columns: columns,
              eofAfterColumns: [5, 0, 0, seqId, 254, 0, 0, 34, 0],
              rows: [{
                header: {
                  payload_length: 0,
                  sequence_id: rowSeqId,
                },
                values: values,
              }],
              FinalResponse: {
                data: [5, 0, 0, rowSeqId + 1, 254, 0, 0, 34, 0],
                type: 'EOF',
              },
            },
          });
        } else {
          mysqlSpec.responses.push({
            header: {
              header: {
                payload_length: 0,
                sequence_id: 1,
              },
              packet_type: responseOp,
            },
            message: payload.parsed,
          });
        }
      } else if (isWrite) {
        // For WRITE_MYSQL without RETURNS, generate a generic OK response
        mysqlSpec.responses.push({
          header: {
            header: {
              payload_length: 0,
              sequence_id: 1,
            },
            packet_type: 'OK',
          },
          message: generateGenericOkResponse(),
        });
      }

      mocks.push({
        version: 'api.keploy.io/v1beta1',
        kind: 'MySQL',
        name: `${spec.name}-mock-${mockIndex++}`,
        spec: mysqlSpec,
      });
    } else if (expect.channel === 'HTTP') {      const httpExpect = expect as any;
      let reqBody = '';
      if (httpExpect.withFile && payloads.has(httpExpect.withFile)) {
        const payload = payloads.get(httpExpect.withFile)!;
        reqBody = JSON.stringify(payload.parsed);
      }

      let respBody = '';
      let statusCode = 200;
      if (httpExpect.returnsFile && payloads.has(httpExpect.returnsFile)) {
        const payload = payloads.get(httpExpect.returnsFile)!;
        if (typeof payload.parsed === 'object' && payload.parsed !== null && 'status' in payload.parsed) {
          statusCode = payload.parsed.status as number;
          const { status, ...rest } = payload.parsed;
          respBody = JSON.stringify(rest);
        } else {
          respBody = JSON.stringify(payload.parsed);
        }
      }

      const httpSpec: KMockHttpSpec = {
        metadata: {},
        req: {
          method: httpExpect.method,
          url: httpExpect.url,
          header: { 'Content-Type': 'application/json' },
          body: reqBody,
        },
        resp: {
          status_code: statusCode,
          header: { 'Content-Type': 'application/json' },
          body: respBody,
        },
        reqTimestampMock: new Date().toISOString(),
        resTimestampMock: new Date().toISOString(),
      };

      mocks.push({
        version: 'api.keploy.io/v1beta1',
        kind: 'Http',
        name: `${spec.name}-mock-${i}`,
        spec: httpSpec,
      });
    } else if (expect.channel === 'EVENT') {
      const eventExpect = expect as any;
      let message: Record<string, unknown> = {};
      if (eventExpect.withFile && payloads.has(eventExpect.withFile)) {
        const payload = payloads.get(eventExpect.withFile)!;
        message = payload.parsed;
      }

      const eventSpec: KMockEventSpec = {
        metadata: { topic: eventExpect.topic },
        message,
        reqTimestampMock: new Date().toISOString(),
        resTimestampMock: new Date().toISOString(),
      };

      mocks.push({
        version: 'api.keploy.io/v1beta1',
        kind: 'Kafka',
        name: `${spec.name}-mock-${i}`,
        spec: eventSpec,
      });
    }
  }

  // Add COMMIT mock after all WRITE_MYSQL expectations if transactional
  if (writeMysqlExpects.length > 0) {
    const lastWriteExpect = writeMysqlExpects[writeMysqlExpects.length - 1].expect as ExpectWriteMysqlStatement;
    if (lastWriteExpect.transactional !== false) {
      mocks.push(createTransactionMock(spec, transactionMockIndex++, 'COMMIT', Math.floor(Date.now() / 1000) + 1));
    }
  }

  return mocks;
}

function buildNegativeKMocks(spec: TestSpec, payloads: Map<string, PayloadFile>): KMock[] {
  const mocks: KMock[] = [];

  for (let i = 0; i < spec.expectsNot.length; i++) {
    const expectNot = spec.expectsNot[i];

    if (expectNot.channel === 'WRITE_MYSQL' || expectNot.channel === 'WRITE_POSTGRESQL') {
      const isMysql = expectNot.channel === 'WRITE_MYSQL';
      const table = expectNot.table;
      let sql: string;

      if ((expectNot as any).withFile && payloads.has((expectNot as any).withFile)) {
        const payload = payloads.get((expectNot as any).withFile)!;
        sql = generateInsertSql(table, payload.parsed);
      } else {
        sql = `INSERT INTO ${table}`;
      }

      const mysqlSpec: KMockMysqlSpec = {
        metadata: {
          connID: '0',
          requestOperation: 'COM_QUERY',
          responseOperation: 'OK',
          type: 'mocks',
          negative: true, // Mark as negative mock
        },
        requests: [{
          header: {
            header: {
              payload_length: sql.length + 1,
              sequence_id: 0,
            },
            packet_type: 'COM_QUERY',
          },
          message: { query: sql },
        }],
        responses: [], // No response needed - proxy will reject
        created: Math.floor(Date.now() / 1000),
        reqtimestampmock: new Date().toISOString(),
        restimestampmock: new Date().toISOString(),
      };

      mocks.push({
        version: 'api.keploy.io/v1beta1',
        kind: 'MySQL',
        name: `${spec.name}-mock-not-${i}`,
        spec: mysqlSpec,
      });
    } else if (expectNot.channel === 'HTTP') {
      const httpExpectNot = expectNot as any;
      let reqBody = '';
      if (httpExpectNot.withFile && payloads.has(httpExpectNot.withFile)) {
        const payload = payloads.get(httpExpectNot.withFile)!;
        reqBody = JSON.stringify(payload.parsed);
      }

      const httpSpec: KMockHttpSpec = {
        metadata: { negative: true }, // Mark as negative mock
        req: {
          method: httpExpectNot.method,
          url: httpExpectNot.url,
          header: { 'Content-Type': 'application/json' },
          body: reqBody,
        },
        resp: {
          status_code: 0, // Never used
          header: {},
          body: '',
        },
        reqTimestampMock: new Date().toISOString(),
        resTimestampMock: new Date().toISOString(),
      };

      mocks.push({
        version: 'api.keploy.io/v1beta1',
        kind: 'Http',
        name: `${spec.name}-mock-not-${i}`,
        spec: httpSpec,
      });
    } else if (expectNot.channel === 'EVENT') {
      const eventExpectNot = expectNot as any;
      let message: Record<string, unknown> = {};
      if (eventExpectNot.withFile && payloads.has(eventExpectNot.withFile)) {
        const payload = payloads.get(eventExpectNot.withFile)!;
        message = payload.parsed;
      }

      const eventSpec: KMockEventSpec = {
        metadata: { topic: eventExpectNot.topic, negative: true }, // Mark as negative mock
        message,
        reqTimestampMock: new Date().toISOString(),
        resTimestampMock: new Date().toISOString(),
      };

      mocks.push({
        version: 'api.keploy.io/v1beta1',
        kind: 'Kafka',
        name: `${spec.name}-mock-not-${i}`,
        spec: eventSpec,
      });
    }
  }

  return mocks;
}

export function compile(spec: TestSpec, options: CompileOptions): void {
  const { outDir, baseDir } = options;

  const fileRefs = new Set<string>();

  if (spec.receive.withFile) {
    fileRefs.add(spec.receive.withFile);
  }
  for (const expect of spec.expects) {
    const e = expect as any;
    if (e.withFile) {
      fileRefs.add(e.withFile);
    }
    if (e.returnsFile) {
      fileRefs.add(e.returnsFile);
    }
  }
  for (const expectNot of spec.expectsNot) {
    const e = expectNot as any;
    if (e.withFile) {
      fileRefs.add(e.withFile);
    }
  }
  if (spec.respond.withFile) {
    fileRefs.add(spec.respond.withFile);
  }

  const payloads = new Map<string, PayloadFile>();
  for (const file of fileRefs) {
    payloads.set(file, loadPayload(baseDir, file));
  }

  const ktest = buildKTest(spec, payloads);
  const kmocks = buildKMocks(spec, payloads);
  const negativeKMocks = buildNegativeKMocks(spec, payloads);

  fs.mkdirSync(path.join(outDir, 'tests'), { recursive: true });

  const ktestYaml = yaml.dump(ktest, { lineWidth: -1, noRefs: true });
  fs.writeFileSync(path.join(outDir, 'tests', spec.name + '.yaml'), ktestYaml);

  const allMocks = [...kmocks, ...negativeKMocks];
  if (allMocks.length > 0) {
    const mockDocs = allMocks.map((mock) => yaml.dump(mock, { lineWidth: -1, noRefs: true }));
    const mocksContent = mockDocs.map((d) => '---\n' + d).join('');
    // Append to mocks.yaml instead of overwriting
    const mocksPath = path.join(outDir, 'mocks.yaml');
    if (fs.existsSync(mocksPath)) {
      fs.appendFileSync(mocksPath, mocksContent);
    } else {
      fs.writeFileSync(mocksPath, mocksContent);
    }
  }
}
