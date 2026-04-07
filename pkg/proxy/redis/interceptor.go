package redis

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/livecodelife/linespec/pkg/dsl"
	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/verify"
)

// readCommands is the set of Redis commands that map to READ:REDIS expectations.
var readCommands = map[string]bool{
	"GET": true, "MGET": true, "HGET": true, "HGETALL": true, "HMGET": true,
	"LRANGE": true, "LLEN": true, "SMEMBERS": true, "SISMEMBER": true,
	"ZRANGE": true, "ZRANGEBYSCORE": true, "EXISTS": true, "TTL": true,
	"TYPE": true, "KEYS": true, "STRLEN": true, "LINDEX": true,
}

// Interceptor is a mock Redis server that serves responses from the registry.
// It speaks RESP2 protocol.
type Interceptor struct {
	addr     string
	registry *registry.MockRegistry
}

// NewInterceptor creates a new Redis interceptor listening on addr.
func NewInterceptor(addr string, reg *registry.MockRegistry) *Interceptor {
	return &Interceptor{
		addr:     addr,
		registry: reg,
	}
}

// Start begins listening for Redis connections. It blocks until ctx is cancelled.
func (i *Interceptor) Start(ctx context.Context) error {
	logger.Debug("Redis Interceptor: Starting on %s", i.addr)
	ln, err := net.Listen("tcp", i.addr)
	if err != nil {
		return err
	}
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
				logger.Debug("Redis Interceptor: Accept error: %v", err)
				continue
			}
		}
		go i.handleConn(conn)
	}
}

func (i *Interceptor) handleConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	for {
		args, err := readCommand(reader)
		if err != nil {
			if err != io.EOF {
				logger.Debug("Redis Interceptor: Read error: %v", err)
			}
			return
		}
		if len(args) == 0 {
			continue
		}

		cmd := strings.ToUpper(args[0])
		var key string
		if len(args) > 1 {
			key = args[1]
		}

		logger.Debug("Redis Interceptor: %s %s", cmd, key)

		response := i.handleCommand(cmd, key, args)
		if _, err := conn.Write(response); err != nil {
			return
		}
	}
}

func (i *Interceptor) handleCommand(cmd, key string, args []string) []byte {
	// Handle protocol-level commands directly.
	switch cmd {
	case "PING":
		if len(args) > 1 {
			return encodeBulkString(args[1])
		}
		return encodeSimpleString("PONG")
	case "AUTH", "SELECT", "MULTI", "EXEC", "DISCARD":
		return encodeSimpleString("OK")
	case "QUIT":
		return encodeSimpleString("OK")
	case "HELLO":
		// Respond with a minimal RESP2-compatible inline response instead of a map.
		// Most clients accept this for HELLO 2 and HELLO 3 commands.
		return encodeArray([][]byte{
			encodeBulkString("server"), encodeBulkString("redis"),
			encodeBulkString("version"), encodeBulkString("7.0.0"),
			encodeBulkString("proto"), encodeInteger(2),
		})
	case "COMMAND":
		return encodeArray(nil) // empty array — clients accept this
	case "INFO":
		return encodeBulkString("# Server\r\nredis_version:7.0.0\r\n")
	case "CLIENT":
		return encodeSimpleString("OK")
	case "CONFIG":
		if len(args) > 1 && strings.ToUpper(args[1]) == "GET" {
			return encodeArray(nil)
		}
		return encodeSimpleString("OK")
	}

	// Data commands: look up a mock.
	i.registry.CheckNegativeRedisMocks(cmd, key)
	mock, found := i.registry.FindRedisMock(cmd, key)

	// Run VERIFY rules if a mock was found.
	if found && len(mock.Verify) > 0 {
		var value string
		if len(args) > 2 {
			value = args[2]
		}
		redisCmd := &verify.RedisCommand{
			Command: cmd,
			Key:     key,
			Value:   value,
			Args:    args,
		}
		rules := verify.ExtractVerifyRulesForTarget(mock.Verify, "redis")
		if err := verify.VerifyRedis(redisCmd, rules); err != nil {
			logger.Debug("Redis Interceptor: VERIFY failed: %v", err)
			return encodeError(err.Error())
		}
	}

	if !found {
		// No mock: nil for reads, OK for writes.
		if readCommands[cmd] {
			return encodeNil()
		}
		return encodeSimpleString("OK")
	}

	// Load and return the mock response payload.
	if mock.ReturnsFile != "" {
		loader := dsl.NewPayloadLoader(mock.BaseDir)
		raw, _, err := loader.LoadRaw(mock.ReturnsFile)
		if err != nil {
			logger.Debug("Redis Interceptor: Failed to load payload: %v", err)
			return encodeError(fmt.Sprintf("failed to load response payload: %v", err))
		}
		return encodePayload(raw)
	}

	if mock.ReturnsEmpty {
		return encodeNil()
	}

	// Default: OK for writes, nil for reads.
	if readCommands[cmd] {
		return encodeNil()
	}
	return encodeSimpleString("OK")
}

