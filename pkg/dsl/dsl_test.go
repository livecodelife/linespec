package dsl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/livecodelife/linespec/pkg/types"
)

func TestLexer_GetUserSuccess(t *testing.T) {
	tokens, err := LexFile("../../examples/user-linespecs/get_user_success.linespec")
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	expectedTokens := []TokenType{
		TokenTest,
		TokenReceive,
		TokenHeaders,
		TokenExpect,
		TokenUsingSql, TokenSqlBlock,
		TokenReturns,
		TokenExpect,
		TokenUsingSql, TokenSqlBlock,
		TokenReturns,
		TokenRespond,
		TokenWith,
		TokenEOF,
	}

	if len(tokens) != len(expectedTokens) {
		t.Errorf("Expected %d tokens, got %d", len(expectedTokens), len(tokens))
		for i, tok := range tokens {
			t.Logf("Token %d: %v", i, tok)
		}
	} else {
		for i, tok := range tokens {
			if tok.Type != expectedTokens[i] {
				t.Errorf("Token %d: expected type %s, got %s", i, expectedTokens[i], tok.Type)
			}
		}
	}
}

func TestParser_GetUserSuccess(t *testing.T) {
	tokens, err := LexFile("../../examples/user-linespecs/get_user_success.linespec")
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	parser := NewParser(tokens)
	spec, err := parser.Parse("get_user_success.linespec")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if spec.Name != "get_user_success" {
		t.Errorf("Expected name get_user_success, got %s", spec.Name)
	}

	if spec.Receive.Method != "GET" {
		t.Errorf("Expected method GET, got %s", spec.Receive.Method)
	}

	if len(spec.Expects) != 2 {
		t.Errorf("Expected 2 expects, got %d", len(spec.Expects))
	}

	if spec.Expects[0].Channel != types.ReadMySQL {
		t.Errorf("Expected channel READ_MYSQL, got %s", spec.Expects[0].Channel)
	}

	if spec.Respond.StatusCode != 200 {
		t.Errorf("Expected status code 200, got %d", spec.Respond.StatusCode)
	}
}

func TestParser_CreateUserSuccess(t *testing.T) {
	tokens, err := LexFile("../../examples/user-linespecs/create_user_success.linespec")
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	parser := NewParser(tokens)
	spec, err := parser.Parse("create_user_success.linespec")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(spec.Expects) != 2 {
		t.Errorf("Expected 2 expects, got %d", len(spec.Expects))
	}

	if spec.Expects[0].Channel != types.ReadMySQL {
		t.Errorf("Expected first channel READ_MYSQL, got %s", spec.Expects[0].Channel)
	}

	if spec.Expects[1].Channel != types.WriteMySQL {
		t.Errorf("Expected second channel WRITE_MYSQL, got %s", spec.Expects[1].Channel)
	}

	if len(spec.Expects[1].Verify) != 2 {
		t.Errorf("Expected 2 verify rules for write expect, got %d", len(spec.Expects[1].Verify))
	}

	if spec.Expects[1].Verify[0].Type != "MATCHES" {
		t.Errorf("Expected first verify type MATCHES, got %s", spec.Expects[1].Verify[0].Type)
	}

	if spec.Expects[1].Verify[1].Type != "NOT_CONTAINS" {
		t.Errorf("Expected second verify type NOT_CONTAINS, got %s", spec.Expects[1].Verify[1].Type)
	}
}

