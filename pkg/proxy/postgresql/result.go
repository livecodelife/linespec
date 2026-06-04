package postgresql

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
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
	// - Type size (2 bytes) - type size or -1 for variable
	// - Type modifier (4 bytes) - -1
	// - Format code (2 bytes) - 0 for text (client may override via Bind)

	for _, col := range columns {
		// Field name
		payload = append(payload, []byte(col)...)
		payload = append(payload, 0) // null terminator

		// Table OID
		payload = append(payload, 0, 0, 0, 0)

		// Column number
		payload = append(payload, 0, 0)

		// Type OID — use name-based heuristics so asyncpg picks the right
		// native codec (int for id/_id, datetime for _at/time, str for rest).
		// asyncpg consults the OID to choose binary vs text format in its Bind
		// request, so matching the actual schema column types here lets asyncpg
		// return proper Python types (int, datetime) instead of plain strings.
		oid, typeSize := oidForColumn(col)
		typeOIDBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(typeOIDBuf, oid)
		payload = append(payload, typeOIDBuf...)

		// Type size
		typeSizeBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(typeSizeBuf, uint16(typeSize))
		payload = append(payload, typeSizeBuf...)

		// Type modifier - -1
		typeMod := make([]byte, 4)
		binary.BigEndian.PutUint32(typeMod, 0xFFFFFFFF) // -1 as uint32
		payload = append(payload, typeMod...)

		// Format code: 0 = text (client specifies actual format in Bind)
		payload = append(payload, 0, 0)
	}

	msg := CreateMessage(MsgRowDescription, payload)
	_, err := conn.Write(msg)
	return err
}

// oidForColumnWithValueHint is like oidForColumn but checks the actual value
// first to detect UUID-formatted strings that would otherwise be misidentified
// as INT4. This is needed because id columns are commonly UUID in practice, and
// typed clients (tokio-postgres, psycopg3) check the declared OID against the
// requested Go/Rust type before decoding — a mismatch causes a type error.
func oidForColumnWithValueHint(col string, val interface{}) (uint32, int) {
	if s, ok := val.(string); ok && isUUIDString(s) {
		return 2950, 16 // UUID
	}
	return oidForColumn(col)
}

// SendRowDescriptionWithHints sends a RowDescription, using the provided sample
// row to refine per-column OID inference. Falls back to SendRowDescription when
// sampleRow is nil.
func (r *ResultHandler) SendRowDescriptionWithHints(conn net.Conn, columns []string, sampleRow map[string]interface{}) error {
	if sampleRow == nil {
		return r.SendRowDescription(conn, columns)
	}

	fieldCount := uint16(len(columns))
	payload := make([]byte, 0, 2+len(columns)*20)

	fieldCountBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(fieldCountBytes, fieldCount)
	payload = append(payload, fieldCountBytes...)

	for _, col := range columns {
		payload = append(payload, []byte(col)...)
		payload = append(payload, 0)           // null terminator
		payload = append(payload, 0, 0, 0, 0) // table OID
		payload = append(payload, 0, 0)        // column number

		oid, typeSize := oidForColumnWithValueHint(col, sampleRow[col])
		typeOIDBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(typeOIDBuf, oid)
		payload = append(payload, typeOIDBuf...)

		typeSizeBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(typeSizeBuf, uint16(typeSize))
		payload = append(payload, typeSizeBuf...)

		payload = append(payload, 0xFF, 0xFF, 0xFF, 0xFF) // type modifier -1
		payload = append(payload, 0, 0)                   // format code 0 (text default)
	}

	msg := CreateMessage(MsgRowDescription, payload)
	_, err := conn.Write(msg)
	return err
}

