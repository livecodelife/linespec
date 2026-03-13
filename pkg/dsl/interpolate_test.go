package dsl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/livecodelife/linespec/pkg/interpolate"
	"github.com/livecodelife/linespec/pkg/types"
)

func TestParserWithResolver_ReceivePath(t *testing.T) {
	input := `RECEIVE HTTP:POST /api/${API_VERSION}/users
RESPOND HTTP:201`

	resolver := interpolate.NewResolver()
	tokens, err := LexString(input)
	if err != nil {
		t.Fatalf("LexString failed: %v", err)
	}

	parser := NewParserWithResolver(tokens, resolver)
	spec, err := parser.Parse("test.linespec")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// The path should have the variable resolved
	if !strings.Contains(spec.Receive.Path, "api_") {
		t.Errorf("Expected path to contain resolved variable, got: %s", spec.Receive.Path)
	}

	// Check that the variable was recorded in the resolver
	if _, exists := resolver.Variables["API_VERSION"]; !exists {
		t.Error("API_VERSION should be in resolver variables")
	}
}

func TestParserWithResolver_Headers(t *testing.T) {
	input := `RECEIVE HTTP:GET /test
HEADERS
  Authorization: Bearer ${AUTH_TOKEN}
  X-API-Key: ${API_KEY}
RESPOND HTTP:200`

	resolver := interpolate.NewResolver()
	tokens, err := LexString(input)
	if err != nil {
		t.Fatalf("LexString failed: %v", err)
	}

	parser := NewParserWithResolver(tokens, resolver)
	spec, err := parser.Parse("test.linespec")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Headers should be resolved
	authHeader := spec.Receive.Headers["Authorization"]
	if authHeader == "Bearer ${AUTH_TOKEN}" {
		t.Error("Authorization header was not resolved")
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		t.Errorf("Authorization header should start with 'Bearer ', got: %s", authHeader)
	}

	apiKey := spec.Receive.Headers["X-API-Key"]
	if apiKey == "${API_KEY}" {
		t.Error("X-API-Key header was not resolved")
	}
}

func TestParserWithResolver_ExpectURL(t *testing.T) {
	input := `RECEIVE HTTP:GET /test
EXPECT HTTP:GET https://api.${DOMAIN}.com/users
RESPOND HTTP:200`

	resolver := interpolate.NewResolver()
	tokens, err := LexString(input)
	if err != nil {
		t.Fatalf("LexString failed: %v", err)
	}

	parser := NewParserWithResolver(tokens, resolver)
	spec, err := parser.Parse("test.linespec")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(spec.Expects) != 1 {
		t.Fatalf("Expected 1 expect, got %d", len(spec.Expects))
	}

	expect := spec.Expects[0]
	if expect.Channel != types.HTTP {
		t.Errorf("Expected HTTP channel, got %s", expect.Channel)
	}

	// URL should have the variable resolved
	if strings.Contains(expect.URL, "${DOMAIN}") {
		t.Errorf("URL should not contain unresolved variable: %s", expect.URL)
	}
	if !strings.Contains(expect.URL, "api.") {
		t.Errorf("URL should contain 'api.' prefix: %s", expect.URL)
	}
}

func TestParserWithResolver_ExpectHeaders(t *testing.T) {
	input := `RECEIVE HTTP:GET /test
EXPECT HTTP:POST https://example.com/webhook
HEADERS
  X-Secret: ${WEBHOOK_SECRET}
RESPOND HTTP:200`

	resolver := interpolate.NewResolver()
	tokens, err := LexString(input)
	if err != nil {
		t.Fatalf("LexString failed: %v", err)
	}

	parser := NewParserWithResolver(tokens, resolver)
	spec, err := parser.Parse("test.linespec")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(spec.Expects) != 1 {
		t.Fatalf("Expected 1 expect, got %d", len(spec.Expects))
	}

	expect := spec.Expects[0]
	secret := expect.Headers["X-Secret"]
	if secret == "${WEBHOOK_SECRET}" {
		t.Error("X-Secret header was not resolved")
	}
	if !strings.HasPrefix(secret, "webhook_secret_") {
		t.Errorf("X-Secret should have generated prefix, got: %s", secret)
	}
}

