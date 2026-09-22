package routes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	sitterjs "github.com/smacker/go-tree-sitter/javascript"
	sittertsx "github.com/smacker/go-tree-sitter/typescript/tsx"

	"github.com/livecodelife/linespec/v3/pkg/discover/framework"
	"github.com/livecodelife/linespec/v3/pkg/discover/ignorewalk"
)

// assembleFilesystemRoutes discovers routes by filesystem convention (e.g.
// Next.js's App and Pages Routers) instead of tree-sitter route queries —
// see framework.FilesystemRoutes's doc comment for why: there is no
// route-registration call site to match against.
//
// Route handler tracing is attempted only for App Router route files
// (route.ts's exported HTTP method functions, which have framework-mandated
// names): a page's or a Pages Router API handler's default export has no
// fixed name discover can look up, so those get route coverage (path,
// method) but an empty HandlerRef — no deeper boundary tracing.
func assembleFilesystemRoutes(dir string, fr *framework.FilesystemRoutes) ([]Group, error) {
	groupOrder := []string{}
	groupRoutes := make(map[string][]Route)
	addRoute := func(groupName string, r Route) {
		if _, ok := groupRoutes[groupName]; !ok {
			groupOrder = append(groupOrder, groupName)
		}
		groupRoutes[groupName] = append(groupRoutes[groupName], r)
	}

	if fr.AppDir != "" {
		appRoot := filepath.Join(dir, fr.AppDir)
		if dirExists(appRoot) {
			if err := walkAppRouter(dir, appRoot, fr, addRoute); err != nil {
				return nil, err
			}
		}
	}
	if fr.PagesDir != "" {
		pagesRoot := filepath.Join(dir, fr.PagesDir)
		if dirExists(pagesRoot) {
			if err := walkPagesRouter(dir, pagesRoot, addRoute); err != nil {
				return nil, err
			}
		}
	}

	groups := make([]Group, 0, len(groupOrder))
	for _, name := range groupOrder {
		groups = append(groups, Group{Name: name, Routes: groupRoutes[name]})
	}
	return groups, nil
}

// dirExists reports whether path exists and is a directory. AppDir and
// PagesDir are both optional in practice — most App Router projects have no
// pages/ at all — so a missing root means "no routes here", not an error.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// walkAppRouter walks the project root (rather than appRoot directly) so
// that a repo-root .gitignore, plus any nested one between it and appRoot,
// is loaded by ignorewalk.Walk exactly as it would be for any other
// discover walk (prov-2026-f0b20266) — a .gitignore living outside the
// subtree being scanned is otherwise invisible to it.
func walkAppRouter(dir, appRoot string, fr *framework.FilesystemRoutes, addRoute func(group string, r Route)) error {
	methods := make(map[string]bool, len(fr.Methods))
	for _, m := range fr.Methods {
		methods[strings.ToUpper(m)] = true
	}

	appRootPrefix := appRoot + string(filepath.Separator)
	return ignorewalk.Walk(dir, ignorewalk.DefaultSkipDirs, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if path != appRoot && !strings.HasPrefix(path, appRootPrefix) {
			return nil
		}

		ext := filepath.Ext(path)
		base := strings.TrimSuffix(filepath.Base(path), ext)
		segmentDir := filepath.Dir(path)
		relDir, relErr := filepath.Rel(appRoot, segmentDir)
		if relErr != nil {
			return nil
		}
		urlPath := appRouterPath(relDir)
		groupName := filepath.ToSlash(relDir)

		switch base {
		case fr.RouteFile:
			routes, err := scanRouteFile(path, ext, urlPath, methods)
			if err != nil {
				return err
			}
			for _, r := range routes {
				addRoute(groupName, r)
			}
		case fr.PageFile:
			addRoute(groupName, Route{Method: "GET", Path: urlPath, Source: SourceLocation{File: path}})
		}
		return nil
	})
}

