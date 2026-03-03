import { Token, LineSpecError } from './lexer';
import {
  TestSpec,
  ReceiveStatement,
  RespondStatement,
  ExpectStatement,
  ExpectHttpStatement,
  ExpectReadMysqlStatement,
  ExpectWriteMysqlStatement,
  ExpectWritePostgresqlStatement,
  ExpectEventStatement,
  ExpectNotStatement,
  ExpectNotHttpStatement,
  ExpectNotWriteMysqlStatement,
  ExpectNotWritePostgresqlStatement,
  ExpectNotEventStatement,
  VerifyRule,
} from './types';
import * as path from 'path';

function peek(tokens: Token[], pos: number): Token | undefined {
  return tokens[pos];
}

function consume(tokens: Token[], pos: { value: number }): Token {
  const token = tokens[pos.value];
  if (!token) {
    throw new LineSpecError('Unexpected end of input');
  }
  pos.value++;
  return token;
}

function expectType(tokens: Token[], pos: { value: number }, type: Token['type']): Token {
  const token = consume(tokens, pos);
  if (token.type !== type) {
    throw new LineSpecError(`Expected ${type} but got ${token.type}`, token.line);
  }
  return token;
}

function parseVerifyRule(value: string, line: number): VerifyRule {
  // Format: query CONTAINS 'string' or query NOT_CONTAINS 'string' or query MATCHES /regex/
  const containsMatch = value.match(/^query\s+CONTAINS\s+['"](.+?)['"]$/i);
  if (containsMatch) {
    return { type: 'CONTAINS', pattern: containsMatch[1] };
  }
  
  const notContainsMatch = value.match(/^query\s+NOT_CONTAINS\s+['"](.+?)['"]$/i);
  if (notContainsMatch) {
    return { type: 'NOT_CONTAINS', pattern: notContainsMatch[1] };
  }
  
  const matchesMatch = value.match(/^query\s+MATCHES\s\/(.+?)\/$/i);
  if (matchesMatch) {
    return { type: 'MATCHES', pattern: matchesMatch[1] };
  }
  
  throw new LineSpecError(`Invalid VERIFY format: ${value}. Expected: VERIFY query CONTAINS 'string', VERIFY query NOT_CONTAINS 'string', or VERIFY query MATCHES /regex/`, line);
}

function parseExpectNotChannel(
  value: string,
  line: number
): Partial<ExpectNotStatement> {
  const eventMatch = value.match(/^(EVENT|MESSAGE):(.+)$/i);
  if (eventMatch) {
    const expectNot: ExpectNotEventStatement = {
      channel: 'EVENT',
      topic: eventMatch[2],
    };
    return expectNot;
  }

  const firstSpace = value.indexOf(' ');
  if (firstSpace === -1) {
    throw new LineSpecError(`Invalid EXPECT NOT channel format: ${value}`, line);
  }

  const channelPart = value.substring(0, firstSpace).toUpperCase();
  const rest = value.substring(firstSpace + 1);

  const httpMatch = channelPart.match(/^HTTP:(\w+)$/i);
  if (httpMatch) {
    const expectNot: ExpectNotHttpStatement = {
      channel: 'HTTP',
      method: httpMatch[1].toUpperCase(),
      url: rest,
    };
    return expectNot;
  }

  const writeMysqlMatch = channelPart.match(/^WRITE:MYSQL$/i);
  if (writeMysqlMatch) {
    const operationMatch = rest.match(/^(INSERT|UPDATE|DELETE)\s+(.+)$/i);
    let operation: 'INSERT' | 'UPDATE' | 'DELETE' | undefined;
    let table: string;
    
    if (operationMatch) {
      operation = operationMatch[1].toUpperCase() as 'INSERT' | 'UPDATE' | 'DELETE';
      table = operationMatch[2];
    } else {
      table = rest;
    }
    
    const expectNot: ExpectNotWriteMysqlStatement = {
      channel: 'WRITE_MYSQL',
      table,
      operation,
    };
    return expectNot;
  }

  if (/^WRITE:POSTGRESQL$/i.test(channelPart)) {
    const expectNot: ExpectNotWritePostgresqlStatement = {
      channel: 'WRITE_POSTGRESQL',
      table: rest,
    };
    return expectNot;
  }

  throw new LineSpecError(`Unrecognized EXPECT NOT channel: ${channelPart}`, line);
}

function parseExpectChannel(
  value: string,
  line: number
): Partial<ExpectStatement> {
  const eventMatch = value.match(/^(EVENT|MESSAGE):(.+)$/i);
  if (eventMatch) {
    const expect: ExpectEventStatement = {
      channel: 'EVENT',
      topic: eventMatch[2],
      withFile: '',
    };
    return expect;
  }

  const firstSpace = value.indexOf(' ');
  if (firstSpace === -1) {
    throw new LineSpecError(`Invalid EXPECT channel format: ${value}`, line);
  }

  const channelPart = value.substring(0, firstSpace).toUpperCase();
  const rest = value.substring(firstSpace + 1);

  const httpMatch = channelPart.match(/^HTTP:(\w+)$/i);
  if (httpMatch) {
    const expect: ExpectHttpStatement = {
      channel: 'HTTP',
      method: httpMatch[1].toUpperCase(),
      url: rest,
      returnsFile: '',
    };
    return expect;
  }

  const writeMysqlMatch = channelPart.match(/^WRITE:MYSQL$/i);
  if (writeMysqlMatch) {
    // Parse optional operation type from rest (e.g., "DELETE users" or just "users")
    const operationMatch = rest.match(/^(INSERT|UPDATE|DELETE)\s+(.+)$/i);
    let operation: 'INSERT' | 'UPDATE' | 'DELETE' | undefined;
    let table: string;
    
    if (operationMatch) {
      operation = operationMatch[1].toUpperCase() as 'INSERT' | 'UPDATE' | 'DELETE';
      table = operationMatch[2];
    } else {
      table = rest;
    }
    
    const expect: ExpectWriteMysqlStatement = {
      channel: 'WRITE_MYSQL',
      table,
      operation,
      withFile: '',
      returnsFile: '',
      transactional: true, // Default to transactional
    };
    return expect;
  }

  if (/^READ:MYSQL$/i.test(channelPart)) {
    const expect: ExpectReadMysqlStatement = {
      channel: 'READ_MYSQL',
      table: rest,
      returnsFile: '',
    };
    return expect;
  }

  if (/^WRITE:POSTGRESQL$/i.test(channelPart)) {
    const expect: ExpectWritePostgresqlStatement = {
      channel: 'WRITE_POSTGRESQL',
      table: rest,
      withFile: '',
      returnsFile: '',
    };
    return expect;
  }

  throw new LineSpecError(`Unrecognized EXPECT channel: ${channelPart}`, line);
}

export { LineSpecError };

export function parse(tokens: Token[], filename: string): TestSpec {
  const pos = { value: 0 };
  let name: string;

  if (peek(tokens, pos.value)?.type === 'TEST') {
    const testToken = consume(tokens, pos);
    name = testToken.value;
  } else {
    name = path.basename(filename, '.linespec');
  }

  const receiveToken = expectType(tokens, pos, 'RECEIVE');
  const httpReceiveMatch = receiveToken.value.match(/^HTTP:(\w+)\s+(.+)$/i);
  if (!httpReceiveMatch) {
    throw new LineSpecError(`Invalid RECEIVE format: ${receiveToken.value}`, receiveToken.line);
  }
  const method = httpReceiveMatch[1].toUpperCase();
  const pathValue = httpReceiveMatch[2];

  let receiveWithFile: string | undefined;
  if (peek(tokens, pos.value)?.type === 'WITH') {
    const withToken = consume(tokens, pos);
    receiveWithFile = withToken.value;
  }

  let receiveHeaders: Record<string, string> | undefined;
  if (peek(tokens, pos.value)?.type === 'HEADERS') {
    const headersToken = consume(tokens, pos);
    const headerLines = headersToken.value.split('\n').map(s => s.trim()).filter(s => s !== '');
    receiveHeaders = {};
    for (const line of headerLines) {
      const colonIndex = line.indexOf(':');
      if (colonIndex > 0) {
        const key = line.substring(0, colonIndex).trim();
        const value = line.substring(colonIndex + 1).trim();
        receiveHeaders[key] = value;
      }
    }
  }

  const receive: ReceiveStatement = {
    channel: 'HTTP',
    method,
    path: pathValue,
    withFile: receiveWithFile,
    headers: receiveHeaders,
  };

  const expects: ExpectStatement[] = [];

  while (peek(tokens, pos.value)?.type === 'EXPECT') {
    const expectToken = consume(tokens, pos);
    const expectPartial = parseExpectChannel(expectToken.value, expectToken.line) as ExpectStatement;

    let sql: string | undefined;
    if (peek(tokens, pos.value)?.type === 'USING_SQL') {
      const sqlToken = consume(tokens, pos);
      sql = sqlToken.value;
    }

    // Check for NO_TRANSACTION keyword
    let transactional = true; // Default
    if (peek(tokens, pos.value)?.type === 'NO_TRANSACTION') {
      consume(tokens, pos);
      transactional = false;
    }

    if (peek(tokens, pos.value)?.type === 'WITH') {
      const withToken = consume(tokens, pos);
      (expectPartial as any).withFile = withToken.value;
    }

    if (peek(tokens, pos.value)?.type === 'RETURNS') {
      const returnsToken = consume(tokens, pos);
      if (returnsToken.value === 'EMPTY') {
        (expectPartial as ExpectReadMysqlStatement).returnsEmpty = true;
      } else {
        (expectPartial as any).returnsFile = returnsToken.value;
      }
    }

    if (sql) {
      (expectPartial as any).sql = sql;
    }
    
    // Set transactional flag for WRITE_MYSQL
    if (expectPartial.channel === 'WRITE_MYSQL') {
      (expectPartial as ExpectWriteMysqlStatement).transactional = transactional;
    }

    // Parse VERIFY clauses
    const verifyRules: VerifyRule[] = [];
    while (peek(tokens, pos.value)?.type === 'VERIFY') {
      const verifyToken = consume(tokens, pos);
      const rule = parseVerifyRule(verifyToken.value, verifyToken.line);
      verifyRules.push(rule);
    }
    
    if (verifyRules.length > 0) {
      (expectPartial as any).verify = verifyRules;
    }

    expects.push(expectPartial);
  }

  const expectsNot: ExpectNotStatement[] = [];

  while (peek(tokens, pos.value)?.type === 'EXPECT_NOT') {
    const expectNotToken = consume(tokens, pos);
    const expectNotPartial = parseExpectNotChannel(expectNotToken.value, expectNotToken.line) as ExpectNotStatement;

    if (peek(tokens, pos.value)?.type === 'WITH') {
      const withToken = consume(tokens, pos);
      (expectNotPartial as any).withFile = withToken.value;
    }

    expectsNot.push(expectNotPartial);
  }

  const respondToken = expectType(tokens, pos, 'RESPOND');
  const httpRespondMatch = respondToken.value.match(/^HTTP:(\d+)$/i);
  if (!httpRespondMatch) {
    throw new LineSpecError(`Invalid RESPOND format: ${respondToken.value}`, respondToken.line);
  }
  const statusCode = parseInt(httpRespondMatch[1], 10);

  let respondWithFile: string | undefined;
  if (peek(tokens, pos.value)?.type === 'WITH') {
    const withToken = consume(tokens, pos);
    respondWithFile = withToken.value;
  }

  let respondNoise: string[] | undefined;
  if (peek(tokens, pos.value)?.type === 'NOISE') {
    const noiseToken = consume(tokens, pos);
    respondNoise = noiseToken.value.split('\n').map(s => s.trim()).filter(s => s !== '');
  }

  const respond: RespondStatement = {
    statusCode,
    withFile: respondWithFile,
    noise: respondNoise,
  };

  if (peek(tokens, pos.value) !== undefined) {
    const extraToken = peek(tokens, pos.value)!;
    throw new LineSpecError('No statements may appear after RESPOND', extraToken.line);
  }

  return {
    name,
    receive,
    expects,
    expectsNot,
    respond,
  };
}
