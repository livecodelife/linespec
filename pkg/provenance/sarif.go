package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Version is the current version of the linespec tool
// This should be set during build time using ldflags
var Version = "dev"

// SARIFRuleID represents a stable rule identifier for lint rules
type SARIFRuleID string

const (
	// Rule IDs as specified in the PRD - these are stable and must not change
	PROV001 SARIFRuleID = "PROV001" // InvalidYaml
	PROV002 SARIFRuleID = "PROV002" // MissingRequiredField
	PROV003 SARIFRuleID = "PROV003" // UnknownStatus
	PROV004 SARIFRuleID = "PROV004" // InvalidIdFormat
	PROV005 SARIFRuleID = "PROV005" // InvalidDateFormat
	PROV006 SARIFRuleID = "PROV006" // UnresolvedSupersedes
	PROV007 SARIFRuleID = "PROV007" // SupersededByMismatch
	PROV008 SARIFRuleID = "PROV008" // UnresolvedRelated
	PROV009 SARIFRuleID = "PROV009" // ScopeConflict
	PROV010 SARIFRuleID = "PROV010" // MissingAssociatedSpecs
	PROV011 SARIFRuleID = "PROV011" // ConstraintsWithoutSpecs
	PROV012 SARIFRuleID = "PROV012" // IntentWithoutConstraints
	PROV013 SARIFRuleID = "PROV013" // TitleTooLong
	PROV014 SARIFRuleID = "PROV014" // CircularSupersedes
	PROV015 SARIFRuleID = "PROV015" // ScopeOverlap
	PROV016 SARIFRuleID = "PROV016" // DeadRecord
	PROV017 SARIFRuleID = "PROV017" // ModifiedImplementedRecord
	PROV018 SARIFRuleID = "PROV018" // MissingSpecFile
	PROV019 SARIFRuleID = "PROV019" // InvalidRegexPattern
)

// SARIFRule represents a rule in the SARIF tool descriptor
type SARIFRule struct {
	ID                   string                  `json:"id"`
	Name                 string                  `json:"name"`
	ShortDescription     *SARIFMessage           `json:"shortDescription,omitempty"`
	FullDescription      *SARIFMessage           `json:"fullDescription,omitempty"`
	HelpURI              string                  `json:"helpUri,omitempty"`
	DefaultConfiguration *SARIFRuleConfiguration `json:"defaultConfiguration,omitempty"`
}

// SARIFRuleConfiguration represents rule configuration in SARIF
type SARIFRuleConfiguration struct {
	Level string `json:"level,omitempty"`
}

// SARIFMessage represents a message in SARIF
type SARIFMessage struct {
	Text string `json:"text"`
}

// SARIFTool represents the tool descriptor in SARIF
type SARIFTool struct {
	Driver *SARIFDriver `json:"driver"`
}

// SARIFDriver represents the driver information in SARIF
type SARIFDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version"`
	InformationURI string      `json:"informationUri,omitempty"`
	Rules          []SARIFRule `json:"rules"`
}

// SARIFLocation represents a location in SARIF
type SARIFLocation struct {
	PhysicalLocation *SARIFPhysicalLocation `json:"physicalLocation"`
}

// SARIFPhysicalLocation represents a physical location in SARIF
type SARIFPhysicalLocation struct {
	ArtifactLocation *SARIFArtifactLocation `json:"artifactLocation"`
	Region           *SARIFRegion           `json:"region,omitempty"`
}

// SARIFArtifactLocation represents an artifact location in SARIF
type SARIFArtifactLocation struct {
	URI       string `json:"uri"`
	URIBaseID string `json:"uriBaseId,omitempty"`
}

// SARIFRegion represents a region in a file in SARIF
type SARIFRegion struct {
	StartLine   int `json:"startLine,omitempty"`
	StartColumn int `json:"startColumn,omitempty"`
}

// SARIFResult represents a single result (finding) in SARIF
type SARIFResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   *SARIFMessage   `json:"message"`
	Locations []SARIFLocation `json:"locations"`
}

// SARIFArtifact represents an artifact in the SARIF document
type SARIFArtifact struct {
	Location *SARIFArtifactLocation `json:"location"`
	MimeType string                 `json:"mimeType,omitempty"`
	Hashes   map[string]string      `json:"hashes,omitempty"`
}

// SARIFRun represents a single run in SARIF
type SARIFRun struct {
	Tool      *SARIFTool      `json:"tool"`
	Results   []SARIFResult   `json:"results"`
	Artifacts []SARIFArtifact `json:"artifacts,omitempty"`
}

