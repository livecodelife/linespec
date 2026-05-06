package provenance

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// GenerateOptions holds options for the generate command.
type GenerateOptions struct {
	RecordID   string // optional: target a specific record
	Format     string // "markdown" (default) or "yaml"
	OutputFile string // optional: write to file instead of stdout
	ConfigFile string
}

// specEntry is the intermediate representation of one section in the generated document.
type specEntry struct {
	ID          string
	Title       string
	Type        RecordType
	Status      Status
	Tags        []string
	Intent      string
	Constraints []string
	Blueprints  []specEntry // populated only for brief entries
}

// specDocument holds the full generated document before rendering.
type specDocument struct {
	GeneratedAt string
	Source      string // "all" or a record ID
	Sections    []specEntry
}

// Generate builds a behavioral specification document from provenance records.
func (c *Commands) Generate(opts GenerateOptions) error {
	out := c.Formatter.Output
	if opts.OutputFile != "" {
		f, err := os.Create(opts.OutputFile)
		if err != nil {
			return fmt.Errorf("failed to create output file: %w", err)
		}
		defer f.Close()
		out = f
	}

	format := opts.Format
	if format == "" {
		format = "markdown"
	}

	var doc *specDocument
	var err error
	if opts.RecordID != "" {
		doc, err = c.generateFromRecord(opts.RecordID)
	} else {
		doc, err = c.generateFromAll()
	}
	if err != nil {
		return err
	}

	if format == "yaml" {
		return c.writeSpecYAML(out, doc)
	}
	return c.writeSpecMarkdown(out, doc)
}

func (c *Commands) generateFromRecord(id string) (*specDocument, error) {
	record, exists := c.Loader.GetRecord(id)
	if !exists {
		return nil, fmt.Errorf("record not found: %s", id)
	}

	effectiveType := record.Type
	if effectiveType == "" {
		effectiveType = RecordTypeBlueprint
	}

	switch effectiveType {
	case RecordTypeImprint:
		return nil, fmt.Errorf("imprint records cannot be used as a generation target")
	case RecordTypeBug:
		return nil, fmt.Errorf("bug records cannot be used as a generation target")
	}

	if record.Status == StatusSuperseded || record.Status == StatusDeprecated {
		fmt.Fprintf(os.Stderr, "Warning: record %s has status %q — the generated document may contain stale information\n", id, record.Status)
	}

	doc := &specDocument{
		GeneratedAt: time.Now().Format("2006-01-02"),
		Source:      id,
	}

	if effectiveType == RecordTypeBrief {
		doc.Sections = []specEntry{c.buildBriefEntry(record)}
	} else {
		intent, constraints := c.mergedBlueprintContent(record)
		doc.Sections = []specEntry{{
			ID:          record.ID,
			Title:       record.Title,
			Type:        RecordTypeBlueprint,
			Status:      record.Status,
			Tags:        record.Tags,
			Intent:      intent,
			Constraints: constraints,
		}}
	}

	return doc, nil
}

