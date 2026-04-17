package interpolate

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"sort"
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
	// VarTypes holds declared types for variables from a VARS block ("uuid", "integer", "string")
	VarTypes map[string]string
}

// NewResolver creates a new Resolver with empty variable sets
func NewResolver() *Resolver {
	return &Resolver{
		Variables: make(map[string]string),
		Generated: make(map[string]bool),
		VarTypes:  make(map[string]string),
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

// DeclareVariable records a type declaration for a variable and pre-generates its value.
// Called by the DSL parser when it processes a VARS block.
func (r *Resolver) DeclareVariable(name, varType string) {
	r.VarTypes[name] = strings.ToLower(varType)
	if _, exists := r.Variables[name]; exists {
		return
	}
	if envVal := os.Getenv(name); envVal != "" {
		r.Variables[name] = envVal
		return
	}
	r.Variables[name] = generateTypedValue(name, varType)
	r.Generated[name] = true
}

// FormatVarMap returns a human-readable summary of all resolved variables, suitable
// for appending to a test failure message to aid debugging.
func (r *Resolver) FormatVarMap() string {
	if len(r.Variables) == 0 {
		return ""
	}
	names := make([]string, 0, len(r.Variables))
	for k := range r.Variables {
		names = append(names, k)
	}
	sort.Strings(names)
	var sb strings.Builder
	sb.WriteString("Resolved variables:\n")
	for _, name := range names {
		typeHint := ""
		if t, ok := r.VarTypes[name]; ok {
			typeHint = fmt.Sprintf(" (%s)", t)
		}
		sb.WriteString(fmt.Sprintf("  %s%s = %q\n", name, typeHint, r.Variables[name]))
	}
	return sb.String()
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

	// Generate using declared type if available, otherwise infer from name
	varType := r.VarTypes[varName]
	value := generateTypedValue(varName, varType)
	r.Variables[varName] = value
	r.Generated[varName] = true
	return value
}

// generateTypedValue creates a random value for the given type.
// varType may be "uuid", "integer", "string", or empty (falls back to name-based inference).
func generateTypedValue(varName, varType string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s_%d", strings.ToLower(varName), os.Getpid())
	}

	switch strings.ToLower(varType) {
	case "uuid":
		b[6] = (b[6] & 0x0f) | 0x40 // version 4
		b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
		return fmt.Sprintf("%s-%s-%s-%s-%s",
			hex.EncodeToString(b[0:4]),
			hex.EncodeToString(b[4:6]),
			hex.EncodeToString(b[6:8]),
			hex.EncodeToString(b[8:10]),
			hex.EncodeToString(b[10:16]),
		)
	case "integer", "int":
		// Produce a small positive integer (1–99999) that renders unambiguously.
		n := (int(b[0])<<8|int(b[1]))%99999 + 1
		return fmt.Sprintf("%d", n)
	default:
		// Fall back to name-based inference so existing behaviour is unchanged.
		upper := strings.ToUpper(varName)
		if upper == "UUID" || strings.HasSuffix(upper, "_UUID") {
			b[6] = (b[6] & 0x0f) | 0x40
			b[8] = (b[8] & 0x3f) | 0x80
			return fmt.Sprintf("%s-%s-%s-%s-%s",
				hex.EncodeToString(b[0:4]),
				hex.EncodeToString(b[4:6]),
				hex.EncodeToString(b[6:8]),
				hex.EncodeToString(b[8:10]),
				hex.EncodeToString(b[10:16]),
			)
		}
		return fmt.Sprintf("%s_%s", strings.ToLower(varName), hex.EncodeToString(b[:8]))
	}
}

// ApplyTypeCorrections walks a parsed JSON/YAML value and converts string values
// that were produced by integer-typed variable interpolation back to Go integers,
// ensuring they encode as JSON numbers rather than quoted strings.
func ApplyTypeCorrections(data interface{}, r *Resolver) interface{} {
	if r == nil || len(r.VarTypes) == 0 {
		return data
	}
	// Build a reverse map: resolved_value → varType (only for integer variables)
	intValues := make(map[string]bool)
	for name, typ := range r.VarTypes {
		if typ == "integer" || typ == "int" {
			if val, ok := r.Variables[name]; ok {
				intValues[val] = true
			}
		}
	}
	if len(intValues) == 0 {
		return data
	}
	return applyCorrections(data, intValues)
}

func applyCorrections(data interface{}, intValues map[string]bool) interface{} {
	switch v := data.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for k, val := range v {
			out[k] = applyCorrections(val, intValues)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, val := range v {
			out[i] = applyCorrections(val, intValues)
		}
		return out
	case string:
		if intValues[v] {
			var n int
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				return n
			}
		}
		return v
	default:
		return v
	}
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
