package kafka

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/registry"
)

func TestBuildRecord(t *testing.T) {
	value := []byte(`{"id":1,"title":"buy milk"}`)
	rec := buildRecord(0, value)

	if len(rec) == 0 {
		t.Fatal("buildRecord returned empty result")
	}

	// First byte is the zigzag varint length — must be positive.
	if rec[0] == 0 {
		t.Error("Record length varint should not be zero")
	}
}

func TestBuildRecordBatch_SingleMessage(t *testing.T) {
	msgs := [][]byte{[]byte(`{"id":1}`)}
	batch := buildRecordBatch(msgs, 0)

	// baseOffset (8) + batchLength (4) + partitionLeaderEpoch (4) + magic (1) + crc (4) + ...
	if len(batch) < 17 {
		t.Fatalf("RecordBatch too short: %d bytes", len(batch))
	}

	baseOffset := binary.BigEndian.Uint64(batch[0:8])
	if baseOffset != 0 {
		t.Errorf("Expected baseOffset=0, got %d", baseOffset)
	}

	magic := batch[16] // offset 8(base)+4(len)+4(epoch) = 16
	if magic != 2 {
		t.Errorf("Expected magic=2 (message format v2), got %d", magic)
	}
}

func TestBuildRecordBatch_MultipleMessages(t *testing.T) {
	msgs := [][]byte{
		[]byte(`{"id":1}`),
		[]byte(`{"id":2}`),
		[]byte(`{"id":3}`),
	}
	batch := buildRecordBatch(msgs, 5)

	baseOffset := binary.BigEndian.Uint64(batch[0:8])
	if baseOffset != 5 {
		t.Errorf("Expected baseOffset=5, got %d", baseOffset)
	}
}

func TestZigzagVarint(t *testing.T) {
	cases := []struct {
		v   int64
		enc []byte
	}{
		{0, []byte{0x00}},
		{-1, []byte{0x01}},
		{1, []byte{0x02}},
		{-2, []byte{0x03}},
	}
	for _, c := range cases {
		got := zigzagVarint(c.v)
		if len(got) != len(c.enc) {
			t.Errorf("zigzagVarint(%d): got len %d, want %d", c.v, len(got), len(c.enc))
			continue
		}
		for i, b := range c.enc {
			if got[i] != b {
				t.Errorf("zigzagVarint(%d)[%d]: got %02x, want %02x", c.v, i, got[i], b)
			}
		}
	}
}

func TestInterceptorSeedFromRegistry(t *testing.T) {
	reg := registry.NewMockRegistry()
	reg.SeedTopic("my-topic", []byte(`{"event":"created"}`))

	interceptor := NewInterceptor("localhost:0", reg)

	interceptor.mu.Lock()
	msgs := interceptor.seeds["my-topic"]
	interceptor.mu.Unlock()

	if len(msgs) != 1 {
		t.Fatalf("Expected 1 seeded message, got %d", len(msgs))
	}
	if string(msgs[0]) != `{"event":"created"}` {
		t.Errorf("Unexpected seed value: %s", msgs[0])
	}
}

func TestInterceptorSetHost(t *testing.T) {
	reg := registry.NewMockRegistry()
	interceptor := NewInterceptor("localhost:0", reg)

	if interceptor.host != "kafka" {
		t.Errorf("Default host should be 'kafka', got %q", interceptor.host)
	}

	interceptor.SetHost("kafka-proxy")
	if interceptor.host != "kafka-proxy" {
		t.Errorf("Expected host 'kafka-proxy', got %q", interceptor.host)
	}
}

