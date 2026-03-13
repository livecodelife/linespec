package postgresql

import (
	"bufio"
	"fmt"
	"io"
	"net"

	"github.com/livecodelife/linespec/pkg/logger"
)

// StartupHandler manages the connection startup phase
type StartupHandler struct{}

func NewStartupHandler() *StartupHandler {
	return &StartupHandler{}
}

// HandleStartup handles the initial connection phase
// Returns true if connection should proceed, false if it should be closed
func (h *StartupHandler) HandleStartup(conn net.Conn) (map[string]string, error) {
	// Wrap connection in buffered reader to allow peeking
	reader := bufio.NewReader(conn)

	// Check for and handle SSL request
	if err := h.handleSSLRequest(reader, conn); err != nil {
		return nil, fmt.Errorf("SSL handling error: %w", err)
	}

	// Read startup message
	startupMsg, err := h.readStartupMessage(reader)
	if err != nil {
		return nil, fmt.Errorf("error reading startup message: %w", err)
	}

	// Parse startup parameters
	params := ParseStartupMessage(startupMsg)

	// Send authentication OK (trust authentication)
	if err := h.sendAuthenticationOK(conn); err != nil {
		return nil, fmt.Errorf("error sending auth OK: %w", err)
	}

	// Send server parameters
	if err := h.sendServerParameters(conn); err != nil {
		return nil, fmt.Errorf("error sending server params: %w", err)
	}

	// Send backend key data (for cancellation support)
	if err := h.sendBackendKeyData(conn); err != nil {
		return nil, fmt.Errorf("error sending backend key data: %w", err)
	}

	// Send ReadyForQuery
	if err := h.sendReadyForQuery(conn); err != nil {
		return nil, fmt.Errorf("error sending ready for query: %w", err)
	}

	return params, nil
}

// handleSSLRequest checks for and handles SSL request using buffered reader
func (h *StartupHandler) handleSSLRequest(reader *bufio.Reader, conn net.Conn) error {
	// Peek at first 8 bytes without consuming
	peekBuf, err := reader.Peek(8)
	if err != nil {
		return fmt.Errorf("error peeking at connection: %w", err)
	}

	// Check if it's an SSL request (length = 8, magic = 0x04D2162F)
	length := int(peekBuf[0])<<24 | int(peekBuf[1])<<16 | int(peekBuf[2])<<8 | int(peekBuf[3])
	isSSLRequest := length == 8 &&
		peekBuf[4] == 0x04 && peekBuf[5] == 0xD2 && peekBuf[6] == 0x16 && peekBuf[7] == 0x2F

	if isSSLRequest {
		// Consume the SSL request
		if _, err := reader.Discard(8); err != nil {
			return fmt.Errorf("error consuming SSL request: %w", err)
		}
		// Send 'N' to decline SSL
		if _, err := conn.Write([]byte{'N'}); err != nil {
			return fmt.Errorf("error sending SSL response: %w", err)
		}
		logger.Debug("PostgreSQL Proxy: Declined SSL request, client will retry with plaintext")
	}

	return nil
}

// readStartupMessage reads the complete startup message from buffered reader
func (h *StartupHandler) readStartupMessage(reader *bufio.Reader) (*Message, error) {
	// Read length (4 bytes)
	lengthBuf := make([]byte, 4)
	if _, err := io.ReadFull(reader, lengthBuf); err != nil {
		return nil, fmt.Errorf("error reading length: %w", err)
	}

	length := int(lengthBuf[0])<<24 | int(lengthBuf[1])<<16 | int(lengthBuf[2])<<8 | int(lengthBuf[3])

	// Sanity check
	if length < 8 || length > 10000 {
		return nil, fmt.Errorf("invalid startup message length: %d", length)
	}

	// Read the rest of the message (including the version bytes which are part of payload)
	payloadLen := length - 4 // length field is not included in payload
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, fmt.Errorf("error reading payload: %w", err)
	}

	return &Message{
		Type:    MsgStartup,
		Length:  int32(length),
		Payload: payload,
	}, nil
}

// sendAuthenticationOK sends the AuthenticationOk message
func (h *StartupHandler) sendAuthenticationOK(conn net.Conn) error {
	_, err := conn.Write(CreateAuthenticationOk())
	return err
}

// sendServerParameters sends server configuration parameters
func (h *StartupHandler) sendServerParameters(conn net.Conn) error {
	// Send some standard PostgreSQL parameters
	params := map[string]string{
		"server_version":              "14.0",
		"server_encoding":             "UTF8",
		"client_encoding":             "UTF8",
		"DateStyle":                   "ISO, MDY",
		"TimeZone":                    "UTC",
		"integer_datetimes":           "on",
		"standard_conforming_strings": "on",
	}

	for name, value := range params {
		msg := CreateParameterStatus(name, value)
		if _, err := conn.Write(msg); err != nil {
			return err
		}
	}

	return nil
}

// sendBackendKeyData sends the cancellation key data
func (h *StartupHandler) sendBackendKeyData(conn net.Conn) error {
	// Use dummy process ID and secret key
	msg := CreateBackendKeyData(12345, 67890)
	_, err := conn.Write(msg)
	return err
}

// sendReadyForQuery sends ReadyForQuery message
func (h *StartupHandler) sendReadyForQuery(conn net.Conn) error {
	msg := CreateReadyForQuery('I') // 'I' = idle (not in a transaction block)
	_, err := conn.Write(msg)
	return err
}

// HandleStartupWithReader handles startup using a provided buffered reader
func (h *StartupHandler) HandleStartupWithReader(reader *bufio.Reader, conn net.Conn) (map[string]string, error) {
	// Check for and handle SSL request
	if err := h.handleSSLRequest(reader, conn); err != nil {
		return nil, fmt.Errorf("SSL handling error: %w", err)
	}

	// Read startup message
	startupMsg, err := h.readStartupMessage(reader)
	if err != nil {
		return nil, fmt.Errorf("error reading startup message: %w", err)
	}

	// Parse startup parameters
	params := ParseStartupMessage(startupMsg)

	// Send authentication OK (trust authentication)
	if err := h.sendAuthenticationOK(conn); err != nil {
		return nil, fmt.Errorf("error sending auth OK: %w", err)
	}

	// Send server parameters
	if err := h.sendServerParameters(conn); err != nil {
		return nil, fmt.Errorf("error sending server params: %w", err)
	}

	// Send backend key data (for cancellation support)
	if err := h.sendBackendKeyData(conn); err != nil {
		return nil, fmt.Errorf("error sending backend key data: %w", err)
	}

	// Send ReadyForQuery
	if err := h.sendReadyForQuery(conn); err != nil {
		return nil, fmt.Errorf("error sending ready for query: %w", err)
	}

	return params, nil
}