func TestParser_VerifyHTTPHeaders(t *testing.T) {
	// Create a temporary linespec file with HTTP verification
	content := `TEST http-verify-headers
RECEIVE HTTP:POST http://localhost:3000/users

EXPECT HTTP:GET http://user-service.local/users/123
VERIFY headers.Authorization CONTAINS 'Bearer'
VERIFY headers.Content-Type CONTAINS 'application/json'
VERIFY headers.X-Request-ID MATCHES /^[a-f0-9-]+$/

RESPOND HTTP:200`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "http_verify_headers.linespec")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	tokens, err := LexFile(tmpFile)
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	parser := NewParser(tokens)
	spec, err := parser.Parse(tmpFile)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(spec.Expects) != 1 {
		t.Fatalf("Expected 1 expect, got %d", len(spec.Expects))
	}

	expect := spec.Expects[0]
	if len(expect.Verify) != 3 {
		t.Errorf("Expected 3 verify rules, got %d", len(expect.Verify))
	}

	// Check first rule
	if expect.Verify[0].Target != "headers.Authorization" {
		t.Errorf("Expected target 'headers.Authorization', got '%s'", expect.Verify[0].Target)
	}
	if expect.Verify[0].Type != "CONTAINS" {
		t.Errorf("Expected type 'CONTAINS', got '%s'", expect.Verify[0].Type)
	}
	if expect.Verify[0].Pattern != "Bearer" {
		t.Errorf("Expected pattern 'Bearer', got '%s'", expect.Verify[0].Pattern)
	}

	// Check third rule (MATCHES)
	if expect.Verify[2].Target != "headers.X-Request-ID" {
		t.Errorf("Expected target 'headers.X-Request-ID', got '%s'", expect.Verify[2].Target)
	}
	if expect.Verify[2].Type != "MATCHES" {
		t.Errorf("Expected type 'MATCHES', got '%s'", expect.Verify[2].Type)
	}
}

func TestParser_VerifyHTTPBody(t *testing.T) {
	content := `TEST http-verify-body
RECEIVE HTTP:POST http://localhost:3000/users

EXPECT HTTP:POST http://user-service.local/users
VERIFY body CONTAINS 'email'
VERIFY body NOT_CONTAINS 'password'
VERIFY body MATCHES /"name":\s*"[^"]+"/

RESPOND HTTP:201`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "http_verify_body.linespec")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	tokens, err := LexFile(tmpFile)
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	parser := NewParser(tokens)
	spec, err := parser.Parse(tmpFile)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(spec.Expects) != 1 {
		t.Fatalf("Expected 1 expect, got %d", len(spec.Expects))
	}

	expect := spec.Expects[0]
	if len(expect.Verify) != 3 {
		t.Errorf("Expected 3 verify rules, got %d", len(expect.Verify))
	}

	// Check NOT_CONTAINS rule
	if expect.Verify[1].Target != "body" {
		t.Errorf("Expected target 'body', got '%s'", expect.Verify[1].Target)
	}
	if expect.Verify[1].Type != "NOT_CONTAINS" {
		t.Errorf("Expected type 'NOT_CONTAINS', got '%s'", expect.Verify[1].Type)
	}
	if expect.Verify[1].Pattern != "password" {
		t.Errorf("Expected pattern 'password', got '%s'", expect.Verify[1].Pattern)
	}
}

func TestParser_VerifyHTTPURL(t *testing.T) {
	content := `TEST http-verify-url
RECEIVE HTTP:GET http://localhost:3000/users

EXPECT HTTP:GET http://user-service.local/users/123
VERIFY url CONTAINS 'user-service.local'
VERIFY path CONTAINS 'users'
VERIFY path MATCHES /\/users\/\d+$/

RESPOND HTTP:200`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "http_verify_url.linespec")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	tokens, err := LexFile(tmpFile)
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	parser := NewParser(tokens)
	spec, err := parser.Parse(tmpFile)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	expect := spec.Expects[0]
	if len(expect.Verify) != 3 {
		t.Errorf("Expected 3 verify rules, got %d", len(expect.Verify))
	}

	// Check url target
	if expect.Verify[0].Target != "url" {
		t.Errorf("Expected target 'url', got '%s'", expect.Verify[0].Target)
	}

	// Check path targets
	if expect.Verify[1].Target != "path" {
		t.Errorf("Expected target 'path', got '%s'", expect.Verify[1].Target)
	}
}

