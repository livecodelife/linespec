package boundaries_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/livecodelife/linespec/v3/pkg/discover/boundaries"
	"github.com/livecodelife/linespec/v3/pkg/discover/framework"
	"github.com/livecodelife/linespec/v3/pkg/discover/routes"
)

func loadFramework(t *testing.T, name string) *framework.Description {
	t.Helper()
	descs, err := framework.Load("")
	if err != nil {
		t.Fatalf("framework.Load: %v", err)
	}
	d, ok := descs[name]
	if !ok {
		t.Fatalf("framework %q not found", name)
	}
	return d
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findHit(t *testing.T, hits []boundaries.Hit, protocol, direction string) boundaries.Hit {
	t.Helper()
	for _, h := range hits {
		if h.Protocol == protocol && h.Direction == direction {
			return h
		}
	}
	t.Fatalf("no hit found with protocol=%q direction=%q in %+v", protocol, direction, hits)
	return boundaries.Hit{}
}

// --- Naming unit tests ---

func TestModelToTable(t *testing.T) {
	cases := []struct{ model, want string }{
		{"User", "users"},
		{"BlogPost", "blog_posts"},
		{"OrderItem", "order_items"},
		{"Category", "categories"},
		{"Address", "addresses"},
		{"Status", "statuses"},
		{"Company", "companies"},
		{"MyApp::User", "users"},
		{"Accounts::BlogPost", "blog_posts"},
	}
	for _, c := range cases {
		got := boundaries.ModelToTable(c.model)
		if got != c.want {
			t.Errorf("ModelToTable(%q) = %q; want %q", c.model, got, c.want)
		}
	}
}

func TestTableFromSQL(t *testing.T) {
	cases := []struct{ sql, want string }{
		{`SELECT * FROM users WHERE id = $1`, "users"},
		{`INSERT INTO orders (col) VALUES ($1)`, "orders"},
		{`UPDATE sessions SET active = $1 WHERE id = $2`, "sessions"},
		{`DELETE FROM cache_entries WHERE expired_at < $1`, "cache_entries"},
		{`SELECT u.id FROM users u JOIN posts p ON p.user_id = u.id`, "users"},
		// Quoted
		{`SELECT * FROM "public"."users"`, "public"},
		// Unresolvable
		{`CALL my_procedure()`, ""},
	}
	for _, c := range cases {
		got := boundaries.TableFromSQL(c.sql)
		if got != c.want {
			t.Errorf("TableFromSQL(%q) = %q; want %q", c.sql, got, c.want)
		}
	}
}

// --- Tracer: Go / Chi ---

func TestTracer_Go_PostgreSQL_Read(t *testing.T) {
	desc := loadFramework(t, "chi")
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "handler.go", `package handlers

import (
	"context"
	"database/sql"
	"net/http"
)

func ListUsers(w http.ResponseWriter, r *http.Request) {
	rows, _ := db.Query("SELECT * FROM users WHERE active = $1", true)
	_ = rows
}
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "GET", Path: "/users", HandlerRef: "ListUsers"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	h := findHit(t, hits["ListUsers"], "postgresql", "read")
	if h.Target != "users" {
		t.Errorf("Target = %q; want %q", h.Target, "users")
	}
	if h.Dynamic {
		t.Error("Dynamic should be false for statically resolvable SQL")
	}
}

func TestTracer_Go_PostgreSQL_Write(t *testing.T) {
	desc := loadFramework(t, "chi")
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "handler.go", `package handlers

import (
	"database/sql"
	"net/http"
)

func CreateUser(w http.ResponseWriter, r *http.Request) {
	db.Exec("INSERT INTO users (name, email) VALUES ($1, $2)", "alice", "alice@example.com")
}
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "POST", Path: "/users", HandlerRef: "CreateUser"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	h := findHit(t, hits["CreateUser"], "postgresql", "write")
	if h.Target != "users" {
		t.Errorf("Target = %q; want %q", h.Target, "users")
	}
}

func TestTracer_Go_Redis_Read(t *testing.T) {
	desc := loadFramework(t, "chi")
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "handler.go", `package handlers

import (
	"context"
	"net/http"
)

func GetSession(w http.ResponseWriter, r *http.Request) {
	val, _ := rdb.Get(context.Background(), "session:active")
	_ = val
}
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "GET", Path: "/session", HandlerRef: "GetSession"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	h := findHit(t, hits["GetSession"], "redis", "read")
	if h.Target != "session:active" {
		t.Errorf("Target = %q; want %q", h.Target, "session:active")
	}
}

func TestTracer_Go_Redis_Write(t *testing.T) {
	desc := loadFramework(t, "chi")
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "handler.go", `package handlers

import (
	"context"
	"net/http"
	"time"
)

func Login(w http.ResponseWriter, r *http.Request) {
	rdb.Set(context.Background(), "user:session", "token123", time.Hour)
}
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "POST", Path: "/login", HandlerRef: "Login"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	h := findHit(t, hits["Login"], "redis", "write")
	if h.Target != "user:session" {
		t.Errorf("Target = %q; want %q", h.Target, "user:session")
	}
}

func TestTracer_Go_HTTP_Read(t *testing.T) {
	desc := loadFramework(t, "chi")
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "handler.go", `package handlers

import (
	"net/http"
)

func FetchProfile(w http.ResponseWriter, r *http.Request) {
	resp, _ := http.Get("https://api.example.com/profile")
	_ = resp
}
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "GET", Path: "/profile", HandlerRef: "FetchProfile"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	h := findHit(t, hits["FetchProfile"], "http", "read")
	if h.Target != "https://api.example.com/profile" {
		t.Errorf("Target = %q; want %q", h.Target, "https://api.example.com/profile")
	}
}

func TestTracer_Go_CallGraphDepth(t *testing.T) {
	desc := loadFramework(t, "chi")
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	// Handler calls fetchUser which calls queryDB — 2 levels deep
	writeFile(t, dir, "handler.go", `package handlers

import (
	"database/sql"
	"net/http"
)

func GetUser(w http.ResponseWriter, r *http.Request) {
	user := fetchUser(db, r.URL.Query().Get("id"))
	_ = user
}

func fetchUser(db *sql.DB, id string) string {
	return queryDB(db, id)
}

func queryDB(db *sql.DB, id string) string {
	rows, _ := db.Query("SELECT * FROM users WHERE id = $1", id)
	_ = rows
	return ""
}
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "GET", Path: "/users/:id", HandlerRef: "GetUser"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	h := findHit(t, hits["GetUser"], "postgresql", "read")
	if h.Target != "users" {
		t.Errorf("Target = %q; want %q", h.Target, "users")
	}
}

func TestTracer_Go_DepthLimit(t *testing.T) {
	desc := loadFramework(t, "chi")
	// Depth 0: only the handler body, no call following
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr = tr.WithDepth(0)

	dir := t.TempDir()
	writeFile(t, dir, "handler.go", `package handlers

import (
	"database/sql"
	"net/http"
)

func GetUser(w http.ResponseWriter, r *http.Request) {
	fetchUser(db, "123")
}

func fetchUser(db *sql.DB, id string) {
	db.Query("SELECT * FROM users WHERE id = $1", id)
}
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "GET", Path: "/users/:id", HandlerRef: "GetUser"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	// With depth=0 the DB call in fetchUser should NOT be found
	for _, h := range hits["GetUser"] {
		if h.Protocol == "postgresql" {
			t.Errorf("expected no postgresql hits at depth=0, got %+v", h)
		}
	}
}

func TestTracer_Go_EmptyHandlerRef(t *testing.T) {
	desc := loadFramework(t, "chi")
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "handler.go", `package handlers

import "net/http"

func ListItems(w http.ResponseWriter, r *http.Request) {}
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "GET", Path: "/items", HandlerRef: ""},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected no hits for empty HandlerRef, got %v", hits)
	}
}

