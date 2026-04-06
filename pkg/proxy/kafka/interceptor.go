package kafka

import (
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"net"
	"sync"
	"time"

	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/verify"
)

type Interceptor struct {
	addr     string
	host     string // hostname advertised in Metadata/FindCoordinator responses
	registry *registry.MockRegistry
	seeds    map[string][][]byte // topic -> ordered list of raw message payloads
	mu       sync.Mutex
}

func NewInterceptor(addr string, reg *registry.MockRegistry) *Interceptor {
	return &Interceptor{
		addr:     addr,
		host:     "kafka",
		registry: reg,
		seeds:    reg.GetSeeds(),
	}
}

// SetHost overrides the hostname advertised to Kafka clients in Metadata and
// FindCoordinator responses. Defaults to "kafka".
func (i *Interceptor) SetHost(host string) {
	i.host = host
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

	for {
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			if err != io.EOF {
				logger.Debug("Kafka Interceptor: Error reading length: %v", err)
			}
			return
		}
		length := binary.BigEndian.Uint32(lenBuf)

		request := make([]byte, length)
		if _, err := io.ReadFull(conn, request); err != nil {
			logger.Debug("Kafka Interceptor: Error reading request: %v", err)
			return
		}

		if length < 8 {
			continue
		}

		apiKey := binary.BigEndian.Uint16(request[0:2])
		apiVersion := binary.BigEndian.Uint16(request[2:4])
		correlationID := request[4:8]

		logger.Debug("Kafka Interceptor: apiKey=%d apiVersion=%d", apiKey, apiVersion)

		switch apiKey {
		case 0: // Produce
			topic, key, value, headers := i.extractProduceData(request[8:])
			if topic != "" {
				logger.Debug("Kafka Interceptor: Produce to topic %s", topic)
				i.registry.CheckNegativeMocks(topic, "")
				mock, found := i.registry.FindMock(topic, "")
				if found && mock != nil && len(mock.Verify) > 0 {
					kafkaRules := verify.ExtractVerifyRulesForTarget(mock.Verify, "kafka")
					if len(kafkaRules) > 0 {
						msg := &verify.KafkaMessage{Key: key, Value: value, Headers: headers}
						if err := verify.VerifyKafka(msg, kafkaRules); err != nil {
							logger.Error("VERIFY failed for Kafka topic %s: %v", topic, err)
							i.sendProduceResponse(conn, correlationID, topic)
							continue
						}
					}
				}
			}
			i.sendProduceResponse(conn, correlationID, topic)

		case 1: // Fetch
			topic := parseFetchTopic(request, apiVersion)
			if topic == "" {
				// Fall back to first seeded topic
				i.mu.Lock()
				for t := range i.seeds {
					topic = t
					break
				}
				i.mu.Unlock()
			}
			logger.Debug("Kafka Interceptor: Fetch for topic %q", topic)
			i.sendFetchResponse(conn, correlationID, topic)

		case 2: // ListOffsets
			topic := parseListOffsetsTopic(request)
			logger.Debug("Kafka Interceptor: ListOffsets for topic %q", topic)
			i.sendListOffsetsResponse(conn, correlationID, topic)

		case 3: // Metadata
			logger.Debug("Kafka Interceptor: Metadata request")
			i.sendMetadataResponse(conn, correlationID)

		case 8: // OffsetCommit
			topic := parseOffsetCommitTopic(request)
			logger.Debug("Kafka Interceptor: OffsetCommit for topic %q", topic)
			i.sendOffsetCommitResponse(conn, correlationID, topic)

		case 9: // OffsetFetch
			topic, partition := parseOffsetFetchRequest(request)
			logger.Debug("Kafka Interceptor: OffsetFetch for topic %q partition %d", topic, partition)
			i.sendOffsetFetchResponse(conn, correlationID, topic, partition)

		case 10: // FindCoordinator
			logger.Debug("Kafka Interceptor: FindCoordinator")
			i.sendFindCoordinatorResponse(conn, correlationID)

		case 11: // JoinGroup
			logger.Debug("Kafka Interceptor: JoinGroup")
			i.sendJoinGroupResponse(conn, correlationID)

		case 12: // Heartbeat
			logger.Debug("Kafka Interceptor: Heartbeat")
			i.sendHeartbeatResponse(conn, correlationID)

		case 13: // LeaveGroup
			logger.Debug("Kafka Interceptor: LeaveGroup")
			i.sendLeaveGroupResponse(conn, correlationID)

		case 14: // SyncGroup
			logger.Debug("Kafka Interceptor: SyncGroup")
			i.sendSyncGroupResponse(conn, correlationID)

		case 18: // ApiVersions
			logger.Debug("Kafka Interceptor: ApiVersions")
			i.sendApiVersionsResponse(conn, correlationID)

		default:
			logger.Debug("Kafka Interceptor: Unhandled apiKey=%d, sending generic response", apiKey)
			i.sendGenericResponse(conn, correlationID)
		}
	}
}

