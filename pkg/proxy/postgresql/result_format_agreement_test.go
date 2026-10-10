package postgresql

// Spec for prov-2026-18ec8274: the mock RowDescription declares the same
// per-column result format the DataRow is encoded with.
//
// Seam this file defines (does not exist yet; the file fails to compile until
// it does):
//
//	type resultFormat int
//	const (
//	    resultFormatLegacy resultFormat = iota // no Bind codes: RowDescription says 0, DataRow uses the name heuristic
//	    resultFormatText
//	    resultFormatBinary
//	)
//
//	// resultFormats: pure; no proxy, connection or registry state. It is the
//	// only place Bind result format codes are resolved, for the RowDescription
//	// and the DataRow alike. Always returns exactly n entries.
//	//   nil/empty codes -> every column resultFormatLegacy
//	//   one code        -> that format for every column (0 text, 1 binary)
//	//   N codes         -> per column; a short list pads the rest with text;
//	//                      surplus codes are ignored
//	func resultFormats(bindCodes []int16, n int) []resultFormat
//
// Everything else is observed on the wire by driving the real proxy loop
// (handleClientMessagesWithInterception) with recorded-style transcripts via
// the existing pgo* helpers, so these tests name no builder signature and
// survive whatever shape SendRowDescription* takes. Describe Portal and the
// Execute-time RowDescription (no prior Describe) are both exercised, as are
// Describe Statement (before Bind) and the simple protocol.

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/livecodelife/linespec/v3/pkg/types"
)

const fagSQL = "SELECT applicationid, name, id, created_at FROM applications"

var fagCols = []string{"applicationid", "name", "id", "created_at"}

// --- helpers (prefix fag) ---------------------------------------------------

type fagField struct {
	name   string
	oid    uint32
	format int16
}

// fagRowDesc decodes a RowDescription payload (including per-column format code).
func fagRowDesc(t *testing.T, payload []byte) []fagField {
	t.Helper()
	n := int(binary.BigEndian.Uint16(payload[0:2]))
	pos := 2
	out := make([]fagField, 0, n)
	for i := 0; i < n; i++ {
		start := pos
		for payload[pos] != 0 {
			pos++
		}
		f := fagField{name: string(payload[start:pos])}
		pos++    // NUL
		pos += 4 // table OID
		pos += 2 // column number
		f.oid = binary.BigEndian.Uint32(payload[pos:])
		pos += 4 // type OID
		pos += 2 // type size
		pos += 4 // type modifier
		f.format = int16(binary.BigEndian.Uint16(payload[pos:]))
		pos += 2
		out = append(out, f)
	}
	return out
}

// fagDataRow decodes a DataRow payload into raw per-column values (nil = NULL).
func fagDataRow(t *testing.T, payload []byte) [][]byte {
	t.Helper()
	n := int(binary.BigEndian.Uint16(payload[0:2]))
	pos := 2
	out := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		l := int32(binary.BigEndian.Uint32(payload[pos:]))
		pos += 4
		if l < 0 {
			out = append(out, nil)
			continue
		}
		out = append(out, payload[pos:pos+int(l)])
		pos += int(l)
	}
	return out
}

// fagBind builds a Bind frame: unnamed portal and statement, no parameters, the
// given result format codes (nil = none).
func fagBind(codes []int16) []byte {
	b := []byte{0, 0, 0, 0, 0, 0} // portal\0 stmt\0 nParamFmts=0 nParams=0
	b = binary.BigEndian.AppendUint16(b, uint16(len(codes)))
	for _, c := range codes {
		b = binary.BigEndian.AppendUint16(b, uint16(c))
	}
	return pgoMsg('B', b)
}

func fagDescribeStatement() []byte { return pgoMsg('D', []byte{'S', 0}) }

func fagQuery(q string) []byte { return pgoMsg('Q', append([]byte(q), 0)) }