func TestTracer_Go_DynamicKafka(t *testing.T) {
	desc := loadFramework(t, "chi")
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "handler.go", `package handlers

import "net/http"

func PublishEvent(w http.ResponseWriter, r *http.Request) {
	producer.SendMessage(buildMsg())
}
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "POST", Path: "/events", HandlerRef: "PublishEvent"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	h := findHit(t, hits["PublishEvent"], "kafka", "write")
	if !h.Dynamic {
		t.Error("kafka SendMessage without extractable topic should be Dynamic=true")
	}
}

func TestTracer_Go_DeduplicatesHits(t *testing.T) {
	desc := loadFramework(t, "chi")
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	// Same DB query called twice — should produce one hit
	writeFile(t, dir, "handler.go", `package handlers

import "net/http"

func Handler(w http.ResponseWriter, r *http.Request) {
	db.Query("SELECT * FROM users WHERE id = $1", 1)
	db.Query("SELECT * FROM users WHERE id = $1", 2)
}
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "GET", Path: "/", HandlerRef: "Handler"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	count := 0
	for _, h := range hits["Handler"] {
		if h.Protocol == "postgresql" && h.Target == "users" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 deduplicated hit for users, got %d", count)
	}
}

// --- Tracer: Ruby / Rails ---

func TestTracer_Ruby_ActiveRecord_Read(t *testing.T) {
	desc := loadFramework(t, "rails")
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "users_controller.rb", `class UsersController < ApplicationController
  def index
    @users = User.where(active: true)
  end
end
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "GET", Path: "/users", HandlerRef: "users#index"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	h := findHit(t, hits["users#index"], "postgresql", "read")
	if h.Target != "users" {
		t.Errorf("Target = %q; want %q", h.Target, "users")
	}
}

func TestTracer_Ruby_ActiveRecord_Write(t *testing.T) {
	desc := loadFramework(t, "rails")
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "users_controller.rb", `class UsersController < ApplicationController
  def create
    @user = User.create!(user_params)
  end
end
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "POST", Path: "/users", HandlerRef: "users#create"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	h := findHit(t, hits["users#create"], "postgresql", "write")
	if h.Target != "users" {
		t.Errorf("Target = %q; want %q", h.Target, "users")
	}
}