// encodePayload converts a JSON payload to a RESP2 response.
// JSON strings -> bulk string, arrays -> RESP2 array, numbers -> integer, null -> nil.
// JSON objects -> bulk string containing the raw JSON (preserves the value for clients
// that do json.loads(redis.get(key))). Use HSET/HGETALL mocks for hash semantics.
func encodePayload(data []byte) []byte {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		// Not valid JSON — return raw bytes as a bulk string.
		return encodeBulkString(string(data))
	}
	// JSON objects are stored as serialized strings in Redis string keys.
	// Return the raw JSON bytes so clients can deserialize them.
	if _, ok := v.(map[string]interface{}); ok {
		return encodeBulkString(string(data))
	}
	return encodeValue(v)
}

func encodeValue(v interface{}) []byte {
	switch val := v.(type) {
	case nil:
		return encodeNil()
	case bool:
		if val {
			return encodeInteger(1)
		}
		return encodeInteger(0)
	case float64:
		// JSON numbers are float64; if it's a whole number return as integer.
		if val == float64(int64(val)) {
			return encodeInteger(int64(val))
		}
		return encodeBulkString(strconv.FormatFloat(val, 'f', -1, 64))
	case string:
		return encodeBulkString(val)
	case []interface{}:
		items := make([][]byte, len(val))
		for idx, item := range val {
			items[idx] = encodeValue(item)
		}
		return encodeArray(items)
	case map[string]interface{}:
		// Hash map: encode as flat array of field/value pairs (HGETALL-style).
		items := make([][]byte, 0, len(val)*2)
		for k, vv := range val {
			items = append(items, encodeBulkString(k))
			items = append(items, encodeValue(vv))
		}
		return encodeArray(items)
	default:
		return encodeBulkString(fmt.Sprintf("%v", v))
	}
}

// readCommand reads a RESP2 command from the reader.
// Supports multibulk (*N\r\n) format used by all modern Redis clients.
func readCommand(r *bufio.Reader) ([]string, error) {
	line, err := readLine(r)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, nil
	}

	if line[0] != '*' {
		// Inline command (e.g. PING\r\n) — split on whitespace.
		return strings.Fields(line), nil
	}

	count, err := strconv.Atoi(line[1:])
	if err != nil || count < 0 {
		return nil, fmt.Errorf("invalid multibulk count: %s", line)
	}

	args := make([]string, count)
	for idx := 0; idx < count; idx++ {
		lenLine, err := readLine(r)
		if err != nil {
			return nil, err
		}
		if len(lenLine) < 2 || lenLine[0] != '$' {
			return nil, fmt.Errorf("expected bulk string, got: %s", lenLine)
		}
		length, err := strconv.Atoi(lenLine[1:])
		if err != nil || length < 0 {
			return nil, fmt.Errorf("invalid bulk string length: %s", lenLine)
		}
		buf := make([]byte, length+2) // +2 for \r\n
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args[idx] = string(buf[:length])
	}
	return args, nil
}

// readLine reads a CRLF-terminated line and returns it without the trailing \r\n.
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// RESP2 encoding helpers.

func encodeSimpleString(s string) []byte {
	return []byte("+" + s + "\r\n")
}

func encodeBulkString(s string) []byte {
	return []byte(fmt.Sprintf("$%d\r\n%s\r\n", len(s), s))
}

func encodeInteger(n int64) []byte {
	return []byte(fmt.Sprintf(":%d\r\n", n))
}

func encodeArray(items [][]byte) []byte {
	if items == nil {
		return []byte("*0\r\n")
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("*%d\r\n", len(items)))
	for _, item := range items {
		sb.Write(item)
	}
	return []byte(sb.String())
}

func encodeNil() []byte {
	return []byte("$-1\r\n")
}

func encodeError(msg string) []byte {
	// Strip newlines to keep the error on one RESP line.
	msg = strings.ReplaceAll(msg, "\r", " ")
	msg = strings.ReplaceAll(msg, "\n", " ")
	return []byte("-ERR " + msg + "\r\n")
}