// SARIFDocument represents the root SARIF document
type SARIFDocument struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []SARIFRun `json:"runs"`
}

// NewSARIFDocument creates a new empty SARIF document
func NewSARIFDocument() *SARIFDocument {
	return &SARIFDocument{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs: []SARIFRun{
			{
				Tool:      NewSARIFTool(),
				Results:   []SARIFResult{},
				Artifacts: []SARIFArtifact{},
			},
		},
	}
}

// NewSARIFTool creates the tool descriptor with the complete rule catalog
func NewSARIFTool() *SARIFTool {
	return &SARIFTool{
		Driver: &SARIFDriver{
			Name:           "linespec",
			Version:        Version,
			InformationURI: "https://linespec.dev/docs/provenance/lint-rules",
			Rules:          GetAllRules(),
		},
	}
}

// GetAllRules returns the complete rule catalog for SARIF
func GetAllRules() []SARIFRule {
	return []SARIFRule{
		{
			ID:   string(PROV001),
			Name: "InvalidYaml",
			ShortDescription: &SARIFMessage{
				Text: "File is not valid YAML",
			},
			FullDescription: &SARIFMessage{
				Text: "The provenance record file could not be parsed as valid YAML. The file must be syntactically valid YAML before any schema validation can occur.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV001",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "error",
			},
		},
		{
			ID:   string(PROV002),
			Name: "MissingRequiredField",
			ShortDescription: &SARIFMessage{
				Text: "A required field is absent or empty",
			},
			FullDescription: &SARIFMessage{
				Text: "A required field (id, title, status, created_at, author, intent) is missing or has an empty value.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV002",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "error",
			},
		},
		{
			ID:   string(PROV003),
			Name: "UnknownStatus",
			ShortDescription: &SARIFMessage{
				Text: "status is not a known enum value",
			},
			FullDescription: &SARIFMessage{
				Text: "The status field must be one of: open, implemented, superseded, deprecated.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV003",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "error",
			},
		},
		{
			ID:   string(PROV004),
			Name: "InvalidIdFormat",
			ShortDescription: &SARIFMessage{
				Text: "id does not match prov-YYYY-NNN",
			},
			FullDescription: &SARIFMessage{
				Text: "The ID must match the format prov-YYYY-NNN or prov-YYYY-XXXXXXXX (legacy sequential or crypto random format).",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV004",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "error",
			},
		},
		{
			ID:   string(PROV005),
			Name: "InvalidDateFormat",
			ShortDescription: &SARIFMessage{
				Text: "created_at is not a valid ISO 8601 date",
			},
			FullDescription: &SARIFMessage{
				Text: "The created_at field must be in YYYY-MM-DD format.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV005",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "error",
			},
		},
		{
			ID:   string(PROV006),
			Name: "UnresolvedSupersedes",
			ShortDescription: &SARIFMessage{
				Text: "supersedes references a non-existent record",
			},
			FullDescription: &SARIFMessage{
				Text: "The supersedes field references a record ID that does not exist in the provenance directory or configured shared repositories.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV006",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "error",
			},
		},
		{
			ID:   string(PROV007),
			Name: "SupersededByMismatch",
			ShortDescription: &SARIFMessage{
				Text: "superseded_by disagrees with the graph",
			},
			FullDescription: &SARIFMessage{
				Text: "The superseded_by field does not agree with the actual graph state. This record is superseded by another, or should not be superseded.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV007",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "warning",
			},
		},
		{
			ID:   string(PROV008),
			Name: "UnresolvedRelated",
			ShortDescription: &SARIFMessage{
				Text: "A related entry references a non-existent record",
			},
			FullDescription: &SARIFMessage{
				Text: "An entry in the related array references a record ID that does not exist in the provenance directory.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV008",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "warning",
			},
		},
		{
			ID:   string(PROV009),
			Name: "ScopeConflict",
			ShortDescription: &SARIFMessage{
				Text: "A pattern appears in both affected_scope and forbidden_scope",
			},
			FullDescription: &SARIFMessage{
				Text: "The same pattern cannot appear in both affected_scope and forbidden_scope simultaneously.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV009",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "error",
			},
		},
		{
			ID:   string(PROV010),
			Name: "MissingAssociatedSpecs",
			ShortDescription: &SARIFMessage{
				Text: "Open record has no associated_specs",
			},
			FullDescription: &SARIFMessage{
				Text: "An open record should have at least one associated spec to provide behavioral proof of its constraints. The severity of this finding depends on enforcement level.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV010",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "warning", // Default level, actual level varies by enforcement
			},
		},
		{
			ID:   string(PROV011),
			Name: "ConstraintsWithoutSpecs",
			ShortDescription: &SARIFMessage{
				Text: "Open record has constraints but no associated_specs",
			},
			FullDescription: &SARIFMessage{
				Text: "The record has behavioral constraints but no associated specs to prove they are met. Consider adding LineSpec tests or other proof artifacts.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV011",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "note",
			},
		},
		{
			ID:   string(PROV012),
			Name: "IntentWithoutConstraints",
			ShortDescription: &SARIFMessage{
				Text: "Record has intent but no constraints",
			},
			FullDescription: &SARIFMessage{
				Text: "The record expresses intent but lacks specific behavioral constraints. Consider adding constraints to make the intent actionable.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV012",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "note",
			},
		},
		{
			ID:   string(PROV013),
			Name: "TitleTooLong",
			ShortDescription: &SARIFMessage{
				Text: "title exceeds 120 characters",
			},
			FullDescription: &SARIFMessage{
				Text: "The title should be concise and not exceed 120 characters for readability.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV013",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "warning",
			},
		},
		{
			ID:   string(PROV014),
			Name: "CircularSupersedes",
			ShortDescription: &SARIFMessage{
				Text: "Circular supersedes chain detected",
			},
			FullDescription: &SARIFMessage{
				Text: "The supersedes chain forms a cycle, which is invalid. Records must not supersede each other circularly.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV014",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "error",
			},
		},
		{
			ID:   string(PROV015),
			Name: "ScopeOverlap",
			ShortDescription: &SARIFMessage{
				Text: "Scope overlaps between two open records",
			},
			FullDescription: &SARIFMessage{
				Text: "Two or more open records have overlapping scope, which can lead to governance conflicts.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV015",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "warning",
			},
		},
		{
			ID:   string(PROV016),
			Name: "DeadRecord",
			ShortDescription: &SARIFMessage{
				Text: "All files in affected_scope have been deleted",
			},
			FullDescription: &SARIFMessage{
				Text: "None of the files matching the affected_scope patterns exist anymore. Consider deprecating this record.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV016",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "warning",
			},
		},
		{
			ID:   string(PROV017),
			Name: "ModifiedImplementedRecord",
			ShortDescription: &SARIFMessage{
				Text: "An implemented record has had immutable fields changed",
			},
			FullDescription: &SARIFMessage{
				Text: "Implemented records should be immutable. Only monitors and associated_traces fields should be modified after implementation.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV017",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "warning",
			},
		},
		{
			ID:   string(PROV018),
			Name: "MissingSpecFile",
			ShortDescription: &SARIFMessage{
				Text: "A path listed in associated_specs does not exist on disk",
			},
			FullDescription: &SARIFMessage{
				Text: "One or more paths listed in associated_specs cannot be found on disk.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV018",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "error",
			},
		},
		{
			ID:   string(PROV019),
			Name: "InvalidRegexPattern",
			ShortDescription: &SARIFMessage{
				Text: "A regex pattern in affected_scope or forbidden_scope fails to compile",
			},
			FullDescription: &SARIFMessage{
				Text: "A regex pattern (prefixed with re:) in the scope fields is not valid Go regular expression syntax.",
			},
			HelpURI: "https://linespec.dev/docs/provenance/lint-rules#PROV019",
			DefaultConfiguration: &SARIFRuleConfiguration{
				Level: "error",
			},
		},
	}
}

