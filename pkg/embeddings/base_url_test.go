package embeddings

import "testing"

// TestOpenAIProviderHonorsBaseURL verifies that EmbeddingConfig.BaseURL, when set,
// is used as the request endpoint for the openai provider instead of the
// hardcoded https://api.openai.com/v1/embeddings, so OpenAI-compatible servers
// (LM Studio, Ollama, vLLM, LiteLLM, text-embeddings-inference) are reachable.
func TestOpenAIProviderHonorsBaseURL(t *testing.T) {
	t.Skip("placeholder: implemented alongside EmbeddingConfig.BaseURL (prov-2026-57aff9e1)")
}

// TestOpenAIProviderDefaultsBaseURL verifies that when BaseURL is unset, the
// openai provider still defaults to https://api.openai.com/v1, preserving
// current behavior.
func TestOpenAIProviderDefaultsBaseURL(t *testing.T) {
	t.Skip("placeholder: implemented alongside EmbeddingConfig.BaseURL (prov-2026-57aff9e1)")
}