// fagProxy returns a proxy with a plain READ mock for the applications table
// returning one row, and a schema cache that makes applicationid a bigint.
func fagProxy(t *testing.T) *Proxy {
	t.Helper()
	dir := t.TempDir()
	row := `{"applicationid": 11, "name": "x", "id": 5, "created_at": "2024-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "row.json"), []byte(row), 0o600); err != nil {
		t.Fatal(err)
	}
	p := pgoProxy(t, types.ExpectStatement{
		Channel:         types.ReadPostgreSQL,
		AccessingTables: []string{"applications"},
		VerifyOperation: "SELECT",
		ReturnsFile:     "row.json",
		BaseDir:         dir,
	})
	p.schemaCache = map[string][]ColumnInfo{"applications": {
		{Field: "applicationid", Type: "bigint"},
		{Field: "name", Type: "text"},
		{Field: "id", Type: "integer"},
		{Field: "created_at", Type: "timestamp with time zone"},
	}}
	return p
}

// fagResult is the first RowDescription and DataRow the client saw.
type fagResult struct {
	types string
	rd    []fagField
	rdN   int
	row   [][]byte
}

func fagObserve(t *testing.T, client []pgoFrame) fagResult {
	t.Helper()
	r := fagResult{types: pgoTypes(client)}
	for _, f := range client {
		switch f.typ {
		case 'T':
			if r.rd == nil {
				r.rd = fagRowDesc(t, f.payload)
			}
			r.rdN++
		case 'D':
			if r.row == nil {
				r.row = fagDataRow(t, f.payload)
			}
		case 'E':
			t.Fatalf("client saw ErrorResponse: %q (frames %q)", f.payload, r.types)
		}
	}
	return r
}

var fagTS = func() []byte {
	d := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Sub(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	return binary.BigEndian.AppendUint64(nil, uint64(d.Microseconds()))
}()

var (
	fagBigint11 = []byte{0, 0, 0, 0, 0, 0, 0, 0x0b} // binary int8 11
	fagInt5     = []byte{0, 0, 0, 5}                // binary int4 5
)

// fagWant is the hand-written expectation for one Bind result-format case.
// text/binary expectations for each of applicationid, name, id, created_at.
type fagWant struct {
	rdFormats []int16  // what the RowDescription must declare, per column
	fields    [][]byte // exact DataRow field bytes, per column
}

var (
	fagTextFields = [][]byte{[]byte("11"), []byte("x"), []byte("5"), []byte("2024-01-01T00:00:00Z")}
	fagBinFields  = [][]byte{fagBigint11, []byte("x"), fagInt5, fagTS}
)

func fagPick(codes ...int) [][]byte { // 0 text / 1 binary per column
	out := make([][]byte, len(codes))
	for i, c := range codes {
		if c == 1 {
			out[i] = fagBinFields[i]
		} else {
			out[i] = fagTextFields[i]
		}
	}
	return out
}

var fagCases = []struct {
	name  string
	codes []int16
	want  fagWant
}{
	// A Bind with zero result format codes: extractBindResultFormats turns it
	// into []int16{0} (PostgreSQL spec: all text) today, so it is NOT the legacy
	// heuristic case. RowDescription 0 and text DataRow, byte-identical to today.
	{"bind with zero codes (text today)", nil, fagWant{[]int16{0, 0, 0, 0}, fagPick(0, 0, 0, 0)}},
	{"one code text", []int16{0}, fagWant{[]int16{0, 0, 0, 0}, fagPick(0, 0, 0, 0)}},
	{"one code binary", []int16{1}, fagWant{[]int16{1, 1, 1, 1}, fagPick(1, 1, 1, 1)}},
	{"per-column all binary", []int16{1, 1, 1, 1}, fagWant{[]int16{1, 1, 1, 1}, fagPick(1, 1, 1, 1)}},
	{"per-column all text", []int16{0, 0, 0, 0}, fagWant{[]int16{0, 0, 0, 0}, fagPick(0, 0, 0, 0)}},
	{"mixed binary-first", []int16{1, 0, 1, 0}, fagWant{[]int16{1, 0, 1, 0}, fagPick(1, 0, 1, 0)}},
	{"mixed text-first", []int16{0, 1, 0, 1}, fagWant{[]int16{0, 1, 0, 1}, fagPick(0, 1, 0, 1)}},
	{"short list pads with text", []int16{1, 1}, fagWant{[]int16{1, 1, 0, 0}, fagPick(1, 1, 0, 0)}},
}

func fagAssert(t *testing.T, r fagResult, w fagWant) {
	t.Helper()
	if len(r.rd) != len(fagCols) {
		t.Fatalf("RowDescription has %d columns, want %d (frames %q)", len(r.rd), len(fagCols), r.types)
	}
	if len(r.row) != len(fagCols) {
		t.Fatalf("DataRow has %d fields, want %d (frames %q)", len(r.row), len(fagCols), r.types)
	}
	for i, col := range fagCols {
		if r.rd[i].name != col {
			t.Errorf("column %d name = %q, want %q", i, r.rd[i].name, col)
		}
		if r.rd[i].format != w.rdFormats[i] {
			t.Errorf("%s: RowDescription format code = %d, want %d", col, r.rd[i].format, w.rdFormats[i])
		}
		if string(r.row[i]) != string(w.fields[i]) {
			t.Errorf("%s: DataRow bytes = % x, want % x", col, r.row[i], w.fields[i])
		}
	}
	if r.rd[0].oid != 20 {
		t.Errorf("applicationid RowDescription OID = %d, want 20 (int8)", r.rd[0].oid)
	}
}

// --- (1) the pure function ---------------------------------------------------

func TestFormatAgreement_ResolveTable(t *testing.T) {
	L, T, B := resultFormatLegacy, resultFormatText, resultFormatBinary
	cases := []struct {
		name  string
		codes []int16
		n     int
		want  []resultFormat
	}{
		{"nil codes is legacy for every column", nil, 3, []resultFormat{L, L, L}},
		{"empty codes is legacy for every column", []int16{}, 2, []resultFormat{L, L}},
		{"one text code applies to all", []int16{0}, 3, []resultFormat{T, T, T}},
		{"one binary code applies to all", []int16{1}, 3, []resultFormat{B, B, B}},
		{"N codes are per column", []int16{1, 0, 1}, 3, []resultFormat{B, T, B}},
		{"mixed", []int16{0, 1, 0, 1}, 4, []resultFormat{T, B, T, B}},
		{"short list pads with text", []int16{1, 1}, 4, []resultFormat{B, B, T, T}},
		{"short list of 2 with text first", []int16{0, 1}, 3, []resultFormat{T, B, T}},
		{"surplus codes are ignored", []int16{1, 0, 1, 1}, 2, []resultFormat{B, T}},
		{"zero columns", []int16{1}, 0, []resultFormat{}},
		{"single column single code", []int16{1}, 1, []resultFormat{B}},
	}
	for _, c := range cases {
		got := resultFormats(c.codes, c.n)
		if len(got) != c.n {
			t.Errorf("%s: len = %d, want %d", c.name, len(got), c.n)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: column %d = %v, want %v", c.name, i, got[i], c.want[i])
			}
		}
	}
}

// The function must not alias or mutate its input.
func TestFormatAgreement_ResolveDoesNotMutateInput(t *testing.T) {
	in := []int16{1, 0}
	_ = resultFormats(in, 4)
	if len(in) != 2 || in[0] != 1 || in[1] != 0 {
		t.Errorf("input mutated: %v", in)
	}
}

// --- (2) Describe Portal: RowDescription agrees with the DataRow -------------

func TestFormatAgreement_DescribePortal(t *testing.T) {
	for _, c := range fagCases {
		t.Run(c.name, func(t *testing.T) {
			p := fagProxy(t)
			client, _ := pgoRun(t, p, pgoCat(
				pgoParse(fagSQL),
				fagBind(c.codes),
				pgoDescribePortal(),
				pgoExecute(),
				pgoSync(),
			))
			r := fagObserve(t, client)
			if r.rdN != 1 {
				t.Fatalf("client saw %d RowDescriptions, want exactly 1 (frames %q)", r.rdN, r.types)
			}
			fagAssert(t, r, c.want)
		})
	}
}

// --- (3) Execute-time RowDescription (no prior Describe) ---------------------

func TestFormatAgreement_ExecuteTimeRowDescription(t *testing.T) {
	for _, c := range fagCases {
		t.Run(c.name, func(t *testing.T) {
			p := fagProxy(t)
			client, _ := pgoRun(t, p, pgoCat(
				pgoParse(fagSQL),
				fagBind(c.codes),
				pgoExecute(),
				pgoSync(),
			))
			r := fagObserve(t, client)
			if r.rdN != 1 {
				t.Fatalf("client saw %d RowDescriptions, want exactly 1 (frames %q)", r.rdN, r.types)
			}
			fagAssert(t, r, c.want)
		})
	}
}

// The exact reported case, stated on its own: applicationid bigint, binary
// results. RowDescription OID 20 AND format code 1; DataRow 00 00 00 08 ... 0b.
func TestFormatAgreement_ApplicationIDBigintBinaryReportedCase(t *testing.T) {
	for _, withDescribe := range []bool{true, false} {
		name := "execute-time"
		steps := [][]byte{pgoParse(fagSQL), fagBind([]int16{1}), pgoExecute(), pgoSync()}
		if withDescribe {
			name = "describe-portal"
			steps = [][]byte{pgoParse(fagSQL), fagBind([]int16{1}), pgoDescribePortal(), pgoExecute(), pgoSync()}
		}
		t.Run(name, func(t *testing.T) {
			client, _ := pgoRun(t, fagProxy(t), pgoCat(steps...))
			r := fagObserve(t, client)
			if len(r.rd) == 0 || len(r.row) == 0 {
				t.Fatalf("missing RowDescription/DataRow (frames %q)", r.types)
			}
			if r.rd[0].name != "applicationid" || r.rd[0].oid != 20 || r.rd[0].format != 1 {
				t.Errorf("applicationid RowDescription = %+v, want OID 20 and format code 1", r.rd[0])
			}
			want := []byte{0, 0, 0, 8, 0, 0, 0, 0, 0, 0, 0, 0x0b}
			got := binary.BigEndian.AppendUint32(nil, uint32(len(r.row[0])))
			got = append(got, r.row[0]...)
			if string(got) != string(want) {
				t.Errorf("applicationid DataRow field = % x, want % x", got, want)
			}
		})
	}
}

// --- (4) Describe Statement answers before Bind: formats unknown, stays 0 ----

func TestFormatAgreement_DescribeStatementDeclaresTextForEveryColumn(t *testing.T) {
	p := fagProxy(t)
	client, _ := pgoRun(t, p, pgoCat(
		pgoParse(fagSQL),
		fagDescribeStatement(),
		pgoSync(),
	))
	r := fagObserve(t, client)
	if r.rd == nil {
		t.Fatalf("no RowDescription for mock-only Describe Statement (frames %q)", r.types)
	}
	for i, f := range r.rd {
		if f.format != 0 {
			t.Errorf("column %d (%s): Describe Statement format = %d, want 0 (formats unknown before Bind)", i, f.name, f.format)
		}
	}
	if r.rd[0].oid != 20 {
		t.Errorf("applicationid OID = %d, want 20", r.rd[0].oid)
	}
}

// Full Npgsql-shaped flow: Describe Statement, then Bind asks binary. The
// statement RowDescription still says 0 (what PostgreSQL does), Execute adds no
// second RowDescription, and the DataRow follows the Bind-time formats.
func TestFormatAgreement_DescribeStatementThenBinaryBind(t *testing.T) {
	p := fagProxy(t)
	client, _ := pgoRun(t, p, pgoCat(
		pgoParse(fagSQL),
		fagDescribeStatement(),
		fagBind([]int16{1}),
		pgoExecute(),
		pgoSync(),
	))
	r := fagObserve(t, client)
	if r.rdN != 1 {
		t.Fatalf("client saw %d RowDescriptions, want exactly 1 (frames %q)", r.rdN, r.types)
	}
	for i, f := range r.rd {
		if f.format != 0 {
			t.Errorf("column %d (%s): Describe Statement format = %d, want 0", i, f.name, f.format)
		}
	}
	fagAssertRow(t, r, fagPick(1, 1, 1, 1))
}

func fagAssertRow(t *testing.T, r fagResult, fields [][]byte) {
	t.Helper()
	if len(r.row) != len(fields) {
		t.Fatalf("DataRow has %d fields, want %d (frames %q)", len(r.row), len(fields), r.types)
	}
	for i := range fields {
		if string(r.row[i]) != string(fields[i]) {
			t.Errorf("%s: DataRow bytes = % x, want % x", fagCols[i], r.row[i], fields[i])
		}
	}
}

// --- (5) DECIDED: legacy nil-codes case stays byte-identical -----------------

// Simple protocol: RowDescription format 0 for every column, DataRow uses the
// name heuristic (id and created_at binary, applicationid and name text).
func TestFormatAgreement_SimpleProtocolLegacyByteIdentical(t *testing.T) {
	p := fagProxy(t)
	client, _ := pgoRun(t, p, fagQuery(fagSQL))
	r := fagObserve(t, client)
	if r.rd == nil || r.row == nil {
		t.Fatalf("simple query produced no RowDescription/DataRow (frames %q)", r.types)
	}
	for i, f := range r.rd {
		if f.format != 0 {
			t.Errorf("column %d (%s): simple-protocol RowDescription format = %d, want 0", i, f.name, f.format)
		}
	}
	fagAssertRow(t, r, [][]byte{[]byte("11"), []byte("x"), fagInt5, fagTS})
}

// A Bind with no result format codes is text today (see fagCases), with
// RowDescription 0. The legacy heuristic is reachable on the extended protocol
// only if the Bind result formats cannot be parsed, which is not tested here.
func TestFormatAgreement_BindWithoutCodesByteIdentical(t *testing.T) {
	flows := map[string][][]byte{
		"describe-portal": {pgoParse(fagSQL), fagBind(nil), pgoDescribePortal(), pgoExecute(), pgoSync()},
		"execute-time":    {pgoParse(fagSQL), fagBind(nil), pgoExecute(), pgoSync()},
	}
	for name, steps := range flows {
		t.Run(name, func(t *testing.T) {
			client, _ := pgoRun(t, fagProxy(t), pgoCat(steps...))
			r := fagObserve(t, client)
			if r.rd == nil || r.row == nil {
				t.Fatalf("missing RowDescription/DataRow (frames %q)", r.types)
			}
			for i, f := range r.rd {
				if f.format != 0 {
					t.Errorf("column %d (%s): RowDescription format = %d, want 0", i, f.name, f.format)
				}
			}
			fagAssertRow(t, r, fagPick(0, 0, 0, 0))
		})
	}
}

// Text-format output is unchanged: RowDescription 0 and text DataRow bytes.
func TestFormatAgreement_TextFormatUnchanged(t *testing.T) {
	client, _ := pgoRun(t, fagProxy(t), pgoCat(
		pgoParse(fagSQL), fagBind([]int16{0}), pgoDescribePortal(), pgoExecute(), pgoSync(),
	))
	r := fagObserve(t, client)
	for i, f := range r.rd {
		if f.format != 0 {
			t.Errorf("column %d (%s): format = %d, want 0", i, f.name, f.format)
		}
	}
	fagAssertRow(t, r, fagPick(0, 0, 0, 0))
}
