package postgresql

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/livecodelife/linespec/pkg/dsl"
	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/proxy/base"
	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/types"
	"github.com/livecodelife/linespec/pkg/verify"
)

// Proxy is a PostgreSQL wire protocol proxy with mock capabilities
// Uses transparent pass-through approach - only intercepts specific queries

// debugWriter is a package-level buffered writer to stderr used only when
// debug mode is enabled. All logDebug calls are no-ops otherwise.
var debugWriter = bufio.NewWriter(os.Stderr)

type Proxy struct {
	addr         string
	upstreamAddr string
	registry     *registry.MockRegistry
	loader       *dsl.PayloadLoader
	startup      *StartupHandler
	result       *ResultHandler
	dbConfig     *base.DatabaseProxyConfig // Configurable database name
	schemaCache  map[string][]ColumnInfo   // table name -> column definitions
}

type ColumnInfo struct {
	Field      string `json:"Field"`
	Type       string `json:"Type"`
	Collation  string `json:"Collation"`
	Null       string `json:"Null"`
	Key        string `json:"Key"`
	Default    string `json:"Default"`
	Extra      string `json:"Extra"`
	Privileges string `json:"Privileges"`
	Comment    string `json:"Comment"`
}

// ConnectionState tracks prepared statements and mocked portals per connection
// This is needed for the extended query protocol where we eavesdrop on Parse
// but only intercept at Bind/Execute
type ConnectionState struct {
	preparedStatements map[string]string                 // statement name -> query
	mockedPortals      map[string]*types.ExpectStatement // portal name -> mock
	justMockedExecute  bool                              // track if we just mocked an Execute
}

// NewConnectionState creates a new connection state
func NewConnectionState() *ConnectionState {
	return &ConnectionState{
		preparedStatements: make(map[string]string),
		mockedPortals:      make(map[string]*types.ExpectStatement),
		justMockedExecute:  false,
	}
}

// }

// NewProxy creates a new PostgreSQL proxy
func NewProxy(addr, upstreamAddr string, reg *registry.MockRegistry) *Proxy {
	return &Proxy{
		addr:         addr,
		upstreamAddr: upstreamAddr,
		registry:     reg,
		loader:       dsl.NewPayloadLoader(""),
		startup:      NewStartupHandler(),
		result:       NewResultHandler(),
		dbConfig:     base.NewDatabaseProxyConfig("postgres"),
		schemaCache:  make(map[string][]ColumnInfo),
	}
}

// handleClientMessages reads and processes client messages in query mode
// Parses PostgreSQL frontend protocol messages and intercepts queries for mocking
// For non-intercepted messages, it forwards to upstream but does NOT drain responses
// (the transparent upstream->client goroutine handles responses)
func (p *Proxy) handleClientMessages(clientConn, upstreamConn net.Conn) error {
	// Wrap client connection in buffered reader for message framing
	clientReader := bufio.NewReader(clientConn)

	for {
		// Read message type
		msgType, err := clientReader.ReadByte()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		// Read message length (4 bytes, big-endian)
		lengthBuf := make([]byte, 4)
		if _, err := io.ReadFull(clientReader, lengthBuf); err != nil {
			return err
		}
		length := int(lengthBuf[0])<<24 | int(lengthBuf[1])<<16 | int(lengthBuf[2])<<8 | int(lengthBuf[3])

		if length < 4 {
			return fmt.Errorf("invalid message length: %d", length)
		}

		// Read payload
		payloadLen := length - 4
		var payload []byte
		if payloadLen > 0 {
			payload = make([]byte, payloadLen)
			if _, err := io.ReadFull(clientReader, payload); err != nil {
				return err
			}
		}

		// Create message struct
		msg := &Message{
			Type:    msgType,
			Length:  int32(length),
			Payload: payload,
		}

		// Check if we should intercept this message
		if p.shouldIntercept(msg) {
			p.logDebug("Intercepting message type %c\n", msgType)
			if err := p.handleInterceptedMessage(msg, clientReader, clientConn, upstreamConn); err != nil {
				p.logDebug("Error handling intercepted message: %v\n", err)
				return err
			}
		} else {
			// Forward to upstream transparently
			// The transparent goroutine (io.Copy) will handle the response
			msgBytes := make([]byte, 0, 1+4+len(payload))
			msgBytes = append(msgBytes, msgType)
			msgBytes = append(msgBytes, lengthBuf...)
			msgBytes = append(msgBytes, payload...)

			if _, err := upstreamConn.Write(msgBytes); err != nil {
				return err
			}
			// NOTE: We do NOT drain the response here - the transparent goroutine
			// handles all upstream->client traffic
		}
	}
}

// handleQueryMessage handles a simple Query message (type 'Q')
func (p *Proxy) handleQueryMessage(query string, msg []byte, clientConn, upstreamConn net.Conn) error {
	tableName := p.extractTable(query)

	// Check if we should mock this query
	p.registry.CheckNegativeMocks(tableName, query)
	if mock, found := p.registry.FindMock(tableName, query); found {
		logger.Debug("PostgreSQL Proxy: Mocking simple query for table %s", tableName)

		// Send mock response
		if err := p.sendMockResponse(clientConn, mock, MsgQuery, query); err != nil {
			return err
		}
		// Don't forward to upstream - we mocked it
		return nil
	}

	// No mock - forward to upstream
	if _, err := upstreamConn.Write(msg); err != nil {
		return err
	}
	return nil
}

// handleParseMessage handles an extended query Parse message (type 'P')
func (p *Proxy) handleParseMessage(query string, msg []byte, clientConn, upstreamConn net.Conn) error {
	tableName := p.extractTable(query)

	// Check if we should mock this query
	p.registry.CheckNegativeMocks(tableName, query)
	if mock, found := p.registry.FindMock(tableName, query); found {
		logger.Debug("PostgreSQL Proxy: Mocking extended query for table %s", tableName)

		// For extended protocol, we need to handle the full flow:
		// 1. Send ParseComplete
		// 2. Read Bind message and send BindComplete
		// 3. Read Execute message and send mock result
		// 4. Read Sync message and send ReadyForQuery

		// Send ParseComplete
		if err := p.writeMessage(clientConn, MsgParseComplete, nil); err != nil {
			return err
		}

		// Handle the rest of the extended query flow
		return p.handleMockedExtendedFlow(clientConn, mock, query)
	}

	// No mock - forward to upstream
	if _, err := upstreamConn.Write(msg); err != nil {
		return err
	}
	return nil
}

// handleMockedExtendedFlow handles the extended query flow after Parse
func (p *Proxy) handleMockedExtendedFlow(clientConn net.Conn, mock *types.ExpectStatement, query string) error {
	buf := make([]byte, 0, 1024)
	tmpBuf := make([]byte, 1024)

	for {
		n, err := clientConn.Read(tmpBuf)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		buf = append(buf, tmpBuf[:n]...)

		// Process messages
		for len(buf) > 0 {
			if len(buf) < 5 {
				break // Need more data
			}

			msgType := buf[0]
			length := int(buf[1])<<24 | int(buf[2])<<16 | int(buf[3])<<8 | int(buf[4])
			totalLen := 1 + length

			if len(buf) < totalLen {
				break // Need more data
			}

			_ = buf[:totalLen] // Extract message (we don't need the content, just to remove it)
			buf = buf[totalLen:]

			switch msgType {
			case MsgBind:
				// Send BindComplete
				if err := p.writeMessage(clientConn, MsgBindComplete, nil); err != nil {
					return err
				}

			case MsgExecute:
				// Send mock result
				if err := p.sendMockResponse(clientConn, mock, MsgParse, query); err != nil {
					return err
				}

			case MsgSync:
				// Send ReadyForQuery
				if err := p.writeMessage(clientConn, MsgReadyForQuery, []byte{'I'}); err != nil {
					return err
				}
				return nil // Extended query flow complete

			default:
				// Ignore other messages in mocked flow
				logger.Debug("PostgreSQL Proxy: Ignoring message type %c in mocked extended flow", msgType)
			}
		}
	}
}

// GetDatabaseName returns the current database name
func (p *Proxy) GetDatabaseName() string {
	return p.dbConfig.GetDatabaseName()
}

// SetDatabaseName sets the database name for schema responses
func (p *Proxy) SetDatabaseName(name string) {
	p.dbConfig.SetDatabaseName(name)
}

// LoadSchema loads schema from a JSON file
func (p *Proxy) LoadSchema(schemaFile string) error {
	data, err := os.ReadFile(schemaFile)
	if err != nil {
		return fmt.Errorf("failed to read schema file: %w", err)
	}

	if err := json.Unmarshal(data, &p.schemaCache); err != nil {
		return fmt.Errorf("failed to parse schema file: %w", err)
	}

	logger.Debug("Loaded schema for %d tables", len(p.schemaCache))
	return nil
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

// handleConnection handles a single client connection with two-phase proxy:
// Phase 1: Transparent startup relay, watching for ReadyForQuery
// Phase 2: Message-framing with query interception
func (p *Proxy) handleConnection(clientConn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("PostgreSQL Proxy: PANIC in handleConnection: %v", r)
		}
		clientConn.Close()
	}()

	remoteAddr := clientConn.RemoteAddr().String()
	logger.Debug("PostgreSQL Proxy: New connection from %s", remoteAddr)
	p.logDebug("New connection from %s\n", remoteAddr)

	// STEP 1: Connect to upstream FIRST (critical per PROXY_FIX.md)
	logger.Debug("PostgreSQL Proxy: Connecting to upstream %s...", p.upstreamAddr)
	p.logDebug("Connecting to upstream %s...\n", p.upstreamAddr)
	upstreamConn, err := net.Dial("tcp", p.upstreamAddr)
	if err != nil {
		logger.Error("PostgreSQL Proxy: Failed to connect to upstream %s: %v", p.upstreamAddr, err)
		p.logDebug("Failed to connect to upstream: %v\n", err)
		return
	}
	defer upstreamConn.Close()
	logger.Debug("PostgreSQL Proxy: Connected to upstream")
	p.logDebug("Connected to upstream\n")

	// STEP 2: Two-phase proxy with query interception
	logger.Debug("PostgreSQL Proxy: Starting two-phase proxy")
	p.logDebug("Starting two-phase proxy\n")
	if err := p.proxyWithStatefulRelay(clientConn, upstreamConn); err != nil {
		logger.Error("PostgreSQL Proxy: Connection from %s failed during startup: %v", remoteAddr, err)
		p.logDebug("Connection failed during startup: %v\n", err)
		return
	}
	logger.Debug("PostgreSQL Proxy: Two-phase proxy complete")
	p.logDebug("Two-phase proxy complete\n")
}

