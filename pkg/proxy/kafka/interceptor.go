package kafka

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"net"
	"sync"
	"time"

	"github.com/livecodelife/linespec/pkg/dsl"
	"github.com/livecodelife/linespec/pkg/interpolate"
	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/verify"
)

type Interceptor struct {
	addr     string
	host     string // hostname advertised in Metadata/FindCoordinator responses
	registry *registry.MockRegistry
	resolver *interpolate.Resolver
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

// SetResolver wires an interpolate.Resolver into the interceptor so that
// ${VAR} tokens in WITH payload files are resolved at runtime.
func (i *Interceptor) SetResolver(resolver *interpolate.Resolver) {
	i.resolver = resolver
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
				resolver := i.resolver
				bodyMatcher := func(withFile, baseDir string) bool {
					if withFile == "" {
						return true
					}
					loader := dsl.NewPayloadLoaderWithResolver(baseDir, resolver)
					expected, err := loader.Load(withFile)
					if err != nil {
						logger.Debug("WITH body match: failed to load %s: %v", withFile, err)
						return false
					}
					var actual interface{}
					if jsonErr := json.Unmarshal([]byte(value), &actual); jsonErr != nil {
						logger.Debug("WITH body match: failed to parse Kafka message value as JSON: %v", jsonErr)
						return false
					}
					return verify.CompareJSON(expected, actual) == nil
				}
				mock, found := i.registry.FindKafkaMockWithBody(topic, bodyMatcher)
				if !found {
					i.registry.RecordPassthrough("Kafka produce topic=" + topic)
				}
				if found && mock != nil && len(mock.Verify) > 0 {
					kafkaRules := verify.ExtractVerifyRulesForTarget(mock.Verify, "kafka")
					if len(kafkaRules) > 0 {
						msg := &verify.KafkaMessage{Key: key, Value: value, Headers: headers}
						if err := verify.VerifyKafka(msg, kafkaRules); err != nil {
							logger.Error("VERIFY failed for Kafka topic %s: %v", topic, err)
							i.registry.RecordVerifyError("EVENT [" + topic + "]: " + err.Error())
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
			logger.Debug("Kafka Interceptor: Metadata request v%d", apiVersion)
			i.sendMetadataResponse(conn, correlationID, apiVersion)

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
			i.sendJoinGroupResponse(conn, correlationID, request, apiVersion)

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

// sendFetchResponse builds a Fetch v1 response (throttle_time_ms, no last_stable_offset).
func (i *Interceptor) sendFetchResponse(conn net.Conn, correlationID []byte, topic string) {
	i.mu.Lock()
	msgs := i.seeds[topic]
	i.mu.Unlock()

	p := make([]byte, 0, 512)
	p = append(p, correlationID...)
	p = appendInt32(p, 0) // throttle_time_ms (v1+)

	// topics array
	p = appendInt32(p, 1)                // 1 topic
	p = appendString(p, topic)           // topic name
	p = appendInt32(p, 1)                // 1 partition response
	p = appendInt32(p, 0)                // partition = 0
	p = appendInt16(p, 0)                // error_code = 0
	p = appendInt64(p, int64(len(msgs))) // high_watermark
	// No last_stable_offset (v4+ only); no aborted_transactions (v4+ only)

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
	// Always return offset 0 so that auto_offset_reset='latest' consumers still
	// start from the beginning of the seeded messages (the fake topic always starts at 0).
	p := append([]byte(nil), correlationID...)
	p = appendInt32(p, 1)  // topics count
	p = appendString(p, topic)
	p = appendInt32(p, 1)  // partitions count
	p = appendInt32(p, 0)  // partition
	p = appendInt16(p, 0)  // error_code
	p = appendInt64(p, -1) // timestamp (-1 = latest sentinel)
	p = appendInt64(p, 0)  // offset 0: consumer starts at the beginning of seeded messages

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

// sendJoinGroupResponse assigns the consumer as a follower (leader = "linespec-leader-0").
// When the consumer is a follower it does NOT attempt to decode member_metadata for partition
// assignment — it just sends SyncGroup immediately. This avoids a buffer-underrun crash in
// aiokafka that occurs when the leader receives 0-byte ConsumerProtocolSubscription metadata.
// The protocol name is parsed from the request so we echo back exactly what the consumer sent.
func (i *Interceptor) sendJoinGroupResponse(conn net.Conn, correlationID, request []byte, apiVersion uint16) {
	protocol := parseJoinGroupProtocol(request, apiVersion)
	logger.Debug("Kafka Interceptor: JoinGroup protocol=%q", protocol)

	p := append([]byte(nil), correlationID...)
	p = appendInt16(p, 0)                    // error_code = 0
	p = appendInt32(p, 1)                    // generation_id = 1
	p = appendString(p, protocol)            // protocol_name (echoed from request)
	p = appendString(p, "linespec-leader-0") // leader (fake ID → consumer is a follower)
	p = appendString(p, memberID)            // member_id assigned to this consumer
	p = appendInt32(p, 0)                    // members array: empty (follower view)

	i.writeResponse(conn, p)
}

// parseJoinGroupProtocol extracts the first protocol name from a JoinGroup request.
// JoinGroup v1 request body (after apiKey, apiVersion, correlationID, clientId):
//   group_id STRING, session_timeout_ms INT32, rebalance_timeout_ms INT32 (v1+),
//   member_id STRING, protocol_type STRING,
//   group_protocols ARRAY of (name STRING, metadata BYTES)
func parseJoinGroupProtocol(request []byte, apiVersion uint16) string {
	// request[0:8] = apiKey(2)+apiVersion(2)+correlationID(4), request[8:] = clientId + body
	data := skipClientID(request[8:])
	if data == nil {
		return "roundrobin"
	}
	// group_id STRING
	data = skipString(data)
	if data == nil {
		return "roundrobin"
	}
	// session_timeout_ms INT32
	if len(data) < 4 {
		return "roundrobin"
	}
	data = data[4:]
	// rebalance_timeout_ms INT32 (v1+)
	if apiVersion >= 1 {
		if len(data) < 4 {
			return "roundrobin"
		}
		data = data[4:]
	}
	// member_id STRING
	data = skipString(data)
	if data == nil {
		return "roundrobin"
	}
	// protocol_type STRING
	data = skipString(data)
	if data == nil {
		return "roundrobin"
	}
	// group_protocols ARRAY count INT32
	if len(data) < 6 {
		return "roundrobin"
	}
	// skip count, read first protocol name
	data = data[4:]
	nameLen := int(int16(binary.BigEndian.Uint16(data[0:2])))
	data = data[2:]
	if nameLen <= 0 || len(data) < nameLen {
		return "roundrobin"
	}
	return string(data[:nameLen])
}

// skipString skips a Kafka STRING (INT16 length + bytes) and returns the remainder.
func skipString(data []byte) []byte {
	if len(data) < 2 {
		return nil
	}
	n := int(int16(binary.BigEndian.Uint16(data[0:2])))
	if n < 0 {
		return data[2:] // null string
	}
	if len(data) < 2+n {
		return nil
	}
	return data[2+n:]
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
		{0, 0, 2},  // Produce
		{1, 1, 1},  // Fetch: v1 only (throttle_time_ms, no last_stable_offset/aborted_txns)
		{2, 0, 0},  // ListOffsets
		{3, 0, 2},  // Metadata: v0-v2 (handled correctly by sendMetadataResponse)
		{8, 0, 1},  // OffsetCommit
		{9, 0, 1},  // OffsetFetch
		{10, 0, 1}, // FindCoordinator
		{11, 0, 1}, // JoinGroup
		{12, 0, 1}, // Heartbeat
		{13, 0, 1}, // LeaveGroup
		{14, 0, 1}, // SyncGroup
		{18, 0, 1}, // ApiVersions
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

// sendMetadataResponse builds a Metadata response matching the requested apiVersion.
//
//	v0:  brokers(no rack) + topics(no is_internal), no throttle/controller/cluster fields
//	v1:  + throttle_time_ms, rack per broker, controller_id, is_internal per topic
//	v2+: + cluster_id (NULLABLE_STRING) between brokers and controller_id
func (i *Interceptor) sendMetadataResponse(conn net.Conn, correlationID []byte, apiVersion uint16) {
	topics := i.getKnownTopics()

	p := make([]byte, 0, 512)
	p = append(p, correlationID...)

	if apiVersion >= 1 {
		p = appendInt32(p, 0) // throttle_time_ms (v1+)
	}

	// brokers array: one broker (self)
	p = appendInt32(p, 1)
	p = appendInt32(p, 1)       // node_id
	p = appendString(p, i.host) // host
	p = appendInt32(p, 9092)    // port
	if apiVersion >= 1 {
		p = appendInt16(p, -1) // rack = null (NULLABLE_STRING, v1+)
	}

	if apiVersion >= 2 {
		p = appendInt16(p, -1) // cluster_id = null (NULLABLE_STRING, v2+)
	}

	if apiVersion >= 1 {
		p = appendInt32(p, 1) // controller_id = 1 (v1+)
	}

	// topics array
	p = appendInt32(p, int32(len(topics)))
	for _, topic := range topics {
		p = appendInt16(p, 0)      // error_code
		p = appendString(p, topic) // topic name
		if apiVersion >= 1 {
			p = append(p, 0) // is_internal = false (BOOL, v1+)
		}
		// partitions: one partition (0)
		p = appendInt32(p, 1) // partitions count
		p = appendInt16(p, 0) // error_code
		p = appendInt32(p, 0) // partition_index
		p = appendInt32(p, 1) // leader_id
		p = appendInt32(p, 1) // replica_nodes count
		p = appendInt32(p, 1) // replica node = 1
		p = appendInt32(p, 1) // isr_nodes count
		p = appendInt32(p, 1) // isr node = 1
	}

	logger.Debug("Kafka Interceptor: Metadata v%d response for topics %v", apiVersion, topics)
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

const maxKafkaFieldSize = 1 << 20 // 1 MiB cap for key/value/header fields

func (i *Interceptor) extractProduceData(data []byte) (topic, key, value string, headers map[string]string) {
	headers = make(map[string]string)

	// Phase A: parse the Produce request envelope.
	data = skipClientID(data)
	if len(data) < 10 {
		return
	}
	// Skip: acks(2) + timeout_ms(4) + topics_count(4) = 10 bytes
	data = data[10:]

	// Read topic name (STRING = INT16 length + bytes).
	if len(data) < 2 {
		return
	}
	topicLen := int(int16(binary.BigEndian.Uint16(data[0:2])))
	if topicLen <= 0 || len(data) < 2+topicLen {
		return
	}
	topic = string(data[2 : 2+topicLen])
	data = data[2+topicLen:]

	// Skip: partitions_count(4) + partition_id(4) = 8 bytes, then read record_set_size(4).
	if len(data) < 12 {
		return
	}
	recordSetSize := int(int32(binary.BigEndian.Uint32(data[8:12])))
	data = data[12:]
	if recordSetSize <= 0 || len(data) < recordSetSize {
		return
	}
	recordSet := data[:recordSetSize]

	// Phase B: detect message format version via magic byte.
	// RecordBatch layout: baseOffset(8) + batchLength(4) + partitionLeaderEpoch(4) + magic(1) = 17 bytes
	if len(recordSet) < 17 {
		return
	}
	magic := recordSet[16]

	switch magic {
	case 2:
		key, value = i.extractRecordBatchV2(recordSet, headers)
	case 0, 1:
		key, value = extractMessageSetV0V1(recordSet, magic)
	default:
		logger.Debug("Kafka Interceptor: unknown record magic byte %d, skipping extraction", magic)
	}
	return
}

// extractRecordBatchV2 parses the first record from a RecordBatch (magic=2).
// Populates headers in-place and returns key, value.
func (i *Interceptor) extractRecordBatchV2(recordSet []byte, headers map[string]string) (key, value string) {
	// Fixed header layout (offsets from start of recordSet):
	//   baseOffset(8) + batchLength(4) + partitionLeaderEpoch(4) + magic(1) + crc(4)
	//   + attributes(2) + lastOffsetDelta(4) + firstTimestamp(8) + maxTimestamp(8)
	//   + producerId(8) + producerEpoch(2) + baseSequence(4) + recordsCount(4) = 61 bytes
	if len(recordSet) < 61 {
		return
	}
	// attributes is at: baseOffset(8) + batchLength(4) + partLeaderEpoch(4) + magic(1) + crc(4) = offset 21
	attrs := int16(binary.BigEndian.Uint16(recordSet[21:23]))
	compression := attrs & 0x07
	if compression != 0 {
		logger.Debug("Kafka Interceptor: compressed RecordBatch v2 (codec=%d), skipping key/value extraction", compression)
		return
	}

	rec := recordSet[61:]

	// Record: length(varint) + attributes(1) + timestampDelta(varint) + offsetDelta(varint)
	//         + keyLength(varint) + key + valueLength(varint) + value
	//         + headersCount(varint) + [headerKey + headerValue ...]
	_, n := decodeZigzagVarint(rec) // record length
	if n <= 0 || len(rec) < n {
		return
	}
	rec = rec[n:]

	if len(rec) < 1 {
		return
	}
	rec = rec[1:] // attributes INT8

	_, n = decodeZigzagVarint(rec) // timestampDelta
	if n <= 0 {
		return
	}
	rec = rec[n:]

	_, n = decodeZigzagVarint(rec) // offsetDelta
	if n <= 0 {
		return
	}
	rec = rec[n:]

	keyLen, n := decodeZigzagVarint(rec)
	if n <= 0 {
		return
	}
	rec = rec[n:]
	if keyLen > 0 && keyLen <= maxKafkaFieldSize {
		if len(rec) < int(keyLen) {
			return
		}
		key = string(rec[:keyLen])
		rec = rec[keyLen:]
	} else if keyLen > 0 {
		if len(rec) < int(keyLen) {
			return
		}
		rec = rec[keyLen:] // skip oversized key
	}
	// keyLen == -1 means null key; rec is already positioned correctly

	valLen, n := decodeZigzagVarint(rec)
	if n <= 0 {
		return
	}
	rec = rec[n:]
	if valLen > 0 && valLen <= maxKafkaFieldSize {
		if len(rec) < int(valLen) {
			return
		}
		value = string(rec[:valLen])
		rec = rec[valLen:]
	} else if valLen > 0 {
		if len(rec) < int(valLen) {
			return
		}
		rec = rec[valLen:] // skip oversized value
	}

	headersCount, n := decodeZigzagVarint(rec)
	if n <= 0 || headersCount <= 0 {
		return
	}
	rec = rec[n:]
	for range headersCount {
		hkLen, n := decodeZigzagVarint(rec)
		if n <= 0 || hkLen < 0 || len(rec) < n+int(hkLen) {
			return
		}
		rec = rec[n:]
		hk := string(rec[:hkLen])
		rec = rec[hkLen:]

		hvLen, n := decodeZigzagVarint(rec)
		if n <= 0 || hvLen < 0 || len(rec) < n+int(hvLen) {
			return
		}
		rec = rec[n:]
		hv := string(rec[:hvLen])
		rec = rec[hvLen:]
		headers[hk] = hv
	}
	return
}

// extractMessageSetV0V1 parses the first message from a MessageSet (magic=0 or 1).
func extractMessageSetV0V1(recordSet []byte, magic byte) (key, value string) {
	// offset(8) + message_size(4) + crc(4) + magic(1) + attributes(1) = 18 bytes
	if len(recordSet) < 18 {
		return
	}
	attrs := recordSet[17]
	compression := attrs & 0x07
	if compression != 0 {
		logger.Debug("Kafka Interceptor: compressed MessageSet v%d (codec=%d), skipping key/value extraction", magic, compression)
		return
	}
	pos := 18
	if magic == 1 {
		pos += 8 // skip timestamp(8)
	}
	if len(recordSet) < pos+4 {
		return
	}
	keyLen := int(int32(binary.BigEndian.Uint32(recordSet[pos : pos+4])))
	pos += 4
	if keyLen > 0 && keyLen <= maxKafkaFieldSize {
		if len(recordSet) < pos+keyLen {
			return
		}
		key = string(recordSet[pos : pos+keyLen])
		pos += keyLen
	} else if keyLen > 0 {
		pos += keyLen // skip oversized key
	}
	// keyLen == -1 means null key

	if len(recordSet) < pos+4 {
		return
	}
	valLen := int(int32(binary.BigEndian.Uint32(recordSet[pos : pos+4])))
	pos += 4
	if valLen > 0 && valLen <= maxKafkaFieldSize {
		if len(recordSet) < pos+valLen {
			return
		}
		value = string(recordSet[pos : pos+valLen])
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

// decodeZigzagVarint decodes a zigzag+uvarint encoded int64 from data.
// Returns the decoded value and the number of bytes consumed (0 on failure).
func decodeZigzagVarint(data []byte) (int64, int) {
	uv, n := binary.Uvarint(data)
	if n <= 0 {
		return 0, 0
	}
	return int64((uv >> 1) ^ -(uv & 1)), n
}

func (i *Interceptor) writeResponse(conn net.Conn, payload []byte) {
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(payload)))
	conn.Write(lenBuf)
	conn.Write(payload)
}

