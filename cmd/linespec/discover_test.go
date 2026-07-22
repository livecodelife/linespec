package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/provenance"
)

// writeChiProject lays out a minimal chi service with one route-bearing package
// (handlers) and three directories that contain no HTTP route registration at
// all (services, models, repo) — the shape described in prov-2026-3486daec: a
// framework is detected, but only route-bearing files get any blueprint.
func writeChiProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.21\n\nrequire github.com/go-chi/chi/v5 v5.0.0\n",
		"handlers/router.go": `package handlers

import "github.com/go-chi/chi/v5"

func SetupRouter(r chi.Router) {
	r.Get("/users", listUsers)
	r.Post("/users", createUser)
}
`,
		"services/worker.go": `package services

func DoWork() {}
`,
		"models/user.go": `package models

type User struct {
	ID int
}
`,
		"repo/repo.go": `package repo

func Save() {}
`,
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestRunDiscover_FrameworkDetected_CoversNonRouteDirectories reproduces
// prov-2026-3486daec: once a framework is detected, discover must still
// produce blueprint coverage for directories that have no HTTP route
// registrations (services, models, repositories, ...), in addition to the
// route-bearing directory it always covered.
func TestRunDiscover_FrameworkDetected_CoversNonRouteDirectories(t *testing.T) {
	dir := writeChiProject(t)

	cfg := &provenance.ProvenanceConfig{Dir: "provenance", Enforcement: "warn"}
	opts := discoverOptions{Dir: dir, Format: "table"}

	runDiscover(opts, cfg, dir)

	provDir := filepath.Join(dir, "provenance")
	entries, err := os.ReadDir(provDir)
	if err != nil {
		t.Fatalf("read provenance dir: %v", err)
	}

	got := 0
	for _, e := range entries {
		if !e.IsDir() {
			got++
		}
	}

	// 1 blueprint for the route-bearing "handlers" package + 1 each for the
	// 3 non-route directories (services, models, repo) that the framework-based
	// route assembler never sees.
	const want = 4
	if got != want {
		t.Fatalf("expected %d blueprint records (1 route group + 3 non-route directories), got %d in %s", want, got, provDir)
	}
}

// TestRunDiscover_FrameworkDetected_NoDuplicateForRouteDirectory guards the
// record's second constraint: the supplemental framework-agnostic pass must
// not also emit a blueprint for the "handlers" directory, since the route
// assembler already produced one for it.
func TestRunDiscover_FrameworkDetected_NoDuplicateForRouteDirectory(t *testing.T) {
	dir := writeChiProject(t)

	cfg := &provenance.ProvenanceConfig{Dir: "provenance", Enforcement: "warn"}
	opts := discoverOptions{Dir: dir, Format: "table"}

	runDiscover(opts, cfg, dir)

	provDir := filepath.Join(dir, "provenance")
	entries, err := os.ReadDir(provDir)
	if err != nil {
		t.Fatalf("read provenance dir: %v", err)
	}

	handlersBlueprints := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(provDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "handlers/router.go") {
			handlersBlueprints++
		}
	}
	if handlersBlueprints != 1 {
		t.Fatalf("expected exactly 1 blueprint covering handlers/router.go, got %d", handlersBlueprints)
	}
}