// proxyWithStatefulRelay implements the two-phase proxy:
// Phase 1 (startup): Transparent bidirectional relay, watching for ReadyForQuery in server->client
// Phase 2 (query): Client->upstream with message-framing and query interception
//
// Returns an error if the startup phase fails (timeout or upstream failure).
// The caller is responsible for closing both connections.
func (p *Proxy) proxyWithStatefulRelay(clientConn, upstreamConn net.Conn) error {
	logger.Debug("PostgreSQL Proxy: Starting bidirectional relay with ReadyForQuery detection")
	p.logDebug("Starting proxyWithStatefulRelay\n")

	const startupTimeout = 10 * time.Second

	// startupReady is closed by the upstream goroutine when ReadyForQuery is detected.
	// upstreamErr receives the error when the upstream connection fails during startup.
	// Using channels eliminates the data race that existed with the previous plain bool.
	startupReady := make(chan struct{})
	upstreamErr := make(chan error, 1)
	var readyOnce sync.Once

	// Buffer for accumulated server data during startup (used only within upstream goroutine)
	var startupBuffer []byte

	// upstreamDone is signalled when the upstream->client goroutine exits (both phases).
	upstreamDone := make(chan error, 1)

	// Start upstream->client goroutine immediately (needed for both phases).
	go func() {
		buf := make([]byte, 4096)
		startupPhase := true
		for {
			n, err := upstreamConn.Read(buf)
			if err != nil {
				if err != io.EOF {
					logger.Debug("PostgreSQL Proxy: Upstream read error: %v", err)
					p.logDebug("Upstream read error: %v\n", err)
				}
				if startupPhase {
					upstreamErr <- err
				}
				upstreamDone <- err
				return
			}

			if n > 0 {
				data := buf[:n]

				// During startup, accumulate and scan for ReadyForQuery.
				if startupPhase {
					startupBuffer = append(startupBuffer, data...)
					if containsReadyForQuery(startupBuffer) {
						logger.Debug("PostgreSQL Proxy: ReadyForQuery detected, startup complete")
						p.logDebug("ReadyForQuery detected, startup complete\n")
						startupPhase = false
						readyOnce.Do(func() { close(startupReady) })
					}
				}

				// Forward to client regardless of startup state.
				if _, err := clientConn.Write(data); err != nil {
					logger.Debug("PostgreSQL Proxy: Client write error: %v", err)
					p.logDebug("Client write error: %v\n", err)
					if startupPhase {
						upstreamErr <- err
					}
					upstreamDone <- err
					return
				}
			}
		}
	}()

	// clientForwardDone signals that the client->upstream forwarding goroutine has stopped.
	clientForwardDone := make(chan struct{})

	// prePhaseTwoBuf holds any client bytes read after startup completed but before
	// Phase 2 started. These must be replayed at the start of Phase 2 because they
	// were already consumed from the TCP receive buffer.
	var prePhaseTwoBuf []byte

	// Forward client->upstream during startup. Stops as soon as startupReady is closed.
	go func() {
		defer close(clientForwardDone)
		buf := make([]byte, 4096)
		for {
			clientConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, err := clientConn.Read(buf)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// Check whether startup finished while we were waiting.
					select {
					case <-startupReady:
						return
					default:
						continue
					}
				}
				if err != io.EOF {
					logger.Debug("PostgreSQL Proxy: Client read error during startup: %v", err)
					p.logDebug("Client read error during startup: %v\n", err)
				} else {
					logger.Debug("PostgreSQL Proxy: Client closed connection during startup")
					p.logDebug("Client closed connection during startup\n")
				}
				return
			}

			if n > 0 {
				// Check for startup completion before forwarding — if startup just
				// finished, the bytes belong to Phase 2 and must not be sent raw.
				select {
				case <-startupReady:
					// Startup completed while Read was in flight; buffer these bytes
					// so Phase 2 can replay them. They are already consumed from the
					// TCP receive buffer and cannot be re-read from clientConn.
					prePhaseTwoBuf = append(prePhaseTwoBuf, buf[:n]...)
					p.logDebug("Startup completed mid-read; buffering %d bytes for Phase 2\n", n)
					return
				default:
				}

				if _, err := upstreamConn.Write(buf[:n]); err != nil {
					logger.Debug("PostgreSQL Proxy: Upstream write error during startup: %v", err)
					p.logDebug("Upstream write error during startup: %v\n", err)
					return
				}
			}
		}
	}()

	// Wait for startup outcome.
	timer := time.NewTimer(startupTimeout)
	defer timer.Stop()

	select {
	case <-startupReady:
		// Success — fall through to Phase 2.
	case err := <-upstreamErr:
		logger.Error("PostgreSQL Proxy: Upstream failed during startup: %v", err)
		p.logDebug("Upstream failed during startup: %v\n", err)
		return fmt.Errorf("upstream failed during startup: %w", err)
	case <-timer.C:
		logger.Error("PostgreSQL Proxy: Startup timed out after %s waiting for ReadyForQuery from %s", startupTimeout, p.upstreamAddr)
		p.logDebug("Startup timed out after %s\n", startupTimeout)
		return fmt.Errorf("startup timed out after %s: upstream %s never sent ReadyForQuery", startupTimeout, p.upstreamAddr)
	}

	// Wait for the client-forwarding goroutine to stop before Phase 2 reads from clientConn.
	<-clientForwardDone

	// Clear read deadline now that startup is complete.
	clientConn.SetReadDeadline(time.Time{})

	logger.Debug("PostgreSQL Proxy: Startup phase complete, switching to message framing")
	p.logDebug("Startup phase complete, switching to message framing\n")

	// Phase 2: upstream->client relay continues in background; we now frame client messages.
	go func() {
		err := <-upstreamDone
		logger.Debug("PostgreSQL Proxy: Upstream->Client goroutine ended: %v", err)
		p.logDebug("Upstream->Client goroutine ended: %v\n", err)
	}()

	// Build the Phase 2 reader: replay any bytes buffered mid-read during startup,
	// then continue reading from the live connection.
	var phase2Reader io.Reader = clientConn
	if len(prePhaseTwoBuf) > 0 {
		p.logDebug("Replaying %d pre-Phase-2 bytes through interceptor\n", len(prePhaseTwoBuf))
		phase2Reader = io.MultiReader(bytes.NewReader(prePhaseTwoBuf), clientConn)
	}

	p.handleClientMessagesWithInterception(phase2Reader, upstreamConn, clientConn)
	return nil
}

