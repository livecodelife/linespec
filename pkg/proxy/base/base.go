// Package base provides common infrastructure for protocol proxies
package base

import (
	"net"
)

// Message represents a generic protocol message
type Message struct {
	Type    byte
	Payload []byte
}

// QueryInfo holds information about a SQL query
type QueryInfo struct {
	SQL       string
	TableName string
	IsWrite   bool
}

// ProtocolHandler defines the interface for database protocol handlers
// Implementations must handle the wire protocol specifics for each database type
type ProtocolHandler interface {
	// Name returns the name of this protocol handler (e.g., "mysql", "postgresql")
	Name() string

	// HandleConnection handles the main proxying loop for a connection pair
	// This is the primary method that implementations should override
	HandleConnection(clientConn, upstreamConn net.Conn)

	// HandleClientStartup performs the initial handshake with a client connection
	// Returns connection parameters and any error
	HandleClientStartup(clientConn net.Conn) (map[string]string, error)

	// HandleUpstreamStartup performs the initial handshake with the upstream server
	// Returns any error
	HandleUpstreamStartup(upstreamConn net.Conn) error

	// ReadClientMessage reads a message from the client
	// Returns nil when connection is closed
	ReadClientMessage(clientConn net.Conn) (*Message, error)

	// ReadUpstreamMessage reads a message from the upstream server
	ReadUpstreamMessage(upstreamConn net.Conn) (*Message, error)

	// WriteClientMessage writes a message to the client
	WriteClientMessage(clientConn net.Conn, msg *Message) error

	// WriteUpstreamMessage writes a message to the upstream server
	WriteUpstreamMessage(upstreamConn net.Conn, msg *Message) error

	// ExtractQuery extracts SQL and metadata from a message
	// Returns nil if message is not a query
	ExtractQuery(msg *Message) *QueryInfo

	// IsExtendedProtocolMessage returns true if message is part of extended query protocol
	// (e.g., Parse, Bind, Execute, Describe in PostgreSQL)
	IsExtendedProtocolMessage(msg *Message) bool

	// HandleExtendedProtocol handles extended protocol message
	// Returns true if message was handled, false to forward to upstream
	HandleExtendedProtocol(msg *Message, upstreamConn, clientConn net.Conn) (bool, error)

	// CreateMockQueryResponse creates a mock response for a simple query
	CreateMockQueryResponse(query *QueryInfo, columns []string, rows []map[string]interface{}) ([]*Message, error)

	// CreateMockExtendedResponse creates mock responses for extended protocol
	// (e.g., ParseComplete, BindComplete, RowDescription, DataRow, CommandComplete)
	CreateMockExtendedResponse(query *QueryInfo, columns []string, rows []map[string]interface{}) ([]*Message, error)

	// CreateEmptyResultSet creates an empty result set response
	CreateEmptyResultSet(columns []string) ([]*Message, error)

	// CreateOKResponse creates a success response for write operations
	CreateOKResponse(query *QueryInfo) ([]*Message, error)

	// CreateErrorResponse creates an error response message
	CreateErrorResponse(errorMessage string) ([]*Message, error)

	// IsWhitelisted checks if a query should bypass mocking
	IsWhitelisted(query *QueryInfo) bool
}

// ProxyServer is a generic protocol proxy server
type ProxyServer struct {
}

