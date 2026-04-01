package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/types"
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
	interceptor, _ := setupInterceptorWithMock(t, "response.yaml", []byte("key: value\n"), nil)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	interceptor.handleRequest(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/yaml" {
		t.Errorf("Expected Content-Type=application/yaml, got %q", ct)
	}
	if w.Body.String() != "key: value\n" {
		t.Errorf("Expected raw YAML body, got %q", w.Body.String())
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
