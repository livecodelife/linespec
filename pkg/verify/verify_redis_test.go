package verify

import (
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/types"
)

func TestVerifyRedis_Command(t *testing.T) {
	tests := []struct {
		name      string
		cmd       *RedisCommand
		rules     []types.VerifyRule
		wantError bool
	}{
		{
			name: "command contains - passes",
			cmd: &RedisCommand{
				Command: "GET",
				Key:     "user:123",
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "command", Pattern: "GET"},
			},
			wantError: false,
		},
		{
			name: "command contains - fails",
			cmd: &RedisCommand{
				Command: "SET",
				Key:     "user:123",
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "command", Pattern: "GET"},
			},
			wantError: true,
		},
		{
			name: "command not_contains - passes",
			cmd: &RedisCommand{
				Command: "GET",
				Key:     "user:123",
			},
			rules: []types.VerifyRule{
				{Type: "NOT_CONTAINS", Target: "command", Pattern: "DEL"},
			},
			wantError: false,
		},
		{
			name: "command matches - passes",
			cmd: &RedisCommand{
				Command: "HGETALL",
				Key:     "user:123",
			},
			rules: []types.VerifyRule{
				{Type: "MATCHES", Target: "command", Pattern: `^H(GET|SET)`},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyRedis(tt.cmd, tt.rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifyRedis() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifyRedis() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifyRedis_Key(t *testing.T) {
	tests := []struct {
		name      string
		cmd       *RedisCommand
		rules     []types.VerifyRule
		wantError bool
	}{
		{
			name: "key contains - passes",
			cmd: &RedisCommand{
				Command: "GET",
				Key:     "user:123",
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "key", Pattern: "user:"},
			},
			wantError: false,
		},
		{
			name: "key contains - fails",
			cmd: &RedisCommand{
				Command: "GET",
				Key:     "session:abc",
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "key", Pattern: "user:"},
			},
			wantError: true,
		},
		{
			name: "key matches - passes",
			cmd: &RedisCommand{
				Command: "GET",
				Key:     "user:12345",
			},
			rules: []types.VerifyRule{
				{Type: "MATCHES", Target: "key", Pattern: `^user:\d+$`},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyRedis(tt.cmd, tt.rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifyRedis() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifyRedis() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifyRedis_Value(t *testing.T) {
	tests := []struct {
		name      string
		cmd       *RedisCommand
		rules     []types.VerifyRule
		wantError bool
	}{
		{
			name: "value contains - passes",
			cmd: &RedisCommand{
				Command: "SET",
				Key:     "user:123",
				Value:   `{"name":"Alice","active":true}`,
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "value", Pattern: "active"},
			},
			wantError: false,
		},
		{
			name: "value not_contains - passes",
			cmd: &RedisCommand{
				Command: "SET",
				Key:     "user:123",
				Value:   `{"name":"Alice"}`,
			},
			rules: []types.VerifyRule{
				{Type: "NOT_CONTAINS", Target: "value", Pattern: "password"},
			},
			wantError: false,
		},
		{
			name: "unknown target returns error",
			cmd: &RedisCommand{
				Command: "GET",
				Key:     "user:123",
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "unknown_target", Pattern: "foo"},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyRedis(tt.cmd, tt.rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifyRedis() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifyRedis() unexpected error: %v", err)
			}
		})
	}
}

func TestExtractVerifyRulesForTarget_Redis(t *testing.T) {
	rules := []types.VerifyRule{
		{Type: "CONTAINS", Target: "query", Pattern: "SELECT"},
		{Type: "CONTAINS", Target: "command", Pattern: "GET"},
		{Type: "CONTAINS", Target: "key", Pattern: "user:"},
		{Type: "CONTAINS", Target: "value", Pattern: "active"},
		{Type: "CONTAINS", Target: "body", Pattern: "test"},
	}

	redisRules := ExtractVerifyRulesForTarget(rules, "redis")
	if len(redisRules) != 3 {
		t.Errorf("Expected 3 Redis rules (command, key, value), got %d", len(redisRules))
	}
}
