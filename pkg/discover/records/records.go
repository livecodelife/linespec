package records

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/livecodelife/linespec/pkg/discover/boundaries"
	"github.com/livecodelife/linespec/pkg/discover/routes"
	"github.com/livecodelife/linespec/pkg/discover/stubs"
	"github.com/livecodelife/linespec/pkg/provenance"
)

// Input holds all data needed to generate draft blueprint records for discovered route groups.
type Input struct {
	Groups        []routes.Group
	StubResults   []stubs.Result              // from Phase 3; matched to groups by method+path
	Boundaries    map[string][]boundaries.Hit // handlerRef → hits; used for summary boundary count
	ProvenanceDir string                      // directory to write records into (e.g. "provenance")
	SpecsDir      string                      // root-relative path to specs (e.g. "specs")
	Author        string                      // author field for generated records; defaults to "linespec-discover"
}

// Result is the outcome of generating a single blueprint record for a route group.
type Result struct {
	GroupName  string
	RecordID   string
	FilePath   string
	Title      string
	RouteCount int
}

// Summary is the final discover report, aggregated across all phases.
type Summary struct {
	RouteCount     int
	BoundaryCount  int
	RecordsCreated int
	Unclassified   []string // source files not matched to any route group
}

// Plan computes what Write would produce without touching the filesystem.
// Useful for --dry-run output. Results are in the same order as in.Groups.
func Plan(in Input, existingIDs []string) ([]Result, Summary) {
	year := provenance.CurrentYear()
	ids := append([]string(nil), existingIDs...)

	results := make([]Result, 0, len(in.Groups))
	for _, g := range in.Groups {
		id, err := provenance.NextID(year, ids)
		if err != nil {
			continue
		}
		ids = append(ids, id)
		title := deriveTitle(g.Name)
		results = append(results, Result{
			GroupName:  g.Name,
			RecordID:   id,
			FilePath:   filepath.Join(in.ProvenanceDir, id+".yml"),
			Title:      title,
			RouteCount: len(g.Routes),
		})
	}

	return results, computeSummary(in, len(results))
}

// Write generates and saves draft blueprint records to in.ProvenanceDir.
// Discover is additive — existing records in in.ProvenanceDir are never modified.
// The provenance directory is created if it does not exist.
func Write(in Input, existingIDs []string) ([]Result, Summary, error) {
	if err := os.MkdirAll(in.ProvenanceDir, 0755); err != nil {
		return nil, Summary{}, fmt.Errorf("create provenance dir %s: %w", in.ProvenanceDir, err)
	}

	loader := provenance.NewLoader(in.ProvenanceDir, nil)

	year := provenance.CurrentYear()
	ids := append([]string(nil), existingIDs...)

	author := in.Author
	if author == "" {
		author = "linespec-discover"
	}

	stubIndex := indexStubs(in.StubResults)

	results := make([]Result, 0, len(in.Groups))
	for _, g := range in.Groups {
		id, err := provenance.NextID(year, ids)
		if err != nil {
			return nil, Summary{}, fmt.Errorf("generate record ID: %w", err)
		}
		ids = append(ids, id)

		title := deriveTitle(g.Name)
		filePath := filepath.Join(in.ProvenanceDir, id+".yml")

		record := &provenance.Record{
			ID:               id,
			Title:            title,
			Status:           provenance.StatusDraft,
			Type:             provenance.RecordTypeBlueprint,
			CreatedAt:        provenance.CurrentDate(),
			Author:           author,
			Intent:           "",
			Constraints:      []string{},
			AffectedScope:    groupAffectedScope(g),
			ForbiddenScope:   []string{},
			Supersedes:       "",
			SupersededBy:     "",
			Related:          []string{},
			AssociatedSpecs:  groupAssociatedSpecs(g, stubIndex),
			AssociatedTraces: []string{},
			Monitors:         []string{},
			Tags:             []string{"discover"},
			FilePath:         filePath,
		}

		if err := loader.SaveRecord(record); err != nil {
			return nil, Summary{}, fmt.Errorf("save record %s: %w", id, err)
		}

		results = append(results, Result{
			GroupName:  g.Name,
			RecordID:   id,
			FilePath:   filePath,
			Title:      title,
			RouteCount: len(g.Routes),
		})
	}

	return results, computeSummary(in, len(results)), nil
}

// deriveTitle converts a group name into a human-readable blueprint title.
//   - File paths: "app/controllers/users_controller.rb" → "users_controller endpoints"
//   - Controller names: "UsersController" → "UsersController endpoints"
//   - Go package names: "handlers" → "handlers endpoints"
func deriveTitle(name string) string {
	if strings.Contains(name, "/") || strings.Contains(name, string(os.PathSeparator)) {
		base := filepath.Base(name)
		ext := filepath.Ext(base)
		clean := strings.TrimSuffix(base, ext)
		return clean + " endpoints"
	}
	return name + " endpoints"
}

// groupAffectedScope returns the deduplicated set of source file paths for a group's routes.
func groupAffectedScope(g routes.Group) []string {
	seen := make(map[string]bool)
	var scope []string
	for _, r := range g.Routes {
		if r.Source.File != "" && !seen[r.Source.File] {
			seen[r.Source.File] = true
			scope = append(scope, r.Source.File)
		}
	}
	return scope
}

// indexStubs builds a "METHOD path" → FilePath lookup from stub results.
func indexStubs(results []stubs.Result) map[string]string {
	m := make(map[string]string, len(results))
	for _, r := range results {
		m[strings.ToUpper(r.Method)+" "+r.Path] = r.FilePath
	}
	return m
}

// groupAssociatedSpecs returns AssociatedSpec entries for the stub files matching a group's routes.
func groupAssociatedSpecs(g routes.Group, stubIndex map[string]string) []provenance.AssociatedSpec {
	var specs []provenance.AssociatedSpec
	seen := make(map[string]bool)
	for _, r := range g.Routes {
		key := strings.ToUpper(r.Method) + " " + r.Path
		path, ok := stubIndex[key]
		if !ok || seen[path] {
			continue
		}
		seen[path] = true
		specs = append(specs, provenance.AssociatedSpec{
			Path: path,
			Type: "linespec",
		})
	}
	return specs
}

// computeSummary aggregates the discover run statistics.
func computeSummary(in Input, created int) Summary {
	routeCount := 0
	for _, g := range in.Groups {
		routeCount += len(g.Routes)
	}

	boundaryCount := 0
	for _, hits := range in.Boundaries {
		boundaryCount += len(hits)
	}

	return Summary{
		RouteCount:     routeCount,
		BoundaryCount:  boundaryCount,
		RecordsCreated: created,
		Unclassified:   []string{},
	}
}
