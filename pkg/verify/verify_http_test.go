package verify

import (
	"strings"
	"testing"

	"github.com/livecodelife/linespec/pkg/types"
)

func TestVerifyHTTP_Headers(t *testing.T) {
	tests := []struct {
		name      string
		req       *HTTPRequest
		rules     []types.VerifyRule
		wantError bool
	}{
		{
			name: "header contains - passes",
			req: &HTTPRequest{
				Method: "GET",
				URL:    "http://localhost:3000/users",
				Headers: map[string]string{
					"Authorization": "Bearer token123",
					"Content-Type":  "application/json",
				},
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "headers.Authorization", Pattern: "Bearer"},
			},
			wantError: false,
		},
		{
			name: "header contains - fails",
			req: &HTTPRequest{
				Method: "GET",
				URL:    "http://localhost:3000/users",
				Headers: map[string]string{
					"Authorization": "Basic abc123",
				},
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "headers.Authorization", Pattern: "Bearer"},
			},
			wantError: true,
		},
		{
			name: "header not_contains - passes",
			req: &HTTPRequest{
				Method: "GET",
				URL:    "http://localhost:3000/users",
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
			},
			rules: []types.VerifyRule{
				{Type: "NOT_CONTAINS", Target: "headers.X-Admin", Pattern: "true"},
			},
			wantError: false,
		},
		{
			name: "header matches - passes",
			req: &HTTPRequest{
				Method: "GET",
				URL:    "http://localhost:3000/users",
				Headers: map[string]string{
					"Authorization": "Bearer abc123def456",
				},
			},
			rules: []types.VerifyRule{
				{Type: "MATCHES", Target: "headers.Authorization", Pattern: `^Bearer\s+\w+$`},
			},
			wantError: false,
		},
		{
			name: "header matches - fails",
			req: &HTTPRequest{
				Method: "GET",
				URL:    "http://localhost:3000/users",
				Headers: map[string]string{
					"Authorization": "Basic abc123",
				},
			},
			rules: []types.VerifyRule{
				{Type: "MATCHES", Target: "headers.Authorization", Pattern: `^Bearer\s+\w+$`},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyHTTP(tt.req, tt.rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifyHTTP() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifyHTTP() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifyHTTP_Body(t *testing.T) {
	tests := []struct {
		name      string
		req       *HTTPRequest
		rules     []types.VerifyRule
		wantError bool
	}{
		{
			name: "body contains - passes",
			req: &HTTPRequest{
				Method: "POST",
				URL:    "http://localhost:3000/users",
				Body:   `{"name":"John","email":"john@example.com"}`,
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "body", Pattern: "john@example.com"},
			},
			wantError: false,
		},
		{
			name: "body contains - fails",
			req: &HTTPRequest{
				Method: "POST",
				URL:    "http://localhost:3000/users",
				Body:   `{"name":"John","email":"john@example.com"}`,
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "body", Pattern: "jane@example.com"},
			},
			wantError: true,
		},
		{
			name: "body matches json pattern - passes",
			req: &HTTPRequest{
				Method: "POST",
				URL:    "http://localhost:3000/users",
				Body:   `{"name":"John","email":"john@example.com"}`,
			},
			rules: []types.VerifyRule{
				{Type: "MATCHES", Target: "body", Pattern: `"email":\s*"[^"]+@example\.com"`},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyHTTP(tt.req, tt.rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifyHTTP() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifyHTTP() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifyHTTP_URL(t *testing.T) {
	tests := []struct {
		name      string
		req       *HTTPRequest
		rules     []types.VerifyRule
		wantError bool
	}{
		{
			name: "url contains - passes",
			req: &HTTPRequest{
				Method: "GET",
				URL:    "http://localhost:3000/users/123",
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "url", Pattern: "users/123"},
			},
			wantError: false,
		},
		{
			name: "url matches pattern - passes",
			req: &HTTPRequest{
				Method: "GET",
				URL:    "http://localhost:3000/users/123",
			},
			rules: []types.VerifyRule{
				{Type: "MATCHES", Target: "url", Pattern: `users/\d+$`},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyHTTP(tt.req, tt.rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifyHTTP() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifyHTTP() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifyHTTP_Path(t *testing.T) {
	tests := []struct {
		name      string
		req       *HTTPRequest
		rules     []types.VerifyRule
		wantError bool
	}{
		{
			name: "path contains - passes",
			req: &HTTPRequest{
				Method: "GET",
				Path:   "/users/123",
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "path", Pattern: "users"},
			},
			wantError: false,
		},
		{
			name: "path matches - passes",
			req: &HTTPRequest{
				Method: "GET",
				Path:   "/users/123",
			},
			rules: []types.VerifyRule{
				{Type: "MATCHES", Target: "path", Pattern: `/users/\d+$`},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyHTTP(tt.req, tt.rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifyHTTP() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifyHTTP() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifyHTTP_MultipleRules(t *testing.T) {
	req := &HTTPRequest{
		Method: "POST",
		URL:    "http://localhost:3000/users",
		Path:   "/users",
		Headers: map[string]string{
			"Authorization": "Bearer token123",
			"Content-Type":  "application/json",
		},
		Body: `{"name":"John","email":"john@example.com"}`,
	}

	tests := []struct {
		name      string
		rules     []types.VerifyRule
		wantError bool
	}{
		{
			name: "all rules pass",
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "headers.Authorization", Pattern: "Bearer"},
				{Type: "CONTAINS", Target: "body", Pattern: "john@example.com"},
				{Type: "MATCHES", Target: "path", Pattern: `/users`},
			},
			wantError: false,
		},
		{
			name: "one rule fails",
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "headers.Authorization", Pattern: "Bearer"},
				{Type: "CONTAINS", Target: "body", Pattern: "nonexistent"},
				{Type: "MATCHES", Target: "path", Pattern: `/users`},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyHTTP(req, tt.rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifyHTTP() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifyHTTP() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifyHTTP_CaseInsensitiveHeaders(t *testing.T) {
	req := &HTTPRequest{
		Method: "GET",
		URL:    "http://localhost:3000/users",
		Headers: map[string]string{
			"authorization": "Bearer token123", // lowercase
		},
	}

	rules := []types.VerifyRule{
		{Type: "CONTAINS", Target: "headers.Authorization", Pattern: "Bearer"},
	}

	err := VerifyHTTP(req, rules)
	if err != nil {
		t.Errorf("VerifyHTTP() should be case-insensitive for headers, got error: %v", err)
	}
}

func TestVerifyHTTP_ErrorMessages(t *testing.T) {
	req := &HTTPRequest{
		Method: "GET",
		URL:    "http://localhost:3000/users",
		Headers: map[string]string{
			"Authorization": "Basic abc123",
		},
	}

	rules := []types.VerifyRule{
		{Type: "CONTAINS", Target: "headers.Authorization", Pattern: "Bearer"},
	}

	err := VerifyHTTP(req, rules)
	if err == nil {
		t.Fatal("Expected error")
	}

	if !strings.Contains(err.Error(), "CONTAINS") {
		t.Errorf("Error message should contain 'CONTAINS', got: %v", err.Error())
	}
	if !strings.Contains(err.Error(), "Bearer") {
		t.Errorf("Error message should contain expected pattern, got: %v", err.Error())
	}
	if !strings.Contains(err.Error(), "Basic") {
		t.Errorf("Error message should contain actual value, got: %v", err.Error())
	}
}
