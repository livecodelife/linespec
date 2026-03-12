package postgresql

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/calebcowen/linespec/pkg/dsl"
	"github.com/calebcowen/linespec/pkg/logger"
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
	debugLog     *os.File
}

// NewProxy creates a new PostgreSQL proxy
func NewProxy(addr, upstreamAddr string, reg *registry.MockRegistry) *Proxy {
	// Open debug log file on mounted volume so it persists after container exits
	debugLog, err := os.OpenFile("/app/project/postgres-proxy-debug.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Printf("❌ Failed to open debug log: %v\n", err)
	} else {
		fmt.Printf("✅ Opened debug log at /app/project/postgres-proxy-debug.log\n")
	}

	return &Proxy{
		addr:         addr,
		upstreamAddr: upstreamAddr,
		registry:     reg,
		loader:       &dsl.PayloadLoader{},
		startup:      NewStartupHandler(),
		result:       NewResultHandler(),
		debugLog:     debugLog,
	}
}

// Start starts the proxy server
func (p *Proxy) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", p.addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", p.addr, err)
	}
	defer ln.Close()

	logger.Debug("PostgreSQL Proxy listening on %s, upstream: %s", p.addr, p.upstreamAddr)

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
				logger.Error("Error accepting connection: %v", err)
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
		logger.Error("PostgreSQL Proxy: Startup error: %v", err)
		return
	}

	// Connect to upstream server
	upstreamConn, err := net.Dial("tcp", p.upstreamAddr)
	if err != nil {
		logger.Error("PostgreSQL Proxy: Failed to connect to upstream %s: %v", p.upstreamAddr, err)
		return
	}
	defer upstreamConn.Close()

	// Perform transparent startup with upstream - just forward startup messages
	if err := p.transparentStartup(clientConn, upstreamConn); err != nil {
		logger.Error("PostgreSQL Proxy: Transparent startup failed: %v", err)
		return
	}

	// Start transparent proxying with selective query interception
	p.proxyTransparently(clientReader, clientConn, upstreamConn)
}

// transparentStartup handles the initial startup by forwarding client messages to upstream
// and upstream responses back to client, completely transparently
func (p *Proxy) transparentStartup(clientConn, upstreamConn net.Conn) error {
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
					logger.Debug("PostgreSQL Proxy: Error reading from upstream: %v", err)
				}
				return
			}

			// Forward to client
			if err := p.writeMessage(clientConn, msg.Type, msg.Payload); err != nil {
				logger.Debug("PostgreSQL Proxy: Error writing to client: %v", err)
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
					logger.Debug("PostgreSQL Proxy: Error reading from client: %v", err)
				}
				return
			}

			// Check if we should intercept this message
			if p.shouldIntercept(msg) {
				if err := p.handleInterceptedMessage(msg, clientReader, clientConn, upstreamConn); err != nil {
					logger.Error("PostgreSQL Proxy: Error handling intercepted message: %v", err)
					return
				}
			} else {
				// Forward transparently to upstream
				if err := p.writeMessage(upstreamConn, msg.Type, msg.Payload); err != nil {
					logger.Debug("PostgreSQL Proxy: Error forwarding to upstream: %v", err)
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
			p.logDebug("Simple query WHITELISTED: %s\n", query[:min(100, len(query))])
			return false // Don't intercept whitelisted queries
		}
		tableName := p.extractTable(query)
		mock, found := p.registry.PeekMock(tableName, query) // Use PeekMock to not increment hits
		p.logDebug("Simple query: table=%s, found=%v, query=%s\n", tableName, found, query[:min(100, len(query))])
		if found && mock != nil {
			p.logDebug("  -> Mock SQL: %s\n", mock.SQL[:min(100, len(mock.SQL))])
		}
		return found // Only intercept if we have a mock for it

	case MsgParse:
		// Extended query protocol - check if the prepared statement should be mocked
		query, _ := p.extractQueryFromParse(msg.Payload)
		if query == "" {
			p.logDebug("Extended query: EMPTY\n")
			return false
		}
		if p.isWhitelisted(query) {
			p.logDebug("Extended query WHITELISTED: %s\n", query[:min(100, len(query))])
			return false
		}
		tableName := p.extractTable(query)
		mock, found := p.registry.PeekMock(tableName, query) // Use PeekMock to not increment hits
		p.logDebug("Extended query: table=%s, found=%v, query=%s\n", tableName, found, query[:min(100, len(query))])
		if found && mock != nil {
			p.logDebug("  -> Mock SQL: %s\n", mock.SQL[:min(100, len(mock.SQL))])
		}
		return found

	default:
		return false
	}
}

