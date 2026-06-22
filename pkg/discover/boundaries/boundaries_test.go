package boundaries_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/livecodelife/linespec/pkg/discover/boundaries"
	"github.com/livecodelife/linespec/pkg/discover/framework"
	"github.com/livecodelife/linespec/pkg/discover/routes"
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
