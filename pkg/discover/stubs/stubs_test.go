package stubs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/discover/routes"
	"github.com/livecodelife/linespec/v3/pkg/discover/stubs"
)

// --- FileName ---

func TestFileName(t *testing.T) {
	cases := []struct {
		method, path string
		want         string
	}{
		{"POST", "/api/v1/users", "post_api_v1_users.linespec"},
		{"GET", "/api/v1/users/:id", "get_api_v1_users_id.linespec"},
		{"DELETE", "/api/v1/todos", "delete_api_v1_todos.linespec"},
		{"PUT", "/api/v1/users/{id}", "put_api_v1_users_id.linespec"},
		{"GET", "/", "get_root.linespec"},
		{"PATCH", "/api/v1/todos/:id", "patch_api_v1_todos_id.linespec"},
		{"GET", "/api/v1/shorten", "get_api_v1_shorten.linespec"},
	}
	for _, c := range cases {
		got := stubs.FileName(c.method, c.path)
		if got != c.want {
			t.Errorf("FileName(%q, %q) = %q; want %q", c.method, c.path, got, c.want)
		}
	}
}

// --- Generate ---

func TestGenerate_BasicGET(t *testing.T) {
	in := stubs.Input{
		Route: routes.Route{
			Method: "GET",
			Path:   "/api/v1/users",
		},
	}
	got := stubs.Generate(in)

	assertContains(t, got, "TEST get_api_v1_users")
	assertContains(t, got, "RECEIVE HTTP:GET /api/v1/users")
	assertContains(t, got, "RESPOND HTTP:200")
	assertNotContains(t, got, "HEADERS")
}

func TestGenerate_POSTWithAuthMiddleware(t *testing.T) {
	in := stubs.Input{
		Route: routes.Route{
			Method:          "POST",
			Path:            "/api/v1/users",
			MiddlewareChain: []string{"AuthMiddleware", "RateLimiter"},
		},
	}
	got := stubs.Generate(in)

	assertContains(t, got, "TEST post_api_v1_users")
	assertContains(t, got, "RECEIVE HTTP:POST /api/v1/users")
	assertContains(t, got, "HEADERS")
	assertContains(t, got, "Authorization: Bearer ${AUTH_TOKEN}")
	assertContains(t, got, "RESPOND HTTP:201")
}

func TestGenerate_DELETEStatus204(t *testing.T) {
	in := stubs.Input{
		Route: routes.Route{Method: "DELETE", Path: "/api/v1/todos/:id"},
	}
	got := stubs.Generate(in)
	assertContains(t, got, "RESPOND HTTP:204")
}

func TestGenerate_PUTStatus201(t *testing.T) {
	in := stubs.Input{
		Route: routes.Route{Method: "PUT", Path: "/api/v1/todos/:id"},
	}
	got := stubs.Generate(in)
	assertContains(t, got, "RESPOND HTTP:201")
}

func TestGenerate_PATCHStatus200(t *testing.T) {
	in := stubs.Input{
		Route: routes.Route{Method: "PATCH", Path: "/api/v1/todos/:id"},
	}
	got := stubs.Generate(in)
	assertContains(t, got, "RESPOND HTTP:200")
}

func TestGenerate_PostgreSQLReadBoundary(t *testing.T) {
	in := stubs.Input{
		Route: routes.Route{Method: "GET", Path: "/api/v1/users"},
		Boundaries: []stubs.BoundaryHit{
			{Protocol: "postgresql", Direction: "read", Target: "users"},
		},
	}
	got := stubs.Generate(in)
	assertContains(t, got, "EXPECT READ:POSTGRESQL users")
}

func TestGenerate_PostgreSQLWriteBoundary(t *testing.T) {
	in := stubs.Input{
		Route: routes.Route{Method: "POST", Path: "/api/v1/users"},
		Boundaries: []stubs.BoundaryHit{
			{Protocol: "postgresql", Direction: "write", Target: "users"},
		},
	}
	got := stubs.Generate(in)
	assertContains(t, got, "EXPECT WRITE:POSTGRESQL users")
}