// oidForColumn returns the PostgreSQL OID and type size for a column based on
// its name heuristics. This gives asyncpg the right codec so it returns native
// Python types (int for id/_id, datetime for _at/time) instead of plain strings.
// Type size: 4 for INT4, 8 for TIMESTAMPTZ, 0xFFFF (-1) for variable-length.
func oidForColumn(col string) (uint32, int) {
	lower := strings.ToLower(col)
	// UUID columns: returned as text by asyncpg when OID=2950, binary format is 16 bytes
	if strings.HasSuffix(lower, "_uuid") || lower == "uuid" {
		return 2950, 16 // UUID
	}
	// Boolean
	if lower == "is_read" || lower == "enabled" || lower == "active" || lower == "deleted" {
		return 16, 1 // BOOL
	}
	// Count/aggregate columns — INT8 matches real PostgreSQL COUNT(*) output (OID 20)
	if lower == "count" || lower == "total" || strings.HasPrefix(lower, "num_") || strings.HasSuffix(lower, "_count") {
		return 20, 8 // INT8
	}
	// Integer id columns — use INT4 so asyncpg returns Python int
	if lower == "id" || strings.HasSuffix(lower, "_id") {
		return 23, 4 // INT4
	}
	// Timestamp columns — use TIMESTAMPTZ so asyncpg returns Python datetime
	if strings.HasSuffix(lower, "_at") || strings.Contains(lower, "time") || strings.HasSuffix(lower, "_date") {
		return 1184, 8 // TIMESTAMPTZ
	}
	// Everything else: TEXT
	return 25, -1 // TEXT (variable length → -1)
}

// SendDataRow sends a single DataRow message using name-based heuristics to
// choose binary vs text format per column (legacy behaviour).
func (r *ResultHandler) SendDataRow(conn net.Conn, columns []string, values map[string]interface{}) error {
	return r.sendDataRowInternal(conn, columns, values, nil)
}

// SendDataRowWithFormats sends a DataRow honouring the per-column result
// format codes that the client supplied in its Bind message (0=text, 1=binary).
// A slice with a single entry applies that code to every column; an empty/nil
// slice falls back to name-based heuristics.
func (r *ResultHandler) SendDataRowWithFormats(conn net.Conn, columns []string, values map[string]interface{}, resultFormatCodes []int16) error {
	return r.sendDataRowInternal(conn, columns, values, resultFormatCodes)
}

// colFormatCode returns the result format code for column index i.
// Returns -1 to signal "use name-based heuristic", 0 for text, 1 for binary.
func colFormatCode(codes []int16, i int) int16 {
	if len(codes) == 0 {
		return -1
	}
	if len(codes) == 1 {
		return codes[0] // single code applies to all columns
	}
	if i < len(codes) {
		return codes[i]
	}
	return 0
}

