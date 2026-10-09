package postgresql

// Tests for prov-2026-1757dc57: decoding binary-format Bind parameters.
//
// Interface these tests define (does not exist yet):
//
//	func decodeBindParam(oid uint32, format int16, data []byte) string
//	    pure; text (format 0) is always string(data); binary (format 1) is decoded
//	    by type OID; unknown OID or mis-sized payload falls back to string(data).
//
//	func (p *Proxy) extractBindParams(payload []byte, paramOIDs []uint32) []string
//	    paramOIDs are the parameter type OIDs resolved by the caller (Parse
//	    message, then ParameterDescription / schemaCache); may be nil or short.
//
// Decoded text forms the matcher sees: integers in decimal, bool as
// "true"/"false", uuid as canonical 8-4-4-4-12 lowercase, numeric as plain
// decimal with dscale digits, floats via shortest round-trip decimal, date as
// YYYY-MM-DD, timestamp as "YYYY-MM-DD HH:MM:SS[.ffffff]" and timestamptz as
// the same followed by "+00" (UTC).

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
	"time"

	"github.com/livecodelife/linespec/v3/pkg/registry"
)

const (
	bpBool        uint32 = 16
	bpInt8        uint32 = 20
	bpInt2        uint32 = 21
	bpInt4        uint32 = 23
	bpFloat4      uint32 = 700
	bpFloat8      uint32 = 701
	bpDate        uint32 = 1082
	bpTimestamp   uint32 = 1114
	bpTimestamptz uint32 = 1184
	bpNumeric     uint32 = 1700
	bpUUID        uint32 = 2950
)

func bpBE16(v uint16) []byte { b := make([]byte, 2); binary.BigEndian.PutUint16(b, v); return b }
func bpBE32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }
func bpBE64(v uint64) []byte { b := make([]byte, 8); binary.BigEndian.PutUint64(b, v); return b }

var pgEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

func bpMicros(t time.Time) []byte { return bpBE64(uint64(t.Sub(pgEpoch).Microseconds())) }

func bpNumericBytes(weight int16, sign uint16, dscale uint16, digits ...uint16) []byte {
	b := append(bpBE16(uint16(len(digits))), bpBE16(uint16(weight))...)
	b = append(b, bpBE16(sign)...)
	b = append(b, bpBE16(dscale)...)
	for _, d := range digits {
		b = append(b, bpBE16(d)...)
	}
	return b
}

// bpBindFrame builds a Bind payload (without the 'B' tag and length prefix).
// A nil param is NULL (length -1).
func bpBindFrame(fmts []int16, params [][]byte) []byte {
	b := []byte{0, 0} // unnamed portal, unnamed statement
	b = append(b, bpBE16(uint16(len(fmts)))...)
	for _, f := range fmts {
		b = append(b, bpBE16(uint16(f))...)
	}
	b = append(b, bpBE16(uint16(len(params)))...)
	for _, p := range params {
		if p == nil {
			b = append(b, 0xff, 0xff, 0xff, 0xff)
			continue
		}
		b = append(b, bpBE32(uint32(len(p)))...)
		b = append(b, p...)
	}
	return append(b, 0, 0) // zero result format codes
}

func bpProxy() *Proxy {
	return NewProxy("localhost:5432", "localhost:5433", registry.NewMockRegistry())
}

