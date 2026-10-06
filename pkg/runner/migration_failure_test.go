package runner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// prov-2026-3168d841: suite setup ran migrations for every service, logged a
// failure at Debug and carried on, and ran non-MySQL migrations before any
// database for them existed.
//
// There is no Docker-free seam for this: TestSuite holds a concrete
// *docker.DockerOrchestrator and the migration loop is inline in
// SetupSharedInfrastructure. Until the fix introduces one, these tests inspect
// the setup function's structure with go/parser. They are deliberately the
// smallest check that fails for the right reason, and are language-specific by
// necessity. Once the migration step is extracted behind an injectable runner,
// replace them with a behavioural test (fake runner returns an error; assert
// the setup error carries service name and output; assert PostgreSQL, MongoDB
// and Oracle migrations run only after their database is started).

func setupFunc(t *testing.T) (*token.FileSet, *ast.FuncDecl) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "runner.go", nil, 0)
	if err != nil {
		t.Fatalf("parse runner.go: %v", err)
	}
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "SetupSharedInfrastructure" {
			return fset, fn
		}
	}
	t.Fatal("SetupSharedInfrastructure not found in runner.go")
	return nil, nil
}

func callsMigrations(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "runMigrationsForConfig" {
				found = true
			}
		}
		return !found
	})
	return found
}

// A migration that exits non-zero must fail setup, not be logged and skipped.
func TestMigrationFailureSurfaced_ErrorIsPropagated(t *testing.T) {
	_, fn := setupFunc(t)
	checked := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || ifs.Init == nil || !callsMigrations(ifs.Init) {
			return true
		}
		checked = true
		returns := false
		ast.Inspect(ifs.Body, func(n ast.Node) bool {
			if _, ok := n.(*ast.ReturnStmt); ok {
				returns = true
			}
			return true
		})
		if !returns {
			t.Error("bug: the error from runMigrationsForConfig is not returned from SetupSharedInfrastructure; a failed migration is logged (at Debug) and the run continues on an empty schema")
		}
		return true
	})
	if !checked {
		t.Fatal("no `if err := ...runMigrationsForConfig(...); err != nil` found in SetupSharedInfrastructure; update this test to the new shape of the migration step")
	}
}

// Non-MySQL databases are started per spec, after suite setup, so their
// migrations cannot run in the suite-setup loop unconditionally. Acceptable
// fixes: the loop filters to MySQL, or setup starts the other databases first.
func TestMigrationFailureSurfaced_NonMySQLMigrationsNeedARunningDatabase(t *testing.T) {
	fset, fn := setupFunc(t)
	var loop *ast.RangeStmt
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if r, ok := n.(*ast.RangeStmt); ok && callsMigrations(r.Body) {
			loop = r
		}
		return true
	})
	if loop == nil {
		return // migrations no longer run from suite setup at all; nothing to order
	}

	filtersToMySQL := false
	ast.Inspect(loop.Body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && strings.EqualFold(strings.Trim(lit.Value, `"`), "mysql") {
			filtersToMySQL = true
		}
		return true
	})

	startsOthersFirst := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || fset.Position(call.Pos()).Offset >= fset.Position(loop.Pos()).Offset {
			return true
		}
		name := ""
		switch f := call.Fun.(type) {
		case *ast.SelectorExpr:
			name = f.Sel.Name
		case *ast.Ident:
			name = f.Name
		}
		lower := strings.ToLower(name)
		if strings.Contains(lower, "postgres") || strings.Contains(lower, "mongo") || strings.Contains(lower, "oracle") {
			startsOthersFirst = true
		}
		return true
	})

	if !filtersToMySQL && !startsOthersFirst {
		t.Error("bug: SetupSharedInfrastructure runs migrations for every service, including PostgreSQL/MongoDB/Oracle ones, before any database for them is started (only the shared MySQL exists at this point)")
	}
}
