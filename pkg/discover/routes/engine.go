package routes

import (
	"fmt"
	"regexp"

	sitter "github.com/smacker/go-tree-sitter"
)

type posCapture struct {
	text      string
	startByte uint32
	endByte   uint32
	row       uint32
	col       uint32
}

type posMatch struct {
	captures map[string]posCapture
}

type parsedFile struct {
	lang *sitter.Language
	src  []byte
	root *sitter.Node
}

func (pf *parsedFile) query(pattern string, filters map[string]string) ([]posMatch, error) {
	q, err := sitter.NewQuery([]byte(pattern), pf.lang)
	if err != nil {
		return nil, fmt.Errorf("compile query: %w", err)
	}
	defer q.Close()

	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(q, pf.root)

	compiled, err := compileFilters(filters)
	if err != nil {
		return nil, err
	}

	var out []posMatch
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}
		m = qc.FilterPredicates(m, pf.src)
		if len(m.Captures) == 0 {
			continue
		}
		pm := posMatch{captures: make(map[string]posCapture, len(m.Captures))}
		for _, c := range m.Captures {
			name := q.CaptureNameForId(c.Index)
			pt := c.Node.StartPoint()
			pm.captures[name] = posCapture{
				text:      c.Node.Content(pf.src),
				startByte: c.Node.StartByte(),
				endByte:   c.Node.EndByte(),
				row:       pt.Row,
				col:       pt.Column,
			}
		}
		if !passesFilters(pm, compiled) {
			continue
		}
		out = append(out, pm)
	}
	return out, nil
}

func compileFilters(filters map[string]string) (map[string]*regexp.Regexp, error) {
	if len(filters) == 0 {
		return nil, nil
	}
	out := make(map[string]*regexp.Regexp, len(filters))
	for k, v := range filters {
		r, err := regexp.Compile(v)
		if err != nil {
			return nil, fmt.Errorf("compile filter %q=%q: %w", k, v, err)
		}
		out[k] = r
	}
	return out, nil
}

func passesFilters(m posMatch, compiled map[string]*regexp.Regexp) bool {
	for k, r := range compiled {
		c, ok := m.captures[k]
		if !ok || !r.MatchString(c.text) {
			return false
		}
	}
	return true
}