func (c *Commands) generateFromAll() (*specDocument, error) {
	doc := &specDocument{
		GeneratedAt: time.Now().Format("2006-01-02"),
		Source:      "all",
	}

	var briefs []*Record
	for _, r := range c.Loader.Records {
		if r.Type == RecordTypeBrief && (r.Status == StatusOpen || r.Status == StatusImplemented) {
			briefs = append(briefs, r)
		}
	}
	sort.Slice(briefs, func(i, j int) bool { return briefs[i].ID < briefs[j].ID })

	// Track records already included under a brief section to avoid double-inclusion.
	underBrief := make(map[string]bool)
	for _, brief := range briefs {
		entry := c.buildBriefEntry(brief)
		for _, bp := range entry.Blueprints {
			underBrief[bp.ID] = true
		}
		doc.Sections = append(doc.Sections, entry)
	}

	// Standalone sections: active non-brief, non-imprint records at the tip of their
	// supersedes chain that are not already included under a brief.
	var standalone []specEntry
	seen := make(map[string]bool)
	for _, r := range c.Loader.Records {
		if r.Type == RecordTypeBrief || r.Type == RecordTypeImprint {
			continue
		}
		if r.Status != StatusOpen && r.Status != StatusImplemented {
			continue
		}
		if r.SupersededBy != "" && r.SupersededBy != "null" {
			continue
		}
		if underBrief[r.ID] || seen[r.ID] {
			continue
		}
		seen[r.ID] = true

		effectiveType := r.Type
		if effectiveType == "" {
			effectiveType = RecordTypeBlueprint
		}

		intent, constraints := c.mergedBlueprintContent(r)
		standalone = append(standalone, specEntry{
			ID:          r.ID,
			Title:       r.Title,
			Type:        effectiveType,
			Status:      r.Status,
			Tags:        r.Tags,
			Intent:      intent,
			Constraints: constraints,
		})
	}
	sort.Slice(standalone, func(i, j int) bool { return standalone[i].ID < standalone[j].ID })
	doc.Sections = append(doc.Sections, standalone...)

	return doc, nil
}

// buildBriefEntry builds a specEntry for a brief, resolving its implementing blueprints.
func (c *Commands) buildBriefEntry(brief *Record) specEntry {
	entry := specEntry{
		ID:          brief.ID,
		Title:       brief.Title,
		Type:        RecordTypeBrief,
		Status:      brief.Status,
		Tags:        brief.Tags,
		Intent:      brief.Intent,
		Constraints: brief.Constraints,
	}

	var implementing []*Record
	for _, r := range c.Loader.Records {
		if r.Implements == brief.ID {
			implementing = append(implementing, r)
		}
	}
	sort.Slice(implementing, func(i, j int) bool { return implementing[i].ID < implementing[j].ID })

	seen := make(map[string]bool)
	for _, r := range implementing {
		active := c.resolveToTip(r)
		if active == nil || seen[active.ID] {
			continue
		}
		seen[active.ID] = true

		effectiveType := active.Type
		if effectiveType == "" {
			effectiveType = RecordTypeBlueprint
		}

		intent, constraints := c.mergedBlueprintContent(active)
		entry.Blueprints = append(entry.Blueprints, specEntry{
			ID:          active.ID,
			Title:       active.Title,
			Type:        effectiveType,
			Status:      active.Status,
			Tags:        active.Tags,
			Intent:      intent,
			Constraints: constraints,
		})
	}

	return entry
}

// resolveToTip follows SupersededBy links to the current active tip record.
// Returns nil if the chain leads to an inactive or missing record.
func (c *Commands) resolveToTip(r *Record) *Record {
	current := r
	for {
		if current.SupersededBy == "" || current.SupersededBy == "null" {
			if current.Status == StatusSuperseded || current.Status == StatusDeprecated {
				return nil
			}
			return current
		}
		next, exists := c.Loader.GetRecord(current.SupersededBy)
		if !exists {
			return nil
		}
		current = next
	}
}

// mergedBlueprintContent returns the intent and constraints for a record with any
// active extending bug records' content merged in.
func (c *Commands) mergedBlueprintContent(r *Record) (intent string, constraints []string) {
	intent = r.Intent
	constraints = append(constraints, r.Constraints...)

	for _, bug := range c.Loader.Records {
		if bug.Type != RecordTypeBug {
			continue
		}
		if bug.Status == StatusSuperseded || bug.Status == StatusDeprecated {
			continue
		}
		if bug.Extends != r.ID {
			continue
		}
		if bug.Intent != "" {
			if intent != "" {
				intent += "\n\n" + bug.Intent
			} else {
				intent = bug.Intent
			}
		}
		constraints = append(constraints, bug.Constraints...)
	}
	return
}

