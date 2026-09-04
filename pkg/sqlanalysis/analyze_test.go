package sqlanalysis

import (
	"reflect"
	"testing"
)

// The MySQL and PostgreSQL cases below are the behaviour those two proxies had
// before this package existed, asserted here so that consolidating them is a
// refactor rather than a rewrite. Where the two disagreed, the disagreement is
// now a named Dialect field instead of a difference between two copies of the
// same function - and both sides of it are pinned.

func TestOperation(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{"SELECT * FROM users", "SELECT"},
		{"  select 1", "SELECT"},
		{"WITH t AS (SELECT 1) SELECT * FROM t", "SELECT"},
		{"INSERT INTO users (a) VALUES (1)", "INSERT"},
		{"UPDATE users SET a = 1", "UPDATE"},
		{"DELETE FROM users WHERE id = 1", "DELETE"},
		{"COMMIT", ""},
	} {
		if got := Operation(tc.query); got != tc.want {
			t.Errorf("Operation(%q) = %q, want %q", tc.query, got, tc.want)
		}
	}
}

// ── PostgreSQL: the behaviour that existed before ────────────────────────────

func TestPostgreSQLResolvesPositionalBindsAndStripsTablePrefix(t *testing.T) {
	got := Analyze(PostgreSQL,
		`SELECT * FROM notifications WHERE notifications.recipient = $1 AND status = 'sent'`,
		Binds{Positional: []string{"someone@example.com"}})

	wantCols := []string{"recipient", "status"}
	if !reflect.DeepEqual(got.WhereColumns, wantCols) {
		t.Errorf("WhereColumns = %v, want %v", got.WhereColumns, wantCols)
	}
	if got.WhereValues["recipient"] != "someone@example.com" {
		t.Errorf("recipient = %q, want the resolved bind", got.WhereValues["recipient"])
	}
	if got.WhereValues["status"] != "sent" {
		t.Errorf("status = %q, want sent", got.WhereValues["status"])
	}
}

func TestPostgreSQLDeduplicatesRepeatedWhereColumns(t *testing.T) {
	got := Analyze(PostgreSQL, `SELECT * FROM t WHERE id = 1 AND id = 2`, Binds{})
	if len(got.WhereColumns) != 1 {
		t.Errorf("WhereColumns = %v, want one entry", got.WhereColumns)
	}
}

func TestPostgreSQLWrittenValues(t *testing.T) {
	ins := Analyze(PostgreSQL,
		`INSERT INTO users (email, name) VALUES ($1, 'Ada')`,
		Binds{Positional: []string{"ada@example.com"}})
	if ins.WrittenValues["email"] != "ada@example.com" || ins.WrittenValues["name"] != "Ada" {
		t.Errorf("INSERT written = %v", ins.WrittenValues)
	}

	upd := Analyze(PostgreSQL,
		`UPDATE users SET name = 'Grace' WHERE id = $1`,
		Binds{Positional: []string{"7"}})
	if upd.WrittenValues["name"] != "Grace" {
		t.Errorf("UPDATE written = %v", upd.WrittenValues)
	}
	// The WHERE column must not leak into the written set.
	if _, ok := upd.WrittenValues["id"]; ok {
		t.Errorf("UPDATE written contains the WHERE column: %v", upd.WrittenValues)
	}
}

// ── MySQL: the behaviour that existed before ─────────────────────────────────

func TestMySQLToleratesBacktickQuoting(t *testing.T) {
	got := Analyze(MySQL,
		"SELECT * FROM `users` WHERE `users`.`token` = 'abc'", Binds{})
	if len(got.WhereColumns) != 1 || got.WhereColumns[0] != "token" {
		t.Errorf("WhereColumns = %v, want [token]", got.WhereColumns)
	}
	if got.WhereValues["token"] != "abc" {
		t.Errorf("token = %q, want abc", got.WhereValues["token"])
	}
}

