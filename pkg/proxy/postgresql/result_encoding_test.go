package postgresql

// Spec for prov-2026-7e08a3cb: mock DataRow values are encoded with the same
// per-column OID that the RowDescription advertises.
//
// Seams assumed (production code does not exist yet; this file fails to
// compile until it does):
//
//	type colOID struct { OID uint32; Size int }
//
//	// columnOIDs: pure; schema type first, then UUID-shaped sample value, then
//	// the oidForColumn name heuristic. A schema-typed column never consults
//	// sampleRow. No proxy/conn/registry state.
//	func columnOIDs(table string, columns []string, sampleRow map[string]interface{},
//	    schemaCache map[string][]ColumnInfo) []colOID
//
//	// encodeDataRow: pure; returns the complete 'D' message bytes. oids may be
//	// nil (name heuristics only). formatCodes follows Bind semantics (nil/empty
//	// = legacy heuristic, one code = all columns, else per column).
//	func encodeDataRow(columns []string, values map[string]interface{},
//	    oids []colOID, formatCodes []int16) []byte
//
// The existing SendRowDescriptionWithHints is used unchanged to observe the
// RowDescription OIDs.

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

var (
	rexBinary = []int16{1}
	rexText   = []int16{0}
)

// rexFields splits a complete DataRow message into per-column raw values
// (nil for SQL NULL).
func rexFields(t *testing.T, msg []byte) [][]byte {
	t.Helper()
	if len(msg) < 7 || msg[0] != MsgDataRow {
		t.Fatalf("not a DataRow message: % x", msg)
	}
	if int(binary.BigEndian.Uint32(msg[1:5])) != len(msg)-1 {
		t.Fatalf("DataRow length %d does not match message size %d", binary.BigEndian.Uint32(msg[1:5]), len(msg)-1)
	}
	n := int(binary.BigEndian.Uint16(msg[5:7]))
	pos := 7
	out := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		l := int32(binary.BigEndian.Uint32(msg[pos : pos+4]))
		pos += 4
		if l < 0 {
			out = append(out, nil)
			continue
		}
		out = append(out, msg[pos:pos+int(l)])
		pos += int(l)
	}
	if pos != len(msg) {
		t.Fatalf("trailing bytes in DataRow: % x", msg[pos:])
	}
	return out
}

func rexSchema(table, col, typ string) map[string][]ColumnInfo {
	return map[string][]ColumnInfo{table: {{Field: col, Type: typ}}}
}

// rexEncode computes OIDs from the schema (as the proxy would) and encodes one
// single-column row.
func rexEncode(t *testing.T, col, schemaType string, val interface{}, codes []int16) []byte {
	t.Helper()
	cache := rexSchema("tbl", col, schemaType)
	cols := []string{col}
	row := map[string]interface{}{col: val}
	oids := columnOIDs("tbl", cols, row, cache)
	f := rexFields(t, encodeDataRow(cols, row, oids, codes))
	if len(f) != 1 || f[0] == nil {
		t.Fatalf("expected one non-null field, got %v", f)
	}
	return f[0]
}

func rexBE(size int, v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b[8-size:]
}

func rexRowDescOIDs(t *testing.T, table string, cols []string, sample map[string]interface{}, cache map[string][]ColumnInfo) map[string]uint32 {
	t.Helper()
	s, c := net.Pipe()
	defer s.Close()
	defer c.Close()
	go func() { _ = NewResultHandler().SendRowDescriptionWithHints(s, table, cols, sample, cache) }()
	return readRowDescription(t, c)
}

