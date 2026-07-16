package lang_test

import (
	"testing"

	"github.com/livecodelife/linespec/pkg/discover/lang"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		path string
		want lang.Language
		ok   bool
	}{
		{"main.go", lang.Go, true},
		{"pkg/handlers/app.rb", lang.Ruby, true},
		{"app/server.py", lang.Python, true},
		{"web/index.js", lang.JavaScript, true},
		{"web/App.jsx", lang.JavaScript, true},
		{"web/module.mjs", lang.JavaScript, true},
		{"scripts/build.cjs", lang.JavaScript, true},
		{"README.md", "", false},
		{"Makefile", "", false},
	}
	for _, tc := range tests {
		got, ok := lang.Detect(tc.path)
		if ok != tc.ok {
			t.Errorf("Detect(%q): ok=%v, want %v", tc.path, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("Detect(%q): got %v, want %v", tc.path, got, tc.want)
		}
	}
}
