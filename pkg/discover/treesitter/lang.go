package treesitter

import (
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/ruby"
)

// Lang identifies a source language for parsing.
type Lang int

const (
	LangGo   Lang = iota
	LangRuby Lang = iota
)

// ParseLang converts a string like "go" or "ruby" to a Lang constant.
func ParseLang(s string) (Lang, bool) {
	switch s {
	case "go":
		return LangGo, true
	case "ruby":
		return LangRuby, true
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
	default:
		return nil, fmt.Errorf("unsupported language: %d", l)
	}
}
