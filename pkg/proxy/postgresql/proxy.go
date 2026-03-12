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
	"time"

	"github.com/calebcowen/linespec/pkg/dsl"
	"github.com/calebcowen/linespec/pkg/registry"
	"github.com/calebcowen/linespec/pkg/types"
)

// Proxy is a PostgreSQL wire protocol proxy with mock capabilities
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

// handleConnection handles a single client connection
func (p *Proxy) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	fmt.Printf("PostgreSQL Proxy: New client connection from %s\n", clientConn.RemoteAddr())

	// Wrap in buffered reader for peeking
	clientReader := bufio.NewReader(clientConn)

	// Handle startup phase
	params, err := p.startup.HandleStartupWithReader(clientReader, clientConn)
	if err != nil {
		fmt.Printf("PostgreSQL Proxy: Startup error: %v\n", err)
		return
	}

	fmt.Printf("PostgreSQL Proxy: Client connected with params: %v\n", params)

	// Connect to upstream server
	upstreamConn, err := net.Dial("tcp", p.upstreamAddr)
	if err != nil {
		fmt.Printf("PostgreSQL Proxy: Failed to connect to upstream %s: %v\n", p.upstreamAddr, err)
		return
	}
	defer upstreamConn.Close()

	// Perform upstream startup (mirror what we did for client)
	fmt.Printf("PostgreSQL Proxy: About to perform upstream startup...\n")
	if err := p.performUpstreamStartup(upstreamConn); err != nil {
		fmt.Printf("PostgreSQL Proxy: Upstream startup error: %v\n", err)
		return
	}
	fmt.Printf("PostgreSQL Proxy: Upstream startup completed successfully\n")

	// Start proxying
	fmt.Printf("PostgreSQL Proxy: Starting message proxying loop\n")
	p.proxyMessages(clientReader, clientConn, upstreamConn)
}

// StartupHandler methods need to be updated to work with buffered reader
type bufferedStartupHandler struct {
	*StartupHandler
}

// HandleStartupWithReader handles startup using a buffered reader
func (h *StartupHandler) HandleStartupWithReader(reader *bufio.Reader, conn net.Conn) (map[string]string, error) {
	// Handle SSL request
	if err := h.handleSSLRequest(reader, conn); err != nil {
		return nil, fmt.Errorf("SSL handling error: %w", err)
	}

	// Read startup message
	startupMsg, err := h.readStartupMessage(reader)
	if err != nil {
		return nil, fmt.Errorf("error reading startup message: %w", err)
	}

	// Parse startup parameters
	params := ParseStartupMessage(startupMsg)

	// Send authentication OK (trust authentication)
	if err := h.sendAuthenticationOK(conn); err != nil {
		return nil, fmt.Errorf("error sending auth OK: %w", err)
	}

	// Send server parameters
	if err := h.sendServerParameters(conn, params); err != nil {
		return nil, fmt.Errorf("error sending server params: %w", err)
	}

	// Send backend key data (for cancellation support)
	if err := h.sendBackendKeyData(conn); err != nil {
		return nil, fmt.Errorf("error sending backend key data: %w", err)
	}

	// Send ReadyForQuery
	if err := h.sendReadyForQuery(conn); err != nil {
		return nil, fmt.Errorf("error sending ready for query: %w", err)
	}

	return params, nil
}

