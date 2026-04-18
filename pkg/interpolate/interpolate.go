package interpolate

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"sort"
	"strconv"
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
	// VarTypes holds declared types for variables from a VARS block ("uuid", "integer", "string", "enum")
	VarTypes map[string]string
	// VarConstraints holds per-variable key=value constraint maps from a VARS block
	VarConstraints map[string]map[string]string
}

// NewResolver creates a new Resolver with empty variable sets
func NewResolver() *Resolver {
	return &Resolver{
		Variables:      make(map[string]string),
		Generated:      make(map[string]bool),
		VarTypes:       make(map[string]string),
		VarConstraints: make(map[string]map[string]string),
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

// DeclareVariable records a type declaration and optional constraints for a variable
// and pre-generates its value. Called by the DSL parser when it processes a VARS block.
func (r *Resolver) DeclareVariable(name, varType string, constraints map[string]string) error {
	ltype := strings.ToLower(varType)
	r.VarTypes[name] = ltype
	if constraints != nil {
		r.VarConstraints[name] = constraints
	}

	if err := validateConstraints(ltype, constraints); err != nil {
		return fmt.Errorf("variable %s: %w", name, err)
	}

	if _, exists := r.Variables[name]; exists {
		return nil
	}
	if envVal := os.Getenv(name); envVal != "" {
		r.Variables[name] = envVal
		return nil
	}
	r.Variables[name] = generateTypedValue(name, ltype, constraints)
	r.Generated[name] = true
	return nil
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
			if c, ok := r.VarConstraints[name]; ok && len(c) > 0 {
				keys := make([]string, 0, len(c))
				for k := range c {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				parts := make([]string, 0, len(c))
				for _, k := range keys {
					parts = append(parts, k+"="+c[k])
				}
				typeHint = fmt.Sprintf(" (%s %s)", t, strings.Join(parts, " "))
			}
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
	constraints := r.VarConstraints[varName]
	value := generateTypedValue(varName, varType, constraints)
	r.Variables[varName] = value
	r.Generated[varName] = true
	return value
}

// validConstraintKeys defines the allowed constraint keys for each type.
var validConstraintKeys = map[string]map[string]bool{
	"uuid":    {},
	"integer": {"min": true, "max": true},
	"int":     {"min": true, "max": true},
	"string":  {"length": true, "charset": true, "pattern": true},
	"enum":    {"values": true},
	"":        {}, // undeclared / name-inferred — no constraints
}

// validateConstraints checks that the provided constraint keys are valid for the given type.
func validateConstraints(varType string, constraints map[string]string) error {
	allowed, known := validConstraintKeys[varType]
	if !known {
		// Unknown types (future-proofing): reject any constraints
		if len(constraints) > 0 {
			return fmt.Errorf("unknown type %q does not support constraints", varType)
		}
		return nil
	}
	for k := range constraints {
		if !allowed[k] {
			return fmt.Errorf("type %q does not support constraint %q", varType, k)
		}
	}
	if varType == "enum" {
		if _, ok := constraints["values"]; !ok {
			return fmt.Errorf("type \"enum\" requires a values= constraint")
		}
	}
	return nil
}

// generateTypedValue creates a random value for the given type and constraints.
func generateTypedValue(varName, varType string, constraints map[string]string) string {
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
		return generateInteger(constraints)

	case "string":
		return generateString(varName, constraints)

	case "enum":
		return generateEnum(constraints)

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

// generateInteger produces a random integer in [min, max] (default 1–99999).
func generateInteger(constraints map[string]string) string {
	minVal := 1
	maxVal := 99999

	if v, ok := constraints["min"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			minVal = n
		}
	}
	if v, ok := constraints["max"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			maxVal = n
		}
	}
	if minVal >= maxVal {
		return strconv.Itoa(minVal)
	}

	rangeSize := big.NewInt(int64(maxVal - minVal + 1))
	n, err := rand.Int(rand.Reader, rangeSize)
	if err != nil {
		return strconv.Itoa(minVal)
	}
	return strconv.Itoa(int(n.Int64()) + minVal)
}

// charsets maps charset names to their character pools.
var charsets = map[string]string{
	"alpha":        "abcdefghijklmnopqrstuvwxyz",
	"uppercase":    "ABCDEFGHIJKLMNOPQRSTUVWXYZ",
	"alphanumeric": "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
	"numeric":      "0123456789",
	"hex":          "0123456789abcdef",
}

// generateString produces a random string using charset/length or pattern constraints.
// If neither is provided it falls back to the legacy varname_hexsuffix format.
func generateString(varName string, constraints map[string]string) string {
	pattern, hasPattern := constraints["pattern"]
	if hasPattern {
		result, err := generateFromPattern(pattern)
		if err == nil {
			return result
		}
		// Fall through to charset/length on pattern parse failure
	}

	cs, hasCharset := constraints["charset"]
	lengthStr, hasLength := constraints["length"]

	if !hasCharset && !hasLength {
		// Legacy default
		b := make([]byte, 8)
		rand.Read(b) //nolint:errcheck
		return fmt.Sprintf("%s_%s", strings.ToLower(varName), hex.EncodeToString(b))
	}

	pool := charsets["alphanumeric"] // default pool when only length is given
	if hasCharset {
		if p, ok := charsets[cs]; ok {
			pool = p
		}
	}

	length := 16
	if hasLength {
		if n, err := strconv.Atoi(lengthStr); err == nil && n > 0 {
			length = n
		}
	}

	return randomStringFromPool(pool, length)
}

// generateEnum picks a random value from a comma-separated values= constraint.
func generateEnum(constraints map[string]string) string {
	raw := constraints["values"]
	parts := strings.Split(raw, ",")
	var options []string
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			options = append(options, s)
		}
	}
	if len(options) == 0 {
		return ""
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(options))))
	if err != nil {
		return options[0]
	}
	return options[int(n.Int64())]
}

