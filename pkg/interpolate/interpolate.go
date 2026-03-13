package interpolate

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// VariablePattern matches ${VAR_NAME} syntax
var VariablePattern = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)\}`)

// Resolver handles environment variable substitution with generated values
type Resolver struct {
	// Variables tracks all discovered variables and their values
	Variables map[string]string
	// Generated tracks which variables were auto-generated (not from env)
	Generated map[string]bool
}

// NewResolver creates a new Resolver with empty variable sets
func NewResolver() *Resolver {
	return &Resolver{
		Variables: make(map[string]string),
		Generated: make(map[string]bool),
	}
}

// ExtractVariables finds all ${VAR_NAME} patterns in a string and returns unique variable names
func ExtractVariables(s string) []string {
	matches := VariablePattern.FindAllStringSubmatch(s, -1)
	seen := make(map[string]bool)
	var vars []string
	for _, m := range matches {
		if len(m) > 1 && !seen[m[1]] {
			seen[m[1]] = true
			vars = append(vars, m[1])
		}
	}
	return vars
}

// Resolve substitutes all ${VAR_NAME} patterns in a string with their values
// If a variable is not set in the environment and not already in Variables,
// it generates a random value
func (r *Resolver) Resolve(s string) string {
	return VariablePattern.ReplaceAllStringFunc(s, func(match string) string {
		varName := VariablePattern.FindStringSubmatch(match)[1]
		value, exists := r.Variables[varName]
		if !exists {
			value = r.getOrGenerateValue(varName)
		}
		return value
	})
}

// ResolveMap substitutes variables in all string values of a map
func (r *Resolver) ResolveMap(m map[string]string) map[string]string {
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = r.Resolve(v)
	}
	return result
}

// getOrGenerateValue returns the value for a variable, generating one if needed
func (r *Resolver) getOrGenerateValue(varName string) string {
	// Check if already resolved
	if value, exists := r.Variables[varName]; exists {
		return value
	}

	// Check environment
	if value := os.Getenv(varName); value != "" {
		r.Variables[varName] = value
		return value
	}

	// Generate random value
	value := generateRandomValue(varName)
	r.Variables[varName] = value
	r.Generated[varName] = true
	return value
}

// generateRandomValue creates a random value based on the variable name
// This helps make the values somewhat predictable for debugging
func generateRandomValue(varName string) string {
	// Generate 16 bytes of random data
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based value if crypto/rand fails
		return fmt.Sprintf("%s_%d", strings.ToLower(varName), os.Getpid())
	}

	// Create a readable prefix based on variable name
	prefix := strings.ToLower(varName)
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b[:8]))
}

// GetGeneratedEnv returns environment variable assignments for all generated values
// This is used to inject variables into containers
func (r *Resolver) GetGeneratedEnv() []string {
	var env []string
	for name := range r.Generated {
		if value, exists := r.Variables[name]; exists {
			env = append(env, fmt.Sprintf("%s=%s", name, value))
		}
	}
	return env
}

// HasVariables checks if a string contains any ${VAR_NAME} patterns
func HasVariables(s string) bool {
	return VariablePattern.MatchString(s)
}

// ExtractAllVariables scans multiple strings and returns all unique variable names
func ExtractAllVariables(strings ...string) []string {
	seen := make(map[string]bool)
	var allVars []string
	for _, s := range strings {
		vars := ExtractVariables(s)
		for _, v := range vars {
			if !seen[v] {
				seen[v] = true
				allVars = append(allVars, v)
			}
		}
	}
	return allVars
}
