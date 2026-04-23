package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/livecodelife/linespec/pkg/dsl"
	"github.com/livecodelife/linespec/pkg/interpolate"
	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/proxy/base"
	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/types"
	"github.com/livecodelife/linespec/pkg/verify"
)

const (
	clientDeprecateEOF              uint32 = 0x01000000
	clientOptionalResultsetMetadata uint32 = 0x02000000
	clientQueryAttributes           uint32 = 0x08000000

	comStmtPrepare      byte = 0x16
	comStmtExecute      byte = 0x17
	comStmtSendLongData byte = 0x18
	comStmtClose        byte = 0x19
	comStmtReset        byte = 0x1a
)

type Proxy struct {
	addr             string
	upstreamAddr     string
	registry         *registry.MockRegistry
	loader           *dsl.PayloadLoader
	schemaCache      map[string][]ColumnInfo   // table name -> column definitions
	transparentMode  bool                      // When true, pass through all queries
	transparentUntil time.Time                 // Time until which to stay in transparent mode
	dbConfig         *base.DatabaseProxyConfig // Configurable database name
}

type ColumnInfo struct {
	Field      string         `json:"Field"`
	Type       string         `json:"Type"`
	Collation  sql.NullString `json:"Collation"`
	Null       string         `json:"Null"`
	Key        string         `json:"Key"`
	Default    sql.NullString `json:"Default"`
	Extra      string         `json:"Extra"`
	Privileges string         `json:"Privileges"`
	Comment    string         `json:"Comment"`
}

func NewProxy(addr, upstreamAddr string, reg *registry.MockRegistry) *Proxy {
	return &Proxy{
		addr:            addr,
		upstreamAddr:    upstreamAddr,
		registry:        reg,
		loader:          dsl.NewPayloadLoader(""),
		schemaCache:     make(map[string][]ColumnInfo),
		transparentMode: false,
		dbConfig:        &base.DatabaseProxyConfig{}, // Must be set via SetDatabaseName before Start
	}
}

// SetDatabaseName sets the database name for schema responses
func (p *Proxy) SetDatabaseName(name string) {
	p.dbConfig.SetDatabaseName(name)
}

// SetResolver wires an interpolate.Resolver into the payload loader so that
// ${VAR} tokens in RETURNS payload files are resolved at runtime.
func (p *Proxy) SetResolver(resolver *interpolate.Resolver) {
	p.loader = dsl.NewPayloadLoaderWithResolver("", resolver)
}

// GetDatabaseName returns the current database name
func (p *Proxy) GetDatabaseName() string {
	return p.dbConfig.GetDatabaseName()
}

// EnableTransparentMode enables transparent passthrough mode for a specified duration
func (p *Proxy) EnableTransparentMode(duration time.Duration) {
	p.transparentMode = true
	p.transparentUntil = time.Now().Add(duration)
	logger.Debug("Proxy transparent mode enabled for %v", duration)
}

// isTransparent returns true if the proxy should pass through all queries
func (p *Proxy) isTransparent() bool {
	if !p.transparentMode {
		return false
	}
	// Check if transparent mode has expired
	if time.Now().After(p.transparentUntil) {
		p.transparentMode = false
		logger.Debug("Proxy transparent mode disabled, now intercepting queries")
		return false
	}
	return true
}

func (p *Proxy) LoadSchema(schemaFile string) error {
	data, err := os.ReadFile(schemaFile)
	if err != nil {
		return fmt.Errorf("failed to read schema file: %w", err)
	}

	if err := json.Unmarshal(data, &p.schemaCache); err != nil {
		return fmt.Errorf("failed to parse schema file: %w", err)
	}

	logger.Debug("Loaded schema for %d tables", len(p.schemaCache))
	for table := range p.schemaCache {
		logger.Debug("  - %s", table)
	}
	return nil
}

// LoadSchemaFromBytes unmarshals schema directly from bytes without file I/O.
func (p *Proxy) LoadSchemaFromBytes(data []byte) error {
	if err := json.Unmarshal(data, &p.schemaCache); err != nil {
		return fmt.Errorf("failed to parse schema data: %w", err)
	}
	logger.Debug("Loaded schema for %d tables", len(p.schemaCache))
	return nil
}

func (p *Proxy) Start(ctx context.Context) error {
	if p.dbConfig.GetDatabaseName() == "" {
		return fmt.Errorf("MySQL proxy requires a database name; pass --db-name argument")
	}
	ln, err := net.Listen("tcp", p.addr)
	if err != nil {
		return err
	}
	defer ln.Close()

	logger.Debug("MySQL Proxy listening on %s, upstream: %s", p.addr, p.upstreamAddr)

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
				continue
			}
		}
		go p.handleConn(conn)
	}
}

