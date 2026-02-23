import { Token, LineSpecError } from './lexer';
import { TestSpec } from './types';
export { LineSpecError };
export declare function parse(tokens: Token[], filename: string): TestSpec;
