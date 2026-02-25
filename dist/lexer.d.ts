export interface Token {
    type: 'TEST' | 'RECEIVE' | 'EXPECT' | 'WITH' | 'RETURNS' | 'USING_SQL' | 'RESPOND' | 'NOISE' | 'NO_TRANSACTION' | 'VERIFY';
    value: string;
    line: number;
}
export declare class LineSpecError extends Error {
    line?: number;
    constructor(message: string, line?: number);
}
export declare function tokenize(source: string): Token[];