// The exact reported case: Npgsql, bigint applicationid = 11, binary results.
func TestResultEncoding_ApplicationIDBigintBinary(t *testing.T) {
	cache := rexSchema("applications", "applicationid", "bigint")
	cols := []string{"applicationid"}
	row := map[string]interface{}{"applicationid": 11}

	oids := rexRowDescOIDs(t, "applications", cols, row, cache)
	if oids["applicationid"] != 20 {
		t.Fatalf("RowDescription OID = %d, want 20 (int8)", oids["applicationid"])
	}
	co := columnOIDs("applications", cols, row, cache)
	if len(co) != 1 || co[0].OID != 20 || co[0].Size != 8 {
		t.Fatalf("columnOIDs = %+v, want [{20 8}]", co)
	}

	msg := encodeDataRow(cols, row, co, rexBinary)
	want := []byte{0, 0, 0, 8, 0, 0, 0, 0, 0, 0, 0, 0x0b}
	f := rexFields(t, msg)
	got := append(rexBE(4, uint64(len(f[0]))), f[0]...)
	if !bytes.Equal(got, want) {
		t.Fatalf("binary applicationid field = % x, want % x", got, want)
	}
}

func TestResultEncoding_ApplicationIDBigintText(t *testing.T) {
	got := rexEncode(t, "applicationid", "bigint", 11, rexText)
	if string(got) != "11" {
		t.Fatalf("text applicationid = %q, want \"11\"", got)
	}
}

func TestResultEncoding_RowDescriptionMatchesColumnOIDs(t *testing.T) {
	cache := map[string][]ColumnInfo{"t": {
		{Field: "applicationid", Type: "bigint"},
		{Field: "amount", Type: "numeric(10,2)"},
		{Field: "flag", Type: "boolean"},
	}}
	cols := []string{"applicationid", "amount", "flag", "id", "name"}
	sample := map[string]interface{}{"name": "x"}
	rd := rexRowDescOIDs(t, "t", cols, sample, cache)
	for i, o := range columnOIDs("t", cols, sample, cache) {
		if rd[cols[i]] != o.OID {
			t.Errorf("column %s: RowDescription OID %d != columnOIDs OID %d", cols[i], rd[cols[i]], o.OID)
		}
	}
}

func TestResultEncoding_PerTypeBinary(t *testing.T) {
	uuid := "123e4567-e89b-12d3-a456-426614174000"
	uuidBytes := []byte{0x12, 0x3e, 0x45, 0x67, 0xe8, 0x9b, 0x12, 0xd3, 0xa4, 0x56, 0x42, 0x66, 0x14, 0x17, 0x40, 0x00}
	const tsMicros = 8840*86400*1_000_000 + 12*3600*1_000_000 // 2024-03-15 12:00:00 since 2000-01-01
	cases := []struct {
		name, typ string
		val       interface{}
		want      []byte
	}{
		{"int2", "smallint", 11, rexBE(2, 11)},
		{"int4", "integer", 11, rexBE(4, 11)},
		{"int8", "bigint", 11, rexBE(8, 11)},
		{"int8_negative", "bigint", -2, rexBE(8, 0xFFFFFFFFFFFFFFFE)},
		{"bool_true", "boolean", true, []byte{1}},
		{"bool_false", "boolean", false, []byte{0}},
		{"float4", "real", 1.5, rexBE(4, 0x3fc00000)},
		{"float8", "double precision", 1.5, rexBE(8, 0x3ff8000000000000)},
		{"date", "date", "2024-03-15", rexBE(4, 8840)},
		{"date_before_epoch", "date", "1999-12-31", rexBE(4, 0xFFFFFFFF)},
		{"timestamp", "timestamp without time zone", "2024-03-15 12:00:00", rexBE(8, tsMicros)},
		{"timestamptz", "timestamp with time zone", "2024-03-15T12:00:00Z", rexBE(8, tsMicros)},
		{"uuid", "uuid", uuid, uuidBytes},
		// numeric_send: int16 ndigits, int16 weight, uint16 sign, int16 dscale, int16 base-10000 digits
		{"numeric_12345.67", "numeric", "12345.67", []byte{0, 3, 0, 1, 0, 0, 0, 2, 0, 1, 0x09, 0x29, 0x1a, 0x2c}},
		{"numeric_-0.5", "numeric", "-0.5", []byte{0, 1, 0xff, 0xff, 0x40, 0, 0, 1, 0x13, 0x88}},
		{"numeric_0", "numeric", "0", []byte{0, 0, 0, 0, 0, 0, 0, 0}},
		{"numeric_100", "numeric", "100", []byte{0, 1, 0, 0, 0, 0, 0, 0, 0, 100}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rexEncode(t, "col", c.typ, c.val, rexBinary)
			if !bytes.Equal(got, c.want) {
				t.Fatalf("binary = % x, want % x", got, c.want)
			}
		})
	}
}

