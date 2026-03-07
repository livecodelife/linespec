package kafka

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"github.com/calebcowen/linespec/pkg/registry"
)

type Interceptor struct {
	addr     string
	registry *registry.MockRegistry
}

func NewInterceptor(addr string, reg *registry.MockRegistry) *Interceptor {
	return &Interceptor{
		addr:     addr,
		registry: reg,
	}
}

func (i *Interceptor) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", i.addr)
	if err != nil {
		return err
	}
	defer ln.Close()

	fmt.Printf("Kafka Interceptor listening on %s\n", i.addr)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				continue
			}
		}
		go i.handleConn(conn)
	}
}

func (i *Interceptor) handleConn(conn net.Conn) {
	defer conn.Close()

	for {
		// Kafka protocol: 4 bytes length + request
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return
		}
		length := binary.BigEndian.Uint32(lenBuf)

		request := make([]byte, length)
		if _, err := io.ReadFull(conn, request); err != nil {
			return
		}

		if length < 8 {
			continue
		}

		apiKey := binary.BigEndian.Uint16(request[0:2])
		_ = binary.BigEndian.Uint16(request[2:4]) // apiVersion
		correlationID := request[4:8]

		// Handle ApiVersions (18) or Metadata (3) requests to allow the client to connect
		switch apiKey {
		case 18: // ApiVersions
			i.sendApiVersionsResponse(conn, correlationID)
		case 3: // Metadata
			i.sendMetadataResponse(conn, correlationID)
		case 0: // Produce
			// We could decode this and match against registry in the future
			i.sendProduceResponse(conn, correlationID)
		default:
			// Just send an empty response for others to prevent hanging
			i.sendGenericResponse(conn, correlationID)
		}
	}
}

func (i *Interceptor) sendApiVersionsResponse(conn net.Conn, correlationID []byte) {
	// Minimal ApiVersions response
	// ErrorCode (int16) = 0
	// NumApiVersions (int32) = 0 (client will fallback to defaults)
	// ThrottleTimeMs (int32) = 0
	payload := append(correlationID, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	i.writeResponse(conn, payload)
}

func (i *Interceptor) sendMetadataResponse(conn net.Conn, correlationID []byte) {
	// Minimal Metadata response: 1 broker (itself), 0 topics
	payload := append(correlationID, 0, 0, 0, 0, 0, 0, 0, 1)
	payload = append(payload, 0, 0, 0, 1) // Broker ID
	host := "kafka"                       // Use the alias in the Docker network
	payload = append(payload, 0, uint8(len(host)))
	payload = append(payload, []byte(host)...)
	payload = append(payload, 0, 0, 0x23, 0x84) // Port 9092
	payload = append(payload, 0, 0, 0, 0)       // Rack (null)
	payload = append(payload, 0, 0, 0, 0)       // Controller ID
	payload = append(payload, 0, 0, 0, 0)       // Num Topics
	i.writeResponse(conn, payload)
}

func (i *Interceptor) sendProduceResponse(conn net.Conn, correlationID []byte) {
	// Success for 1 topic
	// [ThrottleTimeMs, NumTopics, TopicName, NumPartitions, PartitionID, ErrorCode, BaseOffset, LogAppendTimeMs]
	payload := append(correlationID, 0, 0, 0, 0, 0, 0, 0, 1) // 1 topic
	topic := "todo-events"
	payload = append(payload, 0, uint8(len(topic)))
	payload = append(payload, []byte(topic)...)
	payload = append(payload, 0, 0, 0, 1)             // 1 partition
	payload = append(payload, 0, 0, 0, 0)             // Partition 0
	payload = append(payload, 0, 0)                   // ErrorCode 0
	payload = append(payload, 0, 0, 0, 0, 0, 0, 0, 0) // Offset 0
	payload = append(payload, 0, 0, 0, 0, 0, 0, 0, 0) // Timestamp
	i.writeResponse(conn, payload)
}

func (i *Interceptor) sendGenericResponse(conn net.Conn, correlationID []byte) {
	i.writeResponse(conn, correlationID)
}

func (i *Interceptor) writeResponse(conn net.Conn, payload []byte) {
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(payload)))
	conn.Write(lenBuf)
	conn.Write(payload)
}
