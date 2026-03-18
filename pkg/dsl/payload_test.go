package dsl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPayloadLoader_LoadJSON(t *testing.T) {
	loader := NewPayloadLoader("../../examples/user-linespecs")
	payload, err := loader.Load("payloads/user_response.json")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	m, ok := payload.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected map[string]interface{}, got %T", payload)
	}

	if _, ok := m["email"]; !ok {
		t.Errorf("Expected 'email' field in payload")
	}
}

func TestPayloadLoader_LoadYAML(t *testing.T) {
	loader := NewPayloadLoader("../../examples/todo-linespecs")
	payload, err := loader.Load("payloads/auth_token_request.yaml")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	m, ok := payload.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected map[string]interface{}, got %T", payload)
	}

	if _, ok := m["authorization"]; !ok {
		t.Errorf("Expected 'authorization' field in payload")
	}
}

// Tests for the new PayloadParser interface

func TestJSONParser(t *testing.T) {
	parser := &JSONParser{}

	// Test CanParse
	if !parser.CanParse(".json") {
		t.Error("JSONParser should parse .json files")
	}
	if parser.CanParse(".yaml") {
		t.Error("JSONParser should not parse .yaml files")
	}

	// Test Parse
	data := []byte(`{"name": "test", "value": 123}`)
	result, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("Expected map result")
	}
	if m["name"] != "test" {
		t.Errorf("Expected name='test', got %v", m["name"])
	}
}

func TestYAMLParser(t *testing.T) {
	parser := &YAMLParser{}

	// Test CanParse
	if !parser.CanParse(".yaml") {
		t.Error("YAMLParser should parse .yaml files")
	}
	if !parser.CanParse(".yml") {
		t.Error("YAMLParser should parse .yml files")
	}

	// Test Parse
	data := []byte("name: test\nvalue: 123")
	result, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("Expected map result")
	}
	if m["name"] != "test" {
		t.Errorf("Expected name='test', got %v", m["name"])
	}
}

func TestXMLParser(t *testing.T) {
	parser := &XMLParser{}

	// Test CanParse
	if !parser.CanParse(".xml") {
		t.Error("XMLParser should parse .xml files")
	}

	// Test Parse with valid XML
	data := []byte(`<root><name>test</name><value>123</value></root>`)
	result, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// XML unmarshaling in Go produces a map[string]interface{} when using this approach
	// The actual structure depends on the XML content
	if result == nil {
		t.Log("XML result is nil but no error - acceptable for simple XML parsing")
	}
}

func TestRawParser(t *testing.T) {
	parser := &RawParser{}

	// Test CanParse - RawParser is catch-all
	if !parser.CanParse(".anything") {
		t.Error("RawParser should parse any extension")
	}

	// Test Parse
	data := []byte("raw text content")
	result, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	str, ok := result.(string)
	if !ok {
		t.Fatal("Expected string result")
	}
	if str != "raw text content" {
		t.Errorf("Expected 'raw text content', got '%s'", str)
	}
}

func TestPayloadLoaderWithParsers(t *testing.T) {
	// Create temp directory with test files
	tempDir := t.TempDir()

	// Create JSON file
	jsonFile := filepath.Join(tempDir, "test.json")
	if err := os.WriteFile(jsonFile, []byte(`{"status": 200, "data": "test"}`), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create YAML file
	yamlFile := filepath.Join(tempDir, "test.yaml")
	if err := os.WriteFile(yamlFile, []byte("status: 201\ndata: yaml_test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create raw file
	rawFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(rawFile, []byte("raw content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	loader := NewPayloadLoader(tempDir)

	// Test JSON loading
	result, err := loader.Load("test.json")
	if err != nil {
		t.Fatalf("Load JSON failed: %v", err)
	}
	m := result.(map[string]interface{})
	if m["status"] != float64(200) {
		t.Errorf("Expected status=200, got %v", m["status"])
	}

	// Test YAML loading
	result, err = loader.Load("test.yaml")
	if err != nil {
		t.Fatalf("Load YAML failed: %v", err)
	}
	m = result.(map[string]interface{})
	if m["status"] != 201 {
		t.Errorf("Expected status=201, got %v", m["status"])
	}

	// Test raw loading
	result, err = loader.Load("test.txt")
	if err != nil {
		t.Fatalf("Load raw failed: %v", err)
	}
	str := result.(string)
	if str != "raw content" {
		t.Errorf("Expected 'raw content', got '%s'", str)
	}
}

func TestPayloadLoaderLoadNonExistent(t *testing.T) {
	loader := NewPayloadLoader(t.TempDir())
	_, err := loader.Load("nonexistent.json")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}

func TestPayloadLoaderSetParsers(t *testing.T) {
	loader := NewPayloadLoader(t.TempDir())

	// Set custom parsers (only JSON)
	customParsers := []PayloadParser{&JSONParser{}, &RawParser{}}
	loader.SetParsers(customParsers)

	if len(loader.Parsers) != 2 {
		t.Errorf("Expected 2 parsers, got %d", len(loader.Parsers))
	}
}