func TestGenerate_MySQLBoundaries(t *testing.T) {
	in := stubs.Input{
		Route: routes.Route{Method: "POST", Path: "/api/v1/todos"},
		Boundaries: []stubs.BoundaryHit{
			{Protocol: "mysql", Direction: "read", Target: "todos"},
			{Protocol: "mysql", Direction: "write", Target: "todos"},
		},
	}
	got := stubs.Generate(in)
	assertContains(t, got, "EXPECT READ:MYSQL todos")
	assertContains(t, got, "EXPECT WRITE:MYSQL todos")
}

func TestGenerate_HTTPBoundaryRead(t *testing.T) {
	in := stubs.Input{
		Route: routes.Route{Method: "GET", Path: "/api/v1/users"},
		Boundaries: []stubs.BoundaryHit{
			{Protocol: "http", Direction: "read", Target: "https://user-service.internal"},
		},
	}
	got := stubs.Generate(in)
	assertContains(t, got, "EXPECT HTTP:GET https://user-service.internal")
}

func TestGenerate_HTTPBoundaryWrite(t *testing.T) {
	in := stubs.Input{
		Route: routes.Route{Method: "POST", Path: "/api/v1/orders"},
		Boundaries: []stubs.BoundaryHit{
			{Protocol: "http", Direction: "write", Target: "https://payment-service.internal"},
		},
	}
	got := stubs.Generate(in)
	assertContains(t, got, "EXPECT HTTP:POST https://payment-service.internal")
}

func TestGenerate_RedisBoundary(t *testing.T) {
	in := stubs.Input{
		Route: routes.Route{Method: "GET", Path: "/api/v1/session"},
		Boundaries: []stubs.BoundaryHit{
			{Protocol: "redis", Direction: "read", Target: "session"},
		},
	}
	got := stubs.Generate(in)
	assertContains(t, got, "EXPECT READ:REDIS session")
}

func TestGenerate_KafkaBoundary(t *testing.T) {
	in := stubs.Input{
		Route: routes.Route{Method: "POST", Path: "/api/v1/orders"},
		Boundaries: []stubs.BoundaryHit{
			{Protocol: "kafka", Direction: "write", Target: "order-events"},
		},
	}
	got := stubs.Generate(in)
	assertContains(t, got, "EXPECT EVENT:order-events")
}

func TestGenerate_DynamicBoundary(t *testing.T) {
	in := stubs.Input{
		Route: routes.Route{Method: "GET", Path: "/api/v1/data"},
		Boundaries: []stubs.BoundaryHit{
			{Protocol: "postgresql", Direction: "read", Dynamic: true},
		},
	}
	got := stubs.Generate(in)
	assertContains(t, got, "DYNAMIC")
	assertNotContains(t, got, "EXPECT READ:POSTGRESQL")
}

func TestGenerate_NoTargetFallback(t *testing.T) {
	in := stubs.Input{
		Route: routes.Route{Method: "GET", Path: "/api/v1/data"},
		Boundaries: []stubs.BoundaryHit{
			{Protocol: "postgresql", Direction: "read", Target: ""},
		},
	}
	got := stubs.Generate(in)
	// Should produce a valid EXPECT line with a placeholder target, not crash or emit empty.
	assertContains(t, got, "EXPECT READ:POSTGRESQL")
}

func TestGenerate_MultipleBoundaries(t *testing.T) {
	in := stubs.Input{
		Route: routes.Route{
			Method:          "POST",
			Path:            "/api/v1/todos",
			MiddlewareChain: []string{"jwtMiddleware"},
		},
		Boundaries: []stubs.BoundaryHit{
			{Protocol: "postgresql", Direction: "read", Target: "users"},
			{Protocol: "postgresql", Direction: "write", Target: "todos"},
			{Protocol: "kafka", Direction: "write", Target: "todo-events"},
		},
	}
	got := stubs.Generate(in)
	assertContains(t, got, "HEADERS")
	assertContains(t, got, "Authorization: Bearer ${AUTH_TOKEN}")
	assertContains(t, got, "EXPECT READ:POSTGRESQL users")
	assertContains(t, got, "EXPECT WRITE:POSTGRESQL todos")
	assertContains(t, got, "EXPECT EVENT:todo-events")
	assertContains(t, got, "RESPOND HTTP:201")
}

