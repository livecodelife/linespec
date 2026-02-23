import { TestSpec } from './types';
export interface CompileOptions {
    outDir: string;
    baseDir: string;
}
export declare function compile(spec: TestSpec, options: CompileOptions): void;
