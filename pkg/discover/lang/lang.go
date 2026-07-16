// Package lang provides language detection from file extensions.
//
// Detection here is purely extension-based and requires no framework
// description — it is the entry point for discover's framework-agnostic
// path, which needs to know what a file *is* before any route/framework
// knowledge is available.
package lang

import (
	"path/filepath"
	"strings"
)

// Language identifies a source language recognized by discover.
type Language string

// Languages with a tree-sitter grammar wired up in pkg/discover/treesitter.
const (
	Go         Language = "go"
	Ruby       Language = "ruby"
	Python     Language = "python"
	JavaScript Language = "javascript"
)

// extensions maps a lowercase file extension (including the leading dot) to
// the language it signals.
var extensions = map[string]Language{
	".go":  Go,
	".rb":  Ruby,
	".py":  Python,
	".js":  JavaScript,
	".jsx": JavaScript,
	".mjs": JavaScript,
	".cjs": JavaScript,
}

// Detect returns the language signaled by path's extension and whether one
// was recognized. path is not read from disk; only its extension is used.
func Detect(path string) (Language, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	l, ok := extensions[ext]
	return l, ok
}
