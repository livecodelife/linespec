package routes

import "strings"

// joinPaths joins two URL path segments with a single '/' separator.
func joinPaths(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return strings.TrimRight(a, "/") + "/" + strings.TrimLeft(b, "/")
}

// stripStringQuotes removes surrounding quote characters from a captured string literal.
// Handles: "/path", '/path'.
func stripStringQuotes(s string) string {
	if len(s) < 2 {
		return s
	}
	first, last := s[0], s[len(s)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// stripSymbolColon removes a leading ':' from a Ruby symbol literal.
func stripSymbolColon(s string) string {
	return strings.TrimPrefix(s, ":")
}

// symbolToPath converts a Ruby symbol to a path segment: :api → /api.
func symbolToPath(sym string) string {
	return "/" + stripSymbolColon(sym)
}

// deduplicateRoutes removes duplicate routes from a slice. Two routes are
// considered duplicates if they share the same file, line, and HTTP method.
// The first occurrence in the slice is kept (more specific queries run first).
func deduplicateRoutes(routes []Route) []Route {
	type key struct {
		file   string
		line   uint32
		method string
		path   string
	}
	seen := make(map[key]bool, len(routes))
	out := routes[:0:0]
	for _, r := range routes {
		k := key{file: r.Source.File, line: r.Source.Line, method: r.Method, path: r.Path}
		if !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	return out
}