func TestTracer_Ruby_RawSQL(t *testing.T) {
	desc := loadFramework(t, "rails")
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "reports_controller.rb", `class ReportsController < ApplicationController
  def index
    result = execute("SELECT count(*) FROM orders WHERE status = 'active'")
  end
end
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "GET", Path: "/reports", HandlerRef: "reports#index"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	h := findHit(t, hits["reports#index"], "postgresql", "both")
	if h.Target != "orders" {
		t.Errorf("Target = %q; want %q", h.Target, "orders")
	}
}

func TestTracer_Ruby_CamelCaseModel(t *testing.T) {
	desc := loadFramework(t, "rails")
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "posts_controller.rb", `class PostsController < ApplicationController
  def index
    @posts = BlogPost.where(published: true)
  end
end
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "GET", Path: "/posts", HandlerRef: "posts#index"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	h := findHit(t, hits["posts#index"], "postgresql", "read")
	if h.Target != "blog_posts" {
		t.Errorf("Target = %q; want %q", h.Target, "blog_posts")
	}
}

func TestTracer_Ruby_Redis(t *testing.T) {
	desc := loadFramework(t, "rails")
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "sessions_controller.rb", `class SessionsController < ApplicationController
  def show
    data = redis.get("session:token")
  end
end
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "GET", Path: "/session", HandlerRef: "sessions#show"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	h := findHit(t, hits["sessions#show"], "redis", "read")
	if h.Target != "session:token" {
		t.Errorf("Target = %q; want %q", h.Target, "session:token")
	}
}

// TestTracer_RespectsGitignore reproduces prov-2026-f0b20266's second gap:
// buildIndex walked every directory with no denylist or .gitignore
// awareness at all, so a decoy handler living in a gitignored build output
// directory (.next/, mirroring the real Next.js project this was found on)
// would be indexed and traced right alongside real source, producing a
// spurious protocol boundary hit.
func TestTracer_RespectsGitignore(t *testing.T) {
	desc := loadFramework(t, "chi")
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".next/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "handler.go", `package handlers

import "net/http"

func ListUsers(w http.ResponseWriter, r *http.Request) {}
`)
	nextDir := filepath.Join(dir, ".next")
	if err := os.MkdirAll(nextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nextDir, "decoy.go"), []byte(`package decoy

func ListUsers() {
	db.Query("SELECT * FROM users WHERE id = $1", 1)
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "GET", Path: "/users", HandlerRef: "ListUsers"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	for _, h := range hits["ListUsers"] {
		if h.Protocol == "postgresql" {
			t.Errorf("expected no postgresql hit — it only exists in the gitignored .next/decoy.go, got %+v", h)
		}
	}
}

// --- Tracer: TypeScript / Next.js filesystem routing ---

func nextjsBoundaryQueries() []framework.BoundaryQuery {
	return []framework.BoundaryQuery{
		{
			Protocol:  "postgresql",
			Direction: "both",
			Pattern: `(call_expression
				function: (member_expression property: (property_identifier) @method)
				arguments: (arguments (string (string_fragment) @query))
				(#eq? @method "query"))`,
			Captures: framework.BoundaryCaptures{Target: "query"},
		},
		{
			Protocol:  "http",
			Direction: "both",
			Pattern: `(call_expression
				function: (identifier) @fn
				arguments: (arguments (string (string_fragment) @url) _?)
				(#eq? @fn "fetch"))`,
			Captures: framework.BoundaryCaptures{Target: "url"},
		},
	}
}

func TestTracer_TypeScript_PostgreSQL(t *testing.T) {
	desc := &framework.Description{Name: "nextjs", Language: "typescript", BoundaryQueries: nextjsBoundaryQueries()}
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "route.ts", `export async function GET(req: Request) {
	const rows = await db.query("SELECT * FROM users")
	return Response.json(rows)
}
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "GET", Path: "/api/users", HandlerRef: "GET"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}
	h := findHit(t, hits["GET"], "postgresql", "both")
	if h.Target != "users" {
		t.Errorf("Target = %q; want %q", h.Target, "users")
	}
}

func TestTracer_TypeScript_Fetch(t *testing.T) {
	desc := &framework.Description{Name: "nextjs", Language: "typescript", BoundaryQueries: nextjsBoundaryQueries()}
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "route.ts", `export async function POST(req: Request) {
	const res = await fetch("https://payments.example.com/charge", { method: "POST" })
	return Response.json(await res.json())
}
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "POST", Path: "/api/charge", HandlerRef: "POST"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}
	h := findHit(t, hits["POST"], "http", "both")
	if h.Target != "https://payments.example.com/charge" {
		t.Errorf("Target = %q; want %q", h.Target, "https://payments.example.com/charge")
	}
}

