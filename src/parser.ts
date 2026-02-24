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

  if (/^WRITE:MYSQL$/i.test(channelPart)) {
    const expect: ExpectWriteMysqlStatement = {
      channel: 'WRITE_MYSQL',
      table: rest,
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

  const receive: ReceiveStatement = {
    channel: 'HTTP',
    method,
    path: pathValue,
    withFile: receiveWithFile,
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
      (expectPartial as any).returnsFile = returnsToken.value;
    }

    if (sql) {
      (expectPartial as any).sql = sql;
    }
    
    // Set transactional flag for WRITE_MYSQL
    if (expectPartial.channel === 'WRITE_MYSQL') {
      (expectPartial as ExpectWriteMysqlStatement).transactional = transactional;
    }

    expects.push(expectPartial);
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
    respond,
  };
}
