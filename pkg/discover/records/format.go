package records

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FormatTable returns a human-readable table view of discover record results.
func FormatTable(results []Result, sum Summary) string {
	var b strings.Builder

	if len(results) == 0 {
		b.WriteString("No records generated — no route groups discovered.\n")
		fmt.Fprintf(&b, "\nSummary: %d routes, %d protocol boundaries\n", sum.RouteCount, sum.BoundaryCount)
		return b.String()
	}

	fmt.Fprintf(&b, "  %-50s  %-6s  %s\n", "TITLE", "ROUTES", "RECORD ID")
	fmt.Fprintf(&b, "  %s\n", strings.Repeat("-", 75))
	for _, r := range results {
		title := r.Title
		if len(title) > 48 {
			title = title[:45] + "..."
		}
		fmt.Fprintf(&b, "  %-50s  %-6d  %s\n", title, r.RouteCount, r.RecordID)
	}

	toWrite := 0
	for _, r := range results {
		_ = r
		toWrite++
	}

	fmt.Fprintf(&b, "\n  %d route(s), %d protocol boundary hit(s), %d record(s) to create\n",
		sum.RouteCount, sum.BoundaryCount, sum.RecordsCreated)

	if len(sum.Unclassified) > 0 {
		fmt.Fprintf(&b, "\n  Unclassified (%d):\n", len(sum.Unclassified))
		for _, f := range sum.Unclassified {
			fmt.Fprintf(&b, "    %s\n", f)
		}
	}
	return b.String()
}

// FormatJSON returns a JSON representation of the discover record results.
func FormatJSON(results []Result, sum Summary) ([]byte, error) {
	type jsonResult struct {
		GroupName  string `json:"group"`
		RecordID   string `json:"record_id"`
		Title      string `json:"title"`
		RouteCount int    `json:"route_count"`
		FilePath   string `json:"file"`
	}
	type jsonOutput struct {
		Records        []jsonResult `json:"records"`
		RouteCount     int          `json:"route_count"`
		BoundaryCount  int          `json:"boundary_count"`
		RecordsCreated int          `json:"records_created"`
		Unclassified   []string     `json:"unclassified"`
	}

	jr := make([]jsonResult, len(results))
	for i, r := range results {
		jr[i] = jsonResult{
			GroupName:  r.GroupName,
			RecordID:   r.RecordID,
			Title:      r.Title,
			RouteCount: r.RouteCount,
			FilePath:   r.FilePath,
		}
	}

	unclassified := sum.Unclassified
	if unclassified == nil {
		unclassified = []string{}
	}

	out := jsonOutput{
		Records:        jr,
		RouteCount:     sum.RouteCount,
		BoundaryCount:  sum.BoundaryCount,
		RecordsCreated: sum.RecordsCreated,
		Unclassified:   unclassified,
	}
	return json.MarshalIndent(out, "", "  ")
}