// --- Write ---

func TestWrite_CreatesFiles(t *testing.T) {
	dir := t.TempDir()
	inputs := []stubs.Input{
		{Route: routes.Route{Method: "GET", Path: "/api/v1/users"}},
		{Route: routes.Route{Method: "POST", Path: "/api/v1/users"}},
	}

	results, err := stubs.Write(dir, inputs)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	for _, r := range results {
		if r.Skipped {
			t.Errorf("expected file to be written, got skipped: %s", r.FilePath)
		}
		if _, err := os.Stat(r.FilePath); err != nil {
			t.Errorf("file not found on disk: %s", r.FilePath)
		}
	}

	assertFileNameIn(t, results, "get_api_v1_users.linespec")
	assertFileNameIn(t, results, "post_api_v1_users.linespec")
}

func TestWrite_SkipsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "get_api_v1_users.linespec")
	if err := os.WriteFile(existing, []byte("existing content\n"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	inputs := []stubs.Input{
		{Route: routes.Route{Method: "GET", Path: "/api/v1/users"}},
	}
	results, err := stubs.Write(dir, inputs)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Skipped {
		t.Error("expected Skipped=true for existing file")
	}

	got, _ := os.ReadFile(existing)
	if string(got) != "existing content\n" {
		t.Error("existing file was overwritten")
	}
}

func TestWrite_ContentAvailableForDryRun(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "get_api_v1_users.linespec")
	if err := os.WriteFile(existing, []byte("existing\n"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	inputs := []stubs.Input{
		{Route: routes.Route{Method: "GET", Path: "/api/v1/users"}},
	}
	results, err := stubs.Write(dir, inputs)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Even for skipped files, Content should be populated (needed for --dry-run display).
	if results[0].Content == "" {
		t.Error("Content should be populated even for skipped files")
	}
}

func TestWrite_CreatesOutputDirectory(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "specs", "nested")

	inputs := []stubs.Input{
		{Route: routes.Route{Method: "GET", Path: "/health"}},
	}
	_, err := stubs.Write(dir, inputs)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Error("output directory was not created")
	}
}

// --- Plan ---

func TestPlan_NoWrites(t *testing.T) {
	dir := t.TempDir()
	inputs := []stubs.Input{
		{Route: routes.Route{Method: "GET", Path: "/api/v1/users"}},
		{Route: routes.Route{Method: "POST", Path: "/api/v1/users"}},
	}

	results := stubs.Plan(dir, inputs)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	for _, r := range results {
		if r.Skipped {
			t.Errorf("Plan on empty dir should not skip any file: %s", r.FilePath)
		}
		if _, err := os.Stat(r.FilePath); err == nil {
			t.Errorf("Plan must not write files: %s", r.FilePath)
		}
	}
}