// Text format output is unchanged: fmt %v of the value.
func TestResultEncoding_PerTypeText(t *testing.T) {
	cases := []struct {
		name, typ string
		val       interface{}
		want      string
	}{
		{"int2", "smallint", 11, "11"},
		{"int4", "integer", 11, "11"},
		{"int8", "bigint", 11, "11"},
		{"bool", "boolean", true, "true"},
		{"float4", "real", 1.5, "1.5"},
		{"float8", "double precision", 1.5, "1.5"},
		{"numeric", "numeric", "12345.67", "12345.67"},
		{"numeric_neg", "numeric", "-0.5", "-0.5"},
		{"date", "date", "2024-03-15", "2024-03-15"},
		{"timestamp", "timestamp without time zone", "2024-03-15 12:00:00", "2024-03-15 12:00:00"},
		{"timestamptz", "timestamp with time zone", "2024-03-15T12:00:00Z", "2024-03-15T12:00:00Z"},
		{"uuid", "uuid", "123e4567-e89b-12d3-a456-426614174000", "123e4567-e89b-12d3-a456-426614174000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rexEncode(t, "col", c.typ, c.val, rexText); string(got) != c.want {
				t.Fatalf("text = %q, want %q", got, c.want)
			}
		})
	}
}

// A column absent from the schema keeps today's heuristic output, byte for byte.
func TestResultEncoding_MissingFromSchemaFallsBackToHeuristic(t *testing.T) {
	cache := rexSchema("tbl", "other", "bigint") // schema exists, columns below absent
	cols := []string{"id", "created_at", "name"}
	row := map[string]interface{}{"id": 7, "created_at": "2024-03-15T12:00:00Z", "name": "bob"}
	oids := columnOIDs("tbl", cols, row, cache)

	// binary requested: id int4 (4), *_at timestamp (8), string raw.
	f := rexFields(t, encodeDataRow(cols, row, oids, rexBinary))
	if !bytes.Equal(f[0], rexBE(4, 7)) {
		t.Errorf("id binary = % x", f[0])
	}
	if !bytes.Equal(f[1], rexBE(8, 8840*86400*1_000_000+12*3600*1_000_000)) {
		t.Errorf("created_at binary = % x", f[1])
	}
	if string(f[2]) != "bob" {
		t.Errorf("name binary = %q", f[2])
	}
	// text requested
	f = rexFields(t, encodeDataRow(cols, row, oids, rexText))
	if string(f[0]) != "7" || string(f[1]) != "2024-03-15T12:00:00Z" || string(f[2]) != "bob" {
		t.Errorf("text = %q %q %q", f[0], f[1], f[2])
	}
}

// Legacy hazard kept: nil format codes -> binary for id/_id and *_at/*time,
// text otherwise, regardless of schema-less OIDs.
func TestResultEncoding_SimpleProtocolLegacyHeuristicByteIdentical(t *testing.T) {
	cols := []string{"id", "user_id", "created_at", "name"}
	row := map[string]interface{}{"id": 7, "user_id": 9, "created_at": "2024-03-15T12:00:00Z", "name": "bob"}
	for _, codes := range [][]int16{nil, {}} {
		oids := columnOIDs("", cols, row, nil)
		f := rexFields(t, encodeDataRow(cols, row, oids, codes))
		if !bytes.Equal(f[0], rexBE(4, 7)) || !bytes.Equal(f[1], rexBE(4, 9)) {
			t.Errorf("id/user_id = % x / % x, want int4 binary", f[0], f[1])
		}
		if !bytes.Equal(f[2], rexBE(8, 8840*86400*1_000_000+12*3600*1_000_000)) {
			t.Errorf("created_at = % x, want timestamp binary", f[2])
		}
		if string(f[3]) != "bob" {
			t.Errorf("name = %q, want text", f[3])
		}
		// and identical to what the existing sender writes today
		s, c := net.Pipe()
		go func() { _ = NewResultHandler().SendDataRowWithFormats(s, cols, row, codes); s.Close() }()
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(c)
		c.Close()
		if !bytes.Equal(buf.Bytes(), encodeDataRow(cols, row, oids, codes)) {
			t.Errorf("new encoder differs from SendDataRowWithFormats for codes=%v", codes)
		}
	}
}

