// Package graph groups source files into logical subsystems for blueprint record generation.
package graph

import (
	"path/filepath"
	"sort"

	"github.com/livecodelife/linespec/pkg/discover/lang"
	"github.com/livecodelife/linespec/pkg/discover/symbols"
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