func TestBuildPartitionAssignment(t *testing.T) {
	reg := registry.NewMockRegistry()
	reg.SeedTopic("orders", []byte(`{"id":1}`))
	reg.SeedTopic("events", []byte(`{"id":2}`))

	interceptor := NewInterceptor("localhost:0", reg)
	assignment := interceptor.buildPartitionAssignment()

	if len(assignment) == 0 {
		t.Fatal("buildPartitionAssignment returned empty bytes")
	}

	// version INT16 = 1
	version := int16(binary.BigEndian.Uint16(assignment[0:2]))
	if version != 1 {
		t.Errorf("Expected assignment version=1, got %d", version)
	}

	// topics count INT32 = 2 (orders + events)
	count := int32(binary.BigEndian.Uint32(assignment[2:6]))
	if count != 2 {
		t.Errorf("Expected 2 topics in assignment, got %d", count)
	}
}

func TestSkipClientID_Null(t *testing.T) {
	// -1 (0xFF 0xFF) means null clientId
	data := []byte{0xFF, 0xFF, 0x01, 0x02, 0x03}
	result := skipClientID(data)
	if len(result) != 3 {
		t.Errorf("Expected 3 bytes remaining after null clientId, got %d", len(result))
	}
}

func TestSkipClientID_WithValue(t *testing.T) {
	// clientId "hi" (length=2, bytes="hi")
	data := []byte{0x00, 0x02, 'h', 'i', 0x01, 0x02}
	result := skipClientID(data)
	if len(result) != 2 {
		t.Errorf("Expected 2 bytes remaining after clientId 'hi', got %d", len(result))
	}
}

func TestAppendHelpers(t *testing.T) {
	var b []byte
	b = appendInt16(b, 0x1234)
	if b[0] != 0x12 || b[1] != 0x34 {
		t.Errorf("appendInt16 wrong: %x", b)
	}

	b = b[:0]
	b = appendInt32(b, 0x12345678)
	if b[0] != 0x12 || b[1] != 0x34 || b[2] != 0x56 || b[3] != 0x78 {
		t.Errorf("appendInt32 wrong: %x", b)
	}

	b = b[:0]
	b = appendString(b, "hi")
	if b[0] != 0x00 || b[1] != 0x02 || b[2] != 'h' || b[3] != 'i' {
		t.Errorf("appendString wrong: %x", b)
	}
}

// ── Test helpers ──────────────────────────────────────────────────────────────

// buildTestProduceRequest constructs the bytes that extractProduceData receives
// (i.e. request[8:], starting at the clientID field).
func buildTestProduceRequest(clientID, topic string, recordSet []byte) []byte {
	var b []byte
	// clientID STRING
	b = appendString(b, clientID)
	// acks(INT16) + timeout_ms(INT32) + topics_count(INT32)
	b = appendInt16(b, 1)
	b = appendInt32(b, 1500)
	b = appendInt32(b, 1)
	// topic STRING
	b = appendString(b, topic)
	// partitions_count(INT32) + partition_id(INT32)
	b = appendInt32(b, 1)
	b = appendInt32(b, 0)
	// record_set_size(INT32) + record_set
	b = appendInt32(b, int32(len(recordSet)))
	b = append(b, recordSet...)
	return b
}

// buildTestProduceRequestNullClientID is like buildTestProduceRequest but with
// a null clientID (INT16 = -1).
func buildTestProduceRequestNullClientID(topic string, recordSet []byte) []byte {
	var b []byte
	// null clientID: INT16 = -1
	b = appendInt16(b, -1)
	b = appendInt16(b, 1)
	b = appendInt32(b, 1500)
	b = appendInt32(b, 1)
	b = appendString(b, topic)
	b = appendInt32(b, 1)
	b = appendInt32(b, 0)
	b = appendInt32(b, int32(len(recordSet)))
	b = append(b, recordSet...)
	return b
}

