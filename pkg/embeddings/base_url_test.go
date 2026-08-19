package embeddings

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/config"
)

// TestOpenAIProviderHonorsBaseURL verifies that EmbeddingConfig.BaseURL, when set,
// is used as the request endpoint for the openai provider instead of the
// hardcoded https://api.openai.com/v1/embeddings, so OpenAI-compatible servers
// (LM Studio, Ollama, vLLM, LiteLLM, text-embeddings-inference) are reachable.
func TestOpenAIProviderHonorsBaseURL(t *testing.T) {
	var gotPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(openaiResponse{
			Object: "list",
			Data: []struct {
				Object    string    `json:"object"`
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Object: "embedding", Embedding: []float32{0.1, 0.2, 0.3}, Index: 0},
			},
			Model: "text-embedding-3-small",
		})
	}))
	defer server.Close()

	client, err := NewClient(config.EmbeddingConfig{
		Provider: "openai",
		APIKey:   "test-key",
		BaseURL:  server.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}

	docVec, err := client.GenerateDocument("hello world")
	if err != nil {
		t.Fatalf("GenerateDocument() unexpected error: %v", err)
	}
	if len(docVec) != 3 {
		t.Errorf("GenerateDocument() returned %d dims, want 3", len(docVec))
	}
	if gotPath != "/v1/embeddings" {
		t.Errorf("GenerateDocument() hit path %q, want /v1/embeddings", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("GenerateDocument() Authorization = %q, want Bearer test-key", gotAuth)
	}

	queryVec, err := client.GenerateQuery("hello world")
	if err != nil {
		t.Fatalf("GenerateQuery() unexpected error: %v", err)
	}
	if len(queryVec) != 3 {
		t.Errorf("GenerateQuery() returned %d dims, want 3", len(queryVec))
	}
	if gotPath != "/v1/embeddings" {
		t.Errorf("GenerateQuery() hit path %q, want /v1/embeddings", gotPath)
	}
}

// TestOpenAIProviderDefaultsBaseURL verifies that when BaseURL is unset, the
// openai provider still defaults to https://api.openai.com/v1, preserving
// current behavior.
func TestOpenAIProviderDefaultsBaseURL(t *testing.T) {
	got := openAIEmbeddingsEndpoint("")
	want := "https://api.openai.com/v1/embeddings"
	if got != want {
		t.Errorf("openAIEmbeddingsEndpoint(\"\") = %q, want %q", got, want)
	}
}

// TestOpenAIEmbeddingsEndpointTrimsTrailingSlash guards the base_url join logic
// against a doubled slash when the configured base_url already ends in one.
func TestOpenAIEmbeddingsEndpointTrimsTrailingSlash(t *testing.T) {
	got := openAIEmbeddingsEndpoint("http://localhost:1234/v1/")
	want := "http://localhost:1234/v1/embeddings"
	if got != want {
		t.Errorf("openAIEmbeddingsEndpoint(with trailing slash) = %q, want %q", got, want)
	}
}
