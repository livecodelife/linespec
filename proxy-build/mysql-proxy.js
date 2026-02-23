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
exports.encodeOkPayload = encodeOkPayload;
exports.encodeColumnDefPayload = encodeColumnDefPayload;
exports.encodeTextRowPayload = encodeTextRowPayload;
exports.serializeResponses = serializeResponses;
exports.startProxy = startProxy;
const net = __importStar(require("net"));
const http = __importStar(require("http"));
function readPackets(stateBuf) {
    const packets = [];
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
function writePacket(seqId, payload) {
    const header = Buffer.alloc(4);
    header[0] = payload.length & 0xff;
    header[1] = (payload.length >> 8) & 0xff;
    header[2] = (payload.length >> 16) & 0xff;
    header[3] = seqId;
    return Buffer.concat([header, payload]);
}
function encodeLenenc(n) {
    if (n <= 250) {
        return Buffer.from([n]);
    }
    else if (n <= 0xffff) {
        const buf = Buffer.alloc(3);
        buf[0] = 0xfc;
        buf[1] = n & 0xff;
        buf[2] = (n >> 8) & 0xff;
        return buf;
    }
    else if (n <= 0xffffff) {
        const buf = Buffer.alloc(4);
        buf[0] = 0xfd;
        buf[1] = n & 0xff;
        buf[2] = (n >> 8) & 0xff;
        buf[3] = (n >> 16) & 0xff;
        return buf;
    }
    else {
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
function encodeLenencStr(s) {
    const strBuf = Buffer.from(s, 'utf8');
    return Buffer.concat([encodeLenenc(strBuf.length), strBuf]);
}
function decodeLenenc(buf, offset) {
    if (offset >= buf.length) {
        return { value: 0, newOffset: offset };
    }
    const firstByte = buf[offset];
    if (firstByte <= 250) {
        return { value: firstByte, newOffset: offset + 1 };
    }
    else if (firstByte === 0xfc) {
        if (offset + 3 > buf.length) {
            return { value: 0, newOffset: offset };
        }
        const value = buf[offset + 1] | (buf[offset + 2] << 8);
        return { value, newOffset: offset + 3 };
    }
    else if (firstByte === 0xfd) {
        if (offset + 4 > buf.length) {
            return { value: 0, newOffset: offset };
        }
        const value = buf[offset + 1] | (buf[offset + 2] << 8) | (buf[offset + 3] << 16);
        return { value, newOffset: offset + 4 };
    }
    else if (firstByte === 0xfe) {
        if (offset + 9 > buf.length) {
            return { value: 0, newOffset: offset };
        }
        const value = buf[offset + 1] |
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
function encodeOkPayload(msg) {
    const buffers = [Buffer.from([0x00])];
    const affectedRows = msg.affected_rows ?? msg.affectedRows ?? 0;
    buffers.push(encodeLenenc(affectedRows));
    const lastInsertId = msg.last_insert_id ?? msg.lastInsertId ?? 0;
    buffers.push(encodeLenenc(lastInsertId));
    const statusFlags = msg.status_flags ?? msg.statusFlags ?? 2;
    buffers.push(Buffer.alloc(2));
    buffers[buffers.length - 1][0] = statusFlags & 0xff;
    buffers[buffers.length - 1][1] = (statusFlags >> 8) & 0xff;
    buffers.push(Buffer.alloc(2));
    const warningCount = msg.warnings ?? msg.warnings ?? 0;
    if (warningCount !== 0) {
        buffers[buffers.length - 1][0] = warningCount & 0xff;
        buffers[buffers.length - 1][1] = (warningCount >> 8) & 0xff;
    }
    if ('info' in msg && msg.info !== undefined) {
        const infoStr = String(msg.info ?? '');
        if (infoStr) {
            buffers.push(encodeLenencStr(infoStr));
        }
    }
    return Buffer.concat(buffers);
}
function encodeColumnDefPayload(col) {
    const buffers = [];
    buffers.push(encodeLenencStr(col.catalog ?? 'def'));
    buffers.push(encodeLenencStr(col.schema ?? ''));
    buffers.push(encodeLenencStr(col.table ?? ''));
    buffers.push(encodeLenencStr(col.org_table ?? col.orgTable ?? ''));
    buffers.push(encodeLenencStr(col.name ?? ''));
    buffers.push(encodeLenencStr(col.org_name ?? col.orgName ?? ''));
    buffers.push(Buffer.from([0x0c]));
    const charsetBuf = Buffer.alloc(2);
    const charset = col.character_set ?? col.characterSet ?? 33;
    charsetBuf.writeUInt16LE(charset, 0);
    buffers.push(charsetBuf);
    const colLenBuf = Buffer.alloc(4);
    const columnLength = col.column_length ?? col.columnLength ?? 0;
    colLenBuf.writeUInt32LE(columnLength, 0);
    buffers.push(colLenBuf);
    const columnType = col.type ?? 0x00;
    buffers.push(Buffer.from([columnType]));
    buffers.push(Buffer.alloc(2));
    const columnFlags = col.flags ?? col.columnFlags;
    if (columnFlags !== undefined) {
        buffers[buffers.length - 1][0] = columnFlags & 0xff;
        buffers[buffers.length - 1][1] = (columnFlags >> 8) & 0xff;
    }
    buffers.push(Buffer.from([0x00]));
    buffers.push(Buffer.from([0x00]));
    return Buffer.concat(buffers);
}
function encodeTextRowPayload(row) {
    const buffers = [];
    if ('values' in row && Array.isArray(row.values)) {
        const values = row.values;
        for (const entry of values) {
            if (entry.value === null) {
                buffers.push(Buffer.from([0xfb]));
            }
            else {
                buffers.push(encodeLenencStr(String(entry.value)));
            }
        }
    }
    else {
        for (const val of Object.values(row)) {
            if (val === null) {
                buffers.push(Buffer.from([0xfb]));
            }
            else {
                buffers.push(encodeLenencStr(String(val)));
            }
        }
    }
    return Buffer.concat(buffers);
}
function serializeResponses(responses, startSequenceId) {
    const buffers = [];
    let seqId = startSequenceId;
    for (const resp of responses) {
        const packetType = resp.header.packet_type;
        const useSequenceId = resp.header.header.sequence_id ?? seqId;
        let payload;
        if (packetType === 'OK') {
            const msg = resp.message;
            payload = encodeOkPayload(msg);
            buffers.push(writePacket(useSequenceId, payload));
            seqId = useSequenceId + 1;
            continue;
        }
        else if (packetType === 'TextResultSet') {
            const msg = resp.message;
            const columnCount = msg.columnCount ?? msg.column_count ?? 0;
            payload = encodeLenenc(columnCount);
            buffers.push(writePacket(useSequenceId, payload));
            seqId = useSequenceId + 1;
            if (msg.columns && Array.isArray(msg.columns)) {
                for (const col of msg.columns) {
                    const colPayload = encodeColumnDefPayload(col);
                    buffers.push(writePacket(seqId++, colPayload));
                }
            }
            const eofAfterColumns = msg.eofAfterColumns ?? msg.eof_after_columns;
            if (eofAfterColumns) {
                let eofBuf;
                if (Array.isArray(eofAfterColumns)) {
                    eofBuf = Buffer.from(eofAfterColumns);
                }
                else {
                    eofBuf = Buffer.from(eofAfterColumns.split(',').map(b => parseInt(b, 16)));
                }
                buffers.push(writePacket(seqId++, eofBuf));
            }
            if (msg.rows && Array.isArray(msg.rows)) {
                for (const row of msg.rows) {
                    const rowPayload = encodeTextRowPayload(row);
                    buffers.push(writePacket(seqId++, rowPayload));
                }
            }
            let finalData = undefined;
            if (msg.FinalResponse) {
                const finalResp = msg.FinalResponse;
                finalData = finalResp.data;
            }
            if (finalData === undefined && msg.data !== undefined) {
                finalData = msg.data;
            }
            if (finalData !== undefined) {
                let dataBuf;
                if (Array.isArray(finalData)) {
                    dataBuf = Buffer.from(finalData);
                }
                else if (typeof finalData === 'string' && finalData.length > 0) {
                    dataBuf = Buffer.from(finalData.split(',').map(b => parseInt(b, 16)));
                }
                else {
                    continue;
                }
                buffers.push(writePacket(seqId++, dataBuf));
            }
            continue;
        }
        else if (packetType === 'ERR') {
            const msg = resp.message;
            const errBuf = Buffer.alloc(9);
            errBuf[0] = 0xff;
            const errCode = msg.errorCode ?? msg.error_code ?? 1;
            errBuf[1] = errCode & 0xff;
            errBuf[2] = (errCode >> 8) & 0xff;
            errBuf.write('#', 3);
            const sqlState = msg.sqlState ?? msg.sql_state ?? 'HY000';
            const sqlStateBuf = Buffer.from(sqlState, 'utf8');
            sqlStateBuf.copy(errBuf, 4);
            const errorMessage = msg.errorMessage ?? msg.error_message ?? 'Unknown error';
            const errorBuf = Buffer.from(errorMessage, 'utf8');
            errorBuf.copy(errBuf, 9);
            payload = errBuf;
        }
        else {
            if (typeof resp.message === 'string') {
                const parts = resp.message.split(',').map(b => parseInt(b, 16));
                payload = Buffer.from(parts);
            }
            else {
                payload = Buffer.from(JSON.stringify(resp.message), 'utf8');
            }
        }
        buffers.push(writePacket(useSequenceId, payload));
        seqId = useSequenceId + 1;
    }
    return Buffer.concat(buffers);
}
function buildMockQueue(mocks) {
    const queue = [];
    for (const mock of mocks) {
        if (mock.kind === 'MySQL') {
            const spec = mock.spec;
            if (spec.metadata && spec.metadata.type === 'mocks') {
                spec.name = mock.name;
                queue.push(spec);
            }
        }
    }
    queue.sort((a, b) => a.created - b.created);
    return queue;
}
function findAndConsumeMock(queue, requestOperation, query, consumed, unexpected, testName) {
    for (let i = 0; i < queue.length; i++) {
        const spec = queue[i];
        if (spec.metadata.requestOperation !== requestOperation) {
            continue;
        }
        const mockTests = spec.metadata.tests;
        if (mockTests !== undefined && Array.isArray(mockTests) && mockTests.length > 0) {
            if (testName === undefined || !mockTests.includes(testName)) {
                continue;
            }
        }
        if (requestOperation === 'COM_PING') {
            queue.splice(i, 1);
            if (consumed && spec.name) {
                consumed.push(spec.name);
            }
            return spec;
        }
        if (requestOperation === 'COM_QUERY' && query) {
            for (const req of spec.requests) {
                const msg = req.message;
                if (typeof msg === 'object' && msg !== null && 'query' in msg) {
                    const mockQuery = msg.query;
                    if (query.includes(mockQuery) || mockQuery.includes(query)) {
                        queue.splice(i, 1);
                        if (consumed && spec.name) {
                            consumed.push(spec.name);
                        }
                        return spec;
                    }
                }
            }
        }
    }
    return null;
}
function isResponseComplete(payload, passthroughState) {
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
function handleConnection(clientSocket, upstreamHost, upstreamPort, queue, consumed, unexpected, testName) {
    const upstreamSocket = net.createConnection(upstreamPort, upstreamHost);
    upstreamSocket.on('connect', () => {
        console.error(`[mysql-proxy] Connected to upstream ${upstreamHost}:${upstreamPort}`);
    });
    const connState = {
        phase: 'handshake',
        clientBuf: Buffer.alloc(0),
        serverBuf: Buffer.alloc(0),
    };
    const passthroughState = {
        active: false,
        responsePhase: 'first',
        columnsRemaining: 0,
        expectEof: false,
    };
    upstreamSocket.on('error', () => clientSocket.destroy());
    clientSocket.on('error', () => upstreamSocket.destroy());
    upstreamSocket.on('data', (data) => {
        if (connState.phase === 'handshake') {
            clientSocket.write(data);
            connState.serverBuf = Buffer.concat([connState.serverBuf, data]);
            const { packets, remainder } = readPackets(connState.serverBuf);
            connState.serverBuf = remainder;
            for (const packet of packets) {
                if (packet.seqId >= 6 && packet.payload[0] === 0x00) {
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
        }
        else {
            clientSocket.write(data);
        }
    });
    clientSocket.on('data', (data) => {
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
                const mock = findAndConsumeMock(queue, 'COM_PING', undefined, consumed, unexpected, testName);
                if (mock) {
                    const response = serializeResponses(mock.responses, packet.seqId + 1);
                    clientSocket.write(response);
                }
                else {
                    passthroughState.active = true;
                    passthroughState.responsePhase = 'first';
                    passthroughState.columnsRemaining = 0;
                    passthroughState.expectEof = false;
                    const fullPacket = writePacket(packet.seqId, packet.payload);
                    upstreamSocket.write(fullPacket);
                    unexpected.push('COM_PING');
                    console.warn('⚠ unexpected query passed through: COM_PING');
                }
                continue;
            }
            if (commandByte === 0x03) {
                const query = packet.payload.slice(1).toString('utf8');
                console.error(`[DEBUG] Received query: ${query}`);
                const mock = findAndConsumeMock(queue, 'COM_QUERY', query, consumed, unexpected, testName);
                if (mock) {
                    console.error(`[DEBUG] Using mock: ${mock.name}`);
                    console.error(`[DEBUG] Mock has ${mock.responses.length} response(s)`);
                    for (let i = 0; i < mock.responses.length; i++) {
                        console.error(`[DEBUG] Response ${i}: ${JSON.stringify(mock.responses[i]).substring(0, 200)}`);
                    }
                    const response = serializeResponses(mock.responses, packet.seqId + 1);
                    console.error(`[DEBUG] Response buffer length: ${response.length}, full hex: ${response.toString('hex').substring(0, 200)}`);
                    clientSocket.write(response);
                }
                else {
                    console.error(`[DEBUG] No mock found, passing through to upstream`);
                    passthroughState.active = true;
                    passthroughState.responsePhase = 'first';
                    passthroughState.columnsRemaining = 0;
                    const fullPacket = writePacket(packet.seqId, packet.payload);
                    upstreamSocket.write(fullPacket);
                    unexpected.push(query);
                    console.warn(`⚠ unexpected query passed through: ${query}`);
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
function startProxy(mocks, upstreamHost, upstreamPort, listenPort) {
    return new Promise((resolve, reject) => {
        const queue = buildMockQueue(mocks);
        const initialQueue = [...queue];
        const consumed = [];
        const unexpected = [];
        let currentTestName;
        const state = {
            reset: () => {
                consumed.length = 0;
                unexpected.length = 0;
            },
            getConsumed: () => [...consumed],
            getUnexpected: () => [...unexpected],
            setTestName: (name) => {
                currentTestName = name;
            },
            getTestName: () => currentTestName,
            refreshQueue: () => {
                queue.length = 0;
                queue.push(...initialQueue);
            },
        };
        const server = net.createServer((clientSocket) => {
            handleConnection(clientSocket, upstreamHost, upstreamPort, queue, consumed, unexpected, currentTestName);
        });
        const mgmtServer = http.createServer((req, res) => {
            res.setHeader('Content-Type', 'application/json');
            if (req.url === '/reset' && req.method === 'POST') {
                state.reset();
                res.statusCode = 200;
                res.end(JSON.stringify({ status: 'ok' }));
            }
            else if (req.url === '/consumed' && req.method === 'GET') {
                res.statusCode = 200;
                res.end(JSON.stringify(state.getConsumed()));
            }
            else if (req.url === '/unexpected' && req.method === 'GET') {
                res.statusCode = 200;
                res.end(JSON.stringify(state.getUnexpected()));
            }
            else if (req.url?.startsWith('/test/') && req.method === 'POST') {
                const testName = req.url.slice(6);
                state.setTestName(testName || undefined);
                state.refreshQueue();
                res.statusCode = 200;
                res.end(JSON.stringify({ status: 'ok', testName }));
            }
            else {
                res.statusCode = 404;
                res.end(JSON.stringify({ error: 'Not found' }));
            }
        });
        server.on('error', reject);
        mgmtServer.on('error', reject);
        server.listen(listenPort, '0.0.0.0', () => {
            mgmtServer.listen(listenPort + 1, '0.0.0.0', () => {
                resolve({ server, mgmtServer, state });
            });
        });
    });
}