// ── Fetch response ───────────────────────────────────────────────────────────

func (i *Interceptor) sendFetchResponse(conn net.Conn, correlationID []byte, topic string) {
	i.mu.Lock()
	msgs := i.seeds[topic]
	i.mu.Unlock()

	p := make([]byte, 0, 512)
	p = append(p, correlationID...)
	p = appendInt32(p, 0) // throttle_time_ms

	// topics array
	p = appendInt32(p, 1)          // 1 topic
	p = appendString(p, topic)     // topic name
	p = appendInt32(p, 1)          // 1 partition response
	p = appendInt32(p, 0)          // partition = 0
	p = appendInt16(p, 0)          // error_code = 0
	p = appendInt64(p, int64(len(msgs))) // high_watermark
	p = appendInt64(p, int64(len(msgs))) // last_stable_offset (v4+)
	p = appendInt32(p, 0)          // aborted_transactions array count = 0

	if len(msgs) == 0 {
		p = appendInt32(p, -1) // null records
	} else {
		batch := buildRecordBatch(msgs, 0)
		p = appendInt32(p, int32(len(batch)))
		p = append(p, batch...)
	}

	i.writeResponse(conn, p)
}

// buildRecordBatch encodes msgs as a Kafka RecordBatch (message format v2).
func buildRecordBatch(msgs [][]byte, baseOffset int64) []byte {
	now := time.Now().UnixMilli()

	// Build each Record.
	var recordsData []byte
	for idx, value := range msgs {
		recordsData = append(recordsData, buildRecord(idx, value)...)
	}

	// Batch body (everything that CRC covers: from attributes onwards).
	var body []byte
	body = appendInt16(body, 0)                   // attributes
	body = appendInt32(body, int32(len(msgs)-1))  // lastOffsetDelta
	body = appendInt64(body, now)                  // baseTimestamp
	body = appendInt64(body, now)                  // maxTimestamp
	body = appendInt64(body, -1)                   // producerId
	body = appendInt16(body, -1)                   // producerEpoch
	body = appendInt32(body, -1)                   // baseSequence
	body = appendInt32(body, int32(len(msgs)))     // numRecords
	body = append(body, recordsData...)

	crc := crc32.Checksum(body, crc32.MakeTable(crc32.Castagnoli))

	// Header: partitionLeaderEpoch + magic + crc + body.
	var header []byte
	header = appendInt32(header, 0) // partitionLeaderEpoch
	header = append(header, 2)       // magic = 2
	header = appendInt32(header, int32(crc))
	header = append(header, body...)

	// Outer: baseOffset + batchLength + header.
	var result []byte
	result = appendInt64(result, baseOffset)
	result = appendInt32(result, int32(len(header)))
	result = append(result, header...)
	return result
}

