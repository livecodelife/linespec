package dsl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		TokenUsingSqlContains, TokenSqlBlock,
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

func TestParser_ExpectGRPC(t *testing.T) {
	content := `TEST get-user-grpc
RECEIVE HTTP:GET http://localhost:3000/health

EXPECT GRPC:users.UserService/GetUser
WITH {{payloads/get-user-request.json}}
RETURNS {{payloads/get-user-response.json}}

RESPOND HTTP:200
WITH {{payloads/health.json}}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "get_user_grpc.linespec")
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
		t.Fatalf("Expected 1 EXPECT, got %d", len(spec.Expects))
	}

	expect := spec.Expects[0]
	if expect.Channel != types.GRPC {
		t.Errorf("Expected channel GRPC, got %s", expect.Channel)
	}
	if expect.Service != "users.UserService" {
		t.Errorf("Expected service 'users.UserService', got %q", expect.Service)
	}
	if expect.RPCMethod != "GetUser" {
		t.Errorf("Expected method 'GetUser', got %q", expect.RPCMethod)
	}
	if expect.WithFile != "payloads/get-user-request.json" {
		t.Errorf("Unexpected WithFile: %q", expect.WithFile)
	}
	if expect.ReturnsFile != "payloads/get-user-response.json" {
		t.Errorf("Unexpected ReturnsFile: %q", expect.ReturnsFile)
	}
}

func TestParser_ExpectGRPC_DotPackage(t *testing.T) {
	content := `TEST create-order-grpc
RECEIVE HTTP:POST http://localhost:3000/orders

EXPECT GRPC:com.example.orders.OrderService/CreateOrder

RESPOND HTTP:201
WITH {{payloads/order.json}}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "create_order_grpc.linespec")
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
	if expect.Channel != types.GRPC {
		t.Errorf("Expected GRPC channel, got %s", expect.Channel)
	}
	if expect.Service != "com.example.orders.OrderService" {
		t.Errorf("Expected full package service, got %q", expect.Service)
	}
	if expect.RPCMethod != "CreateOrder" {
		t.Errorf("Expected method 'CreateOrder', got %q", expect.RPCMethod)
	}
}

func TestParser_ExpectReadRedis(t *testing.T) {
	content := `TEST get-cached-user
RECEIVE HTTP:GET http://localhost:3000/users/123

EXPECT READ:REDIS GET user:123
RETURNS {{payloads/cached-user.json}}

RESPOND HTTP:200
WITH {{payloads/user.json}}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "get_cached_user.linespec")
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
		t.Fatalf("Expected 1 EXPECT, got %d", len(spec.Expects))
	}

	expect := spec.Expects[0]
	if expect.Channel != types.ReadRedis {
		t.Errorf("Expected channel READ_REDIS, got %s", expect.Channel)
	}
	if expect.Command != "GET" {
		t.Errorf("Expected command 'GET', got %q", expect.Command)
	}
	if expect.RedisKey != "user:123" {
		t.Errorf("Expected key 'user:123', got %q", expect.RedisKey)
	}
	if expect.ReturnsFile != "payloads/cached-user.json" {
		t.Errorf("Unexpected ReturnsFile: %q", expect.ReturnsFile)
	}
}

func TestParser_ExpectWriteRedis(t *testing.T) {
	content := `TEST cache-session
RECEIVE HTTP:POST http://localhost:3000/sessions

EXPECT WRITE:REDIS SET session:abc
WITH {{payloads/session-data.json}}

RESPOND HTTP:201
WITH {{payloads/session.json}}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "cache_session.linespec")
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
	if expect.Channel != types.WriteRedis {
		t.Errorf("Expected channel WRITE_REDIS, got %s", expect.Channel)
	}
	if expect.Command != "SET" {
		t.Errorf("Expected command 'SET', got %q", expect.Command)
	}
	if expect.RedisKey != "session:abc" {
		t.Errorf("Expected key 'session:abc', got %q", expect.RedisKey)
	}
}