// A value that cannot be encoded for its OID becomes text, not a dropped row.
func TestResultEncoding_UnencodableValueFallsBackToText(t *testing.T) {
	cols := []string{"a", "b", "c", "d"}
	cache := map[string][]ColumnInfo{"t": {
		{Field: "a", Type: "bigint"}, {Field: "b", Type: "uuid"},
		{Field: "c", Type: "numeric"}, {Field: "d", Type: "date"},
	}}
	row := map[string]interface{}{"a": "abc", "b": "not-a-uuid", "c": "NaNish", "d": "yesterday"}
	oids := columnOIDs("t", cols, row, cache)
	f := rexFields(t, encodeDataRow(cols, row, oids, rexBinary))
	if len(f) != 4 {
		t.Fatalf("row dropped columns: %d fields", len(f))
	}
	for i, want := range []string{"abc", "not-a-uuid", "NaNish", "yesterday"} {
		if string(f[i]) != want {
			t.Errorf("col %s = %q, want text %q", cols[i], f[i], want)
		}
	}
}

func TestResultEncoding_NullStaysNull(t *testing.T) {
	cols := []string{"applicationid"}
	oids := columnOIDs("t", cols, nil, rexSchema("t", "applicationid", "bigint"))
	f := rexFields(t, encodeDataRow(cols, map[string]interface{}{"applicationid": nil}, oids, rexBinary))
	if f[0] != nil {
		t.Fatalf("expected NULL, got % x", f[0])
	}
}

// DECIDED (2026-10-09): schema-typed columns get identical OIDs from the
// Describe-time and Execute-time computation even when the sample rows differ.
func TestResultEncoding_SchemaTypedOIDStableAcrossDescribeAndExecute(t *testing.T) {
	cache := map[string][]ColumnInfo{"t": {
		{Field: "ref", Type: "text"},
		{Field: "applicationid", Type: "bigint"},
		{Field: "owner_id", Type: "integer"},
	}}
	cols := []string{"ref", "applicationid", "owner_id", "unknown_col"}
	describe := map[string]interface{}{
		"ref": "123e4567-e89b-12d3-a456-426614174000", "applicationid": "123e4567-e89b-12d3-a456-426614174000",
		"owner_id": "123e4567-e89b-12d3-a456-426614174000", "unknown_col": "123e4567-e89b-12d3-a456-426614174000",
	}
	execute := map[string]interface{}{"ref": "", "applicationid": "", "owner_id": "", "unknown_col": ""}

	d := columnOIDs("t", cols, describe, cache)
	e := columnOIDs("t", cols, execute, cache)
	want := []colOID{{25, -1}, {20, 8}, {23, 4}}
	for i, w := range want {
		if d[i] != w || e[i] != w {
			t.Errorf("column %s: describe=%+v execute=%+v want=%+v", cols[i], d[i], e[i], w)
		}
	}
	// The sample row only influences columns the schema does not know.
	if d[3].OID != 2950 {
		t.Errorf("unknown_col describe OID = %d, want 2950 (UUID-shaped sample)", d[3].OID)
	}
	if e[3].OID != 25 {
		t.Errorf("unknown_col execute OID = %d, want 25 (name heuristic)", e[3].OID)
	}
	// And RowDescription agrees with the Describe-time computation.
	rd := rexRowDescOIDs(t, "t", cols, describe, cache)
	for i := range want {
		if rd[cols[i]] != d[i].OID {
			t.Errorf("RowDescription OID for %s = %d, want %d", cols[i], rd[cols[i]], d[i].OID)
		}
	}
}
