package boundaries

import (
	"regexp"
	"strings"
	"unicode"
)

// ModelToTable converts an ActiveRecord model class name to its conventional table name.
// Applies Rails naming conventions: strip namespace, CamelCase → snake_case, then pluralize.
// "User" → "users", "BlogPost" → "blog_posts", "MyApp::OrderItem" → "order_items"
func ModelToTable(model string) string {
	// Strip namespace qualifier: "MyApp::User" → "User"
	if i := strings.LastIndex(model, "::"); i >= 0 {
		model = model[i+2:]
	}
	return pluralize(camelToSnake(model))
}

// TableFromSQL extracts the primary table name from a SQL string.
// Returns empty string when no table name can be statically determined.
// "SELECT * FROM users WHERE id = $1" → "users"
// "INSERT INTO orders (col) VALUES ($1)" → "orders"
func TableFromSQL(sql string) string {
	sql = stripStringQuotes(sql)
	m := sqlTableRE.FindStringSubmatch(sql)
	if len(m) == 0 {
		return ""
	}
	// The regex has alternating groups; find the first non-empty capture.
	for _, g := range m[1:] {
		if g != "" {
			return strings.ToLower(g)
		}
	}
	return ""
}

// looksLikeSQL returns true if s appears to be a SQL query string.
func looksLikeSQL(s string) bool {
	upper := strings.ToUpper(strings.TrimSpace(s))
	for _, kw := range sqlKeywords {
		if strings.HasPrefix(upper, kw) {
			return true
		}
	}
	return false
}

var sqlKeywords = []string{
	"SELECT ", "INSERT ", "UPDATE ", "DELETE ", "WITH ", "CREATE ",
	"DROP ", "ALTER ", "TRUNCATE ", "EXPLAIN ", "CALL ",
}

// sqlTableRE matches FROM/INTO/UPDATE/JOIN followed by an optional quote and a table name.
// Captures in alternating groups to handle multiple keywords in one regex.
var sqlTableRE = regexp.MustCompile(
	`(?i)\bFROM\s+"?([a-z_][a-z0-9_]*)"?` +
		`|\bINTO\s+"?([a-z_][a-z0-9_]*)"?` +
		`|\bUPDATE\s+"?([a-z_][a-z0-9_]*)"?` +
		`|\bJOIN\s+"?([a-z_][a-z0-9_]*)"?`,
)

// camelToSnake converts CamelCase identifiers to snake_case.
// "BlogPost" → "blog_post", "URLShortener" → "url_shortener"
func camelToSnake(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				next := rune(0)
				if i+1 < len(runes) {
					next = runes[i+1]
				}
				// Insert underscore before an uppercase letter when:
				// - preceded by a lowercase letter (e.g. "Post" in "BlogPost")
				// - preceded by an uppercase letter that is followed by a lowercase letter
				//   (e.g. "S" in "URLShortener": URL→url, Shortener→shortener)
				if unicode.IsLower(prev) || (unicode.IsUpper(prev) && unicode.IsLower(next)) {
					b.WriteByte('_')
				}
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// pluralize converts a snake_case English singular noun to its plural form.
// Covers the common cases encountered in Rails model naming.
func pluralize(s string) string {
	if s == "" {
		return s
	}

	irregulars := map[string]string{
		"person":      "people",
		"man":         "men",
		"woman":       "women",
		"child":       "children",
		"tooth":       "teeth",
		"foot":        "feet",
		"mouse":       "mice",
		"ox":          "oxen",
		"sheep":       "sheep",
		"fish":        "fish",
		"deer":        "deer",
		"series":      "series",
		"species":     "species",
		"money":       "money",
		"news":        "news",
		"equipment":   "equipment",
		"information": "information",
		"rice":        "rice",
		"aircraft":    "aircraft",
		"staff":       "staff",
	}
	if p, ok := irregulars[s]; ok {
		return p
	}

	switch {
	case strings.HasSuffix(s, "quiz"):
		return s + "zes"
	case strings.HasSuffix(s, "ss") || strings.HasSuffix(s, "ch") ||
		strings.HasSuffix(s, "sh") || strings.HasSuffix(s, "x") ||
		strings.HasSuffix(s, "z"):
		return s + "es"
	case strings.HasSuffix(s, "fe"):
		return s[:len(s)-2] + "ves"
	case strings.HasSuffix(s, "y") && len(s) > 1 && !isVowel(rune(s[len(s)-2])):
		return s[:len(s)-1] + "ies"
	case strings.HasSuffix(s, "s"):
		// Words already ending in 's' need 'es': status → statuses, bus → buses
		return s + "es"
	default:
		return s + "s"
	}
}

func isVowel(r rune) bool {
	return strings.ContainsRune("aeiou", r)
}