func TestBindParams_DecodeBinary(t *testing.T) {
	uuidBytes, _ := hex.DecodeString("550e8400e29b41d4a716446655440000")
	ts := time.Date(2024, 3, 15, 10, 30, 0, 0, time.UTC)
	tsFrac := time.Date(2024, 3, 15, 10, 30, 45, 123456000, time.UTC)
	f4 := make([]byte, 4)
	binary.BigEndian.PutUint32(f4, math.Float32bits(1.5))
	f4neg := make([]byte, 4)
	binary.BigEndian.PutUint32(f4neg, math.Float32bits(-2.25))

	cases := []struct {
		name string
		oid  uint32
		data []byte
		want string
	}{
		{"int2", bpInt2, bpBE16(7), "7"},
		{"int2_negative", bpInt2, bpBE16(0xFFFF), "-1"},
		{"int4", bpInt4, bpBE32(123456), "123456"},
		{"int4_negative", bpInt4, bpBE32(0xFFFFFFFE), "-2"},
		{"int8", bpInt8, bpBE64(7), "7"},
		{"int8_large", bpInt8, bpBE64(9007199254740993), "9007199254740993"},
		{"int8_negative", bpInt8, bpBE64(math.MaxUint64), "-1"},
		{"bool_true", bpBool, []byte{1}, "true"},
		{"bool_false", bpBool, []byte{0}, "false"},
		{"uuid", bpUUID, uuidBytes, "550e8400-e29b-41d4-a716-446655440000"},
		{"numeric_12345.67", bpNumeric, bpNumericBytes(1, 0, 2, 1, 2345, 6700), "12345.67"},
		{"numeric_negative_fraction", bpNumeric, bpNumericBytes(-1, 0x4000, 1, 5000), "-0.5"},
		{"numeric_zero", bpNumeric, bpNumericBytes(0, 0, 0), "0"},
		{"numeric_NaN", bpNumeric, bpNumericBytes(0, 0xC000, 0), "NaN"},
		{"float4", bpFloat4, f4, "1.5"},
		{"float4_negative", bpFloat4, f4neg, "-2.25"},
		{"float8", bpFloat8, bpBE64(math.Float64bits(3.14159)), "3.14159"},
		{"float8_integral", bpFloat8, bpBE64(math.Float64bits(2)), "2"},
		{"date", bpDate, bpBE32(uint32(int32(ts.Sub(pgEpoch).Hours() / 24))), "2024-03-15"},
		{"date_epoch", bpDate, bpBE32(0), "2000-01-01"},
		{"date_before_epoch", bpDate, bpBE32(0xFFFFFFFF), "1999-12-31"},
		{"timestamp", bpTimestamp, bpMicros(ts), "2024-03-15 10:30:00"},
		{"timestamp_fraction", bpTimestamp, bpMicros(tsFrac), "2024-03-15 10:30:45.123456"},
		{"timestamptz", bpTimestamptz, bpMicros(ts), "2024-03-15 10:30:00+00"},
		{"timestamptz_fraction", bpTimestamptz, bpMicros(tsFrac), "2024-03-15 10:30:45.123456+00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decodeBindParam(c.oid, 1, c.data); got != c.want {
				t.Errorf("decodeBindParam(oid=%d, binary, %x) = %q, want %q", c.oid, c.data, got, c.want)
			}
		})
	}
}

// Text format (code 0) is passed through verbatim for every supported type.
func TestBindParams_DecodeText(t *testing.T) {
	cases := []struct {
		name string
		oid  uint32
		text string
	}{
		{"int2", bpInt2, "7"},
		{"int4", bpInt4, "123456"},
		{"int8", bpInt8, "7"},
		{"bool", bpBool, "true"},
		{"bool_t", bpBool, "t"},
		{"uuid", bpUUID, "550e8400-e29b-41d4-a716-446655440000"},
		{"numeric", bpNumeric, "12345.67"},
		{"float4", bpFloat4, "1.5"},
		{"float8", bpFloat8, "3.14159"},
		{"date", bpDate, "2024-03-15"},
		{"timestamp", bpTimestamp, "2024-03-15 10:30:00"},
		{"timestamptz", bpTimestamptz, "2024-03-15 10:30:00+00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decodeBindParam(c.oid, 0, []byte(c.text)); got != c.text {
				t.Errorf("decodeBindParam(oid=%d, text, %q) = %q, want unchanged", c.oid, c.text, got)
			}
		})
	}
	// Text bytes that look like an 8-byte value must not be decoded as binary.
	if got := decodeBindParam(bpInt8, 0, []byte("12345678")); got != "12345678" {
		t.Errorf("text int8 payload of 8 bytes was reinterpreted: %q", got)
	}
}