func (p *Proxy) handleConn(clientConn net.Conn) {
	defer clientConn.Close()

	upstreamConn, err := net.Dial("tcp", p.upstreamAddr)
	if err != nil {
		logger.Error("Proxy: Failed to connect to upstream %s: %v", p.upstreamAddr, err)
		return
	}
	defer upstreamConn.Close()

	// 1. Server -> Client Pipe (Always Transparent)
	go func() { _, _ = io.Copy(clientConn, upstreamConn); clientConn.Close() }()

	// 2. Client -> Server Loop (Intercept Commands)
	// Per-connection prepared statement state:
	// - preparedStmts: maps synthetic stmt_id -> SQL for mocked prepared statements
	// - nextStmtID: auto-incrementing ID counter for synthetic statements
	// Synthetic stmt_ids are assigned by us (never forwarded to upstream). When the
	// client sends COM_STMT_EXECUTE with a synthetic ID we send a mock response.
	// Real stmt_ids (for non-mocked stmts forwarded to upstream) are never stored here.
	preparedStmts := make(map[uint32]string)
	var nextStmtID uint32 = 1

	var clientCapabilities uint32
	for {
		header := make([]byte, 4)
		if _, err := io.ReadFull(clientConn, header); err != nil {
			return
		}
		length := int(uint32(header[0]) | uint32(header[1])<<8 | uint32(header[2])<<16)
		seq := header[3]
		payload := make([]byte, length)
		if _, err := io.ReadFull(clientConn, payload); err != nil {
			return
		}

		// Extract client capabilities from auth response packet (seq=1)
		if seq == 1 && len(payload) >= 4 {
			clientCapabilities = uint32(payload[0]) | uint32(payload[1])<<8 | uint32(payload[2])<<16 | uint32(payload[3])<<24
		}

		if seq == 0 && length > 0 {
			cmd := payload[0]
			if cmd == 0x03 { // COM_QUERY
				queryBytes := payload[1:]
				if clientCapabilities&clientQueryAttributes != 0 {
					stripped, err := stripQueryAttributes(queryBytes)
					if err != nil {
						logger.Debug("Failed to strip query attributes, using raw bytes: %v", err)
					} else {
						queryBytes = stripped
					}
				}
				query := string(queryBytes)

				// Log all queries for debugging
				logger.Debug("Query received: %.80s", query)

				// Check for transparent mode first - pass through everything
				if p.isTransparent() {
					logger.Debug("Transparent mode - passing through: %.50s", query)
					_, _ = upstreamConn.Write(header)
					_, _ = upstreamConn.Write(payload)
				} else if p.isShowFullFieldsQuery(query) {
					tableName := p.extractShowFullFieldsTable(query)
					if columns, ok := p.schemaCache[tableName]; ok {
						logger.Debug("Returning cached schema for table %s", tableName)
						p.sendSchemaResponse(clientConn, columns)
						continue // Don't forward to upstream
					}
					// If not in cache, pass through to upstream
					logger.Debug("Schema cache miss for table %s, passing through", tableName)
					_, _ = upstreamConn.Write(header)
					_, _ = upstreamConn.Write(payload)
				} else if p.isWhitelisted(query) {
					logger.Debug("Whitelisted query passing through: %.50s", query)
					_, _ = upstreamConn.Write(header)
					_, _ = upstreamConn.Write(payload)
				} else {
					p.checkNegativeMocksForQuery(query)
					mock, found := p.findMock(query)
					if found {
						// Store the actual query in the mock for proper hit tracking
						// (only for unconstrained mocks; SQL/SQLContains mocks use their own key)
						if mock.SQL == "" && mock.SQLContains == "" && len(mock.AccessingTables) == 0 {
							mock.SQL = query
						}
						// Execute VERIFY rules if any
						if len(mock.Verify) > 0 {
							if err := verify.VerifySQL(query, mock.Verify); err != nil {
								logger.Error("VERIFY failed: %v", err)
								p.registry.RecordVerifyError(fmt.Sprintf("WRITE:MYSQL [%s]: %v", mock.Table, err))
								// Send error response to client
								p.sendErrorResponse(clientConn, fmt.Sprintf("VERIFY failed: %v", err))
								continue
							}
							logger.Debug("All VERIFY rules passed")
						}
						logger.Debug("Mocking query for table %s: %s", mock.Table, query)
						_ = p.sendMockResponse(clientConn, mock, clientCapabilities)
					} else {
						p.registry.RecordPassthrough("MySQL query: " + query[:min(80, len(query))])
						_, _ = upstreamConn.Write(header)
						_, _ = upstreamConn.Write(payload)
					}
				}
			} else if cmd == 0x01 { // COM_QUIT
				_, _ = upstreamConn.Write(header)
				_, _ = upstreamConn.Write(payload)
				return
			} else if cmd == comStmtPrepare {
				// COM_STMT_PREPARE: check if this query has a mock.
				// If yes, assign a synthetic stmt_id and send StmtPrepareOK without
				// forwarding to upstream. If no mock, forward and let io.Copy relay
				// the upstream's response (including its real stmt_id) to the client.
				sql := string(payload[1:])
				logger.Debug("COM_STMT_PREPARE received: %.80s", sql)
				if p.isTransparent() || p.isWhitelisted(sql) {
					_, _ = upstreamConn.Write(header)
					_, _ = upstreamConn.Write(payload)
				} else {
					_, found := p.peekMock(sql)
					if found {
						stmtID := nextStmtID
						nextStmtID++
						preparedStmts[stmtID] = sql
						logger.Debug("Intercepting COM_STMT_PREPARE stmtID=%d sql=%.80s", stmtID, sql)
						_ = p.sendStmtPrepareOK(clientConn, stmtID, sql, clientCapabilities)
					} else {
						logger.Debug("No mock for COM_STMT_PREPARE, forwarding: %.80s", sql)
						_, _ = upstreamConn.Write(header)
						_, _ = upstreamConn.Write(payload)
					}
				}
			} else if cmd == comStmtExecute {
				// COM_STMT_EXECUTE: if stmtID is one of our synthetic ones, send a
				// mock response. Otherwise forward to upstream.
				if len(payload) < 5 {
					_, _ = upstreamConn.Write(header)
					_, _ = upstreamConn.Write(payload)
				} else {
					stmtID := uint32(payload[1]) | uint32(payload[2])<<8 | uint32(payload[3])<<16 | uint32(payload[4])<<24
					if sql, ok := preparedStmts[stmtID]; ok {
						logger.Debug("COM_STMT_EXECUTE for synthetic stmtID=%d, sql=%.80s", stmtID, sql)
						p.checkNegativeMocksForQuery(sql)
						mock, found := p.findMock(sql)
						if found {
							if mock.SQL == "" && len(mock.AccessingTables) == 0 {
								mock.SQL = sql
							}
							if len(mock.Verify) > 0 {
								if err := verify.VerifySQL(sql, mock.Verify); err != nil {
									logger.Error("VERIFY failed for prepared stmt: %v", err)
									p.registry.RecordVerifyError(fmt.Sprintf("WRITE:MYSQL [%s]: %v", mock.Table, err))
									p.sendErrorResponse(clientConn, fmt.Sprintf("VERIFY failed: %v", err))
									continue
								}
							}
							logger.Debug("Mocking COM_STMT_EXECUTE for table %s", mock.Table)
							_ = p.sendBinaryMockResponse(clientConn, mock, clientCapabilities)
						} else {
							// Synthetic stmt but mock disappeared (e.g. registry reset) — error out.
							p.sendErrorResponse(clientConn, "LineSpec: mock not found for prepared statement")
						}
					} else {
						// Not our stmt — forward to upstream.
						_, _ = upstreamConn.Write(header)
						_, _ = upstreamConn.Write(payload)
					}
				}
			} else if cmd == comStmtSendLongData {
				// COM_STMT_SEND_LONG_DATA: used to send large parameter values.
				// For synthetic stmts we don't use parameter values, so just discard.
				// For upstream stmts, forward normally.
				if len(payload) >= 5 {
					stmtID := uint32(payload[1]) | uint32(payload[2])<<8 | uint32(payload[3])<<16 | uint32(payload[4])<<24
					if _, ok := preparedStmts[stmtID]; !ok {
						_, _ = upstreamConn.Write(header)
						_, _ = upstreamConn.Write(payload)
					}
					// Synthetic stmt: discard (no response per protocol)
				} else {
					_, _ = upstreamConn.Write(header)
					_, _ = upstreamConn.Write(payload)
				}
			} else if cmd == comStmtClose {
				// COM_STMT_CLOSE: no response expected per MySQL protocol.
				if len(payload) >= 5 {
					stmtID := uint32(payload[1]) | uint32(payload[2])<<8 | uint32(payload[3])<<16 | uint32(payload[4])<<24
					if _, ok := preparedStmts[stmtID]; ok {
						delete(preparedStmts, stmtID)
						logger.Debug("COM_STMT_CLOSE for synthetic stmtID=%d, removed from map", stmtID)
						// Don't forward — upstream has no record of our synthetic stmt
					} else {
						_, _ = upstreamConn.Write(header)
						_, _ = upstreamConn.Write(payload)
					}
				} else {
					_, _ = upstreamConn.Write(header)
					_, _ = upstreamConn.Write(payload)
				}
			} else if cmd == comStmtReset {
				// COM_STMT_RESET: resets long-data state of a prepared statement.
				if len(payload) >= 5 {
					stmtID := uint32(payload[1]) | uint32(payload[2])<<8 | uint32(payload[3])<<16 | uint32(payload[4])<<24
					if _, ok := preparedStmts[stmtID]; ok {
						// Acknowledge the reset for our synthetic stmt.
						_ = p.sendMockOK(clientConn)
					} else {
						_, _ = upstreamConn.Write(header)
						_, _ = upstreamConn.Write(payload)
					}
				} else {
					_, _ = upstreamConn.Write(header)
					_, _ = upstreamConn.Write(payload)
				}
			} else {
				_, _ = upstreamConn.Write(header)
				_, _ = upstreamConn.Write(payload)
			}
		} else {
			_, _ = upstreamConn.Write(header)
			_, _ = upstreamConn.Write(payload)
		}
	}
}

