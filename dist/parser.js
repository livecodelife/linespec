"use strict";
var __createBinding = (this && this.__createBinding) || (Object.create ? (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    var desc = Object.getOwnPropertyDescriptor(m, k);
    if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) {
      desc = { enumerable: true, get: function() { return m[k]; } };
    }
    Object.defineProperty(o, k2, desc);
}) : (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    o[k2] = m[k];
}));
var __setModuleDefault = (this && this.__setModuleDefault) || (Object.create ? (function(o, v) {
    Object.defineProperty(o, "default", { enumerable: true, value: v });
}) : function(o, v) {
    o["default"] = v;
});
var __importStar = (this && this.__importStar) || (function () {
    var ownKeys = function(o) {
        ownKeys = Object.getOwnPropertyNames || function (o) {
            var ar = [];
            for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k)) ar[ar.length] = k;
            return ar;
        };
        return ownKeys(o);
    };
    return function (mod) {
        if (mod && mod.__esModule) return mod;
        var result = {};
        if (mod != null) for (var k = ownKeys(mod), i = 0; i < k.length; i++) if (k[i] !== "default") __createBinding(result, mod, k[i]);
        __setModuleDefault(result, mod);
        return result;
    };
})();
Object.defineProperty(exports, "__esModule", { value: true });
exports.LineSpecError = void 0;
exports.parse = parse;
const lexer_1 = require("./lexer");
Object.defineProperty(exports, "LineSpecError", { enumerable: true, get: function () { return lexer_1.LineSpecError; } });
const path = __importStar(require("path"));
function peek(tokens, pos) {
    return tokens[pos];
}
function consume(tokens, pos) {
    const token = tokens[pos.value];
    if (!token) {
        throw new lexer_1.LineSpecError('Unexpected end of input');
    }
    pos.value++;
    return token;
}
function expectType(tokens, pos, type) {
    const token = consume(tokens, pos);
    if (token.type !== type) {
        throw new lexer_1.LineSpecError(`Expected ${type} but got ${token.type}`, token.line);
    }
    return token;
}
function parseExpectChannel(value, line) {
    const eventMatch = value.match(/^(EVENT|MESSAGE):(.+)$/i);
    if (eventMatch) {
        const expect = {
            channel: 'EVENT',
            topic: eventMatch[2],
            withFile: '',
        };
        return expect;
    }
    const firstSpace = value.indexOf(' ');
    if (firstSpace === -1) {
        throw new lexer_1.LineSpecError(`Invalid EXPECT channel format: ${value}`, line);
    }
    const channelPart = value.substring(0, firstSpace).toUpperCase();
    const rest = value.substring(firstSpace + 1);
    const httpMatch = channelPart.match(/^HTTP:(\w+)$/i);
    if (httpMatch) {
        const expect = {
            channel: 'HTTP',
            method: httpMatch[1].toUpperCase(),
            url: rest,
            returnsFile: '',
        };
        return expect;
    }
    if (/^WRITE:MYSQL$/i.test(channelPart)) {
        const expect = {
            channel: 'WRITE_MYSQL',
            table: rest,
            withFile: '',
            returnsFile: '',
        };
        return expect;
    }
    if (/^READ:MYSQL$/i.test(channelPart)) {
        const expect = {
            channel: 'READ_MYSQL',
            table: rest,
            returnsFile: '',
        };
        return expect;
    }
    if (/^WRITE:POSTGRESQL$/i.test(channelPart)) {
        const expect = {
            channel: 'WRITE_POSTGRESQL',
            table: rest,
            withFile: '',
            returnsFile: '',
        };
        return expect;
    }
    throw new lexer_1.LineSpecError(`Unrecognized EXPECT channel: ${channelPart}`, line);
}
function parse(tokens, filename) {
    const pos = { value: 0 };
    let name;
    if (peek(tokens, pos.value)?.type === 'TEST') {
        const testToken = consume(tokens, pos);
        name = testToken.value;
    }
    else {
        name = path.basename(filename, '.linespec');
    }
    const receiveToken = expectType(tokens, pos, 'RECEIVE');
    const httpReceiveMatch = receiveToken.value.match(/^HTTP:(\w+)\s+(.+)$/i);
    if (!httpReceiveMatch) {
        throw new lexer_1.LineSpecError(`Invalid RECEIVE format: ${receiveToken.value}`, receiveToken.line);
    }
    const method = httpReceiveMatch[1].toUpperCase();
    const pathValue = httpReceiveMatch[2];
    let receiveWithFile;
    if (peek(tokens, pos.value)?.type === 'WITH') {
        const withToken = consume(tokens, pos);
        receiveWithFile = withToken.value;
    }
    const receive = {
        channel: 'HTTP',
        method,
        path: pathValue,
        withFile: receiveWithFile,
    };
    const expects = [];
    while (peek(tokens, pos.value)?.type === 'EXPECT') {
        const expectToken = consume(tokens, pos);
        const expectPartial = parseExpectChannel(expectToken.value, expectToken.line);
        let sql;
        if (peek(tokens, pos.value)?.type === 'USING_SQL') {
            const sqlToken = consume(tokens, pos);
            sql = sqlToken.value;
        }
        if (peek(tokens, pos.value)?.type === 'WITH') {
            const withToken = consume(tokens, pos);
            expectPartial.withFile = withToken.value;
        }
        if (peek(tokens, pos.value)?.type === 'RETURNS') {
            const returnsToken = consume(tokens, pos);
            expectPartial.returnsFile = returnsToken.value;
        }
        if (sql) {
            expectPartial.sql = sql;
        }
        expects.push(expectPartial);
    }
    const respondToken = expectType(tokens, pos, 'RESPOND');
    const httpRespondMatch = respondToken.value.match(/^HTTP:(\d+)$/i);
    if (!httpRespondMatch) {
        throw new lexer_1.LineSpecError(`Invalid RESPOND format: ${respondToken.value}`, respondToken.line);
    }
    const statusCode = parseInt(httpRespondMatch[1], 10);
    let respondWithFile;
    if (peek(tokens, pos.value)?.type === 'WITH') {
        const withToken = consume(tokens, pos);
        respondWithFile = withToken.value;
    }
    const respond = {
        statusCode,
        withFile: respondWithFile,
    };
    if (peek(tokens, pos.value) !== undefined) {
        const extraToken = peek(tokens, pos.value);
        throw new lexer_1.LineSpecError('No statements may appear after RESPOND', extraToken.line);
    }
    return {
        name,
        receive,
        expects,
        respond,
    };
}