// handleClientMessagesWithInterception reads and processes client messages
// New architecture: Eavesdrop on Parse, let prepare phase complete against real DB,
// only intercept at Bind/Execute for mocked statements
func (p *Proxy) handleClientMessagesWithInterception(clientReader io.Reader, upstreamConn, clientConn net.Conn) {
	// Wrap in buffered reader for message framing
	bufReader := bufio.NewReader(clientReader)

	// Per-connection state to track prepared statements and mocked portals
	state := NewConnectionState()

	logger.Debug("PostgreSQL Proxy: Starting message framing loop with state tracking")
	p.logDebug("Starting message framing loop with state tracking\n")

	for {
		// Read message type (1 byte)
		msgType, err := bufReader.ReadByte()
		if err != nil {
			if err == io.EOF {
				logger.Debug("PostgreSQL Proxy: Client closed connection (EOF)")
				p.logDebug("Client closed connection (EOF)\n")
				return
			}
			logger.Debug("PostgreSQL Proxy: Error reading message type: %v", err)
			p.logDebug("Error reading message type: %v\n", err)
			return
		}

		// Read message length (4 bytes, big-endian)
		lengthBuf := make([]byte, 4)
		if _, err := io.ReadFull(bufReader, lengthBuf); err != nil {
			logger.Debug("PostgreSQL Proxy: Error reading message length: %v", err)
			p.logDebug("Error reading message length: %v\n", err)
			return
		}
		length := int(lengthBuf[0])<<24 | int(lengthBuf[1])<<16 | int(lengthBuf[2])<<8 | int(lengthBuf[3])

		if length < 4 {
			logger.Error("PostgreSQL Proxy: Invalid message length: %d", length)
			p.logDebug("Invalid message length: %d\n", length)
			return
		}

		// Read payload
		payloadLen := length - 4
		var payload []byte
		if payloadLen > 0 {
			payload = make([]byte, payloadLen)
			if _, err := io.ReadFull(bufReader, payload); err != nil {
				logger.Debug("PostgreSQL Proxy: Error reading message payload: %v", err)
				p.logDebug("Error reading message payload: %v\n", err)
				return
			}
		}

		logger.Debug("PostgreSQL Proxy: Received message type %c", msgType)
		p.logDebug("Received message type %c\n", msgType)

		// Handle based on message type
		switch msgType {
		case MsgParse:
			// Eavesdrop on Parse to track prepared statements, then forward to real DB
			stmtName, query := p.extractParseInfo(payload)
			if query != "" {
				state.preparedStatements[stmtName] = query
				p.logDebug("  -> Tracked Parse: stmtName='%s', query='%s...'\n", stmtName, query[:min(50, len(query))])
			}
			// Forward to upstream
			if err := p.forwardMessage(upstreamConn, msgType, lengthBuf, payload); err != nil {
				p.logDebug("  -> Error forwarding Parse: %v\n", err)
				return
			}

		case MsgDescribe, MsgClose, MsgFlush:
			// These always flow through to real DB
			p.logDebug("  -> Forwarding message type %c to upstream\n", msgType)
			if err := p.forwardMessage(upstreamConn, msgType, lengthBuf, payload); err != nil {
				p.logDebug("  -> Error forwarding: %v\n", err)
				return
			}

		case MsgBind:
			// Check if this statement should be mocked
			portalName, stmtName := p.extractBindInfo(payload)
			query, exists := state.preparedStatements[stmtName]
			if !exists {
				p.logDebug("  -> Bind for unknown statement '%s', forwarding\n", stmtName)
				if err := p.forwardMessage(upstreamConn, msgType, lengthBuf, payload); err != nil {
					p.logDebug("  -> Error forwarding Bind: %v\n", err)
					return
				}
				continue
			}

			// Check if this query is mocked
			tableName := p.extractTable(query)
			// Use FindMock to increment hit count - this is the actual execution
			p.registry.CheckNegativeMocks(tableName, query)
			if mock, found := p.registry.FindMock(tableName, query); found {
				p.logDebug("  -> Intercepting Bind for mocked statement '%s' (portal '%s')\n", stmtName, portalName)
				// Store the actual query in mock.SQL so sendMockExecuteResponse can detect
				// RETURNING clauses (e.g., INSERT ... RETURNING id) for synthetic result sets
				if mock.SQL == "" {
					mock.SQL = query
				}
				state.mockedPortals[portalName] = mock
				// Send BindComplete ourselves, don't forward to upstream
				if err := p.writeMessage(clientConn, MsgBindComplete, nil); err != nil {
					p.logDebug("  -> Error sending BindComplete: %v\n", err)
					return
				}
				p.logDebug("  -> Sent BindComplete for mocked portal (hit count incremented)\n")
			} else {
				p.logDebug("  -> Bind for non-mocked statement '%s', forwarding\n", stmtName)
				if err := p.forwardMessage(upstreamConn, msgType, lengthBuf, payload); err != nil {
					p.logDebug("  -> Error forwarding Bind: %v\n", err)
					return
				}
			}

		case MsgExecute:
			// Check if this portal is mocked
			portalName := p.extractExecuteInfo(payload)
			if mock, exists := state.mockedPortals[portalName]; exists {
				p.logDebug("  -> Intercepting Execute for mocked portal '%s'\n", portalName)
				// Send mock result
				if err := p.sendMockExecuteResponse(clientConn, mock); err != nil {
					p.logDebug("  -> Error sending mock result: %v\n", err)
					return
				}
				p.logDebug("  -> Sent mock result for portal '%s'\n", portalName)
				// Remove from mocked portals (one-time use)
				delete(state.mockedPortals, portalName)
				// Mark that we just mocked an Execute, so next Sync should send ReadyForQuery
				state.justMockedExecute = true
			} else {
				p.logDebug("  -> Execute for non-mocked portal '%s', forwarding\n", portalName)
				state.justMockedExecute = false
				if err := p.forwardMessage(upstreamConn, msgType, lengthBuf, payload); err != nil {
					p.logDebug("  -> Error forwarding Execute: %v\n", err)
					return
				}
			}

		case MsgSync:
			// If we just mocked an Execute, send ReadyForQuery ourselves
			if state.justMockedExecute {
				p.logDebug("  -> Sending ReadyForQuery after mocked Execute\n")
				if err := p.sendReadyForQuery(clientConn); err != nil {
					p.logDebug("  -> Error sending ReadyForQuery: %v\n", err)
					return
				}
				state.justMockedExecute = false
			} else {
				// Forward Sync to real DB
				p.logDebug("  -> Forwarding Sync to upstream\n")
				if err := p.forwardMessage(upstreamConn, msgType, lengthBuf, payload); err != nil {
					p.logDebug("  -> Error forwarding Sync: %v\n", err)
					return
				}
			}

		case MsgQuery:
			// Simple query protocol - check if we should mock
			query := string(payload)
			tableName := p.extractTable(query)
			p.registry.CheckNegativeMocks(tableName, query)
			if mock, found := p.registry.FindMock(tableName, query); found {
				p.logDebug("  -> Mocking simple query for table %s\n", tableName)
				if err := p.sendMockResponse(clientConn, mock, MsgQuery, query); err != nil {
					p.logDebug("  -> Error sending mock response: %v\n", err)
					return
				}
			} else {
				p.logDebug("  -> Forwarding simple query\n")
				if err := p.forwardMessage(upstreamConn, msgType, lengthBuf, payload); err != nil {
					p.logDebug("  -> Error forwarding Query: %v\n", err)
					return
				}
			}

		default:
			// Forward everything else transparently
			p.logDebug("  -> Forwarding message type %c to upstream\n", msgType)
			if err := p.forwardMessage(upstreamConn, msgType, lengthBuf, payload); err != nil {
				p.logDebug("  -> Error forwarding: %v\n", err)
				return
			}
		}
	}
}

// containsReadyForQuery scans data for a PostgreSQL ReadyForQuery message
// Format: 'Z' (1 byte) + length (4 bytes, big-endian, value = 5) + status (1 byte)
func containsReadyForQuery(data []byte) bool {
	for i := 0; i <= len(data)-6; i++ {
		if data[i] == 'Z' &&
			data[i+1] == 0x00 && data[i+2] == 0x00 &&
			data[i+3] == 0x00 && data[i+4] == 0x05 {
			// Found ReadyForQuery - transaction status can be 'I', 'T', or 'E'
			return true
		}
	}
	return false
}

// parseFrontendMessage attempts to parse a complete frontend message from buf
// Returns: message type, bytes consumed, error
func (p *Proxy) parseFrontendMessage(buf []byte) (byte, int, error) {
	if len(buf) < 5 {
		// Need more data (1 byte type + 4 byte length)
		return 0, 0, nil
	}

	msgType := buf[0]
	length := int(buf[1])<<24 | int(buf[2])<<16 | int(buf[3])<<8 | int(buf[4])

	if length < 4 {
		return 0, 0, fmt.Errorf("invalid message length: %d", length)
	}

	totalLen := 1 + length // type byte + payload
	if len(buf) < totalLen {
		// Need more data
		return 0, 0, nil
	}

	return msgType, totalLen, nil
}

// extractQueryFromMessage extracts the query string from a message
func (p *Proxy) extractQueryFromMessage(msgType byte, msg []byte) string {
	if msgType == MsgQuery {
		// Simple query: payload is the query string (null-terminated)
		if len(msg) > 5 {
			payload := msg[5:] // Skip type + length
			// Find null terminator
			for i, b := range payload {
				if b == 0 {
					return string(payload[:i])
				}
			}
			return string(payload)
		}
	} else if msgType == MsgParse {
		// Extended query: payload is stmt_name\0query\0...
		if len(msg) > 5 {
			payload := msg[5:] // Skip type + length
			// Skip first null-terminated string (stmt_name)
			i := 0
			for i < len(payload) && payload[i] != 0 {
				i++
			}
			if i < len(payload) {
				i++ // Skip null
				// Now at query string
				queryStart := i
				for i < len(payload) && payload[i] != 0 {
					i++
				}
				return string(payload[queryStart:i])
			}
		}
	}
	return ""
}

// isInMockedExtendedFlow returns true if we're currently in a mocked extended query flow
// This is a simple state machine - in a real implementation, track the flow properly
func (p *Proxy) isInMockedExtendedFlow() bool {
	// TODO: Implement proper state tracking for extended query flow
	// For now, return false to forward all extended messages normally
	return false
}

// handleMockedExtendedMessage handles Bind, Execute, Sync in a mocked extended query flow
func (p *Proxy) handleMockedExtendedMessage(msgType byte, clientConn net.Conn) error {
	// TODO: Implement proper extended query mock responses
	// For now, just return an error
	return fmt.Errorf("mocked extended flow not yet implemented")
}

// sendMockResponse sends a mock response to the client
func (p *Proxy) sendMockResponse(clientConn net.Conn, mock *types.ExpectStatement, msgType byte, query string) error {
	logger.Debug("PostgreSQL Proxy: Sending mock response for %s query", msgType)

	// Execute VERIFY rules if any
	if len(mock.Verify) > 0 {
		if err := verify.VerifySQL(query, mock.Verify); err != nil {
			return p.sendErrorResponse(clientConn, fmt.Sprintf("VERIFY failed: %v", err))
		}
	}

	if msgType == MsgQuery {
		// Simple query protocol response
		return p.sendMockResultSimple(clientConn, mock, query)
	} else if msgType == MsgParse {
		// Extended query: Send ParseComplete
		// The actual result will be sent when Execute arrives
		return p.writeMessage(clientConn, MsgParseComplete, nil)
	}

	return nil
}