// writeResult holds the optional RETURNS payload fields for WRITE operations.
type writeResult struct {
	AffectedRows int64 `yaml:"affected_rows" json:"affected_rows"`
	LastInsertID int64 `yaml:"last_insert_id" json:"last_insert_id"`
}

// loadWriteResult loads and parses a RETURNS payload for a WRITE operation.
func (p *Proxy) loadWriteResult(file string) (writeResult, error) {
	raw, err := p.loader.Load(file)
	if err != nil {
		return writeResult{}, err
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return writeResult{}, fmt.Errorf("write result payload must be a YAML object")
	}
	var r writeResult
	if v, ok := m["affected_rows"]; ok {
		switch n := v.(type) {
		case int:
			r.AffectedRows = int64(n)
		case int64:
			r.AffectedRows = n
		case float64:
			r.AffectedRows = int64(n)
		}
	}
	if v, ok := m["last_insert_id"]; ok {
		switch n := v.(type) {
		case int:
			r.LastInsertID = int64(n)
		case int64:
			r.LastInsertID = n
		case float64:
			r.LastInsertID = int64(n)
		}
	}
	return r, nil
}

// encodeLengthInt encodes n as a MySQL length-encoded integer.
func encodeLengthInt(n int64) []byte {
	if n < 251 {
		return []byte{byte(n)}
	}
	if n < 65536 {
		return []byte{0xFC, byte(n), byte(n >> 8)}
	}
	if n < 16777216 {
		return []byte{0xFD, byte(n), byte(n >> 8), byte(n >> 16)}
	}
	return []byte{0xFE,
		byte(n), byte(n >> 8), byte(n >> 16), byte(n >> 24),
		byte(n >> 32), byte(n >> 40), byte(n >> 48), byte(n >> 56),
	}
}

