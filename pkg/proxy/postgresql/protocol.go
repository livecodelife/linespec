package postgresql

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

// Message types for Frontend/Backend protocol
const (
	// Frontend (client) messages
	MsgQuery      = 'Q'
	MsgParse      = 'P'
	MsgBind       = 'B'
	MsgExecute    = 'E'
	MsgSync       = 'S'
	MsgTerminate  = 'X'
	MsgDescribe   = 'D'
	MsgClose      = 'C'
	MsgFlush      = 'H'
	MsgSSLRequest = 0 // Special case
	MsgStartup    = 0 // Special case

	// Backend (server) messages
	MsgAuthentication       = 'R'
	MsgParameterStatus      = 'S'
	MsgBackendKeyData       = 'K'
	MsgReadyForQuery        = 'Z'
	MsgRowDescription       = 'T'
	MsgDataRow              = 'D'
	MsgCommandComplete      = 'C'
	MsgErrorResponse        = 'E'
	MsgNoticeResponse       = 'N'
	MsgParseComplete        = '1'
	MsgBindComplete         = '2'
	MsgCloseComplete        = '3'
	MsgNoData               = 'n'
	MsgPortalSuspended      = 's'
	MsgParameterDescription = 't'
)

// Authentication types
const (
	AuthOK                = 0
	AuthKerberosV5        = 2
	AuthCleartextPassword = 3
	AuthMD5Password       = 5
	AuthSCMCredential     = 6
	AuthGSS               = 7
	AuthGSSContinue       = 8
	AuthSSPI              = 9
	AuthSASL              = 10
)

// SSLRequest magic number
var SSLRequest = []byte{0x00, 0x00, 0x00, 0x08, 0x04, 0xD2, 0x16, 0x2F}

// Message represents a PostgreSQL protocol message
type Message struct {
	Type    byte
	Length  int32
	Payload []byte
}

// ReadMessage reads a complete message from the connection
// Returns nil, nil if it's an SSL request
func ReadMessage(conn net.Conn) (*Message, error) {
	// Peek at first byte to determine if it's SSL request or regular message
	firstByte := make([]byte, 1)
	if _, err := io.ReadFull(conn, firstByte); err != nil {
		return nil, err
	}

	// If first byte is 0, it's likely an SSL request or startup message
	// Read the rest of the 8-byte length field
	lengthBuf := make([]byte, 3)
	if _, err := io.ReadFull(conn, lengthBuf); err != nil {
		return nil, err
	}

	// Combine to get the 4-byte length
	fullLength := make([]byte, 4)
	fullLength[0] = firstByte[0]
	copy(fullLength[1:], lengthBuf)
	length := binary.BigEndian.Uint32(fullLength)

	// Check for SSL request (length=8)
	if length == 8 {
		// Read the remaining 4 bytes to check if it's SSLRequest
		remaining := make([]byte, 4)
		if _, err := io.ReadFull(conn, remaining); err != nil {
			return nil, err
		}

		// Check if it's the SSL request magic number
		if string(remaining) == string(SSLRequest[4:]) {
			return nil, nil // Signal that this is SSL request
		}

		// It's a startup message with length 8 (unlikely, but possible)
		// Reconstruct the startup message
		payload := make([]byte, 4)
		copy(payload, remaining)
		return &Message{
			Type:    MsgStartup,
			Length:  int32(length),
			Payload: payload,
		}, nil
	}

	// It's a startup message or regular message
	// For startup messages, length includes itself and the rest is payload
	payloadLen := length - 4
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, err
	}

	// Check if this looks like a startup message (contains version info)
	// Startup messages start with version number 196608 (3.0) or higher
	if length >= 8 && length < 10240 { // Startup messages are typically larger
		version := binary.BigEndian.Uint32(payload[0:4])
		if version >= 196608 { // Protocol 3.0 or higher
			return &Message{
				Type:    MsgStartup,
				Length:  int32(length),
				Payload: payload,
			}, nil
		}
	}

	// It's a regular message - we already read 4 bytes, need to read more
	// Actually for regular messages we should have read type byte first
	// Let me reconsider... For regular messages it's: [type byte][4-byte length][payload]
	// But for startup it's: [4-byte length][payload]

	// If we got here, we need to re-read with correct format
	// This is getting complex - let's use a simpler approach below
	return nil, fmt.Errorf("unexpected message format")
}

