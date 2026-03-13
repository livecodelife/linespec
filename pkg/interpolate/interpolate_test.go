package interpolate

import (
	"os"
	"strings"
	"testing"
)

func TestExtractVariables(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "no variables",
			input:    "Hello World",
			expected: nil,
		},
		{
			name:     "single variable",
			input:    "Bearer ${API_TOKEN}",
			expected: []string{"API_TOKEN"},
		},
		{
			name:     "multiple variables",
			input:    "Host: ${DB_HOST}, Port: ${DB_PORT}",
			expected: []string{"DB_HOST", "DB_PORT"},
		},
		{
			name:     "duplicate variables",
			input:    "${VAR} and ${VAR}",
			expected: []string{"VAR"},
		},
		{
			name:     "variable in URL",
			input:    "https://api.${DOMAIN}.com/v1/users",
			expected: []string{"DOMAIN"},
		},
		{
			name:     "invalid variable format",
			input:    "${lowercase} and ${123INVALID}",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractVariables(tt.input)
			if len(got) != len(tt.expected) {
				t.Errorf("ExtractVariables() = %v, want %v", got, tt.expected)
				return
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("ExtractVariables()[%d] = %v, want %v", i, got[i], tt.expected[i])
				}
			}
		})
	}
}

func TestResolver_Resolve(t *testing.T) {
	// Set an environment variable for testing
	os.Setenv("TEST_API_KEY", "test-key-12345")
	defer os.Unsetenv("TEST_API_KEY")

	r := NewResolver()

	tests := []struct {
		name     string
		input    string
		contains string
		prefix   string
	}{
		{
			name:     "no variables",
			input:    "Hello World",
			contains: "Hello World",
		},
		{
			name:     "environment variable",
			input:    "Key: ${TEST_API_KEY}",
			contains: "Key: test-key-12345",
		},
		{
			name:   "generated variable",
			input:  "Token: ${RANDOM_TOKEN}",
			prefix: "Token: random_token_",
		},
		{
			name:   "multiple variables",
			input:  "${VAR1} and ${VAR2}",
			prefix: "var1_",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Resolve(tt.input)
			if tt.contains != "" && got != tt.contains {
				t.Errorf("Resolve() = %v, want %v", got, tt.contains)
			}
			if tt.prefix != "" && !strings.HasPrefix(got, tt.prefix) {
				t.Errorf("Resolve() = %v, want prefix %v", got, tt.prefix)
			}
		})
	}
}

func TestResolver_ResolveMap(t *testing.T) {
	r := NewResolver()

	input := map[string]string{
		"Authorization": "Bearer ${AUTH_TOKEN}",
		"X-API-Key":     "${API_KEY}",
	}

	result := r.ResolveMap(input)

	// Check that variables were resolved
	if result["Authorization"] == input["Authorization"] {
		t.Error("Authorization header was not resolved")
	}
	if result["X-API-Key"] == input["X-API-Key"] {
		t.Error("X-API-Key header was not resolved")
	}

	// Check that generated values have expected prefix
	if !strings.HasPrefix(result["X-API-Key"], "api_key_") {
		t.Errorf("API_KEY value has unexpected format: %v", result["X-API-Key"])
	}
}

func TestResolver_GetGeneratedEnv(t *testing.T) {
	r := NewResolver()

	// Resolve some variables
	r.Resolve("${VAR1}")
	r.Resolve("${VAR2}")

	// Set one in environment (shouldn't be in generated)
	os.Setenv("VAR3", "from-env")
	defer os.Unsetenv("VAR3")
	r.Resolve("${VAR3}")

	env := r.GetGeneratedEnv()

	// Should have exactly 2 generated variables
	if len(env) != 2 {
		t.Errorf("GetGeneratedEnv() returned %d vars, want 2", len(env))
	}

	// Check that VAR1 and VAR2 are in the output
	found := make(map[string]bool)
	for _, e := range env {
		if strings.HasPrefix(e, "VAR1=") || strings.HasPrefix(e, "VAR2=") {
			found[e[:strings.Index(e, "=")]] = true
		}
		if strings.HasPrefix(e, "VAR3=") {
			t.Error("VAR3 should not be in generated env (it came from os.Getenv)")
		}
	}

	if !found["VAR1"] || !found["VAR2"] {
		t.Error("Expected VAR1 and VAR2 in generated env")
	}
}

func TestHasVariables(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"Hello", false},
		{"${VAR}", true},
		{"Bearer ${TOKEN}", true},
		{"no vars here", false},
		{"${A}${B}", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := HasVariables(tt.input)
			if got != tt.expected {
				t.Errorf("HasVariables(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtractAllVariables(t *testing.T) {
	vars := ExtractAllVariables(
		"${VAR1} test",
		"${VAR2} and ${VAR3}",
		"${VAR1} again",
	)

	if len(vars) != 3 {
		t.Errorf("ExtractAllVariables() returned %d vars, want 3", len(vars))
	}

	seen := make(map[string]bool)
	for _, v := range vars {
		seen[v] = true
	}

	if !seen["VAR1"] || !seen["VAR2"] || !seen["VAR3"] {
		t.Error("Expected VAR1, VAR2, VAR3 in results")
	}
}

func TestResolver_ConsistentValues(t *testing.T) {
	// Ensure that resolving the same variable twice gives the same value
	r := NewResolver()

	val1 := r.Resolve("${TEST_VAR}")
	val2 := r.Resolve("${TEST_VAR}")

	if val1 != val2 {
		t.Errorf("Same variable resolved to different values: %q vs %q", val1, val2)
	}
}
