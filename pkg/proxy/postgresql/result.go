package postgresql

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ResultHandler generates PostgreSQL result set messages
type ResultHandler struct{}

func NewResultHandler() *ResultHandler {
	return &ResultHandler{}
}

// SendEmptyResultSet sends an empty result set. table and schemaCache are used
// to resolve real column OIDs from introspected schema; pass "" / nil to fall
// back to the name-based heuristic in oidForColumn.
func (r *ResultHandler) SendEmptyResultSet(conn net.Conn, table string, columns []string, schemaCache map[string][]ColumnInfo) error {
	// Send RowDescription
	if err := r.SendRowDescription(conn, table, columns, schemaCache); err != nil {
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

// SendRowDescription sends RowDescription message. table and schemaCache, when
// non-empty, are consulted first to resolve real column OIDs from introspected
// schema; columns not found there fall back to the oidForColumn heuristic.
func (r *ResultHandler) SendRowDescription(conn net.Conn, table string, columns []string, schemaCache map[string][]ColumnInfo) error {
	return r.SendRowDescriptionWithFormats(conn, table, columns, schemaCache, nil)
}

// SendRowDescriptionWithFormats is SendRowDescription declaring the Bind result
// formats (resolved by resultFormats) per column; nil declares 0 everywhere.
func (r *ResultHandler) SendRowDescriptionWithFormats(conn net.Conn, table string, columns []string, schemaCache map[string][]ColumnInfo, bindCodes []int16) error {
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

	oids := columnOIDs(table, columns, nil, schemaCache)
	fmts := resultFormats(bindCodes, len(columns))
	for ci, col := range columns {
		// Field name
		payload = append(payload, []byte(col)...)
		payload = append(payload, 0) // null terminator

		// Table OID
		payload = append(payload, 0, 0, 0, 0)

		// Column number
		payload = append(payload, 0, 0)

		// Type OID — prefer the real introspected schema type when available;
		// otherwise fall back to name-based heuristics so asyncpg picks the
		// right native codec (int for id/_id, datetime for _at/time, str for
		// rest). asyncpg consults the OID to choose binary vs text format in
		// its Bind request, so matching the actual schema column types here
		// lets asyncpg return proper Python types instead of plain strings.
		oid, typeSize := oids[ci].OID, oids[ci].Size
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

		// Format code: 1 for binary columns, 0 for text and legacy columns.
		payload = appendRowDescFormat(payload, fmts[ci])
	}

	msg := CreateMessage(MsgRowDescription, payload)
	_, err := conn.Write(msg)
	return err
}

// columnOID resolves a column's PostgreSQL type OID and wire size. It prefers
// the introspected schemaCache (real column type from information_schema),
// then a UUID-shaped sample value (id columns are commonly UUID in practice,
// and typed clients like tokio-postgres/psycopg3 reject a declared/actual type
// mismatch), and finally the column-name heuristic in oidForColumn. schemaCache
// and/or val may be nil/empty; table == "" always skips the schema lookup.
func columnOID(table, col string, schemaCache map[string][]ColumnInfo, val ...interface{}) (uint32, int) {
	if table != "" {
		for _, c := range schemaCache[table] {
			if strings.EqualFold(c.Field, col) {
				if oid, size, ok := pgOIDForInformationSchemaType(c.Type); ok {
					return oid, size
				}
				break
			}
		}
	}
	if len(val) > 0 {
		if s, ok := val[0].(string); ok && isUUIDString(s) {
			return 2950, 16 // UUID
		}
	}
	return oidForColumn(col)
}

// colOID is a column's PostgreSQL type OID and wire size.
type colOID struct {
	OID  uint32
	Size int
}

// columnOIDs is the single, pure per-statement OID computation shared by the
// RowDescription and DataRow encoders so they cannot disagree. Per column: the
// introspected schema type first (which never consults sampleRow), then a
// UUID-shaped sample value, then the oidForColumn name heuristic. sampleRow and
// schemaCache may be nil.
func columnOIDs(table string, columns []string, sampleRow map[string]interface{}, schemaCache map[string][]ColumnInfo) []colOID {
	out := make([]colOID, len(columns))
	for i, col := range columns {
		oid, size := columnOID(table, col, schemaCache, sampleRow[col])
		out[i] = colOID{OID: oid, Size: size}
	}
	return out
}

// SendRowDescriptionWithHints sends a RowDescription, using the provided sample
// row to refine per-column OID inference. Falls back to SendRowDescription when
// sampleRow is nil and no schema is cached for table.
func (r *ResultHandler) SendRowDescriptionWithHints(conn net.Conn, table string, columns []string, sampleRow map[string]interface{}, schemaCache map[string][]ColumnInfo) error {
	return r.SendRowDescriptionWithHintsAndFormats(conn, table, columns, sampleRow, schemaCache, nil)
}

// SendRowDescriptionWithHintsAndFormats is SendRowDescriptionWithHints declaring
// the Bind result formats per column; nil declares 0 everywhere.
func (r *ResultHandler) SendRowDescriptionWithHintsAndFormats(conn net.Conn, table string, columns []string, sampleRow map[string]interface{}, schemaCache map[string][]ColumnInfo, bindCodes []int16) error {
	if sampleRow == nil && len(schemaCache[table]) == 0 {
		return r.SendRowDescriptionWithFormats(conn, table, columns, schemaCache, bindCodes)
	}

	fieldCount := uint16(len(columns))
	payload := make([]byte, 0, 2+len(columns)*20)

	fieldCountBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(fieldCountBytes, fieldCount)
	payload = append(payload, fieldCountBytes...)

	oids := columnOIDs(table, columns, sampleRow, schemaCache)
	fmts := resultFormats(bindCodes, len(columns))
	for ci, col := range columns {
		payload = append(payload, []byte(col)...)
		payload = append(payload, 0)           // null terminator
		payload = append(payload, 0, 0, 0, 0) // table OID
		payload = append(payload, 0, 0)        // column number

		oid, typeSize := oids[ci].OID, oids[ci].Size
		typeOIDBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(typeOIDBuf, oid)
		payload = append(payload, typeOIDBuf...)

		typeSizeBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(typeSizeBuf, uint16(typeSize))
		payload = append(payload, typeSizeBuf...)

		payload = append(payload, 0xFF, 0xFF, 0xFF, 0xFF) // type modifier -1
		payload = appendRowDescFormat(payload, fmts[ci])
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

// pgInformationSchemaTypeOIDs maps information_schema.columns.data_type values
// (as returned by a live PostgreSQL introspection query) to their built-in type
// OID and wire size. Keys are lowercase to match Postgres's own lowercase
// data_type output. Parameterized types (e.g. "character varying(255)",
// "numeric(10,2)") are matched on their base name by pgOIDForInformationSchemaType.
var pgInformationSchemaTypeOIDs = map[string]struct {
	oid  uint32
	size int
}{
	"smallint":                    {21, 2},
	"integer":                     {23, 4},
	"bigint":                      {20, 8},
	"numeric":                     {1700, -1},
	"decimal":                     {1700, -1},
	"real":                        {700, 4},
	"double precision":            {701, 8},
	"boolean":                     {16, 1},
	"text":                        {25, -1},
	"character varying":           {1043, -1},
	"character":                   {1042, -1},
	"uuid":                        {2950, 16},
	"date":                        {1082, 4},
	"time without time zone":      {1083, 8},
	"time with time zone":         {1266, 12},
	"timestamp without time zone": {1114, 8},
	"timestamp with time zone":    {1184, 8},
	"json":                        {114, -1},
	"jsonb":                       {3802, -1},
	"bytea":                       {17, -1},
}

// pgOIDForInformationSchemaType maps a raw information_schema.columns.data_type
// string to its OID/size, stripping any "(...)" length/precision modifier
// (e.g. "character varying(255)" -> "character varying"). ok is false for
// types not in the table, so the caller can fall back to a heuristic.
func pgOIDForInformationSchemaType(dataType string) (oid uint32, size int, ok bool) {
	base := strings.ToLower(strings.TrimSpace(dataType))
	if idx := strings.IndexByte(base, '('); idx != -1 {
		base = strings.TrimSpace(base[:idx])
	}
	t, found := pgInformationSchemaTypeOIDs[base]
	if !found {
		return 0, 0, false
	}
	return t.oid, t.size, true
}

// SendDataRow sends a single DataRow message using name-based heuristics to
// choose binary vs text format per column (legacy behaviour).
func (r *ResultHandler) SendDataRow(conn net.Conn, columns []string, values map[string]interface{}) error {
	return r.SendDataRowWithOIDs(conn, columns, values, nil, nil)
}

// SendDataRowWithFormats sends a DataRow honouring the per-column result
// format codes that the client supplied in its Bind message (0=text, 1=binary).
// A slice with a single entry applies that code to every column; an empty/nil
// slice falls back to name-based heuristics.
func (r *ResultHandler) SendDataRowWithFormats(conn net.Conn, columns []string, values map[string]interface{}, resultFormatCodes []int16) error {
	return r.SendDataRowWithOIDs(conn, columns, values, nil, resultFormatCodes)
}

// SendDataRowWithOIDs sends a DataRow whose binary values are encoded for the
// per-column OIDs (from columnOIDs) that the RowDescription advertised.
func (r *ResultHandler) SendDataRowWithOIDs(conn net.Conn, columns []string, values map[string]interface{}, oids []colOID, resultFormatCodes []int16) error {
	_, err := conn.Write(encodeDataRow(columns, values, oids, resultFormatCodes))
	return err
}

// resultFormat is a column's resolved result format.
type resultFormat int

const (
	// resultFormatLegacy: no Bind codes (simple protocol). RowDescription says 0
	// and the DataRow uses the name heuristic.
	resultFormatLegacy resultFormat = iota
	resultFormatText
	resultFormatBinary
)

// resultFormats is the single place Bind result format codes are resolved, for
// the RowDescription and the DataRow alike. It always returns exactly n entries:
// nil/empty codes are legacy for every column, one code applies to all columns,
// N codes apply per column (a short list pads with text, surplus is ignored).
func resultFormats(bindCodes []int16, n int) []resultFormat {
	out := make([]resultFormat, n)
	if len(bindCodes) == 0 {
		return out // all resultFormatLegacy
	}
	for i := range out {
		code := bindCodes[0]
		if len(bindCodes) > 1 {
			code = 0
			if i < len(bindCodes) {
				code = bindCodes[i]
			}
		}
		if code == 1 {
			out[i] = resultFormatBinary
		} else {
			out[i] = resultFormatText
		}
	}
	return out
}

// appendRowDescFormat appends the 2-byte RowDescription format code.
func appendRowDescFormat(payload []byte, f resultFormat) []byte {
	if f == resultFormatBinary {
		return append(payload, 0, 1)
	}
	return append(payload, 0, 0)
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

// dataRowText renders a value in text format.
func dataRowText(v interface{}) []byte {
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
	return []byte(s)
}

func appendField(payload, field []byte) []byte {
	lb := make([]byte, 4)
	binary.BigEndian.PutUint32(lb, uint32(len(field)))
	payload = append(payload, lb...)
	return append(payload, field...)
}

// encodeDataRow returns a complete DataRow ('D') message. Each non-NULL value is
// encoded per its result format code (Bind semantics: nil/empty = legacy name
// heuristic, one code applies to all columns). In binary mode the value is
// encoded for oids[i] when that OID is one of the typed binary encoders; a value
// that cannot be encoded for its OID falls back to text. oids may be nil, in
// which case only the name heuristics are used.
func encodeDataRow(columns []string, values map[string]interface{}, oids []colOID, resultFormatCodes []int16) []byte {
	payload := make([]byte, 2, 2+len(columns)*20)
	binary.BigEndian.PutUint16(payload, uint16(len(columns)))

	fmts := resultFormats(resultFormatCodes, len(columns))
	for i, col := range columns {
		val, ok := values[col]
		if !ok || val == nil {
			payload = append(payload, 0xFF, 0xFF, 0xFF, 0xFF) // NULL
			continue
		}

		colLower := strings.ToLower(col)

		switch {
		case fmts[i] == resultFormatBinary:
			if i < len(oids) {
				if enc, handled := encodeBinaryForOID(oids[i].OID, val); handled {
					if enc != nil {
						payload = appendField(payload, enc)
					} else {
						payload = appendField(payload, dataRowText(val))
					}
					continue
				}
			}
			payload = appendLegacyBinary(payload, col, colLower, val)
		case fmts[i] == resultFormatText:
			payload = appendField(payload, dataRowText(val))
		default:
			// No explicit format codes: legacy name-based heuristics.
			isInteger := colLower == "id" || strings.HasSuffix(colLower, "_id")
			isTimestamp := strings.Contains(colLower, "_at") || strings.Contains(colLower, "time")
			if isInteger {
				if intVal, err := toInt32(val); err == nil {
					payload = appendField(payload, be32(uint32(intVal)))
					continue
				}
			} else if isTimestamp {
				if tsBytes, err := encodeTimestampBinary(val); err == nil {
					payload = appendField(payload, tsBytes)
					continue
				}
			}
			payload = appendField(payload, dataRowText(val))
		}
	}
	return CreateMessage(MsgDataRow, payload)
}

func be32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

// appendLegacyBinary is the name-heuristic binary encoder used when a column has
// no typed OID (unchanged behaviour).
func appendLegacyBinary(payload []byte, col, colLower string, val interface{}) []byte {
	switch v := val.(type) {
	case string:
		if isUUIDString(v) {
			if uuidBytes, err := encodeUUIDBinary(v); err == nil {
				return appendField(payload, uuidBytes)
			}
		}
		if strings.Contains(colLower, "_at") || strings.Contains(colLower, "time") {
			if tsBytes, err := encodeTimestampBinary(v); err == nil {
				return appendField(payload, tsBytes)
			}
		}
		// Binary representation of text is just the UTF-8 bytes.
		return appendField(payload, []byte(v))
	case time.Time:
		if tsBytes, err := encodeTimestampBinary(v); err == nil {
			return appendField(payload, tsBytes)
		}
	default:
		// Size the integer by the name heuristic (4 for INT4, 8 for INT8).
		_, typeSize := oidForColumn(col)
		if typeSize == 8 {
			if intVal, err := toInt64(val); err == nil {
				ib := make([]byte, 8)
				binary.BigEndian.PutUint64(ib, uint64(intVal))
				return appendField(payload, ib)
			}
		} else if intVal, err := toInt32(val); err == nil {
			return appendField(payload, be32(uint32(intVal)))
		}
	}
	return appendField(payload, dataRowText(val))
}

// encodeBinaryForOID encodes val in PostgreSQL binary send format for a typed
// OID. handled is false for OIDs without a typed encoder (caller uses the legacy
// path). When handled, a nil result means val cannot be encoded for the OID and
// the caller must fall back to text.
func encodeBinaryForOID(oid uint32, val interface{}) (enc []byte, handled bool) {
	switch oid {
	case 21:
		n, err := strictInt(val, 16)
		if err != nil {
			return nil, true
		}
		b := make([]byte, 2)
		binary.BigEndian.PutUint16(b, uint16(n))
		return b, true
	case 23:
		n, err := strictInt(val, 32)
		if err != nil {
			return nil, true
		}
		return be32(uint32(n)), true
	case 20:
		n, err := strictInt(val, 64)
		if err != nil {
			return nil, true
		}
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, uint64(n))
		return b, true
	case 16:
		switch v := val.(type) {
		case bool:
			if v {
				return []byte{1}, true
			}
			return []byte{0}, true
		case string:
			if bv, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
				if bv {
					return []byte{1}, true
				}
				return []byte{0}, true
			}
		}
		return nil, true
	case 700:
		f, err := strictFloat(val)
		if err != nil {
			return nil, true
		}
		return be32(math.Float32bits(float32(f))), true
	case 701:
		f, err := strictFloat(val)
		if err != nil {
			return nil, true
		}
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, math.Float64bits(f))
		return b, true
	case 1700:
		b, err := encodeNumericBinary(val)
		if err != nil {
			return nil, true
		}
		return b, true
	case 1082:
		t, err := parseDateValue(val)
		if err != nil {
			return nil, true
		}
		days := int32(math.Floor(t.Sub(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)).Hours() / 24))
		return be32(uint32(days)), true
	case 1114, 1184:
		b, err := encodeTimestampBinary(val)
		if err != nil {
			return nil, true
		}
		return b, true
	case 2950:
		if s, ok := val.(string); ok && isUUIDString(s) {
			if b, err := encodeUUIDBinary(s); err == nil {
				return b, true
			}
		}
		return nil, true
	}
	return nil, false
}

// strictInt converts val to an integer that fits in bits (16/32/64). Unlike
// toInt64 it rejects strings with trailing garbage and non-integral floats.
func strictInt(val interface{}, bits int) (int64, error) {
	var n int64
	switch v := val.(type) {
	case int:
		n = int64(v)
	case int8:
		n = int64(v)
	case int16:
		n = int64(v)
	case int32:
		n = int64(v)
	case int64:
		n = v
	case uint:
		if uint64(v) > math.MaxInt64 {
			return 0, fmt.Errorf("integer out of range: %v", v)
		}
		n = int64(v)
	case uint8:
		n = int64(v)
	case uint16:
		n = int64(v)
	case uint32:
		n = int64(v)
	case uint64:
		if v > math.MaxInt64 {
			return 0, fmt.Errorf("integer out of range: %v", v)
		}
		n = int64(v)
	case float32:
		if float64(v) != math.Trunc(float64(v)) {
			return 0, fmt.Errorf("non-integral float: %v", v)
		}
		n = int64(v)
	case float64:
		if v != math.Trunc(v) || math.IsInf(v, 0) || math.Abs(v) >= 1<<63 {
			return 0, fmt.Errorf("non-integral float: %v", v)
		}
		n = int64(v)
	case string:
		var err error
		if n, err = strconv.ParseInt(strings.TrimSpace(v), 10, 64); err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("unsupported type for integer conversion: %T", val)
	}
	if bits < 64 {
		lim := int64(1) << (bits - 1)
		if n < -lim || n >= lim {
			return 0, fmt.Errorf("integer out of range for int%d: %d", bits/8, n)
		}
	}
	return n, nil
}

func strictFloat(val interface{}) (float64, error) {
	switch v := val.(type) {
	case float32:
		return float64(v), nil
	case float64:
		return v, nil
	case string:
		return strconv.ParseFloat(strings.TrimSpace(v), 64)
	}
	n, err := strictInt(val, 64)
	return float64(n), err
}

// parseDateValue parses a date (or timestamp, truncated to its day) value.
func parseDateValue(val interface{}) (time.Time, error) {
	switch v := val.(type) {
	case time.Time:
		y, m, d := v.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC), nil
	case string:
		s := strings.TrimSpace(v)
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t, nil
		}
		if len(s) > 10 {
			if _, err := encodeTimestampBinary(s); err == nil {
				if t, err := time.Parse("2006-01-02", s[:10]); err == nil {
					return t, nil
				}
			}
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse date: %v", val)
}

var numericLiteral = regexp.MustCompile(`^[+-]?(\d+(\.\d*)?|\.\d+)$`)

// encodeNumericBinary encodes a decimal value in PostgreSQL numeric_send layout:
// int16 ndigits, int16 weight, uint16 sign, int16 dscale, then base-10000 digits.
func encodeNumericBinary(val interface{}) ([]byte, error) {
	var s string
	switch v := val.(type) {
	case string:
		s = strings.TrimSpace(v)
	case float32:
		s = strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		s = strconv.FormatFloat(v, 'f', -1, 64)
	default:
		n, err := strictInt(val, 64)
		if err != nil {
			return nil, err
		}
		s = strconv.FormatInt(n, 10)
	}
	if !numericLiteral.MatchString(s) {
		return nil, fmt.Errorf("invalid numeric: %q", s)
	}
	neg := false
	if s[0] == '-' || s[0] == '+' {
		neg = s[0] == '-'
		s = s[1:]
	}
	intPart, frac := s, ""
	if idx := strings.IndexByte(s, '.'); idx >= 0 {
		intPart, frac = s[:idx], s[idx+1:]
	}
	intPart = strings.TrimLeft(intPart, "0")
	dscale := len(frac)
	if pad := (4 - len(intPart)%4) % 4; pad > 0 {
		intPart = strings.Repeat("0", pad) + intPart
	}
	if pad := (4 - len(frac)%4) % 4; pad > 0 {
		frac += strings.Repeat("0", pad)
	}
	var digits []int16
	for i := 0; i < len(intPart); i += 4 {
		n, _ := strconv.Atoi(intPart[i : i+4])
		digits = append(digits, int16(n))
	}
	weight := len(digits) - 1
	for i := 0; i < len(frac); i += 4 {
		n, _ := strconv.Atoi(frac[i : i+4])
		digits = append(digits, int16(n))
	}
	for len(digits) > 0 && digits[0] == 0 {
		digits = digits[1:]
		weight--
	}
	for len(digits) > 0 && digits[len(digits)-1] == 0 {
		digits = digits[:len(digits)-1]
	}
	var sign uint16
	if len(digits) == 0 {
		weight = 0
	} else if neg {
		sign = 0x4000
	}
	out := make([]byte, 8, 8+2*len(digits))
	binary.BigEndian.PutUint16(out[0:], uint16(len(digits)))
	binary.BigEndian.PutUint16(out[2:], uint16(int16(weight)))
	binary.BigEndian.PutUint16(out[4:], sign)
	binary.BigEndian.PutUint16(out[6:], uint16(dscale))
	for _, d := range digits {
		out = binary.BigEndian.AppendUint16(out, uint16(d))
	}
	return out, nil
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
