package stubs

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FormatTable formats stub results as a human-readable table for --dry-run output.
// Each row shows the action (write/skip), the output file path, and the source route.
func FormatTable(results []Result) string {
	if len(results) == 0 {
		return "No stubs would be generated.\n"
	}

	const statusW = 5
	fileW := len("FILE")
	routeW := len("ROUTE")
	for _, r := range results {
		if n := len(r.FilePath); n > fileW {
			fileW = n
		}
		if n := len(r.Method) + 1 + len(r.Path); n > routeW {
			routeW = n
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%-*s  %-*s  %s\n", statusW, "ACT", fileW, "FILE", "ROUTE")
	fmt.Fprintf(&b, "%s  %s  %s\n",
		strings.Repeat("-", statusW),
		strings.Repeat("-", fileW),
		strings.Repeat("-", routeW),
	)

	writes, skips := 0, 0
	for _, r := range results {
		act := "write"
		if r.Skipped {
			act = "skip"
			skips++
		} else {
			writes++
		}
		route := r.Method + " " + r.Path
		fmt.Fprintf(&b, "%-*s  %-*s  %s\n", statusW, act, fileW, r.FilePath, route)
	}

	fmt.Fprintf(&b, "\n%d stub(s) to write, %d skipped (already exist)\n", writes, skips)
	return b.String()
}

type jsonEntry struct {
	FilePath string `json:"file"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Skipped  bool   `json:"skipped"`
}

// FormatJSON formats stub results as a JSON array for --dry-run --format json output.
func FormatJSON(results []Result) ([]byte, error) {
	entries := make([]jsonEntry, len(results))
	for i, r := range results {
		entries[i] = jsonEntry{
			FilePath: r.FilePath,
			Method:   r.Method,
			Path:     r.Path,
			Skipped:  r.Skipped,
		}
	}
	return json.MarshalIndent(entries, "", "  ")
}
