package verify

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/livecodelife/linespec/pkg/types"
)

// HTTPRequest holds HTTP request data for verification
type HTTPRequest struct {
	Method  string
	URL     string
	Path    string
	Headers map[string]string
	Body    string
}

// KafkaMessage holds Kafka message data for verification
type KafkaMessage struct {
	Key     string
	Value   string
	Headers map[string]string
}

// VerificationError represents a failed verification
type VerificationError struct {
	Target  string
	Rule    types.VerifyRule
	Actual  string
	Message string
}

func (e *VerificationError) Error() string {
	switch e.Rule.Type {
	case "CONTAINS":
		return fmt.Sprintf("VERIFY failed: expected %s to CONTAINS '%s', but got: %s",
			e.Target, e.Rule.Pattern, truncate(e.Actual, 200))
	case "NOT_CONTAINS":
		return fmt.Sprintf("VERIFY failed: expected %s to NOT_CONTAINS '%s', but got: %s",
			e.Target, e.Rule.Pattern, truncate(e.Actual, 200))
	case "MATCHES":
		return fmt.Sprintf("VERIFY failed: expected %s to MATCHES /%s/, but got: %s",
			e.Target, e.Rule.Pattern, truncate(e.Actual, 200))
	default:
		return fmt.Sprintf("VERIFY failed: unknown rule type '%s' for %s", e.Rule.Type, e.Target)
	}
}

// truncate limits string length for error messages
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// VerifySQL checks a SQL query against a set of verification rules
func VerifySQL(query string, rules []types.VerifyRule) error {
	return VerifyTarget("query", query, rules)
}

// verifyWithExtractor applies verification rules to a channel whose target
// value is produced per-rule by extract. It owns the loop shared by the
// per-channel Verify* entry points: a failed extraction is wrapped in a
// VerificationError, otherwise the rule is checked via verifyRule.
func verifyWithExtractor(rules []types.VerifyRule, extract func(target string) (string, error)) error {
	for _, rule := range rules {
		value, err := extract(rule.Target)
		if err != nil {
			return &VerificationError{
				Target:  rule.Target,
				Rule:    rule,
				Actual:  "",
				Message: err.Error(),
			}
		}
		if err := verifyRule(rule.Target, value, rule); err != nil {
			return err
		}
	}
	return nil
}

// VerifyHTTP checks an HTTP request against a set of verification rules
func VerifyHTTP(req *HTTPRequest, rules []types.VerifyRule) error {
	return verifyWithExtractor(rules, func(target string) (string, error) {
		return extractHTTPValue(req, target)
	})
}

// extractHTTPValue extracts the target value from an HTTP request
func extractHTTPValue(req *HTTPRequest, target string) (string, error) {
	target = strings.ToLower(target)

	switch target {
	case "method":
		return req.Method, nil
	case "url":
		return req.URL, nil
	case "path":
		return req.Path, nil
	case "body":
		return req.Body, nil
	default:
		// Check for headers.NAME pattern
		if strings.HasPrefix(target, "headers.") {
			headerName := target[8:] // Remove "headers." prefix
			// Try case-insensitive header lookup
			for key, value := range req.Headers {
				if strings.EqualFold(key, headerName) {
					return value, nil
				}
			}
			// Header not found - return empty string (not an error)
			// This allows NOT_CONTAINS to work correctly for missing headers
			return "", nil
		}
		return "", fmt.Errorf("unknown HTTP target: %s", target)
	}
}

// VerifyKafka checks a Kafka message against a set of verification rules
func VerifyKafka(msg *KafkaMessage, rules []types.VerifyRule) error {
	return verifyWithExtractor(rules, func(target string) (string, error) {
		return extractKafkaValue(msg, target)
	})
}

// extractKafkaValue extracts the target value from a Kafka message
func extractKafkaValue(msg *KafkaMessage, target string) (string, error) {
	target = strings.ToLower(target)

	switch target {
	case "key":
		return msg.Key, nil
	case "value":
		return msg.Value, nil
	default:
		// Check for headers.NAME pattern
		if strings.HasPrefix(target, "headers.") {
			headerName := target[8:] // Remove "headers." prefix
			// Try case-insensitive header lookup
			for key, value := range msg.Headers {
				if strings.EqualFold(key, headerName) {
					return value, nil
				}
			}
			// Header not found - return empty string (not an error)
			// This allows NOT_CONTAINS to work correctly for missing headers
			return "", nil
		}
		return "", fmt.Errorf("unknown Kafka target: %s", target)
	}
}

// GRPCRequest holds gRPC request data for verification
type GRPCRequest struct {
	Service  string
	Method   string
	Body     string            // JSON representation of the request message
	Metadata map[string]string // gRPC metadata (like HTTP headers)
}

