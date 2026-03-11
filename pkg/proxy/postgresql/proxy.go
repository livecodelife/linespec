package postgresql

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"sync"

	"github.com/calebcowen/linespec/pkg/dsl"
	"github.com/calebcowen/linespec/pkg/registry"
	"github.com/calebcowen/linespec/pkg/types"
)

// Proxy is a PostgreSQL wire protocol proxy with mock capabilities
// Uses transparent pass-through approach - only intercepts specific queries

type Proxy struct {
	addr         string
	upstreamAddr string
	registry     *registry.MockRegistry
	loader       *dsl.PayloadLoader
	startup      *StartupHandler
	result       *ResultHandler
}

// NewProxy creates a new PostgreSQL proxy
func NewProxy(addr, upstreamAddr string, reg *registry.MockRegistry) *Proxy {
	return &Proxy{
		addr:         addr,
		upstreamAddr: upstreamAddr,
		registry:     reg,
		loader:       &dsl.PayloadLoader{},
		startup:      NewStartupHandler(),
		result:       NewResultHandler(),
	}
}

// Start starts the proxy server
func (p *Proxy) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", p.addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", p.addr, err)
	}
	defer ln.Close()

	fmt.Printf("PostgreSQL Proxy listening on %s, upstream: %s\n", p.addr, p.upstreamAddr)

	// Setup context cancellation
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				fmt.Printf("Error accepting connection: %v\n", err)
				continue
			}
		}

		go p.handleConnection(conn)
	}
}

// handleConnection handles a single client connection using transparent pass-through
func (p *Proxy) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	// Wrap in buffered reader for peeking during startup
	clientReader := bufio.NewReader(clientConn)

	// Handle startup phase (SSL + authentication with client)
	_, err := p.startup.HandleStartupWithReader(clientReader, clientConn)
	if err != nil {
		fmt.Printf("PostgreSQL Proxy: Startup error: %v\n", err)
		return
	}

	// Connect to upstream server
	upstreamConn, err := net.Dial("tcp", p.upstreamAddr)
	if err != nil {
		fmt.Printf("PostgreSQL Proxy: Failed to connect to upstream %s: %v\n", p.upstreamAddr, err)
		return
	}
	defer upstreamConn.Close()

	// Perform transparent startup with upstream - just forward startup messages
	if err := p.transparentStartup(clientReader, clientConn, upstreamConn); err != nil {
		fmt.Printf("PostgreSQL Proxy: Transparent startup failed: %v\n", err)
		return
	}

	// Start transparent proxying with selective query interception
	p.proxyTransparently(clientReader, clientConn, upstreamConn)
}

// transparentStartup handles the initial startup by forwarding client messages to upstream
// and upstream responses back to client, completely transparently
func (p *Proxy) transparentStartup(clientReader *bufio.Reader, clientConn, upstreamConn net.Conn) error {
	// Send startup message to upstream
	startupMsg := p.createStartupMessage("notification_user", "notification_service")
	if _, err := upstreamConn.Write(startupMsg); err != nil {
		return fmt.Errorf("error sending startup to upstream: %w", err)
	}

	// Forward all startup responses from upstream back to client
	// until we see ReadyForQuery or ErrorResponse
	for {
		msg, err := ReadRegularMessage(upstreamConn)
		if err != nil {
			return fmt.Errorf("error reading upstream startup response: %w", err)
		}

		// Forward to client
		if err := p.writeMessage(clientConn, msg.Type, msg.Payload); err != nil {
			return fmt.Errorf("error forwarding startup response: %w", err)
		}

		// Check if we're done with startup
		switch msg.Type {
		case MsgReadyForQuery:
			return nil
		case MsgErrorResponse:
			return fmt.Errorf("upstream returned error during startup")
		}
	}
}

// createStartupMessage creates a PostgreSQL startup message
func (p *Proxy) createStartupMessage(user, database string) []byte {
	startupMsg := make([]byte, 0, 100)
	startupMsg = append(startupMsg, 0, 0, 0, 0) // Length placeholder

	// Version 3.0 = 196608 = 0x00030000
	startupMsg = append(startupMsg, 0x00, 0x03, 0x00, 0x00)

	// User parameter
	startupMsg = append(startupMsg, []byte("user")...)
	startupMsg = append(startupMsg, 0)
	startupMsg = append(startupMsg, []byte(user)...)
	startupMsg = append(startupMsg, 0)

	// Database parameter
	startupMsg = append(startupMsg, []byte("database")...)
	startupMsg = append(startupMsg, 0)
	startupMsg = append(startupMsg, []byte(database)...)
	startupMsg = append(startupMsg, 0)
	startupMsg = append(startupMsg, 0) // End of params

	// Update length
	length := len(startupMsg)
	startupMsg[0] = byte(length >> 24)
	startupMsg[1] = byte(length >> 16)
	startupMsg[2] = byte(length >> 8)
	startupMsg[3] = byte(length)

	return startupMsg
}

