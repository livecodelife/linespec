// Package sqlanalysis reads out of a SQL statement the four things semantic
// matching compares against: the operation, the tables touched, the columns and
// values constrained in the WHERE clause, and the values an INSERT or UPDATE
// writes.
//
// It is deliberately regex-based rather than a parser. What the EXPECT clauses
// assert is shallow - which table, which operation, which columns - and a full
// grammar per dialect would be a large dependency to answer a small question.
// The cost of that choice is that exotic SQL reads as no SQL rather than as
// wrong SQL, which fails a spec loudly instead of matching the wrong mock.
//
// Every proxy that speaks a SQL protocol uses this package. Before it existed
// the MySQL and PostgreSQL proxies each carried their own copy and the two had
// drifted: one captured a dotted table prefix and resolved binds, the other
// could not and did not. Differences that are genuinely about the database live
// on Dialect, where they are named; anything else is one behaviour.
package sqlanalysis

import (
	"regexp"
	"strconv"
	"strings"
)

// PresentSentinel is the value reported for a column constrained by a bind
// whose value is not available. VERIFY_WHERE compares against it when a spec
// cares that a column was constrained but not what it was constrained to.
const PresentSentinel = "PRESENT"

// BindStyle is how a dialect spells a placeholder in a statement.
type BindStyle int

const (
	// BindPositional is PostgreSQL's $1, $2 - resolvable against an ordered
	// list of parameter values.
	BindPositional BindStyle = iota
	// BindAnonymous is MySQL's ?, which carries no identity, so a value can
	// only be resolved by counting placeholders.
	BindAnonymous
	// BindNamed is Oracle's :Name, resolvable against a map.
	BindNamed
)

// WhereScope is how much of a statement is scanned for WHERE conditions.
type WhereScope int

const (
	// ScopeWholeStatement scans everywhere, so a condition inside a subquery
	// or a JOIN ... ON is reported alongside the outer WHERE. Correct for a
	// dialect whose pagination nests the real query inside a wrapper.
	ScopeWholeStatement WhereScope = iota
	// ScopeAfterWhere scans only the text following WHERE, stopping at the
	// first ORDER/LIMIT/GROUP/HAVING.
	ScopeAfterWhere
)

// Dialect names the differences between databases that this package has to know
// about. Nothing else belongs here: a difference two proxies happen to have is
// drift to resolve, not a dialect.
type Dialect struct {
	Name string

	// Bind is how a placeholder is spelled.
	Bind BindStyle

	// Quote holds the characters that may wrap an identifier, if any.
	Quote string

	// SchemaQualified is whether a table may be written schema.table, which
	// changes what an INSERT INTO target looks like and how a table reference
	// is recognised.
	SchemaQualified bool

	// BackslashEscapes is whether a backslash inside a string literal escapes
	// the next character (MySQL's \'), as opposed to only a doubled quote.
	BackslashEscapes bool

	// Where is how much of the statement is scanned for conditions.
	Where WhereScope

	// DedupeColumns reports each constrained column once even when it appears
	// in several conditions.
	DedupeColumns bool
}

var (
	PostgreSQL = Dialect{
		Name:            "postgresql",
		Bind:            BindPositional,
		Quote:           `"`,
		SchemaQualified: true,
		Where:           ScopeWholeStatement,
		DedupeColumns:   true,
	}

	MySQL = Dialect{
		Name:             "mysql",
		Bind:             BindAnonymous,
		Quote:            "`",
		SchemaQualified:  false,
		BackslashEscapes: true,
		Where:            ScopeAfterWhere,
		DedupeColumns:    false,
	}

	Oracle = Dialect{
		Name: "oracle",
		Bind: BindNamed,
		// Oracle folds unquoted identifiers to upper case and double-quotes
		// the rest; the patterns are case-insensitive either way.
		Quote:           `"`,
		SchemaQualified: true,
		// Oracle paginates by wrapping the real query in a ROW_NUMBER()
		// subquery, so the conditions that matter are never in the outermost
		// WHERE.
		Where:         ScopeWholeStatement,
		DedupeColumns: true,
	}
)