// MySQL analysis never receives bind values - the proxy calls it with the query
// alone - so an anonymous placeholder resolves to the PRESENT sentinel, which is
// what VERIFY_WHERE compares against when a spec only cares that a column was
// constrained.
func TestMySQLAnonymousBindIsPresent(t *testing.T) {
	got := Analyze(MySQL, "SELECT * FROM users WHERE id = ?", Binds{})
	if got.WhereValues["id"] != "PRESENT" {
		t.Errorf("id = %q, want PRESENT", got.WhereValues["id"])
	}
}

// MySQL scopes the scan to the text after WHERE and stops at the first trailing
// clause, so an ON condition in a JOIN is not reported as a WHERE column.
func TestMySQLScopesToTheWhereClause(t *testing.T) {
	got := Analyze(MySQL,
		"SELECT * FROM a JOIN b ON a.x = b.x WHERE a.id = 5 ORDER BY a.id LIMIT 10",
		Binds{})
	for _, c := range got.WhereColumns {
		if c == "x" {
			t.Fatalf("JOIN ON condition leaked into WhereColumns: %v", got.WhereColumns)
		}
	}
	if len(got.WhereColumns) != 1 || got.WhereColumns[0] != "id" {
		t.Errorf("WhereColumns = %v, want [id]", got.WhereColumns)
	}
}

// ── Oracle: the reason this package exists ───────────────────────────────────

// Named binds are the first of Oracle's two differences. Every positional branch
// misses them, so without this dialect the WHERE columns of an Oracle query come
// back empty and VERIFY_WHERE_COLUMNS silently has nothing to check.
func TestOracleReadsNamedBindsInTheWhereClause(t *testing.T) {
	got := Analyze(Oracle,
		`SELECT e.employee_id AS EmployeeId, e.surname AS Surname `+
			`FROM hr.employees e WHERE e.employee_id = :KeyEmployeeId`,
		Binds{Named: map[string]string{"KeyEmployeeId": "4471"}})

	if len(got.WhereColumns) != 1 || got.WhereColumns[0] != "employee_id" {
		t.Fatalf("WhereColumns = %v, want [employee_id]", got.WhereColumns)
	}
	if got.WhereValues["employee_id"] != "4471" {
		t.Errorf("employee_id = %q, want the resolved named bind", got.WhereValues["employee_id"])
	}
}

// A named bind nobody supplied a value for still reports the column as
// constrained, so a spec asserting only VERIFY_WHERE_COLUMNS works without the
// caller having to know bind values.
func TestOracleUnresolvedNamedBindIsPresent(t *testing.T) {
	got := Analyze(Oracle,
		`DELETE FROM hr.employees e WHERE e.employee_id = :KeyEmployeeId`, Binds{})
	if got.WhereValues["employee_id"] != "PRESENT" {
		t.Errorf("employee_id = %q, want PRESENT", got.WhereValues["employee_id"])
	}
}

// The schema qualifier is the second difference. A pattern that allows only a
// bare table name does not match INSERT INTO hr.employees at all, so
// VERIFY_WRITTEN_VALUES sees nothing.
func TestOracleReadsWrittenValuesFromASchemaQualifiedInsert(t *testing.T) {
	got := Analyze(Oracle,
		`INSERT INTO hr.employees (employee_id, surname, department, active_flag) `+
			`VALUES (:EmployeeId, :Surname, :Department, :ActiveFlag)`,
		Binds{Named: map[string]string{
			"EmployeeId": "4471",
			"Surname":    "Lovelace",
			"Department": "RES",
		}})

	if got.Operation != "INSERT" {
		t.Fatalf("Operation = %q, want INSERT", got.Operation)
	}
	for col, want := range map[string]string{
		"employee_id": "4471",
		"surname":     "Lovelace",
		"department":  "RES",
		"active_flag": "PRESENT",
	} {
		if got.WrittenValues[col] != want {
			t.Errorf("written[%s] = %q, want %q (all: %v)", col, got.WrittenValues[col], want, got.WrittenValues)
		}
	}
}