func TestParser_VerifyGRPC_RequestBody(t *testing.T) {
	content := `TEST verify-grpc-body
RECEIVE HTTP:GET http://localhost:3000/health

EXPECT GRPC:users.UserService/GetUser
VERIFY request_body CONTAINS "user_id"
VERIFY metadata.authorization CONTAINS "Bearer"

RESPOND HTTP:200
WITH {{payloads/health.json}}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "verify_grpc.linespec")
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
		t.Fatalf("Expected 2 VERIFY rules, got %d", len(expect.Verify))
	}

	if expect.Verify[0].Target != "request_body" {
		t.Errorf("Expected target 'request_body', got %q", expect.Verify[0].Target)
	}
	if expect.Verify[1].Target != "metadata.authorization" {
		t.Errorf("Expected target 'metadata.authorization', got %q", expect.Verify[1].Target)
	}
}

func TestParser_VerifyRedis_Command(t *testing.T) {
	content := `TEST verify-redis-command
RECEIVE HTTP:DELETE http://localhost:3000/users/123

EXPECT WRITE:REDIS DEL user:123
VERIFY command CONTAINS "DEL"
VERIFY key CONTAINS "user:"

RESPOND HTTP:204
WITH {{payloads/empty.json}}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "verify_redis.linespec")
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
		t.Fatalf("Expected 2 VERIFY rules, got %d", len(expect.Verify))
	}
	if expect.Verify[0].Target != "command" {
		t.Errorf("Expected target 'command', got %q", expect.Verify[0].Target)
	}
	if expect.Verify[1].Target != "key" {
		t.Errorf("Expected target 'key', got %q", expect.Verify[1].Target)
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

func TestParser_TimeoutDirective(t *testing.T) {
	content := `TEST timeout_test
RECEIVE HTTP:POST http://localhost:3000/api
TIMEOUT 5m
RESPOND HTTP:200`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "timeout_test.linespec")
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

	if spec.Timeout != 5*time.Minute {
		t.Errorf("Expected Timeout=5m, got %v", spec.Timeout)
	}
}

func TestParser_TimeoutDirective_Seconds(t *testing.T) {
	content := `TEST timeout_seconds_test
RECEIVE HTTP:GET http://localhost:3000/health
TIMEOUT 30s
RESPOND HTTP:200`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "timeout_seconds_test.linespec")
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

	if spec.Timeout != 30*time.Second {
		t.Errorf("Expected Timeout=30s, got %v", spec.Timeout)
	}
}

func TestParser_NoTimeoutDirective(t *testing.T) {
	content := `TEST no_timeout_test
RECEIVE HTTP:GET http://localhost:3000/health
RESPOND HTTP:200`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "no_timeout_test.linespec")
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

	if spec.Timeout != 0 {
		t.Errorf("Expected Timeout=0 (not set), got %v", spec.Timeout)
	}
}

func TestParser_TimeoutDirective_InvalidDuration(t *testing.T) {
	content := `TEST bad_timeout_test
RECEIVE HTTP:GET http://localhost:3000/health
TIMEOUT notaduration
RESPOND HTTP:200`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "bad_timeout_test.linespec")
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
		t.Error("Expected parse error for invalid TIMEOUT value, got nil")
	}
}

func TestLexer_UsingSqlContains(t *testing.T) {
	content := "TEST using_sql_contains_test\nRECEIVE HTTP:GET /api/v1/users/1\nHEADERS\n  Authorization: Bearer token\n\nEXPECT READ:MYSQL users\nUSING_SQL_CONTAINS \"\"\"\nWHERE users.id = 1\n\"\"\"\nRETURNS EMPTY\n\nRESPOND HTTP:200"

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "using_sql_contains_test.linespec")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	tokens, err := LexFile(tmpFile)
	if err != nil {
		t.Fatalf("LexFile failed: %v", err)
	}

	// Verify TokenUsingSqlContains is present (not TokenUsingSql)
	found := false
	for _, tok := range tokens {
		if tok.Type == TokenUsingSqlContains {
			found = true
		}
		if tok.Type == TokenUsingSql {
			t.Errorf("Should not emit TokenUsingSql for USING_SQL_CONTAINS line")
		}
	}
	if !found {
		t.Errorf("Expected TokenUsingSqlContains token, none found")
	}
}