// buildRecord encodes a single Kafka Record (varint/zigzag encoded).
func buildRecord(offsetDelta int, value []byte) []byte {
	var body []byte
	body = append(body, 0)                                    // attributes INT8
	body = append(body, zigzagVarint(0)...)                   // timestampDelta
	body = append(body, zigzagVarint(int64(offsetDelta))...)  // offsetDelta
	body = append(body, zigzagVarint(-1)...)                  // keyLength = -1 (null)
	body = append(body, zigzagVarint(int64(len(value)))...)   // valueLen
	body = append(body, value...)
	body = append(body, zigzagVarint(0)...) // numHeaders = 0

	result := zigzagVarint(int64(len(body)))
	return append(result, body...)
}

// parseFetchTopic extracts the first topic name from a Fetch request.
func parseFetchTopic(request []byte, apiVersion uint16) string {
	if len(request) < 10 {
		return ""
	}
	data := skipClientID(request[8:])
	if data == nil {
		return ""
	}

	// Fetch body:
	//   v0-v2: replica_id(4) + max_wait_ms(4) + min_bytes(4) + topics_count(4) + ...
	//   v3:    + max_bytes(4)
	//   v4:    + max_bytes(4) + isolation_level(1)
	offset := 4 + 4 + 4 // replica_id, max_wait_ms, min_bytes
	if apiVersion >= 3 {
		offset += 4 // max_bytes
	}
	if apiVersion >= 4 {
		offset++ // isolation_level
	}
	offset += 4 // topics array count

	if len(data) < offset+2 {
		return ""
	}
	topicLen := int(int16(binary.BigEndian.Uint16(data[offset : offset+2])))
	offset += 2
	if topicLen <= 0 || len(data) < offset+topicLen {
		return ""
	}
	return string(data[offset : offset+topicLen])
}

// ── ListOffsets ──────────────────────────────────────────────────────────────

func (i *Interceptor) sendListOffsetsResponse(conn net.Conn, correlationID []byte, topic string) {
	i.mu.Lock()
	count := int64(len(i.seeds[topic]))
	i.mu.Unlock()

	p := append([]byte(nil), correlationID...)
	p = appendInt32(p, 1)      // topics count
	p = appendString(p, topic)
	p = appendInt32(p, 1)      // partitions count
	p = appendInt32(p, 0)      // partition
	p = appendInt16(p, 0)      // error_code
	p = appendInt64(p, -1)     // timestamp (earliest sentinel)
	p = appendInt64(p, count)  // offset (number of messages available)

	i.writeResponse(conn, p)
}

func parseListOffsetsTopic(request []byte) string {
	if len(request) < 10 {
		return ""
	}
	data := skipClientID(request[8:])
	if len(data) < 10 {
		return ""
	}
	// replica_id(4) + topics_count(4)
	offset := 8
	if len(data) < offset+2 {
		return ""
	}
	topicLen := int(int16(binary.BigEndian.Uint16(data[offset : offset+2])))
	offset += 2
	if topicLen <= 0 || len(data) < offset+topicLen {
		return ""
	}
	return string(data[offset : offset+topicLen])
}

// ── OffsetCommit ─────────────────────────────────────────────────────────────

func (i *Interceptor) sendOffsetCommitResponse(conn net.Conn, correlationID []byte, topic string) {
	if topic == "" {
		topic = i.firstSeededTopic()
	}
	p := append([]byte(nil), correlationID...)
	p = appendInt32(p, 1)      // topics count
	p = appendString(p, topic)
	p = appendInt32(p, 1)      // partitions count
	p = appendInt32(p, 0)      // partition
	p = appendInt16(p, 0)      // error_code

	i.writeResponse(conn, p)
}

func parseOffsetCommitTopic(request []byte) string {
	if len(request) < 10 {
		return ""
	}
	data := skipClientID(request[8:])
	if len(data) < 6 {
		return ""
	}
	// Skip group_id (STRING: INT16 len + bytes), then topics count, then topic name.
	groupLen := int(int16(binary.BigEndian.Uint16(data[0:2])))
	offset := 2
	if groupLen > 0 {
		offset += groupLen
	}
	// topics array count (INT32)
	if len(data) < offset+6 {
		return ""
	}
	offset += 4
	topicLen := int(int16(binary.BigEndian.Uint16(data[offset : offset+2])))
	offset += 2
	if topicLen <= 0 || len(data) < offset+topicLen {
		return ""
	}
	return string(data[offset : offset+topicLen])
}

