package routes_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/discover/framework"
	"github.com/livecodelife/linespec/v3/pkg/discover/routes"
)

func writeFSFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func nextjsFilesystemDesc() *framework.Description {
	return &framework.Description{
		Name:     "nextjs",
		Language: "typescript",
		FilesystemRoutes: &framework.FilesystemRoutes{
			AppDir:    "app",
			PagesDir:  "pages",
			RouteFile: "route",
			PageFile:  "page",
			Methods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE"},
		},
	}
}

func findGroup(t *testing.T, groups []routes.Group, name string) routes.Group {
	t.Helper()
	for _, g := range groups {
		if g.Name == name {
			return g
		}
	}
	t.Fatalf("no group named %q in %+v", name, groups)
	return routes.Group{}
}

func findRoute(t *testing.T, rs []routes.Route, method, path string) routes.Route {
	t.Helper()
	for _, r := range rs {
		if r.Method == method && r.Path == path {
			return r
		}
	}
	t.Fatalf("no route %s %s in %+v", method, path, rs)
	return routes.Route{}
}

// TestAssemble_AppRouter_RouteHandlers reproduces the App Router's core
// case from prov-2026-4446307d: route.ts exporting GET/POST, discovered by
// filesystem convention rather than a route-registration query.
func TestAssemble_AppRouter_RouteHandlers(t *testing.T) {
	dir := t.TempDir()
	writeFSFile(t, dir, "app/api/users/route.ts", `export async function GET(req: Request) {
	return Response.json([])
}
export async function POST(req: Request) {
	return new Response(null, { status: 201 })
}
`)

	a, err := routes.New(nextjsFilesystemDesc())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	groups, err := a.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	g := findGroup(t, groups, "api/users")
	getRoute := findRoute(t, g.Routes, "GET", "/api/users")
	if getRoute.HandlerRef == "" {
		t.Error("expected a non-empty HandlerRef for a route.ts export")
	}
	findRoute(t, g.Routes, "POST", "/api/users")
}

// TestAssemble_AppRouter_DynamicSegment verifies "[id]" directory segments
// become ":id" path parameters.
func TestAssemble_AppRouter_DynamicSegment(t *testing.T) {
	dir := t.TempDir()
	writeFSFile(t, dir, "app/api/users/[id]/route.ts", `export async function GET(req: Request) {
	return Response.json({})
}
`)

	a, err := routes.New(nextjsFilesystemDesc())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	groups, err := a.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	g := findGroup(t, groups, "api/users/[id]")
	findRoute(t, g.Routes, "GET", "/api/users/:id")
}

// TestAssemble_AppRouter_CatchAllSegment verifies "[...slug]" becomes a
// "*slug" catch-all path.
func TestAssemble_AppRouter_CatchAllSegment(t *testing.T) {
	dir := t.TempDir()
	writeFSFile(t, dir, "app/blog/[...slug]/page.tsx", `export default function Page() { return null }
`)

	a, err := routes.New(nextjsFilesystemDesc())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	groups, err := a.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	g := findGroup(t, groups, "blog/[...slug]")
	r := findRoute(t, g.Routes, "GET", "/blog/*slug")
	if r.HandlerRef != "" {
		t.Errorf("expected no HandlerRef for a page.tsx (no fixed export name to look up), got %q", r.HandlerRef)
	}
}

// TestAssemble_AppRouter_RouteGroupDropped verifies a "(group)" segment
// (Next.js route groups) contributes no path segment, only organization.
func TestAssemble_AppRouter_RouteGroupDropped(t *testing.T) {
	dir := t.TempDir()
	writeFSFile(t, dir, "app/(marketing)/about/page.tsx", `export default function Page() { return null }
`)

	a, err := routes.New(nextjsFilesystemDesc())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	groups, err := a.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	g := findGroup(t, groups, "(marketing)/about")
	findRoute(t, g.Routes, "GET", "/about")
}

// TestAssemble_PagesRouter_BestEffort covers the legacy Pages Router:
// pages/**  for pages, pages/api/** for API routes, both best-effort with
// no HandlerRef.
func TestAssemble_PagesRouter_BestEffort(t *testing.T) {
	dir := t.TempDir()
	writeFSFile(t, dir, "pages/about.tsx", `export default function About() { return null }
`)
	writeFSFile(t, dir, "pages/api/users.ts", `export default function handler(req, res) { res.json([]) }
`)
	writeFSFile(t, dir, "pages/_app.tsx", `export default function App() { return null }
`)

	a, err := routes.New(nextjsFilesystemDesc())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	groups, err := a.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	var all []routes.Route
	for _, g := range groups {
		all = append(all, g.Routes...)
	}
	findRoute(t, all, "GET", "/about")
	findRoute(t, all, "GET", "/api/users")

	for _, r := range all {
		if r.Path == "/_app" {
			t.Errorf("_app.tsx should be skipped as a special file, got route %+v", r)
		}
	}
}

