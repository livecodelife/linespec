package mysql

import (
	"testing"
)

// buildQueryAttributesFrame constructs the framing bytes that precede the SQL
// query in a COM_QUERY payload when CLIENT_QUERY_ATTRIBUTES is negotiated.
// attrs is a slice of (name, value) string pairs.
func buildQueryAttributesFrame(attrs [][2]string) []byte {
	var b []byte

	// parameter_count (length-encoded int)
	b = appendLenencInt(b, uint64(len(attrs)))
	// parameter_set_count (always 1)
	b = appendLenencInt(b, 1)

	if len(attrs) == 0 {
		return b
	}

	// NUL-bitmap: ceil(len/8) bytes, all zero (no NULLs)
	bitmapLen := (len(attrs) + 7) / 8
	b = append(b, make([]byte, bitmapLen)...)

	// new_params_bind_flag = 1
	b = append(b, 0x01)

	// param types and names
	for _, attr := range attrs {
		// type: MYSQL_TYPE_VAR_STRING = 0x00fd (little-endian)
		b = append(b, 0xfd, 0x00)
		b = appendLenencString(b, attr[0])
	}

	// param values
	for _, attr := range attrs {
		b = appendLenencString(b, attr[1])
	}

	return b
}

func appendLenencInt(b []byte, n uint64) []byte {
	switch {
	case n < 251:
		return append(b, byte(n))
	case n < 1<<16:
		return append(b, 0xfc, byte(n), byte(n>>8))
	case n < 1<<24:
		return append(b, 0xfd, byte(n), byte(n>>8), byte(n>>16))
	default:
		return append(b, 0xfe,
			byte(n), byte(n>>8), byte(n>>16), byte(n>>24),
			byte(n>>32), byte(n>>40), byte(n>>48), byte(n>>56))
	}
}

func appendLenencString(b []byte, s string) []byte {
	b = appendLenencInt(b, uint64(len(s)))
	return append(b, s...)
}

// buildPayload returns framing + sql as a single byte slice.
func buildPayload(attrs [][2]string, sql string) []byte {
	frame := buildQueryAttributesFrame(attrs)
	return append(frame, []byte(sql)...)
}

func TestStripQueryAttributes_ZeroAttributes(t *testing.T) {
	payload := buildPayload(nil, "SELECT 1")
	got, err := stripQueryAttributes(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "SELECT 1" {
		t.Errorf("got %q, want %q", string(got), "SELECT 1")
	}
}

func TestStripQueryAttributes_OneAttribute(t *testing.T) {
	attrs := [][2]string{{"trace_id", "abc123"}}
	payload := buildPayload(attrs, "SELECT * FROM users")
	got, err := stripQueryAttributes(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "SELECT * FROM users" {
		t.Errorf("got %q, want %q", string(got), "SELECT * FROM users")
	}
}

func TestStripQueryAttributes_MultipleAttributes(t *testing.T) {
	attrs := [][2]string{
		{"trace_id", "abc123"},
		{"request_id", "req-456"},
		{"user_id", "789"},
	}
	payload := buildPayload(attrs, "INSERT INTO todos (title) VALUES ('test')")
	got, err := stripQueryAttributes(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "INSERT INTO todos (title) VALUES ('test')"
	if string(got) != want {
		t.Errorf("got %q, want %q", string(got), want)
	}
}

func TestStripQueryAttributes_MultiByteParamCount(t *testing.T) {
	// Build a frame with parameter_count = 252 (requires 3-byte lenenc: 0xfc, 0xfc, 0x00)
	// We won't actually have 252 attribute values — just validate the framing is parsed
	// correctly by constructing the bytes manually.
	var b []byte
	// parameter_count = 252
	b = append(b, 0xfc, 0xfc, 0x00)
	// parameter_set_count = 1
	b = append(b, 0x01)
	// NUL-bitmap: ceil(252/8) = 32 bytes, all 0xff (all NULL) so no values to skip
	bitmap := make([]byte, 32)
	for i := range bitmap {
		bitmap[i] = 0xff
	}
	b = append(b, bitmap...)
	// new_params_bind_flag = 0 (no type/name defs)
	b = append(b, 0x00)
	// No param values (all NULL)
	// SQL query
	b = append(b, []byte("SELECT 1")...)

	got, err := stripQueryAttributes(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "SELECT 1" {
		t.Errorf("got %q, want %q", string(got), "SELECT 1")
	}
}

func TestStripQueryAttributes_TruncatedPayload_ParameterCount(t *testing.T) {
	// Empty payload — can't read parameter_count
	_, err := stripQueryAttributes([]byte{})
	if err == nil {
		t.Fatal("expected error for empty payload, got nil")
	}
}

func TestStripQueryAttributes_TruncatedPayload_Bitmap(t *testing.T) {
	var b []byte
	b = appendLenencInt(b, 8) // parameter_count = 8 (needs 1 byte bitmap)
	b = appendLenencInt(b, 1) // parameter_set_count
	// Intentionally omit the bitmap
	_, err := stripQueryAttributes(b)
	if err == nil {
		t.Fatal("expected error for truncated bitmap, got nil")
	}
}

func TestStripQueryAttributes_NullParameterValues(t *testing.T) {
	// Manually construct a frame with 2 params where the first is NULL
	var b []byte
	b = appendLenencInt(b, 2) // parameter_count = 2
	b = appendLenencInt(b, 1) // parameter_set_count
	// NUL-bitmap: 1 byte; bit 0 set = param 0 is NULL
	b = append(b, 0x01)
	// new_params_bind_flag = 1
	b = append(b, 0x01)
	// param 0: type + name
	b = append(b, 0xfd, 0x00) // MYSQL_TYPE_VAR_STRING
	b = appendLenencString(b, "p0")
	// param 1: type + name
	b = append(b, 0xfd, 0x00)
	b = appendLenencString(b, "p1")
	// param values: param 0 is NULL (no bytes), param 1 has a value
	b = appendLenencString(b, "hello")
	// SQL
	b = append(b, []byte("DELETE FROM todos WHERE id=1")...)

	got, err := stripQueryAttributes(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "DELETE FROM todos WHERE id=1"
	if string(got) != want {
		t.Errorf("got %q, want %q", string(got), want)
	}
}

func TestStripQueryAttributes_FramingOnlyNoSQL(t *testing.T) {
	// Zero attributes, no SQL following — should return empty slice
	payload := buildPayload(nil, "")
	got, err := stripQueryAttributes(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %q", string(got))
	}
}