// TestTracer_TypeScript_ArrowFunctionHandler reproduces the other route
// export shape prov-2026-ebc2266b's constraint calls for:
// `export const GET = async (req) => {}` must be traceable the same as a
// `function_declaration` handler.
func TestTracer_TypeScript_ArrowFunctionHandler(t *testing.T) {
	desc := &framework.Description{Name: "nextjs", Language: "typescript", BoundaryQueries: nextjsBoundaryQueries()}
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "route.ts", `export const dynamic = "force-dynamic"

export const GET = async (req: Request) => {
	const rows = await db.query("SELECT * FROM users")
	return Response.json(rows)
}
`)

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "GET", Path: "/api/users", HandlerRef: "GET"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}
	h := findHit(t, hits["GET"], "postgresql", "both")
	if h.Target != "users" {
		t.Errorf("Target = %q; want %q", h.Target, "users")
	}
}

// TestTracer_TypeScript_FileScopedHandlerRef reproduces the disambiguation
// problem filesystem-convention routing introduces: every route.ts in a
// Next.js project exports a function named "GET" (or "POST", ...), so a
// bare-name search across the whole index would incorrectly merge two
// unrelated routes' hits. The "file::name" HandlerRef form scopes the
// top-level lookup to one file; recursive callee lookups stay unscoped.
func TestTracer_TypeScript_FileScopedHandlerRef(t *testing.T) {
	desc := &framework.Description{Name: "nextjs", Language: "typescript", BoundaryQueries: nextjsBoundaryQueries()}
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "users"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "orders"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "users"), "route.ts", `export async function GET(req: Request) {
	return Response.json(await db.query("SELECT * FROM users"))
}
`)
	writeFile(t, filepath.Join(dir, "orders"), "route.ts", `export async function GET(req: Request) {
	return Response.json(await db.query("SELECT * FROM orders"))
}
`)

	usersFile := filepath.Join(dir, "users", "route.ts")
	ordersFile := filepath.Join(dir, "orders", "route.ts")

	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "GET", Path: "/api/users", HandlerRef: usersFile + "::GET"},
		{Method: "GET", Path: "/api/orders", HandlerRef: ordersFile + "::GET"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	usersHits := hits[usersFile+"::GET"]
	if len(usersHits) != 1 || usersHits[0].Target != "users" {
		t.Errorf("expected exactly 1 hit targeting users for %s, got %+v", usersFile, usersHits)
	}
	ordersHits := hits[ordersFile+"::GET"]
	if len(ordersHits) != 1 || ordersHits[0].Target != "orders" {
		t.Errorf("expected exactly 1 hit targeting orders for %s, got %+v", ordersFile, ordersHits)
	}
}

