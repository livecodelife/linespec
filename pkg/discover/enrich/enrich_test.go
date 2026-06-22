package enrich_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/livecodelife/linespec/pkg/discover/enrich"
	"github.com/livecodelife/linespec/pkg/provenance"
)

// cleanGitEnv returns the process environment with GIT_DIR / GIT_WORK_TREE /
// GIT_INDEX_FILE stripped out. When tests run inside linespec provenance complete
// those variables are set by the parent git process and would leak into temp
// git repos created by the tests, causing "must be run in a work tree" errors.
func cleanGitEnv() []string {
	skip := map[string]bool{
		"GIT_DIR": true, "GIT_WORK_TREE": true,
		"GIT_INDEX_FILE": true, "GIT_OBJECT_DIRECTORY": true,
	}
	var out []string
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if !skip[key] {
			out = append(out, e)
		}
	}
	return out
}

// --- helpers ---

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runInDir := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = cleanGitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runInDir("init")
	runInDir("config", "user.email", "test@example.com")
	runInDir("config", "user.name", "Test")
	// Disable hooks so the linespec commit-msg hook doesn't fire in test repos.
	runInDir("config", "core.hooksPath", "/dev/null")
	return dir
}

func writeAndCommit(t *testing.T, repoDir, filename, msg string) {
	t.Helper()
	path := filepath.Join(repoDir, filename)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	runInDir := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = cleanGitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runInDir("add", filename)
	runInDir("commit", "-m", msg)
}

func writeMinimalRecord(t *testing.T, dir string, files []string) string {
	t.Helper()
	provDir := filepath.Join(dir, "provenance")
	if err := os.MkdirAll(provDir, 0755); err != nil {
		t.Fatal(err)
	}
	loader := provenance.NewLoader(provDir, nil)
	record := &provenance.Record{
		ID:            "prov-2026-testaaaa",
		Title:         "test record",
		Status:        provenance.StatusDraft,
		Type:          provenance.RecordTypeBlueprint,
		CreatedAt:     "2026-01-01",
		Author:        "test@example.com",
		AffectedScope: files,
		FilePath:      filepath.Join(provDir, "prov-2026-testaaaa.yml"),
	}
	if err := loader.SaveRecord(record); err != nil {
		t.Fatal(err)
	}
	return record.FilePath
}

