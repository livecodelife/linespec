package embeddings

import (
	"os"
	"strings"
	"testing"

	"github.com/livecodelife/linespec/pkg/config"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name    string
		config  config.EmbeddingConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config with literal key",
			config: config.EmbeddingConfig{
				Provider: "voyage",
				APIKey:   "test-api-key",
			},
			wantErr: false,
		},
		{
			name: "missing provider",
			config: config.EmbeddingConfig{
				APIKey: "test-api-key",
			},
			wantErr: true,
			errMsg:  "provider not configured",
		},
		{
			name: "env var reference - not set",
			config: config.EmbeddingConfig{
				Provider: "voyage",
				APIKey:   "${NONEXISTENT_VAR}",
			},
			wantErr: true,
			errMsg:  "environment variable NONEXISTENT_VAR not set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NewClient() expected error, got nil")
					return
				}
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("NewClient() error = %v, want containing %v", err, tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("NewClient() unexpected error = %v", err)
				return
			}
			if client == nil {
				t.Error("NewClient() returned nil client")
			}
		})
	}
}

func TestNewClientWithEnvVar(t *testing.T) {
	// Set up environment variable
	os.Setenv("TEST_VOYAGE_KEY", "test-key-from-env")
	defer os.Unsetenv("TEST_VOYAGE_KEY")

	config := config.EmbeddingConfig{
		Provider: "voyage",
		APIKey:   "${TEST_VOYAGE_KEY}",
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("NewClient() with env var failed: %v", err)
	}

	if !client.IsConfigured() {
		t.Error("Expected client to be configured")
	}
}

func TestClientIsConfigured(t *testing.T) {
	tests := []struct {
		name     string
		client   *Client
		expected bool
	}{
		{
			name:     "nil client",
			client:   nil,
			expected: false,
		},
		{
			name: "configured client",
			client: &Client{
				config: config.EmbeddingConfig{Provider: "voyage"},
				apiKey: "test-key",
			},
			expected: true,
		},
		{
			name: "missing provider",
			client: &Client{
				config: config.EmbeddingConfig{},
				apiKey: "test-key",
			},
			expected: false,
		},
		{
			name: "missing api key",
			client: &Client{
				config: config.EmbeddingConfig{Provider: "voyage"},
				apiKey: "",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.client.IsConfigured()
			if result != tt.expected {
				t.Errorf("IsConfigured() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestClientDimension(t *testing.T) {
	tests := []struct {
		name     string
		client   *Client
		expected int
	}{
		{
			name:     "nil client",
			client:   nil,
			expected: 2048, // default
		},
		{
			name: "voyage-4-large index model",
			client: &Client{
				config: config.EmbeddingConfig{IndexModel: "voyage-4-large"},
			},
			expected: 2048,
		},
		{
			name: "voyage-4-lite query model",
			client: &Client{
				config: config.EmbeddingConfig{QueryModel: "voyage-4-lite"},
			},
			expected: 2048,
		},
		{
			name: "default",
			client: &Client{
				config: config.EmbeddingConfig{},
			},
			expected: 2048,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.client.Dimension()
			if result != tt.expected {
				t.Errorf("Dimension() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestClientSimilarityThreshold(t *testing.T) {
	tests := []struct {
		name     string
		client   *Client
		expected float64
	}{
		{
			name:     "nil client",
			client:   nil,
			expected: 0.82, // default
		},
		{
			name: "default threshold",
			client: &Client{
				config: config.EmbeddingConfig{},
			},
			expected: 0.82,
		},
		{
			name: "custom threshold",
			client: &Client{
				config: config.EmbeddingConfig{SimilarityThreshold: 0.90},
			},
			expected: 0.90,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.client.SimilarityThreshold()
			if result != tt.expected {
				t.Errorf("SimilarityThreshold() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestClientIndexOnComplete(t *testing.T) {
	tests := []struct {
		name     string
		client   *Client
		expected bool
	}{
		{
			name:     "nil client",
			client:   nil,
			expected: true, // default when nil
		},
		{
			name: "default (zero value - false)",
			client: &Client{
				config: config.EmbeddingConfig{},
			},
			expected: false, // Go bool zero value is false
		},
		{
			name: "explicitly false",
			client: &Client{
				config: config.EmbeddingConfig{IndexOnComplete: false},
			},
			expected: false,
		},
		{
			name: "explicitly true",
			client: &Client{
				config: config.EmbeddingConfig{IndexOnComplete: true},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.client.IndexOnComplete()
			if result != tt.expected {
				t.Errorf("IndexOnComplete() = %v, want %v", result, tt.expected)
			}
		})
	}
}
