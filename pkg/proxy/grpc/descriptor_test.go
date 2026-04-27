package grpc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestLoadDescriptorSet_Valid(t *testing.T) {
	path := filepath.Join("testdata", "test.pb")
	resolver, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}
	if resolver == nil {
		t.Fatal("Expected non-nil resolver")
	}
	if resolver.files == nil {
		t.Fatal("Expected non-nil files")
	}
}

func TestLoadDescriptorSet_InvalidPath(t *testing.T) {
	_, err := LoadDescriptorSet("nonexistent.pb")
	if err == nil {
		t.Fatal("Expected error for nonexistent file")
	}
}

func TestLoadDescriptorSet_InvalidData(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "invalid-*.pb")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString("this is not a valid protobuf descriptor set")
	tmpFile.Close()

	_, err = LoadDescriptorSet(tmpFile.Name())
	if err == nil {
		t.Fatal("Expected error for invalid descriptor data")
	}
}

func TestHasDescriptor_Found(t *testing.T) {
	path := filepath.Join("testdata", "test.pb")
	resolver, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}
	if !resolver.HasDescriptor("test.v1.TestService", "GetUser") {
		t.Error("Expected HasDescriptor to return true for test.v1.TestService/GetUser")
	}
	if !resolver.HasDescriptor("test.v1.TestService", "CreateUser") {
		t.Error("Expected HasDescriptor to return true for test.v1.TestService/CreateUser")
	}
	if !resolver.HasDescriptor("test.v1.TestService", "DeleteUser") {
		t.Error("Expected HasDescriptor to return true for test.v1.TestService/DeleteUser")
	}
}

func TestHasDescriptor_NotFound(t *testing.T) {
	path := filepath.Join("testdata", "test.pb")
	resolver, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}
	if resolver.HasDescriptor("test.v1.NonExistent", "GetUser") {
		t.Error("Expected HasDescriptor to return false for unknown service")
	}
	if resolver.HasDescriptor("test.v1.TestService", "NonExistent") {
		t.Error("Expected HasDescriptor to return false for unknown method")
	}
}

func TestHasDescriptor_NilResolver(t *testing.T) {
	var d *DescriptorResolver
	if d.HasDescriptor("test.v1.TestService", "GetUser") {
		t.Error("Expected HasDescriptor to return false for nil resolver")
	}
}

func TestJSONToProtobuf(t *testing.T) {
	path := filepath.Join("testdata", "test.pb")
	resolver, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}

	jsonData := []byte(`{"id": "42", "email": "test@example.com", "name": "Test User"}`)
	protoBytes, err := resolver.JSONToProtobuf("test.v1.TestService", "GetUser", jsonData)
	if err != nil {
		t.Fatalf("JSONToProtobuf failed: %v", err)
	}
	if len(protoBytes) == 0 {
		t.Fatal("Expected non-empty protobuf output")
	}
}

func TestJSONToProtobuf_UnknownService(t *testing.T) {
	path := filepath.Join("testdata", "test.pb")
	resolver, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}

	_, err = resolver.JSONToProtobuf("test.v1.NonExistent", "GetUser", []byte(`{}`))
	if err == nil {
		t.Fatal("Expected error for unknown service")
	}
}

func TestJSONToProtobuf_UnknownMethod(t *testing.T) {
	path := filepath.Join("testdata", "test.pb")
	resolver, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}

	_, err = resolver.JSONToProtobuf("test.v1.TestService", "NonExistent", []byte(`{}`))
	if err == nil {
		t.Fatal("Expected error for unknown method")
	}
}

func TestJSONToProtobuf_InvalidJSON(t *testing.T) {
	path := filepath.Join("testdata", "test.pb")
	resolver, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}

	_, err = resolver.JSONToProtobuf("test.v1.TestService", "GetUser", []byte(`not json`))
	if err == nil {
		t.Fatal("Expected error for invalid JSON")
	}
}

func TestJSONToProtobuf_NilResolver(t *testing.T) {
	var d *DescriptorResolver
	_, err := d.JSONToProtobuf("test.v1.TestService", "GetUser", []byte(`{}`))
	if err == nil {
		t.Fatal("Expected error for nil resolver")
	}
}