// VerifyGRPC checks a gRPC request against a set of verification rules
func VerifyGRPC(req *GRPCRequest, rules []types.VerifyRule) error {
	return verifyWithExtractor(rules, func(target string) (string, error) {
		return extractGRPCValue(req, target)
	})
}

// extractGRPCValue extracts the target value from a gRPC request
func extractGRPCValue(req *GRPCRequest, target string) (string, error) {
	target = strings.ToLower(target)

	switch target {
	case "service":
		return req.Service, nil
	case "method":
		return req.Method, nil
	case "request_body":
		return req.Body, nil
	default:
		if strings.HasPrefix(target, "metadata.") {
			metaName := target[9:]
			for key, value := range req.Metadata {
				if strings.EqualFold(key, metaName) {
					return value, nil
				}
			}
			return "", nil
		}
		return "", fmt.Errorf("unknown gRPC target: %s", target)
	}
}

// RedisCommand holds Redis command data for verification
type RedisCommand struct {
	Command string
	Key     string
	Value   string
	Args    []string
}

// VerifyRedis checks a Redis command against a set of verification rules
func VerifyRedis(cmd *RedisCommand, rules []types.VerifyRule) error {
	return verifyWithExtractor(rules, func(target string) (string, error) {
		return extractRedisValue(cmd, target)
	})
}

// extractRedisValue extracts the target value from a Redis command
func extractRedisValue(cmd *RedisCommand, target string) (string, error) {
	switch strings.ToLower(target) {
	case "command":
		return cmd.Command, nil
	case "key":
		return cmd.Key, nil
	case "value":
		return cmd.Value, nil
	default:
		return "", fmt.Errorf("unknown Redis target: %s", target)
	}
}

// VerifyTarget checks a target value against a set of verification rules
func VerifyTarget(targetName string, actual string, rules []types.VerifyRule) error {
	for _, rule := range rules {
		if err := verifyRule(targetName, actual, rule); err != nil {
			return err
		}
	}
	return nil
}

// verifyRule checks a single verification rule
func verifyRule(targetName string, actual string, rule types.VerifyRule) error {
	switch strings.ToUpper(rule.Type) {
	case "CONTAINS":
		if !strings.Contains(actual, rule.Pattern) {
			return &VerificationError{
				Target:  targetName,
				Rule:    rule,
				Actual:  actual,
				Message: fmt.Sprintf("expected %s to contain '%s'", targetName, rule.Pattern),
			}
		}

	case "NOT_CONTAINS":
		if strings.Contains(actual, rule.Pattern) {
			return &VerificationError{
				Target:  targetName,
				Rule:    rule,
				Actual:  actual,
				Message: fmt.Sprintf("expected %s to not contain '%s'", targetName, rule.Pattern),
			}
		}

	case "MATCHES":
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return &VerificationError{
				Target:  targetName,
				Rule:    rule,
				Actual:  actual,
				Message: fmt.Sprintf("invalid regex pattern '%s': %v", rule.Pattern, err),
			}
		}
		if !re.MatchString(actual) {
			return &VerificationError{
				Target:  targetName,
				Rule:    rule,
				Actual:  actual,
				Message: fmt.Sprintf("expected %s to match /%s/", targetName, rule.Pattern),
			}
		}

	default:
		return &VerificationError{
			Target:  targetName,
			Rule:    rule,
			Actual:  actual,
			Message: fmt.Sprintf("unknown verification rule type: %s", rule.Type),
		}
	}

	return nil
}

// ExtractVerifyRulesForTarget filters verification rules for a specific target type
// This helps proxies only process rules relevant to their protocol
func ExtractVerifyRulesForTarget(rules []types.VerifyRule, targetType string) []types.VerifyRule {
	var filtered []types.VerifyRule
	for _, rule := range rules {
		target := strings.ToLower(rule.Target)
		switch targetType {
		case "sql":
			if target == "query" {
				filtered = append(filtered, rule)
			}
		case "http":
			if target == "method" || target == "url" || target == "path" || target == "body" || strings.HasPrefix(target, "headers.") {
				filtered = append(filtered, rule)
			}
		case "kafka":
			if target == "key" || target == "value" || strings.HasPrefix(target, "headers.") {
				filtered = append(filtered, rule)
			}
		case "grpc":
			if target == "service" || target == "method" || target == "request_body" || strings.HasPrefix(target, "metadata.") {
				filtered = append(filtered, rule)
			}
		case "redis":
			if target == "command" || target == "key" || target == "value" {
				filtered = append(filtered, rule)
			}
		}
	}
	return filtered
}

