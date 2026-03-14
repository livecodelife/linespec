package verify

import (
	"strings"
	"testing"

	"github.com/livecodelife/linespec/pkg/types"
)

func TestVerifyKafka_Key(t *testing.T) {
	tests := []struct {
		name      string
		msg       *KafkaMessage
		rules     []types.VerifyRule
		wantError bool
	}{
		{
			name: "key contains - passes",
			msg: &KafkaMessage{
				Key:   "user-123",
				Value: `{"event":"created"}`,
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "key", Pattern: "user-"},
			},
			wantError: false,
		},
		{
			name: "key contains - fails",
			msg: &KafkaMessage{
				Key:   "order-123",
				Value: `{"event":"created"}`,
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "key", Pattern: "user-"},
			},
			wantError: true,
		},
		{
			name: "key not_contains - passes",
			msg: &KafkaMessage{
				Key:   "user-123",
				Value: `{"event":"created"}`,
			},
			rules: []types.VerifyRule{
				{Type: "NOT_CONTAINS", Target: "key", Pattern: "admin"},
			},
			wantError: false,
		},
		{
			name: "key matches - passes",
			msg: &KafkaMessage{
				Key:   "user-12345",
				Value: `{"event":"created"}`,
			},
			rules: []types.VerifyRule{
				{Type: "MATCHES", Target: "key", Pattern: `^user-\d+$`},
			},
			wantError: false,
		},
		{
			name: "key matches - fails",
			msg: &KafkaMessage{
				Key:   "order-abc",
				Value: `{"event":"created"}`,
			},
			rules: []types.VerifyRule{
				{Type: "MATCHES", Target: "key", Pattern: `^user-\d+$`},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyKafka(tt.msg, tt.rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifyKafka() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifyKafka() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifyKafka_Value(t *testing.T) {
	tests := []struct {
		name      string
		msg       *KafkaMessage
		rules     []types.VerifyRule
		wantError bool
	}{
		{
			name: "value contains - passes",
			msg: &KafkaMessage{
				Key:   "user-123",
				Value: `{"event":"user_created","user_id":123}`,
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "value", Pattern: "user_created"},
			},
			wantError: false,
		},
		{
			name: "value contains - fails",
			msg: &KafkaMessage{
				Key:   "user-123",
				Value: `{"event":"user_deleted","user_id":123}`,
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "value", Pattern: "user_created"},
			},
			wantError: true,
		},
		{
			name: "value matches json - passes",
			msg: &KafkaMessage{
				Key:   "user-123",
				Value: `{"event":"user_created","user_id":123}`,
			},
			rules: []types.VerifyRule{
				{Type: "MATCHES", Target: "value", Pattern: `"user_id":\s*\d+`},
			},
			wantError: false,
		},
		{
			name: "value not_contains sensitive data - passes",
			msg: &KafkaMessage{
				Key:   "user-123",
				Value: `{"event":"user_created","user_id":123}`,
			},
			rules: []types.VerifyRule{
				{Type: "NOT_CONTAINS", Target: "value", Pattern: "password"},
			},
			wantError: false,
		},
		{
			name: "value not_contains sensitive data - fails",
			msg: &KafkaMessage{
				Key:   "user-123",
				Value: `{"event":"user_created","password":"secret123"}`,
			},
			rules: []types.VerifyRule{
				{Type: "NOT_CONTAINS", Target: "value", Pattern: "password"},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyKafka(tt.msg, tt.rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifyKafka() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifyKafka() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifyKafka_Headers(t *testing.T) {
	tests := []struct {
		name      string
		msg       *KafkaMessage
		rules     []types.VerifyRule
		wantError bool
	}{
		{
			name: "header contains - passes",
			msg: &KafkaMessage{
				Key:   "user-123",
				Value: `{"event":"created"}`,
				Headers: map[string]string{
					"X-Event-Type": "user_created",
					"X-Source":     "api",
				},
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "headers.X-Event-Type", Pattern: "user_created"},
			},
			wantError: false,
		},
		{
			name: "header contains - fails",
			msg: &KafkaMessage{
				Key:   "user-123",
				Value: `{"event":"created"}`,
				Headers: map[string]string{
					"X-Event-Type": "user_deleted",
				},
			},
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "headers.X-Event-Type", Pattern: "user_created"},
			},
			wantError: true,
		},
		{
			name: "header matches - passes",
			msg: &KafkaMessage{
				Key:   "user-123",
				Value: `{"event":"created"}`,
				Headers: map[string]string{
					"X-Correlation-Id": "abc123-def456",
				},
			},
			rules: []types.VerifyRule{
				{Type: "MATCHES", Target: "headers.X-Correlation-Id", Pattern: `^[a-f0-9]+-[a-f0-9]+$`},
			},
			wantError: false,
		},
		{
			name: "header not_contains - passes",
			msg: &KafkaMessage{
				Key:   "user-123",
				Value: `{"event":"created"}`,
				Headers: map[string]string{
					"X-Source": "api",
				},
			},
			rules: []types.VerifyRule{
				{Type: "NOT_CONTAINS", Target: "headers.X-Source", Pattern: "admin"},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyKafka(tt.msg, tt.rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifyKafka() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifyKafka() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifyKafka_MultipleRules(t *testing.T) {
	msg := &KafkaMessage{
		Key:   "user-123",
		Value: `{"event":"user_created","user_id":123}`,
		Headers: map[string]string{
			"X-Event-Type": "user_created",
			"X-Source":     "api",
		},
	}

	tests := []struct {
		name      string
		rules     []types.VerifyRule
		wantError bool
	}{
		{
			name: "all rules pass",
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "key", Pattern: "user-"},
				{Type: "CONTAINS", Target: "value", Pattern: "user_created"},
				{Type: "CONTAINS", Target: "headers.X-Source", Pattern: "api"},
			},
			wantError: false,
		},
		{
			name: "one rule fails",
			rules: []types.VerifyRule{
				{Type: "CONTAINS", Target: "key", Pattern: "user-"},
				{Type: "CONTAINS", Target: "value", Pattern: "user_deleted"},
				{Type: "CONTAINS", Target: "headers.X-Source", Pattern: "api"},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyKafka(msg, tt.rules)
			if tt.wantError && err == nil {
				t.Errorf("VerifyKafka() expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifyKafka() unexpected error: %v", err)
			}
		})
	}
}

func TestVerifyKafka_CaseInsensitiveHeaders(t *testing.T) {
	msg := &KafkaMessage{
		Key:   "user-123",
		Value: `{"event":"created"}`,
		Headers: map[string]string{
			"x-event-type": "user_created", // lowercase
		},
	}

	rules := []types.VerifyRule{
		{Type: "CONTAINS", Target: "headers.X-Event-Type", Pattern: "user_created"},
	}

	err := VerifyKafka(msg, rules)
	if err != nil {
		t.Errorf("VerifyKafka() should be case-insensitive for headers, got error: %v", err)
	}
}

func TestVerifyKafka_MissingHeader(t *testing.T) {
	msg := &KafkaMessage{
		Key:   "user-123",
		Value: `{"event":"created"}`,
		Headers: map[string]string{
			"X-Source": "api",
		},
	}

	// CONTAINS on missing header should fail (empty string doesn't contain pattern)
	rules := []types.VerifyRule{
		{Type: "CONTAINS", Target: "headers.X-Missing", Pattern: "something"},
	}

	err := VerifyKafka(msg, rules)
	if err == nil {
		t.Fatal("Expected error for CONTAINS on missing header")
	}

	// NOT_CONTAINS on missing header should pass (empty string doesn't contain pattern)
	rulesNot := []types.VerifyRule{
		{Type: "NOT_CONTAINS", Target: "headers.X-Missing", Pattern: "something"},
	}

	errNot := VerifyKafka(msg, rulesNot)
	if errNot != nil {
		t.Errorf("NOT_CONTAINS on missing header should pass, got error: %v", errNot)
	}
}

func TestVerifyKafka_ErrorMessages(t *testing.T) {
	msg := &KafkaMessage{
		Key:   "order-123",
		Value: `{"event":"order_created"}`,
	}

	rules := []types.VerifyRule{
		{Type: "CONTAINS", Target: "key", Pattern: "user-"},
	}

	err := VerifyKafka(msg, rules)
	if err == nil {
		t.Fatal("Expected error")
	}

	if !strings.Contains(err.Error(), "CONTAINS") {
		t.Errorf("Error message should contain 'CONTAINS', got: %v", err.Error())
	}
	if !strings.Contains(err.Error(), "user-") {
		t.Errorf("Error message should contain expected pattern, got: %v", err.Error())
	}
	if !strings.Contains(err.Error(), "order-123") {
		t.Errorf("Error message should contain actual value, got: %v", err.Error())
	}
}

func TestExtractVerifyRulesForTarget(t *testing.T) {
	rules := []types.VerifyRule{
		{Type: "CONTAINS", Target: "query", Pattern: "SELECT"},
		{Type: "CONTAINS", Target: "headers.Authorization", Pattern: "Bearer"},
		{Type: "CONTAINS", Target: "body", Pattern: "test"},
		{Type: "CONTAINS", Target: "key", Pattern: "user"},
		{Type: "CONTAINS", Target: "value", Pattern: "event"},
	}

	sqlRules := ExtractVerifyRulesForTarget(rules, "sql")
	if len(sqlRules) != 1 {
		t.Errorf("Expected 1 SQL rule, got %d", len(sqlRules))
	}

	httpRules := ExtractVerifyRulesForTarget(rules, "http")
	if len(httpRules) != 2 {
		t.Errorf("Expected 2 HTTP rules, got %d", len(httpRules))
	}

	kafkaRules := ExtractVerifyRulesForTarget(rules, "kafka")
	// Note: headers are valid for both HTTP and Kafka, so we get 3 rules
	// (key, value, headers.Authorization)
	if len(kafkaRules) != 3 {
		t.Errorf("Expected 3 Kafka rules (key, value, and headers), got %d", len(kafkaRules))
	}
}
