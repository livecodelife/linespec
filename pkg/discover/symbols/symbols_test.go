package symbols_test

import (
	"context"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/discover/lang"
	"github.com/livecodelife/linespec/v3/pkg/discover/symbols"
)

func hasSymbol(f symbols.File, name string, kind symbols.Kind) bool {
	for _, s := range f.Symbols {
		if s.Name == name && s.Kind == kind {
			return true
		}
	}
	return false
}

func hasImport(f symbols.File, path string) bool {
	for _, i := range f.Imports {
		if i == path {
			return true
		}
	}
	return false
}

func TestExtract_Go(t *testing.T) {
	src := []byte(`
package handlers

import (
	"fmt"
	"net/http"
)

type Server struct{}

func (s *Server) Handle(w http.ResponseWriter, r *http.Request) {}

func New() *Server {
	fmt.Println("new")
	return &Server{}
}
`)
	f, err := symbols.Extract(context.Background(), lang.Go, src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !hasSymbol(f, "New", symbols.KindFunction) {
		t.Errorf("expected function symbol %q, got %+v", "New", f.Symbols)
	}
	if !hasSymbol(f, "Handle", symbols.KindMethod) {
		t.Errorf("expected method symbol %q, got %+v", "Handle", f.Symbols)
	}
	if !hasSymbol(f, "Server", symbols.KindType) {
		t.Errorf("expected type symbol %q, got %+v", "Server", f.Symbols)
	}
	if !hasImport(f, "fmt") {
		t.Errorf("expected import %q, got %+v", "fmt", f.Imports)
	}
	if !hasImport(f, "net/http") {
		t.Errorf("expected import %q, got %+v", "net/http", f.Imports)
	}
}

func TestExtract_Ruby(t *testing.T) {
	src := []byte(`
require 'json'
require_relative './foo'

class App < Sinatra::Base
  def hello
    'hi'
  end
end

module Helpers
end
`)
	f, err := symbols.Extract(context.Background(), lang.Ruby, src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !hasSymbol(f, "hello", symbols.KindMethod) {
		t.Errorf("expected method symbol %q, got %+v", "hello", f.Symbols)
	}
	if !hasSymbol(f, "App", symbols.KindClass) {
		t.Errorf("expected class symbol %q, got %+v", "App", f.Symbols)
	}
	if !hasSymbol(f, "Helpers", symbols.KindModule) {
		t.Errorf("expected module symbol %q, got %+v", "Helpers", f.Symbols)
	}
	if !hasImport(f, "json") {
		t.Errorf("expected import %q, got %+v", "json", f.Imports)
	}
	if !hasImport(f, "./foo") {
		t.Errorf("expected import %q, got %+v", "./foo", f.Imports)
	}
}

func TestExtract_Python(t *testing.T) {
	src := []byte(`
import os
from foo.bar import baz

class Handler:
    def get(self):
        pass

def top_level():
    pass
`)
	f, err := symbols.Extract(context.Background(), lang.Python, src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !hasSymbol(f, "top_level", symbols.KindFunction) {
		t.Errorf("expected function symbol %q, got %+v", "top_level", f.Symbols)
	}
	if !hasSymbol(f, "Handler", symbols.KindClass) {
		t.Errorf("expected class symbol %q, got %+v", "Handler", f.Symbols)
	}
	if hasSymbol(f, "get", symbols.KindFunction) {
		t.Errorf("did not expect nested method %q to be extracted as a top-level symbol", "get")
	}
	if !hasImport(f, "os") {
		t.Errorf("expected import %q, got %+v", "os", f.Imports)
	}
	if !hasImport(f, "foo.bar") {
		t.Errorf("expected import %q, got %+v", "foo.bar", f.Imports)
	}
}

func TestExtract_JavaScript(t *testing.T) {
	src := []byte(`
const foo = require('foo');
import bar from './bar';

function topLevel() {}

class Handler {
  method() {}
}
`)
	f, err := symbols.Extract(context.Background(), lang.JavaScript, src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !hasSymbol(f, "topLevel", symbols.KindFunction) {
		t.Errorf("expected function symbol %q, got %+v", "topLevel", f.Symbols)
	}
	if !hasSymbol(f, "Handler", symbols.KindClass) {
		t.Errorf("expected class symbol %q, got %+v", "Handler", f.Symbols)
	}
	if !hasImport(f, "foo") {
		t.Errorf("expected import %q, got %+v", "foo", f.Imports)
	}
	if !hasImport(f, "./bar") {
		t.Errorf("expected import %q, got %+v", "./bar", f.Imports)
	}
}

func TestExtract_Unsupported(t *testing.T) {
	if _, err := symbols.Extract(context.Background(), lang.Language("rust"), []byte("fn main() {}")); err == nil {
		t.Fatal("expected error for unsupported language, got nil")
	}
}

func TestSupported(t *testing.T) {
	for _, l := range []lang.Language{lang.Go, lang.Ruby, lang.Python, lang.JavaScript} {
		if !symbols.Supported(l) {
			t.Errorf("expected %q to be supported", l)
		}
	}
	if symbols.Supported(lang.Language("rust")) {
		t.Error("expected \"rust\" to be unsupported")
	}
}