// proxyTransparently handles bidirectional message forwarding with selective interception
func (p *Proxy) proxyTransparently(clientReader *bufio.Reader, clientConn, upstreamConn net.Conn) {
	// Use a WaitGroup to coordinate the two directions
	var wg sync.WaitGroup
	wg.Add(2)

	// Channel to signal errors or termination
	done := make(chan struct{})

	// Direction 1: Upstream -> Client (always transparent)
	go func() {
		defer wg.Done()
		defer close(done)

		for {
			msg, err := ReadRegularMessage(upstreamConn)
			if err != nil {
				if err != io.EOF {
					fmt.Printf("PostgreSQL Proxy: Error reading from upstream: %v\n", err)
				}
				return
			}

			// Forward to client
			if err := p.writeMessage(clientConn, msg.Type, msg.Payload); err != nil {
				fmt.Printf("PostgreSQL Proxy: Error writing to client: %v\n", err)
				return
			}
		}
	}()

	// Direction 2: Client -> Upstream (selective interception)
	go func() {
		defer wg.Done()

		for {
			select {
			case <-done:
				return
			default:
			}

			msg, err := ReadRegularMessageFromReader(clientReader)
			if err != nil {
				if err != io.EOF {
					fmt.Printf("PostgreSQL Proxy: Error reading from client: %v\n", err)
				}
				return
			}

			// Check if we should intercept this message
			if p.shouldIntercept(msg) {
				if err := p.handleInterceptedMessage(msg, clientReader, clientConn, upstreamConn); err != nil {
					fmt.Printf("PostgreSQL Proxy: Error handling intercepted message: %v\n", err)
					return
				}
			} else {
				// Forward transparently to upstream
				if err := p.writeMessage(upstreamConn, msg.Type, msg.Payload); err != nil {
					fmt.Printf("PostgreSQL Proxy: Error forwarding to upstream: %v\n", err)
					return
				}
			}
		}
	}()

	wg.Wait()
}

// shouldIntercept determines if a message should be intercepted (not forwarded)
func (p *Proxy) shouldIntercept(msg *Message) bool {
	switch msg.Type {
	case MsgQuery:
		// Simple query protocol - check if query should be mocked
		query := string(msg.Payload)
		if p.isWhitelisted(query) {
			return false // Don't intercept whitelisted queries
		}
		tableName := p.extractTable(query)
		_, found := p.registry.FindMock(tableName, query)
		return found // Only intercept if we have a mock for it

	case MsgParse:
		// Extended query protocol - check if the prepared statement should be mocked
		query := p.extractQueryFromParse(msg.Payload)
		if query == "" {
			return false
		}
		if p.isWhitelisted(query) {
			return false
		}
		tableName := p.extractTable(query)
		_, found := p.registry.FindMock(tableName, query)
		return found

	default:
		return false
	}
}

// handleInterceptedMessage handles a message that should be mocked
func (p *Proxy) handleInterceptedMessage(msg *Message, clientReader *bufio.Reader, clientConn, upstreamConn net.Conn) error {
	switch msg.Type {
	case MsgQuery:
		query := string(msg.Payload)
		tableName := p.extractTable(query)
		mock, found := p.registry.FindMock(tableName, query)

		if !found {
			return p.writeMessage(upstreamConn, msg.Type, msg.Payload)
		}

		fmt.Printf("PostgreSQL Proxy: Mocking query for table %s\n", tableName)
		return p.sendMockResponse(clientConn, mock)

	case MsgParse:
		// Extended query protocol: Handle Parse/Bind/Execute/Sync cycle
		query := p.extractQueryFromParse(msg.Payload)
		if query == "" {
			return p.writeMessage(upstreamConn, msg.Type, msg.Payload)
		}

		tableName := p.extractTable(query)
		mock, found := p.registry.FindMock(tableName, query)
		if !found {
			return p.writeMessage(upstreamConn, msg.Type, msg.Payload)
		}

		fmt.Printf("PostgreSQL Proxy: Intercepting extended query for table %s\n", tableName)

		// Send ParseComplete to client
		if err := p.writeMessage(clientConn, MsgParseComplete, nil); err != nil {
			return fmt.Errorf("error sending ParseComplete: %w", err)
		}

		// Read and handle Bind message
		bindMsg, err := ReadRegularMessageFromReader(clientReader)
		if err != nil {
			return fmt.Errorf("error reading Bind: %w", err)
		}
		if bindMsg.Type != MsgBind {
			return fmt.Errorf("expected Bind message, got %c", bindMsg.Type)
		}

		// Send BindComplete to client
		if err := p.writeMessage(clientConn, MsgBindComplete, nil); err != nil {
			return fmt.Errorf("error sending BindComplete: %w", err)
		}

		// Read and handle Execute message
		executeMsg, err := ReadRegularMessageFromReader(clientReader)
		if err != nil {
			return fmt.Errorf("error reading Execute: %w", err)
		}
		if executeMsg.Type != MsgExecute {
			return fmt.Errorf("expected Execute message, got %c", executeMsg.Type)
		}

		// Send mock result set
		if err := p.sendMockResultSetForExtended(clientConn, mock); err != nil {
			return fmt.Errorf("error sending mock result: %w", err)
		}

		// Send CommandComplete
		if err := p.result.SendCommandComplete(clientConn, "SELECT 2"); err != nil {
			return fmt.Errorf("error sending CommandComplete: %w", err)
		}

		// Read and handle Sync message
		syncMsg, err := ReadRegularMessageFromReader(clientReader)
		if err != nil {
			return fmt.Errorf("error reading Sync: %w", err)
		}
		if syncMsg.Type != MsgSync {
			return fmt.Errorf("expected Sync message, got %c", syncMsg.Type)
		}

		// Send ReadyForQuery
		if err := p.writeMessage(clientConn, MsgReadyForQuery, []byte{'I'}); err != nil {
			return fmt.Errorf("error sending ReadyForQuery: %w", err)
		}

		return nil

	default:
		return p.writeMessage(upstreamConn, msg.Type, msg.Payload)
	}
}

