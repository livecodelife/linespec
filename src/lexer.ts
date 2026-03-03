export interface Token {
  type: 'TEST' | 'RECEIVE' | 'EXPECT' | 'EXPECT_NOT' | 'WITH' | 'RETURNS' | 'USING_SQL' | 'RESPOND' | 'NOISE' | 'NO_TRANSACTION' | 'VERIFY' | 'HEADERS';
  value: string;
  line: number;
}

export class LineSpecError extends Error {
  line?: number;

  constructor(message: string, line?: number) {
    super(message);
    this.name = 'LineSpecError';
    this.line = line;
  }
}

export function tokenize(source: string): Token[] {
  const tokens: Token[] = [];
  const lines = source.split('\n');
  let lineNo = 0;
  let sqlStartLine: number | undefined;
  let noiseLines: string[] | undefined;
  let noiseStartLine: number | undefined;
  let headersLines: string[] | undefined;
  let headersStartLine: number | undefined;

  for (let i = 0; i < lines.length; i++) {
    lineNo = i + 1;
    let line = lines[i];

    if (noiseLines !== undefined) {
      if (line.startsWith(' ') || line.startsWith('\t')) {
        noiseLines.push(line.trim());
        continue;
      } else {
        tokens.push({ type: 'NOISE', value: noiseLines.join('\n'), line: noiseStartLine! });
        noiseLines = undefined;
        noiseStartLine = undefined;
        i--;
        continue;
      }
    }

    if (headersLines !== undefined) {
      if (line.startsWith(' ') || line.startsWith('\t')) {
        headersLines.push(line.trim());
        continue;
      } else {
        tokens.push({ type: 'HEADERS', value: headersLines.join('\n'), line: headersStartLine! });
        headersLines = undefined;
        headersStartLine = undefined;
        i--;
        continue;
      }
    }

    if (sqlStartLine !== undefined) {
      if (line.trim() === '"""') {
        const collectedLines = lines.slice(sqlStartLine, i);
        const sqlValue = collectedLines.join('\n').trim();
        tokens.push({ type: 'USING_SQL', value: sqlValue, line: sqlStartLine });
        sqlStartLine = undefined;
        continue;
      }
      continue;
    }

    line = line.trim();
    if (line === '' || line.startsWith('#')) {
      continue;
    }

    let match: RegExpMatchArray | null;

    if ((match = line.match(/^TEST\s+(.+)$/))) {
      tokens.push({ type: 'TEST', value: match[1].trim(), line: lineNo });
    } else if ((match = line.match(/^RECEIVE\s+(.+)$/))) {
      tokens.push({ type: 'RECEIVE', value: match[1].trim(), line: lineNo });
    } else if ((match = line.match(/^EXPECT\s+NOT\s+(.+)$/))) {
      tokens.push({ type: 'EXPECT_NOT', value: match[1].trim(), line: lineNo });
    } else if ((match = line.match(/^EXPECT\s+(.+)$/))) {
      tokens.push({ type: 'EXPECT', value: match[1].trim(), line: lineNo });
    } else if ((match = line.match(/^WITH\s+\{\{(.+?)\}\}$/))) {
      tokens.push({ type: 'WITH', value: match[1], line: lineNo });
    } else if ((match = line.match(/^RETURNS\s+\{\{(.+?)\}\}$/))) {
      tokens.push({ type: 'RETURNS', value: match[1], line: lineNo });
    } else if ((match = line.match(/^RETURNS\s+EMPTY$/))) {
      tokens.push({ type: 'RETURNS', value: 'EMPTY', line: lineNo });
    } else if ((match = line.match(/^USING_SQL\s+"""$/))) {
      sqlStartLine = i + 1;
    } else if ((match = line.match(/^RESPOND\s+(.+)$/))) {
      tokens.push({ type: 'RESPOND', value: match[1].trim(), line: lineNo });
    } else if ((match = line.match(/^NO\s+TRANSACTION$/))) {
      tokens.push({ type: 'NO_TRANSACTION', value: 'NO_TRANSACTION', line: lineNo });
    } else if ((match = line.match(/^VERIFY\s+(.+)$/))) {
      tokens.push({ type: 'VERIFY', value: match[1].trim(), line: lineNo });
    } else if (line === 'NOISE') {
      noiseLines = [];
      noiseStartLine = lineNo;
    } else if (line === 'HEADERS') {
      headersLines = [];
      headersStartLine = lineNo;
    } else {
      throw new LineSpecError(`Unrecognized line: ${lines[i]}`, lineNo);
    }
  }

  if (sqlStartLine !== undefined) {
    throw new LineSpecError(`Unclosed USING_SQL block starting at line ${sqlStartLine}`, sqlStartLine);
  }

  if (noiseLines !== undefined) {
    tokens.push({ type: 'NOISE', value: noiseLines.join('\n'), line: noiseStartLine! });
  }

  if (headersLines !== undefined) {
    tokens.push({ type: 'HEADERS', value: headersLines.join('\n'), line: headersStartLine! });
  }

  return tokens;
}