// TestAssemble_AppRouter_ScopedDirectlyToAppDir reproduces prov-2026-ebc2266b:
// `discover --dir app --framework nextjs` scopes the scan directly to the App
// Router root itself, but assembleFilesystemRoutes unconditionally joined
// AppDir onto dir, looking for a nonexistent nested "app/app" and silently
// finding zero routes.
func TestAssemble_AppRouter_ScopedDirectlyToAppDir(t *testing.T) {
	root := t.TempDir()
	writeFSFile(t, root, "app/api/users/route.ts", `export const dynamic = "force-dynamic"
export const runtime = "nodejs"

export async function GET(req: Request) {
	return Response.json([])
}
`)

	a, err := routes.New(nextjsFilesystemDesc())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	appDir := filepath.Join(root, "app")
	groups, err := a.Assemble(context.Background(), appDir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(groups) == 0 {
		t.Fatal("expected routes when scanning app/ directly, got none")
	}
	g := findGroup(t, groups, "api/users")
	r := findRoute(t, g.Routes, "GET", "/api/users")
	if r.HandlerRef == "" {
		t.Error("expected a non-empty HandlerRef")
	}
}

// TestAssemble_AppRouter_RepoRootStillWorks guards against a regression in
// the fix above: scanning the actual project root (the normal case, where
// AppDir is a real subdirectory of dir) must keep working.
func TestAssemble_AppRouter_RepoRootStillWorks(t *testing.T) {
	root := t.TempDir()
	writeFSFile(t, root, "app/api/users/route.ts", `export async function GET(req: Request) {
	return Response.json([])
}
`)

	a, err := routes.New(nextjsFilesystemDesc())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	groups, err := a.Assemble(context.Background(), root)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	g := findGroup(t, groups, "api/users")
	findRoute(t, g.Routes, "GET", "/api/users")
}

// TestAssemble_AppRouter_ArrowFunctionExport reproduces the other route
// export shape prov-2026-ebc2266b's constraint calls for:
// `export const GET = async (req) => {}`, alongside sibling route-segment
// config consts (dynamic/runtime/maxDuration) that must not be mistaken for
// handlers.
func TestAssemble_AppRouter_ArrowFunctionExport(t *testing.T) {
	dir := t.TempDir()
	writeFSFile(t, dir, "app/api/cron/ingest/route.ts", `import { NextResponse } from "next/server"

export const dynamic = "force-dynamic"
export const runtime = "nodejs"
export const maxDuration = 300

export const GET = async (request: Request) => {
	return NextResponse.json({ ok: true })
}
`)

	a, err := routes.New(nextjsFilesystemDesc())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	groups, err := a.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	g := findGroup(t, groups, "api/cron/ingest")
	if len(g.Routes) != 1 {
		t.Fatalf("expected exactly 1 route (GET only, no route for the config consts), got %+v", g.Routes)
	}
	findRoute(t, g.Routes, "GET", "/api/cron/ingest")
}

// TestAssemble_MissingAppAndPagesDir verifies a project with neither app/
// nor pages/ produces zero routes rather than an error — most real App
// Router-only projects have no pages/ directory at all.
func TestAssemble_MissingAppAndPagesDir(t *testing.T) {
	dir := t.TempDir()

	a, err := routes.New(nextjsFilesystemDesc())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	groups, err := a.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("expected no groups for a directory with neither app/ nor pages/, got %+v", groups)
	}
}

// TestAssemble_AppRouter_RespectsGitignore verifies the filesystem route
// scanner goes through ignorewalk (prov-2026-f0b20266) rather than a raw
// filepath.Walk — a gitignored directory under app/ must not be scanned.
func TestAssemble_AppRouter_RespectsGitignore(t *testing.T) {
	dir := t.TempDir()
	writeFSFile(t, dir, ".gitignore", "app/api/.next-cache/\n")
	writeFSFile(t, dir, "app/api/users/route.ts", `export async function GET(req: Request) { return Response.json([]) }
`)
	writeFSFile(t, dir, "app/api/.next-cache/route.ts", `export async function GET(req: Request) { return Response.json([]) }
`)

	a, err := routes.New(nextjsFilesystemDesc())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	groups, err := a.Assemble(context.Background(), dir)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	for _, g := range groups {
		if g.Name == "api/.next-cache" {
			t.Errorf("expected the gitignored app/api/.next-cache directory to be excluded, got group %+v", g)
		}
	}
}
