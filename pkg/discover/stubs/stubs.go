package stubs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/livecodelife/linespec/pkg/discover/routes"
)

// BoundaryHit is a protocol boundary hit detected in a handler's call chain.
// Produced by pkg/discover/boundaries (Phase 2); consumed here for stub generation.
type BoundaryHit struct {
	Protocol  string // postgresql, mysql, redis, http, kafka, rabbitmq
	Direction string // read, write, both
	Target    string // table name, URL base, topic, or "" when unresolved
	Dynamic   bool   // true when the call site could not be statically resolved
}

// Input is the combined route and boundary data for generating one stub file.
type Input struct {
	Route      routes.Route
	Boundaries []BoundaryHit
}

// Result is the outcome of generating a single stub.
// Content is always populated; Skipped is true when the file already existed.
type Result struct {
	FilePath string
	Method   string // HTTP method of the route (uppercased)
	Path     string // URL path of the route
	Content  string
	Skipped  bool
}

// FileName returns the canonical .linespec stub file name for a route.
// Example: POST /api/v1/users → "post_api_v1_users.linespec"
func FileName(method, path string) string {
	return routeSlug(method, path) + ".linespec"
}

// Generate produces the content of a .linespec stub for the given input.
// The output is syntactically valid DSL with # TODO comments guiding what to fill in.
func Generate(in Input) string {
	var b strings.Builder

	slug := routeSlug(in.Route.Method, in.Route.Path)
	fmt.Fprintf(&b, "TEST %s\n\n", slug)

	fmt.Fprintf(&b, "RECEIVE HTTP:%s %s\n", strings.ToUpper(in.Route.Method), in.Route.Path)

	if headers := inferHeaders(in.Route.MiddlewareChain); len(headers) > 0 {
		b.WriteString("HEADERS\n")
		for _, h := range headers {
			fmt.Fprintf(&b, "  %s\n", h)
		}
	}

	for _, hit := range in.Boundaries {
		b.WriteByte('\n')
		writeExpect(&b, hit)
	}

	fmt.Fprintf(&b, "\nRESPOND HTTP:%d\n", defaultStatus(in.Route.Method))

	return b.String()
}

// Plan computes what Write would produce without touching the filesystem.
// Results show which files would be created (Skipped=false) vs skipped (Skipped=true)
// based on whether each file already exists in dir.
func Plan(dir string, inputs []Input) []Result {
	results := make([]Result, 0, len(inputs))
	for _, in := range inputs {
		fname := FileName(in.Route.Method, in.Route.Path)
		fpath := filepath.Join(dir, fname)
		r := Result{
			FilePath: fpath,
			Method:   strings.ToUpper(in.Route.Method),
			Path:     in.Route.Path,
			Content:  Generate(in),
		}
		if _, err := os.Stat(fpath); err == nil {
			r.Skipped = true
		}
		results = append(results, r)
	}
	return results
}

// Write generates and writes stub files to dir. Files are named by FileName.
// Existing files are skipped and reported with Skipped=true. The output directory
// is created if it does not exist.
func Write(dir string, inputs []Input) ([]Result, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir %s: %w", dir, err)
	}
	results := Plan(dir, inputs)
	for _, r := range results {
		if !r.Skipped {
			if err := os.WriteFile(r.FilePath, []byte(r.Content), 0644); err != nil {
				return nil, fmt.Errorf("write %s: %w", r.FilePath, err)
			}
		}
	}
	return results, nil
}

// routeSlug converts a method and path into a snake_case identifier.
// POST /api/v1/users → "post_api_v1_users"
// GET /api/v1/users/:id → "get_api_v1_users_id"
func routeSlug(method, path string) string {
	method = strings.ToLower(strings.TrimSpace(method))
	// Normalize path parameter sigils and separators.
	path = strings.ReplaceAll(path, "/", "_")
	path = strings.ReplaceAll(path, ":", "")
	path = strings.ReplaceAll(path, "{", "")
	path = strings.ReplaceAll(path, "}", "")
	path = strings.ReplaceAll(path, "*", "")
	path = strings.Trim(path, "_")
	if path == "" {
		return method + "_root"
	}
	return method + "_" + path
}

var authKeywords = []string{
	"auth", "jwt", "bearer", "token", "authenticate", "authorize", "authorization",
}

// inferHeaders returns inferred HEADERS key-value pairs from a middleware chain.
// Any middleware whose name contains an auth-related keyword implies an Authorization header.
func inferHeaders(chain []string) []string {
	for _, mw := range chain {
		lower := strings.ToLower(mw)
		for _, kw := range authKeywords {
			if strings.Contains(lower, kw) {
				return []string{"Authorization: Bearer ${AUTH_TOKEN}"}
			}
		}
	}
	return nil
}

func defaultStatus(method string) int {
	switch strings.ToUpper(method) {
	case "POST", "PUT":
		return 201
	case "DELETE":
		return 204
	default:
		return 200
	}
}

func writeExpect(b *strings.Builder, hit BoundaryHit) {
	if hit.Dynamic {
		b.WriteString("# DYNAMIC: manual classification needed — protocol boundary detected but target not statically resolvable\n")
		return
	}

	protocol := strings.ToUpper(hit.Protocol)
	direction := strings.ToUpper(hit.Direction)
	target := hit.Target

	switch hit.Protocol {
	case "postgresql", "mysql":
		if target == "" {
			target = "TODO_TABLE"
		}
		if direction == "WRITE" {
			fmt.Fprintf(b, "EXPECT WRITE:%s %s\n", protocol, target)
			b.WriteString("# TODO: WITH {{payloads/<name>.yaml}}\n")
		} else {
			fmt.Fprintf(b, "EXPECT READ:%s %s\n", protocol, target)
			b.WriteString("# TODO: add query matchers\n")
			b.WriteString("# TODO: RETURNS {{payloads/<name>.yaml}}\n")
		}
	case "redis":
		if target == "" {
			target = "TODO_KEY"
		}
		if direction == "WRITE" {
			fmt.Fprintf(b, "EXPECT WRITE:REDIS %s\n", target)
		} else {
			fmt.Fprintf(b, "EXPECT READ:REDIS %s\n", target)
			b.WriteString("# TODO: RETURNS {{payloads/<name>.yaml}}\n")
		}
	case "http":
		if target == "" {
			target = "${TODO_URL}"
		}
		httpMethod := "GET"
		if direction == "WRITE" {
			httpMethod = "POST"
		}
		fmt.Fprintf(b, "EXPECT HTTP:%s %s\n", httpMethod, target)
		b.WriteString("# TODO: RETURNS {{payloads/<name>.yaml}}\n")
	case "kafka", "rabbitmq":
		if target == "" {
			target = "TODO_TOPIC"
		}
		fmt.Fprintf(b, "EXPECT EVENT:%s\n", target)
		b.WriteString("# TODO: WITH {{payloads/<name>.yaml}}\n")
	default:
		fmt.Fprintf(b, "# TODO: EXPECT %s %s:%s — unknown protocol, classify manually\n", target, protocol, direction)
	}
}