// buildTestRecordV2 builds a RecordBatch v2 containing a single record with
// the given key, value, and headers.
func buildTestRecordV2(key, value []byte, headers map[string]string) []byte {
	// Build the inner record body (without the leading length varint).
	var body []byte
	body = append(body, 0) // attributes INT8
	body = append(body, zigzagVarint(0)...)
	body = append(body, zigzagVarint(0)...)
	if key == nil {
		body = append(body, zigzagVarint(-1)...)
	} else {
		body = append(body, zigzagVarint(int64(len(key)))...)
		body = append(body, key...)
	}
	if value == nil {
		body = append(body, zigzagVarint(-1)...)
	} else {
		body = append(body, zigzagVarint(int64(len(value)))...)
		body = append(body, value...)
	}
	body = append(body, zigzagVarint(int64(len(headers)))...)
	// Iterate headers in deterministic order for tests.
	for k, v := range headers {
		body = append(body, zigzagVarint(int64(len(k)))...)
		body = append(body, []byte(k)...)
		body = append(body, zigzagVarint(int64(len(v)))...)
		body = append(body, []byte(v)...)
	}
	record := zigzagVarint(int64(len(body)))
	record = append(record, body...)

	// Wrap in a RecordBatch v2 using buildRecordBatch, but we need key support.
	// Build it manually so we can embed our custom record bytes.
	var batchBody []byte
	batchBody = appendInt16(batchBody, 0)                    // attributes (no compression)
	batchBody = appendInt32(batchBody, 0)                    // lastOffsetDelta
	batchBody = appendInt64(batchBody, 0)                    // baseTimestamp
	batchBody = appendInt64(batchBody, 0)                    // maxTimestamp
	batchBody = appendInt64(batchBody, -1)                   // producerId
	batchBody = appendInt16(batchBody, -1)                   // producerEpoch
	batchBody = appendInt32(batchBody, -1)                   // baseSequence
	batchBody = appendInt32(batchBody, 1)                    // numRecords = 1
	batchBody = append(batchBody, record...)

	crc := crc32Castagnoli(batchBody)

	var header []byte
	header = appendInt32(header, 0)        // partitionLeaderEpoch
	header = append(header, 2)             // magic = 2
	header = appendInt32(header, int32(crc))
	header = append(header, batchBody...)

	var result []byte
	result = appendInt64(result, 0)                    // baseOffset
	result = appendInt32(result, int32(len(header)))   // batchLength
	result = append(result, header...)
	return result
}

// buildTestMessageSetV0 builds a MessageSet v0 with the given key and value.
func buildTestMessageSetV0(key, value []byte) []byte {
	var msg []byte
	msg = append(msg, 0)   // magic = 0
	msg = append(msg, 0)   // attributes
	if key == nil {
		msg = appendInt32(msg, -1)
	} else {
		msg = appendInt32(msg, int32(len(key)))
		msg = append(msg, key...)
	}
	if value == nil {
		msg = appendInt32(msg, -1)
	} else {
		msg = appendInt32(msg, int32(len(value)))
		msg = append(msg, value...)
	}
	// crc placeholder (4 bytes, not validated by parser)
	var result []byte
	result = appendInt64(result, 0)              // offset
	result = appendInt32(result, int32(len(msg)+4)) // message_size = crc(4) + msg
	result = appendInt32(result, 0)              // crc (placeholder)
	result = append(result, msg...)
	return result
}

// buildTestMessageSetV1 builds a MessageSet v1 (with timestamp) with the given key and value.
func buildTestMessageSetV1(key, value []byte) []byte {
	var msg []byte
	msg = append(msg, 1)   // magic = 1
	msg = append(msg, 0)   // attributes
	msg = appendInt64(msg, 0) // timestamp
	if key == nil {
		msg = appendInt32(msg, -1)
	} else {
		msg = appendInt32(msg, int32(len(key)))
		msg = append(msg, key...)
	}
	if value == nil {
		msg = appendInt32(msg, -1)
	} else {
		msg = appendInt32(msg, int32(len(value)))
		msg = append(msg, value...)
	}
	var result []byte
	result = appendInt64(result, 0)
	result = appendInt32(result, int32(len(msg)+4))
	result = appendInt32(result, 0) // crc placeholder
	result = append(result, msg...)
	return result
}