// GetRuleIDForIssue maps an Issue to its SARIF Rule ID
func GetRuleIDForIssue(issue Issue) SARIFRuleID {
	switch issue.Field {
	case "id":
		return PROV004
	case "status":
		return PROV003
	case "created_at":
		return PROV005
	case "supersedes":
		return PROV006
	case "superseded_by":
		return PROV007
	case "related":
		return PROV008
	case "associated_specs":
		// Check the message to determine which rule
		if strings.Contains(issue.Message, "does not exist") || strings.Contains(issue.Message, "not found") {
			return PROV018
		}
		if strings.Contains(issue.Message, "constraints") && issue.Severity == SeverityHint {
			return PROV011
		}
		return PROV010
	case "title":
		return PROV013
	case "constraints":
		return PROV012
	case "sealed_at_sha":
		// This would be PROV017, but the message would indicate modified implemented
		return PROV017
	default:
		// Parse message for other issues
		msg := strings.ToLower(issue.Message)
		if strings.Contains(msg, "missing required") {
			return PROV002
		}
		if strings.Contains(msg, "yaml") || strings.Contains(msg, "parse") {
			return PROV001
		}
		if strings.Contains(msg, "circular") {
			return PROV014
		}
		if strings.Contains(msg, "overlap") && strings.Contains(msg, "scope") && !strings.Contains(msg, "conflict") {
			return PROV015
		}
		if strings.Contains(msg, "dead record") || strings.Contains(msg, "deleted") {
			return PROV016
		}
		if strings.Contains(msg, "immutable") || strings.Contains(msg, "implemented record") {
			return PROV017
		}
		if strings.Contains(msg, "invalid regex") || strings.Contains(msg, "pattern") && strings.Contains(msg, "regex") {
			return PROV019
		}
		if strings.Contains(msg, "conflict") && strings.Contains(msg, "scope") {
			return PROV009
		}
	}
	return PROV002 // Default to missing required field
}

