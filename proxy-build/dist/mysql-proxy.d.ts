import * as net from 'net';
import { EventEmitter } from 'events';
import { KMock } from './types';
export declare const proxyEvents: EventEmitter<[never]>;
export declare function getLastVerificationError(testName: string): string | undefined;
export declare function clearVerificationErrors(): void;
export declare function setVerificationError(testName: string, error: string): void;
export declare function startProxy(mocks: KMock[], upstreamHost: string, upstreamPort: number, listenPort: number): Promise<net.Server>;