// ── OffsetFetch ──────────────────────────────────────────────────────────────

func (i *Interceptor) sendOffsetFetchResponse(conn net.Conn, correlationID []byte, topic string, partition int32) {
	if topic == "" {
		topic = i.firstSeededTopic()
	}
	p := append([]byte(nil), correlationID...)
	p = appendInt32(p, 1)      // topics count
	p = appendString(p, topic)
	p = appendInt32(p, 1)      // partitions count
	p = appendInt32(p, partition) // partition
	p = appendInt64(p, -1)     // offset = -1 (no committed offset → consume from beginning)
	p = appendString(p, "")    // metadata
	p = appendInt16(p, 0)      // error_code

	i.writeResponse(conn, p)
}

func parseOffsetFetchRequest(request []byte) (topic string, partition int32) {
	if len(request) < 10 {
		return "", 0
	}
	data := skipClientID(request[8:])
	if len(data) < 4 {
		return "", 0
	}
	// group_id STRING
	groupLen := int(int16(binary.BigEndian.Uint16(data[0:2])))
	offset := 2
	if groupLen > 0 {
		offset += groupLen
	}
	// topics array count
	if len(data) < offset+6 {
		return "", 0
	}
	offset += 4 // skip count
	topicLen := int(int16(binary.BigEndian.Uint16(data[offset : offset+2])))
	offset += 2
	if topicLen <= 0 || len(data) < offset+topicLen {
		return "", 0
	}
	topic = string(data[offset : offset+topicLen])
	return topic, 0
}

// ── FindCoordinator ──────────────────────────────────────────────────────────

func (i *Interceptor) sendFindCoordinatorResponse(conn net.Conn, correlationID []byte) {
	p := append([]byte(nil), correlationID...)
	p = appendInt16(p, 0)       // error_code = 0
	p = appendInt32(p, 1)       // node_id = 1 (self)
	p = appendString(p, i.host) // host (matches container alias)
	p = appendInt32(p, 9092)    // port

	i.writeResponse(conn, p)
}

// ── JoinGroup ────────────────────────────────────────────────────────────────

const memberID = "linespec-consumer-1"

func (i *Interceptor) sendJoinGroupResponse(conn net.Conn, correlationID []byte) {
	p := append([]byte(nil), correlationID...)
	p = appendInt16(p, 0)           // error_code = 0
	p = appendInt32(p, 1)           // generation_id = 1
	p = appendString(p, "range")    // protocol_name
	p = appendString(p, memberID)   // leader
	p = appendString(p, memberID)   // member_id (assigned to this consumer)
	// members array: one entry (we are both the leader and the only member)
	p = appendInt32(p, 1)           // members count
	p = appendString(p, memberID)   // member_id
	// member metadata: empty bytes
	p = appendInt32(p, 0)           // metadata length = 0

	i.writeResponse(conn, p)
}

// ── SyncGroup ────────────────────────────────────────────────────────────────

func (i *Interceptor) sendSyncGroupResponse(conn net.Conn, correlationID []byte) {
	assignment := i.buildPartitionAssignment()

	p := append([]byte(nil), correlationID...)
	p = appendInt16(p, 0) // error_code = 0
	p = appendBytes(p, assignment)

	i.writeResponse(conn, p)
}

// buildPartitionAssignment encodes a MemberAssignment for partition 0 of all seeded topics.
func (i *Interceptor) buildPartitionAssignment() []byte {
	i.mu.Lock()
	topics := make([]string, 0, len(i.seeds))
	for t := range i.seeds {
		topics = append(topics, t)
	}
	i.mu.Unlock()

	// MemberAssignment wire format:
	//   version: INT16
	//   partitions: ARRAY
	//     topic: STRING
	//     partition_ids: ARRAY<INT32>
	//   user_data: BYTES (-1 for null)
	var a []byte
	a = appendInt16(a, 1) // version
	a = appendInt32(a, int32(len(topics)))
	for _, t := range topics {
		a = appendString(a, t)
		a = appendInt32(a, 1)  // 1 partition
		a = appendInt32(a, 0)  // partition 0
	}
	a = appendInt32(a, -1) // null user_data

	return a
}