// handleInterceptedMessage handles a message that should be mocked
func (p *Proxy) handleInterceptedMessage(msg *Message, clientReader *bufio.Reader, clientConn, upstreamConn net.Conn) error {
	p.logDebug("handleInterceptedMessage: type=%c\n", msg.Type)

	switch msg.Type {
	case MsgQuery:
		query := string(msg.Payload)
		tableName := p.extractTable(query)
		mock, found := p.registry.FindMock(tableName, query)

		if !found {
			p.logDebug("  -> Mock not found, forwarding to upstream\n")
			return p.writeMessage(upstreamConn, msg.Type, msg.Payload)
		}

		// Store the actual query in the mock for proper hit tracking
		if mock.SQL == "" {
			mock.SQL = query
		}

		p.logDebug("  -> Mocking query for table %s\n", tableName)
		return p.sendMockResponse(clientConn, mock)

	case MsgParse:
		// Extended query protocol: Handle Parse/Bind/Execute/Sync cycle
		query, parseParamTypes := p.extractQueryFromParse(msg.Payload)
		p.logDebug("  -> Parse query: %s\n", query[:min(100, len(query))])
		p.logDebug("  -> Parse param types from client: %v\n", parseParamTypes)

		if query == "" {
			p.logDebug("  -> Empty query, forwarding to upstream\n")
			return p.writeMessage(upstreamConn, msg.Type, msg.Payload)
		}

		tableName := p.extractTable(query)
		mock, found := p.registry.FindMock(tableName, query)
		if !found {
			p.logDebug("  -> Mock not found for table %s, forwarding to upstream\n", tableName)
			return p.writeMessage(upstreamConn, msg.Type, msg.Payload)
		}

		p.logDebug("  -> Found mock for table %s, hit count incremented\n", tableName)

		// Store the actual query in the mock for later use (e.g., for RETURNING clause detection)
		if mock.SQL == "" {
			mock.SQL = query
		}

		p.logDebug("  -> Intercepting extended query for table %s\n", tableName)

		// Send ParseComplete to client
		p.logDebug("  -> Sending ParseComplete\n")
		if err := p.writeMessage(clientConn, MsgParseComplete, nil); err != nil {
			return fmt.Errorf("error sending ParseComplete: %w", err)
		}

		// Handle optional messages between Parse and Bind (Describe, etc.)
		for {
			p.logDebug("  -> Reading next message...\n")
			nextMsg, err := ReadRegularMessageFromReader(clientReader)
			if err != nil {
				p.logDebug("  -> Error reading next message: %v\n", err)
				return fmt.Errorf("error reading message after ParseComplete: %w", err)
			}

			switch nextMsg.Type {
			case MsgDescribe:
				p.logDebug("  -> Got Describe message\n")
				// Send appropriate describe response
				if err := p.handleDescribe(clientConn, mock, query); err != nil {
					return fmt.Errorf("error handling describe: %w", err)
				}
			// Continue reading for Bind

			case MsgFlush:
				p.logDebug("  -> Got Flush message, ignoring\n")
				// Flush has no response, just continue reading

			case MsgClose:
				p.logDebug("  -> Got Close message\n")
				// Send CloseComplete
				if err := p.writeMessage(clientConn, MsgCloseComplete, nil); err != nil {
					return fmt.Errorf("error sending CloseComplete: %w", err)
				}
				// Continue reading for Bind

			case MsgBind:
				p.logDebug("  -> Got Bind message\n")
				// Send BindComplete
				p.logDebug("  -> Sending BindComplete\n")
				if err := p.writeMessage(clientConn, MsgBindComplete, nil); err != nil {
					return fmt.Errorf("error sending BindComplete: %w", err)
				}
				// Now handle messages after Bind (more Describe, Execute, etc.)
				goto afterBind

			case MsgQuery:
				p.logDebug("  -> Got Query message, handling as simple query\n")
				// Client switched to simple query protocol
				// Send the mock response and complete
				query := string(nextMsg.Payload)
				if err := p.handleSimpleQuery(clientConn, query, mock); err != nil {
					return err
				}
				// Now wait for Sync (which may come as part of the query cycle)
				goto afterExecute

			default:
				p.logDebug("  -> Unexpected message type %c after ParseComplete\n", nextMsg.Type)
				return fmt.Errorf("expected Bind or Describe after ParseComplete, got %c", nextMsg.Type)
			}
		}

	afterBind:
		// Handle messages after Bind (optional Describe, Execute)
		for {
			p.logDebug("  -> Reading message after Bind...\n")
			nextMsg, err := ReadRegularMessageFromReader(clientReader)
			if err != nil {
				p.logDebug("  -> Error reading message after Bind: %v\n", err)
				return fmt.Errorf("error reading message after Bind: %w", err)
			}

			switch nextMsg.Type {
			case MsgDescribe:
				p.logDebug("  -> Got Describe message after Bind\n")
				// Send RowDescription for the result set
				if err := p.sendRowDescription(clientConn, mock); err != nil {
					return fmt.Errorf("error sending row description: %w", err)
				}
				// Continue reading for Execute

			case MsgFlush:
				p.logDebug("  -> Got Flush message after Bind, ignoring\n")
				// Flush has no response, just continue reading

			case MsgClose:
				p.logDebug("  -> Got Close message after Bind\n")
				// Send CloseComplete
				if err := p.writeMessage(clientConn, MsgCloseComplete, nil); err != nil {
					return fmt.Errorf("error sending CloseComplete: %w", err)
				}
				// Continue reading for Execute

			case MsgExecute:
				p.logDebug("  -> Got Execute message\n")
				// Send mock result set
				p.logDebug("  -> Sending mock result set\n")
				// Use the query from the mock or the parsed query
				queryToUse := mock.SQL
				if queryToUse == "" {
					queryToUse = query
				}
				p.logDebug("  -> Using query for columns: %s\n", queryToUse[:min(100, len(queryToUse))])
				if err := p.sendMockResultSetForExtended(clientConn, mock, queryToUse); err != nil {
					p.logDebug("  -> Error sending mock result: %v\n", err)
					return fmt.Errorf("error sending mock result: %w", err)
				}

				// Send CommandComplete (without ReadyForQuery - we'll send that after Sync)
				p.logDebug("  -> Sending CommandComplete\n")
				if _, err := clientConn.Write(CreateCommandComplete("SELECT 2")); err != nil {
					return fmt.Errorf("error sending CommandComplete: %w", err)
				}
				// Now wait for Sync
				goto afterExecute

			default:
				p.logDebug("  -> Unexpected message type %c after Bind\n", nextMsg.Type)
				return fmt.Errorf("expected Execute or Describe after Bind, got %c", nextMsg.Type)
			}
		}

	afterExecute:
		// Read and handle Sync message
		p.logDebug("  -> Reading Sync message...\n")
		syncMsg, err := ReadRegularMessageFromReader(clientReader)
		if err != nil {
			p.logDebug("  -> Error reading Sync: %v\n", err)
			return fmt.Errorf("error reading Sync: %w", err)
		}
		if syncMsg.Type != MsgSync {
			p.logDebug("  -> Expected Sync, got %c\n", syncMsg.Type)
			return fmt.Errorf("expected Sync message, got %c", syncMsg.Type)
		}
		p.logDebug("  -> Got Sync message, sending ReadyForQuery\n")

		// Send ReadyForQuery
		p.logDebug("  -> Sending ReadyForQuery (transaction state: I)\n")
		if err := p.writeMessage(clientConn, MsgReadyForQuery, []byte{'I'}); err != nil {
			return fmt.Errorf("error sending ReadyForQuery: %w", err)
		}
		p.logDebug("  -> Successfully handled extended query\n")

		return nil

	default:
		p.logDebug("  -> Unknown message type %c, forwarding to upstream\n", msg.Type)
		return p.writeMessage(upstreamConn, msg.Type, msg.Payload)
	}
}

