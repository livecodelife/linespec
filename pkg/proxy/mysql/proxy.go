package mysql

import (
	"context"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"

	"github.com/calebcowen/linespec/pkg/dsl"
	"github.com/calebcowen/linespec/pkg/registry"
	"github.com/calebcowen/linespec/pkg/types"
	"github.com/go-mysql-org/go-mysql/mysql"
)

type Proxy struct {
	addr         string
	upstreamAddr string
	registry     *registry.MockRegistry
	loader       *dsl.PayloadLoader
}

func NewProxy(addr, upstreamAddr string, reg *registry.MockRegistry) *Proxy {
	return &Proxy{
		addr:         addr,
		upstreamAddr: upstreamAddr,
		registry:     reg,
		loader:       &dsl.PayloadLoader{},
	}
}

func (p *Proxy) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", p.addr)
	if err != nil {
		return err
	}
	defer ln.Close()

	fmt.Printf("MySQL Proxy listening on %s, upstream: %s\n", p.addr, p.upstreamAddr)

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
		fmt.Printf("Proxy: Failed to connect to upstream %s: %v\n", p.upstreamAddr, err)
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
				if p.isWhitelisted(query) {
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
						fmt.Printf("Proxy: Mocking query for table %s: %s\n", tableName, query)
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
				fmt.Printf("Proxy: Error loading payload %s: %v\n", mock.ReturnsFile, err)
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