// appRouterPath converts an App Router segment directory (relative to
// AppDir, "/"-separated after filepath.ToSlash) into a URL path:
// "[id]" → ":id", "[...slug]" → "*slug", "(group)" → dropped entirely
// (route groups contribute no path segment), everything else literal.
func appRouterPath(relDir string) string {
	if relDir == "." {
		return "/"
	}
	segments := strings.Split(filepath.ToSlash(relDir), "/")
	var out []string
	for _, seg := range segments {
		switch {
		case strings.HasPrefix(seg, "(") && strings.HasSuffix(seg, ")"):
			continue
		case strings.HasPrefix(seg, "[...") && strings.HasSuffix(seg, "]"):
			out = append(out, "*"+seg[4:len(seg)-1])
		case strings.HasPrefix(seg, "[") && strings.HasSuffix(seg, "]"):
			out = append(out, ":"+seg[1:len(seg)-1])
		default:
			out = append(out, seg)
		}
	}
	if len(out) == 0 {
		return "/"
	}
	return "/" + strings.Join(out, "/")
}

// scanRouteFile parses a route.ts/route.js file and returns one Route per
// exported function whose name is an HTTP method in methods.
func scanRouteFile(path, ext, urlPath string, methods map[string]bool) ([]Route, error) {
	lang, err := grammarForExt(ext)
	if err != nil {
		return nil, nil // an unsupported extension under app/ is not this scanner's concern
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	root, err := sitter.ParseCtx(context.Background(), src, lang)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	pf := &parsedFile{lang: lang, src: src, root: root}

	matches, err := pf.query(`(function_declaration name: (identifier) @name)`, nil)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}

	var out []Route
	seen := make(map[string]bool)
	for _, m := range matches {
		name, ok := m.captures["name"]
		if !ok || !methods[name.text] || seen[name.text] {
			continue
		}
		seen[name.text] = true
		out = append(out, Route{
			Method:     name.text,
			Path:       urlPath,
			HandlerRef: path + "::" + name.text,
			Source:     SourceLocation{File: path, Line: name.row + 1, Column: name.col},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Method < out[j].Method })
	return out, nil
}

func grammarForExt(ext string) (*sitter.Language, error) {
	switch ext {
	case ".ts", ".tsx":
		return sittertsx.GetLanguage(), nil
	case ".js", ".jsx", ".mjs", ".cjs":
		return sitterjs.GetLanguage(), nil
	default:
		return nil, fmt.Errorf("unsupported extension for filesystem routes: %q", ext)
	}
}

// walkPagesRouter handles the legacy Pages Router, best-effort: every file
// under pagesRoot is a page, and every file under pagesRoot/api is an API
// route. Neither carries a HandlerRef — the Pages Router's default-export
// handler has no framework-mandated name to look up (unlike route.ts's
// GET/POST/...), so no boundary tracing is attempted for these.
func walkPagesRouter(dir, pagesRoot string, addRoute func(group string, r Route)) error {
	apiRoot := filepath.Join(pagesRoot, "api")
	pagesRootPrefix := pagesRoot + string(filepath.Separator)
	return ignorewalk.Walk(dir, ignorewalk.DefaultSkipDirs, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if path != pagesRoot && !strings.HasPrefix(path, pagesRootPrefix) {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, "_") {
			return nil // _app, _document, _middleware, etc. are not routes
		}

		ext := filepath.Ext(path)
		relRoot := pagesRoot
		method := "GET"
		if strings.HasPrefix(path, apiRoot+string(filepath.Separator)) {
			relRoot = apiRoot
		}
		rel, relErr := filepath.Rel(relRoot, path)
		if relErr != nil {
			return nil
		}
		rel = strings.TrimSuffix(rel, ext)
		urlPath := "/" + filepath.ToSlash(rel)
		if relRoot == apiRoot {
			urlPath = "/api" + urlPath
		}
		groupName := filepath.ToSlash(filepath.Dir(rel))

		addRoute(groupName, Route{Method: method, Path: urlPath, Source: SourceLocation{File: path}})
		return nil
	})
}
