package verify

import (
	"strings"
	"testing"

	"github.com/livecodelife/linespec/pkg/types"
)

func TestVerifySQL_Contains(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		pattern   string
		wantError bool
	}{
		{
			name:      "matches substring",
			query:     "SELECT id, name, email FROM users WHERE id = 1",
			pattern:   "users",
			wantError: false,
		},
		{
			name:      "does not match substring",
			query:     "SELECT id, name FROM todos WHERE id = 1",
			pattern:   "users",
			wantError: true,
		},
		{
			name:      "case sensitive by default",
			query:     "SELECT * FROM Users",
			pattern:   "users",
			wantError: true,
		},
		{
			name:      "matches complex pattern",
			query:     "INSERT INTO users (name, email, password_digest) VALUES ('test', 'test@example.com', 'abc123')",
			pattern:   "password_digest",
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules := []types.VerifyRule{
				{Type: "CONTAINS", Pattern: tt.pattern},
			}
			err := VerifySQL(tt.query, rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifySQL() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifySQL() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifySQL_NotContains(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		pattern   string
		wantError bool
	}{
		{
			name:      "pattern not in query - passes",
			query:     "SELECT id, name FROM users WHERE id = 1",
			pattern:   "password",
			wantError: false,
		},
		{
			name:      "pattern in query - fails",
			query:     "SELECT password FROM users WHERE id = 1",
			pattern:   "password",
			wantError: true,
		},
		{
			name:      "forbidden table not used - passes",
			query:     "SELECT * FROM todos",
			pattern:   "DELETE FROM users",
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules := []types.VerifyRule{
				{Type: "NOT_CONTAINS", Pattern: tt.pattern},
			}
			err := VerifySQL(tt.query, rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifySQL() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifySQL() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifySQL_Matches(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		pattern   string
		wantError bool
	}{
		{
			name:      "regex matches",
			query:     "SELECT * FROM users WHERE email = 'test@example.com'",
			pattern:   `email\s*=\s*['"]`,
			wantError: false,
		},
		{
			name:      "regex does not match",
			query:     "SELECT * FROM users WHERE id = 1",
			pattern:   `email\s*=\s*['"]`,
			wantError: true,
		},
		{
			name:      "complex regex for INSERT pattern",
			query:     "INSERT INTO users (name, email) VALUES ('John', 'john@example.com')",
			pattern:   `INSERT\s+INTO\s+\w+\s*\(`,
			wantError: false,
		},
		{
			name:      "invalid regex pattern",
			query:     "SELECT * FROM users",
			pattern:   `[invalid(`,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules := []types.VerifyRule{
				{Type: "MATCHES", Pattern: tt.pattern},
			}
			err := VerifySQL(tt.query, rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifySQL() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifySQL() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifySQL_MultipleRules(t *testing.T) {
	query := "INSERT INTO users (name, email, password_digest) VALUES ('test', 'test@example.com', 'hash123')"

	tests := []struct {
		name      string
		rules     []types.VerifyRule
		wantError bool
	}{
		{
			name: "all rules pass",
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Pattern: "INSERT"},
				{Type: "CONTAINS", Pattern: "password_digest"},
				{Type: "NOT_CONTAINS", Pattern: "plain_password"},
			},
			wantError: false,
		},
		{
			name: "second rule fails",
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Pattern: "INSERT"},
				{Type: "CONTAINS", Pattern: "nonexistent_column"},
			},
			wantError: true,
		},
		{
			name: "third rule fails",
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Pattern: "INSERT"},
				{Type: "NOT_CONTAINS", Pattern: "password_digest"},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifySQL(query, tt.rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifySQL() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifySQL() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifySQL_EmptyRules(t *testing.T) {
	query := "SELECT * FROM users"
	rules := []types.VerifyRule{}

	err := VerifySQL(query, rules)
	if err != nil {
		t.Errorf("VerifySQL() with empty rules should not error, got: %v", err)
	}
}

func TestVerifySQL_UnknownRuleType(t *testing.T) {
	query := "SELECT * FROM users"
	rules := []types.VerifyRule{
		{Type: "UNKNOWN_OPERATOR", Pattern: "test"},
	}

	err := VerifySQL(query, rules)
	if err == nil {
		t.Errorf("VerifySQL() with unknown rule type should error")
	}
}

func TestVerificationError(t *testing.T) {
	tests := []struct {
		name     string
		err      *VerificationError
		contains string
	}{
		{
			name: "CONTAINS error message",
			err: &VerificationError{
				Target: "query",
				Rule:   types.VerifyRule{Type: "CONTAINS", Pattern: "users"},
				Actual: "SELECT * FROM todos",
			},
			contains: "CONTAINS",
		},
		{
			name: "NOT_CONTAINS error message",
			err: &VerificationError{
				Target: "query",
				Rule:   types.VerifyRule{Type: "NOT_CONTAINS", Pattern: "password"},
				Actual: "SELECT password FROM users",
			},
			contains: "NOT_CONTAINS",
		},
		{
			name: "MATCHES error message",
			err: &VerificationError{
				Target: "query",
				Rule:   types.VerifyRule{Type: "MATCHES", Pattern: `email\s*=`},
				Actual: "SELECT * FROM users WHERE id = 1",
			},
			contains: "MATCHES",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			if !strings.Contains(msg, tt.contains) {
				t.Errorf("Error() = %v, want to contain %v", msg, tt.contains)
			}
		})
	}
}
