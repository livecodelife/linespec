package postgresql

import (
	"net"
)

// StartupHandler manages the connection startup phase
type StartupHandler struct{}

func NewStartupHandler() *StartupHandler {
	return &StartupHandler{}
}

// sendReadyForQuery sends a ReadyForQuery message with the given transaction status byte.
func (h *StartupHandler) sendReadyForQuery(conn net.Conn, txStatus byte) error {
	msg := CreateReadyForQuery(txStatus)
	_, err := conn.Write(msg)
	return err
}