// Binds carries whatever parameter values the proxy managed to recover. Both
// fields may be empty: a dialect that cannot recover values still reports which
// columns were constrained, using PresentSentinel for the value.
type Binds struct {
	// Positional holds $1/? values in order.
	Positional []string
	// Named holds :Name values by name, without the leading colon.
	Named map[string]string
}

// Result is what a statement says about itself.
type Result struct {
	Operation     string
	WhereColumns  []string
	WhereValues   map[string]string
	WrittenValues map[string]string
}

var reOperation = regexp.MustCompile(`(?is)^\s*(SELECT|WITH|INSERT|UPDATE|DELETE)\b`)

// Operation returns the DML verb a statement performs, or "" if it performs
// none. WITH reports SELECT: a common table expression is a read however much
// machinery precedes it.
func Operation(query string) string {
	m := reOperation.FindStringSubmatch(query)
	if m == nil {
		return ""
	}
	op := strings.ToUpper(m[1])
	if op == "WITH" {
		return "SELECT"
	}
	return op
}

// Analyze reads a statement under a dialect.
func Analyze(d Dialect, query string, binds Binds) Result {
	op := Operation(query)
	cols, vals := whereInfo(d, query, binds)
	return Result{
		Operation:     op,
		WhereColumns:  cols,
		WhereValues:   vals,
		WrittenValues: writtenValues(d, query, op, binds),
	}
}

// ── Patterns, built per dialect ──────────────────────────────────────────────

// patterns are derived from a Dialect once and cached, because a proxy analyses
// every statement on the wire and recompiling per call is the kind of cost that
// only shows up under load.
type patterns struct {
	whereCondition *regexp.Regexp
	insertCols     *regexp.Regexp
	valuesKeyword  *regexp.Regexp
	updateSet      *regexp.Regexp
	setItem        *regexp.Regexp
	tableRef       func(table string) *regexp.Regexp
}

var patternCache = map[string]*patterns{}

func forDialect(d Dialect) *patterns {
	if p, ok := patternCache[d.Name]; ok {
		return p
	}
	p := buildPatterns(d)
	patternCache[d.Name] = p
	return p
}

// quoted returns an optional-quote fragment for the dialect's identifier
// quoting, e.g. "`?" for MySQL.
func (d Dialect) quoted() string {
	if d.Quote == "" {
		return ""
	}
	return regexp.QuoteMeta(d.Quote) + "?"
}

// placeholder returns the alternation branch matching this dialect's binds,
// with the bind's identity captured where it has one.
func (d Dialect) placeholder() string {
	switch d.Bind {
	case BindPositional:
		return `\$(\d+)`
	case BindAnonymous:
		return `(\?)`
	case BindNamed:
		return `:([a-z_][a-z0-9_]*)`
	}
	return `(\?)`
}

func buildPatterns(d Dialect) *patterns {
	q := d.quoted()
	// An identifier, optionally quoted, optionally prefixed by a qualifier
	// (table or alias) that is stripped by the caller.
	ident := q + `((?:[a-z_][a-z0-9_]*` + q + `\.` + q + `)?[a-z_][a-z0-9_]*)` + q
	// Anything a column can be compared to: a bind, a quoted literal, a number.
	value := `(?:` + d.placeholder() + `|'([^']*)'|(\d+(?:\.\d+)?))`

	// A table target, schema-qualified where the dialect allows it. Only the
	// table half is captured.
	target := q + `(?:[a-z_][a-z0-9_]*` + q + `\.` + q + `)?([a-z_][a-z0-9_]*)` + q
	if !d.SchemaQualified {
		target = q + `([a-z_][a-z0-9_]*)` + q
	}

	return &patterns{
		whereCondition: regexp.MustCompile(`(?i)\b(?:WHERE|AND|OR)\s+` + ident + `\s*=\s*` + value),
		insertCols:     regexp.MustCompile(`(?i)INSERT\s+(?:INTO\s+)?` + target + `\s*\(([^)]+)\)`),
		valuesKeyword:  regexp.MustCompile(`(?i)\bVALUES?\s*\(`),
		updateSet:      regexp.MustCompile(`(?i)\bSET\s+(.+?)(?:\s+WHERE\b|$)`),
		setItem:        regexp.MustCompile(`(?i)` + ident + `\s*=\s*` + value),
		tableRef: func(table string) *regexp.Regexp {
			return regexp.MustCompile(`(?i)(?:^|[^a-z0-9_])` + regexp.QuoteMeta(table) + `(?:[^a-z0-9_]|$)`)
		},
	}
}

