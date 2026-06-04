package postgresql

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/livecodelife/linespec/pkg/dsl"
	"github.com/livecodelife/linespec/pkg/interpolate"
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
	activeConns  sync.Map                  // clientConn (net.Conn) -> upstreamConn (net.Conn)
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

// MockedPortal pairs a matched mock with the actual SQL query from Parse,
// so sendMockExecuteResponse can use the real SELECT/INSERT/UPDATE text
// rather than falling back to a synthetic "INSERT INTO <table>" string.
// DescribeForwarded records whether a Describe was forwarded for this statement
// before Bind, meaning the upstream already sent RowDescription to the client —
// the Execute response must therefore omit RowDescription to avoid a duplicate.
type MockedPortal struct {
	Mock              *types.ExpectStatement
	Query             string  // actual SQL captured at Parse/Bind time
	DescribeForwarded bool    // true if Describe was forwarded to upstream for this stmt
	ResultFormatCodes []int16 // per-column result format codes from Bind (0=text, 1=binary)
}

// ConnectionState tracks prepared statements and mocked portals per connection
// This is needed for the extended query protocol where we eavesdrop on Parse
// but only intercept at Bind/Execute
type ConnectionState struct {
	preparedStatements  map[string]string        // statement name -> query
	describedStatements map[string]bool          // statement name -> Describe was forwarded to upstream
	mockedPortals       map[string]*MockedPortal // portal name -> mock + actual query
	justMockedExecute   bool                     // track if we just mocked an Execute
	mockOnlyStatements  map[string]bool          // statements handled locally (never forwarded to upstream)
	stmtParamOIDs       map[string][]uint32      // statement name -> OIDs declared in Parse message
	inTransaction       bool                     // true between BEGIN and COMMIT/ROLLBACK
}

// txStatus returns the PostgreSQL transaction status byte for ReadyForQuery.
// 'T' = in transaction, 'I' = idle.
func (s *ConnectionState) txStatus() byte {
	if s.inTransaction {
		return 'T'
	}
	return 'I'
}

