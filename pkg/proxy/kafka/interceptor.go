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
	fmt.Printf("Kafka Interceptor: Starting on %s\n", i.addr)
	ln, err := net.Listen("tcp", i.addr)
	if err != nil {
		fmt.Printf("Kafka Interceptor: Failed to listen: %v\n", err)
		return err
	}
	fmt.Printf("Kafka Interceptor: Successfully listening on %s\n", ln.Addr())
	defer ln.Close()

	fmt.Printf("Kafka Interceptor: Entering accept loop\n")

	go func() {
		<-ctx.Done()
		fmt.Println("Kafka Interceptor: Context cancelled, closing listener")
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				fmt.Println("Kafka Interceptor: Accept error due to context done")
				return nil
			default:
				fmt.Printf("Kafka Interceptor: Accept error (continuing): %v\n", err)
				continue
			}
		}
		fmt.Printf("Kafka Interceptor: Accepted connection from %s\n", conn.RemoteAddr())
		go i.handleConn(conn)
	}
}

func (i *Interceptor) handleConn(conn net.Conn) {
	defer conn.Close()
	fmt.Printf("Kafka Interceptor: New connection from %s\n", conn.RemoteAddr())

	for {
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			if err != io.EOF {
				fmt.Printf("Kafka Interceptor: Error reading length: %v\n", err)
			} else {
				fmt.Printf("Kafka Interceptor: Client closed connection (EOF)\n")
			}
			return
		}
		length := binary.BigEndian.Uint32(lenBuf)
		fmt.Printf("Kafka Interceptor: Reading request of length %d\n", length)

		request := make([]byte, length)
		if _, err := io.ReadFull(conn, request); err != nil {
			fmt.Printf("Kafka Interceptor: Error reading request: %v\n", err)
			return
		}

		if length < 8 {
			fmt.Printf("Kafka Interceptor: Request too short (%d bytes)\n", length)
			continue
		}

		apiKey := binary.BigEndian.Uint16(request[0:2])
		apiVersion := binary.BigEndian.Uint16(request[2:4])
		correlationID := request[4:8]

		fmt.Printf("Kafka Interceptor: apiKey=%d apiVersion=%d correlationID=%d\n", apiKey, apiVersion, binary.BigEndian.Uint32(correlationID))

		fmt.Printf("Kafka Interceptor: Handling apiKey=%d\n", apiKey)
		switch apiKey {
		case 18: // ApiVersions
			fmt.Println("Kafka Interceptor: Sending ApiVersions response")
			i.sendApiVersionsResponse(conn, correlationID)
			fmt.Println("Kafka Interceptor: ApiVersions response sent")
		case 3: // Metadata
			fmt.Println("Kafka Interceptor: Sending Metadata response")
			i.sendMetadataResponse(conn, correlationID)
			fmt.Println("Kafka Interceptor: Metadata response sent")
		case 0: // Produce
			topic := i.extractProduceTopic(request[8:])
			if topic != "" {
				fmt.Printf("Kafka Interceptor: Intercepted Produce to topic %s\n", topic)
				i.registry.FindMock(topic, "")
			} else {
				// Fallback: hit any EVENT mock if we couldn't parse the topic
				fmt.Println("Kafka Interceptor: Intercepted Produce (could not parse topic)")
				i.registry.FindMock("todo-events", "")
			}
			i.sendProduceResponse(conn, correlationID, topic)
		default:
			fmt.Printf("Kafka Interceptor: Unhandled apiKey=%d, sending generic response\n", apiKey)
			i.sendGenericResponse(conn, correlationID)
		}
	}
}

func (i *Interceptor) extractProduceTopic(data []byte) string {
	if len(data) < 12 {
		return ""
	}
	// Try to find topic name
	// It's usually at offset 10 (int16 length)
	topicLen := int(binary.BigEndian.Uint16(data[10:12]))
	if topicLen > 0 && topicLen < 255 && len(data) >= 12+topicLen {
		return string(data[12 : 12+topicLen])
	}

	// Fallback for different protocol versions
	if len(data) >= 14 {
		topicLen = int(binary.BigEndian.Uint16(data[12:14]))
		if topicLen > 0 && topicLen < 255 && len(data) >= 14+topicLen {
			return string(data[14 : 14+topicLen])
		}
	}
	return ""
}

