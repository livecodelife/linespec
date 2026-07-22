package graph_test

import (
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/discover/graph"
	"github.com/livecodelife/linespec/v3/pkg/discover/lang"
)

func TestDirectoryGrouper_Group(t *testing.T) {
	files := []graph.File{
		{Path: "pkg/handlers/users.go", Lang: lang.Go},
		{Path: "pkg/handlers/posts.go", Lang: lang.Go},
		{Path: "pkg/db/conn.go", Lang: lang.Go},
		{Path: "main.go", Lang: lang.Go},
	}

	groups, err := (graph.DirectoryGrouper{}).Group(files)
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d: %+v", len(groups), groups)
	}

	// Groups are sorted by directory name: ".", "pkg/db", "pkg/handlers".
	if groups[0].Name != "." || len(groups[0].Files) != 1 || groups[0].Files[0].Path != "main.go" {
		t.Errorf("unexpected root group: %+v", groups[0])
	}
	if groups[1].Name != "pkg/db" || len(groups[1].Files) != 1 {
		t.Errorf("unexpected pkg/db group: %+v", groups[1])
	}
	if groups[2].Name != "pkg/handlers" || len(groups[2].Files) != 2 {
		t.Errorf("unexpected pkg/handlers group: %+v", groups[2])
	}
	// Files within a group are sorted by path.
	if groups[2].Files[0].Path != "pkg/handlers/posts.go" || groups[2].Files[1].Path != "pkg/handlers/users.go" {
		t.Errorf("expected files sorted by path, got %+v", groups[2].Files)
	}
}

func TestDirectoryGrouper_Empty(t *testing.T) {
	groups, err := (graph.DirectoryGrouper{}).Group(nil)
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("expected no groups for empty input, got %+v", groups)
	}
}

// grouperInterface is a compile-time check that DirectoryGrouper satisfies Grouper,
// so a future community-detection grouper can be substituted without pipeline changes.
var _ graph.Grouper = graph.DirectoryGrouper{}

func TestCoveredDirs(t *testing.T) {
	covered := graph.CoveredDirs([]string{
		"pkg/handlers/router.go",
		"pkg/handlers/other.go",
		"main.go",
	})
	if len(covered) != 2 {
		t.Fatalf("expected 2 covered dirs, got %d: %+v", len(covered), covered)
	}
	if !covered["pkg/handlers"] || !covered["."] {
		t.Errorf("expected pkg/handlers and . to be covered, got %+v", covered)
	}
	if covered["pkg/services"] {
		t.Errorf("pkg/services should not be covered")
	}
}

func TestFilterUncovered(t *testing.T) {
	files := []graph.File{
		{Path: "pkg/handlers/router.go", Lang: lang.Go},
		{Path: "pkg/services/worker.go", Lang: lang.Go},
		{Path: "pkg/models/user.go", Lang: lang.Go},
	}
	covered := map[string]bool{"pkg/handlers": true}

	got := graph.FilterUncovered(files, covered)
	if len(got) != 2 {
		t.Fatalf("expected 2 uncovered files, got %d: %+v", len(got), got)
	}
	for _, f := range got {
		if f.Path == "pkg/handlers/router.go" {
			t.Errorf("covered file %q should have been filtered out", f.Path)
		}
	}
}

func TestFilterUncovered_NoneCovered(t *testing.T) {
	files := []graph.File{
		{Path: "pkg/services/worker.go", Lang: lang.Go},
	}
	got := graph.FilterUncovered(files, map[string]bool{})
	if len(got) != 1 {
		t.Fatalf("expected all files to pass through when nothing is covered, got %+v", got)
	}
}

// TestCoveredDirExtras reproduces the mixed-directory shape flagged in
// prov-2026-3486daec's review: a directory with both a route file
// (router.go) and a plain non-route file (helper.go) sharing a package.
// helper.go must not disappear — it should surface here so it can be merged
// into the route group's existing blueprint instead of being dropped.
func TestCoveredDirExtras(t *testing.T) {
	files := []graph.File{
		{Path: "pkg/handlers/router.go", Lang: lang.Go},
		{Path: "pkg/handlers/helper.go", Lang: lang.Go},
		{Path: "pkg/services/worker.go", Lang: lang.Go},
	}
	routeFiles := []string{"pkg/handlers/router.go"}
	covered := graph.CoveredDirs(routeFiles)

	got := graph.CoveredDirExtras(files, covered, routeFiles)
	if len(got) != 1 || got[0].Path != "pkg/handlers/helper.go" {
		t.Fatalf("expected only pkg/handlers/helper.go, got %+v", got)
	}
}

func TestCoveredDirExtras_NoneCovered(t *testing.T) {
	files := []graph.File{
		{Path: "pkg/services/worker.go", Lang: lang.Go},
	}
	got := graph.CoveredDirExtras(files, map[string]bool{}, nil)
	if len(got) != 0 {
		t.Fatalf("expected no extras when nothing is covered, got %+v", got)
	}
}
