package kafka

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/registry"
)

// wireReader decodes Kafka primitives and records errors on short reads.
type wireReader struct {
	b   []byte
	err error
}

func (r *wireReader) take(n int) []byte {
	if r.err != nil || len(r.b) < n {
		r.err = fmt.Errorf("short read: need %d, have %d", n, len(r.b))
		return make([]byte, n)
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out
}
func (r *wireReader) i16() int16 { return int16(binary.BigEndian.Uint16(r.take(2))) }
func (r *wireReader) i32() int32 { return int32(binary.BigEndian.Uint32(r.take(4))) }
func (r *wireReader) i64() int64 { return int64(binary.BigEndian.Uint64(r.take(8))) }
func (r *wireReader) str() string {
	n := r.i16()
	if n < 0 {
		return ""
	}
	return string(r.take(int(n)))
}

// capture runs fn against one end of a pipe and returns the framed payload.
func capture(t *testing.T, fn func(conn net.Conn)) []byte {
	t.Helper()
	c1, c2 := net.Pipe()
	defer c2.Close()
	go func() {
		fn(c1)
		c1.Close()
	}()
	all, err := io.ReadAll(c2)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 4 || int(binary.BigEndian.Uint32(all[:4])) != len(all)-4 {
		t.Fatalf("bad frame length in %d bytes", len(all))
	}
	return all[4:]
}

func TestMetadataResponse_Layout_V0toV2(t *testing.T) {
	for _, v := range []uint16{0, 1, 2} {
		t.Run(fmt.Sprintf("v%d", v), func(t *testing.T) {
			reg := registry.NewMockRegistry()
			reg.SeedTopic("my-topic", []byte(`{}`))
			ic := NewInterceptor("localhost:0", reg)
			ic.SetHost("kafka-proxy")

			payload := capture(t, func(c net.Conn) {
				ic.sendMetadataResponse(c, []byte{0, 0, 0, 7}, v)
			})
			r := &wireReader{b: payload}
			if got := r.i32(); got != 7 {
				t.Fatalf("correlation id = %d", got)
			}
			if n := r.i32(); n != 1 {
				t.Fatalf("brokers = %d", n)
			}
			if id := r.i32(); id != 1 {
				t.Errorf("node_id = %d", id)
			}
			if h := r.str(); h != "kafka-proxy" {
				t.Errorf("host = %q", h)
			}
			if p := r.i32(); p != 9092 {
				t.Errorf("port = %d", p)
			}
			if v >= 1 {
				if rack := r.i16(); rack != -1 {
					t.Errorf("rack len = %d, want -1", rack)
				}
			}
			if v >= 2 {
				if cl := r.i16(); cl != -1 {
					t.Errorf("cluster_id len = %d, want -1", cl)
				}
			}
			if v >= 1 {
				if c := r.i32(); c != 1 {
					t.Errorf("controller_id = %d", c)
				}
			}
			if n := r.i32(); n != 1 {
				t.Fatalf("topics = %d", n)
			}
			if e := r.i16(); e != 0 {
				t.Errorf("topic error = %d", e)
			}
			if name := r.str(); name != "my-topic" {
				t.Errorf("topic = %q", name)
			}
			if v >= 1 {
				r.take(1) // is_internal
			}
			if n := r.i32(); n != 1 {
				t.Fatalf("partitions = %d", n)
			}
			r.i16() // error_code
			r.i32() // partition_index
			r.i32() // leader
			if n := r.i32(); n != 1 {
				t.Fatalf("replicas = %d", n)
			}
			r.i32()
			if n := r.i32(); n != 1 {
				t.Fatalf("isr = %d", n)
			}
			r.i32()
			if r.err != nil {
				t.Fatal(r.err)
			}
			if len(r.b) != 0 {
				t.Errorf("%d trailing bytes", len(r.b))
			}
		})
	}
}

func TestProduceResponse_Layout_V0toV3(t *testing.T) {
	for _, v := range []uint16{0, 1, 2, 3} {
		t.Run(fmt.Sprintf("v%d", v), func(t *testing.T) {
			ic := NewInterceptor("localhost:0", registry.NewMockRegistry())
			payload := capture(t, func(c net.Conn) {
				ic.sendProduceResponse(c, []byte{0, 0, 0, 9}, "t", v)
			})
			r := &wireReader{b: payload}
			r.i32() // correlation
			if n := r.i32(); n != 1 {
				t.Fatalf("topics = %d", n)
			}
			if s := r.str(); s != "t" {
				t.Errorf("topic = %q", s)
			}
			if n := r.i32(); n != 1 {
				t.Fatalf("partitions = %d", n)
			}
			r.i32() // partition
			if e := r.i16(); e != 0 {
				t.Errorf("error = %d", e)
			}
			r.i64() // base_offset
			if v >= 2 {
				r.i64() // log_append_time
			}
			if v >= 1 {
				r.i32() // throttle_time_ms (trailing)
			}
			if r.err != nil {
				t.Fatal(r.err)
			}
			if len(r.b) != 0 {
				t.Errorf("%d trailing bytes", len(r.b))
			}
		})
	}
}

func TestExtractProduceData_V3_TransactionalID(t *testing.T) {
	rs := buildTestRecordV2([]byte("k3"), []byte(`{"a":1}`), nil)
	for name, txn := range map[string]*string{"null": nil, "non-null": strPtr("txn-1")} {
		t.Run(name, func(t *testing.T) {
			ic := NewInterceptor("localhost:0", registry.NewMockRegistry())
			var b []byte
			b = appendString(b, "ruby-kafka")
			if txn == nil {
				b = appendInt16(b, -1)
			} else {
				b = appendString(b, *txn)
			}
			b = appendInt16(b, 1)
			b = appendInt32(b, 1500)
			b = appendInt32(b, 1)
			b = appendString(b, "orders")
			b = appendInt32(b, 1)
			b = appendInt32(b, 0)
			b = appendInt32(b, int32(len(rs)))
			b = append(b, rs...)

			topic, key, value, _ := ic.extractProduceDataVersioned(b, 3)
			if topic != "orders" || key != "k3" || value != `{"a":1}` {
				t.Errorf("got topic=%q key=%q value=%q", topic, key, value)
			}
		})
	}
}

func TestExtractProduceData_V3_Truncated(t *testing.T) {
	ic := NewInterceptor("localhost:0", registry.NewMockRegistry())
	b := appendString(nil, "c")
	b = appendInt16(b, 50) // transactional_id length exceeds remaining bytes
	if topic, _, _, _ := ic.extractProduceDataVersioned(b, 3); topic != "" {
		t.Errorf("topic = %q, want empty", topic)
	}
}

func strPtr(s string) *string { return &s }
