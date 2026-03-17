package embeddings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/livecodelife/linespec/pkg/config"
)

// Client generates embeddings via a configured API
type Client struct {
	config     config.EmbeddingConfig
	apiKey     string
	defaultDim int
}

// NewClient creates a new embedding client from configuration
func NewClient(cfg config.EmbeddingConfig) (*Client, error) {
	// Validate config
	if cfg.Provider == "" {
		return nil, fmt.Errorf("embedding provider not configured")
	}

	// Get API key from environment if it's an env var reference
	apiKey := cfg.APIKey
	if strings.HasPrefix(apiKey, "${") && strings.HasSuffix(apiKey, "}") {
		envVar := apiKey[2 : len(apiKey)-1]
		apiKey = os.Getenv(envVar)
		if apiKey == "" {
			return nil, fmt.Errorf("API key environment variable %s not set", envVar)
		}
	}

	// Set default similarity threshold if not set
	if cfg.SimilarityThreshold == 0 {
		cfg.SimilarityThreshold = 0.50
	}

	return &Client{
		config:     cfg,
		apiKey:     apiKey,
		defaultDim: 2048, // Default for voyage-4-large and voyage-4-lite at 2048 dims
	}, nil
}

// IsConfigured returns true if the client has valid configuration
func (c *Client) IsConfigured() bool {
	return c != nil && c.config.Provider != "" && c.apiKey != ""
}

// Generate creates an embedding vector for the given text (backward compatibility)
func (c *Client) Generate(text string) ([]float32, error) {
	return c.GenerateDocument(text)
}

// GenerateDocument creates an embedding using 'document' input_type (for indexing)
func (c *Client) GenerateDocument(text string) ([]float32, error) {
	switch c.config.Provider {
	case "voyage":
		// Use index_model (voyage-4-large) for document embeddings
		model := c.config.IndexModel
		if model == "" {
			model = "voyage-4-large"
		}
		return c.generateVoyage(text, model, "document")
	case "openai":
		return c.generateOpenAI(text)
	default:
		return nil, fmt.Errorf("unsupported embedding provider: %s", c.config.Provider)
	}
}

// GenerateQuery creates an embedding using 'query' input_type (for searching)
func (c *Client) GenerateQuery(text string) ([]float32, error) {
	switch c.config.Provider {
	case "voyage":
		// Use query_model (voyage-4-lite) for query embeddings
		model := c.config.QueryModel
		if model == "" {
			model = "voyage-4-lite"
		}
		return c.generateVoyage(text, model, "query")
	case "openai":
		return c.generateOpenAI(text)
	default:
		return nil, fmt.Errorf("unsupported embedding provider: %s", c.config.Provider)
	}
}

// Dimension returns the embedding dimension (2048 for both voyage-4-large and voyage-4-lite)
func (c *Client) Dimension() int {
	if c == nil {
		return 2048 // default dimension for both models at 2048 dims
	}
	return 2048
}

// SimilarityThreshold returns the configured similarity threshold
func (c *Client) SimilarityThreshold() float64 {
	if c == nil || c.config.SimilarityThreshold == 0 {
		return 0.82
	}
	return c.config.SimilarityThreshold
}

// IndexOnComplete returns whether to generate embeddings on complete
func (c *Client) IndexOnComplete() bool {
	if c == nil {
		return true // default
	}
	return c.config.IndexOnComplete
}

// voyageRequest represents the Voyage AI embedding API request
type voyageRequest struct {
	Input           []string `json:"input"`
	Model           string   `json:"model"`
	InputType       string   `json:"input_type,omitempty"`
	OutputDimension int      `json:"output_dimension,omitempty"`
}

// voyageResponse represents the Voyage AI embedding API response
type voyageResponse struct {
	Object string `json:"object"`
	Data   []struct {
		Object    string    `json:"object"`
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

// generateVoyage generates embeddings using the Voyage AI API with specified model and input_type
func (c *Client) generateVoyage(text string, model string, inputType string) ([]float32, error) {
	reqBody := voyageRequest{
		Input:           []string{text},
		Model:           model,
		InputType:       inputType,
		OutputDimension: 2048, // Request 2048 dimensions
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.voyageai.com/v1/embeddings", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var voyageResp voyageResponse
	if err := json.Unmarshal(body, &voyageResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(voyageResp.Data) == 0 {
		return nil, fmt.Errorf("no embedding data in response")
	}

	return voyageResp.Data[0].Embedding, nil
}

// openaiRequest represents the OpenAI embedding API request
type openaiRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// openaiResponse represents the OpenAI embedding API response
type openaiResponse struct {
	Object string `json:"object"`
	Data   []struct {
		Object    string    `json:"object"`
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// generateOpenAI generates embeddings using the OpenAI API (fallback support)
func (c *Client) generateOpenAI(text string) ([]float32, error) {
	model := c.config.IndexModel
	if model == "" {
		model = "text-embedding-3-small"
	}

	reqBody := openaiRequest{
		Model: model,
		Input: text,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/embeddings", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var openaiResp openaiResponse
	if err := json.Unmarshal(body, &openaiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(openaiResp.Data) == 0 {
		return nil, fmt.Errorf("no embedding data in response")
	}

	return openaiResp.Data[0].Embedding, nil
}

// ExtractTextFromRecord extracts the text to embed from a provenance record
// Uses full title, complete intent, and all constraints for rich semantic content
func ExtractTextFromRecord(title, intent string, constraints []string) string {
	var parts []string

	// Decision: full title (most important signal)
	if title != "" {
		parts = append(parts, "Decision: "+title)
	}

	// Intent: complete intent field (not truncated)
	if intent != "" {
		intent = strings.TrimSpace(intent)
		if intent != "" {
			parts = append(parts, "Intent: "+intent)
		}
	}

	// Constraints: each as a complete sentence
	if len(constraints) > 0 {
		constraintParts := make([]string, 0, len(constraints))
		for _, c := range constraints {
			// Clean up constraint text
			cleaned := strings.TrimSpace(c)
			if cleaned != "" {
				constraintParts = append(constraintParts, cleaned)
			}
		}
		if len(constraintParts) > 0 {
			parts = append(parts, "Constraints: "+strings.Join(constraintParts, ". ")+".")
		}
	}

	return strings.Join(parts, "\n")
}
