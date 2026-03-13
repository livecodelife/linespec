package dsl

import (
	"testing"

	"github.com/livecodelife/linespec/pkg/types"
)

func TestLexer_GetUserSuccess(t *testing.T) {
	tokens, err := LexFile("../../user-linespecs/get_user_success.linespec")
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
	tokens, err := LexFile("../../user-linespecs/get_user_success.linespec")
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
	tokens, err := LexFile("../../user-linespecs/create_user_success.linespec")
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

	if len(spec.Expects[1].Verify) != 1 {
		t.Errorf("Expected 1 verify rule for write expect, got %d", len(spec.Expects[1].Verify))
	}

	if spec.Expects[1].Verify[0].Type != "CONTAINS" {
		t.Errorf("Expected verify type CONTAINS, got %s", spec.Expects[1].Verify[0].Type)
	}
}
