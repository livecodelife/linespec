import * as net from 'net';
import { KMock } from './types';
export declare function startProxy(mocks: KMock[], upstreamHost: string, upstreamPort: number, listenPort: number): Promise<net.Server>;
