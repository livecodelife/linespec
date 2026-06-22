package boundaries

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	sitter "github.com/smacker/go-tree-sitter"
	sittergo "github.com/smacker/go-tree-sitter/golang"
	sitterruby "github.com/smacker/go-tree-sitter/ruby"

	"github.com/livecodelife/linespec/pkg/discover/framework"
	"github.com/livecodelife/linespec/pkg/discover/routes"
)

// DefaultDepth is the default call graph traversal depth.
const DefaultDepth = 3

// Hit is a single protocol boundary interaction detected during call graph tracing.
type Hit struct {
	Protocol  string // postgresql, mysql, redis, http, kafka, rabbitmq
	Direction string // read, write, both
	Target    string // table name, URL, key, or topic — empty when unresolved
	Dynamic   bool   // true when the call site could not be statically resolved
}

// Tracer traces protocol boundary hits for a set of routes in a project directory.
type Tracer struct {
	desc  *framework.Description
	lang  *sitter.Language
	ext   string
	depth int
}

// New returns a Tracer for the given framework description.
func New(desc *framework.Description) (*Tracer, error) {
	lang, ext, err := langAndExt(desc.Language)
	if err != nil {
		return nil, err
	}
	return &Tracer{desc: desc, lang: lang, ext: ext, depth: DefaultDepth}, nil
}

// WithDepth returns a new Tracer with the call graph depth set to d.
func (t *Tracer) WithDepth(d int) *Tracer {
	cp := *t
	cp.depth = d
	return &cp
}

func langAndExt(language string) (*sitter.Language, string, error) {
	switch language {
	case "go":
		return sittergo.GetLanguage(), ".go", nil
	case "ruby":
		return sitterruby.GetLanguage(), ".rb", nil
	default:
		return nil, "", fmt.Errorf("unsupported language: %q", language)
	}
}

// Trace scans dir for handler implementations referenced by rs and returns a map
// of HandlerRef → []Hit. Routes with an empty HandlerRef are skipped. Handlers
// referenced by more than one route are traced once and results are shared.
func (t *Tracer) Trace(ctx context.Context, dir string, rs []routes.Route) (map[string][]Hit, error) {
	idx, err := t.buildIndex(ctx, dir)
	if err != nil {
		return nil, err
	}

	out := make(map[string][]Hit)
	visited := make(map[string]bool)
	for _, r := range rs {
		if r.HandlerRef == "" || visited[r.HandlerRef] {
			continue
		}
		visited[r.HandlerRef] = true
		hits, err := t.traceHandler(r.HandlerRef, idx, t.depth, make(map[string]bool))
		if err != nil {
			return nil, fmt.Errorf("trace %q: %w", r.HandlerRef, err)
		}
		out[r.HandlerRef] = hits
	}
	return out, nil
}

// parsedFile holds a parsed source file for repeated querying.
type parsedFile struct {
	path string
	src  []byte
	root *sitter.Node
}

// fileIndex is the set of all parsed source files in a scanned directory.
type fileIndex struct {
	lang  *sitter.Language
	files []*parsedFile
}

func (t *Tracer) buildIndex(ctx context.Context, dir string) (*fileIndex, error) {
	var files []*parsedFile
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != t.ext {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		root, err := sitter.ParseCtx(ctx, src, t.lang)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		files = append(files, &parsedFile{path: path, src: src, root: root})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &fileIndex{lang: t.lang, files: files}, nil
}

// traceHandler finds all boundary hits for a handler reference, walking the call
// graph up to depth levels. callStack prevents recursion on the same function.
func (t *Tracer) traceHandler(handlerRef string, idx *fileIndex, depth int, callStack map[string]bool) ([]Hit, error) {
	funcName := extractFuncName(handlerRef, t.desc.Language)
	if funcName == "" || callStack[funcName] {
		return nil, nil
	}
	callStack[funcName] = true
	defer delete(callStack, funcName)

	var hits []Hit
	for _, pf := range idx.files {
		body := t.findFuncBody(funcName, pf)
		if body == nil {
			continue
		}
		hits = append(hits, t.runBoundaryQueries(body, pf)...)
		if depth > 0 {
			for _, callee := range t.findCallees(body, pf) {
				if callStack[callee] {
					continue
				}
				sub, err := t.traceHandler(callee, idx, depth-1, callStack)
				if err != nil {
					return nil, err
				}
				hits = append(hits, sub...)
			}
		}
	}
	return deduplicateHits(hits), nil
}

// extractFuncName strips the package or controller qualifier from a handler ref.
// Go:   "handlers.CreateUser" → "CreateUser", "myHandler" → "myHandler"
// Ruby: "users#create" → "create", "UsersController#show" → "show"
func extractFuncName(ref, language string) string {
	if ref == "" {
		return ""
	}
	switch language {
	case "go":
		// Strip package qualifier: "handlers.CreateUser" → "CreateUser"
		for i := len(ref) - 1; i >= 0; i-- {
			if ref[i] == '.' {
				return ref[i+1:]
			}
		}
		return ref
	case "ruby":
		// Strip controller#action: "users#create" → "create"
		for i, c := range ref {
			if c == '#' {
				return ref[i+1:]
			}
		}
		return ref
	default:
		return ref
	}
}

// findFuncBody returns the body node of the named function/method in pf, or nil.
func (t *Tracer) findFuncBody(name string, pf *parsedFile) *sitter.Node {
	for _, pattern := range t.funcDefPatterns(name) {
		if node := t.queryForNode(pattern, "body", pf); node != nil {
			return node
		}
	}
	return nil
}

// funcDefPatterns returns tree-sitter query patterns that match a named function definition.
func (t *Tracer) funcDefPatterns(name string) []string {
	// %q produces Go-quoted string; tree-sitter predicates use double-quoted strings.
	// Use the name directly since function names are safe identifiers.
	switch t.desc.Language {
	case "go":
		return []string{
			fmt.Sprintf(`(function_declaration name: (identifier) @name body: (block) @body (#eq? @name "%s"))`, name),
			fmt.Sprintf(`(method_declaration name: (field_identifier) @name body: (block) @body (#eq? @name "%s"))`, name),
		}
	case "ruby":
		return []string{
			fmt.Sprintf(`(method name: (identifier) @name (#eq? @name "%s") body: (body_statement) @body)`, name),
			// Fallback: without field name for older grammar versions
			fmt.Sprintf(`(method (identifier) @name (body_statement) @body (#eq? @name "%s"))`, name),
		}
	default:
		return nil
	}
}

// queryForNode compiles pattern, runs it against pf.root, and returns the first
// node captured under captureName, or nil if no match.
func (t *Tracer) queryForNode(pattern, captureName string, pf *parsedFile) *sitter.Node {
	q, err := sitter.NewQuery([]byte(pattern), t.lang)
	if err != nil {
		return nil
	}
	defer q.Close()

	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(q, pf.root)

	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}
		m = qc.FilterPredicates(m, pf.src)
		for _, c := range m.Captures {
			if q.CaptureNameForId(c.Index) == captureName {
				return c.Node
			}
		}
	}
	return nil
}