func TestJSONToProtobuf_EmptyMessage(t *testing.T) {
	path := filepath.Join("testdata", "test.pb")
	resolver, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}

	protoBytes, err := resolver.JSONToProtobuf("test.v1.TestService", "DeleteUser", []byte(`{}`))
	if err != nil {
		t.Fatalf("JSONToProtobuf failed for empty message: %v", err)
	}
	if len(protoBytes) != 0 {
		t.Errorf("Expected 0 bytes for empty protobuf message, got %d", len(protoBytes))
	}
}

func TestProtobufToJSON_NilResolver(t *testing.T) {
	var d *DescriptorResolver
	_, err := d.ProtobufToJSON("test.v1.TestService", "GetUser", []byte{})
	if err == nil {
		t.Fatal("Expected error for nil resolver")
	}
}

func TestProtobufToJSON_UnknownService(t *testing.T) {
	path := filepath.Join("testdata", "test.pb")
	resolver, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}
	_, err = resolver.ProtobufToJSON("test.v1.NonExistent", "GetUser", []byte{})
	if err == nil {
		t.Fatal("Expected error for unknown service")
	}
}

func TestProtobufToJSON_UnknownMethod(t *testing.T) {
	path := filepath.Join("testdata", "test.pb")
	resolver, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}
	_, err = resolver.ProtobufToJSON("test.v1.TestService", "NonExistent", []byte{})
	if err == nil {
		t.Fatal("Expected error for unknown method")
	}
}

func TestProtobufToJSON_InvalidBytes(t *testing.T) {
	path := filepath.Join("testdata", "test.pb")
	resolver, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}
	_, err = resolver.ProtobufToJSON("test.v1.TestService", "GetUser", []byte("this is not protobuf \xff\xfe"))
	if err == nil {
		t.Fatal("Expected error for invalid protobuf bytes")
	}
}

func TestProtobufToJSON_EmptyMessage(t *testing.T) {
	path := filepath.Join("testdata", "test.pb")
	resolver, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}
	// DeleteUserRequest has no fields, so empty bytes decode to {}
	jsonData, err := resolver.ProtobufToJSON("test.v1.TestService", "DeleteUser", []byte{})
	if err != nil {
		t.Fatalf("ProtobufToJSON failed: %v", err)
	}
	var result map[string]interface{}
	if jsonErr := json.Unmarshal(jsonData, &result); jsonErr != nil {
		t.Fatalf("Expected valid JSON, got: %s", string(jsonData))
	}
}

func TestProtobufToJSON_RoundTrip(t *testing.T) {
	path := filepath.Join("testdata", "test.pb")
	resolver, err := LoadDescriptorSet(path)
	if err != nil {
		t.Fatalf("LoadDescriptorSet failed: %v", err)
	}

	// Build a GetUserRequest{user_id: "test-42"} using dynamic messages
	desc, err := resolver.files.FindDescriptorByName("test.v1.TestService")
	if err != nil {
		t.Fatalf("FindDescriptorByName failed: %v", err)
	}
	svcDesc := desc.(protoreflect.ServiceDescriptor)
	methodDesc := svcDesc.Methods().ByName("GetUser")
	if methodDesc == nil {
		t.Fatal("GetUser method not found")
	}
	inputMsg := dynamicpb.NewMessage(methodDesc.Input())
	userIDField := methodDesc.Input().Fields().ByName("user_id")
	if userIDField == nil {
		t.Fatal("user_id field not found on GetUserRequest")
	}
	inputMsg.Set(userIDField, protoreflect.ValueOfString("test-42"))
	protoBytes, err := proto.Marshal(inputMsg)
	if err != nil {
		t.Fatalf("proto.Marshal failed: %v", err)
	}

	jsonData, err := resolver.ProtobufToJSON("test.v1.TestService", "GetUser", protoBytes)
	if err != nil {
		t.Fatalf("ProtobufToJSON failed: %v", err)
	}

	var result map[string]interface{}
	if jsonErr := json.Unmarshal(jsonData, &result); jsonErr != nil {
		t.Fatalf("Expected valid JSON, got: %s", string(jsonData))
	}
	// UseProtoNames: true preserves snake_case field names
	if result["user_id"] != "test-42" {
		t.Errorf("Expected user_id=test-42, got %v (full JSON: %s)", result["user_id"], string(jsonData))
	}
}