// performUpstreamStartup performs startup with the real PostgreSQL server
func (p *Proxy) performUpstreamStartup(conn net.Conn) error {
	fmt.Printf("PostgreSQL Proxy: Starting upstream connection sequence\n")

	// Send startup message
	startupMsg := make([]byte, 0, 100)
	startupMsg = append(startupMsg, 0, 0, 0, 0) // Length placeholder

	// Version 3.0 = 196608 = 0x00030000
	// PostgreSQL protocol version is: major << 16 | minor
	// Protocol 3.0 = 3 << 16 | 0 = 196608
	// Big-endian bytes: 0x00 0x03 0x00 0x00
	startupMsg = append(startupMsg, 0x00, 0x03, 0x00, 0x00)

	// User parameter
	startupMsg = append(startupMsg, []byte("user")...)
	startupMsg = append(startupMsg, 0)
	startupMsg = append(startupMsg, []byte("notification_user")...)
	startupMsg = append(startupMsg, 0)

	// Database parameter
	startupMsg = append(startupMsg, []byte("database")...)
	startupMsg = append(startupMsg, 0)
	startupMsg = append(startupMsg, []byte("notification_service")...)
	startupMsg = append(startupMsg, 0)
	startupMsg = append(startupMsg, 0) // End of params

	// Update length
	length := len(startupMsg)
	startupMsg[0] = byte(length >> 24)
	startupMsg[1] = byte(length >> 16)
	startupMsg[2] = byte(length >> 8)
	startupMsg[3] = byte(length)

	fmt.Printf("PostgreSQL Proxy: Sending startup message (%d bytes): %v\n", length, startupMsg[:20])

	if _, err := conn.Write(startupMsg); err != nil {
		return fmt.Errorf("error sending startup: %w", err)
	}

	// Read authentication OK and ready for query from upstream
	fmt.Printf("PostgreSQL Proxy: Waiting for upstream startup response\n")
	for i := 0; i < 10; i++ {
		msg, err := ReadRegularMessage(conn)
		if err != nil {
			fmt.Printf("PostgreSQL Proxy: Error reading upstream startup response: %v\n", err)
			return fmt.Errorf("error reading upstream startup response: %w", err)
		}
		fmt.Printf("PostgreSQL Proxy: Received upstream message type '%c' (0x%02x)\n", msg.Type, msg.Type)

		if msg.Type == MsgAuthentication {
			// Check authentication type
			if len(msg.Payload) >= 4 {
				// PostgreSQL sends auth type in little-endian format in the payload
				authType := int(msg.Payload[0]) | int(msg.Payload[1])<<8 | int(msg.Payload[2])<<16 | int(msg.Payload[3])<<24
				fmt.Printf("PostgreSQL Proxy: Upstream requested authentication type: %d (0x%x)\n", authType, authType)

				switch authType {
				case 0: // AuthOK - no authentication needed
					fmt.Printf("PostgreSQL Proxy: Upstream authentication OK\n")
					continue
				case 3: // AuthenticationCleartextPassword
					fmt.Printf("PostgreSQL Proxy: Upstream requesting cleartext password\n")
					// Send password
					password := "notification_password"
					passwordPayload := append([]byte(password), 0)
					if err := p.writeMessage(conn, 'p', passwordPayload); err != nil {
						return fmt.Errorf("error sending password: %w", err)
					}
					continue
				case 5: // AuthenticationMD5Password
					fmt.Printf("PostgreSQL Proxy: Upstream requesting MD5 password\n")
					// For MD5, we'd need to compute the hash
					// First, read the salt from the remaining payload
					if len(msg.Payload) >= 8 {
						salt := msg.Payload[4:8]
						fmt.Printf("PostgreSQL Proxy: MD5 salt: %x\n", salt)
					}
					// Send cleartext as a fallback
					password := "notification_password"
					passwordPayload := append([]byte(password), 0)
					if err := p.writeMessage(conn, 'p', passwordPayload); err != nil {
						return fmt.Errorf("error sending password: %w", err)
					}
					continue
				case 10: // AuthenticationSASL (SCRAM-SHA-256)
					fmt.Printf("PostgreSQL Proxy: Upstream requesting SASL/SCRAM-SHA-256\n")
					// Read the mechanisms from the payload
					// After the 4-byte auth type, there are null-terminated mechanism names
					mechanisms := msg.Payload[4:]
					fmt.Printf("PostgreSQL Proxy: SASL mechanisms: %s\n", string(mechanisms))

					// For now, send cleartext password as fallback
					// In production, we'd implement SCRAM-SHA-256
					password := "notification_password"
					passwordPayload := append([]byte(password), 0)
					if err := p.writeMessage(conn, 'p', passwordPayload); err != nil {
						return fmt.Errorf("error sending password: %w", err)
					}
					continue
				default:
					fmt.Printf("PostgreSQL Proxy: Unsupported authentication type: %d, attempting cleartext password\n", authType)
					// Try sending cleartext password anyway
					password := "notification_password"
					passwordPayload := append([]byte(password), 0)
					if err := p.writeMessage(conn, 'p', passwordPayload); err != nil {
						return fmt.Errorf("error sending password: %w", err)
					}
					continue
				}
			}
		}

		if msg.Type == MsgReadyForQuery {
			fmt.Printf("PostgreSQL Proxy: Upstream ready for query\n")
			// Read ALL pending ParameterStatus messages to ensure clean state
			// PostgreSQL can send many ParameterStatus updates after ReadyForQuery
			drainCount := 0
			for {
				conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				msg2, err := ReadRegularMessage(conn)
				conn.SetReadDeadline(time.Time{})
				if err != nil {
					// Timeout means no more messages
					fmt.Printf("PostgreSQL Proxy: Drained %d post-ready messages\n", drainCount)
					break
				}
				drainCount++
				fmt.Printf("PostgreSQL Proxy: Drained post-ready message type '%c' (0x%02x)\n", msg2.Type, msg2.Type)
				if drainCount > 20 {
					fmt.Printf("PostgreSQL Proxy: Too many post-ready messages, breaking\n")
					break
				}
			}
			break
		}
		if msg.Type == MsgErrorResponse {
			fmt.Printf("PostgreSQL Proxy: Upstream returned error\n")
			return fmt.Errorf("upstream returned error during startup")
		}
	}

	fmt.Printf("PostgreSQL Proxy: performUpstreamStartup loop completed without receiving ReadyForQuery\n")
	return fmt.Errorf("startup failed: never received ReadyForQuery from upstream")
}