// ReadRegularMessage reads a regular (non-startup) message
func ReadRegularMessage(conn net.Conn) (*Message, error) {
	// Read type byte
	typeBuf := make([]byte, 1)
	if _, err := io.ReadFull(conn, typeBuf); err != nil {
		return nil, err
	}

	// Read length (4 bytes, big-endian)
	lengthBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lengthBuf); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lengthBuf)

	// Read payload (length - 4, since length includes itself)
	payloadLen := length - 4
	if payloadLen > 0 {
		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return nil, err
		}
		return &Message{
			Type:    typeBuf[0],
			Length:  int32(length),
			Payload: payload,
		}, nil
	}

	return &Message{
		Type:    typeBuf[0],
		Length:  int32(length),
		Payload: nil,
	}, nil
}

// ParseStartupMessage parses startup message parameters
func ParseStartupMessage(msg *Message) map[string]string {
	params := make(map[string]string)

	if len(msg.Payload) < 4 {
		return params
	}

	// Skip version (first 4 bytes)
	data := msg.Payload[4:]

	// Parse null-terminated key-value pairs
	for len(data) > 0 {
		// Find null terminator
		nullIdx := 0
		for nullIdx < len(data) && data[nullIdx] != 0 {
			nullIdx++
		}

		if nullIdx == 0 {
			break // Empty string means end
		}

		key := string(data[:nullIdx])
		data = data[nullIdx+1:] // Skip null terminator

		if len(data) == 0 {
			break
		}

		// Find value null terminator
		nullIdx = 0
		for nullIdx < len(data) && data[nullIdx] != 0 {
			nullIdx++
		}

		value := string(data[:nullIdx])
		params[key] = value
		data = data[nullIdx+1:] // Skip null terminator
	}

	return params
}

// ParseQuery extracts SQL from Query message
func ParseQuery(msg *Message) string {
	if msg.Type != MsgQuery || len(msg.Payload) == 0 {
		return ""
	}

	// Query string is null-terminated
	query := string(msg.Payload)
	// Remove null terminator if present
	for i, b := range msg.Payload {
		if b == 0 {
			return query[:i]
		}
	}
	return query
}

// WriteMessage writes a message to the connection
func WriteMessage(conn net.Conn, msgType byte, payload []byte) error {
	// Calculate length (includes the 4 bytes for length field itself)
	length := uint32(len(payload) + 4)

	// Build message: [type][length][payload]
	msg := make([]byte, 0, 1+4+len(payload))
	msg = append(msg, msgType)

	lengthBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBytes, length)
	msg = append(msg, lengthBytes...)
	msg = append(msg, payload...)

	_, err := conn.Write(msg)
	return err
}

// CreateAuthenticationOk creates AuthenticationOk message
func CreateAuthenticationOk() []byte {
	return []byte{
		MsgAuthentication,
		0, 0, 0, 8, // Length = 8
		0, 0, 0, 0, // Auth type = 0 (OK)
	}
}

// CreateParameterStatus creates ParameterStatus message
func CreateParameterStatus(name, value string) []byte {
	payload := []byte(name)
	payload = append(payload, 0) // null terminator
	payload = append(payload, value...)
	payload = append(payload, 0) // null terminator

	return CreateMessage(MsgParameterStatus, payload)
}

// CreateBackendKeyData creates BackendKeyData message
func CreateBackendKeyData(processID, secretKey int32) []byte {
	payload := make([]byte, 8)
	binary.BigEndian.PutUint32(payload[0:4], uint32(processID))
	binary.BigEndian.PutUint32(payload[4:8], uint32(secretKey))

	return CreateMessage(MsgBackendKeyData, payload)
}

// CreateReadyForQuery creates ReadyForQuery message
// status: 'I' = Idle, 'T' = In transaction, 'E' = Failed transaction
func CreateReadyForQuery(status byte) []byte {
	return CreateMessage(MsgReadyForQuery, []byte{status})
}

// CreateCommandComplete creates CommandComplete message
func CreateCommandComplete(tag string) []byte {
	payload := append([]byte(tag), 0) // null-terminated
	return CreateMessage(MsgCommandComplete, payload)
}

// CreateMessage creates a message with given type and payload
func CreateMessage(msgType byte, payload []byte) []byte {
	length := uint32(len(payload) + 4)
	msg := make([]byte, 0, 1+4+len(payload))
	msg = append(msg, msgType)

	lengthBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBytes, length)
	msg = append(msg, lengthBytes...)
	msg = append(msg, payload...)

	return msg
}
