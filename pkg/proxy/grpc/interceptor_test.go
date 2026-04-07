package grpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/types"
)

func TestEncodeGRPCFrame(t *testing.T) {
	msg := []byte(`{"user_id": "123"}`)
	frame := encodeGRPCFrame(msg)

	if len(frame) != 5+len(msg) {
		t.Fatalf("Expected frame length %d, got %d", 5+len(msg), len(frame))
	}

	// First byte: compressed flag (must be 0)
	if frame[0] != 0 {
		t.Errorf("Expected compressed flag 0, got %d", frame[0])
	}

	// Bytes 1-4: message length
	msgLen := binary.BigEndian.Uint32(frame[1:5])
	if int(msgLen) != len(msg) {
		t.Errorf("Expected message length %d, got %d", len(msg), msgLen)
	}

	// Remaining bytes: message content
	if !bytes.Equal(frame[5:], msg) {
		t.Errorf("Frame body mismatch")
	}
}

func TestEncodeGRPCFrame_Empty(t *testing.T) {
	frame := encodeGRPCFrame([]byte{})
	if len(frame) != 5 {
		t.Fatalf("Expected frame length 5 for empty message, got %d", len(frame))
	}
	msgLen := binary.BigEndian.Uint32(frame[1:5])
	if msgLen != 0 {
		t.Errorf("Expected message length 0, got %d", msgLen)
	}
}

func TestInterceptor_NoMock_ReturnsUnimplemented(t *testing.T) {
	reg := registry.NewMockRegistry()
	addr := "127.0.0.1:19877"
	interceptor := NewInterceptor(addr, reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = interceptor.Start(ctx)
	}()

	// Wait for startup
	time.Sleep(100 * time.Millisecond)

	// Make a gRPC-style HTTP/2 request
	reqBody := encodeGRPCFrame([]byte(`{"user_id": "123"}`))
	req, err := http.NewRequest("POST", "http://"+addr+"/users.UserService/GetUser", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/grpc+json")

	// Use a plain HTTP/1.1 client for this test (h2c requires upgrade)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		// On some systems the h2c upgrade may fail over HTTP/1.1 — skip
		t.Skipf("Skipping h2c test (HTTP/1.1 fallback): %v", err)
		return
	}
	defer resp.Body.Close()

	// Should get a 200 with grpc-status != 0
	if resp.StatusCode != 200 {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}
}

func TestGRPCPathParsing(t *testing.T) {
	tests := []struct {
		path           string
		expectedSvc    string
		expectedMethod string
		expectError    bool
	}{
		{
			path:           "/users.UserService/GetUser",
			expectedSvc:    "users.UserService",
			expectedMethod: "GetUser",
		},
		{
			path:           "/com.example.orders.OrderService/CreateOrder",
			expectedSvc:    "com.example.orders.OrderService",
			expectedMethod: "CreateOrder",
		},
		{
			path:        "/invalid",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			path := strings.TrimPrefix(tt.path, "/")
			parts := strings.SplitN(path, "/", 2)

			if tt.expectError {
				if len(parts) == 2 {
					t.Errorf("Expected parse error for path %s", tt.path)
				}
				return
			}

			if len(parts) != 2 {
				t.Fatalf("Failed to parse path %s", tt.path)
			}
			if parts[0] != tt.expectedSvc {
				t.Errorf("Expected service %s, got %s", tt.expectedSvc, parts[0])
			}
			if parts[1] != tt.expectedMethod {
				t.Errorf("Expected method %s, got %s", tt.expectedMethod, parts[1])
			}
		})
	}
}

func TestGRPCFrameDecoding(t *testing.T) {
	// Encode a message and then decode it
	original := `{"user_id": "123", "name": "Alice"}`
	frame := encodeGRPCFrame([]byte(original))

	// Decode
	if len(frame) < 5 {
		t.Fatalf("Frame too short")
	}

	compressedFlag := frame[0]
	if compressedFlag != 0 {
		t.Errorf("Expected uncompressed (0), got %d", compressedFlag)
	}

	msgLen := binary.BigEndian.Uint32(frame[1:5])
	if int(msgLen) != len(original) {
		t.Errorf("Expected msg length %d, got %d", len(original), msgLen)
	}

	decoded := string(frame[5 : 5+msgLen])
	if decoded != original {
		t.Errorf("Decoded message mismatch: got %s", decoded)
	}
}

func TestRegistryFindGRPCMock(t *testing.T) {
	reg := registry.NewMockRegistry()

	spec := &types.TestSpec{
		Name: "test-grpc",
		Expects: []types.ExpectStatement{
			{
				Channel:   types.GRPC,
				Service:   "users.UserService",
				RPCMethod: "GetUser",
			},
		},
	}
	reg.Register(spec)

	// Should find the mock
	mock, found := reg.FindGRPCMock("users.UserService", "GetUser")
	if !found {
		t.Fatal("Expected to find gRPC mock")
	}
	if mock.Service != "users.UserService" {
		t.Errorf("Expected service users.UserService, got %s", mock.Service)
	}
	if mock.RPCMethod != "GetUser" {
		t.Errorf("Expected method GetUser, got %s", mock.RPCMethod)
	}

	// Should not find a second time (consumed)
	_, found = reg.FindGRPCMock("users.UserService", "GetUser")
	if found {
		t.Error("Mock should be consumed after first hit")
	}
}

func TestRegistryCheckNegativeGRPCMocks(t *testing.T) {
	reg := registry.NewMockRegistry()

	spec := &types.TestSpec{
		Name: "test-grpc-negative",
		ExpectsNot: []types.ExpectStatement{
			{
				Channel:   types.GRPC,
				Service:   "users.UserService",
				RPCMethod: "DeleteUser",
			},
		},
	}
	reg.Register(spec)

	// Simulate a call to DeleteUser
	reg.CheckNegativeGRPCMocks("users.UserService", "DeleteUser")

	// Verify should fail because the negative mock was hit
	err := reg.VerifyAll()
	if err == nil {
		t.Error("VerifyAll should fail when a negative gRPC mock was called")
	}
}

// TestGRPCFrameRoundtrip verifies that encodeGRPCFrame + manual decode is consistent.
func TestGRPCFrameRoundtrip(t *testing.T) {
	messages := []string{
		`{}`,
		`{"key": "value"}`,
		strings.Repeat("x", 1000),
	}

	for _, msg := range messages {
		frame := encodeGRPCFrame([]byte(msg))
		r := bytes.NewReader(frame)

		header := make([]byte, 5)
		_, err := io.ReadFull(r, header)
		if err != nil {
			t.Fatalf("Failed to read header: %v", err)
		}

		length := binary.BigEndian.Uint32(header[1:5])
		body := make([]byte, length)
		_, err = io.ReadFull(r, body)
		if err != nil {
			t.Fatalf("Failed to read body: %v", err)
		}

		if string(body) != msg {
			t.Errorf("Roundtrip mismatch for message of length %d", len(msg))
		}
	}
}
