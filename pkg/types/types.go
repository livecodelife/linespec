package types

import "time"

// ExpectChannel defines the type of expectation
type ExpectChannel string

const (
	HTTP            ExpectChannel = "HTTP"
	ReadMySQL       ExpectChannel = "READ_MYSQL"
	WriteMySQL      ExpectChannel = "WRITE_MYSQL"
	WritePostgreSQL ExpectChannel = "WRITE_POSTGRESQL"
	ReadPostgreSQL  ExpectChannel = "READ_POSTGRESQL"
	Event           ExpectChannel = "EVENT"
	GRPC            ExpectChannel = "GRPC"
	ReadRedis       ExpectChannel = "READ_REDIS"
	WriteRedis      ExpectChannel = "WRITE_REDIS"
	ReadMongoDB     ExpectChannel = "READ_MONGODB"
	WriteMongoDB    ExpectChannel = "WRITE_MONGODB"
	Job             ExpectChannel = "JOB"
)

// VerifyRule defines a verification rule for any intercepted data
// Target syntax:
//   - SQL: "query"
//   - HTTP: "headers.NAME", "body", "url", "path"
//   - Kafka: "key", "value", "headers.NAME"
type VerifyRule struct {
	Type    string // CONTAINS, NOT_CONTAINS, MATCHES
	Target  string // What to verify (query, headers.X, body, url, path, key, value)
	Pattern string
}

// ExpectStatement defines an external dependency expectation
type ExpectStatement struct {
	Channel       ExpectChannel
	Method        string // For HTTP
	URL           string // For HTTP
	Table         string // For DB
	Topic         string // For Kafka
	SQL           string // For DB (USING_SQL) — exact normalized match (deprecated: use ACCESSING_TABLES)
	SQLContains   string // For DB (USING_SQL_CONTAINS) — normalized substring match (deprecated: use ACCESSING_TABLES)
	// Semantic SQL matching (replaces USING_SQL / USING_SQL_CONTAINS)
	AccessingTables    []string          // ACCESSING_TABLES — exact set of tables referenced in the query
	Database           string            // DATABASE — logical database name (the `name:` field from a `databases:` list entry) this EXPECT targets. Disambiguates a table name that exists in more than one proxied database; empty means "match against any database's proxy" (legacy/single-database behavior).
	VerifyOperation    string            // VERIFY_OPERATION — SELECT, INSERT, UPDATE, DELETE
	VerifyWhereColumns []string          // VERIFY_WHERE_COLUMNS — column names that must appear in WHERE clause
	VerifyWhere        map[string]string // VERIFY_WHERE — column-value pairs in WHERE clause (wire-resolved)
	VerifyWrittenValues map[string]string // VERIFY_WRITTEN_VALUES — column-value pairs for INSERT/UPDATE
	CallN              int               // CALL N — sequential ordering tiebreaker (0 = unset)
	WithFile         string // For Request Payload
	ReturnsFile      string // For Response Payload
	ReturnsEmpty     bool   // For DB (RETURNS EMPTY)
	ReturnsError     bool   // For RETURNS ERROR (simulate connection/network failure)
	ReturnsErrorCode string // For RETURNS ERROR <code> (e.g. "cycle_detected")
	ReturnsHTTPStatus int  // For RETURNS HTTP:NNN (return non-200 HTTP status from dependency)
	NoTransaction    bool   // For WRITE:MYSQL
	Verify        []VerifyRule
	Negative        bool              // If true, this should NOT be called
	BaseDir         string            // To resolve payload files
	Headers         map[string]string // For HTTP request header matching
	ResponseHeaders map[string]string // For HTTP response headers (overrides inferred Content-Type)
	Service         string            // For gRPC: fully-qualified service (e.g., "users.UserService")
	RPCMethod       string            // For gRPC: method name (e.g., "GetUser")
	Command         string            // For Redis: command (GET, SET, HGET, etc.)
	RedisKey        string            // For Redis: key pattern
}

// ReceiveStatement defines the trigger request
type ReceiveStatement struct {
	Channel   ExpectChannel
	Method    string
	Path      string
	Topic     string // For Kafka consumer triggers (RECEIVE KAFKA:topic-name)
	WithFile  string
	Headers   map[string]string
	Service   string // For gRPC triggers: fully-qualified service
	RPCMethod string // For gRPC triggers: method name
}

// RespondStatement defines the mock response for the trigger
type RespondStatement struct {
	StatusCode int
	WithFile   string
	Noise      []string
	Headers    map[string]string // Response headers for the trigger response
}

// TestSpec is the full AST for a .linespec file
type TestSpec struct {
	Name       string
	FilePath   string
	BaseDir    string // Directory containing the .linespec and payloads/
	Receive    ReceiveStatement
	Expects    []ExpectStatement
	ExpectsNot []ExpectStatement
	Respond    RespondStatement
	Created    time.Time
	Timeout    time.Duration // 0 means use the service-level default from .linespec.yml
}

// Mock is the common interface for protocol-specific mocks
type Mock interface {
	GetChannel() ExpectChannel
	GetName() string
	GetVerifyRules() []VerifyRule
}