func (p *Proxy) sendMockResponse(conn net.Conn, mock *types.ExpectStatement, clientCapabilities uint32) error {
	if mock.Channel == types.WriteMySQL {
		if mock.ReturnsFile != "" {
			p.loader.BaseDir = mock.BaseDir
			r, err := p.loadWriteResult(mock.ReturnsFile)
			if err != nil {
				logger.Error("Error loading write result %s: %v", mock.ReturnsFile, err)
				return p.sendMockOK(conn)
			}
			return p.sendMockOKWithResult(conn, r.AffectedRows, r.LastInsertID)
		}
		return p.sendMockOK(conn)
	}

	if mock.Channel == types.ReadMySQL {
		if mock.ReturnsEmpty {
			return p.sendEmptyResultSet(conn, mock.Table, clientCapabilities)
		}

		if mock.ReturnsFile != "" {
			p.loader.BaseDir = mock.BaseDir
			payload, err := p.loader.Load(mock.ReturnsFile)
			if err != nil {
				logger.Error("Error loading payload %s: %v", mock.ReturnsFile, err)
				return p.sendEmptyResultSet(conn, mock.Table, clientCapabilities)
			}
			return p.sendPayloadResultSet(conn, payload, mock.Table, clientCapabilities)
		}

		return p.sendEmptyResultSet(conn, mock.Table, clientCapabilities)
	}

	return p.sendMockOK(conn)
}

func (p *Proxy) sendPayloadResultSet(conn net.Conn, payload interface{}, tableName string, clientCapabilities uint32) error {
	deprecateEOF := clientCapabilities&clientDeprecateEOF != 0
	optionalMeta := clientCapabilities&clientOptionalResultsetMetadata != 0

	var rows []map[string]interface{}

	data, ok := payload.(map[string]interface{})
	if !ok {
		list, ok := payload.([]interface{})
		if ok {
			for _, item := range list {
				if m, ok := item.(map[string]interface{}); ok {
					rows = append(rows, m)
				}
			}
		}
	} else {
		rowsRaw, ok := data["rows"].([]interface{})
		if ok {
			for _, item := range rowsRaw {
				if m, ok := item.(map[string]interface{}); ok {
					rows = append(rows, m)
				}
			}
		} else {
			rows = append(rows, data)
		}
	}

	if len(rows) == 0 {
		return p.sendEmptyResultSet(conn, tableName, clientCapabilities)
	}

	firstRow := rows[0]
	columns := []string{"id", "name", "email", "password", "token", "created_at", "updated_at"}
	for k := range firstRow {
		found := false
		for _, c := range columns {
			if k == c {
				found = true
				break
			}
		}
		if !found {
			columns = append(columns, k)
		}
	}

	finalColumns := make([]string, 0, len(columns))
	for _, col := range columns {
		if _, ok := firstRow[col]; ok {
			finalColumns = append(finalColumns, col)
		}
	}
	columns = finalColumns

	// Column count packet: if CLIENT_OPTIONAL_RESULTSET_METADATA is set,
	// prefix with metadata_follows=0x01 to indicate column definitions follow.
	colCountPayload := []byte{byte(len(columns))}
	if optionalMeta {
		colCountPayload = append([]byte{0x01}, colCountPayload...)
	}
	if err := p.writePacket(conn, 1, colCountPayload); err != nil {
		return err
	}

	seq := uint8(2)
	for _, col := range columns {
		tp := mysql.MYSQL_TYPE_VAR_STRING
		flags := uint16(0)
		val, ok := firstRow[col]
		if ok {
			switch val.(type) {
			case int, int64, float64:
				tp = mysql.MYSQL_TYPE_LONGLONG
				if col == "id" {
					flags = 3
				}
			}
		}
		colDef := p.makeColumnDef(p.dbConfig.GetDatabaseName(), tableName, col, tp, flags)
		if err := p.writePacket(conn, seq, colDef); err != nil {
			return err
		}
		seq++
	}

	// Without CLIENT_DEPRECATE_EOF: send EOF after column definitions.
	if !deprecateEOF {
		if err := p.writePacket(conn, seq, []byte{0xfe, 0, 0, 0x22, 0}); err != nil {
			return err
		}
		seq++
	}

	for _, row := range rows {
		var rowData []byte
		for _, col := range columns {
			val := row[col]
			if val == nil {
				rowData = append(rowData, 0xfb)
			} else {
				strVal := fmt.Sprintf("%v", val)
				rowData = append(rowData, mysql.PutLengthEncodedString([]byte(strVal))...)
			}
		}
		if err := p.writePacket(conn, seq, rowData); err != nil {
			return err
		}
		seq++
	}

	// With CLIENT_DEPRECATE_EOF: send OK packet instead of final EOF.
	if deprecateEOF {
		okPayload := []byte{0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00}
		return p.writePacket(conn, seq, okPayload)
	}
	return p.writePacket(conn, seq, []byte{0xfe, 0, 0, 0x22, 0})
}

func (p *Proxy) sendMockOK(conn net.Conn) error {
	return p.sendMockOKWithResult(conn, 0, 0)
}

// sendMockOKWithResult sends a MySQL OK packet with explicit affected_rows and last_insert_id.
// MySQL OK packet: 0x00 | affected_rows (lenenc) | last_insert_id (lenenc) | status (2 LE) | warnings (2 LE)
func (p *Proxy) sendMockOKWithResult(conn net.Conn, affectedRows, lastInsertID int64) error {
	payload := make([]byte, 0, 7)
	payload = append(payload, 0x00) // OK indicator
	payload = append(payload, encodeLengthInt(affectedRows)...)
	payload = append(payload, encodeLengthInt(lastInsertID)...)
	payload = append(payload, 0x02, 0x00) // SERVER_STATUS_AUTOCOMMIT
	payload = append(payload, 0x00, 0x00) // warnings
	return p.writePacket(conn, 1, payload)
}

