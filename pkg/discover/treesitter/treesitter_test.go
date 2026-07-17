package treesitter_test

import (
	"context"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/discover/treesitter"
)

func TestEngine_QueryGo(t *testing.T) {
	src := []byte(`
package main

import "net/http"

func handler(w http.ResponseWriter, r *http.Request) {}

func main() {
	http.HandleFunc("/hello", handler)
}
`)
	eng, err := treesitter.New(context.Background(), treesitter.LangGo, src)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Match any call_expression to verify parsing and query execution work.
	matches, err := eng.Query(`(call_expression function: (selector_expression field: (field_identifier) @fn))`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected at least one match, got none")
	}

	found := false
	for _, m := range matches {
		if m.Captures["fn"] == "HandleFunc" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find HandleFunc call")
	}
}

func TestEngine_QueryRuby(t *testing.T) {
	src := []byte(`
class App < Sinatra::Base
  get '/hello' do
    'Hello!'
  end
end
`)
	eng, err := treesitter.New(context.Background(), treesitter.LangRuby, src)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Match method call identifiers — verifies Ruby parsing works.
	matches, err := eng.Query(`(call method: (identifier) @method)`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected at least one match, got none")
	}

	found := false
	for _, m := range matches {
		if m.Captures["method"] == "get" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected to find 'get' method call, got matches: %v", matches)
	}
}

func TestEngine_QueryPython(t *testing.T) {
	src := []byte(`
import os

def handler():
    return os.getenv("PORT")
`)
	eng, err := treesitter.New(context.Background(), treesitter.LangPython, src)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	matches, err := eng.Query(`(function_definition name: (identifier) @name)`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected at least one match, got none")
	}

	found := false
	for _, m := range matches {
		if m.Captures["name"] == "handler" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find 'handler' function definition")
	}
}

func TestEngine_QueryJavaScript(t *testing.T) {
	src := []byte(`
function handler(req, res) {
	res.send('hello');
}
`)
	eng, err := treesitter.New(context.Background(), treesitter.LangJavaScript, src)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	matches, err := eng.Query(`(function_declaration name: (identifier) @name)`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected at least one match, got none")
	}

	found := false
	for _, m := range matches {
		if m.Captures["name"] == "handler" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find 'handler' function declaration")
	}
}

func TestEngine_BadQuery(t *testing.T) {
	src := []byte(`package main`)
	eng, err := treesitter.New(context.Background(), treesitter.LangGo, src)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = eng.Query(`(this_is_not_valid_syntax @@@`)
	if err == nil {
		t.Fatal("expected error for malformed query pattern, got nil")
	}
}

func TestParseLang(t *testing.T) {
	tests := []struct {
		input string
		want  treesitter.Lang
		ok    bool
	}{
		{"go", treesitter.LangGo, true},
		{"ruby", treesitter.LangRuby, true},
		{"python", treesitter.LangPython, true},
		{"javascript", treesitter.LangJavaScript, true},
		{"rust", 0, false},
		{"", 0, false},
	}
	for _, tc := range tests {
		got, ok := treesitter.ParseLang(tc.input)
		if ok != tc.ok {
			t.Errorf("ParseLang(%q): ok=%v, want %v", tc.input, ok, tc.ok)
		}
		if ok && got != tc.want {
			t.Errorf("ParseLang(%q): got %v, want %v", tc.input, got, tc.want)
		}
	}
}
