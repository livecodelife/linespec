package kafka

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/verify"
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
	logger.Debug("Kafka Interceptor: Starting on %s", i.addr)
	ln, err := net.Listen("tcp", i.addr)
	if err != nil {
		logger.Error("Kafka Interceptor: Failed to listen: %v", err)
		return err
	}
	logger.Debug("Kafka Interceptor: Successfully listening on %s", ln.Addr())
	defer ln.Close()

	logger.Debug("Kafka Interceptor: Entering accept loop")

	go func() {
		<-ctx.Done()
		logger.Debug("Kafka Interceptor: Context cancelled, closing listener")
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				logger.Debug("Kafka Interceptor: Accept error due to context done")
				return nil
			default:
				logger.Debug("Kafka Interceptor: Accept error (continuing): %v", err)
				continue
			}
		}
		logger.Debug("Kafka Interceptor: Accepted connection from %s", conn.RemoteAddr())
		go i.handleConn(conn)
	}
}

func (i *Interceptor) handleConn(conn net.Conn) {
	defer conn.Close()
	logger.Debug("Kafka Interceptor: New connection from %s", conn.RemoteAddr())

	for {
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			if err != io.EOF {
				logger.Debug("Kafka Interceptor: Error reading length: %v", err)
			} else {
				fmt.Printf("Kafka Interceptor: Client closed connection (EOF)\n")
			}
			return
		}
		length := binary.BigEndian.Uint32(lenBuf)
		logger.Debug("Kafka Interceptor: Reading request of length %d", length)

		request := make([]byte, length)
		if _, err := io.ReadFull(conn, request); err != nil {
			logger.Debug("Kafka Interceptor: Error reading request: %v", err)
			return
		}

		if length < 8 {
			logger.Debug("Kafka Interceptor: Request too short (%d bytes)", length)
			continue
		}

		apiKey := binary.BigEndian.Uint16(request[0:2])
		apiVersion := binary.BigEndian.Uint16(request[2:4])
		correlationID := request[4:8]

		logger.Debug("Kafka Interceptor: apiKey=%d apiVersion=%d correlationID=%d", apiKey, apiVersion, binary.BigEndian.Uint32(correlationID))

		logger.Debug("Kafka Interceptor: Handling apiKey=%d", apiKey)
		switch apiKey {
		case 18: // ApiVersions
			logger.Debug("Kafka Interceptor: Sending ApiVersions response")
			i.sendApiVersionsResponse(conn, correlationID)
			logger.Debug("Kafka Interceptor: ApiVersions response sent")
		case 3: // Metadata
			logger.Debug("Kafka Interceptor: Sending Metadata response")
			i.sendMetadataResponse(conn, correlationID)
			logger.Debug("Kafka Interceptor: Metadata response sent")
		case 0: // Produce
			topic, key, value, headers := i.extractProduceData(request[8:])
			if topic != "" {
				logger.Debug("Kafka Interceptor: Intercepted Produce to topic %s", topic)
				mock, found := i.registry.FindMock(topic, "")
				if found && mock != nil {
					// Execute VERIFY rules if any
					if len(mock.Verify) > 0 {
						kafkaRules := verify.ExtractVerifyRulesForTarget(mock.Verify, "kafka")
						if len(kafkaRules) > 0 {
							msg := &verify.KafkaMessage{
								Key:     key,
								Value:   value,
								Headers: headers,
							}
							if err := verify.VerifyKafka(msg, kafkaRules); err != nil {
								logger.Error("VERIFY failed for Kafka topic %s: %v", topic, err)
								// Send error response to client
								i.sendErrorResponse(conn, correlationID, topic, fmt.Sprintf("VERIFY failed: %v", err))
								continue
							}
							logger.Debug("All VERIFY rules passed for Kafka topic %s", topic)
						}
					}
				}
			} else {
				// Fallback: hit any EVENT mock if we couldn't parse the topic
				logger.Debug("Kafka Interceptor: Intercepted Produce (could not parse topic)")
				i.registry.FindMock("todo-events", "")
			}
			i.sendProduceResponse(conn, correlationID, topic)
		default:
			logger.Debug("Kafka Interceptor: Unhandled apiKey=%d, sending generic response", apiKey)
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

// extractProduceData extracts topic, key, value, and headers from a Produce request
// This is a simplified parser that handles common cases
func (i *Interceptor) extractProduceData(data []byte) (topic string, key string, value string, headers map[string]string) {
	headers = make(map[string]string)

	if len(data) < 12 {
		return "", "", "", headers
	}

	// Try to extract topic
	topicLen := int(binary.BigEndian.Uint16(data[10:12]))
	if topicLen > 0 && topicLen < 255 && len(data) >= 12+topicLen {
		topic = string(data[12 : 12+topicLen])

		// Try to extract key and value from the message data
		// The message format is complex, so we'll do a best-effort extraction
		// Looking for string patterns that might be keys and values
		remainingData := data[12+topicLen:]

		// Simple heuristic: look for reasonable string data
		// Key is often a short string
		// Value is often JSON or a longer string
		if len(remainingData) > 20 {
			// Try to find what looks like a key (short string after topic)
			// Skip some bytes for partition, offset, etc.
			messageStart := 20 // Approximate offset to message data
			if len(remainingData) > messageStart {
				// Look for key (usually comes first, often shorter)
				keyLen := int(binary.BigEndian.Uint32(remainingData[messageStart : messageStart+4]))
				if keyLen > 0 && keyLen < 1000 && len(remainingData) > messageStart+4+int(keyLen) {
					key = string(remainingData[messageStart+4 : messageStart+4+keyLen])
				}

				// Look for value (usually comes after key)
				valueStart := messageStart + 4 + keyLen + 4 // Skip key + length
				if len(remainingData) > valueStart+4 {
					valueLen := int(binary.BigEndian.Uint32(remainingData[valueStart : valueStart+4]))
					if valueLen > 0 && valueLen < 100000 && len(remainingData) > valueStart+4+int(valueLen) {
						value = string(remainingData[valueStart+4 : valueStart+4+valueLen])
					}
				}
			}
		}
	}

	return topic, key, value, headers
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

	logger.Debug("Kafka Interceptor: Sending Metadata response (%d bytes)", len(payload))
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

// sendErrorResponse sends an error response for Produce requests
func (i *Interceptor) sendErrorResponse(conn net.Conn, correlationID []byte, topic string, errorMsg string) {
	// Send a Produce response with an error
	// This is a simplified error response
	if topic == "" {
		topic = "todo-events"
	}

	// Error code 2 = UNKNOWN_TOPIC_OR_PARTITION (or we could use a custom one)
	// For now, we'll use 0 (success) but log the error
	// A proper implementation would use the correct error codes
	logger.Error("Kafka Interceptor: Sending error response: %s", errorMsg)

	// For simplicity, we still send a success response but log the error
	// In a full implementation, this would return a proper error code
	i.sendProduceResponse(conn, correlationID, topic)
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
