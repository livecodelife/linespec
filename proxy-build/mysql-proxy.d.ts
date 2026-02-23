import * as net from 'net';
import * as http from 'http';
import { KMock, KMockMysqlSpec } from './types';
export interface ProxyState {
    reset(): void;
    getConsumed(): string[];
    getUnexpected(): string[];
    setTestName(name: string | undefined): void;
    getTestName(): string | undefined;
    refreshQueue(): void;
}
export declare function encodeOkPayload(msg: Record<string, unknown>): Buffer;
export declare function encodeColumnDefPayload(col: Record<string, unknown>): Buffer;
export declare function encodeTextRowPayload(row: Record<string, unknown>): Buffer;
export declare function serializeResponses(responses: KMockMysqlSpec['responses'], startSequenceId: number): Buffer;
export declare function startProxy(mocks: KMock[], upstreamHost: string, upstreamPort: number, listenPort: number): Promise<{
    server: net.Server;
    mgmtServer: http.Server;
    state: ProxyState;
}>;
