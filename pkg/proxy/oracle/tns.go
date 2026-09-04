// Package oracle proxies Oracle Net (TNS) so that a spec can assert what a
// service asked its database, without the service being changed and without
// this package pretending to be Oracle.
//
// It observes and relays. Bytes travel to a real Oracle in both directions
// untouched; only the client-to-server direction is read, and only far enough
// to recover the statement. That is why the channel is a few hundred lines
// rather than a reimplementation of a database: nothing here synthesises a
// result set, negotiates a data type, or answers a handshake. The cost is that
// RETURNS cannot work on this channel - a response this package did not author
// is one it cannot replace - and that is refused when a spec is parsed rather
// than ignored while it runs.
//
// The framing below was measured against a real Oracle 23ai, and the captured
// packets in testdata are what the tests read. Two details would have been
// wrong if taken from the protocol's reputation instead, and both are noted
// where they are implemented.
package oracle

import (
	"encoding/binary"
	"errors"
)

// Packet types, of which only Data can carry a statement.
const (
	TypeConnect  byte = 1
	TypeAccept   byte = 2
	TypeRefuse   byte = 4
	TypeRedirect byte = 5
	TypeData     byte = 6
	TypeResend   byte = 11
	TypeMarker   byte = 12
)

// HeaderLen is the fixed TNS header: length, checksum, type, flags, header
// checksum.
const HeaderLen = 8

// MaxPacketLen is the largest packet this package frames, and it is a property
// of the framing rather than a policy: a wide length is only recognised when
// the header's first two bytes are zero, so the size it can express stops at
// 64 KiB. A session negotiated with a large SDU could exceed that, and such a
// packet is refused rather than misread - its high bytes would be non-zero,
// the narrow reading would name a length far too small, and PacketLen returns
// ErrImplausibleLength. Loud, and in the safe direction.
const MaxPacketLen = 0xFFFF

var (
	// ErrShortHeader means fewer than HeaderLen bytes were available.
	ErrShortHeader = errors.New("oracle: packet shorter than a TNS header")
	// ErrImplausibleLength means the length field named a size this package
	// will not treat as a packet.
	ErrImplausibleLength = errors.New("oracle: implausible TNS packet length")
)

// Packet is one framed TNS packet, header included.
type Packet struct {
	Type byte
	Raw  []byte
}

// PacketLen reads the packet length from a TNS header.
//
// Oracle uses two framings on one connection and the captured packets show
// both. A CONNECT is sent before any version has been agreed, so its length is
// the 16-bit field the protocol has always had, with a checksum in the next two
// bytes. Once the session negotiates, DATA packets put a 32-bit length across
// all four bytes and leave the checksum zero.
//
// The two are told apart by the high half rather than by tracking connection
// state, which would make framing depend on having seen the handshake - no use
// to a proxy that attaches to a connection already in progress. A packet is at
// least HeaderLen bytes, so a 16-bit length never leaves its first two bytes
// zero; a 32-bit length is only plausible below the maximum below, so its high
// two bytes are always zero. The halves therefore cannot both be right, and
// which one is is decidable from the header alone.
func PacketLen(header []byte) (int, error) {
	if len(header) < HeaderLen {
		return 0, ErrShortHeader
	}
	if header[0] == 0 && header[1] == 0 {
		// Bounded by MaxPacketLen through the zero high half rather than by a
		// separate check, which could not fire.
		n := int(binary.BigEndian.Uint32(header[0:4]))
		if n < HeaderLen {
			return 0, ErrImplausibleLength
		}
		return n, nil
	}
	n := int(binary.BigEndian.Uint16(header[0:2]))
	if n < HeaderLen {
		return 0, ErrImplausibleLength
	}
	return n, nil
}

// PacketType returns the packet's type byte.
func PacketType(header []byte) byte {
	if len(header) < 5 {
		return 0
	}
	return header[4]
}

// longFormMarker introduces a length that did not fit in one byte.
const longFormMarker = 0xFE

// Statement returns the SQL text carried by a packet, and whether one was
// found. A packet with no statement in it is the normal case rather than an
// error: Oracle carries a great deal over one connection that is not a
// statement.
//
// A statement is stored as a length followed by its bytes. Below 254 the length
// is a single byte. At or above it, the length is 0xFE followed by four bytes
// LITTLE-endian - the one little-endian field in a protocol that is big-endian
// everywhere else, and the reason a parser proved only on short queries breaks
// on the first realistic one.
//
// The search is for a length that agrees with what follows it, which is a
// strong constraint: the length must be matched by exactly that many printable
// bytes, and those bytes must open with a SQL verb. Anything else is not a
// statement as far as this package is concerned.
func Statement(packet []byte) (string, bool) {
	if PacketType(packet) != TypeData {
		return "", false
	}
	body := packet
	if len(body) > HeaderLen {
		body = body[HeaderLen:]
	}

	// Longest match wins. A statement can contain a byte that also reads as a
	// plausible shorter length, and the full statement is the one that agrees
	// with its own length field.
	best := ""
	for i := 0; i < len(body); i++ {
		text, ok := statementAt(body, i)
		if ok && len(text) > len(best) {
			best = text
		}
	}
	return best, best != ""
}

// statementAt tries to read a length-prefixed statement beginning at i.
func statementAt(body []byte, i int) (string, bool) {
	var start, length int

	switch {
	case body[i] == longFormMarker:
		if i+5 > len(body) {
			return "", false
		}
		length = int(binary.LittleEndian.Uint32(body[i+1 : i+5]))
		start = i + 5
		// Below the marker's threshold the short form would have been used, so
		// a long form claiming a small length is not a length.
		if length < longFormMarker {
			return "", false
		}
	case body[i] > 0 && body[i] < longFormMarker:
		length = int(body[i])
		start = i + 1
	default:
		return "", false
	}

	if length < minStatementLen || start+length > len(body) {
		return "", false
	}
	text := body[start : start+length]
	if !allPrintable(text) || !opensWithVerb(text) {
		return "", false
	}
	return string(text), true
}

// minStatementLen is the shortest thing worth calling a statement; it also
// keeps a stray small length byte from matching a few incidental characters.
const minStatementLen = len("COMMIT")

func allPrintable(b []byte) bool {
	for _, c := range b {
		if c == '\n' || c == '\r' || c == '\t' {
			continue
		}
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// verbs are the statement openers worth reporting. A statement this package
// does not recognise contributes no interaction, which fails the spec that
// expected one rather than matching the wrong expectation.
var verbs = []string{
	"SELECT", "INSERT", "UPDATE", "DELETE", "MERGE", "WITH",
	"BEGIN", "DECLARE", "COMMIT", "ROLLBACK", "CALL",
}

func opensWithVerb(b []byte) bool {
	for _, v := range verbs {
		if len(b) < len(v) {
			continue
		}
		if equalFoldASCII(b[:len(v)], v) {
			return true
		}
	}
	return false
}

func equalFoldASCII(b []byte, s string) bool {
	for i := range b {
		c := b[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if c != s[i] {
			return false
		}
	}
	return true
}
