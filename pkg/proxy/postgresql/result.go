package postgresql

import (
	"encoding/binary"
	"fmt"
	"net"
)

// ResultHandler generates PostgreSQL result set messages
type ResultHandler struct{}

func NewResultHandler() *ResultHandler {
	return &ResultHandler{}
}

// SendResultSet sends a complete result set to the client
func (r *ResultHandler) SendResultSet(conn net.Conn, columns []string, rows []map[string]interface{}) error {
	// 1. Send RowDescription
	if err := r.SendRowDescription(conn, columns); err != nil {
		return fmt.Errorf("error sending row description: %w", err)
	}

	// 2. Send DataRows
	for _, row := range rows {
		if err := r.SendDataRow(conn, columns, row); err != nil {
			return fmt.Errorf("error sending data row: %w", err)
		}
	}

	// 3. Send CommandComplete
	cmdTag := fmt.Sprintf("SELECT %d", len(rows))
	if _, err := conn.Write(CreateCommandComplete(cmdTag)); err != nil {
		return fmt.Errorf("error sending command complete: %w", err)
	}

	// 4. Send ReadyForQuery
	if _, err := conn.Write(CreateReadyForQuery('I')); err != nil {
		return fmt.Errorf("error sending ready for query: %w", err)
	}

	return nil
}

// SendEmptyResultSet sends an empty result set
func (r *ResultHandler) SendEmptyResultSet(conn net.Conn, columns []string) error {
	// Send RowDescription
	if err := r.SendRowDescription(conn, columns); err != nil {
		return fmt.Errorf("error sending row description: %w", err)
	}

	// Send CommandComplete with 0 rows
	if _, err := conn.Write(CreateCommandComplete("SELECT 0")); err != nil {
		return fmt.Errorf("error sending command complete: %w", err)
	}

	// Send ReadyForQuery
	if _, err := conn.Write(CreateReadyForQuery('I')); err != nil {
		return fmt.Errorf("error sending ready for query: %w", err)
	}

	return nil
}

// SendCommandComplete sends just CommandComplete for non-SELECT operations
func (r *ResultHandler) SendCommandComplete(conn net.Conn, tag string) error {
	if _, err := conn.Write(CreateCommandComplete(tag)); err != nil {
		return fmt.Errorf("error sending command complete: %w", err)
	}

	if _, err := conn.Write(CreateReadyForQuery('I')); err != nil {
		return fmt.Errorf("error sending ready for query: %w", err)
	}

	return nil
}

// SendRowDescription sends RowDescription message
func (r *ResultHandler) SendRowDescription(conn net.Conn, columns []string) error {
	// Field count (2 bytes)
	fieldCount := uint16(len(columns))
	payload := make([]byte, 0, 2+len(columns)*20) // Estimate size

	fieldCountBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(fieldCountBytes, fieldCount)
	payload = append(payload, fieldCountBytes...)

	// For each column, add:
	// - Field name (null-terminated string)
	// - Table OID (4 bytes) - 0 for not associated with a table
	// - Column number (2 bytes) - 0
	// - Type OID (4 bytes) - use TEXT (25) for simplicity
	// - Type size (2 bytes) - -1 for variable
	// - Type modifier (4 bytes) - -1
	// - Format code (2 bytes) - 0 for text

	for _, col := range columns {
		// Field name
		payload = append(payload, []byte(col)...)
		payload = append(payload, 0) // null terminator

		// Table OID
		payload = append(payload, 0, 0, 0, 0)

		// Column number
		payload = append(payload, 0, 0)

		// Type OID - TEXT = 25
		typeOID := make([]byte, 4)
		binary.BigEndian.PutUint32(typeOID, 25)
		payload = append(payload, typeOID...)

		// Type size - -1 for variable
		typeSize := make([]byte, 2)
		binary.BigEndian.PutUint16(typeSize, 0xFFFF) // -1 as uint16
		payload = append(payload, typeSize...)

		// Type modifier - -1
		typeMod := make([]byte, 4)
		binary.BigEndian.PutUint32(typeMod, 0xFFFFFFFF) // -1 as uint32
		payload = append(payload, typeMod...)

		// Format code - 0 (text)
		payload = append(payload, 0, 0)
	}

	msg := CreateMessage(MsgRowDescription, payload)
	_, err := conn.Write(msg)
	return err
}

// SendDataRow sends a single DataRow message
func (r *ResultHandler) SendDataRow(conn net.Conn, columns []string, values map[string]interface{}) error {
	// Field count (2 bytes)
	fieldCount := uint16(len(columns))
	payload := make([]byte, 0, 2+len(columns)*20) // Estimate size

	fieldCountBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(fieldCountBytes, fieldCount)
	payload = append(payload, fieldCountBytes...)

	// For each column value:
	// - Length (4 bytes) - -1 for NULL, otherwise length of value
	// - Value (variable) - text representation

	for _, col := range columns {
		val, ok := values[col]
		if !ok || val == nil {
			// NULL value - length = -1
			payload = append(payload, 0xFF, 0xFF, 0xFF, 0xFF)
			continue
		}

		// Convert value to string
		strVal := fmt.Sprintf("%v", val)

		// Length
		lenBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBytes, uint32(len(strVal)))
		payload = append(payload, lenBytes...)

		// Value
		payload = append(payload, []byte(strVal)...)
	}

	msg := CreateMessage(MsgDataRow, payload)
	_, err := conn.Write(msg)
	return err
}

// inferTypeOID infers PostgreSQL type OID from Go type
func inferTypeOID(val interface{}) uint32 {
	switch v := val.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		// BIGINT = 20
		_ = v
		return 20
	case float32, float64:
		// FLOAT8 = 701
		return 701
	case bool:
		// BOOL = 16
		_ = v
		return 16
	default:
		// Default to TEXT = 25
		return 25
	}
}