// crc32Castagnoli computes CRC32C matching what buildRecordBatch uses.
func crc32Castagnoli(data []byte) uint32 {
	// import hash/crc32 is already used in interceptor.go; replicate here.
	// Use the same table as buildRecordBatch.
	import_crc32 := func(b []byte) uint32 {
		// Inline the same computation to avoid a separate import.
		const poly = 0x82F63B78
		crc := ^uint32(0)
		for _, v := range b {
			crc ^= uint32(v)
			for range 8 {
				if crc&1 != 0 {
					crc = (crc >> 1) ^ poly
				} else {
					crc >>= 1
				}
			}
		}
		return ^crc
	}
	return import_crc32(data)
}

// ── decodeZigzagVarint tests ──────────────────────────────────────────────────

func TestDecodeZigzagVarint(t *testing.T) {
	cases := []int64{0, -1, 1, -2, 127, -128, 300, -300, 65535, -65536}
	for _, v := range cases {
		encoded := zigzagVarint(v)
		got, n := decodeZigzagVarint(encoded)
		if n <= 0 {
			t.Errorf("decodeZigzagVarint(%d): failed to decode (n=%d)", v, n)
			continue
		}
		if got != v {
			t.Errorf("decodeZigzagVarint(%d): got %d", v, got)
		}
	}
}

func TestDecodeZigzagVarint_Empty(t *testing.T) {
	_, n := decodeZigzagVarint([]byte{})
	if n != 0 {
		t.Errorf("expected n=0 for empty input, got %d", n)
	}
}

// ── extractProduceData tests ──────────────────────────────────────────────────

func TestExtractProduceData_V2_WithClientID(t *testing.T) {
	reg := registry.NewMockRegistry()
	ic := NewInterceptor("localhost:0", reg)

	rs := buildTestRecordV2([]byte("order-123"), []byte(`{"status":"placed"}`), nil)
	data := buildTestProduceRequest("my-producer", "orders", rs)

	topic, key, value, headers := ic.extractProduceData(data)
	if topic != "orders" {
		t.Errorf("topic: got %q, want %q", topic, "orders")
	}
	if key != "order-123" {
		t.Errorf("key: got %q, want %q", key, "order-123")
	}
	if value != `{"status":"placed"}` {
		t.Errorf("value: got %q", value)
	}
	if len(headers) != 0 {
		t.Errorf("expected no headers, got %v", headers)
	}
}

func TestExtractProduceData_V2_NullClientID(t *testing.T) {
	reg := registry.NewMockRegistry()
	ic := NewInterceptor("localhost:0", reg)

	rs := buildTestRecordV2([]byte("k"), []byte("v"), nil)
	data := buildTestProduceRequestNullClientID("events", rs)

	topic, key, value, _ := ic.extractProduceData(data)
	if topic != "events" {
		t.Errorf("topic: got %q, want %q", topic, "events")
	}
	if key != "k" {
		t.Errorf("key: got %q, want %q", key, "k")
	}
	if value != "v" {
		t.Errorf("value: got %q, want %q", value, "v")
	}
}

func TestExtractProduceData_V2_WithHeaders(t *testing.T) {
	reg := registry.NewMockRegistry()
	ic := NewInterceptor("localhost:0", reg)

	hdrs := map[string]string{"x-trace-id": "abc123", "x-source": "payments"}
	rs := buildTestRecordV2([]byte("key1"), []byte("val1"), hdrs)
	data := buildTestProduceRequest("producer", "payments", rs)

	topic, key, value, headers := ic.extractProduceData(data)
	if topic != "payments" {
		t.Errorf("topic: got %q", topic)
	}
	if key != "key1" {
		t.Errorf("key: got %q", key)
	}
	if value != "val1" {
		t.Errorf("value: got %q", value)
	}
	if headers["x-trace-id"] != "abc123" {
		t.Errorf("header x-trace-id: got %q", headers["x-trace-id"])
	}
	if headers["x-source"] != "payments" {
		t.Errorf("header x-source: got %q", headers["x-source"])
	}
}