// ── WHERE ────────────────────────────────────────────────────────────────────

var reTrailingClause = regexp.MustCompile(`(?i)\s(ORDER|LIMIT|GROUP|HAVING)\s`)

func whereInfo(d Dialect, query string, binds Binds) ([]string, map[string]string) {
	scope := query
	if d.Where == ScopeAfterWhere {
		idx := strings.Index(strings.ToUpper(query), " WHERE ")
		if idx == -1 {
			return nil, nil
		}
		scope = query[idx+len(" WHERE "):]
		if m := reTrailingClause.FindStringIndex(scope); m != nil {
			scope = scope[:m[0]]
		}
		// The scope no longer contains the WHERE keyword the pattern anchors
		// on, so it is reintroduced rather than the pattern being relaxed -
		// relaxing it would let any equality anywhere read as a condition.
		scope = "WHERE " + scope
	}

	p := forDialect(d)
	matches := p.whereCondition.FindAllStringSubmatch(scope, -1)
	if len(matches) == 0 {
		return nil, nil
	}

	values := make(map[string]string, len(matches))
	var columns []string
	seen := make(map[string]struct{}, len(matches))
	positional := 0

	for _, m := range matches {
		col := unqualify(m[1], d.Quote)
		if _, dup := seen[col]; !dup || !d.DedupeColumns {
			columns = append(columns, col)
		}
		seen[col] = struct{}{}
		values[col] = resolve(d, binds, m[2], m[3], m[4], &positional)
	}
	return columns, values
}

// ── INSERT and UPDATE ────────────────────────────────────────────────────────

func writtenValues(d Dialect, query, operation string, binds Binds) map[string]string {
	result := map[string]string{}
	p := forDialect(d)
	positional := 0

	switch operation {
	case "INSERT":
		cm := p.insertCols.FindStringSubmatch(query)
		vals, ok := firstValuesGroup(d, p, query)
		if cm == nil || !ok {
			return result
		}
		// cm[1] is the table; the column list is the last group.
		cols := splitColumns(cm[len(cm)-1], d.Quote)
		for i, col := range cols {
			if i >= len(vals) {
				break
			}
			result[col] = resolveLiteral(d, binds, vals[i], &positional)
		}
	case "UPDATE":
		sm := p.updateSet.FindStringSubmatch(query)
		if sm == nil {
			return result
		}
		for _, item := range p.setItem.FindAllStringSubmatch(sm[1], -1) {
			col := unqualify(item[1], d.Quote)
			result[col] = resolve(d, binds, item[2], item[3], item[4], &positional)
		}
	}
	return result
}

// ── Tables ───────────────────────────────────────────────────────────────────

// Tables returns which of the known table names a statement references. It is
// a containment test rather than a parse: the proxy already knows every table
// in the schema, so the question is which of them appear, and a name appearing
// only inside a longer identifier does not count.
func Tables(d Dialect, query string, known []string) []string {
	p := forDialect(d)
	var found []string
	for _, table := range known {
		if p.tableRef(table).MatchString(query) {
			found = append(found, table)
		}
	}
	return found
}

// ── Value resolution ─────────────────────────────────────────────────────────

// resolve turns the three value branches of a condition - bind, quoted literal,
// number - into the value to report.
func resolve(d Dialect, binds Binds, bind, literal, number string, positional *int) string {
	switch {
	case bind != "":
		return resolveBind(d, binds, bind, positional)
	case literal != "":
		return literal
	case number != "":
		return number
	}
	// An empty quoted literal reaches here, and an empty string is what it is.
	return literal
}

