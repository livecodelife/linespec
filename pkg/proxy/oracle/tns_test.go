package oracle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every fixture in testdata is a packet a real Oracle 23ai received, captured
// off the wire between sqlplus and the server. That matters more here than
// anywhere else in this repo: a protocol parser tested against bytes the same
// package encoded proves only that it agrees with itself, and the two framing
// details that would have been wrong from memory - the 32-bit length and the
// little-endian long form - are exactly the ones such a test would have missed.

func load(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

// Both framings, from real packets. The 32-bit form carries every DATA packet
// and the 16-bit form carries the CONNECT that opens the connection - which is
// why reading only one of them breaks, and why this is a table rather than a
// single case.
func TestPacketLenReadsBothFramings(t *testing.T) {
	for _, tc := range []struct {
		name string
		wide bool // 32-bit length across all four bytes
	}{
		{"connect.bin", false},
		{"select-short.bin", true},
		{"insert.bin", true},
		{"update.bin", true},
		{"delete.bin", true},
		{"select-long-277.bin", true},
		{"select-long-511.bin", true},
		{"select-long-1200.bin", true},
		{"data-no-statement.bin", true},
	} {
		b := load(t, tc.name)
		n, err := PacketLen(b)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if n != len(b) {
			t.Errorf("%s: PacketLen = %d, but the captured packet is %d bytes", tc.name, n, len(b))
		}
		if wide := b[0] == 0 && b[1] == 0; wide != tc.wide {
			t.Errorf("%s: expected wide=%v, header begins %02x %02x", tc.name, tc.wide, b[0], b[1])
		}
	}
}

// The CONNECT packet is the reason the 16-bit form cannot be dropped: it is the
// first packet of every connection and it is framed the old way, so a proxy
// reading 32 bits everywhere stalls before the session even opens.
func TestConnectPacketIsFramedSixteenBit(t *testing.T) {
	b := load(t, "connect.bin")
	if got := int(b[0])<<8 | int(b[1]); got != len(b) {
		t.Fatalf("16-bit read = %d, want %d", got, len(b))
	}
	if got := int(binary32(b)); got == len(b) {
		t.Fatal("a 32-bit read also matched; the two framings would be ambiguous")
	}
}

func binary32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func TestPacketLenRejectsRubbish(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header []byte
		want   error
	}{
		{"fewer bytes than a header", []byte{1, 2, 3}, ErrShortHeader},
		{"wide length below the header", []byte{0x00, 0x00, 0x00, 0x02, TypeData, 0, 0, 0}, ErrImplausibleLength},
		{"narrow length below the header", []byte{0x00, 0x03, 0x11, 0x22, TypeData, 0, 0, 0}, ErrImplausibleLength},
		{"the largest length the framing can express", []byte{0x00, 0x00, 0xff, 0xff, TypeData, 0, 0, 0}, nil},
	} {
		_, err := PacketLen(tc.header)
		if err != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

// A packet larger than the framing can express - only reachable on a session
// negotiated with a large SDU - is refused rather than misread. Its high bytes
// are non-zero, so it falls to the narrow reading, which names a length far too
// small to be a packet.
func TestPacketBeyondTheFramingIsRefusedNotMisread(t *testing.T) {
	// 100,000 bytes: 0x000186a0.
	header := []byte{0x00, 0x01, 0x86, 0xa0, TypeData, 0, 0, 0}
	if _, err := PacketLen(header); err != ErrImplausibleLength {
		t.Fatalf("got %v, want %v", err, ErrImplausibleLength)
	}
}

func TestPacketType(t *testing.T) {
	if got := PacketType(load(t, "connect.bin")); got != TypeConnect {
		t.Errorf("connect.bin type = %d, want %d", got, TypeConnect)
	}
	if got := PacketType(load(t, "select-short.bin")); got != TypeData {
		t.Errorf("select-short.bin type = %d, want %d", got, TypeData)
	}
}

// ── Statement extraction ─────────────────────────────────────────────────────

func TestStatementShortForm(t *testing.T) {
	for _, tc := range []struct{ file, want string }{
		{"select-short.bin", "SELECT surname FROM hr_employees WHERE employee_id = '4471'"},
		{"insert.bin", "INSERT INTO hr_employees (employee_id, surname, department) VALUES ('4471', 'Lovelace', 'RES')"},
		{"update.bin", "UPDATE hr_employees SET surname = 'Byron', department = 'MATH' WHERE employee_id = '4471' AND department = 'RES'"},
		{"delete.bin", "DELETE FROM hr_employees WHERE employee_id = '4471'"},
	} {
		got, ok := Statement(load(t, tc.file))
		if !ok {
			t.Errorf("%s: no statement found", tc.file)
			continue
		}
		if got != tc.want {
			t.Errorf("%s:\n got  %q\n want %q", tc.file, got, tc.want)
		}
	}
}

// The form a realistic statement actually uses. A generated repository's SELECT
// runs well past 253 bytes, so this is the path that matters and the one a
// parser proved on short queries never reaches.
func TestStatementLongForm(t *testing.T) {
	for _, tc := range []struct {
		file string
		size int
	}{
		{"select-long-277.bin", 277},
		{"select-long-511.bin", 511},
		{"select-long-1200.bin", 1200},
	} {
		got, ok := Statement(load(t, tc.file))
		if !ok {
			t.Errorf("%s: no statement found", tc.file)
			continue
		}
		if len(got) != tc.size {
			t.Errorf("%s: statement is %d bytes, want %d", tc.file, len(got), tc.size)
		}
		if !strings.HasPrefix(got, "SELECT") {
			t.Errorf("%s: statement does not open with SELECT: %.40q", tc.file, got)
		}
	}
}

// A packet carrying no statement is ordinary traffic, not a failure. Reporting
// it as an error would make every connection noisy; reporting it as a match
// would satisfy an expectation nothing performed.
func TestPacketsWithoutAStatement(t *testing.T) {
	for _, name := range []string{"connect.bin", "data-no-statement.bin"} {
		if got, ok := Statement(load(t, name)); ok {
			t.Errorf("%s: reported a statement %q", name, got)
		}
	}
}

// A length byte that happens to precede printable bytes is not a statement
// unless those bytes open with a verb and run exactly that far.
func TestStatementRejectsIncidentalText(t *testing.T) {
	body := append([]byte{0, 0, 0, 24, TypeData, 0, 0, 0}, []byte{
		0x0a, 'h', 'e', 'l', 'l', 'o', ' ', 't', 'h', 'e', 'r', 'e', 0x00,
	}...)
	if got, ok := Statement(body); ok {
		t.Errorf("reported a statement %q from prose", got)
	}
}

// Only DATA packets carry statements; a CONNECT packet contains the connect
// descriptor, which is printable and long and must not be mistaken for SQL.
func TestStatementIgnoresNonDataPackets(t *testing.T) {
	b := load(t, "connect.bin")
	if b[4] != TypeConnect {
		t.Fatalf("fixture is type %d, expected CONNECT", b[4])
	}
	if _, ok := Statement(b); ok {
		t.Error("a CONNECT packet reported a statement")
	}
}

// The long form is only used at or above 254 bytes, so a 0xFE marker naming a
// smaller length is not a length and must not be read as one.
func TestLongFormWithAnImplausiblySmallLength(t *testing.T) {
	body := []byte{0, 0, 0, 32, TypeData, 0, 0, 0,
		longFormMarker, 0x08, 0x00, 0x00, 0x00}
	body = append(body, []byte("SELECT 1")...)
	if got, ok := Statement(body); ok {
		t.Errorf("reported %q from a long form under the threshold", got)
	}
}