func TestExtractProduceData_V2_NullKey(t *testing.T) {
	reg := registry.NewMockRegistry()
	ic := NewInterceptor("localhost:0", reg)

	rs := buildTestRecordV2(nil, []byte("some-value"), nil)
	data := buildTestProduceRequest("p", "topic1", rs)

	_, key, value, _ := ic.extractProduceData(data)
	if key != "" {
		t.Errorf("expected empty key for null key, got %q", key)
	}
	if value != "some-value" {
		t.Errorf("value: got %q", value)
	}
}

func TestExtractProduceData_V2_Compressed(t *testing.T) {
	reg := registry.NewMockRegistry()
	ic := NewInterceptor("localhost:0", reg)

	// Build a RecordBatch v2 with compression=1 (gzip) in attributes.
	rs := buildTestRecordV2([]byte("k"), []byte("v"), nil)
	// Patch attributes at offset 21 (INT16, bits 0-2 = codec).
	// RecordBatch layout: baseOffset(8)+batchLength(4)+partLeaderEpoch(4)+magic(1)+crc(4) = offset 21.
	rs[21] = 0x00
	rs[22] = 0x01

	data := buildTestProduceRequest("p", "compressed-topic", rs)
	topic, key, value, _ := ic.extractProduceData(data)

	if topic != "compressed-topic" {
		t.Errorf("topic: got %q, want %q", topic, "compressed-topic")
	}
	if key != "" || value != "" {
		t.Errorf("expected empty key/value for compressed batch, got key=%q value=%q", key, value)
	}
}

func TestExtractProduceData_V0_KeyValue(t *testing.T) {
	reg := registry.NewMockRegistry()
	ic := NewInterceptor("localhost:0", reg)

	rs := buildTestMessageSetV0([]byte("mykey"), []byte(`{"id":42}`))
	data := buildTestProduceRequest("producer", "v0-topic", rs)

	topic, key, value, _ := ic.extractProduceData(data)
	if topic != "v0-topic" {
		t.Errorf("topic: got %q", topic)
	}
	if key != "mykey" {
		t.Errorf("key: got %q", key)
	}
	if value != `{"id":42}` {
		t.Errorf("value: got %q", value)
	}
}

func TestExtractProduceData_V1_WithTimestamp(t *testing.T) {
	reg := registry.NewMockRegistry()
	ic := NewInterceptor("localhost:0", reg)

	rs := buildTestMessageSetV1([]byte("k1"), []byte("v1"))
	data := buildTestProduceRequest("producer", "v1-topic", rs)

	topic, key, value, _ := ic.extractProduceData(data)
	if topic != "v1-topic" {
		t.Errorf("topic: got %q", topic)
	}
	if key != "k1" {
		t.Errorf("key: got %q, want %q", key, "k1")
	}
	if value != "v1" {
		t.Errorf("value: got %q, want %q", value, "v1")
	}
}

func TestExtractProduceData_ShortData(t *testing.T) {
	reg := registry.NewMockRegistry()
	ic := NewInterceptor("localhost:0", reg)

	// Should not panic on various truncated inputs.
	for _, data := range [][]byte{nil, {}, {0x00}, make([]byte, 5), make([]byte, 15)} {
		topic, key, value, _ := ic.extractProduceData(data)
		_ = topic
		_ = key
		_ = value
	}
}

func TestExtractProduceData_LargeKey(t *testing.T) {
	reg := registry.NewMockRegistry()
	ic := NewInterceptor("localhost:0", reg)

	// Key of 2000 bytes — previously would have been silently dropped (old 1000-byte cap).
	largeKey := []byte(strings.Repeat("x", 2000))
	rs := buildTestRecordV2(largeKey, []byte("value"), nil)
	data := buildTestProduceRequest("p", "large-key-topic", rs)

	topic, key, _, _ := ic.extractProduceData(data)
	if topic != "large-key-topic" {
		t.Errorf("topic: got %q", topic)
	}
	if len(key) != 2000 {
		t.Errorf("expected key length 2000, got %d", len(key))
	}
}