// runBoundaryQueries runs all framework boundary queries against the subtree rooted
// at node and returns the resulting hits.
func (t *Tracer) runBoundaryQueries(node *sitter.Node, pf *parsedFile) []Hit {
	var hits []Hit
	for _, bq := range t.desc.BoundaryQueries {
		matches := t.queryNode(bq.Pattern, node, pf)
		for _, caps := range matches {
			hits = append(hits, t.capsToHit(caps, bq))
		}
	}
	return hits
}

// capsToHit converts a query match to a Hit using the boundary query's metadata.
func (t *Tracer) capsToHit(caps map[string]string, bq framework.BoundaryQuery) Hit {
	h := Hit{
		Protocol:  bq.Protocol,
		Direction: bq.Direction,
	}
	targetCap := bq.Captures.Target
	if targetCap == "" {
		h.Dynamic = true
		return h
	}
	raw, ok := caps[targetCap]
	if !ok || raw == "" {
		h.Dynamic = true
		return h
	}
	h.Target = t.resolveTarget(stripStringQuotes(raw), bq.Protocol)
	if h.Target == "" {
		h.Dynamic = true
	}
	return h
}

// resolveTarget post-processes a raw captured value into a clean target string.
func (t *Tracer) resolveTarget(raw, protocol string) string {
	switch protocol {
	case "postgresql", "mysql":
		if looksLikeSQL(raw) {
			return TableFromSQL(raw)
		}
		// Ruby ActiveRecord: raw is a constant class name like "User" or "Accounts::User"
		if t.desc.Language == "ruby" {
			return ModelToTable(raw)
		}
		// Go: raw should be a SQL string; if we didn't extract a table, return empty
		return TableFromSQL(raw)
	default:
		return raw
	}
}

// findCallees returns all function/method names called from within node.
// These are candidates for recursive call graph traversal.
func (t *Tracer) findCallees(node *sitter.Node, pf *parsedFile) []string {
	pattern := t.calleePattern()
	if pattern == "" {
		return nil
	}
	matches := t.queryNode(pattern, node, pf)
	seen := make(map[string]bool)
	var callees []string
	for _, caps := range matches {
		name := caps["callee"]
		if name != "" && !seen[name] {
			seen[name] = true
			callees = append(callees, name)
		}
	}
	return callees
}

func (t *Tracer) calleePattern() string {
	switch t.desc.Language {
	case "go":
		// Captures the bare function name or the method name from a selector.
		return `(call_expression function: [(identifier) @callee (selector_expression field: (field_identifier) @callee)])`
	case "ruby":
		return `(call method: (identifier) @callee)`
	default:
		return ""
	}
}

// queryNode runs pattern against the subtree rooted at node and returns all matches
// as maps of capture name → text.
func (t *Tracer) queryNode(pattern string, node *sitter.Node, pf *parsedFile) []map[string]string {
	q, err := sitter.NewQuery([]byte(pattern), t.lang)
	if err != nil {
		return nil
	}
	defer q.Close()

	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(q, node)

	var out []map[string]string
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}
		m = qc.FilterPredicates(m, pf.src)
		if len(m.Captures) == 0 {
			continue
		}
		caps := make(map[string]string, len(m.Captures))
		for _, c := range m.Captures {
			caps[q.CaptureNameForId(c.Index)] = c.Node.Content(pf.src)
		}
		out = append(out, caps)
	}
	return out
}

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

func deduplicateHits(hits []Hit) []Hit {
	type key struct {
		proto, dir, target string
		dynamic            bool
	}
	seen := make(map[key]bool, len(hits))
	out := hits[:0:0]
	for _, h := range hits {
		k := key{h.Protocol, h.Direction, h.Target, h.Dynamic}
		if !seen[k] {
			seen[k] = true
			out = append(out, h)
		}
	}
	return out
}
