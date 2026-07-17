package verify

import (
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/types"
)

func TestVerifyGRPC_RequestBody(t *testing.T) {
	tests := []struct {
		name      string
		req       *GRPCRequest
		rules     []types.VerifyRule
		wantError bool
	}{
		{
			name: "request_body contains - passes",
			req: &GRPCRequest{
				Service: "users.UserService",
				Method:  "GetUser",
				Body:    `{"user_id": "123"}`,
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "request_body", Pattern: "user_id"},
			},
			wantError: false,
		},
		{
			name: "request_body contains - fails",
			req: &GRPCRequest{
				Service: "users.UserService",
				Method:  "GetUser",
				Body:    `{"user_id": "123"}`,
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "request_body", Pattern: "order_id"},
			},
			wantError: true,
		},
		{
			name: "request_body not_contains - passes",
			req: &GRPCRequest{
				Body: `{"user_id": "123"}`,
			},
			rules: []types.VerifyRule{
				{Type: "NOT_CONTAINS", Target: "request_body", Pattern: "password"},
			},
			wantError: false,
		},
		{
			name: "request_body matches - passes",
			req: &GRPCRequest{
				Body: `{"user_id": "user-123"}`,
			},
			rules: []types.VerifyRule{
				{Type: "MATCHES", Target: "request_body", Pattern: `"user_id":\s*"user-\d+"`},
			},
			wantError: false,
		},
		{
			name: "request_body matches - fails",
			req: &GRPCRequest{
				Body: `{"user_id": "abc"}`,
			},
			rules: []types.VerifyRule{
				{Type: "MATCHES", Target: "request_body", Pattern: `"user_id":\s*"user-\d+"`},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyGRPC(tt.req, tt.rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifyGRPC() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifyGRPC() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifyGRPC_Metadata(t *testing.T) {
	tests := []struct {
		name      string
		req       *GRPCRequest
		rules     []types.VerifyRule
		wantError bool
	}{
		{
			name: "metadata contains - passes",
			req: &GRPCRequest{
				Body: `{}`,
				Metadata: map[string]string{
					"authorization": "Bearer token123",
					"x-request-id": "req-abc",
				},
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "metadata.authorization", Pattern: "Bearer"},
			},
			wantError: false,
		},
		{
			name: "metadata contains - fails",
			req: &GRPCRequest{
				Metadata: map[string]string{
					"authorization": "Basic abc123",
				},
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "metadata.authorization", Pattern: "Bearer"},
			},
			wantError: true,
		},
		{
			name: "metadata case-insensitive lookup - passes",
			req: &GRPCRequest{
				Metadata: map[string]string{
					"Authorization": "Bearer token123",
				},
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "metadata.authorization", Pattern: "Bearer"},
			},
			wantError: false,
		},
		{
			name: "missing metadata not_contains - passes",
			req: &GRPCRequest{
				Metadata: map[string]string{},
			},
			rules: []types.VerifyRule{
				{Type: "NOT_CONTAINS", Target: "metadata.x-admin", Pattern: "true"},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyGRPC(tt.req, tt.rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifyGRPC() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifyGRPC() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifyGRPC_ServiceMethod(t *testing.T) {
	req := &GRPCRequest{
		Service: "users.UserService",
		Method:  "GetUser",
		Body:    `{"user_id": "123"}`,
	}

	tests := []struct {
		name      string
		rules     []types.VerifyRule
		wantError bool
	}{
		{
			name: "service contains - passes",
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "service", Pattern: "UserService"},
			},
			wantError: false,
		},
		{
			name: "method contains - passes",
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "method", Pattern: "GetUser"},
			},
			wantError: false,
		},
		{
			name: "unknown target returns error",
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "unknown_target", Pattern: "foo"},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyGRPC(req, tt.rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifyGRPC() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifyGRPC() unexpected error: %v", err)
			}
		})
	}
}

func TestExtractVerifyRulesForTarget_GRPC(t *testing.T) {
	rules := []types.VerifyRule{
		{Type: "CONTAINS", Target: "query", Pattern: "SELECT"},
		{Type: "CONTAINS", Target: "request_body", Pattern: "user_id"},
		{Type: "CONTAINS", Target: "metadata.authorization", Pattern: "Bearer"},
		{Type: "CONTAINS", Target: "service", Pattern: "UserService"},
		{Type: "CONTAINS", Target: "method", Pattern: "GetUser"},
		{Type: "CONTAINS", Target: "key", Pattern: "user"},
		{Type: "CONTAINS", Target: "value", Pattern: "event"},
	}

	grpcRules := ExtractVerifyRulesForTarget(rules, "grpc")
	if len(grpcRules) != 4 {
		t.Errorf("Expected 4 gRPC rules (request_body, metadata.authorization, service, method), got %d", len(grpcRules))
	}
}