func TestParser_UsingSqlContains(t *testing.T) {
	content := "TEST using_sql_contains_test\nRECEIVE HTTP:GET /api/v1/users/1\nHEADERS\n  Authorization: Bearer token\n\nEXPECT READ:MYSQL users\nUSING_SQL_CONTAINS \"\"\"\nWHERE users.id = 1\n\"\"\"\nRETURNS EMPTY\n\nRESPOND HTTP:200"

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "using_sql_contains_test.linespec")
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
	if expect.Channel != types.ReadMySQL {
		t.Errorf("Expected channel READ_MYSQL, got %s", expect.Channel)
	}
	if expect.SQL != "" {
		t.Errorf("Expected SQL to be empty, got %q", expect.SQL)
	}
	if expect.SQLContains != "WHERE users.id = 1" {
		t.Errorf("Expected SQLContains 'WHERE users.id = 1', got %q", expect.SQLContains)
	}
	if !expect.ReturnsEmpty {
		t.Errorf("Expected ReturnsEmpty to be true")
	}
}

// ── Semantic SQL matching DSL tests ──────────────────────────────────────────

func TestParser_SemanticAccessingTables(t *testing.T) {
	content := `TEST semantic-read
RECEIVE HTTP:GET http://localhost:3000/users/42

EXPECT READ:MYSQL
ACCESSING_TABLES [users]
VERIFY_OPERATION SELECT
VERIFY_WHERE_COLUMNS [id, email]
RETURNS {{payloads/user.json}}

RESPOND HTTP:200
WITH {{payloads/user.json}}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "semantic.linespec")
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
	e := spec.Expects[0]
	if e.Channel != types.ReadMySQL {
		t.Errorf("Expected READ_MYSQL, got %s", e.Channel)
	}
	if len(e.AccessingTables) != 1 || e.AccessingTables[0] != "users" {
		t.Errorf("Expected AccessingTables=[users], got %v", e.AccessingTables)
	}
	if e.VerifyOperation != "SELECT" {
		t.Errorf("Expected VerifyOperation=SELECT, got %q", e.VerifyOperation)
	}
	if len(e.VerifyWhereColumns) != 2 || e.VerifyWhereColumns[0] != "id" || e.VerifyWhereColumns[1] != "email" {
		t.Errorf("Expected VerifyWhereColumns=[id email], got %v", e.VerifyWhereColumns)
	}
}

func TestParser_SemanticVerifyWhere(t *testing.T) {
	content := `TEST semantic-verify-where
RECEIVE HTTP:GET http://localhost:3000/users/42

EXPECT READ:MYSQL
ACCESSING_TABLES [users]
VERIFY_WHERE
  id: 42
  status: active
RETURNS {{payloads/user.json}}

RESPOND HTTP:200
WITH {{payloads/user.json}}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "verify_where.linespec")
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
	e := spec.Expects[0]
	if e.VerifyWhere["id"] != "42" {
		t.Errorf("Expected VerifyWhere[id]=42, got %q", e.VerifyWhere["id"])
	}
	if e.VerifyWhere["status"] != "active" {
		t.Errorf("Expected VerifyWhere[status]=active, got %q", e.VerifyWhere["status"])
	}
}