// proxyMessages handles the main message proxying loop
func (p *Proxy) proxyMessages(clientReader *bufio.Reader, clientConn, upstreamConn net.Conn) {
	msgCount := 0
	for {
		msgCount++
		if msgCount > 1000 {
			fmt.Printf("PostgreSQL Proxy: Message limit reached, closing connection\n")
			return
		}

		// Read message from buffered reader
		msg, err := ReadRegularMessageFromReader(clientReader)
		if err != nil {
			if err != io.EOF {
				fmt.Printf("PostgreSQL Proxy: Error reading from client: %v\n", err)
			} else {
				fmt.Printf("PostgreSQL Proxy: Client disconnected (EOF)\n")
			}
			return
		}

		fmt.Printf("PostgreSQL Proxy: Received message type '%c' (0x%02x)\n", msg.Type, msg.Type)

		switch msg.Type {
		case MsgTerminate:
			fmt.Printf("PostgreSQL Proxy: Terminate message received\n")
			p.startup.HandleTerminate(clientConn)
			return

		case MsgQuery:
			// Query message - intercept
			query := ParseQuery(msg)
			fmt.Printf("PostgreSQL Proxy: Query: %.100s\n", query)

			if p.isWhitelisted(query) {
				fmt.Printf("PostgreSQL Proxy: Query whitelisted, forwarding\n")
				// Forward to upstream and read response
				if err := p.writeMessage(upstreamConn, msg.Type, msg.Payload); err != nil {
					fmt.Printf("PostgreSQL Proxy: Error forwarding query: %v\n", err)
					return
				}
				// Read and forward all responses until ReadyForQuery
				if err := p.forwardResponsesUntilReady(upstreamConn, clientConn); err != nil {
					fmt.Printf("PostgreSQL Proxy: Error forwarding query response: %v\n", err)
					return
				}
			} else {
				// Try to mock
				tableName := p.extractTable(query)
				mock, found := p.registry.FindMock(tableName, query)

				if found {
					fmt.Printf("PostgreSQL Proxy: Mocking query for table %s\n", tableName)
					if err := p.sendMockResponse(clientConn, mock); err != nil {
						fmt.Printf("PostgreSQL Proxy: Error sending mock response: %v\n", err)
						return
					}
				} else {
					fmt.Printf("PostgreSQL Proxy: No mock found for table %s, forwarding\n", tableName)
					// Forward to upstream and read response
					if err := p.writeMessage(upstreamConn, msg.Type, msg.Payload); err != nil {
						fmt.Printf("PostgreSQL Proxy: Error forwarding query: %v\n", err)
						return
					}
					// Read and forward all responses until ReadyForQuery
					if err := p.forwardResponsesUntilReady(upstreamConn, clientConn); err != nil {
						fmt.Printf("PostgreSQL Proxy: Error forwarding query response: %v\n", err)
						return
					}
				}
			}

		case MsgParse:
			fmt.Printf("PostgreSQL Proxy: Parse message received, forwarding to upstream\n")
			fmt.Printf("PostgreSQL Proxy: Parse payload length: %d\n", len(msg.Payload))

			// Parse message format: [stmt_name]\0 [query]\0 [param_types...]
			var stmtName, query string
			if len(msg.Payload) > 0 {
				// Log the full payload for debugging
				fullPayload := string(msg.Payload)
				if len(fullPayload) > 300 {
					fullPayload = fullPayload[:300] + "..."
				}
				fmt.Printf("PostgreSQL Proxy: Parse payload hex: %x\n", msg.Payload[:min(len(msg.Payload), 100)])
				fmt.Printf("PostgreSQL Proxy: Parse payload: %q\n", fullPayload)

				// Parse the payload
				parts := strings.Split(fullPayload, "\x00")
				if len(parts) >= 1 {
					stmtName = parts[0]
					fmt.Printf("PostgreSQL Proxy: Parse stmt_name: %q\n", stmtName)
				}
				if len(parts) >= 2 {
					query = parts[1]
					fmt.Printf("PostgreSQL Proxy: Parse query: %q\n", query)

					// Check if this is a pg_type introspection query that should be whitelisted
					if p.isWhitelisted(query) {
						fmt.Printf("PostgreSQL Proxy: Query is whitelisted, forwarding to upstream\n")
					}
				}
			}

			// Ensure the message is flushed to upstream
			if err := p.writeMessageWithFlush(upstreamConn, msg.Type, msg.Payload); err != nil {
				fmt.Printf("PostgreSQL Proxy: Error forwarding Parse: %v\n", err)
				return
			}
			fmt.Printf("PostgreSQL Proxy: Parse forwarded, reading responses...\n")

			// Read and forward responses until we get ParseComplete ('1') or ReadyForQuery
			responseCount := 0
			parseCompleteReceived := false
			for {
				responseCount++
				if responseCount > 20 {
					fmt.Printf("PostgreSQL Proxy: Too many responses during Parse, breaking\n")
					return
				}

				resp, err := ReadRegularMessage(upstreamConn)
				if err != nil {
					fmt.Printf("PostgreSQL Proxy: Error reading Parse response: %v\n", err)
					return
				}
				fmt.Printf("PostgreSQL Proxy: Forwarding Parse response type '%c' (0x%02x)\n", resp.Type, resp.Type)
				if err := p.writeMessage(clientConn, resp.Type, resp.Payload); err != nil {
					fmt.Printf("PostgreSQL Proxy: Error writing Parse response: %v\n", err)
					return
				}
				if resp.Type == MsgParseComplete {
					fmt.Printf("PostgreSQL Proxy: ParseComplete received, breaking\n")
					parseCompleteReceived = true
					break
				}
				if resp.Type == MsgErrorResponse {
					fmt.Printf("PostgreSQL Proxy: Error response during Parse\n")
					// Continue reading until ReadyForQuery to complete the cycle
					continue
				}
				// Handle case where we get ReadyForQuery without ParseComplete
				// (can happen if server rejects the prepared statement)
				if resp.Type == MsgReadyForQuery {
					fmt.Printf("PostgreSQL Proxy: Got ReadyForQuery without ParseComplete (Parse may have failed)\n")
					break
				}
			}

			// If we didn't get ParseComplete, the prepared statement creation failed
			// Send an error to the client so it knows the Parse failed
			if !parseCompleteReceived {
				fmt.Printf("PostgreSQL Proxy: Parse failed for statement %q, sending error to client\n", stmtName)
				errorPayload := p.createErrorPayload(fmt.Sprintf("Parse failed for statement %s", stmtName))
				if err := p.writeMessage(clientConn, MsgErrorResponse, errorPayload); err != nil {
					fmt.Printf("PostgreSQL Proxy: Error sending error response: %v\n", err)
					return
				}
				// Send ReadyForQuery to complete the cycle
				if err := p.writeMessage(clientConn, MsgReadyForQuery, []byte{'I'}); err != nil {
					fmt.Printf("PostgreSQL Proxy: Error sending ReadyForQuery: %v\n", err)
					return
				}
			}

		case MsgBind:
			fmt.Printf("PostgreSQL Proxy: Bind message received, forwarding\n")
			if err := p.writeMessageWithFlush(upstreamConn, msg.Type, msg.Payload); err != nil {
				fmt.Printf("PostgreSQL Proxy: Error forwarding Bind: %v\n", err)
				return
			}
			// Read and forward responses until we get BindComplete ('2')
			for {
				resp, err := ReadRegularMessage(upstreamConn)
				if err != nil {
					fmt.Printf("PostgreSQL Proxy: Error reading Bind response: %v\n", err)
					return
				}
				fmt.Printf("PostgreSQL Proxy: Forwarding Bind response type '%c'\n", resp.Type)
				if err := p.writeMessage(clientConn, resp.Type, resp.Payload); err != nil {
					fmt.Printf("PostgreSQL Proxy: Error writing Bind response: %v\n", err)
					return
				}
				if resp.Type == MsgBindComplete {
					break
				}
				if resp.Type == MsgErrorResponse {
					fmt.Printf("PostgreSQL Proxy: Error response during Bind\n")
					continue // Continue reading until ReadyForQuery
				}
				if resp.Type == MsgReadyForQuery {
					break
				}
			}

		case MsgExecute:
			fmt.Printf("PostgreSQL Proxy: Execute message received, forwarding\n")
			if err := p.writeMessageWithFlush(upstreamConn, msg.Type, msg.Payload); err != nil {
				fmt.Printf("PostgreSQL Proxy: Error forwarding Execute: %v\n", err)
				return
			}
			// Execute can return multiple messages:
			// For queries: RowDescription, DataRow(s), CommandComplete
			// For other commands: CommandComplete only
			// We need to read until we get CommandComplete or ReadyForQuery
			for {
				resp, err := ReadRegularMessage(upstreamConn)
				if err != nil {
					fmt.Printf("PostgreSQL Proxy: Error reading Execute response: %v\n", err)
					return
				}
				fmt.Printf("PostgreSQL Proxy: Forwarding Execute response type '%c'\n", resp.Type)
				if err := p.writeMessage(clientConn, resp.Type, resp.Payload); err != nil {
					fmt.Printf("PostgreSQL Proxy: Error writing Execute response: %v\n", err)
					return
				}
				if resp.Type == MsgCommandComplete {
					break
				}
				if resp.Type == MsgErrorResponse {
					fmt.Printf("PostgreSQL Proxy: Error response during Execute\n")
					continue // Continue reading until ReadyForQuery
				}
				if resp.Type == MsgReadyForQuery {
					break
				}
			}

		case MsgSync:
			fmt.Printf("PostgreSQL Proxy: Sync message received, forwarding\n")
			// Sync message - forward to upstream and wait for ReadyForQuery
			if err := p.writeMessageWithFlush(upstreamConn, msg.Type, msg.Payload); err != nil {
				fmt.Printf("PostgreSQL Proxy: Error forwarding Sync: %v\n", err)
				return
			}
			// Read and forward responses until we get ReadyForQuery ('Z')
			// Note: PostgreSQL may send async ParameterStatus messages before ReadyForQuery
			for {
				resp, err := ReadRegularMessage(upstreamConn)
				if err != nil {
					fmt.Printf("PostgreSQL Proxy: Error reading Sync response: %v\n", err)
					return
				}
				fmt.Printf("PostgreSQL Proxy: Forwarding Sync response type '%c'\n", resp.Type)
				if err := p.writeMessage(clientConn, resp.Type, resp.Payload); err != nil {
					fmt.Printf("PostgreSQL Proxy: Error writing Sync response: %v\n", err)
					return
				}
				if resp.Type == MsgReadyForQuery {
					break
				}
			}

		case MsgDescribe:
			fmt.Printf("PostgreSQL Proxy: Describe message received, forwarding\n")
			if err := p.writeMessageWithFlush(upstreamConn, msg.Type, msg.Payload); err != nil {
				fmt.Printf("PostgreSQL Proxy: Error forwarding Describe: %v\n", err)
				return
			}
			fmt.Printf("PostgreSQL Proxy: Describe forwarded, waiting for response...\n")
			// Set a timeout for reading responses
			if tcpConn, ok := upstreamConn.(*net.TCPConn); ok {
				fmt.Printf("PostgreSQL Proxy: Setting read deadline for Describe...\n")
				tcpConn.SetReadDeadline(time.Now().Add(5 * time.Second))
				defer tcpConn.SetReadDeadline(time.Time{})
			} else {
				fmt.Printf("PostgreSQL Proxy: Cannot set deadline - not a TCPConn\n")
			}
			// Read and forward responses until we get the actual Describe response
			// (ParameterDescription 't', NoData 'n', or RowDescription 'T')
			// Note: PostgreSQL may send async ParameterStatus messages before the response
			for {
				resp, err := ReadRegularMessage(upstreamConn)
				if err != nil {
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						fmt.Printf("PostgreSQL Proxy: Timeout waiting for Describe response\n")
					} else {
						fmt.Printf("PostgreSQL Proxy: Error reading Describe response: %v\n", err)
					}
					return
				}
				fmt.Printf("PostgreSQL Proxy: Forwarding Describe response type '%c'\n", resp.Type)
				if err := p.writeMessage(clientConn, resp.Type, resp.Payload); err != nil {
					fmt.Printf("PostgreSQL Proxy: Error writing Describe response: %v\n", err)
					return
				}
				// Stop when we get the actual Describe response types
				if resp.Type == MsgParameterDescription || resp.Type == MsgNoData || resp.Type == MsgRowDescription {
					break
				}
				if resp.Type == MsgErrorResponse {
					fmt.Printf("PostgreSQL Proxy: Error response during Describe\n")
					break
				}
				if resp.Type == MsgReadyForQuery {
					break
				}
			}

		case MsgClose:
			fmt.Printf("PostgreSQL Proxy: Close message received, forwarding\n")
			if err := p.writeMessageWithFlush(upstreamConn, msg.Type, msg.Payload); err != nil {
				fmt.Printf("PostgreSQL Proxy: Error forwarding Close: %v\n", err)
				return
			}
			// Read and forward CloseComplete
			resp, err := ReadRegularMessage(upstreamConn)
			if err != nil {
				fmt.Printf("PostgreSQL Proxy: Error reading CloseComplete: %v\n", err)
				return
			}
			fmt.Printf("PostgreSQL Proxy: Forwarding Close response type '%c'\n", resp.Type)
			if err := p.writeMessage(clientConn, resp.Type, resp.Payload); err != nil {
				fmt.Printf("PostgreSQL Proxy: Error writing CloseComplete: %v\n", err)
				return
			}

		case MsgFlush:
			fmt.Printf("PostgreSQL Proxy: Flush message received, forwarding with buffer flush\n")
			if err := p.writeMessageWithFlush(upstreamConn, msg.Type, msg.Payload); err != nil {
				fmt.Printf("PostgreSQL Proxy: Error forwarding Flush: %v\n", err)
				return
			}
			fmt.Printf("PostgreSQL Proxy: Flush forwarded and buffer flushed\n")
			// Flush doesn't expect a response - it's just a hint to flush buffers

		default:
			fmt.Printf("PostgreSQL Proxy: Unknown message type '%c' (0x%02x), forwarding\n", msg.Type, msg.Type)
			// Other messages - forward to upstream
			if err := p.writeMessage(upstreamConn, msg.Type, msg.Payload); err != nil {
				return
			}
		}
	}
}

