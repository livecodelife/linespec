// Package ignorewalk provides a filepath.Walk-alike that additionally skips
// paths a project's own .gitignore files (root and nested) would exclude,
// on top of a caller-supplied denylist of directory names to always skip.
//
// discover's directory walks previously relied solely on a hardcoded
// denylist (agnosticSkipDirs) and never consulted .gitignore, so any
// gitignored build/output directory outside that denylist — .next/,
// dist/, coverage/, and the like — got walked and treated as source code
// (prov-2026-f0b20266). Walk is the single implementation all three of
// discover's walk sites use to fix that uniformly.
package ignorewalk

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WalkFunc mirrors filepath.WalkFunc: fn may return filepath.SkipDir for a
// directory to skip its contents without aborting the walk.
type WalkFunc func(path string, info os.FileInfo, err error) error

// DefaultSkipDirs lists directory names every discover walk skips
// regardless of .gitignore — VCS metadata and dependency directories whose
// contents are never the user's own code, and the fallback denylist for
// repos with no .gitignore file at all.
var DefaultSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
}

// Walk walks the file tree rooted at root like filepath.Walk, visiting root
// and every descendant in lexical order, except:
//
//   - a directory whose base name is in skipDirNames is never descended
//     into (a fallback denylist, e.g. VCS metadata or dependency
//     directories, that applies regardless of .gitignore);
//   - a directory or file excluded by a .gitignore file found at root or
//     any directory between root and it is skipped, scoped the same way
//     git itself scopes nested .gitignore files.
//
// fn is never called for a path skipped by either mechanism, matching how
// existing callers used filepath.SkipDir purely to implement their own
// denylist check with no other side effect on the directory node itself.
func Walk(root string, skipDirNames map[string]bool, fn WalkFunc) error {
	info, err := os.Lstat(root)
	if err != nil {
		return fn(root, nil, err)
	}
	return walk(root, root, nil, skipDirNames, info, fn)
}

func walk(path, root string, stack []*ruleSet, skipDirNames map[string]bool, info os.FileInfo, fn WalkFunc) error {
	relDir := rootRel(root, path)
	if rs, err := loadGitignore(filepath.Join(path, ".gitignore"), relDir); err == nil && rs != nil {
		stack = append(stack, rs)
	}

	if err := fn(path, info, nil); err != nil {
		if err == filepath.SkipDir {
			return nil
		}
		return err
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, e := range entries {
		childPath := filepath.Join(path, e.Name())
		childInfo, err := e.Info()
		if err != nil {
			if ferr := fn(childPath, nil, err); ferr != nil {
				return ferr
			}
			continue
		}

		if childInfo.IsDir() && skipDirNames[e.Name()] {
			continue
		}

		childRel := rootRel(root, childPath)
		if isIgnored(stack, childRel, childInfo.IsDir()) {
			continue
		}

		if childInfo.IsDir() {
			if err := walk(childPath, root, stack, skipDirNames, childInfo, fn); err != nil {
				return err
			}
			continue
		}
		if err := fn(childPath, childInfo, nil); err != nil {
			return err
		}
	}
	return nil
}

// isIgnored reports whether relPath (root-relative, "/"-separated) is
// ignored under the active stack of .gitignore rule sets — root-to-leaf
// order, so a rule set closer to relPath (found in a deeper directory)
// overrides a match from a shallower one, mirroring git's own precedence
// for nested .gitignore files.
func isIgnored(stack []*ruleSet, relPath string, isDir bool) bool {
	verdict := false
	for _, rs := range stack {
		local := relPath
		if rs.baseDir != "" {
			local = strings.TrimPrefix(relPath, rs.baseDir+"/")
		}
		if matched, ignored := rs.evaluate(local, isDir); matched {
			verdict = ignored
		}
	}
	return verdict
}

func rootRel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
