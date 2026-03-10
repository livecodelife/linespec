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
)

// VerifyRule defines a SQL verification rule
type VerifyRule struct {
	Type    string // CONTAINS, NOT_CONTAINS, MATCHES
	Pattern string
}

// ExpectStatement defines an external dependency expectation
type ExpectStatement struct {
	Channel       ExpectChannel
	Method        string // For HTTP
	URL           string // For HTTP
	Table         string // For DB
	Topic         string // For Kafka
	SQL           string // For DB (USING_SQL)
	WithFile      string // For Request Payload
	ReturnsFile   string // For Response Payload
	ReturnsEmpty  bool   // For DB (RETURNS EMPTY)
	NoTransaction bool   // For WRITE:MYSQL
	Verify        []VerifyRule
	Negative      bool              // If true, this should NOT be called
	BaseDir       string            // To resolve payload files
	Headers       map[string]string // For HTTP header matching
}

// ReceiveStatement defines the trigger request
type ReceiveStatement struct {
	Channel  ExpectChannel
	Method   string
	Path     string
	WithFile string
	Headers  map[string]string
}

// RespondStatement defines the mock response for the trigger
type RespondStatement struct {
	StatusCode int
	WithFile   string
	Noise      []string
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
}

// Mock is the common interface for protocol-specific mocks
type Mock interface {
	GetChannel() ExpectChannel
	GetName() string
	GetVerifyRules() []VerifyRule
}