// extractQueryFromParse extracts the SQL query from a Parse message payload
func (p *Proxy) extractQueryFromParse(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}

	// Parse message format: [stmt_name]\0 [query]\0 [param_types...]
	parts := strings.Split(string(payload), "\x00")
	if len(parts) >= 2 {
		return parts[1] // The query is the second part
	}
	return ""
}

// writeMessage writes a message to the connection
func (p *Proxy) writeMessage(conn net.Conn, msgType byte, payload []byte) error {
	length := uint32(len(payload) + 4)

	msg := make([]byte, 0, 1+4+len(payload))
	msg = append(msg, msgType)
	msg = append(msg, byte(length>>24), byte(length>>16), byte(length>>8), byte(length))
	msg = append(msg, payload...)

	_, err := conn.Write(msg)
	return err
}

// sendMockResponse sends a mock response to the client
func (p *Proxy) sendMockResponse(conn net.Conn, mock *types.ExpectStatement) error {
	// Determine columns from mock or use defaults
	columns := []string{"id", "name", "email"}
	if mock.Table != "" {
		columns = p.inferColumnsForTable(mock.Table)
	}

	switch mock.Channel {
	case types.ReadPostgreSQL:
		if mock.ReturnsEmpty {
			return p.result.SendEmptyResultSet(conn, columns)
		}

		if mock.ReturnsFile != "" {
			p.loader.BaseDir = mock.BaseDir
			payload, err := p.loader.Load(mock.ReturnsFile)
			if err != nil {
				fmt.Printf("PostgreSQL Proxy: Error loading payload %s: %v\n", mock.ReturnsFile, err)
				return p.result.SendEmptyResultSet(conn, columns)
			}

			rows := p.extractRowsFromPayload(payload)
			if len(rows) > 0 {
				columns = make([]string, 0, len(rows[0]))
				for col := range rows[0] {
					columns = append(columns, col)
				}
			}

			return p.result.SendResultSet(conn, columns, rows)
		}

		return p.result.SendEmptyResultSet(conn, columns)

	case types.WritePostgreSQL:
		// For writes, send CommandComplete with row count
		return p.result.SendCommandComplete(conn, "INSERT 0 1")

	default:
		return p.result.SendEmptyResultSet(conn, columns)
	}
}

