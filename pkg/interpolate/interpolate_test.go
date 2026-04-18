package interpolate

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
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

// --- DeclareVariable with constraints ---

func TestDeclareVariable_IntegerRange(t *testing.T) {
	r := NewResolver()
	if err := r.DeclareVariable("COUNT", "integer", map[string]string{"min": "10", "max": "20"}); err != nil {
		t.Fatalf("DeclareVariable error: %v", err)
	}
	val := r.Variables["COUNT"]
	n, err := strconv.Atoi(val)
	if err != nil {
		t.Fatalf("expected integer, got %q", val)
	}
	if n < 10 || n > 20 {
		t.Errorf("generated value %d outside [10, 20]", n)
	}
}

func TestDeclareVariable_IntegerDefault(t *testing.T) {
	r := NewResolver()
	if err := r.DeclareVariable("NUM", "integer", nil); err != nil {
		t.Fatalf("DeclareVariable error: %v", err)
	}
	val := r.Variables["NUM"]
	n, err := strconv.Atoi(val)
	if err != nil {
		t.Fatalf("expected integer, got %q", val)
	}
	if n < 1 || n > 99999 {
		t.Errorf("generated value %d outside default range [1, 99999]", n)
	}
}

func TestDeclareVariable_StringLength(t *testing.T) {
	r := NewResolver()
	if err := r.DeclareVariable("TOKEN", "string", map[string]string{"length": "32", "charset": "hex"}); err != nil {
		t.Fatalf("DeclareVariable error: %v", err)
	}
	val := r.Variables["TOKEN"]
	if len(val) != 32 {
		t.Errorf("expected length 32, got %d (%q)", len(val), val)
	}
	for _, c := range val {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("non-hex character %q in hex string %q", c, val)
			break
		}
	}
}

func TestDeclareVariable_StringCharsetAlpha(t *testing.T) {
	r := NewResolver()
	if err := r.DeclareVariable("LABEL", "string", map[string]string{"length": "16", "charset": "alpha"}); err != nil {
		t.Fatalf("DeclareVariable error: %v", err)
	}
	val := r.Variables["LABEL"]
	if len(val) != 16 {
		t.Errorf("expected length 16, got %d (%q)", len(val), val)
	}
	for _, c := range val {
		if c < 'a' || c > 'z' {
			t.Errorf("non-alpha character %q in alpha string %q", c, val)
			break
		}
	}
}

func TestDeclareVariable_StringPattern(t *testing.T) {
	r := NewResolver()
	if err := r.DeclareVariable("REF", "string", map[string]string{"pattern": "[A-Z]{3}[0-9]{4}"}); err != nil {
		t.Fatalf("DeclareVariable error: %v", err)
	}
	val := r.Variables["REF"]
	if len(val) != 7 {
		t.Errorf("expected length 7 from pattern [A-Z]{3}[0-9]{4}, got %d (%q)", len(val), val)
	}
	matched, _ := regexp.MatchString(`^[A-Z]{3}[0-9]{4}$`, val)
	if !matched {
		t.Errorf("value %q does not match pattern [A-Z]{3}[0-9]{4}", val)
	}
}

func TestDeclareVariable_Enum(t *testing.T) {
	r := NewResolver()
	if err := r.DeclareVariable("STATUS", "enum", map[string]string{"values": "pending,active,cancelled"}); err != nil {
		t.Fatalf("DeclareVariable error: %v", err)
	}
	val := r.Variables["STATUS"]
	valid := map[string]bool{"pending": true, "active": true, "cancelled": true}
	if !valid[val] {
		t.Errorf("enum value %q not in allowed set", val)
	}
}

func TestDeclareVariable_EnumMissingValues(t *testing.T) {
	r := NewResolver()
	err := r.DeclareVariable("STATUS", "enum", map[string]string{})
	if err == nil {
		t.Error("expected error for enum without values=, got nil")
	}
}

func TestDeclareVariable_UnknownConstraint(t *testing.T) {
	r := NewResolver()
	err := r.DeclareVariable("ID", "uuid", map[string]string{"length": "36"})
	if err == nil {
		t.Error("expected error for uuid with unsupported constraint, got nil")
	}
}

func TestDeclareVariable_UnknownConstraintOnInteger(t *testing.T) {
	r := NewResolver()
	err := r.DeclareVariable("N", "integer", map[string]string{"charset": "hex"})
	if err == nil {
		t.Error("expected error for integer with unsupported constraint, got nil")
	}
}

func TestDeclareVariable_NoConstraints_BackwardCompat(t *testing.T) {
	r := NewResolver()
	// These must all work exactly as before
	tests := []struct {
		name    string
		varType string
	}{
		{"SOME_UUID", "uuid"},
		{"ITEM_COUNT", "integer"},
		{"AUTH_TOKEN", "string"},
	}
	for _, tt := range tests {
		if err := r.DeclareVariable(tt.name, tt.varType, nil); err != nil {
			t.Errorf("DeclareVariable(%q, %q, nil) unexpected error: %v", tt.name, tt.varType, err)
		}
		if r.Variables[tt.name] == "" {
			t.Errorf("DeclareVariable(%q, %q, nil) produced empty value", tt.name, tt.varType)
		}
	}
}

func TestGenerateFromPattern_MultipleSegments(t *testing.T) {
	// Test that complex patterns produce correct output
	tests := []struct {
		pattern string
		re      string
		length  int
	}{
		{"[A-Z]{2}[0-9]{3}", `^[A-Z]{2}[0-9]{3}$`, 5},
		{"[a-z]{4}[0-9]{2}", `^[a-z]{4}[0-9]{2}$`, 6},
		{"[A-Za-z0-9]{8}", `^[A-Za-z0-9]{8}$`, 8},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got, err := generateFromPattern(tt.pattern)
			if err != nil {
				t.Fatalf("generateFromPattern(%q) error: %v", tt.pattern, err)
			}
			if len(got) != tt.length {
				t.Errorf("expected length %d, got %d (%q)", tt.length, len(got), got)
			}
			matched, _ := regexp.MatchString(tt.re, got)
			if !matched {
				t.Errorf("%q does not match %q", got, tt.re)
			}
		})
	}
}

func TestDeclareVariable_IntegerRange_StressTest(t *testing.T) {
	// Run many times to ensure all values land in range
	for i := 0; i < 200; i++ {
		r := NewResolver()
		if err := r.DeclareVariable(fmt.Sprintf("N%d", i), "integer", map[string]string{"min": "5", "max": "10"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		val := r.Variables[fmt.Sprintf("N%d", i)]
		n, _ := strconv.Atoi(val)
		if n < 5 || n > 10 {
			t.Errorf("value %d outside [5,10]", n)
		}
	}
}