// sendErrorResponse sends a MySQL error packet to the client
func (p *Proxy) sendErrorResponse(conn net.Conn, message string) error {
	// MySQL Error Packet format:
	// 1 byte: 0xff (error indicator)
	// 2 bytes: error code (16-bit little-endian)
	// 1 byte: SQL state marker '#'
	// 5 bytes: SQL state string
	// n bytes: error message (string)
	errorCode := uint16(1064) // ER_PARSE_ERROR - generic syntax error
	sqlState := "42000"       // SQLSTATE for syntax error or access violation

	payload := make([]byte, 0, 9+len(message))
	payload = append(payload, 0xff)                                // Error indicator
	payload = append(payload, byte(errorCode), byte(errorCode>>8)) // Error code (little-endian)
	payload = append(payload, '#')                                 // SQL state marker
	payload = append(payload, []byte(sqlState)...)                 // SQL state
	payload = append(payload, []byte(message)...)                  // Error message

	return p.writePacket(conn, 1, payload)
}

// sendStmtPrepareOK sends a COM_STMT_PREPARE_OK response for a synthetic prepared statement.
// num_params is derived by counting '?' placeholders in the SQL; num_columns is always 0
// (the actual result columns are defined in the COM_STMT_EXECUTE response).
func (p *Proxy) sendStmtPrepareOK(conn net.Conn, stmtID uint32, sql string, clientCapabilities uint32) error {
	numParams := uint16(strings.Count(sql, "?"))

	// Packet 1: PrepareOK
	prepOK := []byte{
		0x00,                                                                    // OK marker
		byte(stmtID), byte(stmtID >> 8), byte(stmtID >> 16), byte(stmtID >> 24), // stmt_id (LE)
		0, 0, // num_columns = 0
		byte(numParams), byte(numParams >> 8), // num_params (LE)
		0x00, // reserved
		0, 0, // warning_count = 0
	}
	if err := p.writePacket(conn, 1, prepOK); err != nil {
		return err
	}

	if numParams == 0 {
		return nil
	}

	// Send a minimal ColumnDefinition for each parameter placeholder.
	// Clients use num_params to allocate bind buffers; the actual types are
	// sent by the client in COM_STMT_EXECUTE's new_params_bound section.
	seq := uint8(2)
	for i := uint16(0); i < numParams; i++ {
		colDef := p.makeColumnDef("", "", "?", mysql.MYSQL_TYPE_VAR_STRING, 0)
		if err := p.writePacket(conn, seq, colDef); err != nil {
			return err
		}
		seq++
	}
	// EOF after param definitions (CLIENT_DEPRECATE_EOF does not affect COM_STMT_PREPARE EOFs)
	return p.writePacket(conn, seq, []byte{0xfe, 0, 0, 0x22, 0})
}

// sendBinaryMockResponse sends the mock response for a COM_STMT_EXECUTE command.
// The row format uses the MySQL binary result set protocol (0x00 header + null bitmap + values).
func (p *Proxy) sendBinaryMockResponse(conn net.Conn, mock *types.ExpectStatement, clientCapabilities uint32) error {
	if mock.Channel == types.WriteMySQL {
		if mock.ReturnsFile != "" {
			p.loader.BaseDir = mock.BaseDir
			r, err := p.loadWriteResult(mock.ReturnsFile)
			if err != nil {
				logger.Error("Error loading write result %s: %v", mock.ReturnsFile, err)
				return p.sendMockOK(conn)
			}
			return p.sendMockOKWithResult(conn, r.AffectedRows, r.LastInsertID)
		}
		return p.sendMockOK(conn)
	}

	if mock.Channel == types.ReadMySQL {
		if mock.ReturnsEmpty {
			// Empty result sets use the same packet structure in text and binary protocols.
			return p.sendEmptyResultSet(conn, mock.Table, clientCapabilities)
		}

		if mock.ReturnsFile != "" {
			p.loader.BaseDir = mock.BaseDir
			payload, err := p.loader.Load(mock.ReturnsFile)
			if err != nil {
				logger.Error("Error loading payload %s: %v", mock.ReturnsFile, err)
				return p.sendEmptyResultSet(conn, mock.Table, clientCapabilities)
			}
			return p.sendBinaryPayloadResultSet(conn, payload, mock.Table, clientCapabilities)
		}

		return p.sendEmptyResultSet(conn, mock.Table, clientCapabilities)
	}

	return p.sendMockOK(conn)
}

