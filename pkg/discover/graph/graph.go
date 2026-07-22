// Package graph groups source files into logical subsystems for blueprint record generation.
package graph

import (
	"path/filepath"
	"sort"

	"github.com/livecodelife/linespec/v3/pkg/discover/lang"
	"github.com/livecodelife/linespec/v3/pkg/discover/symbols"
)

// File is one source file discovered during a framework-agnostic scan.
type File struct {
	Path    string // relative to the scan root
	Lang    lang.Language
	Symbols []symbols.Symbol
	Imports []string
}

// Group is a cluster of related files sharing a grouping identity.
type Group struct {
	Name  string
	Files []File
}

// Grouper clusters discovered files into logical groups for blueprint record
// generation. Directory grouping (Option A, see DirectoryGrouper) is the
// default. A future community-detection grouper (Option B — clustering by
// import graph connectivity) can implement this interface and be substituted
// in without changing the rest of the discover pipeline.
type Grouper interface {
	Group(files []File) ([]Group, error)
}

// CoveredDirs returns the set of directories containing at least one of routeFiles.
// A directory in this set already has framework-based blueprint coverage (it
// contributed a route to some group) and must not be re-grouped by a
// supplemental, framework-agnostic pass — otherwise the same directory would
// receive two blueprint records.
func CoveredDirs(routeFiles []string) map[string]bool {
	covered := make(map[string]bool, len(routeFiles))
	for _, f := range routeFiles {
		covered[filepath.Dir(f)] = true
	}
	return covered
}

// FilterUncovered returns the subset of files whose containing directory is
// not in covered, preserving order.
func FilterUncovered(files []File, covered map[string]bool) []File {
	var out []File
	for _, f := range files {
		if !covered[filepath.Dir(f.Path)] {
			out = append(out, f)
		}
	}
	return out
}

// CoveredDirExtras returns the subset of files whose containing directory is
// in covered but whose own path is not itself one of routeFiles — e.g.
// helper.go living next to router.go in the same package. FilterUncovered
// already excludes their whole directory from the framework-agnostic
// supplemental pass (so the directory never gets a second, duplicate
// blueprint), but without this they were dropped from coverage entirely
// instead of being merged into the route group's existing blueprint.
func CoveredDirExtras(files []File, covered map[string]bool, routeFiles []string) []File {
	isRoute := make(map[string]bool, len(routeFiles))
	for _, f := range routeFiles {
		isRoute[f] = true
	}
	var out []File
	for _, f := range files {
		if covered[filepath.Dir(f.Path)] && !isRoute[f.Path] {
			out = append(out, f)
		}
	}
	return out
}

// DirectoryGrouper groups files by their containing directory.
type DirectoryGrouper struct{}

// Group implements Grouper by clustering files that share a containing directory.
// Groups are returned sorted by directory name; files within a group are sorted
// by path. The repo root is named ".".
func (DirectoryGrouper) Group(files []File) ([]Group, error) {
	byDir := make(map[string][]File)
	for _, f := range files {
		dir := filepath.Dir(f.Path)
		byDir[dir] = append(byDir[dir], f)
	}

	names := make([]string, 0, len(byDir))
	for dir := range byDir {
		names = append(names, dir)
	}
	sort.Strings(names)

	groups := make([]Group, 0, len(names))
	for _, name := range names {
		fs := byDir[name]
		sort.Slice(fs, func(i, j int) bool { return fs[i].Path < fs[j].Path })
		groups = append(groups, Group{Name: name, Files: fs})
	}
	return groups, nil
}
