package grpc

import (
	"os"
	"path/filepath"
	"testing"
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
