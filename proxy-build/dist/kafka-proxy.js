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
exports.kafkaMockUsage = exports.kafkaProxyEvents = void 0;
exports.setKafkaMocks = setKafkaMocks;
exports.activateKafkaMocksForTest = activateKafkaMocksForTest;
exports.startKafkaProxy = startKafkaProxy;
const net = __importStar(require("net"));
const events_1 = require("events");
// Global event emitter for Kafka proxy events
exports.kafkaProxyEvents = new events_1.EventEmitter();
let globalKafkaMocks = [];
let allKafkaMockSpecs = [];
let currentKafkaTestName = null;
// Track Kafka mock usage
exports.kafkaMockUsage = new Map();
// Debug logging helper
function logKafka(label, data) {
    if (data) {
        if (Buffer.isBuffer(data)) {
            const hex = data.slice(0, Math.min(data.length, 100)).toString('hex').match(/.{1,2}/g)?.join(' ') || '';
            const truncated = data.length > 100 ? `... (${data.length} bytes total)` : '';
            console.error(`[kafka-proxy] ${label}: ${hex}${truncated}`);
        }
        else {
            console.error(`[kafka-proxy] ${label}: ${data}`);
        }
    }
    else {
        console.error(`[kafka-proxy] ${label}`);
    }
}
function parseRequestHeader(data) {
    if (data.length < 14)
        return null;
    const apiKey = data.readInt16BE(0);
    const apiVersion = data.readInt16BE(2);
    const correlationId = data.readInt32BE(4);
    // Client ID length (string is length-prefixed)
    const clientIdLength = data.readInt16BE(8);
    let clientId = '';
    if (clientIdLength > 0 && clientIdLength < 1000) {
        try {
            clientId = data.slice(10, 10 + clientIdLength).toString('utf8');
        }
        catch {
            clientId = '';
        }
    }
    return { apiKey, apiVersion, correlationId, clientId };
}
function parseProduceRequest(data, headerLength) {
    try {
        let offset = headerLength;
        // Parse the request body according to Kafka protocol
        // Transactional ID (nullable string)
        const transactionalIdLen = data.readInt16BE(offset);
        offset += 2;
        if (transactionalIdLen >= 0) {
            offset += transactionalIdLen;
        }
        // Acks (int16)
        offset += 2;
        // Timeout (int32)
        offset += 4;
        // Topic data array
        const topicCount = data.readInt32BE(offset);
        offset += 4;
        if (topicCount < 1 || topicCount > 100)
            return null;
        const topicData = [];
        let topic = '';
        for (let i = 0; i < topicCount; i++) {
            // Topic name (string)
            const topicNameLen = data.readInt16BE(offset);
            offset += 2;
            topic = data.slice(offset, offset + topicNameLen).toString('utf8');
            offset += topicNameLen;
            // Partition data array
            const partitionCount = data.readInt32BE(offset);
            offset += 4;
            for (let j = 0; j < partitionCount; j++) {
                // Partition index (int32)
                offset += 4;
                // Record set (this is complex - contains compressed data)
                // For simplicity, we'll try to extract raw message data
                const recordSetSize = data.readInt32BE(offset);
                offset += 4;
                if (recordSetSize > 0 && recordSetSize < 10000000) {
                    const recordSet = data.slice(offset, offset + recordSetSize);
                    // Store for later parsing
                    topicData.push({ topic, recordSet });
                    offset += recordSetSize;
                }
            }
        }
        // For now, return the topic found
        if (topic) {
            return {
                topic,
                messages: topicData.map(td => ({
                    key: null,
                    value: td.recordSet,
                    headers: {}
                }))
            };
        }
        return null;
    }
    catch (err) {
        logKafka(`Error parsing produce request: ${err}`);
        return null;
    }
}
// Try to extract JSON from record batch
function extractJsonFromRecord(recordSet) {
    try {
        // Kafka record batch format is complex with compression
        // For simplicity, try to find JSON in the buffer
        const str = recordSet.toString('utf8');
        // Look for JSON objects
        const jsonStart = str.indexOf('{');
        const jsonEnd = str.lastIndexOf('}');
        if (jsonStart >= 0 && jsonEnd > jsonStart) {
            const jsonStr = str.substring(jsonStart, jsonEnd + 1);
            return JSON.parse(jsonStr);
        }
        return null;
    }
    catch {
        return null;
    }
}
// Create a simple OK response for produce request
function createProduceResponse(correlationId) {
    // Response size (int32)
    const responseSize = 14; // Fixed size for simple response
    const buf = Buffer.alloc(4 + responseSize);
    // Response size
    buf.writeInt32BE(responseSize, 0);
    // Response header
    buf.writeInt32BE(correlationId, 4);
    // Produce response body (minimal)
    buf.writeInt32BE(1, 8); // Response count = 1
    buf.writeInt16BE(0, 12); // Error code = 0 (success)
    buf.writeInt64BE(BigInt(0), 14); // Base offset = 0
    return buf;
}
function setKafkaMocks(mocks, mockUsage) {
    globalKafkaMocks = mocks;
    allKafkaMockSpecs = [...mocks];
    // Reset usage tracking
    mockUsage.clear();
    mocks.forEach(m => {
        mockUsage.set(m.name, false);
    });
}
function activateKafkaMocksForTest(testName, mockUsage) {
    currentKafkaTestName = testName;
    // Filter mocks for this test - only include mocks that start with test name
    globalKafkaMocks = allKafkaMockSpecs.filter(m => {
        return m.name.startsWith(`${testName}-mock-`);
    });
    // Reset usage tracking for this test
    mockUsage.clear();
    let count = 0;
    globalKafkaMocks.forEach(m => {
        mockUsage.set(m.name, false);
        count++;
    });
    return count;
}
async function startKafkaProxy(mocks, listenPort = 9092) {
    // Initialize mock tracking using the global map
    setKafkaMocks(mocks, exports.kafkaMockUsage);
    const server = net.createServer((clientSocket) => {
        logKafka(`Client connected: ${clientSocket.remoteAddress}:${clientSocket.remotePort}`);
        let clientBuffer = Buffer.alloc(0);
        clientSocket.on('data', (data) => {
            clientBuffer = Buffer.concat([clientBuffer, data]);
            // Parse Kafka request
            // Request format: [size (int32)] [request data]
            while (clientBuffer.length >= 4) {
                const requestSize = clientBuffer.readInt32BE(0);
                if (clientBuffer.length < 4 + requestSize) {
                    break; // Wait for more data
                }
                const requestData = clientBuffer.slice(4, 4 + requestSize);
                clientBuffer = clientBuffer.slice(4 + requestSize);
                // Parse request header
                const header = parseRequestHeader(requestData);
                if (!header) {
                    logKafka('Failed to parse request header');
                    continue;
                }
                logKafka(`Request: apiKey=${header.apiKey}, apiVersion=${header.apiVersion}, correlationId=${header.correlationId}`);
                // Handle Produce request (apiKey = 0)
                if (header.apiKey === 0) {
                    const produce = parseProduceRequest(requestData, 10 + (header.clientId ? header.clientId.length + 2 : 0));
                    if (produce) {
                        logKafka(`Produce to topic: ${produce.topic}`);
                        // Try to extract message data
                        let messageData = {};
                        if (produce.messages.length > 0 && produce.messages[0].value) {
                            const json = extractJsonFromRecord(produce.messages[0].value);
                            if (json) {
                                messageData = json;
                                logKafka(`Message: ${JSON.stringify(messageData).substring(0, 200)}`);
                            }
                        }
                        // Match against mocks
                        let matched = false;
                        for (const mock of globalKafkaMocks) {
                            // Check if topic matches
                            if (mock.spec.metadata?.topic === produce.topic) {
                                // Check if message content matches (simplified)
                                const mockMessage = mock.spec.message;
                                const eventTypeMatch = !mockMessage.event_type ||
                                    messageData.event_type === mockMessage.event_type;
                                if (eventTypeMatch) {
                                    exports.kafkaMockUsage.set(mock.name, true);
                                    matched = true;
                                    logKafka(`Matched mock: ${mock.name}`);
                                    // Emit event for tracking
                                    exports.kafkaProxyEvents.emit('produce', {
                                        topic: produce.topic,
                                        message: messageData,
                                        mockName: mock.name,
                                        timestamp: Date.now()
                                    });
                                    break;
                                }
                            }
                        }
                        if (!matched) {
                            logKafka(`No mock matched for topic: ${produce.topic}`);
                        }
                        // Send success response
                        const response = createProduceResponse(header.correlationId);
                        clientSocket.write(response);
                    }
                }
                else if (header.apiKey === 18) {
                    // ApiVersions request - send supported versions
                    const response = createApiVersionsResponse(header.correlationId);
                    clientSocket.write(response);
                }
                else if (header.apiKey === 1) {
                    // Fetch request - return empty
                    const response = createFetchResponse(header.correlationId);
                    clientSocket.write(response);
                }
                else if (header.apiKey === 3) {
                    // Metadata request
                    const response = createMetadataResponse(header.correlationId);
                    clientSocket.write(response);
                }
                else {
                    // Unknown request - just acknowledge
                    const response = createEmptyResponse(header.correlationId);
                    clientSocket.write(response);
                }
            }
        });
        clientSocket.on('error', (err) => {
            logKafka(`Client error: ${err.message}`);
        });
        clientSocket.on('close', () => {
            logKafka('Client disconnected');
        });
    });
    return new Promise((resolve, reject) => {
        server.listen(listenPort, '0.0.0.0', () => {
            logKafka(`Kafka proxy listening on port ${listenPort}`);
            resolve(server);
        });
        server.on('error', reject);
    });
}
// Create ApiVersions response (apiKey = 18)
function createApiVersionsResponse(correlationId) {
    const buf = Buffer.alloc(256);
    let offset = 0;
    // Response size placeholder
    offset += 4;
    // Correlation ID
    buf.writeInt32BE(correlationId, offset);
    offset += 4;
    // Error code (0 = success)
    buf.writeInt16BE(0, offset);
    offset += 2;
    // API versions array length (4 APIs)
    buf.writeInt32BE(4, offset);
    offset += 4;
    // ApiVersions for Produce (0)
    buf.writeInt16BE(0, offset);
    offset += 2; // api_key
    buf.writeInt16BE(0, offset);
    offset += 2; // min_version
    buf.writeInt16BE(9, offset);
    offset += 2; // max_version
    // ApiVersions for Fetch (1)
    buf.writeInt16BE(1, offset);
    offset += 2;
    buf.writeInt16BE(0, offset);
    offset += 2;
    buf.writeInt16BE(12, offset);
    offset += 2;
    // ApiVersions for Metadata (3)
    buf.writeInt16BE(3, offset);
    offset += 2;
    buf.writeInt16BE(0, offset);
    offset += 2;
    buf.writeInt16BE(9, offset);
    offset += 2;
    // ApiVersions for ApiVersions (18)
    buf.writeInt16BE(18, offset);
    offset += 2;
    buf.writeInt16BE(0, offset);
    offset += 2;
    buf.writeInt16BE(3, offset);
    offset += 2;
    // Throttle time
    buf.writeInt32BE(0, offset);
    offset += 4;
    // Write response size at the beginning
    buf.writeInt32BE(offset - 4, 0);
    return buf.slice(0, offset);
}
// Create Fetch response (apiKey = 1)
function createFetchResponse(correlationId) {
    const buf = Buffer.alloc(32);
    // Response size
    buf.writeInt32BE(28, 0);
    // Correlation ID
    buf.writeInt32BE(correlationId, 4);
    // Throttle time
    buf.writeInt32BE(0, 8);
    // Response count
    buf.writeInt32BE(0, 12);
    // Session ID
    buf.writeInt32BE(0, 16);
    // Response topics count
    buf.writeInt32BE(0, 20);
    return buf;
}
// Create Metadata response (apiKey = 3)
function createMetadataResponse(correlationId) {
    const buf = Buffer.alloc(64);
    let offset = 0;
    // Response size placeholder
    offset += 4;
    // Correlation ID
    buf.writeInt32BE(correlationId, offset);
    offset += 4;
    // Throttle time
    buf.writeInt32BE(0, offset);
    offset += 4;
    // Brokers array length (1 broker - us)
    buf.writeInt32BE(1, offset);
    offset += 4;
    // Broker ID
    buf.writeInt32BE(1, offset);
    offset += 4;
    // Broker host (string length + "linespec-proxy" for Docker networking)
    const host = 'linespec-proxy';
    buf.writeInt16BE(host.length, offset);
    offset += 2;
    buf.write(host, offset, 'utf8');
    offset += host.length;
    // Broker port
    buf.writeInt32BE(9092, offset);
    offset += 4;
    // Rack (nullable string, -1 = null)
    buf.writeInt16BE(-1, offset);
    offset += 2;
    // Cluster ID (nullable string)
    const clusterId = 'linespec-cluster';
    buf.writeInt16BE(clusterId.length, offset);
    offset += 2;
    buf.write(clusterId, offset, 'utf8');
    offset += clusterId.length;
    // Controller ID
    buf.writeInt32BE(1, offset);
    offset += 4;
    // Topics array length (empty for now)
    buf.writeInt32BE(0, offset);
    offset += 4;
    // Write response size
    buf.writeInt32BE(offset - 4, 0);
    return buf.slice(0, offset);
}
// Create empty response for unknown requests
function createEmptyResponse(correlationId) {
    const buf = Buffer.alloc(8);
    buf.writeInt32BE(4, 0); // Size
    buf.writeInt32BE(correlationId, 4); // Correlation ID
    return buf;
}
Buffer.prototype.writeInt64BE = function (value, offset) {
    const high = Number(value >> BigInt(32));
    const low = Number(value & BigInt(0xFFFFFFFF));
    this.writeInt32BE(high, offset);
    this.writeInt32BE(low, offset + 4);
    return offset + 8;
};