func TestParser_VerifyKafkaKey(t *testing.T) {
	content := `TEST kafka-verify-key
RECEIVE HTTP:POST http://localhost:3000/users

EXPECT EVENT:todo-events
VERIFY key CONTAINS 'user-'
VERIFY key MATCHES /^user-\d+$/

RESPOND HTTP:201`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "kafka_verify_key.linespec")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	tokens, err := LexFile(tmpFile)
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	parser := NewParser(tokens)
	spec, err := parser.Parse(tmpFile)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	expect := spec.Expects[0]
	if len(expect.Verify) != 2 {
		t.Errorf("Expected 2 verify rules, got %d", len(expect.Verify))
	}

	if expect.Verify[0].Target != "key" {
		t.Errorf("Expected target 'key', got '%s'", expect.Verify[0].Target)
	}
	if expect.Verify[0].Type != "CONTAINS" {
		t.Errorf("Expected type 'CONTAINS', got '%s'", expect.Verify[0].Type)
	}
}

func TestParser_VerifyKafkaValue(t *testing.T) {
	content := `TEST kafka-verify-value
RECEIVE HTTP:POST http://localhost:3000/users

EXPECT EVENT:todo-events
VERIFY value CONTAINS 'event'
VERIFY value NOT_CONTAINS 'password'
VERIFY value MATCHES /"user_id":\s*\d+/

RESPOND HTTP:201`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "kafka_verify_value.linespec")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	tokens, err := LexFile(tmpFile)
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	parser := NewParser(tokens)
	spec, err := parser.Parse(tmpFile)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	expect := spec.Expects[0]
	if len(expect.Verify) != 3 {
		t.Errorf("Expected 3 verify rules, got %d", len(expect.Verify))
	}

	if expect.Verify[0].Target != "value" {
		t.Errorf("Expected target 'value', got '%s'", expect.Verify[0].Target)
	}
}

func TestParser_VerifyKafkaHeaders(t *testing.T) {
	content := `TEST kafka-verify-headers
RECEIVE HTTP:POST http://localhost:3000/users

EXPECT EVENT:todo-events
VERIFY headers.X-Event-Type CONTAINS 'user_created'
VERIFY headers.X-Source CONTAINS 'api'
VERIFY headers.X-Correlation-ID MATCHES /^[a-f0-9-]+$/

RESPOND HTTP:201`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "kafka_verify_headers.linespec")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	tokens, err := LexFile(tmpFile)
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	parser := NewParser(tokens)
	spec, err := parser.Parse(tmpFile)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	expect := spec.Expects[0]
	if len(expect.Verify) != 3 {
		t.Errorf("Expected 3 verify rules, got %d", len(expect.Verify))
	}

	if expect.Verify[0].Target != "headers.X-Event-Type" {
		t.Errorf("Expected target 'headers.X-Event-Type', got '%s'", expect.Verify[0].Target)
	}
	if expect.Verify[1].Target != "headers.X-Source" {
		t.Errorf("Expected target 'headers.X-Source', got '%s'", expect.Verify[1].Target)
	}
}

func TestParser_VerifyMixedTargets(t *testing.T) {
	// Test that SQL, HTTP, and Kafka verify rules can all be parsed
	content := `TEST mixed-verify
RECEIVE HTTP:POST http://localhost:3000/users

EXPECT WRITE:MYSQL users
VERIFY query CONTAINS 'INSERT'
VERIFY query MATCHES /\bpassword_digest\b/

EXPECT HTTP:POST http://user-service.local/users
VERIFY headers.Authorization CONTAINS 'Bearer'
VERIFY body CONTAINS 'email'

EXPECT EVENT:todo-events
VERIFY key CONTAINS 'user-'
VERIFY value CONTAINS 'created'

RESPOND HTTP:201`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "mixed_verify.linespec")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	tokens, err := LexFile(tmpFile)
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	parser := NewParser(tokens)
	spec, err := parser.Parse(tmpFile)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(spec.Expects) != 3 {
		t.Fatalf("Expected 3 expects, got %d", len(spec.Expects))
	}

	// Check SQL expect
	sqlExpect := spec.Expects[0]
	if len(sqlExpect.Verify) != 2 {
		t.Errorf("Expected 2 SQL verify rules, got %d", len(sqlExpect.Verify))
	}
	if sqlExpect.Verify[0].Target != "query" {
		t.Errorf("Expected SQL target 'query', got '%s'", sqlExpect.Verify[0].Target)
	}

	// Check HTTP expect
	httpExpect := spec.Expects[1]
	if len(httpExpect.Verify) != 2 {
		t.Errorf("Expected 2 HTTP verify rules, got %d", len(httpExpect.Verify))
	}
	if httpExpect.Verify[0].Target != "headers.Authorization" {
		t.Errorf("Expected HTTP target 'headers.Authorization', got '%s'", httpExpect.Verify[0].Target)
	}

	// Check Kafka expect
	kafkaExpect := spec.Expects[2]
	if len(kafkaExpect.Verify) != 2 {
		t.Errorf("Expected 2 Kafka verify rules, got %d", len(kafkaExpect.Verify))
	}
	if kafkaExpect.Verify[0].Target != "key" {
		t.Errorf("Expected Kafka target 'key', got '%s'", kafkaExpect.Verify[0].Target)
	}
}

