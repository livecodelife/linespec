package redis

import (
	"bufio"
	"strings"
	"testing"

	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/types"
)

func TestEncodeSimpleString(t *testing.T) {
	result := string(encodeSimpleString("OK"))
	if result != "+OK\r\n" {
		t.Errorf("Expected '+OK\\r\\n', got %q", result)
	}
}

func TestEncodeBulkString(t *testing.T) {
	result := string(encodeBulkString("hello"))
	if result != "$5\r\nhello\r\n" {
		t.Errorf("Expected '$5\\r\\nhello\\r\\n', got %q", result)
	}
}

func TestEncodeInteger(t *testing.T) {
	result := string(encodeInteger(42))
	if result != ":42\r\n" {
		t.Errorf("Expected ':42\\r\\n', got %q", result)
	}

	result = string(encodeInteger(-1))
	if result != ":-1\r\n" {
		t.Errorf("Expected ':-1\\r\\n', got %q", result)
	}
}

func TestEncodeNil(t *testing.T) {
	result := string(encodeNil())
	if result != "$-1\r\n" {
		t.Errorf("Expected '$-1\\r\\n', got %q", result)
	}
}

func TestEncodeArray(t *testing.T) {
	items := [][]byte{
		encodeBulkString("foo"),
		encodeBulkString("bar"),
	}
	result := string(encodeArray(items))
	expected := "*2\r\n$3\r\nfoo\r\n$3\r\nbar\r\n"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestEncodeArray_Empty(t *testing.T) {
	result := string(encodeArray(nil))
	if result != "*0\r\n" {
		t.Errorf("Expected '*0\\r\\n', got %q", result)
	}
}

func TestEncodeError(t *testing.T) {
	result := string(encodeError("something went wrong"))
	if result != "-ERR something went wrong\r\n" {
		t.Errorf("Unexpected error encoding: %q", result)
	}
}

func TestEncodeError_StripsNewlines(t *testing.T) {
	result := string(encodeError("line1\nline2"))
	if strings.Contains(result, "\n\n") {
		t.Errorf("Error should not contain bare newlines: %q", result)
	}
}

func TestReadCommand_Multibulk(t *testing.T) {
	input := "*3\r\n$3\r\nSET\r\n$6\r\nuser:1\r\n$5\r\nhello\r\n"
	reader := bufio.NewReader(strings.NewReader(input))

	args, err := readCommand(reader)
	if err != nil {
		t.Fatalf("readCommand error: %v", err)
	}
	if len(args) != 3 {
		t.Fatalf("Expected 3 args, got %d", len(args))
	}
	if args[0] != "SET" {
		t.Errorf("Expected args[0]='SET', got %q", args[0])
	}
	if args[1] != "user:1" {
		t.Errorf("Expected args[1]='user:1', got %q", args[1])
	}
	if args[2] != "hello" {
		t.Errorf("Expected args[2]='hello', got %q", args[2])
	}
}

func TestReadCommand_Inline(t *testing.T) {
	input := "PING\r\n"
	reader := bufio.NewReader(strings.NewReader(input))

	args, err := readCommand(reader)
	if err != nil {
		t.Fatalf("readCommand error: %v", err)
	}
	if len(args) != 1 || args[0] != "PING" {
		t.Errorf("Expected [PING], got %v", args)
	}
}

func TestHandleCommand_PING(t *testing.T) {
	reg := registry.NewMockRegistry()
	i := NewInterceptor("127.0.0.1:6379", reg)

	resp := i.handleCommand("PING", "", []string{"PING"})
	if string(resp) != "+PONG\r\n" {
		t.Errorf("Expected '+PONG\\r\\n', got %q", string(resp))
	}
}

func TestHandleCommand_PING_WithMessage(t *testing.T) {
	reg := registry.NewMockRegistry()
	i := NewInterceptor("127.0.0.1:6379", reg)

	resp := i.handleCommand("PING", "", []string{"PING", "hello"})
	if string(resp) != "$5\r\nhello\r\n" {
		t.Errorf("Expected bulk string 'hello', got %q", string(resp))
	}
}

func TestHandleCommand_AUTH(t *testing.T) {
	reg := registry.NewMockRegistry()
	i := NewInterceptor("127.0.0.1:6379", reg)

	resp := i.handleCommand("AUTH", "", []string{"AUTH", "password"})
	if string(resp) != "+OK\r\n" {
		t.Errorf("Expected '+OK\\r\\n', got %q", string(resp))
	}
}

func TestHandleCommand_SELECT(t *testing.T) {
	reg := registry.NewMockRegistry()
	i := NewInterceptor("127.0.0.1:6379", reg)

	resp := i.handleCommand("SELECT", "0", []string{"SELECT", "0"})
	if string(resp) != "+OK\r\n" {
		t.Errorf("Expected '+OK\\r\\n', got %q", string(resp))
	}
}

func TestHandleCommand_COMMAND(t *testing.T) {
	reg := registry.NewMockRegistry()
	i := NewInterceptor("127.0.0.1:6379", reg)

	resp := i.handleCommand("COMMAND", "", []string{"COMMAND"})
	if string(resp) != "*0\r\n" {
		t.Errorf("Expected '*0\\r\\n' (empty array), got %q", string(resp))
	}
}

func TestHandleCommand_GET_NoMock_ReturnsNil(t *testing.T) {
	reg := registry.NewMockRegistry()
	i := NewInterceptor("127.0.0.1:6379", reg)

	resp := i.handleCommand("GET", "user:999", []string{"GET", "user:999"})
	if string(resp) != "$-1\r\n" {
		t.Errorf("Expected nil ($-1\\r\\n) for GET with no mock, got %q", string(resp))
	}
}

func TestHandleCommand_SET_NoMock_ReturnsOK(t *testing.T) {
	reg := registry.NewMockRegistry()
	i := NewInterceptor("127.0.0.1:6379", reg)

	resp := i.handleCommand("SET", "user:999", []string{"SET", "user:999", "value"})
	if string(resp) != "+OK\r\n" {
		t.Errorf("Expected +OK for SET with no mock, got %q", string(resp))
	}
}

func TestHandleCommand_GET_WithMock(t *testing.T) {
	reg := registry.NewMockRegistry()

	spec := &types.TestSpec{
		Name:    "test-redis",
		BaseDir: ".",
		Expects: []types.ExpectStatement{
			{
				Channel:      types.ReadRedis,
				Command:      "GET",
				RedisKey:     "user:123",
				ReturnsEmpty: true,
			},
		},
	}
	reg.Register(spec)

	i := NewInterceptor("127.0.0.1:6379", reg)
	resp := i.handleCommand("GET", "user:123", []string{"GET", "user:123"})

	// ReturnsEmpty should return nil
	if string(resp) != "$-1\r\n" {
		t.Errorf("Expected nil for ReturnsEmpty mock, got %q", string(resp))
	}
}

func TestEncodePayload_String(t *testing.T) {
	data := []byte(`"hello world"`)
	result := string(encodePayload(data))
	if result != "$11\r\nhello world\r\n" {
		t.Errorf("Expected bulk string for JSON string, got %q", result)
	}
}

func TestEncodePayload_Number(t *testing.T) {
	data := []byte(`42`)
	result := string(encodePayload(data))
	if result != ":42\r\n" {
		t.Errorf("Expected integer for JSON number, got %q", result)
	}
}

func TestEncodePayload_Null(t *testing.T) {
	data := []byte(`null`)
	result := string(encodePayload(data))
	if result != "$-1\r\n" {
		t.Errorf("Expected nil for JSON null, got %q", result)
	}
}

func TestEncodePayload_Array(t *testing.T) {
	data := []byte(`["a","b","c"]`)
	result := string(encodePayload(data))
	expected := "*3\r\n$1\r\na\r\n$1\r\nb\r\n$1\r\nc\r\n"
	if result != expected {
		t.Errorf("Expected RESP array for JSON array, got %q", result)
	}
}

func TestEncodePayload_InvalidJSON(t *testing.T) {
	data := []byte(`not json`)
	result := string(encodePayload(data))
	// Should fall back to bulk string
	if !strings.HasPrefix(result, "$") {
		t.Errorf("Expected bulk string fallback for invalid JSON, got %q", result)
	}
}

func TestRegistryFindRedisMock(t *testing.T) {
	reg := registry.NewMockRegistry()

	spec := &types.TestSpec{
		Name: "test-redis",
		Expects: []types.ExpectStatement{
			{
				Channel:  types.ReadRedis,
				Command:  "GET",
				RedisKey: "user:123",
			},
		},
	}
	reg.Register(spec)

	mock, found := reg.FindRedisMock("GET", "user:123")
	if !found {
		t.Fatal("Expected to find Redis mock")
	}
	if mock.Command != "GET" {
		t.Errorf("Expected command GET, got %s", mock.Command)
	}
	if mock.RedisKey != "user:123" {
		t.Errorf("Expected key user:123, got %s", mock.RedisKey)
	}

	// Should not find a second time (consumed)
	_, found = reg.FindRedisMock("GET", "user:123")
	if found {
		t.Error("Mock should be consumed after first hit")
	}
}

func TestRegistryCheckNegativeRedisMocks(t *testing.T) {
	reg := registry.NewMockRegistry()

	spec := &types.TestSpec{
		Name: "test-redis-negative",
		ExpectsNot: []types.ExpectStatement{
			{
				Channel:  types.WriteRedis,
				Command:  "DEL",
				RedisKey: "user:123",
			},
		},
	}
	reg.Register(spec)

	// Simulate a DEL call
	reg.CheckNegativeRedisMocks("DEL", "user:123")

	// Verify should fail because the negative mock was hit
	err := reg.VerifyAll()
	if err == nil {
		t.Error("VerifyAll should fail when a negative Redis mock was called")
	}
}

func TestReadCommands_Classification(t *testing.T) {
	reads := []string{"GET", "HGET", "HGETALL", "MGET", "LRANGE", "EXISTS", "TTL", "KEYS"}
	writes := []string{"SET", "HSET", "DEL", "LPUSH", "RPUSH", "INCR", "EXPIRE"}

	for _, cmd := range reads {
		if !readCommands[cmd] {
			t.Errorf("Expected %s to be in readCommands", cmd)
		}
	}

	for _, cmd := range writes {
		if readCommands[cmd] {
			t.Errorf("Expected %s to NOT be in readCommands (it's a write)", cmd)
		}
	}
}
