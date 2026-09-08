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
// is a single byte and every client agrees on that. At or above it, 0xFE
// introduces a wide length - and the clients do NOT agree on what follows it:
//
//	sqlplus  fe 15 01 00 00      four bytes LITTLE-endian (277)
//	ODP.NET  fe 02 01 15         marshalled: one byte saying how many follow,
//	                             then that many BIG-endian (277)
//
// Both are read, because both were captured from the client that sends them, and
// the .NET driver is the one a service under test actually uses. Reading only the
// first form is silent rather than loud: no length agrees, the packet contributes
// nothing, and an expectation written against a statement past 254 bytes is
// reported as never called.
//
// The search is for a length that agrees with what follows it, which is a
// strong constraint: the length must be matched by exactly that many printable
// bytes, and those bytes must open with a SQL verb. Anything else is not a
// statement as far as this package is concerned - which is also why two competing
// readings of the same bytes are safe to try in turn.
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

// maxMarshalledLenBytes bounds the count byte of a marshalled length. Oracle
// marshals an integer as a byte count followed by that many big-endian bytes, and
// a statement length needs at most four - so a larger count is not one, and
// bounding it keeps a stray 0xFE from proposing a length out of arbitrary
// following bytes.
const maxMarshalledLenBytes = 4

// statementAt tries to read a length-prefixed statement beginning at i, returning
// the longest reading that agrees with the bytes after it.
func statementAt(body []byte, i int) (string, bool) {
	best := ""
	for _, c := range lengthCandidatesAt(body, i) {
		if c.length < minStatementLen || c.start+c.length > len(body) {
			continue
		}
		text := body[c.start : c.start+c.length]
		if !allPrintable(text) || !opensWithVerb(text) {
			continue
		}
		if len(text) > len(best) {
			best = string(text)
		}
	}
	return best, best != ""
}

// lengthCandidate is one reading of a length prefix: where the text would begin
// and how long it would be.
type lengthCandidate struct{ start, length int }

// lengthCandidatesAt proposes every reading of a length prefix at i. The short
// form has one reading; the wide form has two, because sqlplus and ODP.NET encode
// it differently and a proxy does not get to choose its client.
func lengthCandidatesAt(body []byte, i int) []lengthCandidate {
	if body[i] != longFormMarker {
		if body[i] > 0 && body[i] < longFormMarker {
			return []lengthCandidate{{start: i + 1, length: int(body[i])}}
		}
		return nil
	}

	var out []lengthCandidate

	// sqlplus: four bytes little-endian.
	if i+5 <= len(body) {
		// Below the marker's threshold the short form would have been used, so a
		// wide form claiming a small length is not a length.
		if n := int(binary.LittleEndian.Uint32(body[i+1 : i+5])); n >= longFormMarker {
			out = append(out, lengthCandidate{start: i + 5, length: n})
		}
	}

	// ODP.NET: a marshalled length — a count byte, then that many big-endian.
	if i+1 < len(body) {
		count := int(body[i+1])
		if count >= 1 && count <= maxMarshalledLenBytes && i+2+count <= len(body) {
			n := 0
			for _, b := range body[i+2 : i+2+count] {
				n = n<<8 | int(b)
			}
			if n >= longFormMarker {
				out = append(out, lengthCandidate{start: i + 2 + count, length: n})
			}
		}
	}

	return out
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
	// Leading whitespace is part of the statement, not noise before it. A C#
	// verbatim interpolated string - which is how CrudRepositoryBase assembles a
	// paginated query - puts a newline and an indent before SELECT, and Oracle
	// sends that faithfully. Requiring the verb at byte zero rejects the statement
	// on its formatting.
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\n' || b[0] == '\r' || b[0] == '\t') {
		b = b[1:]
	}
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