// writeSpecMarkdown renders the document as Phoenix-compatible Markdown.
// Format: # title, ## section, ### subsection, intent as prose paragraph,
// constraints as bullet points. No metadata, no front matter.
func (c *Commands) writeSpecMarkdown(w io.Writer, doc *specDocument) error {
	// Document title
	if doc.Source == "all" || len(doc.Sections) != 1 {
		fmt.Fprintln(w, "# Behavioral Specification")
	} else {
		fmt.Fprintf(w, "# %s\n", doc.Sections[0].Title)
	}

	for i, section := range doc.Sections {
		fmt.Fprintln(w)

		// Top-level section heading
		if doc.Source == "all" || len(doc.Sections) != 1 {
			fmt.Fprintf(w, "## %s\n", section.Title)
		}
		// When there is exactly one section and the title is already the h1,
		// we skip repeating it as h2.

		writeSpecBody(w, section.Intent, section.Constraints)

		// Blueprint subsections
		for _, bp := range section.Blueprints {
			fmt.Fprintln(w)
			if doc.Source == "all" || len(doc.Sections) != 1 {
				fmt.Fprintf(w, "### %s\n", bp.Title)
			} else {
				fmt.Fprintf(w, "## %s\n", bp.Title)
			}
			writeSpecBody(w, bp.Intent, bp.Constraints)
		}

		// Blank line between top-level sections (except after the last one)
		if i < len(doc.Sections)-1 {
			fmt.Fprintln(w)
		}
	}

	return nil
}

// writeSpecBody writes an intent paragraph and constraint bullet list.
func writeSpecBody(w io.Writer, intent string, constraints []string) {
	intent = dedent(intent)
	if intent != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, intent)
	}
	if len(constraints) > 0 {
		fmt.Fprintln(w)
		for _, constraint := range constraints {
			fmt.Fprintf(w, "- %s\n", strings.TrimSpace(constraint))
		}
	}
}

// dedent strips leading whitespace from every line and surrounding blank lines.
// This normalises YAML block-scalar indentation artifacts in prose intent text.
func dedent(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimLeft(line, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// writeSpecYAML renders the document as structured YAML.
func (c *Commands) writeSpecYAML(w io.Writer, doc *specDocument) error {
	type yamlEntry struct {
		ID          string      `yaml:"id"`
		Title       string      `yaml:"title"`
		Type        string      `yaml:"type"`
		Status      string      `yaml:"status"`
		Tags        []string    `yaml:"tags,omitempty"`
		Intent      string      `yaml:"intent,omitempty"`
		Constraints []string    `yaml:"constraints,omitempty"`
		Blueprints  []yamlEntry `yaml:"blueprints,omitempty"`
	}
	type yamlDoc struct {
		LinespecSpecVersion string      `yaml:"linespec_spec_version"`
		GeneratedAt         string      `yaml:"generated_at"`
		Source              string      `yaml:"source"`
		Specifications      []yamlEntry `yaml:"specifications"`
	}

	toYAMLEntry := func(s specEntry) yamlEntry {
		e := yamlEntry{
			ID:          s.ID,
			Title:       s.Title,
			Type:        string(s.Type),
			Status:      string(s.Status),
			Tags:        s.Tags,
			Intent:      strings.TrimSpace(s.Intent),
			Constraints: s.Constraints,
		}
		for _, bp := range s.Blueprints {
			e.Blueprints = append(e.Blueprints, yamlEntry{
				ID:          bp.ID,
				Title:       bp.Title,
				Type:        string(bp.Type),
				Status:      string(bp.Status),
				Tags:        bp.Tags,
				Intent:      strings.TrimSpace(bp.Intent),
				Constraints: bp.Constraints,
			})
		}
		return e
	}

	out := yamlDoc{
		LinespecSpecVersion: "1.0",
		GeneratedAt:         doc.GeneratedAt,
		Source:              doc.Source,
	}
	for _, s := range doc.Sections {
		out.Specifications = append(out.Specifications, toYAMLEntry(s))
	}

	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	return enc.Encode(out)
}
