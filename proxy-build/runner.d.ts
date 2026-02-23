import type { LoadedTestSet } from './types';
export interface RunnerOptions {
    composePath?: string;
    serviceUrl: string;
    dbPort?: number;
    reportDir?: string;
}
export declare function runTests(testSet: LoadedTestSet, options: RunnerOptions): Promise<void>;
