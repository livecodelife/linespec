package treesitter

import (
	"context"
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
)

// Match is one tree-sitter query match with its named text captures.
type Match struct {
	// Captures maps capture name (without the @) to the matched node's text content.
	Captures map[string]string
}

// Engine parses a source file once and allows repeated query execution against it.
type Engine struct {
	lang *sitter.Language
	src  []byte
	root *sitter.Node
}

// New parses src using the given language and returns an Engine ready for queries.
// The parse is done once here; subsequent Query calls reuse the tree.
func New(ctx context.Context, lang Lang, src []byte) (*Engine, error) {
	l, err := sitterLang(lang)
	if err != nil {
		return nil, err
	}
	root, err := sitter.ParseCtx(ctx, src, l)
	if err != nil {
		return nil, fmt.Errorf("treesitter parse: %w", err)
	}
	return &Engine{lang: l, src: src, root: root}, nil
}

// Query compiles pattern and executes it against the parsed tree, returning all
// matches with named captures resolved to their text content.
// Predicate filters (#match?, #eq?, etc.) declared in the pattern are applied.
func (e *Engine) Query(pattern string) ([]Match, error) {
	q, err := sitter.NewQuery([]byte(pattern), e.lang)
	if err != nil {
		return nil, fmt.Errorf("compile query: %w", err)
	}
	defer q.Close()

	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(q, e.root)

	var out []Match
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}
		m = qc.FilterPredicates(m, e.src)
		if len(m.Captures) == 0 {
			continue
		}
		caps := make(map[string]string, len(m.Captures))
		for _, c := range m.Captures {
			name := q.CaptureNameForId(c.Index)
			caps[name] = c.Node.Content(e.src)
		}
		out = append(out, Match{Captures: caps})
	}
	return out, nil
}