// sendMockResultSimple sends a complete mock result for simple query protocol
func (p *Proxy) sendMockResultSimple(clientConn net.Conn, mock *types.ExpectStatement, query string) error {
	// Determine columns
	columns := []string{"id", "name", "email"}
	if mock.Table != "" {
		columns = p.inferColumnsForTable(mock.Table)
	}

	// For reads, send RowDescription + DataRows
	if mock.Channel == types.ReadPostgreSQL {
		if mock.ReturnsEmpty {
			// Empty result: RowDescription + CommandComplete + ReadyForQuery
			if err := p.result.SendRowDescription(clientConn, columns); err != nil {
				return err
			}
		} else if mock.ReturnsFile != "" {
			p.loader.BaseDir = mock.BaseDir
			payload, err := p.loader.Load(mock.ReturnsFile)
			if err != nil {
				logger.Error("PostgreSQL Proxy: Error loading payload: %v", err)
				return p.result.SendEmptyResultSet(clientConn, columns)
			}

			rows := p.extractRowsFromPayload(payload)
			if len(rows) > 0 {
				columns = make([]string, 0, len(rows[0]))
				for col := range rows[0] {
					columns = append(columns, col)
				}
			}

			if err := p.result.SendRowDescription(clientConn, columns); err != nil {
				return err
			}

			for _, row := range rows {
				if err := p.result.SendDataRow(clientConn, columns, row); err != nil {
					return err
				}
			}
		} else {
			if err := p.result.SendEmptyResultSet(clientConn, columns); err != nil {
				return err
			}
		}
	}

	// Send CommandComplete
	rowCount := 1
	if mock.Channel == types.ReadPostgreSQL {
		if mock.ReturnsFile != "" {
			p.loader.BaseDir = mock.BaseDir
			if payload, err := p.loader.Load(mock.ReturnsFile); err == nil {
				rows := p.extractRowsFromPayload(payload)
				rowCount = len(rows)
			}
		} else if mock.ReturnsEmpty {
			rowCount = 0
		}
	}
	cmdTag := p.createCommandCompleteTag(query, rowCount)
	if err := p.result.SendCommandComplete(clientConn, cmdTag); err != nil {
		return err
	}

	// Send ReadyForQuery
	return p.writeMessage(clientConn, MsgReadyForQuery, []byte{'I'})
}

// proxyStartupDirect proxies the startup phase without using a buffered reader
func (p *Proxy) proxyStartupDirect(clientConn, upstreamConn net.Conn) error {
	// Set a deadline for startup
	if err := upstreamConn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		logger.Debug("PostgreSQL Proxy: Failed to set upstream deadline: %v", err)
	}
	if err := clientConn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		logger.Debug("PostgreSQL Proxy: Failed to set client deadline: %v", err)
	}

	// Handle SSL request (if client sends one)
	if err := p.proxySSLRequestDirect(clientConn, upstreamConn); err != nil {
		return fmt.Errorf("SSL negotiation failed: %w", err)
	}

	// Forward startup message from client to upstream
	startupMsg, err := p.readStartupMessageDirect(clientConn)
	if err != nil {
		return fmt.Errorf("error reading startup message: %w", err)
	}

	// Send to upstream
	if _, err := upstreamConn.Write(startupMsg); err != nil {
		return fmt.Errorf("error sending startup to upstream: %w", err)
	}

	// Forward all startup responses from upstream to client until ReadyForQuery
	for {
		msg, err := ReadRegularMessage(upstreamConn)
		if err != nil {
			return fmt.Errorf("error reading upstream startup response: %w", err)
		}

		logger.Debug("PostgreSQL Proxy: Startup - forwarding message type %c to client", msg.Type)

		// Forward to client - handle client disconnect gracefully
		if err := p.writeMessage(clientConn, msg.Type, msg.Payload); err != nil {
			if isConnectionClosedError(err) {
				logger.Debug("PostgreSQL Proxy: Client disconnected during startup")
				return nil
			}
			return fmt.Errorf("error forwarding startup response: %w", err)
		}

		// Check if startup is complete
		switch msg.Type {
		case MsgReadyForQuery:
			logger.Debug("PostgreSQL Proxy: Startup complete - received ReadyForQuery")
			// Clear deadlines
			upstreamConn.SetDeadline(time.Time{})
			clientConn.SetDeadline(time.Time{})
			return nil
		case MsgErrorResponse:
			return fmt.Errorf("upstream returned error during startup")
		}
	}
}

// proxySSLRequestDirect checks for and handles SSL request without buffered reader
func (p *Proxy) proxySSLRequestDirect(clientConn, upstreamConn net.Conn) error {
	// Read first 8 bytes to check for SSL request
	peekBuf := make([]byte, 8)
	if _, err := io.ReadFull(clientConn, peekBuf); err != nil {
		return fmt.Errorf("error reading SSL check bytes: %w", err)
	}

	// Check if it's an SSL request (length = 8, magic = 0x04D2162F)
	length := int(peekBuf[0])<<24 | int(peekBuf[1])<<16 | int(peekBuf[2])<<8 | int(peekBuf[3])
	isSSLRequest := length == 8 &&
		peekBuf[4] == 0x04 && peekBuf[5] == 0xD2 && peekBuf[6] == 0x16 && peekBuf[7] == 0x2F

	if isSSLRequest {
		// Forward SSL request to upstream
		if _, err := upstreamConn.Write(peekBuf); err != nil {
			return fmt.Errorf("error forwarding SSL request: %w", err)
		}

		// Read upstream's SSL response
		sslResponse := make([]byte, 1)
		if _, err := io.ReadFull(upstreamConn, sslResponse); err != nil {
			return fmt.Errorf("error reading SSL response: %w", err)
		}

		// Forward to client
		if _, err := clientConn.Write(sslResponse); err != nil {
			return fmt.Errorf("error sending SSL response: %w", err)
		}

		logger.Debug("PostgreSQL Proxy: SSL negotiation handled")
		return nil
	}

	// Not an SSL request - we need to buffer these 8 bytes and treat them as the start
	// of a regular startup message. We'll handle this by pushing them back.
	// For simplicity, we'll use the bufio.Reader approach here but only for this message
	return fmt.Errorf("unexpected non-SSL startup - please use the buffered reader version")
}

// readStartupMessageDirect reads the complete startup message directly from connection
func (p *Proxy) readStartupMessageDirect(conn net.Conn) ([]byte, error) {
	// Read length (4 bytes)
	lengthBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lengthBuf); err != nil {
		return nil, err
	}

	length := int(lengthBuf[0])<<24 | int(lengthBuf[1])<<16 | int(lengthBuf[2])<<8 | int(lengthBuf[3])

	// Sanity check
	if length < 8 || length > 10000 {
		return nil, fmt.Errorf("invalid startup message length: %d", length)
	}

	// Read the rest of the message
	payloadLen := length - 4 // length field is not included in payload
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, err
	}

	// Return full message
	return append(lengthBuf, payload...), nil
}

// proxyTransparent does a simple transparent bidirectional copy
func (p *Proxy) proxyTransparent(clientConn, upstreamConn net.Conn) {
	logger.Debug("PostgreSQL Proxy: Starting transparent bidirectional proxy")

	// Create wait group to wait for both directions to complete
	var wg sync.WaitGroup
	wg.Add(2)

	// Client -> Upstream
	go func() {
		defer wg.Done()
		_, err := io.Copy(upstreamConn, clientConn)
		if err != nil && err != io.EOF {
			logger.Debug("PostgreSQL Proxy: Client->Upstream error: %v", err)
		}
		// Signal upstream that client is done writing
		if tcpConn, ok := upstreamConn.(*net.TCPConn); ok {
			tcpConn.CloseWrite()
		}
	}()

	// Upstream -> Client
	go func() {
		defer wg.Done()
		_, err := io.Copy(clientConn, upstreamConn)
		if err != nil && err != io.EOF {
			logger.Debug("PostgreSQL Proxy: Upstream->Client error: %v", err)
		}
		// Signal client that upstream is done writing
		if tcpConn, ok := clientConn.(*net.TCPConn); ok {
			tcpConn.CloseWrite()
		}
	}()

	// Wait for both directions to complete
	wg.Wait()
	logger.Debug("PostgreSQL Proxy: Both directions closed")
}

// proxyStartup transparently proxies the PostgreSQL startup phase including SSL negotiation.
// This replaces the broken approach that sent fake responses to the client.
func (p *Proxy) proxyStartup(clientReader *bufio.Reader, clientConn, upstreamConn net.Conn) error {
	// Set a deadline for startup to prevent indefinite blocking
	if err := upstreamConn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		logger.Debug("PostgreSQL Proxy: Failed to set upstream deadline: %v", err)
	}
	if err := clientConn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		logger.Debug("PostgreSQL Proxy: Failed to set client deadline: %v", err)
	}

	// Handle SSL request (if client sends one)
	if err := p.proxySSLRequest(clientReader, clientConn, upstreamConn); err != nil {
		return fmt.Errorf("SSL negotiation failed: %w", err)
	}

	// Forward startup message from client to upstream
	startupMsg, err := p.readStartupMessage(clientReader)
	if err != nil {
		return fmt.Errorf("error reading startup message: %w", err)
	}

	// Send to upstream
	if _, err := upstreamConn.Write(startupMsg); err != nil {
		return fmt.Errorf("error sending startup to upstream: %w", err)
	}

	// Forward all startup responses from upstream to client until ReadyForQuery
	for {
		msg, err := ReadRegularMessage(upstreamConn)
		if err != nil {
			return fmt.Errorf("error reading upstream startup response: %w", err)
		}

		logger.Debug("PostgreSQL Proxy: Startup - forwarding message type %c to client", msg.Type)

		// Forward to client - handle client disconnect gracefully
		if err := p.writeMessage(clientConn, msg.Type, msg.Payload); err != nil {
			// Check if the client disconnected (broken pipe or connection reset)
			if isConnectionClosedError(err) {
				logger.Debug("PostgreSQL Proxy: Client disconnected during startup")
				return nil // Return nil to close connection gracefully
			}
			return fmt.Errorf("error forwarding startup response: %w", err)
		}

		// Check if startup is complete
		switch msg.Type {
		case MsgReadyForQuery:
			logger.Debug("PostgreSQL Proxy: Startup complete - received ReadyForQuery")
			// Clear deadlines now that startup is complete
			upstreamConn.SetDeadline(time.Time{})
			clientConn.SetDeadline(time.Time{})
			return nil
		case MsgErrorResponse:
			return fmt.Errorf("upstream returned error during startup")
		}
	}
}

