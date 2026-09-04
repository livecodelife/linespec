package oracle

import (
	"context"
	"io"
	"net"
	"sync"

	"github.com/livecodelife/linespec/v3/pkg/logger"
	"github.com/livecodelife/linespec/v3/pkg/proxy/base"
	"github.com/livecodelife/linespec/v3/pkg/registry"
	"github.com/livecodelife/linespec/v3/pkg/sqlanalysis"
)

// Proxy sits between a service and a real Oracle, relaying both directions
// untouched and reading the client's statements as they pass.
//
// It does not answer for Oracle. Every byte the service receives came from the
// database, so a statement's result is whatever the database really returned -
// which is why this channel supports the assertions about what was asked
// (ACCESSING_TABLES, VERIFY_OPERATION, VERIFY_WHERE_COLUMNS, VERIFY_WHERE,
// VERIFY_WRITTEN_VALUES, EXPECT_NOT) and not RETURNS.
type Proxy struct {
	addr         string
	upstreamAddr string
	registry     *registry.MockRegistry
	dbConfig     *base.DatabaseProxyConfig

	mu       sync.Mutex
	listener net.Listener
}

// NewProxy builds a proxy that listens on addr and relays to upstreamAddr.
func NewProxy(addr, upstreamAddr string, reg *registry.MockRegistry) *Proxy {
	return &Proxy{
		addr:         addr,
		upstreamAddr: upstreamAddr,
		registry:     reg,
		dbConfig:     base.NewDatabaseProxyConfig(""),
	}
}

// SetDatabaseName scopes this proxy's expectations, so that a spec naming a
// database is only satisfied by the proxy serving it.
func (p *Proxy) SetDatabaseName(name string) { p.dbConfig.SetDatabaseName(name) }

// Start accepts connections until ctx is done.
func (p *Proxy) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", p.addr)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.listener = ln
	p.mu.Unlock()

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	logger.Debug("Oracle proxy listening on %s -> %s", p.addr, p.upstreamAddr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		go p.handle(conn)
	}
}

// handle relays one client connection. The server-to-client direction is copied
// without inspection: nothing this package asserts lives in a response, and
// reading one would mean decoding Oracle's row format for no gain.
func (p *Proxy) handle(client net.Conn) {
	defer client.Close()

	upstream, err := net.Dial("tcp", p.upstreamAddr)
	if err != nil {
		logger.Error("Oracle proxy: cannot reach upstream %s: %v", p.upstreamAddr, err)
		return
	}
	defer upstream.Close()

	go func() {
		_, _ = io.Copy(client, upstream)
		// A closed upstream must close the client, or the service waits on a
		// connection that will never answer.
		client.Close()
	}()

	if err := p.relayClient(client, upstream); err != nil && err != io.EOF {
		logger.Debug("Oracle proxy: client relay ended: %v", err)
	}
}

// relayClient forwards every client packet upstream, observing the statements
// as they go.
//
// A packet whose framing cannot be read ends the observation rather than the
// connection: the remaining bytes are relayed blind, so the service keeps
// working and only this proxy's assertions are affected. Silently corrupting
// the stream to keep parsing would be the worse trade.
func (p *Proxy) relayClient(client, upstream net.Conn) error {
	header := make([]byte, HeaderLen)
	for {
		if _, err := io.ReadFull(client, header); err != nil {
			return err
		}
		size, err := PacketLen(header)
		if err != nil {
			logger.Debug("Oracle proxy: unreadable packet header (%v); relaying the rest unobserved", err)
			if _, werr := upstream.Write(header); werr != nil {
				return werr
			}
			_, cerr := io.Copy(upstream, client)
			return cerr
		}

		packet := make([]byte, size)
		copy(packet, header)
		if _, err := io.ReadFull(client, packet[HeaderLen:]); err != nil {
			return err
		}

		p.observe(packet)

		if _, err := upstream.Write(packet); err != nil {
			return err
		}
	}
}

// observe matches a packet's statement, if it carries one, against the
// registry. A packet with no statement is ordinary traffic and contributes
// nothing - Oracle carries a great deal over one connection that is not SQL.
func (p *Proxy) observe(packet []byte) {
	sql, ok := Statement(packet)
	if !ok {
		return
	}

	tables := sqlanalysis.Tables(sqlanalysis.Oracle, sql, p.registry.GetTables())
	if len(tables) == 0 {
		logger.Debug("Oracle proxy: statement touches no expected table: %.80s", sql)
		return
	}

	// Bind values are not recovered from the wire, so a column constrained by a
	// placeholder reports the PRESENT sentinel rather than its value. That is
	// enough for VERIFY_WHERE_COLUMNS and for VERIFY_WHERE naming PRESENT, and
	// it is not enough to compare a bound value - which the channel's
	// documentation says rather than leaving it to be discovered.
	r := sqlanalysis.Analyze(sqlanalysis.Oracle, sql, sqlanalysis.Binds{})
	db := p.dbConfig.GetDatabaseName()

	if _, found := p.registry.FindMockByTables(
		db, tables, r.Operation, r.WhereColumns, r.WhereValues, r.WrittenValues,
	); found {
		logger.Debug("Oracle proxy: matched %s on %v", r.Operation, tables)
	}

	p.registry.CheckNegativeMocksByTables(
		db, tables, r.Operation, r.WhereColumns, r.WhereValues, r.WrittenValues,
	)
}