func TestParserWithResolver_SQL(t *testing.T) {
	// SQL blocks need proper triple-quote formatting with closing quotes on separate line
	input := "RECEIVE HTTP:GET /test\nEXPECT READ:MYSQL users\nUSING_SQL \"\"\"\nSELECT * FROM users WHERE api_key = '${API_KEY}'\n\"\"\"\nRETURNS EMPTY\nRESPOND HTTP:200"

	resolver := interpolate.NewResolver()
	tokens, err := LexString(input)
	if err != nil {
		t.Fatalf("LexString failed: %v", err)
	}

	parser := NewParserWithResolver(tokens, resolver)
	spec, err := parser.Parse("test.linespec")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(spec.Expects) != 1 {
		t.Fatalf("Expected 1 expect, got %d", len(spec.Expects))
	}

	expect := spec.Expects[0]
	if strings.Contains(expect.SQL, "${API_KEY}") {
		t.Errorf("SQL should not contain unresolved variable: %s", expect.SQL)
	}
	if !strings.Contains(expect.SQL, "api_key_") {
		t.Errorf("SQL should contain resolved api_key value: %s", expect.SQL)
	}
}

func TestPayloadLoaderWithResolver_JSON(t *testing.T) {
	// Create a temp directory with a test JSON file
	tempDir := t.TempDir()
	jsonContent := `{"token": "${API_TOKEN}", "user_id": 123}`
	jsonPath := filepath.Join(tempDir, "test.json")
	if err := os.WriteFile(jsonPath, []byte(jsonContent), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	resolver := interpolate.NewResolver()
	loader := NewPayloadLoaderWithResolver(tempDir, resolver)

	result, err := loader.Load("test.json")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Result should be a map
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected map result, got %T", result)
	}

	token := m["token"].(string)
	if token == "${API_TOKEN}" {
		t.Error("Token was not resolved")
	}
	if !strings.HasPrefix(token, "api_token_") {
		t.Errorf("Token should have generated prefix, got: %s", token)
	}
}

func TestPayloadLoaderWithResolver_YAML(t *testing.T) {
	// Create a temp directory with a test YAML file
	tempDir := t.TempDir()
	yamlContent := `name: Test User
api_key: ${API_KEY}
endpoint: https://api.${DOMAIN}.com`
	yamlPath := filepath.Join(tempDir, "test.yaml")
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	resolver := interpolate.NewResolver()
	loader := NewPayloadLoaderWithResolver(tempDir, resolver)

	result, err := loader.Load("test.yaml")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Result should be a map
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected map result, got %T", result)
	}

	apiKey := m["api_key"].(string)
	if apiKey == "${API_KEY}" {
		t.Error("api_key was not resolved")
	}

	endpoint := m["endpoint"].(string)
	if strings.Contains(endpoint, "${DOMAIN}") {
		t.Errorf("endpoint should not contain unresolved variable: %s", endpoint)
	}
}

func TestPayloadLoaderWithoutResolver(t *testing.T) {
	// Create a temp directory with a test JSON file
	tempDir := t.TempDir()
	jsonContent := `{"token": "${API_TOKEN}"}`
	jsonPath := filepath.Join(tempDir, "test.json")
	if err := os.WriteFile(jsonPath, []byte(jsonContent), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Loader without resolver
	loader := NewPayloadLoader(tempDir)

	result, err := loader.Load("test.json")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Result should still contain the unresolved variable
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected map result, got %T", result)
	}

	token := m["token"].(string)
	if token != "${API_TOKEN}" {
		t.Errorf("Token should not be resolved without resolver, got: %s", token)
	}
}

// LexString is a helper to lex a string instead of a file
func LexString(input string) ([]Token, error) {
	// Write to temp file and lex
	tempFile := filepath.Join(os.TempDir(), "test_linespec.tmp")
	if err := os.WriteFile(tempFile, []byte(input), 0644); err != nil {
		return nil, err
	}
	defer os.Remove(tempFile)
	return LexFile(tempFile)
}
