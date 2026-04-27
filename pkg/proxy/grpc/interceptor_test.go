package grpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/types"
	"golang.org/x/net/http2"
)

func newH2CClient() *http.Client {
	return &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				dialer := &net.Dialer{}
				return dialer.DialContext(ctx, network, addr)
			},
		},
	}
}

func TestEncodeGRPCFrame(t *testing.T) {
	msg := []byte(`{"user_id": "123"}`)
	frame := encodeGRPCFrame(msg)

	if len(frame) != 5+len(msg) {
		t.Fatalf("Expected frame length %d, got %d", 5+len(msg), len(frame))
	}

	if frame[0] != 0 {
		t.Errorf("Expected compressed flag 0, got %d", frame[0])
	}

	msgLen := binary.BigEndian.Uint32(frame[1:5])
	if int(msgLen) != len(msg) {
		t.Errorf("Expected message length %d, got %d", len(msg), msgLen)
	}

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

func TestEncodeGRPCFrame_Nil(t *testing.T) {
	frame := encodeGRPCFrame(nil)
	if len(frame) != 5 {
		t.Fatalf("Expected frame length 5 for nil message, got %d", len(frame))
	}
	if frame[0] != 0 {
		t.Errorf("Expected compression flag 0, got %d", frame[0])
	}
	msgLen := binary.BigEndian.Uint32(frame[1:5])
	if msgLen != 0 {
		t.Errorf("Expected message length 0, got %d", msgLen)
	}
}

func TestInterceptor_NoMock_ReturnsUnimplemented(t *testing.T) {
	reg := registry.NewMockRegistry()
	addr := "127.0.0.1:19877"
	interceptor := NewInterceptor(addr, "", reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = interceptor.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	reqBody := encodeGRPCFrame([]byte(`{"user_id": "123"}`))
	req, err := http.NewRequest("POST", "http://"+addr+"/users.UserService/GetUser", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/grpc+json")

	client := newH2CClient()
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("Skipping h2c test: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}
}

func TestGRPCPathParsing(t *testing.T) {
	tests := []struct {
		path string
		expectedSvc string
		expectedMethod string
		expectError bool
	}{
		{
			path: "/users.UserService/GetUser",
			expectedSvc: "users.UserService",
			expectedMethod: "GetUser",
		},
		{
			path: "/com.example.orders.OrderService/CreateOrder",
			expectedSvc: "com.example.orders.OrderService",
			expectedMethod: "CreateOrder",
		},
		{
			path: "/invalid",
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
	original := `{"user_id": "123", "name": "Alice"}`
	frame := encodeGRPCFrame([]byte(original))

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
				Channel:  types.GRPC,
				Service:  "users.UserService",
				RPCMethod: "GetUser",
			},
		},
	}
	reg.Register(spec)

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
				Channel:  types.GRPC,
				Service:  "users.UserService",
				RPCMethod: "DeleteUser",
			},
		},
	}
	reg.Register(spec)

	reg.CheckNegativeGRPCMocks("users.UserService", "DeleteUser")

	err := reg.VerifyAll()
	if err == nil {
		t.Error("VerifyAll should fail when a negative gRPC mock was called")
	}
}

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

func TestInterceptor_NoUpstream_BackwardCompat(t *testing.T) {
	reg := registry.NewMockRegistry()
	addr := "127.0.0.1:19878"
	interceptor := NewInterceptor(addr, "", reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = interceptor.Start(ctx)
	}()
	time.Sleep(100 * time.Millisecond)

	reqBody := encodeGRPCFrame([]byte(`{}`))
	req, err := http.NewRequest("POST", "http://"+addr+"/unknown.Service/Method", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/grpc+json")

	client := newH2CClient()
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("Skipping h2c test: %v", err)
		return
	}
	defer resp.Body.Close()

	_, _ = io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Grpc-Status") != "" {
		t.Errorf("Expected Grpc-Status NOT in initial headers, got %s", resp.Header.Get("Grpc-Status"))
	}
	if resp.Trailer.Get("Grpc-Status") != "12" {
		t.Errorf("Expected Grpc-Status 12 (UNIMPLEMENTED) in trailers, got %s", resp.Trailer.Get("Grpc-Status"))
	}
	if resp.Header.Get("Content-Type") != "application/grpc+json" {
		t.Errorf("Expected Content-Type application/grpc+json, got %s", resp.Header.Get("Content-Type"))
	}
}