func TestPlan_DetectsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "get_api_v1_users.linespec")
	if err := os.WriteFile(existing, []byte("existing\n"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	inputs := []stubs.Input{
		{Route: routes.Route{Method: "GET", Path: "/api/v1/users"}},
		{Route: routes.Route{Method: "POST", Path: "/api/v1/users"}},
	}
	results := stubs.Plan(dir, inputs)

	if !results[0].Skipped {
		t.Error("GET /api/v1/users should be Skipped=true (file exists)")
	}
	if results[1].Skipped {
		t.Error("POST /api/v1/users should be Skipped=false (file doesn't exist)")
	}
}

func TestPlan_PopulatesMethodPath(t *testing.T) {
	dir := t.TempDir()
	inputs := []stubs.Input{
		{Route: routes.Route{Method: "post", Path: "/api/v1/orders"}},
	}
	results := stubs.Plan(dir, inputs)
	if results[0].Method != "POST" {
		t.Errorf("Method = %q; want %q", results[0].Method, "POST")
	}
	if results[0].Path != "/api/v1/orders" {
		t.Errorf("Path = %q; want %q", results[0].Path, "/api/v1/orders")
	}
}

func TestPlan_ContentPopulated(t *testing.T) {
	dir := t.TempDir()
	inputs := []stubs.Input{
		{Route: routes.Route{Method: "GET", Path: "/api/v1/users"}},
	}
	results := stubs.Plan(dir, inputs)
	if results[0].Content == "" {
		t.Error("Plan results should have Content populated")
	}
	assertContains(t, results[0].Content, "RECEIVE HTTP:GET /api/v1/users")
}

// --- FormatTable ---

func TestFormatTable_Empty(t *testing.T) {
	out := stubs.FormatTable(nil)
	assertContains(t, out, "No stubs")
}

func TestFormatTable_Basic(t *testing.T) {
	results := []stubs.Result{
		{FilePath: "specs/get_api_v1_users.linespec", Method: "GET", Path: "/api/v1/users", Skipped: false},
		{FilePath: "specs/post_api_v1_users.linespec", Method: "POST", Path: "/api/v1/users", Skipped: false},
		{FilePath: "specs/delete_api_v1_users.linespec", Method: "DELETE", Path: "/api/v1/users", Skipped: true},
	}
	out := stubs.FormatTable(results)

	assertContains(t, out, "write")
	assertContains(t, out, "skip")
	assertContains(t, out, "GET /api/v1/users")
	assertContains(t, out, "POST /api/v1/users")
	assertContains(t, out, "2 stub(s) to write")
	assertContains(t, out, "1 skipped")
}

func TestFormatTable_AllSkipped(t *testing.T) {
	results := []stubs.Result{
		{FilePath: "specs/get.linespec", Method: "GET", Path: "/", Skipped: true},
	}
	out := stubs.FormatTable(results)
	assertContains(t, out, "0 stub(s) to write")
	assertContains(t, out, "1 skipped")
}

// --- FormatJSON ---

func TestFormatJSON_Basic(t *testing.T) {
	results := []stubs.Result{
		{FilePath: "specs/get_api_v1_users.linespec", Method: "GET", Path: "/api/v1/users", Skipped: false},
		{FilePath: "specs/post_api_v1_users.linespec", Method: "POST", Path: "/api/v1/users", Skipped: true},
	}
	out, err := stubs.FormatJSON(results)
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}
	s := string(out)
	assertContains(t, s, `"file"`)
	assertContains(t, s, `"method"`)
	assertContains(t, s, `"path"`)
	assertContains(t, s, `"skipped"`)
	assertContains(t, s, `"GET"`)
	assertContains(t, s, `"POST"`)
	assertContains(t, s, `/api/v1/users`)
	assertContains(t, s, `false`)
	assertContains(t, s, `true`)
}

func TestFormatJSON_Empty(t *testing.T) {
	out, err := stubs.FormatJSON(nil)
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}
	assertContains(t, string(out), "[]")
}

// --- helpers ---

func assertContains(t *testing.T, content, substr string) {
	t.Helper()
	if !strings.Contains(content, substr) {
		t.Errorf("expected output to contain %q\ngot:\n%s", substr, content)
	}
}

func assertNotContains(t *testing.T, content, substr string) {
	t.Helper()
	if strings.Contains(content, substr) {
		t.Errorf("expected output NOT to contain %q\ngot:\n%s", substr, content)
	}
}

func assertFileNameIn(t *testing.T, results []stubs.Result, name string) {
	t.Helper()
	for _, r := range results {
		if filepath.Base(r.FilePath) == name {
			return
		}
	}
	t.Errorf("expected result with file name %q not found", name)
}
