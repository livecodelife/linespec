package base

import (
	"io"
	"net"
	"sync"

	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/registry"
)

// Proxy manages database proxy connections using the MySQL pattern:
// 1. Connect to upstream FIRST
// 2. Start goroutine piping upstream->client transparently (includes startup)
// 3. In main goroutine, read client->upstream with selective interception
type Proxy struct {
	Registry      *registry.MockRegistry
	IsWhitelisted func(query string) bool
	ExtractTable  func(query string) string
}

// NewProxy creates a new proxy with the given configuration
func NewProxy(
	registry *registry.MockRegistry,
	isWhitelisted func(query string) bool,
	extractTable func(query string) string,
) *Proxy {
	return &Proxy{
		Registry:      registry,
		IsWhitelisted: isWhitelisted,
		ExtractTable:  extractTable,
	}
}

// HandleConnection manages a single client connection following the MySQL proxy pattern.
// This is the CORE method that fixes the PostgreSQL startup issue.
// Pattern:
//  1. Connect to upstream FIRST
//  2. Start goroutine: io.Copy(upstream, client) - transparent pipe
//  3. Main goroutine: read client, decide intercept vs forward
func (p *Proxy) HandleConnection(
	clientConn net.Conn,
	upstreamAddr string,
	readQuery func([]byte) string, // Extract SQL from message
	interceptQuery func(string, net.Conn) (bool, error), // Handle interception, return true if intercepted
) error {
	defer clientConn.Close()

	// STEP 1: Connect to upstream FIRST (this was the bug in PostgreSQL proxy)
	logger.Debug("Connecting to upstream: %s", upstreamAddr)
	upstreamConn, err := net.Dial("tcp", upstreamAddr)
	if err != nil {
		logger.Error("Failed to connect to upstream %s: %v", upstreamAddr, err)
		return err
	}
	defer upstreamConn.Close()

	// STEP 2: Start goroutine piping upstream->client transparently
	// This includes the startup handshake - no special handling needed!
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		// This transparently handles startup and all upstream responses
		io.Copy(clientConn, upstreamConn)
		clientConn.Close()
	}()

	// STEP 3: Handle client->upstream with selective interception
	err = p.handleClientToUpstream(clientConn, upstreamConn, readQuery, interceptQuery)

	// Signal upstream->client goroutine to stop
	upstreamConn.Close()
	wg.Wait()

	return err
}

// handleClientToUpstream reads messages from client, decides whether to intercept or forward
func (p *Proxy) handleClientToUpstream(
	clientConn net.Conn,
	upstreamConn net.Conn,
	readQuery func([]byte) string,
	interceptQuery func(string, net.Conn) (bool, error),
) error {
	buf := make([]byte, 16384) // Large buffer for messages

	for {
		// Read message from client
		n, err := clientConn.Read(buf)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			logger.Debug("Error reading from client: %v", err)
			return err
		}

		data := buf[:n]

		// Extract query from message
		query := readQuery(data)

		if query == "" {
			// Not a query message (or couldn't parse), forward to upstream
			if _, err := upstreamConn.Write(data); err != nil {
				return err
			}
			continue
		}

		// Check whitelist
		if p.IsWhitelisted != nil && p.IsWhitelisted(query) {
			logger.Debug("Query whitelisted, forwarding: %.50s", query)
			if _, err := upstreamConn.Write(data); err != nil {
				return err
			}
			continue
		}

		// Check if we should intercept this query
		if p.shouldIntercept(query) {
			logger.Debug("Intercepting query: %.50s", query)
			intercepted, err := interceptQuery(query, clientConn)
			if err != nil {
				return err
			}
			if intercepted {
				// Query was intercepted, don't forward to upstream
				continue
			}
			// Intercept decided not to handle it, forward to upstream
		}

		// Forward to upstream
		if _, err := upstreamConn.Write(data); err != nil {
			return err
		}
	}
}

// shouldIntercept checks if a query should be intercepted based on registry
func (p *Proxy) shouldIntercept(query string) bool {
	if p.Registry == nil {
		return false
	}

	// Extract table name
	tableName := p.ExtractTable(query)

	// Check if we have a mock for this query
	_, found := p.Registry.PeekMock(tableName, query)
	return found
}

// StartServer starts a TCP server that handles connections using the proxy
func (p *Proxy) StartServer(
	listenAddr string,
	upstreamAddr string,
	readQuery func([]byte) string,
	interceptQuery func(string, net.Conn) (bool, error),
) error {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	defer ln.Close()

	logger.Debug("Proxy server listening on %s, upstream: %s", listenAddr, upstreamAddr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			logger.Error("Error accepting connection: %v", err)
			continue
		}

		go func(c net.Conn) {
			defer c.Close()
			if err := p.HandleConnection(c, upstreamAddr, readQuery, interceptQuery); err != nil {
				logger.Debug("Connection handler error: %v", err)
			}
		}(conn)
	}
}