// ── Heartbeat ────────────────────────────────────────────────────────────────

func (i *Interceptor) sendHeartbeatResponse(conn net.Conn, correlationID []byte) {
	p := append([]byte(nil), correlationID...)
	p = appendInt16(p, 0) // error_code = 0
	i.writeResponse(conn, p)
}

// ── LeaveGroup ───────────────────────────────────────────────────────────────

func (i *Interceptor) sendLeaveGroupResponse(conn net.Conn, correlationID []byte) {
	p := append([]byte(nil), correlationID...)
	p = appendInt16(p, 0) // error_code = 0
	i.writeResponse(conn, p)
}

// ── ApiVersions ──────────────────────────────────────────────────────────────

func (i *Interceptor) sendApiVersionsResponse(conn net.Conn, correlationID []byte) {
	p := make([]byte, 0, 128)
	p = append(p, correlationID...)
	p = appendInt16(p, 0) // error_code = 0

	apis := []struct{ key, min, max uint16 }{
		{0, 0, 5},  // Produce
		{1, 0, 4},  // Fetch
		{2, 0, 1},  // ListOffsets
		{3, 0, 4},  // Metadata
		{8, 0, 2},  // OffsetCommit
		{9, 0, 3},  // OffsetFetch
		{10, 0, 2}, // FindCoordinator
		{11, 0, 3}, // JoinGroup
		{12, 0, 2}, // Heartbeat
		{13, 0, 2}, // LeaveGroup
		{14, 0, 2}, // SyncGroup
		{18, 0, 2}, // ApiVersions
	}

	p = appendInt32(p, int32(len(apis)))
	for _, api := range apis {
		p = appendInt16(p, int16(api.key))
		p = appendInt16(p, int16(api.min))
		p = appendInt16(p, int16(api.max))
	}

	i.writeResponse(conn, p)
}

// ── Metadata ─────────────────────────────────────────────────────────────────

func (i *Interceptor) sendMetadataResponse(conn net.Conn, correlationID []byte) {
	topics := i.getKnownTopics()

	p := make([]byte, 0, 512)
	p = append(p, correlationID...)
	p = appendInt32(p, 0) // throttle_time_ms

	// brokers array: one broker (self)
	p = appendInt32(p, 1)
	p = appendInt32(p, 1)        // node_id
	p = appendString(p, i.host)  // host (matches container alias so clients reconnect to us)
	p = appendInt32(p, 9092)     // port
	p = appendInt16(p, -1)       // rack = null

	// topics array
	p = appendInt32(p, int32(len(topics)))
	for _, topic := range topics {
		p = appendInt16(p, 0)       // error_code
		p = appendString(p, topic)
		// partitions: one partition (0)
		p = appendInt32(p, 1)       // partitions count
		p = appendInt16(p, 0)       // error_code
		p = appendInt32(p, 0)       // partition_index
		p = appendInt32(p, 1)       // leader_id
		p = appendInt32(p, 1)       // replica_nodes count
		p = appendInt32(p, 1)       // replica node = 1
		p = appendInt32(p, 1)       // isr_nodes count
		p = appendInt32(p, 1)       // isr node = 1
	}

	logger.Debug("Kafka Interceptor: Metadata response for topics %v", topics)
	i.writeResponse(conn, p)
}

// getKnownTopics returns a deduplicated list of all topics from seeds and registry mocks.
func (i *Interceptor) getKnownTopics() []string {
	seen := make(map[string]struct{})

	i.mu.Lock()
	for t := range i.seeds {
		seen[t] = struct{}{}
	}
	i.mu.Unlock()

	// Include topics from EVENT channel mocks in the registry.
	for _, topic := range i.registry.GetEventTopics() {
		seen[topic] = struct{}{}
	}

	// Always include the default fallback topic if nothing else is known.
	if len(seen) == 0 {
		seen["todo-events"] = struct{}{}
	}

	topics := make([]string, 0, len(seen))
	for t := range seen {
		topics = append(topics, t)
	}
	return topics
}

