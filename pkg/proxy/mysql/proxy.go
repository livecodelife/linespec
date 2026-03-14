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
	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/types"
	"github.com/livecodelife/linespec/pkg/verify"
)

type Proxy struct {
	addr             string
	upstreamAddr     string
	registry         *registry.MockRegistry
	loader           *dsl.PayloadLoader
	schemaCache      map[string][]ColumnInfo // table name -> column definitions
	transparentMode  bool                    // When true, pass through all queries
	transparentUntil time.Time               // Time until which to stay in transparent mode
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
		loader:          &dsl.PayloadLoader{},
		schemaCache:     make(map[string][]ColumnInfo),
		transparentMode: false,
	}
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

func (p *Proxy) Start(ctx context.Context) error {
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
	go func() {
		_, _ = io.Copy(clientConn, upstreamConn)
		clientConn.Close()
	}()

	// 2. Client -> Server Loop (Intercept Commands)
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

		if seq == 0 && length > 0 {
			cmd := payload[0]
			if cmd == 0x03 { // COM_QUERY
				query := string(payload[1:])

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
					tableName := p.extractTable(query)
					mock, found := p.registry.FindMock(tableName, query)
					if found {
						// Store the actual query in the mock for proper hit tracking
						if mock.SQL == "" {
							mock.SQL = query
						}
						// Execute VERIFY rules if any
						if len(mock.Verify) > 0 {
							if err := verify.VerifySQL(query, mock.Verify); err != nil {
								logger.Error("VERIFY failed for table %s: %v", tableName, err)
								// Send error response to client
								p.sendErrorResponse(clientConn, fmt.Sprintf("VERIFY failed: %v", err))
								continue
							}
							logger.Debug("All VERIFY rules passed for table %s", tableName)
						}
						logger.Debug("Mocking query for table %s: %s", tableName, query)
						p.sendMockResponse(clientConn, mock)
					} else {
						_, _ = upstreamConn.Write(header)
						_, _ = upstreamConn.Write(payload)
					}
				}
			} else if cmd == 0x01 { // COM_QUIT
				_, _ = upstreamConn.Write(header)
				_, _ = upstreamConn.Write(payload)
				return
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

func (p *Proxy) sendMockResponse(conn net.Conn, mock *types.ExpectStatement) {
	if mock.Channel == types.WriteMySQL {
		_ = p.sendMockOK(conn)
		return
	}

	if mock.Channel == types.ReadMySQL {
		if mock.ReturnsEmpty {
			_ = p.sendEmptyResultSet(conn, mock.Table)
			return
		}

		if mock.ReturnsFile != "" {
			p.loader.BaseDir = mock.BaseDir
			payload, err := p.loader.Load(mock.ReturnsFile)
			if err != nil {
				logger.Error("Error loading payload %s: %v", mock.ReturnsFile, err)
				_ = p.sendEmptyResultSet(conn, mock.Table)
				return
			}
			_ = p.sendPayloadResultSet(conn, payload, mock.Table)
			return
		}

		_ = p.sendEmptyResultSet(conn, mock.Table)
		return
	}

	_ = p.sendMockOK(conn)
}

func (p *Proxy) sendPayloadResultSet(conn net.Conn, payload interface{}, tableName string) error {
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
		return p.sendEmptyResultSet(conn, tableName)
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

	if err := p.writePacket(conn, 1, []byte{byte(len(columns))}); err != nil {
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
		colDef := p.makeColumnDef("todo_api_development", tableName, col, tp, flags)
		if err := p.writePacket(conn, seq, colDef); err != nil {
			return err
		}
		seq++
	}

	if err := p.writePacket(conn, seq, []byte{0xfe, 0, 0, 0x22, 0}); err != nil {
		return err
	}
	seq++

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

	return p.writePacket(conn, seq, []byte{0xfe, 0, 0, 0x22, 0})
}

func (p *Proxy) sendMockOK(conn net.Conn) error {
	payload := []byte{0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00}
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

func (p *Proxy) sendEmptyResultSet(conn net.Conn, tableName string) error {
	if err := p.writePacket(conn, 1, []byte{1}); err != nil {
		return err
	}
	colDef := p.makeColumnDef("todo_api_development", tableName, "id", mysql.MYSQL_TYPE_LONGLONG, 3)
	if err := p.writePacket(conn, 2, colDef); err != nil {
		return err
	}
	if err := p.writePacket(conn, 3, []byte{0xfe, 0, 0, 0x22, 0}); err != nil {
		return err
	}
	return p.writePacket(conn, 4, []byte{0xfe, 0, 0, 0x22, 0})
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
	if strings.Contains(q, "INFORMATION_SCHEMA") || strings.Contains(q, "SCHEMA_MIGRATIONS") || strings.Contains(q, "AR_INTERNAL_METADATA") {
		return true
	}
	return false
}

func (p *Proxy) extractTable(query string) string {
	q := strings.ReplaceAll(strings.ToLower(query), "`", " ")
	q = strings.ReplaceAll(q, "(", " ")
	q = strings.ReplaceAll(q, ")", " ")
	q = strings.ReplaceAll(q, ",", " ")
	q = strings.ReplaceAll(q, ";", " ")

	knownTables := []string{"users", "todos", "ar_internal_metadata", "schema_migrations"}

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
		colDef := p.makeColumnDef("todo_api_development", "", colName, mysql.MYSQL_TYPE_VAR_STRING, 0)
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
