package postgresql

import (
	"encoding/binary"
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
