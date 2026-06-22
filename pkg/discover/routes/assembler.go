package routes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	sittergo "github.com/smacker/go-tree-sitter/golang"
	sitterruby "github.com/smacker/go-tree-sitter/ruby"

	"github.com/livecodelife/linespec/pkg/discover/framework"
)

// Assembler discovers routes in a project directory using a framework description.
type Assembler struct {
	desc *framework.Description
	lang *sitter.Language
	ext  string
}

// New returns an Assembler for the given framework description.
func New(desc *framework.Description) (*Assembler, error) {
	lang, ext, err := langAndExt(desc.Language)
	if err != nil {
		return nil, err
	}
	return &Assembler{desc: desc, lang: lang, ext: ext}, nil
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

// Assemble scans dir for source files matching the framework's language and returns
// discovered route groups. Each group's name reflects the grouping_strategy declared
// in the framework description (package, controller, or file).
func (a *Assembler) Assemble(ctx context.Context, dir string) ([]Group, error) {
	files, err := a.sourceFiles(dir)
	if err != nil {
		return nil, err
	}

	var results []fileResult
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		root, err := sitter.ParseCtx(ctx, src, a.lang)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", file, err)
		}
		pf := &parsedFile{lang: a.lang, src: src, root: root}

		routes, err := a.discoverFileRoutes(pf, file)
		if err != nil {
			return nil, fmt.Errorf("discover routes in %s: %w", file, err)
		}
		if len(routes) == 0 {
			continue
		}

		gid := a.fileGroupID(pf, file)
		results = append(results, fileResult{file: file, routes: routes, groupID: gid})
	}

	return a.buildGroups(results), nil
}