// isConnectionClosedError checks if the error indicates the client closed the connection
func isConnectionClosedError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "closed by peer") ||
		strings.Contains(errStr, "EOF")
}

// proxySSLRequest checks for and handles SSL request
func (p *Proxy) proxySSLRequest(clientReader *bufio.Reader, clientConn, upstreamConn net.Conn) error {
	// Peek at first 8 bytes to check for SSL request
	peekBuf, err := clientReader.Peek(8)
	if err != nil {
		return fmt.Errorf("error peeking at connection: %w", err)
	}

	// Check if it's an SSL request (length = 8, magic = 0x04D2162F)
	length := int(peekBuf[0])<<24 | int(peekBuf[1])<<16 | int(peekBuf[2])<<8 | int(peekBuf[3])
	isSSLRequest := length == 8 &&
		peekBuf[4] == 0x04 && peekBuf[5] == 0xD2 && peekBuf[6] == 0x16 && peekBuf[7] == 0x2F

	if isSSLRequest {
		// Consume the SSL request
		if _, err := clientReader.Discard(8); err != nil {
			return fmt.Errorf("error consuming SSL request: %w", err)
		}

		// Forward SSL request to upstream
		if _, err := upstreamConn.Write(peekBuf); err != nil {
			return fmt.Errorf("error forwarding SSL request: %w", err)
		}

		// Read upstream's SSL response
		sslResponse := make([]byte, 1)
		if _, err := io.ReadFull(upstreamConn, sslResponse); err != nil {
			return fmt.Errorf("error reading SSL response: %w", err)
		}

		// Forward to client
		if _, err := clientConn.Write(sslResponse); err != nil {
			return fmt.Errorf("error sending SSL response: %w", err)
		}

		// If SSL was accepted (response == 'S'), we would need to upgrade to TLS here
		// For now, we assume SSL is declined (response == 'N') and continue with plaintext
		logger.Debug("PostgreSQL Proxy: SSL negotiation handled")
	}

	return nil
}

// readStartupMessage reads the complete startup message
func (p *Proxy) readStartupMessage(reader *bufio.Reader) ([]byte, error) {
	// Read length (4 bytes)
	lengthBuf := make([]byte, 4)
	if _, err := io.ReadFull(reader, lengthBuf); err != nil {
		return nil, err
	}

	length := int(lengthBuf[0])<<24 | int(lengthBuf[1])<<16 | int(lengthBuf[2])<<8 | int(lengthBuf[3])

	// Sanity check
	if length < 8 || length > 10000 {
		return nil, fmt.Errorf("invalid startup message length: %d", length)
	}

	// Read the rest of the message
	payloadLen := length - 4 // length field is not included in payload
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}

	// Return full message
	return append(lengthBuf, payload...), nil
}

// readerWrapper wraps a bufio.Reader to implement io.Reader
type readerWrapper struct {
	reader *bufio.Reader
}

func (w *readerWrapper) Read(p []byte) (n int, err error) {
	return w.reader.Read(p)
}

// proxyWithInterception handles bidirectional proxying with selective query interception.
// Fully single-threaded to eliminate race conditions.
func (p *Proxy) proxyWithInterception(clientReader *bufio.Reader, clientConn net.Conn, upstreamConn net.Conn) {
	logger.Debug("PostgreSQL Proxy: proxyWithInterception starting - fully single-threaded")

	// CRITICAL: We must use the buffered reader for all client reads to avoid
	// losing buffered data from the startup phase.
	// Create a wrapper that implements io.Reader using the buffered reader
	wrap := &readerWrapper{reader: clientReader}

	// Create error channels
	clientToUpstreamErr := make(chan error, 1)
	upstreamToClientErr := make(chan error, 1)

	// Client -> Upstream (using the wrapper to read from buffered reader)
	go func() {
		_, err := io.Copy(upstreamConn, wrap)
		clientToUpstreamErr <- err
	}()

	// Upstream -> Client
	go func() {
		_, err := io.Copy(clientConn, upstreamConn)
		upstreamToClientErr <- err
	}()

	// Wait for either direction to fail
	select {
	case err := <-clientToUpstreamErr:
		if err != nil && err != io.EOF {
			logger.Debug("PostgreSQL Proxy: Client->Upstream error: %v", err)
		}
	case err := <-upstreamToClientErr:
		if err != nil && err != io.EOF {
			logger.Debug("PostgreSQL Proxy: Upstream->Client error: %v", err)
		}
	}
}

// handleInterceptedMessageWithUpstreamDrain handles an intercepted message by forwarding to upstream
// and consuming responses to avoid conflicts, then sending mock responses.
// For extended protocol, we need the upstream's Parse responses to get parameter info.
func (p *Proxy) handleInterceptedMessageWithUpstreamDrain(msg *Message, clientReader *bufio.Reader, clientConn, upstreamConn net.Conn) error {
	logger.Debug("handleInterceptedMessageWithUpstreamDrain: type=%c", msg.Type)
	p.logDebug("handleInterceptedMessageWithUpstreamDrain: type=%c\n", msg.Type)

	switch msg.Type {
	case MsgQuery:
		query := string(msg.Payload)
		tableName := p.extractTable(query)
		logger.Debug("Simple query for table %s: %s", tableName, query[:min(50, len(query))])
		p.registry.CheckNegativeMocks(tableName, query)
		mock, found := p.registry.FindMock(tableName, query)

		if !found {
			p.logDebug("  -> Mock not found, forwarding to upstream\n")
			return p.writeMessage(upstreamConn, msg.Type, msg.Payload)
		}

		// Store the actual query in the mock for proper hit tracking
		if mock.SQL == "" {
			mock.SQL = query
		}

		// Execute VERIFY rules if any
		if len(mock.Verify) > 0 {
			if err := verify.VerifySQL(query, mock.Verify); err != nil {
				p.logDebug("  -> VERIFY failed: %v\n", err)
				return p.sendErrorResponse(clientConn, fmt.Sprintf("VERIFY failed: %v", err))
			}
			p.logDebug("  -> All VERIFY rules passed\n")
		}

		// For simple queries: forward to upstream to get real responses, don't mock
		// This avoids the race condition and lets the real database handle simple queries
		p.logDebug("  -> Forwarding simple query to upstream\n")
		return p.forwardAndDrainSimpleQuery(msg, clientConn, upstreamConn)

	case MsgParse:
		// Extended query protocol: Handle Parse/Bind/Execute/Sync cycle
		// We MUST forward Parse to upstream to get parameter info, then mock the Execute
		query, parseParamTypes := p.extractQueryFromParse(msg.Payload)
		p.logDebug("  -> Parse query: %s\n", query[:min(100, len(query))])
		p.logDebug("  -> Parse param types from client: %v\n", parseParamTypes)

		if query == "" {
			p.logDebug("  -> Empty query, forwarding to upstream\n")
			return p.writeMessage(upstreamConn, msg.Type, msg.Payload)
		}

		tableName := p.extractTable(query)
		p.registry.CheckNegativeMocks(tableName, query)
		mock, found := p.registry.FindMock(tableName, query)
		if !found {
			p.logDebug("  -> Mock not found for table %s, forwarding to upstream\n", tableName)
			return p.writeMessage(upstreamConn, msg.Type, msg.Payload)
		}

		p.logDebug("  -> Found mock for table %s, hit count incremented\n", tableName)

		// Store the actual query in the mock for later use
		if mock.SQL == "" {
			mock.SQL = query
		}

		// Execute VERIFY rules if any
		if len(mock.Verify) > 0 {
			if err := verify.VerifySQL(query, mock.Verify); err != nil {
				p.logDebug("  -> VERIFY failed: %v\n", err)
				return p.sendErrorResponse(clientConn, fmt.Sprintf("VERIFY failed: %v", err))
			}
			p.logDebug("  -> All VERIFY rules passed\n")
		}

		// Handle extended query protocol with real Parse but mocked Execute
		return p.handleExtendedQueryWithMock(msg, query, mock, clientReader, clientConn, upstreamConn)

	default:
		p.logDebug("  -> Unknown message type %c, forwarding to upstream\n", msg.Type)
		return p.writeMessage(upstreamConn, msg.Type, msg.Payload)
	}
}

// forwardAndDrainSimpleQuery forwards a simple query to upstream and drains all responses
func (p *Proxy) forwardAndDrainSimpleQuery(msg *Message, clientConn, upstreamConn net.Conn) error {
	// Forward query to upstream
	if err := p.writeMessage(upstreamConn, msg.Type, msg.Payload); err != nil {
		return err
	}

	// Drain all responses from upstream and forward to client
	for {
		msg, err := ReadRegularMessage(upstreamConn)
		if err != nil {
			return err
		}

		if err := p.writeMessage(clientConn, msg.Type, msg.Payload); err != nil {
			return err
		}

		if msg.Type == MsgReadyForQuery {
			break
		}
	}

	return nil
}