func TestOracleReadsAnAliasedUpdate(t *testing.T) {
	got := Analyze(Oracle,
		`UPDATE hr.employees e SET surname = :Surname, department = :Department `+
			`WHERE e.employee_id = :KeyEmployeeId`,
		Binds{Named: map[string]string{
			"Surname":       "Lovelace",
			"Department":    "RES",
			"KeyEmployeeId": "4471",
		}})

	if got.Operation != "UPDATE" {
		t.Fatalf("Operation = %q, want UPDATE", got.Operation)
	}
	if got.WrittenValues["surname"] != "Lovelace" || got.WrittenValues["department"] != "RES" {
		t.Errorf("written = %v", got.WrittenValues)
	}
	if got.WhereValues["employee_id"] != "4471" {
		t.Errorf("where employee_id = %q, want the KEY bind", got.WhereValues["employee_id"])
	}
}

// The case this dialect is for. A table keyed on two columns, updated by a
// statement that names only one, rewrites every row sharing that first column. A
// spec asserting VERIFY_WHERE_COLUMNS account_year, fund can only tell the two
// statements apart if both columns are read out of the query.
func TestOracleReportsEveryColumnOfACompositeKey(t *testing.T) {
	scoped := Analyze(Oracle,
		`UPDATE fin.fund_banks fb SET bank_no = :BankNo `+
			`WHERE fb.account_year = :KeyAccountYear AND fb.fund = :KeyFund`,
		Binds{Named: map[string]string{"KeyAccountYear": "2026", "KeyFund": "100"}})

	if len(scoped.WhereColumns) != 2 {
		t.Fatalf("WhereColumns = %v, want both key columns", scoped.WhereColumns)
	}
	if scoped.WhereValues["account_year"] != "2026" || scoped.WhereValues["fund"] != "100" {
		t.Errorf("WhereValues = %v", scoped.WhereValues)
	}

	underScoped := Analyze(Oracle,
		`UPDATE fin.fund_banks fb SET bank_no = :BankNo WHERE fb.account_year = :KeyAccountYear`,
		Binds{Named: map[string]string{"KeyAccountYear": "2026"}})

	if len(underScoped.WhereColumns) != 1 || underScoped.WhereColumns[0] != "account_year" {
		t.Fatalf("WhereColumns = %v, want only [account_year]", underScoped.WhereColumns)
	}
	if _, ok := underScoped.WhereValues["fund"]; ok {
		t.Error("the under-scoped UPDATE reported a fund constraint it does not have")
	}
}

// Oracle pagination wraps the statement in ROW_NUMBER(), and the inner query
// still has to be read through the wrapper.
func TestOracleReadsThroughAPaginationWrapper(t *testing.T) {
	got := Analyze(Oracle,
		`SELECT * FROM (SELECT a.*, ROW_NUMBER() OVER (ORDER BY UPPER(Surname)) rn `+
			`FROM (SELECT e.surname AS Surname FROM hr.employees e `+
			`WHERE e.active_flag = :ActiveFlag) a) WHERE rn BETWEEN :First AND :Last`,
		Binds{Named: map[string]string{"ActiveFlag": "Y"}})

	if got.Operation != "SELECT" {
		t.Fatalf("Operation = %q, want SELECT", got.Operation)
	}
	if got.WhereValues["active_flag"] != "Y" {
		t.Errorf("active_flag = %q, want Y (all: %v)", got.WhereValues["active_flag"], got.WhereValues)
	}
}

// ── Tables ───────────────────────────────────────────────────────────────────

func TestTablesMatchWithAndWithoutASchemaQualifier(t *testing.T) {
	known := []string{"employees", "fund_banks"}

	oracle := Tables(Oracle, `SELECT * FROM hr.employees e`, known)
	if len(oracle) != 1 || oracle[0] != "employees" {
		t.Errorf("Oracle Tables = %v, want [employees]", oracle)
	}

	pg := Tables(PostgreSQL, `SELECT * FROM employees`, known)
	if len(pg) != 1 || pg[0] != "employees" {
		t.Errorf("PostgreSQL Tables = %v, want [employees]", pg)
	}
}

// A table name that only appears as a substring of another identifier is not a
// reference to that table.
func TestTablesDoesNotMatchASubstringOfAnotherIdentifier(t *testing.T) {
	got := Tables(Oracle, `SELECT * FROM fin.fund_banks_archive`, []string{"fund_banks"})
	if len(got) != 0 {
		t.Errorf("Tables = %v, want none", got)
	}
}