// resolveLiteral handles a value taken from a VALUES list, where the branches
// have not already been separated by a capture group.
func resolveLiteral(d Dialect, binds Binds, raw string, positional *int) string {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, "'") && strings.HasSuffix(raw, "'") && len(raw) >= 2:
		return raw[1 : len(raw)-1]
	case raw == "?":
		return resolveBind(d, binds, "?", positional)
	case strings.HasPrefix(raw, "$"):
		return resolveBind(d, binds, raw[1:], positional)
	case strings.HasPrefix(raw, ":"):
		return resolveBind(d, binds, raw[1:], positional)
	}
	return raw
}

func resolveBind(d Dialect, binds Binds, token string, positional *int) string {
	switch d.Bind {
	case BindPositional:
		if idx, err := strconv.Atoi(token); err == nil && idx >= 1 && idx <= len(binds.Positional) {
			return binds.Positional[idx-1]
		}
	case BindAnonymous:
		i := *positional
		*positional++
		if i < len(binds.Positional) {
			return binds.Positional[i]
		}
	case BindNamed:
		if v, ok := binds.Named[token]; ok {
			return v
		}
	}
	return PresentSentinel
}

// ── Identifiers ──────────────────────────────────────────────────────────────

// unqualify lowercases an identifier and drops any table or alias prefix, so
// that a spec naming a column matches whether or not the statement qualified it.
func unqualify(raw, quote string) string {
	s := strings.ToLower(raw)
	if quote != "" {
		s = strings.ReplaceAll(s, quote, "")
	}
	if dot := strings.LastIndex(s, "."); dot >= 0 {
		s = s[dot+1:]
	}
	return strings.TrimSpace(s)
}

// splitColumns splits an INSERT column list. Column names are folded to lower
// case so a spec naming a column matches whatever case the statement used. A
// comma inside a quoted identifier does not separate columns.
func splitColumns(raw, quote string) []string {
	parts := splitTopLevel(raw, quote, false, false)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if quote != "" {
			p = strings.ReplaceAll(p, quote, "")
		}
		out = append(out, strings.ToLower(p))
	}
	return out
}

// firstValuesGroup returns the split items of the first VALUES (...) group in a
// statement. The group ends at the parenthesis matching its opening one, so a
// ')' inside a quoted literal or a nested call does not end it. Later rows of a
// multi-row INSERT are ignored.
func firstValuesGroup(d Dialect, p *patterns, query string) ([]string, bool) {
	loc := p.valuesKeyword.FindStringIndex(query)
	if loc == nil {
		return nil, false
	}
	start := loc[1] // just past the opening '('
	depth := 1
	var quoted bool
	for i := start; i < len(query); i++ {
		switch c := query[i]; {
		case quoted && c == '\\' && d.BackslashEscapes:
			i++
		case c == '\'':
			quoted = !quoted
		case quoted:
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				return splitValues(query[start:i], d), true
			}
		}
	}
	return nil, false
}

// splitValues splits a VALUES list on top-level commas. Values are NOT folded:
// a literal is data and belongs to the caller exactly as written, and a named
// bind is looked up by a name that is case-sensitive.
func splitValues(raw string, d Dialect) []string {
	return splitTopLevel(raw, "'", d.BackslashEscapes, true)
}

// splitTopLevel splits raw on commas that sit outside any run wrapped in one of
// the quote characters and, when parens is set, outside nested parentheses. A
// doubled quote re-enters the run it just left, so it needs no special case; a
// backslash additionally escapes the next character when backslash is set.
// Each item is trimmed.
func splitTopLevel(raw, quotes string, backslash, parens bool) []string {
	var out []string
	var open rune // the quote character currently open, if any
	depth, start := 0, 0
	for i := 0; i < len(raw); i++ {
		c := rune(raw[i])
		switch {
		case open != 0:
			if c == '\\' && backslash && open == '\'' {
				i++
			} else if c == open {
				open = 0
			}
		case strings.ContainsRune(quotes, c):
			open = c
		case parens && c == '(':
			depth++
		case parens && c == ')':
			depth--
		case c == ',' && depth == 0:
			out = append(out, strings.TrimSpace(raw[start:i]))
			start = i + 1
		}
	}
	return append(out, strings.TrimSpace(raw[start:]))
}