// handleExtendedQueryWithMock handles Parse/Bind/Execute/Sync with real Parse but mocked Execute
func (p *Proxy) handleExtendedQueryWithMock(parseMsg *Message, query string, mock *types.ExpectStatement, clientReader *bufio.Reader, clientConn, upstreamConn net.Conn) error {
	p.logDebug("  -> Intercepting extended query for table %s\n", mock.Table)

	// Step 1: Forward Parse to upstream and get ParseComplete
	if err := p.writeMessage(upstreamConn, parseMsg.Type, parseMsg.Payload); err != nil {
		return fmt.Errorf("error forwarding Parse to upstream: %w", err)
	}

	// Wait for ParseComplete from upstream
	p.logDebug("  -> Waiting for ParseComplete from upstream\n")
	msg, err := ReadRegularMessage(upstreamConn)
	if err != nil {
		return fmt.Errorf("error reading ParseComplete: %w", err)
	}
	if msg.Type != MsgParseComplete {
		return fmt.Errorf("expected ParseComplete, got %c", msg.Type)
	}

	// Forward ParseComplete to client
	p.logDebug("  -> Forwarding ParseComplete to client\n")
	if err := p.writeMessage(clientConn, MsgParseComplete, nil); err != nil {
		return fmt.Errorf("error sending ParseComplete: %w", err)
	}

	// Step 2: Handle messages between Parse and Bind
	for {
		p.logDebug("  -> Reading next message from client...\n")
		nextMsg, err := ReadRegularMessageFromReader(clientReader)
		if err != nil {
			return fmt.Errorf("error reading message after ParseComplete: %w", err)
		}

		switch nextMsg.Type {
		case MsgDescribe:
			p.logDebug("  -> Got Describe message, forwarding to upstream\n")
			// Forward Describe to upstream
			if err := p.writeMessage(upstreamConn, nextMsg.Type, nextMsg.Payload); err != nil {
				return fmt.Errorf("error forwarding Describe: %w", err)
			}
			// Drain ParameterDescription and/or RowDescription from upstream
			for {
				resp, err := ReadRegularMessage(upstreamConn)
				if err != nil {
					return fmt.Errorf("error reading Describe response: %w", err)
				}
				if err := p.writeMessage(clientConn, resp.Type, resp.Payload); err != nil {
					return fmt.Errorf("error forwarding Describe response: %w", err)
				}
				if resp.Type != MsgParameterDescription {
					break // ParameterDescription was last, or we got RowDescription/NoData
				}
			}

		case MsgFlush:
			p.logDebug("  -> Got Flush message, forwarding to upstream\n")
			if err := p.writeMessage(upstreamConn, nextMsg.Type, nextMsg.Payload); err != nil {
				return fmt.Errorf("error forwarding Flush: %w", err)
			}

		case MsgClose:
			p.logDebug("  -> Got Close message, forwarding to upstream\n")
			if err := p.writeMessage(upstreamConn, nextMsg.Type, nextMsg.Payload); err != nil {
				return fmt.Errorf("error forwarding Close: %w", err)
			}
			// Wait for CloseComplete
			resp, err := ReadRegularMessage(upstreamConn)
			if err != nil {
				return fmt.Errorf("error reading CloseComplete: %w", err)
			}
			if resp.Type != MsgCloseComplete {
				return fmt.Errorf("expected CloseComplete, got %c", resp.Type)
			}
			if err := p.writeMessage(clientConn, MsgCloseComplete, nil); err != nil {
				return fmt.Errorf("error sending CloseComplete: %w", err)
			}

		case MsgBind:
			p.logDebug("  -> Got Bind message, forwarding to upstream\n")
			// Forward Bind to upstream
			if err := p.writeMessage(upstreamConn, nextMsg.Type, nextMsg.Payload); err != nil {
				return fmt.Errorf("error forwarding Bind: %w", err)
			}
			// Wait for BindComplete
			resp, err := ReadRegularMessage(upstreamConn)
			if err != nil {
				return fmt.Errorf("error reading BindComplete: %w", err)
			}
			if resp.Type != MsgBindComplete {
				return fmt.Errorf("expected BindComplete, got %c", resp.Type)
			}
			// Forward BindComplete to client
			p.logDebug("  -> Forwarding BindComplete to client\n")
			if err := p.writeMessage(clientConn, MsgBindComplete, nil); err != nil {
				return fmt.Errorf("error sending BindComplete: %w", err)
			}
			// Now handle messages after Bind
			goto afterBind

		case MsgQuery:
			p.logDebug("  -> Got Query message, handling as simple query\n")
			// Client switched to simple query - forward to upstream
			return p.forwardAndDrainSimpleQuery(nextMsg, clientConn, upstreamConn)

		default:
			p.logDebug("  -> Unexpected message type %c after ParseComplete, forwarding to upstream\n", nextMsg.Type)
			// Forward to upstream
			if err := p.writeMessage(upstreamConn, nextMsg.Type, nextMsg.Payload); err != nil {
				return err
			}
			// Drain response and forward
			resp, err := ReadRegularMessage(upstreamConn)
			if err != nil {
				return err
			}
			if err := p.writeMessage(clientConn, resp.Type, resp.Payload); err != nil {
				return err
			}
		}
	}

afterBind:
	// Step 3: Handle messages after Bind - wait for Execute
	for {
		p.logDebug("  -> Reading message after Bind...\n")
		nextMsg, err := ReadRegularMessageFromReader(clientReader)
		if err != nil {
			return fmt.Errorf("error reading message after Bind: %w", err)
		}

		switch nextMsg.Type {
		case MsgDescribe:
			p.logDebug("  -> Got Describe message after Bind, forwarding to upstream\n")
			// Forward to upstream and drain response
			if err := p.writeMessage(upstreamConn, nextMsg.Type, nextMsg.Payload); err != nil {
				return err
			}
			resp, err := ReadRegularMessage(upstreamConn)
			if err != nil {
				return err
			}
			if err := p.writeMessage(clientConn, resp.Type, resp.Payload); err != nil {
				return err
			}

		case MsgFlush:
			p.logDebug("  -> Got Flush message after Bind, forwarding to upstream\n")
			if err := p.writeMessage(upstreamConn, nextMsg.Type, nextMsg.Payload); err != nil {
				return err
			}

		case MsgClose:
			p.logDebug("  -> Got Close message after Bind, forwarding to upstream\n")
			if err := p.writeMessage(upstreamConn, nextMsg.Type, nextMsg.Payload); err != nil {
				return err
			}
			resp, err := ReadRegularMessage(upstreamConn)
			if err != nil {
				return err
			}
			if resp.Type != MsgCloseComplete {
				return fmt.Errorf("expected CloseComplete, got %c", resp.Type)
			}
			if err := p.writeMessage(clientConn, MsgCloseComplete, nil); err != nil {
				return err
			}

		case MsgExecute:
			p.logDebug("  -> Got Execute message - MOCKING EXECUTE\n")
			// NOW WE MOCK: Send mock response instead of forwarding to upstream
			// Don't send anything to upstream for Execute - we mock the result

			// Determine row count
			rowCount := 1
			if mock.Channel == types.ReadPostgreSQL {
				if mock.ReturnsFile != "" {
					p.loader.BaseDir = mock.BaseDir
					if payload, err := p.loader.Load(mock.ReturnsFile); err == nil {
						rows := p.extractRowsFromPayload(payload)
						rowCount = len(rows)
					}
				} else if mock.ReturnsEmpty {
					rowCount = 0
				}
			}

			// For READ operations or WRITE with RETURNING, send result set
			if mock.Channel == types.ReadPostgreSQL || len(p.extractReturningColumns(query)) > 0 {
				p.logDebug("  -> Sending mock result set\n")
				if err := p.sendMockResultSetForExtended(clientConn, mock, query); err != nil {
					return fmt.Errorf("error sending mock result: %w", err)
				}
			}
			// For WRITE without RETURNING, don't send NoData - just CommandComplete
			// NoData is only sent in response to a portal Describe, not after Execute

			// Send CommandComplete
			cmdTag := p.createCommandCompleteTag(query, rowCount)
			p.logDebug("  -> Sending CommandComplete: %s\n", cmdTag)
			if _, err := clientConn.Write(CreateCommandComplete(cmdTag)); err != nil {
				return fmt.Errorf("error sending CommandComplete: %w", err)
			}

			// Now wait for Sync
			goto afterExecute

		default:
			p.logDebug("  -> Unexpected message type %c after Bind, forwarding to upstream\n", nextMsg.Type)
			// Forward to upstream
			if err := p.writeMessage(upstreamConn, nextMsg.Type, nextMsg.Payload); err != nil {
				return err
			}
			resp, err := ReadRegularMessage(upstreamConn)
			if err != nil {
				return err
			}
			if err := p.writeMessage(clientConn, resp.Type, resp.Payload); err != nil {
				return err
			}
		}
	}

afterExecute:
	// Step 4: Read and handle Sync message
	p.logDebug("  -> Reading Sync message...\n")
	syncMsg, err := ReadRegularMessageFromReader(clientReader)
	if err != nil {
		return fmt.Errorf("error reading Sync: %w", err)
	}
	if syncMsg.Type != MsgSync {
		return fmt.Errorf("expected Sync message, got %c", syncMsg.Type)
	}

	// Forward Sync to upstream and drain ReadyForQuery
	p.logDebug("  -> Got Sync, forwarding to upstream and draining ReadyForQuery\n")
	if err := p.writeMessage(upstreamConn, syncMsg.Type, syncMsg.Payload); err != nil {
		return fmt.Errorf("error forwarding Sync: %w", err)
	}

	// Drain ReadyForQuery from upstream
	resp, err := ReadRegularMessage(upstreamConn)
	if err != nil {
		return fmt.Errorf("error reading ReadyForQuery: %w", err)
	}
	if resp.Type != MsgReadyForQuery {
		return fmt.Errorf("expected ReadyForQuery, got %c", resp.Type)
	}

	// Send ReadyForQuery to client
	p.logDebug("  -> Sending ReadyForQuery\n")
	if err := p.writeMessage(clientConn, MsgReadyForQuery, resp.Payload); err != nil {
		return fmt.Errorf("error sending ReadyForQuery: %w", err)
	}
	p.logDebug("  -> Successfully handled extended query with mock Execute\n")

	return nil
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
	logger.Debug("handleInterceptedMessage: type=%c", msg.Type)
	p.logDebug("handleInterceptedMessage: type=%c\n", msg.Type)

	switch msg.Type {
	case MsgQuery:
		query := string(msg.Payload)
		tableName := p.extractTable(query)
		logger.Debug("Simple query for table %s: %s", tableName, query[:min(50, len(query))])
		p.registry.CheckNegativeMocks(tableName, query)
		mock, found := p.registry.FindMock(tableName, query)

		if !found {
			p.logDebug("  -> Mock not found, forwarding to upstream\n")
			return p.writeMessage(upstreamConn, msg.Type, msg.Payload)
		}

		// Store the actual query in the mock for proper hit tracking
		if mock.SQL == "" {
			mock.SQL = query
		}

		// Execute VERIFY rules if any
		if len(mock.Verify) > 0 {
			if err := verify.VerifySQL(query, mock.Verify); err != nil {
				p.logDebug("  -> VERIFY failed: %v\n", err)
				return p.sendErrorResponse(clientConn, fmt.Sprintf("VERIFY failed: %v", err))
			}
			p.logDebug("  -> All VERIFY rules passed\n")
		}

		p.logDebug("  -> Mocking query for table %s\n", tableName)
		return p.sendMockResponse(clientConn, mock, MsgQuery, query)

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
		// Use PeekMock here - don't increment hit count yet for extended query protocol
		// The hit count will be incremented when we actually execute (Bind/Execute)
		mock, found := p.registry.PeekMock(tableName, query)
		if !found {
			p.logDebug("  -> Mock not found for table %s, forwarding to upstream\n", tableName)
			return p.writeMessage(upstreamConn, msg.Type, msg.Payload)
		}

		p.logDebug("  -> Found mock for table %s (hit count NOT incremented yet)\n", tableName)

		// Store the actual query in the mock for later use (e.g., for RETURNING clause detection)
		if mock.SQL == "" {
			mock.SQL = query
		}

		// Execute VERIFY rules if any
		if len(mock.Verify) > 0 {
			if err := verify.VerifySQL(query, mock.Verify); err != nil {
				p.logDebug("  -> VERIFY failed: %v\n", err)
				return p.sendErrorResponse(clientConn, fmt.Sprintf("VERIFY failed: %v", err))
			}
			p.logDebug("  -> All VERIFY rules passed\n")
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

			case MsgSync:
				p.logDebug("  -> Got Sync message after Parse\n") // Client is synchronizing after Parse without Bind/Execute
				// Send ReadyForQuery but continue reading for more messages (e.g., Close)
				if err := p.sendReadyForQuery(clientConn); err != nil {
					return fmt.Errorf("error sending ReadyForQuery after Parse+Sync: %w", err)
				}
				// Continue reading for Close or other messages
				p.logDebug("  -> ReadyForQuery sent, waiting for Close/Sync\n")
				for {
					nextMsg, err := ReadRegularMessageFromReader(clientReader)
					if err != nil {
						p.logDebug("  -> Error reading message after Parse+Sync: %v\n", err)
						return fmt.Errorf("error reading message after Parse+Sync: %w", err)
					}
					p.logDebug("  -> Read message type %c after ReadyForQuery\n", nextMsg.Type)
					switch nextMsg.Type {
					case MsgClose:
						p.logDebug("  -> Got Close message after Parse+Sync, sending CloseComplete\n")
						if err := p.writeMessage(clientConn, MsgCloseComplete, nil); err != nil {
							return fmt.Errorf("error sending CloseComplete: %w", err)
						}
						p.logDebug("  -> CloseComplete sent\n")
					case MsgSync:
						p.logDebug("  -> Got Sync message after Close, sending ReadyForQuery\n")
						if err := p.sendReadyForQuery(clientConn); err != nil {
							return fmt.Errorf("error sending ReadyForQuery after Close: %w", err)
						}
						p.logDebug("  -> ReadyForQuery sent after Close, returning\n")
						return nil // Full cycle complete
					case MsgBind:
						// Two-phase protocol: Parse+Describe+Sync, then Bind+Execute+Sync
						// asyncpg caches the prepared statement after the first phase,
						// then executes via Bind in the second phase
						p.logDebug("  -> Got Bind message after Parse+Sync (two-phase protocol)\n")
						if err := p.writeMessage(clientConn, MsgBindComplete, nil); err != nil {
							return fmt.Errorf("error sending BindComplete: %w", err)
						}
						goto afterBind
					default:
						p.logDebug("  -> Unexpected message type %c after Parse+Sync\n", nextMsg.Type)
						return fmt.Errorf("expected Close or Sync after Parse+Sync, got %c", nextMsg.Type)
					}
				}

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
				// Use the query from the mock or the parsed query
				queryToUse := mock.SQL
				if queryToUse == "" {
					queryToUse = query
				}
				p.logDebug("  -> Using query: %s\n", queryToUse[:min(100, len(queryToUse))])

				// Handle differently based on operation type
				if mock.Channel == types.WritePostgreSQL {
					// For WRITE operations (INSERT/UPDATE/DELETE)
					// Check if there's a RETURNING clause
					hasReturning := len(p.extractReturningColumns(queryToUse)) > 0
					p.logDebug("  -> WRITE operation, hasReturning=%v\n", hasReturning)

					if hasReturning {
						// Send result set for RETURNING clause
						p.logDebug("  -> Sending mock result set for RETURNING clause\n")
						if err := p.sendMockResultSetForExtended(clientConn, mock, queryToUse); err != nil {
							p.logDebug("  -> Error sending mock result: %v\n", err)
							return fmt.Errorf("error sending mock result: %w", err)
						}
					}
					// For WRITE without RETURNING, just send CommandComplete (no NoData)
				} else {
					// For READ operations, send result set
					p.logDebug("  -> Sending mock result set for READ operation\n")
					if err := p.sendMockResultSetForExtended(clientConn, mock, queryToUse); err != nil {
						p.logDebug("  -> Error sending mock result: %v\n", err)
						return fmt.Errorf("error sending mock result: %w", err)
					}
				}

				// Send CommandComplete (without ReadyForQuery - we'll send that after Sync)
				p.logDebug("  -> Sending CommandComplete\n")
				// Determine the correct tag based on SQL operation type
				rowCount := 1 // Default to 1 row for most operations
				if mock.Channel == types.ReadPostgreSQL {
					// For reads, count the actual rows from the payload
					p.loader.BaseDir = mock.BaseDir
					if mock.ReturnsFile != "" {
						if payload, err := p.loader.Load(mock.ReturnsFile); err == nil {
							rows := p.extractRowsFromPayload(payload)
							rowCount = len(rows)
						}
					} else if mock.ReturnsEmpty {
						rowCount = 0
					}
				}
				// For WRITE operations, we always expect 1 row to be affected
				cmdTag := p.createCommandCompleteTag(queryToUse, rowCount)
				p.logDebug("  -> CommandComplete tag: %s\n", cmdTag)
				if _, err := clientConn.Write(CreateCommandComplete(cmdTag)); err != nil {
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
		// Read Close(P) and/or Sync messages
		// asyncpg sends: Execute + Close(P) + Sync for named portals
		// Some drivers send just: Execute + Sync for unnamed portals
		for {
			p.logDebug("  -> Reading message after Execute...\n")
			syncMsg, err := ReadRegularMessageFromReader(clientReader)
			if err != nil {
				p.logDebug("  -> Error reading message after Execute: %v\n", err)
				return fmt.Errorf("error reading Sync: %w", err)
			}
			if syncMsg.Type == MsgSync {
				p.logDebug("  -> Got Sync message, sending ReadyForQuery\n")
				break
			}
			if syncMsg.Type == MsgClose {
				// Close(P) - portal close, send CloseComplete and continue
				p.logDebug("  -> Got Close message after Execute, sending CloseComplete\n")
				if err := p.writeMessage(clientConn, MsgCloseComplete, nil); err != nil {
					return fmt.Errorf("error sending CloseComplete: %w", err)
				}
				continue
			}
			p.logDebug("  -> Expected Sync, got %c\n", syncMsg.Type)
			return fmt.Errorf("expected Sync message, got %c", syncMsg.Type)
		}

		// Send ReadyForQuery
		p.logDebug("  -> Sending ReadyForQuery (transaction state: I)\n")
		if err := p.writeMessage(clientConn, MsgReadyForQuery, []byte{'I'}); err != nil {
			return fmt.Errorf("error sending ReadyForQuery: %w", err)
		}
		p.logDebug("  -> Successfully handled extended query\n")

		// Increment hit count now that we've successfully executed the query
		if mock != nil {
			p.registry.IncrementHit(mock)
			p.logDebug("  -> Hit count incremented for table %s\n", mock.Table)
		}

		return nil

	default:
		p.logDebug("  -> Unknown message type %c, forwarding to upstream\n", msg.Type)
		return p.writeMessage(upstreamConn, msg.Type, msg.Payload)
	}
}

// createCommandCompleteTag creates the appropriate CommandComplete tag based on SQL operation type
// Returns the tag string (e.g., "INSERT 0 1", "UPDATE 3", "DELETE 1", "SELECT 2")
// For INSERT: "INSERT <oid> <rows>" (oid is typically 0 for user tables)
// For UPDATE: "UPDATE <rows>"
// For DELETE: "DELETE <rows>"
// For SELECT: "SELECT <rows>"
func (p *Proxy) createCommandCompleteTag(sql string, rowCount int) string {
	logger.Debug("createCommandCompleteTag called with sql: %s, rowCount: %d", sql[:min(50, len(sql))], rowCount)

	if sql == "" {
		return fmt.Sprintf("SELECT %d", rowCount)
	}

	upperSQL := strings.ToUpper(strings.TrimSpace(sql))

	// Check for INSERT
	if strings.HasPrefix(upperSQL, "INSERT") {
		tag := fmt.Sprintf("INSERT 0 %d", rowCount)
		logger.Debug("Generated INSERT tag: %s", tag)
		return tag
	}

	// Check for UPDATE
	if strings.HasPrefix(upperSQL, "UPDATE") {
		tag := fmt.Sprintf("UPDATE %d", rowCount)
		logger.Debug("Generated UPDATE tag: %s", tag)
		return tag
	}

	// Check for DELETE
	if strings.HasPrefix(upperSQL, "DELETE") {
		tag := fmt.Sprintf("DELETE %d", rowCount)
		logger.Debug("Generated DELETE tag: %s", tag)
		return tag
	}

	// Default to SELECT for other operations
	return fmt.Sprintf("SELECT %d", rowCount)
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

// sendErrorResponse sends a PostgreSQL error response to the client
func (p *Proxy) sendErrorResponse(conn net.Conn, message string) error {
	// PostgreSQL ErrorResponse message format
	// 'E' message type, followed by length, then a series of null-terminated field pairs
	// Common fields:
	// S - Severity (ERROR, FATAL, PANIC)
	// C - SQLSTATE error code
	// M - Primary human-readable error message
	// H - Hint
	// P - Position (optional)
	// \0 - Terminator

	var payload []byte
	payload = append(payload, 'S') // Severity field
	payload = append(payload, []byte("ERROR")...)
	payload = append(payload, 0)

	payload = append(payload, 'C')                // SQLSTATE code
	payload = append(payload, []byte("42601")...) // syntax_error
	payload = append(payload, 0)

	payload = append(payload, 'M') // Message field
	payload = append(payload, []byte(message)...)
	payload = append(payload, 0)

	payload = append(payload, 0) // Terminator

	return p.writeMessage(conn, MsgErrorResponse, payload)
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

// inferColumnsForTable infers column names for a table from cached schema or returns defaults
func (p *Proxy) inferColumnsForTable(table string) []string {
	// Try to get columns from schema cache first
	if columns, ok := p.schemaCache[table]; ok && len(columns) > 0 {
		result := make([]string, len(columns))
		for i, col := range columns {
			result[i] = col.Field
		}
		return result
	}

	// Default fallback columns for unknown tables
	return []string{"id", "created_at", "updated_at"}
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

	// Get dynamic table list from registry (from EXPECT statements)
	// This allows any table name from .linespec files to be recognized
	knownTables := p.registry.GetTables()

	// Check each registered table to see if it appears in the query
	for _, table := range knownTables {
		// Escape special regex characters in table names
		escapedTable := regexp.QuoteMeta(table)
		re := regexp.MustCompile(`\b` + escapedTable + `\b`)
		if re.MatchString(q) {
			return table
		}
	}

	// Fallback: Try to extract from SQL keywords (FROM, INTO, UPDATE)
	// This handles tables that weren't explicitly registered in EXPECT statements
	words := strings.Fields(q)
	for i, word := range words {
		if word == "from" || word == "into" || word == "update" || word == "table" {
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

// getKnownTables returns the list of known tables from registry or schema cache
func (p *Proxy) getKnownTables() []string {
	// First, try to get tables from the registry (dynamically from DSL/linespec)
	if p.registry != nil {
		registryTables := p.registry.GetTables()
		if len(registryTables) > 0 {
			return registryTables
		}
	}

	// Fallback to schema cache if available
	if len(p.schemaCache) > 0 {
		tables := make([]string, 0, len(p.schemaCache))
		for table := range p.schemaCache {
			tables = append(tables, table)
		}
		return tables
	}

	return []string{}
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

// logDebug writes a debug message to stderr when debug mode is enabled.
// It is a true no-op when debug mode is off.
func (p *Proxy) logDebug(format string, args ...interface{}) {
	if !logger.IsDebug() {
		return
	}
	fmt.Fprintf(debugWriter, format, args...)
	debugWriter.Flush()
}

// handleDescribe handles a Describe message and sends the appropriate response
func (p *Proxy) handleDescribe(conn net.Conn, mock *types.ExpectStatement, actualQuery string) error {
	p.logDebug("  -> handleDescribe called, mock.Channel=%s, actualQuery=%s\n", mock.Channel, actualQuery[:min(50, len(actualQuery))])
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
		p.logDebug("  -> Write operation, checking for RETURNING clause\n")
		returningColumns := p.extractReturningColumns(sqlToCheck)
		p.logDebug("  -> Found %d RETURNING columns\n", len(returningColumns))
		if len(returningColumns) > 0 {
			// Send RowDescription for the RETURNING columns
			return p.result.SendRowDescription(conn, returningColumns)
		}
	}

	// For write operations without RETURNING, send NoData
	p.logDebug("  -> Sending NoData for write without RETURNING\n")
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

// sendReadyForQuery sends ReadyForQuery message to indicate transaction is idle
func (p *Proxy) sendReadyForQuery(conn net.Conn) error {
	return p.startup.sendReadyForQuery(conn)
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
	rowCount := 1
	if mock.Channel == types.ReadPostgreSQL {
		p.loader.BaseDir = mock.BaseDir
		if mock.ReturnsFile != "" {
			if payload, err := p.loader.Load(mock.ReturnsFile); err == nil {
				rows := p.extractRowsFromPayload(payload)
				rowCount = len(rows)
			}
		} else if mock.ReturnsEmpty {
			rowCount = 0
		}
	}
	cmdTag := p.createCommandCompleteTag(query, rowCount)
	if err := p.result.SendCommandComplete(conn, cmdTag); err != nil {
		return fmt.Errorf("error sending CommandComplete: %w", err)
	}

	p.logDebug("  -> Successfully handled simple query\n")
	return nil
}

// forwardMessage forwards a message to upstream
func (p *Proxy) forwardMessage(upstreamConn net.Conn, msgType byte, lengthBuf, payload []byte) error {
	msgBytes := make([]byte, 0, 1+4+len(payload))
	msgBytes = append(msgBytes, msgType)
	msgBytes = append(msgBytes, lengthBuf...)
	msgBytes = append(msgBytes, payload...)
	_, err := upstreamConn.Write(msgBytes)
	return err
}

// extractParseInfo extracts statement name and query from a Parse message
// Parse format: [stmt_name]\0 [query]\0 [num_params] [param_types...]
func (p *Proxy) extractParseInfo(payload []byte) (stmtName, query string) {
	if len(payload) == 0 {
		return "", ""
	}
	pos := 0
	// Read statement name (until null)
	stmtStart := pos
	for pos < len(payload) && payload[pos] != 0 {
		pos++
	}
	if pos >= len(payload) {
		return "", ""
	}
	stmtName = string(payload[stmtStart:pos])
	pos++ // Skip null
	// Read query (until null)
	if pos >= len(payload) {
		return stmtName, ""
	}
	queryStart := pos
	for pos < len(payload) && payload[pos] != 0 {
		pos++
	}
	if pos > queryStart {
		query = string(payload[queryStart:pos])
	}
	return stmtName, query
}

// extractBindInfo extracts portal name and statement name from a Bind message
// Bind format: [portal]\0 [stmt_name]\0 [param_format_count] [...] [param_count] [...] [result_format_count] [...]
func (p *Proxy) extractBindInfo(payload []byte) (portalName, stmtName string) {
	if len(payload) == 0 {
		return "", ""
	}
	pos := 0
	// Read portal name (until null)
	portalStart := pos
	for pos < len(payload) && payload[pos] != 0 {
		pos++
	}
	if pos >= len(payload) {
		return "", ""
	}
	portalName = string(payload[portalStart:pos])
	pos++ // Skip null
	// Read statement name (until null)
	if pos >= len(payload) {
		return portalName, ""
	}
	stmtStart := pos
	for pos < len(payload) && payload[pos] != 0 {
		pos++
	}
	if pos > stmtStart {
		stmtName = string(payload[stmtStart:pos])
	}
	return portalName, stmtName
}

// extractExecuteInfo extracts portal name from an Execute message
// Execute format: [portal]\0 [max_rows (int32)]
func (p *Proxy) extractExecuteInfo(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	pos := 0
	// Read portal name (until null)
	portalStart := pos
	for pos < len(payload) && payload[pos] != 0 {
		pos++
	}
	if pos > portalStart {
		return string(payload[portalStart:pos])
	}
	return ""
}

// sendMockExecuteResponse sends the mock result for an Execute message
func (p *Proxy) sendMockExecuteResponse(clientConn net.Conn, mock *types.ExpectStatement) error {
	query := mock.SQL
	if query == "" {
		query = fmt.Sprintf("INSERT INTO %s", mock.Table)
	}

	p.logDebug("  -> Sending mock result for query: %s\n", query[:min(50, len(query))])

	// Handle based on operation type
	if mock.Channel == types.WritePostgreSQL {
		// For WRITE operations (INSERT/UPDATE/DELETE)
		hasReturning := len(p.extractReturningColumns(query)) > 0
		p.logDebug("  -> WRITE operation, hasReturning=%v\n", hasReturning)

		if hasReturning {
			// Send result set for RETURNING clause
			if err := p.sendMockResultSetForExtended(clientConn, mock, query); err != nil {
				return err
			}
		}
		// For WRITE without RETURNING, just send CommandComplete
	} else {
		// For READ operations, send result set
		if err := p.sendMockResultSetForExtended(clientConn, mock, query); err != nil {
			return err
		}
	}

	// Send CommandComplete
	rowCount := 1
	if mock.Channel == types.ReadPostgreSQL {
		p.loader.BaseDir = mock.BaseDir
		if mock.ReturnsFile != "" {
			if payload, err := p.loader.Load(mock.ReturnsFile); err == nil {
				rows := p.extractRowsFromPayload(payload)
				rowCount = len(rows)
			}
		} else if mock.ReturnsEmpty {
			rowCount = 0
		}
	}
	cmdTag := p.createCommandCompleteTag(query, rowCount)
	if _, err := clientConn.Write(CreateCommandComplete(cmdTag)); err != nil {
		return err
	}

	p.logDebug("  -> Sent CommandComplete: %s\n", cmdTag)

	// Note: ReadyForQuery is sent separately when Sync arrives
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
