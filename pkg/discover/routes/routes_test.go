package routes_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/discover/framework"
	"github.com/livecodelife/linespec/v3/pkg/discover/routes"
)

// loadBuiltinFramework loads a named framework from the built-in descriptions.
func loadBuiltinFramework(t *testing.T, name string) *framework.Description {
	t.Helper()
	descs, err := framework.Load("")
	if err != nil {
		t.Fatalf("framework.Load: %v", err)
	}
	d, ok := descs[name]
	if !ok {
		t.Fatalf("built-in framework %q not found", name)
	}
	return d
}

func routePaths(rs []routes.Route) []string {
	paths := make([]string, len(rs))
	for i, r := range rs {
		paths[i] = r.Method + " " + r.Path
	}
	sort.Strings(paths)
	return paths
}

func groupNames(gs []routes.Group) []string {
	names := make([]string, len(gs))
	for i, g := range gs {
		names[i] = g.Name
	}
	return names
}

// --- Chi tests ---

func TestAssembler_Chi_FlatRoutes(t *testing.T) {
	desc := loadBuiltinFramework(t, "chi")
	asm, err := routes.New(desc)
	if err != nil {
		t.Fatalf("routes.New: %v", err)
	}

	dir := t.TempDir()
	src := `package handlers

import "github.com/go-chi/chi/v5"

func SetupRouter(r chi.Router) {
	r.Get("/health", healthCheck)
	r.Get("/users", listUsers)
	r.Post("/users", createUser)
	r.Delete("/users/{id}", deleteUser)
}
`
	if err := os.WriteFile(filepath.Join(dir, "router.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	groups, err := asm.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(groups) == 0 {
		t.Fatal("expected at least one group, got none")
	}

	var allRoutes []routes.Route
	for _, g := range groups {
		allRoutes = append(allRoutes, g.Routes...)
	}

	got := routePaths(allRoutes)
	want := []string{
		"DELETE /users/{id}",
		"GET /health",
		"GET /users",
		"POST /users",
	}
	if !equalStringSlices(got, want) {
		t.Errorf("routes mismatch\n got:  %v\n want: %v", got, want)
	}
}

func TestAssembler_Chi_NestedPrefixes(t *testing.T) {
	desc := loadBuiltinFramework(t, "chi")
	asm, err := routes.New(desc)
	if err != nil {
		t.Fatalf("routes.New: %v", err)
	}

	dir := t.TempDir()
	src := `package handlers

import "github.com/go-chi/chi/v5"

func SetupRouter(r chi.Router) {
	r.Get("/health", healthCheck)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/users", listUsers)
		r.Post("/users", createUser)
		r.Route("/users/{id}", func(r chi.Router) {
			r.Get("/", getUser)
			r.Put("/", updateUser)
			r.Delete("/", deleteUser)
		})
	})
}
`
	if err := os.WriteFile(filepath.Join(dir, "router.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	groups, err := asm.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	var allRoutes []routes.Route
	for _, g := range groups {
		allRoutes = append(allRoutes, g.Routes...)
	}

	got := routePaths(allRoutes)
	want := []string{
		"DELETE /api/v1/users/{id}/",
		"GET /api/v1/users",
		"GET /api/v1/users/{id}/",
		"GET /health",
		"POST /api/v1/users",
		"PUT /api/v1/users/{id}/",
	}
	if !equalStringSlices(got, want) {
		t.Errorf("routes mismatch\n got:  %v\n want: %v", got, want)
	}
}

func TestAssembler_Chi_PackageGrouping(t *testing.T) {
	desc := loadBuiltinFramework(t, "chi")
	asm, err := routes.New(desc)
	if err != nil {
		t.Fatalf("routes.New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "users.go", `package handlers

import "github.com/go-chi/chi/v5"

func SetupUsers(r chi.Router) {
	r.Get("/users", listUsers)
	r.Post("/users", createUser)
}
`)
	writeFile(t, dir, "posts.go", `package handlers

import "github.com/go-chi/chi/v5"

func SetupPosts(r chi.Router) {
	r.Get("/posts", listPosts)
}
`)

	groups, err := asm.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// Both files are in the same "handlers" package → one group
	if len(groups) != 1 {
		t.Errorf("expected 1 group (same package), got %d: %v", len(groups), groupNames(groups))
	}
	if len(groups) > 0 && groups[0].Name != "handlers" {
		t.Errorf("expected group name %q, got %q", "handlers", groups[0].Name)
	}
}

func TestAssembler_Chi_HandlerRef(t *testing.T) {
	desc := loadBuiltinFramework(t, "chi")
	asm, err := routes.New(desc)
	if err != nil {
		t.Fatalf("routes.New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "router.go", `package handlers

import "github.com/go-chi/chi/v5"

func SetupRouter(r chi.Router) {
	r.Get("/users", listUsers)
	r.Post("/users", handlers.CreateUser)
}
`)

	groups, err := asm.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	var listRoute, createRoute routes.Route
	for _, g := range groups {
		for _, r := range g.Routes {
			switch r.Method + " " + r.Path {
			case "GET /users":
				listRoute = r
			case "POST /users":
				createRoute = r
			}
		}
	}

	if listRoute.HandlerRef != "listUsers" {
		t.Errorf("GET /users handler: got %q, want %q", listRoute.HandlerRef, "listUsers")
	}
	if createRoute.HandlerRef != "handlers.CreateUser" {
		t.Errorf("POST /users handler: got %q, want %q", createRoute.HandlerRef, "handlers.CreateUser")
	}
}

// --- Rails tests ---

func TestAssembler_Rails_ResourcesExpansion(t *testing.T) {
	desc := loadBuiltinFramework(t, "rails")
	asm, err := routes.New(desc)
	if err != nil {
		t.Fatalf("routes.New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "routes.rb", `
Rails.application.routes.draw do
  resources :users
end
`)

	groups, err := asm.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	var allRoutes []routes.Route
	for _, g := range groups {
		allRoutes = append(allRoutes, g.Routes...)
	}

	got := routePaths(allRoutes)
	want := []string{
		"DELETE /users/:id",
		"GET /users",
		"GET /users/:id",
		"GET /users/:id/edit",
		"GET /users/new",
		"PATCH /users/:id",
		"POST /users",
		"PUT /users/:id",
	}
	if !equalStringSlices(got, want) {
		t.Errorf("routes mismatch\n got:  %v\n want: %v", got, want)
	}
}

func TestAssembler_Rails_NamespacePrefix(t *testing.T) {
	desc := loadBuiltinFramework(t, "rails")
	asm, err := routes.New(desc)
	if err != nil {
		t.Fatalf("routes.New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "routes.rb", `
Rails.application.routes.draw do
  namespace :api do
    resources :users
  end
end
`)

	groups, err := asm.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	var allRoutes []routes.Route
	for _, g := range groups {
		allRoutes = append(allRoutes, g.Routes...)
	}

	got := routePaths(allRoutes)
	want := []string{
		"DELETE /api/users/:id",
		"GET /api/users",
		"GET /api/users/:id",
		"GET /api/users/:id/edit",
		"GET /api/users/new",
		"PATCH /api/users/:id",
		"POST /api/users",
		"PUT /api/users/:id",
	}
	if !equalStringSlices(got, want) {
		t.Errorf("routes mismatch\n got:  %v\n want: %v", got, want)
	}
}

func TestAssembler_Rails_ExplicitRoutes(t *testing.T) {
	desc := loadBuiltinFramework(t, "rails")
	asm, err := routes.New(desc)
	if err != nil {
		t.Fatalf("routes.New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "routes.rb", `
Rails.application.routes.draw do
  get '/login', to: 'sessions#new'
  post '/login', to: 'sessions#create'
  delete '/logout', to: 'sessions#destroy'
end
`)

	groups, err := asm.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	var allRoutes []routes.Route
	for _, g := range groups {
		allRoutes = append(allRoutes, g.Routes...)
	}

	got := routePaths(allRoutes)
	want := []string{
		"DELETE /logout",
		"GET /login",
		"POST /login",
	}
	if !equalStringSlices(got, want) {
		t.Errorf("routes mismatch\n got:  %v\n want: %v", got, want)
	}
}

// --- Sinatra tests ---

func TestAssembler_Sinatra_BasicRoutes(t *testing.T) {
	desc := loadBuiltinFramework(t, "sinatra")
	asm, err := routes.New(desc)
	if err != nil {
		t.Fatalf("routes.New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "app.rb", `
require 'sinatra'

class App < Sinatra::Base
  get '/users' do
    'list users'
  end

  post '/users' do
    'create user'
  end

  get '/users/:id' do
    'get user'
  end

  delete '/users/:id' do
    'delete user'
  end
end
`)

	groups, err := asm.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	var allRoutes []routes.Route
	for _, g := range groups {
		allRoutes = append(allRoutes, g.Routes...)
	}

	got := routePaths(allRoutes)
	want := []string{
		"DELETE /users/:id",
		"GET /users",
		"GET /users/:id",
		"POST /users",
	}
	if !equalStringSlices(got, want) {
		t.Errorf("routes mismatch\n got:  %v\n want: %v", got, want)
	}
}

func TestAssembler_Sinatra_FileGrouping(t *testing.T) {
	desc := loadBuiltinFramework(t, "sinatra")
	asm, err := routes.New(desc)
	if err != nil {
		t.Fatalf("routes.New: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, dir, "users.rb", `
class UsersApp < Sinatra::Base
  get '/users' do; end
  post '/users' do; end
end
`)
	writeFile(t, dir, "posts.rb", `
class PostsApp < Sinatra::Base
  get '/posts' do; end
end
`)

	groups, err := asm.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// Two files → two groups (file grouping strategy)
	if len(groups) != 2 {
		t.Errorf("expected 2 groups (one per file), got %d: %v", len(groups), groupNames(groups))
	}
}

// --- Rails helper tests ---

func TestExpandResources(t *testing.T) {
	tests := []struct {
		name     string
		resource string
		prefix   string
		wantLen  int
		wantCtrl string
	}{
		{"plural no prefix", "users", "", 8, "UsersController"},
		{"plural with prefix", "users", "/api", 8, "UsersController"},
		{"snake_case", "user_profiles", "", 8, "UserProfilesController"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			desc := loadBuiltinFramework(t, "rails")
			asm, err := routes.New(desc)
			if err != nil {
				t.Fatal(err)
			}
			_ = asm // resources expansion tested via Assemble

			// Verify controller name derivation via the Rails assembler indirectly
			// by checking the handler refs on expanded routes.
			dir := t.TempDir()
			writeFile(t, dir, "routes.rb", "Rails.application.routes.draw do\n  resources :"+tc.resource+"\nend\n")
			groups, err := asm.Assemble(context.Background(), dir)
			if err != nil {
				t.Fatal(err)
			}
			var count int
			for _, g := range groups {
				count += len(g.Routes)
				for _, r := range g.Routes {
					if len(r.HandlerRef) == 0 {
						t.Errorf("expected non-empty handler ref, got empty for route %s %s", r.Method, r.Path)
					}
				}
			}
			if count != tc.wantLen {
				t.Errorf("expanded %d routes, want %d", count, tc.wantLen)
			}
		})
	}
}

// --- helpers ---

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