// ── Produce ───────────────────────────────────────────────────────────────────

func (i *Interceptor) sendProduceResponse(conn net.Conn, correlationID []byte, topic string) {
	if topic == "" {
		topic = i.firstSeededTopic()
	}
	p := append([]byte(nil), correlationID...)
	p = appendInt32(p, 0) // throttle_time_ms
	p = appendInt32(p, 1) // topics count
	p = appendString(p, topic)
	p = appendInt32(p, 1) // partition responses count
	p = appendInt32(p, 0) // partition
	p = appendInt16(p, 0) // error_code
	p = appendInt64(p, 0) // base_offset
	p = appendInt64(p, -1) // log_append_time (v1+)
	p = appendInt64(p, -1) // log_start_offset (v5+)

	i.writeResponse(conn, p)
}

func (i *Interceptor) sendGenericResponse(conn net.Conn, correlationID []byte) {
	i.writeResponse(conn, correlationID)
}

// ── Produce parsing ───────────────────────────────────────────────────────────

func (i *Interceptor) extractProduceData(data []byte) (topic, key, value string, headers map[string]string) {
	headers = make(map[string]string)
	if len(data) < 12 {
		return
	}

	topicLen := int(binary.BigEndian.Uint16(data[10:12]))
	if topicLen <= 0 || topicLen >= 255 || len(data) < 12+topicLen {
		return
	}
	topic = string(data[12 : 12+topicLen])
	remaining := data[12+topicLen:]

	if len(remaining) <= 20 {
		return
	}
	messageStart := 20
	keyLen := int(binary.BigEndian.Uint32(remaining[messageStart : messageStart+4]))
	if keyLen > 0 && keyLen < 1000 && len(remaining) > messageStart+4+keyLen {
		key = string(remaining[messageStart+4 : messageStart+4+keyLen])
	}
	valueStart := messageStart + 4 + keyLen + 4
	if len(remaining) > valueStart+4 {
		valueLen := int(binary.BigEndian.Uint32(remaining[valueStart : valueStart+4]))
		if valueLen > 0 && valueLen < 100000 && len(remaining) > valueStart+4+valueLen {
			value = string(remaining[valueStart+4 : valueStart+4+valueLen])
		}
	}
	return
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (i *Interceptor) firstSeededTopic() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	for t := range i.seeds {
		return t
	}
	return "todo-events"
}

// skipClientID skips the clientId STRING at the start of data and returns the remainder.
// Returns nil if data is too short.
func skipClientID(data []byte) []byte {
	if len(data) < 2 {
		return nil
	}
	clientIDLen := int(int16(binary.BigEndian.Uint16(data[0:2])))
	if clientIDLen < 0 {
		return data[2:] // null clientId
	}
	if len(data) < 2+clientIDLen {
		return nil
	}
	return data[2+clientIDLen:]
}

func appendInt16(b []byte, v int16) []byte {
	return append(b, byte(v>>8), byte(v))
}

func appendInt32(b []byte, v int32) []byte {
	return append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func appendInt64(b []byte, v int64) []byte {
	return append(b,
		byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v),
	)
}

func appendString(b []byte, s string) []byte {
	b = appendInt16(b, int16(len(s)))
	return append(b, []byte(s)...)
}

func appendBytes(b []byte, data []byte) []byte {
	b = appendInt32(b, int32(len(data)))
	return append(b, data...)
}

// zigzagVarint encodes v using zigzag + unsigned varint encoding (Kafka record format).
func zigzagVarint(v int64) []byte {
	uv := uint64((v << 1) ^ (v >> 63))
	var buf [10]byte
	n := binary.PutUvarint(buf[:], uv)
	return buf[:n]
}

func (i *Interceptor) writeResponse(conn net.Conn, payload []byte) {
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(payload)))
	conn.Write(lenBuf)
	conn.Write(payload)
}

