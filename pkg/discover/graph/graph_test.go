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
