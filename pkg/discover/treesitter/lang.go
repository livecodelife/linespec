package treesitter

import (
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/ruby"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
)

// Lang identifies a source language for parsing.
type Lang int

const (
	LangGo Lang = iota
	LangRuby
	LangPython
	LangJavaScript
	// LangTypeScript covers both .ts and .tsx: it always parses with the
	// TSX grammar, a strict syntactic superset of plain TypeScript, so one
	// Lang value suffices for both.
	LangTypeScript
)

// ParseLang converts a string like "go" or "ruby" to a Lang constant.
func ParseLang(s string) (Lang, bool) {
	switch s {
	case "go":
		return LangGo, true
	case "ruby":
		return LangRuby, true
	case "python":
		return LangPython, true
	case "javascript":
		return LangJavaScript, true
	case "typescript":
		return LangTypeScript, true
	default:
		return 0, false
	}
}

func sitterLang(l Lang) (*sitter.Language, error) {
	switch l {
	case LangGo:
		return golang.GetLanguage(), nil
	case LangRuby:
		return ruby.GetLanguage(), nil
	case LangPython:
		return python.GetLanguage(), nil
	case LangJavaScript:
		return javascript.GetLanguage(), nil
	case LangTypeScript:
		return tsx.GetLanguage(), nil
	default:
		return nil, fmt.Errorf("unsupported language: %d", l)
	}
}
