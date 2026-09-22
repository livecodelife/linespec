// Package symbols extracts top-level symbols and import relationships from source files.
//
// Extraction is driven entirely by pkg/discover/treesitter queries — no framework
// description is consulted, which is what makes this usable in discover's
// framework-agnostic path.
package symbols

import (
	"context"
	"fmt"
	"strings"

	"github.com/livecodelife/linespec/v3/pkg/discover/lang"
	"github.com/livecodelife/linespec/v3/pkg/discover/treesitter"
)

// Kind categorizes a top-level symbol.
type Kind string

const (
	KindFunction Kind = "function"
	KindMethod   Kind = "method"
	KindType     Kind = "type"
	KindClass    Kind = "class"
	KindModule   Kind = "module"
)

// Symbol is a declaration extracted from a source file.
type Symbol struct {
	Name string
	Kind Kind
}

// File holds the symbols and import paths extracted from one source file.
type File struct {
	Symbols []Symbol
	Imports []string
}

// symbolQuery pairs a tree-sitter query pattern (expecting an @name capture)
// with the Kind assigned to its matches.
type symbolQuery struct {
	pattern string
	kind    Kind
}

// langSpec describes how to extract symbols and imports for one language.
type langSpec struct {
	tsLang        treesitter.Lang
	symbolQueries []symbolQuery
	importQueries []string // each expecting an @path capture
}

// specs is the per-language extraction registry. Adding a language here
// requires the corresponding grammar to already be wired into
// pkg/discover/treesitter/lang.go.
var specs = map[lang.Language]langSpec{
	lang.Go: {
		tsLang: treesitter.LangGo,
		symbolQueries: []symbolQuery{
			{`(function_declaration name: (identifier) @name)`, KindFunction},
			{`(method_declaration name: (field_identifier) @name)`, KindMethod},
			{`(type_spec name: (type_identifier) @name)`, KindType},
		},
		importQueries: []string{
			`(import_spec path: (interpreted_string_literal) @path)`,
		},
	},
	lang.Ruby: {
		tsLang: treesitter.LangRuby,
		symbolQueries: []symbolQuery{
			{`(method name: (identifier) @name)`, KindMethod},
			{`(class name: (constant) @name)`, KindClass},
			{`(module name: (constant) @name)`, KindModule},
		},
		importQueries: []string{
			`(call method: (identifier) @__method (#match? @__method "^require") arguments: (argument_list (string (string_content) @path)))`,
		},
	},
	lang.Python: {
		tsLang: treesitter.LangPython,
		symbolQueries: []symbolQuery{
			{`(module (function_definition name: (identifier) @name))`, KindFunction},
			{`(module (class_definition name: (identifier) @name))`, KindClass},
		},
		importQueries: []string{
			`(import_statement name: (dotted_name) @path)`,
			`(import_statement name: (aliased_import name: (dotted_name) @path))`,
			`(import_from_statement module_name: (dotted_name) @path)`,
		},
	},
	lang.JavaScript: {
		tsLang: treesitter.LangJavaScript,
		symbolQueries: []symbolQuery{
			// Unanchored (not "(program (function_declaration ...))"): an
			// exported top-level declaration is wrapped in an
			// export_statement node, so anchoring directly under program
			// silently missed every "export function"/"export class".
			{`(function_declaration name: (identifier) @name)`, KindFunction},
			{`(class_declaration name: (identifier) @name)`, KindClass},
		},
		importQueries: []string{
			`(import_statement source: (string (string_fragment) @path))`,
			`(call_expression function: (identifier) @__fn (#eq? @__fn "require") arguments: (arguments (string (string_fragment) @path)))`,
		},
	},
	lang.TypeScript: {
		tsLang: treesitter.LangTypeScript,
		symbolQueries: []symbolQuery{
			// Unanchored for the same reason as JavaScript's, above.
			// class_declaration's name field is a type_identifier in the
			// TypeScript/TSX grammar, unlike JavaScript's identifier.
			{`(function_declaration name: (identifier) @name)`, KindFunction},
			{`(class_declaration name: (type_identifier) @name)`, KindClass},
			{`(interface_declaration name: (type_identifier) @name)`, KindType},
			{`(type_alias_declaration name: (type_identifier) @name)`, KindType},
		},
		importQueries: []string{
			`(import_statement source: (string (string_fragment) @path))`,
			`(call_expression function: (identifier) @__fn (#eq? @__fn "require") arguments: (arguments (string (string_fragment) @path)))`,
		},
	},
}

// Supported reports whether Extract can process the given language.
func Supported(l lang.Language) bool {
	_, ok := specs[l]
	return ok
}

// Extract parses src as the given language and returns its symbols and import paths.
func Extract(ctx context.Context, l lang.Language, src []byte) (File, error) {
	spec, ok := specs[l]
	if !ok {
		return File{}, fmt.Errorf("symbols: unsupported language %q", l)
	}

	eng, err := treesitter.New(ctx, spec.tsLang, src)
	if err != nil {
		return File{}, fmt.Errorf("symbols: parse: %w", err)
	}

	var f File
	for _, q := range spec.symbolQueries {
		matches, err := eng.Query(q.pattern)
		if err != nil {
			return File{}, fmt.Errorf("symbols: symbol query: %w", err)
		}
		for _, m := range matches {
			name, ok := m.Captures["name"]
			if !ok {
				continue
			}
			f.Symbols = append(f.Symbols, Symbol{Name: name, Kind: q.kind})
		}
	}

	for _, pattern := range spec.importQueries {
		matches, err := eng.Query(pattern)
		if err != nil {
			return File{}, fmt.Errorf("symbols: import query: %w", err)
		}
		for _, m := range matches {
			path, ok := m.Captures["path"]
			if !ok {
				continue
			}
			f.Imports = append(f.Imports, strings.Trim(path, `"`))
		}
	}

	return f, nil
}
