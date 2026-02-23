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
exports.compile = compile;
const fs = __importStar(require("fs"));
const path = __importStar(require("path"));
const yaml = __importStar(require("js-yaml"));
function loadPayload(baseDir, filename) {
    const resolved = path.resolve(baseDir, filename);
    const raw = fs.readFileSync(resolved, 'utf-8');
    const parsed = yaml.load(raw);
    return { raw, parsed };
}
function statusMessage(code) {
    const messages = {
        200: 'OK',
        201: 'Created',
        204: 'No Content',
        400: 'Bad Request',
        401: 'Unauthorized',
        403: 'Forbidden',
        404: 'Not Found',
        422: 'Unprocessable Entity',
        500: 'Internal Server Error',
    };
    return messages[code] || 'Unknown';
}
function extractPort(url) {
    try {
        const parsed = new URL(url);
        if (parsed.port) {
            return parseInt(parsed.port, 10);
        }
        return 3000;
    }
    catch {
        return 3000;
    }
}
function buildCurl(method, url, headers, body) {
    let curl = `curl --request ${method} \\\n`;
    for (const [key, value] of Object.entries(headers)) {
        curl += `--header '${key}: ${value}' \\\n`;
    }
    if (body) {
        curl += `--data "${body}"`;
    }
    return curl;
}
function buildKTest(spec, payloads) {
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
    const reqHeaders = {
        'Content-Type': 'application/json',
        Accept: 'application/json',
        Host: host,
    };
    if (body) {
        reqHeaders['Content-Length'] = String(body.length);
    }
    let respBody = '';
    if (respond.withFile) {
        const payload = payloads.get(respond.withFile);
        if (payload) {
            respBody = JSON.stringify(payload.parsed);
        }
    }
    const respHeaders = respBody
        ? { 'Content-Type': 'application/json; charset=utf-8' }
        : {};
    let assertions;
    if (respond.noise && respond.noise.length > 0) {
        const noiseRecord = {};
        for (const key of respond.noise) {
            noiseRecord[key] = [];
        }
        assertions = { noise: noiseRecord };
    }
    const ktest = {
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
function buildKMocks(spec, payloads) {
    const mocks = [];
    const hasMysqlExpect = spec.expects.some((e) => e.channel === 'READ_MYSQL' || e.channel === 'WRITE_MYSQL');
    if (hasMysqlExpect) {
        for (let connId = 0; connId < 8; connId += 2) {
            const handshakeMock = {
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
    for (let i = 0; i < spec.expects.length; i++) {
        const expect = spec.expects[i];
        if (expect.channel === 'READ_MYSQL' || expect.channel === 'WRITE_MYSQL') {
            const isWrite = expect.channel === 'WRITE_MYSQL';
            const responseOp = isWrite ? 'OK' : 'TextResultSet';
            const mysqlSpec = {
                metadata: {
                    connID: '0',
                    requestOperation: 'COM_QUERY',
                    responseOperation: responseOp,
                    type: 'mocks',
                },
                requests: [],
                responses: [],
                created: Math.floor(Date.now() / 1000),
                reqtimestampmock: new Date().toISOString(),
                restimestampmock: new Date().toISOString(),
            };
            let sql;
            if (expect.sql) {
                sql = expect.sql;
            }
            else if (expect.channel === 'READ_MYSQL') {
                sql = `SELECT * FROM ${expect.table}`;
            }
            else {
                sql = `INSERT INTO ${expect.table}`;
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
            const returnsFile = expect.returnsFile;
            if (returnsFile && payloads.has(returnsFile)) {
                const payload = payloads.get(returnsFile);
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
            mocks.push({
                version: 'api.keploy.io/v1beta1',
                kind: 'MySQL',
                name: `${spec.name}-mock-${i}`,
                spec: mysqlSpec,
            });
        }
        else if (expect.channel === 'HTTP') {
            const httpExpect = expect;
            let reqBody = '';
            if (httpExpect.withFile && payloads.has(httpExpect.withFile)) {
                const payload = payloads.get(httpExpect.withFile);
                reqBody = JSON.stringify(payload.parsed);
            }
            let respBody = '';
            let statusCode = 200;
            if (httpExpect.returnsFile && payloads.has(httpExpect.returnsFile)) {
                const payload = payloads.get(httpExpect.returnsFile);
                if (typeof payload.parsed === 'object' && payload.parsed !== null && 'status' in payload.parsed) {
                    statusCode = payload.parsed.status;
                    const { status, ...rest } = payload.parsed;
                    respBody = JSON.stringify(rest);
                }
                else {
                    respBody = JSON.stringify(payload.parsed);
                }
            }
            const httpSpec = {
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
        }
        else if (expect.channel === 'EVENT') {
            const eventExpect = expect;
            let message = {};
            if (eventExpect.withFile && payloads.has(eventExpect.withFile)) {
                const payload = payloads.get(eventExpect.withFile);
                message = payload.parsed;
            }
            const eventSpec = {
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
    return mocks;
}
function compile(spec, options) {
    const { outDir, baseDir } = options;
    const fileRefs = new Set();
    if (spec.receive.withFile) {
        fileRefs.add(spec.receive.withFile);
    }
    for (const expect of spec.expects) {
        const e = expect;
        if (e.withFile) {
            fileRefs.add(e.withFile);
        }
        if (e.returnsFile) {
            fileRefs.add(e.returnsFile);
        }
    }
    if (spec.respond.withFile) {
        fileRefs.add(spec.respond.withFile);
    }
    const payloads = new Map();
    for (const file of fileRefs) {
        payloads.set(file, loadPayload(baseDir, file));
    }
    const ktest = buildKTest(spec, payloads);
    const kmocks = buildKMocks(spec, payloads);
    fs.mkdirSync(path.join(outDir, 'tests'), { recursive: true });
    const ktestYaml = yaml.dump(ktest, { lineWidth: -1, noRefs: true });
    fs.writeFileSync(path.join(outDir, 'tests', spec.name + '.yaml'), ktestYaml);
    if (kmocks.length > 0) {
        const mockDocs = kmocks.map((mock) => yaml.dump(mock, { lineWidth: -1, noRefs: true }));
        const mocksContent = mockDocs.map((d) => '---\n' + d).join('');
        fs.writeFileSync(path.join(outDir, 'mocks.yaml'), mocksContent);
    }
}
