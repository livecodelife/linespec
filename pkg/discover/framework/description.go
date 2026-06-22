package framework

// Description is a framework description loaded from a YAML file.
// It teaches discover (and Sedum's world state scanner) how to read a framework's source.
// Both tools consume the same file — discover uses it to scan, Sedum uses it to assess
// existing projects during `sedum graft`.
type Description struct {
	Name     string `yaml:"name"`
	Language string `yaml:"language"` // "go" or "ruby"

	// Detection describes how to identify this framework from project files.
	Detection []DetectionRule `yaml:"detection"`

	// RouteQueries match route registration patterns.
	RouteQueries []RouteQuery `yaml:"route_queries"`

	// MiddlewareQueries match middleware registration patterns.
	MiddlewareQueries []MiddlewareQuery `yaml:"middleware_queries"`

	// GroupQueries match route grouping / path prefix nesting patterns.
	GroupQueries []GroupQuery `yaml:"group_queries"`

	// GroupingStrategy controls how discovered routes are clustered into blueprint records.
	// Valid values: "package" (Go), "controller" (Rails), "file" (Sinatra).
	GroupingStrategy string `yaml:"grouping_strategy"`

	// BoundaryQueries match protocol boundary call patterns (DB, HTTP client, cache, MQ).
	BoundaryQueries []BoundaryQuery `yaml:"boundary_queries"`

	// Partial indicates that this framework uses metaprogramming or reflection-based
	// routing that cannot be fully expressed as tree-sitter queries. Discover will
	// report discovered routes alongside a warning about incomplete coverage.
	Partial bool `yaml:"partial"`
}

// DetectionRule describes how to identify this framework in a project directory.
type DetectionRule struct {
	// Manifest is the filename to look for (e.g. "go.mod", "Gemfile").
	Manifest string `yaml:"manifest"`
	// Contains lists strings that must appear in the manifest for detection to succeed.
	// All strings must be present (AND semantics).
	Contains []string `yaml:"contains"`
}

// RouteQuery is a tree-sitter query that matches route registration patterns.
type RouteQuery struct {
	// Pattern is an S-expression tree-sitter query pattern.
	// Expected captures: "method", "path", "handler". Additional captures are allowed.
	Pattern string `yaml:"pattern"`

	// Filter maps capture name to a regexp that the captured text must match.
	// Used to narrow overly-broad structural patterns (e.g. restrict @method to HTTP verbs).
	Filter map[string]string `yaml:"filter"`
}

// MiddlewareQuery is a tree-sitter query that matches middleware registration.
type MiddlewareQuery struct {
	Pattern string `yaml:"pattern"`
}

// GroupQuery is a tree-sitter query that matches route grouping / path prefix nesting.
type GroupQuery struct {
	Pattern string `yaml:"pattern"`
}

// BoundaryQuery is a tree-sitter query that matches a protocol boundary call site.
type BoundaryQuery struct {
	// Protocol identifies the dependency type: postgresql, mysql, redis, http, kafka, rabbitmq.
	Protocol string `yaml:"protocol"`
	// Direction is "read", "write", or "both".
	Direction string `yaml:"direction"`
	// Pattern is an S-expression tree-sitter query.
	Pattern string `yaml:"pattern"`
	// Captures describes which named captures carry the target and (optionally) direction.
	Captures BoundaryCaptures `yaml:"captures"`
}

// BoundaryCaptures names the captures that carry semantic data from a BoundaryQuery match.
type BoundaryCaptures struct {
	// Target is the capture name for the dependency target (table name, URL base, topic name).
	Target string `yaml:"target"`
	// Direction is an optional capture name for a dynamically-determined direction.
	Direction string `yaml:"direction"`
}