// NewConnectionState creates a new connection state
func NewConnectionState() *ConnectionState {
	return &ConnectionState{
		preparedStatements:  make(map[string]string),
		describedStatements: make(map[string]bool),
		mockedPortals:       make(map[string]*MockedPortal),
		justMockedExecute:   false,
		mockOnlyStatements:  make(map[string]bool),
		stmtParamOIDs:       make(map[string][]uint32),
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

// SetResolver wires an interpolate.Resolver into the payload loader so that
// ${VAR} tokens in RETURNS payload files are resolved at runtime.
func (p *Proxy) SetResolver(resolver *interpolate.Resolver) {
	p.loader = dsl.NewPayloadLoaderWithResolver("", resolver)
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

// ResetConnections closes all active client and upstream connections.
// Called on registry reload so that the service's connection pool reconnects
// with fresh state, eliminating stale mock-only prepared statement mappings
// that would otherwise persist across test boundaries.
func (p *Proxy) ResetConnections() {
	p.activeConns.Range(func(key, value any) bool {
		if cc, ok := key.(net.Conn); ok {
			cc.Close()
		}
		if uc, ok := value.(net.Conn); ok {
			uc.Close()
		}
		return true
	})
	logger.Debug("PostgreSQL Proxy: reset all active connections for test isolation")
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
	p.activeConns.Store(clientConn, upstreamConn)
	defer func() {
		p.activeConns.Delete(clientConn)
		upstreamConn.Close()
	}()
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
			stmtName, query, paramOIDs := p.extractParseInfo(payload)
			if query != "" {
				state.preparedStatements[stmtName] = query
				p.logDebug("  -> Tracked Parse: stmtName='%s', query='%s...'\n", stmtName, query[:min(50, len(query))])
				// Track transaction state so mocked responses carry the correct status byte.
				q := strings.TrimSpace(strings.ToUpper(query))
				if strings.HasPrefix(q, "BEGIN") {
					state.inTransaction = true
				} else if strings.HasPrefix(q, "COMMIT") || strings.HasPrefix(q, "ROLLBACK") {
					state.inTransaction = false
				}
			}
			// Store explicit parameter OIDs from the Parse message so we can echo
			// them back in mock-only ParameterDescription responses. When the client
			// (e.g. tokio-postgres) discovers types via a type-lookup and re-parses
			// with explicit OIDs, returning OID=0 again would trigger another lookup loop.
			if len(paramOIDs) > 0 {
				state.stmtParamOIDs[stmtName] = paramOIDs
			}
			// If this query will be mocked, handle ParseComplete locally so we never
			// forward Parse to upstream. This prevents stale ParseComplete messages from
			// the relay goroutine arriving on idle connections after mocked Execute completes.
			//
			// Exception: if the query has parameters but we cannot resolve their OIDs
			// (no explicit OIDs in the Parse message and no $N::TYPE casts in the query),
			// forward Parse to upstream so we can relay the real ParameterDescription.
			// This is required for drivers like tokio-postgres that send num_params=0 in
			// Parse and rely on the server's ParameterDescription to determine types; when
			// the server returns OID=0 (unspecified), tokio-postgres calls typeinfo(0) which
			// looks up OID 0 in pg_catalog, finds no rows, and raises "unexpected message".
			if query != "" {
				if _, mocked := p.peekMock(query, nil); mocked {
					canResolveOIDs := len(paramOIDs) > 0 || len(p.extractParameterTypes(query)) > 0 || countSQLParams(query) == 0
					if canResolveOIDs {
						state.mockOnlyStatements[stmtName] = true
						p.logDebug("  -> Mock-only Parse for '%s': sending local ParseComplete\n", stmtName)
						if err := p.writeMessage(clientConn, MsgParseComplete, nil); err != nil {
							p.logDebug("  -> Error sending ParseComplete: %v\n", err)
							return
						}
						continue
					}
					// Cannot resolve OIDs locally — forward Parse to upstream for real
					// ParameterDescription, but Bind/Execute will still be intercepted.
					p.logDebug("  -> Mocked '%s' but OIDs unknown, forwarding Parse to upstream\n", stmtName)
					state.mockOnlyStatements[stmtName] = false
				}
			}
			state.mockOnlyStatements[stmtName] = false
			if err := p.forwardMessage(upstreamConn, msgType, lengthBuf, payload); err != nil {
				p.logDebug("  -> Error forwarding Parse: %v\n", err)
				return
			}

		case MsgDescribe, MsgClose, MsgFlush:
			// For Describe, record that upstream will send RowDescription (used by Execute).
			// If the statement/portal is mock-only, respond locally instead of going upstream.
			if msgType == MsgDescribe && len(payload) > 0 {
				if payload[0] == 'S' {
					stmtName := ""
					if len(payload) > 1 {
						nullIdx := 1
						for nullIdx < len(payload) && payload[nullIdx] != 0 {
							nullIdx++
						}
						stmtName = string(payload[1:nullIdx])
					}
					if state.mockOnlyStatements[stmtName] {
						// Respond locally without going to upstream.
						// OID resolution priority:
						//  1. OIDs explicitly sent by the client in the Parse message
						//     (e.g. tokio-postgres re-parses after a type-lookup with real OIDs)
						//  2. OIDs inferred from $N::TYPE casts in the query text
						//  3. Unspecified (OID=0) — client falls back to text format
						query := state.preparedStatements[stmtName]
						var paramDescPayload []byte
						if clientOIDs := state.stmtParamOIDs[stmtName]; len(clientOIDs) > 0 {
							p.logDebug("  -> Mock-only Describe '%s': ParameterDescription(clientOIDs=%v)\n", stmtName, clientOIDs)
							paramDescPayload = buildParameterDescriptionFromOIDs(clientOIDs)
						} else if oids := p.extractParameterTypes(query); len(oids) > 0 {
							p.logDebug("  -> Mock-only Describe '%s': ParameterDescription(OIDs=%v)\n", stmtName, oids)
							paramDescPayload = buildParameterDescriptionFromOIDs(oids)
						} else {
							paramCount := countSQLParams(query)
							p.logDebug("  -> Mock-only Describe '%s': ParameterDescription(%d)\n", stmtName, paramCount)
							paramDescPayload = buildParameterDescription(paramCount)
						}
						if err := p.writeMessage(clientConn, MsgParameterDescription, paramDescPayload); err != nil {
							p.logDebug("  -> Error sending ParameterDescription: %v\n", err)
							return
						}
						// SELECT and any query with RETURNING expect RowDescription so the client
						// can set up the row scanner. Pure writes without RETURNING get NoData.
						if queryReturnsRows(query) {
							cols := p.extractSelectColumns(query)
							if len(cols) == 0 {
								cols = []string{"id"}
							}
							p.logDebug("  -> Mock-only Describe '%s': sending RowDescription %v\n", stmtName, cols)
							// Load the mock payload to use value-based OID hints (e.g. UUID-valued
							// id columns must be declared as OID 2950 so typed clients like
							// tokio-postgres accept the value without a type-mismatch error).
							var sampleRow map[string]interface{}
							if mock, found := p.peekMock(query, nil); found && mock.ReturnsFile != "" {
								p.loader.BaseDir = mock.BaseDir
								if pld, err := p.loader.Load(mock.ReturnsFile); err == nil {
									if rows := p.extractRowsFromPayload(pld); len(rows) > 0 {
										sampleRow = rows[0]
									}
								}
							}
							if err := p.result.SendRowDescriptionWithHints(clientConn, cols, sampleRow); err != nil {
								p.logDebug("  -> Error sending RowDescription: %v\n", err)
								return
							}
							// Mark as described so Execute skips a second RowDescription.
							state.describedStatements[stmtName] = true
						} else {
							if err := p.writeMessage(clientConn, MsgNoData, nil); err != nil {
								p.logDebug("  -> Error sending NoData: %v\n", err)
								return
							}
						}
						continue
					}
					state.describedStatements[stmtName] = true
				} else if payload[0] == 'P' {
					// DescribePortal — respond locally for mocked portals to avoid forwarding
					// to upstream (the portal never existed there).
					portalName := ""
					if len(payload) > 1 {
						nullIdx := 1
						for nullIdx < len(payload) && payload[nullIdx] != 0 {
							nullIdx++
						}
						portalName = string(payload[1:nullIdx])
					}
					if mp, exists := state.mockedPortals[portalName]; exists {
						p.logDebug("  -> Mock-only Describe Portal for '%s': responding locally\n", portalName)
						if mp.Mock.Channel == types.ReadPostgreSQL {
							// READ mock: send RowDescription so Execute can skip it.
							// Use value-based hints so UUID-valued id columns are declared
							// as OID 2950 rather than INT4.
							cols := p.inferColumnsForTable(mp.Mock.Table)
							var sampleRow map[string]interface{}
							if mp.Mock.ReturnsFile != "" {
								p.loader.BaseDir = mp.Mock.BaseDir
								if pld, err := p.loader.Load(mp.Mock.ReturnsFile); err == nil {
									if rows := p.extractRowsFromPayload(pld); len(rows) > 0 {
										sampleRow = rows[0]
									}
								}
							}
							if err := p.result.SendRowDescriptionWithHints(clientConn, cols, sampleRow); err != nil {
								p.logDebug("  -> Error sending RowDescription: %v\n", err)
								return
							}
							mp.DescribeForwarded = true
						} else {
							// WRITE mock: no result set.
							if err := p.writeMessage(clientConn, MsgNoData, nil); err != nil {
								p.logDebug("  -> Error sending NoData: %v\n", err)
								return
							}
						}
						continue
					}
				}
			}
			// Close for a mock-only statement or portal must be handled locally —
			// the statement/portal never existed on upstream so forwarding it would error.
			if msgType == MsgClose && len(payload) > 0 {
				var name string
				if len(payload) > 1 {
					nullIdx := 1
					for nullIdx < len(payload) && payload[nullIdx] != 0 {
						nullIdx++
					}
					name = string(payload[1:nullIdx])
				}
				isMockOnly := false
				if payload[0] == 'S' {
					isMockOnly = state.mockOnlyStatements[name]
				} else if payload[0] == 'P' {
					_, isMockOnly = state.mockedPortals[name]
				}
				if isMockOnly {
					p.logDebug("  -> Mock-only Close for '%c' '%s': sending local CloseComplete\n", payload[0], name)
					if err := p.writeMessage(clientConn, MsgCloseComplete, nil); err != nil {
						p.logDebug("  -> Error sending CloseComplete: %v\n", err)
						return
					}
					continue
				}
			}
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

			// Check if this query is mocked — extract bind params for semantic matching
			bindParams := p.extractBindParams(payload)
			p.checkNegativeMocksForQuery(query, bindParams)
			if mock, found := p.findMock(query, bindParams); found {
				p.logDebug("  -> Intercepting Bind for mocked statement '%s' (portal '%s')\n", stmtName, portalName)

				// Execute VERIFY rules if any
				if len(mock.Verify) > 0 {
					if err := verify.VerifySQL(query, mock.Verify); err != nil {
						p.logDebug("  -> VERIFY failed: %v\n", err)
						p.registry.RecordVerifyError(fmt.Sprintf("WRITE:POSTGRESQL [%s]: %v", mock.Table, err))
						if err2 := p.sendErrorResponse(clientConn, fmt.Sprintf("VERIFY failed: %v", err)); err2 != nil {
							p.logDebug("  -> Error sending VERIFY error response: %v\n", err2)
						}
						continue
					}
					p.logDebug("  -> All VERIFY rules passed\n")
				}

				resultFmts := p.extractBindResultFormats(payload)
				state.mockedPortals[portalName] = &MockedPortal{
					Mock:              mock,
					Query:             query,
					DescribeForwarded: state.describedStatements[stmtName],
					ResultFormatCodes: resultFmts,
				}
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
			if mp, exists := state.mockedPortals[portalName]; exists {
				p.logDebug("  -> Intercepting Execute for mocked portal '%s' (describeForwarded=%v)\n", portalName, mp.DescribeForwarded)
				// Send mock result; skip RowDescription if Describe was already forwarded to upstream
				// (the upstream's RowDescription was already relayed to the client).
				if err := p.sendMockExecuteResponse(clientConn, mp.Mock, mp.Query, mp.DescribeForwarded, mp.ResultFormatCodes); err != nil {
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
				if err := p.sendReadyForQuery(clientConn, state.txStatus()); err != nil {
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
			// Track transaction state so mocked responses carry the correct status byte.
			q := strings.TrimSpace(strings.ToUpper(query))
			if strings.HasPrefix(q, "BEGIN") {
				state.inTransaction = true
			} else if strings.HasPrefix(q, "COMMIT") || strings.HasPrefix(q, "ROLLBACK") {
				state.inTransaction = false
			}
			p.checkNegativeMocksForQuery(query, nil)
			if mock, found := p.findMock(query, nil); found {
				p.logDebug("  -> Mocking simple query for table %s\n", p.extractTable(query))
				if err := p.sendMockResponse(clientConn, mock, MsgQuery, query, state.txStatus()); err != nil {
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

// sendMockResponse sends a mock response to the client.
// txStatus is the PostgreSQL transaction status byte ('T' = in transaction, 'I' = idle)
// to include in the ReadyForQuery message.
func (p *Proxy) sendMockResponse(clientConn net.Conn, mock *types.ExpectStatement, msgType byte, query string, txStatus byte) error {
	logger.Debug("PostgreSQL Proxy: Sending mock response for %s query", msgType)

	// Execute VERIFY rules if any
	if len(mock.Verify) > 0 {
		if err := verify.VerifySQL(query, mock.Verify); err != nil {
			p.registry.RecordVerifyError(fmt.Sprintf("WRITE:POSTGRESQL [%s]: %v", mock.Table, err))
			return p.sendErrorResponse(clientConn, fmt.Sprintf("VERIFY failed: %v", err))
		}
	}

	if msgType == MsgQuery {
		// Simple query protocol response
		return p.sendMockResultSimple(clientConn, mock, query, txStatus)
	} else if msgType == MsgParse {
		// Extended query: Send ParseComplete
		// The actual result will be sent when Execute arrives
		return p.writeMessage(clientConn, MsgParseComplete, nil)
	}

	return nil
}

// sendMockResultSimple sends a complete mock result for simple query protocol.
// txStatus is the transaction status byte for the ReadyForQuery message.
func (p *Proxy) sendMockResultSimple(clientConn net.Conn, mock *types.ExpectStatement, query string, txStatus byte) error {
	// Determine columns
	columns := []string{"id", "name", "email"}
	if mock.Table != "" {
		columns = p.inferColumnsForTable(mock.Table)
	}

	// For reads, send RowDescription + DataRows
	if mock.Channel == types.ReadPostgreSQL {
		if mock.ReturnsError {
			// RETURNS ERROR: send PostgreSQL ErrorResponse to simulate a DB error
			code := "42601" // default: syntax_error
			msg := "error"
			if mock.ReturnsErrorCode != "" {
				msg = mock.ReturnsErrorCode
				// Map common error codes to SQLSTATE
				switch mock.ReturnsErrorCode {
				case "cycle_detected":
					code = "55000" // program_limit_exceeded
				}
			}
			return p.sendErrorResponseWithCode(clientConn, code, msg)
		}
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
			// Extract columns from SQL SELECT clause to maintain consistent order
			// This ensures RowDescription and DataRow have matching column orders
			// (same logic as the extended query path in sendMockResultSetForExtended)
			if query != "" {
				sqlColumns := p.extractSelectColumns(query)
				if len(sqlColumns) > 0 {
					columns = sqlColumns
				}
			} else if len(rows) > 0 {
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
	rowCount := p.resolveRowCount(mock, 1)
	cmdTag := p.createCommandCompleteTag(query, rowCount)
	if err := p.result.SendCommandComplete(clientConn, cmdTag); err != nil {
		return err
	}

	// Send ReadyForQuery with the caller-supplied transaction status byte.
	return p.writeMessage(clientConn, MsgReadyForQuery, []byte{txStatus})
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
	return p.sendErrorResponseWithCode(conn, "42601", message)
}

// sendErrorResponseWithCode sends a PostgreSQL error response with a specific SQLSTATE code
func (p *Proxy) sendErrorResponseWithCode(conn net.Conn, sqlState, message string) error {
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

	payload = append(payload, 'C') // SQLSTATE code
	payload = append(payload, []byte(sqlState)...)
	payload = append(payload, 0)

	payload = append(payload, 'M') // Message field
	payload = append(payload, []byte(message)...)
	payload = append(payload, 0)

	payload = append(payload, 0) // Terminator

	return p.writeMessage(conn, MsgErrorResponse, payload)
}

// sendMockResultSetForExtended sends a mock result set for extended query protocol.
// It sends RowDescription + DataRow messages, unless skipRowDescription is true —
// that flag must be set when the client already received RowDescription via a
// forwarded Describe, to avoid sending it a second time.
// resultFormatCodes are the per-column result format codes extracted from the
// Bind message; they control how each DataRow value is encoded (0=text, 1=binary).
func (p *Proxy) sendMockResultSetForExtended(conn net.Conn, mock *types.ExpectStatement, actualQuery string, skipRowDescription bool, resultFormatCodes []int16) error {
	// Determine columns from mock or use defaults
	columns := []string{"id", "name", "email"}
	if mock.Table != "" {
		columns = p.inferColumnsForTable(mock.Table)
	}

	var rows []map[string]interface{}

	switch mock.Channel {
	case types.ReadPostgreSQL:
		if mock.ReturnsError {
			// RETURNS ERROR: send PostgreSQL ErrorResponse to simulate a DB error
			code := "42601"
			msg := "error"
			if mock.ReturnsErrorCode != "" {
				msg = mock.ReturnsErrorCode
				switch mock.ReturnsErrorCode {
				case "cycle_detected":
					code = "55000"
				}
			}
			return p.sendErrorResponseWithCode(conn, code, msg)
		}
		if mock.ReturnsEmpty {
			if actualQuery != "" {
				if sqlColumns := p.extractSelectColumns(actualQuery); len(sqlColumns) > 0 {
					columns = sqlColumns
				}
			}
			// rows stays nil — RowDescription is sent but no DataRow, producing SELECT 0
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

	// Send RowDescription unless the client already received it via a forwarded Describe.
	if !skipRowDescription {
		var sampleRow map[string]interface{}
		if len(rows) > 0 {
			sampleRow = rows[0]
		}
		if err := p.result.SendRowDescriptionWithHints(conn, columns, sampleRow); err != nil {
			return fmt.Errorf("error sending RowDescription: %w", err)
		}
	} else {
		p.logDebug("  -> Skipping RowDescription (already sent via Describe)\n")
	}

	// Send DataRow for each row, honouring the per-column result format codes
	// from the client's Bind message (0=text, 1=binary).  When resultFormatCodes
	// is nil and we own the RowDescription we fall back to name-based heuristics.
	for _, row := range rows {
		if err := p.result.SendDataRowWithFormats(conn, columns, row, resultFormatCodes); err != nil {
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

// resolveRowCount returns the row count for a CommandComplete tag.
//   - READ operations: number of rows from the RETURNS payload (0 if RETURNS EMPTY).
//   - WRITE operations: affected_rows from the RETURNS payload if present, otherwise defaultWrite.
func (p *Proxy) resolveRowCount(mock *types.ExpectStatement, defaultWrite int) int {
	if mock.Channel == types.ReadPostgreSQL {
		if mock.ReturnsFile != "" {
			p.loader.BaseDir = mock.BaseDir
			if payload, err := p.loader.Load(mock.ReturnsFile); err == nil {
				return len(p.extractRowsFromPayload(payload))
			}
		} else if mock.ReturnsEmpty {
			return 0
		}
		return 1
	}
	if mock.Channel == types.WritePostgreSQL && mock.ReturnsFile != "" {
		p.loader.BaseDir = mock.BaseDir
		if payload, err := p.loader.Load(mock.ReturnsFile); err == nil {
			if m, ok := payload.(map[string]interface{}); ok {
				if v, ok := m["affected_rows"]; ok {
					switch n := v.(type) {
					case int:
						return n
					case int64:
						return int(n)
					case float64:
						return int(n)
					}
				}
			}
		}
	}
	return defaultWrite
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

// logDebug writes a debug message to stderr when debug mode is enabled.
// It is a true no-op when debug mode is off.
func (p *Proxy) logDebug(format string, args ...interface{}) {
	if !logger.IsDebug() {
		return
	}
	fmt.Fprintf(debugWriter, format, args...)
	debugWriter.Flush()
}

// extractParameterTypes extracts PostgreSQL type OIDs for each $N parameter from SQL casts
// e.g., "$1::INTEGER" returns [23] for parameter 1
// e.g., "$1::INTEGER AND $2::VARCHAR" returns [23, 1043]
// countSQLParams returns the highest $N param index found in sql (so the number of params).
// queryReturnsRows reports whether the query will return rows — either because it's
// a SELECT or because it has a RETURNING clause (INSERT/UPDATE/DELETE ... RETURNING).
func queryReturnsRows(sql string) bool {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	if strings.HasPrefix(upper, "SELECT") {
		return true
	}
	return strings.Contains(upper, " RETURNING ")
}

func countSQLParams(sql string) int {
	re := regexp.MustCompile(`\$(\d+)`)
	matches := re.FindAllStringSubmatch(sql, -1)
	max := 0
	for _, m := range matches {
		if len(m) > 1 {
			var n int
			fmt.Sscanf(m[1], "%d", &n)
			if n > max {
				max = n
			}
		}
	}
	return max
}

// buildParameterDescription returns a ParameterDescription payload for n unspecified-type params.
// PostgreSQL format: Int16 count, then n Int32 OIDs (0 = unspecified).
func buildParameterDescription(n int) []byte {
	buf := make([]byte, 2+n*4)
	buf[0] = byte(n >> 8)
	buf[1] = byte(n)
	// OIDs are 0 (unspecified), already zeroed
	return buf
}

// buildParameterDescriptionFromOIDs encodes a ParameterDescription from explicit type OIDs.
func buildParameterDescriptionFromOIDs(oids []uint32) []byte {
	n := len(oids)
	buf := make([]byte, 2+n*4)
	buf[0] = byte(n >> 8)
	buf[1] = byte(n)
	for i, oid := range oids {
		binary.BigEndian.PutUint32(buf[2+i*4:], oid)
	}
	return buf
}

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

// sendReadyForQuery sends a ReadyForQuery message with the given transaction status byte.
func (p *Proxy) sendReadyForQuery(conn net.Conn, txStatus byte) error {
	return p.startup.sendReadyForQuery(conn, txStatus)
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

// extractParseInfo extracts statement name, query, and explicit parameter type OIDs
// from a Parse message.
// Parse format: [stmt_name]\0 [query]\0 [num_params_int16] [param_type_oid_int32...]
func (p *Proxy) extractParseInfo(payload []byte) (stmtName, query string, paramOIDs []uint32) {
	if len(payload) == 0 {
		return "", "", nil
	}
	pos := 0
	// Read statement name (until null)
	stmtStart := pos
	for pos < len(payload) && payload[pos] != 0 {
		pos++
	}
	if pos >= len(payload) {
		return "", "", nil
	}
	stmtName = string(payload[stmtStart:pos])
	pos++ // Skip null
	// Read query (until null)
	if pos >= len(payload) {
		return stmtName, "", nil
	}
	queryStart := pos
	for pos < len(payload) && payload[pos] != 0 {
		pos++
	}
	if pos > queryStart {
		query = string(payload[queryStart:pos])
	}
	pos++ // Skip null terminator of query

	// Read num_params (Int16) and then each OID (Int32).
	if pos+2 <= len(payload) {
		n := int(binary.BigEndian.Uint16(payload[pos : pos+2]))
		pos += 2
		if n > 0 && pos+n*4 <= len(payload) {
			paramOIDs = make([]uint32, n)
			for i := range paramOIDs {
				paramOIDs[i] = binary.BigEndian.Uint32(payload[pos : pos+4])
				pos += 4
			}
		}
	}
	return stmtName, query, paramOIDs
}

// --- Semantic SQL matching helpers ---

// extractAllTables returns all registered table names that appear as word-boundary
// matches in the query, covering FROM, JOIN, INTO, UPDATE, and CTE references.
func (p *Proxy) extractAllTables(query string) []string {
	var found []string
	for _, t := range p.registry.GetTables() {
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(t) + `\b`)
		if re.MatchString(query) {
			found = append(found, t)
		}
	}
	sort.Strings(found)
	return found
}

// extractQueryOperation returns SELECT, INSERT, UPDATE, or DELETE from the first
// SQL keyword. WITH is treated as SELECT.
func extractQueryOperation(query string) string {
	re := regexp.MustCompile(`(?i)^\s*(SELECT|INSERT|UPDATE|DELETE|WITH)\b`)
	if m := re.FindStringSubmatch(query); m != nil {
		op := strings.ToUpper(m[1])
		if op == "WITH" {
			return "SELECT"
		}
		return op
	}
	return ""
}

// extractBindParams reads actual parameter values from a PostgreSQL Bind message payload.
// Returns a slice of strings in $1, $2, … order. NULL params are represented as "".
func (p *Proxy) extractBindParams(payload []byte) []string {
	if len(payload) == 0 {
		return nil
	}
	pos := 0
	for pos < len(payload) && payload[pos] != 0 { pos++ } // skip portal name
	if pos >= len(payload) { return nil }
	pos++
	for pos < len(payload) && payload[pos] != 0 { pos++ } // skip statement name
	if pos >= len(payload) { return nil }
	pos++
	if pos+2 > len(payload) { return nil }
	numParamFmts := int(binary.BigEndian.Uint16(payload[pos : pos+2]))
	pos += 2 + numParamFmts*2
	if pos+2 > len(payload) { return nil }
	numParams := int(binary.BigEndian.Uint16(payload[pos : pos+2]))
	pos += 2
	params := make([]string, 0, numParams)
	for i := 0; i < numParams; i++ {
		if pos+4 > len(payload) { break }
		length := int(int32(binary.BigEndian.Uint32(payload[pos : pos+4])))
		pos += 4
		if length == -1 {
			params = append(params, "")
			continue
		}
		if pos+length > len(payload) { break }
		params = append(params, string(payload[pos:pos+length]))
		pos += length
	}
	return params
}

// reWhereCondition matches "col = $N", "table.col = $N", "col = 'literal'", "col = number"
// following WHERE, AND, or OR.
var reWhereCondition = regexp.MustCompile(
	`(?i)(?:WHERE|AND|OR)\s+((?:[a-z_][a-z0-9_]*\.)?[a-z_][a-z0-9_]*)\s*=\s*(?:\$(\d+)|'([^']*)'|(\d+(?:\.\d+)?))`,
)

// reInsertCols matches the column list in "INSERT INTO table (col1, col2, ...)"
var reInsertCols = regexp.MustCompile(`(?i)INSERT\s+INTO\s+[a-z_][a-z0-9_]*\s*\(([^)]+)\)`)

// reInsertVals matches the value list in "VALUES (val1, val2, ...)"
var reInsertVals = regexp.MustCompile(`(?i)\bVALUES\s*\(([^)]+)\)`)

// reUpdateSet matches the SET clause body up to WHERE or end-of-string
var reUpdateSet = regexp.MustCompile(`(?i)\bSET\s+(.+?)(?:\s+WHERE\b|$)`)

// reSetItem matches "col = $N", "col = 'literal'", or "col = number" in a SET clause
var reSetItem = regexp.MustCompile(
	`(?i)((?:[a-z_][a-z0-9_]*\.)?[a-z_][a-z0-9_]*)\s*=\s*(?:\$(\d+)|'([^']*)'|(\d+(?:\.\d+)?))`,
)

// extractWhereInfo returns column names and resolved column-value pairs from the
// WHERE clause. $N references are resolved from bindParams (1-indexed → index N-1).
func extractWhereInfo(query string, bindParams []string) (columns []string, values map[string]string) {
	values = make(map[string]string)
	seen := make(map[string]struct{})
	for _, m := range reWhereCondition.FindAllStringSubmatch(query, -1) {
		raw := strings.ToLower(m[1])
		// Strip optional "table." prefix so that "notifications.recipient" → "recipient"
		col := raw
		if dot := strings.LastIndex(raw, "."); dot >= 0 {
			col = raw[dot+1:]
		}
		if _, ok := seen[col]; !ok {
			seen[col] = struct{}{}
			columns = append(columns, col)
		}
		switch {
		case m[2] != "":
			if idx, err := strconv.Atoi(m[2]); err == nil && idx >= 1 && idx <= len(bindParams) {
				values[col] = bindParams[idx-1]
			}
		case m[3] != "":
			values[col] = m[3]
		case m[4] != "":
			values[col] = m[4]
		}
	}
	return
}

// extractWrittenValuesFromSQL extracts column-value pairs from INSERT or UPDATE statements.
func extractWrittenValuesFromSQL(query, operation string, bindParams []string) map[string]string {
	result := make(map[string]string)
	switch strings.ToUpper(operation) {
	case "INSERT":
		cm := reInsertCols.FindStringSubmatch(query)
		vm := reInsertVals.FindStringSubmatch(query)
		if cm == nil || vm == nil {
			return result
		}
		cols := splitCommaTrimmed(cm[1])
		vals := splitCommaTrimmed(vm[1])
		for i, col := range cols {
			if i >= len(vals) { break }
			result[strings.ToLower(col)] = resolveValue(vals[i], bindParams)
		}
	case "UPDATE":
		sm := reUpdateSet.FindStringSubmatch(query)
		if sm == nil { return result }
		for _, item := range reSetItem.FindAllStringSubmatch(sm[1], -1) {
			col := strings.ToLower(item[1])
			result[col] = resolveValueFromCaptures(item[2], item[3], item[4], bindParams)
		}
	}
	return result
}

// resolveValue converts a raw SQL value token to a string.
func resolveValue(v string, bindParams []string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "$") {
		if idx, err := strconv.Atoi(v[1:]); err == nil && idx >= 1 && idx <= len(bindParams) {
			return bindParams[idx-1]
		}
		return v
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1]
	}
	return v
}

// resolveValueFromCaptures resolves from regex submatch groups: $N ref, quoted literal, numeric literal.
func resolveValueFromCaptures(paramIdx, quotedLit, numLit string, bindParams []string) string {
	switch {
	case paramIdx != "":
		if idx, err := strconv.Atoi(paramIdx); err == nil && idx >= 1 && idx <= len(bindParams) {
			return bindParams[idx-1]
		}
	case quotedLit != "":
		return quotedLit
	case numLit != "":
		return numLit
	}
	return ""
}

// splitCommaTrimmed splits a comma-separated string and trims whitespace.
func splitCommaTrimmed(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// extractSemanticInfo derives all information needed for FindMockByTables from a
// query and optional bound parameters.
func (p *Proxy) extractSemanticInfo(query string, bindParams []string) (
	tables []string, operation string,
	whereColumns []string, whereValues map[string]string,
	writtenValues map[string]string,
) {
	tables = p.extractAllTables(query)
	operation = extractQueryOperation(query)
	whereColumns, whereValues = extractWhereInfo(query, bindParams)
	writtenValues = extractWrittenValuesFromSQL(query, operation, bindParams)
	return
}

// findMock tries semantic matching (ACCESSING_TABLES) first, then falls back to
// legacy USING_SQL matching. Pass nil bindParams for simple queries with inlined values.
func (p *Proxy) findMock(query string, bindParams []string) (*types.ExpectStatement, bool) {
	tables, op, whereCols, whereVals, writtenVals := p.extractSemanticInfo(query, bindParams)
	if len(tables) > 0 {
		if mock, found := p.registry.FindMockByTables(tables, op, whereCols, whereVals, writtenVals); found {
			return mock, true
		}
	}
	return p.registry.FindMock(p.extractTable(query), query)
}

// peekMock is like findMock but does not increment hit counts.
func (p *Proxy) peekMock(query string, bindParams []string) (*types.ExpectStatement, bool) {
	tables, op, whereCols, whereVals, writtenVals := p.extractSemanticInfo(query, bindParams)
	if len(tables) > 0 {
		if mock, found := p.registry.PeekMockByTables(tables, op, whereCols, whereVals, writtenVals); found {
			return mock, true
		}
	}
	return p.registry.PeekMock(p.extractTable(query), query)
}

// checkNegativeMocksForQuery fires both semantic and legacy negative expectation checks.
func (p *Proxy) checkNegativeMocksForQuery(query string, bindParams []string) {
	tables, op, whereCols, whereVals, writtenVals := p.extractSemanticInfo(query, bindParams)
	if len(tables) > 0 {
		p.registry.CheckNegativeMocksByTables(tables, op, whereCols, whereVals, writtenVals)
	}
	p.registry.CheckNegativeMocks(p.extractTable(query), query)
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

// extractBindResultFormats parses the Bind message payload and returns the
// per-column result format codes requested by the client.
// Bind layout (after portal\0 stmt\0):
//   int16  numParamFmts
//   int16  paramFmt[numParamFmts]
//   int16  numParamValues
//   for each param: int32 len, []byte value  (len=-1 → NULL)
//   int16  numResultFmts
//   int16  resultFmt[numResultFmts]
//
// Returns nil when the codes cannot be parsed.
func (p *Proxy) extractBindResultFormats(payload []byte) []int16 {
	if len(payload) == 0 {
		return nil
	}
	pos := 0

	// Skip portal name (null-terminated)
	for pos < len(payload) && payload[pos] != 0 {
		pos++
	}
	if pos >= len(payload) {
		return nil
	}
	pos++ // skip null

	// Skip statement name (null-terminated)
	for pos < len(payload) && payload[pos] != 0 {
		pos++
	}
	if pos >= len(payload) {
		return nil
	}
	pos++ // skip null

	// Number of parameter format codes
	if pos+2 > len(payload) {
		return nil
	}
	numParamFmts := int(binary.BigEndian.Uint16(payload[pos : pos+2]))
	pos += 2
	pos += numParamFmts * 2
	if pos > len(payload) {
		return nil
	}

	// Number of parameter values
	if pos+2 > len(payload) {
		return nil
	}
	numParams := int(binary.BigEndian.Uint16(payload[pos : pos+2]))
	pos += 2

	// Skip each parameter value
	for i := 0; i < numParams; i++ {
		if pos+4 > len(payload) {
			return nil
		}
		length := int(int32(binary.BigEndian.Uint32(payload[pos : pos+4])))
		pos += 4
		if length == -1 {
			continue // NULL parameter
		}
		pos += length
		if pos > len(payload) {
			return nil
		}
	}

	// Number of result format codes
	if pos+2 > len(payload) {
		return nil
	}
	numResultFmts := int(binary.BigEndian.Uint16(payload[pos : pos+2]))
	pos += 2

	if numResultFmts == 0 {
		// Per PostgreSQL spec, zero format codes means "text format for all columns".
		// Return a single 0 so callers use text encoding rather than binary heuristics.
		return []int16{0}
	}

	codes := make([]int16, numResultFmts)
	for i := 0; i < numResultFmts; i++ {
		if pos+2 > len(payload) {
			return nil
		}
		codes[i] = int16(binary.BigEndian.Uint16(payload[pos : pos+2]))
		pos += 2
	}
	return codes
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

// sendMockExecuteResponse sends the mock result for an Execute message.
// actualQuery is the real SQL from the Parse phase; it takes precedence over
// mock.SQL so that USING_SQL_CONTAINS mocks (where mock.SQL is empty) still
// receive a properly shaped result set instead of falling back to a synthetic
// "INSERT INTO <table>" string.
// skipRowDescription must be true when a Describe was forwarded to the upstream
// before this Execute — the upstream already sent RowDescription to the client,
// so including it again in the Execute response would violate the protocol.
func (p *Proxy) sendMockExecuteResponse(clientConn net.Conn, mock *types.ExpectStatement, actualQuery string, skipRowDescription bool, resultFormatCodes []int16) error {
	query := actualQuery
	if query == "" {
		query = mock.SQL
	}
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
			if err := p.sendMockResultSetForExtended(clientConn, mock, query, skipRowDescription, resultFormatCodes); err != nil {
				return err
			}
		}
		// For WRITE without RETURNING, just send CommandComplete
	} else {
		// For READ operations, send result set
		if err := p.sendMockResultSetForExtended(clientConn, mock, query, skipRowDescription, resultFormatCodes); err != nil {
			return err
		}
	}

	// Send CommandComplete
	rowCount := p.resolveRowCount(mock, 1)
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

	// For INSERT/UPDATE/DELETE ... RETURNING, extract columns after RETURNING.
	if selectIdx == -1 || fromIdx == -1 || fromIdx <= selectIdx {
		returningIdx := strings.Index(upperSQL, " RETURNING ")
		if returningIdx == -1 {
			return nil
		}
		columnsPart := strings.TrimSpace(sql[returningIdx+len(" RETURNING "):])
		p.logDebug("  -> Extracted RETURNING columns part: %s\n", columnsPart)
		return splitColumns(columnsPart)
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

		// Normalize aggregate function calls to their implicit result column name.
		// PostgreSQL names COUNT(*) -> "count", SUM(x) -> "sum", etc. (lowercase
		// function name, no arguments). Aliases defined above take precedence.
		if parenIdx := strings.Index(col, "("); parenIdx != -1 {
			col = strings.ToLower(strings.TrimSpace(col[:parenIdx]))
		}

		col = strings.TrimSpace(col)
		if col != "" {
			columnNames = append(columnNames, col)
		}
	}

	p.logDebug("  -> Extracted columns: %v\n", columnNames)
	return columnNames
}

// splitColumns parses a comma-separated column list (SELECT or RETURNING clause),
// stripping qualified names, aliases, and type casts.
func splitColumns(columnsPart string) []string {
	columnNames := []string{}
	for _, col := range strings.Split(columnsPart, ",") {
		col = strings.TrimSpace(col)
		if col == "" {
			continue
		}
		if dotIdx := strings.LastIndex(col, "."); dotIdx != -1 {
			col = col[dotIdx+1:]
		}
		if asIdx := strings.Index(strings.ToUpper(col), " AS "); asIdx != -1 {
			col = strings.TrimSpace(col[:asIdx])
		}
		if castIdx := strings.Index(col, "::"); castIdx != -1 {
			col = col[:castIdx]
		}
		col = strings.TrimSpace(col)
		if col != "" {
			columnNames = append(columnNames, col)
		}
	}
	return columnNames
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
