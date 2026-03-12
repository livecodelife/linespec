package postgresql

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"time"
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
	// - Type OID (4 bytes) - proper OIDs for each type
	// - Type size (2 bytes) - -1 for variable
	// - Type modifier (4 bytes) - -1
	// - Format code (2 bytes) - 1 for binary (INTEGER, TIMESTAMPTZ), 0 for text (others)

	for _, col := range columns {
		colLower := strings.ToLower(col)
		isInteger := colLower == "id" || strings.HasSuffix(colLower, "_id")
		isTimestamp := strings.Contains(colLower, "_at") || strings.Contains(colLower, "time")

		// Field name
		payload = append(payload, []byte(col)...)
		payload = append(payload, 0) // null terminator

		// Table OID
		payload = append(payload, 0, 0, 0, 0)

		// Column number
		payload = append(payload, 0, 0)

		// Type OID - use proper types
		typeOID := make([]byte, 4)
		oid := uint32(25) // Default TEXT
		if isTimestamp {
			oid = 1184 // TIMESTAMPTZ
		} else if isInteger {
			oid = 23 // INTEGER
		}
		binary.BigEndian.PutUint32(typeOID, oid)
		payload = append(payload, typeOID...)

		// Type size - -1 for variable
		typeSize := make([]byte, 2)
		binary.BigEndian.PutUint16(typeSize, 0xFFFF) // -1 as uint16
		payload = append(payload, typeSize...)

		// Type modifier - -1
		typeMod := make([]byte, 4)
		binary.BigEndian.PutUint32(typeMod, 0xFFFFFFFF) // -1 as uint32
		payload = append(payload, typeMod...)

		// Format code: 0 = text, 1 = binary
		// Use binary format for INTEGER and TIMESTAMPTZ (need proper binary encoding)
		// Use text format for other types
		if isInteger || isTimestamp {
			payload = append(payload, 0, 1) // Binary format
		} else {
			payload = append(payload, 0, 0) // Text format
		}
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
	// - Value (variable) - binary for INTEGER/TIMESTAMPTZ, text for other types

	for _, col := range columns {
		val, ok := values[col]
		if !ok || val == nil {
			// NULL value - length = -1
			payload = append(payload, 0xFF, 0xFF, 0xFF, 0xFF)
			continue
		}

		colLower := strings.ToLower(col)
		isInteger := colLower == "id" || strings.HasSuffix(colLower, "_id")
		isTimestamp := strings.Contains(colLower, "_at") || strings.Contains(colLower, "time")

		if isInteger {
			// Send INTEGER in binary format (4 bytes, big-endian)
			// RowDescription declares format=1 for INTEGER, so asyncpg expects binary
			intVal, err := toInt32(val)
			if err != nil {
				// Fallback to text format if conversion fails
				strVal := fmt.Sprintf("%v", val)
				lenBytes := make([]byte, 4)
				binary.BigEndian.PutUint32(lenBytes, uint32(len(strVal)))
				payload = append(payload, lenBytes...)
				payload = append(payload, []byte(strVal)...)
			} else {
				// Binary format: length = 4, value = 4 bytes big-endian
				payload = append(payload, 0, 0, 0, 4) // length = 4
				intBytes := make([]byte, 4)
				binary.BigEndian.PutUint32(intBytes, uint32(intVal))
				payload = append(payload, intBytes...)
			}
		} else if isTimestamp {
			// Send TIMESTAMPTZ in binary format (8 bytes, microseconds since 2000-01-01)
			timestampBytes, err := encodeTimestampBinary(val)
			if err != nil {
				// Fallback to text format if encoding fails
				strVal := fmt.Sprintf("%v", val)
				lenBytes := make([]byte, 4)
				binary.BigEndian.PutUint32(lenBytes, uint32(len(strVal)))
				payload = append(payload, lenBytes...)
				payload = append(payload, []byte(strVal)...)
			} else {
				// Binary format: length = 8, value = 8 bytes big-endian
				payload = append(payload, 0, 0, 0, 8) // length = 8
				payload = append(payload, timestampBytes...)
			}
		} else {
			// Text format for other types (TEXT, VARCHAR, etc.)
			strVal := fmt.Sprintf("%v", val)
			lenBytes := make([]byte, 4)
			binary.BigEndian.PutUint32(lenBytes, uint32(len(strVal)))
			payload = append(payload, lenBytes...)
			payload = append(payload, []byte(strVal)...)
		}
	}

	msg := CreateMessage(MsgDataRow, payload)
	_, err := conn.Write(msg)
	return err
}

// encodeTimestampBinary converts a timestamp value to PostgreSQL binary format
// PostgreSQL timestamps are int64 values representing microseconds since 2000-01-01 00:00:00 UTC
func encodeTimestampBinary(val interface{}) ([]byte, error) {
	var t time.Time

	switch v := val.(type) {
	case time.Time:
		t = v
	case string:
		// Try parsing various ISO timestamp formats
		formats := []string{
			time.RFC3339,           // "2006-01-02T15:04:05Z07:00"
			"2006-01-02T15:04:05Z", // ISO format with Z
			"2006-01-02 15:04:05",  // PostgreSQL format without timezone
			"2006-01-02 15:04:05-07",
			"2006-01-02 15:04:05+00",
		}

		var err error
		for _, format := range formats {
			t, err = time.Parse(format, v)
			if err == nil {
				break
			}
		}
		if err != nil {
			return nil, fmt.Errorf("failed to parse timestamp: %v", v)
		}
	default:
		return nil, fmt.Errorf("unsupported timestamp type: %T", val)
	}

	// PostgreSQL epoch is 2000-01-01 00:00:00 UTC
	postgresEpoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

	// Calculate microseconds since PostgreSQL epoch
	diff := t.UTC().Sub(postgresEpoch)
	microseconds := diff.Microseconds()

	// Encode as int64 (8 bytes, big-endian)
	result := make([]byte, 8)
	binary.BigEndian.PutUint64(result, uint64(microseconds))

	return result, nil
}

// toInt32 converts a value to int32
func toInt32(val interface{}) (int32, error) {
	switch v := val.(type) {
	case int:
		return int32(v), nil
	case int8:
		return int32(v), nil
	case int16:
		return int32(v), nil
	case int32:
		return v, nil
	case int64:
		return int32(v), nil
	case uint:
		return int32(v), nil
	case uint8:
		return int32(v), nil
	case uint16:
		return int32(v), nil
	case uint32:
		return int32(v), nil
	case uint64:
		return int32(v), nil
	case float32:
		return int32(v), nil
	case float64:
		return int32(v), nil
	case string:
		// Try to parse as integer
		var result int64
		_, err := fmt.Sscanf(v, "%d", &result)
		if err != nil {
			return 0, fmt.Errorf("failed to parse integer from string: %v", v)
		}
		return int32(result), nil
	default:
		return 0, fmt.Errorf("unsupported type for integer conversion: %T", val)
	}
}
