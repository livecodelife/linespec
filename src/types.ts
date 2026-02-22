export type ExpectChannel = 'HTTP' | 'READ_MYSQL' | 'WRITE_MYSQL' | 'WRITE_POSTGRESQL' | 'EVENT';

export interface ExpectHttpStatement {
  channel: 'HTTP';
  method: string;
  url: string;
  withFile?: string;
  returnsFile: string;
}

export interface ExpectReadMysqlStatement {
  channel: 'READ_MYSQL';
  table: string;
  sql?: string;
  withFile?: string;
  returnsFile: string;
}

export interface ExpectWriteMysqlStatement {
  channel: 'WRITE_MYSQL';
  table: string;
  sql?: string;
  withFile: string;
  returnsFile: string;
}

export interface ExpectWritePostgresqlStatement {
  channel: 'WRITE_POSTGRESQL';
  table: string;
  sql?: string;
  withFile: string;
  returnsFile: string;
}

export interface ExpectEventStatement {
  channel: 'EVENT';
  topic: string;
  withFile: string;
}

export type ExpectStatement =
  | ExpectHttpStatement
  | ExpectReadMysqlStatement
  | ExpectWriteMysqlStatement
  | ExpectWritePostgresqlStatement
  | ExpectEventStatement;

export interface ReceiveStatement {
  channel: 'HTTP';
  method: string;
  path: string;
  withFile?: string;
}

export interface RespondStatement {
  statusCode: number;
  withFile?: string;
}

export interface TestSpec {
  name: string;
  receive: ReceiveStatement;
  expects: ExpectStatement[];
  respond: RespondStatement;
}

export interface KTestReq {
  method: string;
  proto_major: number;
  proto_minor: number;
  url: string;
  header: Record<string, string>;
  body: string;
  timestamp: string;
}

export interface KTestResp {
  status_code: number;
  header: Record<string, string>;
  body: string;
  status_message: string;
  proto_major: number;
  proto_minor: number;
  timestamp: string;
}

export interface KTestAssertions {
  noise: Record<string, string[]>;
}

export interface KTestSpec {
  metadata: Record<string, unknown>;
  req: KTestReq;
  resp: KTestResp;
  objects: unknown[];
  assertions?: KTestAssertions;
  created: number;
  app_port: number;
}

export interface KTest {
  version: 'api.keploy.io/v1beta1';
  kind: 'Http';
  name: string;
  spec: KTestSpec;
  curl: string;
}

export interface KMockDnsSpec {
  metadata: {
    name: string;
    qtype: string;
  };
  request: {
    name: string;
    qtype: number;
    qclass: number;
  };
  response: {
    answers?: string[];
  };
  reqTimestampMock: string;
  resTimestampMock: string;
}

export interface KMockMysqlSpec {
  metadata: {
    connID: string;
    requestOperation: string;
    responseOperation: string;
    type: string;
  };
  requests: Array<{
    header: {
      header: {
        payload_length: number;
        sequence_id: number;
      };
      packet_type: string;
    };
    message: string | Record<string, unknown>;
  }>;
  responses: Array<{
    header: {
      header: {
        payload_length: number;
        sequence_id: number;
      };
      packet_type: string;
    };
    meta?: Record<string, unknown>;
    message: string | Record<string, unknown>;
  }>;
  created: number;
  reqtimestampmock: string;
  restimestampmock: string;
}

export interface KMockHttpSpec {
  metadata: Record<string, unknown>;
  req: {
    method: string;
    url: string;
    header: Record<string, string>;
    body: string;
  };
  resp: {
    status_code: number;
    header: Record<string, string>;
    body: string;
  };
  reqTimestampMock: string;
  resTimestampMock: string;
}

export interface KMockEventSpec {
  metadata: { topic: string };
  message: Record<string, unknown>;
  reqTimestampMock: string;
  resTimestampMock: string;
}

export type KMockSpec = KMockDnsSpec | KMockMysqlSpec | KMockHttpSpec | KMockEventSpec;

export interface KMock {
  version: 'api.keploy.io/v1beta1';
  kind: 'DNS' | 'MySQL' | 'Http' | 'Kafka';
  name: string;
  spec: KMockSpec;
}

export interface PayloadFile {
  raw: string;
  parsed: Record<string, unknown>;
}

export interface LoadedKTest {
  name: string;
  ktest: KTest;
}

export interface LoadedMock {
  name: string;
  mock: KMock;
}

export interface LoadedTestSet {
  tests: LoadedKTest[];
  mocks: LoadedMock[];
}
