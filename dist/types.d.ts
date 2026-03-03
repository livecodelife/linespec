export type ExpectChannel = 'HTTP' | 'READ_MYSQL' | 'WRITE_MYSQL' | 'WRITE_POSTGRESQL' | 'EVENT';
export type ExpectNotChannel = 'HTTP' | 'WRITE_MYSQL' | 'WRITE_POSTGRESQL' | 'EVENT';
export interface VerifyRule {
    type: 'CONTAINS' | 'NOT_CONTAINS' | 'MATCHES';
    pattern: string;
}
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
    returnsFile?: string;
    returnsEmpty?: boolean;
    verify?: VerifyRule[];
}
export interface ExpectWriteMysqlStatement {
    channel: 'WRITE_MYSQL';
    table: string;
    operation?: 'INSERT' | 'UPDATE' | 'DELETE';
    sql?: string;
    withFile: string;
    returnsFile?: string;
    transactional?: boolean;
    verify?: VerifyRule[];
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
export type ExpectStatement = ExpectHttpStatement | ExpectReadMysqlStatement | ExpectWriteMysqlStatement | ExpectWritePostgresqlStatement | ExpectEventStatement;
export interface ExpectNotHttpStatement {
    channel: 'HTTP';
    method: string;
    url: string;
    withFile?: string;
}
export interface ExpectNotWriteMysqlStatement {
    channel: 'WRITE_MYSQL';
    table: string;
    operation?: 'INSERT' | 'UPDATE' | 'DELETE';
    withFile?: string;
}
export interface ExpectNotWritePostgresqlStatement {
    channel: 'WRITE_POSTGRESQL';
    table: string;
    withFile?: string;
}
export interface ExpectNotEventStatement {
    channel: 'EVENT';
    topic: string;
    withFile?: string;
}
export type ExpectNotStatement = ExpectNotHttpStatement | ExpectNotWriteMysqlStatement | ExpectNotWritePostgresqlStatement | ExpectNotEventStatement;
export interface ReceiveStatement {
    channel: 'HTTP';
    method: string;
    path: string;
    withFile?: string;
    headers?: Record<string, string>;
}
export interface RespondStatement {
    statusCode: number;
    withFile?: string;
    noise?: string[];
}
export interface TestSpec {
    name: string;
    receive: ReceiveStatement;
    expects: ExpectStatement[];
    expectsNot: ExpectNotStatement[];
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
        verify?: VerifyRule[];
        name?: string;
        negative?: boolean;
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
    metadata: {
        topic: string;
        negative?: boolean;
    };
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
    dir: string;
    tests: LoadedKTest[];
    mocks: LoadedMock[];
    mocksByTest: Map<string, LoadedMock[]>;
}
export interface TestResult {
    name: string;
    pass: boolean;
    reason?: string;
    expectedStatus: number;
    actualStatus?: number;
    expectedBody?: string;
    actualBody?: string;
    diff?: string;
    req: KTestReq;
}