// Unknown OIDs and mis-sized binary values fall back to string(bytes), no panic.
func TestBindParams_DecodeFallbackNeverPanics(t *testing.T) {
	raw := []byte{0x00, 0x01, 0x02, 0xff}
	if got := decodeBindParam(99999, 1, raw); got != string(raw) {
		t.Errorf("unknown OID: got %q, want %q", got, string(raw))
	}
	if got := decodeBindParam(0, 1, raw); got != string(raw) {
		t.Errorf("OID 0: got %q, want %q", got, string(raw))
	}

	oids := []uint32{bpBool, bpInt8, bpInt2, bpInt4, bpFloat4, bpFloat8, bpDate,
		bpTimestamp, bpTimestamptz, bpNumeric, bpUUID, 0, 99999}
	payloads := [][]byte{nil, {}, {1}, {1, 2, 3}, {1, 2, 3, 4, 5}, make([]byte, 7), make([]byte, 9), make([]byte, 15), make([]byte, 17),
		// numeric header claiming more digits than present
		{0, 9, 0, 0, 0, 0, 0, 0},
	}
	for _, oid := range oids {
		for _, pl := range payloads {
			for _, format := range []int16{0, 1} {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("decodeBindParam(oid=%d, fmt=%d, %x) panicked: %v", oid, format, pl, r)
						}
					}()
					_ = decodeBindParam(oid, format, pl)
				}()
			}
		}
	}

	// Specific mis-sized values must come back as the raw string, not a guess.
	for _, c := range []struct {
		name string
		oid  uint32
		data []byte
	}{
		{"int8_4bytes", bpInt8, bpBE32(7)},
		{"int4_8bytes", bpInt4, bpBE64(7)},
		{"int2_1byte", bpInt2, []byte{7}},
		{"uuid_15bytes", bpUUID, make([]byte, 15)},
		{"bool_2bytes", bpBool, []byte{1, 0}},
		{"timestamp_4bytes", bpTimestamp, bpBE32(1)},
		{"float8_4bytes", bpFloat8, bpBE32(1)},
	} {
		if got := decodeBindParam(c.oid, 1, c.data); got != string(c.data) {
			t.Errorf("%s: got %q, want raw fallback %q", c.name, got, string(c.data))
		}
	}
}

func TestBindParams_ExtractFormatCodes(t *testing.T) {
	p := bpProxy()

	t.Run("zero_codes_all_text", func(t *testing.T) {
		frame := bpBindFrame(nil, [][]byte{[]byte("7"), []byte("hello")})
		got := p.extractBindParams(frame, []uint32{bpInt8, 25})
		want := []string{"7", "hello"}
		bpEqual(t, got, want)
	})

	t.Run("zero_codes_text_not_decoded_even_if_8_bytes", func(t *testing.T) {
		got := p.extractBindParams(bpBindFrame(nil, [][]byte{[]byte("12345678")}), []uint32{bpInt8})
		bpEqual(t, got, []string{"12345678"})
	})

	t.Run("one_code_binary_applies_to_all", func(t *testing.T) {
		frame := bpBindFrame([]int16{1}, [][]byte{bpBE64(7), bpBE32(42), {1}})
		got := p.extractBindParams(frame, []uint32{bpInt8, bpInt4, bpBool})
		bpEqual(t, got, []string{"7", "42", "true"})
	})

	t.Run("one_code_text_applies_to_all", func(t *testing.T) {
		frame := bpBindFrame([]int16{0}, [][]byte{[]byte("7"), []byte("42")})
		got := p.extractBindParams(frame, []uint32{bpInt8, bpInt4})
		bpEqual(t, got, []string{"7", "42"})
	})

	t.Run("per_param_codes_mixed", func(t *testing.T) {
		frame := bpBindFrame([]int16{1, 0, 1}, [][]byte{bpBE64(7), []byte("alice"), bpBE16(3)})
		got := p.extractBindParams(frame, []uint32{bpInt8, 25, bpInt2})
		bpEqual(t, got, []string{"7", "alice", "3"})
	})

	t.Run("null_param_is_empty_string_and_keeps_alignment", func(t *testing.T) {
		frame := bpBindFrame([]int16{1, 1, 1}, [][]byte{bpBE64(7), nil, bpBE32(9)})
		got := p.extractBindParams(frame, []uint32{bpInt8, bpInt4, bpInt4})
		bpEqual(t, got, []string{"7", "", "9"})
	})

	t.Run("null_with_single_text_code", func(t *testing.T) {
		frame := bpBindFrame([]int16{0}, [][]byte{nil, []byte("x")})
		got := p.extractBindParams(frame, nil)
		bpEqual(t, got, []string{"", "x"})
	})

	t.Run("missing_oids_fall_back_to_raw", func(t *testing.T) {
		frame := bpBindFrame([]int16{1}, [][]byte{bpBE64(7)})
		got := p.extractBindParams(frame, nil)
		bpEqual(t, got, []string{string(bpBE64(7))})
	})

	t.Run("fewer_oids_than_params", func(t *testing.T) {
		frame := bpBindFrame([]int16{1, 1}, [][]byte{bpBE64(7), bpBE64(8)})
		got := p.extractBindParams(frame, []uint32{bpInt8})
		bpEqual(t, got, []string{"7", string(bpBE64(8))})
	})

	t.Run("unknown_oid_falls_back", func(t *testing.T) {
		frame := bpBindFrame([]int16{1}, [][]byte{{0xde, 0xad}})
		got := p.extractBindParams(frame, []uint32{424242})
		bpEqual(t, got, []string{string([]byte{0xde, 0xad})})
	})

	t.Run("mis_sized_binary_falls_back", func(t *testing.T) {
		frame := bpBindFrame([]int16{1}, [][]byte{bpBE32(7)})
		got := p.extractBindParams(frame, []uint32{bpInt8})
		bpEqual(t, got, []string{string(bpBE32(7))})
	})
}

