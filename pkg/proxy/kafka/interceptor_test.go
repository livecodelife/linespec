package kafka

import (
	"encoding/binary"
	"testing"

	"github.com/livecodelife/linespec/pkg/registry"
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