// randomStringFromPool generates a string of length n using characters from pool.
func randomStringFromPool(pool string, n int) string {
	poolSize := big.NewInt(int64(len(pool)))
	result := make([]byte, n)
	for i := range result {
		idx, err := rand.Int(rand.Reader, poolSize)
		if err != nil {
			result[i] = pool[0]
			continue
		}
		result[i] = pool[idx.Int64()]
	}
	return string(result)
}

// patternSegmentRe matches one segment of a simple character-class pattern:
//
//	[chars]{N}  or  [chars]  (count defaults to 1)
//	literal text (no brackets)
var patternSegmentRe = regexp.MustCompile(`\[([^\]]+)\](?:\{(\d+)\})?|([^\[{]+)`)

// generateFromPattern produces a string matching a simplified regex pattern.
// Supported syntax: character classes [A-Z], [a-z], [0-9], literals, {N} exact-count quantifiers.
func generateFromPattern(pattern string) (string, error) {
	matches := patternSegmentRe.FindAllStringSubmatch(pattern, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("pattern %q contains no recognisable segments", pattern)
	}

	var sb strings.Builder
	for _, m := range matches {
		charClass := m[1] // content inside [...]
		countStr := m[2]  // content inside {...}, may be empty
		literal := m[3]   // plain text outside brackets

		if literal != "" {
			sb.WriteString(literal)
			continue
		}

		pool, err := expandCharClass(charClass)
		if err != nil {
			return "", err
		}

		count := 1
		if countStr != "" {
			if n, err := strconv.Atoi(countStr); err == nil && n > 0 {
				count = n
			}
		}
		sb.WriteString(randomStringFromPool(pool, count))
	}
	return sb.String(), nil
}

// expandCharClass converts a character class string (e.g. "A-Z0-9_") to its full character pool.
func expandCharClass(class string) (string, error) {
	var pool []byte
	runes := []rune(class)
	for i := 0; i < len(runes); i++ {
		if i+2 < len(runes) && runes[i+1] == '-' {
			lo, hi := runes[i], runes[i+2]
			if lo > hi {
				return "", fmt.Errorf("invalid character range %c-%c in pattern", lo, hi)
			}
			for c := lo; c <= hi; c++ {
				pool = append(pool, byte(c))
			}
			i += 2
		} else {
			pool = append(pool, byte(runes[i]))
		}
	}
	if len(pool) == 0 {
		return "", fmt.Errorf("empty character class")
	}
	return string(pool), nil
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