// sendBinaryPayloadResultSet sends a binary-protocol result set for COM_STMT_EXECUTE.
// Column metadata packets are identical to text protocol; rows differ: each row starts with
// 0x00 followed by a null bitmap, then MYSQL_TYPE_VAR_STRING values (length-encoded strings).
func (p *Proxy) sendBinaryPayloadResultSet(conn net.Conn, payload interface{}, tableName string, clientCapabilities uint32) error {
	deprecateEOF := clientCapabilities&clientDeprecateEOF != 0
	optionalMeta := clientCapabilities&clientOptionalResultsetMetadata != 0

	var rows []map[string]interface{}

	data, ok := payload.(map[string]interface{})
	if !ok {
		list, ok := payload.([]interface{})
		if ok {
			for _, item := range list {
				if m, ok := item.(map[string]interface{}); ok {
					rows = append(rows, m)
				}
			}
		}
	} else {
		rowsRaw, ok := data["rows"].([]interface{})
		if ok {
			for _, item := range rowsRaw {
				if m, ok := item.(map[string]interface{}); ok {
					rows = append(rows, m)
				}
			}
		} else {
			rows = append(rows, data)
		}
	}

	if len(rows) == 0 {
		return p.sendEmptyResultSet(conn, tableName, clientCapabilities)
	}

	firstRow := rows[0]
	columns := []string{"id", "name", "email", "password", "token", "created_at", "updated_at"}
	for k := range firstRow {
		found := false
		for _, c := range columns {
			if k == c {
				found = true
				break
			}
		}
		if !found {
			columns = append(columns, k)
		}
	}

	finalColumns := make([]string, 0, len(columns))
	for _, col := range columns {
		if _, ok := firstRow[col]; ok {
			finalColumns = append(finalColumns, col)
		}
	}
	columns = finalColumns

	// Column count packet (identical to text protocol)
	colCountPayload := []byte{byte(len(columns))}
	if optionalMeta {
		colCountPayload = append([]byte{0x01}, colCountPayload...)
	}
	if err := p.writePacket(conn, 1, colCountPayload); err != nil {
		return err
	}

	seq := uint8(2)
	for _, col := range columns {
		tp := mysql.MYSQL_TYPE_VAR_STRING
		flags := uint16(0)
		if val, ok := firstRow[col]; ok {
			switch val.(type) {
			case int, int64, float64:
				tp = mysql.MYSQL_TYPE_LONGLONG
				if col == "id" {
					flags = 3
				}
			}
		}
		colDef := p.makeColumnDef(p.dbConfig.GetDatabaseName(), tableName, col, tp, flags)
		if err := p.writePacket(conn, seq, colDef); err != nil {
			return err
		}
		seq++
	}

	// EOF after column definitions (same as text protocol)
	if !deprecateEOF {
		if err := p.writePacket(conn, seq, []byte{0xfe, 0, 0, 0x22, 0}); err != nil {
			return err
		}
		seq++
	}

	// Binary rows: 0x00 header + null bitmap + length-encoded values
	numCols := len(columns)
	nullBitmapLen := (numCols + 7 + 2) / 8 // offset of 2 per MySQL binary protocol spec
	for _, row := range rows {
		// Build null bitmap (bit = 1 if column is NULL; bit index = col_index + 2)
		nullBitmap := make([]byte, nullBitmapLen)
		for i, col := range columns {
			if row[col] == nil {
				bitPos := i + 2
				nullBitmap[bitPos/8] |= 1 << (uint(bitPos) % 8)
			}
		}

		rowData := make([]byte, 0, 1+nullBitmapLen+numCols*16)
		rowData = append(rowData, 0x00) // binary row header
		rowData = append(rowData, nullBitmap...)

		for _, col := range columns {
			val := row[col]
			if val == nil {
				continue // represented in null bitmap
			}
			strVal := fmt.Sprintf("%v", val)
			rowData = append(rowData, mysql.PutLengthEncodedString([]byte(strVal))...)
		}

		if err := p.writePacket(conn, seq, rowData); err != nil {
			return err
		}
		seq++
	}

	// Final terminator (same as text protocol)
	if deprecateEOF {
		return p.writePacket(conn, seq, []byte{0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00})
	}
	return p.writePacket(conn, seq, []byte{0xfe, 0, 0, 0x22, 0})
}

func (p *Proxy) sendEmptyResultSet(conn net.Conn, tableName string, clientCapabilities uint32) error {
	deprecateEOF := clientCapabilities&clientDeprecateEOF != 0

	if err := p.writePacket(conn, 1, []byte{1}); err != nil {
		return err
	}
	colDef := p.makeColumnDef(p.dbConfig.GetDatabaseName(), tableName, "id", mysql.MYSQL_TYPE_LONGLONG, 3)
	if err := p.writePacket(conn, 2, colDef); err != nil {
		return err
	}

	// Without CLIENT_DEPRECATE_EOF: send EOF after column definitions.
	if !deprecateEOF {
		if err := p.writePacket(conn, 3, []byte{0xfe, 0, 0, 0x22, 0}); err != nil {
			return err
		}
		// Final EOF (no rows)
		return p.writePacket(conn, 4, []byte{0xfe, 0, 0, 0x22, 0})
	}

	// With CLIENT_DEPRECATE_EOF: no intermediate EOF; send OK as final terminator.
	okPayload := []byte{0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00}
	return p.writePacket(conn, 3, okPayload)
}

func (p *Proxy) writePacket(conn net.Conn, seq uint8, payload []byte) error {
	length := len(payload)
	header := []byte{
		byte(length),
		byte(length >> 8),
		byte(length >> 16),
		seq,
	}
	_, err := conn.Write(append(header, payload...))
	return err
}

func (p *Proxy) makeColumnDef(schema, table, col string, tp uint8, flags uint16) []byte {
	data := make([]byte, 0, 100)
	data = append(data, mysql.PutLengthEncodedString([]byte("def"))...)
	data = append(data, mysql.PutLengthEncodedString([]byte(schema))...)
	data = append(data, mysql.PutLengthEncodedString([]byte(table))...)
	data = append(data, mysql.PutLengthEncodedString([]byte(table))...)
	data = append(data, mysql.PutLengthEncodedString([]byte(col))...)
	data = append(data, mysql.PutLengthEncodedString([]byte(col))...)
	data = append(data, 0x0c, 45, 0, 0xff, 0, 0, 0, tp, byte(flags), byte(flags>>8), 0, 0, 0)
	return data
}

func (p *Proxy) isWhitelisted(query string) bool {
	q := strings.TrimSpace(strings.ToUpper(query))
	prefixes := []string{
		"SET ", "SHOW ", "CREATE ", "ALTER ", "DROP ", "DESCRIBE ", "EXPLAIN ",
		"SELECT @@", "SELECT DATABASE()", "SELECT GET_LOCK", "SELECT RELEASE_LOCK",
		"BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE SAVEPOINT",
	}
	for _, pref := range prefixes {
		if strings.HasPrefix(q, pref) {
			return true
		}
	}
	if q == "SELECT 1" {
		return true
	}
	// Allow INFORMATION_SCHEMA queries to pass through
	// Migration tables are handled through normal table extraction
	if strings.Contains(q, "INFORMATION_SCHEMA") {
		return true
	}
	return false
}

// ── Semantic SQL matching helpers ─────────────────────────────────────────────