// forwardResponsesUntilReady reads and forwards all responses from upstream until ReadyForQuery
func (p *Proxy) forwardResponsesUntilReady(upstreamConn, clientConn net.Conn) error {
	for {
		resp, err := ReadRegularMessage(upstreamConn)
		if err != nil {
			return fmt.Errorf("error reading upstream response: %w", err)
		}

		fmt.Printf("PostgreSQL Proxy: Forwarding response type '%c'\n", resp.Type)
		if err := p.writeMessage(clientConn, resp.Type, resp.Payload); err != nil {
			return fmt.Errorf("error forwarding response: %w", err)
		}

		if resp.Type == MsgReadyForQuery {
			break
		}
		if resp.Type == MsgErrorResponse {
			fmt.Printf("PostgreSQL Proxy: Error response from upstream\n")
			break
		}
	}
	return nil
}

// ReadRegularMessageFromReader reads a regular message from a buffered reader
func ReadRegularMessageFromReader(reader *bufio.Reader) (*Message, error) {
	// Read type byte
	typeBuf := make([]byte, 1)
	if _, err := io.ReadFull(reader, typeBuf); err != nil {
		return nil, err
	}

	// Read length (4 bytes, big-endian)
	lengthBuf := make([]byte, 4)
	if _, err := io.ReadFull(reader, lengthBuf); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lengthBuf)

	// Read payload (length - 4, since length includes itself)
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