func (i *Interceptor) sendApiVersionsResponse(conn net.Conn, correlationID []byte) {
	// ApiVersions Response v0:
	// correlation_id + error_code (2) + [api_versions]
	// api_version: api_key (2) + min_version (2) + max_version (2)
	payload := make([]byte, 0, 128)
	payload = append(payload, correlationID...)
	payload = append(payload, 0, 0) // error_code = 0 (NO_ERROR)

	// Array of API versions we support
	apis := []struct {
		key uint16
		min uint16
		max uint16
	}{
		{0, 0, 7},  // Produce
		{3, 0, 9},  // Metadata
		{18, 0, 3}, // ApiVersions
	}

	payload = append(payload, 0, 0, 0, byte(len(apis))) // array length (4 bytes)
	for _, api := range apis {
		payload = append(payload, byte(api.key>>8), byte(api.key))
		payload = append(payload, byte(api.min>>8), byte(api.min))
		payload = append(payload, byte(api.max>>8), byte(api.max))
	}

	i.writeResponse(conn, payload)
}

func (i *Interceptor) sendMetadataResponse(conn net.Conn, correlationID []byte) {
	// Build a proper Metadata Response (Version: 1)
	// https://kafka.apache.org/protocol.html#The_Messages_Metadata
	//
	// Response format:
	//   correlation_id (4 bytes)
	//   throttle_time_ms (4 bytes) - added in v1
	//   brokers[] (4 bytes length + items)
	//     node_id (4 bytes)
	//     host (2 bytes length + string)
	//     port (4 bytes)
	//     rack (2 bytes, -1 for null) - added in v1
	//   topics[] (4 bytes length + items)
	//     error_code (2 bytes)
	//     name (2 bytes length + string)
	//     partitions[] (4 bytes length + items)
	//       error_code (2 bytes)
	//       partition_index (4 bytes)
	//       leader_id (4 bytes)
	//       replica_nodes[] (4 bytes length + items, each 4 bytes)
	//       isr_nodes[] (4 bytes length + items, each 4 bytes)

	payload := make([]byte, 0, 512)

	// correlation_id (4 bytes)
	payload = append(payload, correlationID...)

	// throttle_time_ms (4 bytes) = 0
	payload = append(payload, 0, 0, 0, 0)

	// brokers array length (4 bytes) = 1
	payload = append(payload, 0, 0, 0, 1)

	// Broker 1:
	// node_id = 1
	payload = append(payload, 0, 0, 0, 1)
	// host = "kafka" (2 bytes length + string)
	host := "kafka"
	payload = append(payload, byte(len(host)>>8), byte(len(host)))
	payload = append(payload, []byte(host)...)
	// port = 9092 (4 bytes big-endian)
	payload = append(payload, 0, 0, 0x23, 0x84)
	// rack = null (2 bytes, -1 = 0xFFFF)
	payload = append(payload, 0xFF, 0xFF)

	// topics array length (4 bytes) = 1
	payload = append(payload, 0, 0, 0, 1)

	// Topic: todo-events
	// error_code = 0 (2 bytes)
	payload = append(payload, 0, 0)
	// name = "todo-events" (2 bytes length + string)
	topicName := "todo-events"
	payload = append(payload, byte(len(topicName)>>8), byte(len(topicName)))
	payload = append(payload, []byte(topicName)...)

	// partitions array length (4 bytes) = 1
	payload = append(payload, 0, 0, 0, 1)

	// Partition 0:
	// error_code = 0 (2 bytes)
	payload = append(payload, 0, 0)
	// partition_index = 0 (4 bytes)
	payload = append(payload, 0, 0, 0, 0)
	// leader_id = 1 (4 bytes)
	payload = append(payload, 0, 0, 0, 1)
	// replica_nodes array length (4 bytes) = 1
	payload = append(payload, 0, 0, 0, 1)
	// replica node = 1 (4 bytes)
	payload = append(payload, 0, 0, 0, 1)
	// isr_nodes array length (4 bytes) = 1
	payload = append(payload, 0, 0, 0, 1)
	// isr node = 1 (4 bytes)
	payload = append(payload, 0, 0, 0, 1)

	fmt.Printf("Kafka Interceptor: Sending Metadata response (%d bytes)\n", len(payload))
	i.writeResponse(conn, payload)
}

func (i *Interceptor) sendProduceResponse(conn net.Conn, correlationID []byte, topic string) {
	if topic == "" {
		topic = "todo-events"
	}
	payload := append(correlationID, 0, 0, 0, 0, 0, 0, 0, 1)
	payload = append(payload, 0, uint8(len(topic)))
	payload = append(payload, []byte(topic)...)
	payload = append(payload, 0, 0, 0, 1)
	payload = append(payload, 0, 0, 0, 0)
	payload = append(payload, 0, 0)
	payload = append(payload, 0, 0, 0, 0, 0, 0, 0, 0)
	payload = append(payload, 0, 0, 0, 0, 0, 0, 0, 0)
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