// sendMockResultSetForExtended sends a mock result set for extended query protocol
// This includes RowDescription and DataRow messages
func (p *Proxy) sendMockResultSetForExtended(conn net.Conn, mock *types.ExpectStatement) error {
	// Determine columns from mock or use defaults
	columns := []string{"id", "name", "email"}
	if mock.Table != "" {
		columns = p.inferColumnsForTable(mock.Table)
	}

	var rows []map[string]interface{}

	switch mock.Channel {
	case types.ReadPostgreSQL:
		if mock.ReturnsEmpty {
			// For empty results, send NoData message instead of RowDescription
			return p.writeMessage(conn, MsgNoData, nil)
		}

		if mock.ReturnsFile != "" {
			p.loader.BaseDir = mock.BaseDir
			payload, err := p.loader.Load(mock.ReturnsFile)
			if err != nil {
				fmt.Printf("PostgreSQL Proxy: Error loading payload %s: %v\n", mock.ReturnsFile, err)
				return p.writeMessage(conn, MsgNoData, nil)
			}

			rows = p.extractRowsFromPayload(payload)
			if len(rows) > 0 {
				columns = make([]string, 0, len(rows[0]))
				for col := range rows[0] {
					columns = append(columns, col)
				}
			}
		}

	case types.WritePostgreSQL:
		// For writes, no result set needed
		return nil
	}

	// Send RowDescription
	if err := p.result.SendRowDescription(conn, columns); err != nil {
		return fmt.Errorf("error sending RowDescription: %w", err)
	}

	// Send DataRow for each row
	for _, row := range rows {
		if err := p.result.SendDataRow(conn, columns, row); err != nil {
			return fmt.Errorf("error sending DataRow: %w", err)
		}
	}

	return nil
}

// extractRowsFromPayload extracts rows from a payload
func (p *Proxy) extractRowsFromPayload(payload interface{}) []map[string]interface{} {
	var rows []map[string]interface{}

	switch data := payload.(type) {
	case []interface{}:
		for _, item := range data {
			if m, ok := item.(map[string]interface{}); ok {
				rows = append(rows, m)
			}
		}
	case map[string]interface{}:
		if rowsRaw, ok := data["rows"].([]interface{}); ok {
			for _, item := range rowsRaw {
				if m, ok := item.(map[string]interface{}); ok {
					rows = append(rows, m)
				}
			}
		} else {
			rows = append(rows, data)
		}
	}

	return rows
}

// inferColumnsForTable infers column names for a table
func (p *Proxy) inferColumnsForTable(table string) []string {
	switch table {
	case "notifications":
		return []string{"id", "content", "recipient", "created_at", "updated_at"}
	case "users":
		return []string{"id", "name", "email", "created_at", "updated_at"}
	case "todos":
		return []string{"id", "title", "description", "completed", "user_id", "created_at", "updated_at"}
	default:
		return []string{"id", "name", "created_at", "updated_at"}
	}
}

// isWhitelisted checks if a query should bypass mocking
func (p *Proxy) isWhitelisted(query string) bool {
	q := strings.TrimSpace(strings.ToUpper(query))
	prefixes := []string{
		"SET ",
		"SHOW ",
		"CREATE ",
		"ALTER ",
		"DROP ",
		"BEGIN",
		"COMMIT",
		"ROLLBACK",
		"SELECT VERSION()",
		"SELECT CURRENT_",
		"SELECT PG_CATALOG",
		"SELECT TYPOID",
		"SELECT T.TYPNAMESPACE",
	}

	for _, pref := range prefixes {
		if strings.HasPrefix(q, pref) {
			return true
		}
	}

	if strings.Contains(q, "INFORMATION_SCHEMA") || strings.Contains(q, "PG_CATALOG") || strings.Contains(q, "PG_TYPE") {
		return true
	}

	return false
}

// extractTable extracts table name from SQL query
func (p *Proxy) extractTable(query string) string {
	q := strings.ToLower(strings.TrimSpace(query))
	q = strings.ReplaceAll(q, "`", " ")
	q = strings.ReplaceAll(q, "\"", " ")
	q = strings.ReplaceAll(q, "'", " ")

	// Common table names to check
	knownTables := []string{"notifications", "users", "todos"}
	for _, table := range knownTables {
		re := regexp.MustCompile(`\b` + table + `\b`)
		if re.MatchString(q) {
			return table
		}
	}

	// Try to extract from SQL keywords
	words := strings.Fields(q)
	for i, word := range words {
		if word == "from" || word == "into" || word == "update" {
			if i+1 < len(words) {
				table := words[i+1]
				if idx := strings.Index(table, "."); idx != -1 {
					table = table[idx+1:]
				}
				table = strings.Trim(table, "`\"'")
				return table
			}
		}
	}

	return "unknown"
}

// ReadRegularMessageFromReader reads a regular message from a buffered reader
func ReadRegularMessageFromReader(reader *bufio.Reader) (*Message, error) {
	typeBuf := make([]byte, 1)
	if _, err := io.ReadFull(reader, typeBuf); err != nil {
		return nil, err
	}

	lengthBuf := make([]byte, 4)
	if _, err := io.ReadFull(reader, lengthBuf); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lengthBuf)

	payloadLen := length - 4
	if payloadLen > 0 {
		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return nil, err
		}
		return &Message{
			Type:    typeBuf[0],
			Length:  int32(length),
			Payload: payload,
		}, nil
	}

	return &Message{
		Type:    typeBuf[0],
		Length:  int32(length),
		Payload: nil,
	}, nil
}
