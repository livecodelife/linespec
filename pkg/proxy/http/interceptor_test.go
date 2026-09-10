package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/livecodelife/linespec/v3/pkg/registry"
	"github.com/livecodelife/linespec/v3/pkg/types"
)

func setupInterceptorWithMock(t *testing.T, returnsFile string, payloadContent []byte, responseHeaders map[string]string) (*Interceptor, string) {
	t.Helper()
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, returnsFile), payloadContent, 0644); err != nil {
		t.Fatalf("failed to write payload file: %v", err)
	}

	reg := registry.NewMockRegistry()
	reg.Register(&types.TestSpec{
		BaseDir: tmpDir,
		Expects: []types.ExpectStatement{
			{
				Channel:         types.HTTP,
				Method:          "GET",
				URL:             "/test",
				ReturnsFile:     returnsFile,
				BaseDir:         tmpDir,
				ResponseHeaders: responseHeaders,
			},
		},
	})

	interceptor := NewInterceptor(":0", reg)
	return interceptor, tmpDir
}

func TestInterceptor_ContentTypeJSON(t *testing.T) {
	interceptor, _ := setupInterceptorWithMock(t, "response.json", []byte(`{"key":"value"}`), nil)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	interceptor.handleRequest(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Expected Content-Type=application/json, got %q", ct)
	}
	// Body must be the raw JSON, not re-marshaled
	body := w.Body.String()
	if body != `{"key":"value"}` {
		t.Errorf("Expected raw JSON body, got %q", body)
	}
}

func TestInterceptor_ContentTypeYAML(t *testing.T) {
	// YAML payloads are re-encoded as JSON so HTTP clients always receive valid JSON.
	interceptor, _ := setupInterceptorWithMock(t, "response.yaml", []byte("key: value\n"), nil)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	interceptor.handleRequest(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Expected Content-Type=application/json (YAML re-encoded), got %q", ct)
	}
	if w.Body.String() != `{"key":"value"}` {
		t.Errorf("Expected JSON-encoded body, got %q", w.Body.String())
	}
}

func TestInterceptor_ContentTypeXML(t *testing.T) {
	xmlContent := `<root><key>value</key></root>`
	interceptor, _ := setupInterceptorWithMock(t, "response.xml", []byte(xmlContent), nil)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	interceptor.handleRequest(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/xml" {
		t.Errorf("Expected Content-Type=application/xml, got %q", ct)
	}
}

func TestInterceptor_ResponseHeadersOverride(t *testing.T) {
	overrides := map[string]string{
		"Content-Type": "application/vnd.api+json",
		"X-Custom":     "hello",
	}
	interceptor, _ := setupInterceptorWithMock(t, "response.json", []byte(`{"key":"value"}`), overrides)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	interceptor.handleRequest(w, req)

	if ct := w.Header().Get("Content-Type"); ct != "application/vnd.api+json" {
		t.Errorf("Expected overridden Content-Type, got %q", ct)
	}
	if xc := w.Header().Get("X-Custom"); xc != "hello" {
		t.Errorf("Expected X-Custom=hello, got %q", xc)
	}
}

func TestInterceptor_Start(t *testing.T) {
	reg := registry.NewMockRegistry()
	interceptor := NewInterceptor("127.0.0.1:0", reg)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	// Should exit cleanly when context is cancelled
	err := interceptor.Start(ctx)
	if err != nil {
		t.Errorf("Unexpected error from Start: %v", err)
	}
}

// TestInterceptor_ConcurrentFanOutMatchesByVerify is the case the fix exists for:
// three concurrent calls to one endpoint, one mock each, distinguished only by a
// VERIFY rule on the request body. Every call must get the response belonging to
// its own body no matter what order the calls arrive in.
func TestInterceptor_ConcurrentFanOutMatchesByVerify(t *testing.T) {
	titles := []string{"first", "second", "third"}

	tmpDir := t.TempDir()
	expects := make([]types.ExpectStatement, 0, len(titles))
	for _, title := range titles {
		file := title + "_resp.json"
		if err := os.WriteFile(filepath.Join(tmpDir, file), []byte(`{"title":"`+title+`"}`), 0644); err != nil {
			t.Fatalf("failed to write payload file: %v", err)
		}
		expects = append(expects, types.ExpectStatement{
			Channel:     types.HTTP,
			Method:      "POST",
			URL:         "/api/generate",
			ReturnsFile: file,
			BaseDir:     tmpDir,
			Verify: []types.VerifyRule{
				{Type: "CONTAINS", Target: "body", Pattern: `"title":"` + title + `"`},
			},
		})
	}

	reg := registry.NewMockRegistry()
	reg.Register(&types.TestSpec{BaseDir: tmpDir, Expects: expects})
	interceptor := NewInterceptor(":0", reg)

	// Drive the calls in the worst order for arrival-order pairing: exactly
	// reversed against declaration order.
	for i := len(titles) - 1; i >= 0; i-- {
		title := titles[i]
		body := `{"title":"` + title + `"}`
		req := httptest.NewRequest("POST", "/api/generate", strings.NewReader(body))
		w := httptest.NewRecorder()
		interceptor.handleRequest(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("call for %q: expected 200, got %d (%s)", title, w.Code, w.Body.String())
		}
		if got := w.Body.String(); got != body {
			t.Errorf("call for %q: expected its own mock response %s, got %s", title, body, got)
		}
	}

	if err := reg.VerifyAll(); err != nil {
		t.Errorf("expected every mock to be consumed exactly once: %v", err)
	}
}

// TestInterceptor_SingleWrongBodyStillReportsVerifyFailure guards the diagnostics
// the fallback exists to keep: one mock, a request that fails its VERIFY rule, and
// the failure must still name the rule rather than silently passing through as an
// uncalled mock.
func TestInterceptor_SingleWrongBodyStillReportsVerifyFailure(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "resp.json"), []byte(`{"ok":true}`), 0644); err != nil {
		t.Fatalf("failed to write payload file: %v", err)
	}

	reg := registry.NewMockRegistry()
	reg.Register(&types.TestSpec{
		BaseDir: tmpDir,
		Expects: []types.ExpectStatement{
			{
				Channel: types.HTTP, Method: "POST", URL: "/api/generate",
				ReturnsFile: "resp.json", BaseDir: tmpDir,
				Verify: []types.VerifyRule{
					{Type: "CONTAINS", Target: "body", Pattern: `"title":"expected"`},
				},
			},
		},
	})
	interceptor := NewInterceptor(":0", reg)

	req := httptest.NewRequest("POST", "/api/generate", strings.NewReader(`{"title":"wrong"}`))
	w := httptest.NewRecorder()
	interceptor.handleRequest(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a failed VERIFY, got %d (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "VERIFY failed") {
		t.Errorf("expected the response to report the VERIFY failure, got %s", w.Body.String())
	}
}