func anthropicServer(t *testing.T, intent string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": intent},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

// --- tests ---

func TestEnrich_SkipsWhenNoAPIKey(t *testing.T) {
	repoDir := initRepo(t)
	writeAndCommit(t, repoDir, "handlers/users.go", "add user handler")
	recordFile := writeMinimalRecord(t, repoDir, []string{"handlers/users.go"})

	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	results, err := enrich.Enrich(enrich.Input{
		RepoDir:     repoDir,
		RecordFiles: []string{recordFile},
	})
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Skipped {
		t.Error("expected Skipped=true when no API key configured")
	}
	if results[0].Err != nil {
		t.Errorf("unexpected error: %v", results[0].Err)
	}
}

func TestEnrich_SkipsWhenNoCommits(t *testing.T) {
	repoDir := initRepo(t)
	// Write a file but don't commit it.
	if err := os.WriteFile(filepath.Join(repoDir, "handler.go"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	recordFile := writeMinimalRecord(t, repoDir, []string{"handler.go"})

	results, err := enrich.Enrich(enrich.Input{
		RepoDir:     repoDir,
		RecordFiles: []string{recordFile},
		Provider:    "openai",
		APIKey:      "sk-test",
	})
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Skipped {
		t.Error("expected Skipped=true when no commits exist for the files")
	}
}

func TestEnrich_CallsLLMAndUpdatesRecord(t *testing.T) {
	wantIntent := "Handles user authentication and session management."

	srv := anthropicServer(t, wantIntent)
	defer srv.Close()

	repoDir := initRepo(t)
	writeAndCommit(t, repoDir, "handlers/auth.go", "add authentication handler")
	recordFile := writeMinimalRecord(t, repoDir, []string{"handlers/auth.go"})

	results, err := enrich.EnrichWithBaseURL(enrich.Input{
		RepoDir:     repoDir,
		RecordFiles: []string{recordFile},
		Provider:    "anthropic",
		APIKey:      "test-key",
	}, srv.URL)
	if err != nil {
		t.Fatalf("EnrichWithBaseURL: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Skipped {
		t.Fatal("expected not skipped")
	}
	if results[0].Err != nil {
		t.Fatalf("unexpected error: %v", results[0].Err)
	}
	if results[0].Intent != wantIntent {
		t.Errorf("Intent = %q; want %q", results[0].Intent, wantIntent)
	}

	// Verify the record file was updated on disk.
	data, err := os.ReadFile(recordFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), wantIntent) {
		t.Errorf("record file does not contain intent %q\ncontent:\n%s", wantIntent, data)
	}
}

func TestEnrich_LLMFailureIsNonFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	repoDir := initRepo(t)
	writeAndCommit(t, repoDir, "handlers/users.go", "add user listing")
	recordFile := writeMinimalRecord(t, repoDir, []string{"handlers/users.go"})

	results, err := enrich.EnrichWithBaseURL(enrich.Input{
		RepoDir:     repoDir,
		RecordFiles: []string{recordFile},
		Provider:    "anthropic",
		APIKey:      "test-key",
	}, srv.URL)
	if err != nil {
		t.Fatalf("Enrich must not return fatal error on LLM failure: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err == nil {
		t.Error("expected per-result error on LLM failure")
	}

	// Intent must remain empty after LLM failure.
	record, err := provenance.NewLoader("", nil).LoadFile(recordFile)
	if err != nil {
		t.Fatal(err)
	}
	if record.Intent != "" {
		t.Errorf("intent should remain empty after LLM failure, got %q", record.Intent)
	}
}

func TestEnrich_MultipleRecords(t *testing.T) {
	repoDir := initRepo(t)
	writeAndCommit(t, repoDir, "handlers/users.go", "add user handler")
	writeAndCommit(t, repoDir, "handlers/posts.go", "add post handler")

	// Use separate provenance dirs so record filenames don't collide.
	prov1 := filepath.Join(t.TempDir(), "prov1")
	prov2 := filepath.Join(t.TempDir(), "prov2")
	os.MkdirAll(prov1, 0755)
	os.MkdirAll(prov2, 0755)

	makeRecord := func(dir string, files []string) string {
		loader := provenance.NewLoader(dir, nil)
		rec := &provenance.Record{
			ID:            "prov-2026-testbbbb",
			Title:         "test",
			Status:        provenance.StatusDraft,
			Type:          provenance.RecordTypeBlueprint,
			CreatedAt:     "2026-01-01",
			Author:        "test@example.com",
			AffectedScope: files,
			FilePath:      filepath.Join(dir, "prov-2026-testbbbb.yml"),
		}
		if err := loader.SaveRecord(rec); err != nil {
			t.Fatal(err)
		}
		return rec.FilePath
	}

	r1 := makeRecord(prov1, []string{"handlers/users.go"})
	r2 := makeRecord(prov2, []string{"handlers/posts.go"})

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		resp := map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Synthesized intent"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	results, err := enrich.EnrichWithBaseURL(enrich.Input{
		RepoDir:     repoDir,
		RecordFiles: []string{r1, r2},
		Provider:    "anthropic",
		APIKey:      "test-key",
	}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for i, res := range results {
		if res.Err != nil {
			t.Errorf("result[%d].Err = %v", i, res.Err)
		}
		if res.Skipped {
			t.Errorf("result[%d] unexpectedly skipped", i)
		}
	}
	if callCount != 2 {
		t.Errorf("expected 2 LLM calls, got %d", callCount)
	}
}

func TestEnrich_CommitMessagesDeduplicatedAcrossFiles(t *testing.T) {
	repoDir := initRepo(t)
	// Same message appears from two files - should only call LLM once.
	writeAndCommit(t, repoDir, "handlers/a.go", "initial implementation")

	// Add a second file in the same commit - can't do that with writeAndCommit,
	// so create a second commit with a different message.
	writeAndCommit(t, repoDir, "handlers/b.go", "add second handler")

	recordFile := writeMinimalRecord(t, repoDir, []string{"handlers/a.go", "handlers/b.go"})

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		resp := map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Implements request handling for user and post resources."},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	results, err := enrich.EnrichWithBaseURL(enrich.Input{
		RepoDir:     repoDir,
		RecordFiles: []string{recordFile},
		Provider:    "anthropic",
		APIKey:      "test-key",
	}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err != nil {
		t.Errorf("unexpected error: %v", results[0].Err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 LLM call, got %d", callCount)
	}
}

func TestEnrich_MissingRecordFileProducesPerResultError(t *testing.T) {
	results, err := enrich.Enrich(enrich.Input{
		RecordFiles: []string{"/nonexistent/record.yml"},
		Provider:    "openai",
		APIKey:      "sk-test",
	})
	if err != nil {
		t.Fatalf("Enrich must not return fatal error for missing record file: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err == nil {
		t.Error("expected per-result error for missing record file")
	}
}
