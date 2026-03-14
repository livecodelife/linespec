package dsl

import (
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