// writeMessage writes a message to the connection
func (p *Proxy) writeMessage(conn net.Conn, msgType byte, payload []byte) error {
	// Message format: [type (1 byte)][length (4 bytes big-endian)][payload]
	length := uint32(len(payload) + 4)

	msg := make([]byte, 0, 1+4+len(payload))
	msg = append(msg, msgType)
	msg = append(msg, byte(length>>24), byte(length>>16), byte(length>>8), byte(length))
	msg = append(msg, payload...)

	_, err := conn.Write(msg)
	return err
}

// writeMessageWithFlush writes a message and ensures it's flushed from TCP buffers
func (p *Proxy) writeMessageWithFlush(conn net.Conn, msgType byte, payload []byte) error {
	// First write the message
	if err := p.writeMessage(conn, msgType, payload); err != nil {
		return err
	}

	// For TCP connections, ensure the data is flushed
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		if err := tcpConn.SetNoDelay(true); err != nil {
			// Non-fatal: log but continue
			fmt.Printf("PostgreSQL Proxy: Warning: could not set TCP_NODELAY: %v\n", err)
		}
	}

	return nil
}

// sendMockResponse sends a mock response to the client
func (p *Proxy) sendMockResponse(conn net.Conn, mock *types.ExpectStatement) error {
	// Determine columns from mock or use defaults
	columns := []string{"id", "name", "email"}
	if mock.Table != "" {
		// Try to infer columns based on table
		columns = p.inferColumnsForTable(mock.Table)
	}

	// Handle different expectation types
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

			// Extract rows from payload
			rows := p.extractRowsFromPayload(payload)
			if len(rows) > 0 {
				// Use columns from first row
				columns = make([]string, 0, len(rows[0]))
				for col := range rows[0] {
					columns = append(columns, col)
				}
			}

			return p.result.SendResultSet(conn, columns, rows)
		}

		return p.result.SendEmptyResultSet(conn, columns)

	case types.WritePostgreSQL:
		// For writes, just send CommandComplete with row count
		return p.result.SendCommandComplete(conn, "INSERT 0 1")

	default:
		// Unknown type - send empty result
		return p.result.SendEmptyResultSet(conn, columns)
	}
}

