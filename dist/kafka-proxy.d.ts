import * as net from 'net';
import { EventEmitter } from 'events';
import { KMockEventSpec } from './types';
export declare const kafkaProxyEvents: EventEmitter<[never]>;
interface KafkaMockWithName {
    name: string;
    spec: KMockEventSpec;
}
export declare const kafkaMockUsage: Map<string, boolean>;
export declare function setKafkaMocks(mocks: KafkaMockWithName[], mockUsage: Map<string, boolean>): void;
export declare function activateKafkaMocksForTest(testName: string, mockUsage: Map<string, boolean>): number;
export declare function startKafkaProxy(mocks: KafkaMockWithName[], listenPort?: number): Promise<net.Server>;
declare global {
    interface Buffer {
        writeInt64BE(value: bigint, offset: number): number;
    }
}
export {};