// extractAllTables scans the query for every registered table name.
func (p *Proxy) extractAllTables(query string) []string {
	q := strings.ToLower(query)
	known := p.getKnownTables()
	seen := make(map[string]bool)
	var tables []string
	for _, t := range known {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(t) + `\b`)
		if re.MatchString(q) && !seen[t] {
			seen[t] = true
			tables = append(tables, t)
		}
	}
	return tables
}

// extractQueryOperation returns the first SQL DML keyword.
func (p *Proxy) extractQueryOperation(query string) string {
	upper := strings.ToUpper(strings.TrimSpace(query))
	switch {
	case strings.HasPrefix(upper, "SELECT"), strings.HasPrefix(upper, "WITH"):
		return "SELECT"
	case strings.HasPrefix(upper, "INSERT"):
		return "INSERT"
	case strings.HasPrefix(upper, "UPDATE"):
		return "UPDATE"
	case strings.HasPrefix(upper, "DELETE"):
		return "DELETE"
	}
	return ""
}

var (
	mysqlReWhereCondition = regexp.MustCompile(`(?i)\b(\w+)\s*=\s*('[^']*'|\d+(?:\.\d+)?|\?)`)
	mysqlReInsertCols     = regexp.MustCompile(`(?i)INSERT\s+(?:INTO\s+)?\w+\s*\(([^)]+)\)`)
	mysqlReInsertVals     = regexp.MustCompile(`(?i)\)\s*VALUES?\s*\(([^)]+)\)`)
	mysqlReUpdateSet      = regexp.MustCompile(`(?i)SET\s+(.+?)(?:\s+WHERE\s+|\s*$)`)
	mysqlReSetItem        = regexp.MustCompile(`(?i)(\w+)\s*=\s*('[^']*'|\d+(?:\.\d+)?|\?)`)
)

// extractWhereInfo extracts WHERE clause column names and resolved values.
func (p *Proxy) extractWhereInfo(query string) (columns []string, values map[string]string) {
	upper := strings.ToUpper(query)
	whereIdx := strings.Index(upper, " WHERE ")
	if whereIdx == -1 {
		return nil, nil
	}
	wherePart := query[whereIdx+7:]
	// Trim ORDER BY / LIMIT etc.
	for _, kw := range []string{" ORDER ", " LIMIT ", " GROUP ", " HAVING "} {
		if i := strings.Index(strings.ToUpper(wherePart), kw); i != -1 {
			wherePart = wherePart[:i]
		}
	}
	values = make(map[string]string)
	matches := mysqlReWhereCondition.FindAllStringSubmatch(wherePart, -1)
	for _, m := range matches {
		col := strings.ToLower(m[1])
		val := strings.Trim(m[2], "'")
		if val == "?" {
			val = "PRESENT"
		}
		columns = append(columns, col)
		values[col] = val
	}
	return columns, values
}

// extractWrittenValuesFromSQL extracts written column/value pairs for INSERT/UPDATE.
func (p *Proxy) extractWrittenValuesFromSQL(query, operation string) map[string]string {
	result := make(map[string]string)
	switch operation {
	case "INSERT":
		colM := mysqlReInsertCols.FindStringSubmatch(query)
		valM := mysqlReInsertVals.FindStringSubmatch(query)
		if colM == nil || valM == nil {
			return result
		}
		cols := splitCommaTrimmedMySQL(colM[1])
		vals := splitCommaTrimmedMySQL(valM[1])
		for i, col := range cols {
			if i < len(vals) {
				v := strings.Trim(vals[i], "'")
				if v == "?" {
					v = "PRESENT"
				}
				result[strings.ToLower(col)] = v
			}
		}
	case "UPDATE":
		setM := mysqlReUpdateSet.FindStringSubmatch(query)
		if setM == nil {
			return result
		}
		items := mysqlReSetItem.FindAllStringSubmatch(setM[1], -1)
		for _, m := range items {
			v := strings.Trim(m[2], "'")
			if v == "?" {
				v = "PRESENT"
			}
			result[strings.ToLower(m[1])] = v
		}
	}
	return result
}