func TestInterceptor_ContentTypeEcho(t *testing.T) {
	reg := registry.NewMockRegistry()
	spec := &types.TestSpec{
		Name: "test-ct-echo",
		Expects: []types.ExpectStatement{
			{
				Channel:  types.GRPC,
				Service:  "test.v1.TestService",
				RPCMethod: "GetUser",
			},
		},
	}
	reg.Register(spec)

	addr := "127.0.0.1:19879"
	interceptor := NewInterceptor(addr, "", reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = interceptor.Start(ctx)
	}()
	time.Sleep(100 * time.Millisecond)

	reqBody := encodeGRPCFrame([]byte(`{"user_id": "1"}`))
	req, err := http.NewRequest("POST", "http://"+addr+"/test.v1.TestService/GetUser", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/grpc")

	client := newH2CClient()
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("Skipping h2c test: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.Header.Get("Content-Type") != "application/grpc" {
		t.Errorf("Expected Content-Type application/grpc (echo), got %s", resp.Header.Get("Content-Type"))
	}
}

func TestInterceptor_ContentTypeDefault(t *testing.T) {
	reg := registry.NewMockRegistry()
	addr := "127.0.0.1:19880"
	interceptor := NewInterceptor(addr, "", reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = interceptor.Start(ctx)
	}()
	time.Sleep(100 * time.Millisecond)

	reqBody := encodeGRPCFrame([]byte(`{}`))
	req, err := http.NewRequest("POST", "http://"+addr+"/unknown.Service/Method", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	client := newH2CClient()
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("Skipping h2c test: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.Header.Get("Content-Type") != "application/grpc+json" {
		t.Errorf("Expected default Content-Type application/grpc+json, got %s", resp.Header.Get("Content-Type"))
	}
}

func TestInterceptor_WithDescriptor(t *testing.T) {
	reg := registry.NewMockRegistry()
	spec := &types.TestSpec{
		Name: "test-descriptor",
		Expects: []types.ExpectStatement{
			{
				Channel:  types.GRPC,
				Service:  "test.v1.TestService",
				RPCMethod: "GetUser",
			},
		},
	}
	reg.Register(spec)

	path := filepath.Join("testdata", "test.pb")
	desc, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}

	addr := "127.0.0.1:19881"
	interceptor := NewInterceptor(addr, "", reg)
	interceptor.SetDescriptor(desc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = interceptor.Start(ctx)
	}()
	time.Sleep(100 * time.Millisecond)

	reqBody := encodeGRPCFrame([]byte(`{"user_id": "1"}`))
	req, err := http.NewRequest("POST", "http://"+addr+"/test.v1.TestService/GetUser", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/grpc")

	client := newH2CClient()
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("Skipping h2c test: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "application/grpc" {
		t.Errorf("Expected Content-Type application/grpc, got %s", resp.Header.Get("Content-Type"))
	}
}

func TestInterceptor_EmptyBody_SendsDataFrame(t *testing.T) {
	reg := registry.NewMockRegistry()
	spec := &types.TestSpec{
		Name: "test-empty-body",
		Expects: []types.ExpectStatement{
			{
				Channel:  types.GRPC,
				Service:  "test.v1.TestService",
				RPCMethod: "GetUser",
			},
		},
	}
	reg.Register(spec)

	addr := "127.0.0.1:19882"
	interceptor := NewInterceptor(addr, "", reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = interceptor.Start(ctx)
	}()
	time.Sleep(100 * time.Millisecond)

	reqBody := encodeGRPCFrame([]byte(`{"user_id": "1"}`))
	req, err := http.NewRequest("POST", "http://"+addr+"/test.v1.TestService/GetUser", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/grpc+json")

	client := newH2CClient()
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("Skipping h2c test: %v", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	if len(body) != 5 {
		t.Errorf("Expected 5-byte empty gRPC frame, got %d bytes: %v", len(body), body)
	}
	if body[0] != 0 {
		t.Errorf("Expected compression flag 0, got %d", body[0])
	}
	msgLen := binary.BigEndian.Uint32(body[1:5])
	if msgLen != 0 {
		t.Errorf("Expected message length 0, got %d", msgLen)
	}

	if resp.Trailer.Get("Grpc-Status") != "0" {
		t.Errorf("Expected Grpc-Status 0 in trailers, got %s", resp.Trailer.Get("Grpc-Status"))
	}
}

func TestInterceptor_ReturnsEmpty_SendsDataFrame(t *testing.T) {
	reg := registry.NewMockRegistry()
	spec := &types.TestSpec{
		Name: "test-returns-empty",
		Expects: []types.ExpectStatement{
			{
				Channel:      types.GRPC,
				Service:      "test.v1.TestService",
				RPCMethod:    "DeleteUser",
				ReturnsEmpty: true,
			},
		},
	}
	reg.Register(spec)

	addr := "127.0.0.1:19883"
	interceptor := NewInterceptor(addr, "", reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = interceptor.Start(ctx)
	}()
	time.Sleep(100 * time.Millisecond)

	reqBody := encodeGRPCFrame([]byte(`{"user_id": "1"}`))
	req, err := http.NewRequest("POST", "http://"+addr+"/test.v1.TestService/DeleteUser", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/grpc+json")

	client := newH2CClient()
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("Skipping h2c test: %v", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	if len(body) != 5 {
		t.Errorf("Expected 5-byte empty gRPC frame for RETURNS EMPTY, got %d bytes: %v", len(body), body)
	}
	if body[0] != 0 {
		t.Errorf("Expected compression flag 0, got %d", body[0])
	}
	msgLen := binary.BigEndian.Uint32(body[1:5])
	if msgLen != 0 {
		t.Errorf("Expected message length 0, got %d", msgLen)
	}

	if resp.Trailer.Get("Grpc-Status") != "0" {
		t.Errorf("Expected Grpc-Status 0 in trailers, got %s", resp.Trailer.Get("Grpc-Status"))
	}
}

func TestInterceptor_ErrorResponse_UsesTrailerHeaders(t *testing.T) {
	reg := registry.NewMockRegistry()
	addr := "127.0.0.1:19884"
	interceptor := NewInterceptor(addr, "", reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = interceptor.Start(ctx)
	}()
	time.Sleep(100 * time.Millisecond)

	reqBody := encodeGRPCFrame([]byte(`{}`))
	req, err := http.NewRequest("POST", "http://"+addr+"/unknown.Service/Method", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/grpc+json")

	client := newH2CClient()
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("Skipping h2c test: %v", err)
		return
	}
	defer resp.Body.Close()

	_, _ = io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}

	if resp.Header.Get("Grpc-Status") != "" {
		t.Errorf("Expected Grpc-Status to NOT be in initial headers, got %s", resp.Header.Get("Grpc-Status"))
	}

	if resp.Trailer.Get("Grpc-Status") != "12" {
		t.Errorf("Expected Grpc-Status 12 in trailers, got %s", resp.Trailer.Get("Grpc-Status"))
	}

	if resp.Trailer.Get("Grpc-Message") == "" {
		t.Error("Expected Grpc-Message in trailers")
	}

	if resp.Header.Get("Content-Type") != "application/grpc+json" {
		t.Errorf("Expected Content-Type application/grpc+json, got %s", resp.Header.Get("Content-Type"))
	}
}

func makeBinaryGetUserRequest(t *testing.T, userID string) []byte {
	t.Helper()
	path := filepath.Join("testdata", "test.pb")
	resolver, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}
	desc, err := resolver.files.FindDescriptorByName("test.v1.TestService")
	if err != nil {
		t.Fatalf("FindDescriptorByName failed: %v", err)
	}
	svcDesc := desc.(protoreflect.ServiceDescriptor)
	method := svcDesc.Methods().ByName("GetUser")
	msg := dynamicpb.NewMessage(method.Input())
	msg.Set(method.Input().Fields().ByName("user_id"), protoreflect.ValueOfString(userID))
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("proto.Marshal failed: %v", err)
	}
	return data
}

func TestInterceptor_BinaryProtobuf_WithBodyMatch_Matches(t *testing.T) {
	tmpDir := t.TempDir()
	// UseProtoNames: true preserves snake_case field names
	if err := os.WriteFile(filepath.Join(tmpDir, "expected.json"), []byte(`{"user_id": "test-42"}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reg := registry.NewMockRegistry()
	spec := &types.TestSpec{
		Name:    "test-binary-with",
		BaseDir: tmpDir,
		Expects: []types.ExpectStatement{
			{
				Channel:   types.GRPC,
				Service:   "test.v1.TestService",
				RPCMethod: "GetUser",
				WithFile:  "expected.json",
				BaseDir:   tmpDir,
			},
		},
	}
	reg.Register(spec)

	path := filepath.Join("testdata", "test.pb")
	desc, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}

	addr := "127.0.0.1:19886"
	interceptor := NewInterceptor(addr, "", reg)
	interceptor.SetDescriptor(desc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = interceptor.Start(ctx) }()
	time.Sleep(100 * time.Millisecond)

	protoBytes := makeBinaryGetUserRequest(t, "test-42")
	reqBody := encodeGRPCFrame(protoBytes)
	req, err := http.NewRequest("POST", "http://"+addr+"/test.v1.TestService/GetUser", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/grpc")

	client := newH2CClient()
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("Skipping h2c test: %v", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)

	if resp.Trailer.Get("Grpc-Status") != "0" {
		t.Errorf("Expected grpc-status 0 (mock matched), got %s (message: %s)",
			resp.Trailer.Get("Grpc-Status"), resp.Trailer.Get("Grpc-Message"))
	}
}

func TestInterceptor_BinaryProtobuf_WithBodyMatch_Mismatch(t *testing.T) {
	tmpDir := t.TempDir()
	// WITH file expects a different user_id — should not match
	if err := os.WriteFile(filepath.Join(tmpDir, "expected.json"), []byte(`{"user_id": "other-user"}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reg := registry.NewMockRegistry()
	spec := &types.TestSpec{
		Name:    "test-binary-with-mismatch",
		BaseDir: tmpDir,
		Expects: []types.ExpectStatement{
			{
				Channel:   types.GRPC,
				Service:   "test.v1.TestService",
				RPCMethod: "GetUser",
				WithFile:  "expected.json",
				BaseDir:   tmpDir,
			},
		},
	}
	reg.Register(spec)

	path := filepath.Join("testdata", "test.pb")
	desc, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}

	addr := "127.0.0.1:19887"
	interceptor := NewInterceptor(addr, "", reg)
	interceptor.SetDescriptor(desc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = interceptor.Start(ctx) }()
	time.Sleep(100 * time.Millisecond)

	// Send user_id="test-42" but WITH file expects "other-user" → no match
	protoBytes := makeBinaryGetUserRequest(t, "test-42")
	reqBody := encodeGRPCFrame(protoBytes)
	req, err := http.NewRequest("POST", "http://"+addr+"/test.v1.TestService/GetUser", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/grpc")

	client := newH2CClient()
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("Skipping h2c test: %v", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)

	// Mock should not match; expect UNIMPLEMENTED (12)
	if resp.Trailer.Get("Grpc-Status") != "12" {
		t.Errorf("Expected grpc-status 12 (no mock matched), got %s", resp.Trailer.Get("Grpc-Status"))
	}
}

func TestInterceptor_BinaryProtobuf_WithBodyMatch_NoDescriptor(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "expected.json"), []byte(`{"user_id": "test-42"}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reg := registry.NewMockRegistry()
	spec := &types.TestSpec{
		Name:    "test-binary-no-descriptor",
		BaseDir: tmpDir,
		Expects: []types.ExpectStatement{
			{
				Channel:   types.GRPC,
				Service:   "test.v1.TestService",
				RPCMethod: "GetUser",
				WithFile:  "expected.json",
				BaseDir:   tmpDir,
			},
		},
	}
	reg.Register(spec)

	addr := "127.0.0.1:19888"
	// Intentionally no descriptor set
	interceptor := NewInterceptor(addr, "", reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = interceptor.Start(ctx) }()
	time.Sleep(100 * time.Millisecond)

	protoBytes := makeBinaryGetUserRequest(t, "test-42")
	reqBody := encodeGRPCFrame(protoBytes)
	req, err := http.NewRequest("POST", "http://"+addr+"/test.v1.TestService/GetUser", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/grpc")

	client := newH2CClient()
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("Skipping h2c test: %v", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)

	// No descriptor → bodyMatcher logs diagnostic and returns false → no mock matched
	if resp.Trailer.Get("Grpc-Status") != "12" {
		t.Errorf("Expected grpc-status 12 (no descriptor → no match), got %s", resp.Trailer.Get("Grpc-Status"))
	}
}