// SeverityToSARIFLevel maps LineSpec Severity to SARIF level
func SeverityToSARIFLevel(severity Severity, enforcement string) string {
	switch severity {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityHint:
		return "note"
	default:
		return "none"
	}
}

// NormalizePath converts a file path to use forward slashes and make it relative to repo root
func NormalizePath(filePath, repoRoot string) string {
	// Convert to forward slashes
	path := filepath.ToSlash(filePath)

	// Make relative to repo root if it's absolute
	if strings.HasPrefix(path, repoRoot) {
		rel := strings.TrimPrefix(path, repoRoot)
		rel = strings.TrimPrefix(rel, "/")
		return rel
	}

	return path
}

// ComputeFileHash computes the SHA-256 hash of a file
func ComputeFileHash(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// ToSARIF converts a LintResult to a SARIF document
func (r *LintResult) ToSARIF(loader *Loader, repoRoot string, analyzedFiles []string) *SARIFDocument {
	doc := NewSARIFDocument()
	run := &doc.Runs[0]

	// Convert issues to results
	for _, issue := range r.Issues {
		ruleID := GetRuleIDForIssue(issue)
		level := SeverityToSARIFLevel(issue.Severity, r.Enforcement)

		result := SARIFResult{
			RuleID: string(ruleID),
			Level:  level,
			Message: &SARIFMessage{
				Text: issue.Message,
			},
			Locations: []SARIFLocation{
				{
					PhysicalLocation: &SARIFPhysicalLocation{
						ArtifactLocation: &SARIFArtifactLocation{
							URI:       fmt.Sprintf("provenance/%s.yml", issue.RecordID),
							URIBaseID: "%SRCROOT%",
						},
						Region: &SARIFRegion{
							StartLine:   1, // Field-level precision requires line number tracking
							StartColumn: 1,
						},
					},
				},
			},
		}

		run.Results = append(run.Results, result)
	}

	// Add artifacts for all analyzed files
	for _, filePath := range analyzedFiles {
		if filePath == "" {
			continue
		}

		// Compute hash
		hash, err := ComputeFileHash(filePath)
		if err != nil {
			// File might not exist, skip hash
			hash = ""
		}

		// Normalize path
		normalizedPath := NormalizePath(filePath, repoRoot)

		artifact := SARIFArtifact{
			Location: &SARIFArtifactLocation{
				URI:       normalizedPath,
				URIBaseID: "%SRCROOT%",
			},
			MimeType: "application/yaml",
		}

		if hash != "" {
			artifact.Hashes = map[string]string{
				"sha-256": hash,
			}
		}

		run.Artifacts = append(run.Artifacts, artifact)
	}

	return doc
}

// ToJSON converts a SARIFDocument to JSON
func (doc *SARIFDocument) ToJSON() ([]byte, error) {
	return json.MarshalIndent(doc, "", "  ")
}

// GetAnalyzedFiles returns the list of file paths for records in a LintResult
func GetAnalyzedFiles(lintResult *LintResult, loader *Loader) []string {
	files := make(map[string]bool)

	for _, issue := range lintResult.Issues {
		if issue.RecordID == "" {
			continue
		}

		// Find the record's file path
		record, exists := loader.GetRecord(issue.RecordID)
		if exists && record.FilePath != "" {
			files[record.FilePath] = true
		}
	}

	// Convert map to slice
	analyzedFiles := make([]string, 0, len(files))
	for file := range files {
		analyzedFiles = append(analyzedFiles, file)
	}

	return analyzedFiles
}