func TestBindParams_ExtractTruncatedNeverPanics(t *testing.T) {
	p := bpProxy()
	frame := bpBindFrame([]int16{1, 0, 1}, [][]byte{bpBE64(7), []byte("alice"), nil})
	oids := []uint32{bpInt8, 25, bpInt4}

	// Every strict prefix must return without panicking.
	for n := 0; n < len(frame); n++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("extractBindParams panicked on %d-byte prefix: %v", n, r)
				}
			}()
			_ = p.extractBindParams(frame[:n], oids)
		}()
	}

	// A payload cut mid-way through the second parameter keeps the first one decoded.
	cut := len(frame) - 2 /*result fmts*/ - 4 /*NULL len*/ - 2 /*2 bytes of "alice"*/
	if got := p.extractBindParams(frame[:cut], oids); len(got) < 1 || got[0] != "7" {
		t.Errorf("truncated frame: first param = %v, want it decoded as \"7\"", got)
	}

	// Declared format-code count larger than the payload.
	bad := []byte{0, 0, 0xff, 0xff, 0, 1}
	_ = p.extractBindParams(bad, oids)
}

// Npgsql sends int8 parameters in binary format. This is the Bind frame for
// `... WHERE sponsorid = $1` with sponsorid = 7: unnamed portal and statement,
// one parameter format code (binary), one 8-byte parameter, and one result
// format code (binary). Parameter OID 20 (int8) comes from Npgsql's Parse.
// Candidate for the per-driver conformance corpus (prov-2026-167380f0).
func TestBindParams_NpgsqlTranscriptInt8(t *testing.T) {
	frame, err := hex.DecodeString(
		"00" + // portal ""
			"00" + // statement ""
			"0001" + "0001" + // 1 parameter format code: binary
			"0001" + // 1 parameter
			"00000008" + "0000000000000007" + // int8 = 7
			"0001" + "0001") // 1 result format code: binary
	if err != nil {
		t.Fatal(err)
	}
	got := bpProxy().extractBindParams(frame, []uint32{bpInt8})
	bpEqual(t, got, []string{"7"})

	// Same frame with unresolved OIDs must not panic and must not lose the param.
	if got := bpProxy().extractBindParams(frame, nil); len(got) != 1 {
		t.Errorf("nil OIDs: got %d params, want 1", len(got))
	}
}

func bpEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %q (len %d), want %q (len %d)", got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("param %d = %q, want %q", i+1, got[i], want[i])
		}
	}
}
