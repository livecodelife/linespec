package main

import (
	"os"
	"strings"
	"testing"
)

// TestMain isolates every test in this package from the caller's git environment. linespec's
// pre-commit hook runs a record's specs inside the hook, where git exports GIT_DIR,
// GIT_INDEX_FILE and GIT_WORK_TREE; a test that runs git in a temp dir would otherwise act on
// the enclosing repository (prov-2026-f3c3e6e7).
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if name, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(name, "GIT_") {
			os.Unsetenv(name)
		}
	}
	home, err := os.MkdirTemp("", "linespec-test-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
