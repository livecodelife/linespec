package runner

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/livecodelife/linespec/v3/pkg/config"
	"gopkg.in/yaml.v3"
)

// closedLocalPort returns a loopback port with nothing listening on it, so a
// database that "never answers" fails every ping with a connection error.
func closedLocalPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	l.Close()
	return port
}

// The timeout error must tell the operator what to do: the timeout used (in
// seconds), the last connection error, and the config key that raises it.
func TestWaitForSQLDBTimeoutErrorIsActionable(t *testing.T) {
	port := closedLocalPort(t)
	s := &TestSuite{}

	err := s.waitForMySQL(context.Background(), "127.0.0.1", port, "u", "p", "db", 2*time.Second)
	if err == nil {
		t.Fatal("expected a timeout error against a database that never answers")
	}
	msg := err.Error()

	if !regexp.MustCompile(`(?i)\b2\s*(s|secs?|seconds?)\b`).MatchString(msg) {
		t.Errorf("error does not name the 2 second timeout: %q", msg)
	}
	// Last ping error: connecting to a closed loopback port is refused.
	if !strings.Contains(strings.ToLower(msg), "refused") {
		t.Errorf("error does not include the last connection error (connection refused): %q", msg)
	}
	if !strings.Contains(msg, "database.ready_timeout_seconds") {
		t.Errorf("error does not name the config key database.ready_timeout_seconds: %q", msg)
	}
}

func TestDBReadyTimeoutConfigurable(t *testing.T) {
	t.Run("parses database.ready_timeout_seconds from YAML", func(t *testing.T) {
		var cfg config.LineSpecConfig
		raw := "database:\n  type: mysql\n  ready_timeout_seconds: 120\n"
		if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Database == nil || cfg.Database.ReadyTimeoutSeconds != 120 {
			t.Fatalf("ready_timeout_seconds not parsed: %+v", cfg.Database)
		}
		if got := cfg.Database.ReadyTimeout(); got != 120*time.Second {
			t.Errorf("ReadyTimeout() = %v, want 120s", got)
		}
	})

	t.Run("default exceeds the observed 42 second MySQL start", func(t *testing.T) {
		var db config.DatabaseConfig
		got := db.ReadyTimeout()
		if got <= 42*time.Second {
			t.Errorf("default ReadyTimeout() = %v, must exceed 42s", got)
		}
		if got != 90*time.Second {
			t.Errorf("default ReadyTimeout() = %v, record specifies 90s", got)
		}
	})

	// The MySQL, PostgreSQL and MongoDB waits must take their timeout from the
	// config, not a literal. Inspect every call site in runner.go.
	t.Run("MySQL, PostgreSQL and MongoDB waits use the setting", func(t *testing.T) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "runner.go", nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		raw, rerr := os.ReadFile("runner.go")
		if rerr != nil {
			t.Fatal(rerr)
		}
		src := string(raw)
		seen := map[string]int{}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "waitForMySQL", "waitForPostgreSQL", "waitForMongoDB":
			default:
				return true
			}
			seen[sel.Sel.Name]++
			last := call.Args[len(call.Args)-1]
			text := src[fset.Position(last.Pos()).Offset:fset.Position(last.End()).Offset]
			if !strings.Contains(text, "ReadyTimeout") {
				t.Errorf("%s call at %s passes %q, not the configured ReadyTimeout",
					sel.Sel.Name, fset.Position(call.Pos()), text)
			}
			return true
		})
		for _, name := range []string{"waitForMySQL", "waitForPostgreSQL", "waitForMongoDB"} {
			if seen[name] == 0 {
				t.Errorf("no call to %s found in runner.go", name)
			}
		}
	})

	// A healthy database returns as soon as it answers, not after the timeout.
	t.Run("ready database returns promptly", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				c.Close()
			}
		}()
		_, port, _ := net.SplitHostPort(l.Addr().String())
		s := &TestSuite{}
		start := time.Now()
		err = s.waitForMongoDB(context.Background(), "127.0.0.1", port, "", "", "db", 30*time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if el := time.Since(start); el > 5*time.Second {
			t.Errorf("ready database took %v, expected prompt return", el)
		}
	})
}
