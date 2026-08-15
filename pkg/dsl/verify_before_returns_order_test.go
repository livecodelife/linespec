package dsl

import (
	"os"
	"path/filepath"
	"testing"
)

// TestVerifyBeforeReturnsOrderIndependent is the regression coverage for
// prov-2026-4ea65be4 (GitHub issue #185).
//
// parseExpect (pkg/dsl/parser.go) used to check trailing EXPECT clauses in a
// fixed sequence, ending with a `for p.peek().Type == TokenVerify` loop after
// the RETURNS/RESPONSE_HEADERS checks. When a legacy VERIFY line appeared
// before RETURNS in the source, RETURNS was never consumed (peek() saw
// TokenVerify, not TokenReturns, when the RETURNS check ran), which orphaned
// the RETURNS token at the head of the remaining stream and caused the parser
// to report "RESPOND block is required for HTTP-triggered tests" even when
// RESPOND was present later in the file.
func TestVerifyBeforeReturnsOrderIndependent(t *testing.T) {
	content := `TEST verify-before-returns
RECEIVE HTTP:POST http://localhost:3000/orders

EXPECT HTTP:GET http://order-service.local/orders/123
VERIFY headers.Authorization CONTAINS 'Bearer'
RETURNS {{payloads/order.json}}

RESPOND HTTP:200`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "verify_before_returns.linespec")
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
		t.Fatalf("Parse failed (VERIFY before RETURNS should not orphan RETURNS or drop RESPOND): %v", err)
	}

	if len(spec.Expects) != 1 {
		t.Fatalf("Expected 1 expect, got %d", len(spec.Expects))
	}

	expect := spec.Expects[0]
	if len(expect.Verify) != 1 {
		t.Fatalf("Expected 1 verify rule, got %d", len(expect.Verify))
	}
	if expect.Verify[0].Target != "headers.Authorization" {
		t.Errorf("Expected verify target 'headers.Authorization', got '%s'", expect.Verify[0].Target)
	}
	if expect.ReturnsFile != "payloads/order.json" {
		t.Errorf("Expected RETURNS to resolve to payload 'payloads/order.json', got '%s'", expect.ReturnsFile)
	}

	if spec.Respond.StatusCode != 200 {
		t.Errorf("Expected RESPOND HTTP:200 to be parsed, got status code %d", spec.Respond.StatusCode)
	}
}

// TestReturnsBeforeVerifyStillWorks pins down the previously-passing ordering
// (RETURNS before VERIFY) so the fix does not regress it.
func TestReturnsBeforeVerifyStillWorks(t *testing.T) {
	content := `TEST returns-before-verify
RECEIVE HTTP:POST http://localhost:3000/orders

EXPECT HTTP:GET http://order-service.local/orders/123
RETURNS {{payloads/order.json}}
VERIFY headers.Authorization CONTAINS 'Bearer'

RESPOND HTTP:200`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "returns_before_verify.linespec")
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
	if expect.ReturnsFile != "payloads/order.json" {
		t.Errorf("Expected RETURNS to resolve to payload 'payloads/order.json', got '%s'", expect.ReturnsFile)
	}
	if len(expect.Verify) != 1 {
		t.Fatalf("Expected 1 verify rule, got %d", len(expect.Verify))
	}
	if spec.Respond.StatusCode != 200 {
		t.Errorf("Expected RESPOND HTTP:200 to be parsed, got status code %d", spec.Respond.StatusCode)
	}
}
