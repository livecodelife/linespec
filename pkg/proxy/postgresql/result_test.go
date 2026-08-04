package postgresql

import (
	"encoding/binary"
	"net"
	"testing"
)

// readRowDescription reads a single RowDescription ('T') message off conn and
// decodes it into column-name -> type OID, matching the wire layout written by
// SendRowDescription/SendRowDescriptionWithHints.
func readRowDescription(t *testing.T, conn net.Conn) map[string]uint32 {
	t.Helper()

	header := make([]byte, 5)
	if _, err := readFull(conn, header); err != nil {
		t.Fatalf("failed to read message header: %v", err)
	}
	if header[0] != MsgRowDescription {
		t.Fatalf("expected RowDescription ('T'), got %q", header[0])
	}
	length := binary.BigEndian.Uint32(header[1:5])
	payload := make([]byte, length-4)
	if _, err := readFull(conn, payload); err != nil {
		t.Fatalf("failed to read message payload: %v", err)
	}

	fieldCount := binary.BigEndian.Uint16(payload[0:2])
	result := make(map[string]uint32, fieldCount)
	pos := 2
	for i := 0; i < int(fieldCount); i++ {
		start := pos
		for payload[pos] != 0 {
			pos++
		}
		name := string(payload[start:pos])
		pos++ // null terminator
		pos += 4 // table OID
		pos += 2 // column number
		oid := binary.BigEndian.Uint32(payload[pos : pos+4])
		pos += 4
		pos += 2 // type size
		pos += 4 // type modifier
		pos += 2 // format code
		result[name] = oid
	}
	return result
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// TestSendRowDescription_SchemaCacheOverridesHeuristic reproduces the "mocked
// result sets sent with OID 0 [or an unreliable name-based heuristic fallback]"
// bug: without introspected schema, oidForColumn's name heuristic has no rule
// for a column like "amount" and falls back to OID 25 (TEXT) even though the
// real column is numeric (OID 1700) — a typed client can't decode it correctly.
// With schemaCache populated (as LoadSchema now does from --schema-file), the
// real introspected type must win.
func TestSendRowDescription_SchemaCacheOverridesHeuristic(t *testing.T) {
	schemaCache := map[string][]ColumnInfo{
		"orders": {
			{Field: "id", Type: "integer"},
			{Field: "amount", Type: "numeric"},
		},
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	handler := NewResultHandler()
	done := make(chan error, 1)
	go func() {
		done <- handler.SendRowDescription(serverConn, "orders", []string{"id", "amount"}, schemaCache)
	}()

	oids := readRowDescription(t, clientConn)
	if err := <-done; err != nil {
		t.Fatalf("SendRowDescription returned error: %v", err)
	}

	if oids["id"] != 23 {
		t.Errorf("expected id OID 23 (int4), got %d", oids["id"])
	}
	if oids["amount"] != 1700 {
		t.Errorf("expected amount OID 1700 (numeric) from schemaCache, got %d (heuristic-only would wrongly return 25/TEXT)", oids["amount"])
	}
	for col, oid := range oids {
		if oid == 0 {
			t.Errorf("column %q sent with OID 0 (unspecified) — client cannot decode this", col)
		}
	}
}

// TestSendRowDescription_NoSchemaFallsBackToHeuristic guards the existing
// behaviour (no regression) when no schema is cached for the table: the
// name-based heuristic must still run, and must never emit OID 0.
func TestSendRowDescription_NoSchemaFallsBackToHeuristic(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	handler := NewResultHandler()
	done := make(chan error, 1)
	go func() {
		done <- handler.SendRowDescription(serverConn, "unknown_table", []string{"id", "name"}, nil)
	}()

	oids := readRowDescription(t, clientConn)
	if err := <-done; err != nil {
		t.Fatalf("SendRowDescription returned error: %v", err)
	}

	if oids["id"] != 23 {
		t.Errorf("expected heuristic id OID 23 (int4), got %d", oids["id"])
	}
	if oids["name"] == 0 {
		t.Error("column \"name\" sent with OID 0 (unspecified)")
	}
}

// TestColumnOID_SchemaTakesPriorityOverValueHint verifies the resolution
// priority: introspected schema > UUID value hint > name heuristic.
func TestColumnOID_SchemaTakesPriorityOverValueHint(t *testing.T) {
	schemaCache := map[string][]ColumnInfo{
		"widgets": {{Field: "id", Type: "integer"}},
	}
	// Without schema, a UUID-shaped string value overrides the "id" heuristic.
	oid, _ := columnOID("", "id", nil, "550e8400-e29b-41d4-a716-446655440000")
	if oid != 2950 {
		t.Errorf("expected UUID OID 2950 when no schema present, got %d", oid)
	}
	// With schema declaring id as integer, schema wins even over a UUID-shaped value.
	oid, _ = columnOID("widgets", "id", schemaCache, "550e8400-e29b-41d4-a716-446655440000")
	if oid != 23 {
		t.Errorf("expected schema-declared OID 23 (int4) to take priority, got %d", oid)
	}
}