func TestParser_InvalidVerifySyntax(t *testing.T) {
	// Test that invalid VERIFY syntax returns an error
	content := `TEST invalid-verify
RECEIVE HTTP:POST http://localhost:3000/users

EXPECT HTTP:GET http://user-service.local/users
VERIFY invalid_target CONTAINS 'test'

RESPOND HTTP:200`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "invalid_verify.linespec")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	tokens, err := LexFile(tmpFile)
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	parser := NewParser(tokens)
	_, err = parser.Parse(tmpFile)
	if err == nil {
		t.Error("Expected parse to fail with invalid VERIFY syntax, but it succeeded")
	}

	if !strings.Contains(err.Error(), "VERIFY") || !strings.Contains(err.Error(), "Invalid") {
		t.Errorf("Expected error message to mention 'Invalid VERIFY', got: %v", err)
	}
}

func TestParser_ReceiveKafka(t *testing.T) {
	content := `TEST process_todo_created_event

RECEIVE KAFKA:todo-events
WITH {{payloads/todo_created_event.yaml}}

EXPECT WRITE:POSTGRESQL notifications
WITH {{payloads/notification_insert.yaml}}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "process_todo_created_event.linespec")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	tokens, err := LexFile(tmpFile)
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	parser := NewParser(tokens)
	spec, err := parser.Parse(tmpFile)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if spec.Receive.Channel != types.Event {
		t.Errorf("Expected Receive.Channel EVENT, got %s", spec.Receive.Channel)
	}
	if spec.Receive.Topic != "todo-events" {
		t.Errorf("Expected Receive.Topic 'todo-events', got %s", spec.Receive.Topic)
	}
	if spec.Receive.WithFile != "payloads/todo_created_event.yaml" {
		t.Errorf("Expected Receive.WithFile, got %s", spec.Receive.WithFile)
	}
	// RESPOND is absent — StatusCode should be zero.
	if spec.Respond.StatusCode != 0 {
		t.Errorf("Expected zero StatusCode for consumer test, got %d", spec.Respond.StatusCode)
	}
	if len(spec.Expects) != 1 || spec.Expects[0].Channel != types.WritePostgreSQL {
		t.Errorf("Expected 1 WRITE:POSTGRESQL expect, got %v", spec.Expects)
	}
}

func TestParser_ReceiveKafkaEventAlias(t *testing.T) {
	// Both KAFKA: and EVENT: prefixes should work.
	content := `TEST event_alias_test

RECEIVE EVENT:my-topic

EXPECT WRITE:MYSQL results

RESPOND HTTP:200`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "event_alias_test.linespec")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	tokens, err := LexFile(tmpFile)
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	parser := NewParser(tokens)
	spec, err := parser.Parse(tmpFile)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if spec.Receive.Channel != types.Event {
		t.Errorf("Expected Channel EVENT, got %s", spec.Receive.Channel)
	}
	if spec.Receive.Topic != "my-topic" {
		t.Errorf("Expected topic 'my-topic', got %s", spec.Receive.Topic)
	}
}

func TestParser_HTTPRequiresRespond(t *testing.T) {
	// HTTP-triggered tests without a RESPOND block should fail.
	content := `TEST missing_respond

RECEIVE HTTP:GET http://localhost/api

EXPECT WRITE:MYSQL items`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "missing_respond.linespec")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	tokens, err := LexFile(tmpFile)
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	parser := NewParser(tokens)
	_, err = parser.Parse(tmpFile)
	if err == nil {
		t.Error("Expected parse error for HTTP test missing RESPOND block")
	}
}

func TestParser_VerifyRulePreservesTarget(t *testing.T) {
	// Create a simple linespec to verify the Target field is preserved
	content := `TEST preserve-target
RECEIVE HTTP:GET http://localhost:3000/users

EXPECT WRITE:MYSQL users
VERIFY query CONTAINS 'INSERT'

RESPOND HTTP:200`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "preserve_target.linespec")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	tokens, err := LexFile(tmpFile)
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	parser := NewParser(tokens)
	spec, err := parser.Parse(tmpFile)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(spec.Expects) != 1 {
		t.Fatalf("Expected 1 expect, got %d", len(spec.Expects))
	}

	expect := spec.Expects[0]
	if len(expect.Verify) != 1 {
		t.Fatalf("Expected 1 verify rule, got %d", len(expect.Verify))
	}

	verifyRule := expect.Verify[0]
	if verifyRule.Target != "query" {
		t.Errorf("Expected Target to be 'query', got '%s'", verifyRule.Target)
	}
	if verifyRule.Type != "CONTAINS" {
		t.Errorf("Expected Type to be 'CONTAINS', got '%s'", verifyRule.Type)
	}
	if verifyRule.Pattern != "INSERT" {
		t.Errorf("Expected Pattern to be 'INSERT', got '%s'", verifyRule.Pattern)
	}
}

func TestParser_ResponseHeadersOnExpect(t *testing.T) {
	content := `TEST response-headers
RECEIVE HTTP:GET http://localhost:3000/items

EXPECT HTTP:GET http://dependency.local/items
RETURNS {{payloads/items.yaml}}
RESPONSE_HEADERS
  Content-Type: application/yaml
  X-Custom: my-value

RESPOND HTTP:200`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "response_headers.linespec")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	tokens, err := LexFile(tmpFile)
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	parser := NewParser(tokens)
	spec, err := parser.Parse(tmpFile)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(spec.Expects) != 1 {
		t.Fatalf("Expected 1 expect, got %d", len(spec.Expects))
	}

	expect := spec.Expects[0]
	if expect.ReturnsFile != "payloads/items.yaml" {
		t.Errorf("Expected ReturnsFile='payloads/items.yaml', got %q", expect.ReturnsFile)
	}
	if expect.ResponseHeaders == nil {
		t.Fatal("Expected ResponseHeaders to be set")
	}
	if expect.ResponseHeaders["Content-Type"] != "application/yaml" {
		t.Errorf("Expected Content-Type='application/yaml', got %q", expect.ResponseHeaders["Content-Type"])
	}
	if expect.ResponseHeaders["X-Custom"] != "my-value" {
		t.Errorf("Expected X-Custom='my-value', got %q", expect.ResponseHeaders["X-Custom"])
	}
}

func TestParser_HeadersOnRespond(t *testing.T) {
	content := `TEST respond-headers
RECEIVE HTTP:GET http://localhost:3000/items

EXPECT WRITE:MYSQL items

RESPOND HTTP:201
WITH {{payloads/result.json}}
HEADERS
  X-Request-ID: abc123`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "respond_headers.linespec")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	tokens, err := LexFile(tmpFile)
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	parser := NewParser(tokens)
	spec, err := parser.Parse(tmpFile)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if spec.Respond.StatusCode != 201 {
		t.Errorf("Expected status 201, got %d", spec.Respond.StatusCode)
	}
	if spec.Respond.Headers == nil {
		t.Fatal("Expected Respond.Headers to be set")
	}
	if spec.Respond.Headers["X-Request-ID"] != "abc123" {
		t.Errorf("Expected X-Request-ID='abc123', got %q", spec.Respond.Headers["X-Request-ID"])
	}
}