func TestParser_SemanticVerifyWrittenValues(t *testing.T) {
	content := `TEST semantic-insert
RECEIVE HTTP:POST http://localhost:3000/users

EXPECT WRITE:MYSQL
ACCESSING_TABLES [users]
VERIFY_OPERATION INSERT
VERIFY_WRITTEN_VALUES
  email: john@example.com
  name: John Doe
WITH {{payloads/user_write.json}}

RESPOND HTTP:201
WITH {{payloads/user.json}}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "verify_written.linespec")
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
	e := spec.Expects[0]
	if e.Channel != types.WriteMySQL {
		t.Errorf("Expected WRITE_MYSQL, got %s", e.Channel)
	}
	if e.VerifyOperation != "INSERT" {
		t.Errorf("Expected VerifyOperation=INSERT, got %q", e.VerifyOperation)
	}
	if e.VerifyWrittenValues["email"] != "john@example.com" {
		t.Errorf("Expected VerifyWrittenValues[email]=john@example.com, got %q", e.VerifyWrittenValues["email"])
	}
	if e.VerifyWrittenValues["name"] != "John Doe" {
		t.Errorf("Expected VerifyWrittenValues[name]=John Doe, got %q", e.VerifyWrittenValues["name"])
	}
}

func TestParser_SemanticCallN(t *testing.T) {
	content := `TEST semantic-call-n
RECEIVE HTTP:GET http://localhost:3000/users/42

EXPECT READ:MYSQL CALL 1
ACCESSING_TABLES [users]
VERIFY_OPERATION SELECT
RETURNS {{payloads/user.json}}

EXPECT READ:MYSQL CALL 2
ACCESSING_TABLES [users]
VERIFY_OPERATION SELECT
RETURNS EMPTY

RESPOND HTTP:200
WITH {{payloads/user.json}}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "call_n.linespec")
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
	if len(spec.Expects) != 2 {
		t.Fatalf("Expected 2 expects, got %d", len(spec.Expects))
	}
	if spec.Expects[0].CallN != 1 {
		t.Errorf("Expected CallN=1 on first expect, got %d", spec.Expects[0].CallN)
	}
	if spec.Expects[1].CallN != 2 {
		t.Errorf("Expected CallN=2 on second expect, got %d", spec.Expects[1].CallN)
	}
	if !spec.Expects[1].ReturnsEmpty {
		t.Errorf("Expected second expect to have ReturnsEmpty=true")
	}
}

func TestParser_SemanticJoinMultipleTables(t *testing.T) {
	content := `TEST semantic-join
RECEIVE HTTP:GET http://localhost:3000/orders

EXPECT READ:POSTGRESQL
ACCESSING_TABLES [orders, users]
VERIFY_OPERATION SELECT
VERIFY_WHERE_COLUMNS [user_id]
RETURNS {{payloads/orders.json}}

RESPOND HTTP:200
WITH {{payloads/orders.json}}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "join.linespec")
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
	e := spec.Expects[0]
	if e.Channel != types.ReadPostgreSQL {
		t.Errorf("Expected READ_POSTGRESQL, got %s", e.Channel)
	}
	if len(e.AccessingTables) != 2 {
		t.Errorf("Expected 2 tables, got %v", e.AccessingTables)
	}
	// Tables should be sorted: orders, users
	if e.AccessingTables[0] != "orders" || e.AccessingTables[1] != "users" {
		t.Errorf("Expected [orders users], got %v", e.AccessingTables)
	}
	if e.VerifyOperation != "SELECT" {
		t.Errorf("Expected VerifyOperation=SELECT, got %q", e.VerifyOperation)
	}
	if len(e.VerifyWhereColumns) != 1 || e.VerifyWhereColumns[0] != "user_id" {
		t.Errorf("Expected VerifyWhereColumns=[user_id], got %v", e.VerifyWhereColumns)
	}
}

func TestParser_SemanticPresentSentinel(t *testing.T) {
	content := `TEST semantic-present
RECEIVE HTTP:GET http://localhost:3000/users/auth

EXPECT READ:MYSQL
ACCESSING_TABLES [users]
VERIFY_WHERE
  token: PRESENT
RETURNS {{payloads/user.json}}

RESPOND HTTP:200
WITH {{payloads/user.json}}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "present.linespec")
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
	e := spec.Expects[0]
	if e.VerifyWhere["token"] != "PRESENT" {
		t.Errorf("Expected VerifyWhere[token]=PRESENT, got %q", e.VerifyWhere["token"])
	}
}

func TestParser_SemanticExampleLinespecs(t *testing.T) {
	// Verify all new semantic example linespecs parse without error
	files := []string{
		"../../examples/user-linespecs/get_user_semantic.linespec",
		"../../examples/user-linespecs/create_user_semantic.linespec",
		"../../examples/user-linespecs/update_user_semantic.linespec",
		"../../examples/todo-linespecs/get_todo_semantic.linespec",
		"../../examples/notification-linespecs/list_notifications_semantic.linespec",
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			tokens, err := LexFile(f)
			if err != nil {
				t.Fatalf("LexFile failed: %v", err)
			}
			parser := NewParser(tokens)
			_, err = parser.Parse(f)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
		})
	}
}

// Ensure time import is used (it was imported for pre-existing tests)
var _ = time.Second