// extractQueryFromParse extracts the SQL query and parameter types from a Parse message payload
// Returns the query string and a slice of parameter type OIDs (nil if not specified)
func (p *Proxy) extractQueryFromParse(payload []byte) (string, []uint32) {
	if len(payload) == 0 {
		return "", nil
	}

	// Parse message format: [stmt_name]\0 [query]\0 [num_params (int16)] [param_oid_1 (int32)] ...
	// Find the first null byte (end of statement name)
	pos := 0

	// Skip statement name (read until null)
	for pos < len(payload) && payload[pos] != 0 {
		pos++
	}
	if pos >= len(payload) {
		return "", nil
	}
	pos++ // Skip the null byte

	// Now read the query (until next null)
	queryStart := pos
	for pos < len(payload) && payload[pos] != 0 {
		pos++
	}
	if pos >= len(payload) {
		return "", nil
	}
	query := string(payload[queryStart:pos])
	pos++ // Skip the null byte

	// Now read num_params (int16, big-endian)
	if pos+2 > len(payload) {
		return query, nil // No parameter types specified
	}
	numParams := binary.BigEndian.Uint16(payload[pos : pos+2])
	pos += 2

	if numParams == 0 {
		return query, nil // No parameter types specified
	}

	// Read parameter type OIDs
	paramTypes := make([]uint32, numParams)
	for i := uint16(0); i < numParams; i++ {
		if pos+4 > len(payload) {
			break // Not enough data
		}
		paramTypes[i] = binary.BigEndian.Uint32(payload[pos : pos+4])
		pos += 4
	}

	return query, paramTypes
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
				logger.Error("PostgreSQL Proxy: Error loading payload %s: %v", mock.ReturnsFile, err)
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
func (p *Proxy) sendMockResultSetForExtended(conn net.Conn, mock *types.ExpectStatement, actualQuery string) error {
	// Determine columns from mock or use defaults
	columns := []string{"id", "name", "email"}
	if mock.Table != "" {
		columns = p.inferColumnsForTable(mock.Table)
	}

	var rows []map[string]interface{}

	switch mock.Channel {
	case types.ReadPostgreSQL:
		if mock.ReturnsEmpty {
			// For empty results, we need to send RowDescription with a dummy NULL row
			// SQLAlchemy ORM requires at least one row to properly set up the result processing
			// The application will handle the NULLs correctly (e.g., scalar_one_or_none() returns None)
			// Extract columns from SQL query to ensure proper schema
			if actualQuery != "" {
				sqlColumns := p.extractSelectColumns(actualQuery)
				p.logDebug("  -> Empty result: Extracted columns from SQL: %v\n", sqlColumns)
				if len(sqlColumns) > 0 {
					columns = sqlColumns
				}
			}
			// Create a dummy row with all NULL values
			dummyRow := make(map[string]interface{})
			for _, col := range columns {
				dummyRow[col] = nil
			}
			rows = []map[string]interface{}{dummyRow}
			p.logDebug("  -> Empty result: Sending dummy NULL row\n")
		}

		if mock.ReturnsFile != "" {
			p.loader.BaseDir = mock.BaseDir
			payload, err := p.loader.Load(mock.ReturnsFile)
			if err != nil {
				logger.Error("PostgreSQL Proxy: Error loading payload %s: %v", mock.ReturnsFile, err)
				return p.writeMessage(conn, MsgNoData, nil)
			}

			rows = p.extractRowsFromPayload(payload)
			// Extract columns from SQL SELECT clause to maintain consistent order
			// This ensures RowDescription and DataRow have matching column orders
			if actualQuery != "" {
				sqlColumns := p.extractSelectColumns(actualQuery)
				p.logDebug("  -> Extracted columns from SQL: %v (query: %s)\n", sqlColumns, actualQuery[:min(100, len(actualQuery))])
				if len(sqlColumns) > 0 {
					columns = sqlColumns
				}
			}
			p.logDebug("  -> Final columns: %v\n", columns)
			p.logDebug("  -> Row data: %v\n", rows)
		}

	case types.WritePostgreSQL:
		// For writes, check if there's return data (e.g., from RETURNING clause)
		// If ReturnsFile is specified, use that data
		if mock.ReturnsFile != "" {
			p.loader.BaseDir = mock.BaseDir
			payload, err := p.loader.Load(mock.ReturnsFile)
			if err != nil {
				return p.writeMessage(conn, MsgNoData, nil)
			}
			rows = p.extractRowsFromPayload(payload)
			if len(rows) > 0 {
				columns = make([]string, 0, len(rows[0]))
				for col := range rows[0] {
					columns = append(columns, col)
				}
			}
		} else {
			// For writes without explicit return data, check if the query has a RETURNING clause
			// If so, we need to generate a synthetic return row
			returningColumns := p.extractReturningColumns(mock.SQL)
			if len(returningColumns) > 0 {
				// Generate a synthetic row with the returning columns
				columns = returningColumns
				row := p.generateSyntheticReturnRow(returningColumns)
				rows = []map[string]interface{}{row}
			} else {
				// For writes without RETURNING clause, send NoData
				return p.writeMessage(conn, MsgNoData, nil)
			}
		}
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

// logDebug writes a debug message to the log file
func (p *Proxy) logDebug(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)

	// Write to file if available
	if p.debugLog != nil {
		p.debugLog.WriteString(msg)
		p.debugLog.Sync()
	}

	// Always write to a well-known file path that persists
	f, _ := os.OpenFile("/app/project/proxy-queries.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if f != nil {
		f.WriteString(msg)
		f.Close()
	}
}

// handleDescribe handles a Describe message and sends the appropriate response
func (p *Proxy) handleDescribe(conn net.Conn, mock *types.ExpectStatement, actualQuery string) error {
	// For a statement Describe, we need to send ParameterDescription
	// For a portal Describe, we need to send RowDescription

	// First, send ParameterDescription with the number of parameters
	// We need to count the number of $N placeholders in the SQL
	// Use actualQuery if mock.SQL is empty (for WRITE operations)
	sqlToCheck := mock.SQL
	if sqlToCheck == "" {
		sqlToCheck = actualQuery
	}
	numParams := p.countParameters(sqlToCheck)
	p.logDebug("  -> Sending ParameterDescription with %d parameters\n", numParams)

	// Extract parameter types from SQL casts (e.g., $1::INTEGER)
	paramTypes := p.extractParameterTypes(sqlToCheck)
	p.logDebug("  -> Extracted %d parameter types: %v\n", len(paramTypes), paramTypes)
	if len(paramTypes) != numParams {
		// Fallback: create default types if extraction didn't work
		p.logDebug("  -> Fallback to default TEXT types (mismatch: got %d types for %d params)\n", len(paramTypes), numParams)
		paramTypes = make([]uint32, numParams)
		for i := 0; i < numParams; i++ {
			paramTypes[i] = 25 // Default TEXT
		}
	}

	paramDesc := CreateParameterDescription(uint16(numParams), paramTypes)
	p.logDebug("  -> Sending ParameterDescription with types: %v\n", paramTypes)
	if _, err := conn.Write(paramDesc); err != nil {
		return fmt.Errorf("error sending ParameterDescription: %w", err)
	}

	// Then send RowDescription for SELECT queries
	if mock.Channel == types.ReadPostgreSQL {
		// For empty results, we still need to send RowDescription with proper columns
		// SQLAlchemy ORM needs the column structure to set up result processing
		// Extract columns from SQL query to ensure consistency
		columns := p.extractSelectColumns(actualQuery)
		if len(columns) == 0 {
			// Fallback to inferred columns if SQL parsing fails
			columns = p.inferColumnsForTable(mock.Table)
		}
		p.logDebug("  -> Sending RowDescription with columns: %v\n", columns)

		// Send RowDescription
		return p.result.SendRowDescription(conn, columns)
	}

	// For write operations, check if there's a RETURNING clause
	if mock.Channel == types.WritePostgreSQL {
		returningColumns := p.extractReturningColumns(sqlToCheck)
		if len(returningColumns) > 0 {
			// Send RowDescription for the RETURNING columns
			return p.result.SendRowDescription(conn, returningColumns)
		}
	}

	// For write operations without RETURNING, send NoData
	return p.writeMessage(conn, MsgNoData, nil)
}

// countParameters counts the number of $N parameters in a SQL query
func (p *Proxy) countParameters(sql string) int {
	if sql == "" {
		return 0
	}
	// Use regex to find all $N patterns and get the max N
	re := regexp.MustCompile(`\$(\d+)`)
	matches := re.FindAllStringSubmatch(sql, -1)
	maxParam := 0
	for _, match := range matches {
		if len(match) > 1 {
			n, _ := fmt.Sscanf(match[1], "%d", &maxParam)
			if n > 0 && maxParam > 0 {
				// maxParam is already set by Sscanf
			}
		}
	}
	return maxParam
}

// extractParameterTypes extracts PostgreSQL type OIDs for each $N parameter from SQL casts
// e.g., "$1::INTEGER" returns [23] for parameter 1
// e.g., "$1::INTEGER AND $2::VARCHAR" returns [23, 1043]
func (p *Proxy) extractParameterTypes(sql string) []uint32 {
	if sql == "" {
		return nil
	}

	// Map of PostgreSQL type names to OIDs
	typeOIDs := map[string]uint32{
		"INTEGER":     23,
		"INT":         23,
		"INT4":        23,
		"BIGINT":      20,
		"INT8":        20,
		"SMALLINT":    21,
		"INT2":        21,
		"VARCHAR":     1043,
		"TEXT":        25,
		"CHAR":        18,
		"BOOLEAN":     16,
		"BOOL":        16,
		"TIMESTAMP":   1114,
		"TIMESTAMPTZ": 1184,
		"DATE":        1082,
		"TIME":        1083,
		"NUMERIC":     1700,
		"DECIMAL":     1700,
		"FLOAT":       701,
		"REAL":        700,
		"DOUBLE":      701,
		"BYTEA":       17,
		"UUID":        2950,
		"JSON":        114,
		"JSONB":       3802,
	}

	// Find all $N::TYPE patterns
	re := regexp.MustCompile(`\$(\d+)::([A-Za-z0-9_]+)`)
	matches := re.FindAllStringSubmatch(sql, -1)

	// Find the max parameter number to size the array
	maxParam := 0
	for _, match := range matches {
		if len(match) > 1 {
			var paramNum int
			fmt.Sscanf(match[1], "%d", &paramNum)
			if paramNum > maxParam {
				maxParam = paramNum
			}
		}
	}

	if maxParam == 0 {
		return nil
	}

	// Create array with default type TEXT (25)
	paramTypes := make([]uint32, maxParam)
	for i := range paramTypes {
		paramTypes[i] = 25 // Default TEXT
	}

	// Fill in the types we found
	for _, match := range matches {
		if len(match) > 2 {
			var paramNum int
			fmt.Sscanf(match[1], "%d", &paramNum)
			typeName := strings.ToUpper(match[2])
			if oid, ok := typeOIDs[typeName]; ok && paramNum > 0 && paramNum <= maxParam {
				paramTypes[paramNum-1] = oid
			}
		}
	}

	return paramTypes
}

// extractReturningColumns extracts column names from a RETURNING clause in a SQL query
func (p *Proxy) extractReturningColumns(sql string) []string {
	if sql == "" {
		return nil
	}

	// Find RETURNING clause
	idx := strings.Index(strings.ToUpper(sql), "RETURNING")
	if idx == -1 {
		return nil
	}

	// Extract the part after RETURNING
	returningPart := sql[idx+9:] // Skip "RETURNING"

	// Remove any trailing semicolon or extra clauses
	if semi := strings.Index(returningPart, ";"); semi != -1 {
		returningPart = returningPart[:semi]
	}

	// Split by comma and trim spaces
	parts := strings.Split(returningPart, ",")
	columns := make([]string, 0, len(parts))

	for _, part := range parts {
		col := strings.TrimSpace(part)
		if col == "" {
			continue
		}

		// Handle table.column format (e.g., "notifications.id")
		if dot := strings.LastIndex(col, "."); dot != -1 {
			col = col[dot+1:]
		}

		// Remove any type casts (e.g., "::VARCHAR")
		if cast := strings.Index(col, "::"); cast != -1 {
			col = col[:cast]
		}

		columns = append(columns, col)
	}

	return columns
}

// generateSyntheticReturnRow generates a synthetic row for INSERT RETURNING operations
func (p *Proxy) generateSyntheticReturnRow(columns []string) map[string]interface{} {
	row := make(map[string]interface{})

	for _, col := range columns {
		switch strings.ToLower(col) {
		case "id":
			// Generate a synthetic ID
			row[col] = 1
		case "created_at", "updated_at":
			// Generate current timestamp
			row[col] = time.Now().UTC().Format(time.RFC3339)
		default:
			// For other columns, return empty string or 0
			row[col] = ""
		}
	}

	return row
}

// sendRowDescription sends a RowDescription message for the given mock
func (p *Proxy) sendRowDescription(conn net.Conn, mock *types.ExpectStatement) error {
	columns := p.inferColumnsForTable(mock.Table)
	return p.result.SendRowDescription(conn, columns)
}

// handleSimpleQuery handles a simple Query message and sends the mock response
func (p *Proxy) handleSimpleQuery(conn net.Conn, query string, mock *types.ExpectStatement) error {
	p.logDebug("  -> Handling simple query: %s\n", query[:min(100, len(query))])

	// Send RowDescription first
	columns := p.inferColumnsForTable(mock.Table)
	if err := p.result.SendRowDescription(conn, columns); err != nil {
		return fmt.Errorf("error sending row description: %w", err)
	}

	// Send mock result set
	if err := p.sendMockResultSetForExtended(conn, mock, query); err != nil {
		return fmt.Errorf("error sending mock result: %w", err)
	}

	// Send CommandComplete
	if err := p.result.SendCommandComplete(conn, "SELECT 2"); err != nil {
		return fmt.Errorf("error sending CommandComplete: %w", err)
	}

	p.logDebug("  -> Successfully handled simple query\n")
	return nil
}

// extractSelectColumns extracts column names from a SELECT clause in SQL query
// This ensures RowDescription and DataRow have consistent column ordering
func (p *Proxy) extractSelectColumns(sql string) []string {
	if sql == "" {
		return nil
	}

	// Convert to uppercase for case-insensitive matching
	upperSQL := strings.ToUpper(sql)

	// Find SELECT and FROM positions (in the uppercase version)
	selectIdx := strings.Index(upperSQL, "SELECT")
	fromIdx := strings.Index(upperSQL, "FROM")

	if selectIdx == -1 || fromIdx == -1 || fromIdx <= selectIdx {
		return nil
	}

	// Extract the columns part (between SELECT and FROM) from the original SQL
	// Use the same indices since SELECT and FROM are the same in both cases
	columnsPart := sql[selectIdx+6 : fromIdx] // +6 to skip "SELECT"
	columnsPart = strings.TrimSpace(columnsPart)
	p.logDebug("  -> Extracted columns part: %s\n", columnsPart)

	// Handle DISTINCT keyword
	if strings.HasPrefix(strings.ToUpper(columnsPart), "DISTINCT ") {
		columnsPart = strings.TrimPrefix(columnsPart, "DISTINCT ")
		columnsPart = strings.TrimPrefix(columnsPart, "distinct ")
		columnsPart = strings.TrimSpace(columnsPart)
	}

	// Split by comma and extract column names
	columnNames := []string{}
	columns := strings.Split(columnsPart, ",")

	for _, col := range columns {
		col = strings.TrimSpace(col)
		if col == "" {
			continue
		}

		// Handle qualified column names (e.g., "notifications.id")
		// Extract just the column name after the last dot
		if dotIdx := strings.LastIndex(col, "."); dotIdx != -1 {
			col = col[dotIdx+1:]
		}

		// Handle column aliases (e.g., "id AS notification_id")
		// Take just the first part before AS
		upperCol := strings.ToUpper(col)
		if asIdx := strings.Index(upperCol, " AS "); asIdx != -1 {
			col = strings.TrimSpace(col[:asIdx])
		}

		// Remove any type casts (e.g., "::INTEGER")
		if castIdx := strings.Index(col, "::"); castIdx != -1 {
			col = col[:castIdx]
		}

		col = strings.TrimSpace(col)
		if col != "" {
			columnNames = append(columnNames, col)
		}
	}

	p.logDebug("  -> Extracted columns: %v\n", columnNames)
	return columnNames
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