// TestTracer_TypeScript_NoExponentialBlowupOnNameCollisions reproduces
// prov-2026-afd410b6: without memoization, traceHandler's recursive callee
// walk redoes the same (function name, remaining depth) work every time it
// is reached via a different call path. Real JS/TS codebases routinely
// redefine short, common names (get, map, filter, parse, ...) across many
// unrelated files/classes — findFuncBody matches by bare name only, with no
// scoping — so this isn't a contrived pathology: it reproduced a multi-minute
// hang on a real ~500-file Next.js repo, and this fixture (a handful of
// "hub" files that all define the same 6 names, each calling 3 others,
// crossed against several route handlers) is enough to make the
// pre-memoization implementation take many seconds even at this small scale.
// A regression here should show up as this test timing out, not just
// getting slower — the growth is exponential in call-graph depth, not linear.
func TestTracer_TypeScript_NoExponentialBlowupOnNameCollisions(t *testing.T) {
	dir := t.TempDir()

	names := []string{"get", "set", "map", "filter", "parse", "log"}
	// Every hub file redefines every name, each calling a fixed set of 3
	// other names — deliberately dense branching, not sparse/random, so the
	// reproduction doesn't depend on a particular RNG seed's luck.
	callees := map[string][3]string{
		"get": {"set", "map", "filter"}, "set": {"map", "filter", "parse"},
		"map": {"filter", "parse", "log"}, "filter": {"parse", "log", "get"},
		"parse": {"log", "get", "set"}, "log": {"get", "set", "map"},
	}
	for i := 0; i < 12; i++ {
		var body strings.Builder
		for _, n := range names {
			c := callees[n]
			body.WriteString("export function " + n + "(x) { return " + c[0] + "(x) + " + c[1] + "(x) + " + c[2] + "(x) }\n")
		}
		writeFile(t, dir, fmt.Sprintf("hub%d.ts", i), body.String())
	}

	var calls strings.Builder
	for _, n := range names {
		calls.WriteString(n + "(1);\n")
	}
	writeFile(t, dir, "route.ts", `export async function GET(req: Request) {
`+calls.String()+`	const rows = await db.query("SELECT * FROM entries")
	return Response.json(rows)
}
`)

	desc := &framework.Description{Name: "nextjs", Language: "typescript", BoundaryQueries: nextjsBoundaryQueries()}
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	done := make(chan struct{})
	var hits map[string][]boundaries.Hit
	var traceErr error
	go func() {
		hits, traceErr = tr.Trace(context.Background(), dir, []routes.Route{
			{Method: "GET", Path: "/api/entries", HandlerRef: "GET"},
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Trace did not complete within 15s — exponential blowup on colliding names likely reintroduced")
	}

	if traceErr != nil {
		t.Fatalf("Trace: %v", traceErr)
	}
	h := findHit(t, hits["GET"], "postgresql", "both")
	if h.Target != "entries" {
		t.Errorf("Target = %q; want %q", h.Target, "entries")
	}
}

// TestTracer_TypeScript_LargeIndexWithUniqueNames reproduces prov-2026-330a7fc5:
// prov-2026-afd410b6's memoization fix made the number of distinct (function
// name, remaining depth) lookups roughly linear in call-graph size, but each
// lookup still recompiled its tree-sitter query pattern from scratch for
// every single file in the index — on a real ~236-file/36-route Next.js
// project that constant factor alone was enough to look hung again (2+
// minutes with no progress), even with zero name collisions at all (unlike
// prov-2026-afd410b6's fixture, every function name here is unique). This
// fixture is a smaller, CI-sized version of the same shape: many files, each
// with its own uniquely-named functions, crossed against several routes.
func TestTracer_TypeScript_LargeIndexWithUniqueNames(t *testing.T) {
	dir := t.TempDir()

	const numFiles = 80
	const numRoutes = 15
	var allNames []string
	for i := 0; i < numFiles; i++ {
		for j := 0; j < 3; j++ {
			allNames = append(allNames, fmt.Sprintf("fn_%d_%d", i, j))
		}
	}
	for i := 0; i < numFiles; i++ {
		var body strings.Builder
		for j := 0; j < 3; j++ {
			name := fmt.Sprintf("fn_%d_%d", i, j)
			c1 := allNames[(i*3+j+1)%len(allNames)]
			c2 := allNames[(i*3+j+2)%len(allNames)]
			body.WriteString(fmt.Sprintf("export function %s(x) { return %s(x) + %s(x) }\n", name, c1, c2))
		}
		writeFile(t, dir, fmt.Sprintf("mod%d.ts", i), body.String())
	}

	var routeList []routes.Route
	for r := 0; r < numRoutes; r++ {
		var calls strings.Builder
		for k := 0; k < 5; k++ {
			calls.WriteString(allNames[(r*5+k)%len(allNames)] + "(1);\n")
		}
		handler := fmt.Sprintf("Handler%d", r)
		writeFile(t, dir, fmt.Sprintf("route%d.ts", r), fmt.Sprintf(`export async function %s(req: Request) {
%s	const rows = await db.query("SELECT * FROM r%d")
	return Response.json(rows)
}
`, handler, calls.String(), r))
		routeList = append(routeList, routes.Route{Method: "GET", Path: fmt.Sprintf("/r%d", r), HandlerRef: handler})
	}

	desc := &framework.Description{Name: "nextjs", Language: "typescript", BoundaryQueries: nextjsBoundaryQueries()}
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	done := make(chan struct{})
	var hits map[string][]boundaries.Hit
	var traceErr error
	go func() {
		hits, traceErr = tr.Trace(context.Background(), dir, routeList)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Trace did not complete within 10s on an 80-file/15-route index with unique names — query-recompilation cost likely reintroduced")
	}

	if traceErr != nil {
		t.Fatalf("Trace: %v", traceErr)
	}
	if len(hits) != numRoutes {
		t.Errorf("expected %d handler entries, got %d", numRoutes, len(hits))
	}
	for r := 0; r < numRoutes; r++ {
		h := findHit(t, hits[fmt.Sprintf("Handler%d", r)], "postgresql", "both")
		want := fmt.Sprintf("r%d", r)
		if h.Target != want {
			t.Errorf("Handler%d: Target = %q; want %q", r, h.Target, want)
		}
	}
}

func TestTracer_PackageQualifiedHandlerRef(t *testing.T) {
	desc := loadFramework(t, "chi")
	tr, err := boundaries.New(desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "handler.go", `package handlers

import "net/http"

func ListOrders(w http.ResponseWriter, r *http.Request) {
	db.Query("SELECT * FROM orders WHERE user_id = $1", 1)
}
`)

	// Handler ref with package qualifier
	hits, err := tr.Trace(context.Background(), dir, []routes.Route{
		{Method: "GET", Path: "/orders", HandlerRef: "handlers.ListOrders"},
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	h := findHit(t, hits["handlers.ListOrders"], "postgresql", "read")
	if h.Target != "orders" {
		t.Errorf("Target = %q; want %q", h.Target, "orders")
	}
}