func splitCommaTrimmedMySQL(s string) []string {
	parts := strings.Split(s, ",")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

// findMock tries semantic matching first, then falls back to legacy table-based matching.
func (p *Proxy) findMock(query string) (*types.ExpectStatement, bool) {
	tables := p.extractAllTables(query)
	if len(tables) > 0 {
		op := p.extractQueryOperation(query)
		whereCols, whereVals := p.extractWhereInfo(query)
		written := p.extractWrittenValuesFromSQL(query, op)
		if mock, ok := p.registry.FindMockByTables(tables, op, whereCols, whereVals, written); ok {
			return mock, true
		}
	}
	tableName := p.extractTable(query)
	return p.registry.FindMock(tableName, query)
}

// peekMock tries semantic matching first, then falls back to legacy table-based matching.
func (p *Proxy) peekMock(query string) (*types.ExpectStatement, bool) {
	tables := p.extractAllTables(query)
	if len(tables) > 0 {
		op := p.extractQueryOperation(query)
		whereCols, whereVals := p.extractWhereInfo(query)
		written := p.extractWrittenValuesFromSQL(query, op)
		if mock, ok := p.registry.PeekMockByTables(tables, op, whereCols, whereVals, written); ok {
			return mock, true
		}
	}
	tableName := p.extractTable(query)
	return p.registry.PeekMock(tableName, query)
}

// checkNegativeMocksForQuery checks both semantic and legacy negative expectations.
func (p *Proxy) checkNegativeMocksForQuery(query string) {
	tables := p.extractAllTables(query)
	if len(tables) > 0 {
		op := p.extractQueryOperation(query)
		whereCols, whereVals := p.extractWhereInfo(query)
		written := p.extractWrittenValuesFromSQL(query, op)
		p.registry.CheckNegativeMocksByTables(tables, op, whereCols, whereVals, written)
	}
	tableName := p.extractTable(query)
	p.registry.CheckNegativeMocks(tableName, query)
}

// ── End semantic SQL matching helpers ─────────────────────────────────────────

func (p *Proxy) extractTable(query string) string {
	q := strings.ReplaceAll(strings.ToLower(query), "`", " ")
	q = strings.ReplaceAll(q, "(", " ")
	q = strings.ReplaceAll(q, ")", " ")
	q = strings.ReplaceAll(q, ",", " ")
	q = strings.ReplaceAll(q, ";", " ")

	// Use configurable table list from schema cache if available
	knownTables := p.getKnownTables()

	for _, table := range knownTables {
		re := regexp.MustCompile(`\b` + table + `\b`)
		if re.MatchString(q) {
			return table
		}
	}

	words := strings.Fields(q)
	for i, word := range words {
		if word == "from" || word == "into" || word == "update" || word == "table" {
			if i+1 < len(words) {
				table := words[i+1]
				if idx := strings.Index(table, "."); idx != -1 {
					return table[:idx]
				}
				return table
			}
		}
	}
	return "unknown"
}

// getKnownTables returns the list of known tables from schema cache or defaults
func (p *Proxy) getKnownTables() []string {
	// If we have tables in schema cache, use those
	if len(p.schemaCache) > 0 {
		tables := make([]string, 0, len(p.schemaCache))
		for table := range p.schemaCache {
			tables = append(tables, table)
		}
		return tables
	}

	return []string{}
}

// extractShowFullFieldsTable extracts table name from SHOW FULL FIELDS FROM <table> query
func (p *Proxy) extractShowFullFieldsTable(query string) string {
	// Match patterns like:
	// SHOW FULL FIELDS FROM `users`
	// SHOW FULL FIELDS FROM users
	// SHOW FULL COLUMNS FROM `users`
	// SHOW COLUMNS FROM `users`
	patterns := []string{
		`(?i)SHOW\s+FULL\s+FIELDS\s+FROM\s+\x60?(\w+)\x60?`,
		`(?i)SHOW\s+FULL\s+COLUMNS\s+FROM\s+\x60?(\w+)\x60?`,
		`(?i)SHOW\s+FIELDS\s+FROM\s+\x60?(\w+)\x60?`,
		`(?i)SHOW\s+COLUMNS\s+FROM\s+\x60?(\w+)\x60?`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(query)
		if len(matches) >= 2 {
			table := matches[1]
			// Convert to lowercase for consistent lookup
			return strings.ToLower(table)
		}
	}

	return ""
}

// isShowFullFieldsQuery checks if the query is a SHOW FULL FIELDS/COLUMNS query
func (p *Proxy) isShowFullFieldsQuery(query string) bool {
	return p.extractShowFullFieldsTable(query) != ""
}

// sendSchemaResponse sends a MySQL result set response for SHOW FULL FIELDS from cached schema
func (p *Proxy) sendSchemaResponse(conn net.Conn, columns []ColumnInfo) error {
	// MySQL SHOW FULL FIELDS returns 9 columns:
	// Field, Type, Collation, Null, Key, Default, Extra, Privileges, Comment
	columnNames := []string{"Field", "Type", "Collation", "Null", "Key", "Default", "Extra", "Privileges", "Comment"}

	// Column count packet (seq=1)
	if err := p.writePacket(conn, 1, []byte{byte(len(columnNames))}); err != nil {
		return err
	}

	// Column definition packets (seq=2 to seq=10)
	seq := uint8(2)
	for _, colName := range columnNames {
		colDef := p.makeColumnDef(p.dbConfig.GetDatabaseName(), "", colName, mysql.MYSQL_TYPE_VAR_STRING, 0)
		if err := p.writePacket(conn, seq, colDef); err != nil {
			return err
		}
		seq++
	}

	// EOF packet after column definitions (seq=10)
	if err := p.writePacket(conn, seq, []byte{0xfe, 0, 0, 0x22, 0}); err != nil {
		return err
	}
	seq++

	// Row data packets
	for _, col := range columns {
		var rowData []byte

		// Field (column name)
		rowData = append(rowData, mysql.PutLengthEncodedString([]byte(col.Field))...)

		// Type
		rowData = append(rowData, mysql.PutLengthEncodedString([]byte(col.Type))...)

		// Collation (can be nil)
		if !col.Collation.Valid || col.Collation.String == "" {
			rowData = append(rowData, 0xfb) // NULL
		} else {
			rowData = append(rowData, mysql.PutLengthEncodedString([]byte(col.Collation.String))...)
		}

		// Null (YES/NO)
		rowData = append(rowData, mysql.PutLengthEncodedString([]byte(col.Null))...)

		// Key (PRI, UNI, MUL, or empty)
		rowData = append(rowData, mysql.PutLengthEncodedString([]byte(col.Key))...)

		// Default (can be nil)
		if !col.Default.Valid || col.Default.String == "" {
			rowData = append(rowData, 0xfb) // NULL
		} else {
			rowData = append(rowData, mysql.PutLengthEncodedString([]byte(col.Default.String))...)
		}

		// Extra
		rowData = append(rowData, mysql.PutLengthEncodedString([]byte(col.Extra))...)

		// Privileges (use default if not specified)
		privileges := col.Privileges
		if privileges == "" {
			privileges = "select,insert,update,references"
		}
		rowData = append(rowData, mysql.PutLengthEncodedString([]byte(privileges))...)

		// Comment (use empty if not specified)
		comment := col.Comment
		rowData = append(rowData, mysql.PutLengthEncodedString([]byte(comment))...)

		if err := p.writePacket(conn, seq, rowData); err != nil {
			return err
		}
		seq++
	}

	// Final EOF packet
	return p.writePacket(conn, seq, []byte{0xfe, 0, 0, 0x22, 0})
}