// isUUIDString returns true when s is a standard 36-character UUID
// (8-4-4-4-12 hex groups separated by hyphens).
func isUUIDString(s string) bool {
	if len(s) != 36 {
		return false
	}
	if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// encodeUUIDBinary converts a UUID string to its 16-byte binary representation.
func encodeUUIDBinary(s string) ([]byte, error) {
	cleaned := strings.ReplaceAll(s, "-", "")
	if len(cleaned) != 32 {
		return nil, fmt.Errorf("invalid UUID string: %q", s)
	}
	return hex.DecodeString(cleaned)
}

func (r *ResultHandler) sendDataRowInternal(conn net.Conn, columns []string, values map[string]interface{}, resultFormatCodes []int16) error {
	// Field count (2 bytes)
	fieldCount := uint16(len(columns))
	payload := make([]byte, 0, 2+len(columns)*20) // Estimate size

	fieldCountBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(fieldCountBytes, fieldCount)
	payload = append(payload, fieldCountBytes...)

	// For each column value:
	// For each column value:
	// - Length (4 bytes) — -1 for NULL, otherwise byte-length of the encoded value
	// - Value (variable) — encoding depends on the per-column result format code
	//   supplied by the client in its Bind message (0=text, 1=binary).
	//   When resultFormatCodes is nil we fall back to name-based heuristics for
	//   backwards compatibility.

	for i, col := range columns {
		val, ok := values[col]
		if !ok || val == nil {
			// NULL value — length = -1
			payload = append(payload, 0xFF, 0xFF, 0xFF, 0xFF)
			continue
		}

		fmtCode := colFormatCode(resultFormatCodes, i)
		colLower := strings.ToLower(col)

		appendText := func(v interface{}) {
			var s string
			// Slices and maps must be JSON-encoded so the database driver
			// can scan them into string fields that hold JSONB/JSON values.
			switch v.(type) {
			case []interface{}, map[string]interface{}, map[interface{}]interface{}:
				if b, err := json.Marshal(v); err == nil {
					s = string(b)
				} else {
					s = fmt.Sprintf("%v", v)
				}
			default:
				s = fmt.Sprintf("%v", v)
			}
			lb := make([]byte, 4)
			binary.BigEndian.PutUint32(lb, uint32(len(s)))
			payload = append(payload, lb...)
			payload = append(payload, []byte(s)...)
		}

		if fmtCode == 1 {
			// Client explicitly requested binary format for this column.
			// Detect the value type and encode accordingly.
			encoded := false
			switch v := val.(type) {
			case string:
				if isUUIDString(v) {
					// UUID → 16 raw bytes
					uuidBytes, err := encodeUUIDBinary(v)
					if err == nil {
						payload = append(payload, 0, 0, 0, 16)
						payload = append(payload, uuidBytes...)
						encoded = true
					}
				}
				if !encoded && (strings.Contains(colLower, "_at") || strings.Contains(colLower, "time")) {
					tsBytes, err := encodeTimestampBinary(v)
					if err == nil {
						payload = append(payload, 0, 0, 0, 8)
						payload = append(payload, tsBytes...)
						encoded = true
					}
				}
				if !encoded {
					// Raw bytes — binary representation of text is just the UTF-8 bytes
					lb := make([]byte, 4)
					binary.BigEndian.PutUint32(lb, uint32(len(v)))
					payload = append(payload, lb...)
					payload = append(payload, []byte(v)...)
					encoded = true
				}
			case time.Time:
				tsBytes, err := encodeTimestampBinary(v)
				if err == nil {
					payload = append(payload, 0, 0, 0, 8)
					payload = append(payload, tsBytes...)
					encoded = true
				}
			default:
				// Encode integers using the size declared in RowDescription so the
				// byte count matches what the client expects (4 for INT4, 8 for INT8).
				_, typeSize := oidForColumn(col)
				if typeSize == 8 {
					intVal, err := toInt64(val)
					if err == nil {
						payload = append(payload, 0, 0, 0, 8)
						ib := make([]byte, 8)
						binary.BigEndian.PutUint64(ib, uint64(intVal))
						payload = append(payload, ib...)
						encoded = true
					}
				} else {
					intVal, err := toInt32(val)
					if err == nil {
						payload = append(payload, 0, 0, 0, 4)
						ib := make([]byte, 4)
						binary.BigEndian.PutUint32(ib, uint32(intVal))
						payload = append(payload, ib...)
						encoded = true
					}
				}
			}
			if !encoded {
				appendText(val)
			}
		} else if fmtCode == 0 {
			// Client explicitly requested text format.
			appendText(val)
		} else {
			// fmtCode == -1: no explicit format codes — use name-based heuristics
			// (legacy behaviour, used when we send our own RowDescription).
			isInteger := colLower == "id" || strings.HasSuffix(colLower, "_id")
			isTimestamp := strings.Contains(colLower, "_at") || strings.Contains(colLower, "time")

			if isInteger {
				intVal, err := toInt32(val)
				if err != nil {
					appendText(val)
				} else {
					payload = append(payload, 0, 0, 0, 4)
					ib := make([]byte, 4)
					binary.BigEndian.PutUint32(ib, uint32(intVal))
					payload = append(payload, ib...)
				}
			} else if isTimestamp {
				tsBytes, err := encodeTimestampBinary(val)
				if err != nil {
					appendText(val)
				} else {
					payload = append(payload, 0, 0, 0, 8)
					payload = append(payload, tsBytes...)
				}
			} else {
				appendText(val)
			}
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

// toInt64 converts a value to int64
func toInt64(val interface{}) (int64, error) {
	switch v := val.(type) {
	case int:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case uint:
		return int64(v), nil
	case uint8:
		return int64(v), nil
	case uint16:
		return int64(v), nil
	case uint32:
		return int64(v), nil
	case uint64:
		return int64(v), nil
	case float32:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case string:
		var result int64
		_, err := fmt.Sscanf(v, "%d", &result)
		if err != nil {
			return 0, fmt.Errorf("failed to parse integer from string: %v", v)
		}
		return result, nil
	default:
		return 0, fmt.Errorf("unsupported type for integer conversion: %T", val)
	}
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