// extractRowsFromPayload extracts rows from a payload
func (p *Proxy) extractRowsFromPayload(payload interface{}) []map[string]interface{} {
	var rows []map[string]interface{}

	// Handle different payload formats
	switch data := payload.(type) {
	case []interface{}:
		// Array of objects
		for _, item := range data {
			if m, ok := item.(map[string]interface{}); ok {
				rows = append(rows, m)
			}
		}
	case map[string]interface{}:
		// Check if it has a "rows" key
		if rowsRaw, ok := data["rows"].([]interface{}); ok {
			for _, item := range rowsRaw {
				if m, ok := item.(map[string]interface{}); ok {
					rows = append(rows, m)
				}
			}
		} else {
			// Single object - wrap in array
			rows = append(rows, data)
		}
	}

	return rows
}

// inferColumnsForTable infers column names for a table
func (p *Proxy) inferColumnsForTable(table string) []string {
	// Default columns based on table name patterns
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
		"SELECT TYPOID",         // asyncpg type introspection
		"SELECT T.TYPNAMESPACE", // asyncpg type introspection
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
				// Remove schema prefix if present
				if idx := strings.Index(table, "."); idx != -1 {
					table = table[idx+1:]
				}
				// Remove quotes
				table = strings.Trim(table, "`\"'")
				return table
			}
		}
	}

	return "unknown"
}

// createErrorPayload creates a PostgreSQL error response payload
// Format: field type (byte) + null-terminated string
// Common fields: S (severity), C (code), M (message)
func (p *Proxy) createErrorPayload(message string) []byte {
	var payload []byte
	// Severity: ERROR
	payload = append(payload, 'S')
	payload = append(payload, []byte("ERROR")...)
	payload = append(payload, 0)
	// Code: 08P01 (protocol violation) - generic error
	payload = append(payload, 'C')
	payload = append(payload, []byte("08P01")...)
	payload = append(payload, 0)
	// Message
	payload = append(payload, 'M')
	payload = append(payload, []byte(message)...)
	payload = append(payload, 0)
	// Null terminator to end the error fields
	payload = append(payload, 0)
	return payload
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