func (a *Assembler) sourceFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == a.ext {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

type groupCtx struct {
	prefix    string
	bodyStart uint32
	bodyEnd   uint32
}

func (a *Assembler) discoverFileRoutes(pf *parsedFile, file string) ([]Route, error) {
	// Step 1: build group/prefix contexts from group queries.
	// Filtering is handled via inline #eq?/#match? predicates in the pattern.
	var groups []groupCtx
	for _, q := range a.desc.GroupQueries {
		matches, err := pf.query(q.Pattern, nil)
		if err != nil {
			return nil, fmt.Errorf("group query: %w", err)
		}
		for _, m := range matches {
			prefixCap, hasPrefix := m.captures["prefix"]
			bodyCap, hasBody := m.captures["body"]
			if !hasPrefix || !hasBody {
				continue
			}
			var prefixStr string
			if strings.HasPrefix(prefixCap.text, ":") {
				prefixStr = symbolToPath(prefixCap.text)
			} else {
				prefixStr = stripStringQuotes(prefixCap.text)
			}
			groups = append(groups, groupCtx{
				prefix:    prefixStr,
				bodyStart: bodyCap.startByte,
				bodyEnd:   bodyCap.endByte,
			})
		}
	}

	// Step 2: collect file-level middleware names.
	// Filtering is handled via inline #eq?/#match? predicates in the pattern.
	var middlewareChain []string
	for _, q := range a.desc.MiddlewareQueries {
		matches, err := pf.query(q.Pattern, nil)
		if err != nil {
			return nil, fmt.Errorf("middleware query: %w", err)
		}
		for _, m := range matches {
			if mw, ok := m.captures["middleware"]; ok {
				middlewareChain = append(middlewareChain, mw.text)
			}
		}
	}

	// Step 3: run route queries and build the route list.
	var routes []Route
	for _, q := range a.desc.RouteQueries {
		matches, err := pf.query(q.Pattern, q.Filter)
		if err != nil {
			return nil, fmt.Errorf("route query: %w", err)
		}
		for _, m := range matches {
			methodCap, hasMethod := m.captures["method"]
			if !hasMethod {
				continue
			}
			method := strings.ToUpper(methodCap.text)

			// Rails resources/resource: expand to conventional endpoints.
			if method == "RESOURCES" || method == "RESOURCE" {
				resourceCap, ok := m.captures["resource"]
				if !ok {
					continue
				}
				resourceName := stripSymbolColon(resourceCap.text)
				prefix := buildPrefix(methodCap.startByte, groups)
				var expanded []Route
				if method == "RESOURCES" {
					expanded = expandResources(resourceName, prefix)
				} else {
					expanded = expandResource(resourceName, prefix)
				}
				for i := range expanded {
					expanded[i].MiddlewareChain = middlewareChain
					expanded[i].Source = SourceLocation{
						File:   file,
						Line:   methodCap.row + 1,
						Column: methodCap.col,
					}
				}
				routes = append(routes, expanded...)
				continue
			}

			pathCap, hasPath := m.captures["path"]
			if !hasPath {
				continue
			}
			routePath := stripStringQuotes(pathCap.text)
			fullPath := buildFullPath(methodCap.startByte, groups, routePath)

			handlerRef := ""
			if h, ok := m.captures["handler"]; ok {
				handlerRef = h.text
			}

			routes = append(routes, Route{
				Method:          method,
				Path:            fullPath,
				HandlerRef:      handlerRef,
				MiddlewareChain: middlewareChain,
				Source: SourceLocation{
					File:   file,
					Line:   methodCap.row + 1,
					Column: methodCap.col,
				},
			})
		}
	}

	return deduplicateRoutes(routes), nil
}

// buildFullPath computes the full URL path by prepending any containing group prefixes.
func buildFullPath(routeByte uint32, groups []groupCtx, routePath string) string {
	return joinPaths(buildPrefix(routeByte, groups), routePath)
}

// buildPrefix finds all group contexts whose body spans routeByte and concatenates
// their prefixes from outermost (largest range) to innermost (smallest range).
func buildPrefix(routeByte uint32, groups []groupCtx) string {
	var containing []groupCtx
	for _, g := range groups {
		if routeByte > g.bodyStart && routeByte < g.bodyEnd {
			containing = append(containing, g)
		}
	}
	if len(containing) == 0 {
		return ""
	}
	// Sort by bodyStart ascending → outermost group comes first.
	sort.Slice(containing, func(i, j int) bool {
		return containing[i].bodyStart < containing[j].bodyStart
	})
	prefix := ""
	for _, g := range containing {
		prefix = joinPaths(prefix, g.prefix)
	}
	return prefix
}

// fileGroupID returns the group identity for a file based on the grouping strategy.
func (a *Assembler) fileGroupID(pf *parsedFile, file string) string {
	if a.desc.GroupingStrategy == "package" {
		if name := goPackageName(pf); name != "" {
			return name
		}
	}
	return file
}

// goPackageName extracts the Go package name from a parsed Go source file.
func goPackageName(pf *parsedFile) string {
	matches, err := pf.query(`(package_clause (package_identifier) @name)`, nil)
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0].captures["name"].text
}

type fileResult struct {
	file    string
	routes  []Route
	groupID string
}

func (a *Assembler) buildGroups(results []fileResult) []Group {
	groupMap := make(map[string][]Route)
	var order []string
	seen := make(map[string]bool)

	for _, r := range results {
		for _, route := range r.routes {
			gid := a.routeGroupID(route, r.file, r.groupID)
			if !seen[gid] {
				seen[gid] = true
				order = append(order, gid)
			}
			groupMap[gid] = append(groupMap[gid], route)
		}
	}

	groups := make([]Group, 0, len(order))
	for _, id := range order {
		groups = append(groups, Group{Name: id, Routes: groupMap[id]})
	}
	return groups
}

// routeGroupID returns the grouping key for a single route.
func (a *Assembler) routeGroupID(route Route, file, fileGroupID string) string {
	switch a.desc.GroupingStrategy {
	case "package":
		return fileGroupID
	case "controller":
		return controllerFromHandlerRef(route.HandlerRef, file)
	default: // "file"
		return file
	}
}
